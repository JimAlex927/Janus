package config

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/http/httpguts"
)

func validateCORSSettings(settings CORSSettings) error {
	if len(settings.AllowOrigins) == 0 {
		return fmt.Errorf("allow_origins must not be empty")
	}
	wildcardOrigin := false
	seenOrigins := make(map[string]struct{}, len(settings.AllowOrigins))
	for _, origin := range settings.AllowOrigins {
		if origin == "*" {
			if len(settings.AllowOrigins) != 1 {
				return fmt.Errorf("allow_origins wildcard must be used alone")
			}
			wildcardOrigin = true
			continue
		}
		if strings.TrimSpace(origin) != origin {
			return fmt.Errorf("allow_origins contains whitespace around %q", origin)
		}
		parsed, err := url.ParseRequestURI(origin)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("allow_origins contains invalid origin %q", origin)
		}
		key := strings.ToLower(origin)
		if _, duplicate := seenOrigins[key]; duplicate {
			return fmt.Errorf("allow_origins contains duplicate origin %q", origin)
		}
		seenOrigins[key] = struct{}{}
	}
	if wildcardOrigin && settings.AllowCredentials {
		return fmt.Errorf("allow_credentials cannot be used with allow_origins wildcard")
	}
	if len(settings.AllowMethods) == 0 {
		return fmt.Errorf("allow_methods must not be empty")
	}
	if err := validateCORSMethods(settings.AllowMethods); err != nil {
		return err
	}
	if err := validateCORSHeaderNames("allow_headers", settings.AllowHeaders); err != nil {
		return err
	}
	if err := validateCORSHeaderNames("expose_headers", settings.ExposeHeaders); err != nil {
		return err
	}
	if settings.MaxAgeSeconds < 0 || settings.MaxAgeSeconds > MaxCORSMaxAgeSeconds {
		return fmt.Errorf("max_age_seconds must be between 0 and %d", MaxCORSMaxAgeSeconds)
	}
	return nil
}

func validateCORSMethods(methods []string) error {
	seen := make(map[string]struct{}, len(methods))
	for _, method := range methods {
		if method == "" || method != strings.ToUpper(method) || !httpguts.ValidHeaderFieldName(method) {
			return fmt.Errorf("allow_methods contains invalid uppercase HTTP method %q", method)
		}
		if _, duplicate := seen[method]; duplicate {
			return fmt.Errorf("allow_methods contains duplicate method %q", method)
		}
		seen[method] = struct{}{}
	}
	return nil
}

func validateCORSHeaderNames(field string, names []string) error {
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		canonical := http.CanonicalHeaderKey(trimmed)
		if canonical == "" || trimmed != name || !httpguts.ValidHeaderFieldName(name) {
			return fmt.Errorf("%s contains invalid header name %q", field, name)
		}
		if _, duplicate := seen[canonical]; duplicate {
			return fmt.Errorf("%s contains duplicate header %q", field, name)
		}
		seen[canonical] = struct{}{}
	}
	return nil
}
