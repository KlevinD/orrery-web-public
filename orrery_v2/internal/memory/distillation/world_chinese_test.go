package distillation

import (
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
)

func TestWorldConsolidatorChineseRequirements(t *testing.T) {
	for _, tc := range []struct {
		name, previous, current string
		conflict                bool
	}{
		{"report format", "报告必须用表格", "报告不要用表格", true},
		{"reverse update", "报告不得使用表格", "报告必须采用表格", true},
		{"different subject", "报告必须用表格", "邮件不要用表格", false},
		{"different action", "报告必须用表格", "报告不要用图片", false},
		{"same requirement", "报告必须用表格", "报告必须用表格", false},
		{"question", "报告必须用表格", "报告是否不要用表格？", false},
		{"hypothetical", "报告必须用表格", "假设报告不要用表格", false},
		{"conditional clause", "报告必须用表格", "如果客户不同意，报告不要用表格", false},
		{"quote spanning clauses", "报告必须用表格", "客户说“邮件不要用表格，报告不要用表格”", false},
		{"quoted text", "报告必须用表格", "客户说“报告不要用表格”", false},
		{"unrelated negation", "报告必须用表格", "报告必须用表格，邮件不要用表格", false},
		{"double negation", "报告必须用表格", "报告不必不用表格", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
			result, err := NewWorldConsolidator(WorldConsolidatorOptions{Now: func() time.Time { return now }}).Consolidate(
				[]memcore.MemoryRecord{
					experienceRecord("new", "format", tc.current, []string{"format"}, now),
					worldRecord("old", "format", tc.previous, now.Add(-time.Hour)),
				}, WorldRequest{TargetScope: memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "org-1"}, Topic: "format", CreatedBy: agentcore.AgentID("reviewer")},
			)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(result.Candidates[0].ContradictsRefs) > 0; got != tc.conflict {
				t.Fatalf("conflict = %v, want %v; candidate = %+v", got, tc.conflict, result.Candidates[0])
			}
			if tc.name == "same requirement" && len(result.Candidates[0].SupersedesRefs) != 0 {
				t.Fatal("identical Chinese rule was treated as a changed rule")
			}
		})
	}
}
