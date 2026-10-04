package repo

import (
	"errors"
	"slices"
	"testing"
)

func TestMemoryListsDirectoriesAndKinds(t *testing.T) {
	m := &Memory{Nodes: map[string]Node{
		"a":     {Kind: File},
		"b":     {Kind: Symlink},
		"b/c":   {Kind: File}, // a path with others below it is a directory
		"d/e/f": {Kind: Executable},
		"d/g":   {Kind: Submodule},
		"h/i":   {Kind: File},
	}}
	for dir, want := range map[string][]Entry{
		"":    {{"a", File}, {"b", Directory}, {"d", Directory}, {"h", Directory}},
		"d":   {{"e", Directory}, {"g", Submodule}},
		"d/e": {{"f", Executable}},
		"x":   {},
	} {
		got, err := m.Entries(dir)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("Entries(%q) = %v, %v; want %v", dir, got, err, want)
		}
	}
}

func TestMemoryRefusesMoreThanMaxEntries(t *testing.T) {
	m := &Memory{Nodes: map[string]Node{}}
	for i := range MaxEntries + 1 {
		m.Nodes[string(rune('a'+i%26))+string(rune('a'+i/26%26))+string(rune('a'+i/676%26))+string(rune('a'+i/17576))] = Node{Kind: File}
	}
	if _, err := m.Entries(""); !errors.Is(err, ErrTooLarge) {
		t.Errorf("err = %v", err)
	}
}

func TestMemoryBlobs(t *testing.T) {
	m := &Memory{Nodes: map[string]Node{"a": {Kind: File, Content: []byte("abc")}, "b": {Kind: Symlink, Content: []byte("a")}, "c": {Kind: Executable}}}
	if got, err := m.Blob("a", 3); err != nil || string(got) != "abc" {
		t.Errorf("Blob = %q, %v", got, err)
	}
	if _, err := m.Blob("a", 2); !errors.Is(err, ErrTooLarge) {
		t.Errorf("err = %v", err)
	}
	for _, path := range []string{"b", "missing", "d/e"} {
		if _, err := m.Blob(path, 10); err == nil {
			t.Errorf("a blob at %s", path)
		}
	}
	if _, err := m.Blob("c", 1); err != nil {
		t.Error(err)
	}
}

func TestMemoryHeadAndUncommitted(t *testing.T) {
	m := &Memory{Untracked: map[string]bool{"x": true}}
	if id, err := m.Head(); err != nil || len(id) != 40 {
		t.Errorf("Head = %q, %v", id, err)
	}
	if !m.Uncommitted("x") || m.Uncommitted("y") {
		t.Error("Uncommitted")
	}
	m.Err = ErrNoCommit
	if id, err := m.Head(); id != "" || err != ErrNoCommit {
		t.Errorf("Head = %q, %v", id, err)
	}
}

func TestKinds(t *testing.T) {
	for kind, regular := range map[Kind]bool{Missing: false, File: true, Executable: true, Symlink: false, Submodule: false, Directory: false, Other: false} {
		if kind.Regular() != regular || kind.String() == "" {
			t.Errorf("%d: regular %v, %q", kind, kind.Regular(), kind)
		}
	}
}

func TestAsciiMakesAMessageSafe(t *testing.T) {
	got := ascii("  bad \x1b[31m é\n" + string(make([]byte, 300)))
	if len(got) > 1200 {
		t.Errorf("%d bytes", len(got))
	}
	for i := 0; i < len(got); i++ {
		if got[i] < 0x20 || got[i] > 0x7e {
			t.Fatalf("%q", got)
		}
	}
	if ascii("short") != "short" {
		t.Error("short text changed")
	}
}
