// Package routing defines the route boundaries shared by forwarding,
// authorization, and metrics. Paths passed here are URL-decoded paths.
package routing

import "strings"

// Prefix returns a known mount only when it ends at a path-segment boundary.
func Prefix(path string) string {
	for _, prefix := range []string{"/users", "/payments", "/admin"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return prefix
		}
	}
	return ""
}
