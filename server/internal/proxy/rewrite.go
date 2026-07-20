// Package proxy 实现 Docker Registry v2 协议的反代 / 缓存代理。
//
// Phase 1 MVP scope：
//   - GET /v2/                         → 200 {}
//   - GET/HEAD /v2/<name>/manifests/<ref>  → 转发（manifest 不落盘，只走元数据）
//   - GET/HEAD /v2/<name>/blobs/<digest>  → 命中返回本地 / 上游流式下载 + 落盘
//
// Out of scope (Phase 1.1+)：
//   - Www-Authenticate token dance（公开镜像匿名够用）
//   - 推送 (push)
//   - 跨 registry 路由
//
// 安全约束（PRD §6）：
//   - 不保存客户端 token / 不解析 Authorization
//   - 小容量 VPS 旁路：单对象超限 或 磁盘不足 → 仍转发，不缓存
//   - 日志脱敏：access log 不记 Authorization / Cookie
package proxy

import "strings"

// libraryRewrite 把单段 name 补全成 library/name（Docker Hub 官方镜像规则）。
//
// 规则：
//   - 空字符串不动
//   - 已含 "/" 的 name 不动（已显式 namespace）
//   - 其它（如 "nginx"）补成 "library/nginx"
//
// 客户端拉 "nginx:latest" → /v2/nginx/manifests/latest → libraryRewrite → library/nginx
func libraryRewrite(name string) string {
	if name == "" {
		return name
	}
	if strings.ContainsAny(name, "/") {
		return name
	}
	return "library/" + name
}
