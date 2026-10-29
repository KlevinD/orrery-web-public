package developmentproject

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/rebac"
	"orrery_v2/internal/app/scmconnection"
	"orrery_v2/internal/platform/db"
)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type sequenceIDs struct {
	mu   sync.Mutex
	next int
}

func (g *sequenceIDs) NewIDContext() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.next++
	return "generated-" + string(rune('a'+g.next-1)), nil
}

type auditRecorder struct {
	events []AuditEvent
	err    error
}

type flakyProjectRelationSink struct {
	delegate  *authorization.Service
	writeErr  error
	deleteErr error
}

func (s *flakyProjectRelationSink) WriteRelations(ctx context.Context, tuples []authorization.RelationTuple) error {
	if s.writeErr != nil {
		return s.writeErr
	}
	return s.delegate.WriteRelations(ctx, tuples)
}

func (s *flakyProjectRelationSink) DeleteRelations(ctx context.Context, tuples []authorization.RelationTuple) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	return s.delegate.DeleteRelations(ctx, tuples)
}

func (r *auditRecorder) RecordDevelopmentProjectAudit(_ context.Context, event AuditEvent) error {
	r.events = append(r.events, event)
	return r.err
}

type connectionCatalog struct {
	connection   scmconnection.Connection
	repositories []scmconnection.RepositoryMetadata
	err          error
}

func (c connectionCatalog) GetConnectionForUse(context.Context, scmconnection.ViewerRef, string) (scmconnection.Connection, error) {
	if c.err != nil {
		return scmconnection.Connection{}, c.err
	}
	return c.connection, nil
}

func (c connectionCatalog) ListRepositories(context.Context, scmconnection.ViewerRef, string) ([]scmconnection.RepositoryMetadata, error) {
	if c.err != nil {
		return nil, c.err
	}
	return append([]scmconnection.RepositoryMetadata(nil), c.repositories...), nil
}

func (c connectionCatalog) GetConnectionForExecution(context.Context, string, string) (scmconnection.Connection, error) {
	if c.err != nil {
		return scmconnection.Connection{}, c.err
	}
	return c.connection, nil
}

type countingConnectionCatalog struct {
	connection   scmconnection.Connection
	repositories []scmconnection.RepositoryMetadata
	err          error
	getCalls     int
	listCalls    int
}

func (c *countingConnectionCatalog) GetConnectionForUse(context.Context, scmconnection.ViewerRef, string) (scmconnection.Connection, error) {
	c.getCalls++
	if c.err != nil {
		return scmconnection.Connection{}, c.err
	}
	return c.connection, nil
}

func (c *countingConnectionCatalog) ListRepositories(context.Context, scmconnection.ViewerRef, string) ([]scmconnection.RepositoryMetadata, error) {
	c.listCalls++
	if c.err != nil {
		return nil, c.err
	}
	return append([]scmconnection.RepositoryMetadata(nil), c.repositories...), nil
}

type testEnvironment struct {
	repo       Repository
	router     *db.Router
	authorizer *authorization.Service
	service    *Service
	viewer     ViewerRef
	close      func()
}

func TestServiceIsolatesSameProjectIDAcrossTenants(t *testing.T) {
	env := newTestEnvironment(t)
	defer env.close()
	for _, tenantID := range []string{"tenant-a", "tenant-b"} {
		viewer := testViewer(tenantID, "owner")
		writePlatformMember(t, env.authorizer, viewer)
		created, err := env.service.Create(context.Background(), viewer, CreateCommand{DevelopmentProjectID: "shared", Name: "Project " + tenantID})
		if err != nil {
			t.Fatalf("Create(%s): %v", tenantID, err)
		}
		if created.DevelopmentProjectID != "shared" {
			t.Fatalf("created = %+v", created)
		}
	}
	a, err := env.repo.Get(context.Background(), "tenant-a", "shared")
	if err != nil {
		t.Fatalf("Get tenant-a: %v", err)
	}
	b, err := env.repo.Get(context.Background(), "tenant-b", "shared")
	if err != nil {
		t.Fatalf("Get tenant-b: %v", err)
	}
	if a.Name == b.Name || a.TenantID != "tenant-a" || b.TenantID != "tenant-b" {
		t.Fatalf("tenant records collided: a=%+v b=%+v", a, b)
	}
	if _, err := env.service.Get(context.Background(), testViewer("tenant-c", "owner"), "shared"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant Get error = %v, want ErrNotFound", err)
	}
}

func TestCreateRejectsUnsafeResourceAndMemberIdentifiers(t *testing.T) {
	env := newTestEnvironment(t)
	defer env.close()
	writePlatformMember(t, env.authorizer, env.viewer)

	for _, projectID := range []string{"nested/project", `nested\project`, "project\nother", strings.Repeat("p", 257)} {
		if _, err := env.service.Create(context.Background(), env.viewer, CreateCommand{DevelopmentProjectID: projectID, Name: "Unsafe"}); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("Create(%q) error = %v, want ErrInvalidRequest", projectID, err)
		}
	}

	project, err := env.service.Create(context.Background(), env.viewer, CreateCommand{DevelopmentProjectID: "safe-identifiers", Name: "Safe"})
	if err != nil {
		t.Fatalf("Create safe project: %v", err)
	}
	for _, subjectID := range []string{"", "member\nother", strings.Repeat("s", 257)} {
		if _, _, err := env.service.AddMember(context.Background(), env.viewer, AddMemberCommand{
			DevelopmentProjectID: project.DevelopmentProjectID, ExpectedProjectVersion: project.Version,
			SubjectKind: "user", SubjectID: subjectID, Role: MemberRoleViewer,
		}); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("AddMember(%q) error = %v, want ErrInvalidRequest", subjectID, err)
		}
	}
}

func TestCreateIsIdempotentAndRejectsKeyReuseWithDifferentRequest(t *testing.T) {
	env := newTestEnvironment(t)
	defer env.close()
	writePlatformMember(t, env.authorizer, env.viewer)
	ctx := context.Background()

	command := CreateCommand{IdempotencyKey: "create-project-1", Name: "Idempotent project"}
	first, err := env.service.Create(ctx, env.viewer, command)
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}
	second, err := env.service.Create(ctx, env.viewer, command)
	if err != nil {
		t.Fatalf("replayed Create: %v", err)
	}
	if second.DevelopmentProjectID != first.DevelopmentProjectID || second.Version != first.Version {
		t.Fatalf("replayed project = %+v, want %+v", second, first)
	}

	command.Name = "Different project"
	if _, err := env.service.Create(ctx, env.viewer, command); !errors.Is(err, ErrIdempotency) {
		t.Fatalf("conflicting Create error = %v, want ErrIdempotency", err)
	}
}

func TestProjectMutationsAreIdempotentAndDoNotRepeatProviderCalls(t *testing.T) {
	env := newCreatedEnvironment(t, "project-mutation-idempotency")
	defer env.close()
	ctx := context.Background()
	catalog := &countingConnectionCatalog{
		connection: scmconnection.Connection{ConnectionID: "connection-1", Status: scmconnection.StatusActive},
		repositories: []scmconnection.RepositoryMetadata{{
			RepositoryLocator: "managed-local:repository", DisplayName: "repository", DefaultBranch: "main",
			HeadCommit: strings.Repeat("a", 40), ReadOnly: true,
		}},
	}
	env.service.SetSCMConnections(catalog)

	updatedName := "Updated project"
	update := UpdateCommand{IdempotencyKey: "project-update-1", DevelopmentProjectID: "project-mutation-idempotency", ExpectedVersion: 1, Name: &updatedName}
	updated, err := env.service.Update(ctx, env.viewer, update)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	replayedUpdate, err := env.service.Update(ctx, env.viewer, update)
	if err != nil || replayedUpdate.Version != updated.Version {
		t.Fatalf("replayed Update: dto=%+v err=%v", replayedUpdate, err)
	}
	differentName := "Different project"
	conflictingUpdate := update
	conflictingUpdate.Name = &differentName
	if _, err := env.service.Update(ctx, env.viewer, conflictingUpdate); !errors.Is(err, ErrIdempotency) {
		t.Fatalf("conflicting Update error = %v, want ErrIdempotency", err)
	}

	add := AddMemberCommand{
		IdempotencyKey: "member-add-1", DevelopmentProjectID: updated.DevelopmentProjectID,
		ExpectedProjectVersion: updated.Version, SubjectKind: "user", SubjectID: "reviewer-1", Role: MemberRoleViewer,
	}
	member, afterAdd, err := env.service.AddMember(ctx, env.viewer, add)
	if err != nil {
		t.Fatalf("AddMember: %v", err)
	}
	replayedMember, replayedAdd, err := env.service.AddMember(ctx, env.viewer, add)
	if err != nil || replayedMember != member || replayedAdd.Version != afterAdd.Version {
		t.Fatalf("replayed AddMember: member=%+v project=%+v err=%v", replayedMember, replayedAdd, err)
	}

	updateMember := UpdateMemberCommand{
		IdempotencyKey: "member-update-1", DevelopmentProjectID: updated.DevelopmentProjectID, MemberID: member.MemberID,
		ExpectedProjectVersion: afterAdd.Version, ExpectedMemberVersion: member.Version, Role: MemberRoleContributor,
	}
	member, afterMemberUpdate, err := env.service.UpdateMember(ctx, env.viewer, updateMember)
	if err != nil {
		t.Fatalf("UpdateMember: %v", err)
	}
	replayedMember, replayedMemberUpdate, err := env.service.UpdateMember(ctx, env.viewer, updateMember)
	if err != nil || replayedMember != member || replayedMemberUpdate.Version != afterMemberUpdate.Version {
		t.Fatalf("replayed UpdateMember: member=%+v project=%+v err=%v", replayedMember, replayedMemberUpdate, err)
	}

	removeMember := RemoveMemberCommand{
		IdempotencyKey: "member-remove-1", DevelopmentProjectID: updated.DevelopmentProjectID, MemberID: member.MemberID,
		ExpectedProjectVersion: afterMemberUpdate.Version, ExpectedMemberVersion: member.Version,
	}
	afterMemberRemove, err := env.service.RemoveMember(ctx, env.viewer, removeMember)
	if err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	replayedMemberRemove, err := env.service.RemoveMember(ctx, env.viewer, removeMember)
	if err != nil || replayedMemberRemove.Version != afterMemberRemove.Version {
		t.Fatalf("replayed RemoveMember: project=%+v err=%v", replayedMemberRemove, err)
	}

	bind := BindRepositoryCommand{
		IdempotencyKey: "repository-bind-1", DevelopmentProjectID: updated.DevelopmentProjectID,
		ExpectedProjectVersion: afterMemberRemove.Version, ConnectionID: "connection-1", RepositoryLocator: "managed-local:repository",
		Role: RepositoryRoleDependency, AccessMode: AccessReadOnly,
	}
	binding, afterBind, err := env.service.BindRepository(ctx, env.viewer, bind)
	if err != nil {
		t.Fatalf("BindRepository: %v", err)
	}
	replayedBinding, replayedBind, err := env.service.BindRepository(ctx, env.viewer, bind)
	if err != nil || replayedBinding.BindingID != binding.BindingID || replayedBind.Version != afterBind.Version || catalog.getCalls != 1 || catalog.listCalls != 1 {
		t.Fatalf("replayed BindRepository: binding=%+v project=%+v calls=%d/%d err=%v", replayedBinding, replayedBind, catalog.getCalls, catalog.listCalls, err)
	}

	sourceDirectory := "src"
	updateRepository := UpdateRepositoryCommand{
		IdempotencyKey: "repository-update-1", DevelopmentProjectID: updated.DevelopmentProjectID, BindingID: binding.BindingID,
		ExpectedProjectVersion: afterBind.Version, ExpectedBindingVersion: binding.Version, SourceSubdirectory: &sourceDirectory,
	}
	binding, afterRepositoryUpdate, err := env.service.UpdateRepository(ctx, env.viewer, updateRepository)
	if err != nil {
		t.Fatalf("UpdateRepository: %v", err)
	}
	replayedBinding, replayedRepositoryUpdate, err := env.service.UpdateRepository(ctx, env.viewer, updateRepository)
	if err != nil || replayedBinding.BindingID != binding.BindingID || replayedBinding.Version != binding.Version || replayedBinding.SourceSubdirectory != binding.SourceSubdirectory || replayedRepositoryUpdate.Version != afterRepositoryUpdate.Version {
		t.Fatalf("replayed UpdateRepository: binding=%+v project=%+v err=%v", replayedBinding, replayedRepositoryUpdate, err)
	}

	verify := VerifyRepositoryCommand{
		IdempotencyKey: "repository-verify-1", DevelopmentProjectID: updated.DevelopmentProjectID, BindingID: binding.BindingID,
		ExpectedProjectVersion: afterRepositoryUpdate.Version, ExpectedBindingVersion: binding.Version,
	}
	binding, afterVerify, err := env.service.VerifyRepository(ctx, env.viewer, verify)
	if err != nil {
		t.Fatalf("VerifyRepository: %v", err)
	}
	replayedBinding, replayedVerify, err := env.service.VerifyRepository(ctx, env.viewer, verify)
	if err != nil || replayedBinding.BindingID != binding.BindingID || replayedBinding.Version != binding.Version || replayedBinding.LastFetchedCommit != binding.LastFetchedCommit || replayedVerify.Version != afterVerify.Version || catalog.getCalls != 2 || catalog.listCalls != 2 {
		t.Fatalf("replayed VerifyRepository: binding=%+v project=%+v calls=%d/%d err=%v", replayedBinding, replayedVerify, catalog.getCalls, catalog.listCalls, err)
	}

	removeRepository := RemoveRepositoryCommand{
		IdempotencyKey: "repository-remove-1", DevelopmentProjectID: updated.DevelopmentProjectID, BindingID: binding.BindingID,
		ExpectedProjectVersion: afterVerify.Version, ExpectedBindingVersion: binding.Version,
	}
	afterRemove, err := env.service.RemoveRepository(ctx, env.viewer, removeRepository)
	if err != nil {
		t.Fatalf("RemoveRepository: %v", err)
	}
	replayedRemove, err := env.service.RemoveRepository(ctx, env.viewer, removeRepository)
	if err != nil || replayedRemove.Version != afterRemove.Version {
		t.Fatalf("replayed RemoveRepository: project=%+v err=%v", replayedRemove, err)
	}

	archive := ArchiveCommand{IdempotencyKey: "project-archive-1", DevelopmentProjectID: updated.DevelopmentProjectID, ExpectedVersion: afterRemove.Version}
	archived, err := env.service.Archive(ctx, env.viewer, archive)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	replayedArchive, err := env.service.Archive(ctx, env.viewer, archive)
	if err != nil || replayedArchive.Version != archived.Version || replayedArchive.Status != StatusArchived {
		t.Fatalf("replayed Archive: project=%+v err=%v", replayedArchive, err)
	}
}

func TestRepositoryConfigurationNormalizesPortableSourceSubdirectory(t *testing.T) {
	env := newTestEnvironment(t)
	defer env.close()
	env.service.SetSCMConnections(connectionCatalog{
		connection: scmconnection.Connection{ConnectionID: "connection-1", Status: scmconnection.StatusActive},
		repositories: []scmconnection.RepositoryMetadata{{
			RepositoryLocator: "repository-1", DisplayName: "Repository", DefaultBranch: "main",
			HeadCommit: strings.Repeat("a", 40), ReadOnly: true,
		}},
	})
	writePlatformMember(t, env.authorizer, env.viewer)
	project, err := env.service.Create(context.Background(), env.viewer, CreateCommand{DevelopmentProjectID: "portable-repository-path", Name: "Portable"})
	if err != nil {
		t.Fatalf("Create project: %v", err)
	}
	binding, _, err := env.service.BindRepository(context.Background(), env.viewer, BindRepositoryCommand{
		DevelopmentProjectID: project.DevelopmentProjectID, ExpectedProjectVersion: project.Version,
		ConnectionID: "connection-1", RepositoryLocator: "repository-1", Role: RepositoryRoleDependency,
		AccessMode: AccessReadOnly, SourceSubdirectory: `src\service\..\api`,
	})
	if err != nil {
		t.Fatalf("BindRepository: %v", err)
	}
	if binding.SourceSubdirectory != "src/api" {
		t.Fatalf("SourceSubdirectory = %q, want src/api", binding.SourceSubdirectory)
	}
}

func TestBindRepositoryRejectsInvalidProviderMetadata(t *testing.T) {
	for _, metadata := range []scmconnection.RepositoryMetadata{
		{RepositoryLocator: "repository-1", DisplayName: "Repository", DefaultBranch: "../main", HeadCommit: strings.Repeat("a", 40)},
		{RepositoryLocator: "repository-1", DisplayName: "Repository\nInjected", DefaultBranch: "main", HeadCommit: strings.Repeat("a", 40)},
		{RepositoryLocator: "repository-1", DisplayName: "Repository", DefaultBranch: "main", HeadCommit: "not-a-commit"},
	} {
		env := newTestEnvironment(t)
		writePlatformMember(t, env.authorizer, env.viewer)
		env.service.SetSCMConnections(connectionCatalog{
			connection:   scmconnection.Connection{ConnectionID: "connection-1", Status: scmconnection.StatusActive},
			repositories: []scmconnection.RepositoryMetadata{metadata},
		})
		project, err := env.service.Create(context.Background(), env.viewer, CreateCommand{DevelopmentProjectID: "provider-metadata", Name: "Metadata"})
		if err != nil {
			env.close()
			t.Fatalf("Create project: %v", err)
		}
		_, _, err = env.service.BindRepository(context.Background(), env.viewer, BindRepositoryCommand{
			DevelopmentProjectID: project.DevelopmentProjectID, ExpectedProjectVersion: project.Version,
			ConnectionID: "connection-1", RepositoryLocator: "repository-1", Role: RepositoryRoleDependency, AccessMode: AccessReadOnly,
		})
		env.close()
		if !errors.Is(err, ErrRepositoryPolicy) {
			t.Fatalf("BindRepository metadata=%+v error = %v, want ErrRepositoryPolicy", metadata, err)
		}
	}
}

func TestAgentCannotOwnDevelopmentProject(t *testing.T) {
	env := newTestEnvironment(t)
	defer env.close()
	viewer := testViewer("tenant-a", "automation-agent")
	viewer.ViewerKind = identity.ViewerKindAgent
	if err := env.authorizer.WriteRelations(context.Background(), []authorization.RelationTuple{{
		TenantID: viewer.TenantID,
		Subject:  authorization.SubjectRef{Kind: viewer.ViewerKind, ID: viewer.ViewerID},
		Relation: "member",
		Object:   identity.ObjectRef{Type: "platform", ID: "global", TenantID: viewer.TenantID},
	}}); err != nil {
		t.Fatalf("write agent platform member: %v", err)
	}

	_, err := env.service.Create(context.Background(), viewer, CreateCommand{DevelopmentProjectID: "agent-owned", Name: "Agent owned"})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Create agent-owned project error = %v, want ErrInvalidRequest", err)
	}
	if _, err := env.repo.Get(context.Background(), viewer.TenantID, "agent-owned"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("agent-owned project persisted: %v", err)
	}
}

func TestProjectEffectIDsAreTenantScoped(t *testing.T) {
	service := NewService(nil, nil, fixedClock{now: time.Date(2026, 7, 25, 4, 0, 0, 0, time.UTC)})
	viewer := testViewer("tenant-a", "owner")
	projectA := Project{TenantID: "tenant-a", DevelopmentProjectID: "shared", Version: 1}
	projectB := Project{TenantID: "tenant-b", DevelopmentProjectID: "shared", Version: 1}
	effectA := service.projectAuditEffect(projectA, viewer, "development_project.created", "succeeded", "created", nil)
	effectB := service.projectAuditEffect(projectB, viewer, "development_project.created", "succeeded", "created", nil)
	if effectA.EffectID == effectB.EffectID || effectA.Audit == nil || effectB.Audit == nil || effectA.Audit.AuditID == effectB.Audit.AuditID {
		t.Fatalf("tenant audit effects collided: a=%+v b=%+v", effectA, effectB)
	}
}

func TestCapabilitiesAreComputedFromScopesAndProjectRelations(t *testing.T) {
	env := newCreatedEnvironment(t, "project-roles")
	defer env.close()
	project, err := env.repo.Get(context.Background(), env.viewer.TenantID, "project-roles")
	if err != nil {
		t.Fatalf("Get project: %v", err)
	}
	contributor, _, err := env.service.AddMember(context.Background(), env.viewer, AddMemberCommand{
		DevelopmentProjectID: "project-roles", ExpectedProjectVersion: project.Version,
		SubjectKind: "user", SubjectID: "contributor", Role: MemberRoleContributor,
	})
	if err != nil {
		t.Fatalf("Add contributor: %v", err)
	}
	project, _ = env.repo.Get(context.Background(), env.viewer.TenantID, "project-roles")
	_, _, err = env.service.AddMember(context.Background(), env.viewer, AddMemberCommand{
		DevelopmentProjectID: "project-roles", ExpectedProjectVersion: project.Version,
		SubjectKind: "user", SubjectID: "maintainer", Role: MemberRoleMaintainer,
	})
	if err != nil {
		t.Fatalf("Add maintainer: %v", err)
	}

	contributorView, err := env.service.Get(context.Background(), testViewer(env.viewer.TenantID, contributor.SubjectID), "project-roles")
	if err != nil {
		t.Fatalf("Get contributor: %v", err)
	}
	if !contributorView.Capabilities.CanView || !contributorView.Capabilities.CanUpdate || !contributorView.Capabilities.CanRequestExecution || !contributorView.Capabilities.CanSubmit {
		t.Fatalf("contributor capabilities = %+v", contributorView.Capabilities)
	}
	if contributorView.Capabilities.CanMonitor || contributorView.Capabilities.CanConfigure || contributorView.Capabilities.CanManageMembers || contributorView.Capabilities.CanApprove || contributorView.Capabilities.CanMerge {
		t.Fatalf("contributor capabilities are over-privileged: %+v", contributorView.Capabilities)
	}

	maintainerView, err := env.service.Get(context.Background(), testViewer(env.viewer.TenantID, "maintainer"), "project-roles")
	if err != nil {
		t.Fatalf("Get maintainer: %v", err)
	}
	if !maintainerView.Capabilities.CanMonitor || !maintainerView.Capabilities.CanConfigure || !maintainerView.Capabilities.CanApprove || maintainerView.Capabilities.CanManageMembers || maintainerView.Capabilities.CanMerge {
		t.Fatalf("maintainer capabilities = %+v", maintainerView.Capabilities)
	}

	ownerView, err := env.service.Get(context.Background(), env.viewer, "project-roles")
	if err != nil {
		t.Fatalf("Get owner: %v", err)
	}
	if !ownerView.Capabilities.CanMonitor || !ownerView.Capabilities.CanManageMembers || !ownerView.Capabilities.CanArchive || !ownerView.Capabilities.CanMerge {
		t.Fatalf("owner capabilities = %+v", ownerView.Capabilities)
	}
}

func TestListExecutionsAllowsProjectViewerSummaryWithoutMonitorCapability(t *testing.T) {
	env := newCreatedEnvironment(t, "project-monitoring")
	defer env.close()
	ctx := context.Background()
	project, err := env.repo.Get(ctx, env.viewer.TenantID, "project-monitoring")
	if err != nil {
		t.Fatalf("Get project: %v", err)
	}
	member, _, err := env.service.AddMember(ctx, env.viewer, AddMemberCommand{
		DevelopmentProjectID: project.DevelopmentProjectID, ExpectedProjectVersion: project.Version,
		SubjectKind: "user", SubjectID: "summary-viewer", Role: MemberRoleViewer,
	})
	if err != nil {
		t.Fatalf("Add viewer: %v", err)
	}
	monitor := &executionMonitorStub{page: ExecutionPage{Items: []ExecutionSummary{{TodoID: "todo-1"}}}}
	env.service.SetExecutionMonitor(monitor)
	page, err := env.service.ListExecutions(ctx, testViewer(env.viewer.TenantID, member.SubjectID), project.DevelopmentProjectID, ListExecutionsQuery{Limit: 25})
	if err != nil {
		t.Fatalf("ListExecutions: %v", err)
	}
	if monitor.canMonitor || monitor.project.DevelopmentProjectID != project.DevelopmentProjectID || len(page.Items) != 1 {
		t.Fatalf("viewer execution summary = %+v, monitor = %+v", page, monitor)
	}
	if _, err := env.service.ListExecutions(ctx, testViewer("tenant-b", member.SubjectID), project.DevelopmentProjectID, ListExecutionsQuery{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant ListExecutions error = %v, want ErrNotFound", err)
	}
}

type executionMonitorStub struct {
	project    Project
	canMonitor bool
	page       ExecutionPage
}

func (s *executionMonitorStub) ListProjectExecutions(_ context.Context, _ ViewerRef, project Project, canMonitor bool, _ ListExecutionsQuery) (ExecutionPage, error) {
	s.project, s.canMonitor = project, canMonitor
	return s.page, nil
}

func TestNonConfigurerCannotReadExecutionCommandsOrPolicyRanges(t *testing.T) {
	env := newTestEnvironment(t)
	defer env.close()
	writePlatformMember(t, env.authorizer, env.viewer)
	if _, err := env.service.Create(context.Background(), env.viewer, CreateCommand{
		DevelopmentProjectID: "project-redacted-config", Name: "Redacted config",
		ExecutionPolicy: ExecutionPolicy{
			NetworkMode: NetworkRestricted, AllowedHosts: []string{"api.example.com"},
			AllowedCommandPrefixes: []string{"go test"}, AllowedFileScopes: []string{"orrery_v2"},
		},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	project, err := env.repo.Get(context.Background(), env.viewer.TenantID, "project-redacted-config")
	if err != nil {
		t.Fatalf("Get project: %v", err)
	}
	member, _, err := env.service.AddMember(context.Background(), env.viewer, AddMemberCommand{
		DevelopmentProjectID: project.DevelopmentProjectID, ExpectedProjectVersion: project.Version,
		SubjectKind: "user", SubjectID: "contributor-redacted", Role: MemberRoleContributor,
	})
	if err != nil {
		t.Fatalf("AddMember: %v", err)
	}
	project, err = env.repo.Get(context.Background(), env.viewer.TenantID, project.DevelopmentProjectID)
	if err != nil {
		t.Fatalf("Get project after member: %v", err)
	}
	project.RepositoryBindings = []RepositoryBinding{{
		BindingID: "binding-redacted", ConnectionID: "connection-redacted",
		RepositoryLocator: "managed-local:repository", DisplayName: "repository",
		Role: RepositoryRolePrimary, AccessMode: AccessReadWrite, DefaultBranch: "main",
		SourceSubdirectory: "orrery_v2", BuildCommands: []string{"go build ./..."},
		TestCommands: []string{"go test ./..."}, BranchPrefix: "orrery/todo/",
		Status: BindingStatusActive, LastFetchedCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		LastVerifiedAt: env.service.now(), CreatedAt: env.service.now(), UpdatedAt: env.service.now(), Version: 1,
	}}
	expectedVersion := project.Version
	project.Version++
	if err := env.repo.Update(context.Background(), project.TenantID, project, expectedVersion); err != nil {
		t.Fatalf("Update repository fixture: %v", err)
	}

	contributor := testViewer(env.viewer.TenantID, member.SubjectID)
	dto, err := env.service.Get(context.Background(), contributor, project.DevelopmentProjectID)
	if err != nil {
		t.Fatalf("Get as contributor: %v", err)
	}
	if len(dto.ExecutionPolicy.AllowedHosts) != 0 || len(dto.ExecutionPolicy.AllowedCommandPrefixes) != 0 || len(dto.ExecutionPolicy.AllowedFileScopes) != 0 {
		t.Fatalf("contributor execution policy leaked configuration: %+v", dto.ExecutionPolicy)
	}
	bindings, err := env.service.ListRepositories(context.Background(), contributor, project.DevelopmentProjectID)
	if err != nil {
		t.Fatalf("ListRepositories as contributor: %v", err)
	}
	if len(bindings) != 1 || len(bindings[0].BuildCommands) != 0 || len(bindings[0].TestCommands) != 0 {
		t.Fatalf("contributor repository commands leaked: %+v", bindings)
	}

	ownerDTO, err := env.service.Get(context.Background(), env.viewer, project.DevelopmentProjectID)
	if err != nil || len(ownerDTO.ExecutionPolicy.AllowedCommandPrefixes) != 1 {
		t.Fatalf("owner policy was redacted: dto=%+v err=%v", ownerDTO.ExecutionPolicy, err)
	}
	ownerBindings, err := env.service.ListRepositories(context.Background(), env.viewer, project.DevelopmentProjectID)
	if err != nil || len(ownerBindings) != 1 || len(ownerBindings[0].BuildCommands) != 1 || len(ownerBindings[0].TestCommands) != 1 {
		t.Fatalf("owner repository commands were redacted: bindings=%+v err=%v", ownerBindings, err)
	}
}

func TestProjectDTORedactsSecretReferences(t *testing.T) {
	env := newTestEnvironment(t)
	defer env.close()
	writePlatformMember(t, env.authorizer, env.viewer)
	dto, err := env.service.Create(context.Background(), env.viewer, CreateCommand{
		DevelopmentProjectID: "project-secrets", Name: "Secret policy",
		ExecutionPolicy: ExecutionPolicy{AllowedSecretRefs: []string{"secret://scm/token", "settings://provider/key"}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	payload, _ := json.Marshal(dto)
	if dto.ExecutionPolicy.AllowedSecretCount != 2 || stringContainsAny(string(payload), "secret://", "settings://", "allowed_secret_refs") {
		t.Fatalf("public DTO leaked secret refs: %s", payload)
	}
}

func TestPartialExecutionPolicyUpdatePreservesRedactedSecretReferences(t *testing.T) {
	env := newTestEnvironment(t)
	defer env.close()
	writePlatformMember(t, env.authorizer, env.viewer)
	if _, err := env.service.Create(context.Background(), env.viewer, CreateCommand{
		DevelopmentProjectID: "project-policy-patch", Name: "Policy",
		ExecutionPolicy: ExecutionPolicy{NetworkMode: NetworkNone, AllowedSecretRefs: []string{"secret://scm/token"}},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	networkMode := NetworkAllowed
	if _, err := env.service.Update(context.Background(), env.viewer, UpdateCommand{
		DevelopmentProjectID: "project-policy-patch", ExpectedVersion: 1,
		ExecutionPolicy: &ExecutionPolicyPatch{NetworkMode: &networkMode},
	}); err != nil {
		t.Fatalf("Update policy: %v", err)
	}
	stored, err := env.repo.Get(context.Background(), env.viewer.TenantID, "project-policy-patch")
	if err != nil || stored.ExecutionPolicy.NetworkMode != NetworkAllowed || len(stored.ExecutionPolicy.AllowedSecretRefs) != 1 || stored.ExecutionPolicy.AllowedSecretRefs[0] != "secret://scm/token" {
		t.Fatalf("stored policy = %+v, err=%v", stored.ExecutionPolicy, err)
	}
}

func TestRepositoryBindingRequiresConnectionUse(t *testing.T) {
	env := newCreatedEnvironment(t, "project-connection")
	defer env.close()
	env.service.SetSCMConnections(connectionCatalog{err: scmconnection.ErrNotFound})

	_, _, err := env.service.BindRepository(context.Background(), env.viewer, BindRepositoryCommand{
		DevelopmentProjectID: "project-connection", ExpectedProjectVersion: 1,
		ConnectionID: "not-authorized", RepositoryLocator: "managed-local:repository",
		Role: RepositoryRolePrimary, AccessMode: AccessReadWrite,
	})
	if !errors.Is(err, scmconnection.ErrNotFound) {
		t.Fatalf("BindRepository error = %v, want scmconnection.ErrNotFound", err)
	}
	project, getErr := env.repo.Get(context.Background(), env.viewer.TenantID, "project-connection")
	if getErr != nil || len(project.RepositoryBindings) != 0 {
		t.Fatalf("binding persisted without connection.use: project=%+v err=%v", project, getErr)
	}
}

func TestRepositoryVerificationFailsWhenDefaultBranchChanges(t *testing.T) {
	env := newCreatedEnvironment(t, "project-branch-change")
	defer env.close()
	catalog := connectionCatalog{
		connection: scmconnection.Connection{ConnectionID: "connection-1", Status: scmconnection.StatusActive},
		repositories: []scmconnection.RepositoryMetadata{{
			RepositoryLocator: "managed-local:repository", DisplayName: "repository", DefaultBranch: "main",
			HeadCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}},
	}
	env.service.SetSCMConnections(catalog)
	binding, project, err := env.service.BindRepository(context.Background(), env.viewer, BindRepositoryCommand{
		DevelopmentProjectID: "project-branch-change", ExpectedProjectVersion: 1,
		BindingID: "binding-1", ConnectionID: "connection-1", RepositoryLocator: "managed-local:repository",
		Role: RepositoryRolePrimary, AccessMode: AccessReadWrite,
	})
	if err != nil {
		t.Fatalf("BindRepository: %v", err)
	}
	catalog.repositories[0].DefaultBranch = "trunk"
	env.service.SetSCMConnections(catalog)
	audit := &auditRecorder{}
	env.service.SetAuditRecorder(audit)

	verified, dto, err := env.service.VerifyRepository(context.Background(), env.viewer, VerifyRepositoryCommand{
		DevelopmentProjectID: project.DevelopmentProjectID, BindingID: binding.BindingID,
		ExpectedProjectVersion: project.Version, ExpectedBindingVersion: binding.Version,
	})
	if !errors.Is(err, ErrRepositoryPolicy) {
		t.Fatalf("VerifyRepository error = %v, want ErrRepositoryPolicy", err)
	}
	if verified.Status != BindingStatusError || verified.Version != 2 || dto.Version != 3 {
		t.Fatalf("failed verification result: binding=%+v project=%+v", verified, dto)
	}
	stored, getErr := env.repo.Get(context.Background(), env.viewer.TenantID, project.DevelopmentProjectID)
	if getErr != nil || len(stored.RepositoryBindings) != 1 || stored.RepositoryBindings[0].Status != BindingStatusError || stored.Version != 3 {
		t.Fatalf("stored failed verification: project=%+v err=%v", stored, getErr)
	}
	if len(audit.events) != 1 || audit.events[0].Action != "development_project.repository_verified" || audit.events[0].Result != "failed" {
		t.Fatalf("verification audit = %+v", audit.events)
	}
}

func TestFailedRepositoryVerificationReplayPreservesErrorCategory(t *testing.T) {
	env := newCreatedEnvironment(t, "project-verification-replay")
	defer env.close()
	catalog := &countingConnectionCatalog{
		connection: scmconnection.Connection{ConnectionID: "connection-1", Status: scmconnection.StatusActive},
		repositories: []scmconnection.RepositoryMetadata{{
			RepositoryLocator: "managed-local:repository", DisplayName: "repository", DefaultBranch: "main",
			HeadCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}},
	}
	env.service.SetSCMConnections(catalog)
	binding, project, err := env.service.BindRepository(context.Background(), env.viewer, BindRepositoryCommand{
		DevelopmentProjectID: "project-verification-replay", ExpectedProjectVersion: 1,
		BindingID: "binding-1", ConnectionID: "connection-1", RepositoryLocator: "managed-local:repository",
		Role: RepositoryRolePrimary, AccessMode: AccessReadWrite,
	})
	if err != nil {
		t.Fatalf("BindRepository: %v", err)
	}
	catalog.err = scmconnection.ErrDependency
	command := VerifyRepositoryCommand{
		IdempotencyKey: "test-idempotency-key", DevelopmentProjectID: project.DevelopmentProjectID,
		BindingID: binding.BindingID, ExpectedProjectVersion: project.Version, ExpectedBindingVersion: binding.Version,
	}
	firstBinding, firstProject, firstErr := env.service.VerifyRepository(context.Background(), env.viewer, command)
	if !errors.Is(firstErr, scmconnection.ErrDependency) {
		t.Fatalf("first VerifyRepository error = %v, want scmconnection.ErrDependency", firstErr)
	}
	getCalls, listCalls := catalog.getCalls, catalog.listCalls
	replayedBinding, replayedProject, replayedErr := env.service.VerifyRepository(context.Background(), env.viewer, command)
	if !errors.Is(replayedErr, scmconnection.ErrDependency) {
		t.Fatalf("replayed VerifyRepository error = %v, want scmconnection.ErrDependency", replayedErr)
	}
	if catalog.getCalls != getCalls || catalog.listCalls != listCalls {
		t.Fatalf("provider calls repeated on replay: before=%d/%d after=%d/%d", getCalls, listCalls, catalog.getCalls, catalog.listCalls)
	}
	if replayedBinding.BindingID != firstBinding.BindingID || replayedBinding.Version != firstBinding.Version || replayedBinding.Status != firstBinding.Status || replayedProject.Version != firstProject.Version {
		t.Fatalf("replayed result = %+v/%+v, want %+v/%+v", replayedBinding, replayedProject, firstBinding, firstProject)
	}

	stored, err := env.repo.Get(context.Background(), env.viewer.TenantID, project.DevelopmentProjectID)
	if err != nil {
		t.Fatalf("Get stored project: %v", err)
	}
	stored.MutationReceipts[len(stored.MutationReceipts)-1].VerificationFailureCode = "unknown_failure"
	if err := env.repo.Update(context.Background(), stored.TenantID, stored, stored.Version); err != nil {
		t.Fatalf("persist corrupt receipt: %v", err)
	}
	if _, err := env.repo.Get(context.Background(), stored.TenantID, stored.DevelopmentProjectID); !errors.Is(err, ErrDependency) {
		t.Fatalf("Get project with unknown failure category = %v, want ErrDependency", err)
	}
}

func TestOnlyOneWritablePrimaryCanBeBoundUnderConcurrency(t *testing.T) {
	env := newCreatedEnvironment(t, "project-repositories")
	defer env.close()
	env.service.SetSCMConnections(connectionCatalog{
		connection: scmconnection.Connection{ConnectionID: "connection-1", Status: scmconnection.StatusActive},
		repositories: []scmconnection.RepositoryMetadata{
			{RepositoryLocator: "managed-local:one", DisplayName: "one", DefaultBranch: "main", HeadCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
			{RepositoryLocator: "managed-local:two", DisplayName: "two", DefaultBranch: "main", HeadCommit: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
		},
	})

	commands := []BindRepositoryCommand{
		{DevelopmentProjectID: "project-repositories", ExpectedProjectVersion: 1, BindingID: "binding-1", ConnectionID: "connection-1", RepositoryLocator: "managed-local:one", Role: RepositoryRolePrimary, AccessMode: AccessReadWrite},
		{DevelopmentProjectID: "project-repositories", ExpectedProjectVersion: 1, BindingID: "binding-2", ConnectionID: "connection-1", RepositoryLocator: "managed-local:two", Role: RepositoryRolePrimary, AccessMode: AccessReadWrite},
	}
	start := make(chan struct{})
	errorsOut := make(chan error, len(commands))
	for _, command := range commands {
		command := command
		go func() {
			<-start
			_, _, err := env.service.BindRepository(context.Background(), env.viewer, command)
			errorsOut <- err
		}()
	}
	close(start)
	succeeded := 0
	conflicted := 0
	for range commands {
		err := <-errorsOut
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrConflict), errors.Is(err, ErrRepositoryPolicy):
			conflicted++
		default:
			t.Fatalf("unexpected BindRepository error: %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("results: succeeded=%d conflicted=%d", succeeded, conflicted)
	}
	project, err := env.repo.Get(context.Background(), env.viewer.TenantID, "project-repositories")
	if err != nil || len(project.RepositoryBindings) != 1 {
		t.Fatalf("project bindings = %+v, err=%v", project.RepositoryBindings, err)
	}
}

func TestReadOnlyRepositoryCannotBeBoundWithWriteAccess(t *testing.T) {
	env := newCreatedEnvironment(t, "project-read-only-repository")
	defer env.close()
	env.service.SetSCMConnections(connectionCatalog{
		connection: scmconnection.Connection{ConnectionID: "connection-1", Status: scmconnection.StatusActive},
		repositories: []scmconnection.RepositoryMetadata{{
			RepositoryLocator: "managed-local:read-only", DisplayName: "read-only", DefaultBranch: "main",
			HeadCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ReadOnly: true,
		}},
	})

	_, _, err := env.service.BindRepository(context.Background(), env.viewer, BindRepositoryCommand{
		DevelopmentProjectID: "project-read-only-repository", ExpectedProjectVersion: 1,
		ConnectionID: "connection-1", RepositoryLocator: "managed-local:read-only",
		Role: RepositoryRolePrimary, AccessMode: AccessReadWrite,
	})

	if !errors.Is(err, ErrRepositoryPolicy) {
		t.Fatalf("BindRepository error = %v, want ErrRepositoryPolicy", err)
	}
	stored, getErr := env.repo.Get(context.Background(), env.viewer.TenantID, "project-read-only-repository")
	if getErr != nil || len(stored.RepositoryBindings) != 0 {
		t.Fatalf("write binding persisted for read-only repository: project=%+v err=%v", stored, getErr)
	}
}

func TestServiceRejectsAmbiguousTenantKeySegment(t *testing.T) {
	env := newTestEnvironment(t)
	defer env.close()
	viewer := testViewer("tenant/ambiguous", "owner")

	_, err := env.service.Create(context.Background(), viewer, CreateCommand{Name: "Invalid tenant"})

	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Create error = %v, want ErrInvalidRequest", err)
	}
}

func TestArchivedProjectCannotStartExecutionOrChangeResources(t *testing.T) {
	env := newCreatedEnvironment(t, "project-archived")
	defer env.close()
	archived, err := env.service.Archive(context.Background(), env.viewer, ArchiveCommand{DevelopmentProjectID: "project-archived", ExpectedVersion: 1})
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if archived.Status != StatusArchived || archived.Capabilities.CanRequestExecution || archived.Capabilities.CanBindRepository {
		t.Fatalf("archived DTO = %+v", archived)
	}
	if err := env.service.EnsureExecutionAllowed(context.Background(), env.viewer, "project-archived"); !errors.Is(err, ErrArchived) {
		t.Fatalf("EnsureExecutionAllowed error = %v, want ErrArchived", err)
	}
}

func TestAuditFailureLeavesDurableProjectEffectAndConcurrentMutationRecovers(t *testing.T) {
	env := newCreatedEnvironment(t, "project-audit")
	defer env.close()
	auditFailure := errors.New("audit unavailable")
	env.service.SetAuditRecorder(&auditRecorder{err: auditFailure})
	changed := "Changed"
	if _, err := env.service.Update(context.Background(), env.viewer, UpdateCommand{
		DevelopmentProjectID: "project-audit", ExpectedVersion: 1, Name: &changed,
	}); !errors.Is(err, auditFailure) {
		t.Fatalf("Update audit error = %v", err)
	}
	stored, err := env.repo.Get(context.Background(), env.viewer.TenantID, "project-audit")
	if err != nil || stored.Version != 2 || stored.Name != changed || len(stored.PendingEffects) != 1 {
		t.Fatalf("durable project mutation = %+v, err=%v", stored, err)
	}

	description := "concurrent change"
	if _, err := env.service.Update(context.Background(), env.viewer, UpdateCommand{
		DevelopmentProjectID: "project-audit", ExpectedVersion: 2, Description: &description,
	}); !errors.Is(err, auditFailure) {
		t.Fatalf("concurrent Update audit error = %v", err)
	}
	stored, err = env.repo.Get(context.Background(), env.viewer.TenantID, "project-audit")
	if err != nil || stored.Version != 3 || stored.Name != changed || stored.Description != description || len(stored.PendingEffects) != 2 {
		t.Fatalf("concurrent mutation was lost: stored=%+v err=%v", stored, err)
	}

	recoveredAudit := &auditRecorder{}
	env.service.SetAuditRecorder(recoveredAudit)
	if err := env.service.ReconcilePendingEffects(context.Background(), 100); err != nil {
		t.Fatalf("ReconcilePendingEffects: %v", err)
	}
	if err := env.service.ReconcilePendingEffects(context.Background(), 100); err != nil {
		t.Fatalf("second ReconcilePendingEffects: %v", err)
	}
	stored, err = env.repo.Get(context.Background(), env.viewer.TenantID, "project-audit")
	if err != nil || stored.Version != 3 || len(stored.PendingEffects) != 0 {
		t.Fatalf("reconciled project = %+v, err=%v", stored, err)
	}
	if len(recoveredAudit.events) != 2 || recoveredAudit.events[0].AuditID == "" || recoveredAudit.events[0].AuditID == recoveredAudit.events[1].AuditID {
		t.Fatalf("recovered audits = %+v", recoveredAudit.events)
	}
}

func TestPendingEffectReconciliationAdvancesPastFailingProjects(t *testing.T) {
	env := newTestEnvironment(t)
	defer env.close()
	writePlatformMember(t, env.authorizer, env.viewer)
	auditFailure := errors.New("audit unavailable")
	env.service.SetAuditRecorder(&auditRecorder{err: auditFailure})
	for _, projectID := range []string{"project-fair-a", "project-fair-b", "project-fair-c"} {
		_, err := env.service.Create(context.Background(), env.viewer, CreateCommand{DevelopmentProjectID: projectID, Name: projectID})
		if !errors.Is(err, auditFailure) {
			t.Fatalf("Create(%s) error = %v, want audit failure", projectID, err)
		}
	}

	attempts := &auditRecorder{err: auditFailure}
	env.service.SetAuditRecorder(attempts)
	if err := env.service.ReconcilePendingEffects(context.Background(), 1); !errors.Is(err, auditFailure) {
		t.Fatalf("first ReconcilePendingEffects error = %v, want audit failure", err)
	}
	if err := env.service.ReconcilePendingEffects(context.Background(), 1); !errors.Is(err, auditFailure) {
		t.Fatalf("second ReconcilePendingEffects error = %v, want audit failure", err)
	}
	if len(attempts.events) != 2 || attempts.events[0].EntityID != "project-fair-a" || attempts.events[1].EntityID != "project-fair-b" {
		t.Fatalf("reconciliation attempts = %+v, want consecutive projects", attempts.events)
	}
}

func TestProjectCreationRelationFailureIsDurableAndRecoverable(t *testing.T) {
	env := newTestEnvironment(t)
	defer env.close()
	writePlatformMember(t, env.authorizer, env.viewer)
	relationFailure := errors.New("authorization unavailable")
	sink := &flakyProjectRelationSink{delegate: env.authorizer, writeErr: relationFailure}
	env.service.SetAuthorization(env.authorizer, authorization.NewRelationProjector(sink))

	_, err := env.service.Create(context.Background(), env.viewer, CreateCommand{DevelopmentProjectID: "project-relation-recovery", Name: "Relation recovery"})
	if !errors.Is(err, relationFailure) {
		t.Fatalf("Create relation error = %v", err)
	}
	stored, err := env.repo.Get(context.Background(), env.viewer.TenantID, "project-relation-recovery")
	if err != nil || stored.Version != 1 || len(stored.PendingEffects) != 2 {
		t.Fatalf("durable create = %+v, err=%v", stored, err)
	}

	sink.writeErr = nil
	if err := env.service.ReconcilePendingEffects(context.Background(), 100); err != nil {
		t.Fatalf("ReconcilePendingEffects: %v", err)
	}
	stored, err = env.repo.Get(context.Background(), env.viewer.TenantID, "project-relation-recovery")
	if err != nil || len(stored.PendingEffects) != 0 {
		t.Fatalf("reconciled create = %+v, err=%v", stored, err)
	}
	if _, err := env.service.Get(context.Background(), env.viewer, "project-relation-recovery"); err != nil {
		t.Fatalf("Get reconciled project: %v", err)
	}
}

func TestProjectMustRetainAnOwner(t *testing.T) {
	env := newCreatedEnvironment(t, "project-owner")
	defer env.close()
	project, _ := env.repo.Get(context.Background(), env.viewer.TenantID, "project-owner")
	owner := project.Members[0]
	_, err := env.service.RemoveMember(context.Background(), env.viewer, RemoveMemberCommand{
		DevelopmentProjectID: "project-owner", MemberID: owner.MemberID,
		ExpectedProjectVersion: project.Version, ExpectedMemberVersion: owner.Version,
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("RemoveMember last owner error = %v, want ErrInvalidRequest", err)
	}
}

func newCreatedEnvironment(t *testing.T, projectID string) testEnvironment {
	t.Helper()
	env := newTestEnvironment(t)
	writePlatformMember(t, env.authorizer, env.viewer)
	if _, err := env.service.Create(context.Background(), env.viewer, CreateCommand{DevelopmentProjectID: projectID, Name: "Development project"}); err != nil {
		env.close()
		t.Fatalf("Create project: %v", err)
	}
	return env
}

func newTestEnvironment(t *testing.T) testEnvironment {
	t.Helper()
	registry, err := db.OpenRegistry(context.Background(), db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "development-project-test"},
	}})
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	router := db.NewRouter(registry)
	repo := NewPlatformRepository(router)
	authorizer := authorization.NewService(authorization.NewReBACAdapter(rebac.NewMemoryStore(), nil))
	service := NewService(repo, &sequenceIDs{}, fixedClock{now: time.Date(2026, 7, 25, 4, 0, 0, 0, time.UTC)})
	service.SetAuthorization(authorizer, authorization.NewRelationProjector(authorizer))
	service.SetAuditRecorder(&auditRecorder{})
	service.SetSCMConnections(connectionCatalog{})
	service.SetSecretReferenceValidator(secretReferenceValidatorFunc(func(context.Context, string) error { return nil }))
	return testEnvironment{
		repo: repo, router: router, authorizer: authorizer, service: service, viewer: testViewer("tenant-a", "owner"),
		close: func() { _ = registry.Close() },
	}
}

type secretReferenceValidatorFunc func(context.Context, string) error

func (f secretReferenceValidatorFunc) ValidateSecretReference(ctx context.Context, ref string) error {
	return f(ctx, ref)
}

func TestExecutionPolicyRequiresResolvableAndSafeSecretReferences(t *testing.T) {
	env := newTestEnvironment(t)
	defer env.close()
	writePlatformMember(t, env.authorizer, env.viewer)
	missing := errors.New("secret missing")
	env.service.SetSecretReferenceValidator(secretReferenceValidatorFunc(func(context.Context, string) error { return missing }))

	_, err := env.service.Create(context.Background(), env.viewer, CreateCommand{
		DevelopmentProjectID: "project-unresolved-secret", Name: "Policy",
		ExecutionPolicy: ExecutionPolicy{AllowedSecretRefs: []string{"secret://scm/missing"}},
	})
	if !errors.Is(err, ErrInvalidRequest) || !strings.Contains(err.Error(), "cannot be resolved") {
		t.Fatalf("Create error = %v, want unresolved secret error", err)
	}
	for _, ref := range []string{"secret://scm/name@example", "settings://provider/key?version=1", "secret://" + strings.Repeat("a", 250)} {
		if validSecretRef(ref) {
			t.Fatalf("validSecretRef(%q) = true", ref)
		}
	}
}

func TestExecutionPolicyRejectsPortableTraversalAndUnboundedInputs(t *testing.T) {
	env := newTestEnvironment(t)
	defer env.close()
	writePlatformMember(t, env.authorizer, env.viewer)

	tooManyHosts := make([]string, 129)
	for index := range tooManyHosts {
		tooManyHosts[index] = "host-" + strconv.Itoa(index) + ".example"
	}
	tests := []struct {
		name   string
		policy ExecutionPolicy
	}{
		{name: "windows traversal", policy: ExecutionPolicy{AllowedFileScopes: []string{"..\\secret"}}},
		{name: "windows drive", policy: ExecutionPolicy{AllowedFileScopes: []string{"C:\\secret"}}},
		{name: "absolute scope", policy: ExecutionPolicy{AllowedFileScopes: []string{"/secret"}}},
		{name: "command control", policy: ExecutionPolicy{AllowedCommandPrefixes: []string{"go test\nrm"}}},
		{name: "command shell separator", policy: ExecutionPolicy{AllowedCommandPrefixes: []string{"go test; rm"}}},
		{name: "command execution flag", policy: ExecutionPolicy{AllowedCommandPrefixes: []string{"go test -exec=malicious"}}},
		{name: "command too long", policy: ExecutionPolicy{AllowedCommandPrefixes: []string{strings.Repeat("a", 513)}}},
		{name: "host URL", policy: ExecutionPolicy{NetworkMode: NetworkRestricted, AllowedHosts: []string{"https://example.com"}}},
		{name: "host bad wildcard", policy: ExecutionPolicy{NetworkMode: NetworkRestricted, AllowedHosts: []string{"api.*.example.com"}}},
		{name: "host bad port", policy: ExecutionPolicy{NetworkMode: NetworkRestricted, AllowedHosts: []string{"example.com:70000"}}},
		{name: "too many hosts", policy: ExecutionPolicy{NetworkMode: NetworkRestricted, AllowedHosts: tooManyHosts}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := env.service.Create(context.Background(), env.viewer, CreateCommand{DevelopmentProjectID: "invalid-" + strings.ReplaceAll(test.name, " ", "-"), Name: "Invalid policy", ExecutionPolicy: test.policy})
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("Create error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestExecutionPolicyCanonicalizesPortableFileScopesAndFailsClosedOnUpdate(t *testing.T) {
	env := newTestEnvironment(t)
	defer env.close()
	writePlatformMember(t, env.authorizer, env.viewer)
	created, err := env.service.Create(context.Background(), env.viewer, CreateCommand{
		DevelopmentProjectID: "portable-policy", Name: "Portable policy",
		ExecutionPolicy: ExecutionPolicy{NetworkMode: NetworkRestricted, AllowedHosts: []string{"API.Example.COM:443"}, AllowedFileScopes: []string{"src\\component"}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	stored, err := env.repo.Get(context.Background(), env.viewer.TenantID, created.DevelopmentProjectID)
	if err != nil || len(stored.ExecutionPolicy.AllowedFileScopes) != 1 || stored.ExecutionPolicy.AllowedFileScopes[0] != "src/component" || stored.ExecutionPolicy.AllowedHosts[0] != "api.example.com:443" {
		t.Fatalf("canonical policy = %+v, err=%v", stored.ExecutionPolicy, err)
	}
	invalidPrefixes := []string{"npm run check\x00ignored"}
	_, err = env.service.Update(context.Background(), env.viewer, UpdateCommand{
		DevelopmentProjectID: created.DevelopmentProjectID, ExpectedVersion: created.Version,
		ExecutionPolicy: &ExecutionPolicyPatch{AllowedCommandPrefixes: &invalidPrefixes},
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Update error = %v, want ErrInvalidRequest", err)
	}
	stored, err = env.repo.Get(context.Background(), env.viewer.TenantID, created.DevelopmentProjectID)
	if err != nil || stored.Version != created.Version || len(stored.ExecutionPolicy.AllowedCommandPrefixes) != 0 {
		t.Fatalf("invalid update mutated project: stored=%+v err=%v", stored, err)
	}
}

func testViewer(tenantID, userID string) ViewerRef {
	return ViewerRef{
		ViewerID: userID, ViewerKind: identity.ViewerKindUser, TenantID: tenantID,
		PermissionScope: []string{
			"development.read", "development.monitor", "development.create", "development.write", "development.execute",
			"development.submit", "development.approve", "development.manage", "development.merge",
			"connection.read", "connection.use",
		},
	}
}

func writePlatformMember(t *testing.T, authorizer *authorization.Service, viewer ViewerRef) {
	t.Helper()
	err := authorizer.WriteRelations(context.Background(), []authorization.RelationTuple{{
		TenantID: viewer.TenantID,
		Subject:  authorization.SubjectRef{Kind: identity.ViewerKindUser, ID: viewer.ViewerID}, Relation: "member",
		Object: identity.ObjectRef{Type: "platform", ID: "global", TenantID: viewer.TenantID},
	}})
	if err != nil {
		t.Fatalf("write platform member: %v", err)
	}
}

func stringContainsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
