package delivery

import (
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	missiontask "orrery_v2/internal/mission/task"
)

func TestValidatorRequiresSummaryAndExpectedDeliverables(t *testing.T) {
	contract := missiontask.TaskContract{
		ExpectedDeliverables: []missiontask.ExpectedDeliverable{
			{ID: missiontask.ExpectedDeliverableID("expected-001"), Title: "Patch"},
			{ID: missiontask.ExpectedDeliverableID("expected-002"), Title: "Tests"},
		},
	}
	result := agentcore.AgentResult{
		Summary: agentcore.Summary{Text: "done"},
		Deliverables: []agentcore.Deliverable{{
			ID:                    agentcore.DeliverableID("deliverable-001"),
			ExpectedDeliverableID: "expected-001",
			Title:                 "Patch",
			ArtifactRef:           agentcore.ArtifactRef{ID: "artifact-001", Kind: agentcore.ArtifactKindCode},
		}},
	}

	errs := NewValidator(contract).Validate(result)

	assertErrorCode(t, errs, CodeMissingExpectedDeliverable)
}

func TestValidatorRejectsUntraceableDeliverableArtifact(t *testing.T) {
	contract := missiontask.TaskContract{
		ExpectedDeliverables: []missiontask.ExpectedDeliverable{
			{ID: missiontask.ExpectedDeliverableID("expected-001"), Title: "Patch"},
		},
	}
	result := agentcore.AgentResult{
		Summary: agentcore.Summary{Text: "done"},
		Deliverables: []agentcore.Deliverable{{
			ID:                    agentcore.DeliverableID("deliverable-001"),
			ExpectedDeliverableID: "expected-001",
			Title:                 "Patch",
		}},
	}

	errs := NewValidator(contract).Validate(result)

	assertErrorCode(t, errs, CodeUntraceableDeliverable)
}

func TestValidatorAllowsIncompleteBlockedDeliverables(t *testing.T) {
	contract := missiontask.TaskContract{ExpectedDeliverables: []missiontask.ExpectedDeliverable{{ID: "expected-001", Title: "Draft"}}}
	result := agentcore.AgentResult{
		Summary: agentcore.Summary{Text: "Cover quality review failed."},
		Blocker: &agentcore.Blocker{Summary: "Cover quality review failed."},
	}
	if errs := NewValidator(contract).Validate(result); len(errs) != 0 {
		t.Fatalf("blocked work does not require a completed draft: %#v", errs)
	}
	result.Deliverables = []agentcore.Deliverable{{ID: "partial-1", ExpectedDeliverableID: "expected-001", Title: "Draft"}}
	assertErrorCode(t, NewValidator(contract).Validate(result), CodeUntraceableDeliverable)
}

func assertErrorCode(t *testing.T, errs []ValidationError, code ValidationCode) {
	t.Helper()
	for _, err := range errs {
		if err.Code == code {
			return
		}
	}
	t.Fatalf("expected validation code %s in %#v", code, errs)
}
