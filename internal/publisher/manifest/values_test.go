package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Where the Go reader and the `yaml` package (which the references use) both read a document, do they read the same values? The verdicts of
// the two are compared by the other tests, and the facts that the rules use; this compares every scalar of every document, whatever field it is
// in: values.json holds, for each document of the corpus, a digest of the values that the `yaml` package reads (made by generate.mjs in the form
// of canonicalNode below), and the test makes the digest of what the reader reads, for each document that the reader reads, and compares.

// canonicalNode is the form of a value that generate.mjs makes of a value of the yaml package: null `n`, a boolean `b0` or `b1`, a number `f` and
// the 16 hex digits of its IEEE double, a string `s`, its length in bytes and `:` and its text, a sequence `q`, its length and `[` the values `]`,
// a mapping `m`, its number of keys and `{` each key (as a string) and its value, the keys in the order of their bytes, `}`.
func canonicalNode(n *Node) string { return canonical(n, false) }

// canonical is canonicalNode, with every zero written as negative zero when negativeZero is set: the yaml package reads the integer -0 as the
// number -0, and the Go reader reads it as 0 (its integers are exact, not doubles). The test tells that difference from any other.
func canonical(n *Node, negativeZero bool) string {
	switch n.Kind {
	case kindNull:
		return "n"
	case kindBool:
		if n.Text == "true" {
			return "b1"
		}
		return "b0"
	case kindNumber:
		f, err := strconv.ParseFloat(n.Text, 64)
		if err != nil {
			return "f?" + n.Text
		}
		if f == 0 && negativeZero {
			f = math.Copysign(0, -1)
		}
		return fmt.Sprintf("f%016x", math.Float64bits(f))
	case kindString:
		return fmt.Sprintf("s%d:%s", len(n.Text), n.Text)
	case kindSeq:
		var b strings.Builder
		fmt.Fprintf(&b, "q%d[", len(n.Items))
		for _, item := range n.Items {
			b.WriteString(canonical(item, negativeZero))
		}
		b.WriteString("]")
		return b.String()
	}
	keys := slices.Sorted(func(yield func(string) bool) {
		for key := range n.Fields {
			if !yield(key) {
				return
			}
		}
	})
	var b strings.Builder
	fmt.Fprintf(&b, "m%d{", len(keys))
	for _, key := range keys {
		fmt.Fprintf(&b, "s%d:%s%s", len(key), key, canonical(n.Fields[key], negativeZero))
	}
	b.WriteString("}")
	return b.String()
}

func valueDigest(n *Node, negativeZero bool) string {
	sum := sha256.Sum256([]byte(canonical(n, negativeZero)))
	return hex.EncodeToString(sum[:])[:16]
}

func TestValuesAgreeWithTheYamlPackage(t *testing.T) {
	var golden struct {
		Manifest, Md string
	}
	readGolden(t, "values.json", &golden)
	_, _, manifests, mds := loadReference(t)
	if len(golden.Manifest) != 16*len(manifests) || len(golden.Md) != 16*len(mds) {
		t.Fatalf("values.json holds %d and %d digests for %d manifests and %d OVDB.md documents", len(golden.Manifest)/16, len(golden.Md)/16, len(manifests), len(mds))
	}
	var compared, same, bothRefuse, readerOnly, packageOnly, negativeZero int
	var differences, looser []string
	check := func(c referenceCase, md bool, digest string) {
		input, ok := readerInput(c, md)
		if !ok {
			return
		}
		node, err := parseYAML(input)
		packageRefuses := digest == strings.Repeat("-", 16)
		switch {
		case err != nil && packageRefuses:
			bothRefuse++
		case err != nil:
			packageOnly++ // the reader is stricter: the package reads what it refuses
		case packageRefuses:
			readerOnly++ // the reader reads what the package refuses: a reader looser than the reference
			looser = append(looser, fmt.Sprintf("%s: %.80q", c.Family, input))
		default:
			compared++
			switch got := valueDigest(node, false); {
			case got == digest:
				same++
			case valueDigest(node, true) == digest:
				negativeZero++ // the one difference found: see the README
			default:
				differences = append(differences, fmt.Sprintf("%s: %.120q reads as %s", c.Family, input, canonicalNode(node)))
			}
		}
	}
	for i, c := range manifests {
		check(c, false, golden.Manifest[16*i:16*i+16])
	}
	for i, c := range mds {
		check(c, true, golden.Md[16*i:16*i+16])
	}
	t.Logf("values: %d documents read by both, %d with the same values and %d that differ in -0 only; the reader refuses and the yaml package reads %d, both refuse %d, the reader reads and the yaml package refuses %d", compared, same, negativeZero, packageOnly, bothRefuse, readerOnly)
	flat := strings.Join(strings.Fields(readReadme(t)), " ")
	for _, want := range []string{
		fmt.Sprintf("%d documents are read by both", compared),
		fmt.Sprintf("%d of them with the same values", same),
		fmt.Sprintf("%d that differ only in the integer -0", negativeZero),
		fmt.Sprintf("the reader refuses %d documents that the yaml package reads", packageOnly),
		fmt.Sprintf("both refuse %d", bothRefuse),
		fmt.Sprintf("the reader reads %d that the yaml package refuses", readerOnly),
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("README does not state %q", want)
		}
	}
	if compared < 3000 || packageOnly == 0 || bothRefuse == 0 {
		t.Errorf("the comparison is too small to mean anything: %d compared, %d, %d", compared, packageOnly, bothRefuse)
	}
	for _, d := range differences[:min(len(differences), 20)] {
		t.Errorf("a document that both read has other values in the Go reader and in the yaml package (%d in all): %s", len(differences), d)
	}
	for _, d := range looser[:min(len(looser), 20)] {
		t.Errorf("the Go reader reads a document that the yaml package refuses (%d in all): %s", len(looser), d)
	}
}
