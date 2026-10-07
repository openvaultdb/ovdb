// ecb-pin-check reproduces the fixed inactive metadata baseline from local Git
// repositories. It cannot fetch URLs, read ECB XML or enable provider execution.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/openvaultdb/ovdb/publisher/source/pinchain"
)

var exit = os.Exit
var validate = pinchain.Validate

func main() { exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, out, errOut io.Writer) int {
	repos := map[string]string{}
	flags := map[string]*string{}
	set := flag.NewFlagSet("ecb-pin-check", flag.ContinueOnError)
	set.SetOutput(errOut)
	for name, repo := range map[string]string{"ovdb": "openvaultdb/ovdb", "modelspec-registry": "modelspec-org/registry", "meaning-registry": "meaninggraph/registry", "meaning-core": "meaninggraph/core", "directory": "openvaultdb/directory", "decoder": "dal-go/dalgo2http"} {
		flags[repo] = set.String(name, "", "local Git repository path")
	}
	if err := set.Parse(args); err != nil {
		return 1
	}
	if set.NArg() != 0 {
		_, _ = fmt.Fprintln(errOut, "unexpected positional arguments")
		return 1
	}
	for repo, path := range flags {
		repos[repo] = *path
	}
	receipt, err := validate(context.Background(), repos)
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
	return 0
}
