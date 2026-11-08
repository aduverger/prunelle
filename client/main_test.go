package main

import (
	"net/http/httptest"
	"testing"
)

func TestAPITarget(t *testing.T) {
	for target, allowed := range map[string]bool{
		"http://127.0.0.1:7390/v1/events":             true,
		"http://[::1]:7390/v1/reviews":                true,
		"https://dev.example.ts.net/v1/reviews":       true,
		"http://dev.example.ts.net/v1/reviews":        false,
		"https://example.org/v1/reviews":              false,
		"https://example.ts.net.evil.test/v1/reviews": false,
		"http://127.0.0.1:7390/admin":                 false,
		"http://login@127.0.0.1:7390/v1/reviews":      false,
	} {
		if got := apiTarget(httptest.NewRequest("GET", target, nil)); got != allowed {
			t.Errorf("apiTarget(%q) = %v, want %v", target, got, allowed)
		}
	}
}
