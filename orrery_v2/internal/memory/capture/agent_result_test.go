package capture

import (
	"context"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
	memtestkit "orrery_v2/internal/memory/testkit"
	missioncore "orrery_v2/internal/mission/core"
)

func TestBuildAgentResultDraftExcludesHiddenTraceAndUsesSourceRefs(t *testing.T) {
	result := agentcore.AgentResult{
		ID:        "result-1",
		MissionID: missioncore.MissionID("mission-1"),
		TaskID:    missioncore.TaskID("task-1"),
		AgentID:   agentcore.AgentID("agent-1"),
		RunID:     agentcore.AgentRunID("run-1"),
		Summary:   agentcore.Summary{Text: "Implemented feature with tests."},
		Deliverables: []agentcore.Deliverable{{
			ID:          agentcore.DeliverableID("deliverable-1"),
			Title:       "Patch",
			ArtifactRef: agentcore.ArtifactRef{ID: "artifact-1", Kind: agentcore.ArtifactKindCode, URI: "file://patch"},
		}},
		Evidence: []agentcore.Evidence{{
			ID:         agentcore.EvidenceID("evidence-1"),
			Kind:       agentcore.EvidenceKindToolLog,
			ToolLogRef: agentcore.ToolLogRef{ID: "tool-log-1"},
		}},
		Decisions:        []agentcore.Decision{{ID: agentcore.DecisionID("decision-1"), Summary: "Use deterministic capture."}},
		Requests:         []agentcore.AgentRequest{{ID: agentcore.AgentRequestID("request-1"), Kind: agentcore.AgentRequestKindReview, Summary: "Review candidate."}},
		InternalTraceRef: "store://trace/run-1",
		CreatedAt:        time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC),
	}
	sourceRefs := []agentcore.ContextSourceRef{{
		Kind: agentcore.ContextSourceKindTaskContract,
		Ref:  agentcore.ContextSourceTypedRef{TaskContractRef: &agentcore.TaskContractRef{ID: "task-1"}},
	}}

	draft := BuildAgentResultDraft(result, AgentResultCaptureOptions{ContextSourceRefs: sourceRefs})

	if draft.ID != "candidate.agent_result.run-1" {
		t.Fatalf("ID = %q", draft.ID)
	}
	if draft.Layer != memcore.MemoryLayerFact || draft.SourceType != memcore.SourceSummary {
		t.Fatalf("draft layer/source = %s/%s", draft.Layer, draft.SourceType)
	}
	if draft.SourceRef != "result-1" {
		t.Fatalf("SourceRef = %q, want result-1", draft.SourceRef)
	}
	if contains(draft.Narrative, "store://trace") {
		t.Fatalf("draft leaked hidden trace ref: %q", draft.Narrative)
	}
	if len(draft.EvidenceRefs) == 0 || !contains(draft.EvidenceRefs[0], "evidence-1") {
		t.Fatalf("EvidenceRefs = %+v", draft.EvidenceRefs)
	}
	if len(draft.LineageRefs) == 0 || !contains(draft.LineageRefs[0], "task_contract:task-1") {
		t.Fatalf("LineageRefs = %+v", draft.LineageRefs)
	}
}

func TestCapturingCommitterSubmitsThenCapturesIdempotentFactCandidate(t *testing.T) {
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
	writer := NewEpisodicWriter(EpisodicWriterOptions{Repository: store, Policy: DefaultCapturePolicy{}})
	inner := &recordingCommitter{}
	committer := NewAgentResultCaptureCommitter(inner, writer)
	result := agentcore.AgentResult{
		ID:        "result-1",
		MissionID: missioncore.MissionID("mission-1"),
		TaskID:    missioncore.TaskID("task-1"),
		AgentID:   agentcore.AgentID("agent-1"),
		RunID:     agentcore.AgentRunID("run-1"),
		Summary:   agentcore.Summary{Text: "Tests passed."},
		Evidence:  []agentcore.Evidence{{ID: agentcore.EvidenceID("evidence-1"), Kind: agentcore.EvidenceKindTestLog, ArtifactRef: agentcore.ArtifactRef{ID: "artifact-1"}}},
		CreatedAt: time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC),
	}

	if err := committer.SubmitAgentResult(ctx, result, agentcore.CommitOptions{IdempotencyKey: "run-1"}); err != nil {
		t.Fatalf("SubmitAgentResult: %v", err)
	}
	if inner.calls != 1 {
		t.Fatalf("inner calls = %d, want 1", inner.calls)
	}
	candidates, err := store.ListCandidates(ctx, memstore.CandidateQuery{MissionID: "mission-1"})
	if err != nil {
		t.Fatalf("ListCandidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v", candidates)
	}
	var hasMission bool
	for _, candidate := range candidates {
		if candidate.ID == "candidate.agent_result.run-1" &&
			candidate.ProposedLayer == memcore.MemoryLayerFact &&
			candidate.Status == memcore.CandidateAccepted &&
			!candidate.ReviewRequired {
			hasMission = true
		}
	}
	if !hasMission {
		t.Fatalf("captured candidates = %+v", candidates)
	}
	records, err := store.FindRecords(ctx, memstore.RecordQuery{ProfileIDs: []agentcore.MemoryProfileID{"memory-agent-1"}})
	if err != nil {
		t.Fatalf("FindRecords: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("records = %+v", records)
	}
	missionProfileID := memcore.ScopeMemoryProfileID(memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"})
	missionRecords, err := store.FindRecords(ctx, memstore.RecordQuery{ProfileIDs: []agentcore.MemoryProfileID{missionProfileID}})
	if err != nil {
		t.Fatalf("FindRecords mission: %v", err)
	}
	if len(missionRecords) != 1 || missionRecords[0].CandidateID != "candidate.agent_result.run-1" || missionRecords[0].Topic != "agent_result" {
		t.Fatalf("mission records = %+v", missionRecords)
	}
}

func TestBuildAgentRunDiaryDraftUsesPersonalScopeAndNoHiddenTrace(t *testing.T) {
	result := agentcore.AgentResult{
		ID:               "result-1",
		MissionID:        missioncore.MissionID("mission-1"),
		TaskID:           missioncore.TaskID("task-1"),
		AgentID:          agentcore.AgentID("agent-1"),
		RunID:            agentcore.AgentRunID("run-1"),
		Summary:          agentcore.Summary{Text: "Implemented feature with tests."},
		InternalTraceRef: "store://trace/run-1",
		CreatedAt:        time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC),
	}

	draft := BuildAgentRunDiaryDraft(result, AgentResultCaptureOptions{})

	if draft.ID != "candidate.agent_diary.agent-1.run-1" {
		t.Fatalf("ID = %q", draft.ID)
	}
	if draft.SourceType != memcore.SourceAgentRunDiary ||
		draft.ProposedScope.Kind != memcore.ScopeAgent ||
		draft.ProposedScope.ID != "agent-1" ||
		draft.ProposedKind != agentcore.MemoryKindPersonal ||
		draft.Topic != "memory/2026-06-14.md" {
		t.Fatalf("draft = %+v", draft)
	}
	if !contains(strings.Join(draft.Tags, ","), "diary_date:2026-06-14") ||
		!contains(strings.Join(draft.Tags, ","), "diary_path:memory/2026-06-14.md") ||
		!contains(draft.Narrative, "Diary: memory/2026-06-14.md") {
		t.Fatalf("draft daily diary metadata = %+v narrative=%q", draft.Tags, draft.Narrative)
	}
	if contains(draft.Narrative, "store://trace") {
		t.Fatalf("draft leaked hidden trace ref: %q", draft.Narrative)
	}
}

func TestNewAgentResultCaptureCommitterAllowsNilWriter(t *testing.T) {
	inner := &recordingCommitter{}
	committer := NewAgentResultCaptureCommitter(inner, nil)
	err := committer.SubmitAgentResult(context.Background(), agentcore.AgentResult{
		ID:      "result-1",
		RunID:   agentcore.AgentRunID("run-1"),
		Summary: agentcore.Summary{Text: "ok"},
	}, agentcore.CommitOptions{IdempotencyKey: "run-1"})
	if err != nil {
		t.Fatalf("SubmitAgentResult: %v", err)
	}
	if inner.calls != 1 {
		t.Fatalf("inner calls = %d, want 1", inner.calls)
	}
}

func TestCapturingCommitterSkipsOperationalTraceAndCapabilityReport(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	inner := &recordingCommitter{}
	committer := NewAgentResultCaptureCommitter(inner, NewEpisodicWriter(EpisodicWriterOptions{Repository: store}))
	result := agentcore.AgentResult{
		ID:        "result-trace",
		MissionID: "mission-1",
		AgentID:   "agent-1",
		RunID:     "run-trace",
		Summary: agentcore.Summary{Text: `<think>inspect tools and retry</think>
## Personal Assistant knowledge-read request, capability gap observed
Workarounds attempted: bash returned fetch failed.
C:\\workspace\\trace.log`},
		Evidence:  []agentcore.Evidence{{ID: "evidence-trace", Kind: agentcore.EvidenceKindToolLog}},
		CreatedAt: time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC),
	}

	if err := committer.SubmitAgentResult(ctx, result, agentcore.CommitOptions{}); err != nil {
		t.Fatalf("SubmitAgentResult: %v", err)
	}
	if inner.calls != 1 {
		t.Fatalf("inner calls = %d, want 1", inner.calls)
	}
	candidates, err := store.ListCandidates(ctx, memstore.CandidateQuery{MissionID: "mission-1"})
	if err != nil {
		t.Fatalf("ListCandidates: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("operational trace became memory: %+v", candidates)
	}
}

func TestBuildAgentResultDraftSanitizesHiddenThoughtAndCode(t *testing.T) {
	draft := BuildAgentResultDraft(agentcore.AgentResult{
		ID:        "result-natural",
		MissionID: "mission-1",
		AgentID:   "agent-1",
		RunID:     "run-natural",
		Summary:   agentcore.Summary{Text: "<think>private reasoning</think>\nThe customer prefers a concise weekly summary.\n```shell\ncurl http://internal\n```"},
		Decisions: []agentcore.Decision{{ID: "decision-1", Summary: "Send the summary every Friday"}},
	}, AgentResultCaptureOptions{})

	if draft.Summary != "The customer prefers a concise weekly summary." {
		t.Fatalf("sanitized summary = %q", draft.Summary)
	}
	if strings.Contains(draft.Narrative, "private reasoning") || strings.Contains(draft.Narrative, "curl") {
		t.Fatalf("narrative leaked trace/code: %q", draft.Narrative)
	}
}

type recordingCommitter struct {
	calls int
}

func (c *recordingCommitter) SubmitAgentResult(context.Context, agentcore.AgentResult, agentcore.CommitOptions) error {
	c.calls++
	return nil
}

func contains(value string, needle string) bool {
	return strings.Contains(value, needle)
}
