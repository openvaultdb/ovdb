package repo

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/openvaultdb/ovdb/internal/publisher/rules"
)

// Runner runs git in one repository, with the arguments after git's own -C option, and
// returns what it printed on standard output. It reads at most limit bytes: more is
// ErrTooLarge, and git is stopped. A git that exits with a failure is an *ExitError.
// ExecRunner is the one that runs the real git; the tests of Git fake it.
type Runner interface {
	Run(args []string, limit int) ([]byte, error)
}

// maxSmall is the most bytes read from a git call whose answer is a line or a path.
const maxSmall = 4096

// Git is a Reader that asks git, through a Runner. It reads the commit that HEAD names
// when Head is called, by its id, never the working tree; every path it hands to git
// is checked first, and follows a commit id, so git never reads one as an option.
type Git struct {
	run    Runner
	commit string
}

// NewGit returns a Git that runs git through run.
func NewGit(run Runner) *Git { return &Git{run: run} }

// gitFlags go before every git command: no command of the repository's own configuration (a file
// system monitor, which `ls-files` runs when the untracked cache is on) is run. There is no
// --literal-pathspecs: the only pathspec given is a path that rules.IsRepositoryPath has accepted, and
// that has no character that pathspec magic uses (: * ? [ \), so it could change nothing.
var gitFlags = []string{"-c", "core.fsmonitor=false"}

func (g *Git) git(limit int, args ...string) ([]byte, error) {
	return g.run.Run(append(append([]string(nil), gitFlags...), args...), limit)
}

// Head finds the commit of HEAD, and refuses a git older than MinGit, a bare repository and a directory that is not the top of its repository.
func (g *Git) Head() (string, error) {
	version, err := g.git(maxSmall, "version")
	if err != nil {
		return "", err
	}
	if err := checkVersion(string(version)); err != nil {
		return "", err
	}
	where, err := g.git(maxSmall, "rev-parse", "--is-bare-repository", "--show-prefix")
	if err != nil {
		return "", err
	}
	bare, prefix, _ := strings.Cut(string(where), "\n")
	switch {
	case bare != "true" && bare != "false":
		return "", ErrMalformed
	case bare == "true":
		return "", ErrBare
	case strings.TrimSpace(prefix) != "":
		return "", ErrSubdirectory
	}
	out, err := g.git(maxSmall, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	var exit *ExitError
	if errors.As(err, &exit) && exit.Code == 1 {
		// Exit 1 says HEAD names no commit: an unborn branch, or an object that cannot be read as one.
		if _, named := g.git(maxSmall, "rev-parse", "--verify", "--quiet", "HEAD"); named != nil {
			return "", ErrNoCommit
		}
		return "", g.unreadable(err)
	} else if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(out))
	if !isID(id) {
		return "", ErrMalformed
	}
	g.commit = id
	return id, nil
}

// isID reports whether s is a git object id: 40 or 64 lower-case hexadecimal digits.
func isID(s string) bool {
	return (len(s) == 40 || len(s) == 64) && strings.Trim(s, "0123456789abcdef") == ""
}

// Entries lists a directory of the commit.
func (g *Git) Entries(dir string) ([]Entry, error) {
	rev := cmp.Or(g.commit, "HEAD")
	if dir != "" {
		if !rules.IsRepositoryPath(dir) {
			return nil, ErrMalformed
		}
		rev += ":" + dir
	}
	out, err := g.git(MaxTreeBytes, "ls-tree", "-z", rev, "--")
	if err != nil {
		return nil, g.unreadable(err)
	}
	return parseTree(out)
}

// Blob reads a file of the commit.
func (g *Git) Blob(path string, limit int) ([]byte, error) {
	if !rules.IsRepositoryPath(path) {
		return nil, ErrMalformed
	}
	out, err := g.git(limit, "cat-file", "blob", cmp.Or(g.commit, "HEAD")+":"+path)
	return out, g.unreadable(err)
}

// checkVersion refuses a `git version` output that is older than MinGit, or not one.
func checkVersion(out string) error {
	fields := strings.Fields(strings.TrimPrefix(out, "git version "))
	if len(fields) == 0 {
		return ErrMalformed
	}
	var major, minor int
	if n, _ := fmt.Sscanf(fields[0], "%d.%d", &major, &minor); n != 2 {
		return ErrMalformed
	}
	var wantMajor, wantMinor int
	_, _ = fmt.Sscanf(MinGit, "%d.%d", &wantMajor, &wantMinor)
	if major < wantMajor || (major == wantMajor && minor < wantMinor) {
		return ErrOldGit
	}
	return nil
}

// unreadable says why git could not read an object that the commit has: only a failure of git is explained (the path is known to be
// there, because Entries said so, so the failure is the object's), and what is not one is passed on. The reasons are told from the
// repository's configuration and from what git printed, in git's own C locale.
func (g *Git) unreadable(err error) error {
	var exit *ExitError
	if !errors.As(err, &exit) {
		return err
	}
	if out, listed := g.git(maxSmall, "config", "--local", "--get-regexp", `^(extensions\.partialclone|remote\..*\.promisor)$`); listed == nil && len(bytes.TrimSpace(out)) > 0 {
		return ErrPartialClone
	}
	said := strings.ToLower(exit.Full)
	switch {
	case strings.Contains(said, "alternate"):
		return ErrAlternates
	case containsAny(said, "corrupt", "inflate", "unable to unpack", "is empty", "hash mismatch", "bad object"):
		return ErrObjectCorrupt
	}
	return ErrObjectMissing
}

func containsAny(s string, parts ...string) bool {
	return slices.ContainsFunc(parts, func(p string) bool { return strings.Contains(s, p) })
}

// Uncommitted asks the index and the working tree; a bare repository has neither.
func (g *Git) Uncommitted(path string) bool {
	if !rules.IsRepositoryPath(path) {
		return false
	}
	out, err := g.git(maxSmall, "ls-files", "-z", "--cached", "--others", "--", path)
	return err == nil && string(out) == path+"\x00"
}

// parseTree reads the output of `git ls-tree -z`: records of
// `<mode> <type> <id>\t<name>` each ended by a NUL. Anything else is ErrMalformed, and
// more than MaxEntries records is ErrTooLarge. The names are returned as they are, but
// never empty and never with a slash or a NUL: they are not otherwise judged here.
func parseTree(out []byte) ([]Entry, error) {
	var entries []Entry
	for len(out) > 0 {
		record, rest, ok := bytes.Cut(out, []byte{0})
		if !ok {
			return nil, ErrMalformed
		}
		out = rest
		meta, name, ok := bytes.Cut(record, []byte{'\t'})
		fields := strings.Split(string(meta), " ")
		if !ok || len(name) == 0 || bytes.IndexByte(name, '/') >= 0 || len(fields) != 3 || !isMode(fields[0]) || !isID(fields[2]) {
			return nil, ErrMalformed
		}
		kind, ok := kindOf(fields[0], fields[1])
		if !ok {
			return nil, ErrMalformed
		}
		if len(entries) == MaxEntries {
			return nil, ErrTooLarge
		}
		entries = append(entries, Entry{Name: string(name), Kind: kind})
	}
	return entries, nil
}

// isMode reports whether s is a git file mode: six octal digits.
func isMode(s string) bool { return len(s) == 6 && strings.Trim(s, "01234567") == "" }

// kindOf is the kind of an entry of the given mode and type; false for a type git does not have.
func kindOf(mode, typ string) (Kind, bool) {
	switch typ {
	case "tree":
		return Directory, true
	case "commit":
		return Submodule, true
	case "blob":
		switch mode {
		case "100644":
			return File, true
		case "100755":
			return Executable, true
		case "120000":
			return Symlink, true
		}
		return Other, true
	}
	return Other, false
}
