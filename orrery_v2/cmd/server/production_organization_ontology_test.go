package main

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
	toolres "orrery_v2/internal/resource/tool"
)

func TestProductionCreateOrganizationWritesOntologyContextGraph(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-organization-ontology"},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	defer databases.Close()
	core, ok := databases.Get(db.RoleCore)
	if !ok || core == nil {
		t.Fatalf("core database missing")
	}
	coordinator := migration.NewCoordinator(databases)
	if err := ontology.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register ontology migrations: %v", err)
	}
	if err := contextgraph.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register context graph migrations: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("migrate core db: %v", err)
	}
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, ontology.NewPlatformRegistry(core)); err != nil {
		t.Fatalf("install default ontology: %v", err)
	}
	ontologyRunner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatalf("new production ontology runner: %v", err)
	}
	orgCatalog := operations.NewOrganizationCatalogStore(db.NewRouter(databases))
	command := operations.NewCommandService(&organizationOntologyProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}})
	command.SetOrganizationCatalogStore(orgCatalog)
	command.SetIDGenerator(sequenceOperationsIDs{"org-1"})
	query := operations.NewQueryService(&organizationOntologyProfileStore{}, toolres.NewMemoryRegistry())
	query.SetOrganizationCatalogStore(orgCatalog)
	api := productionAPI{
		operations:      operations.NewService(query, command),
		ontologyProject: ontologyRunner,
		ontologyEvents:  newProductionOntologyEventLog(core),
	}

	organization, err := api.CreateOrganization(ctx, operations.ViewerRef{
		ViewerID:        "admin-1",
		ViewerKind:      "user",
		PermissionScope: []string{operations.PermissionOrgManage},
	}, operations.CreateOrganizationCommand{Name: "Platform"})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}

	objects, _, facts, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, organization.OrganizationID)
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	assertContextGraphObject(t, objects, "organization", organization.OrganizationID)
	assertContextGraphFact(t, facts, "organization", organization.OrganizationID, "name", "Platform")
	assertContextGraphFact(t, facts, "organization", organization.OrganizationID, "status", "active")
	assertContextGraphFact(t, facts, "organization", organization.OrganizationID, "code", "01")
	assertContextGraphEvidence(t, evidence, "organization-created:"+organization.OrganizationID)
	events, err := api.ontologyEvents.List(ctx)
	if err != nil {
		t.Fatalf("list ontology events: %v", err)
	}
	assertOntologyEvent(t, events, "organization-created:"+organization.OrganizationID, "organization.created")
}

type organizationOntologyProfileStore struct {
	profiles map[agentcore.AgentProfileID]agentcore.AgentProfile
}

func (s *organizationOntologyProfileStore) GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	profile, ok := s.profiles[id]
	if !ok {
		return agentcore.AgentProfile{}, operations.ErrNotFound
	}
	return profile, nil
}

func (s *organizationOntologyProfileStore) SaveProfile(ctx context.Context, profile agentcore.AgentProfile) error {
	if s.profiles == nil {
		s.profiles = map[agentcore.AgentProfileID]agentcore.AgentProfile{}
	}
	s.profiles[profile.ID] = profile
	return nil
}

func (s *organizationOntologyProfileStore) SaveAvailability(ctx context.Context, availability agentcore.AgentAvailability) error {
	return nil
}

func (s *organizationOntologyProfileStore) QueryDirectory(ctx context.Context, q agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	entries := make([]agentcore.AgentDirectoryEntry, 0, len(s.profiles))
	for _, profile := range s.profiles {
		if q.Organization != "" && profile.OrgIdentity.OrganizationRef.ID != q.Organization {
			continue
		}
		if q.Department != "" && profile.OrgIdentity.DepartmentRef.ID != q.Department {
			continue
		}
		if q.Position != "" && profile.OrgIdentity.PositionRef.ID != q.Position {
			continue
		}
		if q.EnabledOnly && !profile.Enabled {
			continue
		}
		if q.Placement == agentcore.TalentPlacementFilterInternal && !profile.Placement.IsInternal(profile.OrgIdentity) {
			continue
		}
		if q.Placement == agentcore.TalentPlacementFilterMarket && !profile.Placement.IsMarket(profile.OrgIdentity) {
			continue
		}
		entries = append(entries, agentcore.AgentDirectoryEntry{
			AgentProfileID:  profile.ID,
			AgentInstanceID: agentcore.AgentInstanceID(profile.ID.String()),
			RealName:        profile.RealName,
			DisplayName:     profile.DisplayName,
			AgentKind:       profile.AgentKind,
			OrgIdentity:     profile.OrgIdentity,
			Placement:       profile.Placement,
			RuntimeSpecSummary: agentcore.RuntimeSpecSummary{
				Kind:      profile.RuntimeSpec.Kind,
				ConfigRef: profile.RuntimeSpec.ConfigRef,
			},
			Enabled: profile.Enabled,
		})
	}
	return agentcore.AgentDirectoryPage{Entries: entries, Total: len(entries), Limit: q.Page.Limit, Offset: q.Page.Offset}, nil
}

type sequenceOperationsIDs []string

func (g sequenceOperationsIDs) NewID(prefix string) (string, error) {
	if len(g) == 0 {
		return prefix + "-1", nil
	}
	return g[0], nil
}
