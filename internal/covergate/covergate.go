// Package covergate is a scoped, exact statement-coverage gate. Given a Go cover
// profile and a list of packages, it passes only when every statement of exactly
// those packages was executed, each of them is present in the profile, and none
// of them can be left out of a test run: no TestMain, no build constraint, no
// GOOS or GOARCH file name.
//
// It is the exact gate of github.com/modelspec-org/cli and
// github.com/meaninggraph/cli (internal/covergate, Apache-2.0), cut down to a
// list of packages, because this module cannot be gated as a whole: some of its
// packages have a TestMain, and some have files for one operating system. The
// gate has no threshold: covered statements are compared with all statements, so
// 99.99% fails, and no flag, variable or argument lowers the bar.
//
// What it reads is a file tree (an fs.FS) and a profile (an io.Reader), so the
// gate and its tests start no process.
package covergate

import (
	"bufio"
	"errors"
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Block is one statement block of a cover profile, merged across the test
// binaries that reported it: it is hit when any of them hit it.
type Block struct {
	Location   string // file.go:line.col,line.col, the file as an import path
	Statements int
	Hit        bool
}

// Dir returns the import path of the package the block belongs to.
func (b Block) Dir() string {
	file, _, _ := strings.Cut(b.Location, ":")
	return path.Dir(file)
}

// Parse reads a cover profile in any mode. A block listed more than once, as
// when several test binaries cover the same package, counts once.
func Parse(r io.Reader) ([]Block, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	merged := map[string]*Block{}
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" || (line == 1 && strings.HasPrefix(text, "mode:")) {
			continue
		}
		fields := strings.Fields(text)
		if len(fields) != 3 || !strings.Contains(fields[0], ":") {
			return nil, fmt.Errorf("line %d: expected \"file:range statements count\", got %q", line, text)
		}
		statements, err := strconv.Atoi(fields[1])
		if err != nil || statements < 0 {
			return nil, fmt.Errorf("line %d: bad statement count %q", line, fields[1])
		}
		count, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || count < 0 {
			return nil, fmt.Errorf("line %d: bad hit count %q", line, fields[2])
		}
		block, seen := merged[fields[0]]
		if !seen {
			block = &Block{Location: fields[0], Statements: statements}
			merged[fields[0]] = block
		}
		block.Hit = block.Hit || count > 0
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	blocks := make([]Block, 0, len(merged))
	for _, block := range merged {
		blocks = append(blocks, *block)
	}
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].Location < blocks[j].Location })
	return blocks, nil
}

// Package is a package of the module as the gate reads it from the file tree.
type Package struct {
	Dir           string   // relative to the module root, "." for the root
	Path          string   // import path
	HasStatements bool     // a non-test file has a block with a statement: what the cover tool counts
	Files         []string // the non-test files with a statement, as import paths: each must be in the profile
	TestMains     []string // test files that declare TestMain
	Constraints   []string // one message per build constraint, GOOS/GOARCH file name or import "C"
}

// The gate reads every .go file of the package directory, whatever its name or
// constraint, because go/build lists only the files that this platform and
// these tags would build: a TestMain behind //go:build race, or in a file named
// x_linux_test.go, would run only on the platform or with the tag that is not
// the gate's. So a TestMain is refused in any file, and so is anything that can
// make the build leave a file out: a build constraint in any spelling that Go
// reads (decided by go/build/constraint on the lines that Go itself reads, the
// header before the package clause), a GOOS or GOARCH file name, and import "C"
// (left out when cgo is off).
//
// Those rules name the causes that are known. The file-set rule closes the
// class: every non-test file of a gated package that has a statement must have
// a block in the cover profile, so a file that the build left out for any other
// reason fails the gate by name too, whatever the reason.
//
// A TestMain runs the tests itself, so it can run them and then exit 0, and every
// test of the package can fail with the run green: the gate counts statements
// that ran, not tests that passed. If a package needs set-up and tear-down, do
// it in the tests that need it, with t.Cleanup, or in a helper they call; a seam
// for a test is a variable or a parameter in the code it tests.

// The operating systems and architectures that make a file name a build
// constraint, as of Go 1.27 (go/build's internal syslist).
var (
	knownOS   = strings.Fields("aix android darwin dragonfly freebsd hurd illumos ios js linux nacl netbsd openbsd plan9 solaris wasip1 windows zos")
	knownArch = strings.Fields("386 amd64 amd64p32 arm armbe arm64 arm64be loong64 mips mipsle mips64 mips64le mips64p32 mips64p32le ppc ppc64 ppc64le riscv riscv64 s390 s390x sparc sparc64 wasm")
)

// nameConstraint returns the GOOS or GOARCH that a file name carries, or "".
// Like go/build it ignores the part before the first underscore and a _test
// suffix: linux.go is a plain file, x_linux.go and x_linux_test.go are not.
func nameConstraint(name string) string {
	stem := strings.TrimSuffix(strings.TrimSuffix(name, ".go"), "_test")
	_, tail, found := strings.Cut(stem, "_")
	if !found {
		return ""
	}
	parts := strings.Split(tail, "_")
	last := parts[len(parts)-1]
	if slices.Contains(knownOS, last) || slices.Contains(knownArch, last) {
		return last
	}
	return ""
}

// LoadPackage reads the Go files of the directory dir of the module at the root
// of fsys. It reads that one directory, not those below it.
func LoadPackage(fsys fs.FS, module, dir string) (Package, error) {
	pkg := Package{Dir: dir, Path: path.Join(module, dir)}
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return Package{}, err
	}
	files := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		files++
		file := path.Join(dir, name)
		src, err := fs.ReadFile(fsys, file)
		if err != nil {
			return Package{}, err
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, src, parser.SkipObjectResolution)
		if err != nil {
			return Package{}, fmt.Errorf("%s does not parse: %w", file, err)
		}
		if suffix := nameConstraint(name); suffix != "" {
			pkg.Constraints = append(pkg.Constraints, fmt.Sprintf("%s: the file name carries a GOOS or GOARCH build constraint (%s)", file, suffix))
		}
		for _, line := range headerConstraints(src, parsed) {
			pkg.Constraints = append(pkg.Constraints, fmt.Sprintf("%s: %s", file, line))
		}
		if importsC(parsed) {
			pkg.Constraints = append(pkg.Constraints, fmt.Sprintf(`%s: imports "C", so the build leaves it out when cgo is off`, file))
		}
		if strings.HasSuffix(name, "_test.go") {
			if declaresTestMain(parsed) {
				pkg.TestMains = append(pkg.TestMains, file)
			}
		} else if hasStatement(parsed) {
			pkg.HasStatements = true
			pkg.Files = append(pkg.Files, path.Join(pkg.Path, name))
		}
	}
	if files == 0 {
		return Package{}, fmt.Errorf("%s has no Go files", dir)
	}
	return pkg, nil
}

// headerConstraints returns the build constraint lines of the file as Go reads
// them: the //go:build and // +build lines of the header, which is everything
// before the package clause. go/build/constraint decides what a constraint line
// is, so the spellings that Go accepts (//+build, // +build with more spaces or
// a tab) are all seen.
func headerConstraints(src []byte, file *ast.File) []string {
	var found []string
	header := string(src[:min(int(file.Package)-1, len(src))]) // token.Pos of a file made with a fresh FileSet is its offset + 1
	for _, line := range strings.Split(header, "\n") {
		line = strings.TrimSpace(line)
		if constraint.IsGoBuild(line) || constraint.IsPlusBuild(line) {
			found = append(found, line)
		}
	}
	return found
}

// importsC reports whether the file imports "C": cgo, which a build with
// CGO_ENABLED=0 leaves out without a word.
func importsC(file *ast.File) bool {
	for _, spec := range file.Imports {
		if spec.Path.Value == `"C"` {
			return true
		}
	}
	return false
}

func declaresTestMain(file *ast.File) bool {
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "TestMain" {
			return true
		}
	}
	return false
}

// hasStatement reports whether the file has a block with a statement in it,
// which is what the cover tool counts.
func hasStatement(file *ast.File) bool {
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		if block, ok := n.(*ast.BlockStmt); ok && len(block.List) > 0 {
			found = true
		}
		return !found
	})
	return found
}

// Result is what the gate found out.
type Result struct {
	Covered, Total int
	Problems       []string
}

// OK reports whether the gate passes.
func (r Result) OK() bool { return len(r.Problems) == 0 }

// Check judges the blocks of the profile that belong to the packages, and the
// packages themselves. Blocks of other packages are ignored: the gate speaks
// for exactly the packages it is given.
func Check(pkgs []Package, blocks []Block) Result {
	var result Result
	inProfile := map[string]int{}       // statements per package in the profile
	filesInProfile := map[string]bool{} // files with a statement in the profile
	byPath := map[string]Package{}
	for _, pkg := range pkgs {
		byPath[pkg.Path] = pkg
	}
	for _, block := range blocks {
		dir := block.Dir()
		if _, listed := byPath[dir]; !listed {
			continue
		}
		inProfile[dir] += block.Statements
		if block.Statements > 0 {
			file, _, _ := strings.Cut(block.Location, ":")
			filesInProfile[file] = true
		}
		result.Total += block.Statements
		if block.Hit {
			result.Covered += block.Statements
		} else if block.Statements > 0 {
			result.Problems = append(result.Problems, fmt.Sprintf("uncovered: %s (%d statements)", block.Location, block.Statements))
		}
	}
	for _, pkg := range pkgs {
		for _, constraint := range pkg.Constraints {
			result.Problems = append(result.Problems, fmt.Sprintf("%s: a Go file with a build constraint can be left out of a test run and so out of the coverage profile, where the gate cannot see it; no file of a gated package may have one", constraint))
		}
		for _, file := range pkg.TestMains {
			result.Problems = append(result.Problems, fmt.Sprintf("%s declares TestMain, which can hide a failing test (it may run the tests and exit 0); no gated package may have one", file))
		}
		if pkg.HasStatements && inProfile[pkg.Path] == 0 {
			result.Problems = append(result.Problems, fmt.Sprintf("package %s has statements and none of them is in the cover profile (its tests were not run, or the profile was written without it)", pkg.Path))
		}
		for _, file := range pkg.Files {
			if !filesInProfile[file] && inProfile[pkg.Path] > 0 {
				result.Problems = append(result.Problems, fmt.Sprintf("file %s has statements and none of them is in the cover profile: the build left it out (a constraint, a file name, cgo, or anything else); the gate counts only what was built", file))
			}
		}
	}
	if result.Total == 0 {
		result.Problems = append(result.Problems, "the cover profile holds no statement of the given packages: nothing was measured")
	}
	return result
}

// modulePath reads the module path from the go.mod at the root of fsys.
func modulePath(fsys fs.FS) (string, error) {
	mod, err := fs.ReadFile(fsys, "go.mod")
	if err != nil {
		return "", fmt.Errorf("the gate must run in the module root: %w", err)
	}
	for _, line := range strings.Split(string(mod), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[0] == "module" {
			return strings.Trim(fields[1], `"`), nil
		}
	}
	return "", errors.New("go.mod names no module")
}

// packageDir turns an argument into a directory relative to the module root:
// ./internal/x, internal/x or the import path of the package. A pattern (...),
// an absolute or backslashed path, or one that leaves the module is refused:
// the gate takes a list, not a pattern, so that a package that is added later
// is not gated until someone adds it to the list.
func packageDir(arg, module string) (string, error) {
	dir := strings.TrimPrefix(arg, "./")
	if dir == module {
		return ".", nil
	}
	dir = strings.TrimPrefix(dir, module+"/")
	if dir == "" || strings.Contains(dir, "...") || strings.Contains(dir, `\`) || path.Clean(dir) != dir || dir == ".." || strings.HasPrefix(dir, "../") || path.IsAbs(dir) {
		return "", fmt.Errorf("%q is not a package of the module (give a directory or an import path, no pattern)", arg)
	}
	return dir, nil
}

// Run is the gate command: Run([]string{"cover.out", "./internal/a", ...}, ...)
// prints the totals and returns 0 when every statement of the packages is
// covered and nothing hides a package from the profile, 1 when anything is
// wrong, and 2 for a usage or read error. root is the module's file tree.
func Run(args []string, stdout, stderr io.Writer, open func(string) (io.ReadCloser, error), root fs.FS) int {
	if len(args) < 2 {
		_, _ = fmt.Fprintln(stderr, "usage: covergate <cover profile> <package>...")
		return 2
	}
	file, err := open(args[0])
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "covergate: %v\n", err)
		return 2
	}
	defer func() { _ = file.Close() }()
	blocks, err := Parse(file)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "covergate: %s: %v\n", args[0], err)
		return 2
	}
	module, err := modulePath(root)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "covergate: %v\n", err)
		return 2
	}
	var pkgs []Package
	seen := map[string]bool{}
	for _, arg := range args[1:] {
		dir, err := packageDir(arg, module)
		if err == nil && seen[dir] {
			err = fmt.Errorf("%q is given twice", arg)
		}
		var pkg Package
		if err == nil {
			seen[dir] = true
			pkg, err = LoadPackage(root, module, dir)
		}
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "covergate: %v\n", err)
			return 2
		}
		pkgs = append(pkgs, pkg)
	}
	result := Check(pkgs, blocks)
	_, _ = fmt.Fprintf(stdout, "statements covered: %d of %d, in %d packages\n", result.Covered, result.Total, len(pkgs))
	if result.OK() {
		return 0
	}
	for _, problem := range result.Problems {
		_, _ = fmt.Fprintln(stderr, problem)
	}
	_, _ = fmt.Fprintf(stderr, "covergate: %d problem(s); every statement of the given packages must be covered\n", len(result.Problems))
	return 1
}
