package middleware

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/http/httpguts"
)

const (
	defaultForwardAuthTimeout  = 5 * time.Second
	defaultForwardAuthMaxBody  = 1 << 20
	maxForwardAuthResponseBody = 1 << 20
)

// ForwardAuthOptions follows Traefik ForwardAuth's core contract. The auth
// endpoint receives a sanitized copy of the request; 2xx permits the original
// request and any configured auth response headers are copied to it.
type ForwardAuthOptions struct {
	Address                  string
	AuthRequestHeaders       []string
	AuthResponseHeaders      []string
	AuthResponseHeadersRegex string
	HeaderField              string
	ForwardBody              bool
	MaxBodyBytes             int64
	MaxResponseBodyBytes     int64
	PreserveRequestMethod    bool
	Timeout                  time.Duration
	ForwardedHeaders         func(*http.Request) map[string]string
	Client                   *http.Client
}

func ForwardAuth(options ForwardAuthOptions) (Middleware, error) {
	authURL, err := validateForwardAuthOptions(&options)
	if err != nil {
		return nil, err
	}
	client := options.Client
	if client == nil {
		client = &http.Client{Timeout: options.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	requestHeaders := make(map[string]struct{}, len(options.AuthRequestHeaders))
	for _, name := range options.AuthRequestHeaders {
		requestHeaders[http.CanonicalHeaderKey(name)] = struct{}{}
	}
	responseHeaders := make(map[string]struct{}, len(options.AuthResponseHeaders))
	for _, name := range options.AuthResponseHeaders {
		responseHeaders[http.CanonicalHeaderKey(name)] = struct{}{}
	}
	var responseRegex *regexp.Regexp
	if options.AuthResponseHeadersRegex != "" {
		responseRegex, _ = regexp.Compile(options.AuthResponseHeadersRegex)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, restore, err := authBody(r, options)
			if err != nil {
				http.Error(w, "authentication request body too large", http.StatusUnauthorized)
				return
			}
			if restore != nil {
				defer restore()
			}
			authRequest := buildAuthRequest(r, authURL, body, options, requestHeaders)
			ctx, cancel := context.WithTimeout(r.Context(), options.Timeout)
			defer cancel()
			authRequest = authRequest.WithContext(ctx)
			response, err := client.Do(authRequest)
			if err != nil {
				http.Error(w, "authentication service unavailable", http.StatusBadGateway)
				return
			}
			defer response.Body.Close()
			response.Header = response.Header.Clone()
			removeConnectionHeaders(response.Header)
			if response.StatusCode < 200 || response.StatusCode >= 300 {
				writeForwardAuthResponse(w, response, options.MaxResponseBodyBytes)
				return
			}
			if options.HeaderField != "" {
				copyAuthHeader(r.Header, response.Header, options.HeaderField)
			}
			copyAuthResponseHeaders(r.Header, response.Header, responseHeaders, responseRegex)
			next.ServeHTTP(w, r)
		})
	}, nil
}

func validateForwardAuthOptions(options *ForwardAuthOptions) (*url.URL, error) {
	if strings.TrimSpace(options.Address) == "" {
		return nil, errors.New("forward_auth address is required")
	}
	u, err := url.Parse(options.Address)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, errors.New("forward_auth address must be an http(s) URL without credentials")
	}
	if options.Timeout == 0 {
		options.Timeout = defaultForwardAuthTimeout
	}
	if options.Timeout <= 0 || options.Timeout > time.Minute {
		return nil, errors.New("forward_auth timeout must be between 1ns and 1m")
	}
	if options.MaxResponseBodyBytes == 0 {
		options.MaxResponseBodyBytes = maxForwardAuthResponseBody
	}
	if options.MaxResponseBodyBytes < 1 || options.MaxResponseBodyBytes > maxForwardAuthResponseBody {
		return nil, fmt.Errorf("forward_auth max_response_body_bytes must be between 1 and %d", maxForwardAuthResponseBody)
	}
	if options.ForwardBody {
		if options.MaxBodyBytes == 0 {
			options.MaxBodyBytes = defaultForwardAuthMaxBody
		}
		if options.MaxBodyBytes < 1 || options.MaxBodyBytes > 64<<20 {
			return nil, errors.New("forward_auth max_body_bytes must be between 1 and 64MiB")
		}
	}
	for _, name := range options.AuthRequestHeaders {
		if !validForwardAuthRequestHeader(name) {
			return nil, fmt.Errorf("forward_auth request header %q is invalid or reserved", name)
		}
	}
	for _, name := range options.AuthResponseHeaders {
		if !validForwardAuthHeader(name) {
			return nil, fmt.Errorf("forward_auth response header %q is invalid or reserved", name)
		}
	}
	if options.HeaderField != "" && !validForwardAuthHeader(options.HeaderField) {
		return nil, fmt.Errorf("forward_auth header_field %q is invalid or reserved", options.HeaderField)
	}
	if options.AuthResponseHeadersRegex != "" {
		if _, err := regexp.Compile(options.AuthResponseHeadersRegex); err != nil {
			return nil, fmt.Errorf("forward_auth auth_response_headers_regex: %w", err)
		}
	}
	return u, nil
}

func validForwardAuthHeader(name string) bool {
	canonical := http.CanonicalHeaderKey(strings.TrimSpace(name))
	if canonical == "" || strings.TrimSpace(name) != name || !httpguts.ValidHeaderFieldName(name) {
		return false
	}
	switch {
	case canonical == "Authorization", canonical == "Cookie":
		return false
	case strings.HasPrefix(canonical, "X-Forwarded-"):
		return false
	case canonical == "Connection", canonical == "Content-Length", canonical == "Host", canonical == "Keep-Alive", canonical == "Proxy-Authenticate", canonical == "Proxy-Authorization", canonical == "Proxy-Connection", canonical == "Te", canonical == "Trailer", canonical == "Transfer-Encoding", canonical == "Upgrade":
		return false
	}
	return true
}

func validForwardAuthRequestHeader(name string) bool {
	canonical := http.CanonicalHeaderKey(strings.TrimSpace(name))
	if canonical == "" || strings.TrimSpace(name) != name || !httpguts.ValidHeaderFieldName(name) {
		return false
	}
	switch canonical {
	case "Connection", "Content-Length", "Host", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Proxy-Connection", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return false
	}
	return !strings.HasPrefix(canonical, "X-Forwarded-")
}

func authBody(r *http.Request, options ForwardAuthOptions) (io.Reader, func(), error) {
	if !options.ForwardBody || r.Body == nil || r.Body == http.NoBody {
		return nil, nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, options.MaxBodyBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(data)) > options.MaxBodyBytes {
		return nil, nil, errors.New("body too large")
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(data))
	return bytes.NewReader(data), func() { r.Body = io.NopCloser(bytes.NewReader(data)) }, nil
}

func buildAuthRequest(r *http.Request, target *url.URL, body io.Reader, options ForwardAuthOptions, allowed map[string]struct{}) *http.Request {
	method := http.MethodGet
	if options.PreserveRequestMethod {
		method = r.Method
	}
	authRequest, _ := http.NewRequestWithContext(r.Context(), method, target.String(), body)
	authRequest.Header = make(http.Header)
	if len(allowed) == 0 {
		for name, values := range r.Header {
			if validForwardAuthRequestHeader(name) {
				authRequest.Header[name] = append([]string(nil), values...)
			}
		}
	} else {
		for name, values := range r.Header {
			if _, ok := allowed[http.CanonicalHeaderKey(name)]; ok {
				authRequest.Header[name] = append([]string(nil), values...)
			}
		}
	}
	// Connection itself is not copied, so consult the original header for
	// additional hop-by-hop fields named by the client.
	for _, value := range r.Header.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			authRequest.Header.Del(strings.TrimSpace(name))
		}
	}
	authRequest.Header.Set("X-Forwarded-Method", r.Method)
	authRequest.Header.Set("X-Forwarded-Uri", r.URL.RequestURI())
	if options.ForwardedHeaders != nil {
		for name, value := range options.ForwardedHeaders(r) {
			if httpguts.ValidHeaderFieldName(name) && !strings.ContainsAny(value, "\r\n\x00") {
				authRequest.Header.Set(name, value)
			}
		}
	}
	return authRequest
}

func copyAuthHeader(destination, source http.Header, name string) {
	name = http.CanonicalHeaderKey(name)
	destination.Del(name)
	if value := source.Values(name); len(value) > 0 {
		destination[name] = append([]string(nil), value...)
	}
}

func copyAuthResponseHeaders(destination, source http.Header, allowed map[string]struct{}, pattern *regexp.Regexp) {
	// Selected headers belong to the authentication service. Remove client
	// values even when the successful auth response omits a selected header.
	for name := range destination {
		_, selected := allowed[http.CanonicalHeaderKey(name)]
		if pattern != nil && pattern.MatchString(name) {
			selected = true
		}
		if selected && validForwardAuthHeader(name) {
			delete(destination, name)
		}
	}
	for name, values := range source {
		canonical := http.CanonicalHeaderKey(name)
		_, selected := allowed[canonical]
		if pattern != nil && pattern.MatchString(name) {
			selected = true
		}
		if !selected || !validForwardAuthHeader(name) {
			continue
		}
		destination.Del(name)
		destination[name] = append([]string(nil), values...)
	}
}

func writeForwardAuthResponse(w http.ResponseWriter, response *http.Response, maxBody int64) {
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil || int64(len(body)) > maxBody {
		http.Error(w, "invalid authentication response", http.StatusBadGateway)
		return
	}
	for name, values := range response.Header {
		if validForwardAuthHeader(name) {
			w.Header()[name] = append([]string(nil), values...)
		}
	}
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(body)
}

func removeConnectionHeaders(header http.Header) {
	for _, value := range header.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			header.Del(strings.TrimSpace(name))
		}
	}
	header.Del("Connection")
}
