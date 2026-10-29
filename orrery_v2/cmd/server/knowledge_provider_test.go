package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/app/userauth"
	knowledgecore "orrery_v2/internal/knowledge/core"
	knowledgeprovider "orrery_v2/internal/knowledge/provider"
	"orrery_v2/internal/platform/testkit"
	httptransport "orrery_v2/internal/transport/http"
	mcptransport "orrery_v2/internal/transport/mcp"
)

func TestShouldBootstrapKnowledgeDefaultsSupportsConfiguredPageIndex(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	settings := productionSettingsStore{router: env.DBRouter}
	if _, err := settings.SaveSettings(ctx, httptransport.SettingsDTO{
		Knowledge: httptransport.SettingsKnowledgeDTO{
			Enabled:         true,
			DefaultProvider: "pageindex",
			PageIndex: httptransport.SettingsPageIndexKnowledgeDTO{
				APIURL:         "http://pageindex:8091",
				APIKeyRef:      "secret://knowledge/pageindex",
				TimeoutSeconds: 30,
			},
		},
	}); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	if !shouldBootstrapKnowledgeDefaults(ctx, settings) {
		t.Fatalf("configured PageIndex knowledge should allow default knowledge bootstrap")
	}
}

func TestShouldBootstrapKnowledgeDefaultsSkipsUnconfiguredPageIndexDefault(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	settings := productionSettingsStore{router: env.DBRouter}

	if shouldBootstrapKnowledgeDefaults(ctx, settings) {
		t.Fatalf("default PageIndex knowledge without API key should not bootstrap")
	}
}

func TestProductionKnowledgeAddUploadsToAgentDefaultKnowledgeBase(t *testing.T) {
	ctx := context.Background()
	command := &recordingKnowledgeUploadService{
		document: knowledgeadmin.KnowledgeDocumentDTO{
			ID:              "kdoc-1",
			KnowledgeBaseID: "kb-agent-default",
			Filename:        "release-note.md",
			ContentType:     "text/markdown; charset=utf-8",
			Status:          "uploaded",
			JobID:           "job-1",
		},
	}
	executor := productionKnowledgeToolExecutor{
		baseFinder: fixedKnowledgeBaseFinder{bases: []knowledgecore.KnowledgeBase{{
			ID:         "kb-agent-default",
			OwnerType:  knowledgecore.OwnerAgent,
			OwnerID:    "agent-writer",
			IsDefault:  true,
			Status:     knowledgecore.KnowledgeActive,
			Visibility: knowledgecore.VisibilityPrivate,
		}}},
		upload: command,
	}

	result, err := executor.ExecuteKnowledgeTool(ctx, "knowledge_add", mcptransport.KnowledgeToolRequest{
		PrincipalID: "agent-writer",
		AgentID:     "agent-writer",
		Title:       "Release note",
		Content:     "Ship knowledge add.",
		Filename:    "release-note.md",
	})
	if err != nil {
		t.Fatalf("ExecuteKnowledgeTool: %v", err)
	}
	if command.viewer.ViewerID != "agent-writer" ||
		len(command.viewer.AgentIDs) != 1 ||
		command.viewer.AgentIDs[0] != "agent-writer" ||
		len(command.viewer.PermissionScope) != 1 ||
		command.viewer.PermissionScope[0] != knowledgeadmin.PermissionKnowledgeWrite {
		t.Fatalf("upload viewer = %+v", command.viewer)
	}
	if command.cmd.KnowledgeBaseID != "kb-agent-default" ||
		command.cmd.Filename != "release-note.md" ||
		command.cmd.ContentType != "text/markdown; charset=utf-8" {
		t.Fatalf("upload command = %+v", command.cmd)
	}
	if !strings.Contains(command.content, "# Release note") || !strings.Contains(command.content, "Ship knowledge add.") {
		t.Fatalf("uploaded content = %q", command.content)
	}
	if result.Output["knowledge_base_id"] != "kb-agent-default" ||
		result.Output["document_id"] != "kdoc-1" ||
		result.Output["filename"] != "release-note.md" ||
		result.Output["job_id"] != "job-1" {
		t.Fatalf("result = %+v", result.Output)
	}
}

func TestProductionKnowledgeAddPrefersProfileKnowledgeBase(t *testing.T) {
	command := &recordingKnowledgeUploadService{document: knowledgeadmin.KnowledgeDocumentDTO{
		ID: "kdoc-1", KnowledgeBaseID: "kb-profile", Filename: "note.md", Status: "uploaded",
	}}
	executor := productionKnowledgeToolExecutor{
		profiles: fixedKnowledgeProfileLookup{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"A000001": {ID: "A000001", PersonalKnowledgeBaseID: "kb-profile", Enabled: true},
		}},
		baseFinder: fixedKnowledgeBaseFinder{bases: []knowledgecore.KnowledgeBase{{
			ID: "kb-profile", OwnerType: knowledgecore.OwnerAgent, OwnerID: "A000001", Status: knowledgecore.KnowledgeActive,
		}}},
		upload: command,
	}

	if _, err := executor.ExecuteKnowledgeTool(context.Background(), "knowledge_add", mcptransport.KnowledgeToolRequest{
		PrincipalID: "A000001", AgentID: "A000001", Content: "profile knowledge",
	}); err != nil {
		t.Fatalf("ExecuteKnowledgeTool: %v", err)
	}
	if command.cmd.KnowledgeBaseID != "kb-profile" {
		t.Fatalf("upload knowledge base = %q", command.cmd.KnowledgeBaseID)
	}
}

func TestProductionKnowledgeAddRepairsDanglingProfileKnowledgeBase(t *testing.T) {
	command := &recordingKnowledgeUploadService{document: knowledgeadmin.KnowledgeDocumentDTO{
		ID: "kdoc-1", KnowledgeBaseID: "kb-repaired", Filename: "note.md", Status: "uploaded",
	}}
	ensurer := &recordingDefaultKnowledgeEnsurer{base: knowledgeadmin.KnowledgeBaseDTO{ID: "kb-repaired"}}
	executor := productionKnowledgeToolExecutor{
		profiles: fixedKnowledgeProfileLookup{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"A000001": {ID: "A000001", PersonalKnowledgeBaseID: "kb-missing", Enabled: true},
		}},
		baseFinder:    fixedKnowledgeBaseFinder{},
		ensureDefault: ensurer,
		upload:        command,
	}

	if _, err := executor.ExecuteKnowledgeTool(context.Background(), "knowledge_add", mcptransport.KnowledgeToolRequest{
		PrincipalID: "A000001", AgentID: "A000001", Content: "profile knowledge",
	}); err != nil {
		t.Fatalf("ExecuteKnowledgeTool: %v", err)
	}
	if command.cmd.KnowledgeBaseID != "kb-repaired" {
		t.Fatalf("upload knowledge base = %q", command.cmd.KnowledgeBaseID)
	}
}

func TestProductionKnowledgeAddEnsuresMissingAgentDefaultKnowledgeBase(t *testing.T) {
	ctx := context.Background()
	command := &recordingKnowledgeUploadService{
		document: knowledgeadmin.KnowledgeDocumentDTO{
			ID:              "kdoc-1",
			KnowledgeBaseID: "kb-agent-default",
			Filename:        "frontier-research.md",
			ContentType:     "text/markdown; charset=utf-8",
			Status:          "uploaded",
		},
	}
	ensurer := &recordingDefaultKnowledgeEnsurer{base: knowledgeadmin.KnowledgeBaseDTO{ID: "kb-agent-default"}}
	executor := productionKnowledgeToolExecutor{
		baseFinder:    fixedKnowledgeBaseFinder{},
		ensureDefault: ensurer,
		upload:        command,
	}

	if _, err := executor.ExecuteKnowledgeTool(ctx, "knowledge_add", mcptransport.KnowledgeToolRequest{
		PrincipalID: "tiedan",
		AgentID:     "tiedan",
		Title:       "Frontier research candidate",
		Content:     "Source-backed candidate.",
	}); err != nil {
		t.Fatalf("ExecuteKnowledgeTool: %v", err)
	}
	if ensurer.cmd.OwnerType != string(knowledgecore.OwnerAgent) || ensurer.cmd.OwnerID != "tiedan" ||
		len(ensurer.viewer.PermissionScope) != 1 || ensurer.viewer.PermissionScope[0] != knowledgeadmin.PermissionKnowledgeManage {
		t.Fatalf("default knowledge ensure = viewer:%+v cmd:%+v", ensurer.viewer, ensurer.cmd)
	}
	if command.cmd.KnowledgeBaseID != "kb-agent-default" {
		t.Fatalf("upload command = %+v", command.cmd)
	}
}

func TestProductionKnowledgeSearchUsesAgentReadPermission(t *testing.T) {
	ctx := context.Background()
	search := &recordingKnowledgeSearchService{
		page: knowledgeadmin.KnowledgeSearchPageDTO{Results: []knowledgeadmin.KnowledgeSearchResultDTO{{
			KnowledgeBaseID: "kb-agent-default",
			DocumentID:      "kdoc-1",
			ChunkID:         "chunk-1",
			Score:           0.87,
			Snippet:         "release note",
			HeadingPath:     []string{"Release"},
			DocumentName:    "release-note.md",
		}}, Executions: []knowledgeadmin.KnowledgeSearchExecutionDTO{{
			Provider: "pageindex", KnowledgeBaseID: "kb-agent-default",
			RequestedMode: "agentic", ActualMode: "fast_structure_search",
			FallbackReason: "agentic_timeout_fast_fallback",
		}}},
	}
	executor := productionKnowledgeToolExecutor{search: search}

	result, err := executor.ExecuteKnowledgeTool(ctx, "knowledge_search", mcptransport.KnowledgeToolRequest{
		PrincipalID:      "agent-reader",
		AgentID:          "agent-reader",
		Query:            "release",
		RetrievalMode:    "agentic",
		KnowledgeBaseIDs: []string{"kb-agent-default"},
		OwnerType:        "agent",
		OwnerID:          "agent-reader",
		Limit:            3,
	})
	if err != nil {
		t.Fatalf("ExecuteKnowledgeTool: %v", err)
	}
	if search.viewer.ViewerID != "agent-reader" ||
		len(search.viewer.AgentIDs) != 1 ||
		search.viewer.AgentIDs[0] != "agent-reader" ||
		len(search.viewer.PermissionScope) != 1 ||
		search.viewer.PermissionScope[0] != knowledgeadmin.PermissionKnowledgeRead {
		t.Fatalf("search viewer = %+v", search.viewer)
	}
	if search.cmd.Query != "release" ||
		search.cmd.RetrievalMode != knowledgeprovider.RetrievalModeAgentic ||
		len(search.cmd.KnowledgeBaseIDs) != 1 ||
		search.cmd.KnowledgeBaseIDs[0] != "kb-agent-default" ||
		search.cmd.OwnerFilter.OwnerType != "agent" ||
		search.cmd.OwnerFilter.OwnerID != "agent-reader" ||
		search.cmd.Limit != 3 {
		t.Fatalf("search command = %+v", search.cmd)
	}
	results, ok := result.Output["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("result = %+v", result.Output)
	}
	first, ok := results[0].(map[string]any)
	if !ok || first["document_id"] != "kdoc-1" || first["snippet"] != "release note" {
		t.Fatalf("result = %+v", result.Output)
	}
	retrievals, ok := result.Output["retrievals"].([]any)
	if !ok || len(retrievals) != 1 {
		t.Fatalf("retrievals = %+v", result.Output["retrievals"])
	}
	receipt, ok := retrievals[0].(map[string]any)
	if !ok || receipt["requested_mode"] != "agentic" ||
		receipt["actual_mode"] != "fast_structure_search" ||
		receipt["fallback_reason"] != "agentic_timeout_fast_fallback" {
		t.Fatalf("retrieval receipt = %+v", receipt)
	}
}

func TestProductionKnowledgeSearchUsesTrustedDelegatedUserScope(t *testing.T) {
	search := &recordingKnowledgeSearchService{}
	executor := productionKnowledgeToolExecutor{search: search}

	_, err := executor.ExecuteKnowledgeTool(context.Background(), "knowledge_search", mcptransport.KnowledgeToolRequest{
		TenantID: "minvia", PrincipalID: "human-owner", PrincipalKind: string(agentcore.ExecutionPrincipalUser),
		AgentID: "personal-assistant", Query: "company policy",
	})
	if err != nil {
		t.Fatalf("ExecuteKnowledgeTool: %v", err)
	}
	if len(search.viewer.AgentIDs) != 1 || search.viewer.AgentIDs[0] != "personal-assistant" ||
		len(search.viewer.HumanIDs) != 1 || search.viewer.HumanIDs[0] != "human-owner" ||
		len(search.viewer.OrganizationIDs) != 1 || search.viewer.OrganizationIDs[0] != "minvia" {
		t.Fatalf("delegated knowledge viewer = %+v", search.viewer)
	}
}

func TestProductionKnowledgeSearchRejectsUnsupportedRetrievalMode(t *testing.T) {
	search := &recordingKnowledgeSearchService{}
	executor := productionKnowledgeToolExecutor{search: search}

	_, err := executor.ExecuteKnowledgeTool(context.Background(), "knowledge_search", mcptransport.KnowledgeToolRequest{
		PrincipalID: "agent-reader", AgentID: "agent-reader", Query: "release",
		RetrievalMode: "unbounded",
	})

	if !errors.Is(err, knowledgeadmin.ErrInvalidRequest) {
		t.Fatalf("error = %v, want ErrInvalidRequest", err)
	}
	if search.cmd.Query != "" {
		t.Fatalf("provider was called with %+v", search.cmd)
	}
}

func TestProductionKnowledgeSearchTimeoutCancelsProvider(t *testing.T) {
	search := &blockingKnowledgeSearchService{canceled: make(chan struct{})}
	executor := productionKnowledgeToolExecutor{search: search, searchTimeout: 20 * time.Millisecond}
	started := time.Now()
	_, err := executor.ExecuteKnowledgeTool(context.Background(), "knowledge_search", mcptransport.KnowledgeToolRequest{
		PrincipalID: "agent-reader", AgentID: "agent-reader", Query: "release",
	})
	if !errors.Is(err, knowledgeadmin.ErrServiceDependency) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatalf("knowledge timeout took %s", time.Since(started))
	}
	select {
	case <-search.canceled:
	case <-time.After(time.Second):
		t.Fatal("provider context was not canceled")
	}
}

func TestProductionKnowledgeSearchTimeoutByRetrievalMode(t *testing.T) {
	executor := productionKnowledgeToolExecutor{}

	for _, test := range []struct {
		mode knowledgeprovider.RetrievalMode
		want time.Duration
	}{
		{mode: knowledgeprovider.RetrievalModeAuto, want: 45 * time.Second},
		{mode: knowledgeprovider.RetrievalModeFast, want: 45 * time.Second},
		{mode: knowledgeprovider.RetrievalModeAgentic, want: 60 * time.Second},
	} {
		if got := executor.knowledgeSearchTimeout(context.Background(), test.mode); got != test.want {
			t.Fatalf("knowledge search timeout for %q = %s, want %s", test.mode, got, test.want)
		}
	}
}

func TestProductionKnowledgeSearchFallsBackToFastAfterAgenticTimeout(t *testing.T) {
	search := &agenticTimeoutThenFastKnowledgeSearchService{
		page: knowledgeadmin.KnowledgeSearchPageDTO{
			Results: []knowledgeadmin.KnowledgeSearchResultDTO{{
				KnowledgeBaseID: "kb-1", DocumentID: "doc-1", Snippet: "project milestone",
			}},
			Executions: []knowledgeadmin.KnowledgeSearchExecutionDTO{{
				Provider: "pageindex", KnowledgeBaseID: "kb-1", RequestedMode: "fast", ActualMode: "fast_structure_search",
			}},
		},
	}
	executor := productionKnowledgeToolExecutor{search: search, searchTimeout: 100 * time.Millisecond}

	result, err := executor.ExecuteKnowledgeTool(context.Background(), "knowledge_search", mcptransport.KnowledgeToolRequest{
		PrincipalID: "agent-reader", AgentID: "agent-reader", Query: "project milestone", RetrievalMode: "agentic",
	})
	if err != nil {
		t.Fatalf("ExecuteKnowledgeTool: %v", err)
	}
	if len(search.modes) != 2 || search.modes[0] != knowledgeprovider.RetrievalModeAgentic || search.modes[1] != knowledgeprovider.RetrievalModeFast {
		t.Fatalf("search modes = %v, want agentic then fast", search.modes)
	}
	retrievals, ok := result.Output["retrievals"].([]any)
	if !ok || len(retrievals) != 1 {
		t.Fatalf("retrievals = %+v", result.Output["retrievals"])
	}
	receipt := retrievals[0].(map[string]any)
	if receipt["requested_mode"] != "agentic" || receipt["actual_mode"] != "fast_structure_search" || receipt["fallback_reason"] != "agentic_timeout_fast_fallback" {
		t.Fatalf("retrieval receipt = %+v", receipt)
	}
}

func TestProductionKnowledgeSearchTimeoutHonorsExplicitOverride(t *testing.T) {
	executor := productionKnowledgeToolExecutor{searchTimeout: 25 * time.Millisecond}

	if got := executor.knowledgeSearchTimeout(context.Background(), knowledgeprovider.RetrievalModeAgentic); got != 25*time.Millisecond {
		t.Fatalf("knowledge search timeout = %s, want explicit override", got)
	}
}

func TestProductionKnowledgeSearchCapsProviderLimitAtEight(t *testing.T) {
	for _, requested := range []int{0, 8, 100} {
		search := &recordingKnowledgeSearchService{}
		executor := productionKnowledgeToolExecutor{search: search, searchTimeout: time.Second}
		if _, err := executor.ExecuteKnowledgeTool(context.Background(), "knowledge_search", mcptransport.KnowledgeToolRequest{
			PrincipalID: "agent-reader", Query: "release", Limit: requested,
		}); err != nil {
			t.Fatal(err)
		}
		if search.cmd.Limit != 8 {
			t.Fatalf("requested=%d provider limit=%d, want 8", requested, search.cmd.Limit)
		}
	}
}

func TestProductionKnowledgeSearchUsesAgentProfileScope(t *testing.T) {
	search := &recordingKnowledgeSearchService{}
	profiles := fixedKnowledgeProfileLookup{profile: agentcore.AgentProfile{
		ID:        "agent-reader",
		AgentKind: agentcore.AgentKindAI,
		Enabled:   true,
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef: agentcore.OrganizationRef{ID: "org-1"},
			PositionRef:     agentcore.PositionRef{ID: "position-1"},
			RoleRefs:        []agentcore.RoleRef{{ID: "role-1"}},
		},
	}}
	executor := productionKnowledgeToolExecutor{search: search, profiles: profiles}

	if _, err := executor.ExecuteKnowledgeTool(context.Background(), "knowledge_search", mcptransport.KnowledgeToolRequest{
		PrincipalID: "agent-reader",
		AgentID:     "agent-reader",
		Query:       "release",
	}); err != nil {
		t.Fatalf("ExecuteKnowledgeTool: %v", err)
	}
	if len(search.viewer.OrganizationIDs) != 1 || search.viewer.OrganizationIDs[0] != "org-1" ||
		len(search.viewer.PositionIDs) != 1 || search.viewer.PositionIDs[0] != "position-1" ||
		len(search.viewer.RoleIDs) != 1 || search.viewer.RoleIDs[0] != "role-1" {
		t.Fatalf("search viewer = %+v", search.viewer)
	}
}

func TestProductionKnowledgeSearchPersonalAssistantUsesOwnerHumanScope(t *testing.T) {
	search := &recordingKnowledgeSearchService{}
	profiles := fixedKnowledgeProfileLookup{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"personal-assistant-1": {
			ID:                       "personal-assistant-1",
			PersonalAssistantOwnerID: "ding",
			AgentKind:                agentcore.AgentKindAI,
			Enabled:                  true,
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "template-org"},
				PositionRef:     agentcore.PositionRef{ID: "template-position"},
				RoleRefs:        []agentcore.RoleRef{{ID: "template-role"}},
			},
		},
		"H000001": {
			ID:        "H000001",
			AgentKind: agentcore.AgentKindHuman,
			Enabled:   true,
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "owner-org"},
				PositionRef:     agentcore.PositionRef{ID: "owner-position"},
				RoleRefs:        []agentcore.RoleRef{{ID: "owner-role"}},
			},
			UserAccount: agentcore.UserAccountSpec{
				Username: "ding",
			},
		},
	}}
	executor := productionKnowledgeToolExecutor{search: search, profiles: profiles}

	if _, err := executor.ExecuteKnowledgeTool(context.Background(), "knowledge_search", mcptransport.KnowledgeToolRequest{
		PrincipalID: "H000001",
		AgentID:     "personal-assistant-1",
		Query:       "release",
	}); err != nil {
		t.Fatalf("ExecuteKnowledgeTool: %v", err)
	}
	if len(search.viewer.AgentIDs) != 1 || search.viewer.AgentIDs[0] != "personal-assistant-1" ||
		len(search.viewer.HumanIDs) != 1 || search.viewer.HumanIDs[0] != "H000001" ||
		len(search.viewer.OrganizationIDs) != 1 || search.viewer.OrganizationIDs[0] != "owner-org" ||
		len(search.viewer.PositionIDs) != 1 || search.viewer.PositionIDs[0] != "owner-position" ||
		len(search.viewer.RoleIDs) != 1 || search.viewer.RoleIDs[0] != "owner-role" {
		t.Fatalf("search viewer = %+v", search.viewer)
	}
}

func TestProductionKnowledgeSearchResolvesUsernamePrincipalToCanonicalHumanID(t *testing.T) {
	search := &recordingKnowledgeSearchService{}
	profiles := fixedKnowledgeProfileLookup{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"personal-assistant-1": {
			ID:                       "personal-assistant-1",
			PersonalAssistantOwnerID: "ding",
			AgentKind:                agentcore.AgentKindAI,
			Enabled:                  true,
		},
		"H000001": {
			ID:        "H000001",
			AgentKind: agentcore.AgentKindHuman,
			Enabled:   true,
			UserAccount: agentcore.UserAccountSpec{
				Username: "ding",
			},
		},
	}}
	executor := productionKnowledgeToolExecutor{
		search:   search,
		profiles: profiles,
		accounts: fixedKnowledgeAccountLookup{accounts: map[string]userauth.Account{
			"ding": {UserID: "ding", Username: "ding", AgentProfileID: "H000001", Enabled: true},
		}},
	}

	if _, err := executor.ExecuteKnowledgeTool(context.Background(), "knowledge_search", mcptransport.KnowledgeToolRequest{
		PrincipalID:   "ding",
		PrincipalKind: string(agentcore.ExecutionPrincipalUser),
		AgentID:       "personal-assistant-1",
		Query:         "release",
	}); err != nil {
		t.Fatalf("ExecuteKnowledgeTool: %v", err)
	}
	if strings.Join(search.viewer.HumanIDs, ",") != "ding,H000001" {
		t.Fatalf("search viewer = %+v", search.viewer)
	}
}

func TestProductionKnowledgeSearchDoesNotResolveAnotherUsernameWithoutUserDelegation(t *testing.T) {
	search := &recordingKnowledgeSearchService{}
	profiles := fixedKnowledgeProfileLookup{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"agent-reader": {ID: "agent-reader", AgentKind: agentcore.AgentKindAI, Enabled: true},
		"H000001":      {ID: "H000001", AgentKind: agentcore.AgentKindHuman, Enabled: true},
	}}
	executor := productionKnowledgeToolExecutor{
		search:   search,
		profiles: profiles,
		accounts: fixedKnowledgeAccountLookup{accounts: map[string]userauth.Account{
			"ding": {UserID: "ding", Username: "ding", AgentProfileID: "H000001", Enabled: true},
		}},
	}

	if _, err := executor.ExecuteKnowledgeTool(context.Background(), "knowledge_search", mcptransport.KnowledgeToolRequest{
		PrincipalID:   "ding",
		PrincipalKind: string(agentcore.ExecutionPrincipalAgent),
		AgentID:       "agent-reader",
		Query:         "release",
	}); err != nil {
		t.Fatalf("ExecuteKnowledgeTool: %v", err)
	}
	if len(search.viewer.HumanIDs) != 0 {
		t.Fatalf("search viewer = %+v", search.viewer)
	}
}

func TestProductionKnowledgeSearchPersonalAssistantRejectsDifferentHumanPrincipalScope(t *testing.T) {
	search := &recordingKnowledgeSearchService{}
	profiles := fixedKnowledgeProfileLookup{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"personal-assistant-1": {
			ID:                       "personal-assistant-1",
			PersonalAssistantOwnerID: "ding",
			AgentKind:                agentcore.AgentKindAI,
			Enabled:                  true,
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "template-org"},
				PositionRef:     agentcore.PositionRef{ID: "template-position"},
				RoleRefs:        []agentcore.RoleRef{{ID: "template-role"}},
			},
		},
		"H000002": {
			ID:        "H000002",
			AgentKind: agentcore.AgentKindHuman,
			Enabled:   true,
			UserAccount: agentcore.UserAccountSpec{
				Username: "other-user",
			},
		},
	}}
	executor := productionKnowledgeToolExecutor{search: search, profiles: profiles}

	if _, err := executor.ExecuteKnowledgeTool(context.Background(), "knowledge_search", mcptransport.KnowledgeToolRequest{
		PrincipalID: "H000002",
		AgentID:     "personal-assistant-1",
		Query:       "release",
	}); err != nil {
		t.Fatalf("ExecuteKnowledgeTool: %v", err)
	}
	if len(search.viewer.AgentIDs) != 0 || len(search.viewer.HumanIDs) != 0 ||
		len(search.viewer.OrganizationIDs) != 0 || len(search.viewer.PositionIDs) != 0 || len(search.viewer.RoleIDs) != 0 {
		t.Fatalf("search viewer = %+v", search.viewer)
	}
}

func TestProductionKnowledgeSearchOrdinaryAgentDoesNotInheritHumanPrincipalScope(t *testing.T) {
	search := &recordingKnowledgeSearchService{}
	profiles := fixedKnowledgeProfileLookup{profile: agentcore.AgentProfile{
		ID:        "agent-reader",
		AgentKind: agentcore.AgentKindAI,
		Enabled:   true,
	}}
	executor := productionKnowledgeToolExecutor{search: search, profiles: profiles}

	if _, err := executor.ExecuteKnowledgeTool(context.Background(), "knowledge_search", mcptransport.KnowledgeToolRequest{
		PrincipalID: "H000001",
		AgentID:     "agent-reader",
		Query:       "release",
	}); err != nil {
		t.Fatalf("ExecuteKnowledgeTool: %v", err)
	}
	if len(search.viewer.HumanIDs) != 0 {
		t.Fatalf("search viewer = %+v", search.viewer)
	}
}

func TestDynamicKnowledgeProviderRegistrySelectsConfiguredMCPProvider(t *testing.T) {
	mcp := &productionOntologyKnowledgeProvider{}
	registry := dynamicKnowledgeProviderRegistry{mcp: mcp}
	base := knowledgecore.KnowledgeBase{Provider: knowledgecore.ProviderMCP}

	if got := registry.ForBase(base); got != mcp {
		t.Fatalf("MCP provider = %T, want configured external provider", got)
	}
}

func TestDynamicKnowledgeProviderRegistryRetainsLastKnownSettings(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()

	store := productionSettingsStore{router: env.DBRouter}
	want := httptransport.SettingsDTO{
		Knowledge: httptransport.SettingsKnowledgeDTO{
			Enabled:         true,
			DefaultProvider: "pageindex",
			PageIndex: httptransport.SettingsPageIndexKnowledgeDTO{
				APIURL:         "http://pageindex:8091",
				APIKeyRef:      "secret://knowledge/pageindex",
				TimeoutSeconds: 30,
			},
		},
	}
	if _, err := store.SaveSettings(ctx, want); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	registry := dynamicKnowledgeProviderRegistry{
		settings:      store,
		settingsCache: &knowledgeSettingsSnapshot{},
	}
	if got := registry.settingsDTO(ctx); got.Knowledge.PageIndex.APIKeyRef != want.Knowledge.PageIndex.APIKeyRef {
		t.Fatalf("initial PageIndex settings = %+v", got.Knowledge.PageIndex)
	}

	registry.settings.router = nil
	got := registry.settingsDTO(ctx)
	if !got.Knowledge.Enabled ||
		got.Knowledge.PageIndex.APIURL != want.Knowledge.PageIndex.APIURL ||
		got.Knowledge.PageIndex.APIKeyRef != want.Knowledge.PageIndex.APIKeyRef {
		t.Fatalf("cached PageIndex settings = %+v", got.Knowledge.PageIndex)
	}
}

func TestDynamicKnowledgeProviderRegistryUsesProductionFallbackWithoutSnapshot(t *testing.T) {
	registry := dynamicKnowledgeProviderRegistry{
		settings:      productionSettingsStore{},
		settingsCache: &knowledgeSettingsSnapshot{},
	}

	got := registry.settingsDTO(context.Background())
	if hasConfiguredKnowledgeSettings(got.Knowledge) {
		t.Fatalf("knowledge settings = %+v, want disabled production fallback", got.Knowledge)
	}
}

type fixedKnowledgeBaseFinder struct {
	bases []knowledgecore.KnowledgeBase
	err   error
}

type fixedKnowledgeSettingsLookup struct {
	settings httptransport.SettingsDTO
	err      error
}

func (f fixedKnowledgeSettingsLookup) GetSettings(context.Context) (httptransport.SettingsDTO, error) {
	return f.settings, f.err
}

func (f fixedKnowledgeBaseFinder) ListKnowledgeBases(ctx context.Context, query knowledgecore.KnowledgeBaseQuery) (knowledgecore.KnowledgeBasePage, error) {
	if f.err != nil {
		return knowledgecore.KnowledgeBasePage{}, f.err
	}
	items := make([]knowledgecore.KnowledgeBase, 0, len(f.bases))
	for _, base := range f.bases {
		if query.OwnerType != "" && base.OwnerType != query.OwnerType {
			continue
		}
		if query.OwnerID != "" && base.OwnerID != query.OwnerID {
			continue
		}
		items = append(items, base)
	}
	return knowledgecore.KnowledgeBasePage{Items: items, Total: len(items)}, nil
}

type recordingKnowledgeUploadService struct {
	viewer   knowledgeadmin.ViewerRef
	cmd      knowledgeadmin.UploadDocumentCommand
	content  string
	document knowledgeadmin.KnowledgeDocumentDTO
	err      error
}

type recordingDefaultKnowledgeEnsurer struct {
	viewer knowledgeadmin.ViewerRef
	cmd    knowledgeadmin.EnsureDefaultCommand
	base   knowledgeadmin.KnowledgeBaseDTO
	err    error
}

func (r *recordingDefaultKnowledgeEnsurer) EnsureDefaultPersonalKnowledgeBase(_ context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.EnsureDefaultCommand) (knowledgeadmin.KnowledgeBaseDTO, error) {
	r.viewer = viewer
	r.cmd = cmd
	return r.base, r.err
}

func (s *recordingKnowledgeUploadService) UploadDocument(ctx context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.UploadDocumentCommand) (knowledgeadmin.KnowledgeDocumentDTO, error) {
	s.viewer = viewer
	s.cmd = cmd
	if cmd.Reader != nil {
		raw, _ := io.ReadAll(cmd.Reader)
		s.content = string(raw)
	}
	return s.document, s.err
}

type recordingKnowledgeSearchService struct {
	viewer knowledgeadmin.ViewerRef
	cmd    knowledgeadmin.SearchKnowledgeCommand
	page   knowledgeadmin.KnowledgeSearchPageDTO
	err    error
}

type blockingKnowledgeSearchService struct {
	canceled chan struct{}
}

type agenticTimeoutThenFastKnowledgeSearchService struct {
	modes []knowledgeprovider.RetrievalMode
	page  knowledgeadmin.KnowledgeSearchPageDTO
}

func (s *agenticTimeoutThenFastKnowledgeSearchService) SearchKnowledge(ctx context.Context, _ knowledgeadmin.ViewerRef, cmd knowledgeadmin.SearchKnowledgeCommand) (knowledgeadmin.KnowledgeSearchPageDTO, error) {
	s.modes = append(s.modes, cmd.RetrievalMode)
	if cmd.RetrievalMode == knowledgeprovider.RetrievalModeAgentic {
		<-ctx.Done()
		return knowledgeadmin.KnowledgeSearchPageDTO{}, ctx.Err()
	}
	return s.page, nil
}

func (s *blockingKnowledgeSearchService) SearchKnowledge(ctx context.Context, _ knowledgeadmin.ViewerRef, _ knowledgeadmin.SearchKnowledgeCommand) (knowledgeadmin.KnowledgeSearchPageDTO, error) {
	<-ctx.Done()
	close(s.canceled)
	return knowledgeadmin.KnowledgeSearchPageDTO{}, ctx.Err()
}

type fixedKnowledgeProfileLookup struct {
	profile  agentcore.AgentProfile
	profiles map[agentcore.AgentProfileID]agentcore.AgentProfile
	err      error
}

type fixedKnowledgeAccountLookup struct {
	accounts map[string]userauth.Account
	err      error
}

func (f fixedKnowledgeAccountLookup) GetAccountByUsername(_ context.Context, username string) (userauth.Account, error) {
	if f.err != nil {
		return userauth.Account{}, f.err
	}
	account, ok := f.accounts[username]
	if !ok {
		return userauth.Account{}, userauth.ErrAccountNotFound
	}
	return account, nil
}

func (f fixedKnowledgeProfileLookup) GetProfile(_ context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	if f.profiles != nil {
		profile, ok := f.profiles[id]
		if !ok {
			return agentcore.AgentProfile{}, agentcore.ErrNotFound
		}
		return profile, nil
	}
	return f.profile, f.err
}

func (s *recordingKnowledgeSearchService) SearchKnowledge(ctx context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.SearchKnowledgeCommand) (knowledgeadmin.KnowledgeSearchPageDTO, error) {
	s.viewer = viewer
	s.cmd = cmd
	return s.page, s.err
}
