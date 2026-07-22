// Package proxy: 多 Upstream 池（PRD §9.2.2）。
//
// UpstreamPool 把多个 Registry 上游封装为一个池子，按 client 请求的 path 前缀
// 分发到对应的 *Upstream。dockerhub（mirror_path == ""）是默认上游，未命中按
// path 推断则回退到 dockerhub。
package proxy

import (
	"fmt"
	"sync"
	"time"
)

// UpstreamPool 多上游池。
type UpstreamPool struct {
	mu sync.RWMutex
	// key 是 client path 第一个 slash 段，如 "dockerhub"/"ghcr"/"quay"/"k8s"
	upstreams map[string]*poolEntry
	// defaultUpstream 是 mirror_path == "" 那个；fallback
	defaultUpstream *poolEntry
}

type poolEntry struct {
	name    string
	baseURL string
	client  *Upstream
}

// NewUpstreamPool 构造池子。
func NewUpstreamPool(entries []UpstreamPoolEntry) (*UpstreamPool, error) {
	p := &UpstreamPool{upstreams: make(map[string]*poolEntry)}
	for _, e := range entries {
		u, err := NewUpstream(UpstreamOptions{BaseURL: e.BaseURL, Timeout: e.Timeout, UA: e.UA})
		if err != nil {
			return nil, fmt.Errorf("upstream %q: %w", e.Name, err)
		}
		pe := &poolEntry{name: e.Name, baseURL: e.BaseURL, client: u}
		p.upstreams[e.Name] = pe
		if e.Name == "dockerhub" {
			p.defaultUpstream = pe
		}
	}
	return p, nil
}

// UpstreamPoolEntry 构造项。
type UpstreamPoolEntry struct {
	Name    string        // "dockerhub" / "ghcr" / "quay" / "k8s"
	BaseURL string
	Timeout time.Duration
	UA      string
}

// resolveUpstream 按 client path 推断 upstream。
//
// 规则：
//   - 路径首段（第一个 / 之前）作为 registry key
//   - 命中 pool.upstreams → 用对应 Upstream
//   - 未命中 → fallback dockerhub
//
// 返回 (name, *Upstream)。default fallback 也返 valid up。
func (p *UpstreamPool) resolveUpstream(firstSegment string) (string, *Upstream) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if e, ok := p.upstreams[firstSegment]; ok {
		return e.name, e.client
	}
	if p.defaultUpstream != nil {
		return p.defaultUpstream.name, p.defaultUpstream.client
	}
	return "", nil
}

// hasUpstream 判断 firstSegment 是否是 pool 内注册名（不含 default fallback）。
//
// 用于 splitRegistry 区分"显式 registry 前缀"和"docker daemon 旧形式"。
func (p *UpstreamPool) hasUpstream(firstSegment string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.upstreams[firstSegment]
	return ok
}

// ListNames 返回所有 pool 内 upstream 名字（调试 / 监控用）。
func (p *UpstreamPool) ListNames() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]string, 0, len(p.upstreams))
	for k := range p.upstreams {
		out = append(out, k)
	}
	return out
}
