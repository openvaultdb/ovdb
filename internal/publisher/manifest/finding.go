package manifest

import (
	"fmt"
	"strconv"
)

// Severity says how bad a finding is.
type Severity string

// SeverityError is a finding that makes the documents wrong: they are refused.
const SeverityError Severity = "error"

// Finding is one problem with a document.
type Finding struct {
	Rule     string // a stable id: match on this, never on the message
	Severity Severity
	Document string // the document: "OVDB.md" or the manifest's path
	Line     int    // the line in the document, 0 when there is none
	Message  string // what is wrong and what to write; no control character
}

// String is "document:line: message".
func (f Finding) String() string {
	if f.Line > 0 {
		return f.Document + ":" + strconv.Itoa(f.Line) + ": " + f.Message
	}
	return f.Document + ": " + f.Message
}

// MaxFindings is the most findings reported for one document. When there are
// more, the last one says how many were left out; the verdict does not change.
const MaxFindings = 100

// RuleCapped is the rule of the finding that says findings were left out.
const RuleCapped = "findings-capped"

// collector gathers the findings of one document up to MaxFindings.
type collector struct {
	document string
	findings []Finding
	left     int
}

func (c *collector) add(rule string, line int, format string, args ...any) {
	if len(c.findings) >= MaxFindings {
		c.left++
		return
	}
	c.findings = append(c.findings, Finding{Rule: rule, Severity: SeverityError, Document: c.document, Line: line, Message: fmt.Sprintf(format, args...)})
}

// result returns the findings, with the note that some were left out.
func (c *collector) result() []Finding {
	if c.left > 0 {
		c.findings = append(c.findings, Finding{Rule: RuleCapped, Severity: SeverityError, Document: c.document, Message: fmt.Sprintf("%d more findings are not shown (at most %d are reported)", c.left, MaxFindings)})
	}
	return c.findings
}
