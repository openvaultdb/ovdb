package main

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// runMain calls main with the seams replaced and returns the exit code it asked for.
func runMain(t *testing.T, argv []string, profile string, tree fs.FS) int {
	t.Helper()
	oldExit, oldArgs, oldOpen, oldRoot := exit, args, open, root
	t.Cleanup(func() { exit, args, open, root = oldExit, oldArgs, oldOpen, oldRoot })
	code := -1
	exit = func(c int) { code = c }
	args = func() []string { return argv }
	open = func(string) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(profile)), nil }
	root = func() fs.FS { return tree }
	main()
	return code
}

func TestMainExitCodes(t *testing.T) {
	tree := fstest.MapFS{
		"go.mod": {Data: []byte("module example.test/m\n")},
		"p/p.go": {Data: []byte("package p\n\nfunc F() int { return 1 }\n")},
	}
	if code := runMain(t, []string{"cover.out", "./p"}, "mode: set\nexample.test/m/p/p.go:3.1,3.9 1 1\n", tree); code != 0 {
		t.Errorf("a covered package exits %d", code)
	}
	if code := runMain(t, []string{"cover.out", "./p"}, "mode: set\nexample.test/m/p/p.go:3.1,3.9 1 0\n", tree); code != 1 {
		t.Errorf("an uncovered package exits %d", code)
	}
	if code := runMain(t, nil, "", tree); code != 2 {
		t.Errorf("no arguments exit %d", code)
	}
}

// The defaults read the real process: its arguments, files and working directory.
func TestDefaultSeams(t *testing.T) {
	if got := args(); len(got) != len(os.Args)-1 {
		t.Errorf("args() = %v", got)
	}
	name := filepath.Join(t.TempDir(), "cover.out")
	if err := os.WriteFile(name, []byte("mode: set\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := open(name)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := io.ReadAll(file); string(data) != "mode: set\n" {
		t.Errorf("open read %q", data)
	}
	_ = file.Close()
	if _, err := open(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("open of a missing file succeeds")
	}
	// The tests run in the directory of the package; its files are the tree.
	if _, err := fs.Stat(root(), "main.go"); err != nil {
		t.Errorf("root() is not the working directory: %v", err)
	}
	if exit == nil {
		t.Error("exit is not set")
	}
}
