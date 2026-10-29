package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/config"
	skillres "orrery_v2/internal/resource/skill"
	skillstd "orrery_v2/internal/resource/skill/std"
	toolres "orrery_v2/internal/resource/tool"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestDefaultSystemToolDefinitionsAreAgentNeutral(t *testing.T) {
	for _, definition := range defaultSystemToolDefinitions() {
		text := strings.Join([]string{definition.Name, definition.Description}, "\n")
		if strings.Contains(text, "PM") {
			t.Fatalf("tool %s should not describe itself as PM-owned: %q", definition.ID, text)
		}
		for _, tag := range definition.Tags {
			if tag == "pm" {
				t.Fatalf("tool %s should not carry PM resource tag: %+v", definition.ID, definition.Tags)
			}
		}
		for _, permission := range definition.Permissions {
			if strings.Contains(permission.Description, "PM") {
				t.Fatalf("tool %s permission should be agent-neutral: %+v", definition.ID, permission)
			}
		}
	}
}

func TestDefaultResourceCatalogDoesNotRestoreDeletedStandardResources(t *testing.T) {
	ctx := context.Background()
	tools := toolres.NewMemoryRegistry()
	skills := skillres.NewMemoryRegistry()
	if err := ensureDefaultResourceCatalog(ctx, tools, skills); err != nil {
		t.Fatalf("ensure defaults: %v", err)
	}
	deletedTool := defaultSystemToolDefinitions()[0].ID
	deletedSkill := defaultStandardSkillDefinitions()[0].ID
	if err := tools.Delete(ctx, deletedTool); err != nil {
		t.Fatalf("delete default tool: %v", err)
	}
	if err := skills.Delete(ctx, deletedSkill); err != nil {
		t.Fatalf("delete default skill: %v", err)
	}

	if err := ensureDefaultResourceCatalog(ctx, tools, skills); err != nil {
		t.Fatalf("ensure defaults after delete: %v", err)
	}

	if _, err := tools.Get(ctx, deletedTool); !errors.Is(err, toolres.ErrNotFound) {
		t.Fatalf("deleted default tool was restored, err=%v", err)
	}
	if _, err := skills.Get(ctx, deletedSkill); !errors.Is(err, skillres.ErrNotFound) {
		t.Fatalf("deleted default skill was restored, err=%v", err)
	}
	if err := tools.Register(ctx, toolres.ToolDefinition{ID: deletedTool, Kind: toolres.KindInternalServiceTool, Name: "Restored Tool", Version: "1.0.0", Enabled: true}); err != nil {
		t.Fatalf("explicit tool restore should clear deletion marker: %v", err)
	}
	if err := skills.Register(ctx, skillres.SkillDefinition{ID: deletedSkill, Name: "restored-skill", Version: "1.0.0", Enabled: true}); err != nil {
		t.Fatalf("explicit skill restore should clear deletion marker: %v", err)
	}
	if toolDeleted, err := tools.IsDeleted(ctx, deletedTool); err != nil || toolDeleted {
		t.Fatalf("tool deletion marker after explicit restore = %v err=%v", toolDeleted, err)
	}
	if skillDeleted, err := skills.IsDeleted(ctx, deletedSkill); err != nil || skillDeleted {
		t.Fatalf("skill deletion marker after explicit restore = %v err=%v", skillDeleted, err)
	}
}

func TestDefaultResourceCatalogSeedsSystemMCPServer(t *testing.T) {
	ctx := context.Background()
	tools := toolres.NewMemoryRegistry()
	skills := skillres.NewMemoryRegistry()
	if err := ensureDefaultResourceCatalog(ctx, tools, skills); err != nil {
		t.Fatalf("ensure defaults: %v", err)
	}

	definition, err := tools.Get(ctx, agentcore.ToolID("mcp.system"))
	if err != nil {
		t.Fatalf("default system mcp server was not registered: %v", err)
	}
	if definition.Kind != toolres.KindMCPServer || definition.MCP == nil {
		t.Fatalf("system mcp definition = %+v", definition)
	}
	if definition.MCP.ServerName != "orrery-system-tools" || definition.MCP.Transport != toolres.TransportStreamHTTP {
		t.Fatalf("system mcp transport = %+v", definition.MCP)
	}
	exposed := map[string]bool{}
	for _, name := range definition.MCP.ExposedTools {
		exposed[name] = true
	}
	for _, want := range []agentcore.ToolID{
		toolstd.MissionPrepare,
		toolstd.ScheduledTaskCreate,
		toolstd.ScheduledTaskRunNow,
		toolstd.KnowledgeSearch,
	} {
		if !exposed[string(want)] {
			t.Fatalf("system mcp server missing exposed tool %s: %+v", want, definition.MCP.ExposedTools)
		}
	}
	for _, deprecated := range []agentcore.ToolID{toolstd.MissionContextList, toolstd.MissionContextSelect, toolstd.MissionTaskDelegate} {
		if exposed[string(deprecated)] {
			t.Fatalf("system mcp server should not expose deprecated tool %s: %+v", deprecated, definition.MCP.ExposedTools)
		}
	}
	if len(definition.MCP.ExposedToolDetails) != len(definition.MCP.ExposedTools) {
		t.Fatalf("system mcp tool details should mirror exposed tools: tools=%+v details=%+v", definition.MCP.ExposedTools, definition.MCP.ExposedToolDetails)
	}
}

func TestDefaultResourceCatalogSeedsLegalMCPServer(t *testing.T) {
	setTestConfigOverride(t, "integrations.legal.mcp_url", "")
	ctx := context.Background()
	tools := toolres.NewMemoryRegistry()
	skills := skillres.NewMemoryRegistry()
	if err := ensureDefaultResourceCatalog(ctx, tools, skills); err != nil {
		t.Fatalf("ensure defaults: %v", err)
	}

	definition, err := tools.Get(ctx, defaultLegalMCPServerToolID)
	if err != nil {
		t.Fatalf("default legal mcp server was not registered: %v", err)
	}
	if definition.Kind != toolres.KindMCPServer || definition.MCP == nil {
		t.Fatalf("legal mcp definition = %+v", definition)
	}
	if definition.MCP.ServerName != "ieado-legal" || definition.MCP.Transport != toolres.TransportStreamHTTP {
		t.Fatalf("legal mcp transport = %+v", definition.MCP)
	}
	if definition.MCP.URL != defaultLegalMCPDefaultURL {
		t.Fatalf("legal mcp url = %q, want %q", definition.MCP.URL, defaultLegalMCPDefaultURL)
	}
	exposed := map[string]bool{}
	for _, name := range definition.MCP.ExposedTools {
		exposed[name] = true
	}
	for _, want := range []string{"legal_enterprise_search", "legal_contract_review_run", "legal_intake_run"} {
		if !exposed[want] {
			t.Fatalf("legal mcp server missing exposed tool %s: %+v", want, definition.MCP.ExposedTools)
		}
	}
	if len(definition.MCP.ExposedTools) != 13 {
		t.Fatalf("legal mcp should expose compact 13-tool surface, got %d tools: %+v", len(definition.MCP.ExposedTools), definition.MCP.ExposedTools)
	}
	if len(definition.MCP.ExposedToolDetails) != len(definition.MCP.ExposedTools) {
		t.Fatalf("legal mcp tool details should mirror exposed tools: tools=%+v details=%+v", definition.MCP.ExposedTools, definition.MCP.ExposedToolDetails)
	}
}

func TestDefaultResourceCatalogAllowsLegalMCPURLOverride(t *testing.T) {
	ctx := context.Background()
	tools := toolres.NewMemoryRegistry()
	skills := skillres.NewMemoryRegistry()
	cfg := config.Config{Integrations: config.IntegrationsConfig{LegalMCPURL: " http://172.18.0.1:18093/mcp "}}
	if err := ensureDefaultResourceCatalogWithConfig(ctx, tools, skills, cfg); err != nil {
		t.Fatalf("ensure defaults: %v", err)
	}
	definition, err := tools.Get(ctx, defaultLegalMCPServerToolID)
	if err != nil {
		t.Fatalf("default legal mcp server was not registered: %v", err)
	}
	if definition.MCP == nil || definition.MCP.URL != "http://172.18.0.1:18093/mcp" {
		t.Fatalf("legal mcp url = %+v", definition.MCP)
	}
}

func TestDefaultResourceCatalogSeedsOrreryPublicReadOnlyMCPServer(t *testing.T) {
	t.Setenv("ORRERY_PUBLIC_MCP_URL", "")
	ctx := context.Background()
	tools := toolres.NewMemoryRegistry()
	skills := skillres.NewMemoryRegistry()
	if err := ensureDefaultResourceCatalog(ctx, tools, skills); err != nil {
		t.Fatalf("ensure defaults: %v", err)
	}

	definition, err := tools.Get(ctx, defaultOrreryPublicMCPServerToolID)
	if err != nil {
		t.Fatalf("default orrery public mcp server was not registered: %v", err)
	}
	if definition.Kind != toolres.KindMCPServer || definition.MCP == nil {
		t.Fatalf("orrery public mcp definition = %+v", definition)
	}
	if definition.MCP.ServerName != "orrery-public-readonly" || definition.MCP.Transport != toolres.TransportStreamHTTP {
		t.Fatalf("orrery public mcp transport = %+v", definition.MCP)
	}
	if definition.MCP.URL != defaultOrreryPublicMCPDefaultURL {
		t.Fatalf("orrery public mcp url = %q, want %q", definition.MCP.URL, defaultOrreryPublicMCPDefaultURL)
	}
	wantTools := []string{"search_documents", "get_document", "get_document_structure", "get_page_content"}
	if strings.Join(definition.MCP.ExposedTools, ",") != strings.Join(wantTools, ",") {
		t.Fatalf("orrery public mcp tools = %+v", definition.MCP.ExposedTools)
	}
	if len(definition.MCP.ExposedToolDetails) != len(wantTools) {
		t.Fatalf("orrery public mcp details = %+v", definition.MCP.ExposedToolDetails)
	}
	searchSchema := definition.MCP.ExposedToolDetails[0].InputSchema
	properties, ok := searchSchema["properties"].(map[string]any)
	if !ok || properties["query"] == nil || properties["folder_id"] != nil {
		t.Fatalf("search schema must expose query without folder_id: %+v", searchSchema)
	}
	if definition.MCP.Auth.Mode != toolres.MCPAuthModeGlobal || !definition.MCP.Auth.Required || !definition.MCP.Auth.DefaultCredentialConfigured {
		t.Fatalf("orrery public mcp auth = %+v", definition.MCP.Auth)
	}
	if got := definition.MCP.Headers["Authorization"]; got != "Bearer ${secret://mcp/orrery-public-readonly/api-key}" {
		t.Fatalf("orrery public authorization header = %q", got)
	}
	if len(definition.Permissions) != 1 || definition.Permissions[0].Scope != defaultOrreryPublicMCPPermissionScope {
		t.Fatalf("orrery public permissions = %+v", definition.Permissions)
	}
}

func TestDefaultResourceCatalogSeedsWebsiteCustomerMCPAndInternalCurationTools(t *testing.T) {
	ctx := context.Background()
	tools := toolres.NewMemoryRegistry()
	if err := ensureDefaultResourceCatalog(ctx, tools, skillres.NewMemoryRegistry()); err != nil {
		t.Fatalf("ensure defaults: %v", err)
	}
	customer, err := tools.Get(ctx, defaultOrreryCustomerMCPServerToolID)
	if err != nil || customer.MCP == nil {
		t.Fatalf("customer MCP = %+v err=%v", customer, err)
	}
	if customer.Permissions[0].Scope != defaultOrreryCustomerMCPPermissionScope || strings.Contains(strings.Join(customer.MCP.ExposedTools, ","), "write") {
		t.Fatalf("customer MCP boundary = %+v", customer)
	}
	if customer.MCP.CallTimeoutSeconds != 60 {
		t.Fatalf("customer MCP timeout = %d, want 60", customer.MCP.CallTimeoutSeconds)
	}
	if customer.MCP.URL != defaultOrreryCustomerMCPDefaultURL {
		t.Fatalf("customer MCP url = %q, want %q", customer.MCP.URL, defaultOrreryCustomerMCPDefaultURL)
	}
	for _, id := range []agentcore.ToolID{defaultWebsiteKnowledgeGapListToolID, defaultWebsiteDistillationCandidatesListToolID, defaultWebsiteFAQCatalogListToolID, defaultWebsiteFAQMetricsListToolID, defaultWebsiteKnowledgeProposalCreateToolID} {
		definition, err := tools.Get(ctx, id)
		if err != nil || definition.Kind != toolres.KindInternalServiceTool || len(definition.Permissions) != 1 || definition.Permissions[0].Scope != defaultWebsiteKnowledgeCurationMCPPermissionScope {
			t.Fatalf("curation internal tool %s = %+v err=%v", id, definition, err)
		}
		if strings.Contains(strings.ToLower(definition.Name+definition.Description), "publish") && id == defaultWebsiteKnowledgeProposalCreateToolID {
			t.Fatalf("curation tool exposes publish authority: %+v", definition)
		}
	}
}

func TestDefaultResourceCatalogSeedsWebsiteMarketingLeadMCPWhenConfigured(t *testing.T) {
	t.Setenv("ORRERY_WEBSITE_MARKETING_MCP_COMMAND", " /usr/bin/node ")
	t.Setenv("ORRERY_WEBSITE_MARKETING_MCP_ENTRYPOINT", " /opt/marketing/dist/cli.js ")
	ctx := context.Background()
	tools := toolres.NewMemoryRegistry()
	if err := ensureDefaultResourceCatalog(ctx, tools, skillres.NewMemoryRegistry()); err != nil {
		t.Fatalf("ensure defaults: %v", err)
	}

	definition, err := tools.Get(ctx, agentcore.ToolID("mcp.marketing"))
	if err != nil {
		t.Fatalf("website marketing MCP was not registered: %v", err)
	}
	if definition.Kind != toolres.KindMCPServer || definition.MCP == nil {
		t.Fatalf("website marketing MCP definition = %+v", definition)
	}
	if definition.MCP.Transport != toolres.TransportStdio || definition.MCP.Command != "/usr/bin/node" {
		t.Fatalf("website marketing MCP transport = %+v", definition.MCP)
	}
	if got := strings.Join(definition.MCP.Args, ","); got != "/opt/marketing/dist/cli.js" {
		t.Fatalf("website marketing MCP args = %q", got)
	}
	if got := strings.Join(definition.MCP.ExposedTools, ","); got != "marketing_lead_capture_create" {
		t.Fatalf("website marketing MCP exposed tools = %q", got)
	}
	if len(definition.MCP.EnvSecretRefs) != 1 || definition.MCP.EnvSecretRefs[0].Name != "mcp_marketing_database_url" || definition.MCP.EnvSecretRefs[0].Env != "MARKETING_MCP_DATABASE_URL" {
		t.Fatalf("website marketing MCP database secret = %+v", definition.MCP.EnvSecretRefs)
	}
}

func TestDefaultResourceCatalogAllowsOrreryPublicMCPURLOverride(t *testing.T) {
	t.Setenv("ORRERY_PUBLIC_MCP_URL", " http://172.18.0.1:18094/mcp ")
	ctx := context.Background()
	tools := toolres.NewMemoryRegistry()
	skills := skillres.NewMemoryRegistry()
	if err := ensureDefaultResourceCatalog(ctx, tools, skills); err != nil {
		t.Fatalf("ensure defaults: %v", err)
	}
	definition, err := tools.Get(ctx, defaultOrreryPublicMCPServerToolID)
	if err != nil {
		t.Fatalf("default orrery public mcp server was not registered: %v", err)
	}
	if definition.MCP == nil || definition.MCP.URL != "http://172.18.0.1:18094/mcp" {
		t.Fatalf("orrery public mcp url = %+v", definition.MCP)
	}
}

func TestDefaultSkillCatalogWithPackageStoreIsIdempotent(t *testing.T) {
	ctx := context.Background()
	skills := skillres.NewMemoryRegistry()
	store := skillres.NewPackageStore(t.TempDir())

	if err := ensureDefaultSkillCatalog(ctx, skills, store); err != nil {
		t.Fatalf("first ensure defaults: %v", err)
	}
	if err := ensureDefaultSkillCatalog(ctx, skills, store); err != nil {
		t.Fatalf("second ensure defaults should update existing standard skills: %v", err)
	}

	if _, err := skills.Get(ctx, agentcore.SkillID("skill.frontend-design")); err != nil {
		t.Fatalf("frontend design standard skill should use canonical id: %v", err)
	}
	webResearch, err := skills.Get(ctx, skillstd.WebResearch)
	if err != nil {
		t.Fatalf("web research standard skill should be registered: %v", err)
	}
	if webResearch.RiskLevel != skillres.RiskLow || len(webResearch.RequiredTools) != 2 {
		t.Fatalf("web research standard skill = %+v", webResearch)
	}
}

func TestDefaultSkillCatalogRefreshesPackageRiskOnceAtStartup(t *testing.T) {
	ctx := context.Background()
	skills := skillres.NewMemoryRegistry()
	store := skillres.NewPackageStore(filepath.Join(t.TempDir(), "skills"))
	definition, err := store.CreatePackage(ctx, "---\nname: party-government-document-generator\ndescription: test\nversion: 1.0.0\n---\n# Skill\n")
	if err != nil {
		t.Fatalf("create package: %v", err)
	}
	definition.RiskLevel = skillres.RiskLow
	if err := skills.Register(ctx, definition); err != nil {
		t.Fatalf("register stale skill: %v", err)
	}
	if _, _, err := store.WriteFile(ctx, definition.ID, "generate_document.exe", []byte("binary")); err != nil {
		t.Fatalf("write executable: %v", err)
	}

	if err := ensureDefaultSkillCatalog(ctx, skills, store); err != nil {
		t.Fatalf("ensure defaults: %v", err)
	}

	updated, err := skills.Get(ctx, definition.ID)
	if err != nil {
		t.Fatalf("get updated skill: %v", err)
	}
	if updated.RiskLevel != skillres.RiskHigh {
		t.Fatalf("risk = %q, want high", updated.RiskLevel)
	}
}

func TestDefaultResourceCatalogSupportsDeletedCheckerCapability(t *testing.T) {
	var _ interface {
		IsDeleted(context.Context, agentcore.ToolID) (bool, error)
	} = toolres.NewMemoryRegistry()
	var _ interface {
		IsDeleted(context.Context, agentcore.SkillID) (bool, error)
	} = skillres.NewMemoryRegistry()
}
