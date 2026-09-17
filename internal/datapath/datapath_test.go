package datapath

import (
	"testing"

	"github.com/openvaultdb/ovdb/internal/envelope"
)

// AC:cd-examples: every row of the examples table, from todo:/lists/to-buy.
func TestResolveExamples(t *testing.T) {
	t.Parallel()
	base, err := Parse("/lists/to-buy")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		input, want string
		kind        Kind
	}{
		{"items", "/lists/to-buy/items", Collection},
		{"..", "/lists", Collection},
		{"../to-watch", "/lists/to-watch", Record},
		{"../../../..", "/", Root},
		{"/", "/", Root},
		{"", "/lists/to-buy", Record},
		{".", "/lists/to-buy", Record},
		{"./items//x/", "/lists/to-buy/items/x", Record},
		{"/lists/new-list/items", "/lists/new-list/items", Collection},
		{"../../a%2Fb", "/a%2Fb", Collection},
	} {
		got, err := Resolve(base, tc.input)
		if err != nil {
			t.Errorf("Resolve(%q): %v", tc.input, err)
			continue
		}
		if got.String() != tc.want || got.Kind() != tc.kind {
			t.Errorf("Resolve(%q) = %s (%s), want %s (%s)", tc.input, got, got.Kind(), tc.want, tc.kind)
		}
	}
}

// AC:escaped-ids-round-trip: escapes decode to the id and encode back the
// same way; a literal % is invalid_argument.
func TestEscaping(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		input, id, display, url string
	}{
		{"/files/a%2Fb%2Etxt", "a/b.txt", "/files/a%2Fb%2Etxt", "files/a%2Fb%2Etxt"},
		{"/files/a%2fb", "a/b", "/files/a%2Fb", "files/a%2Fb"},
		{"/x/%24%23%5B%5D", "$#[]", "/x/%24%23%5B%5D", "x/%24%23%5B%5D"},
		{"/x/a.b", "a.b", "/x/a%2Eb", "x/a%2Eb"},
		{"/x/hello world?", "hello world?", "/x/hello world?", "x/hello%20world%3F"},
		{"/x/café", "café", "/x/café", "x/caf%C3%A9"},
	} {
		p, err := Parse(tc.input)
		if err != nil {
			t.Errorf("Parse(%q): %v", tc.input, err)
			continue
		}
		if p.Name() != tc.id || p.String() != tc.display || p.URLKey() != tc.url {
			t.Errorf("Parse(%q) = id %q display %q url %q, want %q %q %q", tc.input, p.Name(), p, p.URLKey(), tc.id, tc.display, tc.url)
		}
		back, err := FromKey(p.Key())
		if err != nil || back.String() != p.String() {
			t.Errorf("FromKey(%q) = %s, %v", p.Key(), back, err)
		}
	}
	for _, input := range []string{"/files/50%off", "/x/%", "/x/a%2", "/x/%20"} {
		_, err := Parse(input)
		if e := envelope.As(err); e == nil || e.Code != envelope.InvalidArgument {
			t.Errorf("Parse(%q) = %v, want invalid_argument", input, err)
		}
	}
}

func TestParentChildKey(t *testing.T) {
	t.Parallel()
	p, _ := Parse("/lists/to-buy/items")
	if p.Parent().Key() != "lists/to-buy" || p.Parent().Parent().Parent().Parent().String() != "/" {
		t.Errorf("parents of %s", p)
	}
	if c := p.Child("a/b"); c.String() != "/lists/to-buy/items/a%2Fb" || c.Kind() != Record {
		t.Errorf("child = %s", c)
	}
	if (Path{}).Key() != "" || (Path{}).Name() != "" {
		t.Error("root key")
	}
}
