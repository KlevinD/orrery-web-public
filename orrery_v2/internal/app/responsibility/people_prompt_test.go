package responsibility

import (
	"strings"
	"testing"
)

func TestBuildResponsibilityPulsePromptKeepsBoundariesAndAuditReferences(t *testing.T) {
	prompt := BuildResponsibilityPulsePrompt(PeopleResponsibilityPortfolio{
		ID: "portfolio-1", SubjectID: "subject-1", SourceRevision: "5:2",
		Responsibilities: []PeopleResolvedResponsibility{{Code: "DELIVERY", Title: "Deliver", Description: "Core duties:\n- Ship work\nForbidden actions:\n- Skip evidence"}},
	})
	for _, want := range []string{"People responsibility capsule", "subject: subject-1", "source revision: 5:2", "[DELIVERY] Deliver", "Forbidden actions:", "do not attempt every duty", "portfolio and revision as audit references"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}
