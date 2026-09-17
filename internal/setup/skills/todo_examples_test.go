package skills

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	uicopy "github.com/openvaultdb/ovdb/copy"
	embedded "github.com/openvaultdb/ovdb/skills"
)

// Review M4: the "try it" step and the TODO skill's add examples name items
// the seed doesn't have (Tea to buy, Arrival to watch, as Journey D does),
// so an agent following them changes what the open app shows.
func TestTodoExamplesAddItemsNotInTheSeed(t *testing.T) {
	seeded := []string{"milk", "bananas", "coffee", "the matrix", "interstellar"}
	skill, err := fs.ReadFile(embedded.FS, "openvaultdb-todo-demo/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	examples := []string{uicopy.T("skills.todo.example", nil), uicopy.T("skills.next.try_todo", nil)}
	examples = append(examples, regexp.MustCompile(`(?m)^\| "add[^|]*\|[^\n]*`).FindAllString(string(skill), -1)...)
	examples = append(examples, regexp.MustCompile(`for example "add[^"]*"`).FindAllString(string(skill), -1)...)
	for _, example := range examples {
		lower := strings.ToLower(example)
		for _, title := range seeded {
			if strings.Contains(lower, title) {
				t.Errorf("example adds seeded %q: %s", title, example)
			}
		}
	}
	for _, key := range []string{"skills.todo.example", "skills.next.try_todo"} {
		if text := strings.ToLower(uicopy.T(key, nil)); !strings.Contains(text, "tea") || !strings.Contains(text, "arrival") {
			t.Errorf("%s = %q, want Tea and Arrival", key, text)
		}
	}
}
