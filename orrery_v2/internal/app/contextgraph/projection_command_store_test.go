package contextgraph

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func noopProjectionCommandEffect(context.Context, WriterStore) error { return nil }

func projectionCommandTestFence(tenantID string) ProjectionAuditCommandFence {
	return ProjectionAuditCommandFence{
		TenantID: tenantID, BindingVersion: 1,
		AuthorizationRevision: "authorization:0", PolicyRevision: "policy:0",
	}
}

func prepareProjectionCommandSecurityTables(t *testing.T, ctx context.Context, database *db.Store) {
	t.Helper()
	for _, statement := range []string{
		`CREATE TABLE rebac_authorization_revision (id TEXT PRIMARY KEY, revision BIGINT NOT NULL)`,
		`INSERT INTO rebac_authorization_revision (id, revision) VALUES ('global', 0)`,
		`CREATE TABLE data_policy_revision (id TEXT PRIMARY KEY, revision BIGINT NOT NULL)`,
		`INSERT INTO data_policy_revision (id, revision) VALUES ('global', 0)`,
		`CREATE TABLE ontology_scope_bindings (tenant_id TEXT PRIMARY KEY, binding_version BIGINT NOT NULL, state TEXT NOT NULL)`,
	} {
		if _, err := database.Exec(ctx, statement); err != nil {
			t.Fatalf("prepare security revision table: %v", err)
		}
	}
}

func TestProjectionAuditCommandSecurityFenceChecksPersistedScopeBinding(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "contextgraph-projection-command-binding-fence"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer database.Close()
	prepareProjectionCommandSecurityTables(t, ctx, database)
	fence := projectionCommandTestFence("tenant-1")
	fence.BindingVersion = 2
	fence.PersistedBindingVersion = 2
	fence.PersistenceRequired = true

	err = database.WithTx(ctx, func(tx db.Tx) error { return fenceProjectionAuditCommandSecurityTx(ctx, tx, fence) })
	if !errors.Is(err, ErrProjectionAuditCommandFence) {
		t.Fatalf("missing binding fence error=%v", err)
	}
	if _, err := database.Exec(ctx, `INSERT INTO ontology_scope_bindings (tenant_id, binding_version, state) VALUES (?, ?, 'active')`, fence.TenantID, fence.PersistedBindingVersion); err != nil {
		t.Fatalf("insert scope binding: %v", err)
	}
	if err := database.WithTx(ctx, func(tx db.Tx) error { return fenceProjectionAuditCommandSecurityTx(ctx, tx, fence) }); err != nil {
		t.Fatalf("matching binding fence: %v", err)
	}
	if _, err := database.Exec(ctx, `UPDATE ontology_scope_bindings SET binding_version = 3 WHERE tenant_id = ?`, fence.TenantID); err != nil {
		t.Fatalf("advance scope binding: %v", err)
	}
	err = database.WithTx(ctx, func(tx db.Tx) error { return fenceProjectionAuditCommandSecurityTx(ctx, tx, fence) })
	if !errors.Is(err, ErrProjectionAuditCommandFence) {
		t.Fatalf("stale binding fence error=%v", err)
	}
}

func TestPlatformStoreProjectionAuditCommandClaimCASAndIdempotency(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "contextgraph-projection-command"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer database.Close()
	if err := migration.NewRunner(contextGraphAuditStoreTestMigrations()).Up(ctx, database); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	prepareProjectionCommandSecurityTables(t, ctx, database)
	store := NewPlatformStore(database)
	seed := ProjectionAudit{
		ID: "audit-1", TenantID: "tenant-1", SourceEventID: "event-1", SourceEventType: "project.organization.linked",
		Status: "skipped", ReasonCode: "missing_target", SourceAccessRef: AccessRef{Kind: "organization", ID: "tenant-1", TenantID: "tenant-1"},
		ObservedAt: time.Unix(10, 0).UTC(),
	}
	if err := store.SaveProjectionAudit(ctx, seed); err != nil {
		t.Fatalf("save audit: %v", err)
	}
	audit, err := store.GetProjectionAudit(ctx, seed.TenantID, seed.ID)
	if err != nil || audit.Revision != 1 {
		t.Fatalf("initial audit=%+v err=%v", audit, err)
	}
	hash := strings.Repeat("a", 64)
	request := ProjectionAuditCommandClaimRequest{Audit: audit, Operation: ProjectionAuditCommandReplay, IdempotencyKey: "replay-1", RequestHash: hash}
	claim, err := store.ClaimProjectionAuditCommand(ctx, request)
	if err != nil || !claim.Acquired || claim.State != ProjectionAuditCommandClaimed {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}

	retry, err := store.ClaimProjectionAuditCommand(ctx, request)
	if err != nil || retry.Acquired || retry.State != ProjectionAuditCommandClaimed {
		t.Fatalf("retry claim=%+v err=%v", retry, err)
	}
	conflicting := request
	conflicting.IdempotencyKey = "ack-1"
	conflicting.Operation = ProjectionAuditCommandAcknowledge
	if _, err := store.ClaimProjectionAuditCommand(ctx, conflicting); !errors.Is(err, ErrProjectionAuditCommandConflict) {
		t.Fatalf("concurrent acknowledge error=%v", err)
	}
	reused := request
	reused.Operation = ProjectionAuditCommandAcknowledge
	if _, err := store.ClaimProjectionAuditCommand(ctx, reused); !errors.Is(err, ErrProjectionAuditCommandConflict) {
		t.Fatalf("reused key error=%v", err)
	}

	result := audit
	result.Status = "replayed"
	result.ObservedAt = time.Unix(20, 0).UTC()
	history := ProjectionAuditReplayHistory{
		ID: "history-1", AuditID: audit.ID, TenantID: audit.TenantID, SourceEventID: audit.SourceEventID,
		SourceEventType: audit.SourceEventType, Status: "replayed", SourceAccessRef: audit.SourceAccessRef,
		AccessState: AccessStateResolved, AttemptedAt: result.ObservedAt,
	}
	originalLease := claim.LeaseToken
	if _, err := database.Exec(ctx, `UPDATE context_graph_projection_audit_command_claims SET lease_expires_at = 0 WHERE tenant_id = ? AND audit_id = ?`, audit.TenantID, audit.ID); err != nil {
		t.Fatalf("expire command lease: %v", err)
	}
	if err := store.CompleteProjectionAuditCommand(ctx, ProjectionAuditCommandCompletion{
		TenantID: audit.TenantID, AuditID: audit.ID, Operation: request.Operation,
		IdempotencyKey: request.IdempotencyKey, RequestHash: request.RequestHash, LeaseToken: originalLease, Result: result, History: &history, Effect: noopProjectionCommandEffect, Fence: projectionCommandTestFence(audit.TenantID),
	}); !errors.Is(err, ErrProjectionAuditCommandConflict) {
		t.Fatalf("expired lease completion error=%v", err)
	}
	unmodifiedAfterExpiry, err := store.GetProjectionAudit(ctx, audit.TenantID, audit.ID)
	if err != nil || unmodifiedAfterExpiry.Revision != audit.Revision || unmodifiedAfterExpiry.Status != "skipped" {
		t.Fatalf("expired lease mutated audit=%+v err=%v", unmodifiedAfterExpiry, err)
	}
	takeover, err := store.ClaimProjectionAuditCommand(ctx, request)
	if err != nil || !takeover.Acquired || takeover.LeaseToken == "" || takeover.LeaseToken == originalLease {
		t.Fatalf("takeover=%+v err=%v", takeover, err)
	}
	if err := store.CompleteProjectionAuditCommand(ctx, ProjectionAuditCommandCompletion{
		TenantID: audit.TenantID, AuditID: audit.ID, Operation: request.Operation,
		IdempotencyKey: request.IdempotencyKey, RequestHash: request.RequestHash, LeaseToken: originalLease, Result: result, History: &history, Effect: noopProjectionCommandEffect, Fence: projectionCommandTestFence(audit.TenantID),
	}); !errors.Is(err, ErrProjectionAuditCommandConflict) {
		t.Fatalf("stale lease completion error=%v", err)
	}
	claim = takeover
	invalidHistory := history
	invalidHistory.TenantID = "tenant-2"
	if err := store.CompleteProjectionAuditCommand(ctx, ProjectionAuditCommandCompletion{
		TenantID: audit.TenantID, AuditID: audit.ID, Operation: request.Operation,
		IdempotencyKey: request.IdempotencyKey, RequestHash: request.RequestHash, LeaseToken: claim.LeaseToken,
		Result: result, History: &invalidHistory, Effect: noopProjectionCommandEffect, Fence: projectionCommandTestFence(audit.TenantID),
	}); !errors.Is(err, ErrProjectionAuditCommandConflict) {
		t.Fatalf("cross-tenant history completion error=%v", err)
	}
	unmodified, err := store.GetProjectionAudit(ctx, audit.TenantID, audit.ID)
	if err != nil || unmodified.Revision != audit.Revision || unmodified.Status != "skipped" {
		t.Fatalf("audit mutation was not rolled back with history: audit=%+v err=%v", unmodified, err)
	}
	if err := store.CompleteProjectionAuditCommand(ctx, ProjectionAuditCommandCompletion{
		TenantID: audit.TenantID, AuditID: audit.ID, Operation: request.Operation,
		IdempotencyKey: request.IdempotencyKey, RequestHash: request.RequestHash, LeaseToken: claim.LeaseToken,
		Result: result, History: &history, Effect: noopProjectionCommandEffect, Fence: projectionCommandTestFence(audit.TenantID),
	}); err != nil {
		t.Fatalf("complete command: %v", err)
	}
	current, err := store.GetProjectionAudit(ctx, audit.TenantID, audit.ID)
	if err != nil || current.Revision != 2 || current.Status != "replayed" {
		t.Fatalf("completed audit=%+v err=%v", current, err)
	}
	historyPage, err := store.ListVisibleProjectionReplayHistory(ctx, ProjectionReplayHistoryCursorQuery{
		TenantID: audit.TenantID, AuditID: audit.ID, Visibility: VisibilityConstraint{AllowedAccessRefs: []AccessRef{audit.SourceAccessRef}}, Limit: 10,
	})
	if err != nil || len(historyPage.Items) != 1 || historyPage.Items[0].ID != history.ID {
		t.Fatalf("atomic replay history=%+v err=%v", historyPage, err)
	}
	completed, err := store.ClaimProjectionAuditCommand(ctx, ProjectionAuditCommandClaimRequest{
		Audit: current, Operation: request.Operation, IdempotencyKey: request.IdempotencyKey, RequestHash: request.RequestHash,
	})
	if err != nil || completed.Acquired || completed.State != ProjectionAuditCommandSucceeded || completed.ResultStatus != "replayed" || completed.ObservedAt == nil || !completed.ObservedAt.Equal(result.ObservedAt) {
		t.Fatalf("completed retry=%+v err=%v", completed, err)
	}
	newReplay := request
	newReplay.Audit = current
	newReplay.IdempotencyKey = "replay-2"
	if _, err := store.ClaimProjectionAuditCommand(ctx, newReplay); !errors.Is(err, ErrProjectionAuditCommandConflict) {
		t.Fatalf("non-actionable replay error=%v", err)
	}
}

func TestPlatformStoreProjectionAuditCommandRollsBackEffectWithCompletion(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "contextgraph-projection-command-effect"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer database.Close()
	if err := migration.NewRunner(contextGraphCompleteStoreTestMigrations()).Up(ctx, database); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	prepareProjectionCommandSecurityTables(t, ctx, database)
	store := NewPlatformStore(database)
	seed := ProjectionAudit{
		ID: "audit-1", TenantID: "tenant-1", SourceEventID: "event-1", SourceEventType: "project.organization.linked",
		Status: "skipped", ReasonCode: "missing_target", SourceAccessRef: AccessRef{Kind: "organization", ID: "tenant-1", TenantID: "tenant-1"},
		AccessState: AccessStateResolved, ObservedAt: time.Unix(10, 0).UTC(),
	}
	if err := store.SaveProjectionAudit(ctx, seed); err != nil {
		t.Fatalf("save audit: %v", err)
	}
	audit, err := store.GetProjectionAudit(ctx, seed.TenantID, seed.ID)
	if err != nil {
		t.Fatalf("get audit: %v", err)
	}
	request := ProjectionAuditCommandClaimRequest{Audit: audit, Operation: ProjectionAuditCommandReplay, IdempotencyKey: "replay-effect", RequestHash: strings.Repeat("9", 64)}
	claim, err := store.ClaimProjectionAuditCommand(ctx, request)
	if err != nil || !claim.Acquired {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	result := audit
	result.Status = "replayed"
	result.ObservedAt = time.Unix(20, 0).UTC()
	history := ProjectionAuditReplayHistory{
		ID: "history-effect", AuditID: audit.ID, TenantID: audit.TenantID, SourceEventID: audit.SourceEventID,
		SourceEventType: audit.SourceEventType, Status: "replayed", SourceAccessRef: audit.SourceAccessRef,
		AccessState: AccessStateResolved, AttemptedAt: result.ObservedAt,
	}
	effectErr := errors.New("projection write failed")
	err = store.CompleteProjectionAuditCommand(ctx, ProjectionAuditCommandCompletion{
		TenantID: audit.TenantID, AuditID: audit.ID, Operation: request.Operation, IdempotencyKey: request.IdempotencyKey,
		RequestHash: request.RequestHash, LeaseToken: claim.LeaseToken, Result: result, History: &history,
		Fence: projectionCommandTestFence(audit.TenantID),
		Effect: func(ctx context.Context, writer WriterStore) error {
			if err := writer.SaveObject(ctx, Object{ID: "effect-object", TenantID: audit.TenantID, ObjectType: "task", CanonicalKey: "task:effect", Label: "Effect", OwnerAccessRef: audit.SourceAccessRef}); err != nil {
				return err
			}
			return effectErr
		},
	})
	if !errors.Is(err, ErrProjectionAuditCommandEffect) || !errors.Is(err, effectErr) {
		t.Fatalf("completion error=%v", err)
	}
	current, err := store.GetProjectionAudit(ctx, audit.TenantID, audit.ID)
	if err != nil || current.Status != "skipped" || current.Revision != audit.Revision {
		t.Fatalf("audit=%+v err=%v", current, err)
	}
	var objectCount int
	if err := database.QueryRow(ctx, `SELECT COUNT(*) FROM context_graph_objects WHERE tenant_id = ? AND id = ?`, audit.TenantID, "effect-object").Scan(&objectCount); err != nil || objectCount != 0 {
		t.Fatalf("rolled-back effect object count=%d err=%v", objectCount, err)
	}
	if _, err := database.Exec(ctx, `UPDATE rebac_authorization_revision SET revision = 1 WHERE id = 'global'`); err != nil {
		t.Fatalf("advance authorization revision: %v", err)
	}
	effectCalled := false
	err = store.CompleteProjectionAuditCommand(ctx, ProjectionAuditCommandCompletion{
		TenantID: audit.TenantID, AuditID: audit.ID, Operation: request.Operation, IdempotencyKey: request.IdempotencyKey,
		RequestHash: request.RequestHash, LeaseToken: claim.LeaseToken, Result: result, History: &history,
		Fence: projectionCommandTestFence(audit.TenantID), Effect: func(context.Context, WriterStore) error {
			effectCalled = true
			return nil
		},
	})
	if !errors.Is(err, ErrProjectionAuditCommandFence) || !errors.Is(err, ErrProjectionAuditCommandConflict) || effectCalled {
		t.Fatalf("fenced completion error=%v effectCalled=%v", err, effectCalled)
	}
}

func TestPlatformStoreProjectionAuditCommandRejectsStaleAuditAndReleasesFailedClaim(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "contextgraph-projection-command-stale"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer database.Close()
	if err := migration.NewRunner(contextGraphAuditStoreTestMigrations()).Up(ctx, database); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	prepareProjectionCommandSecurityTables(t, ctx, database)
	store := NewPlatformStore(database)
	seed := ProjectionAudit{
		ID: "audit-1", TenantID: "tenant-1", SourceEventID: "event-1", SourceEventType: "unsupported.event",
		Status: "skipped", ReasonCode: "unsupported", SourceAccessRef: AccessRef{Kind: "organization", ID: "tenant-1", TenantID: "tenant-1"},
		ObservedAt: time.Unix(10, 0).UTC(),
	}
	if err := store.SaveProjectionAudit(ctx, seed); err != nil {
		t.Fatalf("save audit: %v", err)
	}
	stale, err := store.GetProjectionAudit(ctx, seed.TenantID, seed.ID)
	if err != nil {
		t.Fatalf("get audit: %v", err)
	}
	seed.Details = map[string]any{"changed": true}
	if err := store.SaveProjectionAudit(ctx, seed); err != nil {
		t.Fatalf("advance audit: %v", err)
	}
	if _, err := store.ClaimProjectionAuditCommand(ctx, ProjectionAuditCommandClaimRequest{
		Audit: stale, Operation: ProjectionAuditCommandReplay, IdempotencyKey: "stale", RequestHash: strings.Repeat("b", 64),
	}); !errors.Is(err, ErrProjectionAuditCommandConflict) {
		t.Fatalf("stale claim error=%v", err)
	}

	current, err := store.GetProjectionAudit(ctx, seed.TenantID, seed.ID)
	if err != nil {
		t.Fatalf("get current audit: %v", err)
	}
	request := ProjectionAuditCommandClaimRequest{
		Audit: current, Operation: ProjectionAuditCommandReplay, IdempotencyKey: "failed-1", RequestHash: strings.Repeat("c", 64),
	}
	claim, err := store.ClaimProjectionAuditCommand(ctx, request)
	if err != nil || !claim.Acquired {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	failureResult := current
	failureResult.Details = map[string]any{"last_replay_status": "failed"}
	failureHistory := ProjectionAuditReplayHistory{
		ID: "failure-history", AuditID: current.ID, TenantID: current.TenantID, SourceEventID: current.SourceEventID,
		SourceEventType: current.SourceEventType, Status: "failed", Error: "dependency_failure",
		SourceAccessRef: current.SourceAccessRef, AccessState: AccessStateResolved, AttemptedAt: time.Unix(30, 0).UTC(),
	}
	invalidFailureHistory := failureHistory
	invalidFailureHistory.AuditID = "other-audit"
	failureFence := projectionCommandTestFence(current.TenantID)
	if err := store.FailProjectionAuditCommand(ctx, ProjectionAuditCommandFailure{
		TenantID: current.TenantID, AuditID: current.ID, Operation: request.Operation,
		IdempotencyKey: request.IdempotencyKey, RequestHash: request.RequestHash, LeaseToken: claim.LeaseToken, FailureCode: "dependency_failure",
		Result: &failureResult, History: &invalidFailureHistory, Fence: &failureFence,
	}); !errors.Is(err, ErrProjectionAuditCommandConflict) {
		t.Fatalf("invalid failure history error=%v", err)
	}
	unmodified, err := store.GetProjectionAudit(ctx, current.TenantID, current.ID)
	if err != nil || unmodified.Revision != current.Revision || unmodified.Details["last_replay_status"] != nil {
		t.Fatalf("failed audit mutation was not rolled back with history: audit=%+v err=%v", unmodified, err)
	}
	if err := store.FailProjectionAuditCommand(ctx, ProjectionAuditCommandFailure{
		TenantID: current.TenantID, AuditID: current.ID, Operation: request.Operation,
		IdempotencyKey: request.IdempotencyKey, RequestHash: request.RequestHash, LeaseToken: claim.LeaseToken, FailureCode: "dependency_failure",
		Result: &failureResult, History: &failureHistory, Fence: &failureFence,
	}); err != nil {
		t.Fatalf("fail command: %v", err)
	}
	failed, err := store.ClaimProjectionAuditCommand(ctx, request)
	if err != nil || failed.Acquired || failed.State != ProjectionAuditCommandFailed || failed.FailureCode != "dependency_failure" {
		t.Fatalf("failed retry=%+v err=%v", failed, err)
	}
	current, err = store.GetProjectionAudit(ctx, seed.TenantID, seed.ID)
	if err != nil || current.Revision != failureResult.Revision+1 || current.Details["last_replay_status"] != "failed" {
		t.Fatalf("failed audit=%+v err=%v", current, err)
	}
	request.Audit = current
	request.IdempotencyKey = "failed-2"
	abortClaim, err := store.ClaimProjectionAuditCommand(ctx, request)
	if err != nil || !abortClaim.Acquired {
		t.Fatalf("claim after failure=%+v err=%v", abortClaim, err)
	}
	if err := store.FailProjectionAuditCommand(ctx, ProjectionAuditCommandFailure{
		TenantID: current.TenantID, AuditID: current.ID, Operation: request.Operation,
		IdempotencyKey: request.IdempotencyKey, RequestHash: request.RequestHash, LeaseToken: abortClaim.LeaseToken,
		FailureCode: "access_revoked",
	}); err != nil {
		t.Fatalf("abort command without audit mutation: %v", err)
	}
	aborted, err := store.ClaimProjectionAuditCommand(ctx, request)
	if err != nil || aborted.Acquired || aborted.State != ProjectionAuditCommandFailed || aborted.FailureCode != "access_revoked" {
		t.Fatalf("aborted retry=%+v err=%v", aborted, err)
	}
	afterAbort, err := store.GetProjectionAudit(ctx, current.TenantID, current.ID)
	if err != nil || afterAbort.Revision != current.Revision || afterAbort.Details["last_replay_status"] != "failed" {
		t.Fatalf("abort changed audit=%+v err=%v", afterAbort, err)
	}
}

func TestPlatformStoreProjectionAuditCommandFailureFencesSecurityState(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "contextgraph-projection-command-failure-fence"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer database.Close()
	if err := migration.NewRunner(contextGraphAuditStoreTestMigrations()).Up(ctx, database); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	prepareProjectionCommandSecurityTables(t, ctx, database)
	store := NewPlatformStore(database)
	seed := ProjectionAudit{
		ID: "audit-1", TenantID: "tenant-1", SourceEventID: "event-1", SourceEventType: "project.organization.linked",
		Status: "skipped", ReasonCode: "missing_target", SourceAccessRef: AccessRef{Kind: "organization", ID: "tenant-1", TenantID: "tenant-1"},
		ObservedAt: time.Unix(10, 0).UTC(),
	}
	if err := store.SaveProjectionAudit(ctx, seed); err != nil {
		t.Fatalf("save audit: %v", err)
	}
	audit, err := store.GetProjectionAudit(ctx, seed.TenantID, seed.ID)
	if err != nil {
		t.Fatalf("get audit: %v", err)
	}
	request := ProjectionAuditCommandClaimRequest{
		Audit: audit, Operation: ProjectionAuditCommandReplay, IdempotencyKey: "failure-fence", RequestHash: strings.Repeat("f", 64),
	}
	claim, err := store.ClaimProjectionAuditCommand(ctx, request)
	if err != nil || !claim.Acquired {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	result := audit
	result.Details = map[string]any{"last_replay_status": "failed"}
	history := ProjectionAuditReplayHistory{
		ID: "failure-fence-history", AuditID: audit.ID, TenantID: audit.TenantID, SourceEventID: audit.SourceEventID,
		SourceEventType: audit.SourceEventType, Status: "failed", Error: "dependency_failure",
		SourceAccessRef: audit.SourceAccessRef, AccessState: AccessStateResolved, AttemptedAt: time.Unix(20, 0).UTC(),
	}
	fence := projectionCommandTestFence(audit.TenantID)
	if _, err := database.Exec(ctx, `UPDATE data_policy_revision SET revision = 1 WHERE id = 'global'`); err != nil {
		t.Fatalf("advance policy revision: %v", err)
	}
	err = store.FailProjectionAuditCommand(ctx, ProjectionAuditCommandFailure{
		TenantID: audit.TenantID, AuditID: audit.ID, Operation: request.Operation, IdempotencyKey: request.IdempotencyKey,
		RequestHash: request.RequestHash, LeaseToken: claim.LeaseToken, FailureCode: "dependency_failure",
		Result: &result, History: &history, Fence: &fence,
	})
	if !errors.Is(err, ErrProjectionAuditCommandFence) || !errors.Is(err, ErrProjectionAuditCommandConflict) {
		t.Fatalf("stale failure fence error=%v", err)
	}
	current, err := store.GetProjectionAudit(ctx, audit.TenantID, audit.ID)
	if err != nil || current.Revision != audit.Revision || current.Details["last_replay_status"] != nil {
		t.Fatalf("fenced failure changed audit=%+v err=%v", current, err)
	}
	var historyCount int
	if err := database.QueryRow(ctx, `SELECT COUNT(*) FROM context_graph_projection_audit_replay_history WHERE tenant_id = ? AND id = ?`, audit.TenantID, history.ID).Scan(&historyCount); err != nil || historyCount != 0 {
		t.Fatalf("fenced failure history count=%d err=%v", historyCount, err)
	}
}

func TestPlatformStoreProjectionAuditCommandExpiresAndReleasesAbandonedChangedClaim(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "contextgraph-projection-command-abandoned"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer database.Close()
	if err := migration.NewRunner(contextGraphAuditStoreTestMigrations()).Up(ctx, database); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	store := NewPlatformStore(database)
	seed := ProjectionAudit{
		ID: "audit-1", TenantID: "tenant-1", SourceEventID: "event-1", SourceEventType: "project.organization.linked",
		Status: "skipped", ReasonCode: "missing_target", SourceAccessRef: AccessRef{Kind: "organization", ID: "tenant-1", TenantID: "tenant-1"},
		ObservedAt: time.Unix(10, 0).UTC(),
	}
	if err := store.SaveProjectionAudit(ctx, seed); err != nil {
		t.Fatalf("save audit: %v", err)
	}
	audit, err := store.GetProjectionAudit(ctx, seed.TenantID, seed.ID)
	if err != nil {
		t.Fatalf("get audit: %v", err)
	}
	request := ProjectionAuditCommandClaimRequest{
		Audit: audit, Operation: ProjectionAuditCommandReplay, IdempotencyKey: "abandoned", RequestHash: strings.Repeat("d", 64),
	}
	claim, err := store.ClaimProjectionAuditCommand(ctx, request)
	if err != nil || !claim.Acquired {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	audit.Details = map[string]any{"legacy_partial_write": true}
	if err := store.SaveProjectionAudit(ctx, audit); err != nil {
		t.Fatalf("advance audit outside command CAS: %v", err)
	}
	if _, err := database.Exec(ctx, `UPDATE context_graph_projection_audit_command_claims SET lease_expires_at = 0 WHERE tenant_id = ? AND audit_id = ?`, audit.TenantID, audit.ID); err != nil {
		t.Fatalf("expire lease: %v", err)
	}
	recovered, err := store.ClaimProjectionAuditCommand(ctx, request)
	if err != nil || recovered.Acquired || recovered.State != ProjectionAuditCommandFailed || recovered.FailureCode != "state_conflict" {
		t.Fatalf("recovered=%+v err=%v", recovered, err)
	}
	var claims int
	if err := database.QueryRow(ctx, `SELECT COUNT(*) FROM context_graph_projection_audit_command_claims WHERE tenant_id = ? AND audit_id = ?`, audit.TenantID, audit.ID).Scan(&claims); err != nil || claims != 0 {
		t.Fatalf("remaining claims=%d err=%v", claims, err)
	}
}

func TestPlatformStoreProjectionAuditCommandExpiredLeaseAllowsNewIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "contextgraph-projection-command-new-key-takeover"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer database.Close()
	if err := migration.NewRunner(contextGraphAuditStoreTestMigrations()).Up(ctx, database); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	prepareProjectionCommandSecurityTables(t, ctx, database)
	store := NewPlatformStore(database)
	seed := ProjectionAudit{
		ID: "audit-1", TenantID: "tenant-1", SourceEventID: "event-1", SourceEventType: "project.organization.linked",
		Status: "skipped", ReasonCode: "missing_target", SourceAccessRef: AccessRef{Kind: "organization", ID: "tenant-1", TenantID: "tenant-1"},
		ObservedAt: time.Unix(10, 0).UTC(),
	}
	if err := store.SaveProjectionAudit(ctx, seed); err != nil {
		t.Fatalf("save audit: %v", err)
	}
	audit, err := store.GetProjectionAudit(ctx, seed.TenantID, seed.ID)
	if err != nil {
		t.Fatalf("get audit: %v", err)
	}
	oldRequest := ProjectionAuditCommandClaimRequest{
		Audit: audit, Operation: ProjectionAuditCommandReplay, IdempotencyKey: "lost-client-key", RequestHash: strings.Repeat("e", 64),
	}
	oldClaim, err := store.ClaimProjectionAuditCommand(ctx, oldRequest)
	if err != nil || !oldClaim.Acquired {
		t.Fatalf("old claim=%+v err=%v", oldClaim, err)
	}
	if _, err := database.Exec(ctx, `UPDATE context_graph_projection_audit_command_claims SET lease_expires_at = 0 WHERE tenant_id = ? AND audit_id = ?`, audit.TenantID, audit.ID); err != nil {
		t.Fatalf("expire lease: %v", err)
	}
	newRequest := oldRequest
	newRequest.IdempotencyKey = "new-client-key"
	newRequest.RequestHash = strings.Repeat("f", 64)
	newClaim, err := store.ClaimProjectionAuditCommand(ctx, newRequest)
	if err != nil || !newClaim.Acquired || newClaim.LeaseToken == "" || newClaim.LeaseToken == oldClaim.LeaseToken {
		t.Fatalf("new claim=%+v err=%v", newClaim, err)
	}
	abandoned, err := store.ClaimProjectionAuditCommand(ctx, oldRequest)
	if err != nil || abandoned.Acquired || abandoned.State != ProjectionAuditCommandFailed || abandoned.FailureCode != "lease_expired" {
		t.Fatalf("abandoned command=%+v err=%v", abandoned, err)
	}
	result := audit
	result.Status = "replayed"
	result.ObservedAt = time.Unix(20, 0).UTC()
	history := ProjectionAuditReplayHistory{
		ID: "history-new-key", AuditID: audit.ID, TenantID: audit.TenantID, SourceEventID: audit.SourceEventID,
		SourceEventType: audit.SourceEventType, Status: "replayed", SourceAccessRef: audit.SourceAccessRef,
		AccessState: AccessStateResolved, AttemptedAt: result.ObservedAt,
	}
	if err := store.CompleteProjectionAuditCommand(ctx, ProjectionAuditCommandCompletion{
		TenantID: audit.TenantID, AuditID: audit.ID, Operation: newRequest.Operation,
		IdempotencyKey: newRequest.IdempotencyKey, RequestHash: newRequest.RequestHash, LeaseToken: newClaim.LeaseToken,
		Result: result, History: &history, Effect: noopProjectionCommandEffect, Fence: projectionCommandTestFence(audit.TenantID),
	}); err != nil {
		t.Fatalf("complete replacement command: %v", err)
	}
}
