package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/openvaultdb/ovdb/publisher/source/preflight"
)

func TestOfflinePreflightReceiptCannotLoadProviderProfiles(t *testing.T) {
	// A blocked disposition cannot cross the existing operator-profile loader.
	r := preflight.Receipt{Format: "ovdb-ecb-offline-preflight/1", Classification: "blocked-publisher-artifact-missing", DirectoryStatus: "inactive", Blockers: []string{"B1", "B2", "B3", "B4"}, PublisherCheck: "publisher artifact missing", RuntimeEnforcement: "unproved"}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/blocked.json"
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if profiles, err := loadProviderReadProfiles(path); err == nil || profiles != nil {
		t.Fatal("blocked preflight became a loadable operator profile")
	}
}
