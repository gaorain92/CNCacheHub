// Package dnsserver 提供 CNCacheHub 内置的 mini DNS server（PRD §9.3）。
//
// 功能：LANCache 风格——只对白名单 Steam 域名返回 A 记录（指向本机网关），
// 其他域名转发到上游 DNS（1.1.1.1 / 8.8.8.8 / 用户自配）。
//
// 设计取舍：
//   - 监听 5353/UDP（避免 53 需要 root / CAP_NET_BIND_SERVICE），用户路由器再转发
//   - 白名单 + 通配符（*.steamcontent.com 匹配 cdn.steamcontent.com）
//   - 同步统计（queryCount / hitCount / missCount / lastQueryAt）写入 system_settings
package dnsserver

import "time"

// Config 是 DNS server 运行时配置（来自 DB / API）。
type Config struct {
	Enabled     bool      `json:"enabled"`
	ListenAddr  string    `json:"listenAddr"`  // 形如 "0.0.0.0:5353"
	Upstream    string    `json:"upstream"`    // 形如 "1.1.1.1:53"
	AnswerIP    string    `json:"answerIp"`    // 命中白名单时返回的 A 记录（CNCacheHub 自身 IP）
	DomainRules []string  `json:"domainRules"` // 白名单域名，支持 *.example.com
	UpdatedAt   time.Time `json:"updatedAt"`
}

// Stats 是查询统计。
type Stats struct {
	TotalQueries  int64     `json:"totalQueries"`
	HitQueries    int64     `json:"hitQueries"`  // 命中白名单
	MissQueries   int64     `json:"missQueries"` // 转发上游
	BlockedQueries int64    `json:"blockedQueries"` // 上游失败 / 黑洞
	LastQueryAt   int64     `json:"lastQueryAt"` // unix 秒；0 = 从未
	LastError     string    `json:"lastError"`
}

// DefaultConfig 是首次启动时落库的默认值。
func DefaultConfig() Config {
	return Config{
		Enabled:    false, // 默认关，让用户主动开
		ListenAddr: "0.0.0.0:5353",
		Upstream:   "1.1.1.1:53",
		AnswerIP:   "127.0.0.1", // 占位；用户需改成 CNCacheHub 自身可达 IP
		DomainRules: []string{
			// LANCache 官方默认列表
			"*.steamcontent.com",
			"*.steampipe.steamcontent.com",
			"*.steamserver.net",
			"*.steamstatic.com",
			"content*.steampowered.com",
			"client-download.steampowered.com",
			"*.cs.steampowered.com",
			"*.cm.steampowered.com",
		},
	}
}
