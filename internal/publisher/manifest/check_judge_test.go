package manifest

import (
	"slices"
	"strings"
	"testing"
)

func TestJudgeSharesOneBudgetAcrossDocuments(t *testing.T) {
	j, bad := NewJudge(Publisher)
	if j == nil || bad != nil {
		t.Fatalf("NewJudge = %v, %v", j, bad)
	}
	md, findings := j.OVDBMd([]byte("---\novdb: 1\npublish:\n  - ./a.yaml\n  - ./b.yaml\n---\n"))
	if len(findings) != 0 || !slices.Equal(md.Entries, []string{"a.yaml", "b.yaml"}) || !slices.Equal(md.EntryLines, []int{4, 5}) {
		t.Fatalf("OVDB.md: %v, entries %v at %v", findings, md.Entries, md.EntryLines)
	}
	var shown int
	for range 5 {
		m, f := j.Manifest([]byte(strings.Repeat("k: [\n", 1)), "a.yaml")
		if m.Read {
			t.Error("a broken manifest was read")
		}
		shown += len(f)
	}
	for range 150 {
		shown += len(j.Report("a.yaml", "x-rule", 3, "problem %d", 1))
	}
	if shown != MaxFindings {
		t.Errorf("%d findings shown, want the budget of %d", shown, MaxFindings)
	}
	if n := j.Notice("OVDB.md"); len(n) != 1 || n[0].Rule != RuleCapped || !strings.Contains(n[0].Message, "more findings are not shown") {
		t.Errorf("notice %v", n)
	}
	fresh, _ := NewJudge(Directory)
	if fresh.Notice("OVDB.md") != nil {
		t.Error("a notice without findings left out")
	}
	if got := fresh.Report("d", "r", 0, "%s", strings.Repeat("x", 1000)); len(got) != 1 || len(got[0].Message) > MaxMessageBytes {
		t.Errorf("long message: %v", got)
	}
}

func TestJudgeRefusesAnUnknownProfile(t *testing.T) {
	j, findings := NewJudge(Profile(9))
	if j != nil || len(findings) != 1 || findings[0].Rule != RuleProfile {
		t.Errorf("NewJudge = %v, %v", j, findings)
	}
}
