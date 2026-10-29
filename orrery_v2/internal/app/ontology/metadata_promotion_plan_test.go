package ontology

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestMetadataPromotionSecurityRevisionFenceUsesConditionalRowLocks(t *testing.T) {
	tx := &metadataPromotionFenceTx{revisions: map[string]int64{
		"rebac_authorization_revision": 4,
		"data_policy_revision":         9,
	}}
	fence := metadataPromotionSecurityRevisionTx{tx: tx}
	if err := fence.FenceSecurityRevisions(context.Background(), MetadataPromotionSecurityRevisions{
		Authorization: "authorization:4", Policy: "policy:9",
	}); err != nil {
		t.Fatalf("FenceSecurityRevisions: %v", err)
	}
	if len(tx.queries) != 2 || !strings.Contains(tx.queries[0], "revision = ?") || !strings.Contains(tx.queries[1], "revision = ?") {
		t.Fatalf("conditional revision fences = %v", tx.queries)
	}

	tx.revisions["rebac_authorization_revision"] = 5
	if err := fence.FenceSecurityRevisions(context.Background(), MetadataPromotionSecurityRevisions{
		Authorization: "authorization:4", Policy: "policy:9",
	}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale revision fence error = %v", err)
	}
	if err := fence.FenceSecurityRevisions(context.Background(), MetadataPromotionSecurityRevisions{
		Authorization: "untrusted", Policy: "policy:9",
	}); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("invalid revision fence error = %v", err)
	}
}

type metadataPromotionFenceTx struct {
	revisions map[string]int64
	bindings  map[string]metadataPromotionFenceBinding
	queries   []string
}

type metadataPromotionFenceBinding struct {
	version int64
	active  bool
}

func (t *metadataPromotionFenceTx) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	if strings.Contains(query, "ontology_scope_bindings") {
		t.queries = append(t.queries, query)
		tenantID, tenantOK := args[0].(string)
		expected, versionOK := args[1].(int64)
		binding, exists := t.bindings[tenantID]
		if !tenantOK || !versionOK || !exists || !binding.active || binding.version != expected {
			return metadataPromotionFenceResult(0), nil
		}
		return metadataPromotionFenceResult(1), nil
	}
	table := ""
	for _, candidate := range []string{"rebac_authorization_revision", "data_policy_revision"} {
		if strings.Contains(query, candidate) {
			table = candidate
			break
		}
	}
	t.queries = append(t.queries, query)
	expected, ok := args[0].(int64)
	if table == "" || !ok || t.revisions[table] != expected {
		return metadataPromotionFenceResult(0), nil
	}
	return metadataPromotionFenceResult(1), nil
}

func TestMetadataPromotionScopeBindingFenceLocksPersistedVersion(t *testing.T) {
	tx := &metadataPromotionFenceTx{bindings: map[string]metadataPromotionFenceBinding{
		"tenant-1": {version: 4, active: true},
	}}
	fence := metadataPromotionScopeBindingTx{tx: tx}
	expected := MetadataPromotionScopeBinding{
		TenantID: "tenant-1", BindingVersion: 4, PersistedBindingVersion: 4, PersistenceRequired: true,
	}
	if err := fence.FenceScopeBinding(context.Background(), expected); err != nil {
		t.Fatalf("FenceScopeBinding: %v", err)
	}
	if len(tx.queries) != 1 || !strings.Contains(tx.queries[0], "binding_version = ?") || !strings.Contains(tx.queries[0], "state = 'active'") {
		t.Fatalf("conditional binding fence = %v", tx.queries)
	}

	binding := tx.bindings["tenant-1"]
	binding.version = 5
	tx.bindings["tenant-1"] = binding
	if err := fence.FenceScopeBinding(context.Background(), expected); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale binding fence error = %v", err)
	}
	delete(tx.bindings, "tenant-1")
	if err := fence.FenceScopeBinding(context.Background(), expected); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("deleted binding fence error = %v", err)
	}
	if err := fence.FenceScopeBinding(context.Background(), MetadataPromotionScopeBinding{
		TenantID: "tenant-1", BindingVersion: 1,
	}); err != nil {
		t.Fatalf("deterministic binding fence: %v", err)
	}
	if err := fence.FenceScopeBinding(context.Background(), MetadataPromotionScopeBinding{
		TenantID: "tenant-1", BindingVersion: 1, PersistenceRequired: true,
	}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("missing required binding fence error = %v", err)
	}
}

func (*metadataPromotionFenceTx) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("unexpected query")
}

func (*metadataPromotionFenceTx) QueryRowContext(context.Context, string, ...any) *sql.Row {
	return nil
}

type metadataPromotionFenceResult int64

func (r metadataPromotionFenceResult) LastInsertId() (int64, error) { return 0, nil }
func (r metadataPromotionFenceResult) RowsAffected() (int64, error) { return int64(r), nil }

func TestMetadataPromotionPlanIDIsRandomAndOpaque(t *testing.T) {
	first, err := NewMetadataPromotionPlanID()
	if err != nil {
		t.Fatalf("NewMetadataPromotionPlanID first: %v", err)
	}
	second, err := NewMetadataPromotionPlanID()
	if err != nil {
		t.Fatalf("NewMetadataPromotionPlanID second: %v", err)
	}
	if first == second || !strings.HasPrefix(first, "opplan_") || len(first) < 48 {
		t.Fatalf("plan ids are not opaque random values: %q %q", first, second)
	}
}

func TestMetadataPromotionPlanCanonicalDiffRejectsTampering(t *testing.T) {
	changes := []MetadataProposalPromotionChange{
		{Operation: PromotionOperationUpdate, TargetKind: "mapping_rule", TargetID: "b", Field: "source_domain", CurrentValue: "mission", ProposedValue: "planning", ExpectedVersion: 2, CurrentVersion: 2},
		{Operation: PromotionOperationAppend, TargetKind: "object_type", TargetID: "a", Field: "allowed_actions", CurrentValue: "read", ProposedValue: "manage", ExpectedVersion: 1, CurrentVersion: 1},
	}
	canonical, hash, err := CanonicalMetadataPromotionPlanDiff(changes)
	if err != nil {
		t.Fatalf("CanonicalMetadataPromotionPlanDiff: %v", err)
	}
	if canonical[0].TargetKind != "mapping_rule" || !isSecureMetadataPromotionHash(hash) {
		t.Fatalf("canonical diff = %+v hash = %q", canonical, hash)
	}
	record := testMetadataPromotionPlanRecord(t)
	record.Diff = changes
	record.DiffHash = hash
	record.Targets = []MetadataPromotionPlanTarget{
		{Kind: "mapping_rule", ID: "b", DefinitionTenantID: "tenant-1", Version: 2, ContentHash: secureMetadataPromotionHash([]byte("mapping-rule-b-v2"))},
		{Kind: "object_type", ID: "a", DefinitionTenantID: "tenant-1", Version: 1, ContentHash: secureMetadataPromotionHash([]byte("object-type-a-v1"))},
	}
	if _, err := normalizeMetadataPromotionPlan(record, nil); err != nil {
		t.Fatalf("normalize canonical record: %v", err)
	}
	record.Diff[0].ProposedValue = "tampered"
	if _, err := normalizeMetadataPromotionPlan(record, nil); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("tampered diff error = %v", err)
	}
}

func TestMetadataPromotionPlanMemoryStoreIsSingleUse(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(100, 0).UTC()
	store := NewMemoryMetadataPromotionPlanStore()
	store.now = func() time.Time { return now }
	record := testMetadataPromotionPlanRecord(t)
	record.CreatedAt = now
	record.UpdatedAt = now
	record.ExpiresAt = now.Add(10 * time.Minute)
	if err := store.SaveMetadataPromotionPlan(ctx, record); err != nil {
		t.Fatalf("SaveMetadataPromotionPlan: %v", err)
	}
	if err := store.SaveMetadataPromotionPlan(ctx, record); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("duplicate plan error = %v", err)
	}
	got, err := store.GetMetadataPromotionPlan(ctx, record.PlanID)
	if err != nil {
		t.Fatalf("GetMetadataPromotionPlan: %v", err)
	}
	got.Diff[0].ProposedValue = "mutated"
	again, err := store.GetMetadataPromotionPlan(ctx, record.PlanID)
	if err != nil || again.Diff[0].ProposedValue == "mutated" {
		t.Fatalf("stored plan was not cloned: %+v err=%v", again, err)
	}
	now = now.Add(time.Minute)
	consumed, err := store.TransitionMetadataPromotionPlan(ctx, MetadataPromotionPlanTransition{
		PlanID: record.PlanID, ExpectedState: MetadataPromotionPlanActive, State: MetadataPromotionPlanConsumed,
		IdempotencyKey: "apply-1", ReceiptID: "receipt-1",
	})
	if err != nil {
		t.Fatalf("TransitionMetadataPromotionPlan: %v", err)
	}
	if consumed.State != MetadataPromotionPlanConsumed || consumed.IdempotencyKey != "apply-1" || consumed.ReceiptID != "receipt-1" || !consumed.UpdatedAt.Equal(now) {
		t.Fatalf("consumed plan = %+v", consumed)
	}
	if _, err := store.TransitionMetadataPromotionPlan(ctx, MetadataPromotionPlanTransition{
		PlanID: record.PlanID, ExpectedState: MetadataPromotionPlanActive, State: MetadataPromotionPlanInvalidated,
		InvalidationReason: "changed",
	}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("second transition error = %v", err)
	}
}

func TestMetadataPromotionPlanPlatformStoreRoundTripsAndChecksState(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "metadata-promotion-plan-store"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner([]migration.Migration{ontologyMetadataPromotionPlanMigration()}).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	now := time.Unix(200, 0).UTC()
	plans := NewPlatformMetadataPromotionPlanStore(store)
	plans.now = func() time.Time { return now }
	record := testMetadataPromotionPlanRecord(t)
	record.CreatedAt = now
	record.UpdatedAt = now
	record.ExpiresAt = now.Add(10 * time.Minute)
	if err := plans.SaveMetadataPromotionPlan(ctx, record); err != nil {
		t.Fatalf("SaveMetadataPromotionPlan: %v", err)
	}
	stored, err := plans.GetMetadataPromotionPlanForUpdate(ctx, record.PlanID)
	if err != nil {
		t.Fatalf("GetMetadataPromotionPlanForUpdate: %v", err)
	}
	if stored.PrincipalKey != "principal-key" || stored.ProposalRevision != 3 || stored.Targets[0].Version != 2 || stored.DiffHash == "" {
		t.Fatalf("stored plan = %+v", stored)
	}
	now = now.Add(2 * time.Minute)
	invalidated, err := plans.TransitionMetadataPromotionPlan(ctx, MetadataPromotionPlanTransition{
		PlanID: record.PlanID, ExpectedState: MetadataPromotionPlanActive, State: MetadataPromotionPlanInvalidated,
		InvalidationReason: "proposal_changed",
	})
	if err != nil {
		t.Fatalf("TransitionMetadataPromotionPlan: %v", err)
	}
	if invalidated.State != MetadataPromotionPlanInvalidated || invalidated.InvalidationReason != "proposal_changed" || !invalidated.UpdatedAt.Equal(now) {
		t.Fatalf("invalidated plan = %+v", invalidated)
	}
	if _, err := plans.TransitionMetadataPromotionPlan(ctx, MetadataPromotionPlanTransition{
		PlanID: record.PlanID, ExpectedState: MetadataPromotionPlanActive, State: MetadataPromotionPlanConsumed,
		IdempotencyKey: "apply-1", ReceiptID: "receipt-1",
	}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale state transition error = %v", err)
	}
}

func TestMetadataPromotionPlanMigrationCreatesIndexes(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "metadata-promotion-plan-migration"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner([]migration.Migration{ontologyMetadataPromotionPlanMigration()}).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	if exists, err := store.TableExists(ctx, "ontology_metadata_promotion_plans"); err != nil || !exists {
		t.Fatalf("promotion plan table exists = %v err=%v", exists, err)
	}
	for _, index := range []string{
		"idx_ontology_metadata_promotion_plans_active",
		"idx_ontology_metadata_promotion_plans_proposal",
		"idx_ontology_metadata_promotion_plans_idempotency",
		"idx_ontology_metadata_promotion_plans_receipt",
	} {
		var found string
		if err := store.QueryRow(ctx, `SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&found); err != nil {
			t.Fatalf("missing index %s: %v", index, err)
		}
	}
}

func testMetadataPromotionPlanRecord(t *testing.T) MetadataPromotionPlanRecord {
	t.Helper()
	diff, diffHash, err := CanonicalMetadataPromotionPlanDiff([]MetadataProposalPromotionChange{{
		Operation: PromotionOperationUpdate, TargetKind: "mapping_rule", TargetID: "mission.created",
		Field: "source_domain", CurrentValue: "mission", ProposedValue: "planning", ExpectedVersion: 2, CurrentVersion: 2,
	}})
	if err != nil {
		t.Fatalf("canonical diff: %v", err)
	}
	now := time.Unix(100, 0).UTC()
	return MetadataPromotionPlanRecord{
		PlanID: "opplan_test", PrincipalKey: "principal-key", TenantID: "tenant-1", BindingVersion: 4,
		ProposalID: "proposal-1", ProposalRevision: 3, ProposalContentHash: secureMetadataPromotionHash([]byte("proposal-v3")),
		Targets: []MetadataPromotionPlanTarget{{
			Kind: "mapping_rule", ID: "mission.created", DefinitionTenantID: "tenant-1", Version: 2,
			ContentHash: secureMetadataPromotionHash([]byte("mapping-rule-v2")),
		}},
		DiffHash: diffHash, Diff: diff, AuthorizationRevision: "auth-5", PolicyRevision: "policy-7",
		ExpiresAt: now.Add(10 * time.Minute), State: MetadataPromotionPlanActive, CreatedAt: now, UpdatedAt: now,
	}
}
