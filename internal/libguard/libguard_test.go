package libguard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"testing"
)

func parse(t *testing.T, sources ...string) []*ast.File {
	t.Helper()
	var files []*ast.File
	for _, source := range sources {
		file, err := parser.ParseFile(token.NewFileSet(), "x.go", source, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	return files
}

// A library writes a typed string constant either way, in any file of the
// package: both forms are read, other types and other values are not.
func TestStringConstsOfTypeReadsBothForms(t *testing.T) {
	files := parse(t, `package p
type Action string
type Other string
const (
	Added Action = "added"
	Skipped = Action("skipped")
	NotThis Other = "other"
	AlsoNot = Other("other2")
	Plain = "plain"
	Computed Action = prefix + "x"
	// The forms that were skipped: a typed constant that also converts, and
	// several names at once.
	C Action = Action("c")
	D, E Action = "d", "e"
	F, G = Action("f"), Action("g")
	H, I Action = "h", "i"
)
const prefix = "p"
`, `package p
const Later = Action("later")
`)
	if got, want := StringConstsOfType(files, "Action"), []string{"added", "c", "d", "e", "f", "g", "h", "i", "later", "skipped"}; !slices.Equal(got, want) {
		t.Errorf("Action constants = %v, want %v", got, want)
	}
}

func TestConstNamesWithPrefixAndStringLiterals(t *testing.T) {
	files := parse(t, `package p
const (
	KindA = iota
	KindB
	Other
)
var reason = "modified target"
`)
	if got := ConstNamesWithPrefix(files, "Kind"); !slices.Equal(got, []string{"KindA", "KindB"}) {
		t.Errorf("names = %v", got)
	}
	if !HasStringLiteral(files, "modified target") || HasStringLiteral(files, "nothing") {
		t.Error("HasStringLiteral")
	}
}

// A failing go list says what the go command said, not only that it exited 1.
func TestGoListFailureCarriesStderr(t *testing.T) {
	_, err := goListDir("github.com/openvaultdb/ovdb/no/such/package")
	if err == nil || !strings.Contains(err.Error(), "no/such/package") || !strings.Contains(err.Error(), "exit status 1: ") || strings.HasSuffix(err.Error(), "exit status 1: ") {
		t.Errorf("goListDir error = %v", err)
	}
	if dir, err := goListDir("github.com/strongo/cli-helpers/skillsync"); err != nil || !strings.Contains(dir, "skillsync") {
		t.Errorf("goListDir = %q, %v", dir, err)
	}
}
