// Package ratelimit 提供简单的 in-memory token-bucket 速率限制（PRD §15.3 安全）。
//
// 设计：
//   - 按 key（IP / user / endpoint）分桶，互不影响
//   - Token bucket：每桶 capacity 个 token，每 interval refill 1 个
//   - 内存式（无 Redis 依赖）— 单进程够用；多节点需要换 shared backend
//   - GC 兜底：清理 5 分钟没用过的桶防止内存泄漏
package ratelimit

import (
	"sync"
	"time"
)

// Bucket 内部 token bucket 状态。
type Bucket struct {
	mu        sync.Mutex
	tokens    float64
	lastRefil time.Time
	capacity  float64
	refillPer float64 // 每秒补充 token 数
}

// take 尝试扣 1 token。
//
// 返回 (allowed, retryAfter)：
//   - allowed=true 表示成功
//   - allowed=false 表示被限；retryAfter 是等多久后下次有机会
func (b *Bucket) take(now time.Time) (bool, time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// 计算 refil（上次到现在补充的 token）
	elapsed := now.Sub(b.lastRefil).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * b.refillPer
		if b.tokens > b.capacity {
			b.tokens = b.capacity
		}
		b.lastRefil = now
	}

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	// 算出还差多少秒到 1 token
	missing := 1 - b.tokens
	wait := time.Duration(missing/b.refillPer*float64(time.Second)) + time.Millisecond
	return false, wait
}

// Limiter 多个 key 共享的 token-bucket 池。
type Limiter struct {
	mu        sync.Mutex
	buckets   map[string]*Bucket
	capacity  float64
	refill    float64 // tokens/秒
	idle      time.Duration
	gcEvery   time.Duration
	lastGC    time.Time
}

// NewLimiter 构造。
//
//   - capacity：每桶上限（突发容量）
//   - refill：每秒补充的 token（rate）
//   - idle：超过这个时间没用的桶会被 GC
//   - gcEvery：GC 周期（默认 5 分钟；test 可传短值）
//
// 常见配置：
//   - login 端点：capacity=5, refill=0.1（每 10 秒 1 个，最多突发 5）
//   - 通用 API 写：capacity=30, refill=5（每秒 5 个，突发 30）
//   - 通用 API 读：capacity=200, refill=50（每秒 50 个，突发 200）
func NewLimiter(capacity, refill float64, idle time.Duration) *Limiter {
	return NewLimiterWithGC(capacity, refill, idle, 5*time.Minute)
}

// NewLimiterWithGC 同 NewLimiter 但显式指定 GC 周期（测试用）。
func NewLimiterWithGC(capacity, refill float64, idle, gcEvery time.Duration) *Limiter {
	return &Limiter{
		buckets:  make(map[string]*Bucket),
		capacity: capacity,
		refill:   refill,
		idle:     idle,
		gcEvery:  gcEvery,
		lastGC:   time.Now(),
	}
}

// Take 拿 1 token — 命中 allow 返 true，超限返 false。
func (l *Limiter) Take(key string) (bool, time.Duration) {
	now := time.Now()
	l.mu.Lock()
	b, ok := l.buckets[key]
	if !ok {
		b = &Bucket{
			tokens:    l.capacity, // 新桶满（让用户先有突发容量）
			lastRefil: now,
			capacity:  l.capacity,
			refillPer: l.refill,
		}
		l.buckets[key] = b
	}
	// 顺便 GC
	if now.Sub(l.lastGC) > l.gcEvery {
		l.gcLocked(now)
		l.lastGC = now
	}
	l.mu.Unlock()
	return b.take(now)
}

// gcLocked 清理长期不用的桶（调用者持有 l.mu）。
func (l *Limiter) gcLocked(now time.Time) {
	for k, b := range l.buckets {
		b.mu.Lock()
		if now.Sub(b.lastRefil) > l.idle {
			delete(l.buckets, k)
		}
		b.mu.Unlock()
	}
}

// Stats 返回当前桶数（用于 metrics）。
func (l *Limiter) Stats() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
