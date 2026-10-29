package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/llm"
	"orrery_v2/internal/agent/runtime/agentkernel"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/llmprofile"
	"orrery_v2/internal/agent/runtime/providers/profilemodel"
	"orrery_v2/internal/agent/runtime/session"
	"orrery_v2/internal/platform/db"
	toolstd "orrery_v2/internal/resource/tool/std"
)

type agentCoreCountingProfiles struct {
	profiles []llmprofile.LLMProfile
	reads    int
}

func (p *agentCoreCountingProfiles) LLMProfiles(context.Context) ([]llmprofile.LLMProfile, error) {
	p.reads++
	return append([]llmprofile.LLMProfile(nil), p.profiles...), nil
}

func TestProductionAgentCoreModelSelectionUsesOneLiveSettingsRead(t *testing.T) {
	var models []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		models = append(models, body.Model)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": "answer"}}}})
	}))
	defer server.Close()
	p := &agentCoreCountingProfiles{profiles: []llmprofile.LLMProfile{
		{ProfileID: "first", Model: "first-model", BaseURL: server.URL, APIKey: "fixture", Enabled: true, GlobalDefault: true},
		{ProfileID: "second", Model: "second-model", BaseURL: server.URL, APIKey: "fixture", Enabled: true},
		{ProfileID: "disabled", Model: "disabled-model", BaseURL: server.URL, APIKey: "fixture"},
	}}
	m := productionAgentCoreModels{factory: profilemodel.NewProfileModelFactory(profilemodel.ProfileModelFactoryConfig{Profiles: p, Client: server.Client()})}
	for i, selected := range []string{llmprofile.PlatformDefaultProfileID, llmprofile.PlatformDefaultProfileID, "first", "missing", "disabled"} {
		if i == 1 {
			p.profiles[0].GlobalDefault, p.profiles[1].GlobalDefault = false, true
		}
		chat, err := m.ModelForRequest(context.Background(), profilemodel.ModelRequest{LLMProfileID: selected})
		if p.reads != i+1 {
			t.Fatalf("selection used multiple settings snapshots: %d", p.reads)
		}
		if i >= 3 {
			if err == nil {
				t.Fatalf("invalid profile accepted: %s", selected)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := chat.Generate(context.Background(), []*llm.Message{llm.UserMessage("hello")}); err != nil {
			t.Fatal(err)
		}
	}
	if fmt.Sprint(models) != "[first-model second-model first-model]" {
		t.Fatalf("wrong live model selection: %v", models)
	}
}

func TestProductionAgentCoreToolPolicyVerifiesExecutionEnvelope(t *testing.T) {
	for _, scenario := range []string{"delegated-owner", "legacy-no-envelope", "wrong-tenant", "wrong-run", "wrong-agent", "wrong-principal", "wrong-kind", "expired", "tampered", "excluded-tool", "forbidden", "wrong-scope"} {
		t.Run(scenario, func(t *testing.T) {
			binding := agentcore.RuntimeToolBinding{ToolID: toolstd.KnowledgeSearch, GrantedPermissions: []string{toolstd.PermissionKnowledgeRead}}
			principal := agentcore.ExecutionPrincipal{ID: "owner", Kind: agentcore.ExecutionPrincipalUser, TenantID: "tenant-a", PermissionScope: []string{toolstd.PermissionKnowledgeRead}}
			e := &agentcore.ExecutionEnvelope{TenantID: "tenant-a", RunID: "read-run", AgentID: "assistant", AgentProfileID: "assistant", PrincipalID: principal.ID, PrincipalKind: principal.Kind, PermissionScopes: []string{toolstd.PermissionKnowledgeRead}, AllowedToolIDs: []string{string(binding.ToolID)}}
			call := runtimecore.RuntimeToolCallRequest{TenantID: e.TenantID, RunID: agentcore.AgentRunID(e.RunID), AgentID: agentcore.AgentID(e.AgentID), ExecutionPrincipal: principal, ExecutionEnvelope: e, Name: string(binding.ToolID), ToolBindings: []agentcore.RuntimeToolBinding{binding}}
			switch scenario {
			case "legacy-no-envelope":
				call.ExecutionEnvelope = nil
			case "wrong-tenant":
				call.TenantID = "tenant-b"
			case "wrong-run":
				call.RunID = "other-run"
			case "wrong-agent":
				call.AgentID = "other-agent"
			case "wrong-principal":
				call.ExecutionPrincipal.ID = "other-owner"
			case "wrong-kind":
				call.ExecutionPrincipal.Kind = agentcore.ExecutionPrincipalAgent
			case "expired":
				e.ExpiresAt = time.Now().Add(-time.Minute)
			case "excluded-tool":
				e.AllowedToolIDs = []string{string(toolstd.WebSearch)}
			case "forbidden":
				e.ForbiddenActions = []string{toolstd.PermissionKnowledgeRead}
			case "wrong-scope":
				e.PermissionScopes = []string{toolstd.PermissionWebRead}
			}
			if err := e.Seal(); err != nil {
				t.Fatal(err)
			}
			if scenario == "tampered" {
				e.PermissionScopes = []string{"*"}
			}
			err := productionAgentCoreToolPolicy(context.Background(), call)
			allowed := scenario == "delegated-owner" || scenario == "legacy-no-envelope"
			if (allowed && err != nil) || (!allowed && !errors.Is(err, agentkernel.ErrToolDenied)) {
				t.Fatalf("allowed=%v err=%v", allowed, err)
			}
		})
	}
}

func TestProductionAgentCoreBudgetsPreserveHostLimits(t *testing.T) {
	for _, mode := range []string{"beyond-old-limits", "request-max-turns", "model-limit", "tool-limit"} {
		t.Run(mode, func(t *testing.T) {
			requests, tools := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				requests++
				var calls []any
				count := 1
				if mode == "tool-limit" {
					count = 257
				}
				if mode != "beyond-old-limits" || requests < 18 {
					for i := 0; i < count; i++ {
						calls = append(calls, map[string]any{"id": fmt.Sprintf("call-%d-%d", requests, i), "type": "function", "function": map[string]any{"name": "knowledge_search", "arguments": "{}"}})
					}
				}
				message := map[string]any{"role": "assistant", "content": "answer"}
				finish := "stop"
				if len(calls) > 0 {
					message["content"], message["tool_calls"], finish = "", calls, "tool_calls"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": finish, "message": message}}})
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + filepath.Join(t.TempDir(), "journal.db")})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			journal, err := agentkernel.NewSQLToolJournal(store)
			if err != nil {
				t.Fatal(err)
			}
			workspaces := session.NewWorkspaceBroker()
			config := productionAgentCoreConnectionConfig(productionDiscussionAgentDependencies{
				KernelJournal: journal,
				LLMProfiles:   llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{ProfileID: "fixture", Model: "fixture", BaseURL: server.URL, APIKey: "fixture", Enabled: true}}},
				ToolDispatcher: runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
					tools++
					return runtimecore.RuntimeToolCallResult{Output: map[string]any{"answer": "approved"}}, nil
				}),
			}, workspaces)
			request := agentcore.RuntimeRequest{TenantID: "tenant-a", AgentID: "agent-a", RunID: "budget-run", InputMessages: []agentcore.RuntimeChatMessage{{Role: "user", Content: "budget"}}, ToolBindings: []agentcore.RuntimeToolBinding{{ToolID: toolstd.KnowledgeSearch, Alias: "knowledge_search", GrantedPermissions: []string{toolstd.PermissionKnowledgeRead}}}, RuntimeOptions: agentcore.RuntimeOptions{WorkingDirectory: t.TempDir()}}
			if mode == "request-max-turns" {
				request.RuntimeOptions.MaxTurns = 3
			}
			binding, err := workspaces.Bind(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			defer workspaces.Release(binding.Ref)
			runner, err := config.Engine.OpenSession(ctx, agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "budget"}, agentcore.SessionConfig{WorkspaceRef: binding.Ref})
			if err != nil {
				t.Fatal(err)
			}
			defer runner.Close(context.Background())
			result, err := runner.Run(ctx, agentcore.SendCommand{Content: []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: "budget"}}}, func(event agentcore.SessionEvent) error { return event.Validate() })
			wantRequests, wantTools := 18, 17
			switch mode {
			case "request-max-turns":
				wantRequests, wantTools = 3, 2
			case "model-limit":
				wantRequests, wantTools = 128, 127
			case "tool-limit":
				wantRequests, wantTools = 1, 0
			}
			if mode == "beyond-old-limits" {
				if err != nil || result.ResultText != "answer" {
					t.Fatalf("larger valid run failed: %+v %v", result, err)
				}
			} else if !errors.Is(err, agentkernel.ErrBudget) {
				t.Fatalf("budget was not enforced: %v", err)
			}
			if requests != wantRequests || tools != wantTools {
				t.Fatalf("requests=%d tools=%d, want %d/%d", requests, tools, wantRequests, wantTools)
			}
		})
	}
}
