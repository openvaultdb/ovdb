// Package libguard reads the source of a library ovdb builds on, for the
// tests that hold ovdb's text and branches to the values that library can
// produce. Where a library exports a list of its values the test uses that
// list; where it exports only constants (github.com/strongo/cli-helpers'
// skillsync actions, selfupdate failure kinds) these helpers read them from
// the source of the exact version go.mod selects, so the library gaining a
// value fails a test, with a message saying what to add, instead of reaching a
// person as a panic or a silently wrong line.
//
// Only tests import this package.
package libguard

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// goListDir is the directory of the package importPath as the build resolves
// it. A failure carries what the go command said, not just its exit status.
func goListDir(importPath string) (string, error) {
	var stderr bytes.Buffer
	cmd := exec.Command("go", "list", "-f", "{{.Dir}}", importPath)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go list %s: %w: %s", importPath, err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

// Source parses the non-test Go files of the package importPath as the build
// resolves it. It fails the test when the go command or the source is not
// available: a guard that cannot read the library must not pass quietly.
func Source(t testing.TB, importPath string) []*ast.File {
	t.Helper()
	dir, err := goListDir(importPath)
	if err != nil {
		t.Fatalf("%v (the guard reads the library's source to list its values)", err)
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no Go files in %s: %v", dir, err)
	}
	var files []*ast.File
	fset := token.NewFileSet()
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		files = append(files, file)
	}
	return files
}

// StringConstsOfType lists, sorted, the string values of the constants of type
// typeName in files, written either as `Name Type = "value"` or as
// `Name = Type("value")`.
func StringConstsOfType(files []*ast.File, typeName string) []string {
	var values []string
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok || len(value.Values) != 1 {
					continue
				}
				literal := value.Values[0]
				if ident, ok := value.Type.(*ast.Ident); ok && ident.Name == typeName {
					// Name Type = "value"
				} else if call, ok := literal.(*ast.CallExpr); ok && value.Type == nil && len(call.Args) == 1 {
					// Name = Type("value")
					if fun, ok := call.Fun.(*ast.Ident); !ok || fun.Name != typeName {
						continue
					}
					literal = call.Args[0]
				} else {
					continue
				}
				if lit, ok := literal.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if text, err := strconv.Unquote(lit.Value); err == nil {
						values = append(values, text)
					}
				}
			}
		}
	}
	sort.Strings(values)
	return values
}

// ConstNamesWithPrefix lists, in source order, the names of the constants in
// files that start with prefix.
func ConstNamesWithPrefix(files []*ast.File, prefix string) []string {
	var names []string
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				if value, ok := spec.(*ast.ValueSpec); ok {
					for _, name := range value.Names {
						if strings.HasPrefix(name.Name, prefix) {
							names = append(names, name.Name)
						}
					}
				}
			}
		}
	}
	return names
}

// HasStringLiteral reports whether any file holds the string literal text.
func HasStringLiteral(files []*ast.File, text string) bool {
	found := false
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if value, err := strconv.Unquote(lit.Value); err == nil && value == text {
					found = true
				}
			}
			return !found
		})
	}
	return found
}
