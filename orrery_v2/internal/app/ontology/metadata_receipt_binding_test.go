package ontology

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestProposalReceiptBindingNewPromotionPinsCanonicalProposal(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{
		ID:               "task",
		TenantID:         "tenant-1",
		TenantScope:      TenantScopeTenant,
		DisplayName:      "Task",
		CanonicalKeyRule: "task:{id}",
	}); err != nil {
		t.Fatalf("SaveObjectType: %v", err)
	}
	proposal, err := normalizeMetadataProposal(receiptBindingProposalFixture(), nil)
	if err != nil {
		t.Fatalf("normalize proposal: %v", err)
	}
	plan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{Registry: registry, Proposal: proposal})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotion: %v", err)
	}
	receipts := NewMemoryMetadataProposalPromotionReceiptStore()
	receipt, err := ApplyMetadataProposalPromotion(ctx, MetadataProposalPromotionApplyRequest{
		Registry:          registry,
		ReceiptStore:      receipts,
		Proposal:          proposal,
		TenantID:          proposal.TenantID,
		ActorID:           "reviewer-1",
		ActorKind:         "user",
		IdempotencyKey:    "apply-1",
		ConfirmationToken: MetadataProposalPromotionConfirmationToken(plan),
	})
	if err != nil {
		t.Fatalf("ApplyMetadataProposalPromotion: %v", err)
	}
	if receipt.ProposalRevision != proposal.Revision ||
		receipt.ProposalContentHash != proposal.ContentHash ||
		receipt.ProposalBindingStatus != PromotionReceiptProposalBindingResolved {
		t.Fatalf("receipt proposal binding = %+v", receipt)
	}
	stored, err := receipts.GetPromotionReceipt(ctx, receipt.ReceiptID)
	if err != nil {
		t.Fatalf("GetPromotionReceipt: %v", err)
	}
	if stored.ProposalRevision != proposal.Revision || stored.ProposalContentHash != proposal.ContentHash || stored.ProposalBindingStatus != PromotionReceiptProposalBindingResolved {
		t.Fatalf("stored receipt proposal binding = %+v", stored)
	}
}

func TestProposalReceiptBindingLegacyIdempotencyFingerprintRemainsCompatible(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{
		ID:               "task",
		TenantID:         "tenant-1",
		TenantScope:      TenantScopeTenant,
		DisplayName:      "Task",
		CanonicalKeyRule: "task:{id}",
	}); err != nil {
		t.Fatalf("SaveObjectType: %v", err)
	}
	proposal, err := normalizeMetadataProposal(receiptBindingProposalFixture(), nil)
	if err != nil {
		t.Fatalf("normalize proposal: %v", err)
	}
	plan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{Registry: registry, Proposal: proposal})
	if err != nil {
		t.Fatalf("DryRunMetadataProposalPromotion: %v", err)
	}
	req := MetadataProposalPromotionApplyRequest{
		Registry:          registry,
		Proposal:          proposal,
		TenantID:          proposal.TenantID,
		ActorID:           "reviewer-1",
		ActorKind:         "user",
		IdempotencyKey:    "legacy-apply-1",
		ConfirmationToken: MetadataProposalPromotionConfirmationToken(plan),
	}
	receipts := NewMemoryMetadataProposalPromotionReceiptStore()
	legacy := MetadataProposalPromotionReceipt{
		ReceiptID:             "legacy-receipt-1",
		TenantID:              proposal.TenantID,
		ProposalID:            proposal.ID,
		ProposalBindingStatus: PromotionReceiptProposalBindingLegacyUnresolved,
		ActorID:               req.ActorID,
		ActorKind:             req.ActorKind,
		IdempotencyKey:        req.IdempotencyKey,
		RequestHash:           legacyMetadataPromotionRequestHash(req, proposal, req.TenantID, req.ActorID),
		Status:                PromotionReceiptStatusApplied,
		Applied:               true,
		Changes:               append([]MetadataProposalPromotionChange(nil), plan.Changes...),
		AppliedAt:             time.Unix(10, 0).UTC(),
	}
	if err := receipts.SavePromotionReceipt(ctx, legacy); err != nil {
		t.Fatalf("SavePromotionReceipt: %v", err)
	}
	req.ReceiptStore = receipts
	got, err := ApplyMetadataProposalPromotion(ctx, req)
	if err != nil {
		t.Fatalf("ApplyMetadataProposalPromotion legacy retry: %v", err)
	}
	if got.ReceiptID != legacy.ReceiptID || got.ProposalBindingStatus != PromotionReceiptProposalBindingLegacyUnresolved {
		t.Fatalf("legacy retry receipt = %+v", got)
	}
}

func TestProposalReceiptBindingBackfillResolvesOnlyProvableReceipt(t *testing.T) {
	ctx := context.Background()
	store, closeStore := newReceiptBindingTestStore(t, ctx, "receipt-binding-backfill")
	defer closeStore()
	proposalStore := NewPlatformMetadataProposalStore(store)
	if err := proposalStore.SaveMetadataProposal(ctx, receiptBindingProposalFixture()); err != nil {
		t.Fatalf("SaveMetadataProposal: %v", err)
	}
	proposal, err := proposalStore.GetMetadataProposalForTenant(ctx, "tenant-1", "proposal-receipt-binding")
	if err != nil {
		t.Fatalf("GetMetadataProposalForTenant: %v", err)
	}
	change := MetadataProposalPromotionChange{
		Operation:       PromotionOperationAppend,
		TargetKind:      "object_type",
		TargetID:        "task",
		Field:           "allowed_actions",
		ProposedValue:   "task.complete",
		ExpectedVersion: 7,
	}
	req := MetadataProposalPromotionApplyRequest{
		ActorKind:               "user",
		ConfirmationToken:       "apply:proposal-receipt-binding:1:0",
		RegistryExpectedVersion: 7,
	}
	implicitVersionReq := req
	implicitVersionReq.RegistryExpectedVersion = 0
	receiptStore := NewPlatformMetadataProposalPromotionReceiptStore(store)
	for _, receipt := range []MetadataProposalPromotionReceipt{
		{
			ReceiptID:             "receipt-provable",
			TenantID:              proposal.TenantID,
			ProposalID:            proposal.ID,
			ProposalBindingStatus: PromotionReceiptProposalBindingLegacyUnresolved,
			ActorID:               "reviewer-1",
			ActorKind:             "user",
			IdempotencyKey:        "apply-provable",
			RequestHash:           legacyMetadataPromotionRequestHash(req, proposal, proposal.TenantID, "reviewer-1"),
			Status:                PromotionReceiptStatusApplied,
			Applied:               true,
			Changes:               []MetadataProposalPromotionChange{change},
			AppliedAt:             time.Unix(10, 0).UTC(),
		},
		{
			ReceiptID:             "receipt-provable-implicit-version",
			TenantID:              proposal.TenantID,
			ProposalID:            proposal.ID,
			ProposalBindingStatus: PromotionReceiptProposalBindingLegacyUnresolved,
			ActorID:               "reviewer-1",
			ActorKind:             "user",
			IdempotencyKey:        "apply-provable-implicit-version",
			RequestHash:           legacyMetadataPromotionRequestHash(implicitVersionReq, proposal, proposal.TenantID, "reviewer-1"),
			Status:                PromotionReceiptStatusNoop,
			Changes:               []MetadataProposalPromotionChange{change},
			AppliedAt:             time.Unix(15, 0).UTC(),
		},
		{
			ReceiptID:             "receipt-unresolved",
			TenantID:              proposal.TenantID,
			ProposalID:            proposal.ID,
			ProposalBindingStatus: PromotionReceiptProposalBindingLegacyUnresolved,
			ActorID:               "reviewer-1",
			ActorKind:             "user",
			IdempotencyKey:        "apply-unresolved",
			Status:                PromotionReceiptStatusNoop,
			Changes:               []MetadataProposalPromotionChange{change},
			AppliedAt:             time.Unix(20, 0).UTC(),
		},
	} {
		if err := receiptStore.SavePromotionReceipt(ctx, receipt); err != nil {
			t.Fatalf("SavePromotionReceipt(%s): %v", receipt.ReceiptID, err)
		}
	}
	dryRun, err := BackfillMetadataReceiptProposalBindings(ctx, store, MetadataReceiptBindingBackfillOptions{Limit: 10})
	if err != nil {
		t.Fatalf("BackfillMetadataReceiptProposalBindings dry run: %v", err)
	}
	if !dryRun.DryRun || dryRun.Inspected != 3 || dryRun.Resolved != 2 || dryRun.LegacyUnresolved != 1 || dryRun.Updated != 0 {
		t.Fatalf("dry-run report = %+v", dryRun)
	}
	apply, err := BackfillMetadataReceiptProposalBindings(ctx, store, MetadataReceiptBindingBackfillOptions{Apply: true, Limit: 10})
	if err != nil {
		t.Fatalf("BackfillMetadataReceiptProposalBindings apply: %v", err)
	}
	if apply.Updated != 2 || apply.LegacyUnresolved != 1 {
		t.Fatalf("apply report = %+v", apply)
	}
	resolved, err := receiptStore.GetPromotionReceipt(ctx, "receipt-provable")
	if err != nil {
		t.Fatalf("GetPromotionReceipt resolved: %v", err)
	}
	if resolved.ProposalBindingStatus != PromotionReceiptProposalBindingResolved ||
		resolved.ProposalRevision != proposal.Revision ||
		resolved.ProposalContentHash != proposal.ContentHash {
		t.Fatalf("resolved receipt = %+v", resolved)
	}
	unresolved, err := receiptStore.GetPromotionReceipt(ctx, "receipt-unresolved")
	if err != nil {
		t.Fatalf("GetPromotionReceipt unresolved: %v", err)
	}
	if unresolved.ProposalBindingStatus != PromotionReceiptProposalBindingLegacyUnresolved || unresolved.ProposalRevision != 0 || unresolved.ProposalContentHash != "" {
		t.Fatalf("unresolved receipt = %+v", unresolved)
	}
}

func TestProposalReceiptBindingMigrationMarksLegacyUnresolved(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "receipt-binding-migration"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	base := metadataReceiptBindingTestMigrations(false)
	if err := migration.NewRunner(base).Up(ctx, store); err != nil {
		t.Fatalf("base migration up: %v", err)
	}
	if _, err := store.Exec(ctx, `
INSERT INTO ontology_metadata_promotion_receipts (
	receipt_id, tenant_id, proposal_id, actor_id, actor_kind, idempotency_key, request_hash,
	status, applied, changes_json, issues_json, error_message, applied_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"receipt-legacy", "tenant-1", "proposal-1", "reviewer-1", "user", "apply-1", "legacy-hash",
		PromotionReceiptStatusApplied, true, "[]", "[]", "", int64(1), int64(1), int64(1)); err != nil {
		t.Fatalf("insert legacy receipt: %v", err)
	}
	if err := migration.NewRunner([]migration.Migration{ontologyMetadataReceiptProposalBindingMigration()}).Up(ctx, store); err != nil {
		t.Fatalf("binding migration up: %v", err)
	}
	var revision int64
	var contentHash string
	var status PromotionReceiptProposalBindingStatus
	if err := store.QueryRow(ctx, `SELECT proposal_revision, proposal_content_hash, proposal_binding_status FROM ontology_metadata_promotion_receipts WHERE receipt_id = ?`, "receipt-legacy").Scan(&revision, &contentHash, &status); err != nil {
		t.Fatalf("query migrated receipt: %v", err)
	}
	if revision != 0 || contentHash != "" || status != PromotionReceiptProposalBindingLegacyUnresolved {
		t.Fatalf("legacy proposal binding = %d/%q/%q", revision, contentHash, status)
	}
}

func newReceiptBindingTestStore(t *testing.T, ctx context.Context, name string) (*db.Store, func()) {
	t.Helper()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: name})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := migration.NewRunner(metadataReceiptBindingTestMigrations(true)).Up(ctx, store); err != nil {
		store.Close()
		t.Fatalf("migration up: %v", err)
	}
	return store, func() { _ = store.Close() }
}

func metadataReceiptBindingTestMigrations(includeBinding bool) []migration.Migration {
	migrations := []migration.Migration{
		ontologyRegistryMigration(),
		ontologyMappingRuleGovernanceMigration(),
		ontologyMetadataProposalMigration(),
		ontologyMetadataProposalReviewMigration(),
		ontologyMetadataProposalSourceTaxonomyMigration(),
		ontologyActionBindingsMigration(),
		ontologyMetadataProposalPromotionReceiptMigration(),
		ontologyMetadataPromotionReceiptRequestHashMigration(),
		ontologyMetadataPromotionReceiptTenantIdempotencyMigration(),
		ontologyMetadataProposalRevisionHashMigration(),
	}
	if includeBinding {
		migrations = append(migrations, ontologyMetadataReceiptProposalBindingMigration())
		migrations = append(migrations, ontologyGovernanceCursorIndexMigration())
	}
	return migrations
}

func receiptBindingProposalFixture() OntologyMetadataProposal {
	return OntologyMetadataProposal{
		ID:            "proposal-receipt-binding",
		TenantID:      "tenant-1",
		TargetKind:    "object_type",
		TargetID:      "task",
		MetadataKey:   "allowed_action",
		ProposedValue: "task.complete",
		Source:        "deterministic.action_type_binding",
		Status:        MetadataProposalStatusAccepted,
	}
}
