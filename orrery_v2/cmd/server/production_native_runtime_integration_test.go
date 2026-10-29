package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	llmprofile "orrery_v2/internal/agent/runtime/llmprofile"
	claudeprovider "orrery_v2/internal/agent/runtime/providers/claude"
	codexprovider "orrery_v2/internal/agent/runtime/providers/codex"
	openclawprovider "orrery_v2/internal/agent/runtime/providers/openclaw"
	piprovider "orrery_v2/internal/agent/runtime/providers/pi"
	runtimesession "orrery_v2/internal/agent/runtime/session"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/testkit"
	skillres "orrery_v2/internal/resource/skill"
	toolstd "orrery_v2/internal/resource/tool/std"
	httptransport "orrery_v2/internal/transport/http"
)

func TestProductionRuntimeRegistryMarksManagedUnavailable(t *testing.T) {
	registry := newProductionRuntimeRegistry(productionDiscussionAgentDependencies{
		Config: config.Config{Runtime: config.RuntimeConfig{ManagedEnabled: true}},
		LLMProfiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{
			ProfileID: "llm-test",
			Provider:  "openai",
			BaseURL:   "http://127.0.0.1:1/v1",
			APIKey:    "sk-test",
			Model:     "gpt-test",
			Enabled:   true,
		}}},
	})
	t.Cleanup(func() { _ = registry.Close() })
	if _, err := registry.ResolveRuntime(context.Background(), agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged}); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeWorkspaceUnavailable) {
		t.Fatalf("managed runtime = %v", err)
	}
}

func TestProductionRuntimeRegistryUsesNativeCodexAndClaudeWithoutLegacyFallback(t *testing.T) {
	registry := newProductionRuntimeRegistry(productionDiscussionAgentDependencies{})
	t.Cleanup(func() { _ = registry.Close() })
	codexConnection, err := registry.Resolve(agentcore.RuntimeKindCodex)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := codexConnection.(*codexprovider.NativeConnection); !ok {
		t.Fatalf("Codex production connection = %T", codexConnection)
	}
	claudeConnection, err := registry.Resolve(agentcore.RuntimeKindClaude)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := claudeConnection.(*claudeprovider.NativeConnection); !ok {
		t.Fatalf("Claude production connection = %T", claudeConnection)
	}
	piConnection, err := registry.Resolve(agentcore.RuntimeKindPi)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := piConnection.(*piprovider.NativeConnection); !ok {
		t.Fatalf("PI production connection = %T", piConnection)
	}
}

func TestProductionRuntimeRegistryUsesOpenClawSessionConnection(t *testing.T) {
	registry := newProductionRuntimeRegistry(productionDiscussionAgentDependencies{})
	t.Cleanup(func() { _ = registry.Close() })
	connection, err := registry.Resolve(agentcore.RuntimeKindOpenClaw)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := connection.(*openclawprovider.Connection); !ok {
		t.Fatalf("OpenClaw production connection = %T", connection)
	}
	runtime, err := registry.ResolveRuntime(context.Background(), agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindOpenClaw})
	if err != nil {
		t.Fatal(err)
	}
	capabilities := runtime.Capabilities(context.Background())
	if !capabilities.SupportedCommands.Send || !capabilities.Signals.TerminalResult || !capabilities.SupportsResume {
		t.Fatalf("OpenClaw capabilities = %+v", capabilities)
	}
}

func TestProductionRuntimeAuthorizerRejectsCrossScopeIdentity(t *testing.T) {
	authorizer := productionRuntimeAuthorizer{}
	base := runtimesession.RuntimeAuthorization{
		TenantID: "tenant-a", RunID: "run-a", AgentID: "agent-a", RuntimeKind: agentcore.RuntimeKindCodex,
		MissionID: "mission-a", TaskID: "task-a", ContextAgentID: "agent-a", ContextMissionID: "mission-a", ContextTaskID: "task-a",
	}
	if err := authorizer.AuthorizeRuntime(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	base.ContextAgentID = "agent-b"
	if err := authorizer.AuthorizeRuntime(context.Background(), base); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeSessionMismatch) {
		t.Fatalf("cross-agent error = %v", err)
	}
	base.ContextAgentID = "agent-a"
	base.ContextTaskID = "task-b"
	if err := authorizer.AuthorizeRuntime(context.Background(), base); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeSessionMismatch) {
		t.Fatalf("cross-task error = %v", err)
	}
}

func TestProductionRuntimeCredentialReferenceMustBeTenantScoped(t *testing.T) {
	for _, test := range []struct {
		ref    string
		tenant string
		want   bool
	}{
		{ref: "secret://tenants/tenant-a/codex-auth", tenant: "tenant-a", want: true},
		{ref: "settings://tenants/tenant-a/codex-auth", tenant: "tenant-a", want: true},
		{ref: "secret://tenants/tenant-b/codex-auth", tenant: "tenant-a", want: false},
		{ref: "secret://codex-auth", tenant: "tenant-a", want: false},
		{ref: "secret://tenants/tenant-a/codex-auth?token=private", tenant: "tenant-a", want: false},
	} {
		if got := runtimeCredentialRefBelongsToTenant(test.ref, test.tenant); got != test.want {
			t.Fatalf("runtimeCredentialRefBelongsToTenant(%q, %q) = %v, want %v", test.ref, test.tenant, got, test.want)
		}
	}
}

func TestProductionRuntimeRegistryConfiguresPiWithToolCalls(t *testing.T) {
	dispatcher := runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
		return runtimecore.RuntimeToolCallResult{Output: map[string]any{"ok": true}}, nil
	})
	registry := newProductionRuntimeRegistry(productionDiscussionAgentDependencies{
		ToolDispatcher: dispatcher,
	})
	t.Cleanup(func() { _ = registry.Close() })
	runtime, err := registry.ResolveRuntime(context.Background(), agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi})
	if err != nil {
		t.Fatalf("resolve pi: %v", err)
	}
	if got := runtime.Capabilities(context.Background()); got.Transport.Provider != string(agentcore.RuntimeKindPi) || !got.Signals.ToolLifecycle {
		t.Fatalf("capabilities = %+v", got)
	}
}

func TestProductionRuntimeRegistryConfiguresCodexWithGovernedToolsAndDiary(t *testing.T) {
	dispatcher := runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
		return runtimecore.RuntimeToolCallResult{Output: map[string]any{"ok": true}}, nil
	})
	registry := newProductionRuntimeRegistry(productionDiscussionAgentDependencies{ToolDispatcher: dispatcher})
	t.Cleanup(func() { _ = registry.Close() })
	runtime, err := registry.ResolveRuntime(context.Background(), agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex})
	if err != nil {
		t.Fatalf("resolve Codex: %v", err)
	}
	capabilities := runtime.Capabilities(context.Background())
	if !capabilities.Signals.ToolLifecycle || !capabilities.SupportsResume || capabilities.Transport.Protocol != "app-server-jsonrpc-stdio" {
		t.Fatalf("Codex capabilities = %+v", capabilities)
	}
}

func TestProductionRuntimeRegistryConfiguresPiSecretResolver(t *testing.T) {
	executable := writePiRequiresMiniMaxCNKeyFixture(t)
	registry := newProductionRuntimeRegistry(productionDiscussionAgentDependencies{
		Secrets: llmprofile.StaticSecretResolver{Values: map[string]string{
			"secret://llm/minimax-cn": "token-plan",
		}},
	})
	t.Cleanup(func() { _ = registry.Close() })
	runtime, err := registry.ResolveRuntime(context.Background(), agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi})
	if err != nil {
		t.Fatalf("resolve pi: %v", err)
	}

	response, err := runtime.RunSession(context.Background(), agentcore.RuntimeRequest{
		TenantID: "tenant-pi-secret", RunID: "run-pi-secret", AgentID: "agent-pi-secret",
		MissionID:     "mission-pi-secret",
		TaskID:        "task-pi-secret",
		ContextPack:   agentcore.ContextPack{AgentID: "agent-pi-secret", WorkerPersonalGoal: "check secret"},
		InputMessages: []agentcore.RuntimeChatMessage{{Role: "user", Content: "check secret"}},
		RuntimeOptions: agentcore.RuntimeOptions{
			WorkingDirectory: t.TempDir(),
			ProviderConfig:   json.RawMessage(`{"executable":` + jsonStringLiteral(executable) + `,"model":"MiniMax-M2.7","extra_args":["--provider","minimax-cn"]}`),
			ProviderSpecific: map[string]string{
				"llm_provider":    "minimax-cn",
				"llm_api_key_ref": "secret://llm/minimax-cn",
			},
			Environment: map[string]string{"ORRERY_EXECENV_ROOT": t.TempDir()},
		},
	}, nil)
	if err != nil {
		t.Fatalf("run pi: %v", err)
	}
	if response.Status != agentcore.RuntimeStatusCompleted || firstTestRuntimeText(response.Messages) != "key ok" {
		t.Fatalf("response = %+v", response)
	}
}

func TestProductionDiscussionAgentDependenciesIncludesApprovalBroker(t *testing.T) {
	broker := runtimecore.ApprovalBrokerFunc(func(context.Context, runtimecore.ApprovalRequest, runtimecore.ApprovalResponder) error {
		return nil
	})
	deps := productionDiscussionAgentDependencies{ApprovalBroker: broker}

	if deps.IsZero() {
		t.Fatal("dependencies with approval broker should not be zero")
	}
}

func TestProductionRuntimeRegistryUsesSharedDiscussionRegistry(t *testing.T) {
	shared := runtimesession.NewConnectionRegistry()
	deps := productionDiscussionAgentDependencies{RuntimeRegistry: shared}

	if got := newProductionRuntimeRegistry(deps); got != shared {
		t.Fatalf("runtime registry = %p, want shared registry %p", got, shared)
	}
}

func writePiRequiresMiniMaxCNKeyFixture(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		path := filepath.Join(t.TempDir(), "pi-requires-minimax-cn-key.sh")
		body := `#!/bin/sh
if [ "$1" = "--version" ] || [ "$1" = "-v" ]; then
  printf '%s\n' '1.3.0'
  exit 0
fi
if [ "$MINIMAX_CN_API_KEY" != "token-plan" ]; then
  echo "missing key" >&2
  exit 2
fi
while IFS= read -r command; do
  id=$(printf '%s' "$command" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  case "$command" in
    *'"type":"get_state"'*)
      printf '%s\n' "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"get_state\",\"success\":true,\"data\":{\"sessionId\":\"pi-session-secret\",\"thinkingLevel\":\"medium\",\"model\":{\"id\":\"pi-test\",\"name\":\"PI Test\",\"provider\":\"fixture\",\"reasoning\":true}}}"
      ;;
    *'"type":"get_available_models"'*)
      printf '%s\n' "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"get_available_models\",\"success\":true,\"data\":{\"models\":[{\"id\":\"pi-test\",\"name\":\"PI Test\",\"provider\":\"fixture\",\"reasoning\":true}]}}"
      ;;
    *'"type":"get_commands"'*)
      printf '%s\n' "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"get_commands\",\"success\":true,\"data\":{\"commands\":[]}}"
      ;;
    *'"type":"prompt"'*)
      printf '%s\n' "{\"id\":\"$id\",\"type\":\"response\",\"command\":\"prompt\",\"success\":true}"
      printf '%s\n' '{"type":"agent_start"}'
      printf '%s\n' '{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"key ok"}}'
      printf '%s\n' '{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"key ok"}],"stopReason":"stop"}}'
      printf '%s\n' '{"type":"agent_end","messages":[]}'
      printf '%s\n' '{"type":"agent_settled","aborted":false}'
      ;;
  esac
done
`
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatalf("write pi secret fixture: %v", err)
		}
		return path
	}
	return buildPiRuntimeFixture(t, piRuntimeFixtureSpec{
		RequiredEnvKey:   "MINIMAX_CN_API_KEY",
		RequiredEnvValue: "token-plan",
		MessageEndText:   "key ok",
	})
}

func firstTestRuntimeText(messages []agentcore.RuntimeMessage) string {
	for _, message := range messages {
		if message.Type == agentcore.RuntimeMessageText && strings.TrimSpace(message.Content) != "" {
			return strings.TrimSpace(message.Content)
		}
	}
	return ""
}

func jsonStringLiteral(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func TestProductionSettingsRuntimeSpecDefaultsClaudeAgentSDKApprovalTimeout(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	settings := productionSettingsStore{router: env.DBRouter}
	if _, err := settings.SaveSettings(ctx, httptransport.SettingsDTO{
		RuntimeProfiles: []httptransport.SettingsRuntimeProfileDTO{{
			ProfileID:        "runtime-claude-sdk",
			DisplayName:      "Claude Code SDK",
			RuntimeKind:      "claude",
			RuntimeConfigRef: "runtime-claude-sdk",
			RuntimeConfig: map[string]any{
				"mode": "agent_sdk",
			},
			Enabled: true,
		}},
	}); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	spec, err := settings.ResolveRuntimeSpec(ctx, agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindClaude, ConfigRef: "runtime-claude-sdk"})
	if err != nil {
		t.Fatalf("ResolveRuntimeSpec: %v", err)
	}
	var config map[string]any
	if err := json.Unmarshal(spec.Config, &config); err != nil {
		t.Fatalf("decode runtime config: %v", err)
	}
	if config["approval_timeout_ms"] != float64(600000) {
		t.Fatalf("approval_timeout_ms = %#v, want 600000", config["approval_timeout_ms"])
	}
}

func TestProductionRuntimeApprovalHoldTimeoutReadsRuntimeProfileSetting(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	settings := productionSettingsStore{router: env.DBRouter}
	if _, err := settings.SaveSettings(ctx, httptransport.SettingsDTO{
		RuntimeProfiles: []httptransport.SettingsRuntimeProfileDTO{{
			ProfileID:        "runtime-claude-sdk",
			DisplayName:      "Claude Code SDK",
			RuntimeKind:      "claude",
			RuntimeConfigRef: "runtime-claude-sdk",
			RuntimeConfig: map[string]any{
				"mode":                "agent_sdk",
				"approval_timeout_ms": 180000,
			},
			Enabled: true,
		}},
	}); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	timeout := runtimeApprovalHoldTimeoutFromSettings(ctx, settings)
	if timeout != 3*time.Minute {
		t.Fatalf("approval hold timeout = %s, want 3m", timeout)
	}
}

func TestProductionRuntimeApprovalHoldTimeoutReadsPiRuntimeProfileSetting(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	settings := productionSettingsStore{router: env.DBRouter}
	if _, err := settings.SaveSettings(ctx, httptransport.SettingsDTO{
		RuntimeProfiles: []httptransport.SettingsRuntimeProfileDTO{{
			ProfileID:        "runtime-pi-local",
			DisplayName:      "PI",
			RuntimeKind:      "pi",
			RuntimeConfigRef: "runtime-pi-local",
			RuntimeConfig: map[string]any{
				"approval_mode":       "on-request",
				"approval_timeout_ms": 240000,
			},
			Enabled: true,
		}},
	}); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	timeout := runtimeApprovalHoldTimeoutFromSettings(ctx, settings)
	if timeout != 4*time.Minute {
		t.Fatalf("approval hold timeout = %s, want 4m", timeout)
	}
}

func TestProductionSettingsRuntimeSpecResolvesRuntimeProfileWhenProfileConfigIsJSONNull(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	settings := productionSettingsStore{router: env.DBRouter}
	if _, err := settings.SaveSettings(ctx, httptransport.SettingsDTO{
		RuntimeProfiles: []httptransport.SettingsRuntimeProfileDTO{{
			ProfileID:        "runtime-pi-local",
			DisplayName:      "PI",
			RuntimeKind:      "pi",
			RuntimeConfigRef: "runtime-pi-local",
			RuntimeConfig: map[string]any{
				"executable": "pi",
			},
			Enabled: true,
		}},
	}); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	spec, err := settings.ResolveRuntimeSpec(ctx, agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi, Config: json.RawMessage(`null`)})
	if err != nil {
		t.Fatalf("ResolveRuntimeSpec: %v", err)
	}
	if spec.ConfigRef != "runtime-pi-local" {
		t.Fatalf("config ref = %q", spec.ConfigRef)
	}
	var config map[string]any
	if err := json.Unmarshal(spec.Config, &config); err != nil {
		t.Fatalf("decode runtime config: %v", err)
	}
	if config["executable"] != "pi" {
		t.Fatalf("runtime config = %+v", config)
	}
}

func TestProductionSettingsSavePartialSettingsDoesNotForcePageIndexDefaults(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	settings := productionSettingsStore{router: env.DBRouter}

	if _, err := settings.SaveSettings(ctx, httptransport.SettingsDTO{
		LLMProfiles: []httptransport.SettingsLLMProfileDTO{{
			ProfileID:     "embedding",
			DisplayName:   "Embedding",
			Provider:      "openai",
			BaseURL:       "http://127.0.0.1:1/v1",
			APIKeyRef:     "secret://llm/embedding",
			Model:         "test-embedding",
			Enabled:       true,
			GlobalDefault: true,
		}},
	}); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	got, err := settings.GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got.Knowledge.Enabled || got.Knowledge.DefaultProvider != "" || got.Knowledge.PageIndex.APIURL != "" {
		t.Fatalf("knowledge settings = %+v, want partial save to stay unset for local fallback", got.Knowledge)
	}
}

func TestProductionRuntimeToolDispatcherRoutesSkillTools(t *testing.T) {
	ctx := context.Background()
	skills := &serverTestSkillExecutor{}
	dispatcher := productionRuntimeToolDispatcher{skills: skills}

	result, err := dispatcher.CallRuntimeTool(ctx, runtimecore.RuntimeToolCallRequest{
		Name: string(toolstd.SkillLoad),
		Input: map[string]any{
			"skill_id": "skill.tdd",
		},
		SkillBindings: []agentcore.RuntimeSkillBinding{{
			SkillID: "skill.tdd",
			Alias:   "tdd",
		}},
	})
	if err != nil {
		t.Fatalf("CallRuntimeTool(load_skill): %v", err)
	}
	if skills.toolID != string(toolstd.SkillLoad) ||
		skills.request.SkillID != "skill.tdd" ||
		len(skills.request.SkillBindings) != 1 {
		t.Fatalf("skill executor request = tool:%q req:%+v", skills.toolID, skills.request)
	}
	if result.Output["skill_id"] != "skill.tdd" {
		t.Fatalf("result = %+v", result.Output)
	}
}

func TestProductionRuntimeToolDispatcherReturnsRecoverableToolFailure(t *testing.T) {
	dispatcher := productionRuntimeToolDispatcher{}

	result, err := dispatcher.CallRuntimeTool(context.Background(), runtimecore.RuntimeToolCallRequest{
		Name:   string(toolstd.MissionCoordinatorPlanParallel),
		CallID: "plan-1",
		Input:  map[string]any{"worker_lanes": []any{}},
	})
	if err != nil {
		t.Fatalf("tool parameter failure should be returned to the Agent, got transport error: %v", err)
	}
	if result.Output["status"] != "failed" || result.Output["tool"] != string(toolstd.MissionCoordinatorPlanParallel) || result.Output["tool_call_id"] != "plan-1" {
		t.Fatalf("failure result = %+v", result.Output)
	}
	if !strings.Contains(fmt.Sprint(result.Output["error"]), "discussion_id") || result.Output["retryable"] != true {
		t.Fatalf("failure should include an actionable reason and retry signal: %+v", result.Output)
	}
	arguments, ok := result.Output["arguments"].(map[string]any)
	if !ok || len(arguments["worker_lanes"].([]any)) != 0 {
		t.Fatalf("failure should preserve the rejected arguments: %+v", result.Output)
	}
}

func TestProductionRuntimeToolDispatcherRecoversToolPanic(t *testing.T) {
	dispatcher := productionRuntimeToolDispatcher{skills: panickingProductionSkillExecutor{}}
	result, err := dispatcher.CallRuntimeTool(context.Background(), runtimecore.RuntimeToolCallRequest{
		Name:   string(toolstd.SkillLoad),
		CallID: "skill-panic-1",
		Input:  map[string]any{"skill_id": "skill.tdd"},
	})
	if err != nil {
		t.Fatalf("tool panic should be returned to the Agent, got transport error: %v", err)
	}
	if result.Output["status"] != "failed" || result.Output["error_code"] != "tool_execution_panic" || !strings.Contains(fmt.Sprint(result.Output["error"]), "test tool panic") {
		t.Fatalf("panic failure result = %+v", result.Output)
	}
}

type panickingProductionSkillExecutor struct{}

func (panickingProductionSkillExecutor) ExecuteSkillTool(context.Context, string, skillres.RuntimeSkillRequest) (skillres.RuntimeToolResult, error) {
	panic("test tool panic")
}

func TestProductionRuntimeToolDispatcherRoutesPersonalAssistantShellWithoutDiscussionID(t *testing.T) {
	ctx := context.Background()
	shell := &productionRuntimeTestPersonalAssistantShellExecutor{}
	dispatcher := productionRuntimeToolDispatcher{personalShell: shell}

	result, err := dispatcher.CallRuntimeTool(ctx, runtimecore.RuntimeToolCallRequest{
		Name:    string(toolstd.PersonalAssistantShellExec),
		AgentID: "personal-assistant",
		ExecutionPrincipal: agentcore.ExecutionPrincipal{
			Kind: agentcore.ExecutionPrincipalUser, ID: "user-1",
			PermissionScope: []string{toolstd.PermissionPersonalAssistantShell},
		},
		WorkspaceRoot:    filepath.Clean("D:/workspace"),
		WorkingDirectory: filepath.Clean("D:/workspace/current"),
		Input: map[string]any{
			"command": "pwd",
			"cwd":     ".",
		},
	})
	if err != nil {
		t.Fatalf("CallRuntimeTool(personal shell): %v", err)
	}
	if shell.toolID != string(toolstd.PersonalAssistantShellExec) ||
		shell.request.AgentID != "personal-assistant" ||
		shell.request.WorkspaceRoot != filepath.Clean("D:/workspace") ||
		shell.request.WorkingDirectory != filepath.Clean("D:/workspace/current") ||
		shell.request.Command != "pwd" ||
		shell.request.Cwd != "." ||
		shell.request.MaximumTimeoutMs != 0 {
		t.Fatalf("shell executor request = tool:%q req:%+v", shell.toolID, shell.request)
	}
	if result.Output["stdout"] != "shell ok" {
		t.Fatalf("result = %+v", result.Output)
	}
}

func TestProductionRuntimeToolDispatcherRejectsUserWithoutPersonalAssistantShellPermission(t *testing.T) {
	shell := &productionRuntimeTestPersonalAssistantShellExecutor{}
	dispatcher := productionRuntimeToolDispatcher{personalShell: shell}

	result, err := dispatcher.CallRuntimeTool(context.Background(), runtimecore.RuntimeToolCallRequest{
		Name:    string(toolstd.PersonalAssistantShellExec),
		AgentID: "personal-assistant",
		ExecutionPrincipal: agentcore.ExecutionPrincipal{
			Kind: agentcore.ExecutionPrincipalUser, ID: "user-1",
			PermissionScope: []string{"mission.read"},
		},
		Input: map[string]any{"command": "pwd"},
	})
	if err != nil {
		t.Fatalf("CallRuntimeTool(personal shell): %v", err)
	}
	if shell.toolID != "" || result.Output["status"] != "failed" {
		t.Fatalf("shell should be denied before execution: tool=%q result=%+v", shell.toolID, result.Output)
	}
}

func TestProductionRuntimeToolDispatcherUsesScheduledTaskTimeoutForNativeShell(t *testing.T) {
	shell := &productionRuntimeTestPersonalAssistantShellExecutor{}
	dispatcher := productionRuntimeToolDispatcher{personalShell: shell}

	_, err := dispatcher.CallRuntimeTool(context.Background(), runtimecore.RuntimeToolCallRequest{
		Name:    string(toolstd.PersonalAssistantShellExec),
		AgentID: "tangdou",
		Input: map[string]any{
			"command":    "python3 scanner.py",
			"timeout_ms": 120000,
		},
		ProviderSpecific: map[string]string{
			runtimecore.ToolCallTimeoutSecondsProviderKey: "1200",
		},
	})
	if err != nil {
		t.Fatalf("CallRuntimeTool(personal shell): %v", err)
	}
	if shell.request.TimeoutMs != 1200000 || shell.request.MaximumTimeoutMs != 1200000 {
		t.Fatalf("scheduled shell timeout = %d max = %d, want 1200000", shell.request.TimeoutMs, shell.request.MaximumTimeoutMs)
	}
}

func TestProductionRuntimeToolDispatcherRoutesRunTerminalAliasToPersonalAssistantShell(t *testing.T) {
	ctx := context.Background()
	shell := &productionRuntimeTestPersonalAssistantShellExecutor{}
	dispatcher := productionRuntimeToolDispatcher{personalShell: shell}

	_, err := dispatcher.CallRuntimeTool(ctx, runtimecore.RuntimeToolCallRequest{
		Name:    "run_terminal",
		AgentID: "personal-assistant",
		Input: map[string]any{
			"command": "pwd",
		},
	})
	if err != nil {
		t.Fatalf("CallRuntimeTool(run_terminal): %v", err)
	}
	if shell.toolID != "run_terminal" || shell.request.Command != "pwd" {
		t.Fatalf("shell executor request = tool:%q req:%+v", shell.toolID, shell.request)
	}
}

func TestProductionRuntimeToolDispatcherRoutesEvaluationReviewThroughMCPGateway(t *testing.T) {
	ctx := context.Background()
	gateway := &productionRuntimeTestMCPGateway{}
	dispatcher := productionRuntimeToolDispatcher{mcpGateway: gateway}

	result, err := dispatcher.CallRuntimeTool(ctx, runtimecore.RuntimeToolCallRequest{
		TenantID: "tenant-review", Name: string(toolstd.EvaluationReview), AgentID: "review-agent",
		Input: map[string]any{
			"mode":                 "agent",
			"agent_id":             "profile-agent-1",
			"summary":              "结论：档案 Agent 交付稳定。",
			"estimated_human_days": 1.5,
		},
		ToolBindings: []agentcore.RuntimeToolBinding{{
			ToolID:             toolstd.EvaluationReview,
			Alias:              "evaluation_review",
			GrantedPermissions: []string{toolstd.PermissionEvaluationReview},
		}},
	})
	if err != nil {
		t.Fatalf("CallRuntimeTool(evaluation_review): %v", err)
	}
	if gateway.request.TenantID != "tenant-review" ||
		gateway.request.ToolID != string(toolstd.EvaluationReview) ||
		gateway.request.AgentID != "review-agent" ||
		gateway.request.PrincipalID != "review-agent" ||
		gateway.request.Arguments["agent_id"] != "profile-agent-1" {
		t.Fatalf("mcp gateway request = %+v", gateway.request)
	}
	if len(gateway.request.Permissions) != 1 || gateway.request.Permissions[0] != toolstd.PermissionEvaluationReview {
		t.Fatalf("permissions = %+v", gateway.request.Permissions)
	}
	if result.Output["ok"] != true {
		t.Fatalf("result = %+v", result.Output)
	}
}

func TestProductionRuntimeToolDispatcherRoutesEmailSendThroughMCPGateway(t *testing.T) {
	gateway := &productionRuntimeTestMCPGateway{}
	dispatcher := productionRuntimeToolDispatcher{mcpGateway: gateway}

	result, err := dispatcher.CallRuntimeTool(context.Background(), runtimecore.RuntimeToolCallRequest{
		TenantID: "org-1", Name: string(toolstd.EmailSend), AgentID: "mail-agent",
		Input: map[string]any{"to": []any{"user@example.test"}, "subject": "Status", "text": "Body"},
		ToolBindings: []agentcore.RuntimeToolBinding{{
			ToolID: toolstd.EmailSend, Alias: "email_send", GrantedPermissions: []string{toolstd.PermissionEmailSend},
		}},
	})
	if err != nil {
		t.Fatalf("CallRuntimeTool(email_send): %v", err)
	}
	if gateway.request.TenantID != "org-1" || gateway.request.ToolID != string(toolstd.EmailSend) || gateway.request.AgentID != "mail-agent" || gateway.request.PrincipalID != "mail-agent" || gateway.request.Arguments["subject"] != "Status" {
		t.Fatalf("mcp gateway request = %+v", gateway.request)
	}
	if len(gateway.request.Permissions) != 1 || gateway.request.Permissions[0] != toolstd.PermissionEmailSend || result.Output["ok"] != true {
		t.Fatalf("request=%+v result=%+v", gateway.request, result.Output)
	}
}

func TestProductionRuntimeToolDispatcherRoutesMemoryDiaryThroughMCPGateway(t *testing.T) {
	ctx := context.Background()
	gateway := &productionRuntimeTestMCPGateway{}
	dispatcher := productionRuntimeToolDispatcher{mcpGateway: gateway}

	result, err := dispatcher.CallRuntimeTool(ctx, runtimecore.RuntimeToolCallRequest{
		Name:    string(toolstd.MemoryDiaryWrite),
		AgentID: "diary-agent",
		Input: map[string]any{
			"mode":     "agent",
			"agent_id": "profile-agent-1",
			"date":     "2026-06-16",
		},
		ToolBindings: []agentcore.RuntimeToolBinding{{
			ToolID:             toolstd.MemoryDiaryWrite,
			Alias:              "memory_diary_write",
			GrantedPermissions: []string{toolstd.PermissionMemoryDiaryWrite},
		}},
	})
	if err != nil {
		t.Fatalf("CallRuntimeTool(memory_diary_write): %v", err)
	}
	if gateway.request.ToolID != string(toolstd.MemoryDiaryWrite) ||
		gateway.request.AgentID != "diary-agent" ||
		gateway.request.PrincipalID != "diary-agent" ||
		gateway.request.Arguments["agent_id"] != "profile-agent-1" {
		t.Fatalf("mcp gateway request = %+v", gateway.request)
	}
	if len(gateway.request.Permissions) != 1 || gateway.request.Permissions[0] != toolstd.PermissionMemoryDiaryWrite {
		t.Fatalf("permissions = %+v", gateway.request.Permissions)
	}
	if result.Output["ok"] != true {
		t.Fatalf("result = %+v", result.Output)
	}
}

func TestProductionRuntimeToolDispatcherLocksDiaryAppendToHostMetadata(t *testing.T) {
	ctx := context.Background()
	gateway := &productionRuntimeTestMCPGateway{}
	dispatcher := productionRuntimeToolDispatcher{mcpGateway: gateway}

	_, err := dispatcher.CallRuntimeTool(ctx, runtimecore.RuntimeToolCallRequest{
		TenantID: "tenant-a",
		Name:     string(toolstd.MemoryDiaryAppend),
		AgentID:  "agent-1",
		Input: map[string]any{
			"path":             "memory/attacker.md",
			"date":             "1999-01-01",
			"content":          "- Durable result.",
			"session_id":       "attacker-session",
			"context_revision": "attacker-revision",
		},
		ProviderSpecific: map[string]string{
			"diary_maintenance":        "true",
			"diary_target_path":        "memory/2026-07-17.md",
			"diary_target_date":        "2026-07-17",
			"diary_session_id":         "session-1",
			"diary_context_revision":   "revision-1",
			"diary_trigger":            "run_terminal",
			"diary_idempotency_key":    "append-1",
			"diary_source_sensitivity": "secret",
			"diary_evidence_refs":      `["evidence:test-1"]`,
			"diary_lineage_refs":       `["memory:digest-1","run:run-1"]`,
		},
		ToolBindings: []agentcore.RuntimeToolBinding{{
			ToolID: toolstd.MemoryDiaryAppend, GrantedPermissions: []string{toolstd.PermissionMemoryDiaryWrite},
		}},
	})
	if err != nil {
		t.Fatalf("CallRuntimeTool(memory_diary_append): %v", err)
	}
	if gateway.request.TenantID != "tenant-a" || !gateway.request.Maintenance || gateway.request.AgentID != "agent-1" {
		t.Fatalf("gateway identity = %+v", gateway.request)
	}
	args := gateway.request.Arguments
	if args["path"] != "memory/2026-07-17.md" || args["date"] != "2026-07-17" ||
		args["session_id"] != "session-1" || args["context_revision"] != "revision-1" ||
		args["trigger"] != "run_terminal" || args["idempotency_key"] != "append-1" {
		t.Fatalf("host metadata was not enforced: %+v", args)
	}
	if args["content"] != "- Durable result." {
		t.Fatalf("model content was lost: %+v", args)
	}
	if args["source_sensitivity"] != "secret" || !reflect.DeepEqual(args["evidence_refs"], []any{"evidence:test-1"}) ||
		!reflect.DeepEqual(args["lineage_refs"], []any{"memory:digest-1", "run:run-1"}) {
		t.Fatalf("host provenance was not enforced: %+v", args)
	}
}

func TestProductionRuntimeToolDispatcherRoutesKnowledgeAddThroughMCPGateway(t *testing.T) {
	ctx := context.Background()
	gateway := &productionRuntimeTestMCPGateway{}
	dispatcher := productionRuntimeToolDispatcher{mcpGateway: gateway}

	result, err := dispatcher.CallRuntimeTool(ctx, runtimecore.RuntimeToolCallRequest{
		TenantID: "tenant-knowledge", Name: string(toolstd.KnowledgeAdd), AgentID: "knowledge-agent",
		Input: map[string]any{
			"title":   "Release note",
			"content": "Ship knowledge add.",
		},
		ToolBindings: []agentcore.RuntimeToolBinding{{
			ToolID:             toolstd.KnowledgeAdd,
			Alias:              "knowledge_add",
			GrantedPermissions: []string{toolstd.PermissionKnowledgeWrite},
		}},
	})
	if err != nil {
		t.Fatalf("CallRuntimeTool(knowledge_add): %v", err)
	}
	if gateway.request.TenantID != "tenant-knowledge" ||
		gateway.request.ToolID != string(toolstd.KnowledgeAdd) ||
		gateway.request.AgentID != "knowledge-agent" ||
		gateway.request.PrincipalID != "knowledge-agent" ||
		gateway.request.Arguments["title"] != "Release note" ||
		gateway.request.Arguments["content"] != "Ship knowledge add." {
		t.Fatalf("mcp gateway request = %+v", gateway.request)
	}
	if len(gateway.request.Permissions) != 1 || gateway.request.Permissions[0] != toolstd.PermissionKnowledgeWrite {
		t.Fatalf("permissions = %+v", gateway.request.Permissions)
	}
	if result.Output["ok"] != true {
		t.Fatalf("result = %+v", result.Output)
	}
}
