package covergate

import (
	"io/fs"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// moduleRoot is the repository, two levels above this package.
func moduleRoot() fs.FS { return os.DirFS("../..") }

// workflow reads ci.yml with its line endings made LF: a checkout with CRLF
// (the default of Git for Windows) must not change what the tests below see.
func workflow(t *testing.T, fsys fs.FS) string {
	t.Helper()
	text, err := fs.ReadFile(fsys, ".github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(text), "\r\n", "\n")
}

// gatedInWorkflow reads the packages that .github/workflows/ci.yml gates.
func gatedInWorkflow(t *testing.T, fsys fs.FS) []string {
	t.Helper()
	var lines []string
	for _, line := range strings.Split(workflow(t, fsys), "\n") {
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

// packagesWithGo returns the directories below dir that hold a Go file, as the go tool counts packages: a directory named testdata is not
// looked into (internal/publisher/manifest/testdata/chain is a program that a test builds, not a package of the module).
func packagesWithGo(t *testing.T, fsys fs.FS, dir string) []string {
	t.Helper()
	var found []string
	err := fs.WalkDir(fsys, dir, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == "testdata" {
			return fs.SkipDir
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

// gatedRoots are the directories that the gate's list names packages in: every
// package below them is gated.
var gatedRoots = []string{"internal/publisher", "internal/covergate", "cmd/covergate"}

// The gate takes a list, and the list is in the workflow. This keeps it equal to
// every package below the directories that it names, so that a package added
// under internal/publisher, internal/covergate or cmd/covergate is gated from its
// first commit (and a first commit that cannot meet the gate fails here, before
// the workflow runs), and no other package is gated by accident.
func TestWorkflowGatesTheCheckPackages(t *testing.T) {
	fsys := moduleRoot()
	var want []string
	for _, root := range gatedRoots {
		want = append(want, packagesWithGo(t, fsys, root)...)
	}
	got := gatedInWorkflow(t, fsys)
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("ci.yml gates %v; the packages below %v are %v", got, gatedRoots, want)
	}
	for _, pkg := range got {
		if !slices.ContainsFunc(gatedRoots, func(root string) bool { return pkg == root || strings.HasPrefix(pkg, root+"/") }) {
			t.Errorf("%s is gated but is below none of %v", pkg, gatedRoots)
		}
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
	text := workflow(t, moduleRoot())
	testStep := regexp.MustCompile(`run: go test [^\n]*-coverprofile="\$RUNNER_TEMP/publisher-cover\.out" ([^\n]*)`).FindStringSubmatch(text)
	if testStep == nil {
		t.Fatal("ci.yml has no go test step that writes the publisher cover profile")
	}
	if got, want := testStep[1], "./internal/publisher/... ./internal/covergate/... ./cmd/covergate/..."; got != want {
		t.Errorf("the test step runs %q, want %q", got, want)
	}
	if !strings.Contains(text, `./cmd/covergate "$RUNNER_TEMP/publisher-cover.out"`) {
		t.Error("the gate reads another profile than the test step writes")
	}
}

// The tests above give the same answers when ci.yml has CRLF line endings.
func TestWorkflowTestsIgnoreLineEndings(t *testing.T) {
	text, err := fs.ReadFile(moduleRoot(), ".github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	crlf := fstest.MapFS{".github/workflows/ci.yml": {Data: []byte(strings.ReplaceAll(strings.ReplaceAll(string(text), "\r\n", "\n"), "\n", "\r\n"))}}
	if got, want := gatedInWorkflow(t, crlf), gatedInWorkflow(t, moduleRoot()); !slices.Equal(got, want) {
		t.Errorf("with CRLF the gated packages are %q, with LF %q", got, want)
	}
	if strings.Contains(workflow(t, crlf), "\r") {
		t.Error("workflow() keeps a carriage return")
	}
	if testStep := regexp.MustCompile(`run: go test [^\n]*-coverprofile="\$RUNNER_TEMP/publisher-cover\.out" ([^\n]*)`).FindStringSubmatch(workflow(t, crlf)); testStep == nil || strings.Contains(testStep[1], "\r") {
		t.Errorf("the test step with CRLF: %q", testStep)
	}
}

// The job that checks the goldens runs both generators with --check and the test of the checkout rule, with the Node that made the goldens.
func TestWorkflowChecksTheGoldens(t *testing.T) {
	text := workflow(t, moduleRoot())
	for _, want := range []string{
		"run: node --test internal/publisher/references.test.mjs",
		"run: node internal/publisher/rules/testdata/reference/generate.mjs --check",
		"run: node internal/publisher/manifest/testdata/reference/generate.mjs --check",
	} {
		if strings.Count(text, want) != 1 {
			t.Errorf("ci.yml does not have exactly one step %q", want)
		}
	}
	raw, err := fs.ReadFile(moduleRoot(), "internal/publisher/manifest/testdata/reference/corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	recorded := regexp.MustCompile(`"node":\s*"v([0-9.]+)"`).FindSubmatch(raw)
	if recorded == nil {
		t.Fatal("the corpus does not record the Node that made it")
	}
	if want := "node-version: '" + string(recorded[1]) + "'"; strings.Count(text, want) != 1 {
		t.Errorf("ci.yml does not run the goldens job with the Node that made them (%s)", want)
	}
}

// No workflow runs on a schedule: what runs in CI runs because of a change.
func TestNoScheduledWorkflows(t *testing.T) {
	entries, err := fs.ReadDir(moduleRoot(), ".github/workflows")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no workflows")
	}
	schedule := regexp.MustCompile(`(?m)^\s*(schedule\s*:|-?\s*cron\s*:)`)
	for _, entry := range entries {
		raw, err := fs.ReadFile(moduleRoot(), ".github/workflows/"+entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if schedule.Match(raw) {
			t.Errorf("%s has a schedule", entry.Name())
		}
	}
}
