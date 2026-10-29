package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/operations"
	knowledgecore "orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/provider"
	pageindexprovider "orrery_v2/internal/knowledge/provider/pageindex"
	knowledgestore "orrery_v2/internal/knowledge/store"
	knowledgetestkit "orrery_v2/internal/knowledge/testkit"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testkit"
)

func TestEnsureDefaultCoordinatorKnowledgeBasesCreatesPrivateAgentDefaults(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	orgs := operations.NewOrganizationCatalogStore(env.DBRouter)
	cfg, err := config.LoadYAML(testConfigSource(t))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := ensureDefaultCoordinatorAgents(ctx, cfg, profiles, orgs); err != nil {
		t.Fatalf("ensure default agents: %v", err)
	}
	knowledgeRepo, knowledgeCommand := newTestKnowledgeCommand(t, env)

	if err := ensureDefaultCoordinatorKnowledgeBases(ctx, profiles, knowledgeCommand, knowledgeRepo, t.TempDir()); err != nil {
		t.Fatalf("ensure knowledge defaults: %v", err)
	}
	if err := ensureDefaultCoordinatorKnowledgeBases(ctx, profiles, knowledgeCommand, knowledgeRepo, t.TempDir()); err != nil {
		t.Fatalf("ensure knowledge defaults again: %v", err)
	}

	for _, profileID := range []string{defaultLeadAgentID, defaultCoordinatorAgentID, defaultQAPlanAgentID, defaultQACheckAgentID} {
		page, err := knowledgeRepo.ListKnowledgeBases(ctx, knowledgecore.KnowledgeBaseQuery{OwnerType: knowledgecore.OwnerAgent, OwnerID: profileID})
		if err != nil {
			t.Fatalf("list knowledge for %s: %v", profileID, err)
		}
		if page.Total != 1 {
			t.Fatalf("profile %s default knowledge count = %d, items=%+v", profileID, page.Total, page.Items)
		}
		base := page.Items[0]
		if !base.IsDefault || base.Visibility != knowledgecore.VisibilityPrivate || base.Status != knowledgecore.KnowledgeActive || base.VectorStorePath == "" {
			t.Fatalf("profile %s default knowledge mismatch: %+v", profileID, base)
		}
	}
}

func TestEnsureDefaultCoordinatorKnowledgeBasesCreatesPageIndexBackedDefaults(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	orgs := operations.NewOrganizationCatalogStore(env.DBRouter)
	cfg, err := config.LoadYAML(testConfigSource(t))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := ensureDefaultCoordinatorAgents(ctx, cfg, profiles, orgs); err != nil {
		t.Fatalf("ensure default agents: %v", err)
	}
	folderCreates := 0
	pageindex := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/folder/" {
			t.Fatalf("unexpected PageIndex request %s %s", r.Method, r.URL.Path)
		}
		folderCreates++
		_ = json.NewEncoder(w).Encode(map[string]string{
			"id":   "pageindex-folder-" + string(rune('a'+folderCreates-1)),
			"name": "folder",
		})
	}))
	defer pageindex.Close()
	knowledgeRepo, knowledgeCommand := newTestKnowledgeCommand(t, env)
	knowledgeCommand.WithProviderRegistry(provider.NewStaticRegistry(knowledgecore.ProviderPageIndex, pageindexprovider.New(pageindexprovider.Config{
		APIURL: pageindex.URL,
		APIKey: "test-pageindex-token",
	})))

	if err := ensureDefaultCoordinatorKnowledgeBases(ctx, profiles, knowledgeCommand, knowledgeRepo, t.TempDir()); err != nil {
		t.Fatalf("ensure PageIndex knowledge defaults: %v", err)
	}

	for _, profileID := range []string{defaultLeadAgentID, defaultCoordinatorAgentID, defaultQAPlanAgentID, defaultQACheckAgentID} {
		page, err := knowledgeRepo.ListKnowledgeBases(ctx, knowledgecore.KnowledgeBaseQuery{OwnerType: knowledgecore.OwnerAgent, OwnerID: profileID})
		if err != nil {
			t.Fatalf("list PageIndex knowledge for %s: %v", profileID, err)
		}
		if page.Total != 1 {
			t.Fatalf("profile %s PageIndex knowledge count = %d, items=%+v", profileID, page.Total, page.Items)
		}
		base := page.Items[0]
		if base.EffectiveProvider() != knowledgecore.ProviderPageIndex || base.ProviderResourceID == "" || base.VectorStorePath != "" {
			t.Fatalf("profile %s PageIndex default mismatch: %+v", profileID, base)
		}
	}
	if folderCreates != 4 {
		t.Fatalf("PageIndex folder creates = %d, want 4", folderCreates)
	}
}

func TestEnsureDefaultCoordinatorKnowledgeBasesPreservesProfileBinding(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	repo, command := newTestKnowledgeCommand(t, env)
	base, err := ensureDefaultKnowledgeBase(ctx, command, knowledgecore.OwnerAgent, defaultLeadAgentID, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile := defaultCoordinatorAgentProfile(defaultLeadAgentID, "lead", agentcore.RuntimeSpec{}, defaultAgentTemplate{})
	profile.PersonalKnowledgeBaseID = base.ID
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}

	if err := ensureDefaultCoordinatorKnowledgeBases(ctx, profiles, command, repo, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	page, err := repo.ListKnowledgeBases(ctx, knowledgecore.KnowledgeBaseQuery{OwnerType: knowledgecore.OwnerAgent, OwnerID: profile.ID.String()})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || string(page.Items[0].ID) != base.ID {
		t.Fatalf("knowledge bootstrap created duplicate bases: %+v", page.Items)
	}
}

func TestEnsureDefaultCoordinatorKnowledgeBasesRepairsDanglingProfileBinding(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	profile := defaultCoordinatorAgentProfile(defaultLeadAgentID, "lead", agentcore.RuntimeSpec{}, defaultAgentTemplate{})
	profile.PersonalKnowledgeBaseID = "kb-missing"
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	repo, command := newTestKnowledgeCommand(t, env)

	if err := ensureDefaultCoordinatorKnowledgeBases(ctx, profiles, command, repo, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	repaired, err := profiles.GetProfile(ctx, profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if repaired.PersonalKnowledgeBaseID == "" || repaired.PersonalKnowledgeBaseID == "kb-missing" {
		t.Fatalf("repaired knowledge binding = %q", repaired.PersonalKnowledgeBaseID)
	}
	base, err := repo.GetKnowledgeBase(ctx, knowledgecore.KnowledgeBaseID(repaired.PersonalKnowledgeBaseID))
	if err != nil {
		t.Fatal(err)
	}
	if base.OwnerType != knowledgecore.OwnerAgent || base.OwnerID != profile.ID.String() || !base.IsDefault {
		t.Fatalf("repaired knowledge base = %+v", base)
	}
}

func TestProductionHumanProfileBootstrapDoesNotCreatePrivateHumanKnowledge(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	knowledgeRepo, knowledgeCommand := newTestKnowledgeCommand(t, env)
	bootstrap := newProductionHumanProfileBootstrap(profiles).WithKnowledgeEnsurer(knowledgeCommand, t.TempDir())

	profile := defaultHumanAgentProfile("human-operator-user", "Operator User")
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("seed human profile: %v", err)
	}
	if _, err := bootstrap.EnsureHumanProfile(ctx, missioncontrol.EnsureHumanProfileCommand{HumanAgentID: profile.ID.String(), DisplayName: "Operator User"}); err != nil {
		t.Fatalf("resolve human profile: %v", err)
	}
	if _, err := bootstrap.EnsureViewerProfile(ctx, operations.ViewerRef{ViewerID: "Operator User", DisplayName: "Operator User", AgentProfileID: profile.ID.String()}); err != nil {
		t.Fatalf("ensure viewer profile again: %v", err)
	}

	page, err := knowledgeRepo.ListKnowledgeBases(ctx, knowledgecore.KnowledgeBaseQuery{OwnerType: knowledgecore.OwnerHuman, OwnerID: profile.ID.String()})
	if err != nil {
		t.Fatalf("list human knowledge: %v", err)
	}
	if page.Total != 0 {
		t.Fatalf("human default knowledge count = %d, items=%+v", page.Total, page.Items)
	}
}

func TestProductionHumanProfileBootstrapKeepsWebsiteGatewayViewerTransient(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	knowledgeRepo, knowledgeCommand := newTestKnowledgeCommand(t, env)
	bootstrap := newProductionHumanProfileBootstrap(profiles).WithKnowledgeEnsurer(knowledgeCommand, t.TempDir())

	profile, err := bootstrap.EnsureDiscussionHumanProfile(ctx, discussionsession.ViewerRef{
		ViewerID: "gateway-website-visitor123", ViewerKind: discussionsession.ViewerKindUser, DisplayName: "Website visitor",
	})
	if err != nil {
		t.Fatalf("ensure website viewer profile: %v", err)
	}
	if _, err := profiles.GetProfile(ctx, profile.ID); !errors.Is(err, agentcore.ErrNotFound) {
		t.Fatalf("website viewer profile must not be persisted, err=%v", err)
	}
	availability, err := profiles.GetAvailability(ctx, profile.ID)
	if err != nil {
		t.Fatalf("get website viewer availability: %v", err)
	}
	if availability.Status != agentcore.AvailabilityUnknown {
		t.Fatalf("website viewer availability = %q, want %q", availability.Status, agentcore.AvailabilityUnknown)
	}
	page, err := knowledgeRepo.ListKnowledgeBases(ctx, knowledgecore.KnowledgeBaseQuery{OwnerType: knowledgecore.OwnerHuman, OwnerID: profile.ID.String()})
	if err != nil {
		t.Fatalf("list website viewer knowledge: %v", err)
	}
	if page.Total != 0 {
		t.Fatalf("website viewer must not receive personal knowledge, count=%d items=%+v", page.Total, page.Items)
	}
}

func TestProductionHumanProfileBootstrapDoesNotCreateKnowledgeForDeletedHuman(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	knowledgeRepo, knowledgeCommand := newTestKnowledgeCommand(t, env)
	bootstrap := newProductionHumanProfileBootstrap(profiles).WithKnowledgeEnsurer(knowledgeCommand, t.TempDir())

	profile := defaultHumanAgentProfile("human-operator-user", "Operator User")
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("seed human profile: %v", err)
	}
	if err := profiles.DeleteProfile(ctx, profile.ID, "workforce.member.delete"); err != nil {
		t.Fatalf("delete profile: %v", err)
	}
	if _, err := bootstrap.EnsureHumanProfile(ctx, missioncontrol.EnsureHumanProfileCommand{HumanAgentID: profile.ID.String(), DisplayName: "Operator User"}); !errors.Is(err, agentcore.ErrNotFound) {
		t.Fatalf("resolve deleted human error = %v, want ErrNotFound", err)
	}

	page, err := knowledgeRepo.ListKnowledgeBases(ctx, knowledgecore.KnowledgeBaseQuery{OwnerType: knowledgecore.OwnerHuman, OwnerID: profile.ID.String()})
	if err != nil {
		t.Fatalf("list human knowledge: %v", err)
	}
	if page.Total != 0 {
		t.Fatalf("deleted human should not create another knowledge base, count=%d items=%+v", page.Total, page.Items)
	}
}

func TestDefaultCoordinatorKnowledgeDoesNotModifyMemoryProfiles(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	orgs := operations.NewOrganizationCatalogStore(env.DBRouter)
	cfg, err := config.LoadYAML(testConfigSource(t))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := ensureDefaultCoordinatorAgents(ctx, cfg, profiles, orgs); err != nil {
		t.Fatalf("ensure default agents: %v", err)
	}
	coreStore, err := env.Databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	knowledgeRepo, knowledgeCommand := newTestKnowledgeCommand(t, env)

	if err := ensureDefaultCoordinatorKnowledgeBases(ctx, profiles, knowledgeCommand, knowledgeRepo, t.TempDir()); err != nil {
		t.Fatalf("ensure knowledge defaults: %v", err)
	}

	if _, ok, err := coreStore.GetRecord(ctx, "memory_profiles", "memory-"+defaultLeadAgentID); err != nil {
		t.Fatalf("get memory record: %v", err)
	} else if ok {
		t.Fatalf("knowledge bootstrap should not create memory profile records")
	}
}

func newTestKnowledgeCommand(t *testing.T, env *testkit.Env) (*knowledgestore.PlatformStore, *knowledgeadmin.CommandService) {
	t.Helper()
	coreStore, err := env.Databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	repo := knowledgestore.NewPlatformStore(coreStore)
	vectorStore := knowledgetestkit.NewFakeVectorStore(t.TempDir())
	command := knowledgeadmin.NewCommandService(repo, vectorStore, knowledgeIDGenerator{generator: env.IDGenerator}, env.Clock)
	return repo, command
}
