package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/agentkernel"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/llmprofile"
	"orrery_v2/internal/platform/db"
	toolres "orrery_v2/internal/resource/tool"
	toolstd "orrery_v2/internal/resource/tool/std"
	mcptransport "orrery_v2/internal/transport/mcp"
)

type coreCompatAuthority struct {
	context *agentcore.ResponsibilityContext
}

type coreCompatProfiles struct{ write bool }

func (p coreCompatProfiles) GetProfile(_ context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	if id != "profile-scoped" {
		return agentcore.AgentProfile{}, agentcore.ErrNotFound
	}
	profile := agentcore.AgentProfile{ID: id, Enabled: true, ToolRefs: []agentcore.ToolRef{{ToolID: toolstd.KnowledgeSearch, PermissionScope: toolstd.PermissionKnowledgeRead}}}
	if p.write {
		profile.ToolRefs = append(profile.ToolRefs, agentcore.ToolRef{ToolID: toolstd.KnowledgeAdd, PermissionScope: toolstd.PermissionKnowledgeWrite})
	}
	return profile, nil
}
func (coreCompatProfiles) QueryDirectory(context.Context, agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	return agentcore.AgentDirectoryPage{}, nil
}

func (a coreCompatAuthority) ResolveResponsibilityExecution(_ context.Context, run string) (*mcptransport.ResponsibilityExecutionAuthority, error) {
	return &mcptransport.ResponsibilityExecutionAuthority{RunID: run, TenantID: "tenant-scoped", AgentID: "agent-scoped", AgentProfileID: "profile-scoped", Context: agentcore.CloneResponsibilityContext(a.context)}, nil
}

type coreCompatKnowledge struct {
	calls atomic.Int32
	t     *testing.T
}

func (k *coreCompatKnowledge) ExecuteKnowledgeTool(_ context.Context, _ string, req mcptransport.KnowledgeToolRequest) (mcptransport.KnowledgeToolResult, error) {
	k.calls.Add(1)
	if req.TenantID != "tenant-scoped" || req.AgentID != "agent-scoped" || req.PrincipalID != "agent-scoped" {
		k.t.Error("gateway execution identity changed")
	}
	return mcptransport.KnowledgeToolResult{Output: map[string]any{"items": []any{}, "status": "completed"}}, nil
}

type coreCompatUnrecorded struct{}

func (coreCompatUnrecorded) StartToolCall(context.Context, mcptransport.ToolCallLifecycleStart) (mcptransport.ToolCallAttempt, error) {
	return coreCompatUnrecorded{}, nil
}
func (coreCompatUnrecorded) CompleteToolCall(context.Context, mcptransport.CallToolResult) error {
	return errors.New("injected receipt persistence failure")
}
func (coreCompatUnrecorded) FailToolCall(context.Context, mcptransport.ToolCallFailure) error {
	return nil
}

func TestProductionAgentCoreScopedTasksUseAuthoritativeGateway(t *testing.T) {
	for _, mode := range []string{"parallel-allowed", "task-denied", "server-revoked", "unrecorded"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + filepath.Join(t.TempDir(), "runtime.db")})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			journal, _ := agentkernel.NewSQLToolJournal(store)
			checkpoints, _ := agentkernel.NewSQLCheckpointStore(store)
			scope := &agentcore.ResponsibilityContext{ContextID: "context-scoped", ResponsibilityID: "responsibility-scoped", ResponsibilityVersion: 1, PolicyID: "policy-scoped", PolicyVersion: 1, AgentProfileID: "profile-scoped", MissionID: "mission-scoped", TaskID: "task-scoped", RequireExplicitToolScopes: true, AllowedTools: []string{string(toolstd.KnowledgeSearch)}, PolicyToolScopes: []string{toolstd.PermissionKnowledgeRead}, TaskPermissionScopes: []string{toolstd.PermissionKnowledgeRead}}
			if mode == "task-denied" {
				scope.TaskPermissionScopes = []string{toolstd.PermissionMemoryRead}
			}
			authority := agentcore.CloneResponsibilityContext(scope)
			if mode == "server-revoked" {
				authority.TaskPermissionScopes = []string{toolstd.PermissionMemoryRead}
			}
			catalog := toolres.NewMemoryRegistry()
			for _, definition := range toolstd.Definitions(time.Now()) {
				if err := catalog.Register(ctx, definition); err != nil {
					t.Fatal(err)
				}
			}
			knowledge := &coreCompatKnowledge{t: t}
			gatewayOptions := mcptransport.GatewayOptions{Registry: catalog, Profiles: coreCompatProfiles{}, Router: mcptransport.NewToolRouter(mcptransport.ToolRouterOptions{Registry: catalog, KnowledgeExecutor: knowledge}), ResponsibilityAuthority: coreCompatAuthority{context: authority}}
			if mode == "unrecorded" {
				gatewayOptions.Lifecycle = coreCompatUnrecorded{}
			}
			gateway := mcptransport.NewGateway(gatewayOptions)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count := requests.Add(1)
				message := map[string]any{"role": "assistant", "content": "done"}
				finish := "stop"
				if count == 1 {
					calls := []any{map[string]any{"id": "call-1", "type": "function", "function": map[string]any{"name": "knowledge_search", "arguments": `{"query":"synthetic evidence"}`}}}
					if mode == "parallel-allowed" {
						calls = append(calls, map[string]any{"id": "call-2", "type": "function", "function": map[string]any{"name": "knowledge_search", "arguments": `{"query":"synthetic second evidence"}`}})
					}
					message["content"], message["tool_calls"], finish = "", calls, "tool_calls"
				} else {
					var payload struct {
						Messages []struct {
							Role string `json:"role"`
							ID   string `json:"tool_call_id"`
						} `json:"messages"`
					}
					if json.NewDecoder(r.Body).Decode(&payload) != nil {
						t.Error("invalid follow-up model request")
					}
					if mode == "parallel-allowed" {
						n := len(payload.Messages)
						if n < 2 || payload.Messages[n-2].ID != "call-1" || payload.Messages[n-1].ID != "call-2" {
							t.Error("canonical gateway batch lost result order")
						}
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message, "finish_reason": finish}}})
			}))
			defer server.Close()
			registry := newProductionRuntimeRegistry(productionDiscussionAgentDependencies{KernelJournal: journal, KernelCheckpoints: checkpoints, ToolDispatcher: productionRuntimeToolDispatcher{mcpGateway: gateway}, LLMProfiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{ProfileID: "test", Provider: "openai", BaseURL: server.URL + "/v1", Model: "test", APIKey: "test", Enabled: true, GlobalDefault: true}}}})
			defer registry.Close()
			runtime, err := registry.ResolveRuntime(ctx, agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged})
			if err != nil {
				t.Fatal(err)
			}
			req := agentcore.RuntimeRequest{
				TenantID: "tenant-scoped", AgentID: "agent-scoped", RunID: "run-scoped", MissionID: "mission-scoped", TaskID: "task-scoped",
				ExecutionPrincipal: agentcore.ExecutionPrincipal{Kind: agentcore.ExecutionPrincipalAgent, ID: "agent-scoped", TenantID: "tenant-scoped"},
				ContextPack:        agentcore.ContextPack{AgentID: "agent-scoped", MissionID: "mission-scoped", TaskID: "task-scoped", ResponsibilityContext: scope},
				InputMessages:      []agentcore.RuntimeChatMessage{{Role: "user", Content: "Read authorized sources"}},
				ToolBindings: []agentcore.RuntimeToolBinding{{
					ToolID: toolstd.KnowledgeSearch, GrantedPermissions: []string{toolstd.PermissionKnowledgeRead},
					InputSchema: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}, "required": []any{"query"}, "additionalProperties": false},
				}},
				RuntimeOptions: agentcore.RuntimeOptions{WorkingDirectory: t.TempDir()},
			}
			response, err := runtime.RunSession(ctx, req, func(_ context.Context, event agentcore.SessionEnvelope) error { return event.Validate() })
			if err != nil {
				t.Fatal(err)
			}
			wantCalls, wantModels, wantStatus := int32(0), int32(1), agentcore.RuntimeStatusFailed
			if mode == "parallel-allowed" {
				wantCalls, wantModels, wantStatus = 2, 2, agentcore.RuntimeStatusCompleted
			}
			if mode == "server-revoked" {
				wantModels, wantStatus = 2, agentcore.RuntimeStatusCompleted
			}
			if mode == "unrecorded" {
				wantCalls = 1
			}
			if knowledge.calls.Load() != wantCalls || requests.Load() != wantModels || response.Status != wantStatus {
				t.Fatalf("mode=%s calls=%d models=%d status=%s", mode, knowledge.calls.Load(), requests.Load(), response.Status)
			}
			if mode == "unrecorded" {
				record, found, err := journal.Get(ctx, agentkernel.ToolExecutionKey{TenantID: req.TenantID, RunID: req.RunID, CallID: "call-1"})
				if err != nil || !found || record.State != agentkernel.ToolExecutionUnknown {
					t.Fatalf("gateway receipt failure lost: %+v %v", record, err)
				}
			}
		})
	}
}

func TestProductionAgentCoreReadToolCompatibilityAndParallelAllowlist(t *testing.T) {
	for _, id := range []agentcore.ToolID{toolstd.SessionSearch, toolstd.SessionRead, toolstd.ScheduledTaskList} {
		permission := toolstd.PermissionDiscussionRead
		if id == toolstd.ScheduledTaskList {
			permission = toolstd.PermissionSchedulerRead
		}
		for _, granted := range []bool{false, true} {
			binding := agentcore.RuntimeToolBinding{ToolID: id}
			if granted {
				binding.GrantedPermissions = []string{permission}
			}
			call := runtimecore.RuntimeToolCallRequest{TenantID: "tenant", AgentID: "internal", RunID: "run", Name: string(id), ToolBindings: []agentcore.RuntimeToolBinding{binding}}
			if err := productionAgentCoreToolPolicy(context.Background(), call); (err == nil) != granted {
				t.Fatalf("read grant id=%s granted=%v err=%v", id, granted, err)
			}
			call.AgentID = agentcore.AgentID(defaultWebsiteAdvisorAgentID)
			if err := productionAgentCoreToolPolicy(context.Background(), call); err == nil {
				t.Fatalf("website acquired internal history/scheduler: %s", id)
			}
		}
	}
	ids := productionAgentCoreParallelToolIDs()
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate parallel tool: %s", id)
		}
		seen[id] = true
	}
	for _, denied := range []agentcore.ToolID{toolstd.EmailSend, toolstd.KnowledgeAdd, toolstd.MemoryDiaryAppend, toolstd.PersonalAssistantShellExec} {
		if seen[string(denied)] {
			t.Fatalf("stateful tool enabled for parallelism: %s", denied)
		}
	}
}
