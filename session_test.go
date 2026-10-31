package main

import "testing"

func TestRateLimitKey(t *testing.T) {
	tests := map[string]string{
		"203.0.113.7":                    "203.0.113.7",
		"::ffff:203.0.113.7":             "203.0.113.7",
		"2001:db8:1:2:aaaa::1":           "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff:ffff:ffff:ff": "2001:db8:1:2::/64",
		"fe80::1%eth0":                   "fe80::/64",
		"not-an-ip":                      "not-an-ip",
	}
	for ip, want := range tests {
		if got := rateLimitKey(ip); got != want {
			t.Errorf("rateLimitKey(%q) = %q, want %q", ip, got, want)
		}
	}
}
