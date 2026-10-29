package pi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestBridgeExtensionInstallsRestrictedPreCompactionDiaryFlush(t *testing.T) {
	source := piBridgeExtensionSource()
	for _, required := range []string{
		`pi.on("session_before_compact"`,
		`/diary-flush-start`,
		`compaction_sequence`,
		`complete(`,
		`ctx.model`,
		`convertToLlm`,
		`serializeConversation`,
		`memory_diary_read`,
		`memory_diary_append`,
		`/diary-flush-attempt`,
		`const maxToolRounds = 4`,
		`for (let round = 0; round < maxToolRounds; round++)`,
		`NO_REPLY`,
		`/diary-tool-call`,
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("PI extension missing %q:\n%s", required, source)
		}
	}
	if strings.Contains(source, `name: "bash"`) || strings.Contains(source, `name: "write"`) {
		t.Fatalf("diary maintenance source exposed a general mutation tool:\n%s", source)
	}
}

func TestBridgeExtensionPreservesRuntimeToolImages(t *testing.T) {
	source := piBridgeExtensionSource()
	for _, required := range []string{
		`function runtimeToolResultContent(output: any): any[]`,
		`block?.type === "image"`,
		`block.mimeType.startsWith("image/")`,
		`content: runtimeToolResultContent(output)`,
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("PI extension missing image result bridge %q:\n%s", required, source)
		}
	}
}

func TestBridgeDiaryAttemptAndOutcomeUsePersistedController(t *testing.T) {
	controller := &recordingDiaryFlushController{}
	bridge := newBridgeServer(context.Background(), controller, nil, runtimecore.ExecOptions{
		TenantID: "system", AgentID: "agent-1",
		ProviderSpecific: map[string]string{"diary_flush_enabled": "true", "diary_tenant_id": "tenant-a"},
	})
	server := httptest.NewServer(bridge)
	defer server.Close()

	startResp, err := http.Post(server.URL+"/diary-flush-start", "application/json", strings.NewReader(`{
		"compaction_sequence":99
	}`))
	if err != nil {
		t.Fatalf("POST start: %v", err)
	}
	var started map[string]any
	if err := json.NewDecoder(startResp.Body).Decode(&started); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	startResp.Body.Close()
	if startResp.StatusCode != http.StatusOK {
		t.Fatalf("start status=%d", startResp.StatusCode)
	}
	if started["compaction_sequence"] != float64(1) {
		t.Fatalf("host compaction sequence = %+v, want 1", started)
	}

	attemptResp, err := http.Post(server.URL+"/diary-flush-attempt", "application/json", strings.NewReader(`{
		"session_id":"session-1","context_revision":"revision-1","context_window_tokens":100000,
		"reserve_tokens":20000,"projected_tokens":76000,"transcript_bytes":1024,"compaction_sequence":1
	}`))
	if err != nil {
		t.Fatalf("POST attempt: %v", err)
	}
	attemptResp.Body.Close()
	if attemptResp.StatusCode != http.StatusOK || controller.begin.TenantID != "tenant-a" || controller.begin.ContextRevision != "revision-1" || controller.begin.ProjectedTokens != 76000 {
		t.Fatalf("attempt status=%d request=%+v", attemptResp.StatusCode, controller.begin)
	}

	outcomeResp, err := http.Post(server.URL+"/diary-flush-outcome", "application/json", strings.NewReader(`{
		"status":"no_content","session_id":"session-1","context_revision":"revision-1",
		"date":"2026-07-17","compaction_sequence":1
	}`))
	if err != nil {
		t.Fatalf("POST outcome: %v", err)
	}
	outcomeResp.Body.Close()
	if outcomeResp.StatusCode != http.StatusOK || controller.complete.TenantID != "tenant-a" || controller.complete.Status != "no_content" || controller.complete.ContextRevision != "revision-1" {
		t.Fatalf("outcome status=%d request=%+v", outcomeResp.StatusCode, controller.complete)
	}
	metadata := bridge.Metadata()
	if metadata["diary_precompact_latest_sequence"] != "1" || metadata["diary_precompact_success_sequence"] != "1" {
		t.Fatalf("bridge metadata = %+v", metadata)
	}
}

func TestBridgeDiaryConfigRecomputesTargetAfterMidnight(t *testing.T) {
	var call runtimecore.RuntimeToolCallRequest
	dispatcher := runtimecore.RuntimeToolDispatcherFunc(func(_ context.Context, req runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
		call = req
		return runtimecore.RuntimeToolCallResult{Output: map[string]any{"status": "not_found"}}, nil
	})
	bridge := newBridgeServer(context.Background(), dispatcher, nil, runtimecore.ExecOptions{ProviderSpecific: map[string]string{
		"diary_flush_enabled": "true", "diary_timezone": "Asia/Shanghai",
		"diary_target_date": "2026-07-17", "diary_target_path": "memory/2026-07-17.md",
	}})
	bridge.startDiaryCompaction()
	bridge.now = func() time.Time { return time.Date(2026, 7, 17, 16, 30, 0, 0, time.UTC) }
	server := httptest.NewServer(bridge)
	defer server.Close()
	resp, err := http.Get(server.URL + "/diary-config")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload["date"] != "2026-07-18" || payload["path"] != "memory/2026-07-18.md" {
		t.Fatalf("diary config = %+v", payload)
	}
	toolResp, err := http.Post(server.URL+"/diary-tool-call", "application/json", strings.NewReader(`{
		"tool_call_id":"read-1","tool":"memory_diary_read","session_id":"session-1","context_revision":"revision-1",
		"compaction_sequence":1,"input":{"path":"memory/2026-07-18.md"}
	}`))
	if err != nil {
		t.Fatalf("POST tool: %v", err)
	}
	toolResp.Body.Close()
	if toolResp.StatusCode != http.StatusOK || call.ProviderSpecific["diary_target_date"] != "2026-07-18" || call.ProviderSpecific["diary_target_path"] != "memory/2026-07-18.md" {
		t.Fatalf("tool status=%d metadata=%+v", toolResp.StatusCode, call.ProviderSpecific)
	}
}

func TestBridgeDiaryEndpointLocksHostIdentityAndRecordsOutcome(t *testing.T) {
	var got runtimecore.RuntimeToolCallRequest
	dispatcher := runtimecore.RuntimeToolDispatcherFunc(func(_ context.Context, call runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
		got = call
		return runtimecore.RuntimeToolCallResult{Output: map[string]any{"status": "written", "memory_id": "memory-1"}}, nil
	})
	bridge := newBridgeServer(context.Background(), dispatcher, nil, runtimecore.ExecOptions{
		TenantID: "system", RunID: "run-1", AgentID: "agent-1",
		ContextPack: agentcore.ContextPack{ResponsibilityContext: &agentcore.ResponsibilityContext{
			ContextID: "context-1", ResponsibilityID: "responsibility-1", ResponsibilityVersion: 1,
			PolicyID: "policy-1", PolicyVersion: 1, AgentProfileID: "agent-1",
			RequireExplicitToolScopes: true, TaskPermissionScopes: []string{toolstd.PermissionMemoryDiaryWrite},
		}},
		ProviderSpecific: map[string]string{
			"diary_flush_enabled": "true", "diary_target_date": "2026-07-17", "diary_target_path": "memory/2026-07-17.md",
			"diary_mission_id": "mission-1", "diary_task_id": "task-1", "diary_run_id": "run-1",
			"diary_tenant_id": "tenant-a",
		},
	})
	bridge.startDiaryCompaction()
	server := httptest.NewServer(bridge)
	defer server.Close()
	resp, err := http.Post(server.URL+"/diary-tool-call", "application/json", strings.NewReader(`{
		"tool_call_id":"append-1","tool":"memory_diary_append","session_id":"session-1","context_revision":"revision-1",
		"compaction_sequence":1,"input":{"path":"memory/attacker.md","content":"- Durable result."}
	}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got.TenantID != "tenant-a" || got.AgentID != "agent-1" || got.Name != string(toolstd.MemoryDiaryAppend) {
		t.Fatalf("dispatch identity = %+v", got)
	}
	if len(got.ToolBindings) != 2 || got.ToolBindings[0].ToolID != toolstd.MemoryDiaryRead || got.ToolBindings[1].ToolID != toolstd.MemoryDiaryAppend {
		t.Fatalf("diary bindings = %+v", got.ToolBindings)
	}
	if got.ResponsibilityContext == nil || !got.ResponsibilityContext.RequireExplicitToolScopes || len(got.ResponsibilityContext.TaskPermissionScopes) != 1 || got.ResponsibilityContext.TaskPermissionScopes[0] != toolstd.PermissionMemoryDiaryWrite {
		t.Fatalf("diary responsibility context = %+v", got.ResponsibilityContext)
	}
	if got.ProviderSpecific["diary_target_path"] != "memory/2026-07-17.md" ||
		got.ProviderSpecific["diary_session_id"] != "session-1" ||
		got.ProviderSpecific["diary_context_revision"] != "revision-1" ||
		got.ProviderSpecific["diary_trigger"] != "pre_compaction" ||
		got.ProviderSpecific["diary_idempotency_key"] == "" {
		t.Fatalf("host metadata = %+v", got.ProviderSpecific)
	}
	metadata := bridge.Metadata()
	if metadata["diary_precompact_status"] != "written" || metadata["diary_precompact_revision"] != "revision-1" ||
		metadata["diary_precompact_latest_sequence"] != "1" || metadata["diary_precompact_success_sequence"] != "1" {
		t.Fatalf("bridge metadata = %+v", metadata)
	}
}

func TestBridgeDiaryOutcomeDoesNotDowngradeSuccessfulRevision(t *testing.T) {
	bridge := newBridgeServer(context.Background(), nil, nil, runtimecore.ExecOptions{})
	bridge.recordDiaryOutcome("written", "revision-1", 1)
	bridge.recordDiaryOutcome("failed", "revision-1", 1)
	metadata := bridge.Metadata()
	if metadata["diary_precompact_status"] != "written" || metadata["diary_precompact_revision"] != "revision-1" {
		t.Fatalf("bridge metadata was downgraded: %+v", metadata)
	}
}

func TestBridgeDiarySequenceResetsForEachRuntimeRun(t *testing.T) {
	opts := runtimecore.ExecOptions{ProviderSpecific: map[string]string{"diary_flush_enabled": "true"}}
	bridge := newBridgeServer(context.Background(), nil, nil, opts)
	if first, second := bridge.startDiaryCompaction(), bridge.startDiaryCompaction(); first != 1 || second != 2 {
		t.Fatalf("first run sequences = %d, %d", first, second)
	}
	bridge.Update(context.Background(), nil, nil, opts)
	if got := bridge.startDiaryCompaction(); got != 1 {
		t.Fatalf("sequence after runtime update = %d, want 1", got)
	}
}

func TestDiaryMaintenanceBindingsContainOnlyReadAndAppend(t *testing.T) {
	bindings := piDiaryMaintenanceBindings()
	if len(bindings) != 2 || bindings[0].ToolID != toolstd.MemoryDiaryRead || bindings[1].ToolID != toolstd.MemoryDiaryAppend {
		t.Fatalf("bindings = %+v", bindings)
	}
	for _, binding := range bindings {
		if binding.ToolID != toolstd.MemoryDiaryRead && binding.ToolID != toolstd.MemoryDiaryAppend {
			t.Fatalf("unexpected diary tool binding = %+v", binding)
		}
	}
}

type recordingDiaryFlushController struct {
	begin    runtimecore.RuntimeDiaryFlushAttemptRequest
	complete runtimecore.RuntimeDiaryFlushOutcomeRequest
}

func (c *recordingDiaryFlushController) CallRuntimeTool(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
	return runtimecore.RuntimeToolCallResult{}, nil
}

func (c *recordingDiaryFlushController) BeginRuntimeDiaryFlush(_ context.Context, req runtimecore.RuntimeDiaryFlushAttemptRequest) (runtimecore.RuntimeDiaryFlushAttemptDecision, error) {
	c.begin = req
	return runtimecore.RuntimeDiaryFlushAttemptDecision{Allowed: true, Attempt: 1}, nil
}

func (c *recordingDiaryFlushController) CompleteRuntimeDiaryFlush(_ context.Context, req runtimecore.RuntimeDiaryFlushOutcomeRequest) error {
	c.complete = req
	return nil
}
