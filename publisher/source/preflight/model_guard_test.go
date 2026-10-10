package preflight

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

// The guard on the ECB model files in the working tree.
//
// The pin chain (publisher/source/pinchain) names the model by the bytes of a fixed commit, and keeps a copy of each file under
// pinchain/testdata whose digest it pins. The tests of this package build their publisher from those pinned bytes. What ties the working tree to
// them is this guard: the owner decided on 2026-10-09 ("2 - ok for A") that a model whose spelling ModelSpec has changed may be in the tree in one of
// two states, and in no other:
//
//   - the accepted state: both files are byte-identical to the pinned ones; or
//   - the renamed state: both files are byte-identical to the pinned ones with exactly the three words renamed (entity to record, property to field
//     and entity to record again, in the HCL; the identifier, entities, properties and entity in the JSON), as `modelspec rewrite` writes them.
//
// The guard recomputes the rename itself from the pinned bytes (renameHCL, renameJSON), and holds the result and the files to named SHA-256
// digests as well. Any other difference fails: the rename and one more change, a mixed spelling, one file renamed and not the other, a pinned copy
// that is not the pinned bytes. The pins, the accepted proposal and the receipts are not touched by it.
const (
	// pinnedModelHCLSHA256 and pinnedModelJSONSHA256 are the SHA-256 of the two model files as the chain pins them (ecb-daily.pins.json, commit
	// 6751a14). They occur in the chain's manifest and are named here too, so that a change of the manifest and of the stored copies alone does not
	// move the guard: requirePinnedModel holds both to these constants.
	pinnedModelHCLSHA256  = "937a7f0836d13c849dd8d68ed48df196fb7e61c11fd97bd10ed314d6e27961bd"
	pinnedModelJSONSHA256 = "d284028a72070865347134715f0bab04e22e1c33fa71ef9bef645d57a8cc703c"

	// renamedModelHCLSHA256 and renamedModelJSONSHA256 are the SHA-256 of the files that `modelspec rewrite --write` (ModelSpec CLI 0.2.0) writes
	// from the pinned ones (937a7f08..., d284028a...). The CLI's `export` of the renamed HCL, with the module id, name and version of the JSON,
	// is byte-identical to the renamed JSON.
	renamedModelHCLSHA256  = "9917cc88d14fc338dda614b217d1d861051584dceaf3de3a3b668419013ab3ab"
	renamedModelJSONSHA256 = "185908d7e7909d14973b20a45fb7a2da1766707b6f421d69b133dad159139707"

	workingModelHCL  = "../../../publisher/source/model/ecb-daily.modelspec.hcl"
	workingModelJSON = "../../../publisher/source/model/ecb-daily.modelspec.json"
)

// modelFiles are the HCL and the JSON form of the ECB model.
type modelFiles struct{ hcl, json []byte }

// modelDigests are the SHA-256 digests of the two files.
type modelDigests struct{ hcl, json string }

// acceptedModelState says in which state the working files are, "accepted" or "renamed", or why they are in neither. pinned are the stored copies
// of the pinned files and pins the digests that the chain pins for them.
func acceptedModelState(pinned, working modelFiles, pins modelDigests) (string, error) {
	pinnedHash := modelDigests{hash(pinned.hcl), hash(pinned.json)}
	if pins != pinnedHash {
		return "", errors.New("the stored copies are not the bytes that the chain pins")
	}
	renamed := modelFiles{}
	var err error
	if renamed.hcl, err = renameHCL(pinned.hcl); err != nil {
		return "", fmt.Errorf("the rename of the pinned HCL: %w", err)
	}
	if renamed.json, err = renameJSON(pinned.json); err != nil {
		return "", fmt.Errorf("the rename of the pinned JSON: %w", err)
	}
	if hash(renamed.hcl) != renamedModelHCLSHA256 || hash(renamed.json) != renamedModelJSONSHA256 {
		return "", errors.New("the rename of the pinned files is not what the reference tool writes")
	}
	is := func(got, want []byte, digest string) bool { return bytes.Equal(got, want) && hash(got) == digest }
	switch {
	case is(working.hcl, pinned.hcl, pinnedHash.hcl) && is(working.json, pinned.json, pinnedHash.json):
		return "accepted", nil
	case is(working.hcl, renamed.hcl, renamedModelHCLSHA256) && is(working.json, renamed.json, renamedModelJSONSHA256):
		return "renamed", nil
	}
	return "", errors.New("the model files are neither the pinned ones nor exactly their rename (both files, in one state)")
}

// pinnedModel are the stored copies of the pinned model files and their pinned digests.
func pinnedModel(t *testing.T) (pinned modelFiles, pins modelDigests) {
	t.Helper()
	b, _ := baseline(t)
	for _, a := range b.Artifacts {
		switch a.Role {
		case "model-hcl":
			pins.hcl = a.SHA256
			pinned.hcl = bytesAt(t, "../pinchain/testdata/model-hcl")
		case "model-json":
			pins.json = a.SHA256
			pinned.json = bytesAt(t, "../pinchain/testdata/model-json")
		}
	}
	if pinned.hcl == nil || pinned.json == nil {
		t.Fatal("the chain pins no model")
	}
	return pinned, pins
}

// holdToPinnedConstants requires the digests that the chain's manifest pins, and the stored copies, to be the named constants.
func holdToPinnedConstants(pinned modelFiles, pins modelDigests) error {
	want := modelDigests{pinnedModelHCLSHA256, pinnedModelJSONSHA256}
	if pins != want || (modelDigests{hash(pinned.hcl), hash(pinned.json)}) != want {
		return errors.New("the chain's pins or the stored copies of the model are not the pinned digests named in the guard")
	}
	return nil
}

// requirePinnedModel fails the test unless the model files of the working tree are in an accepted state, and returns the pinned bytes, which are
// what the rest of a fixture is built from.
func requirePinnedModel(t *testing.T) modelFiles {
	t.Helper()
	pinned, pins := pinnedModel(t)
	working := modelFiles{bytesAt(t, workingModelHCL), bytesAt(t, workingModelJSON)}
	if err := modelDrift(pinned, pins, working); err != nil {
		t.Fatal("publisher model artifact drift: ", err)
	}
	return pinned
}

// modelDrift says why the working files are not in an accepted state: the pins and the stored copies must be the named constants (holdToPinnedConstants),
// and the working files must be the stored copies or exactly their rename (acceptedModelState).
func modelDrift(pinned modelFiles, pins modelDigests, working modelFiles) error {
	if err := holdToPinnedConstants(pinned, pins); err != nil {
		return err
	}
	_, err := acceptedModelState(pinned, working, pins)
	return err
}

func TestTheModelFilesInTheTreeAreAnAcceptedState(t *testing.T) {
	requirePinnedModel(t)
}

func TestTheGuardNamesTheDigestsThatTheChainPins(t *testing.T) {
	pinned, pins := pinnedModel(t)
	if err := holdToPinnedConstants(pinned, pins); err != nil {
		t.Fatal(err)
	}
	want := modelDigests{pinnedModelHCLSHA256, pinnedModelJSONSHA256}
	other := modelFiles{hcl: append(bytes.Clone(pinned.hcl), '\n'), json: pinned.json}
	for name, c := range map[string]struct {
		files modelFiles
		pins  modelDigests
	}{
		"a manifest that pins other bytes":   {pinned, modelDigests{"x", want.json}},
		"a manifest that pins other JSON":    {pinned, modelDigests{want.hcl, "x"}},
		"stored copies that are other bytes": {other, modelDigests{hash(other.hcl), want.json}},
	} {
		if err := holdToPinnedConstants(c.files, c.pins); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestTheGuardAcceptsTwoStatesOfTheModel(t *testing.T) {
	pinned, pins := pinnedModel(t)
	renamed := modelFiles{}
	var err error
	if renamed.hcl, err = renameHCL(pinned.hcl); err != nil {
		t.Fatal(err)
	}
	if renamed.json, err = renameJSON(pinned.json); err != nil {
		t.Fatal(err)
	}
	for want, working := range map[string]modelFiles{"accepted": pinned, "renamed": renamed} {
		if got, err := acceptedModelState(pinned, working, pins); err != nil || got != want {
			t.Errorf("%s: %q, %v", want, got, err)
		}
	}
}

func TestTheGuardRefusesEverythingElse(t *testing.T) {
	pinned, pins := pinnedModel(t)
	renamed := modelFiles{}
	var err error
	if renamed.hcl, err = renameHCL(pinned.hcl); err != nil {
		t.Fatal(err)
	}
	if renamed.json, err = renameJSON(pinned.json); err != nil {
		t.Fatal(err)
	}
	with := func(m modelFiles, hcl, json func([]byte) []byte) modelFiles {
		if hcl != nil {
			m.hcl = hcl(bytes.Clone(m.hcl))
		}
		if json != nil {
			m.json = json(bytes.Clone(m.json))
		}
		return m
	}
	extra := func(b []byte) []byte { return append(b, '\n') }
	replace := func(old, changed string) func([]byte) []byte {
		return func(b []byte) []byte {
			if !bytes.Contains(b, []byte(old)) {
				t.Fatalf("ineffective fixture: no %q", old)
			}
			return bytes.Replace(b, []byte(old), []byte(changed), 1)
		}
	}
	for name, working := range map[string]modelFiles{
		"the rename and one more change, in the HCL":        with(renamed, extra, nil),
		"the rename and one more change, in the JSON":       with(renamed, nil, extra),
		"the rename and a changed pattern":                  with(renamed, nil, replace(`"^[A-Z]{3}$"`, `"^[A-Z]{2}$"`)),
		"the pinned files and one more change":              with(pinned, nil, extra),
		"a changed value in the pinned HCL":                 with(pinned, replace(`required = true`, `required = false`), nil),
		"a mixed spelling: records with properties":         with(pinned, nil, replace(`"entities"`, `"records"`)),
		"a mixed spelling: entities with fields":            with(pinned, nil, replace(`"properties"`, `"fields"`)),
		"a mixed spelling: the identifier only":             with(pinned, nil, replace(`"1.0-draft"`, `"1.0-draft-2"`)),
		"a mixed spelling in the HCL: record with property": with(pinned, replace(`entity "FxReferenceQuote"`, `record "FxReferenceQuote"`), nil),
		"a mixed spelling in the HCL: entity with field":    with(pinned, replace(`property "time"`, `field "time"`), nil),
		"a mixed spelling in the renamed HCL":               with(renamed, replace(`field "time"`, `property "time"`), nil),
		"a mixed spelling in the renamed JSON":              with(renamed, nil, replace(`"fields"`, `"properties"`)),
		"only the JSON renamed":                             {hcl: pinned.hcl, json: renamed.json},
		"only the HCL renamed":                              {hcl: renamed.hcl, json: pinned.json},
		"the files exchanged between the states":            {hcl: renamed.json, json: renamed.hcl},
		"no files":                                          {},
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := acceptedModelState(pinned, working, pins); err == nil {
				t.Fatalf("accepted as %q", got)
			}
		})
	}
	// The guard is also held to the chain: a stored copy that is not the pinned bytes, and a pinned copy that does not rename as the reference does.
	if _, err := acceptedModelState(with(pinned, extra, nil), pinned, pins); err == nil {
		t.Error("a stored HCL that is not the pinned one was accepted")
	}
	if _, err := acceptedModelState(with(pinned, nil, extra), pinned, pins); err == nil {
		t.Error("a stored JSON that is not the pinned one was accepted")
	}
	other := modelFiles{hcl: []byte("entity \"A\" {\n}\n"), json: []byte(`{"modelspec":"1.0-draft","entities":{}}`)}
	if _, err := acceptedModelState(other, other, modelDigests{hash(other.hcl), hash(other.json)}); err == nil {
		t.Error("a pair that renames to other bytes than the reference's was accepted")
	}
	notRenamable := modelFiles{hcl: []byte("entity \"A\" {\n  tags = {}\n}\n"), json: pinned.json}
	if _, err := acceptedModelState(notRenamable, notRenamable, modelDigests{hash(notRenamable.hcl), pins.json}); err == nil {
		t.Error("an HCL that cannot be renamed was accepted")
	}
	notRenamable = modelFiles{hcl: pinned.hcl, json: []byte(`[]`)}
	if _, err := acceptedModelState(notRenamable, notRenamable, modelDigests{pins.hcl, hash(notRenamable.json)}); err == nil {
		t.Error("a JSON that cannot be renamed was accepted")
	}
}

// One test for each of the two lines of the guard that no other test needed. Each builds a state that its line refuses, and asserts that line's message.
const namedDigestsRefusal = "the chain's pins or the stored copies of the model are not the pinned digests named in the guard"

// A manifest that pins the named digests, beside stored copies that are other bytes: in holdToPinnedConstants, only the comparison of the stored copies
// with the constants refuses it. (The pair check, acceptedModelState, refuses the same state too, in its own words: "the stored copies are not the bytes
// that the chain pins". The pair of TestTheGuardNamesTheDigestsThatTheChainPins that carries other stored bytes carries other pins too, so that the
// comparison of the pins refuses it.)
func TestTheGuardRefusesStoredCopiesThatAreNotTheNamedDigestsWhateverTheManifestPins(t *testing.T) {
	pinned, pins := pinnedModel(t)
	want := modelDigests{pinnedModelHCLSHA256, pinnedModelJSONSHA256}
	if pins != want {
		t.Fatalf("the manifest does not pin the named digests: %v", pins)
	}
	for name, stored := range map[string]modelFiles{
		"the HCL":  {hcl: append(bytes.Clone(pinned.hcl), '\n'), json: pinned.json},
		"the JSON": {hcl: pinned.hcl, json: append(bytes.Clone(pinned.json), '\n')},
	} {
		t.Run(name, func(t *testing.T) {
			err := holdToPinnedConstants(stored, pins)
			if err == nil || err.Error() != namedDigestsRefusal {
				t.Fatalf("got %v", err)
			}
		})
	}
}

// Stored copies whose HCL is already the renamed one, with the digest of that HCL as the pin: the pair check accepts them, because a rename of the
// renamed HCL is itself and the digests agree, and the working files may be those copies. Only the holding of the pair to the named constants, which
// requirePinnedModel makes through modelDrift, refuses the state.
func TestTheGuardHoldsTheModelToTheNamedDigestsBeforeItAcceptsAState(t *testing.T) {
	pinned, pins := pinnedModel(t)
	renamedHCL, err := renameHCL(pinned.hcl)
	if err != nil {
		t.Fatal(err)
	}
	stored := modelFiles{hcl: renamedHCL, json: pinned.json}
	storedPins := modelDigests{hash(renamedHCL), pins.json}
	if state, err := acceptedModelState(stored, stored, storedPins); err != nil || state != "accepted" {
		t.Fatalf("the pair check does not accept the state this test needs: %q, %v", state, err)
	}
	err = modelDrift(stored, storedPins, stored)
	if err == nil || err.Error() != namedDigestsRefusal {
		t.Fatalf("got %v", err)
	}
	// The pinned state itself has no drift.
	if err := modelDrift(pinned, pins, pinned); err != nil {
		t.Fatal(err)
	}
}
