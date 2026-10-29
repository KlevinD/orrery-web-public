package operations_test

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/app/operations"
	memcore "orrery_v2/internal/memory/core"
	toolres "orrery_v2/internal/resource/tool"
)

type fakeWorkforceProfileStore struct {
	profiles     map[agentcore.AgentProfileID]agentcore.AgentProfile
	availability map[agentcore.AgentProfileID]agentcore.AgentAvailability
	deleted      map[agentcore.AgentProfileID]bool
	saveError    error
	missingErr   error
}

func (f *fakeWorkforceProfileStore) GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	profile, ok := f.profiles[id]
	if !ok {
		if f.missingErr != nil {
			return agentcore.AgentProfile{}, f.missingErr
		}
		return agentcore.AgentProfile{}, operations.ErrNotFound
	}
	return profile, nil
}

func (f *fakeWorkforceProfileStore) SaveProfile(ctx context.Context, profile agentcore.AgentProfile) error {
	if f.saveError != nil {
		return f.saveError
	}
	if f.profiles == nil {
		f.profiles = map[agentcore.AgentProfileID]agentcore.AgentProfile{}
	}
	f.profiles[profile.ID] = profile
	return nil
}

func (f *fakeWorkforceProfileStore) SaveAvailability(ctx context.Context, availability agentcore.AgentAvailability) error {
	if f.availability == nil {
		f.availability = map[agentcore.AgentProfileID]agentcore.AgentAvailability{}
	}
	f.availability[availability.AgentProfileID] = availability
	return nil
}

func (f *fakeWorkforceProfileStore) DeleteProfile(ctx context.Context, id agentcore.AgentProfileID, reason string) error {
	if f.profiles == nil {
		f.profiles = map[agentcore.AgentProfileID]agentcore.AgentProfile{}
	}
	delete(f.profiles, id)
	if f.deleted == nil {
		f.deleted = map[agentcore.AgentProfileID]bool{}
	}
	f.deleted[id] = true
	return nil
}

func (f *fakeWorkforceProfileStore) GetAvailability(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentAvailability, error) {
	availability, ok := f.availability[id]
	if !ok {
		return agentcore.AgentAvailability{AgentProfileID: id, Status: agentcore.AvailabilityUnknown}, nil
	}
	return availability, nil
}

type fakeOperationsAuditRecorder struct {
	events []operations.AuditEventDTO
}

func (f *fakeOperationsAuditRecorder) RecordAuditEvent(ctx context.Context, event operations.AuditEventDTO) error {
	f.events = append(f.events, event)
	return nil
}

type fakeMemoryProfileStore struct {
	profiles map[agentcore.MemoryProfileID]memcore.MemoryProfile
}

func (f *fakeMemoryProfileStore) SaveProfile(ctx context.Context, profile memcore.MemoryProfile) error {
	if f.profiles == nil {
		f.profiles = map[agentcore.MemoryProfileID]memcore.MemoryProfile{}
	}
	f.profiles[profile.ID] = profile
	return nil
}

func (f *fakeMemoryProfileStore) GetProfile(ctx context.Context, id agentcore.MemoryProfileID) (memcore.MemoryProfile, error) {
	profile, ok := f.profiles[id]
	if !ok {
		return memcore.MemoryProfile{}, memcore.ErrNotFound
	}
	return profile, nil
}

type fakeKnowledgeLifecycle struct {
	next        int
	created     []fakeKnowledgeCreate
	deleted     []fakeKnowledgeDelete
	createError error
	deleteError error
}

type fakeKnowledgeCreate struct {
	viewer knowledgeadmin.ViewerRef
	cmd    knowledgeadmin.CreateKnowledgeBaseCommand
	id     string
}

type fakeKnowledgeDelete struct {
	viewer knowledgeadmin.ViewerRef
	id     string
}

func (f *fakeKnowledgeLifecycle) CreateKnowledgeBase(ctx context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.CreateKnowledgeBaseCommand) (knowledgeadmin.KnowledgeBaseDTO, error) {
	if f.createError != nil {
		return knowledgeadmin.KnowledgeBaseDTO{}, f.createError
	}
	f.next++
	id := "kb-fixture-" + regexp.MustCompile(`[^a-zA-Z0-9]+`).ReplaceAllString(cmd.OwnerID, "-")
	if cmd.OwnerID == "" {
		id = "kb-fixture"
	}
	if f.next > 1 {
		id = fmt.Sprintf("%s-%d", id, f.next)
	}
	f.created = append(f.created, fakeKnowledgeCreate{viewer: viewer, cmd: cmd, id: id})
	return knowledgeadmin.KnowledgeBaseDTO{ID: id, Name: cmd.Name, OwnerType: cmd.OwnerType, OwnerID: cmd.OwnerID, Visibility: cmd.Visibility}, nil
}

func (f *fakeKnowledgeLifecycle) DeleteKnowledgeBase(ctx context.Context, viewer knowledgeadmin.ViewerRef, id string) error {
	if f.deleteError != nil {
		return f.deleteError
	}
	f.deleted = append(f.deleted, fakeKnowledgeDelete{viewer: viewer, id: id})
	return nil
}

func assertOpaqueGeneratedID(t *testing.T, label, prefix, value string) {
	t.Helper()
	if !regexp.MustCompile("^" + regexp.QuoteMeta(prefix) + "-[0-9a-f]{32}$").MatchString(value) {
		t.Fatalf("%s = %q, want %s-<32 lowercase hex chars>", label, value, prefix)
	}
	if regexp.MustCompile("^" + regexp.QuoteMeta(prefix) + `-\d{14}\.\d{6,9}$`).MatchString(value) {
		t.Fatalf("%s = %q, timestamp ids are not allowed", label, value)
	}
}

type fakeOrganizationCatalogStore struct {
	organizations map[string]operations.OrganizationDTO
	departments   map[string]operations.DepartmentDTO
	positions     map[string]operations.PositionDTO
	saveOrgError  error
	savePosError  error
}

func newFakeOrganizationCatalogStore() *fakeOrganizationCatalogStore {
	return &fakeOrganizationCatalogStore{
		organizations: map[string]operations.OrganizationDTO{},
		departments:   map[string]operations.DepartmentDTO{},
		positions:     map[string]operations.PositionDTO{},
	}
}

func seedOrganizationCatalog(t *testing.T, ctx context.Context, orgs *fakeOrganizationCatalogStore, organizationID string, departments []operations.DepartmentDTO, positions []operations.PositionDTO) {
	t.Helper()
	defaultDepartmentID := ""
	if len(departments) > 0 {
		defaultDepartmentID = departments[0].DepartmentID
	}
	defaultPositionID := ""
	if len(positions) > 0 {
		defaultPositionID = positions[0].PositionID
	}
	if err := orgs.SaveOrganization(ctx, operations.OrganizationDTO{
		OrganizationID:      organizationID,
		Name:                organizationID,
		DefaultDepartmentID: defaultDepartmentID,
		DefaultPositionID:   defaultPositionID,
	}); err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	for _, department := range departments {
		if err := orgs.SaveDepartment(ctx, department); err != nil {
			t.Fatalf("seed department %s: %v", department.DepartmentID, err)
		}
	}
	for _, position := range positions {
		if err := orgs.SavePosition(ctx, position); err != nil {
			t.Fatalf("seed position %s: %v", position.PositionID, err)
		}
	}
}

func (f *fakeOrganizationCatalogStore) SaveOrganization(ctx context.Context, organization operations.OrganizationDTO) error {
	if f.saveOrgError != nil {
		return f.saveOrgError
	}
	f.organizations[organization.OrganizationID] = organization
	return nil
}

func (f *fakeOrganizationCatalogStore) SaveDepartment(ctx context.Context, department operations.DepartmentDTO) error {
	f.departments[department.DepartmentID] = department
	return nil
}

func (f *fakeOrganizationCatalogStore) SavePosition(ctx context.Context, position operations.PositionDTO) error {
	if f.savePosError != nil {
		return f.savePosError
	}
	f.positions[position.PositionID] = position
	return nil
}

func (f *fakeOrganizationCatalogStore) DeleteOrganization(ctx context.Context, organizationID string) error {
	delete(f.organizations, organizationID)
	return nil
}

func (f *fakeOrganizationCatalogStore) DeleteDepartment(ctx context.Context, departmentID string) error {
	delete(f.departments, departmentID)
	return nil
}

func (f *fakeOrganizationCatalogStore) DeletePosition(ctx context.Context, positionID string) error {
	delete(f.positions, positionID)
	return nil
}

func (f *fakeOrganizationCatalogStore) ListOrganizations(ctx context.Context, query operations.OrgGraphQuery) (operations.OrgGraphDTO, error) {
	var graph operations.OrgGraphDTO
	for _, organization := range f.organizations {
		if query.OrganizationID != "" && organization.OrganizationID != query.OrganizationID {
			continue
		}
		graph.Organizations = append(graph.Organizations, organization)
	}
	for _, department := range f.departments {
		if query.OrganizationID != "" && department.OrganizationID != query.OrganizationID {
			continue
		}
		graph.Departments = append(graph.Departments, department)
	}
	for _, position := range f.positions {
		department := f.departments[position.DepartmentID]
		if query.OrganizationID != "" && department.OrganizationID != query.OrganizationID {
			continue
		}
		graph.Positions = append(graph.Positions, position)
	}
	return graph, nil
}

type fakeEmptyAgentDirectory struct{}

func (fakeEmptyAgentDirectory) QueryDirectory(ctx context.Context, q agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	return agentcore.AgentDirectoryPage{}, nil
}

func (f *fakeWorkforceProfileStore) QueryDirectory(ctx context.Context, q agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	entries := make([]agentcore.AgentDirectoryEntry, 0, len(f.profiles))
	for _, profile := range f.profiles {
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
		if len(q.Roles) > 0 && !testProfileHasAnyRole(profile, q.Roles) {
			continue
		}
		entries = append(entries, entryFromTestProfile(profile))
	}
	return agentcore.AgentDirectoryPage{Entries: entries, Total: len(entries)}, nil
}

func testProfileHasAnyRole(profile agentcore.AgentProfile, roles []agentcore.RoleID) bool {
	wanted := map[agentcore.RoleID]bool{}
	for _, role := range roles {
		wanted[role] = true
	}
	for _, role := range profile.OrgIdentity.RoleRefs {
		if wanted[role.ID] {
			return true
		}
	}
	return false
}

func entryFromTestProfile(profile agentcore.AgentProfile) agentcore.AgentDirectoryEntry {
	return agentcore.AgentDirectoryEntry{
		AgentProfileID:  profile.ID,
		AgentInstanceID: agentcore.AgentInstanceID(profile.ID.String() + ":fixture"),
		RealName:        profile.RealName,
		DisplayName:     profile.DisplayName,
		AgentKind:       profile.AgentKind,
		OrgIdentity:     profile.OrgIdentity,
		Enabled:         profile.Enabled,
	}
}

type fakeEmptyToolRegistry struct{}

func (fakeEmptyToolRegistry) List(ctx context.Context) ([]toolres.ToolDefinition, error) {
	return nil, nil
}

func containsChinese(value string) bool {
	for _, r := range value {
		if r >= '\u4e00' && r <= '\u9fff' {
			return true
		}
	}
	return false
}
