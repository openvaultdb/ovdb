// Package manifest judges a publisher's OVDB.md and OVDB manifest (ovdb.yaml) as
// pure functions over the bytes of the two documents: no file system, no git, no
// network. It reports findings (a rule, a severity, a message that says what is
// wrong and what to write, and the line) and the facts a caller that reads the
// repository needs (the paths the documents name, the form of the manifest, the
// model and graph addresses, the recordset names), so that caller never reads the
// documents again.
//
// A profile says whose rules judge: the Directory's (Directory), the OVDB
// Directory's own shape rules. Another profile (the publisher's, which adds what
// the Chinook checker adds) is added as a Profile value without changing the
// signatures here.
//
// Both documents are YAML (OVDB.md has YAML front matter) and are read with the
// strict reader of github.com/meaninggraph/cli/pkg/meaning, a subset of YAML 1.2 in
// which it agrees with the `yaml` npm package that the JavaScript references use
// on every document it accepts. A document outside the subset is refused with the
// reader's own message. Nothing is coerced: a number, a boolean or a null where
// the rules want text is a finding, never read as the text it would print as.
//
// What these functions do not judge, because it needs files other than the two
// documents: that the named files are tracked regular files at HEAD and within
// size, that the recordsets are the model's entities, that a licence equals the
// meaning file's, that the meaning file's models: entry gives model.hcl, and
// anything read through a registry.
//
// The URL, id, path, engine, licence and claim rules are those of package
// rules, whose parity with the references is proved there; a required text is
// checked with rules.IsBlank, never strings.TrimSpace.
package manifest
