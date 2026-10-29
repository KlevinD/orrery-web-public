package main

import (
	"context"
	"encoding/json"
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

func TestProductionAgentCoreApprovalRecoveryUsesFreshBrokerAndGateway(t *testing.T) {
	for _, mode := range []string{"single", "mixed-approved", "mixed-denied"} {
		t.Run(mode, func(t *testing.T) {
			batch := mode != "single"
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + filepath.Join(t.TempDir(), "runtime.db")})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			journal, _ := agentkernel.NewSQLToolJournal(store)
			checkpoints, _ := agentkernel.NewSQLCheckpointStore(store)
			scope := &agentcore.ResponsibilityContext{ContextID: "context-scoped", ResponsibilityID: "responsibility-scoped", ResponsibilityVersion: 1, PolicyID: "policy-scoped", PolicyVersion: 1, AgentProfileID: "profile-scoped", MissionID: "mission-scoped", TaskID: "task-scoped", RequireExplicitToolScopes: true, AllowedTools: []string{string(toolstd.KnowledgeAdd)}, PolicyToolScopes: []string{toolstd.PermissionKnowledgeWrite}, TaskPermissionScopes: []string{toolstd.PermissionKnowledgeWrite}}
			if batch {
				scope.AllowedTools = append(scope.AllowedTools, string(toolstd.KnowledgeSearch))
				scope.PolicyToolScopes = append(scope.PolicyToolScopes, toolstd.PermissionKnowledgeRead)
				scope.TaskPermissionScopes = append(scope.TaskPermissionScopes, toolstd.PermissionKnowledgeRead)
			}
			catalog := toolres.NewMemoryRegistry()
			for _, definition := range toolstd.Definitions(time.Now()) {
				if err := catalog.Register(ctx, definition); err != nil {
					t.Fatal(err)
				}
			}
			knowledge := &coreCompatKnowledge{t: t}
			gateway := mcptransport.NewGateway(mcptransport.GatewayOptions{Registry: catalog, Profiles: coreCompatProfiles{write: true}, Router: mcptransport.NewToolRouter(mcptransport.ToolRouterOptions{Registry: catalog, KnowledgeExecutor: knowledge}), ResponsibilityAuthority: coreCompatAuthority{context: scope}})
			var models atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Messages []struct {
						Role       string `json:"role"`
						ToolCallID string `json:"tool_call_id"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error("invalid model request")
				}
				message := map[string]any{"role": "assistant", "content": "done"}
				finish := "stop"
				if models.Add(1) == 1 {
					message["content"], finish = "", "tool_calls"
					message["tool_calls"] = []any{map[string]any{"id": "recover-write", "type": "function", "function": map[string]any{"name": "knowledge_add", "arguments": `{"content":"synthetic note","knowledge_base_id":"kb-writable"}`}}}
					if batch {
						message["tool_calls"] = append([]any{map[string]any{"id": "recover-read", "type": "function", "function": map[string]any{"name": "knowledge_search", "arguments": `{"query":"synthetic query"}`}}}, message["tool_calls"].([]any)...)
						message["tool_calls"] = append(message["tool_calls"].([]any), map[string]any{"id": "recover-write-two", "type": "function", "function": map[string]any{"name": "knowledge_add", "arguments": `{"content":"second synthetic note","knowledge_base_id":"kb-writable"}`}})
					}
				} else if batch {
					if len(request.Messages) < 3 {
						t.Error("recovered batch lost tool results")
					} else {
						for i, id := range []string{"recover-read", "recover-write", "recover-write-two"} {
							item := request.Messages[len(request.Messages)-3+i]
							if item.Role != "tool" || item.ToolCallID != id {
								t.Error("recovered result order changed")
							}
						}
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message, "finish_reason": finish}}})
			}))
			defer server.Close()
			deps := productionDiscussionAgentDependencies{KernelJournal: journal, KernelCheckpoints: checkpoints, ToolDispatcher: productionRuntimeToolDispatcher{mcpGateway: gateway}, LLMProfiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{ProfileID: "test", Provider: "openai", BaseURL: server.URL + "/v1", Model: "test", APIKey: "test", Enabled: true, GlobalDefault: true}}}}
			requested, release, oldDone := make(chan struct{}, 1), make(chan struct{}), make(chan struct{})
			var originals []runtimecore.ApprovalRequest
			deps.ApprovalBroker = runtimecore.ApprovalBrokerFunc(func(ctx context.Context, request runtimecore.ApprovalRequest, respond runtimecore.ApprovalResponder) error {
				originals = append(originals, request)
				if batch && len(originals) == 1 {
					return respond(runtimecore.ApprovalResponse{Decision: runtimecore.ApprovalDecisionApproved})
				}
				requested <- struct{}{}
				select {
				case <-release:
					return respond(runtimecore.ApprovalResponse{Decision: runtimecore.ApprovalDecisionApproved})
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			first := newProductionRuntimeRegistry(deps)
			defer first.Close()
			runtime, err := first.ResolveRuntime(ctx, agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged})
			if err != nil {
				t.Fatal(err)
			}
			req := agentcore.RuntimeRequest{TenantID: "tenant-scoped", AgentID: "agent-scoped", RunID: "run-scoped", MissionID: "mission-scoped", TaskID: "task-scoped", ExecutionPrincipal: agentcore.ExecutionPrincipal{Kind: agentcore.ExecutionPrincipalAgent, ID: "agent-scoped", TenantID: "tenant-scoped"}, ContextPack: agentcore.ContextPack{AgentID: "agent-scoped", MissionID: "mission-scoped", TaskID: "task-scoped", ResponsibilityContext: scope}, InputMessages: []agentcore.RuntimeChatMessage{{Role: "user", Content: "Save the authorized note"}}, RuntimeOptions: agentcore.RuntimeOptions{WorkingDirectory: t.TempDir(), ProviderSpecific: map[string]string{"agentcore_task_mode": "long", "agentcore_task_recovery": "automatic"}}, ToolBindings: []agentcore.RuntimeToolBinding{{ToolID: toolstd.KnowledgeAdd, GrantedPermissions: []string{toolstd.PermissionKnowledgeWrite}, InputSchema: map[string]any{"type": "object", "properties": map[string]any{"content": map[string]any{"type": "string"}, "knowledge_base_id": map[string]any{"type": "string"}}, "required": []any{"content"}, "additionalProperties": false}}}}
			if batch {
				req.ToolBindings = append(req.ToolBindings, agentcore.RuntimeToolBinding{ToolID: toolstd.KnowledgeSearch, GrantedPermissions: []string{toolstd.PermissionKnowledgeRead}, InputSchema: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}, "required": []any{"query"}, "additionalProperties": false}})
			}
			go func() {
				defer close(oldDone)
				response, runErr := runtime.RunSession(ctx, req, nil)
				if runErr == nil && response.Status == agentcore.RuntimeStatusCompleted {
					t.Error("fenced old registry completed a write")
				}
			}()
			select {
			case <-requested:
			case <-ctx.Done():
				t.Fatal("first approval unavailable")
			}
			var approvals atomic.Int32
			deps.ApprovalBroker = runtimecore.ApprovalBrokerFunc(func(_ context.Context, request runtimecore.ApprovalRequest, respond runtimecore.ApprovalResponder) error {
				count := int(approvals.Add(1))
				if count > len(originals) {
					t.Error("unexpected approval after recovery")
					return respond(runtimecore.ApprovalResponse{Decision: runtimecore.ApprovalDecisionDenied})
				}
				if string(request.Payload) != string(originals[count-1].Payload) || models.Load() != 1 || knowledge.calls.Load() != 0 {
					t.Error("recovery reused approval or regenerated/dispatched the proposal")
				}
				for _, original := range originals {
					if request.RequestID == original.RequestID {
						t.Error("recovery reused an old approval identity")
					}
				}
				if mode == "mixed-denied" && count == 2 {
					return respond(runtimecore.ApprovalResponse{Decision: runtimecore.ApprovalDecisionDenied})
				}
				return respond(runtimecore.ApprovalResponse{Decision: runtimecore.ApprovalDecisionApproved})
			})
			second := newProductionRuntimeRegistry(deps)
			defer second.Close()
			recovered, err := second.ResolveRuntime(ctx, agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged})
			if err != nil {
				t.Fatal(err)
			}
			response, runErr := recovered.RunSession(ctx, req, nil)
			close(release)
			select {
			case <-oldDone:
			case <-ctx.Done():
				t.Fatal("old registry did not terminate")
			}
			wantCalls, wantApprovals := int32(1), int32(1)
			if batch {
				wantCalls, wantApprovals = 3, 2
			}
			if mode == "mixed-denied" {
				if response.Status == agentcore.RuntimeStatusCompleted || knowledge.calls.Load() != 0 || models.Load() != 1 || approvals.Load() != 2 {
					t.Fatalf("denied recovery tools=%d models=%d approvals=%d status=%s err=%v", knowledge.calls.Load(), models.Load(), approvals.Load(), response.Status, runErr)
				}
				return
			}
			if runErr != nil || response.Status != agentcore.RuntimeStatusCompleted || knowledge.calls.Load() != wantCalls || models.Load() != 2 || approvals.Load() != wantApprovals {
				t.Fatalf("recovery tools=%d models=%d approvals=%d status=%s err=%v", knowledge.calls.Load(), models.Load(), approvals.Load(), response.Status, runErr)
			}
		})
	}
}
