package capture

import (
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	missioncore "orrery_v2/internal/mission/core"
)

func TestBuildEvidenceDraftCreatesEvidenceSourceFactCandidate(t *testing.T) {
	evidence := agentcore.Evidence{
		ID:                    agentcore.EvidenceID("evidence-1"),
		EvidenceRequirementID: "evidence-required-1",
		AcceptanceCriterionID: "acceptance-1",
		Kind:                  agentcore.EvidenceKindTestLog,
		ArtifactRef:           agentcore.ArtifactRef{ID: "artifact-1", Kind: agentcore.ArtifactKindLog},
	}

	draft := BuildEvidenceDraft(evidence, EvidenceCaptureOptions{
		MissionID: missioncore.MissionID("mission-1"),
		TaskID:    missioncore.TaskID("task-1"),
		AgentID:   agentcore.AgentID("agent-1"),
		CreatedAt: time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC),
	})

	if draft.ID != "candidate.evidence.evidence-1" {
		t.Fatalf("ID = %q", draft.ID)
	}
	if draft.SourceType != memcore.SourceEvidence || draft.Layer != memcore.MemoryLayerFact {
		t.Fatalf("source/layer = %s/%s", draft.SourceType, draft.Layer)
	}
	if draft.SourceRef != "evidence-1" {
		t.Fatalf("SourceRef = %q", draft.SourceRef)
	}
	if len(draft.EvidenceRefs) != 3 {
		t.Fatalf("EvidenceRefs = %+v, want evidence, artifact, and acceptance refs", draft.EvidenceRefs)
	}
}

func TestBuildDecisionDraftCreatesDecisionSourceFactCandidate(t *testing.T) {
	decision := agentcore.Decision{
		ID:      agentcore.DecisionID("decision-1"),
		Summary: "Use deterministic memory capture.",
	}

	draft := BuildDecisionDraft(decision, DecisionCaptureOptions{
		MissionID: missioncore.MissionID("mission-1"),
		TaskID:    missioncore.TaskID("task-1"),
		AgentID:   agentcore.AgentID("agent-1"),
		CreatedAt: time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC),
	})

	if draft.ID != "candidate.decision.decision-1" {
		t.Fatalf("ID = %q", draft.ID)
	}
	if draft.SourceType != memcore.SourceDecision || draft.Layer != memcore.MemoryLayerFact {
		t.Fatalf("source/layer = %s/%s", draft.SourceType, draft.Layer)
	}
	if draft.Summary != "Use deterministic memory capture." {
		t.Fatalf("Summary = %q", draft.Summary)
	}
	if len(draft.LineageRefs) != 2 {
		t.Fatalf("LineageRefs = %+v, want decision plus task refs", draft.LineageRefs)
	}
}
