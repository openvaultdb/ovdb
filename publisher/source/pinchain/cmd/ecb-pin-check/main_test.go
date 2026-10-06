package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/openvaultdb/ovdb/publisher/source/pinchain"
)

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, errors.New("closed output") }

func TestMetadataCLIAndRefusals(t *testing.T) {
	saved := validate
	t.Cleanup(func() { validate = saved })
	called := false
	validate = func(_ context.Context, repos map[string]string) (*pinchain.Receipt, error) {
		called = true
		if len(repos) != 6 || repos["openvaultdb/ovdb"] != "metadata-repo" {
			t.Fatal("repository arguments lost")
		}
		return &pinchain.Receipt{Format: "ovdb-ecb-metadata-receipt/1", Classification: "blocked-baseline-verified", DirectoryStatus: "inactive"}, nil
	}
	var out, errOut bytes.Buffer
	args := []string{"-ovdb", "metadata-repo"}
	if code := run(args, &out, &errOut); code != 0 || !called {
		t.Fatalf("code=%d error=%s", code, errOut.String())
	}
	var receipt pinchain.Receipt
	if err := json.Unmarshal(out.Bytes(), &receipt); err != nil || receipt.ExecutionEnabled || receipt.DirectoryStatus != "inactive" {
		t.Fatal("unsafe output")
	}
	for _, bad := range [][]string{{"-unknown"}, {"extra"}} {
		called = false
		out.Reset()
		if run(bad, &out, &errOut) != 1 || called || out.Len() != 0 {
			t.Fatal("invalid argument invoked checker")
		}
	}
	if run(args, failedWriter{}, &errOut) != 1 {
		t.Fatal("output failure accepted")
	}
	validate = func(context.Context, map[string]string) (*pinchain.Receipt, error) {
		return nil, errors.New("metadata drift")
	}
	out.Reset()
	if run(nil, &out, &errOut) != 1 || out.Len() != 0 {
		t.Fatal("failed check emitted receipt")
	}
}
func TestMainExit(t *testing.T) {
	savedExit, savedArgs := exit, os.Args
	t.Cleanup(func() { exit = savedExit; os.Args = savedArgs })
	os.Args = []string{"ecb-pin-check", "-unknown"}
	code := -1
	exit = func(c int) { code = c }
	main()
	if code != 1 {
		t.Fatal("main exit code")
	}
	if validate == nil || exit == nil || io.Discard == nil {
		t.Fatal("missing command defaults")
	}
}
