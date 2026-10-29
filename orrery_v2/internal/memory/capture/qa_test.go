package capture

import (
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	missiontask "orrery_v2/internal/mission/task"
)

func TestBuildQADraftCreatesEvidenceBackedAcceptedFactSource(t *testing.T) {
	qa := missiontask.QAResultRecord{
		ID:        missiontask.QAResultID("qa-1"),
		TaskID:    missiontask.TaskID("task-1"),
		CheckedBy: agentcore.AgentID("qa-agent"),
		Status:    missiontask.QAStatusApproved,
		EvidenceResults: []missiontask.EvidenceCheckResult{{
			RequirementID: missiontask.EvidenceRequirementID("evidence-required-1"),
			EvidenceID:    agentcore.EvidenceID("evidence-1"),
			Status:        missiontask.CheckStatusPassed,
		}},
		CheckedAt: time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC),
	}

	draft := BuildQADraft(qa, "mission-1")

	if draft.ID != "candidate.qa_result.qa-1" {
		t.Fatalf("ID = %q", draft.ID)
	}
	if draft.SourceType != memcore.SourceQAResult || draft.Layer != memcore.MemoryLayerFact {
		t.Fatalf("source/layer = %s/%s", draft.SourceType, draft.Layer)
	}
	if len(draft.EvidenceRefs) != 1 || draft.EvidenceRefs[0] != "evidence:evidence-1" {
		t.Fatalf("EvidenceRefs = %+v", draft.EvidenceRefs)
	}
}
