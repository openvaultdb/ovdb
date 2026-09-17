package uicopy

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// moduleRoot resolves the ovdb module root from this test file's own path
// (copy/copy_ast_test.go) rather than the working directory, so the walk
// below is correct regardless of which package `go test` is run from.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("uicopy: could not determine the calling test file's path")
	}
	return filepath.Dir(filepath.Dir(file))
}

// TestEveryLiteralKeyExists is the Go half of
// spec/features/configuration-parity#REQ:copy-catalogue and its
// AC:copy-key-missing-fails: it walks every .go file in the module for a
// call shaped like uicopy.T("literal key", ...) and fails, naming the file
// and the key, when that literal is absent from copy/en.json. Only literal
// keys are checked; a key built at runtime cannot be verified statically and
// is intentionally skipped.
func TestEveryLiteralKeyExists(t *testing.T) {
	root := moduleRoot(t)
	fset := token.NewFileSet()
	var missing []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			// node_modules and dist are build trees with no .go files, and
			// scanning them is pure overhead; web/ itself is NOT skipped —
			// web/embed.go is a real uicopy.T call site.
			case "node_modules", "dist", ".git", ".wb", ".wb-worklog":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		src, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		file, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "T" {
				return true
			}
			pkgIdent, ok := sel.X.(*ast.Ident)
			if !ok || pkgIdent.Name != "uicopy" {
				return true
			}
			if len(call.Args) == 0 {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			key, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			if _, ok := catalogue[key]; !ok {
				missing = append(missing, fmt.Sprintf("%s: uicopy.T(%q, ...)", relPath(root, path), key))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s for uicopy.T call sites: %v", root, err)
	}
	if len(missing) > 0 {
		t.Fatalf("copy keys referenced but missing from copy/en.json:\n%s", strings.Join(missing, "\n"))
	}
}

func relPath(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return rel
	}
	return path
}
