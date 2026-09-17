// Package explore is Explore data (spec/features/explore-data-handoff): the
// intent-first menu that hands database access to DataTug CLI or names
// DataTug.app's honest limitation, never running DataTug itself.
//
// Choosing DataTug CLI checks whether `datatug` is on PATH, writes a
// four-key descriptor with no token under <OVDB home>/explore/datatug/, and
// renders the environment-variable lines, the token command and the exact
// `datatug query run` command for the caller's shell family
// (REQ:prepare-datatug-cli-connection). --no-policies is always in the
// printed command: spike S4 confirmed a fresh DataTug home has no access
// policies, so omitting it fails every newcomer's first run
// (accesspolicies.ErrNoPolicies). Every fact here was proven end to end in
// spike S4 before this package existed.
package explore

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"

	uicopy "github.com/openvaultdb/ovdb/copy"
	"github.com/openvaultdb/ovdb/internal/envelope"
	"github.com/openvaultdb/ovdb/internal/paths"
	"github.com/openvaultdb/ovdb/internal/setup"
)

// TokenEnv is the descriptor's fixed tokenEnv value: datatug-cli derives
// "_BASE_URL" and "_PRINCIPAL_ID" from it (spike S4).
const TokenEnv = "OVDB_DATATUG_TOKEN"

// PrincipalID is the fixed, datatug-cli-local convention spike S4 confirmed:
// OVDB never validates it, only datatug-cli's own destination-binding check
// does (source.go's OpenSource and ExecuteQueryToRecordsReader).
const PrincipalID = "local-owner"

// DefaultCollection is the root collection Explore data suggests trying
// first: the demo's own top-level collection, and a reasonable guess for any
// other database (nested collections are not reachable — REQ table).
const DefaultCollection = "lists"

// ResolveCollection picks the root collection to query (review-inc-7.md
// F5): the demo always defaults to DefaultCollection; any other database
// with exactly one root collection defaults to it; with several, or none
// registered, --collection is required and the error names the choices. A
// value containing "/" is refused: datatug-cli's --from builds a single
// root-collection reference (datatug-cli#256), so a path is silently sent
// as one literal collection name and always comes back empty, never an
// error — the exact trap spike S4 documented.
func ResolveCollection(isDemo bool, requested string, collections []string, db string) (string, *envelope.Error) {
	if strings.Contains(requested, "/") {
		return "", envelope.New(envelope.InvalidArgument, uicopy.T("explore.failed", nil)).
			WithReason(uicopy.T("explore.collection_nested", map[string]string{"collection": requested})).
			WithNext(envelope.Next{Label: uicopy.T("next.help", nil), Command: "ovdb explore datatug-cli --db " + db + " --help"})
	}
	if requested != "" {
		return requested, nil
	}
	if isDemo {
		return DefaultCollection, nil
	}
	if len(collections) == 1 {
		return collections[0], nil
	}
	if len(collections) == 0 {
		return "", envelope.New(envelope.InvalidArgument, uicopy.T("explore.failed", nil)).
			WithReason(uicopy.T("explore.collection_none", nil)).
			WithNext(envelope.Next{Label: uicopy.T("next.help", nil), Command: "ovdb explore datatug-cli --db " + db + " --help"})
	}
	next := make([]envelope.Next, 0, len(collections))
	for _, c := range collections {
		next = append(next, envelope.Next{Label: c, Command: "ovdb explore datatug-cli --db " + db + " --collection " + c})
	}
	return "", envelope.New(envelope.InvalidArgument, uicopy.T("explore.failed", nil)).
		WithReason(uicopy.T("explore.collection_required", map[string]string{"collections": strings.Join(collections, ", ")})).
		WithNext(next...)
}

// InstallCommands are shown when datatug is not on PATH
// (REQ:prepare-datatug-cli-connection).
var InstallCommands = []string{
	"brew tap datatug/tap && brew install datatug",
	"go install github.com/datatug/datatug-cli@latest",
}

// Next actions a presentation offers in place (envelope.Next.Action).
const (
	ActionDataTugCLI = "datatug_cli"
	ActionDataTugApp = "datatug_app"
)

// Descriptor is exactly what datatug-cli's OpenSource accepts: any other
// key, including a token, is a hard error there, so this struct is the only
// place this shape may grow (spike S4).
type Descriptor struct {
	BaseURL     string `json:"baseUrl"`
	DatabaseID  string `json:"databaseId"`
	TokenEnv    string `json:"tokenEnv"`
	PrincipalID string `json:"principalId"`
}

// NewDescriptor builds the descriptor for db at baseURL (REQ:prepare-datatug-cli-connection).
func NewDescriptor(baseURL, db string) Descriptor {
	return Descriptor{BaseURL: baseURL, DatabaseID: db, TokenEnv: TokenEnv, PrincipalID: PrincipalID}
}

// DescriptorPath is where db's descriptor lives under the OVDB home.
func DescriptorPath(home, db string) string {
	return filepath.Join(home, "explore", "datatug", db+".json")
}

// WriteDescriptor writes descriptor to path as an owner-only file, creating
// its owner-only parent directories.
func WriteDescriptor(path string, descriptor Descriptor) error {
	if err := paths.EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	data, err := json.MarshalIndent(descriptor, "", "  ")
	if err != nil {
		return err
	}
	return paths.WriteFilePrivate(path, append(data, '\n'))
}

// LookPath resolves whether name is on PATH; exec.LookPath when nil.
type LookPath func(name string) (string, error)

// OnPath reports whether datatug is on PATH, using lookPath (exec.LookPath
// when nil).
func OnPath(lookPath LookPath) bool {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	_, err := lookPath("datatug")
	return err == nil
}

// EnvLine is one exported/assigned environment variable, rendered for the
// caller's shell family.
type EnvLine struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// tokenPlaceholder is what the token env line shows instead of a real
// secret: the descriptor and this response never carry the token itself
// (REQ:prepare-datatug-cli-connection).
func tokenPlaceholder(tokenCommand string) string {
	return "<paste the token from: " + tokenCommand + ">"
}

// EnvLines is the three environment variables datatug-cli reads, matching
// descriptor byte-for-byte (spike S4: baseUrl and principalId are compared
// as exact strings).
func EnvLines(descriptor Descriptor, tokenCommand string) []EnvLine {
	return []EnvLine{
		{Name: TokenEnv, Value: tokenPlaceholder(tokenCommand)},
		{Name: TokenEnv + "_BASE_URL", Value: descriptor.BaseURL},
		{Name: TokenEnv + "_PRINCIPAL_ID", Value: descriptor.PrincipalID},
	}
}

// ShellText renders lines as goos's export syntax: POSIX `export NAME=value`
// (unquoted for the token placeholder, double-quoted otherwise) or
// PowerShell `$env:NAME = "value"` — the shell family of the OVDB process's
// own runtime.GOOS, with no other detection (plan simplification #12).
func ShellText(goos string, lines []EnvLine) string {
	rendered := make([]string, len(lines))
	for i, line := range lines {
		if goos == "windows" {
			rendered[i] = fmt.Sprintf("$env:%s = %q", line.Name, line.Value)
			continue
		}
		if strings.HasPrefix(line.Value, "<") {
			rendered[i] = fmt.Sprintf("export %s=%s", line.Name, line.Value)
			continue
		}
		rendered[i] = fmt.Sprintf("export %s=%q", line.Name, line.Value)
	}
	return strings.Join(rendered, "\n")
}

// TokenCommand is the command that mints the read-only token Explore data
// names but never mints or prints itself (REQ:prepare-datatug-cli-connection).
func TokenCommand(goos, db string) string {
	return "ovdb token create --db " + shellQuote(goos, db) + " --scope read-only"
}

// safeShellChars are the characters shellQuote leaves bare: a run of only
// these needs no quoting in sh or PowerShell alike.
const safeShellChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789/._-:\\"

// shellQuote single-quotes s for goos's shell unless it is already safe to
// use bare — spike S4's own printed example leaves an ordinary id like
// "lists" or "todo" unquoted — with a real escape for an embedded single
// quote (sh: '\”; PowerShell: ”) rather than leaving it unquoted, which a
// shell would split into extra arguments (review-inc-7.md F7; live:
// --collection 'a b;touch PWNED' printed as --from a b;touch PWNED).
func shellQuote(goos, s string) string {
	if s != "" && strings.Trim(s, safeShellChars) == "" {
		return s
	}
	if goos == "windows" {
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// QueryCommand is the exact `datatug query run` command for goos, with
// --no-policies always present: spike S4 proved a fresh DataTug home has no
// access policies, so a newcomer following the command without it always
// hits accesspolicies.ErrNoPolicies. The --db target is always quoted (spike
// S4's own text), unlike --from/--as, which stay bare for an ordinary value
// exactly as spike S4 shows and are quoted only when shellQuote must.
func QueryCommand(goos, descriptorPath, collection string) string {
	target := quoteAlways(goos, "openvaultdb://"+descriptorPath)
	from := shellQuote(goos, collection)
	if goos == "windows" {
		return fmt.Sprintf("datatug query run --db %s `\n  --from %s --as %s --no-policies --format json",
			target, from, PrincipalID)
	}
	return fmt.Sprintf("datatug query run --db %s \\\n  --from %s --as %s --no-policies --format json",
		target, from, PrincipalID)
}

// quoteAlways single-quotes s for goos's shell unconditionally — the --db
// target is always shown quoted (spike S4), unlike shellQuote's other
// callers, which leave a safe value bare.
func quoteAlways(goos, s string) string {
	if goos == "windows" {
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// DataTugCLI is the body of Choosing DataTug CLI: the four-key descriptor
// (written as a side effect, matching REQ:prepare-datatug-cli-connection),
// the environment-variable lines, the token command and the exact query
// command, plus install guidance when datatug is not found.
type DataTugCLI struct {
	OnPath          bool            `json:"on_path"`
	Collection      string          `json:"collection"`
	DescriptorPath  string          `json:"descriptor_path"`
	Descriptor      Descriptor      `json:"descriptor"`
	EnvLines        []EnvLine       `json:"env_lines"`
	Shell           string          `json:"shell"`
	ShellText       string          `json:"shell_text"`
	TokenCommand    string          `json:"token_command"`
	QueryCommand    string          `json:"query_command"`
	InstallCommands []string        `json:"install_commands,omitempty"`
	Next            []envelope.Next `json:"next"`
}

// Prepare builds and writes the DataTug CLI connection for db (capability
// row 22), on the server at baseURL, with collection (DefaultCollection
// when empty). lookPath checks the calling process's own PATH; a caller
// that instead wants the answer relayed by a different process (the local
// server, over HTTP) should pass nil and let ApplyOnPath override it — see
// review-inc-7.md F2: an agent's or shell's PATH commonly differs from
// whatever PATH started the detached OVDB server, so a check made inside
// the server is not the client's PATH and must never be presented as if it
// were.
func Prepare(lookPath LookPath, home, baseURL, db, collection string) (DataTugCLI, error) {
	if collection == "" {
		collection = DefaultCollection
	}
	descriptor := NewDescriptor(baseURL, db)
	path := DescriptorPath(home, db)
	if err := WriteDescriptor(path, descriptor); err != nil {
		return DataTugCLI{}, err
	}
	tokenCommand := TokenCommand(goruntime.GOOS, db)
	lines := EnvLines(descriptor, tokenCommand)
	result := DataTugCLI{
		Collection: collection, DescriptorPath: path, Descriptor: descriptor, EnvLines: lines,
		Shell: shellFamily(goruntime.GOOS), ShellText: ShellText(goruntime.GOOS, lines),
		TokenCommand: tokenCommand, QueryCommand: QueryCommand(goruntime.GOOS, path, collection),
	}
	ApplyOnPath(&result, OnPath(lookPath))
	return result, nil
}

// ApplyOnPath sets document's OnPath, InstallCommands and Next from onPath,
// the one place both Prepare (the server's own check) and a client
// overriding it with its own process's PATH (F2) compute them, so the two
// can never drift into different wording for the same boolean.
func ApplyOnPath(document *DataTugCLI, onPath bool) {
	document.OnPath = onPath
	document.InstallCommands = nil
	next := []envelope.Next{{Label: uicopy.T("explore.next.create_token", nil), Command: document.TokenCommand}}
	if !onPath {
		document.InstallCommands = InstallCommands
		next = append([]envelope.Next{{Label: uicopy.T("explore.next.install_datatug", nil), Command: InstallCommands[0]}}, next...)
	}
	document.Next = next
}

// CopyText is what a copy action (TUI "c", web's copy button) puts on the
// clipboard for document: the environment variables and the query command,
// in the order they are printed — everything needed once the token
// placeholder is filled in, byte for byte what is shown (review-inc-7.md F4).
func CopyText(document DataTugCLI) string {
	return document.ShellText + "\n" + document.QueryCommand
}

func shellFamily(goos string) string {
	if goos == "windows" {
		return "powershell"
	}
	return "sh"
}

// Menu is the intent-first choice (REQ:intent-first-menu): DataTug CLI and
// DataTug.app, each naming db and what works today, before any file is
// written. is Demo picks the demo-specific DataTug CLI copy
// (REQ:demo-copy-is-specific).
type Menu struct {
	Schema        int    `json:"schema"`
	Database      string `json:"database"`
	IsDemo        bool   `json:"is_demo"`
	DataTugCLIKey string `json:"datatug_cli_description_key"`
	DataTugAppKey string `json:"datatug_app_description_key"`
}

// datatugCLIDescriptionKey is the demo-specific copy for the todo demo
// (REQ:demo-copy-is-specific) or the generic "what works today" line —
// never the option's own label (review-inc-7.md F3: an earlier version of
// this returned "explore.menu.datatug_cli", the label key, so every
// non-demo database's description read as its own heading repeated).
func datatugCLIDescriptionKey(isDemo bool) string {
	if isDemo {
		return "explore.menu.datatug_cli_demo"
	}
	return "explore.menu.datatug_cli_help"
}

// NewMenu builds the menu for db; isDemo is whether db is the installed
// TODO demo (setup.FindDemo). Both description keys name real "what works
// today" copy, never the option's own label.
func NewMenu(db string, isDemo bool) Menu {
	return Menu{
		Schema: envelope.Schema, Database: db, IsDemo: isDemo,
		DataTugCLIKey: datatugCLIDescriptionKey(isDemo), DataTugAppKey: "explore.menu.datatug_app_help",
	}
}

// IsDemo reports whether db is the registered TODO demo among databases.
func IsDemo(home, db string, databases []setup.Database) bool {
	found, ok := setup.FindDemo(home, databases)
	return ok && found.ID == db
}

// AppURL is DataTug.app's public URL (README.md: "Source code for
// DataTug.app (https://datatug.app)").
const AppURL = "https://datatug.app"

// DataTugApp is the body of Choosing DataTug.app (REQ:honest-datatug-app-state):
// the honest limitation, Use DataTug CLI instead and Open DataTug.app — never
// a control implying OVDB can open a database there.
type DataTugApp struct {
	Schema   int             `json:"schema"`
	Database string          `json:"database"`
	URL      string          `json:"url"`
	Next     []envelope.Next `json:"next"`
}

// NewDataTugApp builds the DataTug.app document for db.
func NewDataTugApp(db string) DataTugApp {
	return DataTugApp{
		Schema: envelope.Schema, Database: db, URL: AppURL,
		Next: []envelope.Next{
			{Label: uicopy.T("explore.datatug_app.use_cli_instead", nil), Command: "ovdb explore datatug-cli --db " + db, Action: ActionDataTugCLI},
			{Label: uicopy.T("explore.datatug_app.open", nil), Command: "ovdb explore datatug-app --db " + db, Action: ActionDataTugApp},
		},
	}
}
