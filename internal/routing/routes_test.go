package routing

import "testing"

func TestTableMatchesLongestSegmentSafePrefix(t *testing.T) {
	table, err := NewTable([]Route{
		{Prefix: "/api", Role: "USER"},
		{Prefix: "/api/admin", Role: "ADMIN"},
		{Prefix: "/catalog/v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, want string
		matched    bool
	}{
		{"/api", "/api", true},
		{"/api/orders", "/api", true},
		{"/api/admin", "/api/admin", true},
		{"/api/admin/users", "/api/admin", true},
		{"/apiary", "", false},
		{"/api/admin-tools", "/api", true},
		{"/catalog/v1/items", "/catalog/v1", true},
	} {
		route, ok := table.Match(tc.path)
		if ok != tc.matched || route.Prefix != tc.want {
			t.Errorf("Match(%q) = (%q, %t), want (%q, %t)", tc.path, route.Prefix, ok, tc.want, tc.matched)
		}
	}
}

func TestTableRejectsInvalidPrefixes(t *testing.T) {
	for _, prefix := range []string{"", "/", "users", "/users/", "/users//v1", "/users/../admin", "/users?debug"} {
		if _, err := NewTable([]Route{{Prefix: prefix}}); err == nil {
			t.Errorf("accepted invalid prefix %q", prefix)
		}
	}
	if _, err := NewTable([]Route{{Prefix: "/users"}, {Prefix: "/users"}}); err == nil {
		t.Error("accepted duplicate prefix")
	}
}
