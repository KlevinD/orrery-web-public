package memorydiary

import (
	"context"
	"encoding/json"
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

func TestFlushThresholdUsesOpenClawDefaults(t *testing.T) {
	config := DefaultFlushConfig()
	if config.SoftThresholdTokens != 4000 || config.ForceTranscriptBytes != 2*1024*1024 || config.MaxAttempts != 3 {
		t.Fatalf("defaults = %+v", config)
	}
	if ShouldPreCompaction(FlushPressure{ContextWindowTokens: 100_000, ReserveTokens: 20_000, ProjectedTokens: 75_999}, config) {
		t.Fatalf("flush triggered below soft threshold")
	}
	if !ShouldPreCompaction(FlushPressure{ContextWindowTokens: 100_000, ReserveTokens: 20_000, ProjectedTokens: 76_000}, config) {
		t.Fatalf("flush did not trigger at soft threshold")
	}
	if !ShouldPreCompaction(FlushPressure{ContextWindowTokens: 100_000, ReserveTokens: 20_000, ProjectedTokens: 1, TranscriptBytes: 2 * 1024 * 1024}, config) {
		t.Fatalf("2 MiB transcript guard did not force flush")
	}
}

func TestCoordinatorPreparesOpenClawMaintenanceMetadata(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	coordinator := NewCoordinator(CoordinatorOptions{
		State:    NewMemoryFlushStateStore(),
		Location: location,
		Now:      func() time.Time { return time.Date(2026, 7, 17, 16, 30, 0, 0, time.UTC) },
	})
	req := coordinator.PrepareRuntimeRequest(context.Background(), agentcore.RuntimeRequest{
		TenantID: "tenant-a", AgentID: "agent-1", RunID: "run-1", MissionID: "mission-1", TaskID: "task-1",
		ContextPack: agentcore.ContextPack{
			AgentOrganizationIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "org-agent-1"}},
			PersonalMemoryDigestRef:   agentcore.MemoryDigestRef{ID: "digest-1", Sensitivity: "secret"},
			SourceRefs: []agentcore.ContextSourceRef{{
				Kind: agentcore.ContextSourceKindMemoryDigest,
				Ref:  agentcore.ContextSourceTypedRef{MemoryDigestRef: &agentcore.MemoryDigestRef{ID: "digest-1"}},
			}},
		},
		RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{"keep": "value", "diary_tenant_id": "untrusted-tenant"}},
	})
	if req.RuntimeOptions.ProviderSpecific["diary_target_path"] != "memory/2026-07-18.md" ||
		req.RuntimeOptions.ProviderSpecific["diary_target_date"] != "2026-07-18" ||
		req.RuntimeOptions.ProviderSpecific["diary_soft_threshold_tokens"] != "4000" ||
		req.RuntimeOptions.ProviderSpecific["diary_force_transcript_bytes"] != "2097152" ||
		req.RuntimeOptions.ProviderSpecific["diary_tenant_id"] != "org-agent-1" ||
		req.RuntimeOptions.ProviderSpecific["diary_source_sensitivity"] != "secret" ||
		!strings.Contains(req.RuntimeOptions.ProviderSpecific["diary_lineage_refs"], "memory_digest:digest-1") ||
		!strings.Contains(req.RuntimeOptions.ProviderSpecific["diary_lineage_refs"], "run:run-1") ||
		req.RuntimeOptions.ProviderSpecific["keep"] != "value" {
		t.Fatalf("prepared metadata = %+v", req.RuntimeOptions.ProviderSpecific)
	}
}

func TestCoordinatorPreparesDiaryMetadataFromPersonalMemoryOwnership(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	profile := memcore.MemoryProfile{
		ID:         "memory-profile-agent-1",
		OwnerRef:   memcore.OwnerRef{AgentID: "agent-1", OrganizationID: "org-memory-owner"},
		MemoryKind: agentcore.MemoryKindPersonal,
		Enabled:    true,
	}
	if err := store.SaveProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	service := NewDiaryService(DiaryServiceOptions{Repository: store})
	coordinator := NewCoordinator(CoordinatorOptions{State: NewMemoryFlushStateStore(), TerminalAppender: service})

	req := coordinator.PrepareRuntimeRequest(ctx, agentcore.RuntimeRequest{
		TenantID: "system",
		AgentID:  "agent-1",
		ContextPack: agentcore.ContextPack{
			AgentOrganizationIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "system"}},
		},
	})
	if tenantID := req.RuntimeOptions.ProviderSpecific["diary_tenant_id"]; tenantID != "org-memory-owner" {
		t.Fatalf("diary tenant id = %q, want org-memory-owner", tenantID)
	}
}

func TestCoordinatorDisablesGenericDiaryForDelegatedLogicalMemoryOwner(t *testing.T) {
	coordinator := NewCoordinator(CoordinatorOptions{State: NewMemoryFlushStateStore()})
	req := agentcore.RuntimeRequest{
		TenantID: "tenant-a",
		AgentID:  "lead-agent",
		ContextPack: agentcore.ContextPack{
			MemoryOwnerAgentID: "personal-assistant-owner",
		},
	}

	prepared := coordinator.PrepareRuntimeRequest(context.Background(), req)
	if prepared.RuntimeOptions.ProviderSpecific["diary_flush_enabled"] != "false" {
		t.Fatalf("prepared metadata = %+v", prepared.RuntimeOptions.ProviderSpecific)
	}
	outcome := coordinator.FlushTerminal(context.Background(), agentcore.DiaryFlushRequest{
		Request: prepared,
		Response: agentcore.RuntimeResponse{
			Status:           agentcore.RuntimeStatusCompleted,
			ProviderMetadata: map[string]string{"session_id": "session-1"},
		},
		Capabilities: canonicalDiaryCapabilities(),
	})
	if outcome.Status != agentcore.DiaryFlushSkipped || outcome.Reason != "dedicated_logical_memory_capture" {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestCoordinatorDisablesGenericDiaryForDedicatedCaptureWithSameRuntimeOwner(t *testing.T) {
	coordinator := NewCoordinator(CoordinatorOptions{State: NewMemoryFlushStateStore()})
	req := agentcore.RuntimeRequest{
		TenantID: "tenant-a",
		AgentID:  "personal-assistant-owner",
		ContextPack: agentcore.ContextPack{
			MemoryOwnerAgentID: "personal-assistant-owner",
		},
		RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{
			agentcore.DedicatedLogicalMemoryCaptureProviderKey: "true",
		}},
	}

	prepared := coordinator.PrepareRuntimeRequest(context.Background(), req)
	if prepared.RuntimeOptions.ProviderSpecific["diary_flush_enabled"] != "false" {
		t.Fatalf("prepared metadata = %+v", prepared.RuntimeOptions.ProviderSpecific)
	}
	outcome := coordinator.FlushTerminal(context.Background(), agentcore.DiaryFlushRequest{
		Request: prepared,
		Response: agentcore.RuntimeResponse{
			Status:           agentcore.RuntimeStatusCompleted,
			ProviderMetadata: map[string]string{"session_id": "session-1"},
		},
		Capabilities: canonicalDiaryCapabilities(),
	})
	if outcome.Status != agentcore.DiaryFlushSkipped || outcome.Reason != "dedicated_logical_memory_capture" {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestCoordinatorUsesAgentTimezoneAtFlushTime(t *testing.T) {
	coordinator := NewCoordinator(CoordinatorOptions{
		State: NewMemoryFlushStateStore(), Location: time.UTC,
		Now: func() time.Time { return time.Date(2026, 7, 18, 2, 0, 0, 0, time.UTC) },
	})
	req := coordinator.PrepareRuntimeRequest(context.Background(), agentcore.RuntimeRequest{
		TenantID: "tenant-a", AgentID: "agent-1",
		RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{"diary_timezone": "America/New_York"}},
	})
	if req.RuntimeOptions.ProviderSpecific["diary_target_date"] != "2026-07-17" || req.RuntimeOptions.ProviderSpecific["diary_target_path"] != "memory/2026-07-17.md" {
		t.Fatalf("target metadata = %+v", req.RuntimeOptions.ProviderSpecific)
	}
}

func TestCoordinatorTerminalFlushUsesSameSessionAndRestrictedTools(t *testing.T) {
	state := NewMemoryFlushStateStore()
	runner := &sequenceDiaryRunner{responses: []agentcore.RuntimeResponse{{
		Status: agentcore.RuntimeStatusCompleted,
		Messages: []agentcore.RuntimeMessage{
			{Type: agentcore.RuntimeMessageToolUse, Tool: string(toolstd.MemoryDiaryAppend), CallID: "append-1"},
			{Type: agentcore.RuntimeMessageToolResult, Tool: string(toolstd.MemoryDiaryAppend), CallID: "append-1", Status: "completed", Output: `{"status":"written","memory_id":"memory-1"}`},
		},
	}}}
	coordinator := NewCoordinator(CoordinatorOptions{
		State: state,
		Now:   func() time.Time { return time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC) },
	})
	mainReq := agentcore.RuntimeRequest{
		TenantID: "tenant-a", AgentID: "agent-1", RunID: "run-1", MissionID: "mission-1", TaskID: "task-1",
		RuntimeOptions: agentcore.RuntimeOptions{
			Model: "same-model", MCPConfig: []byte(`{"servers":{"unsafe":{"url":"http://127.0.0.1:9999"}}}`),
			Environment:      map[string]string{"ORRERY_PI_SKILL_PATHS": "D:/skills/unsafe", "ORRERY_MCP_URL": "http://127.0.0.1:9999"},
			ProviderSpecific: map[string]string{},
		},
	}
	mainResp := agentcore.RuntimeResponse{
		Status:           agentcore.RuntimeStatusCompleted,
		Messages:         []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Content: "Shipped the feature.", Status: agentcore.RuntimeMessageStatusFinal}},
		ProviderMetadata: map[string]string{"session_id": "session-1"},
	}
	outcome := coordinator.FlushTerminal(context.Background(), agentcore.DiaryFlushRequest{
		Runtime: runner, Request: mainReq, Response: mainResp,
		Capabilities: canonicalDiaryCapabilities(),
	})
	if outcome.Status != agentcore.DiaryFlushWritten || outcome.RecordID != "memory-1" || len(runner.requests) != 1 {
		t.Fatalf("outcome = %+v requests=%d", outcome, len(runner.requests))
	}
	req := runner.requests[0]
	if req.AgentID != "agent-1" || req.TenantID != "tenant-a" || req.RuntimeOptions.Model != "same-model" || req.RuntimeOptions.ResumeSessionID != "session-1" {
		t.Fatalf("maintenance identity/session = %+v", req)
	}
	if len(req.ToolBindings) != 2 || req.ToolBindings[0].ToolID != toolstd.MemoryDiaryRead || req.ToolBindings[1].ToolID != toolstd.MemoryDiaryAppend {
		t.Fatalf("maintenance tools = %+v", req.ToolBindings)
	}
	if len(req.SkillBindings) != 0 || !strings.Contains(req.RuntimeOptions.SystemPrompt, "Agent diary memory flush turn") ||
		!strings.Contains(req.RuntimeOptions.SystemPrompt, "NO_REPLY") || !strings.Contains(req.InputMessages[0].Content, "new durable material") ||
		!strings.Contains(req.InputMessages[0].Content, "Shipped the feature.") {
		t.Fatalf("maintenance prompt/request = %+v", req)
	}
	if req.RuntimeOptions.ProviderSpecific["diary_maintenance"] != "true" || req.RuntimeOptions.ProviderSpecific["diary_trigger"] != string(TriggerRunTerminal) {
		t.Fatalf("maintenance metadata = %+v", req.RuntimeOptions.ProviderSpecific)
	}
	if req.RuntimeOptions.ProviderSpecific["diary_flush_enabled"] != "false" {
		t.Fatalf("maintenance turn must not recursively install a pre-compaction flush: %+v", req.RuntimeOptions.ProviderSpecific)
	}
	if len(req.RuntimeOptions.MCPConfig) != 0 || req.RuntimeOptions.Environment["ORRERY_PI_SKILL_PATHS"] != "" || req.RuntimeOptions.Environment["ORRERY_MCP_URL"] != "" {
		t.Fatalf("maintenance inherited unsafe runtime resources: %+v", req.RuntimeOptions)
	}

	second := coordinator.FlushTerminal(context.Background(), agentcore.DiaryFlushRequest{
		Runtime: runner, Request: mainReq, Response: mainResp,
		Capabilities: canonicalDiaryCapabilities(),
	})
	if second.Status != agentcore.DiaryFlushSkipped || len(runner.requests) != 1 {
		t.Fatalf("same revision was not deduplicated: outcome=%+v requests=%d", second, len(runner.requests))
	}
}

func TestCoordinatorTerminalFlushUsesFreshPiSessionWithOriginalDiaryIdentity(t *testing.T) {
	runner := &sequenceDiaryRunner{responses: []agentcore.RuntimeResponse{{
		Status: agentcore.RuntimeStatusCompleted,
		Messages: []agentcore.RuntimeMessage{
			{Type: agentcore.RuntimeMessageToolUse, Tool: string(toolstd.MemoryDiaryAppend), CallID: "append-pi"},
			{Type: agentcore.RuntimeMessageToolResult, Tool: string(toolstd.MemoryDiaryAppend), CallID: "append-pi", Status: "completed", Output: `{"status":"written","memory_id":"memory-pi"}`},
		},
	}}}
	coordinator := NewCoordinator(CoordinatorOptions{State: NewMemoryFlushStateStore()})
	request := terminalFlushRequest(runner, "durable PI result")
	request.Request.RuntimeOptions.ProviderSpecific = map[string]string{"runtime_kind": string(agentcore.RuntimeKindPi)}

	outcome := coordinator.FlushTerminal(context.Background(), request)
	if outcome.Status != agentcore.DiaryFlushWritten || outcome.RecordID != "memory-pi" || len(runner.requests) != 1 {
		t.Fatalf("outcome = %+v requests=%d", outcome, len(runner.requests))
	}
	maintenance := runner.requests[0]
	if maintenance.RuntimeOptions.ResumeSessionID != "" {
		t.Fatalf("PI maintenance resumed closed session %q", maintenance.RuntimeOptions.ResumeSessionID)
	}
	if maintenance.RuntimeOptions.ProviderSpecific["diary_session_id"] != "session-1" ||
		maintenance.RuntimeOptions.ProviderSpecific["diary_context_revision"] == "" ||
		!strings.Contains(maintenance.InputMessages[0].Content, "durable PI result") {
		t.Fatalf("PI maintenance lost diary identity/context: %+v", maintenance)
	}
}

func TestCoordinatorCapsMaintenanceRequestToFlushDeadline(t *testing.T) {
	runner := &sequenceDiaryRunner{responses: []agentcore.RuntimeResponse{{
		Status:   agentcore.RuntimeStatusCompleted,
		Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Content: "NO_REPLY", Status: agentcore.RuntimeMessageStatusFinal}},
	}}}
	req := terminalFlushRequest(runner, "result-1")
	req.Request.RuntimeOptions.Timeout = 20 * time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	outcome := NewCoordinator(CoordinatorOptions{State: NewMemoryFlushStateStore()}).FlushTerminal(ctx, req)
	if outcome.Status != agentcore.DiaryFlushNoContent || len(runner.requests) != 1 {
		t.Fatalf("outcome=%+v requests=%d", outcome, len(runner.requests))
	}
	maintenance := runner.requests[0]
	if maintenance.RuntimeOptions.Timeout <= 0 || maintenance.RuntimeOptions.Timeout > time.Second {
		t.Fatalf("maintenance timeout = %s, want flush deadline cap", maintenance.RuntimeOptions.Timeout)
	}
	if maintenance.Deadline.IsZero() {
		t.Fatal("maintenance deadline was not propagated from flush context")
	}
}

func TestTerminalContextSnapshotExcludesThinkingAndPreservesRecentOutcome(t *testing.T) {
	messages := []agentcore.RuntimeMessage{
		{Type: agentcore.RuntimeMessageThinking, Content: "private reasoning type"},
	}
	for index := 0; index < 8; index++ {
		messages = append(messages, agentcore.RuntimeMessage{Type: agentcore.RuntimeMessageText, Content: strings.Repeat("x", maxTerminalEntryValueBytes*2)})
	}
	messages = append(messages, agentcore.RuntimeMessage{
		Type: agentcore.RuntimeMessageToolResult, Tool: "bash", Status: "failed",
		Output: `{"command":"find /workspace","error":"runtime deadline exceeded"}`,
	})
	messages = append(messages, agentcore.RuntimeMessage{
		Type:    agentcore.RuntimeMessageText,
		Content: "<think>private inline reasoning</think>Durable final outcome with artifact-123.",
		Status:  agentcore.RuntimeMessageStatusFinal,
	})
	snapshot := terminalContextSnapshot(agentcore.RuntimeResponse{Messages: messages})
	if strings.Contains(snapshot, "private reasoning") || strings.Contains(snapshot, "private inline reasoning") {
		t.Fatalf("terminal context leaked thinking: %s", snapshot)
	}
	if !strings.Contains(snapshot, "Durable final outcome with artifact-123.") {
		t.Fatalf("terminal context lost recent final outcome: %s", snapshot)
	}
	if strings.Contains(snapshot, "runtime deadline exceeded") || strings.Contains(snapshot, "find /workspace") || strings.Contains(snapshot, strings.Repeat("x", 256)) {
		t.Fatalf("terminal context retained execution trace: %s", snapshot)
	}
	var decoded []map[string]any
	if err := json.Unmarshal([]byte(snapshot), &decoded); err != nil {
		t.Fatalf("terminal context is not valid JSON: %v\n%s", err, snapshot)
	}
}

func TestCoordinatorPersistsFailureWhenMaintenanceBudgetExpires(t *testing.T) {
	state := NewMemoryFlushStateStore()
	runner := contextDeadlineDiaryRunner{}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	outcome := NewCoordinator(CoordinatorOptions{State: state}).FlushTerminal(ctx, terminalFlushRequest(runner, "result-1"))
	if outcome.Status != agentcore.DiaryFlushFailed || outcome.Reason != "flush_deadline_exceeded" || outcome.Attempts != 1 {
		t.Fatalf("outcome = %+v", outcome)
	}
	persisted, ok, err := state.LoadFlushState(context.Background(), "tenant-a", "agent-1", "session-1")
	if err != nil || !ok || persisted.AttemptCount != 1 || !strings.Contains(persisted.LastFailure, context.DeadlineExceeded.Error()) {
		t.Fatalf("persisted state = %+v, %t, %v", persisted, ok, err)
	}
}

func TestCoordinatorTreatsNoReplyAsNoContent(t *testing.T) {
	appender := &recordingTerminalAppender{}
	runner := &sequenceDiaryRunner{responses: []agentcore.RuntimeResponse{{
		Status:   agentcore.RuntimeStatusCompleted,
		Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Content: "<think>private</think>NO_REPLY", Status: agentcore.RuntimeMessageStatusFinal}},
	}}}
	coordinator := NewCoordinator(CoordinatorOptions{State: NewMemoryFlushStateStore(), TerminalAppender: appender})
	req := terminalFlushRequest(runner, "result-1")
	req.Request.RuntimeOptions.ProviderSpecific = map[string]string{"runtime_kind": string(agentcore.RuntimeKindPi)}
	outcome := coordinator.FlushTerminal(context.Background(), req)
	if outcome.Status != agentcore.DiaryFlushNoContent || len(runner.requests) != 1 || len(appender.requests) != 0 {
		t.Fatalf("outcome = %+v requests=%d appends=%d", outcome, len(runner.requests), len(appender.requests))
	}
}

func TestCoordinatorPersistsScheduledCompletionWhenMaintenanceReturnsNoReply(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	repository := memtestkit.NewFakeStore()
	profile := memcore.MemoryProfile{
		ID:         "memory-profile-agent-1",
		OwnerRef:   memcore.OwnerRef{AgentID: "agent-1", OrganizationID: "tenant-a"},
		MemoryKind: agentcore.MemoryKindPersonal,
		Enabled:    true,
	}
	if err := repository.SaveProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	state := NewMemoryFlushStateStore()
	service := NewDiaryService(DiaryServiceOptions{Repository: repository, State: state, Now: func() time.Time { return now }})
	runner := &sequenceDiaryRunner{responses: []agentcore.RuntimeResponse{{
		Status:   agentcore.RuntimeStatusCompleted,
		Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Content: "NO_REPLY", Status: agentcore.RuntimeMessageStatusFinal}},
	}}}
	coordinator := NewCoordinator(CoordinatorOptions{
		State: state, TerminalAppender: service, Location: time.UTC, Now: func() time.Time { return now },
	})
	req := terminalFlushRequest(runner, "preparing scheduled report")
	req.Request.TenantID = "system"
	req.Request.ContextPack.AgentOrganizationIdentity = agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "system"}}
	req.Request.MissionID = "mission-1"
	req.Request.TaskID = "task-1"
	req.Request.RuntimeOptions.ProviderSpecific = map[string]string{
		"runtime_kind":        string(agentcore.RuntimeKindPi),
		"run_source":          "scheduler_agent_task",
		"scheduled_task_id":   "scheduled-1",
		"scheduled_task_name": "Daily work diary",
	}
	req.Response.Messages = append(req.Response.Messages, agentcore.RuntimeMessage{
		Type: agentcore.RuntimeMessageToolResult, Tool: "Shell", Status: "completed", Output: `{"exit_code":0,"stdout":"report written"}`,
	})
	req.Response.Messages = append(req.Response.Messages, agentcore.RuntimeMessage{
		Type: agentcore.RuntimeMessageText, Status: agentcore.RuntimeMessageStatusFinal,
		Content: "The daily customer report is complete and the support team can use the updated recommendations.",
	})

	outcome := coordinator.FlushTerminal(ctx, req)
	if outcome.Status != agentcore.DiaryFlushWritten || outcome.RecordID == "" || outcome.Reason != "append_succeeded" {
		t.Fatalf("outcome = %+v", outcome)
	}
	if len(runner.requests) != 1 || runner.requests[0].TenantID != "tenant-a" || runner.requests[0].RuntimeOptions.ProviderSpecific["diary_tenant_id"] != "tenant-a" {
		t.Fatalf("maintenance diary tenant = %+v", runner.requests)
	}
	candidates, err := repository.ListCandidates(ctx, memstore.CandidateQuery{ScopeKind: memcore.ScopeAgent, ScopeID: "agent-1"})
	if err != nil || len(candidates) != 1 {
		t.Fatalf("candidates = %+v err=%v", candidates, err)
	}
	if candidates[0].SourceType != memcore.SourceAgentRunDiary || candidates[0].Status != memcore.CandidateAccepted || candidates[0].MissionID != "mission-1" {
		t.Fatalf("candidate = %+v", candidates[0])
	}
	records, err := repository.FindRecords(ctx, memstore.RecordQuery{ProfileIDs: []agentcore.MemoryProfileID{profile.ID}})
	if err != nil || len(records) != 1 {
		t.Fatalf("records = %+v err=%v", records, err)
	}
	if records[0].ID != outcome.RecordID || records[0].Topic != "memory/2026-07-18.md" ||
		!strings.Contains(records[0].Content, "daily customer report is complete") || strings.Contains(records[0].Content, "scheduled-1") ||
		strings.Contains(records[0].Content, "run-1") || strings.Contains(records[0].Content, "preparing scheduled report") {
		t.Fatalf("record = %+v", records[0])
	}
	flushState, found, err := state.LoadFlushState(ctx, "tenant-a", "agent-1", "scheduled-run:run-1")
	if err != nil || !found || flushState.LastSuccessfulRevision == "" || flushState.LastFailure != "" {
		t.Fatalf("flush state = %+v found=%t err=%v", flushState, found, err)
	}
}

func TestCoordinatorDeduplicatesScheduledOutcomeAcrossRuntimeRetriesWithDifferentMessages(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	repository := memtestkit.NewFakeStore()
	profile := memcore.MemoryProfile{
		ID:         "memory-profile-agent-1",
		OwnerRef:   memcore.OwnerRef{AgentID: "agent-1", OrganizationID: "tenant-a"},
		MemoryKind: agentcore.MemoryKindPersonal,
		Enabled:    true,
	}
	if err := repository.SaveProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	state := NewMemoryFlushStateStore()
	service := NewDiaryService(DiaryServiceOptions{Repository: repository, State: state, Now: func() time.Time { return now }})
	runner := &sequenceDiaryRunner{responses: []agentcore.RuntimeResponse{{
		Status:   agentcore.RuntimeStatusCompleted,
		Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Content: "NO_REPLY", Status: agentcore.RuntimeMessageStatusFinal}},
	}}}
	coordinator := NewCoordinator(CoordinatorOptions{State: state, TerminalAppender: service, Location: time.UTC, Now: func() time.Time { return now }})
	req := terminalFlushRequest(runner, "MEMORY_SMOKE_OK")
	req.Request.RuntimeOptions.ProviderSpecific = map[string]string{
		"runtime_kind":        string(agentcore.RuntimeKindPi),
		"run_source":          "scheduler_agent_task",
		"scheduled_task_id":   "scheduled-1",
		"scheduled_run_id":    "scheduled-run-1",
		"scheduled_task_name": "Memory smoke",
	}
	req.Response.ProviderMetadata["session_id"] = "provider-session-1"

	first := coordinator.FlushTerminal(ctx, req)
	if first.Status != agentcore.DiaryFlushWritten {
		t.Fatalf("first outcome = %+v", first)
	}
	req.Request.RunID = "responsibility-run-2"
	req.Response.RunID = "responsibility-run-2"
	req.Response.ProviderMetadata["session_id"] = "provider-session-2"
	req.Response.Messages = append(req.Response.Messages, agentcore.RuntimeMessage{
		Type: agentcore.RuntimeMessageText, Content: "retry-specific runtime message", Status: agentcore.RuntimeMessageStatusFinal,
	})
	second := coordinator.FlushTerminal(ctx, req)
	if second.Status != agentcore.DiaryFlushSkipped || second.Reason != "context_revision_already_flushed" {
		t.Fatalf("second outcome = %+v", second)
	}
	if first.ContextRevision != second.ContextRevision {
		t.Fatalf("context revisions differ: first=%q second=%q", first.ContextRevision, second.ContextRevision)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("maintenance requests = %d, want 1", len(runner.requests))
	}
	candidates, err := repository.ListCandidates(ctx, memstore.CandidateQuery{ScopeKind: memcore.ScopeAgent, ScopeID: "agent-1"})
	if err != nil || len(candidates) != 1 {
		t.Fatalf("candidates = %+v err=%v", candidates, err)
	}
}

func TestCoordinatorKeepsScheduledTimeoutInTraceOnly(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	repository := memtestkit.NewFakeStore()
	profile := memcore.MemoryProfile{
		ID:         "memory-profile-agent-1",
		OwnerRef:   memcore.OwnerRef{AgentID: "agent-1", OrganizationID: "tenant-a"},
		MemoryKind: agentcore.MemoryKindPersonal,
		Enabled:    true,
	}
	if err := repository.SaveProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	state := NewMemoryFlushStateStore()
	service := NewDiaryService(DiaryServiceOptions{Repository: repository, State: state, Now: func() time.Time { return now }})
	coordinator := NewCoordinator(CoordinatorOptions{
		State: state, TerminalAppender: service, Location: time.UTC, Now: func() time.Time { return now },
	})
	req := terminalFlushRequest(nil, "")
	req.Capabilities = agentcore.Capabilities{}
	req.Request.TenantID = "system"
	req.Request.ContextPack.AgentOrganizationIdentity = agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "system"}}
	req.Request.MissionID = "mission-1"
	req.Request.TaskID = "task-1"
	req.Request.RuntimeOptions.ProviderSpecific = map[string]string{
		"runtime_kind":        string(agentcore.RuntimeKindPi),
		"run_source":          "scheduler_agent_task",
		"scheduled_task_id":   "scheduled-1",
		"scheduled_run_id":    "scheduled-run-1",
		"scheduled_task_name": "Weekly FAQ maintenance",
	}
	req.Response = agentcore.RuntimeResponse{
		RunID: "run-1", Status: agentcore.RuntimeStatusTimeout,
		Error: agentcore.RuntimeFailure{Message: "runtime deadline exceeded"},
	}

	outcome := coordinator.FlushTerminal(ctx, req)
	if outcome.Status != agentcore.DiaryFlushSkipped || outcome.RecordID != "" || outcome.Reason != "terminal_trace_not_memory" {
		t.Fatalf("outcome = %+v", outcome)
	}
	candidates, err := repository.ListCandidates(ctx, memstore.CandidateQuery{ScopeKind: memcore.ScopeAgent, ScopeID: "agent-1"})
	if err != nil || len(candidates) != 0 {
		t.Fatalf("candidates = %+v err=%v", candidates, err)
	}
	records, err := repository.FindRecords(ctx, memstore.RecordQuery{ProfileIDs: []agentcore.MemoryProfileID{profile.ID}})
	if err != nil || len(records) != 0 {
		t.Fatalf("records = %+v err=%v", records, err)
	}
	if _, found, err := state.LoadFlushState(ctx, "tenant-a", "agent-1", "scheduled-run:scheduled-run-1"); err != nil || found {
		t.Fatalf("trace-only timeout created flush state: found=%t err=%v", found, err)
	}
}

func TestCoordinatorHostAppendsCompletedPiMaintenanceDraft(t *testing.T) {
	appender := &recordingTerminalAppender{receipt: AppendReceipt{RecordID: "memory-host-1"}}
	runner := &sequenceDiaryRunner{responses: []agentcore.RuntimeResponse{{
		Status: agentcore.RuntimeStatusCompleted,
		Messages: []agentcore.RuntimeMessage{{
			Type: agentcore.RuntimeMessageText, Status: agentcore.RuntimeMessageStatusFinal,
			Content: "<think>private maintenance reasoning</think>## Durable outcome\n\n- Shipped artifact-123.",
		}},
	}}}
	coordinator := NewCoordinator(CoordinatorOptions{
		State: NewMemoryFlushStateStore(), TerminalAppender: appender,
		Now: func() time.Time { return time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC) },
	})
	req := terminalFlushRequest(runner, "terminal result")
	req.Request.MissionID = "mission-1"
	req.Request.TaskID = "task-1"
	req.Request.RuntimeOptions.ProviderSpecific = map[string]string{
		"runtime_kind":             string(agentcore.RuntimeKindPi),
		"diary_source_sensitivity": "personal",
		"diary_evidence_refs":      `["evidence-1"]`,
		"diary_lineage_refs":       `["run:run-1"]`,
	}

	outcome := coordinator.FlushTerminal(context.Background(), req)
	if outcome.Status != agentcore.DiaryFlushWritten || outcome.RecordID != "memory-host-1" || outcome.Attempts != 1 {
		t.Fatalf("outcome = %+v", outcome)
	}
	if len(appender.requests) != 1 {
		t.Fatalf("host append requests = %d", len(appender.requests))
	}
	appendReq := appender.requests[0]
	if appendReq.TenantID != "tenant-a" || appendReq.AgentID != "agent-1" || appendReq.SessionID != "session-1" ||
		appendReq.Trigger != TriggerRunTerminal || appendReq.IdempotencyKey == "" || appendReq.Date != "2026-07-18" || appendReq.Path != "memory/2026-07-18.md" {
		t.Fatalf("host append identity = %+v", appendReq)
	}
	if strings.Contains(appendReq.Content, "private maintenance reasoning") || !strings.Contains(appendReq.Content, "Shipped artifact-123") {
		t.Fatalf("host append content = %q", appendReq.Content)
	}
	if len(appendReq.EvidenceRefs) != 1 || appendReq.EvidenceRefs[0] != "evidence-1" ||
		len(appendReq.LineageRefs) != 1 || appendReq.LineageRefs[0] != "run:run-1" || appendReq.SourceSensitivity != "personal" {
		t.Fatalf("host append provenance = %+v", appendReq)
	}
}

func TestParseMaintenanceAppendOutputRejectsFailureTextContainingSuccessWords(t *testing.T) {
	for _, raw := range []string{
		`{"status":"error","message":"diary was not written"}`,
		`{"status":"failed","message":"replayed request failed"}`,
		`diary was not written`,
	} {
		if recordID, ok := parseMaintenanceAppendOutput(raw); ok {
			t.Fatalf("parseMaintenanceAppendOutput(%q) = %q, true; want failure", raw, recordID)
		}
	}
}

func TestCoordinatorPersistsTerminalAttemptBeforeRuntimeCall(t *testing.T) {
	state := NewMemoryFlushStateStore()
	runner := &sequenceDiaryRunner{responses: []agentcore.RuntimeResponse{{
		Status:   agentcore.RuntimeStatusCompleted,
		Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Content: "NO_REPLY", Status: agentcore.RuntimeMessageStatusFinal}},
	}}}
	runner.inspect = func(_ agentcore.RuntimeRequest) {
		persisted, ok, err := state.LoadFlushState(context.Background(), "tenant-a", "agent-1", "session-1")
		if err != nil || !ok {
			t.Fatalf("LoadFlushState during runtime call = %+v, %t, %v", persisted, ok, err)
		}
		if persisted.AttemptCount != 1 || persisted.AttemptTrigger != TriggerRunTerminal {
			t.Fatalf("terminal attempt was not claimed before runtime call: %+v", persisted)
		}
	}
	outcome := NewCoordinator(CoordinatorOptions{State: state}).FlushTerminal(context.Background(), terminalFlushRequest(runner, "result-1"))
	if outcome.Status != agentcore.DiaryFlushNoContent {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestCoordinatorDoesNotRunMaintenanceOnGenericNativeToolRuntime(t *testing.T) {
	runner := &sequenceDiaryRunner{}
	req := terminalFlushRequest(runner, "result-1")
	req.Capabilities.Signals.ToolLifecycle = false
	outcome := NewCoordinator(CoordinatorOptions{State: NewMemoryFlushStateStore()}).FlushTerminal(context.Background(), req)
	if outcome.Status != agentcore.DiaryFlushSkipped || outcome.Reason != "runtime_unsupported" || len(runner.requests) != 0 {
		t.Fatalf("outcome = %+v requests=%d", outcome, len(runner.requests))
	}
}

func TestCoordinatorExhaustsThreeAttemptsAndRetriesForNewRevision(t *testing.T) {
	runner := &sequenceDiaryRunner{errors: []error{errors.New("one"), errors.New("two"), errors.New("three"), errors.New("four"), errors.New("five"), errors.New("six")}}
	coordinator := NewCoordinator(CoordinatorOptions{State: NewMemoryFlushStateStore()})
	first := coordinator.FlushTerminal(context.Background(), terminalFlushRequest(runner, "result-1"))
	if first.Status != agentcore.DiaryFlushExhausted || first.Attempts != 3 || len(runner.requests) != 3 {
		t.Fatalf("first outcome = %+v requests=%d", first, len(runner.requests))
	}
	again := coordinator.FlushTerminal(context.Background(), terminalFlushRequest(runner, "result-1"))
	if again.Status != agentcore.DiaryFlushSkipped || len(runner.requests) != 3 {
		t.Fatalf("exhausted revision retried: %+v requests=%d", again, len(runner.requests))
	}
	newRevision := coordinator.FlushTerminal(context.Background(), terminalFlushRequest(runner, "result-2"))
	if newRevision.Status != agentcore.DiaryFlushExhausted || len(runner.requests) != 6 {
		t.Fatalf("new revision did not receive attempts: %+v requests=%d", newRevision, len(runner.requests))
	}
}

func TestCoordinatorPersistsPreCompactionAttemptsAndNoContentCursor(t *testing.T) {
	state := NewMemoryFlushStateStore()
	coordinator := NewCoordinator(CoordinatorOptions{State: state, Now: func() time.Time {
		return time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	}})
	req := PreCompactionAttemptRequest{
		TenantID: "tenant-a", AgentID: "agent-1", SessionID: "session-1", ContextRevision: "revision-1",
		Pressure: FlushPressure{ContextWindowTokens: 100_000, ReserveTokens: 20_000, ProjectedTokens: 76_000},
	}
	first, err := coordinator.BeginPreCompaction(context.Background(), req)
	if err != nil || !first.Allowed || first.Attempt != 1 {
		t.Fatalf("BeginPreCompaction(first) = %+v, %v", first, err)
	}
	if err := coordinator.CompletePreCompaction(context.Background(), PreCompactionOutcomeRequest{
		TenantID: req.TenantID, AgentID: req.AgentID, SessionID: req.SessionID, ContextRevision: req.ContextRevision,
		Status: "no_content",
	}); err != nil {
		t.Fatalf("CompletePreCompaction: %v", err)
	}
	again, err := coordinator.BeginPreCompaction(context.Background(), req)
	if err != nil || again.Allowed || again.Reason != "context_revision_already_flushed" {
		t.Fatalf("BeginPreCompaction(replay) = %+v, %v", again, err)
	}
	cursor, ok, err := state.LoadFlushState(context.Background(), req.TenantID, req.AgentID, req.SessionID)
	if err != nil || !ok || cursor.LastSuccessfulRevision != req.ContextRevision || cursor.AttemptCount != 1 {
		t.Fatalf("cursor = %+v, %t, %v", cursor, ok, err)
	}
}

func TestCoordinatorDoesNotDowngradeSuccessfulPreCompactionOutcome(t *testing.T) {
	state := NewMemoryFlushStateStore()
	coordinator := NewCoordinator(CoordinatorOptions{State: state})
	ctx := context.Background()
	base := FlushState{
		TenantID: "tenant-a", AgentID: "agent-1", SessionID: "session-1",
		LastSuccessfulRevision: "revision-1", LastSuccessfulDate: "2026-07-17",
		AttemptRevision: "revision-1", AttemptTrigger: TriggerPreCompaction, AttemptCount: 1,
	}
	if err := state.SaveFlushState(ctx, base); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.CompletePreCompaction(ctx, PreCompactionOutcomeRequest{
		TenantID: "tenant-a", AgentID: "agent-1", SessionID: "session-1", ContextRevision: "revision-1", Status: "failed",
	}); err != nil {
		t.Fatalf("CompletePreCompaction: %v", err)
	}
	got, _, err := state.LoadFlushState(ctx, "tenant-a", "agent-1", "session-1")
	if err != nil || got.LastSuccessfulRevision != "revision-1" || got.LastFailure != "" || got.Exhausted {
		t.Fatalf("state was downgraded: %+v err=%v", got, err)
	}
}

func TestCoordinatorPreCompactionExhaustionPersistsAcrossCallbacks(t *testing.T) {
	coordinator := NewCoordinator(CoordinatorOptions{State: NewMemoryFlushStateStore()})
	req := PreCompactionAttemptRequest{
		TenantID: "tenant-a", AgentID: "agent-1", SessionID: "session-1", ContextRevision: "revision-1",
		Pressure: FlushPressure{TranscriptBytes: 2 * 1024 * 1024},
	}
	for attempt := 1; attempt <= 3; attempt++ {
		decision, err := coordinator.BeginPreCompaction(context.Background(), req)
		if err != nil || !decision.Allowed || decision.Attempt != attempt {
			t.Fatalf("attempt %d decision = %+v, %v", attempt, decision, err)
		}
		if err := coordinator.CompletePreCompaction(context.Background(), PreCompactionOutcomeRequest{
			TenantID: req.TenantID, AgentID: req.AgentID, SessionID: req.SessionID, ContextRevision: req.ContextRevision,
			Status: "failed", Failure: "model failed",
		}); err != nil {
			t.Fatalf("complete attempt %d: %v", attempt, err)
		}
	}
	decision, err := coordinator.BeginPreCompaction(context.Background(), req)
	if err != nil || decision.Allowed || decision.Reason != "context_revision_exhausted" {
		t.Fatalf("exhausted decision = %+v, %v", decision, err)
	}
}

func TestCoordinatorReportsStateSaveFailureAfterSuccessfulMaintenance(t *testing.T) {
	state := &failingFlushStateStore{load: FlushState{TenantID: "tenant-a", AgentID: "agent-1", SessionID: "session-1"}}
	runner := &sequenceDiaryRunner{responses: []agentcore.RuntimeResponse{{
		Status: agentcore.RuntimeStatusCompleted,
		Messages: []agentcore.RuntimeMessage{{
			Type: agentcore.RuntimeMessageToolResult, Tool: string(toolstd.MemoryDiaryAppend), Status: "completed",
			Output: `{"status":"written","memory_id":"memory-1"}`,
		}},
	}}}
	coordinator := NewCoordinator(CoordinatorOptions{State: state})
	outcome := coordinator.FlushTerminal(context.Background(), terminalFlushRequest(runner, "result-1"))
	if outcome.Status != agentcore.DiaryFlushFailed || outcome.Reason != "state_save_failed" {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestCoordinatorSkipsTerminalAfterCompletePreCompactionFlush(t *testing.T) {
	runner := &sequenceDiaryRunner{}
	coordinator := NewCoordinator(CoordinatorOptions{State: NewMemoryFlushStateStore()})
	req := terminalFlushRequest(runner, "result-1")
	req.Response.ProviderMetadata["diary_precompact_status"] = "written"
	req.Response.ProviderMetadata["pi_post_compaction_activity"] = "false"
	req.Response.ProviderMetadata["pi_compaction_count"] = "1"
	req.Response.ProviderMetadata["diary_precompact_latest_sequence"] = "1"
	req.Response.ProviderMetadata["diary_precompact_success_sequence"] = "1"
	outcome := coordinator.FlushTerminal(context.Background(), req)
	if outcome.Status != agentcore.DiaryFlushSkipped || outcome.Reason != "pre_compaction_covers_latest_context" || len(runner.requests) != 0 {
		t.Fatalf("outcome = %+v requests=%d", outcome, len(runner.requests))
	}
}

func TestCoordinatorDoesNotLetEarlierFlushCoverLaterCompaction(t *testing.T) {
	metadata := map[string]string{
		"diary_precompact_status":           "written",
		"pi_post_compaction_activity":       "false",
		"pi_compaction_count":               "2",
		"diary_precompact_latest_sequence":  "2",
		"diary_precompact_success_sequence": "1",
	}
	if preCompactionCoversLatest(metadata) {
		t.Fatalf("earlier successful flush covered a later compaction: %+v", metadata)
	}
}

func terminalFlushRequest(runtime agentcore.SessionRuntime, result string) agentcore.DiaryFlushRequest {
	return agentcore.DiaryFlushRequest{
		Runtime: runtime,
		Request: agentcore.RuntimeRequest{TenantID: "tenant-a", AgentID: "agent-1", RunID: "run-1"},
		Response: agentcore.RuntimeResponse{
			Status:           agentcore.RuntimeStatusCompleted,
			Messages:         []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Content: result, Status: agentcore.RuntimeMessageStatusFinal}},
			ProviderMetadata: map[string]string{"session_id": "session-1"},
		},
		Capabilities: canonicalDiaryCapabilities(),
	}
}

func canonicalDiaryCapabilities() agentcore.Capabilities {
	return agentcore.Capabilities{SupportsResume: true, Signals: agentcore.SignalSet{ToolLifecycle: true, TerminalResult: true}}
}

type sequenceDiaryRunner struct {
	requests  []agentcore.RuntimeRequest
	responses []agentcore.RuntimeResponse
	errors    []error
	inspect   func(agentcore.RuntimeRequest)
}

type contextDeadlineDiaryRunner struct{}

type recordingTerminalAppender struct {
	requests []AppendRequest
	receipt  AppendReceipt
	err      error
}

func (a *recordingTerminalAppender) Append(_ context.Context, req AppendRequest) (AppendReceipt, error) {
	a.requests = append(a.requests, req)
	return a.receipt, a.err
}

func (contextDeadlineDiaryRunner) RunSession(ctx context.Context, _ agentcore.RuntimeRequest, _ agentcore.SessionEventObserver) (agentcore.RuntimeResponse, error) {
	<-ctx.Done()
	return agentcore.RuntimeResponse{}, ctx.Err()
}

func (contextDeadlineDiaryRunner) CancelSession(context.Context, agentcore.AgentRunID, string) error {
	return nil
}
func (contextDeadlineDiaryRunner) SteerSession(context.Context, agentcore.AgentRunID, string) error {
	return nil
}
func (contextDeadlineDiaryRunner) PrewarmSession(context.Context, agentcore.RuntimeRequest) error {
	return nil
}
func (contextDeadlineDiaryRunner) ReleaseRuntimeWorkspace(context.Context, string) error { return nil }
func (contextDeadlineDiaryRunner) Capabilities(context.Context) agentcore.Capabilities {
	return canonicalDiaryCapabilities()
}

type failingFlushStateStore struct {
	load      FlushState
	mutations int
}

func (s *failingFlushStateStore) LoadFlushState(context.Context, string, string, string) (FlushState, bool, error) {
	return s.load, true, nil
}

func (s *failingFlushStateStore) SaveFlushState(context.Context, FlushState) error {
	return errors.New("injected state save failure")
}

func (s *failingFlushStateStore) MutateFlushState(_ context.Context, _, _, _ string, mutate func(*FlushState, bool) error) (FlushState, error) {
	s.mutations++
	if s.mutations > 1 {
		return FlushState{}, errors.New("injected state save failure")
	}
	state := s.load
	if err := mutate(&state, true); err != nil {
		return FlushState{}, err
	}
	s.load = state
	return state, nil
}

func (r *sequenceDiaryRunner) RunSession(_ context.Context, req agentcore.RuntimeRequest, _ agentcore.SessionEventObserver) (agentcore.RuntimeResponse, error) {
	r.requests = append(r.requests, req)
	if r.inspect != nil {
		r.inspect(req)
	}
	index := len(r.requests) - 1
	if index < len(r.errors) && r.errors[index] != nil {
		return agentcore.RuntimeResponse{}, r.errors[index]
	}
	if index < len(r.responses) {
		return r.responses[index], nil
	}
	return agentcore.RuntimeResponse{Status: agentcore.RuntimeStatusFailed}, nil
}

func (*sequenceDiaryRunner) CancelSession(context.Context, agentcore.AgentRunID, string) error {
	return nil
}
func (*sequenceDiaryRunner) SteerSession(context.Context, agentcore.AgentRunID, string) error {
	return nil
}
func (*sequenceDiaryRunner) PrewarmSession(context.Context, agentcore.RuntimeRequest) error {
	return nil
}
func (*sequenceDiaryRunner) ReleaseRuntimeWorkspace(context.Context, string) error { return nil }
func (*sequenceDiaryRunner) Capabilities(context.Context) agentcore.Capabilities {
	return canonicalDiaryCapabilities()
}
