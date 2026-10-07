// Package pinchain verifies one preparatory ECB metadata baseline. It performs
// no network I/O and never authorizes a provider read or source activation.
package pinchain

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

//go:embed ecb-daily.pins.json
var manifest []byte

// Artifact pins exact Git objects plus independently reviewable byte digests.
type Artifact struct {
	Role       string `json:"role"`
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	Path       string `json:"path"`
	Blob       string `json:"blob"`
	SHA256     string `json:"sha256"`
	Bytes      int    `json:"bytes"`
}
type pins struct {
	Format               string     `json:"format"`
	DecoderVersion       string     `json:"decoderVersion"`
	DecoderModuleVersion string     `json:"decoderModuleVersion"`
	Artifacts            []Artifact `json:"artifacts"`
}

// Receipt contains metadata only. Verified means this inactive baseline was
// reproduced, not admitted. It is not SourceRight or provider-read evidence.
type Receipt struct {
	Format               string     `json:"format"`
	Classification       string     `json:"classification"`
	ManifestSHA256       string     `json:"manifestSha256"`
	ExecutionEnabled     bool       `json:"executionEnabled"`
	DirectoryStatus      string     `json:"directoryStatus"`
	Blockers             []string   `json:"blockers"`
	DecoderVersion       string     `json:"decoderVersion"`
	DecoderModuleVersion string     `json:"decoderModuleVersion"`
	Artifacts            []Artifact `json:"artifacts"`
}

// Manifest returns a detached copy; a changed baseline requires code review.
func Manifest() []byte { return bytes.Clone(manifest) }

// Validate reads exclusively from local Git objects in the named repositories.
// Mutable refs and working files cannot substitute for the embedded baseline.
// Callers must run this before a future provider read; this package deliberately
// has no executor and successful validation leaves every admission gate blocked.
func Validate(ctx context.Context, repositories map[string]string) (*Receipt, error) {
	runtime, err := AdmitGit(ctx)
	if err != nil {
		return nil, err
	}
	return runtime.Validate(ctx, repositories)
}

func validateRepositories(ctx context.Context, repositories map[string]string, read func(context.Context, string, Artifact) ([]byte, error)) (*Receipt, error) {
	return validate(func(a Artifact) ([]byte, error) {
		dir := repositories[a.Repository]
		if dir == "" {
			return nil, fmt.Errorf("missing local repository %s", a.Repository)
		}
		return read(ctx, dir, a)
	})
}

func validate(read func(Artifact) ([]byte, error)) (*Receipt, error) {
	return validateManifest(manifest, read)
}

func validateManifest(manifestBytes []byte, read func(Artifact) ([]byte, error)) (*Receipt, error) {
	var p pins
	if err := json.Unmarshal(manifestBytes, &p); err != nil {
		return nil, err
	}
	if p.Format != "ovdb-ecb-metadata-pins/1" || len(p.Artifacts) != 12 {
		return nil, fmt.Errorf("invalid baseline manifest")
	}
	artifacts := map[string][]byte{}
	for _, a := range p.Artifacts {
		if !objectID.MatchString(a.Commit) || !objectID.MatchString(a.Blob) || a.Bytes < 1 || a.Bytes > maxMetadataBytes || artifacts[a.Role] != nil {
			return nil, fmt.Errorf("invalid pin %s", a.Role)
		}
		data, err := read(a)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", a.Role, err)
		}
		if len(data) != a.Bytes || digest(data) != a.SHA256 {
			return nil, fmt.Errorf("%s: metadata byte digest drift", a.Role)
		}
		artifacts[a.Role] = data
	}
	if err := validateChain(p, artifacts); err != nil {
		return nil, err
	}
	return &Receipt{Format: "ovdb-ecb-metadata-receipt/1", Classification: "blocked-baseline-verified", ManifestSHA256: digest(manifestBytes), DirectoryStatus: "inactive", Blockers: []string{"B1", "B2", "B3", "B4"}, DecoderVersion: p.DecoderVersion, DecoderModuleVersion: p.DecoderModuleVersion, Artifacts: p.Artifacts}, nil
}

const maxMetadataBytes = 1 << 20

var objectID = regexp.MustCompile(`^[0-9a-f]{40}$`)

func digest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

// gitOutput bounds all output and omits Git/parser diagnostics from errors.
// Replacement objects are disabled, including replacements for parent commits.
func gitOutput(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return gitCommand(ctx, "git", dir, args...)
}

type boundedOutput struct{ buffer bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > maxMetadataBytes {
		return 0, fmt.Errorf("metadata output exceeds bound")
	}
	return b.buffer.Write(p)
}

func readGit(ctx context.Context, dir string, a Artifact) ([]byte, error) {
	return readGitWithCommand(a, func(args ...string) ([]byte, error) { return gitOutput(ctx, dir, args...) })
}

func readGitWithCommand(a Artifact, run func(...string) ([]byte, error)) ([]byte, error) {
	remote, err := run("remote", "get-url", "origin")
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(string(remote))
	if name != "https://github.com/"+a.Repository && name != "https://github.com/"+a.Repository+".git" && name != "git@github.com:"+a.Repository+".git" {
		return nil, fmt.Errorf("repository origin identity mismatch")
	}
	if a.Repository == "dal-go/dalgo2http" {
		version, err := run("rev-parse", "--verify", "refs/tags/v0.3.0^{commit}")
		if err != nil || strings.TrimSpace(string(version)) != a.Commit {
			return nil, fmt.Errorf("decoder module version tag/commit drift")
		}
	}
	kind, err := run("cat-file", "-t", a.Commit)
	if err != nil || string(kind) != "commit\n" {
		return nil, fmt.Errorf("pin is not an available commit object")
	}
	entry, err := run("ls-tree", "-z", a.Commit, "--", a.Path)
	if err != nil {
		return nil, err
	}
	if string(entry) != "100644 blob "+a.Blob+"\t"+a.Path+"\x00" {
		return nil, fmt.Errorf("commit path/blob drift or non-file entry")
	}
	return run("cat-file", "blob", a.Blob)
}
