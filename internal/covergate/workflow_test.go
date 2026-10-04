package covergate

import (
	"io/fs"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// moduleRoot is the repository, two levels above this package.
func moduleRoot() fs.FS { return os.DirFS("../..") }

// gatedInWorkflow reads the packages that .github/workflows/ci.yml gates.
func gatedInWorkflow(t *testing.T, fsys fs.FS) []string {
	t.Helper()
	text, err := fs.ReadFile(fsys, ".github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(string(text), "\n") {
		if strings.Contains(line, "run: go run ./cmd/covergate ") {
			lines = append(lines, line)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("ci.yml has %d covergate steps, want 1", len(lines))
	}
	fields := strings.Fields(lines[0])
	var pkgs []string
	for _, field := range fields[slices.Index(fields, "./cmd/covergate")+2:] { // the profile comes first
		pkgs = append(pkgs, strings.TrimPrefix(field, "./"))
	}
	return pkgs
}

// packagesWithGo returns the directories below dir that hold a Go file.
func packagesWithGo(t *testing.T, fsys fs.FS, dir string) []string {
	t.Helper()
	var found []string
	err := fs.WalkDir(fsys, dir, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(p, ".go") && !slices.Contains(found, path.Dir(p)) {
			found = append(found, path.Dir(p))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// The gate takes a list, and the list is in the workflow. This keeps it equal to
// the packages the publisher check is made of, so that a package added under
// internal/publisher is gated from its first commit (and a first commit that
// cannot meet the gate fails here, before the workflow runs), and none is gated
// by accident.
func TestWorkflowGatesTheCheckPackages(t *testing.T) {
	fsys := moduleRoot()
	want := append(packagesWithGo(t, fsys, "internal/publisher"), "internal/covergate", "cmd/covergate")
	got := gatedInWorkflow(t, fsys)
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("ci.yml gates %v; the publisher check packages and the gate are %v", got, want)
	}
	if !slices.Contains(got, "internal/covergate") {
		t.Error("the gate's own package is not gated")
	}
}

// The packages that are gated are ones the gate can read: no TestMain and no
// build constraint, today, in this tree.
func TestGatedPackagesMeetTheGate(t *testing.T) {
	fsys := moduleRoot()
	mod, err := modulePath(fsys)
	if err != nil || mod != "github.com/openvaultdb/ovdb" {
		t.Fatalf("module = %q, %v", mod, err)
	}
	for _, dir := range gatedInWorkflow(t, fsys) {
		pkg, err := LoadPackage(fsys, mod, dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(pkg.TestMains) > 0 || len(pkg.Constraints) > 0 {
			t.Errorf("%s: TestMain %v, build constraints %v", dir, pkg.TestMains, pkg.Constraints)
		}
		if !pkg.HasStatements {
			t.Errorf("%s has no statements; leave it out of the list", dir)
		}
	}
}

// The step that runs the tests writes the profile that the next step reads.
func TestWorkflowRunsTheTestsOfTheSamePackages(t *testing.T) {
	text, err := fs.ReadFile(moduleRoot(), ".github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	testStep := regexp.MustCompile(`run: go test [^\n]*-coverprofile="\$RUNNER_TEMP/publisher-cover\.out" ([^\n]*)`).FindStringSubmatch(string(text))
	if testStep == nil {
		t.Fatal("ci.yml has no go test step that writes the publisher cover profile")
	}
	if got, want := testStep[1], "./internal/publisher/... ./internal/covergate/... ./cmd/covergate/..."; got != want {
		t.Errorf("the test step runs %q, want %q", got, want)
	}
	if !strings.Contains(string(text), `./cmd/covergate "$RUNNER_TEMP/publisher-cover.out"`) {
		t.Error("the gate reads another profile than the test step writes")
	}
}
