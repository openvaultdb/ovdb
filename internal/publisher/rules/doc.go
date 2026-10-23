// Package rules holds the pure rules that a publisher's OVDB manifest is held
// to: what a published URL, a homepage, a database id, a commit, a repository,
// a path inside a repository, an engine name and a licence id may look like, and
// how two claimed addresses compare.
//
// The rules are the OVDB Directory's (github.com/openvaultdb/directory,
// scripts/lib/urls.mjs, git.mjs and directory.mjs, CC0-1.0) as the Chinook
// database's own pre-check ports them (github.com/datatug/chinookdb,
// scripts/lib/directory-rules.mjs, MIT). Both are JavaScript. The rule of this
// package is that a function here never accepts a string either of them
// refuses; it may refuse more, and every way it does is recorded in README.md
// and proved by the reference matrix (see the tests).
//
// Every function is pure: no file, no network, no clock, no YAML. Every
// function bounds its input first (see the Max constants) and then reads it
// once, left to right. The URL rules are written on the text, as a reader of a
// plain subset, and never hand the string to net/url: Go's parser and the WHATWG
// parser that the JavaScript relies on disagree on hosts whose last label is
// numeric, on punycode, on backslashes and on control characters.
//
// Two traps for the code that uses this package. A text field is "required"
// when it is not blank by JavaScript's trim(), which is not Go's
// strings.TrimSpace (it strips U+FEFF and not U+0085, Go the reverse): use
// [IsBlank], never strings.TrimSpace, for those checks. And claims conflict
// when [Relation.Conflicts] says so; never compare a [Relation] with Same or
// Under by hand, because an address that cannot be compared is a conflict.
//
// Limits on input are part of the rules: an input longer than the bound of its
// function is refused (or, for [Compare], reported as [Incomparable]) without
// being read further.
package rules

// Bounds on input, in bytes. The bounds that the references state themselves
// (a host of at most 253 characters, a label of at most 63, an id of at most
// 80, a homepage of at most 200) are the references' rules; the others are
// this package's own and only ever refuse more.
const (
	// MaxURLLength bounds every URL. The references have no bound on a URL.
	MaxURLLength = 2048
	// MaxHostLength is the longest host of a URL, dots included.
	MaxHostLength = 253
	// MaxLabelLength is the longest label of a host.
	MaxLabelLength = 63
	// MaxHomepageLength is the longest homepage.
	MaxHomepageLength = 200
	// MaxIDLength is the longest database id.
	MaxIDLength = 80
	// MaxEngineLength is the longest engine name.
	MaxEngineLength = 40
	// MaxLicenceLength is the longest licence id.
	MaxLicenceLength = 64
	// MaxPathLength bounds a path inside a repository. The references have no
	// bound on one.
	MaxPathLength = 1024
	// MaxRepositoryLength bounds a repository URL. The references have no bound
	// on one.
	MaxRepositoryLength = 255
	// MaxClaimLength bounds an address that is compared with another.
	MaxClaimLength = MaxURLLength
)
