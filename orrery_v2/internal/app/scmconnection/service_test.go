package scmconnection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/rebac"
	"orrery_v2/internal/platform/db"
)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type auditRecorder struct {
	events []AuditEvent
	err    error
}

type flakyConnectionRelationSink struct {
	delegate *authorization.Service
	writeErr error
}

func (s *flakyConnectionRelationSink) WriteRelations(ctx context.Context, tuples []authorization.RelationTuple) error {
	if s.writeErr != nil {
		return s.writeErr
	}
	return s.delegate.WriteRelations(ctx, tuples)
}

func (s *flakyConnectionRelationSink) DeleteRelations(ctx context.Context, tuples []authorization.RelationTuple) error {
	return s.delegate.DeleteRelations(ctx, tuples)
}

func (r *auditRecorder) RecordSCMConnectionAudit(_ context.Context, event AuditEvent) error {
	r.events = append(r.events, event)
	return r.err
}

type verificationStub struct {
	verification Verification
	repositories []RepositoryMetadata
	err          error
}

type countingVerificationStub struct {
	verification Verification
	calls        int
}

func (*countingVerificationStub) SupportsProvider(string) bool { return true }

func (v *countingVerificationStub) Verify(context.Context, Connection) (Verification, error) {
	v.calls++
	return v.verification, nil
}

func (*countingVerificationStub) ListRepositories(context.Context, Connection) ([]RepositoryMetadata, error) {
	return nil, nil
}

func (verificationStub) SupportsProvider(string) bool { return true }

func (v verificationStub) Verify(context.Context, Connection) (Verification, error) {
	return v.verification, v.err
}

func (v verificationStub) ListRepositories(context.Context, Connection) ([]RepositoryMetadata, error) {
	return append([]RepositoryMetadata(nil), v.repositories...), v.err
}

type managedLocalOnlyVerificationStub struct{ verificationStub }

func (managedLocalOnlyVerificationStub) SupportsProvider(provider string) bool {
	return provider == ProviderManagedLocal
}

func TestServiceIsolatesSameConnectionIDAcrossTenants(t *testing.T) {
	ctx := context.Background()
	repo, closeRepo := newTestRepository(t)
	defer closeRepo()
	store := rebac.NewMemoryStore()
	authorizer := authorization.NewService(authorization.NewReBACAdapter(store, nil))
	service := newAuthorizedService(repo, authorizer, &auditRecorder{})

	for _, tenantID := range []string{"tenant-a", "tenant-b"} {
		viewer := testViewer(tenantID, "owner")
		writePlatformMember(t, authorizer, viewer)
		created, err := service.Create(ctx, viewer, CreateCommand{
			ConnectionID: "shared", Scope: ScopePersonal, Provider: ProviderGenericGit,
			BaseURL: "https://git.example/" + tenantID, SecretRef: "secret://scm/" + tenantID,
		})
		if err != nil {
			t.Fatalf("Create(%s): %v", tenantID, err)
		}
		if created.ConnectionID != "shared" {
			t.Fatalf("Create(%s) id = %q", tenantID, created.ConnectionID)
		}
	}

	a, err := repo.Get(ctx, "tenant-a", "shared")
	if err != nil {
		t.Fatalf("Get tenant-a: %v", err)
	}
	b, err := repo.Get(ctx, "tenant-b", "shared")
	if err != nil {
		t.Fatalf("Get tenant-b: %v", err)
	}
	if a.BaseURL == b.BaseURL || a.TenantID != "tenant-a" || b.TenantID != "tenant-b" {
		t.Fatalf("tenant records collided: a=%+v b=%+v", a, b)
	}
	if _, err := service.Get(ctx, testViewer("tenant-c", "owner"), "shared"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant Get error = %v, want ErrNotFound", err)
	}
}

func TestCreateRejectsUnsafeConnectionIdentifiers(t *testing.T) {
	ctx := context.Background()
	repo, closeRepo := newTestRepository(t)
	defer closeRepo()
	authorizer := authorization.NewService(authorization.NewReBACAdapter(rebac.NewMemoryStore(), nil))
	service := newAuthorizedService(repo, authorizer, &auditRecorder{})
	viewer := testViewer("tenant-a", "owner")
	writePlatformMember(t, authorizer, viewer)

	for _, connectionID := range []string{"nested/connection", `nested\connection`, "connection\nother", strings.Repeat("c", 257)} {
		_, err := service.Create(ctx, viewer, CreateCommand{
			ConnectionID: connectionID, Scope: ScopePersonal, Provider: ProviderGenericGit,
			BaseURL: "https://git.example/repository",
		})
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("Create(%q) error = %v, want ErrInvalidRequest", connectionID, err)
		}
	}
}

func TestCreateIsIdempotentAndRejectsKeyReuseWithDifferentRequest(t *testing.T) {
	ctx := context.Background()
	repo, closeRepo := newTestRepository(t)
	defer closeRepo()
	authorizer := authorization.NewService(authorization.NewReBACAdapter(rebac.NewMemoryStore(), nil))
	service := newAuthorizedService(repo, authorizer, &auditRecorder{})
	viewer := testViewer("tenant-a", "owner")
	writePlatformMember(t, authorizer, viewer)

	command := CreateCommand{
		IdempotencyKey: "create-connection-1", Scope: ScopePersonal, Provider: ProviderGenericGit,
		BaseURL: "https://git.example/repository",
	}
	first, err := service.Create(ctx, viewer, command)
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}
	second, err := service.Create(ctx, viewer, command)
	if err != nil {
		t.Fatalf("replayed Create: %v", err)
	}
	if second.ConnectionID != first.ConnectionID || second.Version != first.Version {
		t.Fatalf("replayed connection = %+v, want %+v", second, first)
	}

	command.BaseURL = "https://git.example/different"
	if _, err := service.Create(ctx, viewer, command); !errors.Is(err, ErrIdempotency) {
		t.Fatalf("conflicting Create error = %v, want ErrIdempotency", err)
	}
}

func TestMutationsAreIdempotentAndRejectKeyReuseWithDifferentRequests(t *testing.T) {
	ctx := context.Background()
	repo, closeRepo := newTestRepository(t)
	defer closeRepo()
	authorizer := authorization.NewService(authorization.NewReBACAdapter(rebac.NewMemoryStore(), nil))
	service := newAuthorizedService(repo, authorizer, &auditRecorder{})
	verifier := &countingVerificationStub{verification: Verification{
		VerifiedAt: time.Date(2026, 7, 25, 3, 0, 0, 0, time.UTC),
		Repository: RepositoryMetadata{RepositoryLocator: "owner/repository", DisplayName: "repository", DefaultBranch: "main", HeadCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
	}}
	service.SetVerifier(verifier)
	viewer := testViewer("tenant-a", "owner")
	writePlatformMember(t, authorizer, viewer)
	created, err := service.Create(ctx, viewer, CreateCommand{ConnectionID: "mutation-idempotency", Scope: ScopePersonal, Provider: ProviderGenericGit, BaseURL: "https://git.example/original"})
	if err != nil {
		t.Fatal(err)
	}

	updatedURL := "https://git.example/updated"
	update := UpdateCommand{IdempotencyKey: "update-1", ConnectionID: created.ConnectionID, ExpectedVersion: created.Version, BaseURL: &updatedURL}
	firstUpdate, err := service.Update(ctx, viewer, update)
	if err != nil {
		t.Fatal(err)
	}
	replayedUpdate, err := service.Update(ctx, viewer, update)
	if err != nil {
		t.Fatalf("replayed Update: %v", err)
	}
	if replayedUpdate.Version != firstUpdate.Version || replayedUpdate.BaseURL != firstUpdate.BaseURL {
		t.Fatalf("replayed update = %+v, want %+v", replayedUpdate, firstUpdate)
	}
	differentURL := "https://git.example/different"
	update.BaseURL = &differentURL
	if _, err := service.Update(ctx, viewer, update); !errors.Is(err, ErrIdempotency) {
		t.Fatalf("conflicting Update error = %v, want ErrIdempotency", err)
	}

	verify := ActionCommand{IdempotencyKey: "verify-1", ConnectionID: created.ConnectionID, ExpectedVersion: firstUpdate.Version}
	firstVerified, firstResult, err := service.Verify(ctx, viewer, verify)
	if err != nil {
		t.Fatal(err)
	}
	replayedVerified, replayedResult, err := service.Verify(ctx, viewer, verify)
	if err != nil {
		t.Fatalf("replayed Verify: %v", err)
	}
	if verifier.calls != 1 || replayedVerified.Version != firstVerified.Version || !replayedResult.VerifiedAt.Equal(firstResult.VerifiedAt) {
		t.Fatalf("verify calls=%d first=%+v/%+v replay=%+v/%+v", verifier.calls, firstVerified, firstResult, replayedVerified, replayedResult)
	}
}

func TestAgentCannotOwnSCMConnection(t *testing.T) {
	ctx := context.Background()
	repo, closeRepo := newTestRepository(t)
	defer closeRepo()
	authorizer := authorization.NewService(authorization.NewReBACAdapter(rebac.NewMemoryStore(), nil))
	service := newAuthorizedService(repo, authorizer, &auditRecorder{})
	viewer := testViewer("tenant-a", "automation-agent")
	viewer.ViewerKind = identity.ViewerKindAgent
	if err := authorizer.WriteRelations(ctx, []authorization.RelationTuple{{
		TenantID: viewer.TenantID,
		Subject:  authorization.SubjectRef{Kind: viewer.ViewerKind, ID: viewer.ViewerID},
		Relation: "member",
		Object:   identity.ObjectRef{Type: "platform", ID: "global", TenantID: viewer.TenantID},
	}}); err != nil {
		t.Fatalf("write agent platform member: %v", err)
	}

	_, err := service.Create(ctx, viewer, CreateCommand{
		ConnectionID: "agent-owned", Scope: ScopePersonal, Provider: ProviderGenericGit,
		BaseURL: "https://git.example/repository", SecretRef: "secret://scm/token",
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Create agent-owned connection error = %v, want ErrInvalidRequest", err)
	}
	if _, err := repo.Get(ctx, viewer.TenantID, "agent-owned"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("agent-owned connection persisted: %v", err)
	}
}

func TestConnectionEffectIDsAreTenantScoped(t *testing.T) {
	service := NewService(nil, nil, fixedClock{now: time.Date(2026, 7, 25, 1, 0, 0, 0, time.UTC)})
	viewer := testViewer("tenant-a", "owner")
	connectionA := Connection{TenantID: "tenant-a", ConnectionID: "shared", Version: 1}
	connectionB := Connection{TenantID: "tenant-b", ConnectionID: "shared", Version: 1}
	effectA := service.connectionAuditEffect(connectionA, viewer, "scm_connection.created", "succeeded", "created", nil)
	effectB := service.connectionAuditEffect(connectionB, viewer, "scm_connection.created", "succeeded", "created", nil)
	if effectA.EffectID == effectB.EffectID || effectA.Audit == nil || effectB.Audit == nil || effectA.Audit.AuditID == effectB.Audit.AuditID {
		t.Fatalf("tenant audit effects collided: a=%+v b=%+v", effectA, effectB)
	}
}

func TestConnectionDTOAndJSONNeverExposeSecretReference(t *testing.T) {
	ctx := context.Background()
	repo, closeRepo := newTestRepository(t)
	defer closeRepo()
	authorizer := authorization.NewService(authorization.NewReBACAdapter(rebac.NewMemoryStore(), nil))
	service := newAuthorizedService(repo, authorizer, &auditRecorder{})
	viewer := testViewer("tenant-a", "owner")
	writePlatformMember(t, authorizer, viewer)

	dto, err := service.Create(ctx, viewer, CreateCommand{
		ConnectionID: "conn-1", Scope: ScopePersonal, Provider: ProviderGenericGit,
		BaseURL: "https://git.example/repository", SecretRef: "secret://scm/token-1",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	payload, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(payload), "secret://") || strings.Contains(strings.ToLower(string(payload)), "secret_ref") {
		t.Fatalf("public DTO leaked secret reference: %s", payload)
	}
	stored, err := repo.Get(ctx, "tenant-a", "conn-1")
	if err != nil || stored.SecretRef != "secret://scm/token-1" {
		t.Fatalf("stored connection = %+v, err=%v", stored, err)
	}
}

func TestManagedLocalRequiresPlatformAdministrationScopes(t *testing.T) {
	ctx := context.Background()
	repo, closeRepo := newTestRepository(t)
	defer closeRepo()
	authorizer := authorization.NewService(authorization.NewReBACAdapter(rebac.NewMemoryStore(), nil))
	service := newAuthorizedService(repo, authorizer, &auditRecorder{})
	viewer := testViewer("tenant-a", "owner")
	viewer.PermissionScope = []string{"connection.manage", "connection.read", "connection.use"}
	writePlatformMember(t, authorizer, viewer)

	_, err := service.Create(ctx, viewer, CreateCommand{
		ConnectionID: "local-1", Scope: ScopePlatform, Provider: ProviderManagedLocal,
		BaseURL: filepath.Clean(t.TempDir()),
	})
	if !errors.Is(err, authorization.ErrDenied) {
		t.Fatalf("Create managed_local error = %v, want authorization.ErrDenied", err)
	}
}

func TestScopeWithoutConnectionRelationIsNotFound(t *testing.T) {
	ctx := context.Background()
	repo, closeRepo := newTestRepository(t)
	defer closeRepo()
	authorizer := authorization.NewService(authorization.NewReBACAdapter(rebac.NewMemoryStore(), nil))
	service := newAuthorizedService(repo, authorizer, &auditRecorder{})
	now := time.Date(2026, 7, 25, 2, 0, 0, 0, time.UTC)
	if err := repo.Create(ctx, "tenant-a", Connection{
		TenantID: "tenant-a", ConnectionID: "hidden", OwnerID: "someone-else", Scope: ScopePersonal,
		Provider: ProviderGenericGit, BaseURL: "https://git.example/hidden", Status: StatusActive,
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}); err != nil {
		t.Fatalf("seed connection: %v", err)
	}

	_, err := service.Get(ctx, testViewer("tenant-a", "owner"), "hidden")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get without relation error = %v, want ErrNotFound", err)
	}
}

func TestUpdateUsesCompareAndSwapVersion(t *testing.T) {
	ctx := context.Background()
	service, _, viewer, closeRepo := newCreatedService(t, "conn-cas")
	defer closeRepo()
	firstURL := "https://git.example/first"
	updated, err := service.Update(ctx, viewer, UpdateCommand{ConnectionID: "conn-cas", ExpectedVersion: 1, BaseURL: &firstURL})
	if err != nil {
		t.Fatalf("Update first: %v", err)
	}
	if updated.Version != 2 || updated.BaseURL != firstURL {
		t.Fatalf("updated = %+v", updated)
	}
	staleURL := "https://git.example/stale"
	if _, err := service.Update(ctx, viewer, UpdateCommand{ConnectionID: "conn-cas", ExpectedVersion: 1, BaseURL: &staleURL}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale Update error = %v, want ErrConflict", err)
	}
}

func TestAuditFailureLeavesDurableConnectionEffectsAndRecoversWithoutRollback(t *testing.T) {
	ctx := context.Background()
	repo, closeRepo := newTestRepository(t)
	defer closeRepo()
	authorizer := authorization.NewService(authorization.NewReBACAdapter(rebac.NewMemoryStore(), nil))
	viewer := testViewer("tenant-a", "owner")
	writePlatformMember(t, authorizer, viewer)
	auditFailure := errors.New("audit unavailable")
	service := newAuthorizedService(repo, authorizer, &auditRecorder{err: auditFailure})

	_, err := service.Create(ctx, viewer, CreateCommand{
		ConnectionID: "failed-create", Scope: ScopePersonal, Provider: ProviderGenericGit,
		BaseURL: "https://git.example/repository", SecretRef: "secret://scm/token",
	})
	if !errors.Is(err, auditFailure) {
		t.Fatalf("Create audit error = %v", err)
	}
	created, err := repo.Get(ctx, viewer.TenantID, "failed-create")
	if err != nil || created.Version != 1 || len(created.PendingEffects) != 1 {
		t.Fatalf("durable failed create = %+v, err=%v", created, err)
	}

	recoveredCreateAudit := &auditRecorder{}
	service.SetAuditRecorder(recoveredCreateAudit)
	if err := service.ReconcilePendingEffects(ctx, 100); err != nil {
		t.Fatalf("reconcile create: %v", err)
	}
	service.SetAuditRecorder(&auditRecorder{err: auditFailure})
	changed := "https://git.example/changed"
	if _, err := service.Update(ctx, viewer, UpdateCommand{ConnectionID: "failed-create", ExpectedVersion: 1, BaseURL: &changed}); !errors.Is(err, auditFailure) {
		t.Fatalf("Update audit error = %v", err)
	}
	accountID := "account-2"
	if _, err := service.Update(ctx, viewer, UpdateCommand{ConnectionID: "failed-create", ExpectedVersion: 2, InstallationOrAccountID: &accountID}); !errors.Is(err, auditFailure) {
		t.Fatalf("concurrent Update audit error = %v", err)
	}
	stored, err := repo.Get(ctx, viewer.TenantID, "failed-create")
	if err != nil || stored.Version != 3 || stored.BaseURL != changed || stored.InstallationOrAccountID != accountID || len(stored.PendingEffects) != 2 {
		t.Fatalf("concurrent connection mutation was lost: stored=%+v err=%v", stored, err)
	}

	recoveredAudit := &auditRecorder{}
	service.SetAuditRecorder(recoveredAudit)
	if err := service.ReconcilePendingEffects(ctx, 100); err != nil {
		t.Fatalf("reconcile updates: %v", err)
	}
	if err := service.ReconcilePendingEffects(ctx, 100); err != nil {
		t.Fatalf("second reconcile updates: %v", err)
	}
	stored, err = repo.Get(ctx, viewer.TenantID, "failed-create")
	if err != nil || stored.Version != 3 || len(stored.PendingEffects) != 0 {
		t.Fatalf("reconciled connection = %+v, err=%v", stored, err)
	}
	if len(recoveredAudit.events) != 2 || recoveredAudit.events[0].AuditID == "" || recoveredAudit.events[0].AuditID == recoveredAudit.events[1].AuditID {
		t.Fatalf("recovered audits = %+v", recoveredAudit.events)
	}
}

func TestPendingEffectReconciliationAdvancesPastFailingConnections(t *testing.T) {
	ctx := context.Background()
	repo, closeRepo := newTestRepository(t)
	defer closeRepo()
	authorizer := authorization.NewService(authorization.NewReBACAdapter(rebac.NewMemoryStore(), nil))
	viewer := testViewer("tenant-a", "owner")
	writePlatformMember(t, authorizer, viewer)
	auditFailure := errors.New("audit unavailable")
	service := newAuthorizedService(repo, authorizer, &auditRecorder{err: auditFailure})
	for _, connectionID := range []string{"connection-fair-a", "connection-fair-b", "connection-fair-c"} {
		_, err := service.Create(ctx, viewer, CreateCommand{
			ConnectionID: connectionID, Scope: ScopePersonal, Provider: ProviderGenericGit,
			BaseURL: "https://git.example/" + connectionID, SecretRef: "secret://scm/token",
		})
		if !errors.Is(err, auditFailure) {
			t.Fatalf("Create(%s) error = %v, want audit failure", connectionID, err)
		}
	}

	attempts := &auditRecorder{err: auditFailure}
	service.SetAuditRecorder(attempts)
	if err := service.ReconcilePendingEffects(ctx, 1); !errors.Is(err, auditFailure) {
		t.Fatalf("first ReconcilePendingEffects error = %v, want audit failure", err)
	}
	if err := service.ReconcilePendingEffects(ctx, 1); !errors.Is(err, auditFailure) {
		t.Fatalf("second ReconcilePendingEffects error = %v, want audit failure", err)
	}
	if len(attempts.events) != 2 || attempts.events[0].EntityID != "connection-fair-a" || attempts.events[1].EntityID != "connection-fair-b" {
		t.Fatalf("reconciliation attempts = %+v, want consecutive connections", attempts.events)
	}
}

func TestConnectionCreationRelationFailureIsDurableAndRecoverable(t *testing.T) {
	ctx := context.Background()
	repo, closeRepo := newTestRepository(t)
	defer closeRepo()
	authorizer := authorization.NewService(authorization.NewReBACAdapter(rebac.NewMemoryStore(), nil))
	viewer := testViewer("tenant-a", "owner")
	writePlatformMember(t, authorizer, viewer)
	relationFailure := errors.New("authorization unavailable")
	sink := &flakyConnectionRelationSink{delegate: authorizer, writeErr: relationFailure}
	service := NewService(repo, nil, fixedClock{now: time.Date(2026, 7, 25, 1, 0, 0, 0, time.UTC)})
	service.SetAuthorization(authorizer, authorization.NewRelationProjector(sink))
	service.SetAuditRecorder(&auditRecorder{})
	service.SetSecretReferenceValidator(secretReferenceValidatorFunc(func(context.Context, string) error { return nil }))

	_, err := service.Create(ctx, viewer, CreateCommand{ConnectionID: "connection-relation-recovery", Scope: ScopePersonal, Provider: ProviderGenericGit, BaseURL: "https://git.example/recovery", SecretRef: "secret://scm/token"})
	if !errors.Is(err, relationFailure) {
		t.Fatalf("Create relation error = %v", err)
	}
	stored, err := repo.Get(ctx, viewer.TenantID, "connection-relation-recovery")
	if err != nil || stored.Version != 1 || len(stored.PendingEffects) != 2 {
		t.Fatalf("durable create = %+v, err=%v", stored, err)
	}

	sink.writeErr = nil
	if err := service.ReconcilePendingEffects(ctx, 100); err != nil {
		t.Fatalf("ReconcilePendingEffects: %v", err)
	}
	stored, err = repo.Get(ctx, viewer.TenantID, "connection-relation-recovery")
	if err != nil || len(stored.PendingEffects) != 0 {
		t.Fatalf("reconciled create = %+v, err=%v", stored, err)
	}
	if _, err := service.Get(ctx, viewer, "connection-relation-recovery"); err != nil {
		t.Fatalf("Get reconciled connection: %v", err)
	}
}

func TestVerificationFailurePersistsErrorStatus(t *testing.T) {
	ctx := context.Background()
	service, repo, viewer, closeRepo := newCreatedService(t, "conn-verify")
	defer closeRepo()
	service.SetVerifier(verificationStub{err: errors.New("provider unavailable")})

	dto, _, err := service.Verify(ctx, viewer, ActionCommand{ConnectionID: "conn-verify", ExpectedVersion: 1})
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("Verify error = %v, want ErrVerificationFailed", err)
	}
	if dto.Status != StatusError || dto.Version != 2 {
		t.Fatalf("Verify DTO = %+v", dto)
	}
	if dto.Capabilities.CanUse {
		t.Fatalf("error connection can_use = true: %+v", dto.Capabilities)
	}
	stored, getErr := repo.Get(ctx, viewer.TenantID, "conn-verify")
	if getErr != nil || stored.Status != StatusError || stored.Version != 2 {
		t.Fatalf("stored = %+v, err=%v", stored, getErr)
	}
	if _, err := service.ListRepositories(ctx, viewer, "conn-verify"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("ListRepositories(error connection) error = %v", err)
	}
}

func TestRevokedConnectionCannotListRepositories(t *testing.T) {
	ctx := context.Background()
	service, _, viewer, closeRepo := newCreatedService(t, "conn-revoked")
	defer closeRepo()
	service.SetVerifier(verificationStub{repositories: []RepositoryMetadata{{RepositoryLocator: "repo-1"}}})
	dto, err := service.Revoke(ctx, viewer, ActionCommand{ConnectionID: "conn-revoked", ExpectedVersion: 1})
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if dto.Capabilities.CanUse {
		t.Fatalf("revoked connection can_use = true: %+v", dto.Capabilities)
	}
	if _, err := service.ListRepositories(ctx, viewer, "conn-revoked"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("ListRepositories(revoked) error = %v", err)
	}
	if dto.Capabilities.CanRotate || dto.Capabilities.CanVerify {
		t.Fatalf("revoked connection exposes reactivation capabilities: %+v", dto.Capabilities)
	}
	if _, _, err := service.Verify(ctx, viewer, ActionCommand{ConnectionID: "conn-revoked", ExpectedVersion: dto.Version}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Verify(revoked) error = %v, want ErrInvalidRequest", err)
	}
	if _, err := service.Rotate(ctx, viewer, RotateCommand{ConnectionID: "conn-revoked", ExpectedVersion: dto.Version, SecretRef: "secret://scm/replacement"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Rotate(revoked) error = %v, want ErrInvalidRequest", err)
	}
}

func TestManagedLocalConnectionCannotRotateCredentials(t *testing.T) {
	ctx := context.Background()
	repo, closeRepo := newTestRepository(t)
	defer closeRepo()
	authorizer := authorization.NewService(authorization.NewReBACAdapter(rebac.NewMemoryStore(), nil))
	service := newAuthorizedService(repo, authorizer, &auditRecorder{})
	service.SetVerifier(managedLocalOnlyVerificationStub{})
	viewer := testViewer("tenant-a", "owner")
	viewer.PermissionScope = append(viewer.PermissionScope, "settings.manage")
	writePlatformMember(t, authorizer, viewer)
	created, err := service.Create(ctx, viewer, CreateCommand{ConnectionID: "managed-local", Scope: ScopePlatform, Provider: ProviderManagedLocal, BaseURL: filepath.Clean(t.TempDir())})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Capabilities.CanRotate {
		t.Fatalf("managed_local can_rotate = true: %+v", created.Capabilities)
	}
	if _, err := service.Rotate(ctx, viewer, RotateCommand{ConnectionID: created.ConnectionID, ExpectedVersion: created.Version, SecretRef: "secret://scm/not-used"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Rotate error = %v, want ErrInvalidRequest", err)
	}
}

func newCreatedService(t *testing.T, connectionID string) (*Service, Repository, ViewerRef, func()) {
	t.Helper()
	repo, closeRepo := newTestRepository(t)
	authorizer := authorization.NewService(authorization.NewReBACAdapter(rebac.NewMemoryStore(), nil))
	service := newAuthorizedService(repo, authorizer, &auditRecorder{})
	viewer := testViewer("tenant-a", "owner")
	writePlatformMember(t, authorizer, viewer)
	if _, err := service.Create(context.Background(), viewer, CreateCommand{
		ConnectionID: connectionID, Scope: ScopePersonal, Provider: ProviderGenericGit,
		BaseURL: "https://git.example/repository", SecretRef: "secret://scm/token",
	}); err != nil {
		closeRepo()
		t.Fatalf("Create: %v", err)
	}
	return service, repo, viewer, closeRepo
}

func newAuthorizedService(repo Repository, authorizer *authorization.Service, audit *auditRecorder) *Service {
	service := NewService(repo, nil, fixedClock{now: time.Date(2026, 7, 25, 1, 0, 0, 0, time.UTC)})
	service.SetAuthorization(authorizer, authorization.NewRelationProjector(authorizer))
	service.SetAuditRecorder(audit)
	service.SetSecretReferenceValidator(secretReferenceValidatorFunc(func(context.Context, string) error { return nil }))
	return service
}

type secretReferenceValidatorFunc func(context.Context, string) error

func (f secretReferenceValidatorFunc) ValidateSecretReference(ctx context.Context, ref string) error {
	return f(ctx, ref)
}

func TestCreateAndRotateRequireResolvableSecretReferences(t *testing.T) {
	service, repo, viewer, closeRepo := newCreatedService(t, "connection-secret-validation")
	defer closeRepo()
	missing := errors.New("secret is missing")
	service.SetSecretReferenceValidator(secretReferenceValidatorFunc(func(context.Context, string) error { return missing }))

	if _, err := service.Rotate(context.Background(), viewer, RotateCommand{
		ConnectionID: "connection-secret-validation", ExpectedVersion: 1, SecretRef: "secret://scm/missing",
	}); !errors.Is(err, ErrInvalidRequest) || !strings.Contains(err.Error(), "cannot be resolved") {
		t.Fatalf("Rotate error = %v, want unresolved secret error", err)
	}
	stored, err := repo.Get(context.Background(), viewer.TenantID, "connection-secret-validation")
	if err != nil || stored.Version != 1 || stored.SecretRef != "secret://scm/token" {
		t.Fatalf("stored connection = %+v, err=%v", stored, err)
	}
}

func TestListRejectsInvalidFiltersAndPagination(t *testing.T) {
	ctx := context.Background()
	repo, closeRepo := newTestRepository(t)
	defer closeRepo()
	authorizer := authorization.NewService(authorization.NewReBACAdapter(rebac.NewMemoryStore(), nil))
	service := newAuthorizedService(repo, authorizer, &auditRecorder{})
	viewer := testViewer("tenant-a", "owner")
	writePlatformMember(t, authorizer, viewer)
	for _, id := range []string{"connection-a", "connection-b"} {
		if _, err := service.Create(ctx, viewer, CreateCommand{ConnectionID: id, Scope: ScopePersonal, Provider: ProviderGenericGit, BaseURL: "https://git.example/" + id, SecretRef: "secret://scm/token"}); err != nil {
			t.Fatalf("Create(%s): %v", id, err)
		}
	}

	for _, query := range []ListQuery{
		{Limit: -5},
		{Offset: -7},
		{Provider: "unknown"},
		{Status: "unknown"},
		{OrganizationID: "invalid\norganization"},
	} {
		if _, err := service.List(ctx, viewer, query); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("List(%+v) error = %v, want ErrInvalidRequest", query, err)
		}
	}
}

func TestCreateRejectsScopeMetadataAndCredentialBearingURLs(t *testing.T) {
	repo, closeRepo := newTestRepository(t)
	defer closeRepo()
	authorizer := authorization.NewService(authorization.NewReBACAdapter(rebac.NewMemoryStore(), nil))
	service := newAuthorizedService(repo, authorizer, &auditRecorder{})
	viewer := testViewer("tenant-a", "owner")
	writePlatformMember(t, authorizer, viewer)

	commands := []CreateCommand{
		{ConnectionID: "personal-org", Scope: ScopePersonal, OrganizationID: "org-1", Provider: ProviderGenericGit, BaseURL: "https://git.example/repository"},
		{ConnectionID: "platform-org", Scope: ScopePlatform, OrganizationID: "org-1", Provider: ProviderGenericGit, BaseURL: "https://git.example/repository"},
		{ConnectionID: "local-personal", Scope: ScopePersonal, Provider: ProviderManagedLocal, BaseURL: filepath.Clean(t.TempDir())},
		{ConnectionID: "embedded-credential", Scope: ScopePersonal, Provider: ProviderGenericGit, BaseURL: "https://token@git.example/repository"},
		{ConnectionID: "query-secret", Scope: ScopePersonal, Provider: ProviderGenericGit, BaseURL: "https://git.example/repository?token=secret"},
	}
	for _, command := range commands {
		t.Run(command.ConnectionID, func(t *testing.T) {
			if _, err := service.Create(context.Background(), viewer, command); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("Create error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestCreateAndUpdateRejectOversizedOrControlBearingProviderMetadata(t *testing.T) {
	repo, closeRepo := newTestRepository(t)
	defer closeRepo()
	authorizer := authorization.NewService(authorization.NewReBACAdapter(rebac.NewMemoryStore(), nil))
	service := newAuthorizedService(repo, authorizer, &auditRecorder{})
	viewer := testViewer("tenant-a", "owner")
	writePlatformMember(t, authorizer, viewer)

	tooManyPatterns := make([]string, 129)
	for index := range tooManyPatterns {
		tooManyPatterns[index] = fmt.Sprintf("repository-%03d", index)
	}
	commands := []CreateCommand{
		{ConnectionID: "account-control", Scope: ScopePersonal, Provider: ProviderGenericGit, BaseURL: "https://git.example/repository", InstallationOrAccountID: "account\ninvalid"},
		{ConnectionID: "account-long", Scope: ScopePersonal, Provider: ProviderGenericGit, BaseURL: "https://git.example/repository", InstallationOrAccountID: strings.Repeat("a", 257)},
		{ConnectionID: "pattern-control", Scope: ScopePersonal, Provider: ProviderGenericGit, BaseURL: "https://git.example/repository", AllowedRepositoryPatterns: []string{"repo\ninvalid"}},
		{ConnectionID: "pattern-wildcard", Scope: ScopePersonal, Provider: ProviderGenericGit, BaseURL: "https://git.example/repository", AllowedRepositoryPatterns: []string{"repo*invalid"}},
		{ConnectionID: "patterns-many", Scope: ScopePersonal, Provider: ProviderGenericGit, BaseURL: "https://git.example/repository", AllowedRepositoryPatterns: tooManyPatterns},
	}
	for _, command := range commands {
		t.Run(command.ConnectionID, func(t *testing.T) {
			if _, err := service.Create(context.Background(), viewer, command); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("Create error = %v, want ErrInvalidRequest", err)
			}
		})
	}

	created, err := service.Create(context.Background(), viewer, CreateCommand{ConnectionID: "metadata-update", Scope: ScopePersonal, Provider: ProviderGenericGit, BaseURL: "https://git.example/repository"})
	if err != nil {
		t.Fatalf("Create update fixture: %v", err)
	}
	invalidAccount := "account\x00invalid"
	if _, err := service.Update(context.Background(), viewer, UpdateCommand{ConnectionID: created.ConnectionID, ExpectedVersion: created.Version, InstallationOrAccountID: &invalidAccount}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Update error = %v, want ErrInvalidRequest", err)
	}
	stored, err := repo.Get(context.Background(), viewer.TenantID, created.ConnectionID)
	if err != nil || stored.Version != created.Version || stored.InstallationOrAccountID != "" {
		t.Fatalf("invalid update mutated connection: stored=%+v err=%v", stored, err)
	}
}

func TestUnsupportedProviderCannotBeUsedOrReportedUsable(t *testing.T) {
	ctx := context.Background()
	repo, closeRepo := newTestRepository(t)
	defer closeRepo()
	authorizer := authorization.NewService(authorization.NewReBACAdapter(rebac.NewMemoryStore(), nil))
	service := newAuthorizedService(repo, authorizer, &auditRecorder{})
	service.SetVerifier(managedLocalOnlyVerificationStub{})
	viewer := testViewer("tenant-a", "owner")
	writePlatformMember(t, authorizer, viewer)

	created, err := service.Create(ctx, viewer, CreateCommand{ConnectionID: "future-provider", Scope: ScopePersonal, Provider: ProviderGitHub, BaseURL: "https://github.example/repository"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Capabilities.CanUse || created.Capabilities.CanVerify {
		t.Fatalf("unsupported provider capabilities = %+v", created.Capabilities)
	}
	if _, err := service.GetConnectionForUse(ctx, viewer, created.ConnectionID); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("GetConnectionForUse error = %v, want ErrInvalidRequest", err)
	}
	if _, err := service.ListRepositories(ctx, viewer, created.ConnectionID); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("ListRepositories error = %v, want ErrInvalidRequest", err)
	}
	if _, _, err := service.Verify(ctx, viewer, ActionCommand{ConnectionID: created.ConnectionID, ExpectedVersion: created.Version}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Verify error = %v, want ErrInvalidRequest", err)
	}
	stored, err := repo.Get(ctx, viewer.TenantID, created.ConnectionID)
	if err != nil || stored.Version != created.Version || stored.Status != StatusError {
		t.Fatalf("unsupported verification mutated connection: stored=%+v err=%v", stored, err)
	}
}

func TestServiceRejectsAmbiguousTenantKeySegment(t *testing.T) {
	repo, closeRepo := newTestRepository(t)
	defer closeRepo()
	authorizer := authorization.NewService(authorization.NewReBACAdapter(rebac.NewMemoryStore(), nil))
	service := newAuthorizedService(repo, authorizer, &auditRecorder{})
	viewer := testViewer("tenant/ambiguous", "owner")

	_, err := service.Create(context.Background(), viewer, CreateCommand{
		ConnectionID: "connection", Scope: ScopePersonal, Provider: ProviderGenericGit, BaseURL: "https://git.example/repository",
	})

	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Create error = %v, want ErrInvalidRequest", err)
	}
}

func newTestRepository(t *testing.T) (Repository, func()) {
	t.Helper()
	registry, err := db.OpenRegistry(context.Background(), db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "scm-connection-test"},
	}})
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	return NewPlatformRepository(db.NewRouter(registry)), func() { _ = registry.Close() }
}

func testViewer(tenantID, userID string) ViewerRef {
	return ViewerRef{
		ViewerID: userID, ViewerKind: identity.ViewerKindUser, TenantID: tenantID,
		PermissionScope: []string{"connection.read", "connection.use", "connection.manage", "settings.manage"},
	}
}

func writePlatformMember(t *testing.T, authorizer *authorization.Service, viewer ViewerRef) {
	t.Helper()
	err := authorizer.WriteRelations(context.Background(), []authorization.RelationTuple{{
		TenantID: viewer.TenantID,
		Subject:  authorization.SubjectRef{Kind: identity.ViewerKindUser, ID: viewer.ViewerID},
		Relation: "member",
		Object:   identity.ObjectRef{Type: "platform", ID: "global", TenantID: viewer.TenantID},
	}})
	if err != nil {
		t.Fatalf("write platform member: %v", err)
	}
}
