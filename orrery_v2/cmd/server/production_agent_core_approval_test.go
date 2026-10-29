package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/agentkernel"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/llmprofile"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/runtimeapproval"
	"orrery_v2/internal/platform/db"
	toolres "orrery_v2/internal/resource/tool"
	toolstd "orrery_v2/internal/resource/tool/std"
	mcptransport "orrery_v2/internal/transport/mcp"
)

func TestProductionAgentCoreKnowledgeWriteUsesApprovalAndGateway(t *testing.T) {
	for _, mode := range []string{"approved", "denied", "task-denied", "server-revoked", "unrecorded", "website", "broker-failed", "modified", "coordinator-approved", "coordinator-denied"} {
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
			scope := &agentcore.ResponsibilityContext{ContextID: "context-scoped", ResponsibilityID: "responsibility-scoped", ResponsibilityVersion: 1, PolicyID: "policy-scoped", PolicyVersion: 1, AgentProfileID: "profile-scoped", MissionID: "mission-scoped", TaskID: "task-scoped", RequireExplicitToolScopes: true, AllowedTools: []string{string(toolstd.KnowledgeAdd)}, PolicyToolScopes: []string{toolstd.PermissionKnowledgeWrite}, TaskPermissionScopes: []string{toolstd.PermissionKnowledgeWrite}}
			authority := agentcore.CloneResponsibilityContext(scope)
			if mode == "task-denied" {
				scope.TaskPermissionScopes = []string{toolstd.PermissionKnowledgeRead}
			}
			catalog := toolres.NewMemoryRegistry()
			for _, definition := range toolstd.Definitions(time.Now()) {
				if err := catalog.Register(ctx, definition); err != nil {
					t.Fatal(err)
				}
			}
			knowledge := &coreCompatKnowledge{t: t}
			options := mcptransport.GatewayOptions{Registry: catalog, Profiles: coreCompatProfiles{write: true}, Router: mcptransport.NewToolRouter(mcptransport.ToolRouterOptions{Registry: catalog, KnowledgeExecutor: knowledge}), ResponsibilityAuthority: coreCompatAuthority{context: authority}}
			if mode == "unrecorded" {
				options.Lifecycle = coreCompatUnrecorded{}
			}
			gateway := mcptransport.NewGateway(options)
			var models, approvals atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				message := map[string]any{"role": "assistant", "content": "done"}
				finish := "stop"
				if models.Add(1) == 1 {
					message["content"], finish = "", "tool_calls"
					message["tool_calls"] = []any{map[string]any{"id": "write-1", "type": "function", "function": map[string]any{"name": "knowledge_add", "arguments": `{"content":"synthetic note","knowledge_base_id":"kb-writable"}`}}}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message, "finish_reason": finish}}})
			}))
			defer server.Close()
			var repository *discussionsession.Repository
			var coordinator *discussionsession.RuntimeApprovalCoordinator
			if strings.HasPrefix(mode, "coordinator-") {
				registry, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{db.RoleCore: {Driver: db.DriverFile, URL: "file://" + filepath.Join(t.TempDir(), "core.db")}}})
				if err != nil {
					t.Fatal(err)
				}
				defer registry.Close()
				repository = discussionsession.NewRepository(db.NewRouter(registry))
				now := time.Now()
				if err := repository.SaveConversation(ctx, discussionsession.ConversationRecord{TenantID: "tenant-scoped", ConversationID: "approval-conversation", HILMode: discussionsession.DiscussionHILModeManual, Status: "active", Title: "synthetic approval", CreatedBy: "user-scoped", CreatedAt: now, UpdatedAt: now}); err != nil {
					t.Fatal(err)
				}
				policy := runtimeapproval.DefaultPolicy()
				if mode == "coordinator-approved" {
					policy.Tools.Allowlist = append(policy.Tools.Allowlist, runtimeapproval.ToolRule{RuleID: "test-knowledge-write", ToolID: "knowledge_add", Effects: []string{"write"}, Risk: "medium"})
				}
				coordinator = discussionsession.NewRuntimeApprovalCoordinator(discussionsession.RuntimeApprovalCoordinatorOptions{Repository: repository, Decisions: runtimeapproval.NewDecisionService(runtimeapproval.StaticPolicyProvider{Policy: policy}, nil)})
				ctx = discussionsession.ContextWithRuntimeApproval(ctx, discussionsession.RuntimeApprovalContext{ConversationID: "approval-conversation", RunID: "run-scoped", AgentID: "agent-scoped", RuntimeKind: "managed"})
			}
			broker := runtimecore.ApprovalBrokerFunc(func(ctx context.Context, request runtimecore.ApprovalRequest, respond runtimecore.ApprovalResponder) error {
				approvals.Add(1)
				if knowledge.calls.Load() != 0 || request.Source != "managed" || request.ToolName != string(toolstd.KnowledgeAdd) || request.Metadata["approval_tool_use_id"] != "write-1" || !strings.Contains(string(request.Payload), "kb-writable") {
					t.Error("approval was late or lost immutable operation material")
				}
				if coordinator != nil {
					return coordinator.RequestApproval(ctx, request, respond)
				}
				if mode == "broker-failed" {
					return errors.New("injected coordinator failure")
				}
				if mode == "server-revoked" {
					authority.TaskPermissionScopes = []string{toolstd.PermissionKnowledgeRead}
				}
				response := runtimecore.ApprovalResponse{Decision: runtimecore.ApprovalDecisionApproved}
				if mode == "denied" {
					response.Decision = runtimecore.ApprovalDecisionDenied
				}
				if mode == "modified" {
					response.Content = `{"content":"changed after review"}`
				}
				return respond(response)
			})
			registry := newProductionRuntimeRegistry(productionDiscussionAgentDependencies{KernelJournal: journal, KernelCheckpoints: checkpoints, ApprovalBroker: broker, ToolDispatcher: productionRuntimeToolDispatcher{mcpGateway: gateway}, LLMProfiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{ProfileID: "test", Provider: "openai", BaseURL: server.URL + "/v1", Model: "test", APIKey: "test", Enabled: true, GlobalDefault: true}}}})
			defer registry.Close()
			runtime, err := registry.ResolveRuntime(ctx, agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged})
			if err != nil {
				t.Fatal(err)
			}
			req := agentcore.RuntimeRequest{TenantID: "tenant-scoped", AgentID: "agent-scoped", RunID: "run-scoped", MissionID: "mission-scoped", TaskID: "task-scoped", ExecutionPrincipal: agentcore.ExecutionPrincipal{Kind: agentcore.ExecutionPrincipalAgent, ID: "agent-scoped", TenantID: "tenant-scoped"}, ContextPack: agentcore.ContextPack{AgentID: "agent-scoped", MissionID: "mission-scoped", TaskID: "task-scoped", ResponsibilityContext: scope}, InputMessages: []agentcore.RuntimeChatMessage{{Role: "user", Content: "Save the authorized note"}}, RuntimeOptions: agentcore.RuntimeOptions{WorkingDirectory: t.TempDir()}, ToolBindings: []agentcore.RuntimeToolBinding{{ToolID: toolstd.KnowledgeAdd, GrantedPermissions: []string{toolstd.PermissionKnowledgeWrite}, InputSchema: map[string]any{"type": "object", "properties": map[string]any{"content": map[string]any{"type": "string"}, "knowledge_base_id": map[string]any{"type": "string"}}, "required": []any{"content"}, "additionalProperties": false}}}}
			if mode == "website" {
				req.AgentID = agentcore.AgentID(defaultWebsiteAdvisorAgentID)
				req.ContextPack.AgentID = req.AgentID
				req.ExecutionPrincipal.ID = req.AgentID.String()
			}
			var requested, resolved atomic.Int32
			response, runErr := runtime.RunSession(ctx, req, func(_ context.Context, envelope agentcore.SessionEnvelope) error {
				if envelope.Event.Kind == agentcore.EventPermission {
					requested.Add(1)
					encoded, _ := json.Marshal(envelope.Event.Permission)
					if strings.Contains(string(encoded), "synthetic note") || strings.Contains(string(encoded), "kb-writable") {
						t.Error("private material leaked into canonical permission event")
					}
				}
				if envelope.Event.Kind == agentcore.EventPermissionResolved {
					resolved.Add(1)
				}
				return envelope.Validate()
			})
			wantTools, wantModels, wantApprovals := int32(0), int32(1), int32(1)
			wantStatus := agentcore.RuntimeStatusFailed
			switch mode {
			case "approved", "coordinator-approved":
				wantTools, wantModels, wantStatus = 1, 2, agentcore.RuntimeStatusCompleted
			case "unrecorded":
				wantTools = 1
			case "task-denied", "website":
				wantApprovals = 0
			case "server-revoked":
				wantModels, wantStatus = 2, agentcore.RuntimeStatusCompleted
			}
			if mode != "broker-failed" && mode != "modified" && runErr != nil {
				t.Fatal(runErr)
			}
			if knowledge.calls.Load() != wantTools || models.Load() != wantModels || approvals.Load() != wantApprovals || (runErr == nil && response.Status != wantStatus) {
				t.Fatalf("mode=%s tools=%d models=%d approvals=%d status=%s err=%v", mode, knowledge.calls.Load(), models.Load(), approvals.Load(), response.Status, runErr)
			}
			if wantApprovals == 1 && runErr == nil && (requested.Load() != 1 || resolved.Load() != 1) {
				t.Fatal("canonical permission lifecycle did not resolve once")
			}
			if mode == "unrecorded" {
				record, found, err := journal.Get(ctx, agentkernel.ToolExecutionKey{TenantID: req.TenantID, RunID: req.RunID, CallID: "write-1"})
				if err != nil || !found || record.State != agentkernel.ToolExecutionUnknown {
					t.Fatal("approval erased an uncertain write outcome")
				}
			}
			if repository != nil {
				view, err := repository.GetConversation(ctx, "approval-conversation")
				audited := false
				for _, event := range view.Events {
					if event.Metadata["approval_tool_use_id"] == "write-1" {
						audited = event.Status == "approved" && mode == "coordinator-approved" || event.Status == "denied" && mode == "coordinator-denied"
					}
				}
				if err != nil || !audited {
					t.Fatal("coordinator did not persist the actual policy decision")
				}
			}
		})
	}
}
