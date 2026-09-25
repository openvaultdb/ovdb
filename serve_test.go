package main

import (
	"strings"
	"testing"
)

func TestServeReadOnlyFlag(t *testing.T) {
	flag := newServeCmd().Flags().Lookup("read-only")
	if flag == nil || flag.DefValue != "false" {
		t.Fatalf("--read-only flag = %#v, want boolean defaulting to false", flag)
	}
}

func TestServePublicURLFlag(t *testing.T) {
	for _, value := range []string{"ftp://example.com", "https://user:pass@example.com", "https://example.com/path", "https://example.com?token=x"} {
		cmd := newServeCmd()
		cmd.SetArgs([]string{"--public-url", value})
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "--public-url") {
			t.Fatalf("--public-url %q: got %v", value, err)
		}
	}
}
