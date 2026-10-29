package problemhub

import (
	"context"
	"testing"
	"time"

	memcore "orrery_v2/internal/memory/core"
	memtestkit "orrery_v2/internal/memory/testkit"
)

func TestMemoryLessonSinkPreservesHumanReviewStateOnReplay(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	sink := NewMemoryLessonSink(store)
	proposal := LessonProposal{
		ID: "lesson-1", TenantID: "tenant-a", ProblemCaseID: "problem-1", Topic: "reliability.transient",
		Summary: "Verified resolution: runtime timeout", Lesson: "A bounded retry restored the run.",
		Warning: "Match the original context.", EvidenceRefs: []string{"evidence-1"},
		LineageRefs: []string{"problem_case:problem-1", "repair_rule:rule-1"}, Confidence: 0.7,
		CreatedBy: "problem-hub", CreatedAt: time.Date(2026, 9, 3, 5, 0, 0, 0, time.UTC),
	}
	if _, err := sink.ProposeProblemLesson(ctx, proposal); err != nil {
		t.Fatalf("ProposeProblemLesson: %v", err)
	}
	candidate, err := store.GetCandidate(ctx, proposal.ID)
	if err != nil {
		t.Fatalf("GetCandidate: %v", err)
	}
	if candidate.SourceType != memcore.SourceProblemCase || candidate.SourceRef != "problem-1" ||
		candidate.ProposedLayer != memcore.MemoryLayerExperience || candidate.ProposedScope.Kind != memcore.ScopeOrganization ||
		candidate.ProposedScope.ID != "tenant-a" || !candidate.ReviewRequired || candidate.Status != memcore.CandidateProposed {
		t.Fatalf("candidate = %+v", candidate)
	}
	if len(candidate.EvidenceRefs) != 1 || candidate.EvidenceRefs[0] != "evidence_item:evidence-1" {
		t.Fatalf("candidate evidence = %+v", candidate.EvidenceRefs)
	}
	if err := store.UpdateCandidateStatus(ctx, proposal.ID, memcore.CandidateRejected); err != nil {
		t.Fatalf("reject candidate: %v", err)
	}
	if _, err := sink.ProposeProblemLesson(ctx, proposal); err != nil {
		t.Fatalf("replay proposal: %v", err)
	}
	replayed, err := store.GetCandidate(ctx, proposal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Status != memcore.CandidateRejected {
		t.Fatalf("human review state was overwritten: %s", replayed.Status)
	}
}
