package core

import "testing"

func TestPipelineResultAcceptsIncompleteBlockedContract(t *testing.T) {
	input := AgentInput{Task: TaskInput{
		ExpectedDeliverables: []TaskDeliverableInput{{ID: "report"}},
		EvidenceRequirements: []TaskEvidenceInput{{ID: "proof", Required: true}},
		AcceptanceCriteria:   []TaskAcceptanceInput{{ID: "review"}},
	}}
	result := AgentResult{Summary: Summary{Text: "Missing authoritative mapping and rollback baseline."},
		Blocker: &Blocker{Summary: "Missing authoritative mapping and rollback baseline."}}
	if err := defaultValidator(nil).Validate(input, result); err != nil {
		t.Fatalf("blocked report must reach durable submission: %v", err)
	}
	result.Blocker = nil
	if err := defaultValidator(nil).Validate(input, result); !IsResultValidationError(err) {
		t.Fatalf("incomplete completed report must still fail validation: %v", err)
	}
}

func TestPipelineBlockedResultStillValidatesContent(t *testing.T) {
	for _, test := range []struct {
		name   string
		result AgentResult
	}{
		{"missing summary", AgentResult{Blocker: &Blocker{Summary: "Need evidence"}}},
		{"empty blocker", AgentResult{Summary: Summary{Text: "Need evidence"}, Blocker: &Blocker{Summary: " "}}},
		{"untraceable evidence", AgentResult{Summary: Summary{Text: "Need evidence"}, Blocker: &Blocker{Summary: "Need evidence"}, Evidence: []Evidence{{ID: "unverified"}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := defaultValidator(nil).Validate(AgentInput{}, test.result); !IsResultValidationError(err) {
				t.Fatalf("invalid blocked report must fail validation: %v", err)
			}
		})
	}
}
