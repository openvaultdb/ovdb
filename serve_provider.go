package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/openvaultdb/openvaultdb-go/pkg/providerreads"
	"github.com/openvaultdb/openvaultdb-go/pkg/server"
)

// These are operator-supplied admission facts, not inferred provider terms or
// verified artifacts. NewChecked validates their equality with frozen source
// rights and the fixed mounted profile before any listener or upstream read.
func loadProviderReadProfiles(path string) (map[string]server.ProviderReadProfile, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open provider-read profiles: %w", err)
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, providerreads.MaxMetadataBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read provider-read profiles: %w", err)
	}
	if len(body) > providerreads.MaxMetadataBytes {
		return nil, fmt.Errorf("provider-read profiles exceed 256 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var profiles map[string]server.ProviderReadProfile
	if err := decoder.Decode(&profiles); err != nil {
		return nil, fmt.Errorf("decode provider-read profiles: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("provider-read profiles require exactly one JSON object")
	}
	if len(profiles) == 0 || len(profiles) > providerreads.MaxItems {
		return nil, fmt.Errorf("provider-read profiles require 1..%d database entries", providerreads.MaxItems)
	}
	// This CLI's explicit admission boundary requires reviewed source notices.
	// The library retains optional SourceRight compatibility; NewChecked checks
	// the supplied inventory against mounted terms, identity and retention.
	for _, profile := range profiles {
		if profile.SourceRight == nil {
			return nil, fmt.Errorf("provider-read profiles require an operator-supplied sourceRight notice inventory")
		}
	}
	return profiles, nil
}
