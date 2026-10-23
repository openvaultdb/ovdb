package preview

import "testing"

func TestOn(t *testing.T) {
	for value, want := range map[string]bool{"1": true, "": false, "0": false, "true": false} {
		t.Setenv(EnvVar, value)
		if got := On(); got != want {
			t.Errorf("On() with %s=%q = %v, want %v", EnvVar, value, got, want)
		}
	}
}
