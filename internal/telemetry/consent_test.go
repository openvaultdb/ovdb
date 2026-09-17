package telemetry_test

import (
	"testing"

	"github.com/openvaultdb/ovdb/internal/telemetry"
)

// Review F5: CI is detected from any truthy value of the common CI
// variables, and from the presence of the ones that carry a URL, version or
// build number instead of a boolean.
func TestForcedOffCIDetection(t *testing.T) {
	for _, tc := range []struct {
		key, value, want string
	}{
		{"CI", "true", "CI"},
		{"CI", "1", "CI"},
		{"CI", "True", "CI"},
		{"CI", "yes", "CI"},
		{"CI", "0", ""},
		{"CI", "false", ""},
		{"CI", "FALSE", ""},
		{"CI", "", ""},
		{"GITHUB_ACTIONS", "1", "CI"},
		{"GITLAB_CI", "true", "CI"},
		{"BUILDKITE", "true", "CI"},
		{"CIRCLECI", "true", "CI"},
		{"TF_BUILD", "True", "CI"},
		{"TRAVIS", "true", "CI"},
		{"APPVEYOR", "True", "CI"},
		{"TF_BUILD", "false", ""},
		{"JENKINS_URL", "https://jenkins.example/", "CI"},
		{"TEAMCITY_VERSION", "2024.1", "CI"},
		{"BITBUCKET_BUILD_NUMBER", "42", "CI"},
		{"CODEBUILD_BUILD_ID", "proj:abc", "CI"},
		{"JENKINS_URL", "", ""},
		{"DO_NOT_TRACK", "1", "DO_NOT_TRACK"},
		{"DO_NOT_TRACK", "false", ""},
		{"OVDB_TELEMETRY", "0", "OVDB_TELEMETRY"},
		{"OVDB_TELEMETRY", "1", ""},
	} {
		getenv := func(key string) string {
			if key == tc.key {
				return tc.value
			}
			return ""
		}
		if got := telemetry.ForcedOff(getenv); got != tc.want {
			t.Errorf("%s=%q: forced off %q, want %q", tc.key, tc.value, got, tc.want)
		}
	}
}
