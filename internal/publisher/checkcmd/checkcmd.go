// Package checkcmd is `ovdb publisher check`: it checks the Git repository in a directory, as committed at HEAD, with the Publisher profile of
// package repo, and prints what it finds for a person or, with --json, as a document for a CI job. It reads no file of the working tree and never
// uses the network: the one thing it runs is git, through repo's reader. The command is not part of the parity matrix (it has no TUI, web or API surface).
package checkcmd

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/openvaultdb/ovdb/internal/publisher/manifest"
	"github.com/openvaultdb/ovdb/internal/publisher/repo"
	"github.com/openvaultdb/ovdb/internal/publisher/rules"
)

// ErrRefused is what the command returns when the check ran and the repository is refused: exit code 1, and nothing more to print (the findings are the
// output). main's error handler stays silent for it.
var ErrRefused = errors.New("the repository is refused")

// Deps are the seams of the command: what it reads from the machine.
type Deps struct {
	// Open returns the reader of the repository in dir (git, run through repo.ExecRunner, in the real command).
	Open func(dir string) repo.Reader
	// Stat is os.Stat in the real command.
	Stat func(name string) (fs.FileInfo, error)
	// T is the copy catalogue (uicopy.T): every word the command says is a key of copy/en.json, so that text lives in one place. The keys the command uses
	// are literals of this package, and a test holds each to the catalogue.
	T func(key string, params map[string]string) string
	// Usage makes the error of a usage mistake (the shared error envelope with exit code 2), Unrunnable the error of an environment that cannot run the
	// check (the same, for a missing tool: exit code 2 too), and JSON writes a schema-1 document, as every ovdb --json document is written.
	Usage      func(cmd *cobra.Command, reason string) error
	Unrunnable func(message, reason, next string) error
	JSON       func(v any) []byte
	// WriteFailed makes the error for a result that could not be written to standard output (a closed or full pipe): exit code 2, the check could not
	// deliver what it was asked for, and a pass that printed nothing must not look like one.
	WriteFailed func(reason string) error
}

// Real is the real command's seams for the machine (git and the file system); the caller adds the catalogue and the error envelope.
func Real() Deps {
	return Deps{
		Open: func(dir string) repo.Reader { return repo.NewGit(repo.ExecRunner{Dir: dir}) },
		Stat: os.Stat,
	}
}

// NewCmd is the `publisher` group, with `check` in it.
func NewCmd(d Deps) *cobra.Command {
	c := command{d}
	group := &cobra.Command{
		Use:   "publisher",
		Short: c.T("publisher.short", nil),
		Args:  c.noArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	group.SetFlagErrorFunc(c.flagError)
	group.AddCommand(newCheckCmd(c))
	return group
}

// command is the command with its seams.
type command struct{ Deps }

func newCheckCmd(c command) *cobra.Command {
	var repository string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "check [path]",
		Short: c.T("publisher.check.short", nil),
		Long:  c.T("publisher.check.long", nil),
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return c.Usage(cmd, c.T("publisher.usage.too_many", map[string]string{"count": strconv.Itoa(len(args))}))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return c.run(cmd, dir, repository, cmd.Flags().Changed("repository"), jsonOut)
		},
	}
	cmd.SetFlagErrorFunc(c.flagError)
	cmd.Flags().StringVar(&repository, "repository", "", c.T("publisher.flag.repository", nil))
	cmd.Flags().BoolVar(&jsonOut, "json", false, c.T("publisher.flag.json", nil))
	return cmd
}

func (c command) noArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	return c.Usage(cmd, c.T("publisher.usage.unexpected", map[string]string{"args": safe(strings.Join(args, " "))}))
}

func (c command) flagError(cmd *cobra.Command, err error) error {
	return c.Usage(cmd, safe(err.Error()))
}

// pinned remembers the commit that Head gave, and the first error that says git could not be run or is too old, from any call: such an error gives no
// verdict about the repository (git that is missing, that is older than the check needs, or that did not finish in time).
type pinned struct {
	repo.Reader
	commit     string
	unrunnable error
}

func (p *pinned) note(err error) error {
	if p.unrunnable == nil && (errors.Is(err, repo.ErrCannotRun) || errors.Is(err, repo.ErrOldGit)) {
		p.unrunnable = err
	}
	return err
}

func (p *pinned) Head() (string, error) {
	commit, err := p.Reader.Head()
	if err == nil && p.commit == "" {
		p.commit = commit
	}
	return commit, p.note(err)
}

func (p *pinned) Entries(dir string) ([]repo.Entry, error) {
	entries, err := p.Reader.Entries(dir)
	return entries, p.note(err)
}

func (p *pinned) Blob(path string, limit int) ([]byte, error) {
	data, err := p.Reader.Blob(path, limit)
	return data, p.note(err)
}

// errWriter remembers the first error of a write.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) Write(p []byte) (int, error) {
	n, err := e.w.Write(p)
	if err != nil && e.err == nil {
		e.err = err
	}
	return n, err
}

func (c command) run(cmd *cobra.Command, dir, repository string, haveRepository, jsonOut bool) error {
	if dir == "" {
		return c.Usage(cmd, c.T("publisher.usage.no_path", map[string]string{"path": `""`}))
	}
	info, err := c.Stat(dir)
	switch {
	case err != nil:
		return c.Usage(cmd, c.T("publisher.usage.no_path", map[string]string{"path": safe(dir)}))
	case !info.IsDir():
		return c.Usage(cmd, c.T("publisher.usage.not_dir", map[string]string{"path": safe(dir)}))
	}
	opts := repo.Options{Profile: manifest.Publisher}
	if haveRepository {
		if _, ok := rules.RepositoryKey(repository); !ok {
			return c.Usage(cmd, c.T("publisher.usage.bad_repository", map[string]string{"value": safe(repository)}))
		}
		opts.Repository = &repository
	}
	reader := &pinned{Reader: c.Open(dir)}
	result := repo.Check(reader, opts)
	// Git that cannot be run, that is too old or that did not finish gives no verdict about the repository: the check could not run as asked, which is exit
	// code 2.
	if errors.Is(reader.unrunnable, repo.ErrTimeout) {
		return c.Unrunnable(c.T("publisher.env.timeout", nil), c.T("publisher.env.timeout_reason", nil), c.T("publisher.env.timeout_next", nil))
	}
	if reader.unrunnable != nil {
		return c.Unrunnable(c.T("publisher.env.failed", nil), safe(reader.unrunnable.Error()), c.T("publisher.env.next", nil))
	}
	doc := newDocument(reader.commit, result)
	out := &errWriter{w: cmd.OutOrStdout()}
	if jsonOut {
		_, _ = out.Write(c.JSON(doc))
	} else {
		c.writeHuman(out, doc)
	}
	if out.err != nil {
		return c.WriteFailed(safe(out.err.Error()))
	}
	if !result.OK() {
		return ErrRefused
	}
	return nil
}

// MaxHumanBytes and MaxJSONBytes are the most that one run prints, with a margin: at most manifest.MaxFindings findings and the notice that says more were
// left out (TestTheLargestOutputIsBounded builds that case).
const (
	MaxHumanBytes = 150 << 10
	MaxJSONBytes  = 1 << 20
)

// Document is the --json document, schema version 1 (envelope.Schema, as every ovdb document).
type Document struct {
	Schema    int       `json:"schema"`
	Command   string    `json:"command"`
	Commit    string    `json:"commit"`
	Profile   string    `json:"profile"`
	OK        bool      `json:"ok"`
	Manifests int       `json:"manifests"`
	Findings  []Finding `json:"findings"`
	Summary   Summary   `json:"summary"`
}

// Finding is one finding: Path is the file the finding is about, "repository" when it is about the repository as a whole.
type Finding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Message  string `json:"message"`
}

// Summary counts the findings of each severity.
//
// Errors counts the findings, not the notice that says more were left out. Capped is true when the check left findings out (at most manifest.MaxFindings
// are reported) and Omitted says how many.
type Summary struct {
	Errors  int  `json:"errors"`
	Capped  bool `json:"capped"`
	Omitted int  `json:"omitted"`
}

var omittedCount = regexp.MustCompile(`^([0-9]+) more findings are not shown`)

func newDocument(commit string, r manifest.Result) Document {
	doc := Document{Schema: 1, Command: "publisher check", Commit: commit, Profile: "publisher", OK: r.OK(), Manifests: len(r.OVDBMd.Entries), Findings: []Finding{}}
	for _, f := range r.Findings {
		doc.Findings = append(doc.Findings, Finding{Rule: f.Rule, Severity: string(f.Severity), Path: f.Document, Line: f.Line, Message: f.Message})
		switch {
		case f.Rule == manifest.RuleCapped:
			doc.Summary.Capped = true
			if m := omittedCount.FindStringSubmatch(f.Message); m != nil {
				doc.Summary.Omitted, _ = strconv.Atoi(m[1])
			}
		case f.Severity == manifest.SeverityError:
			doc.Summary.Errors++
		}
	}
	return doc
}

// writeHuman prints the findings, one block each in the order repo.Check returns them, and the summary.
func (c command) writeHuman(w io.Writer, doc Document) {
	for _, f := range doc.Findings {
		if f.Rule == manifest.RuleCapped {
			continue // the summary says it
		}
		where := safe(f.Path)
		if f.Line > 0 {
			where += ":" + strconv.Itoa(f.Line)
		}
		// The whole message a rule wrote (the rules bound it, and put what to do last), escaped; only a message longer than the rules allow is cut.
		message := safeN(f.Message, manifest.MaxMessageBytes)
		if message == "" {
			message = c.T("publisher.finding.no_message", nil)
		}
		say(w, where+"  ["+safe(f.Rule)+"]")
		say(w, "  "+message)
		say(w, "")
	}
	params := map[string]string{"commit": shortCommit(doc.Commit), "count": strconv.Itoa(doc.Summary.Errors), "manifests": strconv.Itoa(doc.Manifests),
		"omitted": strconv.Itoa(doc.Summary.Omitted), "max": strconv.Itoa(manifest.MaxFindings)}
	switch {
	case !doc.OK && unreadableObjects(doc):
		// Committing does not help here, whether or not the commit id is known.
		say(w, c.T("publisher.check.state.damaged", params))
	case !doc.OK && doc.Commit == "":
		say(w, c.noCommitSummary(doc, params))
	case !doc.OK && doc.Summary.Capped:
		say(w, c.T("publisher.check.refused_capped", params))
	case !doc.OK && doc.Summary.Errors == 1:
		say(w, c.T("publisher.check.refused_one", params))
	case !doc.OK:
		say(w, c.T("publisher.check.refused_many", params))
	case doc.Manifests == 1:
		say(w, c.T("publisher.check.ok_one", params))
		say(w, c.T("publisher.check.ok_note", nil))
	default:
		say(w, c.T("publisher.check.ok_many", params))
		say(w, c.T("publisher.check.ok_note", nil))
	}
}

// unreadableObjects reports whether a finding says that git could not read an object of the commit: a partial clone, a missing or damaged object, borrowed
// objects that are gone. The remedy is a complete clone, not a commit.
func unreadableObjects(doc Document) bool {
	for _, f := range doc.Findings {
		switch f.Rule {
		case "repo-partial-clone", "repo-object-missing", "repo-object-corrupt", "repo-alternates":
			return true
		}
	}
	return false
}

// noCommitSummary is the summary of a refusal that has no commit id to show: it says what was found instead, by the rule of the finding that made the
// repository unreadable.
func (c command) noCommitSummary(doc Document, params map[string]string) string {
	rule := ""
	if len(doc.Findings) > 0 {
		rule = doc.Findings[0].Rule
	}
	switch rule {
	case "repo-no-commit":
		return c.T("publisher.check.state.no_commit", params)
	case "repo-bare":
		return c.T("publisher.check.state.bare", params)
	case "repo-subdirectory":
		return c.T("publisher.check.state.subdirectory", params)
	case "repo-unreadable":
		return c.T("publisher.check.state.unreadable", params)
	}
	return c.T("publisher.check.state.other", params)
}

func say(w io.Writer, text string) { _, _ = io.WriteString(w, text+"\n") }

func shortCommit(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

// safe makes a string from a repository, a command line or a message printable: printable ASCII stays, every other character is shown as an escape
// (\x1b, \u00e9), and a long one is cut. Nothing that came from outside reaches the terminal otherwise.
func safe(s string) string { return safeN(s, 200) }

// safeN is safe with the length at which a string is cut (and the cut shown by "...").
func safeN(s string, limit int) string {
	cut := len(s) > limit
	if cut {
		s = s[:limit]
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r >= 0x20 && r <= 0x7e:
			b.WriteRune(r)
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r < 0x80:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r > 0xffff:
			fmt.Fprintf(&b, `\U%08x`, r)
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
		i += size
	}
	if cut {
		b.WriteString("...")
	}
	return b.String()
}
