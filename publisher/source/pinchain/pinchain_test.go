package pinchain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) (pins, map[string][]byte) {
	t.Helper()
	var p pins
	if err := json.Unmarshal(manifest, &p); err != nil {
		t.Fatal(err)
	}
	data := map[string][]byte{}
	for _, a := range p.Artifacts {
		b, err := os.ReadFile(filepath.Join("testdata", a.Role))
		if err != nil {
			t.Fatal(err)
		}
		data[a.Role] = b
	}
	return p, data
}
func TestPinnedBaselineAndMetadataReceipt(t *testing.T) {
	_, data := fixture(t)
	receipt, err := validate(func(a Artifact) ([]byte, error) { return data[a.Role], nil })
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ExecutionEnabled || receipt.DirectoryStatus != "inactive" || receipt.Classification != "blocked-baseline-verified" || len(receipt.Artifacts) != 12 || strings.Join(receipt.Blockers, ",") != "B1,B2,B3,B4" {
		t.Fatalf("unsafe receipt: %+v", receipt)
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"referenceDate", "fetchedAt", "rows", "providerReads", "sourceRights"} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("receipt contains %s", forbidden)
		}
	}
	copied := Manifest()
	copied[0] = '!'
	if Manifest()[0] != '{' {
		t.Fatal("manifest was mutable")
	}
}
func TestEveryArtifactByteDriftRefusesWithoutReceipt(t *testing.T) {
	p, data := fixture(t)
	for _, pin := range p.Artifacts {
		t.Run(pin.Role, func(t *testing.T) {
			receipt, err := validate(func(a Artifact) ([]byte, error) {
				if a.Role == pin.Role {
					return append(bytes.Clone(data[a.Role]), ' '), nil
				}
				return data[a.Role], nil
			})
			if err == nil || receipt != nil {
				t.Fatal("drift produced a receipt")
			}
		})
	}
	receipt, err := validate(func(Artifact) ([]byte, error) { return nil, errors.New("missing object") })
	if err == nil || receipt != nil {
		t.Fatal("missing object produced a receipt")
	}
	if r, err := Validate(context.Background(), nil); err == nil || r != nil {
		t.Fatal("missing repository accepted")
	}
}
func TestSemanticDriftFailsEvenWithAvailableBytes(t *testing.T) {
	cases := []struct{ role, old, new string }{
		{"modelspec-registry", "6751a14ae12bfeadbcc9a7c6aa6174c81d697e20", "0000000000000000000000000000000000000000"},
		{"meaning-registry", "publisher/source/model/ecb-daily.meaning.yaml", "wrong.yaml"},
		{"model-hcl", "type = \"string\"", "type = \"number\""},
		{"model-hcl", "required = true", "required = false"},
		{"model-json", "^[A-Z]{3}$", "^[A-Z]{2}$"},
		{"descriptor", "\"baseCurrency\": \"EUR\"", "\"baseCurrency\": \"USD\""},
		{"descriptor", "ecb-eurofxref/1", "ecb-eurofxref/2"},
		{"descriptor", "\"executionEnabled\": false", "\"executionEnabled\": true"},
		{"meaning", "ref=1de4de09a449b9978a6155f226669e02986cb7d9", "ref=main"},
		{"meaning", "property: time", "property: rate"},
		{"decoder", "\"rate\": rate", "\"rate\": currency"},
		{"decoder-contract", "ecb-eurofxref/1", "ecb-eurofxref/2"},
		{"decoder-tests", "TestECBDailyDecoder", "RemovedDecoderTest"},
		{"directory", "status: inactive", "status: active"},
		{"directory", "retention: none", "retention: snapshot"},
		{"directory", "B4:", "B5:"},
		{"directory", "https://modelspec.org/registry/models/ecb-daily/", "https://example.org/"},
	}
	for _, test := range cases {
		t.Run(test.role+"/"+test.old, func(t *testing.T) {
			p, data := fixture(t)
			changed := bytes.Replace(data[test.role], []byte(test.old), []byte(test.new), 1)
			if bytes.Equal(changed, data[test.role]) {
				t.Fatal("mutation missed fixture")
			}
			data[test.role] = changed
			if err := validateChain(p, data); err == nil {
				t.Fatal("semantic drift accepted")
			}
		})
	}
}
func TestClosedHCLSubset(t *testing.T) {
	for _, text := range []string{"", `entity "A" { property "x" { type = "string" required = true pattern = "x" extra = true } }`, `entity "A" { property "x" { type = var.type required = true pattern = "x" } }`, `entity "A" { property "x" { type = "string" required = true pattern = "x" type = "string" } }`, `entity "A" {`} {
		parsed, err := readHCL([]byte(text))
		if err == nil && len(parsed) > 0 {
			t.Fatalf("unsupported HCL accepted: %s", text)
		}
	}
}

// Real Git plumbing fixture contains invented metadata only, without network,
// working files or commits through a shared repository's lifecycle hooks.
func TestGitObjectIdentityAndMode(t *testing.T) {
	dir := t.TempDir()
	run := func(input string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Stdin = strings.NewReader(input)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Synthetic", "GIT_AUTHOR_EMAIL=synthetic@example.invalid", "GIT_COMMITTER_NAME=Synthetic", "GIT_COMMITTER_EMAIL=synthetic@example.invalid")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("", "init", "--bare")
	run("", "remote", "add", "origin", "https://github.com/openvaultdb/ovdb.git")
	blob := run("invented metadata\n", "hash-object", "-w", "--stdin")
	tree := run("100644 blob "+blob+"\tmetadata.json\n", "mktree")
	commit := run("invented metadata\n", "commit-tree", tree)
	a := Artifact{Repository: "openvaultdb/ovdb", Commit: commit, Path: "metadata.json", Blob: blob}
	got, err := readGit(context.Background(), dir, a)
	if err != nil || string(got) != "invented metadata\n" {
		t.Fatalf("read=%s err=%v", got, err)
	}
	for _, mutate := range []func(*Artifact){func(a *Artifact) { a.Path = "missing" }, func(a *Artifact) { a.Blob = strings.Repeat("0", 40) }, func(a *Artifact) { a.Commit = a.Blob }, func(a *Artifact) { a.Repository = "meaninggraph/core" }} {
		bad := a
		mutate(&bad)
		if _, err := readGit(context.Background(), dir, bad); err == nil {
			t.Fatal("invalid Git identity accepted")
		}
	}
	symlinkTree := run("120000 blob "+blob+"\tmetadata.json\n", "mktree")
	bad := a
	bad.Commit = run("symlink\n", "commit-tree", symlinkTree)
	if _, err := readGit(context.Background(), dir, bad); err == nil {
		t.Fatal("symlink accepted as metadata")
	}
	replacement := run("drift\n", "hash-object", "-w", "--stdin")
	run("", "replace", blob, replacement)
	got, err = readGit(context.Background(), dir, a)
	if err != nil || string(got) != "invented metadata\n" {
		t.Fatal("replacement object affected fixed bytes")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readGit(canceled, dir, a); err == nil {
		t.Fatal("canceled Git read accepted")
	}
	if _, err := gitOutput(context.Background(), dir, "cat-file", "blob", strings.Repeat("0", 40)); err == nil {
		t.Fatal("missing blob accepted")
	}
	b := &boundedOutput{}
	if _, err := b.Write(make([]byte, maxMetadataBytes+1)); err == nil {
		t.Fatal("unbounded metadata accepted")
	}
}

func TestMalformedManifestAndUnapprovedChain(t *testing.T) {
	p, data := fixture(t)
	read := func(a Artifact) ([]byte, error) { return data[a.Role], nil }
	for _, input := range [][]byte{[]byte("{"), []byte(`{"format":"other"}`)} {
		if r, err := validateManifest(input, read); r != nil || err == nil {
			t.Fatal("bad manifest accepted")
		}
	}
	for _, mutate := range []func(*pins){func(p *pins) { p.Artifacts[0].Commit = "main" }, func(p *pins) { p.Artifacts[0].Blob = "bad" }, func(p *pins) { p.Artifacts[0].Bytes = 0 }, func(p *pins) { p.Artifacts[0].Bytes = maxMetadataBytes + 1 }, func(p *pins) { p.Artifacts[1].Role = p.Artifacts[0].Role }, func(p *pins) { p.Artifacts[0].SHA256 = "bad" }} {
		cp := p
		cp.Artifacts = append([]Artifact(nil), p.Artifacts...)
		mutate(&cp)
		raw, _ := json.Marshal(cp)
		if r, err := validateManifest(raw, read); r != nil || err == nil {
			t.Fatal("invalid pin accepted")
		}
	}
	data["directory"] = bytes.Replace(data["directory"], []byte("status: inactive"), []byte("status: active"), 1)
	for i, a := range p.Artifacts {
		if a.Role == "directory" {
			p.Artifacts[i].Bytes = len(data[a.Role])
			p.Artifacts[i].SHA256 = digest(data[a.Role])
		}
	}
	raw, _ := json.Marshal(p)
	if r, err := validateManifest(raw, read); r != nil || err == nil {
		t.Fatal("rehashed active state accepted")
	}
	if r, err := validateRepositories(context.Background(), map[string]string{"modelspec-org/registry": "local"}, func(context.Context, string, Artifact) ([]byte, error) { return nil, errors.New("no object") }); r != nil || err == nil {
		t.Fatal("unavailable Git object accepted")
	}
}

func TestMalformedChainInputs(t *testing.T) {
	for _, role := range []string{"modelspec-registry", "meaning-registry", "directory", "meaning", "core", "decoder", "decoder-contract", "decoder-tests", "model-hcl", "model-json"} {
		t.Run(role, func(t *testing.T) {
			p, data := fixture(t)
			data[role] = []byte("{bad")
			if err := validateChain(p, data); err == nil {
				t.Fatal("malformed artifact accepted")
			}
		})
	}
	for _, change := range []struct{ role, old, new string }{
		{"meaning-registry", "CC0-1.0", "MIT"},
		{"meaning", "ecb: ecb-daily.modelspec.hcl", "ecb: wrong.hcl"},
		{"model-hcl", "pattern = \"^[A-Z]{3}$\"", "pattern = \"different\""},
		{"model-json", "\"type\": \"string\"", "\"type\": \"number\""},
		{"model-json", "\"required\": true", "\"required\": false"},
	} {
		p, data := fixture(t)
		data[change.role] = bytes.Replace(data[change.role], []byte(change.old), []byte(change.new), 1)
		if err := validateChain(p, data); err == nil {
			t.Fatal("chain drift accepted")
		}
	}
	p, data := fixture(t)
	for i := range p.Artifacts {
		if p.Artifacts[i].Role == "meaning" {
			p.Artifacts[i].Commit = strings.Repeat("0", 40)
		}
	}
	if err := validateChain(p, data); err == nil {
		t.Fatal("split OVDB pins accepted")
	}
	p, data = fixture(t)
	for i := range p.Artifacts {
		if p.Artifacts[i].Role == "decoder-tests" {
			p.Artifacts[i].Commit = strings.Repeat("0", 40)
		}
	}
	if err := validateChain(p, data); err == nil {
		t.Fatal("split decoder pins accepted")
	}
	p, data = fixture(t)
	p.DecoderModuleVersion = "v0.4.0"
	if err := validateChain(p, data); err == nil {
		t.Fatal("module version drift accepted")
	}
	p, data = fixture(t)
	data["decoder"] = []byte("package dalgo2http\nvar x = map[string]any{\"time\": 3}\n")
	if err := validateChain(p, data); err == nil {
		t.Fatal("non-identifier field accepted")
	}
	p, data = fixture(t)
	data["model-hcl"] = bytes.ReplaceAll(data["model-hcl"], []byte("string"), []byte("number"))
	data["model-json"] = bytes.ReplaceAll(data["model-json"], []byte("string"), []byte("number"))
	if err := validateChain(p, data); err == nil {
		t.Fatal("equivalent nonnative types accepted")
	}
}

func TestHCLSyntaxRefusals(t *testing.T) {
	for _, text := range []string{`unknown`, `entity wrong {}`, `entity "A" { property wrong {} }`, `entity "A" { property "x" { type = "string" required = maybe pattern = "x" } }`, `entity "A" { property "x" { type = "string" } }`, `entity "A" { property "x" { type = "unterminated`, "entity \"A\" {}\n@"} {
		if _, err := readHCL([]byte(text)); err == nil {
			t.Fatalf("invalid HCL accepted %s", text)
		}
	}
	if _, err := readHCL([]byte(`entity "A" {} /*`)); err == nil {
		t.Fatal("malformed trailing comment accepted")
	}
}

func TestGitCommandRefusalsAndVersionBinding(t *testing.T) {
	a := Artifact{Repository: "dal-go/dalgo2http", Commit: strings.Repeat("a", 40), Blob: strings.Repeat("b", 40), Path: "decoder.go"}
	command := func(args ...string) ([]byte, error) {
		switch args[0] {
		case "remote":
			return []byte("git@github.com:dal-go/dalgo2http.git\n"), nil
		case "rev-parse":
			return []byte(a.Commit + "\n"), nil
		case "ls-tree":
			return []byte("100644 blob " + a.Blob + "\t" + a.Path + "\x00"), nil
		case "cat-file":
			if args[1] == "-t" {
				return []byte("commit\n"), nil
			}
			return []byte("invented metadata"), nil
		}
		return nil, errors.New("unexpected command")
	}
	if _, err := readGitWithCommand(a, command); err != nil {
		t.Fatal(err)
	}
	for _, fail := range []string{"remote", "rev-parse", "ls-tree"} {
		if _, err := readGitWithCommand(a, func(args ...string) ([]byte, error) {
			if args[0] == fail {
				return nil, errors.New("unavailable")
			}
			return command(args...)
		}); err == nil {
			t.Fatal("Git failure accepted")
		}
	}
	if _, err := readGitWithCommand(a, func(args ...string) ([]byte, error) {
		if args[0] == "rev-parse" {
			return []byte(strings.Repeat("c", 40)), nil
		}
		return command(args...)
	}); err == nil {
		t.Fatal("release tag moved")
	}
	b := &boundedOutput{}
	if n, err := b.Write([]byte("metadata")); n != 8 || err != nil {
		t.Fatal("bounded writer rejected metadata")
	}
}

func TestRegistrySourcePathAndDecoderNonliteralKeyRefusals(t *testing.T) {
	p, data := fixture(t)
	data["modelspec-registry"] = bytes.Replace(data["modelspec-registry"], []byte("source_file: publisher/source/model/ecb-daily.modelspec.hcl"), []byte("source_file: wrong.hcl"), 1)
	if err := validateChain(p, data); err == nil {
		t.Fatal("registry source path drift accepted")
	}
	p, data = fixture(t)
	data["decoder"] = []byte("package dalgo2http\nvar x = map[string]any{key: rate}\nvar y = []string{\"rate\"}\n")
	if err := validateChain(p, data); err == nil {
		t.Fatal("nonliteral decoder key accepted")
	}
}

// [fix r1] A future reviewed repin still cannot admit ambiguous JSON. The
// present byte digest is a separate check, not a substitute for strict parsing.
func TestModelJSONTrailingAndDuplicateKeysRefuse(t *testing.T) {
	_, data := fixture(t)
	for name, changed := range map[string][]byte{
		"trailing object":       append(bytes.Clone(data["model-json"]), []byte(`{}`)...),
		"duplicate property":    bytes.Replace(data["model-json"], []byte(`"type": "string"`), []byte(`"type": "number", "type": "string"`), 1),
		"duplicate entity":      bytes.Replace(data["model-json"], []byte(`"FxReferenceQuote": {`), []byte(`"FxReferenceQuote": {}, "FxReferenceQuote": {`), 1),
		"case folded attribute": bytes.Replace(data["model-json"], []byte(`"required": true`), []byte(`"Required": true`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateModels(data["model-hcl"], changed); err == nil {
				t.Fatal("ambiguous model JSON accepted")
			}
		})
	}
}
