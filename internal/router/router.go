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

// This is the route Handler
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	//从请求中提取出 host
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(host)
	//Note: The core route loop
	//这里是路由匹配循环
	for _, route := range rt.routes {
		//先匹配当前请求中的host
		// 1、match the host to any host in the routes map first
		if route.Host != "" && route.Host != host {
			continue
		}
		// 2、 if matched host, then match the  PathPrefix to url.path of current request.
		p := route.PathPrefix
		if p == "/" || r.URL.Path == p || strings.HasPrefix(r.URL.Path, p+"/") {
			route.Handler.ServeHTTP(w, r)
			return
		}
	}
	//If nothing matched. Return 404 not found
	http.NotFound(w, r)
}
