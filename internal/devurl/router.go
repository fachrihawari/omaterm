package devurl

import (
	"net"
	"strings"
	"sync"
)

// Route is a host and the process it is served from.
type Route struct {
	Host    string `json:"host"`
	Project string `json:"project"`
	Process string `json:"process"`
	Port    int    `json:"port"`
	Running bool   `json:"running"`
	// Status is the process's, as the not-running page says it.
	Status string `json:"status,omitempty"`
}

// Router maps a host to its route. It is replaced as a whole, and safe for
// concurrent use.
type Router struct {
	mu     sync.RWMutex
	routes map[string]Route
	order  []string
}

// NewRouter returns a router with no route.
func NewRouter() *Router { return &Router{routes: map[string]Route{}} }

// Set replaces the routes. A host used twice goes to the first; the routes
// it refuses are returned.
func (r *Router) Set(routes []Route) (refused []Route) {
	m := make(map[string]Route, len(routes))
	order := make([]string, 0, len(routes))
	for _, rt := range routes {
		rt.Host = strings.ToLower(rt.Host)
		if _, taken := m[rt.Host]; taken {
			refused = append(refused, rt)
			continue
		}
		m[rt.Host] = rt
		order = append(order, rt.Host)
	}
	r.mu.Lock()
	r.routes, r.order = m, order
	r.mu.Unlock()
	return refused
}

// Lookup is the route for a Host header or a host name; a port in it is
// left out.
func (r *Router) Lookup(host string) (Route, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rt, ok := r.routes[hostOnly(host)]
	return rt, ok
}

// Routes are all the routes, in the order they were set.
func (r *Router) Routes() []Route {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Route, 0, len(r.order))
	for _, h := range r.order {
		out = append(out, r.routes[h])
	}
	return out
}

// hostOnly is host in lower case, without a port.
func hostOnly(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}
