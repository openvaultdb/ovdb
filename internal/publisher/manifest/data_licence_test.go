package manifest

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDataLicenceConjunctionAndLegacyProfiles(t *testing.T) {
	good := []string{"CC0-1.0 AND CC-BY-4.0", "CC-BY-4.0 AND CC0-1.0", "MIT AND Apache-2.0", "AGPL-3.0-only AND BSD-2-Clause AND BSD-3-Clause AND GPL-2.0-only"}
	bad := []string{"AGPL-3.0-only AND GPL-2.0-only AND GPL-3.0-only AND LGPL-3.0-only", "MIT AND ISC AND 0BSD AND CC0-1.0 AND MPL-2.0", "MIT AND MIT", "mit AND ISC", "MIT AND Unknown", "MIT OR ISC", "MIT WITH ISC", "(MIT AND ISC)", "MIT AND GPL-2.0+", "MIT AND LicenseRef-x", " MIT AND ISC", "MIT AND ISC ", "MIT  AND ISC", "MIT AND  ISC", "MIT\tAND ISC", "MIT AND\nISC", "MIT\u00a0AND ISC", "MIT AND ", " AND ISC"}
	for _, profile := range []Profile{Directory, Publisher} {
		for _, base := range []string{ownManifest, sharedPublisherManifest(t)} {
			for _, tc := range []struct {
				values []string
				want   bool
			}{{good, true}, {bad, false}} {
				for _, value := range tc.values {
					quoted, _ := json.Marshal(value)
					doc := strings.Replace(base, "data: MIT", "data: "+string(quoted), 1)
					result := Check([]byte(goodMD), "ovdb.yaml", []byte(doc), profile)
					if result.OK() != tc.want || tc.want && result.Manifest.LicenceData.Value != value {
						t.Fatalf("profile %v, data %q: %v", profile, value, result.Findings)
					}
				}
			}
			for _, atom := range []string{"GPL-2.0+", "LicenseRef-x", "CC-BY-SA-3.0", "unknown-ID", "mit"} {
				result := Check([]byte(goodMD), "ovdb.yaml", []byte(strings.Replace(base, "data: MIT", "data: "+atom, 1)), profile)
				if result.OK() != (profile == Directory) {
					t.Fatalf("legacy profile %v atom %s: %v", profile, atom, result.Findings)
				}
			}
			for _, value := range []string{"[MIT, ISC]", "{id: MIT}", "42", "null"} {
				if Check([]byte(goodMD), "ovdb.yaml", []byte(strings.Replace(base, "data: MIT", "data: "+value, 1)), profile).OK() {
					t.Fatalf("non-scalar %s accepted", value)
				}
			}
		}
		for _, field := range []string{"model: MIT", "meaning: CC0-1.0"} {
			if Check([]byte(goodMD), "ovdb.yaml", []byte(strings.Replace(ownManifest, field, strings.Split(field, ":")[0]+": MIT AND ISC", 1)), profile).OK() {
				t.Fatal("model/meaning compound accepted")
			}
		}
	}
}

func TestManifestAttachmentShape(t *testing.T) {
	for _, value := range []string{"null", "42", "{path: contract.json}", "{path: ../contract.json, sha256: " + strings.Repeat("a", 64) + "}", "{path: contract.yaml, sha256: " + strings.Repeat("a", 64) + "}", "{path: contract.json, sha256: no}", "{path: contract.json, sha256: " + strings.Repeat("a", 64) + ", accepted: true}"} {
		data := []byte(ownManifest + "representation_contract: " + value + "\n")
		j, _ := NewJudge(Publisher)
		_, attachment, findings := j.ManifestWithAttachment(data, "ovdb.yaml")
		if len(findings) == 0 || !attachment.Unusable() || attachment.Value != nil {
			t.Fatalf("bad attachment %s admitted", value)
		}
	}
	good := []byte(ownManifest + "representation_contract: {path: contract.json, sha256: " + strings.Repeat("a", 64) + "}\n")
	if ref, err := RepresentationAttachment(good); err != nil || ref == nil {
		t.Fatalf("valid attachment: %v", err)
	}
	for _, legacy := range []string{ownManifest, "[a]", "bad: [", "null"} {
		if ref, err := RepresentationAttachment([]byte(legacy)); ref != nil || err != nil {
			t.Fatal("legacy gained attachment outcome")
		}
		j, _ := NewJudge(Publisher)
		_, attachment, _ := j.ManifestWithAttachment([]byte(legacy), "ovdb.yaml")
		if !attachment.Absent() {
			t.Fatal("legacy gained a judged attachment")
		}
	}
	for _, key := range []string{"representation_contract", `"representation_contract"`, `"representation\u005fcontract"`} {
		data := []byte(strings.Replace(string(good), "representation_contract", key, 1))
		j, _ := NewJudge(Publisher)
		_, attachment, findings := j.ManifestWithAttachment(data, "ovdb.yaml")
		ref, err := RepresentationAttachment(data)
		if len(findings) != 0 || err != nil || !attachment.Usable() || ref == nil || attachment.Value == nil || *attachment.Value != *ref {
			t.Fatalf("judged attachment %s differs from helper: %v, %v", key, findings, err)
		}
	}
}
