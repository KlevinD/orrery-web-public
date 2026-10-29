package main

import (
	"context"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
	memtestkit "orrery_v2/internal/memory/testkit"
	mcptransport "orrery_v2/internal/transport/mcp"
)

func TestProductionPersonalAssistantMemoryIsOwnerScopedAndRecalled(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 8, 12, 9, 30, 0, 0, time.UTC)
	recall := &capturingSemanticMemoryRecall{}
	provider := newProductionDiscussionMemoryProvider(nil, store, productionDiscussionMemoryProviderOptions{
		Now: func() time.Time { return now }, Recall: recall,
	})
	ownerAgentID := discussionsession.PersonalAssistantAgentID("tenant-a", "account-1")
	otherAgentID := discussionsession.PersonalAssistantAgentID("tenant-a", "account-2")

	initial, err := provider.DiscussionAgentMemoryContext(ctx, ownerAgentID)
	if err != nil {
		t.Fatalf("initial context: %v", err)
	}
	if strings.TrimSpace(initial.PersonalDigest.Summary) != "" {
		t.Fatalf("initial context = %+v", initial)
	}
	if err := provider.CapturePersonalAssistantMemoryExchange(ctx, discussionsession.PersonalAssistantMemoryExchange{
		TenantID:           "tenant-a",
		OwnerID:            "account-1",
		AgentID:            ownerAgentID,
		ConversationID:     "conv-1",
		RunID:              "run-1",
		UserMessageID:      "msg-user-1",
		AssistantMessageID: "msg-assistant-1",
		UserContent:        "I prefer concise plans with named owners.",
		AssistantContent:   "I will use concise plans and name each owner.",
		OccurredAt:         now,
	}); err != nil {
		t.Fatalf("capture exchange: %v", err)
	}

	recall.records, err = store.FindRecords(ctx, memstore.RecordQuery{IncludeArchived: true})
	if err != nil {
		t.Fatal(err)
	}
	recalled, err := provider.DiscussionAgentMemoryContextForQuery(ctx, ownerAgentID, "test")
	if err != nil {
		t.Fatalf("recall context: %v", err)
	}
	if recall.calls != 1 || recall.request.Query != "test" || recall.request.Subject.AgentID != agentcore.AgentID(ownerAgentID) {
		t.Fatalf("explicit personal-assistant memory search did not use semantic recall: %+v", recall.request)
	}
	if !strings.Contains(recalled.PersonalDigest.Summary, "I prefer concise plans") ||
		!strings.Contains(recalled.PersonalDigest.Summary, "I will use concise plans") {
		t.Fatalf("recalled context = %+v", recalled)
	}
	profileID := agentcore.MemoryProfileID(discussionsession.PersonalAssistantMemoryProfileID(ownerAgentID))
	profile, err := store.GetProfile(ctx, profileID)
	if err != nil {
		t.Fatalf("get owner profile: %v", err)
	}
	if profile.OwnerRef.AgentID != agentcore.AgentID(ownerAgentID) ||
		profile.DefaultScope != (memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: ownerAgentID}) ||
		profile.VisibilityPolicy != (memcore.VisibilityPolicy{Mode: memcore.VisibilityOwnerOnly, ScopeKind: memcore.ScopeAgent, ScopeID: ownerAgentID}) {
		t.Fatalf("owner profile = %+v", profile)
	}

	other, err := provider.DiscussionAgentMemoryContext(ctx, otherAgentID)
	if err != nil {
		t.Fatalf("other owner context: %v", err)
	}
	if strings.TrimSpace(other.PersonalDigest.Summary) != "" {
		t.Fatalf("other owner received memory: %+v", other)
	}
	host, err := provider.DiscussionAgentMemoryContext(ctx, defaultLeadAgentID)
	if err != nil {
		t.Fatalf("execution host context: %v", err)
	}
	if strings.TrimSpace(host.PersonalDigest.Summary) != "" {
		t.Fatalf("execution host received owner memory: %+v", host)
	}
}

func TestProductionPersonalAssistantMemoryRepairsLegacyVisibilityScopeOnCapture(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	provider := newProductionDiscussionMemoryProvider(nil, store)
	agentID := discussionsession.PersonalAssistantAgentID("tenant-a", "account-1")
	profile := memcore.NewScopeMemoryProfile(
		memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: agentID},
		memcore.OwnerRef{AgentID: agentcore.AgentID(agentID)},
	)
	profile.VisibilityPolicy = memcore.VisibilityPolicy{Mode: memcore.VisibilityOwnerOnly}
	if err := store.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save legacy profile: %v", err)
	}

	if err := provider.CapturePersonalAssistantMemoryExchange(ctx, discussionsession.PersonalAssistantMemoryExchange{
		TenantID: "tenant-a", OwnerID: "account-1", AgentID: agentID,
		ConversationID: "conv-legacy", RunID: "run-legacy",
		UserContent: "Keep responses concise.", AssistantContent: "Understood.",
	}); err != nil {
		t.Fatalf("capture exchange: %v", err)
	}

	repaired, err := store.GetProfile(ctx, profile.ID)
	if err != nil {
		t.Fatalf("get repaired profile: %v", err)
	}
	want := memcore.VisibilityPolicy{Mode: memcore.VisibilityOwnerOnly, ScopeKind: memcore.ScopeAgent, ScopeID: agentID}
	if repaired.VisibilityPolicy != want {
		t.Fatalf("visibility policy = %+v, want %+v", repaired.VisibilityPolicy, want)
	}
	if _, err := store.GetCandidate(ctx, personalAssistantMemoryCandidateID(agentID, "run-legacy")); err != nil {
		t.Fatalf("capture candidate after repair: %v", err)
	}
}

func TestProductionPersonalAssistantMemoryRejectsConflictingVisibilityScope(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	provider := newProductionDiscussionMemoryProvider(nil, store)
	agentID := discussionsession.PersonalAssistantAgentID("tenant-a", "account-1")
	profile := memcore.NewScopeMemoryProfile(
		memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: agentID},
		memcore.OwnerRef{AgentID: agentcore.AgentID(agentID)},
	)
	profile.VisibilityPolicy.ScopeID = discussionsession.PersonalAssistantAgentID("tenant-a", "account-2")
	if err := store.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save conflicting profile: %v", err)
	}

	if _, err := provider.DiscussionAgentMemoryContext(ctx, agentID); err == nil || !strings.Contains(err.Error(), "incompatible ownership") {
		t.Fatalf("conflicting visibility error = %v", err)
	}
	unchanged, err := store.GetProfile(ctx, profile.ID)
	if err != nil {
		t.Fatalf("get conflicting profile: %v", err)
	}
	if unchanged.VisibilityPolicy.ScopeID != profile.VisibilityPolicy.ScopeID {
		t.Fatalf("conflicting profile was overwritten: %+v", unchanged.VisibilityPolicy)
	}
}

func TestProductionPersonalAssistantSensitiveExchangeRequiresReview(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	provider := newProductionDiscussionMemoryProvider(nil, store)
	agentID := discussionsession.PersonalAssistantAgentID("tenant-a", "account-1")

	if err := provider.CapturePersonalAssistantMemoryExchange(ctx, discussionsession.PersonalAssistantMemoryExchange{
		TenantID:         "tenant-a",
		OwnerID:          "account-1",
		AgentID:          agentID,
		ConversationID:   "conv-secret",
		RunID:            "run-secret",
		UserContent:      "Remember this secret launch code.",
		AssistantContent: "I understand.",
	}); err != nil {
		t.Fatalf("capture sensitive exchange: %v", err)
	}
	candidate, err := store.GetCandidate(ctx, personalAssistantMemoryCandidateID(agentID, "run-secret"))
	if err != nil {
		t.Fatalf("get sensitive candidate: %v", err)
	}
	if candidate.Status != memcore.CandidateProposed || !candidate.ReviewRequired || candidate.Sensitivity != memcore.SensitivitySecret {
		t.Fatalf("sensitive candidate = %+v", candidate)
	}
	context, err := provider.DiscussionAgentMemoryContext(ctx, agentID)
	if err != nil {
		t.Fatalf("retrieve sensitive context: %v", err)
	}
	if strings.Contains(strings.ToLower(context.PersonalDigest.Summary), "secret launch code") {
		t.Fatalf("unreviewed sensitive exchange was recalled: %+v", context)
	}
}

func TestProductionMemorySearchToolUsesLogicalAssistantIdentity(t *testing.T) {
	provider := &recordingMemorySearchProvider{memory: discussionsession.DiscussionAgentMemoryContext{
		PersonalDigest: discussionsession.DiscussionAgentMemoryDigest{
			DigestID: "digest-1", MemoryKind: "personal", Summary: "Use concise plans.", SourceMemory: []string{"memory-1"}, Confidence: 0.9,
		},
	}}
	executor := productionMemorySearchToolExecutor{provider: provider}
	logicalID := discussionsession.PersonalAssistantAgentID("tenant-a", "account-1")

	result, err := executor.ExecuteMemorySearchTool(context.Background(), mcptransport.MemorySearchToolRequest{
		TenantID: "tenant-a", PrincipalID: "user-1", PrincipalKind: string(agentcore.ExecutionPrincipalUser),
		AgentID: "lead-agent", LogicalAgentID: logicalID, Query: "plan style",
	})
	if err != nil {
		t.Fatal(err)
	}
	if provider.agentID != logicalID || provider.query != "plan style" || provider.access.TenantID != "tenant-a" ||
		provider.access.PrincipalID != "user-1" || provider.access.PrincipalKind != "user" || provider.access.AgentID != "lead-agent" {
		t.Fatalf("memory provider request = access %+v agent %q query %q", provider.access, provider.agentID, provider.query)
	}
	digest, ok := result.Output["personal_digest"].(map[string]any)
	if !ok || digest["summary"] != "Use concise plans." || digest["confidence"] != 0.9 {
		t.Fatalf("memory search output = %+v", result.Output)
	}
}

type recordingMemorySearchProvider struct {
	access  discussionsession.DiscussionAgentMemoryAccessContext
	agentID string
	query   string
	memory  discussionsession.DiscussionAgentMemoryContext
}

func (p *recordingMemorySearchProvider) DiscussionAgentMemoryContextForAuthorizedQuery(_ context.Context, access discussionsession.DiscussionAgentMemoryAccessContext, agentID, query string) (discussionsession.DiscussionAgentMemoryContext, error) {
	p.access = access
	p.agentID = agentID
	p.query = query
	return p.memory, nil
}
