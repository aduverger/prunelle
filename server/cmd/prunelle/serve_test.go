package main

import "testing"

// The access model trusts loopback callers, so the server must refuse any other listen address.
func TestRequireLoopback(t *testing.T) {
	for listen, ok := range map[string]bool{
		"127.0.0.1:7390": true,
		"[::1]:7390":     true,
		"localhost:7390": true,
		":7390":          false,
		"0.0.0.0:7390":   false,
		"100.64.0.1:80":  false,
		"127.0.0.1":      false,
	} {
		if err := requireLoopback(listen); (err == nil) != ok {
			t.Errorf("requireLoopback(%q) = %v, want accepted=%v", listen, err, ok)
		}
	}
}
