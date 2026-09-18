// Package router matches immutable request rules against compiled handlers.
package router

import (
	"fmt"
	"net"
	"net/http"
	"strings"

	"janus/internal/protocol"
	"janus/internal/rules"
	"janus/internal/telemetry"
)

type Route struct {
	Name       string
	Limen      string
	Match      string
	Priority   int
	Host       string
	PathPrefix string
	// CORSPreflight permits one safe fallback lookup for an OPTIONS CORS
	// preflight. The lookup substitutes only its requested method, so a
	// Method(`POST`) route can select its CORS handler before an upstream runs.
	// Routes without this flag retain ordinary OPTIONS matching exactly.
	CORSPreflight bool
	Handler       http.Handler
}

type Router struct {
	byLimen map[string]*routeIndex
	any     *routeIndex
}

type routeIndex struct {
	exactHosts map[string]*hostIndex
	hostless   *hostIndex
	fallback   []*compiledRoute
}

type hostIndex struct {
	paths    *pathRadixTree
	fallback []*compiledRoute
}

type compiledRoute struct {
	route      Route
	matcher    *rules.Matcher
	hints      rules.IndexHints
	order      int
	hostScoped bool
}

// New compiles the route expressions and builds the immutable candidate
// indexes. Configuration validation should normally catch expression errors;
// returning the error here keeps programmatic construction safe as well.
func New(routes []Route) (*Router, error) {
	return NewWithRegistry(routes, rules.DefaultRegistry())
}

// NewWithRegistry is the extension point for source-controlled custom rules.
// A registry is immutable by convention after the router is built.
func NewWithRegistry(routes []Route, registry *rules.Registry) (*Router, error) {
	rt := &Router{byLimen: make(map[string]*routeIndex), any: newRouteIndex()}
	if registry == nil {
		registry = rules.DefaultRegistry()
	}
	for order, route := range routes {
		if route.Handler == nil {
			return nil, fmt.Errorf("route %q has a nil handler", route.Name)
		}
		expression := route.Match
		if expression == "" {
			expression = legacyExpression(route)
		}
		matcher, err := registry.Compile(expression)
		if err != nil {
			return nil, fmt.Errorf("route %q: %w", route.Name, err)
		}
		hints := matcher.IndexHints()
		compiled := &compiledRoute{route: route, matcher: matcher, hints: hints, order: order, hostScoped: hints.Host != ""}
		index := rt.any
		if route.Limen != "" {
			index = rt.byLimen[route.Limen]
			if index == nil {
				index = newRouteIndex()
				rt.byLimen[route.Limen] = index
			}
		}
		rt.insert(index, compiled)
	}
	return rt, nil
}

func newRouteIndex() *routeIndex {
	return &routeIndex{exactHosts: make(map[string]*hostIndex), hostless: newHostIndex()}
}

func newHostIndex() *hostIndex { return &hostIndex{paths: newPathRadixTree()} }

func (rt *Router) insert(index *routeIndex, route *compiledRoute) {
	if !route.hints.Safe || (route.hints.Host == "" && route.hints.PathPrefix == "") {
		index.fallback = append(index.fallback, route)
		return
	}
	bucket := index.hostless
	if route.hints.Host != "" {
		bucket = index.exactHosts[route.hints.Host]
		if bucket == nil {
			bucket = newHostIndex()
			index.exactHosts[route.hints.Host] = bucket
		}
	}
	if route.hints.PathPrefix == "" {
		bucket.fallback = append(bucket.fallback, route)
		return
	}
	bucket.paths.insert(route.hints.PathPrefix, route)
}

func legacyExpression(route Route) string {
	parts := make([]string, 0, 2)
	if route.Host != "" {
		parts = append(parts, "Host(`"+route.Host+"`)")
	}
	path := route.PathPrefix
	if path == "" {
		path = "/"
	}
	parts = append(parts, "PathPrefix(`"+path+"`)")
	return strings.Join(parts, " && ")
}

// ServeHTTP normalizes request facts once, obtains candidates from the Limen,
// Host and path indexes, then selects the highest-priority complete match.
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	facts := requestFacts(r)
	best := rt.match(protocol.LimenID(r), facts, false)
	if isCORSPreflight(r) {
		preflightFacts := *facts
		preflightFacts.Method = r.Header.Get("Access-Control-Request-Method")
		if preflight := rt.match(protocol.LimenID(r), &preflightFacts, true); preflight != nil && (best == nil || routePrecedes(preflight, best)) {
			best = preflight
		}
	}
	if best == nil {
		telemetry.MarkError(r.Context(), "route_not_found")
		http.NotFound(w, r)
		return
	}
	best.route.Handler.ServeHTTP(w, r)
}

func (rt *Router) match(limen string, facts *rules.Facts, corsPreflightOnly bool) *compiledRoute {
	var best *compiledRoute
	for _, index := range rt.indexesFor(limen) {
		for _, candidate := range index.candidates(facts.Host, facts.Path) {
			best = chooseCandidate(candidate, facts, best, corsPreflightOnly)
		}
	}
	return best
}

func chooseCandidate(candidate *compiledRoute, facts *rules.Facts, best *compiledRoute, corsPreflightOnly bool) *compiledRoute {
	if corsPreflightOnly && !candidate.route.CORSPreflight {
		return best
	}
	if !candidate.matcher.Match(facts) {
		return best
	}
	if best == nil || routePrecedes(candidate, best) {
		best = candidate
	}
	return best
}

func isCORSPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions && r.Header.Get("Origin") != "" && r.Header.Get("Access-Control-Request-Method") != ""
}

func (rt *Router) indexesFor(limen string) []*routeIndex {
	result := make([]*routeIndex, 0, 2)
	if index := rt.byLimen[limen]; index != nil {
		result = append(result, index)
	}
	result = append(result, rt.any)
	return result
}

func (index *routeIndex) candidates(host, path string) []*compiledRoute {
	result := append([]*compiledRoute(nil), index.fallback...)
	if bucket := index.exactHosts[host]; bucket != nil {
		result = append(result, bucket.fallback...)
		result = bucket.paths.collect(path, result)
	}
	result = append(result, index.hostless.fallback...)
	result = index.hostless.paths.collect(path, result)
	return result
}

func routePrecedes(a, b *compiledRoute) bool {
	if a.route.Priority != b.route.Priority {
		return a.route.Priority > b.route.Priority
	}
	if a.hostScoped != b.hostScoped {
		return a.hostScoped
	}
	if len(a.hints.PathPrefix) != len(b.hints.PathPrefix) {
		return len(a.hints.PathPrefix) > len(b.hints.PathPrefix)
	}
	return a.order < b.order
}

func requestFacts(r *http.Request) *rules.Facts {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	applicationProtocol := "http"
	if protocol.IsWebSocketRequest(r) {
		applicationProtocol = "websocket"
	} else if protocol.WantsSSE(r) {
		applicationProtocol = "sse"
	}
	return &rules.Facts{Host: host, Path: r.URL.Path, Method: r.Method, Protocol: applicationProtocol, Header: r.Header, Query: r.URL.Query()}
}

type pathRadixTree struct{ root *pathNode }

type pathNode struct {
	children map[string]*pathNode
	routes   []*compiledRoute
}

func newPathRadixTree() *pathRadixTree {
	return &pathRadixTree{root: &pathNode{children: make(map[string]*pathNode)}}
}

func (tree *pathRadixTree) insert(prefix string, route *compiledRoute) {
	node := tree.root
	for _, segment := range pathSegments(prefix) {
		child := node.children[segment]
		if child == nil {
			child = &pathNode{children: make(map[string]*pathNode)}
			node.children[segment] = child
		}
		node = child
	}
	node.routes = append(node.routes, route)
}

func (tree *pathRadixTree) collect(path string, result []*compiledRoute) []*compiledRoute {
	node := tree.root
	result = append(result, node.routes...)
	for _, segment := range pathSegments(path) {
		child := node.children[segment]
		if child == nil {
			break
		}
		node = child
		result = append(result, node.routes...)
	}
	return result
}

func pathSegments(path string) []string {
	if path == "/" || path == "" {
		return nil
	}
	return strings.Split(strings.TrimPrefix(path, "/"), "/")
}
