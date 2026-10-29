package store_test

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
	memtestkit "orrery_v2/internal/memory/testkit"
)

func TestStoreManagesCandidateLifecycleBeforeRecordCreation(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	candidate := memcore.MemoryCandidate{
		ID:            "candidate-1",
		MissionID:     "mission-1",
		SourceType:    memcore.SourceDiscussionMessage,
		SourceRef:     "message-1",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		ProposedKind:  agentcore.MemoryKindMission,
		Summary:       "A decision was made.",
		EvidenceRefs:  []string{"message-1"},
		CreatedBy:     agentcore.AgentID("agent-a"),
		Status:        memcore.CandidateProposed,
	}

	if err := store.SaveCandidate(ctx, candidate); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	proposed, err := store.ListCandidates(ctx, memstore.CandidateQuery{MissionID: "mission-1", Status: memcore.CandidateProposed})
	if err != nil {
		t.Fatalf("ListCandidates: %v", err)
	}
	if len(proposed) != 1 || proposed[0].ID != candidate.ID {
		t.Fatalf("proposed = %+v", proposed)
	}
	if err := store.UpdateCandidateStatus(ctx, candidate.ID, memcore.CandidateAccepted); err != nil {
		t.Fatalf("UpdateCandidateStatus: %v", err)
	}
	accepted, err := store.GetCandidate(ctx, candidate.ID)
	if err != nil {
		t.Fatalf("GetCandidate: %v", err)
	}
	if !accepted.CanCreateRecordDirectly() {
		t.Fatalf("accepted candidate should allow record creation: %+v", accepted)
	}
}
