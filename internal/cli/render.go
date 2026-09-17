package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
)

// Render prints err when it is an envelope error and reports whether it did:
// the JSON document on stdout when args ask for --json, otherwise the
// problem pattern on stderr (first-run-onboarding#REQ:problem-pattern).
// main calls it before fang's own error output, so legacy commands keep
// theirs. args are the raw arguments, because a flag error happens before
// --json itself is parsed.
func Render(err error, args []string, stdout, stderr io.Writer) bool {
	e := envelope.As(err)
	if e == nil {
		return false
	}
	if wantsJSON(args) {
		_, _ = stdout.Write(envelope.MarshalError(e))
		return true
	}
	_, _ = fmt.Fprintln(stderr, e.Message)
	if e.Reason != "" {
		_, _ = fmt.Fprintln(stderr)
		_, _ = fmt.Fprintln(stderr, uicopy.T("problem.why", map[string]string{"reason": e.Reason}))
	}
	if len(e.Next) > 0 {
		_, _ = fmt.Fprintln(stderr)
		_, _ = fmt.Fprintln(stderr, uicopy.T("problem.what_you_can_do", nil))
		writeNext(stderr, e.Next)
	}
	return true
}

func wantsJSON(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "--json" || arg == "--json=true" {
			return true
		}
	}
	return false
}

// writeNext prints next actions as "  • label   command" with commands
// aligned; a label too long to share a line puts its command on the next.
func writeNext(w io.Writer, next []envelope.Next) {
	const maxAligned = 40
	width := 0
	for _, n := range next {
		if length := len([]rune(n.Label)); length <= maxAligned {
			width = max(width, length)
		}
	}
	for _, n := range next {
		length := len([]rune(n.Label))
		switch {
		case n.Command == "":
			_, _ = fmt.Fprintf(w, "  • %s\n", n.Label)
		case length > maxAligned:
			_, _ = fmt.Fprintf(w, "  • %s\n      %s\n", n.Label, n.Command)
		default:
			_, _ = fmt.Fprintf(w, "  • %s%s   %s\n", n.Label, strings.Repeat(" ", width-length), n.Command)
		}
	}
}

// printer writes one command's output: the API document with --json,
// human lines otherwise.
type printer struct {
	cmd  *cobra.Command
	json bool
}

func (p printer) document(body []byte, human func(w io.Writer)) {
	if p.json {
		_, _ = p.cmd.OutOrStdout().Write(body)
		return
	}
	human(p.cmd.OutOrStdout())
}

// say prints one line. Callers pass uicopy.T(...) with a literal key so the
// catalogue test sees every key.
func say(w io.Writer, text string) {
	_, _ = fmt.Fprintln(w, text)
}
