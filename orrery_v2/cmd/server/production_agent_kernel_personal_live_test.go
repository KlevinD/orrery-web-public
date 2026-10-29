package main

import (
	"context"
	"encoding/json"
	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/agentkernel"
	"orrery_v2/internal/agent/runtime/llmprofile"
	"orrery_v2/internal/platform/db"
	toolres "orrery_v2/internal/resource/tool"
	toolstd "orrery_v2/internal/resource/tool/std"
	mcptransport "orrery_v2/internal/transport/mcp"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type personalLiveAuthority struct{ envelope *agentcore.ExecutionEnvelope }

func (a personalLiveAuthority) ResolveResponsibilityExecution(context.Context, string) (*mcptransport.ResponsibilityExecutionAuthority, error) {
	e := a.envelope
	return &mcptransport.ResponsibilityExecutionAuthority{RunID: e.RunID, TenantID: e.TenantID, AgentID: e.AgentID, AgentProfileID: e.AgentProfileID, Envelope: agentcore.CloneExecutionEnvelope(e)}, nil
}

type personalLiveProfiles struct {
	principalRegressionProfileStore
}

func (p personalLiveProfiles) GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	profile, err := p.principalRegressionProfileStore.GetProfile(ctx, id)
	if id == "A000017" {
		profile.ToolRefs = append(profile.ToolRefs, agentcore.ToolRef{ToolID: toolstd.KnowledgeSearch, PermissionScope: toolstd.PermissionKnowledgeRead})
	}
	return profile, err
}

type personalLiveKnowledge struct{ calls int }

func (e *personalLiveKnowledge) ExecuteKnowledgeTool(_ context.Context, _ string, req mcptransport.KnowledgeToolRequest) (mcptransport.KnowledgeToolResult, error) {
	e.calls++
	return mcptransport.KnowledgeToolResult{Output: map[string]any{"fixture": true, "document_count": 7, "scope": "authorized test fixture"}}, nil
}

// Uses the deployed upstream profile, a local journal and a fixture KB. Nothing
// is added to user conversations or sent through gateway delivery channels.
func TestProductionAgentCoreLivePersonalToolRoundTrip(t *testing.T) {
	raw := os.Getenv("ORRERY_AGENTCORE_LIVE_MODEL_PROFILE")
	if raw == "" {
		t.Skip("live model profile not configured")
	}
	var profile llmprofile.LLMProfile
	if json.Unmarshal([]byte(raw), &profile) != nil {
		t.Fatal("invalid live profile")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + filepath.Join(t.TempDir(), "journal.db")})
	if err != nil {
		t.Fatal("journal unavailable")
	}
	defer store.Close()
	journal, _ := agentkernel.NewSQLToolJournal(store)
	envelope := &agentcore.ExecutionEnvelope{TenantID: "minvia", RunID: "personal-live-round-trip", AgentID: "A000017", AgentProfileID: "A000017", PrincipalID: "H000001", PrincipalKind: agentcore.ExecutionPrincipalUser, PermissionScopes: []string{toolstd.PermissionKnowledgeRead}}
	if err := envelope.SealAt(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	tools := toolres.NewMemoryRegistry()
	for _, definition := range toolstd.Definitions(time.Now().UTC()) {
		if err := tools.Register(ctx, definition); err != nil {
			t.Fatal(err)
		}
	}
	knowledge := &personalLiveKnowledge{}
	// Include the same server-owned execution authority used by live bound runs.
	gateway := mcptransport.NewGateway(mcptransport.GatewayOptions{Registry: tools, Profiles: personalLiveProfiles{}, ResponsibilityAuthority: personalLiveAuthority{envelope}, Router: mcptransport.NewToolRouter(mcptransport.ToolRouterOptions{Registry: tools, KnowledgeExecutor: knowledge})})
	registry := newProductionRuntimeRegistry(productionDiscussionAgentDependencies{KernelJournal: journal, LLMProfiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{profile}}, ToolDispatcher: productionRuntimeToolDispatcher{mcpGateway: gateway}})
	defer registry.Close()
	runtime, err := registry.ResolveRuntime(ctx, agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged})
	if err != nil {
		t.Fatal("runtime unavailable")
	}
	started := time.Now()
	response, err := runtime.RunSession(ctx, agentcore.RuntimeRequest{TenantID: envelope.TenantID, AgentID: "A000017", RunID: agentcore.AgentRunID(envelope.RunID), ExecutionEnvelope: envelope,
		ExecutionPrincipal: agentcore.ExecutionPrincipal{ID: "H000001", Kind: agentcore.ExecutionPrincipalUser, TenantID: "minvia", PermissionScope: []string{toolstd.PermissionKnowledgeRead}},
		ContextPack:        agentcore.ContextPack{AgentID: "A000017"},
		InputMessages:      []agentcore.RuntimeChatMessage{{Role: "system", Content: "Call knowledge_search exactly once for the authorized fixture. After reading its document_count, reply with exactly FIXTURE_COUNT=7. Do not invent a result or repeat the call."}, {Role: "user", Content: "查一下测试知识库有多少内容。"}},
		ToolBindings:       []agentcore.RuntimeToolBinding{{ToolID: toolstd.KnowledgeSearch, GrantedPermissions: []string{toolstd.PermissionKnowledgeRead}, InputSchema: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}, "required": []any{"query"}, "additionalProperties": false}}},
		RuntimeOptions:     agentcore.RuntimeOptions{WorkingDirectory: t.TempDir(), ProviderSpecific: map[string]string{"llm_profile_id": llmprofile.PlatformDefaultProfileID}}}, nil)
	if err != nil || response.Status != agentcore.RuntimeStatusCompleted || knowledge.calls != 1 {
		t.Fatalf("personal tool round trip failed: status=%s tools=%d err=%v", response.Status, knowledge.calls, err)
	}
	found := false
	for _, message := range response.Messages {
		found = found || strings.Contains(message.Content, "FIXTURE_COUNT=7")
	}
	if !found {
		t.Fatal("model did not use the fixture tool result")
	}
	t.Logf("authorized personal-assistant tool round trip completed in %s", time.Since(started).Round(time.Millisecond))
}
