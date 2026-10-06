package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/openvaultdb/ovdb/publisher/source/preflight"
)

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, errors.New("failed") }
func (failReader) Close() error             { return nil }

func TestCLIBlockedOnlyAndErrors(t *testing.T) {
	savedOpen, savedVerify := open, verify
	t.Cleanup(func() { open = savedOpen; verify = savedVerify })
	open = func(string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(`{"executor":{"serverId":"proposed","databaseId":"ecb","recordset":"daily"}}`)), nil
	}
	called := false
	verify = func(_ context.Context, repos map[string]string, p preflight.Proposal) (*preflight.Receipt, error) {
		called = true
		if len(repos) != 6 || repos["openvaultdb/ovdb"] != "local" || p.Executor.ServerID != "proposed" {
			t.Fatal("lost arguments")
		}
		return &preflight.Receipt{Format: "ovdb-ecb-offline-preflight/1", Classification: "blocked-publisher-artifact-missing", DirectoryStatus: "inactive", Blockers: []string{"B1", "B2", "B3", "B4"}}, nil
	}
	var out, errs bytes.Buffer
	args := []string{"-proposal", "metadata.json", "-ovdb", "local"}
	if code := run(args, &out, &errs); code != 2 || !called || !strings.Contains(out.String(), `"executionEnabled": false`) {
		t.Fatal(code, errs.String())
	}
	for _, bad := range [][]string{nil, {"-proposal", "file", "extra"}, {"-unknown"}} {
		called = false
		out.Reset()
		if run(bad, &out, &errs) != 1 || called || out.Len() != 0 {
			t.Fatal("bad arguments invoked verifier")
		}
	}
	if run(args, failWriter{}, &errs) != 1 {
		t.Fatal("failed output accepted")
	}
	verify = func(context.Context, map[string]string, preflight.Proposal) (*preflight.Receipt, error) {
		return nil, errors.New("drift")
	}
	out.Reset()
	if run(args, &out, &errs) != 1 || out.Len() != 0 {
		t.Fatal("failed check emitted receipt")
	}
	for _, reader := range []func(string) (io.ReadCloser, error){func(string) (io.ReadCloser, error) { return nil, errors.New("missing") }, func(string) (io.ReadCloser, error) { return failReader{}, nil }, func(string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(`{"unexpected":true}`)), nil
	}} {
		open = reader
		if run(args, &out, &errs) != 1 {
			t.Fatal("bad input accepted")
		}
	}
}

func TestMainAndRealOpenDefault(t *testing.T) {
	f := t.TempDir() + "/proposal.json"
	if err := os.WriteFile(f, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := open(f)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	savedExit, savedArgs := exit, os.Args
	t.Cleanup(func() { exit = savedExit; os.Args = savedArgs })
	os.Args = []string{"ecb-preflight", "-unknown"}
	code := -1
	exit = func(c int) { code = c }
	main()
	if code != 1 {
		t.Fatal(code)
	}
}
