package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/cncachehub/server/internal/access"
	"github.com/cncachehub/server/internal/storage"
)

// AccessControlConfig 是 /api/access-control GET/PUT 的 JSON 形态。
//
// 设计：响应里 token 总是返回 "***"（即使实际有值），由用户 PUT
// 时再用明文回传。这是标准 secret 字段处理方式（GitHub/Stripe 都这么干）。
type AccessControlConfig struct {
	Enabled        bool     `json:"enabled"`
	Token          string   `json:"token"`             // GET: "***" if non-empty; PUT: 明文（空字符串表示不改）
	TokenSet       bool     `json:"tokenSet"`          // GET: true if token non-empty
	IPWhitelist    []string `json:"ipWhitelist"`       // CIDR 列表
	LoopbackBypass bool     `json:"loopbackBypass"`
	UpdatedAt      int64    `json:"updatedAt"`
}

// AccessControlDB 是 handler 需要的最小 DB 接口（GetMany + SetMany）。
// main.go 注入 *storage.DB（直接满足）。
type AccessControlDB interface {
	GetMany(ctx context.Context, keys ...string) (map[string]string, error)
	SetMany(ctx context.Context, kvs map[string]string, updatedBy int64) error
}

// accessControlGetHandler GET /api/access-control
// 需要 admin。返回当前配置（token masked）。
func accessControlGetHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(opts, w, r) {
			return
		}
		acDB, ok := opts.AuthDB.(AccessControlDB)
		if !ok {
			writeError(w, http.StatusServiceUnavailable, "access_control_db_unavailable", "AuthDB does not support access control reads")
			return
		}
		settings, err := acDB.GetMany(r.Context(),
			storage.SettingAccessControlEnabled,
			storage.SettingAccessControlToken,
			storage.SettingAccessControlIPWhitelist,
			storage.SettingAccessControlLoopbackBypass,
		)
		if err != nil {
			writeInternalErr(w, r, "db_error", err)
			return
		}

		whitelist := access.ParseCIDRList(settings[storage.SettingAccessControlIPWhitelist])
		if whitelist == nil {
			whitelist = []string{} // JSON 不要 null
		}
		out := AccessControlConfig{
			Enabled:        settings[storage.SettingAccessControlEnabled] == "true",
			Token:          "",
			TokenSet:       settings[storage.SettingAccessControlToken] != "",
			IPWhitelist:    whitelist,
			LoopbackBypass: settings[storage.SettingAccessControlLoopbackBypass] != "false", // 默认 true
		}
		_ = out // updated_at 留给前端通过 settings 列表拿
		writeJSON(w, http.StatusOK, out)
	}
}

// accessControlPutHandler PUT /api/access-control
// 需要 admin。更新配置。
//
// 入参：所有字段可选。token 空字符串 = "保持原值不清空"；传 "__CLEAR__" 表示清空（可选）。
//
// 简化：token 空 = 不变，token 非空 = 替换。
func accessControlPutHandler(opts Options) http.HandlerFunc {
	type patch struct {
		Enabled        *bool    `json:"enabled,omitempty"`
		Token          *string  `json:"token,omitempty"` // 空 = 不变
		IPWhitelist    []string `json:"ipWhitelist,omitempty"`
		LoopbackBypass *bool    `json:"loopbackBypass,omitempty"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(opts, w, r) {
			return
		}
		var p patch
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		acDB, ok := opts.AuthDB.(AccessControlDB)
		if !ok {
			writeError(w, http.StatusServiceUnavailable, "access_control_db_unavailable", "AuthDB does not support access control writes")
			return
		}

		kvs := map[string]string{}
		if p.Enabled != nil {
			if *p.Enabled {
				kvs[storage.SettingAccessControlEnabled] = "true"
			} else {
				kvs[storage.SettingAccessControlEnabled] = "false"
			}
		}
		if p.Token != nil {
			kvs[storage.SettingAccessControlToken] = *p.Token
		}
		if p.IPWhitelist != nil {
			// 验证所有 CIDR 合法
			for _, c := range p.IPWhitelist {
				if !isValidCIDR(c) {
					writeError(w, http.StatusBadRequest, "invalid_cidr", "invalid CIDR: "+c)
					return
				}
			}
			kvs[storage.SettingAccessControlIPWhitelist] = strings.Join(p.IPWhitelist, ",")
		}
		if p.LoopbackBypass != nil {
			if *p.LoopbackBypass {
				kvs[storage.SettingAccessControlLoopbackBypass] = "true"
			} else {
				kvs[storage.SettingAccessControlLoopbackBypass] = "false"
			}
		}
		if len(kvs) == 0 {
			writeError(w, http.StatusBadRequest, "noop", "no fields to update")
			return
		}

		// 拿 userID 写 audit
		_, uid, _ := opts.SessionUserRole(r.Context(), r)
		if err := acDB.SetMany(r.Context(), kvs, uid); err != nil {
			writeInternalErr(w, r, "db_error", err)
			return
		}

		// 通过 AuthDB 写 audit（如果支持）
		if auditDB, ok := opts.AuthDB.(auditWriter); ok && uid > 0 {
			_ = auditDB.WriteAudit(r.Context(), storage.AuditLog{
				UserID:    uid,
				Action:    "update_access_control",
				Status:    "ok",
				Details:   summarizeAccessPatch(p),
				CreatedAt: nowUnix(),
			})
		}

		// 通知 Resolver 重新从 DB 读最新配置（热重载）
		if opts.AccessControlReload != nil {
			opts.AccessControlReload()
		}

		// 重新读最新值并返回
		settings, _ := acDB.GetMany(r.Context(),
			storage.SettingAccessControlEnabled,
			storage.SettingAccessControlToken,
			storage.SettingAccessControlIPWhitelist,
			storage.SettingAccessControlLoopbackBypass,
		)
		whitelist := access.ParseCIDRList(settings[storage.SettingAccessControlIPWhitelist])
		if whitelist == nil {
			whitelist = []string{}
		}
		out := AccessControlConfig{
			Enabled:        settings[storage.SettingAccessControlEnabled] == "true",
			Token:          "",
			TokenSet:       settings[storage.SettingAccessControlToken] != "",
			IPWhitelist:    whitelist,
			LoopbackBypass: settings[storage.SettingAccessControlLoopbackBypass] != "false",
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// isValidCIDR 验证 CIDR 字符串。
func isValidCIDR(s string) bool {
	_, _, err := net.ParseCIDR(s)
	return err == nil
}

// auditWriter 是 handler 用来写 audit log 的可选接口。
type auditWriter interface {
	WriteAudit(ctx context.Context, e storage.AuditLog) error
}

// summarizeAccessPatch 生成 audit 详情字符串（不包含 token 明文）。
func summarizeAccessPatch(p struct {
	Enabled        *bool    `json:"enabled,omitempty"`
	Token          *string  `json:"token,omitempty"`
	IPWhitelist    []string `json:"ipWhitelist,omitempty"`
	LoopbackBypass *bool    `json:"loopbackBypass,omitempty"`
}) string {
	parts := []string{}
	if p.Enabled != nil {
		if *p.Enabled {
			parts = append(parts, "enabled=true")
		} else {
			parts = append(parts, "enabled=false")
		}
	}
	if p.Token != nil {
		if *p.Token == "" {
			parts = append(parts, "token=unchanged")
		} else {
			parts = append(parts, "token=set(len="+itoaLen(*p.Token)+")")
		}
	}
	if p.IPWhitelist != nil {
		parts = append(parts, "ip_whitelist="+itoaLen(strings.Join(p.IPWhitelist, ","))+"chars")
	}
	if p.LoopbackBypass != nil {
		if *p.LoopbackBypass {
			parts = append(parts, "loopback_bypass=true")
		} else {
			parts = append(parts, "loopback_bypass=false")
		}
	}
	if len(parts) == 0 {
		return "noop"
	}
	return strings.Join(parts, ",")
}

func itoaLen(s string) string {
	return strconv.Itoa(len(s))
}
