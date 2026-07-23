package api

import (
	"encoding/json"
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
			writeError(w, http.StatusInternalServerError, "resource_rules_list_failed", err.Error())
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
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
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
			writeError(w, http.StatusInternalServerError, "resource_rule_create_failed", err.Error())
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
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		patch := storage.ResourceRulePatch{
			UpstreamURL: req.UpstreamURL, DefaultTTLSeconds: req.DefaultTTLSeconds,
			Enabled: req.Enabled, Description: req.Description,
		}
		rule, err := opts.UpdateResourceRule(r.Context(), id, patch)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "resource_rule_update_failed", err.Error())
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
			writeError(w, http.StatusInternalServerError, "resource_rule_delete_failed", err.Error())
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
			writeError(w, http.StatusInternalServerError, "resource_cache_list_failed", err.Error())
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
			writeError(w, http.StatusInternalServerError, "resource_cache_delete_failed", err.Error())
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
