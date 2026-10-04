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

// The job that checks the goldens is held to an exact shape, not to a list of known ways of switching it off: the workflow has these top-level keys
// and these triggers, and the job has these keys and, in this order, these steps with these keys and values (a `name` is a label and free). Any
// other change to the job, or to what could make it not run, fails the test and has to be made here too, where a reviewer sees it.
func goldensWorkflowKeys() []string { return []string{"jobs", "name", "on"} }

// goldensTriggers are the triggers of the workflow, with no filter that could skip the job: every pull request, and every push to main.
func goldensTriggers() map[string]any {
	return map[string]any{"push": map[string]any{"branches": []any{"main"}}, "pull_request": map[string]any{"branches": []any{"**"}}}
}

// goldensSteps are the steps of the job, without their names.
func goldensSteps(node string) []any {
	run := func(command string) map[string]any { return map[string]any{"run": command} }
	return []any{
		map[string]any{"uses": "actions/checkout@v7"},
		map[string]any{"uses": "actions/setup-node@v5", "with": map[string]any{"node-version": node}},
		run("node --test internal/publisher/references.test.mjs"),
		run("node internal/publisher/rules/testdata/reference/generate.mjs --check"),
		run("node internal/publisher/manifest/testdata/reference/generate.mjs --check"),
		run("node internal/publisher/repo/testdata/reference/generate.mjs --check"),
		map[string]any{"uses": "actions/setup-go@v7", "with": map[string]any{"go-version": "1.27.0", "cache": true}},
		run("OVDB_REAL_GIT=1 go test -count=1 -v -run RealGit ./internal/publisher/repo/"),
	}
}

// goldensJob is the job, as a workflow has it.
func goldensJob(node string) map[string]any {
	return map[string]any{"runs-on": "ubuntu-latest", "steps": goldensSteps(node)}
}

// withoutNames drops the `name` of the job and of each step, which label and decide nothing.
func withoutNames(job map[string]any) map[string]any {
	out := map[string]any{}
	maps.Copy(out, job)
	delete(out, "name")
	var steps []any
	for _, step := range out["steps"].([]any) {
		if fields, ok := step.(map[string]any); ok {
			clean := map[string]any{}
			maps.Copy(clean, fields)
			delete(clean, "name")
			steps = append(steps, clean)
		} else {
			steps = append(steps, step)
		}
	}
	out["steps"] = steps
	return out
}

// goldensJobProblems says how a workflow differs from the allowed shape of the job that checks the goldens.
func goldensJobProblems(doc map[string]any, node string) []string {
	var problems []string
	if keys := slices.Sorted(maps.Keys(doc)); !slices.Equal(keys, goldensWorkflowKeys()) {
		problems = append(problems, fmt.Sprintf("the workflow has the keys %v, want %v", keys, goldensWorkflowKeys()))
	}
	if got := triggers(doc); fmt.Sprint(got) != fmt.Sprint(goldensTriggers()) {
		problems = append(problems, fmt.Sprintf("the triggers are %v, want %v: a filter could make the job not run", got, goldensTriggers()))
	}
	jobs, _ := doc["jobs"].(map[string]any)
	job, _ := jobs["publisher-goldens"].(map[string]any)
	if job == nil {
		return append(problems, "there is no job publisher-goldens")
	}
	want := goldensJob(node)
	got := withoutNames(job)
	if fmt.Sprint(got["runs-on"]) != fmt.Sprint(want["runs-on"]) || len(got) != len(want) {
		problems = append(problems, fmt.Sprintf("the job has the keys %v with runs-on %v, want runs-on and steps only, on %v", slices.Sorted(maps.Keys(got)), got["runs-on"], want["runs-on"]))
	}
	gotSteps, _ := got["steps"].([]any)
	wantSteps := want["steps"].([]any)
	if len(gotSteps) != len(wantSteps) {
		problems = append(problems, fmt.Sprintf("the job has %d steps, want %d", len(gotSteps), len(wantSteps)))
	}
	for i := range min(len(gotSteps), len(wantSteps)) {
		if fmt.Sprint(gotSteps[i]) != fmt.Sprint(wantSteps[i]) { // maps print with their keys in order
			problems = append(problems, fmt.Sprintf("step %d is %v, want %v", i+1, gotSteps[i], wantSteps[i]))
		}
	}
	return problems
}

// goldensWorkflow writes the allowed workflow, with the changes that a test makes to it.
func goldensWorkflow(t *testing.T, node string, change func(doc map[string]any, job map[string]any)) []byte {
	t.Helper()
	job := goldensJob(node)
	doc := map[string]any{"name": "Go CI", "on": goldensTriggers(), "jobs": map[string]any{"publisher-goldens": job}}
	if change != nil {
		change(doc, job)
	}
	raw, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// step edits the i-th step of the job.
func step(job map[string]any, i int, edit func(map[string]any)) {
	edit(job["steps"].([]any)[i].(map[string]any))
}

// The job that checks the goldens is exactly the allowed one, with the Node that made the goldens.
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
	// The same judgment, on workflows that differ from the allowed one in one way each, among them every way that the reviews found.
	if problems := goldensJobProblems(parseWorkflow(t, goldensWorkflow(t, node, nil)), node); len(problems) > 0 {
		t.Fatalf("the allowed job is refused: %v", problems)
	}
	insert := func(i int, s map[string]any) func(map[string]any, map[string]any) {
		return func(_ map[string]any, job map[string]any) {
			steps := job["steps"].([]any)
			job["steps"] = append(append(slices.Clone(steps[:i]), s), steps[i:]...)
		}
	}
	jobKey := func(key string, value any) func(map[string]any, map[string]any) {
		return func(_ map[string]any, job map[string]any) { job[key] = value }
	}
	stepKey := func(i int, key string, value any) func(map[string]any, map[string]any) {
		return func(_ map[string]any, job map[string]any) { step(job, i, func(s map[string]any) { s[key] = value }) }
	}
	trigger := func(event string, filter string, value any) func(map[string]any, map[string]any) {
		return func(doc map[string]any, _ map[string]any) {
			if event == "" {
				doc[filter] = value
				return
			}
			on := doc["on"].(map[string]any)
			if value == nil {
				delete(on, event)
				return
			}
			on[event].(map[string]any)[filter] = value
		}
	}
	for name, change := range map[string]func(map[string]any, map[string]any){
		"if false on the job":           jobKey("if", false),
		"continue-on-error on the job":  jobKey("continue-on-error", true),
		"needs a job that is skipped":   jobKey("needs", []any{"strongo_workflow"}),
		"an empty matrix":               jobKey("strategy", map[string]any{"matrix": map[string]any{"os": []any{}}}),
		"timeout-minutes 0":             jobKey("timeout-minutes", 0),
		"a defaults.run shell":          jobKey("defaults", map[string]any{"run": map[string]any{"shell": "true {0}"}}),
		"NODE_OPTIONS in the env":       jobKey("env", map[string]any{"NODE_OPTIONS": "--require /dev/null"}),
		"a runner that does not exist":  jobKey("runs-on", "no-such-runner"),
		"another runner":                jobKey("runs-on", "windows-latest"),
		"if false on a step":            stepKey(3, "if", false),
		"continue-on-error on a step":   stepKey(2, "continue-on-error", true),
		"a no-op shell on a step":       stepKey(4, "shell", "true {0}"),
		"an env on a step":              stepKey(5, "env", map[string]any{"NODE_OPTIONS": "--version"}),
		"a working directory on a step": stepKey(5, "working-directory", "/tmp"),
		"a command with || true":        stepKey(3, "run", "node internal/publisher/rules/testdata/reference/generate.mjs --check || true"),
		"a command changed":             stepKey(2, "run", "node --test internal/publisher/references.test.mjs --test-name-pattern=none"),
		"the real-git test without -v":  stepKey(7, "run", "OVDB_REAL_GIT=1 go test -count=1 -run RealGit ./internal/publisher/repo/"),
		"another Node": func(_ map[string]any, job map[string]any) {
			step(job, 1, func(s map[string]any) { s["with"] = map[string]any{"node-version": "22.0.0"} })
		},
		"another action":                    stepKey(0, "uses", "actions/checkout@v6"),
		"a step added":                      insert(8, map[string]any{"run": "true"}),
		"a step added first":                insert(0, map[string]any{"run": "true"}),
		"a step missing":                    func(_ map[string]any, job map[string]any) { job["steps"] = job["steps"].([]any)[1:] },
		"the steps in another order":        func(_ map[string]any, job map[string]any) { s := job["steps"].([]any); s[2], s[3] = s[3], s[2] },
		"a step twice":                      insert(3, map[string]any{"run": "node --test internal/publisher/references.test.mjs"}),
		"a paths filter on push":            trigger("push", "paths", []any{"docs/**"}),
		"a paths-ignore filter on pull":     trigger("pull_request", "paths-ignore", []any{"internal/**"}),
		"branches changed on push":          trigger("push", "branches", []any{"release"}),
		"a branches filter on pull request": trigger("pull_request", "branches", []any{"main"}),
		"no pull request":                   trigger("pull_request", "", nil),
		"no push":                           trigger("push", "", nil),
		"a schedule": func(doc map[string]any, _ map[string]any) {
			doc["on"].(map[string]any)["schedule"] = []any{map[string]any{"cron": "0 0 * * *"}}
		},
		"an env on the workflow":     trigger("", "env", map[string]any{"NODE_OPTIONS": "--version"}),
		"defaults on the workflow":   trigger("", "defaults", map[string]any{"run": map[string]any{"shell": "true {0}"}}),
		"the job under another name": func(doc map[string]any, job map[string]any) { doc["jobs"] = map[string]any{"goldens": job} },
		"the job removed":            func(doc map[string]any, _ map[string]any) { doc["jobs"] = map[string]any{} },
	} {
		if len(goldensJobProblems(parseWorkflow(t, goldensWorkflow(t, node, change)), node)) == 0 {
			t.Errorf("%s: the job is accepted", name)
		}
	}
	// A name is a label: changing one changes nothing.
	named := goldensWorkflow(t, node, func(_ map[string]any, job map[string]any) {
		job["name"] = "Another title"
		step(job, 3, func(s map[string]any) { s["name"] = "Another label" })
	})
	if problems := goldensJobProblems(parseWorkflow(t, named), node); len(problems) > 0 {
		t.Errorf("names are labels: %v", problems)
	}
	// The workflow in the form a text search would pass and a parser reads as another: the job commented out.
	if problems := goldensJobProblems(parseWorkflow(t, []byte("name: x\non:\n  push: {branches: [main]}\n  pull_request: {branches: ['**']}\njobs:\n# publisher-goldens:\n#   runs-on: ubuntu-latest\n  other: {runs-on: x}\n")), node); len(problems) == 0 {
		t.Error("the job commented out is accepted")
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
