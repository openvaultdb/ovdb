package main

import "testing"

func TestServeReadOnlyFlag(t *testing.T) {
	flag := newServeCmd().Flags().Lookup("read-only")
	if flag == nil || flag.DefValue != "false" {
		t.Fatalf("--read-only flag = %#v, want boolean defaulting to false", flag)
	}
}
