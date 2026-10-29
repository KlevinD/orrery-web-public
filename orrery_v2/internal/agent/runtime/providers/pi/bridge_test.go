package pi

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestPiBridgeToolsNormalizesProviderNames(t *testing.T) {
	tools, err := piBridgeTools([]agentcore.RuntimeToolBinding{{
		ToolID: "enterprise_graph.build_context_bundle",
		Alias:  "Enterprise Graph",
	}}, false)
	if err != nil {
		t.Fatalf("piBridgeTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "Enterprise_Graph" {
		t.Fatalf("tools = %+v", tools)
	}
}

func TestPiBridgeToolsRejectsNormalizedNameCollisions(t *testing.T) {
	_, err := piBridgeTools([]agentcore.RuntimeToolBinding{
		{ToolID: "enterprise_graph.build_context_bundle", Alias: "Enterprise Graph"},
		{ToolID: "enterprise_graph.search", Alias: "Enterprise-Graph"},
	}, false)
	if err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("piBridgeTools error = %v, want collision", err)
	}
}

func TestPiBridgeToolsRejectsEmptyProviderName(t *testing.T) {
	_, err := piBridgeTools([]agentcore.RuntimeToolBinding{{Alias: "工具"}}, false)
	if err == nil || !strings.Contains(err.Error(), "no provider-safe name") {
		t.Fatalf("piBridgeTools error = %v, want provider-safe name failure", err)
	}
}

func TestPrepareBridgeWritesExtensionAndConfig(t *testing.T) {
	opts := runtimecore.ExecOptions{
		Cwd:         t.TempDir(),
		Environment: map[string]string{"PI_CODING_AGENT_DIR": filepath.Join(t.TempDir(), "pi-home")},
		ToolBindings: []agentcore.RuntimeToolBinding{{
			ToolID:      "mission.prepare",
			Alias:       "mission-prepare",
			Summary:     "Prepare mission",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"title": map[string]any{"type": "string"}}},
		}},
		SkillBindings: []agentcore.RuntimeSkillBinding{{SkillID: "tdd", Summary: "Write tests first"}},
	}
	prepared, cleanup, err := prepareBridge(context.Background(), runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
		return runtimecore.RuntimeToolCallResult{}, nil
	}), nil, opts)
	defer cleanup()
	if err != nil {
		t.Fatalf("prepare bridge: %v", err)
	}
	if prepared.ExtensionPath == "" || prepared.ConfigPath == "" || prepared.BaseURL == "" {
		t.Fatalf("prepared bridge = %+v", prepared)
	}
	extensionText := readBridgeFile(t, prepared.ExtensionPath)
	if !strings.Contains(extensionText, "pi.registerTool") || !strings.Contains(extensionText, "ORRERY_PI_BRIDGE_CONFIG") {
		t.Fatalf("extension missing bridge registration:\n%s", extensionText)
	}
	var cfg piBridgeConfig
	if err := json.Unmarshal([]byte(readBridgeFile(t, prepared.ConfigPath)), &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if !hasBridgeTool(cfg.Tools, "mission_prepare", "mission.prepare") ||
		!hasBridgeTool(cfg.Tools, "plan_create", "plan.create") ||
		!hasBridgeTool(cfg.Tools, "plan_update", "plan.update") ||
		!hasBridgeTool(cfg.Tools, "plan_complete", "plan.complete") {
		t.Fatalf("bridge tools = %+v", cfg.Tools)
	}
	if cfg.AuthToken == "" || cfg.AuthToken != prepared.authToken {
		t.Fatal("bridge config must contain the generated local authentication token")
	}
	if cfg.ToolCallTimeoutMs != 0 {
		t.Fatalf("ordinary bridge tool timeout = %d, want runtime signal", cfg.ToolCallTimeoutMs)
	}
}

func TestPrepareBridgeDiaryMaintenanceExposesOnlyBoundDiaryTools(t *testing.T) {
	opts := runtimecore.ExecOptions{
		Cwd:              t.TempDir(),
		Environment:      map[string]string{"PI_CODING_AGENT_DIR": filepath.Join(t.TempDir(), "pi-home")},
		ProviderSpecific: map[string]string{"diary_maintenance": "true"},
		ToolBindings: []agentcore.RuntimeToolBinding{
			{ToolID: "memory_diary_read", InputSchema: map[string]any{"type": "object"}},
			{ToolID: "memory_diary_append", InputSchema: map[string]any{"type": "object"}},
		},
	}
	prepared, cleanup, err := prepareBridge(context.Background(), runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
		return runtimecore.RuntimeToolCallResult{}, nil
	}), nil, opts)
	defer cleanup()
	if err != nil {
		t.Fatalf("prepare bridge: %v", err)
	}
	var cfg piBridgeConfig
	if err := json.Unmarshal([]byte(readBridgeFile(t, prepared.ConfigPath)), &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if len(cfg.Tools) != 2 || !hasBridgeTool(cfg.Tools, "memory_diary_read", "memory_diary_read") ||
		!hasBridgeTool(cfg.Tools, "memory_diary_append", "memory_diary_append") {
		t.Fatalf("diary maintenance tools = %+v", cfg.Tools)
	}
	for _, tool := range cfg.Tools {
		if strings.HasPrefix(tool.Name, "plan_") {
			t.Fatalf("diary maintenance exposed plan tool %q", tool.Name)
		}
	}
}

func TestPrepareBridgeUsesTaskScopedToolCallTimeout(t *testing.T) {
	agentDir := filepath.Join(t.TempDir(), "pi-home")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(agentDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"theme":"dark","retry":{"enabled":false},"httpIdleTimeoutMs":300000}`), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := runtimecore.ExecOptions{
		Cwd:         t.TempDir(),
		Environment: map[string]string{"PI_CODING_AGENT_DIR": agentDir},
		ProviderSpecific: map[string]string{
			runtimecore.ToolCallTimeoutSecondsProviderKey: "1200",
		},
		ToolBindings: []agentcore.RuntimeToolBinding{{ToolID: "personal_assistant_shell_exec", Alias: "Shell"}},
	}
	prepared, cleanup, err := prepareBridge(context.Background(), runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
		return runtimecore.RuntimeToolCallResult{}, nil
	}), nil, opts)
	defer cleanup()
	if err != nil {
		t.Fatalf("prepare bridge: %v", err)
	}
	var cfg piBridgeConfig
	if err := json.Unmarshal([]byte(readBridgeFile(t, prepared.ConfigPath)), &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if cfg.ToolCallTimeoutMs != 1200000 {
		t.Fatalf("task-scoped bridge tool timeout = %d, want 1200000", cfg.ToolCallTimeoutMs)
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(readBridgeFile(t, settingsPath)), &settings); err != nil {
		t.Fatalf("decode PI settings: %v", err)
	}
	retry := settings["retry"].(map[string]any)
	provider := retry["provider"].(map[string]any)
	if settings["httpIdleTimeoutMs"] != float64(1200000) || settings["theme"] != "dark" || retry["enabled"] != false || provider["timeoutMs"] != float64(1200000) {
		t.Fatalf("PI settings = %+v", settings)
	}
	if extension := readBridgeFile(t, prepared.ExtensionPath); !strings.Contains(extension, "toolCallSignal(config, signal)") || !strings.Contains(extension, "AbortSignal.any([fallback") {
		t.Fatalf("extension does not combine the task-scoped timeout with runtime cancellation:\n%s", extension)
	}
}

func TestBridgeServerHandlesPlanToolsWithoutRuntimeDispatcher(t *testing.T) {
	var dispatcherCalls int
	dispatcher := runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
		dispatcherCalls++
		return runtimecore.RuntimeToolCallResult{}, errors.New("dispatcher should not handle plan tools")
	})
	server := httptest.NewServer(newBridgeServer(context.Background(), dispatcher, nil, runtimecore.ExecOptions{RunID: "run-plan", AgentID: "agent-plan"}))
	defer server.Close()

	resp, err := http.Post(server.URL+"/tool-call", "application/json", strings.NewReader(`{"tool_call_id":"plan-call-1","tool":"plan_create","input":{"plan_id":"plan-1","items":[{"id":"step-1","text":"Inspect runtime bridge","status":"in_progress"},{"id":"step-2","text":"Persist progress","status":"pending"}]}}`))
	if err != nil {
		t.Fatalf("post plan create: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	payload := decodeBridgeJSON(t, resp.Body)
	output := payload["output"].(map[string]any)
	if output["status"] != "active" || output["plan_id"] != "plan-1" {
		t.Fatalf("plan create output = %+v", output)
	}
	if dispatcherCalls != 0 {
		t.Fatalf("dispatcher calls = %d", dispatcherCalls)
	}
}

func TestBridgeServerRejectsInvalidPlanPayload(t *testing.T) {
	server := httptest.NewServer(newBridgeServer(context.Background(), nil, nil, runtimecore.ExecOptions{}))
	defer server.Close()

	resp, err := http.Post(server.URL+"/tool-call", "application/json", strings.NewReader(`{"tool_call_id":"plan-call-1","tool":"plan_create","input":{"items":[{"text":"missing id","status":"pending"}]}}`))
	if err != nil {
		t.Fatalf("post plan create: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	payload := decodeBridgeJSON(t, resp.Body)
	if !strings.Contains(payload["error"].(string), "plan_id is required") {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestPrepareBridgeWritesOnlyBusinessHumanApprovalTools(t *testing.T) {
	opts := runtimecore.ExecOptions{
		Cwd:            t.TempDir(),
		Environment:    map[string]string{"PI_CODING_AGENT_DIR": filepath.Join(t.TempDir(), "pi-home")},
		ProviderConfig: json.RawMessage(`{"approval_mode":"on-request"}`),
		ToolBindings: []agentcore.RuntimeToolBinding{
			{ToolID: "knowledge_search", GrantedPermissions: []string{"knowledge.read"}},
			{ToolID: toolstd.KnowledgeAdd, GrantedPermissions: []string{toolstd.PermissionKnowledgeWrite}},
			{ToolID: toolstd.OntologyProposalCreate, GrantedPermissions: []string{toolstd.PermissionOntologyManage}},
			{ToolID: toolstd.OntologyProposalDecide, GrantedPermissions: []string{toolstd.PermissionOntologyManage}},
			{ToolID: toolstd.OntologyInferenceWorkflow, GrantedPermissions: []string{toolstd.PermissionOntologyManage}},
			{ToolID: toolstd.OntologyPromotionPlanCreate, GrantedPermissions: []string{toolstd.PermissionOntologyManage}},
			{ToolID: toolstd.OntologyPromotionApply, GrantedPermissions: []string{toolstd.PermissionOntologyManage}},
			{ToolID: toolstd.OntologyProjectionRefreshRetry, GrantedPermissions: []string{toolstd.PermissionOntologyManage}},
			{ToolID: toolstd.RequestBusinessApproval, GrantedPermissions: []string{toolstd.PermissionApprovalRequest}},
			{ToolID: "email_send", GrantedPermissions: []string{"communication.send"}},
			{
				ToolID:             "mcp.marketing.marketing_wechat_draft_create",
				Alias:              "mcp_marketing_marketing_wechat_draft_create",
				GrantedPermissions: []string{"marketing.write"},
			},
		},
	}
	broker := runtimecore.ApprovalBrokerFunc(func(context.Context, runtimecore.ApprovalRequest, runtimecore.ApprovalResponder) error {
		return nil
	})
	prepared, cleanup, err := prepareBridge(context.Background(), nil, broker, opts)
	defer cleanup()
	if err != nil {
		t.Fatalf("prepare bridge: %v", err)
	}
	if prepared.ExtensionPath == "" || prepared.ConfigPath == "" || prepared.BaseURL == "" {
		t.Fatalf("prepared bridge = %+v", prepared)
	}
	extensionText := readBridgeFile(t, prepared.ExtensionPath)
	if !strings.Contains(extensionText, `pi.on("tool_call"`) || !strings.Contains(extensionText, `"/approval"`) {
		t.Fatalf("extension missing runtime approval hook:\n%s", extensionText)
	}
	var cfg piBridgeConfig
	if err := json.Unmarshal([]byte(readBridgeFile(t, prepared.ConfigPath)), &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if cfg.Approval == nil || !cfg.Approval.Enabled {
		t.Fatalf("approval config = %+v", cfg.Approval)
	}
	if got := strings.Join(cfg.Approval.HumanApprovalTools, ","); got != "email_send,mcp_marketing_marketing_wechat_draft_create" {
		t.Fatalf("human approval tools = %q", got)
	}
	if !strings.Contains(extensionText, "human_approval_tools") {
		t.Fatalf("extension missing business approval gate:\n%s", extensionText)
	}
}

func TestPiHumanApprovalRuntimeToolsExcludeSystemAndBridgeTools(t *testing.T) {
	got := piHumanApprovalRuntimeTools([]agentcore.RuntimeToolBinding{
		{ToolID: toolstd.KnowledgeAdd, GrantedPermissions: []string{toolstd.PermissionKnowledgeWrite}},
		{ToolID: toolstd.OntologyProposalCreate, GrantedPermissions: []string{toolstd.PermissionOntologyManage}},
		{ToolID: toolstd.OntologyProposalDecide, GrantedPermissions: []string{toolstd.PermissionOntologyManage}},
		{ToolID: toolstd.OntologyInferenceWorkflow, GrantedPermissions: []string{toolstd.PermissionOntologyManage}},
		{ToolID: toolstd.OntologyPromotionPlanCreate, GrantedPermissions: []string{toolstd.PermissionOntologyManage}},
		{ToolID: toolstd.OntologyPromotionApply, GrantedPermissions: []string{toolstd.PermissionOntologyManage}},
		{ToolID: toolstd.OntologyProjectionRefreshRetry, GrantedPermissions: []string{toolstd.PermissionOntologyManage}},
		{ToolID: "other_write", GrantedPermissions: []string{toolstd.PermissionKnowledgeWrite}},
		{ToolID: toolstd.RequestBusinessApproval, GrantedPermissions: []string{toolstd.PermissionApprovalRequest}},
		{ToolID: "email_send", GrantedPermissions: []string{"communication.send"}},
	})
	if joined := strings.Join(got, ","); joined != "email_send" {
		t.Fatalf("human approval tools = %q", joined)
	}
}

func TestPrepareBridgeKeepsMarketingBusinessApprovalWhenRuntimeApprovalIsAuto(t *testing.T) {
	opts := runtimecore.ExecOptions{
		Cwd:            t.TempDir(),
		Environment:    map[string]string{"PI_CODING_AGENT_DIR": filepath.Join(t.TempDir(), "pi-home")},
		ProviderConfig: json.RawMessage(`{"approval_mode":"auto"}`),
		ToolBindings: []agentcore.RuntimeToolBinding{{
			ToolID: "mcp.marketing.marketing_wechat_draft_create",
			Alias:  "mcp_marketing_marketing_wechat_draft_create",
		}},
	}
	broker := runtimecore.ApprovalBrokerFunc(func(context.Context, runtimecore.ApprovalRequest, runtimecore.ApprovalResponder) error {
		return nil
	})
	prepared, cleanup, err := prepareBridge(context.Background(), nil, broker, opts)
	defer cleanup()
	if err != nil {
		t.Fatalf("prepare bridge: %v", err)
	}
	if prepared.ConfigPath == "" {
		t.Fatalf("missing bridge config: %+v", prepared)
	}
	extensionText := readBridgeFile(t, prepared.ExtensionPath)
	if !strings.Contains(extensionText, "human_approval_tools") {
		t.Fatalf("extension missing business approval support:\n%s", extensionText)
	}
	var cfg piBridgeConfig
	if err := json.Unmarshal([]byte(readBridgeFile(t, prepared.ConfigPath)), &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if cfg.Approval == nil || !cfg.Approval.Enabled || len(cfg.Approval.HumanApprovalTools) != 1 ||
		cfg.Approval.HumanApprovalTools[0] != "mcp_marketing_marketing_wechat_draft_create" {
		t.Fatalf("approval config = %+v", cfg.Approval)
	}
}

func TestPrepareBridgeSkipsDeferredApprovalOnly(t *testing.T) {
	opts := runtimecore.ExecOptions{
		Cwd:         t.TempDir(),
		Environment: map[string]string{"PI_CODING_AGENT_DIR": filepath.Join(t.TempDir(), "pi-home")},
	}
	prepared, cleanup, err := prepareBridge(context.Background(), nil, runtimecore.DeferredApprovalBroker{}, opts)
	defer cleanup()
	if err != nil {
		t.Fatalf("prepare bridge: %v", err)
	}
	if prepared.ExtensionPath != "" || prepared.ConfigPath != "" || prepared.BaseURL != "" || len(prepared.Environment) != 0 {
		t.Fatalf("deferred approval without tools should not install bridge: %+v", prepared)
	}
}

func TestPrepareBridgeLoadsProviderRequestCaptureExtensionWithoutTools(t *testing.T) {
	opts := runtimecore.ExecOptions{
		Cwd: t.TempDir(),
		Environment: map[string]string{
			"PI_CODING_AGENT_DIR":                    filepath.Join(t.TempDir(), "pi-home"),
			"ORRERY_PI_PROVIDER_REQUEST_CAPTURE_URL": "http://127.0.0.1:1234/provider-request",
		},
	}
	prepared, cleanup, err := prepareBridge(context.Background(), nil, nil, opts)
	defer cleanup()
	if err != nil {
		t.Fatalf("prepare bridge: %v", err)
	}
	if prepared.ExtensionPath == "" || prepared.Environment["ORRERY_PI_EXTENSION_PATH"] == "" {
		t.Fatalf("capture-only extension missing: %+v", prepared)
	}
	extensionText := readBridgeFile(t, prepared.ExtensionPath)
	if !strings.Contains(extensionText, `before_provider_request`) ||
		!strings.Contains(extensionText, `ORRERY_PI_PROVIDER_REQUEST_CAPTURE_URL`) {
		t.Fatalf("extension missing provider request capture hook:\n%s", extensionText)
	}
	if strings.Contains(extensionText, "loadConfig();") {
		t.Fatalf("extension should not require bridge config when only provider capture is enabled:\n%s", extensionText)
	}
}

func TestBridgeServerDispatchesRuntimeTool(t *testing.T) {
	var got runtimecore.RuntimeToolCallRequest
	dispatcher := runtimecore.RuntimeToolDispatcherFunc(func(_ context.Context, call runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
		got = call
		return runtimecore.RuntimeToolCallResult{Output: map[string]any{"status": "completed", "title": call.Input["title"]}}, nil
	})
	opts := runtimecore.ExecOptions{
		RunID:              "run-bridge",
		AgentID:            "agent-bridge",
		ExecutionPrincipal: agentcore.ExecutionPrincipal{Kind: agentcore.ExecutionPrincipalUser, ID: "user-bridge", Source: "system_assistant"},
		Cwd:                t.TempDir(),
		ContextPack: agentcore.ContextPack{ResponsibilityContext: &agentcore.ResponsibilityContext{
			ContextID: "context-1", ResponsibilityID: "responsibility-1", ResponsibilityVersion: 2,
			PolicyID: "policy-1", PolicyVersion: 4, AgentProfileID: "profile-1",
		}},
		ToolBindings: []agentcore.RuntimeToolBinding{{
			ToolID: "mission.prepare",
			Alias:  "mission-prepare",
		}},
	}
	prepared, cleanup, err := prepareBridge(context.Background(), dispatcher, nil, opts)
	defer cleanup()
	if err != nil {
		t.Fatalf("prepare bridge: %v", err)
	}
	resp, err := postPreparedBridge(prepared, "/tool-call", `{"tool_call_id":"call-1","tool":"mission_prepare","input":{"title":"Ship PI"}}`)
	if err != nil {
		t.Fatalf("post tool call: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got.Name != "mission.prepare" || got.CallID != "call-1" || got.Input["title"] != "Ship PI" {
		t.Fatalf("dispatch request = %+v", got)
	}
	if got.RunID != "run-bridge" || got.AgentID != "agent-bridge" {
		t.Fatalf("dispatch identity = run %q agent %q", got.RunID, got.AgentID)
	}
	if got.ExecutionPrincipal.Kind != agentcore.ExecutionPrincipalUser || got.ExecutionPrincipal.ID != "user-bridge" || got.ExecutionPrincipal.Source != "system_assistant" {
		t.Fatalf("execution principal = %+v", got.ExecutionPrincipal)
	}
	if got.ResponsibilityContext == nil || got.ResponsibilityContext.ContextID != "context-1" || got.ResponsibilityContext.PolicyVersion != 4 {
		t.Fatalf("responsibility context = %+v", got.ResponsibilityContext)
	}
}

func TestPreparedBridgeRejectsUnauthenticatedLocalRequests(t *testing.T) {
	opts := runtimecore.ExecOptions{
		Cwd: t.TempDir(),
		ToolBindings: []agentcore.RuntimeToolBinding{{
			ToolID: "mission.prepare",
			Alias:  "mission-prepare",
		}},
	}
	prepared, cleanup, err := prepareBridge(context.Background(), runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
		return runtimecore.RuntimeToolCallResult{}, nil
	}), nil, opts)
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post(prepared.BaseURL+"/tool-call", "application/json", strings.NewReader(`{"tool_call_id":"call-1","tool":"mission_prepare","input":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestBridgeServerRebindsToolDispatchToCurrentRun(t *testing.T) {
	type currentRunContextKey struct{}
	var firstCalls int
	first := runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
		firstCalls++
		return runtimecore.RuntimeToolCallResult{}, nil
	})
	var got runtimecore.RuntimeToolCallRequest
	var gotContextValue string
	second := runtimecore.RuntimeToolDispatcherFunc(func(ctx context.Context, call runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
		got = call
		gotContextValue, _ = ctx.Value(currentRunContextKey{}).(string)
		return runtimecore.RuntimeToolCallResult{Output: map[string]any{"status": "completed"}}, nil
	})
	binding := agentcore.RuntimeToolBinding{ToolID: "mission.prepare", Alias: "mission-prepare"}
	bridge := newBridgeServer(context.Background(), first, nil, runtimecore.ExecOptions{
		RunID: "run-1", AgentID: "agent-1", ToolBindings: []agentcore.RuntimeToolBinding{binding},
	})
	bridge.Update(context.WithValue(context.Background(), currentRunContextKey{}, "current-run"), second, nil, runtimecore.ExecOptions{
		RunID: "run-2", AgentID: "agent-2", ToolBindings: []agentcore.RuntimeToolBinding{binding},
	})
	server := httptest.NewServer(bridge)
	defer server.Close()

	resp, err := http.Post(server.URL+"/tool-call", "application/json", strings.NewReader(`{"tool_call_id":"call-2","tool":"mission_prepare","input":{"title":"Current run"}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if firstCalls != 0 || got.RunID != "run-2" || got.AgentID != "agent-2" || got.CallID != "call-2" {
		t.Fatalf("dispatch = %+v, stale dispatcher calls = %d", got, firstCalls)
	}
	if gotContextValue != "current-run" {
		t.Fatalf("dispatch context value = %q, want current-run", gotContextValue)
	}
}

func TestBridgeServerRequestsRuntimeApproval(t *testing.T) {
	var got runtimecore.ApprovalRequest
	broker := runtimecore.ApprovalBrokerFunc(func(_ context.Context, req runtimecore.ApprovalRequest, respond runtimecore.ApprovalResponder) error {
		got = req
		return respond(runtimecore.ApprovalResponse{Decision: runtimecore.ApprovalDecisionApproved})
	})
	opts := runtimecore.ExecOptions{
		RunID:   "run-pi",
		AgentID: "agent-pi",
		Cwd:     t.TempDir(),
	}
	server := httptest.NewServer(newBridgeServer(context.Background(), nil, broker, opts))
	defer server.Close()

	resp, err := http.Post(server.URL+"/approval", "application/json", strings.NewReader(`{"tool_call_id":"call-1","tool":"bash","input":{"command":"go test ./..."}}`))
	if err != nil {
		t.Fatalf("post approval: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	payload := decodeBridgeJSON(t, resp.Body)
	if payload["allow"] != true || payload["decision"] != string(runtimecore.ApprovalDecisionApproved) {
		t.Fatalf("approval payload = %+v", payload)
	}
	if got.Source != "pi" || got.RequestID != "call-1" ||
		got.Kind != runtimecore.ApprovalKindCommandExecution || got.ToolName != "exec_command" {
		t.Fatalf("approval request = %+v", got)
	}
	if got.Metadata["approval_run_id"] != "run-pi" || got.Metadata["approval_agent_id"] != "agent-pi" ||
		got.Metadata["approval_native_event"] != "tool_call" || got.Metadata["approval_native_tool_name"] != "bash" ||
		got.Metadata["command"] != "go test ./..." {
		t.Fatalf("approval metadata = %+v", got.Metadata)
	}
}

func TestBridgeServerAutoApprovesInternalPlanToolsWithoutApprovalBroker(t *testing.T) {
	server := httptest.NewServer(newBridgeServer(context.Background(), nil, runtimecore.DeferredApprovalBroker{}, runtimecore.ExecOptions{}))
	defer server.Close()

	for _, toolName := range []string{planCreateToolName, planUpdateToolName, planCompleteToolName} {
		t.Run(toolName, func(t *testing.T) {
			resp, err := http.Post(server.URL+"/approval", "application/json", strings.NewReader(`{"tool_call_id":"plan-call","tool":"`+toolName+`","input":{"plan_id":"plan-1"}}`))
			if err != nil {
				t.Fatalf("post approval: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			payload := decodeBridgeJSON(t, resp.Body)
			if payload["allow"] != true || payload["decision"] != string(runtimecore.ApprovalDecisionApproved) ||
				payload["reason"] != "internal runtime plan tool" {
				t.Fatalf("approval payload = %+v", payload)
			}
		})
	}
}

func TestPiBridgeApprovalRequestRetainsServerOwnedWorkspaceRoot(t *testing.T) {
	policyRoot := t.TempDir()
	workDir := filepath.Join(policyRoot, "agent")
	request := piBridgeApprovalRequest("call-root", "bash", map[string]any{"command": "pwd"}, runtimecore.ExecOptions{
		Cwd:              "/fallback/workspace",
		WorkspacePolicy:  agentcore.RuntimeWorkspacePolicy{Root: policyRoot, WorkDir: workDir, EnforceBoundary: true},
		ProviderSpecific: map[string]string{agentcore.CanonicalDiscussionWorkspaceProviderKey: "true"},
	})
	if request.WorkspaceRoot != policyRoot {
		t.Fatalf("workspace root = %q, want policy root %q", request.WorkspaceRoot, policyRoot)
	}
	if request.WorkingDirectory != workDir || !request.WorkspaceBoundaryEnforced || !request.WorkspaceWriteAuthorized {
		t.Fatalf("workspace approval authority = %+v", request)
	}

	request = piBridgeApprovalRequest("call-cwd", "bash", map[string]any{"command": "pwd"}, runtimecore.ExecOptions{Cwd: "/fallback/workspace"})
	if request.WorkspaceRoot != "/fallback/workspace" {
		t.Fatalf("workspace root = %q, want cwd fallback", request.WorkspaceRoot)
	}

	request = piBridgeApprovalRequest("call-work-dir", "bash", map[string]any{"command": "pwd"}, runtimecore.ExecOptions{
		WorkspacePolicy: agentcore.RuntimeWorkspacePolicy{WorkDir: "/managed/workspace"},
	})
	if request.WorkspaceRoot != "/managed/workspace" {
		t.Fatalf("workspace root = %q, want managed work dir fallback", request.WorkspaceRoot)
	}
}

func TestBridgeServerForwardsOneTimeMarketingHILReceipt(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encodedKey, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "marketing-hil.pem")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encodedKey}), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORRERY_MARKETING_HIL_SIGNING_KEY_ID", "orrery-test")
	t.Setenv("ORRERY_MARKETING_HIL_PRIVATE_KEY_PATH", keyPath)

	broker := runtimecore.ApprovalBrokerFunc(func(_ context.Context, req runtimecore.ApprovalRequest, respond runtimecore.ApprovalResponder) error {
		if req.Metadata["approval_force_manual"] != "true" {
			t.Fatalf("approval metadata = %+v", req.Metadata)
		}
		return respond(runtimecore.ApprovalResponse{Decision: runtimecore.ApprovalDecisionApproved, Metadata: map[string]any{
			"approval_id": "approval-native-1", "approval_mode": "hil", "approval_action_id": "approve_once",
			"approval_decided_by": "user:ding", "approval_decided_at": "2026-08-08T03:00:00Z",
		}})
	})
	var calls []runtimecore.RuntimeToolCallRequest
	dispatcher := runtimecore.RuntimeToolDispatcherFunc(func(_ context.Context, call runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
		calls = append(calls, call)
		return runtimecore.RuntimeToolCallResult{Output: map[string]any{"status": "ok"}}, nil
	})
	binding := agentcore.RuntimeToolBinding{
		ToolID: "mcp.marketing.marketing_wechat_draft_create", Alias: "marketing_wechat_draft_create",
		Source: "external_mcp", ServerToolID: "mcp.marketing", DownstreamToolName: "marketing_wechat_draft_create",
	}
	bridge := newBridgeServer(context.Background(), dispatcher, broker, runtimecore.ExecOptions{
		RunID: "run-marketing", AgentID: "jordan", ToolBindings: []agentcore.RuntimeToolBinding{binding},
	})
	bridge.now = func() time.Time { return time.Date(2026, 8, 8, 3, 1, 0, 0, time.UTC) }
	server := httptest.NewServer(bridge)
	defer server.Close()
	body := `{"tool_call_id":"call-marketing-1","tool":"marketing_wechat_draft_create","input":{"intent_id":"intent-1","intent_hash":"` + strings.Repeat("a", 64) + `","account_id":"wechat-main"}}`

	approval, err := http.Post(server.URL+"/approval", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer approval.Body.Close()
	if payload := decodeBridgeJSON(t, approval.Body); payload["allow"] != true {
		t.Fatalf("approval payload = %+v", payload)
	}
	toolCall, err := http.Post(server.URL+"/tool-call", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer toolCall.Body.Close()
	if toolCall.StatusCode != http.StatusOK || len(calls) != 1 {
		t.Fatalf("tool status = %d, calls = %d", toolCall.StatusCode, len(calls))
	}
	receipt, ok := calls[0].RequestMeta[marketingHILReceiptMetaKey].(map[string]any)
	if !ok || receipt["approval_id"] != "approval-native-1" || receipt["intent_id"] != "intent-1" || receipt["signature"] == "" {
		t.Fatalf("receipt = %#v", calls[0].RequestMeta[marketingHILReceiptMetaKey])
	}
	replay, err := http.Post(server.URL+"/tool-call", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Body.Close()
	if replay.StatusCode != http.StatusForbidden || len(calls) != 1 {
		t.Fatalf("replay status = %d, calls = %d", replay.StatusCode, len(calls))
	}
}

func TestBridgeServerConsumesMatchingMarketingHILContinuationOnce(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encodedKey, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "marketing-hil.pem")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encodedKey}), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORRERY_MARKETING_HIL_SIGNING_KEY_ID", "orrery-test")
	t.Setenv("ORRERY_MARKETING_HIL_PRIVATE_KEY_PATH", keyPath)

	var calls []runtimecore.RuntimeToolCallRequest
	dispatcher := runtimecore.RuntimeToolDispatcherFunc(func(_ context.Context, call runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
		calls = append(calls, call)
		return runtimecore.RuntimeToolCallResult{Output: map[string]any{"status": "ok"}}, nil
	})
	binding := agentcore.RuntimeToolBinding{
		ToolID: "mcp.marketing.marketing_wechat_draft_create", Alias: "marketing_wechat_draft_create",
		Source: "external_mcp", ServerToolID: "mcp.marketing", DownstreamToolName: "marketing_wechat_draft_create",
	}
	bridge := newBridgeServer(context.Background(), dispatcher, nil, runtimecore.ExecOptions{ToolBindings: []agentcore.RuntimeToolBinding{binding}, ProviderSpecific: map[string]string{
		"approval_continuation":              "true",
		"approval_decision":                  "approved",
		"approval_action_id":                 "approve_once",
		"approval_id":                        "approval-event-1",
		"approval_decided_by":                "human-ding",
		"approval_decided_at":                "2026-08-21T08:00:00Z",
		"hil_decision_id":                    "hil-decision-1",
		"hil_receipt_id":                     "hil-receipt-1",
		"hil_receipt_effect":                 "authorize",
		"approval_material_account_id":       "wechat-main",
		"approval_material_intent_id":        "intent-approved",
		"approval_material_intent_hash":      strings.Repeat("a", 64),
		runtimecore.ApprovalMetadataToolName: "mcp_marketing_marketing_wechat_draft_create",
	}})
	bridge.now = func() time.Time { return time.Date(2026, 8, 21, 8, 1, 0, 0, time.UTC) }
	server := httptest.NewServer(bridge)
	defer server.Close()

	mismatch := `{"tool_call_id":"call-wrong","tool":"marketing_wechat_draft_create","input":{"intent_id":"intent-other","intent_hash":"` + strings.Repeat("a", 64) + `","account_id":"wechat-main"}}`
	response, err := http.Post(server.URL+"/approval", "application/json", strings.NewReader(mismatch))
	if err != nil {
		t.Fatal(err)
	}
	if payload := decodeBridgeJSON(t, response.Body); payload["allow"] != false {
		response.Body.Close()
		t.Fatalf("mismatched continuation approval = %+v", payload)
	}
	response.Body.Close()

	body := `{"tool_call_id":"call-approved","tool":"marketing_wechat_draft_create","input":{"intent_id":"intent-approved","intent_hash":"` + strings.Repeat("a", 64) + `","account_id":"wechat-main"}}`
	response, err = http.Post(server.URL+"/approval", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if payload := decodeBridgeJSON(t, response.Body); payload["allow"] != true {
		response.Body.Close()
		t.Fatalf("matching continuation approval = %+v", payload)
	}
	response.Body.Close()

	replay, err := http.Post(server.URL+"/approval", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if payload := decodeBridgeJSON(t, replay.Body); payload["allow"] != false {
		replay.Body.Close()
		t.Fatalf("replayed continuation approval = %+v", payload)
	}
	replay.Body.Close()

	toolCall, err := http.Post(server.URL+"/tool-call", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer toolCall.Body.Close()
	if toolCall.StatusCode != http.StatusOK || len(calls) != 1 {
		t.Fatalf("tool status = %d, calls = %d", toolCall.StatusCode, len(calls))
	}
	receipt, ok := calls[0].RequestMeta[marketingHILReceiptMetaKey].(map[string]any)
	if !ok || receipt["approval_id"] != "approval-event-1" || receipt["intent_id"] != "intent-approved" {
		t.Fatalf("continuation receipt = %#v", calls[0].RequestMeta[marketingHILReceiptMetaKey])
	}
}

func TestBridgeServerBlocksDeniedRuntimeApproval(t *testing.T) {
	broker := runtimecore.ApprovalBrokerFunc(func(_ context.Context, req runtimecore.ApprovalRequest, respond runtimecore.ApprovalResponder) error {
		return respond(runtimecore.ApprovalResponse{Decision: runtimecore.ApprovalDecisionDenied, Reason: "not now"})
	})
	server := httptest.NewServer(newBridgeServer(context.Background(), nil, broker, runtimecore.ExecOptions{}))
	defer server.Close()

	resp, err := http.Post(server.URL+"/approval", "application/json", strings.NewReader(`{"tool_call_id":"call-1","tool":"write","input":{"path":"secrets.txt"}}`))
	if err != nil {
		t.Fatalf("post approval: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	payload := decodeBridgeJSON(t, resp.Body)
	if payload["allow"] != false || payload["decision"] != string(runtimecore.ApprovalDecisionDenied) ||
		!strings.Contains(payload["reason"].(string), "not now") {
		t.Fatalf("approval payload = %+v", payload)
	}
}

func TestBridgeServerRejectsDeferredRuntimeApprovalBroker(t *testing.T) {
	server := httptest.NewServer(newBridgeServer(context.Background(), nil, runtimecore.DeferredApprovalBroker{}, runtimecore.ExecOptions{}))
	defer server.Close()

	resp, err := http.Post(server.URL+"/approval", "application/json", strings.NewReader(`{"tool_call_id":"call-1","tool":"bash","input":{"command":"npm test"}}`))
	if err != nil {
		t.Fatalf("post approval: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	payload := decodeBridgeJSON(t, resp.Body)
	if payload["allow"] != false || payload["decision"] != string(runtimecore.ApprovalDecisionDenied) ||
		!strings.Contains(payload["reason"].(string), "approval broker unavailable") {
		t.Fatalf("approval payload = %+v", payload)
	}
}

func TestBridgeServerBlocksWhenRuntimeApprovalTimesOut(t *testing.T) {
	broker := piBridgeTimeoutBroker{timeout: 10 * time.Millisecond}
	server := httptest.NewServer(newBridgeServer(context.Background(), nil, broker, runtimecore.ExecOptions{}))
	defer server.Close()

	started := time.Now()
	resp, err := http.Post(server.URL+"/approval", "application/json", strings.NewReader(`{"tool_call_id":"call-1","tool":"bash","input":{"command":"npm test"}}`))
	if err != nil {
		t.Fatalf("post approval: %v", err)
	}
	defer resp.Body.Close()
	if time.Since(started) > time.Second {
		t.Fatal("approval timeout should not wait for the whole runtime context")
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	payload := decodeBridgeJSON(t, resp.Body)
	if payload["allow"] != false || payload["decision"] != string(runtimecore.ApprovalDecisionDenied) ||
		!strings.Contains(payload["reason"].(string), "timed out") {
		t.Fatalf("approval payload = %+v", payload)
	}
}

type piBridgeTimeoutBroker struct {
	timeout time.Duration
}

func (b piBridgeTimeoutBroker) RequestApproval(context.Context, runtimecore.ApprovalRequest, runtimecore.ApprovalResponder) error {
	return nil
}

func (b piBridgeTimeoutBroker) RuntimeApprovalHoldTimeout() time.Duration {
	return b.timeout
}

func TestBridgeServerPropagatesRuntimeToolFailure(t *testing.T) {
	dispatcher := runtimecore.RuntimeToolDispatcherFunc(func(_ context.Context, call runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
		return runtimecore.RuntimeToolCallResult{}, errors.New("title is required")
	})
	opts := runtimecore.ExecOptions{
		RunID:   "run-bridge",
		AgentID: "agent-bridge",
		Cwd:     t.TempDir(),
		ToolBindings: []agentcore.RuntimeToolBinding{{
			ToolID: "mission.prepare",
			Alias:  "mission-prepare",
		}},
	}
	prepared, cleanup, err := prepareBridge(context.Background(), dispatcher, nil, opts)
	defer cleanup()
	if err != nil {
		t.Fatalf("prepare bridge: %v", err)
	}

	resp, err := postPreparedBridge(prepared, "/tool-call", `{"tool_call_id":"call-1","tool":"mission_prepare","input":{}}`)
	if err != nil {
		t.Fatalf("post tool call: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	payload := decodeBridgeJSON(t, resp.Body)
	if !strings.Contains(payload["error"].(string), "title is required") {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestBridgeServerReturnsOptionalRetrievalFailureToAgent(t *testing.T) {
	dispatcher := runtimecore.RuntimeToolDispatcherFunc(func(_ context.Context, call runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
		return runtimecore.RuntimeToolCallResult{}, errors.New("PageIndex retrieval prompt exceeded its safe model context budget")
	})
	opts := runtimecore.ExecOptions{
		RunID:   "run-bridge",
		AgentID: "agent-bridge",
		Cwd:     t.TempDir(),
		ToolBindings: []agentcore.RuntimeToolBinding{{
			ToolID: toolstd.KnowledgeSearch,
			Alias:  "knowledge_search",
		}},
	}
	prepared, cleanup, err := prepareBridge(context.Background(), dispatcher, nil, opts)
	defer cleanup()
	if err != nil {
		t.Fatalf("prepare bridge: %v", err)
	}

	resp, err := postPreparedBridge(prepared, "/tool-call", `{"tool_call_id":"call-1","tool":"knowledge_search","input":{"query":"launch"}}`)
	if err != nil {
		t.Fatalf("post tool call: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	payload := decodeBridgeJSON(t, resp.Body)
	output, ok := payload["output"].(map[string]any)
	if !ok || output["status"] != "failed" || output["error_code"] != "optional_retrieval_failed" || output["retryable"] != false {
		t.Fatalf("payload = %+v", payload)
	}
	if !strings.Contains(output["error"].(string), "context budget") || !strings.Contains(output["recovery_hint"].(string), "Continue without") {
		t.Fatalf("output = %+v", output)
	}
}

func TestProviderRequestCaptureRecordsJSONLAndRedactsHeaders(t *testing.T) {
	capturePath := filepath.Join(t.TempDir(), "provider-requests.jsonl")
	prepared, cleanup, err := prepareProviderRequestCapture(context.Background(), capturePath)
	defer cleanup()
	if err != nil {
		t.Fatalf("prepare provider request capture: %v", err)
	}
	body := strings.NewReader(`{"model":"gpt-5","messages":[{"role":"user","content":"hello"}]}`)
	req, err := http.NewRequest(http.MethodPost, prepared.BaseURL+"/provider-request?target=openai", body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer secret-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post provider request: %v", err)
	}
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	lines := strings.Split(strings.TrimSpace(readBridgeFile(t, capturePath)), "\n")
	if len(lines) != 1 {
		t.Fatalf("capture lines = %q", lines)
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("decode capture entry: %v", err)
	}
	if entry["method"] != http.MethodPost || entry["path"] != "/provider-request" {
		t.Fatalf("entry route = %+v", entry)
	}
	headers := entry["headers"].(map[string]any)
	if headers["Authorization"] != "[redacted]" {
		t.Fatalf("headers should redact authorization: %+v", headers)
	}
	if !strings.Contains(entry["body"].(string), `"model":"gpt-5"`) {
		t.Fatalf("entry body = %q", entry["body"])
	}
}

func readBridgeFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

func decodeBridgeJSON(t *testing.T, body io.Reader) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		t.Fatalf("decode bridge response: %v", err)
	}
	return payload
}

func postPreparedBridge(prepared preparedBridge, path, body string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, prepared.BaseURL+path, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+prepared.authToken)
	return http.DefaultClient.Do(req)
}

func hasBridgeTool(tools []piBridgeTool, name, sourceToolID string) bool {
	for _, tool := range tools {
		if tool.Name == name && tool.SourceToolID == sourceToolID {
			return true
		}
	}
	return false
}
