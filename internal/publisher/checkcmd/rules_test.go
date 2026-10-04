package checkcmd

import (
	"bytes"
	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The text of a finding is made where the rule is raised, and the command prints it as it is (it adds the rule id and the place). So the guard is on
// the places where findings are raised, in the packages whose findings reach the output: every call that raises one (add, Report) gives a rule id of
// the stable form (lower case words joined by hyphens, which is what the JSON document's consumers match on) and a message format that is not empty.
// A rule raised without a message, or an id of another shape, fails here and not in a publisher's terminal.
func TestEveryFindingIsRaisedWithAStableRuleAndAMessage(t *testing.T) {
	stable := regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*-?$`)
	calls := 0
	ids := map[string]bool{}
	for _, pkg := range []string{"manifest", "repo"} {
		files, _ := filepath.Glob(filepath.Join("..", pkg, "*.go"))
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(parsed, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				name := ""
				switch fn := call.Fun.(type) {
				case *ast.SelectorExpr:
					name = fn.Sel.Name
				case *ast.Ident:
					name = fn.Name
				}
				if (name != "add" && name != "Report") || len(call.Args) < 3 {
					return true
				}
				calls++
				for i, arg := range call.Args {
					lit, text := literal(arg)
					if !lit {
						continue
					}
					if i >= 2 && text == "" {
						t.Errorf("%s: a finding with an empty message", file)
					} else if i < 2 && strings.Contains(text, "-") && !strings.ContainsAny(text, " .") {
						ids[text] = true
						if !stable.MatchString(text) {
							t.Errorf("%s: rule id %q is not lower-case words joined by hyphens", file, text)
						}
					}
				}
				return true
			})
		}
	}
	// The longest message a rule can write (manifest.MaxMessageBytes) ends with its last word in the text form, whichever rule it is: the advice is last.
	if len(ids) < 25 {
		t.Errorf("only %d rule ids found", len(ids))
	}
	word := " LASTWORD"
	message := strings.Repeat("x", manifest.MaxMessageBytes-len(word)) + word
	for id := range ids {
		var out bytes.Buffer
		doc := Document{Findings: []Finding{{Rule: id, Severity: "error", Path: "p", Message: message}}, Summary: Summary{Errors: 1}, Commit: "abc"}
		command{deps(nil)}.writeHuman(&out, doc)
		if !strings.Contains(out.String(), "\n  "+message+"\n") {
			t.Errorf("rule %q: the longest message is cut in the text form: %q", id, out.String())
		}
	}
	if calls < 50 {
		t.Errorf("only %d calls that raise a finding were found: the guard looks at nothing", calls)
	}
}

// literal reports whether e is a string literal, or a "prefix-"+x concatenation (the prefix of a rule id built at run time), and its text.
func literal(e ast.Expr) (bool, string) {
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			s, err := strconv.Unquote(e.Value)
			return err == nil, s
		}
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			return literal(e.X)
		}
	}
	return false, ""
}

// The command says its words through its T seam, which the module-wide test of the catalogue (uicopy.T literals) cannot see: so every literal key it passes
// is held to copy/en.json here, and the schema it writes is the shared one.
func TestEveryCopyKeyOfTheCommandIsInTheCatalogue(t *testing.T) {
	parsed, err := parser.ParseFile(token.NewFileSet(), "checkcmd.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	keys := 0
	ast.Inspect(parsed, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "T" {
			return true
		}
		lit, text := literal(call.Args[0])
		if !lit {
			t.Errorf("a copy key of the command is not a literal at %v: the catalogue test cannot see it", call.Pos())
			return true
		}
		keys++
		if !uicopy.Has(text) {
			t.Errorf("copy key %q is not in copy/en.json", text)
		}
		return true
	})
	if keys < 20 {
		t.Errorf("only %d copy keys found", keys)
	}
	if doc := newDocument("", manifest.Result{}); doc.Schema != envelope.Schema {
		t.Errorf("schema %d, the shared schema is %d", doc.Schema, envelope.Schema)
	}
}
