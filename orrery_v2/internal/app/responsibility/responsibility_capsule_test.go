package responsibility

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBuildResponsibilityPulsePromptKeepsBoundariesAndCapsSize(t *testing.T) {
	portfolio := PeopleResponsibilityPortfolio{
		ID: "portfolio-1", SubjectID: "member-openclaw-tiedan", SourceRevision: "39:25",
		Responsibilities: []PeopleResolvedResponsibility{{
			ID: "resp-1", Code: "RESP-POSITION-AGENT", Title: "OpenClaw operator responsibilities", Revision: 1,
			Description: `Summary: Execute authorized work and produce auditable evidence.

Core duties:
- Execute assigned work
- Record material steps

Forbidden actions:
- Expand tool or data permissions

Allowed tool scopes:
- openclaw.agent.run

Human-in-the-loop triggers:
- A high-risk change is required

Evidence expectations:
- Tool call summary
- Verification result`,
		}},
	}
	goal, prompt := BuildResponsibilityPulsePromptWithLimit(portfolio, 1600)
	if !strings.Contains(goal, "portfolio-1") {
		t.Fatalf("goal = %q", goal)
	}
	for _, expected := range []string{"source revision: 39:25", "Execute assigned work", "Expand tool or data permissions", "openclaw.agent.run", "Tool call summary", "Never duplicate"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt missing %q: %s", expected, prompt)
		}
	}
	if utf8.RuneCountInString(prompt) > 1600 {
		t.Fatalf("prompt has %d runes", utf8.RuneCountInString(prompt))
	}
}

func TestBuildResponsibilityPulsePromptDoesNotCopyUnknownLongSections(t *testing.T) {
	portfolio := PeopleResponsibilityPortfolio{
		ID: "portfolio-1", SubjectID: "member-1", SourceRevision: "1:1",
		Responsibilities: []PeopleResolvedResponsibility{{
			ID: "resp-1", Code: "RESP-1", Title: "Responsibility", Revision: 1,
			Description: "Summary: Keep systems healthy.\n\nUnbounded notes:\n- " + strings.Repeat("private background ", 1000),
		}},
	}
	_, prompt := BuildResponsibilityPulsePromptWithLimit(portfolio, 900)
	if strings.Contains(prompt, "private background") || !strings.Contains(prompt, "Keep systems healthy") {
		t.Fatalf("bounded prompt = %q", prompt)
	}
}
