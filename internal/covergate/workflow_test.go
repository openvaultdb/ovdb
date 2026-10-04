package covergate

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"gopkg.in/yaml.v3"
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

// parseWorkflow reads a workflow as YAML. The tests below ask what the workflow says, not what its text has: a flow-form `on: {schedule: ...}`,
// quoted keys, and a job that is commented out are all the same to a parser and different to a regular expression.
func parseWorkflow(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("a workflow is not YAML: %v", err)
	}
	return doc
}

// triggers are the events a workflow runs on: the keys of `on` (YAML 1.1 reads the key `on` as true, yaml.v3 as the string, both are taken),
// whether it is a string, a list or a mapping.
func triggers(doc map[string]any) map[string]any {
	on, found := doc["on"]
	if !found {
		on = doc[strconv.FormatBool(true)]
	}
	out := map[string]any{}
	switch on := on.(type) {
	case string:
		out[on] = nil
	case []any:
		for _, event := range on {
			out[fmt.Sprint(event)] = nil
		}
	case map[string]any:
		maps.Copy(out, on)
	case map[any]any:
		for key, value := range on {
			out[fmt.Sprint(key)] = value
		}
	}
	return out
}

// scheduled reports whether a workflow runs on a schedule.
func scheduled(doc map[string]any) bool {
	_, yes := triggers(doc)["schedule"]
	return yes
}

// goldensJobProblems says what is wrong with the job that checks the goldens in a workflow: it must be there, run on pull requests and pushes, and
// have, once, the setup of Node with the version that made the goldens, and each of the three commands.
func goldensJobProblems(doc map[string]any, node string) []string {
	var problems []string
	for _, event := range []string{"pull_request", "push"} {
		if _, ok := triggers(doc)[event]; !ok {
			problems = append(problems, "the workflow does not run on "+event)
		}
	}
	jobs, _ := doc["jobs"].(map[string]any)
	job, _ := jobs["publisher-goldens"].(map[string]any)
	if job == nil {
		return append(problems, "there is no job publisher-goldens")
	}
	steps, _ := job["steps"].([]any)
	// A job or a step that can be switched off, or whose failure is ignored, checks nothing.
	for _, key := range []string{"if", "continue-on-error"} {
		if _, ok := job[key]; ok {
			problems = append(problems, "the job has "+key+": it must always run, and fail the workflow when a step fails")
		}
		for i, step := range steps {
			if step, _ := step.(map[string]any); step != nil {
				if _, ok := step[key]; ok {
					problems = append(problems, fmt.Sprintf("step %d of the job has %s: it must always run, and fail the workflow when it fails", i+1, key))
				}
			}
		}
	}
	count := func(key, value string) int {
		n := 0
		for _, step := range steps {
			switch step := step.(type) {
			case map[string]any:
				if key == "node-version" {
					if with, _ := step["with"].(map[string]any); with != nil && fmt.Sprint(with[key]) == value && fmt.Sprint(step["uses"]) != "" {
						n++
					}
				} else if step[key] == value {
					n++
				}
			}
		}
		return n
	}
	for _, run := range []string{
		"node --test internal/publisher/references.test.mjs",
		"node internal/publisher/rules/testdata/reference/generate.mjs --check",
		"node internal/publisher/manifest/testdata/reference/generate.mjs --check",
		"node internal/publisher/repo/testdata/reference/generate.mjs --check",
		"OVDB_REAL_GIT=1 go test -count=1 -run RealGit ./internal/publisher/repo/",
	} {
		if count("run", run) != 1 {
			problems = append(problems, fmt.Sprintf("the job does not have exactly one step that runs %q", run))
		}
	}
	if count("node-version", node) != 1 {
		problems = append(problems, "the job does not set up Node "+node+", the version that made the goldens")
	}
	return problems
}

// The job that checks the goldens runs both generators with --check and the test of the checkout rule, with the Node that made the goldens.
func TestWorkflowChecksTheGoldens(t *testing.T) {
	raw, err := fs.ReadFile(moduleRoot(), "internal/publisher/manifest/testdata/reference/corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	recorded := regexp.MustCompile(`"node":\s*"v([0-9.]+)"`).FindSubmatch(raw)
	if recorded == nil {
		t.Fatal("the corpus does not record the Node that made it")
	}
	node := string(recorded[1])
	if problems := goldensJobProblems(parseWorkflow(t, []byte(workflow(t, moduleRoot()))), node); len(problems) > 0 {
		t.Errorf("ci.yml: %v", problems)
	}
	// The same judgment, on workflows that a text search would pass.
	good := "on:\n  push: {branches: [main]}\n  pull_request:\njobs:\n  publisher-goldens:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-node@v5\n        with: {node-version: '" + node + "'}\n      - run: node --test internal/publisher/references.test.mjs\n      - run: node internal/publisher/rules/testdata/reference/generate.mjs --check\n      - run: node internal/publisher/manifest/testdata/reference/generate.mjs --check\n      - run: node internal/publisher/repo/testdata/reference/generate.mjs --check\n      - run: OVDB_REAL_GIT=1 go test -count=1 -run RealGit ./internal/publisher/repo/\n"
	if problems := goldensJobProblems(parseWorkflow(t, []byte(good)), node); len(problems) > 0 {
		t.Fatalf("a good job is refused: %v", problems)
	}
	for name, bad := range map[string]string{
		"the job commented out":        good[:strings.Index(good, "  publisher-goldens:")] + "# " + strings.ReplaceAll(strings.TrimSuffix(good[strings.Index(good, "  publisher-goldens:"):], "\n"), "\n", "\n# ") + "\n",
		"a command commented out":      strings.Replace(good, "      - run: node internal/publisher/rules", "      # - run: node internal/publisher/rules", 1),
		"a command twice":              good + "      - run: node --test internal/publisher/references.test.mjs\n",
		"another Node":                 strings.Replace(good, node, "22.0.0", 1),
		"no pull request":              strings.Replace(good, "  pull_request:\n", "", 1),
		"the job under another name":   strings.Replace(good, "publisher-goldens:", "goldens:", 1),
		"if false on the job":          strings.Replace(good, "    runs-on: ubuntu-latest\n", "    if: false\n    runs-on: ubuntu-latest\n", 1),
		"continue-on-error on the job": strings.Replace(good, "    runs-on: ubuntu-latest\n", "    continue-on-error: true\n    runs-on: ubuntu-latest\n", 1),
		"if false on a step":           strings.Replace(good, "      - run: node internal/publisher/rules", "      - if: false\n        run: node internal/publisher/rules", 1),
		"continue-on-error on a step":  strings.Replace(good, "      - run: node --test", "      - continue-on-error: true\n        run: node --test", 1),
	} {
		if len(goldensJobProblems(parseWorkflow(t, []byte(bad)), node)) == 0 {
			t.Errorf("%s: the job is accepted", name)
		}
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
	for _, entry := range entries {
		raw, err := fs.ReadFile(moduleRoot(), ".github/workflows/"+entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if scheduled(parseWorkflow(t, raw)) {
			t.Errorf("%s has a schedule", entry.Name())
		}
	}
	// The forms that a search of the text for `schedule:` does not see.
	for name, text := range map[string]string{
		"a block mapping":      "on:\n  schedule:\n    - cron: '0 0 * * *'\n",
		"a flow mapping":       "on: {push: {branches: [main]}, schedule: [{cron: '0 0 * * *'}]}\n",
		"quoted keys":          "'on':\n  \"schedule\":\n    - 'cron': '0 0 * * *'\n",
		"the key as a boolean": "true:\n  schedule: [{cron: x}]\n",
	} {
		if got := scheduled(parseWorkflow(t, []byte(text+"jobs: {}\n"))); !got {
			t.Errorf("%s: not seen as scheduled", name)
		}
	}
	for name, text := range map[string]string{
		"a string":            "on: push\njobs: {}\n",
		"a list":              "on: [push, pull_request]\njobs: {}\n",
		"a comment":           "on:\n  push:\n# schedule:\n#   - cron: x\n",
		"a step that says it": "on: push\njobs:\n  a:\n    steps:\n      - run: echo schedule\n",
	} {
		if scheduled(parseWorkflow(t, []byte(text))) {
			t.Errorf("%s: scheduled", name)
		}
	}
}
