// Package routing defines immutable route tables shared by forwarding,
// authorization, and metrics. Paths passed here are URL-decoded paths.
package routing

import (
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
)

// Route is a decoded mount with optional authorization metadata.
type Route struct {
	Prefix string
	Role   string
}

// Table is immutable after construction and safe for concurrent readers.
type Table struct{ routes []Route }

func NewTable(routes []Route) (*Table, error) {
	if len(routes) == 0 {
		return nil, fmt.Errorf("at least one route is required")
	}
	seen := make(map[string]struct{}, len(routes))
	table := &Table{routes: append([]Route(nil), routes...)}
	for i := range table.routes {
		prefix := table.routes[i].Prefix
		if !validPrefix(prefix) {
			return nil, fmt.Errorf("invalid route prefix %q", prefix)
		}
		if _, exists := seen[prefix]; exists {
			return nil, fmt.Errorf("duplicate route prefix %s", prefix)
		}
		seen[prefix] = struct{}{}
	}
	sort.Slice(table.routes, func(i, j int) bool { return len(table.routes[i].Prefix) > len(table.routes[j].Prefix) })
	return table, nil
}

func validPrefix(prefix string) bool {
	if prefix == "" || prefix[0] != '/' || prefix == "/" || strings.HasSuffix(prefix, "/") || strings.ContainsAny(prefix, "?#") {
		return false
	}
	for _, segment := range strings.Split(prefix[1:], "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

// Match returns the longest route whose mount ends at a segment boundary.
func (t *Table) Match(path string) (Route, bool) {
	if t == nil {
		return Route{}, false
	}
	for _, route := range t.routes {
		if path == route.Prefix || strings.HasPrefix(path, route.Prefix+"/") {
			return route, true
		}
	}
	return Route{}, false
}

func (t *Table) Routes() []Route { return append([]Route(nil), t.routes...) }

var current atomic.Value // *Table

func init() {
	table, err := NewTable([]Route{{Prefix: "/users", Role: "USER"}, {Prefix: "/payments", Role: "ADMIN"}, {Prefix: "/admin", Role: "ADMIN"}})
	if err != nil {
		panic(err)
	}
	current.Store(table)
}

// Current returns the process default table for compatibility callers. New
// request handlers should capture a specific table at construction time.
func Current() *Table { return current.Load().(*Table) }

// Replace updates the process default table atomically.
func Replace(table *Table) {
	if table != nil {
		current.Store(table)
	}
}

// Prefix preserves the original helper for callers using the default table.
func Prefix(path string) string {
	route, ok := Current().Match(path)
	if !ok {
		return ""
	}
	return route.Prefix
}
