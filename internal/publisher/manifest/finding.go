package manifest

import (
	"fmt"
	"strconv"

	"github.com/openvaultdb/ovdb/internal/publisher/rules"
)

// Severity says how bad a finding is.
type Severity string

// SeverityError is a finding that makes the documents wrong: they are refused.
const SeverityError Severity = "error"

// Finding is one problem with a document.
type Finding struct {
	Rule     string // a stable id: match on this, never on the message
	Severity Severity
	Document string // the document: "OVDB.md" or the manifest's path, as the caller gave it (String quotes it when it is not plain)
	Line     int    // the line where the value starts, 0 when there is none; see the README
	Message  string // what is wrong and what to write; printable ASCII, at most MaxMessageBytes
}

// String is "document:line: message". A document name that is not plain
// printable ASCII is quoted and cut, so no finding prints a control character.
func (f Finding) String() string {
	name := f.Document
	if len(name) > 200 || !printable(name) {
		name = rules.Quote(name)
	}
	if f.Line > 0 {
		return name + ":" + strconv.Itoa(f.Line) + ": " + f.Message
	}
	return name + ": " + f.Message
}

// MaxFindings is the most findings reported by one call, whatever the number of
// documents it judges. When there are more, the last finding is RuleCapped and
// says how many were left out; the verdict does not change.
const MaxFindings = 100

// MaxMessageBytes is the longest message of a finding. It is enforced where
// findings are made, so no input, however long or repetitive, makes a longer one.
const MaxMessageBytes = 400

// RuleCapped is the rule of the finding that says findings were left out.
const RuleCapped = "findings-capped"

// budget is the number of findings a call may still report; the collectors of
// the documents of one call share it, so the cap is one for the call.
type budget struct{ left, dropped int }

func newBudget() *budget { return &budget{left: MaxFindings} }

// notice is the last finding of a call that left some out, or nothing.
func (b *budget) notice(document string) []Finding {
	if b.dropped == 0 {
		return nil
	}
	return []Finding{{Rule: RuleCapped, Severity: SeverityError, Document: document, Message: fmt.Sprintf("%d more findings are not shown (at most %d are reported)", b.dropped, MaxFindings)}}
}

// collector gathers the findings of one document.
type collector struct {
	document string
	findings []Finding
	notices  []Finding
	budget   *budget
}

func newCollector(document string, b *budget) *collector {
	return &collector{document: document, budget: b}
}

// add is the one place where a finding is made: it keeps to the budget and to
// MaxMessageBytes.
func (c *collector) add(rule string, line int, format string, args ...any) {
	if c.budget.left <= 0 {
		c.budget.dropped++
		return
	}
	c.budget.left--
	c.findings = append(c.findings, Finding{Rule: rule, Severity: SeverityError, Document: c.document, Line: line, Message: bounded(fmt.Sprintf(format, args...))})
}

// bounded cuts a message to MaxMessageBytes.
func bounded(message string) string {
	if len(message) > MaxMessageBytes {
		return message[:MaxMessageBytes-3] + "..."
	}
	return message
}

// notice makes a notice: a finding of severity SeverityNotice, kept apart from the findings. It keeps to MaxMessageBytes, and is not counted in the budget of
// findings, because it is not one and changes no verdict.
func (c *collector) notice(rule string, line int, format string, args ...any) {
	c.notices = append(c.notices, Finding{Rule: rule, Severity: SeverityNotice, Document: c.document, Line: line, Message: bounded(fmt.Sprintf(format, args...))})
}

// printable reports whether s is printable ASCII.
func printable(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}
