package repo

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

const (
	commit = "0123456789abcdef0123456789abcdef01234567"
	blob   = "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391"
)

type reply struct {
	out string
	err error
}

// fakeRunner answers the git calls of a test by their arguments after the flags every call has, and records each
// call with its limit.
type fakeRunner struct {
	t       *testing.T
	replies map[string]reply
	calls   []string
}

func (f *fakeRunner) Run(args []string, limit int) ([]byte, error) {
	f.t.Helper()
	if !slices.Equal(args[:len(gitFlags)], gitFlags) {
		f.t.Errorf("args %q do not start with %q", args, gitFlags)
	}
	key := strings.Join(args[len(gitFlags):], " ")
	f.calls = append(f.calls, fmt.Sprintf("%s [%d]", key, limit))
	r, ok := f.replies[key]
	if !ok {
		f.t.Fatalf("unexpected git call %q", key)
	}
	return []byte(r.out), r.err
}

func newGit(t *testing.T, replies map[string]reply) (*Git, *fakeRunner) {
	run := &fakeRunner{t: t, replies: replies}
	return NewGit(run), run
}

const (
	versionCall = "version"
	versionOut  = "git version 2.54.0 (Apple Git-157)\n"
	whereCall   = "rev-parse --is-bare-repository --show-prefix"
	commitCall  = "rev-parse --verify --quiet HEAD^{commit}"
)

func headed(t *testing.T) (*Git, *fakeRunner, map[string]reply) {
	replies := map[string]reply{versionCall: {out: versionOut}, whereCall: {out: "false\n\n"}, commitCall: {out: commit + "\n"}}
	g, run := newGit(t, replies)
	if id, err := g.Head(); id != commit || err != nil {
		t.Fatalf("Head = %q, %v", id, err)
	}
	return g, run, replies
}

func TestHead(t *testing.T) {
	g, run, _ := headed(t)
	if want := []string{versionCall + " [4096]", whereCall + " [4096]", commitCall + " [4096]"}; !slices.Equal(run.calls, want) {
		t.Errorf("calls %q, want %q", run.calls, want)
	}
	if g.commit != commit {
		t.Errorf("commit %q", g.commit)
	}
	sha256 := strings.Repeat("ab", 32)
	g, _ = newGit(t, map[string]reply{versionCall: {out: versionOut}, whereCall: {out: "false\n\n"}, commitCall: {out: sha256 + "\n"}})
	if id, err := g.Head(); id != sha256 || err != nil {
		t.Errorf("a SHA-256 id: %q, %v", id, err)
	}
}

func TestHeadRefusesWhatItCannotRead(t *testing.T) {
	exit := func(code int) error { return &ExitError{Code: code, Stderr: "fatal: x"} }
	for _, c := range []struct {
		name    string
		replies map[string]reply
		want    func(error) bool
	}{
		{"bare", map[string]reply{whereCall: {out: "true\n\n"}}, func(err error) bool { return err == ErrBare }},
		{"a subdirectory", map[string]reply{whereCall: {out: "false\nsub/\n"}}, func(err error) bool { return err == ErrSubdirectory }},
		{"not a repository", map[string]reply{whereCall: {err: exit(128)}}, func(err error) bool { return err.Error() == "git exited with status 128: fatal: x" }},
		{"odd answer", map[string]reply{whereCall: {out: "maybe\n\n"}}, func(err error) bool { return err == ErrMalformed }},
		{"no answer", map[string]reply{whereCall: {out: ""}}, func(err error) bool { return err == ErrMalformed }},
		{"unborn", map[string]reply{whereCall: {out: "false\n\n"}, commitCall: {err: exit(1)}, "rev-parse --verify --quiet HEAD": {err: exit(1)}}, func(err error) bool { return err == ErrNoCommit }},
		{"another failure", map[string]reply{whereCall: {out: "false\n\n"}, commitCall: {err: exit(128)}}, func(err error) bool { return strings.Contains(err.Error(), "128") }},
		{"a plain failure", map[string]reply{whereCall: {out: "false\n\n"}, commitCall: {err: ErrTooLarge}}, func(err error) bool { return err == ErrTooLarge }},
		{"not an id", map[string]reply{whereCall: {out: "false\n\n"}, commitCall: {out: "HEAD\n"}}, func(err error) bool { return err == ErrMalformed }},
		{"an id in capitals", map[string]reply{whereCall: {out: "false\n\n"}, commitCall: {out: strings.ToUpper(commit) + "\n"}}, func(err error) bool { return err == ErrMalformed }},
		{"no id", map[string]reply{whereCall: {out: "false\n\n"}, commitCall: {out: "\n"}}, func(err error) bool { return err == ErrMalformed }},
	} {
		if _, ok := c.replies[versionCall]; !ok {
			c.replies[versionCall] = reply{out: versionOut}
		}
		g, _ := newGit(t, c.replies)
		id, err := g.Head()
		if id != "" || err == nil || !c.want(err) {
			t.Errorf("%s: Head = %q, %v", c.name, id, err)
		}
	}
}

// What `git ls-tree -z HEAD` printed (git 2.54) for a repository with OVDB.md, a symlink, a directory, an executable and a
// gitlink: a record is `<mode> <type> <id>\t<name>` and a NUL, in git's order. The last record is one that git could print
// for a file whose name has a tab, a backslash and a line break.
const listing = "100644 blob 78981922613b2afb6025042ff6bd878ac1994e85\tOVDB.md\x00" +
	"120000 blob c605fce0155e1896fb303b435a94606be85f9764\tlink\x00" +
	"040000 tree de3cfdfa749a945f64c3e2b166089a1d55c3151f\tmodel\x00" +
	"100755 blob 1a2485251c33a70432394c93fb89330ef214bfc9\trun.sh\x00" +
	"160000 commit 0123456789abcdef0123456789abcdef01234567\tsub\x00" +
	"100644 blob e69de29bb2d1d6434b8b29ae775ad8c2e48c5391\ta\tb\\c\nd\x00"

func TestEntries(t *testing.T) {
	g, run, replies := headed(t)
	replies["ls-tree -z "+commit+" --"] = reply{out: listing}
	replies["ls-tree -z "+commit+":model --"] = reply{out: ""}
	want := []Entry{{"OVDB.md", File}, {"link", Symlink}, {"model", Directory}, {"run.sh", Executable}, {"sub", Submodule}, {"a\tb\\c\nd", File}}
	if got, err := g.Entries(""); err != nil || !slices.Equal(got, want) {
		t.Errorf("Entries = %v, %v", got, err)
	}
	if got, err := g.Entries("model"); err != nil || len(got) != 0 {
		t.Errorf("Entries(model) = %v, %v", got, err)
	}
	if last := run.calls[len(run.calls)-1]; last != "ls-tree -z "+commit+":model -- [4194304]" {
		t.Errorf("last call %q", last)
	}
	for _, dir := range []string{"../x", "/x", "a b", "-x/", "a//b", "a\x00"} {
		before := len(run.calls)
		if _, err := g.Entries(dir); err != ErrMalformed || len(run.calls) != before {
			t.Errorf("Entries(%q) = %v after %d calls", dir, err, len(run.calls)-before)
		}
	}
}

func TestEntriesBeforeHeadAskAboutHEAD(t *testing.T) {
	g, _ := newGit(t, map[string]reply{"ls-tree -z HEAD --": {out: listing}, "cat-file blob HEAD:a": {out: "x"}})
	if got, err := g.Entries(""); err != nil || len(got) != 6 {
		t.Errorf("Entries = %v, %v", got, err)
	}
	if got, err := g.Blob("a", 9); err != nil || string(got) != "x" {
		t.Errorf("Blob = %q, %v", got, err)
	}
}

func TestEntriesPassOnWhatGitSays(t *testing.T) {
	g, _ := newGit(t, map[string]reply{"ls-tree -z HEAD --": {err: ErrTooLarge}})
	if _, got := g.Entries(""); got != ErrTooLarge {
		t.Errorf("err = %v, want %v", got, ErrTooLarge)
	}
	g, _ = newGit(t, map[string]reply{"ls-tree -z HEAD --": {err: &ExitError{Code: 128}}, configCall: {err: &ExitError{Code: 1}}})
	if _, got := g.Entries(""); got != ErrObjectMissing {
		t.Errorf("err = %v, want %v", got, ErrObjectMissing)
	}
	g, _ = newGit(t, map[string]reply{"ls-tree -z HEAD --": {out: "garbage"}})
	if _, err := g.Entries(""); err != ErrMalformed {
		t.Errorf("err = %v", err)
	}
}

func TestBlob(t *testing.T) {
	g, run, replies := headed(t)
	replies["cat-file blob "+commit+":model/a.json"] = reply{out: "{}"}
	if got, err := g.Blob("model/a.json", 100); err != nil || string(got) != "{}" {
		t.Errorf("Blob = %q, %v", got, err)
	}
	if last := run.calls[len(run.calls)-1]; last != "cat-file blob "+commit+":model/a.json [100]" {
		t.Errorf("last call %q", last)
	}
	replies["cat-file blob "+commit+":big"] = reply{err: ErrTooLarge}
	if _, err := g.Blob("big", 1); err != ErrTooLarge {
		t.Errorf("err = %v", err)
	}
	before := len(run.calls)
	for _, path := range []string{"", "../x", "/x", "a b", ":/x", "a:b", "a\nb"} {
		if _, err := g.Blob(path, 1); err != ErrMalformed {
			t.Errorf("Blob(%q) = %v", path, err)
		}
	}
	if len(run.calls) != before {
		t.Error("git was asked about a path it should not see")
	}
}

func TestUncommitted(t *testing.T) {
	g, run, replies := headed(t)
	call := "ls-files -z --cached --others -- "
	replies[call+"a"] = reply{out: "a\x00"}
	replies[call+"b"] = reply{out: ""}
	replies[call+"c"] = reply{err: &ExitError{Code: 128, Stderr: "fatal: this operation must be run in a work tree"}}
	replies[call+"d"] = reply{out: "d/x\x00d/y\x00"}
	for path, want := range map[string]bool{"a": true, "b": false, "c": false, "d": false, "../x": false, "": false} {
		if got := g.Uncommitted(path); got != want {
			t.Errorf("Uncommitted(%q) = %v", path, got)
		}
	}
	if last := run.calls[len(run.calls)-3]; !strings.HasSuffix(last, " [4096]") {
		t.Errorf("call %q", last)
	}
}

func TestParseTreeRefusesWhatIsNotAListing(t *testing.T) {
	const id = blob
	for name, in := range map[string]string{
		"no NUL":                       "100644 blob " + id + "\tx",
		"no tab":                       "100644 blob " + id + " x\x00",
		"an empty name":                "100644 blob " + id + "\t\x00",
		"a slash":                      "100644 blob " + id + "\ta/b\x00",
		"a short mode":                 "10064 blob " + id + "\tx\x00",
		"a mode with 8":                "100648 blob " + id + "\tx\x00",
		"a long mode":                  "1006444 blob " + id + "\tx\x00",
		"a short id":                   "100644 blob " + id[1:] + "\tx\x00",
		"an upper id":                  "100644 blob " + strings.ToUpper(id) + "\tx\x00",
		"a non-hex id":                 "100644 blob " + strings.Replace(id, "e", "g", 1) + "\tx\x00",
		"too few fields":               "100644 " + id + "\tx\x00",
		"too many fields":              "100644 blob " + id + " extra\tx\x00",
		"an unknown type":              "100644 tag " + id + "\tx\x00",
		"a size column":                "100644 blob " + id + "      12\tx\x00",
		"a good record then a bad one": "100644 blob " + id + "\tx\x00100644 blob " + id + "\ty",
	} {
		if got, err := parseTree([]byte(in)); err != ErrMalformed || got != nil {
			t.Errorf("%s: %v, %v", name, got, err)
		}
	}
}

func TestParseTreeKinds(t *testing.T) {
	got, err := parseTree([]byte("100664 blob " + blob + "\told\x00040000 tree " + blob + "\td\x00160000 commit " + blob + "\ts\x00"))
	want := []Entry{{"old", Other}, {"d", Directory}, {"s", Submodule}}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("entries %v, %v", got, err)
	}
	if got, err := parseTree(nil); err != nil || got != nil {
		t.Errorf("nothing: %v, %v", got, err)
	}
}

func TestParseTreeCountsEntries(t *testing.T) {
	record := func(i int) string { return fmt.Sprintf("100644 blob %s\tf%05d\x00", blob, i) }
	var b strings.Builder
	for i := range MaxEntries {
		b.WriteString(record(i))
	}
	if got, err := parseTree([]byte(b.String())); err != nil || len(got) != MaxEntries {
		t.Fatalf("%d entries, %v", len(got), err)
	}
	b.WriteString(record(MaxEntries))
	if got, err := parseTree([]byte(b.String())); err != ErrTooLarge || got != nil {
		t.Errorf("%d entries, %v", len(got), err)
	}
	if b.Len() > MaxTreeBytes {
		t.Errorf("%d entries are %d bytes, over the %d bytes that are read: the bound on entries would never be reached", MaxEntries, b.Len(), MaxTreeBytes)
	}
}

func FuzzParseTree(f *testing.F) {
	f.Add([]byte(listing))
	f.Add([]byte(""))
	f.Add([]byte("100644 blob " + blob + "\tx\x00"))
	f.Add([]byte("040000 tree " + blob + "\t\x00"))
	f.Add([]byte("100644 blob " + blob + "\ta/b\x00"))
	f.Fuzz(func(t *testing.T, in []byte) {
		entries, err := parseTree(in)
		if err != nil {
			if (err != ErrMalformed && err != ErrTooLarge) || entries != nil {
				t.Fatalf("err %v with %d entries", err, len(entries))
			}
			return
		}
		if len(entries) > MaxEntries || len(entries) > len(in)/len("100644 blob \t\x00") {
			t.Fatalf("%d entries from %d bytes", len(entries), len(in))
		}
		for _, e := range entries {
			if e.Name == "" || strings.ContainsAny(e.Name, "/\x00") || e.Kind == Missing {
				t.Fatalf("entry %+v", e)
			}
		}
	})
}

func TestHeadRefusesAGitThatWouldFetch(t *testing.T) {
	for out, want := range map[string]error{
		"git version 2.54.0 (Apple Git-157)\n": nil,
		"git version 2.45.0\n":                 nil,
		"git version 2.44.2\n":                 ErrOldGit,
		"git version 2.44.0\n":                 ErrOldGit,
		"git version 2.45.1.windows.1\n":       nil,
		"git version 3.0.0\n":                  nil,
		"git version 2.43.5\n":                 ErrOldGit,
		"git version 2.9.1\n":                  ErrOldGit,
		"git version 1.99.0\n":                 ErrOldGit,
		"git version\n":                        ErrMalformed,
		"":                                     ErrMalformed,
		"git version two\n":                    ErrMalformed,
		"git version 2\n":                      ErrMalformed,
		"hello 2.54.0\n":                       ErrMalformed,
	} {
		g, _ := newGit(t, map[string]reply{versionCall: {out: out}, whereCall: {out: "true\n\n"}})
		_, err := g.Head()
		if want == nil {
			want = ErrBare // the version was accepted: the next call decides
		}
		if err != want {
			t.Errorf("%q: Head = %v, want %v", out, err, want)
		}
	}
	g, _ := newGit(t, map[string]reply{versionCall: {err: &ExitError{Code: 1}}})
	if _, err := g.Head(); err == nil || err == ErrOldGit {
		t.Errorf("a git that cannot say its version: %v", err)
	}
}

func TestHeadTellsAnUnbornBranchFromAnObjectThatCannotBeRead(t *testing.T) {
	const verify = "rev-parse --verify --quiet HEAD"
	failed := &ExitError{Code: 1, Full: "error: unable to normalize alternate object path: /gone\n"}
	for name, c := range map[string]struct {
		named   reply
		config  reply
		wantErr error
	}{
		"unborn":                {named: reply{err: &ExitError{Code: 1}}, wantErr: ErrNoCommit},
		"a commit that is gone": {named: reply{out: commit + "\n"}, config: reply{err: &ExitError{Code: 1}}, wantErr: ErrObjectMissing},
		"a promisor remote":     {named: reply{out: commit + "\n"}, config: reply{out: "extensions.partialclone origin\n"}, wantErr: ErrPartialClone},
	} {
		g, _ := newGit(t, map[string]reply{versionCall: {out: versionOut}, whereCall: {out: "false\n\n"}, commitCall: {err: &ExitError{Code: 1}}, verify: c.named, configCall: c.config})
		if _, err := g.Head(); err != c.wantErr {
			t.Errorf("%s: Head = %v, want %v", name, err, c.wantErr)
		}
	}
	g, _ := newGit(t, map[string]reply{versionCall: {out: versionOut}, whereCall: {out: "false\n\n"}, commitCall: {err: failed}, verify: {out: commit + "\n"}, configCall: {err: &ExitError{Code: 1}}})
	if _, err := g.Head(); err != ErrAlternates {
		t.Errorf("alternates: Head = %v", err)
	}
}

const configCall = `config --local --get-regexp ^(extensions\.partialclone|remote\..*\.promisor)$`

// The reasons that git cannot read an object, told apart: what git printed in the C locale (taken from git 2.54), and whether the
// repository is a partial clone.
func TestAnObjectThatCannotBeReadIsExplained(t *testing.T) {
	for name, c := range map[string]struct {
		stderr string
		config reply
		want   error
	}{
		"a missing blob":               {"fatal: git cat-file abc:OVDB.md: bad file\n", reply{err: &ExitError{Code: 1}}, ErrObjectMissing},
		"a missing tree":               {"fatal: not a tree object\n", reply{err: &ExitError{Code: 1}}, ErrObjectMissing},
		"an unknown object":            {"fatal: Not a valid object name abc:model\n", reply{err: &ExitError{Code: 1}}, ErrObjectMissing},
		"a damaged blob":               {"error: inflate: data stream error (incorrect header check)\nerror: unable to unpack ce01 header\nfatal: loose object ce01 (stored in .git/objects/ce/01) is corrupt\n", reply{err: &ExitError{Code: 1}}, ErrObjectCorrupt},
		"an empty object file":         {"error: object file .git/objects/ce/01 is empty\nfatal: git cat-file abc:OVDB.md: bad file\n", reply{err: &ExitError{Code: 1}}, ErrObjectCorrupt},
		"alternates":                   {"error: unable to normalize alternate object path: /gone/.git/objects\nfatal: not a tree object\n", reply{err: &ExitError{Code: 1}}, ErrAlternates},
		"a partial clone":              {"fatal: git cat-file abc:OVDB.md: bad file\n", reply{out: "extensions.partialclone origin\n"}, ErrPartialClone},
		"a promisor remote":            {"fatal: not a tree object\n", reply{out: "remote.origin.promisor true\n"}, ErrPartialClone},
		"a config that cannot be read": {"fatal: not a tree object\n", reply{err: &ExitError{Code: 128}}, ErrObjectMissing},
		"an empty answer":              {"fatal: not a tree object\n", reply{out: "\n"}, ErrObjectMissing},
	} {
		replies := map[string]reply{configCall: c.config, "cat-file blob HEAD:a": {err: &ExitError{Code: 128, Full: c.stderr}}, "ls-tree -z HEAD --": {err: &ExitError{Code: 128, Full: c.stderr}}}
		g, _ := newGit(t, replies)
		if _, err := g.Blob("a", 10); err != c.want {
			t.Errorf("%s: Blob = %v, want %v", name, err, c.want)
		}
		if _, err := g.Entries(""); err != c.want {
			t.Errorf("%s: Entries = %v, want %v", name, err, c.want)
		}
	}
}

// A commit that git cannot read: it names a reason when git says one, and is otherwise what git said.
func TestHeadExplainsACommitThatCannotBeRead(t *testing.T) {
	garbage := &ExitError{Code: 128, Full: "error: inflate: data stream error (incorrect header check)\nerror: unable to unpack 2fe3 header\nfatal: loose object 2fe3 (stored in .git/objects/2f/e3) is corrupt\n", Stderr: "fatal: loose object"}
	g, _ := newGit(t, map[string]reply{versionCall: {out: versionOut}, whereCall: {out: "false\n\n"}, commitCall: {err: garbage}})
	if _, err := g.Head(); err != ErrObjectCorrupt {
		t.Errorf("a garbage commit: Head = %v", err)
	}
	other := &ExitError{Code: 128, Full: "fatal: something else\n", Stderr: "fatal: something else"}
	g, _ = newGit(t, map[string]reply{versionCall: {out: versionOut}, whereCall: {out: "false\n\n"}, commitCall: {err: other}})
	if _, err := g.Head(); err != other {
		t.Errorf("a failure that names no reason: Head = %v", err)
	}
}
