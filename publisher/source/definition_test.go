package source

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/openvaultdb/openvaultdb-go/pkg/license"
)

func example(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("ecb-daily.example.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBlockedDailyDefinition(t *testing.T) {
	d, err := Parse(example(t))
	if err != nil {
		t.Fatal(err)
	}
	if d.Resources["daily"].Retention != "none" || d.Recordsets["FxReferenceQuote"].FieldMapping["time"] != "referenceDate" {
		t.Fatal("daily boundary lost")
	}
	if err := d.Matches([]string{"FxReferenceQuote"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := d.Matches([]string{"alias"}, nil); err == nil {
		t.Fatal("native identity changed")
	}
	if err := d.Matches([]string{"FxReferenceQuote"}, map[string]string{"FxReferenceQuote": "Other"}); err == nil {
		t.Fatal("model changed")
	}
	if err := d.MatchesTerms(license.Declaration{URL: d.Rights.Terms[0].URL}); err != nil {
		t.Fatal(err)
	}
	if err := d.MatchesTerms(license.Declaration{SPDX: "MIT", URL: d.Rights.Terms[0].URL}); err == nil {
		t.Fatal("inferred output licence accepted")
	}
	d.Admission.ExecutionEnabled = true
	d.Admission.Status = "admitted"
	if d.RequireExecution() == nil {
		t.Fatal("definition enabled execution")
	}
	b := Schema()
	b[0] = 'x'
	if Schema()[0] != '{' {
		t.Fatal("shared schema modified")
	}
}

func TestClosedAdmissionAndNoCopyContract(t *testing.T) {
	for _, tc := range []struct{ path, value string }{
		{"admission.executionEnabled", "true"}, {"admission.status", `"admitted"`},
		{"admission.gates.runtime", `"passed"`}, {"admission.gates.semantic", `"passed"`},
		{"admission.gates.rights", `"passed"`}, {"admission.gates.paid", `"passed"`},
		{"resources.daily.retention", `"snapshot"`}, {"resources.daily.cache", `"public"`},
		{"resources.daily.redirect", `"follow"`}, {"resources.daily.browserAccess", `"verified"`},
		{"resources.daily.mode", `"snapshot"`}, {"resources.daily.maxBytes", "2097153"},
		{"resources.daily.timeoutMs", "0"}, {"resources.daily.headers", `{"Authorization":"secret"}`},
		{"resources.daily.url", `"https://www.ecb.europa.eu/data?token=secret"`},
		{"resources.daily.url", `"https://user:secret@www.ecb.europa.eu/data"`},
		{"resources.daily.url", `"https://127.0.0.1/data"`},
		{"readEvidence.classification", `"publisher-verified"`},
		{"readEvidence.inputVerification", `"immutable-pinned"`},
		{"recordsets.FxReferenceQuote.providerSourceId", `"provider:gateway/FxReferenceQuote"`},
		{"recordsets.FxReferenceQuote.resource", `"historic"`},
		{"recordsets.FxReferenceQuote.keyFields", `["missing"]`},
		{"recordsets.FxReferenceQuote.rows", `[]`},
		{"rights.freeSource.url", `"https://gateway.org/data"`},
		{"rights.terms", `[]`}, {"rights.freeSource", `null`},
		{"admission.executionEnabled", `null`}, {"resources.daily.retention", `null`},
	} {
		t.Run(tc.path+tc.value, func(t *testing.T) {
			var doc map[string]any
			_ = json.Unmarshal(example(t), &doc)
			keys := strings.Split(tc.path, ".")
			m := doc
			for _, key := range keys[:len(keys)-1] {
				m = m[key].(map[string]any)
			}
			var value any
			_ = json.Unmarshal([]byte(tc.value), &value)
			m[keys[len(keys)-1]] = value
			b, _ := json.Marshal(doc)
			if _, err := Parse(b); err == nil {
				t.Fatal("unsafe admission accepted")
			}
		})
	}
	for _, mutate := range []func([]byte) []byte{
		func(b []byte) []byte { return bytes.Replace(b, []byte(`"format":`), []byte(`"Format":`), 1) },
		func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"format":`), []byte(`"format":"ovdb-http-source/1","format":`), 1)
		},
		func(b []byte) []byte { return append(b, []byte(`{}`)...) },
		func(b []byte) []byte { return bytes.Replace(b, []byte(`"retention": "none",`), nil, 1) },
	} {
		if _, err := Parse(mutate(example(t))); err == nil {
			t.Fatal("invalid authored shape accepted")
		}
	}
}
