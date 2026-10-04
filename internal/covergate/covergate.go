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
	"go/scanner"
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
// when several test binaries cover the same package (-coverpkg), counts once, and
// is hit when any listing of it was hit. Two listings of one location must
// describe the same block, with the same statement count: if they do not, the
// profile is refused.
//
// What Parse cannot tell from the profile alone is a repeated location that is
// two different blocks of the source with the same statement count: that is what
// a //line directive makes (a block is named by the file the directive gives
// and the physical line and column), and "hit if any listing was hit" would then
// count an unexecuted block as covered. A profile cannot say which case it is,
// so the gate does not try: it refuses every //line directive in a gated package
// (see LoadPackage), and with none there, one location is one block.
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
		} else if block.Statements != statements {
			return nil, fmt.Errorf("line %d: %s is listed with %d statements and with %d: one location is one block", line, fields[0], block.Statements, statements)
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

// Import is an import of a package of the module, and the file that has it.
type Import struct{ File, Path string }

// Package is a package of the module as the gate reads it from the file tree.
type Package struct {
	Dir           string   // relative to the module root, "." for the root
	Path          string   // import path
	HasStatements bool     // a non-test file has a block with a statement: what the cover tool counts
	Files         []string // the non-test files with a statement, as import paths: each must be in the profile
	TestMains     []string // test files that declare TestMain
	Constraints   []string // one message per build constraint, GOOS/GOARCH file name or import "C"
	Directives    []string // one message per //line or /*line*/ directive, which renames the blocks of a profile
	Imports       []Import // the imports of this module by the non-test files: Check holds each to the list of gated packages
	Others        []Import // the other imports of the non-test files: Run holds each to the modules that go.mod and go.work send to a directory
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
// A //line or /*line*/ directive is refused too, in any file, test files
// included: the cover profile names a block by the file the directive gives and
// the physical line and column, so a block after a directive can have the
// location of a covered block of another file, and the two merge into one that
// counts as covered. Directives are found with the scanner's comments, so one
// in any position that Go honours is seen and the same text inside a string
// literal is not.
//
// A non-test file of a gated package may import, from this module, only a gated
// package: a package of the list the gate was given. Any other package of the
// module (one outside the gated roots, or a testdata directory, which the go tool
// never lists as a package) has statements that no profile entry the gate reads
// covers, and a function with an untested branch there, called from a gated file,
// would pass. Test files may import any (their code is not counted). Today every
// gated package imports only gated ones (manifest and repo import rules and manifest,
// cmd/covergate imports internal/covergate), so the rule needs no exception.

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
		for _, line := range lineDirectives(file, src) {
			pkg.Directives = append(pkg.Directives, fmt.Sprintf("%s:%d: a line directive renames the blocks of the cover profile, so a block that never ran can take the location of one that did; no file of a gated package may have one", file, line))
		}
		if importsC(parsed) {
			pkg.Constraints = append(pkg.Constraints, fmt.Sprintf(`%s: imports "C", so the build leaves it out when cgo is off`, file))
		}
		if strings.HasSuffix(name, "_test.go") {
			if declaresTestMain(parsed) {
				pkg.TestMains = append(pkg.TestMains, file)
			}
		} else {
			for _, spec := range parsed.Imports {
				imported, _ := strconv.Unquote(spec.Path.Value) // the parser has checked the literal
				if imported == module || strings.HasPrefix(imported, module+"/") {
					pkg.Imports = append(pkg.Imports, Import{File: file, Path: imported})
				} else {
					pkg.Others = append(pkg.Others, Import{File: file, Path: imported})
				}
			}
		}
		if !strings.HasSuffix(name, "_test.go") && hasStatement(parsed) {
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
	header = strings.TrimPrefix(header, "\ufeff")              // Go reads the header without a leading byte order mark
	for _, line := range strings.Split(header, "\n") {
		line = strings.TrimSpace(line)
		if constraint.IsGoBuild(line) || constraint.IsPlusBuild(line) {
			found = append(found, line)
		}
	}
	return found
}

// lineDirectives returns the lines of the line directives of src, as Go sees
// them: comments that begin //line or /*line followed by a space or a tab, found
// by the Go scanner (so not text inside a string literal), at any column.
func lineDirectives(name string, src []byte) []int {
	fset := token.NewFileSet()
	file := fset.AddFile(name, -1, len(src))
	var sc scanner.Scanner
	sc.Init(file, src, nil, scanner.ScanComments)
	var lines []int
	for {
		pos, tok, text := sc.Scan()
		if tok == token.EOF {
			return lines
		}
		if tok != token.COMMENT {
			continue
		}
		for _, prefix := range []string{"//line", "/*line"} {
			if rest, ok := strings.CutPrefix(text, prefix); ok && (strings.HasPrefix(rest, " ") || strings.HasPrefix(rest, "\t")) {
				lines = append(lines, fset.PositionFor(pos, false).Line)
			}
		}
	}
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

// LocalModules are the modules that the module's go.mod and its go.work (when it has one) send to a directory: a `replace` whose target is a path, and a
// `use`. A package of one of them is built from that directory, and the directory is not a package of the list that the gate was given, so its
// statements are in no profile entry that the gate reads. A go.mod or go.work that cannot be read says nothing here: the gate is given the
// module root, and the absence of a file is the absence of its directives.
func LocalModules(root fs.FS) []string {
	var modules []string
	for _, name := range []string{"go.mod", "go.work"} {
		text, err := fs.ReadFile(root, name)
		if err != nil {
			continue
		}
		block := ""
		for _, line := range strings.Split(string(text), "\n") {
			line, _, _ = strings.Cut(line, "//")
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			if block != "" && fields[0] == ")" {
				block = ""
				continue
			}
			if block == "" && len(fields) == 2 && fields[1] == "(" {
				block = fields[0]
				continue
			}
			directive, rest := block, fields
			if block == "" {
				directive, rest = fields[0], fields[1:]
			}
			switch directive {
			case "replace":
				if old, target, found := splitReplace(rest); found && isLocalPath(target) {
					modules = append(modules, old)
				}
			case "use":
				if len(rest) > 0 {
					if dir := unquote(rest[0]); dir != "" {
						modules = append(modules, workModule(root, dir))
					}
				}
			}
		}
	}
	return slices.DeleteFunc(modules, func(m string) bool { return m == "" })
}

// splitReplace reads `old [version] => new [version]`.
func splitReplace(fields []string) (old, target string, found bool) {
	arrow := slices.Index(fields, "=>")
	if arrow < 1 || arrow+1 >= len(fields) {
		return "", "", false
	}
	return unquote(fields[0]), unquote(fields[arrow+1]), true
}

func unquote(s string) string {
	if u, err := strconv.Unquote(s); err == nil {
		return u
	}
	return s
}

// isLocalPath reports whether a replace target is a directory (go: a path that starts with ./ or ../, or is rooted) and not a module.
func isLocalPath(target string) bool {
	return target == "." || target == ".." || strings.HasPrefix(target, "./") || strings.HasPrefix(target, "../") || strings.HasPrefix(target, "/") || strings.HasPrefix(target, `.\`) || strings.HasPrefix(target, `..\`)
}

// workModule is the module path of the go.mod in a directory that go.work uses, or "" when it has none that can be read.
func workModule(root fs.FS, dir string) string {
	dir = path.Clean(strings.ReplaceAll(dir, `\`, "/"))
	if path.IsAbs(dir) || dir == ".." || strings.HasPrefix(dir, "../") {
		return "" // outside the tree the gate reads
	}
	module, err := modulePath(subFS{root, dir})
	if err != nil {
		return ""
	}
	return module
}

// subFS is the part of a file tree below a directory.
type subFS struct {
	fs.FS
	dir string
}

func (s subFS) Open(name string) (fs.File, error) { return s.FS.Open(path.Join(s.dir, name)) }

// ReplacedImports are the problems of the non-test files of the gated packages that import a package of a module that LocalModules names.
func ReplacedImports(pkgs []Package, modules []string) []string {
	var problems []string
	for _, pkg := range pkgs {
		for _, imp := range pkg.Others {
			for _, module := range modules {
				if imp.Path == module || strings.HasPrefix(imp.Path, module+"/") {
					problems = append(problems, fmt.Sprintf("%s imports %s, a package of the module %s that go.mod replaces with a directory (or go.work uses): the gate cannot see its statements, so an untested branch there would pass; a gated package may not import it", imp.File, imp.Path, module))
				}
			}
		}
	}
	return problems
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
		result.Problems = append(result.Problems, pkg.Directives...)
		for _, imp := range pkg.Imports {
			if _, gated := byPath[imp.Path]; !gated {
				result.Problems = append(result.Problems, fmt.Sprintf("%s imports %s, a package of this module that is not one of the gated packages: the gate cannot see its statements, so an untested branch there would pass; gate it, or do not import it from a gated package (a testdata directory is never a package of the list)", imp.File, imp.Path))
			}
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
// wrong, and 2 for a usage or read error. root is the module's file tree, and goenv asks the go tool for one of its settings (`go env NAME`).
func Run(args []string, stdout, stderr io.Writer, open func(string) (io.ReadCloser, error), root fs.FS, goenv func(name string) (string, error)) int {
	if len(args) < 2 {
		_, _ = fmt.Fprintln(stderr, "usage: covergate <cover profile> <package>...")
		return 2
	}
	if problem := hiddenBuild(root, goenv); problem != "" {
		_, _ = fmt.Fprintf(stderr, "covergate: %s\n", problem)
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
	result.Problems = append(result.Problems, ReplacedImports(pkgs, LocalModules(root))...)
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

// hiddenBuild says why the profile cannot be trusted to be of the sources the gate reads, or "": a build that is not of the module's own files at the
// module's own versions makes a profile of other code. The settings are asked of the go tool (`go env GOFLAGS GOWORK`), not read from this process's
// environment, because the go tool also reads them from its GOENV file (what `go env -w` writes): GOFLAGS can name another go.mod (-modfile), replace
// files (-overlay), use another go.work (-workfile), run the compiler through a program (-toolexec), choose which files are built (-tags), and choose
// vendored (-mod=vendor) or freshly resolved (-mod=mod) dependencies; GOWORK can name a go.work outside the tree; and a vendor directory is used by
// the go command of its own accord. Every one of them hides a function from the gate or puts another in its place.
func hiddenBuild(root fs.FS, goenv func(name string) (string, error)) string {
	flags, err := goenv("GOFLAGS")
	if err != nil {
		return fmt.Sprintf("the go tool could not say what GOFLAGS is (%v), so the gate cannot tell what the profile measured", err)
	}
	for _, flag := range strings.Fields(flags) {
		name, value, _ := strings.Cut(strings.TrimLeft(flag, "-"), "=")
		switch {
		case name == "modfile", name == "overlay", name == "workfile", name == "toolexec", name == "tags", name == "mod" && (value == "vendor" || value == "mod"):
			return fmt.Sprintf("GOFLAGS has %s: the profile would be of another build than the module's own files, so the gate cannot tell what it measured; unset it (it may come from the file `go env GOENV` names)", flag)
		}
	}
	work, err := goenv("GOWORK")
	if err != nil {
		return fmt.Sprintf("the go tool could not say what GOWORK is (%v), so the gate cannot tell what the profile measured", err)
	}
	if work = strings.TrimSpace(work); work != "" && work != "off" {
		return fmt.Sprintf("the go tool uses the workspace file %s: modules it names replace the module's dependencies, so the profile may be of other code than the gate reads; set GOWORK=off", work)
	}
	if info, err := fs.Stat(root, "vendor"); err == nil && info.IsDir() {
		return "the module has a vendor directory: the go command builds from it, not from the module's dependencies, so the profile may be of other code than the gate reads; remove it"
	}
	return ""
}
