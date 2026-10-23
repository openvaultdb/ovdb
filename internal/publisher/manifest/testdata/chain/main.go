// Command chain reads documents and says, for each, whether the strict YAML reader refuses it and, if so, the lines of the reader that led
// to the refusal. It is run by reader_places_test.go through `go run -modfile`: the test copies the reader's module into a temporary
// directory, instruments it there, and builds this program against the copy with a go.mod of its own that replaces the module by the copy.
// Every SyntaxError then carries the lines of yaml.go and yaml_flow.go that were running, innermost first. It is not part of any package
// that is built or gated (the go tool does not look in testdata), and the shipped binary does not contain it.
//
// Input: a JSON array of base64 strings, the bytes of the documents. Output: a JSON array of {Rule, Chain}, with an empty Rule when the
// document is read.
package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/meaninggraph/cli/pkg/meaning"
)

type result struct{ Rule, Chain string }

func main() {
	var inputs []string
	if err := json.NewDecoder(os.Stdin).Decode(&inputs); err != nil {
		fmt.Fprintln(os.Stderr, "chain:", err)
		os.Exit(1)
	}
	out := make([]result, len(inputs))
	for i, in := range inputs {
		data, err := base64.StdEncoding.DecodeString(in)
		if err != nil {
			fmt.Fprintln(os.Stderr, "chain:", err)
			os.Exit(1)
		}
		_, err = meaning.ParseYAML(data)
		var syntax *meaning.SyntaxError
		if !errors.As(err, &syntax) {
			continue
		}
		// The instrumentation puts "[@frame<frame<...] " in front of the message.
		rest, ok := strings.CutPrefix(syntax.Message, "[@")
		chain, _, found := strings.Cut(rest, "] ")
		if !ok || !found {
			fmt.Fprintln(os.Stderr, "chain: a refusal without its chain:", syntax.Message)
			os.Exit(1)
		}
		out[i] = result{Rule: syntax.Rule, Chain: chain}
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, "chain:", err)
		os.Exit(1)
	}
}
