package manifest

import (
	"regexp"
	"strings"

	"github.com/openvaultdb/ovdb/internal/publisher/rules"
)

// The chains of a concept inside the repository's own graph, as the Directory holds them (meaning.mjs 195-226, called at directory.mjs 767-770 for every
// concept): the concepts a concept extends, nearest first, must not return to a concept already in the chain, must not be more than maxChain, and each must
// be a concept of the graph; a values-of must name one, and its own chain is held the same way. A reference by address to another graph, or to this one
// with a pin, is read by the Directory at a commit that it finds through the MeaningGraph registry (or, for this graph, the repository's history), which a
// check of one repository does not have: the FORM of such a reference is judged here (it needs a pin, which is a full commit id), and a well-formed one
// ends the chain, and nothing is said of what is beyond it.

// maxChain is the longest extends chain (meaning.mjs maxChain).
const maxChain = 50

// conceptAddress is the Directory's address form of a concept reference (meaning.mjs conceptRefPattern): meaning://{host}/{path}/{id}, with an optional ?ref=.
var conceptAddress = regexp.MustCompile(`^meaning://([A-Za-z0-9.-]+(?:/[A-Za-z0-9._-]+)+)/([a-z][a-z0-9]*(?:-[a-z][a-z0-9]*)*)(?:\?ref=([A-Za-z0-9._/-]+))?$`)

// commitID is the Directory's commitPattern (git.mjs): a full commit id in lower-case hexadecimal.
var commitID = regexp.MustCompile(`^[0-9a-f]{40}$`)

// resolved is what a reference reaches: a concept of the graph, a graph that a registry would have to say (outside), or an error.
type resolved struct {
	concept *Node
	id      string
	outside bool
	err     string
}

// resolve reads ref as written inside the own graph, whose address (without meaning://) is own: a bare id and the graph's own unpinned address are in
// the graph.
func resolve(ref string, byID map[string]*Node, own string) resolved {
	var repo, id, pin string
	switch match := conceptAddress.FindStringSubmatch(ref); {
	case isConceptID(ref):
		id = ref
	case match != nil:
		repo, id, pin = match[1], match[2], match[3]
	default:
		return resolved{err: rules.Quote(ref) + " is not a concept reference"}
	}
	if repo != "" && (pin != "" || !strings.EqualFold(repo, own)) {
		// Another graph, or this one pinned: the Directory resolves it through the registry (resolveGraph, meaning.mjs), and the form of the reference is
		// refused whatever any registry says: it needs a pin, and the pin is a full commit id. Only a well-formed reference is the registry's, or, for this
		// graph, the history's, to resolve. (With no graph address to compare with, the manifest has its own finding, and an unpinned address is not judged.)
		switch {
		case pin == "" && own == "":
			return resolved{outside: true}
		case pin == "":
			return resolved{err: "meaning://" + repo + " needs a ?ref= pin (a reference by address to another graph is read at a commit)"}
		case !commitID.MatchString(pin):
			return resolved{err: "meaning://" + repo + "?ref=" + pin + ": a pin is a full 40-character commit id (lower-case hexadecimal)"}
		}
		return resolved{outside: true}
	}
	concept := byID[id]
	if concept == nil {
		return resolved{err: ref + " names concept " + id + ", which this graph does not have"}
	}
	return resolved{concept: concept, id: id}
}

// lineage walks the extends chain of a concept and says what is wrong with it, or "".
func lineage(start *Node, id string, byID map[string]*Node, own string) string {
	seen := map[string]bool{}
	current, currentID := start, id
	for length := 1; ; length++ {
		if seen[currentID] {
			return "extends returns to " + currentID
		}
		seen[currentID] = true
		extends := current.Field("extends")
		if extends == nil {
			return ""
		}
		if length > maxChain {
			return "extends chain is longer than 50 concepts"
		}
		next := resolve(extends.Text, byID, own)
		switch {
		case next.outside:
			return ""
		case next.err != "":
			return "extends: " + next.err
		}
		current, currentID = next.concept, next.id
	}
}

// chains holds the extends chain and the values-of of each concept of the graph, in the order of the file. The concepts are those with a good shape,
// declared once, as the Directory has them.
func (c *collector) chains(concepts []*Node, ids []string, byID map[string]*Node, w MeaningWants) {
	own := ""
	if w.GraphAddress.Usable() {
		own = strings.TrimPrefix(w.GraphAddress.Value, "meaning://")
	}
	for i, concept := range concepts {
		id := ids[i]
		if problem := lineage(concept, id, byID, own); problem != "" {
			c.add("meaning-chain", fieldLine(concept, "extends"), "concept %s: %s", id, problem)
		}
		valuesOf := concept.Field("values-of")
		if valuesOf == nil {
			continue
		}
		target := resolve(valuesOf.Text, byID, own)
		switch {
		case target.outside:
		case target.err != "":
			c.add("meaning-chain", valuesOf.Line, "concept %s: values-of: %s", id, target.err)
		default:
			if problem := lineage(target.concept, target.id, byID, own); problem != "" {
				c.add("meaning-chain", valuesOf.Line, "concept %s: values-of %s: %s", id, target.id, problem)
			}
		}
	}
}
