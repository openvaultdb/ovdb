package cli

import "testing"

// TestInteractiveDecidesTUIVsNonInteractive is
// first-run-onboarding#REQ:bare-ovdb-launches-tui and
// REQ:bare-ovdb-non-interactive's branch: a real terminal on both stdin and
// stdout opens the TUI; anything else — one side not a tty, or
// OVDB_NON_INTERACTIVE set — takes the non-interactive path, and never waits
// for input (REQ:never-block-without-terminal).
func TestInteractiveDecidesTUIVsNonInteractive(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		isTerminal  bool
		nonInteract string
		want        bool
	}{
		{"both a terminal", true, "", true},
		{"not a terminal", false, "", false},
		{"terminal but OVDB_NON_INTERACTIVE set", true, "1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			vars := map[string]string{EnvNonInteractive: tc.nonInteract}
			app := &App{
				Getenv:     func(key string) string { return vars[key] },
				IsTerminal: func(uintptr) bool { return tc.isTerminal },
			}
			if got := app.interactive(); got != tc.want {
				t.Errorf("interactive() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTermSizeFallsBackToDefault(t *testing.T) {
	t.Parallel()
	app := &App{TermSize: func() (int, int) { return 0, 0 }}
	w, h := app.termSize()
	if w != 0 || h != 0 {
		t.Errorf("termSize() = %d,%d, want the injected fake's values", w, h)
	}

	app2 := &App{}
	w2, h2 := app2.termSize()
	if w2 <= 0 || h2 <= 0 {
		t.Errorf("termSize() with no fake = %d,%d, want a positive fallback size", w2, h2)
	}
}
