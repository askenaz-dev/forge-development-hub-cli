package portalapi

import "testing"

// Internal test (same package) so it can call the unexported deriveNamespace.
// Guards the hub-http-registry normalization rule.
func TestDeriveNamespace(t *testing.T) {
	cases := []struct{ in, want string }{
		{"design-platform", "design-platform"},
		{"dx-platform", "dx-platform"},
		{"Design_Platform", "design-platform"}, // lowercase + "_"→"-"
		{"  appsec  ", "appsec"},               // trim whitespace
		{"Team/Foo!", "teamfoo"},               // drop chars outside [a-z0-9-]
		{"--weird--", "weird"},                 // trim leading/trailing "-"
		{"", "unknown"},                        // empty fallback
		{"___", "unknown"},                     // all-separator fallback
	}
	for _, c := range cases {
		if got := deriveNamespace(c.in); got != c.want {
			t.Errorf("deriveNamespace(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
