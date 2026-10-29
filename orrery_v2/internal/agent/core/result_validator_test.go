package core

import "testing"

func TestValidatePipelineResultRejectsMissingAcceptanceMapping(t *testing.T) {
	input := validAgentInput()
	result := AgentResult{
		ID:        "result-1",
		MissionID: "mission-1",
		TaskID:    "task-1",
		AgentID:   "agent-1",
		RunID:     "run-1",
		Summary:   Summary{Text: "done"},
		Deliverables: []Deliverable{{
			ID:                    "deliverable-1",
			ExpectedDeliverableID: "deliverable-1",
			ArtifactRef:           ArtifactRef{ID: "artifact-1", Kind: ArtifactKindCode, URI: "file://diff.patch"},
		}},
		Evidence: []Evidence{{
			ID:                    "evidence-1",
			EvidenceRequirementID: "evidence-1",
			Kind:                  EvidenceKindTestLog,
			ToolLogRef:            ToolLogRef{ID: "tool-log-1"},
		}},
	}
	err := validatePipelineResult(input, result)
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !IsResultValidationError(err) {
		t.Fatalf("expected result validation error, got %T: %v", err, err)
	}
}
