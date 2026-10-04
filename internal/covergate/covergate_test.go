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
	var stdout, stderr bytes.Buffer
	code := Run(args, &stdout, &stderr, opener(map[string]string{"cover.out": profile}), fsys)
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
