package manifest

import (
	"errors"
	"strings"

	"github.com/meaninggraph/cli/pkg/meaning"
)

// This is the one file of the package that imports the strict YAML reader. Where
// the reader comes from (today github.com/meaninggraph/cli/pkg/meaning) is
// decided here and nowhere else: everything else in the package reads the
// documents through the Node type and parseYAML below, so changing the reader
// is a change to this file.

// Node is a parsed YAML value with the line it starts on: a Null, String,
// Number, Bool, Map or Seq (see the reader's package for the exact subset).
type Node = meaning.Node

// kind is the kind of a Node.
type kind = meaning.Kind

// The kinds of a Node.
const (
	kindNull   = meaning.Null
	kindString = meaning.String
	kindNumber = meaning.Number
	kindBool   = meaning.Bool
	kindMap    = meaning.Map
	kindSeq    = meaning.Seq
)

// syntaxError is a problem with the YAML of a document: a rule of the subset
// that the reader enforces, the line, and the reader's own message.
type syntaxError = meaning.SyntaxError

// parseYAML reads one YAML document of the reader's subset. An empty document is
// a Null node. A refused document is an error that errors.As reaches as a
// *syntaxError. The reader is written for meaning files, and one of its messages
// says so; every message that names the kind of document is reworded here, for
// the documents of this package (a manifest, or the front matter of OVDB.md).
func parseYAML(data []byte) (*Node, error) {
	node, err := meaning.ParseYAML(data)
	var syntax *meaning.SyntaxError
	if errors.As(err, &syntax) {
		reworded := *syntax
		reworded.Message = strings.NewReplacer(
			"a meaning file is one document, optionally started by a --- line", "write one document (it may start with a --- line)",
			"a meaning file", "a document",
		).Replace(syntax.Message)
		return node, &reworded
	}
	return node, err
}
