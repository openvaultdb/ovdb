// Package preview is the single switch for ovdb's unreleased onboarding
// surface. Everything behind it stays callable, but is neither offered in
// help nor used as a changed default until the gate is removed.
//
// See spec/features/first-run-onboarding#REQ:preview-gate.
package preview

import "os"

// EnvVar is the environment variable that turns the preview on.
const EnvVar = "OVDB_PREVIEW"

// On reports whether OVDB_PREVIEW is exactly "1".
func On() bool { return os.Getenv(EnvVar) == "1" }
