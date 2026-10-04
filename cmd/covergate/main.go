// Command covergate fails unless every statement of the given packages is
// covered in a Go cover profile. See package covergate.
//
//	go test -covermode=atomic -coverprofile=cover.out ./internal/a ./internal/b
//	go run ./cmd/covergate cover.out ./internal/a ./internal/b
//
// Run it from the module root. The packages are a list, not a pattern.
package main

import (
	"io"
	"io/fs"
	"os"

	"github.com/openvaultdb/ovdb/internal/covergate"
)

// The seams of main: the tests replace them.
var (
	exit = os.Exit
	args = func() []string { return os.Args[1:] }
	open = func(name string) (io.ReadCloser, error) { return os.Open(name) }
	// root is the module's file tree, read for the packages to gate.
	root = func() fs.FS { return os.DirFS(".") }
)

func main() {
	exit(covergate.Run(args(), os.Stdout, os.Stderr, open, root()))
}
