// ecb-preflight verifies proposed metadata offline; it has no provider executor.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/openvaultdb/openvaultdb-go/pkg/providerreads"
	"github.com/openvaultdb/ovdb/publisher/source/preflight"
)

var exit = os.Exit
var verify = preflight.Run
var open = func(path string) (io.ReadCloser, error) { return os.Open(path) }

func main() { exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, out, errOut io.Writer) int {
	set := flag.NewFlagSet("ecb-preflight", flag.ContinueOnError)
	set.SetOutput(errOut)
	proposalPath := set.String("proposal", "", "independently reviewed proposed metadata JSON; never execution permission")
	paths := map[string]*string{}
	for flag, name := range map[string]string{"ovdb": "openvaultdb/ovdb", "modelspec-registry": "modelspec-org/registry", "meaning-registry": "meaninggraph/registry", "meaning-core": "meaninggraph/core", "directory": "openvaultdb/directory", "decoder": "dal-go/dalgo2http"} {
		paths[name] = set.String(flag, "", "complete local Git repository")
	}
	if err := set.Parse(args); err != nil {
		return 1
	}
	if set.NArg() != 0 || *proposalPath == "" {
		_, _ = fmt.Fprintln(errOut, "require proposal file and no positional arguments")
		return 1
	}
	file, err := open(*proposalPath)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, "proposal metadata unavailable")
		return 1
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, providerreads.MaxMetadataBytes+1))
	if err != nil {
		_, _ = fmt.Fprintln(errOut, "proposal metadata unreadable")
		return 1
	}
	proposal, err := preflight.Parse(data)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, "invalid bounded proposal metadata")
		return 1
	}
	repositories := map[string]string{}
	for name, path := range paths {
		repositories[name] = *path
	}
	receipt, err := verify(context.Background(), repositories, proposal)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return 1
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(receipt); err != nil {
		_, _ = fmt.Fprintln(errOut, "cannot write metadata receipt")
		return 1
	}
	// Exit 2 means a verified preparatory disposition with admission still blocked.
	return 2
}
