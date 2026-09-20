// Package admin provides the private health endpoints and operator console.
package admin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"embed"
	"janus/internal/config"
	"janus/internal/discovery"
	janusruntime "janus/internal/runtime"
	"janus/internal/store"
	"janus/internal/telemetry"

	"golang.org/x/crypto/bcrypt"
)

//go:embed ui/* ui/assets/*
var uiFiles embed.FS

// uiBaseURL is set by the release build with -X. It intentionally defaults to
// root so ordinary `go build` and existing deployments keep their URLs.
var uiBaseURL string

type State struct{ live, ready atomic.Bool }

func NewState() *State { state := &State{}; state.live.Store(true); return state }
func (s *State) SetLive(value bool) {
	if s != nil {
		s.live.Store(value)
	}
}
func (s *State) SetReady(value bool) {
	if s != nil {
		s.ready.Store(value)
	}
}
func (s *State) Live() bool  { return s != nil && s.live.Load() }
func (s *State) Ready() bool { return s != nil && s.ready.Load() }

// Options connects the admin process to the stable Runtime without putting
// admin concerns into the business request path.
//
// Library enables the named-configuration library (/api/v1/configs...).
// SaveActive enables POST /api/v1/config/settings, which validates and
// atomically rewrites the active file without touching the running
// generation; such changes require a process restart to take effect.
type Options struct {
	State          *State
	Metrics        *telemetry.Metrics
	Health         func() []telemetry.BackendHealth
	Current        func() config.Config
	Revision       func() uint64
	Discovery      func() map[string]discovery.Status
	RegistryHealth func(string, *config.NacosRegistry) discovery.RegistryHealth
	// Publish must atomically reject a stale expected revision. The console
	// performs an early check for a clear response, while this callback closes
	// the check-then-publish race at Runtime's serialization boundary.
	Publish    func(config.Config, uint64) error
	Subscribe  func() (<-chan janusruntime.Event, func())
	Library    *store.Store
	SaveActive func(config.Config) error
	// UIBaseURL mounts the console and its API below an absolute path such as
	// /janus. Empty uses the build-time default, which is root by default.
	UIBaseURL string
}

type Handler struct {
	state          *State
	metrics        *telemetry.Metrics
	health         func() []telemetry.BackendHealth
	current        func() config.Config
	revision       func() uint64
	discovery      func() map[string]discovery.Status
	registryHealth func(string, *config.NacosRegistry) discovery.RegistryHealth
	publish        func(config.Config, uint64) error
	subscribe      func() (<-chan janusruntime.Event, func())
	library        *store.Store
	saveActive     func(config.Config) error
	cookiePath     string
	sessionsMu     sync.Mutex
	controlMu      sync.Mutex // serialize file/runtime/library mutations
	sessions       map[string]time.Time
}

const maxSessions = 128

func NewHandler(state *State) http.Handler { return NewHandlerWithMetrics(state, nil, nil) }
func NewHandlerWithMetrics(state *State, metrics *telemetry.Metrics, health func() []telemetry.BackendHealth) http.Handler {
	return NewHandlerWithOptions(Options{State: state, Metrics: metrics, Health: health})
}

func NewHandlerWithOptions(options Options) http.Handler {
	if options.State == nil {
		options.State = NewState()
	}
	baseURL := normalizeUIBaseURL(options.UIBaseURL)
	if strings.TrimSpace(options.UIBaseURL) == "" {
		baseURL = normalizeUIBaseURL(uiBaseURL)
	}
	cookiePath := "/"
	if baseURL != "" {
		cookiePath = baseURL + "/"
	}
	h := &Handler{state: options.State, metrics: options.Metrics, health: options.Health, current: options.Current, revision: options.Revision, discovery: options.Discovery, registryHealth: options.RegistryHealth, publish: options.Publish, subscribe: options.Subscribe, library: options.Library, saveActive: options.SaveActive, cookiePath: cookiePath, sessions: make(map[string]time.Time)}
	console := http.NewServeMux()
	console.HandleFunc("/", h.ui)
	console.Handle("/api/v1/auth/login", protectLogin(http.HandlerFunc(h.login)))
	console.HandleFunc("/api/v1/auth/logout", h.logout)
	console.HandleFunc("/api/v1/status", h.status)
	console.HandleFunc("/api/v1/discovery", h.discoveryStatus)
	console.HandleFunc("/api/v1/discovery/registries/health", h.registryHealthCheck)
	console.HandleFunc("/api/v1/metrics", h.metricsSummary)
	console.HandleFunc("/api/v1/capabilities/middlewares", h.middlewareCapabilities)
	console.HandleFunc("/api/v1/config", h.configHandler)
	console.HandleFunc("/api/v1/config/validate", h.validate)
	console.HandleFunc("/api/v1/config/publish", h.publishConfig)
	console.HandleFunc("/api/v1/config/settings", h.saveSettings)
	console.HandleFunc("/api/v1/configs", h.configs)
	console.HandleFunc("/api/v1/configs/", h.configsSub)
	console.HandleFunc("/api/v1/events", h.events)
	mux := http.NewServeMux()
	mux.HandleFunc("/livez", h.livez)
	mux.HandleFunc("/readyz", h.readyz)
	if options.Metrics != nil {
		mux.HandleFunc("/metrics", h.metricsHandler)
	}
	if baseURL == "" {
		mux.Handle("/", console)
	} else {
		mux.HandleFunc(baseURL, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, baseURL+"/", http.StatusTemporaryRedirect)
		})
		mux.Handle(baseURL+"/", http.StripPrefix(baseURL, console))
	}
	return securityHeaders(mux)
}

func normalizeUIBaseURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "/" || !strings.HasPrefix(value, "/") || strings.Contains(value, "//") {
		return ""
	}
	value = strings.TrimRight(value, "/")
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return ""
		}
	}
	if value == "" {
		return ""
	}
	for _, character := range value {
		if !(character == '/' || character == '-' || character == '_' || character == '.' || character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9') {
			return ""
		}
	}
	return value
}

func (h *Handler) livez(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	if !h.state.Live() {
		http.Error(w, "not live", 503)
		return
	}
	writeOK(w, r)
}
func (h *Handler) readyz(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	if !h.state.Ready() {
		http.Error(w, "not ready", 503)
		return
	}
	writeOK(w, r)
}
func (h *Handler) metricsHandler(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(200)
	if r.Method == http.MethodHead {
		return
	}
	var snapshot []telemetry.BackendHealth
	if h.health != nil {
		snapshot = h.health()
	}
	_, _ = w.Write(h.metrics.Render(snapshot))
}
func (h *Handler) ui(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	data, err := uiFiles.ReadFile("ui/" + path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(path, ".html") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		// cookiePath is the validated, slash-terminated console mount. Insert
		// before any resource references so assets and API URLs share one base.
		data = []byte(strings.Replace(string(data), "<head>", `<head><base href="`+h.cookiePath+`">`, 1))
	}
	if strings.HasSuffix(path, ".js") {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	}
	if strings.HasSuffix(path, ".css") {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	}
	_, _ = w.Write(data)
}
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	var input struct{ Username, Password string }
	if !decodeJSON(w, r, &input) {
		return
	}
	if h.current == nil {
		http.Error(w, "admin login is unavailable", 503)
		return
	}
	a := h.current().Settings.Admin
	if a.Username == "" || a.PasswordHash == "" || input.Username != a.Username || bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(input.Password)) != nil {
		http.Error(w, "invalid credentials", 401)
		return
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		http.Error(w, "session unavailable", 500)
		return
	}
	token := hex.EncodeToString(raw[:])
	h.sessionsMu.Lock()
	now := time.Now()
	for existing, expiry := range h.sessions {
		if now.After(expiry) {
			delete(h.sessions, existing)
		}
	}
	if len(h.sessions) >= maxSessions {
		h.sessionsMu.Unlock()
		http.Error(w, "too many admin sessions", http.StatusTooManyRequests)
		return
	}
	h.sessions[token] = now.Add(8 * time.Hour)
	h.sessionsMu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "janus_session", Value: token, Path: h.cookiePath, HttpOnly: true, Secure: h.secureSessionCookie(r), SameSite: http.SameSiteStrictMode, MaxAge: 8 * 60 * 60})
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	if c, err := r.Cookie("janus_session"); err == nil {
		h.sessionsMu.Lock()
		delete(h.sessions, c.Value)
		h.sessionsMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "janus_session", Path: h.cookiePath, MaxAge: -1, HttpOnly: true, Secure: h.secureSessionCookie(r), SameSite: http.SameSiteStrictMode})
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	if !h.guard(r) {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	result := map[string]any{"live": h.state.Live(), "ready": h.state.Ready(), "revision": h.revisionValue()}
	writeJSON(w, 200, result)
}

func (h *Handler) discoveryStatus(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	if !h.guard(r) {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if h.discovery == nil {
		http.Error(w, "discovery status unavailable", http.StatusNotImplemented)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"revision": h.revisionValue(),
		"services": h.discovery(),
	})
}

func (h *Handler) registryHealthCheck(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	if !h.guard(r) {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if h.registryHealth == nil {
		http.Error(w, "registry health checks unavailable", http.StatusNotImplemented)
		return
	}
	var request struct {
		Name     string                `json:"name"`
		Registry *config.NacosRegistry `json:"registry,omitempty"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.Name == "" {
		http.Error(w, "registry name is required", http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, h.registryHealth(request.Name, request.Registry))
}

func (h *Handler) metricsSummary(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	if !h.guard(r) {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if h.metrics == nil {
		http.Error(w, "metrics unavailable", http.StatusNotImplemented)
		return
	}
	writeJSON(w, http.StatusOK, h.metrics.Summary())
}

func (h *Handler) middlewareCapabilities(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	if !h.guard(r) {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"middlewares": config.MiddlewareCapabilities(),
	})
}
func (h *Handler) configHandler(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) || h.current == nil {
		return
	}
	if !h.guard(r) {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	c := h.current()
	c.Settings.Admin.PasswordHash = ""
	c.Discovery = c.Discovery.Redacted()
	writeJSON(w, 200, map[string]any{"config": c, "revision": h.revisionValue()})
}
func (h *Handler) validate(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	if !h.guard(r) {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	c, ok := h.decodeConfig(w, r)
	if !ok {
		return
	}
	if err := c.Validate(); err != nil {
		writeJSON(w, 422, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (h *Handler) publishConfig(w http.ResponseWriter, r *http.Request) {
	h.controlMu.Lock()
	defer h.controlMu.Unlock()
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	if h.publish == nil {
		http.Error(w, "publishing is unavailable", 503)
		return
	}
	if !h.authenticated(r) {
		http.Error(w, "authentication required", 401)
		return
	}
	expectedRevision, ok := h.expectedRevision(w, r)
	if !ok {
		return
	}
	c, ok := h.decodeConfig(w, r)
	if !ok {
		return
	}
	if err := h.publish(c, expectedRevision); err != nil {
		if errors.Is(err, janusruntime.ErrRevisionConflict) {
			http.Error(w, "configuration revision conflict", http.StatusConflict)
			return
		}
		writeJSON(w, 422, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "revision": h.revisionValue()})
}
func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) || h.subscribe == nil {
		http.Error(w, "events unavailable", 501)
		return
	}
	if !h.guard(r) {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	_, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unavailable", 500)
		return
	}
	ch, cancel := h.subscribe()
	defer cancel()
	controller := http.NewResponseController(w)
	writeEvent := func(message string) error {
		// The server's WriteTimeout is a whole-response deadline, unsuitable
		// for a persistent event stream. Bound each write/flush instead, and
		// clear the deadline between events. Slow readers still cannot pin us.
		if err := controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return err
		}
		if _, err := io.WriteString(w, message); err != nil {
			return err
		}
		if err := controller.Flush(); err != nil {
			return err
		}
		if err := controller.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return err
		}
		return nil
	}
	if err := writeEvent(fmt.Sprintf("event: ready\ndata: {\"revision\":%d}\n\n", h.revisionValue())); err != nil {
		return
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-ch:
			if !open {
				return
			}
			data, _ := json.Marshal(event)
			if err := writeEvent(fmt.Sprintf("event: %s\ndata: %s\n\n", event.Type, data)); err != nil {
				return
			}
		case <-ticker.C:
			if err := writeEvent(": keep-alive\n\n"); err != nil {
				return
			}
		}
	}
}
func (h *Handler) authenticated(r *http.Request) bool {
	c, err := r.Cookie("janus_session")
	if err != nil {
		return false
	}
	h.sessionsMu.Lock()
	expiry, ok := h.sessions[c.Value]
	if ok && time.Now().After(expiry) {
		delete(h.sessions, c.Value)
		ok = false
	}
	h.sessionsMu.Unlock()
	return ok
}

func (h *Handler) guard(r *http.Request) bool {
	if h.current == nil {
		return false
	}
	a := h.current().Settings.Admin
	return (a.Username == "" && a.PasswordHash == "") || h.authenticated(r)
}
func (h *Handler) revisionValue() uint64 {
	if h.revision == nil {
		return 0
	}
	return h.revision()
}

// expectedRevision checks the HTTP precondition used by every operation that
// swaps the live generation. Runtime verifies the same value atomically when
// it receives the publish callback, so this early check is only an ergonomic
// fast path rather than the source of correctness.
func (h *Handler) expectedRevision(w http.ResponseWriter, r *http.Request) (uint64, bool) {
	if h.revision == nil {
		return 0, true
	}
	expected := h.revision()
	if r.Header.Get("X-Janus-Revision") != fmt.Sprint(expected) {
		http.Error(w, "configuration revision conflict", http.StatusConflict)
		return 0, false
	}
	return expected, true
}
func (h *Handler) decodeConfig(w http.ResponseWriter, r *http.Request) (config.Config, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, config.MaxConfigBytes+1))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return config.Config{}, false
	}
	if len(body) > config.MaxConfigBytes {
		http.Error(w, "config is too large", 413)
		return config.Config{}, false
	}
	inheritedHash := ""
	if h.current != nil {
		inheritedHash = h.current().Settings.Admin.PasswordHash
	}
	previousDiscovery := config.DiscoveryConfig{}
	if h.current != nil {
		previousDiscovery = h.current().Discovery
	}
	c, err := config.LoadWithAdminSecrets(strings.NewReader(string(body)), inheritedHash, previousDiscovery)
	if err != nil {
		writeJSON(w, 422, map[string]any{"ok": false, "error": err.Error()})
		return config.Config{}, false
	}
	return c, true
}
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	d := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), 400)
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func allowMethod(w http.ResponseWriter, r *http.Request, methods ...string) bool {
	for _, method := range methods {
		if r.Method == method {
			return true
		}
	}
	w.Header().Set("Allow", strings.Join(methods, ", "))
	http.Error(w, "method not allowed", 405)
	return false
}
func writeOK(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(200)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte("ok\n"))
	}
}
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		if strings.Contains(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}
