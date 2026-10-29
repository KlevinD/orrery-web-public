package capture

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memaudit "orrery_v2/internal/memory/audit"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
	memtestkit "orrery_v2/internal/memory/testkit"
)

type recordingRecordMetadataSink struct {
	records []memcore.MemoryRecord
}

func (s *recordingRecordMetadataSink) PublishMaterializedMemoryRecord(_ context.Context, record memcore.MemoryRecord) error {
	s.records = append(s.records, record.Clone())
	return nil
}

func TestPolicyClassifiesNormalEvidenceBackedFactsAsAccepted(t *testing.T) {
	policy := DefaultCapturePolicy{}
	decision := policy.Decide(CandidateDraft{
		Layer:        memcore.MemoryLayerFact,
		Summary:      "Tests passed for backend package.",
		EvidenceRefs: []string{"tool-call-go-test"},
		SourceType:   memcore.SourceQAResult,
	})

	if decision.Status != memcore.CandidateAccepted {
		t.Fatalf("Status = %q, want accepted", decision.Status)
	}
	if decision.ReviewRequired {
		t.Fatalf("ReviewRequired = true, want false")
	}
	if decision.Sensitivity != memcore.SensitivityNormal {
		t.Fatalf("Sensitivity = %q, want normal", decision.Sensitivity)
	}
}

func TestPolicyKeepsSensitiveCandidatesProposedAndReviewRequired(t *testing.T) {
	policy := DefaultCapturePolicy{}
	for _, summary := range []string{
		"Candidate Alice compensation is 220000.",
		"Performance disagreement with employee Bob.",
		"Legal dispute details require counsel review.",
	} {
		decision := policy.Decide(CandidateDraft{
			Layer:      memcore.MemoryLayerFact,
			Summary:    summary,
			SourceType: memcore.SourceDiscussionMessage,
		})
		if decision.Status != memcore.CandidateProposed {
			t.Fatalf("Status for %q = %q, want proposed", summary, decision.Status)
		}
		if !decision.ReviewRequired {
			t.Fatalf("ReviewRequired for %q = false, want true", summary)
		}
		if decision.Sensitivity == memcore.SensitivityNormal {
			t.Fatalf("Sensitivity for %q = normal, want sensitive", summary)
		}
	}
}

func TestEpisodicWriterSavesCandidateIdempotentlyAndAuditsCreation(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	if err := store.SaveProfile(ctx, memcore.MemoryProfile{
		ID:         "memory-agent-1",
		OwnerRef:   memcore.OwnerRef{AgentID: "agent-1"},
		MemoryKind: agentcore.MemoryKindPersonal,
		DefaultScope: memcore.MemoryScope{
			Kind: memcore.ScopeAgent,
			ID:   "agent-1",
		},
		VisibilityPolicy: memcore.VisibilityPolicy{Mode: memcore.VisibilityOwnerOnly},
		Enabled:          true,
	}); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	audit := &memtestkit.MemoryAuditRecorder{}
	writer := NewEpisodicWriter(EpisodicWriterOptions{
		Repository: store,
		Audit:      audit,
		Policy:     DefaultCapturePolicy{},
		Now:        func() time.Time { return time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC) },
	})
	draft := CandidateDraft{
		ID:            "candidate-result-run-1",
		MissionID:     "mission-1",
		SourceType:    memcore.SourceAgentRunDiary,
		SourceRef:     "result-1",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "agent-1"},
		ProposedKind:  agentcore.MemoryKindPersonal,
		Layer:         memcore.MemoryLayerFact,
		Topic:         "memory/2026-06-14.md",
		Summary:       "Tests passed.",
		EvidenceRefs:  []string{"tool-call-go-test"},
		CreatedBy:     agentcore.AgentID("agent-1"),
	}

	first, err := writer.CaptureCandidate(ctx, draft)
	if err != nil {
		t.Fatalf("CaptureCandidate first: %v", err)
	}
	second, err := writer.CaptureCandidate(ctx, draft)
	if err != nil {
		t.Fatalf("CaptureCandidate second: %v", err)
	}
	if first.ID != second.ID || first.Status != memcore.CandidateAccepted {
		t.Fatalf("captures = %+v / %+v, want same accepted candidate", first, second)
	}
	candidates, err := store.ListCandidates(ctx, memstore.CandidateQuery{MissionID: "mission-1"})
	if err != nil {
		t.Fatalf("ListCandidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v, want one idempotent candidate", candidates)
	}
	records, err := store.FindRecords(ctx, memstore.RecordQuery{ProfileIDs: []agentcore.MemoryProfileID{"memory-agent-1"}})
	if err != nil {
		t.Fatalf("FindRecords: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("records = %+v", records)
	}
	events := audit.Snapshot()
	if len(events) != 1 ||
		events[0].Type != memaudit.EventMemoryCandidateCreated ||
		events[0].SubjectID != first.ID {
		t.Fatalf("audit events = %+v", events)
	}
}

func TestEpisodicWriterMaterializesAcceptedMissionCandidateIntoCollectiveProfile(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	metadata := &recordingRecordMetadataSink{}
	writer := NewEpisodicWriter(EpisodicWriterOptions{
		Repository: store,
		Metadata:   metadata,
		Policy:     DefaultCapturePolicy{},
		Now:        func() time.Time { return time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC) },
	})

	candidate, err := writer.CaptureCandidate(ctx, CandidateDraft{
		ID:            "candidate-qa-mission-1",
		MissionID:     "mission-1",
		SourceType:    memcore.SourceQAResult,
		SourceRef:     "qa-1",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		ProposedKind:  agentcore.MemoryKindMission,
		Layer:         memcore.MemoryLayerFact,
		Topic:         "qa_result",
		Summary:       "任务验收通过。",
		EvidenceRefs:  []string{"evidence-qa-1"},
		CreatedBy:     "qa-agent",
	})
	if err != nil {
		t.Fatalf("CaptureCandidate: %v", err)
	}
	if candidate.Status != memcore.CandidateAccepted {
		t.Fatalf("candidate status = %q", candidate.Status)
	}
	profileID := memcore.ScopeMemoryProfileID(memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"})
	profile, err := store.GetProfile(ctx, profileID)
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if profile.MemoryKind != agentcore.MemoryKindMission || profile.VisibilityPolicy.Mode != memcore.VisibilityScopedMembers {
		t.Fatalf("mission profile = %+v", profile)
	}
	records, err := store.FindRecords(ctx, memstore.RecordQuery{ProfileIDs: []agentcore.MemoryProfileID{profileID}})
	if err != nil {
		t.Fatalf("FindRecords: %v", err)
	}
	if len(records) != 1 || records[0].CandidateID != candidate.ID || records[0].Visibility.ScopeID != "mission-1" {
		t.Fatalf("records = %+v", records)
	}
	if len(metadata.records) != 1 || metadata.records[0].ID != records[0].ID {
		t.Fatalf("metadata records = %+v", metadata.records)
	}
}

func TestEpisodicWriterReconcilesOnlyPendingAgentResultsIdempotently(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	audit := &memtestkit.MemoryAuditRecorder{}
	writer := NewEpisodicWriter(EpisodicWriterOptions{
		Repository: store,
		Audit:      audit,
		Now:        func() time.Time { return time.Date(2026, 8, 17, 15, 0, 0, 0, time.UTC) },
	})
	pending := memcore.MemoryCandidate{
		ID:             "candidate.agent_result.legacy-run",
		MissionID:      "mission-legacy",
		SourceType:     memcore.SourceSummary,
		SourceRef:      "legacy-result",
		ProposedScope:  memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-legacy"},
		ProposedKind:   agentcore.MemoryKindMission,
		ProposedLayer:  memcore.MemoryLayerFact,
		Topic:          "agent_result",
		Summary:        "Legacy task completed successfully.",
		CreatedBy:      "agent-legacy",
		Status:         memcore.CandidateProposed,
		ReviewRequired: true,
		ReviewReason:   "legacy review gate",
		CreatedAt:      time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC),
	}
	manual := pending.Clone()
	manual.ID = "candidate.manual.legacy-run"
	manual.SourceType = memcore.SourceDiscussionMessage
	if err := store.SaveCandidate(ctx, pending); err != nil {
		t.Fatalf("SaveCandidate pending: %v", err)
	}
	if err := store.SaveCandidate(ctx, manual); err != nil {
		t.Fatalf("SaveCandidate manual: %v", err)
	}

	report, err := writer.ReconcilePendingAgentResults(ctx, 100)
	if err != nil {
		t.Fatalf("ReconcilePendingAgentResults: %v", err)
	}
	if report != (AgentResultReconciliationReport{Scanned: 1, Accepted: 1, Materialized: 1}) {
		t.Fatalf("report = %+v", report)
	}
	saved, err := store.GetCandidate(ctx, pending.ID)
	if err != nil {
		t.Fatalf("GetCandidate pending: %v", err)
	}
	if saved.Status != memcore.CandidateAccepted || saved.ReviewRequired || saved.ReviewReason != "" {
		t.Fatalf("reconciled candidate = %+v", saved)
	}
	untouched, err := store.GetCandidate(ctx, manual.ID)
	if err != nil {
		t.Fatalf("GetCandidate manual: %v", err)
	}
	if untouched.Status != memcore.CandidateProposed || !untouched.ReviewRequired {
		t.Fatalf("manual candidate changed = %+v", untouched)
	}
	recordID := "memory.record." + idSegment(pending.ID)
	if record, err := store.GetRecord(ctx, recordID); err != nil {
		t.Fatalf("GetRecord: %v", err)
	} else if record.CandidateID != pending.ID || record.Scope.ID != "mission-legacy" {
		t.Fatalf("record = %+v", record)
	}

	second, err := writer.ReconcilePendingAgentResults(ctx, 100)
	if err != nil {
		t.Fatalf("ReconcilePendingAgentResults second: %v", err)
	}
	if second != (AgentResultReconciliationReport{Scanned: 1}) {
		t.Fatalf("second report = %+v", second)
	}
	if events := audit.Snapshot(); len(events) != 2 ||
		events[0].Type != memaudit.EventMemoryCandidateAccepted ||
		events[1].Type != memaudit.EventMemoryRecordCreated {
		t.Fatalf("audit events = %+v", events)
	}
}
