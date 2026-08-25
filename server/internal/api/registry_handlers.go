// Package api: registries 端点（PRD §9.2.2 + §9.7.3）。
//
// §9.7.3 上游凭据管理：
//   - PATCH /api/registries/{name} body 可传 username / password / token
//   - 写入 SQLite 前用 master key AES-256-GCM 加密
//   - GET /api/registries 永远不回显 password / token，只返 hasPassword / hasToken bool
//   - 日志 / bundle / 错误信息里也不会泄密
package api

import (
	"net/http"

	"github.com/cncachehub/server/internal/storage"
	"github.com/go-chi/chi/v5"
)

// registriesListHandler GET /api/registries
//
// 返回所有 registry（enabled + disabled）+ 凭据状态标志。
// password / token 明文不返回（omitempty + json:"-"）。
func registriesListHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.ListRegistries == nil {
			writeError(w, http.StatusServiceUnavailable, "registries_unavailable", "registries not configured")
			return
		}
		regs, err := opts.ListRegistries(r.Context())
		if err != nil {
			writeInternalErr(w, r, "registries_query_failed", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items": regs,
			"total": len(regs),
		})
	}
}

// registryPatchHandler PATCH /api/registries/{name}
//
// body 字段（全部可选）：
//   - enabled *bool             — 启停
//   - username *string          — 上游登录名（明文）
//   - password *string          — 上游密码（明文，handler 加密后存）
//   - token *string             — 上游 bearer token（明文，handler 加密后存）
//   - clearPassword bool        — 显式清空密码
//   - clearToken bool           — 显式清空 token
//
// 任意字段组合都行。例：只改 enabled 就只传 {"enabled": true}。
// 例：设置凭据：{"username": "alice", "password": "secret", "token": "ghp_xxx"}
// 例：清空：{"clearPassword": true, "clearToken": true}
func registryPatchHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.SetRegistryEnabled == nil {
			writeError(w, http.StatusServiceUnavailable, "registries_unavailable", "registries not configured")
			return
		}
		u, ok := userFromContext(r.Context())
		if !ok || !u.IsAdmin {
			writeError(w, http.StatusForbidden, "admin_required", "only admin can update registry")
			return
		}
		name := chi.URLParam(r, "name")
		if name == "" {
			writeError(w, http.StatusBadRequest, "invalid_name", "name is required")
			return
		}
		var patch RegistryPatch
		if !decodeJSONBody(w, r, &patch) {
			return
		}
		// 至少 1 个字段
		if patch.Enabled == nil && patch.Username == nil && patch.Password == nil && patch.Token == nil && !patch.ClearPassword && !patch.ClearToken {
			writeError(w, http.StatusBadRequest, "missing_field", "at least one of enabled/username/password/token/clearPassword/clearToken is required")
			return
		}

		// 1. enabled（如果传了）
		if patch.Enabled != nil {
			if err := opts.SetRegistryEnabled(r.Context(), name, *patch.Enabled); err != nil {
				writeInternalErr(w, r, "registry_update_failed", err)
				return
			}
		}

		// 2. credentials（如果传了任何 credential 字段）
		credPatch := storage.RegistryCredentialsPatch{}
		hasCredChange := false
		if patch.Username != nil {
			un := *patch.Username
			credPatch.Username = &un
			hasCredChange = true
		}
		if patch.Password != nil {
			// 加密
			if opts.CredentialCipher == nil {
				writeError(w, http.StatusServiceUnavailable, "credential_cipher_unavailable", "master key not initialized")
				return
			}
			ct, err := opts.CredentialCipher.Encrypt([]byte(*patch.Password))
			if err != nil {
				writeInternalErr(w, r, "encrypt_failed", err)
				return
			}
			credPatch.Password = &ct
			hasCredChange = true
		}
		if patch.Token != nil {
			if opts.CredentialCipher == nil {
				writeError(w, http.StatusServiceUnavailable, "credential_cipher_unavailable", "master key not initialized")
				return
			}
			ct, err := opts.CredentialCipher.Encrypt([]byte(*patch.Token))
			if err != nil {
				writeInternalErr(w, r, "encrypt_failed", err)
				return
			}
			credPatch.Token = &ct
			hasCredChange = true
		}
		if patch.ClearPassword {
			credPatch.ClearPassword = true
			hasCredChange = true
		}
		if patch.ClearToken {
			credPatch.ClearToken = true
			hasCredChange = true
		}
		if hasCredChange {
			if opts.SetRegistryCredentials == nil {
				writeError(w, http.StatusServiceUnavailable, "registry_credentials_unavailable", "credential storage not configured")
				return
			}
			if err := opts.SetRegistryCredentials(r.Context(), name, credPatch); err != nil {
				writeInternalErr(w, r, "credential_update_failed", err)
				return
			}
		}

		// 3. 写 audit + 返最新值
		if uid := u.ID; uid > 0 && opts.AuthDB != nil {
			_ = opts.AuthDB.WriteAudit(r.Context(), storage.AuditLog{
				UserID:    uid,
				Action:    "update_registry",
				Status:    "ok",
				Details:   authAuditFromCredPatch(patch, name),
				CreatedAt: nowUnix(),
			})
		}

		// 重新读 + 返
		regs, _ := opts.ListRegistries(r.Context())
		var updated *storage.Registry
		for i := range regs {
			if regs[i].Name == name {
				updated = &regs[i]
				break
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"name":        name,
			"enabled":     updated.Enabled,
			"username":    updated.Username,
			"hasPassword": updated.HasPassword,
			"hasToken":    updated.HasToken,
		})
	}
}

// authAuditFromCredPatch 生成 audit 详情（脱敏：只记字段名 + 有无值）。
func authAuditFromCredPatch(p RegistryPatch, name string) string {
	parts := []string{"registry=" + name}
	if p.Enabled != nil {
		if *p.Enabled {
			parts = append(parts, "enabled=true")
		} else {
			parts = append(parts, "enabled=false")
		}
	}
	if p.Username != nil {
		parts = append(parts, "username=set")
	}
	if p.Password != nil {
		parts = append(parts, "password=set(len="+itoaLen(*p.Password)+")")
	}
	if p.Token != nil {
		parts = append(parts, "token=set(len="+itoaLen(*p.Token)+")")
	}
	if p.ClearPassword {
		parts = append(parts, "clear_password=true")
	}
	if p.ClearToken {
		parts = append(parts, "clear_token=true")
	}
	out := ""
	for i, q := range parts {
		if i > 0 {
			out += ","
		}
		out += q
	}
	return out
}
