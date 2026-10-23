package covergate

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"testing/iotest"
)

const module = "example.test/m"

func file(src string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(src)} }

// tree is a module with one package for each way the gate judges one.
func tree() fstest.MapFS {
	return fstest.MapFS{
		"go.mod":                      file("module example.test/m\n\ngo 1.27.0\n"),
		"good/good.go":                file("package good\n\nfunc Add(a, b int) int { return a + b }\n"),
		"good/good_test.go":           file("package good\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { _ = Add(1, 2) }\n"),
		"good/sub/sub.go":             file("package sub\n\nfunc F() int { return 1 }\n"),
		"good/notes.txt":              file("not Go"),
		"other/other.go":              file("package other\n\nfunc F() int { return 1 }\n"),
		"empty/doc.go":                file("// Package empty has no statement.\npackage empty\n\ntype T struct{}\n\nfunc (T) M() {}\n"),
		"tagged/tagged.go":            file("//go:build linux\n\npackage tagged\n\nfunc F() int { return 1 }\n"),
		"tagged/old.go":               file("// +build linux\n\npackage tagged\n\nfunc G() int { return 1 }\n"),
		"osfile/osfile_linux.go":      file("package osfile\n\nfunc F() int { return 1 }\n"),
		"osfile/osfile_amd64_test.go": file("package osfile\n"),
		"plain/linux.go":              file("package plain\n\nfunc F() int { return 1 }\n"),
		"plain/word_test.go":          file("package plain\n"),
		"testmain/t.go":               file("package testmain\n\nfunc F() int { return 1 }\n"),
		"testmain/main_test.go":       file("package testmain\n\nimport \"testing\"\n\nfunc TestMain(m *testing.M) { m.Run() }\n"),
		"method/m.go":                 file("package method\n\nfunc F() int { return 1 }\n"),
		"method/m_test.go":            file("package method\n\ntype S struct{}\n\nfunc (S) TestMain() {}\n"),
		"plus1/p.go":                  file("//+build windows\n\npackage plus1\n\nfunc F() int { return 1 }\n"),
		"plus2/p.go":                  file("//  +build windows\n\npackage plus2\n\nfunc F() int { return 1 }\n"),
		"plus3/p.go":                  file("//\t+build windows\n\npackage plus3\n\nfunc F() int { return 1 }\n"),
		"plus4/p.go":                  file("// Copyright.\n\n//   +build !linux\n\npackage plus4\n\nfunc F() int { return 1 }\n"),
		"gobuild/p.go":                file("  //go:build ignore\n\npackage gobuild\n\nfunc F() int { return 1 }\n"),
		"cgo/p.go":                    file("package cgo\n\nimport \"C\"\n\nfunc F() int { return 1 }\n"),
		"cgo/other.go":                file("package cgo\n\nimport (\n\t\"fmt\"\n\t\"C\"\n)\n\nfunc G() { fmt.Println() }\n"),
		"afterclause/p.go":            file("package afterclause\n\n//go:build windows\n\nfunc F() int { return 1 }\n"),
		"commentbody/p.go":            file("package commentbody\n\nvar text = `\n//+build windows\n`\n\nfunc F() int { return len(text) }\n"),
		"partial/a.go":                file("package partial\n\nfunc A() int { return 1 }\n"),
		"partial/b.go":                file("package partial\n\nfunc B() int { return 2 }\n"),
		"partial/c.go":                file("package partial\n\ntype T struct{}\n"),
		"linedir/a.go":                file("package linedir\n\nfunc A() int {\n\treturn 1\n}\n"),
		"linedir/b.go":                file("package linedir\n\n//line a.go:3\nfunc B() int { return 2 }\n"),
		"lineother/p.go":              file("package lineother\n\n//line sub/other.go:1:1\nfunc F() int { return 1 }\n"),
		"lineblock/p.go":              file("package lineblock\n\nfunc F() int { /*line x.go:10*/ return 1 }\n\n/*line y.go:1*/ func G() {}\n"),
		"linetest/p.go":               file("package linetest\n\nfunc F() int { return 1 }\n"),
		"linetest/p_test.go":          file("package linetest\n\n//line p.go:3\nfunc helper() {}\n"),
		"linestring/p.go":             file("package linestring\n\nvar text = `\n//line a.go:1\n/*line b.go:1*/\n`\n\nfunc F() int { return len(text) }\n"),
		"linecomment/p.go":            file("package linecomment\n\n// the line a.go:1 is not a directive\n//lineage is not one either\n//line\nfunc F() int { return 1 }\n"),
		"linetab/p.go":                file("package linetab\n\n//line\tx.go:1\nfunc F() int { return 1 }\n\nfunc G() int { /*line\ty.go:7*/ return 2 }\n"),
		"bom/p.go":                    file("\xef\xbb\xbf//go:build windows\n\npackage bom\n\nconst C = 1\n"),
		"hidden/h.go":                 file("package hidden\n\nimport \"example.test/m/hidden/testdata/inner\"\n\nfunc F(x int) int { return inner.G(x) }\n"),
		"hidden/testdata/inner/i.go":  file("package inner\n\nfunc G(x int) int {\n\tif x > 0 {\n\t\treturn x\n\t}\n\treturn -x\n}\n"),
		"seen/s.go":                   file("package seen\n\nfunc F() int { return 1 }\n"),
		"seen/s_test.go":              file("package seen\n\nimport (\n\t\"testing\"\n\n\t\"example.test/m/hidden/testdata/inner\"\n)\n\nfunc TestG(t *testing.T) { _ = inner.G(1) }\n"),
		"bad/bad.go":                  file("this is not Go"),
		"nogo/readme.txt":             file("nothing"),
	}
}

func TestParse(t *testing.T) {
	profile := "mode: atomic\n" +
		module + "/good/good.go:3.30,3.50 1 1\n" +
		module + "/good/good.go:5.1,6.2 2 0\n" +
		"\n" +
		module + "/good/good.go:5.1,6.2 2 3\n" + // the same block from a second test binary
		module + "/other/other.go:3.1,3.9 4 0\n"
	blocks, err := Parse(strings.NewReader(profile))
	if err != nil {
		t.Fatal(err)
	}
	want := []Block{
		{module + "/good/good.go:3.30,3.50", 1, true},
		{module + "/good/good.go:5.1,6.2", 2, true},
		{module + "/other/other.go:3.1,3.9", 4, false},
	}
	if len(blocks) != len(want) {
		t.Fatalf("blocks = %+v", blocks)
	}
	for i := range want {
		if blocks[i] != want[i] {
			t.Errorf("block %d = %+v, want %+v", i, blocks[i], want[i])
		}
	}
	if got := blocks[0].Dir(); got != module+"/good" {
		t.Errorf("Dir = %q", got)
	}
	// Without a mode line, and with only blocks.
	if blocks, err := Parse(strings.NewReader("a/b.go:1.1,2.2 1 0\n")); err != nil || len(blocks) != 1 {
		t.Errorf("Parse without mode = %v, %v", blocks, err)
	}
}

func TestParseErrors(t *testing.T) {
	for name, profile := range map[string]string{
		"too few fields":          "a/b.go:1.1,2.2 1\n",
		"too many fields":         "a/b.go:1.1,2.2 1 0 9\n",
		"no file separator":       "ab 1 0\n",
		"statements not a number": "a/b.go:1.1,2.2 x 0\n",
		"negative statements":     "a/b.go:1.1,2.2 -1 0\n",
		"count not a number":      "a/b.go:1.1,2.2 1 x\n",
		"negative count":          "a/b.go:1.1,2.2 1 -1\n",
		"one location, two sizes": "a/b.go:1.1,2.2 1 0\na/b.go:1.1,2.2 2 1\n",
		"a line over the limit":   strings.Repeat("a", 2<<20),
	} {
		if _, err := Parse(strings.NewReader(profile)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := Parse(iotest.ErrReader(errors.New("disk"))); err == nil || !strings.Contains(err.Error(), "disk") {
		t.Errorf("a failing reader gives %v", err)
	}
}

// A //line or /*line*/ directive is refused wherever it is (a test file too), by
// the scanner's comments, followed by a space or a tab (Go honours only the
// space; the gate is stricter on purpose, and linetab is the row for the tab).
// The same text in a raw string, or a comment that only starts like one, is not.
func TestLineDirectives(t *testing.T) {
	for _, c := range []struct {
		dir   string
		lines []int
	}{
		{"linedir", []int{3}}, {"lineother", []int{3}}, {"lineblock", []int{3, 5}}, {"linetest", []int{3}},
		{"linetab", []int{3, 6}}, {"linestring", nil}, {"linecomment", nil}, {"good", nil},
	} {
		pkg, err := LoadPackage(tree(), module, c.dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(pkg.Directives) != len(c.lines) {
			t.Errorf("%s: directives %q, want lines %v", c.dir, pkg.Directives, c.lines)
			continue
		}
		for i, line := range c.lines {
			if !strings.Contains(pkg.Directives[i], ":"+strconv.Itoa(line)+": a line directive") || !strings.Contains(pkg.Directives[i], c.dir+"/") {
				t.Errorf("%s: %q does not name the file and line %d", c.dir, pkg.Directives[i], line)
			}
		}
	}
	// A BOM before the constraint does not hide it from the header rule (a file
	// of declarations only has no statement for the file-set rule to find).
	pkg, err := LoadPackage(tree(), module, "bom")
	if err != nil || pkg.HasStatements || len(pkg.Constraints) != 1 || !strings.Contains(pkg.Constraints[0], "//go:build windows") {
		t.Errorf("bom = %+v, %v", pkg, err)
	}
}

// The reproduction of the review: a block that never ran, placed after a //line
// directive that names a covered file at the position of one of its covered
// blocks, shares the location of that block in the profile and, merged as "hit
// if any listing was", would count as covered. Parse still merges listings of one
// block (that is what -coverpkg makes); the directive is what the gate refuses.
func TestLineDirectiveCannotHideAnUnrunBlock(t *testing.T) {
	profile := "mode: atomic\n" +
		module + "/linedir/a.go:3.16,5.2 1 1\n" + // a.go, covered
		module + "/linedir/a.go:3.16,5.2 1 0\n" // b.go's block, renamed to the same location by its directive
	blocks, err := Parse(strings.NewReader(profile))
	if err != nil || len(blocks) != 1 || !blocks[0].Hit {
		t.Fatalf("the merged profile = %+v, %v", blocks, err)
	}
	code, stdout, stderr := run(t, profile, tree(), "cover.out", "./linedir")
	if code != 1 || !strings.Contains(stdout, "1 of 1") || !strings.Contains(stderr, "linedir/b.go:3: a line directive") {
		t.Errorf("Run = %d %q %q: the gate must refuse the directive although every statement counts as covered", code, stdout, stderr)
	}
	if code, _, _ := run(t, profile, tree(), "cover.out", "./lineother"); code != 1 {
		t.Errorf("a directive naming another file: exit %d", code)
	}
}

func TestNameConstraint(t *testing.T) {
	for name, want := range map[string]string{
		"x_linux.go": "linux", "x_linux_test.go": "linux", "x_amd64.go": "amd64", "x_linux_arm64.go": "arm64", "x_windows_test.go": "windows", "x_wasip1.go": "wasip1",
		"linux.go": "", "amd64_test.go": "", "x.go": "", "x_test.go": "", "x_foo.go": "", "x_linuxish.go": "", "x_linux_foo.go": "",
	} {
		if got := nameConstraint(name); got != want {
			t.Errorf("nameConstraint(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestLoadPackage(t *testing.T) {
	fsys := tree()
	for _, c := range []struct {
		dir                    string
		statements             bool
		testMains, constraints int
	}{
		{"good", true, 0, 0}, {"empty", false, 0, 0}, {"tagged", true, 0, 2},
		// every spelling of a constraint that Go reads, as the header of a file
		{"plus1", true, 0, 1}, {"plus2", true, 0, 1}, {"plus3", true, 0, 1}, {"plus4", true, 0, 1}, {"gobuild", true, 0, 1},
		// import "C", alone or in a group; and text that Go does not read as a constraint
		{"cgo", true, 0, 2}, {"afterclause", true, 0, 0}, {"commentbody", true, 0, 0}, {"osfile", true, 0, 2}, {"plain", true, 0, 0}, {"testmain", true, 1, 0}, {"method", true, 0, 0},
	} {
		pkg, err := LoadPackage(fsys, module, c.dir)
		if err != nil {
			t.Fatalf("%s: %v", c.dir, err)
		}
		if pkg.Path != module+"/"+c.dir || pkg.Dir != c.dir || pkg.HasStatements != c.statements || len(pkg.TestMains) != c.testMains || len(pkg.Constraints) != c.constraints {
			t.Errorf("%s = %+v", c.dir, pkg)
		}
	}
	if pkg, err := LoadPackage(fstest.MapFS{"a.go": file("package main\n\nfunc main() {}\n")}, module, "."); err != nil || pkg.Path != module || pkg.HasStatements {
		t.Errorf("the module root = %+v, %v", pkg, err)
	}
	for _, dir := range []string{"bad", "nogo", "missing"} {
		if _, err := LoadPackage(fsys, module, dir); err == nil {
			t.Errorf("%s: no error", dir)
		}
	}
	if _, err := LoadPackage(failingFS{fsys, "good/good.go"}, module, "good"); err == nil {
		t.Error("an unreadable file gives no error")
	}
}

// failingFS cannot open one file.
type failingFS struct {
	fs.FS
	name string
}

func (f failingFS) Open(name string) (fs.File, error) {
	if name == f.name {
		return nil, errors.New("unreadable")
	}
	return f.FS.Open(name)
}

func TestCheck(t *testing.T) {
	good := Package{Dir: "good", Path: module + "/good", HasStatements: true}
	covered := []Block{{module + "/good/good.go:1.1,2.2", 3, true}, {module + "/good/good.go:4.1,5.2", 0, false}}
	if r := Check([]Package{good}, covered); !r.OK() || r.Covered != 3 || r.Total != 3 {
		t.Errorf("covered = %+v", r)
	}
	// Blocks of packages that are not given are not judged.
	if r := Check([]Package{good}, append(slicesClone(covered), Block{module + "/other/o.go:1.1,2.2", 9, false})); !r.OK() || r.Total != 3 {
		t.Errorf("an unlisted package is judged: %+v", r)
	}
	r := Check([]Package{good}, []Block{{module + "/good/good.go:1.1,2.2", 3, true}, {module + "/good/good.go:7.1,8.2", 2, false}})
	if r.OK() || r.Covered != 3 || r.Total != 5 || len(r.Problems) != 1 || !strings.Contains(r.Problems[0], "uncovered: "+module+"/good/good.go:7.1,8.2 (2 statements)") {
		t.Errorf("uncovered = %+v", r)
	}
	r = Check([]Package{good, {Dir: "other", Path: module + "/other", HasStatements: true}}, covered)
	if r.OK() || len(r.Problems) != 1 || !strings.Contains(r.Problems[0], "package "+module+"/other has statements and none of them is in the cover profile") {
		t.Errorf("a missing package = %+v", r)
	}
	r = Check([]Package{{Dir: "e", Path: module + "/e"}}, nil)
	if r.OK() || len(r.Problems) != 1 || !strings.Contains(r.Problems[0], "nothing was measured") {
		t.Errorf("an empty profile = %+v", r)
	}
	r = Check([]Package{{Dir: "good", Path: module + "/good", HasStatements: true, TestMains: []string{"good/main_test.go"}, Constraints: []string{"good/x_linux.go: the file name carries a GOOS or GOARCH build constraint (linux)"}}}, covered)
	if r.OK() || len(r.Problems) != 2 || !strings.Contains(strings.Join(r.Problems, "\n"), "declares TestMain") || !strings.Contains(strings.Join(r.Problems, "\n"), "build constraint") {
		t.Errorf("TestMain and constraints = %+v", r)
	}
}

// A file of a gated package that has a statement but no block in the profile was
// left out by the build, for whatever reason; the gate names it. A file without a
// statement, and a package that is missing altogether (reported once, as a
// package), do not add to it.
func TestFileSetRule(t *testing.T) {
	pkg, err := LoadPackage(tree(), module, "partial")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{module + "/partial/a.go", module + "/partial/b.go"}; len(pkg.Files) != 2 || pkg.Files[0] != want[0] || pkg.Files[1] != want[1] {
		t.Fatalf("Files = %v, want %v", pkg.Files, want)
	}
	only := []Block{{module + "/partial/a.go:3.16,3.28", 1, true}}
	r := Check([]Package{pkg}, only)
	if r.OK() || len(r.Problems) != 1 || !strings.Contains(r.Problems[0], "file "+module+"/partial/b.go has statements and none of them is in the cover profile") {
		t.Errorf("a file left out = %+v", r)
	}
	both := append(slicesClone(only), Block{module + "/partial/b.go:3.16,3.28", 1, true})
	if r := Check([]Package{pkg}, both); !r.OK() {
		t.Errorf("every file counted = %+v", r)
	}
	if r := Check([]Package{pkg}, nil); len(r.Problems) != 2 || strings.Contains(strings.Join(r.Problems, "\n"), "file "+module) {
		t.Errorf("a package missing altogether is reported as a package only: %+v", r)
	}
	code, _, stderr := run(t, "mode: set\n"+module+"/partial/a.go:3.16,3.28 1 1\n", tree(), "cover.out", "./partial")
	if code != 1 || !strings.Contains(stderr, module+"/partial/b.go") {
		t.Errorf("Run: %d %q", code, stderr)
	}
}

func slicesClone(blocks []Block) []Block { return append([]Block(nil), blocks...) }

type closer struct{ io.Reader }

func (closer) Close() error { return nil }

func opener(files map[string]string) func(string) (io.ReadCloser, error) {
	return func(name string) (io.ReadCloser, error) {
		text, ok := files[name]
		if !ok {
			return nil, errors.New("no such file: " + name)
		}
		return closer{strings.NewReader(text)}, nil
	}
}

func run(t *testing.T, profile string, fsys fs.FS, args ...string) (int, string, string) {
	t.Helper()
	return runWith(t, profile, fsys, nil, args...)
}

func runWith(t *testing.T, profile string, fsys fs.FS, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(args, &stdout, &stderr, opener(map[string]string{"cover.out": profile}), fsys, func(name string) (string, error) { return env[name], nil })
	return code, stdout.String(), stderr.String()
}

const goodProfile = "mode: atomic\n" + module + "/good/good.go:3.30,3.50 1 1\n"

func TestRun(t *testing.T) {
	code, stdout, stderr := run(t, goodProfile, tree(), "cover.out", "./good")
	if code != 0 || stdout != "statements covered: 1 of 1, in 1 packages\n" || stderr != "" {
		t.Errorf("good: %d %q %q", code, stdout, stderr)
	}
	// Import path, directory and the module root name the same packages.
	if code, _, _ := run(t, goodProfile, tree(), "cover.out", module+"/good"); code != 0 {
		t.Errorf("an import path: %d", code)
	}
	if code, _, _ := run(t, goodProfile, tree(), "cover.out", "good"); code != 0 {
		t.Errorf("a bare directory: %d", code)
	}
	root := fstest.MapFS{"go.mod": file("module example.test/m\n"), "m.go": file("package m\n\nfunc F() int { return 1 }\n")}
	if code, _, _ := run(t, "mode: set\n"+module+"/m.go:3.1,3.9 1 1\n", root, "cover.out", module); code != 0 {
		t.Errorf("the module root: %d", code)
	}
	if code, _, _ := run(t, "mode: set\n"+module+"/m.go:3.1,3.9 1 1\n", root, "cover.out", "."); code != 0 {
		t.Errorf("a dot: %d", code)
	}

	code, stdout, stderr = run(t, goodProfile+module+"/good/good.go:9.1,9.9 2 0\n", tree(), "cover.out", "./good")
	if code != 1 || !strings.Contains(stdout, "1 of 3") || !strings.Contains(stderr, "uncovered:") || !strings.Contains(stderr, "1 problem(s)") {
		t.Errorf("uncovered: %d %q %q", code, stdout, stderr)
	}
	code, _, stderr = run(t, goodProfile, tree(), "cover.out", "./good", "./other")
	if code != 1 || !strings.Contains(stderr, "package "+module+"/other has statements") {
		t.Errorf("a package missing from the profile: %d %q", code, stderr)
	}
	code, _, stderr = run(t, goodProfile, tree(), "cover.out", "./good", "./testmain", "./tagged")
	if code != 1 || !strings.Contains(stderr, "declares TestMain") || !strings.Contains(stderr, "//go:build linux") {
		t.Errorf("TestMain and constraints: %d %q", code, stderr)
	}

	for name, c := range map[string]struct {
		profile string
		fsys    fs.FS
		args    []string
	}{
		"no arguments":               {goodProfile, tree(), nil},
		"no package":                 {goodProfile, tree(), []string{"cover.out"}},
		"a missing profile":          {goodProfile, tree(), []string{"nope.out", "./good"}},
		"a bad profile":              {"garbage\n", tree(), []string{"cover.out", "./good"}},
		"no go.mod":                  {goodProfile, fstest.MapFS{}, []string{"cover.out", "./good"}},
		"no module line":             {goodProfile, fstest.MapFS{"go.mod": file("go 1.27\n")}, []string{"cover.out", "./good"}},
		"a pattern":                  {goodProfile, tree(), []string{"cover.out", "./good/..."}},
		"outside":                    {goodProfile, tree(), []string{"cover.out", "../x"}},
		"an absolute path":           {goodProfile, tree(), []string{"cover.out", "/good"}},
		"a backslash":                {goodProfile, tree(), []string{"cover.out", `good\sub`}},
		"a trailing slash":           {goodProfile, tree(), []string{"cover.out", "good/"}},
		"given twice":                {goodProfile, tree(), []string{"cover.out", "./good", "good"}},
		"a missing package":          {goodProfile, tree(), []string{"cover.out", "./missing"}},
		"a directory without Go":     {goodProfile, tree(), []string{"cover.out", "./nogo"}},
		"a file that does not parse": {goodProfile, tree(), []string{"cover.out", "./bad"}},
	} {
		if code, _, stderr := run(t, c.profile, c.fsys, c.args...); code != 2 || stderr == "" {
			t.Errorf("%s: exit %d, %q", name, code, stderr)
		}
	}
}

func TestPackageDir(t *testing.T) {
	for arg, want := range map[string]string{"./a/b": "a/b", "a/b": "a/b", module + "/a/b": "a/b", module: ".", "./" + module + "/a": "a", ".": ".", "./.": "."} {
		if got, err := packageDir(arg, module); err != nil || got != want {
			t.Errorf("packageDir(%q) = %q, %v, want %q", arg, got, err, want)
		}
	}
	for _, arg := range []string{"", "./", "..", "../a", "a/../b", "a//b", "a/", "/a", "a/...", "./...", `a\b`, "a/."} {
		if got, err := packageDir(arg, module); err == nil {
			t.Errorf("packageDir(%q) = %q, want an error", arg, got)
		}
	}
}

// The reproduction of the review of slice 3a: a function with an untested branch in a testdata directory, called from a gated file, is in no list
// that the gate reads (the go tool does not list a testdata directory as a package), so with every statement of the gated package covered the gate
// passed. A non-test file of a gated package may not import a testdata path; a test file may.
func TestTestdataImportCannotHideAnUntestedBranch(t *testing.T) {
	profile := "mode: set\n" + module + "/hidden/h.go:5.23,5.43 1 1\n"
	code, stdout, stderr := run(t, profile, tree(), "cover.out", "./hidden")
	if code != 1 || !strings.Contains(stdout, "1 of 1") || !strings.Contains(stderr, "hidden/h.go imports example.test/m/hidden/testdata/inner") {
		t.Errorf("Run = %d %q %q: every statement counts as covered, and the gate must refuse the import", code, stdout, stderr)
	}
	if code, _, stderr := run(t, "mode: set\n"+module+"/seen/s.go:3.16,3.26 1 1\n", tree(), "cover.out", "./seen"); code != 0 {
		t.Errorf("a test file imports a testdata path, which is not counted: %d %q", code, stderr)
	}
	pkg, err := LoadPackage(tree(), module, "hidden")
	if err != nil || len(pkg.Imports) != 1 || pkg.Imports[0].Path != module+"/hidden/testdata/inner" {
		t.Errorf("LoadPackage = %+v, %v", pkg, err)
	}
}

// The next bypass of the review of slice 3b-1: the same function in an ordinary package of the module that is not in the list. A gated
// package may import, from the module, only gated packages.
func TestAGatedPackageMayImportOnlyGatedPackages(t *testing.T) {
	fsys := tree()
	fsys["ungated/u.go"] = &fstest.MapFile{Data: []byte("package ungated\n\nfunc U(b bool) int {\n\tif b {\n\t\treturn 1\n\t}\n\treturn 0\n}\n")}
	fsys["gate/g.go"] = &fstest.MapFile{Data: []byte("package gate\n\nimport \"" + module + "/ungated\"\n\nfunc G() int { return ungated.U(false) }\n")}
	fsys["gate/h.go"] = &fstest.MapFile{Data: []byte("package gate\n\nimport \"" + module + "/seen\"\n\nfunc H() int { return seen.S() }\n")}
	profile := "mode: set\n" + module + "/gate/g.go:5.16,5.40 1 1\n" + module + "/gate/h.go:5.16,5.40 1 1\n" + module + "/seen/s.go:3.16,3.26 1 1\n"
	code, stdout, stderr := run(t, profile, fsys, "cover.out", "./gate", "./seen")
	if code != 1 || !strings.Contains(stdout, "of") || !strings.Contains(stderr, "gate/g.go imports "+module+"/ungated, a package of this module that is not one of the gated packages") || strings.Contains(stderr, "h.go imports") {
		t.Errorf("Run = %d %q %q", code, stdout, stderr)
	}
	if code, _, stderr := run(t, profile, fsys, "cover.out", "./gate", "./seen", "./ungated"); code == 0 || strings.Contains(stderr, "not one of the gated") {
		t.Errorf("with the package gated the import is allowed (it then has no profile entry): %d %q", code, stderr)
	}
}

// The third bypass, from the review of slice 3b-1: a nested module, with its own go.mod, that the module's go.mod reaches by replace (or go.work by use).
// Its package is built from a directory inside the repository that the list of packages does not name.
func TestAGatedPackageMayNotImportAModuleThatIsReplacedByADirectory(t *testing.T) {
	zz := file("package zz\n\nfunc Z(b bool) int {\n\tif b {\n\t\treturn 1\n\t}\n\treturn 0\n}\n")
	importer := file("package gate\n\nimport \"example.test/third/zz\"\n\nfunc G() int { return zz.Z(false) }\n")
	profile := "mode: set\n" + module + "/gate/g.go:5.16,5.40 1 1\n"
	for name, c := range map[string]struct {
		root  map[string]string
		wants bool
	}{
		"a replace line":                 {map[string]string{"go.mod": "module example.test/m\n\ngo 1.27.0\n\nreplace example.test/third => ./third\n"}, true},
		"a replace with a version":       {map[string]string{"go.mod": "module example.test/m\n\nreplace example.test/third v1.0.0 => ../third // c\n"}, true},
		"a replace block":                {map[string]string{"go.mod": "module example.test/m\n\nreplace (\n\t// c\n\texample.test/other => ./other\n\t\"example.test/third\" => ./third\n)\n"}, true},
		"a rooted target":                {map[string]string{"go.mod": "module example.test/m\nreplace example.test/third => /abs/third\n"}, true},
		"go.work use":                    {map[string]string{"go.mod": "module example.test/m\n", "go.work": "go 1.27\n\nuse ./third\n", "third/go.mod": "module example.test/third\n"}, true},
		"go.work use block":              {map[string]string{"go.mod": "module example.test/m\n", "go.work": "use (\n\t.\n\t./third\n)\n", "third/go.mod": "module example.test/third\n"}, true},
		"go.work replace":                {map[string]string{"go.mod": "module example.test/m\n", "go.work": "replace example.test/third => ./third\n"}, true},
		"a module replaced by a module":  {map[string]string{"go.mod": "module example.test/m\nreplace example.test/third => example.test/fork v1.0.0\n"}, false},
		"another module replaced":        {map[string]string{"go.mod": "module example.test/m\nreplace example.test/other => ./other\n"}, false},
		"a go.work use without a go.mod": {map[string]string{"go.mod": "module example.test/m\n", "go.work": "use ./third\n"}, false},
		"a go.work use outside":          {map[string]string{"go.mod": "module example.test/m\n", "go.work": "use ../third\n"}, false},
		"a replace with no target":       {map[string]string{"go.mod": "module example.test/m\nreplace example.test/third =>\n"}, false},
		"a module with the same prefix":  {map[string]string{"go.mod": "module example.test/m\nreplace example.test/thir => ./thir\n"}, false},
		"nothing":                        {map[string]string{"go.mod": "module example.test/m\n"}, false},
	} {
		fsys := fstest.MapFS{"gate/g.go": importer, "third/zz/zz.go": zz}
		for name, text := range c.root {
			fsys[name] = file(text)
		}
		code, _, stderr := run(t, profile, fsys, "cover.out", "./gate")
		if got := strings.Contains(stderr, "gate/g.go imports example.test/third/zz, a package of the module example.test/third"); got != c.wants || (code == 0) == c.wants {
			t.Errorf("%s: code %d, stderr %q, want a problem: %v", name, code, stderr, c.wants)
		}
	}
}

// A profile made under a build that is not of the module's own files is refused, and says why: GOFLAGS=-modfile=<abs>/alt.mod hid a function from the
// gate, and a vendor directory failed it only by accident (review of #39).
func TestRunRefusesABuildThatHidesWhatItMeasures(t *testing.T) {
	for _, flags := range []string{"-modfile=/abs/alt.mod", "-mod=vendor", "-mod=mod", "-overlay=/abs/o.json", "-workfile=/abs/go.work", "-count=1 -modfile=/abs/alt.mod", "--mod=vendor", "-toolexec=/abs/wrap", "-tags=hidden"} {
		code, stdout, stderr := runWith(t, goodProfile, tree(), map[string]string{"GOFLAGS": flags}, "cover.out", "./good")
		if code != 2 || stdout != "" || !strings.Contains(stderr, "GOFLAGS has "+strings.Fields(flags)[len(strings.Fields(flags))-1]) || !strings.Contains(stderr, "unset it") {
			t.Errorf("GOFLAGS=%s: %d %q %q", flags, code, stdout, stderr)
		}
	}
	for _, flags := range []string{"", "-mod=readonly", "-count=1 -race", "-modcacherw", "-modcacherw -count=2"} {
		if code, _, stderr := runWith(t, goodProfile, tree(), map[string]string{"GOFLAGS": flags}, "cover.out", "./good"); code != 0 {
			t.Errorf("GOFLAGS=%q is fine and was refused: %d %q", flags, code, stderr)
		}
	}
	withVendor := tree()
	withVendor["vendor/modules.txt"] = file("# example.test/dep v1.0.0\n")
	if code, _, stderr := run(t, goodProfile, withVendor, "cover.out", "./good"); code != 2 || !strings.Contains(stderr, "vendor directory") || !strings.Contains(stderr, "remove it") {
		t.Errorf("a vendor directory: %d %q", code, stderr)
	}
	withFile := tree()
	withFile["vendor"] = file("a file called vendor is not the go command's vendor directory")
	if code, _, stderr := run(t, goodProfile, withFile, "cover.out", "./good"); code != 0 {
		t.Errorf("a file called vendor: %d %q", code, stderr)
	}
}

// The settings are the go tool's, and a failure to ask is a refusal; GOWORK that names a workspace file is refused, "off" and empty are not.
func TestRunAsksTheGoToolForGOWORKAndFailsClosed(t *testing.T) {
	if code, _, stderr := runWith(t, goodProfile, tree(), map[string]string{"GOWORK": "/outside/go.work"}, "cover.out", "./good"); code != 2 || !strings.Contains(stderr, "workspace file /outside/go.work") || !strings.Contains(stderr, "GOWORK=off") {
		t.Errorf("a go.work: %d %q", code, stderr)
	}
	for _, work := range []string{"", "off", " off\n"} {
		if code, _, stderr := runWith(t, goodProfile, tree(), map[string]string{"GOWORK": work}, "cover.out", "./good"); code != 0 {
			t.Errorf("GOWORK=%q: %d %q", work, code, stderr)
		}
	}
	for _, failing := range []string{"GOFLAGS", "GOWORK"} {
		var stdout, stderr bytes.Buffer
		env := func(name string) (string, error) {
			if name == failing {
				return "", errors.New("no go tool")
			}
			return "", nil
		}
		code := Run([]string{"cover.out", "./good"}, &stdout, &stderr, opener(map[string]string{"cover.out": goodProfile}), tree(), env)
		if code != 2 || !strings.Contains(stderr.String(), "could not say what "+failing+" is") {
			t.Errorf("%s unknown: %d %q", failing, code, stderr.String())
		}
	}
}
