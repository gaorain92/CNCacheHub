// Package api: registries 端点（PRD §9.2.2）。
package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// registriesListHandler GET /api/registries
func registriesListHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opts.ListRegistries == nil {
			writeError(w, http.StatusServiceUnavailable, "registries_unavailable", "registries not configured")
			return
		}
		regs, err := opts.ListRegistries(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "registries_query_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items": regs,
			"total": len(regs),
		})
	}
}

// registryPatchHandler PATCH /api/registries/:name
//
// admin only（PRD §9.7.1）。body: {"enabled": true/false}。
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
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_body", "invalid JSON body")
			return
		}
		if patch.Enabled == nil {
			writeError(w, http.StatusBadRequest, "missing_field", "enabled is required")
			return
		}
		if err := opts.SetRegistryEnabled(r.Context(), name, *patch.Enabled); err != nil {
			writeError(w, http.StatusInternalServerError, "registry_update_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"name":    name,
			"enabled": *patch.Enabled,
		})
	}
}
