package repo

import (
	"fmt"
	"slices"
	"strings"
)

// Node is a path of a Memory that is not a directory.
type Node struct {
	Kind    Kind
	Content []byte
}

// Memory is a Reader over a repository held in memory: the fake of the tests, and the
// reference of what a Reader does. A path that has others below it is a directory.
type Memory struct {
	Err       error           // what Head returns, when set
	Nodes     map[string]Node // everything the commit holds, but directories
	Untracked map[string]bool // paths the working tree holds and the commit does not
}

// Head returns a commit, or Err.
func (m *Memory) Head() (string, error) {
	if m.Err != nil {
		return "", m.Err
	}
	return strings.Repeat("a", 40), nil
}

// Entries lists a directory, in name order.
func (m *Memory) Entries(dir string) ([]Entry, error) {
	prefix := ""
	if dir != "" {
		prefix = dir + "/"
	}
	seen := map[string]Kind{}
	for path, node := range m.Nodes {
		rest, ok := strings.CutPrefix(path, prefix)
		if !ok {
			continue
		}
		name, _, nested := strings.Cut(rest, "/")
		if _, dup := seen[name]; nested {
			seen[name] = Directory
		} else if !dup {
			seen[name] = node.Kind
		}
	}
	if len(seen) > MaxEntries {
		return nil, ErrTooLarge
	}
	entries := make([]Entry, 0, len(seen))
	for name, kind := range seen {
		entries = append(entries, Entry{Name: name, Kind: kind})
	}
	slices.SortFunc(entries, func(a, b Entry) int { return strings.Compare(a.Name, b.Name) })
	return entries, nil
}

// Blob returns the contents of a regular file.
func (m *Memory) Blob(path string, limit int) ([]byte, error) {
	node, ok := m.Nodes[path]
	switch {
	case !ok || !node.Kind.Regular():
		return nil, fmt.Errorf("no regular file at %s", ascii(path))
	case len(node.Content) > limit:
		return nil, ErrTooLarge
	}
	return node.Content, nil
}

// Uncommitted reports whether the working tree holds path.
func (m *Memory) Uncommitted(path string) bool { return m.Untracked[path] }
