package repo

import (
	"errors"
	"strconv"
	"strings"
)

// Kind is what a path is in the commit that is read.
type Kind int

const (
	// Missing: the commit holds nothing at the path.
	Missing Kind = iota
	// File is a regular file (git mode 100644).
	File
	// Executable is a regular file with the executable bit (100755). The Chinook
	// checker counts it as a file, and so does this package.
	Executable
	// Symlink is a symbolic link (120000). It is never followed.
	Symlink
	// Submodule is a gitlink (160000). It is never entered.
	Submodule
	// Directory is a tree (040000).
	Directory
	// Other is anything git prints that is none of these, such as a blob with an
	// old mode (100664): not a regular file.
	Other
)

var kindNames = map[Kind]string{
	Missing: "missing", File: "a regular file", Executable: "an executable file", Symlink: "a symlink",
	Submodule: "a submodule", Directory: "a directory", Other: "not a regular file",
}

func (k Kind) String() string { return kindNames[k] }

// Regular reports whether the path is a regular file, which is all a manifest may name.
func (k Kind) Regular() bool { return k == File || k == Executable }

// Entry is one name in a directory of the commit, as a Reader reports it.
type Entry struct {
	Name string // one path component
	Kind Kind
}

// The limits on what a Reader hands over; a repository is hostile input.
const (
	// MaxEntries is the most entries one directory may have.
	MaxEntries = 50_000
	// MaxTreeBytes is the most bytes of git's listing of one directory that are read.
	MaxTreeBytes = 4 << 20
	// MaxFileBytes is the most bytes of a file that a manifest names (the model and the meaning file) that are read. The checker reads 16 MiB.
	MaxFileBytes = 4 << 20
	// MinGit is the first git that has GIT_NO_LAZY_FETCH: an older one fetches a missing object of a partial clone, from a remote whose command the repository chooses.
	MinGit = "2.45"
)

// What a Reader may report, besides what the operating system reports.
var (
	// ErrNoCommit: the repository has no commit (an unborn branch).
	ErrNoCommit = errors.New("the repository has no commit yet: commit OVDB.md and the manifests it lists")
	// ErrBare: the repository is bare. The Chinook checker reads a file as HEAD:./path, which git refuses outside a working tree, so it refuses a bare repository; so does this check.
	ErrBare = errors.New("the repository is bare: check a clone with a working tree")
	// ErrSubdirectory: the directory is inside a repository, not at its top.
	ErrSubdirectory = errors.New("the directory is inside a repository, not its top: run the check at the top, where the Directory reads OVDB.md")
	// ErrTooLarge: what was asked for is larger than the limit.
	ErrTooLarge = errors.New("larger than this check reads")
	// ErrPartialClone: the repository is a partial clone and an object that the commit needs is not in it. The checker's git fetches it; this check never does.
	ErrPartialClone = errors.New("this is a partial clone and an object the commit needs is not in it, and this check does not fetch: run the check in a full clone, or fetch the files first (git checkout fetches them)")
	// ErrObjectMissing: an object of the commit is not in the repository.
	ErrObjectMissing = errors.New("an object of the commit is missing from this repository: fetch it, or run the check in a complete clone")
	// ErrObjectCorrupt: an object of the commit is damaged.
	ErrObjectCorrupt = errors.New("an object of the commit is damaged in this repository: run the check in a clone made again from the remote")
	// ErrAlternates: the repository borrows its objects from a directory that is not there.
	ErrAlternates = errors.New("this repository borrows objects from another directory that is not there (objects/info/alternates): run the check in a complete clone")
	// ErrOldGit: git is older than MinGit.
	ErrOldGit = errors.New("git is older than " + MinGit + ", which is the first that can be told never to fetch a missing object (GIT_NO_LAZY_FETCH): update git")
	// ErrCannotRun: git could not be started (it is not installed, or not where the PATH says).
	ErrCannotRun = errors.New("git could not be run")
	// ErrMalformed: git's output is not in the form that is read.
	ErrMalformed = errors.New("git's output is not in a form this check reads")
)

// Reader is a repository as the check sees it: one commit, read as committed and never
// from the working tree. Head is called first, and pins the commit that the other
// methods read. A Reader never follows a symlink and never enters a submodule.
type Reader interface {
	// Head returns the commit, or ErrNoCommit, ErrSubdirectory, or any other error that
	// says why the repository cannot be read at all.
	Head() (string, error)
	// Entries lists the names directly in directory dir of the commit ("" is the top).
	// More than MaxEntries is ErrTooLarge. The names are as git has them: the caller
	// does not trust them.
	Entries(dir string) ([]Entry, error)
	// Blob returns the contents of the regular file at path, or ErrTooLarge when they
	// are more than limit bytes.
	Blob(path string, limit int) ([]byte, error)
	// Uncommitted reports whether the working tree or the index holds path though the
	// commit does not. It only improves a message.
	Uncommitted(path string) bool
}

// ascii makes a message of an error or of git's text safe to print: printable ASCII, short.
func ascii(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	q := strconv.QuoteToASCII(s)
	q = q[1 : len(q)-1]
	// QuoteToASCII writes " as \": a quote is printable, and the escape reads as a stray backslash (exec: \"git\": executable file not found).
	var b strings.Builder
	for i := 0; i < len(q); i++ {
		switch {
		case q[i] == '\\' && i+1 < len(q) && q[i+1] == '"':
			b.WriteByte('"')
			i++
		case q[i] == '\\' && i+1 < len(q):
			b.WriteByte('\\')
			b.WriteByte(q[i+1])
			i++
		default:
			b.WriteByte(q[i])
		}
	}
	return b.String()
}
