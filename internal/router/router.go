// Package router matches immutable host/path rules against incoming requests.
package router

import (
	"net"
	"net/http"
	"sort"
	"strings"
)

type Route struct {
	Host       string
	PathPrefix string
	Handler    http.Handler
}

type Router struct{ routes []Route }

// New gives exact hosts precedence, then the longest segment prefix.
// Callers must validate duplicate rules and non-nil handlers before construction.
func New(routes []Route) *Router {
	routes = append([]Route(nil), routes...)
	for i := range routes {
		routes[i].Host = strings.ToLower(routes[i].Host)
	}
	sort.SliceStable(routes, func(i, j int) bool {
		if (routes[i].Host == "") != (routes[j].Host == "") {
			return routes[i].Host != ""
		}
		return len(routes[i].PathPrefix) > len(routes[j].PathPrefix)
	})
	return &Router{routes: routes}
}

func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(host)
	for _, route := range rt.routes {
		if route.Host != "" && route.Host != host {
			continue
		}
		p := route.PathPrefix
		if p == "/" || r.URL.Path == p || strings.HasPrefix(r.URL.Path, p+"/") {
			route.Handler.ServeHTTP(w, r)
			return
		}
	}
	http.NotFound(w, r)
}
