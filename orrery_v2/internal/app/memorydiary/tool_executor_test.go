package memorydiary

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
	memtestkit "orrery_v2/internal/memory/testkit"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestToolExecutorWritesDailyDiaryForAgentFromRunCandidates(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	seedMemoryProfile(t, ctx, store, "agent-1")
	seedRunDiaryCandidate(t, ctx, store, "agent-1", "run-1", "2026-06-16", "Implemented search.", time.Date(2026, 6, 16, 3, 0, 0, 0, time.UTC))
	seedRunDiaryCandidate(t, ctx, store, "agent-1", "run-2", "2026-06-16", "Fixed review state.", time.Date(2026, 6, 16, 8, 0, 0, 0, time.UTC))
	executor := NewToolExecutor(ToolExecutorOptions{
		Repository: store,
		Enabled:    func(context.Context) (bool, error) { return true, nil },
		Now:        func() time.Time { return time.Date(2026, 6, 17, 2, 0, 0, 0, time.UTC) },
	})

	result, err := executor.ExecuteMemoryDiaryTool(ctx, string(toolstd.MemoryDiaryWrite), ToolRequest{
		PrincipalID:   "scheduler-agent",
		Mode:          ToolModeAgent,
		TargetAgentID: "agent-1",
		Date:          "2026-06-16",
	})
	if err != nil {
		t.Fatalf("ExecuteMemoryDiaryTool: %v", err)
	}
	if result.Output["written_count"] != 1 || result.Output["status"] != "completed" {
		t.Fatalf("output = %+v", result.Output)
	}
	records, err := store.FindRecords(ctx, memstore.RecordQuery{
		ScopeKinds: []memcore.ScopeKind{memcore.ScopeAgent},
		ScopeIDs:   []string{"agent-1"},
		Topics:     []string{"memory/2026-06-16.md"},
		Tags:       []string{DiaryTagSummary},
	})
	if err != nil {
		t.Fatalf("FindRecords: %v", err)
	}
	if len(records) != 1 ||
		records[0].ID != "memory.record.agent_daily_diary.agent-1.2026-06-16" ||
		records[0].CandidateID != "candidate.agent_daily_diary.agent-1.2026-06-16" ||
		!strings.Contains(records[0].Content, "# memory/2026-06-16.md") ||
		!strings.Contains(records[0].Content, "Implemented search.") ||
		!strings.Contains(records[0].Content, "Fixed review state.") ||
		records[0].Visibility.Mode != memcore.VisibilityOwnerOnly ||
		len(records[0].LineageRefs) != 2 {
		t.Fatalf("records = %+v", records)
	}
}

func TestToolExecutorDeduplicatesRunCandidateAndAutoRecord(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	seedMemoryProfile(t, ctx, store, "agent-1")
	at := time.Date(2026, 6, 16, 3, 0, 0, 0, time.UTC)
	seedRunDiaryCandidate(t, ctx, store, "agent-1", "run-1", "2026-06-16", "Implemented search.", at)
	if err := store.SaveRecord(ctx, memcore.MemoryRecord{
		ID:                 "memory.record.candidate.agent_diary.agent-1.run-1",
		ProfileID:          "memory-agent-1",
		CandidateID:        "candidate.agent_diary.agent-1.run-1",
		Scope:              memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "agent-1"},
		Layer:              memcore.MemoryLayerFact,
		Topic:              "memory/2026-06-16.md",
		SourceEvidenceRefs: []string{"evidence:run-1"},
		Content:            "Diary: memory/2026-06-16.md\nSummary: Implemented search.",
		Summary:            "Implemented search.",
		Tags:               []string{DiaryTagAgent, DiaryTagDailyNote, DiaryTagRunSource, "diary_date:2026-06-16", "diary_path:memory/2026-06-16.md"},
		Visibility:         memcore.Visibility{Mode: memcore.VisibilityOwnerOnly, ScopeKind: memcore.ScopeAgent, ScopeID: "agent-1"},
		CreatedBy:          agentcore.AgentID("agent-1"),
		CreatedAt:          at,
		UpdatedAt:          at,
	}); err != nil {
		t.Fatalf("SaveRecord: %v", err)
	}
	executor := NewToolExecutor(ToolExecutorOptions{
		Repository: store,
		Enabled:    func(context.Context) (bool, error) { return true, nil },
		Now:        func() time.Time { return time.Date(2026, 6, 17, 2, 0, 0, 0, time.UTC) },
	})

	result, err := executor.ExecuteMemoryDiaryTool(ctx, string(toolstd.MemoryDiaryWrite), ToolRequest{
		PrincipalID:   "scheduler-agent",
		Mode:          ToolModeAgent,
		TargetAgentID: "agent-1",
		Date:          "2026-06-16",
	})
	if err != nil {
		t.Fatalf("ExecuteMemoryDiaryTool: %v", err)
	}
	diaries := result.Output["diaries"].([]map[string]any)
	if len(diaries) != 1 || diaries[0]["source_count"] != 1 {
		t.Fatalf("diaries = %+v", diaries)
	}
	records, err := store.FindRecords(ctx, memstore.RecordQuery{
		Tags: []string{DiaryTagSummary},
	})
	if err != nil {
		t.Fatalf("FindRecords: %v", err)
	}
	if len(records) != 1 || len(records[0].LineageRefs) != 1 {
		t.Fatalf("records = %+v", records)
	}
}

func TestToolExecutorWritesAllAgentsWithDiaryMaterial(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	seedMemoryProfile(t, ctx, store, "agent-1")
	seedMemoryProfile(t, ctx, store, "agent-2")
	seedRunDiaryCandidate(t, ctx, store, "agent-2", "run-1", "2026-06-16", "Closed task.", time.Date(2026, 6, 16, 9, 0, 0, 0, time.UTC))
	executor := NewToolExecutor(ToolExecutorOptions{
		Repository: store,
		Enabled:    func(context.Context) (bool, error) { return true, nil },
		Now:        func() time.Time { return time.Date(2026, 6, 17, 2, 0, 0, 0, time.UTC) },
	})

	result, err := executor.ExecuteMemoryDiaryTool(ctx, string(toolstd.MemoryDiaryWrite), ToolRequest{Mode: ToolModeAllAgents, Date: "2026-06-16"})
	if err != nil {
		t.Fatalf("ExecuteMemoryDiaryTool: %v", err)
	}
	if result.Output["written_count"] != 1 || result.Output["skipped_count"] != 1 {
		t.Fatalf("output = %+v", result.Output)
	}
	records, err := store.FindRecords(ctx, memstore.RecordQuery{Tags: []string{DiaryTagSummary}})
	if err != nil {
		t.Fatalf("FindRecords: %v", err)
	}
	if len(records) != 1 || records[0].Scope.ID != "agent-2" {
		t.Fatalf("records = %+v", records)
	}
}

func TestToolExecutorIsIdempotentUnlessForced(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	seedMemoryProfile(t, ctx, store, "agent-1")
	seedRunDiaryCandidate(t, ctx, store, "agent-1", "run-1", "2026-06-16", "First run.", time.Date(2026, 6, 16, 3, 0, 0, 0, time.UTC))
	executor := NewToolExecutor(ToolExecutorOptions{
		Repository: store,
		Enabled:    func(context.Context) (bool, error) { return true, nil },
		Now:        func() time.Time { return time.Date(2026, 6, 17, 2, 0, 0, 0, time.UTC) },
	})

	if _, err := executor.ExecuteMemoryDiaryTool(ctx, string(toolstd.MemoryDiaryWrite), ToolRequest{Mode: ToolModeAgent, TargetAgentID: "agent-1", Date: "2026-06-16"}); err != nil {
		t.Fatalf("first ExecuteMemoryDiaryTool: %v", err)
	}
	result, err := executor.ExecuteMemoryDiaryTool(ctx, string(toolstd.MemoryDiaryWrite), ToolRequest{Mode: ToolModeAgent, TargetAgentID: "agent-1", Date: "2026-06-16"})
	if err != nil {
		t.Fatalf("second ExecuteMemoryDiaryTool: %v", err)
	}
	if result.Output["written_count"] != 0 || result.Output["status"] != "completed" {
		t.Fatalf("output = %+v", result.Output)
	}
	diaries := result.Output["diaries"].([]map[string]any)
	if len(diaries) != 1 || diaries[0]["status"] != "exists" {
		t.Fatalf("diaries = %+v", diaries)
	}
}

func TestToolExecutorReturnsDisabledWhenMemorySettingOff(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	seedMemoryProfile(t, ctx, store, "agent-1")
	executor := NewToolExecutor(ToolExecutorOptions{
		Repository: store,
		Enabled:    func(context.Context) (bool, error) { return false, nil },
		Now:        func() time.Time { return time.Date(2026, 6, 17, 2, 0, 0, 0, time.UTC) },
	})

	result, err := executor.ExecuteMemoryDiaryTool(ctx, string(toolstd.MemoryDiaryWrite), ToolRequest{Mode: ToolModeAgent, TargetAgentID: "agent-1", Date: "2026-06-16"})
	if err != nil {
		t.Fatalf("ExecuteMemoryDiaryTool: %v", err)
	}
	if result.Output["status"] != "disabled" || result.Output["written_count"] != 0 {
		t.Fatalf("output = %+v", result.Output)
	}
	records, err := store.FindRecords(ctx, memstore.RecordQuery{Tags: []string{DiaryTagSummary}})
	if err != nil {
		t.Fatalf("FindRecords: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("records = %+v", records)
	}
}

func TestToolExecutorRejectsInvalidDate(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	executor := NewToolExecutor(ToolExecutorOptions{Repository: store})

	_, err := executor.ExecuteMemoryDiaryTool(ctx, string(toolstd.MemoryDiaryWrite), ToolRequest{Mode: ToolModeAllAgents, Date: "2026/06/16"})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err = %v, want ErrInvalidRequest", err)
	}
}

func seedMemoryProfile(t *testing.T, ctx context.Context, store *memtestkit.FakeStore, agentID string) {
	t.Helper()
	if err := store.SaveProfile(ctx, memcore.MemoryProfile{
		ID:         agentcore.MemoryProfileID("memory-" + agentID),
		OwnerRef:   memcore.OwnerRef{AgentID: agentcore.AgentID(agentID)},
		MemoryKind: agentcore.MemoryKindPersonal,
		DefaultScope: memcore.MemoryScope{
			Kind: memcore.ScopeAgent,
			ID:   agentID,
		},
		VisibilityPolicy: memcore.VisibilityPolicy{Mode: memcore.VisibilityOwnerOnly},
		Enabled:          true,
	}); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
}

func seedRunDiaryCandidate(t *testing.T, ctx context.Context, store *memtestkit.FakeStore, agentID, runID, date, summary string, at time.Time) {
	t.Helper()
	if err := store.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            "candidate.agent_diary." + agentID + "." + runID,
		SourceType:    memcore.SourceAgentRunDiary,
		SourceRef:     runID,
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: agentID},
		ProposedKind:  agentcore.MemoryKindPersonal,
		ProposedLayer: memcore.MemoryLayerFact,
		Topic:         "memory/" + date + ".md",
		Summary:       summary,
		Narrative:     "Diary: memory/" + date + ".md\nSummary: " + summary,
		Tags:          []string{DiaryTagAgent, DiaryTagDailyNote, DiaryTagRunSource, "diary_date:" + date, "diary_path:memory/" + date + ".md"},
		EvidenceRefs:  []string{"evidence:" + runID},
		CreatedBy:     agentcore.AgentID(agentID),
		Status:        memcore.CandidateAccepted,
		CreatedAt:     at,
		UpdatedAt:     at,
	}); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
}
