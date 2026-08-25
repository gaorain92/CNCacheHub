package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/cncachehub/server/internal/storage"
	"github.com/go-chi/chi/v5"
)

// ResourceRuleResponse 是 list/get 返回结构。
type ResourceRuleResponse struct {
	Items []storage.ResourceRule `json:"items"`
	Total int                    `json:"total"`
}

type resourceRuleCreateRequest struct {
	Name              string `json:"name"`
	Kind              string `json:"kind"`
	UpstreamURL       string `json:"upstreamUrl"`
	DefaultTTLSeconds int    `json:"defaultTtlSeconds"`
	Description       string `json:"description"`
	Enabled           *bool  `json:"enabled,omitempty"`
}

type resourceRulePatchRequest struct {
	UpstreamURL       *string `json:"upstreamUrl,omitempty"`
	DefaultTTLSeconds *int    `json:"defaultTtlSeconds,omitempty"`
	Enabled           *bool   `json:"enabled,omitempty"`
	Description       *string `json:"description,omitempty"`
}

func resourceRuleListHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := opts.ListResourceRules(r.Context())
		if err != nil {
			writeInternalErr(w, r, "resource_rules_list_failed", err)
			return
		}
		writeJSON(w, http.StatusOK, ResourceRuleResponse{Items: items, Total: len(items)})
	}
}

func resourceRuleCreateHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(opts, w, r) {
			return
		}
		var req resourceRuleCreateRequest
		if !decodeJSONBody(w, r, &req) {
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			writeError(w, http.StatusBadRequest, "invalid_name", "name required")
			return
		}
		if strings.TrimSpace(req.UpstreamURL) == "" {
			writeError(w, http.StatusBadRequest, "invalid_upstream", "upstreamUrl required")
			return
		}
		if !strings.HasPrefix(req.UpstreamURL, "http://") && !strings.HasPrefix(req.UpstreamURL, "https://") {
			writeError(w, http.StatusBadRequest, "invalid_upstream_scheme", "upstreamUrl must start with http:// or https://")
			return
		}
		if !validResourceKind(req.Kind) {
			writeError(w, http.StatusBadRequest, "invalid_kind", "kind must be 'github' | 'playwright' | 'huggingface' | 'terraform' | 'custom'")
			return
		}
		enabled := true
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		rule, err := opts.CreateResourceRule(r.Context(), storage.ResourceRule{
			Name: req.Name, Kind: req.Kind, UpstreamURL: req.UpstreamURL,
			DefaultTTLSeconds: req.DefaultTTLSeconds, Description: req.Description, Enabled: enabled,
		})
		if err != nil {
			writeInternalErr(w, r, "resource_rule_create_failed", err)
			return
		}
		writeJSON(w, http.StatusCreated, rule)
	}
}

func resourceRulePatchHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(opts, w, r) {
			return
		}
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_id", "id must be positive integer")
			return
		}
		var req resourceRulePatchRequest
		if !decodeJSONBody(w, r, &req) {
			return
		}
		patch := storage.ResourceRulePatch{
			UpstreamURL: req.UpstreamURL, DefaultTTLSeconds: req.DefaultTTLSeconds,
			Enabled: req.Enabled, Description: req.Description,
		}
		rule, err := opts.UpdateResourceRule(r.Context(), id, patch)
		if err != nil {
			writeInternalErr(w, r, "resource_rule_update_failed", err)
			return
		}
		writeJSON(w, http.StatusOK, rule)
	}
}

func resourceRuleDeleteHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(opts, w, r) {
			return
		}
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_id", "id must be positive integer")
			return
		}
		if err := opts.DeleteResourceRule(r.Context(), id); err != nil {
			writeInternalErr(w, r, "resource_rule_delete_failed", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func resourceCacheListHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ruleID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || ruleID <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_id", "id must be positive integer")
			return
		}
		limit := 100
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, perr := strconv.Atoi(l); perr == nil && n > 0 {
				limit = n
			}
		}
		items, err := opts.ListResourceCache(r.Context(), ruleID, limit)
		if err != nil {
			writeInternalErr(w, r, "resource_cache_list_failed", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
	}
}

func resourceCacheDeleteHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(opts, w, r) {
			return
		}
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_id", "id must be positive integer")
			return
		}
		if err := opts.DeleteResourceCacheEntry(r.Context(), id); err != nil {
			writeInternalErr(w, r, "resource_cache_delete_failed", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func validResourceKind(k string) bool {
	switch k {
	case "github", "playwright", "huggingface", "terraform", "custom":
		return true
	}
	return false
}

// ResourceTemplate 是 /api/resources/templates 返回的预置模板。
type ResourceTemplate struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	UpstreamURL string `json:"upstreamUrl"`
	PathPattern string `json:"pathPattern"`
	Description string `json:"description"`
	Sample      string `json:"sample"` // 示例接入命令
}

// resourceTemplatesHandler GET /api/resources/templates — 返回内置推荐模板
// 不需要 admin（普通用户能看到推荐列表）
func resourceTemplatesHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		templates := []ResourceTemplate{
			{
				Name: "github-raw", Kind: "github", UpstreamURL: "https://raw.githubusercontent.com",
				PathPattern: "**",
				Description: "GitHub raw 文件（任意 repo/branch/path）",
				Sample:      "curl http://CNCacheHub/r/github-raw/golang/go/master/CONTRIBUTING.md",
			},
			{
				Name: "ghcr-blob", Kind: "github", UpstreamURL: "https://ghcr.io",
				PathPattern: "v2/**",
				Description: "GitHub Container Registry 的 OCI blob（手动 v2 API）",
				Sample:      "curl http://CNCacheHub/r/ghcr-blob/v2/owner/image/manifests/latest",
			},
			{
				Name: "huggingface", Kind: "huggingface", UpstreamURL: "https://huggingface.co",
				PathPattern: "**",
				Description: "Hugging Face 模型 / datasets / tokenizer",
				Sample:      "curl http://CNCacheHub/r/huggingface/Qwen/Qwen2.5-7B-Instruct/resolve/main/config.json",
			},
			{
				Name: "playwright-cdn", Kind: "playwright", UpstreamURL: "https://playwright.azureedge.net",
				PathPattern: "**",
				Description: "Playwright 浏览器二进制（设置 PLAYWRIGHT_DOWNLOAD_HOST）",
				Sample:      "export PLAYWRIGHT_DOWNLOAD_HOST=http://CNCacheHub/r/playwright-cdn",
			},
			{
				Name: "terraform-releases", Kind: "terraform", UpstreamURL: "https://releases.hashicorp.com",
				PathPattern: "**",
				Description: "Terraform / Vault 等 HashiCorp 工具 release",
				Sample:      "curl http://CNCacheHub/r/terraform-releases/terraform/1.7.5/terraform_1.7.5_linux_amd64.zip",
			},
			{
				Name: "npm-public", Kind: "custom", UpstreamURL: "https://registry.npmjs.org",
				PathPattern: "**",
				Description: "NPM public registry（tarball 缓存，tarball URL 含 ?token 时不缓存）",
				Sample:      "curl http://CNCacheHub/r/npm-public/-/lodash/-/lodash-4.17.21.tgz",
			},
			{
				Name: "pypi-files", Kind: "custom", UpstreamURL: "https://files.pythonhosted.org",
				PathPattern: "**",
				Description: "PyPI 包文件（sdist + wheel）",
				Sample:      "curl http://CNCacheHub/r/pypi-files/packages/.../requests-2.31.0.tar.gz",
			},
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": templates, "total": len(templates)})
	}
}
