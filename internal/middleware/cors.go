package middleware

import (
	"net/http"
	"strconv"
	"strings"

	"janus/internal/protocol"
)

// CORSOptions controls the browser-facing CORS contract. Configuration
// validation supplies normalized, safe values before this constructor runs.
type CORSOptions struct {
	AllowOrigins     []string
	AllowMethods     []string
	AllowHeaders     []string
	ExposeHeaders    []string
	AllowCredentials bool
	MaxAgeSeconds    int
}

// CORS applies a configured cross-origin policy. Successful preflight requests
// end with 204 before the wrapped handler (and therefore before the upstream)
// runs. An unrecognized Origin is an ordinary request without CORS response
// headers; an otherwise recognized but disallowed preflight is rejected.
func CORS(options CORSOptions) Middleware {
	policy := newCORSPolicy(options)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// CORS does not govern the WebSocket Origin contract. Leave upgrade
			// handshakes untouched, just like the generic response header policy.
			if protocol.IsWebSocketRequest(r) {
				next.ServeHTTP(w, r)
				return
			}
			origin := r.Header.Get("Origin")
			allowOrigin, allowed := policy.allowOrigin(origin)
			if !allowed {
				next.ServeHTTP(w, r)
				return
			}
			if requestedMethod := r.Header.Get("Access-Control-Request-Method"); r.Method == http.MethodOptions && requestedMethod != "" {
				if !policy.allowsMethod(requestedMethod) || !policy.allowsRequestedHeaders(r.Header.Get("Access-Control-Request-Headers")) {
					http.Error(w, "CORS preflight is not allowed", http.StatusForbidden)
					return
				}
				policy.apply(w.Header(), allowOrigin, true)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			response := &corsResponseWriter{ResponseWriter: w, policy: policy, allowOrigin: allowOrigin}
			next.ServeHTTP(response, r)
			// A handler may return without WriteHeader or Write. net/http then
			// commits its implicit 200 after ServeHTTP returns, so decorate its
			// headers while they are still mutable.
			response.apply()
		})
	}
}

type corsPolicy struct {
	wildcard         bool
	origins          map[string]struct{}
	methods          map[string]struct{}
	headers          map[string]struct{}
	allowMethods     string
	allowHeaders     string
	exposeHeaders    string
	allowCredentials bool
	maxAgeSeconds    int
}

func newCORSPolicy(options CORSOptions) corsPolicy {
	policy := corsPolicy{
		origins:          make(map[string]struct{}, len(options.AllowOrigins)),
		methods:          make(map[string]struct{}, len(options.AllowMethods)),
		headers:          make(map[string]struct{}, len(options.AllowHeaders)),
		allowMethods:     strings.Join(options.AllowMethods, ", "),
		allowHeaders:     strings.Join(options.AllowHeaders, ", "),
		exposeHeaders:    strings.Join(options.ExposeHeaders, ", "),
		allowCredentials: options.AllowCredentials,
		maxAgeSeconds:    options.MaxAgeSeconds,
	}
	for _, origin := range options.AllowOrigins {
		if origin == "*" {
			policy.wildcard = true
			continue
		}
		policy.origins[strings.ToLower(origin)] = struct{}{}
	}
	for _, method := range options.AllowMethods {
		policy.methods[strings.ToUpper(method)] = struct{}{}
	}
	for _, header := range options.AllowHeaders {
		policy.headers[strings.ToLower(header)] = struct{}{}
	}
	return policy
}

func (p corsPolicy) allowOrigin(origin string) (string, bool) {
	if origin == "" {
		return "", false
	}
	if p.wildcard {
		return "*", true
	}
	_, allowed := p.origins[strings.ToLower(origin)]
	return origin, allowed
}

func (p corsPolicy) allowsMethod(method string) bool {
	_, allowed := p.methods[method]
	return allowed
}

func (p corsPolicy) allowsRequestedHeaders(raw string) bool {
	if raw == "" {
		return true
	}
	for _, header := range strings.Split(raw, ",") {
		if _, allowed := p.headers[strings.ToLower(strings.TrimSpace(header))]; !allowed {
			return false
		}
	}
	return true
}

func (p corsPolicy) apply(header http.Header, origin string, preflight bool) {
	header.Set("Access-Control-Allow-Origin", origin)
	if origin != "*" {
		appendVary(header, "Origin")
	}
	if p.allowCredentials {
		header.Set("Access-Control-Allow-Credentials", "true")
	}
	if preflight {
		appendVary(header, "Access-Control-Request-Method")
		appendVary(header, "Access-Control-Request-Headers")
		header.Set("Access-Control-Allow-Methods", p.allowMethods)
		if p.allowHeaders != "" {
			header.Set("Access-Control-Allow-Headers", p.allowHeaders)
		}
		if p.maxAgeSeconds > 0 {
			header.Set("Access-Control-Max-Age", strconv.Itoa(p.maxAgeSeconds))
		}
		return
	}
	if p.exposeHeaders != "" {
		header.Set("Access-Control-Expose-Headers", p.exposeHeaders)
	}
}

func appendVary(header http.Header, name string) {
	for _, value := range header.Values("Vary") {
		for _, existing := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(existing), name) {
				return
			}
		}
	}
	header.Add("Vary", name)
}

type corsResponseWriter struct {
	http.ResponseWriter
	policy      corsPolicy
	allowOrigin string
	applied     bool
}

func (w *corsResponseWriter) apply() {
	if !w.applied {
		w.applied = true
		w.policy.apply(w.Header(), w.allowOrigin, false)
	}
}

func (w *corsResponseWriter) WriteHeader(status int) {
	if status >= http.StatusOK {
		w.apply()
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *corsResponseWriter) Write(body []byte) (int, error) {
	w.apply()
	return w.ResponseWriter.Write(body)
}

func (w *corsResponseWriter) Flush() {
	w.apply()
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *corsResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
