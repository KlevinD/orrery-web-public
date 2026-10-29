package core

import (
	"strings"
	"testing"
)

func TestRuntimeContextPromptCarriesExactCompletionContractIDs(t *testing.T) {
	prompt := buildRuntimeContextPrompt(AgentPromptMessagesRequest{Task: TaskInput{
		ExpectedDeliverables: []TaskDeliverableInput{{ID: "deliverable-1", Title: "Report"}},
		AcceptanceCriteria:   []TaskAcceptanceInput{{ID: "acceptance-1", Description: "Report is complete."}},
		EvidenceRequirements: []TaskEvidenceInput{{ID: "evidence-1", Description: "Report is complete.", Kind: string(EvidenceKindArtifact), Required: true}},
	}})
	for _, expected := range []string{"[deliverable-1]", "[acceptance-1]", "[evidence-1]", "```orrery-agent-result", "runtime_output", "每个 ID", "不能把预期交付 ID 放进 evidence"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt missing %q: %s", expected, prompt)
		}
	}
}
