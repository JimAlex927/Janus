package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"janus/internal/config"
	"janus/internal/store"
)

// This file implements the named-configuration library API backed by the
// SQLite store:
//
//	GET    /api/v1/configs               list drafts (light records)
//	POST   /api/v1/configs               create a draft from blank/active/named source
//	GET    /api/v1/configs/{id}          fetch a draft with content and canvas layout
//	PUT    /api/v1/configs/{id}          save a draft (name/content/layout)
//	DELETE /api/v1/configs/{id}          delete a non-active draft
//	POST   /api/v1/configs/{id}/publish  publish a draft as the running generation
//	POST   /api/v1/config/settings       rewrite the active file settings (restart required)
//
// Publishing normalizes the draft to the active startup-owned sections
// (settings and limens) before going through the standard publish path, so a
// stored draft can never fail activation over listener or process settings.

type configRecordView struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toRecordView(r store.ConfigRecord) configRecordView {
	return configRecordView{ID: r.ID, Name: r.Name, Status: r.Status, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

func (h *Handler) configs(w http.ResponseWriter, r *http.Request) {
	if h.current == nil {
		return
	}
	if r.Method == http.MethodGet {
		h.listConfigs(w, r)
		return
	}
	if r.Method == http.MethodPost {
		h.createConfig(w, r)
		return
	}
	w.Header().Set("Allow", "GET, POST")
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func (h *Handler) configsSub(w http.ResponseWriter, r *http.Request) {
	if h.current == nil {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/configs/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" || len(parts) > 2 {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid configuration id", http.StatusBadRequest)
		return
	}
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	switch action {
	case "":
		switch r.Method {
		case http.MethodGet:
			h.getConfig(w, r, id)
		case http.MethodPut:
			h.saveConfig(w, r, id)
		case http.MethodDelete:
			h.deleteConfig(w, r, id)
		default:
			w.Header().Set("Allow", "GET, PUT, DELETE")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	case "publish":
		if !allowMethod(w, r, http.MethodPost) {
			return
		}
		h.publishStoredConfig(w, r, id)
	case "stage-limens":
		if !allowMethod(w, r, http.MethodPost) {
			return
		}
		h.stageLimens(w, r, id)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) requireLibrary(w http.ResponseWriter) bool {
	if h.library == nil {
		http.Error(w, "configuration library is unavailable", http.StatusNotImplemented)
		return false
	}
	return true
}

func (h *Handler) requireVersionedActive(w http.ResponseWriter) (config.Config, bool) {
	active := h.current()
	if active.Version != config.CurrentConfigVersion {
		http.Error(w, "configuration library requires a versioned active configuration", http.StatusUnprocessableEntity)
		return config.Config{}, false
	}
	return active, true
}

func (h *Handler) listConfigs(w http.ResponseWriter, r *http.Request) {
	if !h.guard(r) {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if !h.requireLibrary(w) {
		return
	}
	records, err := h.library.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	views := make([]configRecordView, 0, len(records))
	for _, record := range records {
		views = append(views, toRecordView(record))
	}
	writeJSON(w, http.StatusOK, map[string]any{"configs": views})
}

func (h *Handler) createConfig(w http.ResponseWriter, r *http.Request) {
	if !h.guard(r) {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if !h.requireLibrary(w) {
		return
	}
	var input struct {
		Name    string          `json:"name"`
		From    string          `json:"from"`
		Content json.RawMessage `json:"content"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Name) == "" {
		http.Error(w, "configuration name is required", http.StatusBadRequest)
		return
	}
	active, ok := h.requireVersionedActive(w)
	if !ok {
		return
	}
	candidate := blankTemplate(active)
	switch {
	case len(input.Content) > 0:
		parsed, err := decodeStoredContent(input.Content, active.Settings.Admin.PasswordHash, active.Discovery)
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		candidate = parsed
	case input.From == "" || input.From == "blank":
		// blankTemplate above.
	case input.From == "active":
		candidate = active
	default:
		id, err := strconv.ParseInt(input.From, 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "invalid source configuration", http.StatusBadRequest)
			return
		}
		source, err := h.library.Get(id)
		if err != nil {
			writeLibraryError(w, err)
			return
		}
		if source == nil {
			http.Error(w, "source configuration does not exist", http.StatusNotFound)
			return
		}
		candidate = source.Content
	}
	candidate = candidate.WithDefaults()
	if err := candidate.Validate(); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	record, err := h.library.Create(input.Name, candidate)
	if err != nil {
		writeLibraryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "id": record.ID, "name": record.Name, "status": record.Status})
}

func (h *Handler) getConfig(w http.ResponseWriter, r *http.Request, id int64) {
	if !h.guard(r) {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if !h.requireLibrary(w) {
		return
	}
	record, err := h.library.Get(id)
	if err != nil {
		writeLibraryError(w, err)
		return
	}
	if record == nil {
		http.Error(w, "configuration does not exist", http.StatusNotFound)
		return
	}
	layout, err := h.library.Layout(id)
	if err != nil {
		writeLibraryError(w, err)
		return
	}
	content := record.Content
	content.Settings.Admin.PasswordHash = ""
	content.Discovery = content.Discovery.Redacted()
	writeJSON(w, http.StatusOK, map[string]any{
		"id":         record.ID,
		"name":       record.Name,
		"status":     record.Status,
		"content":    content,
		"layout":     json.RawMessage(layout),
		"created_at": record.CreatedAt,
		"updated_at": record.UpdatedAt,
	})
}

func (h *Handler) saveConfig(w http.ResponseWriter, r *http.Request, id int64) {
	if !h.guard(r) {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if !h.requireLibrary(w) {
		return
	}
	if _, ok := h.requireVersionedActive(w); !ok {
		return
	}
	var input struct {
		Name    *string         `json:"name"`
		Content json.RawMessage `json:"content"`
		Layout  json.RawMessage `json:"layout"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	record, err := h.library.Get(id)
	if err != nil {
		writeLibraryError(w, err)
		return
	}
	if record == nil {
		http.Error(w, "configuration does not exist", http.StatusNotFound)
		return
	}
	name := record.Name
	if input.Name != nil {
		if strings.TrimSpace(*input.Name) == "" {
			http.Error(w, "configuration name is required", http.StatusBadRequest)
			return
		}
		name = strings.TrimSpace(*input.Name)
	}
	content := record.Content
	if len(input.Content) > 0 {
		parsed, err := decodeStoredContent(input.Content, h.current().Settings.Admin.PasswordHash, record.Content.Discovery)
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		parsed = parsed.WithDefaults()
		if err := parsed.Validate(); err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		content = parsed
	}
	if err := h.library.Update(id, name, content); err != nil {
		writeLibraryError(w, err)
		return
	}
	if len(input.Layout) > 0 {
		if err := h.library.SaveLayout(id, string(input.Layout)); err != nil {
			writeLibraryError(w, err)
			return
		}
	}
	updated, err := h.library.Get(id)
	if err != nil {
		writeLibraryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": updated.ID, "updated_at": updated.UpdatedAt})
}

func (h *Handler) deleteConfig(w http.ResponseWriter, r *http.Request, id int64) {
	if !h.guard(r) {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if !h.requireLibrary(w) {
		return
	}
	if err := h.library.Delete(id); err != nil {
		writeLibraryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) publishStoredConfig(w http.ResponseWriter, r *http.Request, id int64) {
	if h.publish == nil {
		http.Error(w, "publishing is unavailable", http.StatusServiceUnavailable)
		return
	}
	if !h.authenticated(r) {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if !h.requireLibrary(w) {
		return
	}
	active, ok := h.requireVersionedActive(w)
	if !ok {
		return
	}
	record, err := h.library.Get(id)
	if err != nil {
		writeLibraryError(w, err)
		return
	}
	if record == nil {
		http.Error(w, "configuration does not exist", http.StatusNotFound)
		return
	}
	startupChanged := !startupOwnedEqual(record.Content, active)
	candidate := normalizeToActive(record.Content, active)
	candidate = candidate.WithDefaults()
	if err := candidate.Validate(); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := h.publish(candidate); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := h.library.Publish(id); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "revision": h.revisionValue(), "warning": err.Error()})
		return
	}
	result := map[string]any{"ok": true, "revision": h.revisionValue()}
	if startupChanged {
		result["warning"] = "limens or settings differ from the running configuration: routes/services were published, listener and process settings require a file edit and restart"
	}
	writeJSON(w, http.StatusOK, result)
}

// stageLimens merges a draft's limens into the active file without touching
// the running generation. Listener bindings cannot be hot-reloaded, so the
// operator must restart Janus; the file reloader keeps serving the previous
// generation until then, exactly as with a manual file edit or a settings
// save. Stored settings are deliberately left alone (see saveSettings).
func (h *Handler) stageLimens(w http.ResponseWriter, r *http.Request, id int64) {
	if h.saveActive == nil {
		http.Error(w, "settings persistence is unavailable", http.StatusNotImplemented)
		return
	}
	if !h.authenticated(r) {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if !h.requireLibrary(w) {
		return
	}
	active, ok := h.requireVersionedActive(w)
	if !ok {
		return
	}
	record, err := h.library.Get(id)
	if err != nil {
		writeLibraryError(w, err)
		return
	}
	if record == nil {
		http.Error(w, "configuration does not exist", http.StatusNotFound)
		return
	}
	merged := active
	merged.Limens = cloneLimenMap(record.Content.Limens)
	merged = merged.WithDefaults()
	if err := merged.Validate(); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := h.saveActive(merged); err != nil {
		http.Error(w, fmt.Sprintf("persist limens: %v", err), http.StatusInternalServerError)
		return
	}
	result := map[string]any{"ok": true, "restart_required": true}
	if !jsonEqual(record.Content.Settings, active.Settings) {
		result["warning"] = "stored settings were not staged; use global settings to change them"
	}
	writeJSON(w, http.StatusOK, result)
}
// saveSettings validates and atomically rewrites the active file with new
// process settings. The running generation is intentionally untouched:
// listener and process-wide settings cannot be hot-reloaded, so the operator
// must restart Janus. The file reloader keeps serving the previous generation
// until then, exactly as with a manual file edit.
func (h *Handler) saveSettings(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	if h.saveActive == nil {
		http.Error(w, "settings persistence is unavailable", http.StatusNotImplemented)
		return
	}
	if !h.authenticated(r) {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if h.current == nil {
		http.Error(w, "settings are unavailable", http.StatusServiceUnavailable)
		return
	}
	var settings config.Settings
	if !decodeJSON(w, r, &settings) {
		return
	}
	merged := h.current()
	merged.Settings = settings
	merged = merged.WithDefaults()
	if err := merged.Validate(); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := h.saveActive(merged); err != nil {
		http.Error(w, fmt.Sprintf("persist settings: %v", err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "restart_required": true})
}

// decodeStoredContent runs draft content through the same strict decoding as
// the console publish path: size bound, duplicate keys and unknown fields are
// rejected, and redacted secrets are inherited from the previous discovery.
func decodeStoredContent(raw json.RawMessage, inheritedHash string, previous config.DiscoveryConfig) (config.Config, error) {
	if len(raw) > config.MaxConfigBytes {
		return config.Config{}, fmt.Errorf("config exceeds %d bytes", config.MaxConfigBytes)
	}
	return config.LoadWithAdminSecrets(bytes.NewReader(raw), inheritedHash, previous)
}

// normalizeToActive forces the startup-owned sections of a draft to the
// active snapshot. Only routes, services, middlewares and discovery are
// versioned per draft, which keeps every stored draft activatable.
func normalizeToActive(draft, active config.Config) config.Config {
	draft.Version = active.Version
	draft.Listen = active.Listen
	draft.TrustedProxies = append([]string(nil), active.TrustedProxies...)
	if active.Limens != nil {
		draft.Limens = cloneLimenMap(active.Limens)
	} else {
		draft.Limens = nil
	}
	draft.Settings = active.Settings
	return draft
}

func cloneLimenMap(source map[string]config.LimenConfig) map[string]config.LimenConfig {
	clone := make(map[string]config.LimenConfig, len(source))
	for name, binding := range source {
		binding.Protocols = append([]string(nil), binding.Protocols...)
		binding.TrustedProxies = append([]string(nil), binding.TrustedProxies...)
		if binding.TLS != nil {
			tls := *binding.TLS
			binding.TLS = &tls
		}
		if binding.HTTP3 != nil {
			http3 := *binding.HTTP3
			binding.HTTP3 = &http3
		}
		clone[name] = binding
	}
	return clone
}

// startupOwnedEqual reports whether two configurations share the same
// startup-owned sections (version, listen, limens, settings). The library
// only versions hot sections, so a mismatch means the draft carries listener
// or process settings that publishing will normalize away.
func startupOwnedEqual(a, b config.Config) bool {
	aN, bN := a.WithDefaults(), b.WithDefaults()
	if aN.Version != bN.Version || aN.Listen != bN.Listen {
		return false
	}
	if !jsonEqual(aN.Limens, bN.Limens) {
		return false
	}
	if !jsonEqual(aN.TrustedProxies, bN.TrustedProxies) {
		return false
	}
	return jsonEqual(aN.Settings, bN.Settings)
}

func jsonEqual(a, b any) bool {
	rawA, errA := json.Marshal(a)
	rawB, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(rawA) == string(rawB)
}

// blankTemplate builds a minimal valid draft carrying the active
// startup-owned sections, so it validates standalone and stays activatable.
func blankTemplate(active config.Config) config.Config {
	names := make([]string, 0, len(active.Limens))
	for name := range active.Limens {
		names = append(names, name)
	}
	sort.Strings(names)
	limen := ""
	if len(names) > 0 {
		limen = names[0]
	}
	candidate := active
	candidate.Routes = []config.Route{{
		Name:  "example-api",
		Limen: limen,
		Match: "PathPrefix(`/api`)",
		Action: &config.RouteAction{
			Forward: &config.ForwardAction{Service: "example"},
		},
	}}
	candidate.Services = map[string]config.Service{
		"example": {Upstreams: []string{"http://127.0.0.1:9000"}},
	}
	candidate.Middlewares = nil
	return candidate
}

func writeLibraryError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "configuration does not exist", http.StatusNotFound)
		return
	}
	message := err.Error()
	switch {
	case strings.Contains(message, "cannot delete active config"):
		http.Error(w, message, http.StatusConflict)
	default:
		http.Error(w, message, http.StatusBadRequest)
	}
}
