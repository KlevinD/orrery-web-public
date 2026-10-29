package ontology

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestProposalRevisionCanonicalHashIgnoresEvidenceOrdering(t *testing.T) {
	first := OntologyMetadataProposal{
		TenantID:      " tenant-1 ",
		TargetKind:    "Object_Type",
		TargetID:      "Customer",
		MetadataKey:   "Owner_Domain",
		ProposedValue: " crm ",
		Source:        "deterministic.owner",
		EvidenceRefs: []MetadataEvidenceRef{
			{Kind: "document", ID: "doc-2", Metadata: map[string]string{"b": "2", "a": "1"}},
			{Kind: "document", ID: "doc-1"},
		},
	}
	second := first
	second.EvidenceRefs = []MetadataEvidenceRef{
		{Kind: "document", ID: "doc-1"},
		{Kind: "document", ID: "doc-2", Metadata: map[string]string{"a": "1", "b": "2"}},
	}
	firstHash := CanonicalMetadataProposalContentHash(first)
	secondHash := CanonicalMetadataProposalContentHash(second)
	if firstHash == "" || firstHash != secondHash {
		t.Fatalf("canonical hashes = %q and %q", firstHash, secondHash)
	}
}

func TestProposalRevisionCanonicalHashNormalizesMetadataKeyCollisionsDeterministically(t *testing.T) {
	proposal := proposalRevisionFixture("proposal-hash-collision")
	proposal.EvidenceRefs[0].Metadata = map[string]string{" key ": "first", "key": "second"}
	want := CanonicalMetadataProposalContentHash(proposal)
	for range 100 {
		if got := CanonicalMetadataProposalContentHash(proposal); got != want {
			t.Fatalf("canonical hash changed from %q to %q", want, got)
		}
	}
}

func TestProposalRevisionDecisionRequiresExpectedRevision(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryMetadataProposalStore()
	if err := store.SaveMetadataProposal(ctx, proposalRevisionFixture("proposal-decision")); err != nil {
		t.Fatalf("SaveMetadataProposal: %v", err)
	}
	before := store.proposals["proposal-decision"]
	if before.Revision != 1 || before.ContentHash == "" {
		t.Fatalf("initial proposal = %+v", before)
	}
	if _, err := store.DecideMetadataProposal(ctx, MetadataProposalDecision{
		ProposalID: "proposal-decision",
		Status:     MetadataProposalStatusAccepted,
		ActorID:    "reviewer-1",
		Reason:     "approved",
	}); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("missing expected revision error = %v", err)
	}
	if _, err := store.DecideMetadataProposal(ctx, MetadataProposalDecision{
		ProposalID:       "proposal-decision",
		ExpectedRevision: 2,
		Status:           MetadataProposalStatusAccepted,
		ActorID:          "reviewer-1",
		Reason:           "approved",
	}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale expected revision error = %v", err)
	}
	decided, err := store.DecideMetadataProposal(ctx, MetadataProposalDecision{
		ProposalID:       "proposal-decision",
		ExpectedRevision: 1,
		Status:           MetadataProposalStatusAccepted,
		ActorID:          "reviewer-1",
		Reason:           "approved",
	})
	if err != nil {
		t.Fatalf("DecideMetadataProposal: %v", err)
	}
	if decided.Revision != 2 || decided.ContentHash != before.ContentHash {
		t.Fatalf("decided proposal = %+v", decided)
	}
}

func TestProposalRevisionPlatformContentMutationIsCompareAndSwap(t *testing.T) {
	ctx := context.Background()
	store, closeStore := newProposalRevisionTestStore(t, ctx, "proposal-revision-cas")
	defer closeStore()
	proposals := NewPlatformMetadataProposalStore(store)
	proposal := proposalRevisionFixture("proposal-cas")
	if err := proposals.SaveMetadataProposal(ctx, proposal); err != nil {
		t.Fatalf("SaveMetadataProposal: %v", err)
	}
	initial, err := proposals.GetMetadataProposalForTenant(ctx, proposal.TenantID, proposal.ID)
	if err != nil {
		t.Fatalf("GetMetadataProposal: %v", err)
	}
	unchanged, err := proposals.SaveMetadataProposalWithExpectedRevision(ctx, proposal, initial.Revision)
	if err != nil {
		t.Fatalf("idempotent SaveMetadataProposalWithExpectedRevision: %v", err)
	}
	if unchanged.Revision != initial.Revision || unchanged.ContentHash != initial.ContentHash {
		t.Fatalf("idempotent update changed proposal = %+v", unchanged)
	}
	proposal.Reason = "new canonical evidence rationale"
	updated, err := proposals.SaveMetadataProposalWithExpectedRevision(ctx, proposal, initial.Revision)
	if err != nil {
		t.Fatalf("SaveMetadataProposalWithExpectedRevision: %v", err)
	}
	if updated.Revision != 2 || updated.ContentHash == initial.ContentHash {
		t.Fatalf("updated proposal = %+v", updated)
	}
	proposal.Reason = "stale mutation"
	if _, err := proposals.SaveMetadataProposalWithExpectedRevision(ctx, proposal, initial.Revision); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale content mutation error = %v", err)
	}
	if err := proposals.SaveMetadataProposal(ctx, proposal); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("unguarded content mutation error = %v", err)
	}
	proposal.TenantID = "tenant-2"
	if _, err := proposals.SaveMetadataProposalWithExpectedRevision(ctx, proposal, updated.Revision); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant mutation error = %v, want ErrNotFound", err)
	}
}

func TestProposalRevisionBackfillReportsThenAppliesDeterministically(t *testing.T) {
	ctx := context.Background()
	store, closeStore := newProposalRevisionTestStore(t, ctx, "proposal-revision-backfill")
	defer closeStore()
	proposals := NewPlatformMetadataProposalStore(store)
	proposal := proposalRevisionFixture("proposal-legacy")
	if err := proposals.SaveMetadataProposal(ctx, proposal); err != nil {
		t.Fatalf("SaveMetadataProposal: %v", err)
	}
	if _, err := store.Exec(ctx, `UPDATE ontology_metadata_proposals SET proposal_revision = 0, proposal_content_hash = '' WHERE id = ?`, proposal.ID); err != nil {
		t.Fatalf("make proposal legacy: %v", err)
	}
	dryRun, err := BackfillMetadataProposalRevisions(ctx, store, MetadataProposalRevisionBackfillOptions{Limit: 10})
	if err != nil {
		t.Fatalf("BackfillMetadataProposalRevisions dry run: %v", err)
	}
	if !dryRun.DryRun || dryRun.Inspected != 1 || dryRun.Resolvable != 1 || dryRun.Updated != 0 || dryRun.Unresolved != 0 {
		t.Fatalf("dry-run report = %+v", dryRun)
	}
	apply, err := BackfillMetadataProposalRevisions(ctx, store, MetadataProposalRevisionBackfillOptions{Apply: true, Limit: 10})
	if err != nil {
		t.Fatalf("BackfillMetadataProposalRevisions apply: %v", err)
	}
	if apply.DryRun || apply.Updated != 1 {
		t.Fatalf("apply report = %+v", apply)
	}
	got, err := proposals.GetMetadataProposalForTenant(ctx, proposal.TenantID, proposal.ID)
	if err != nil {
		t.Fatalf("GetMetadataProposal: %v", err)
	}
	if got.Revision != 1 || got.ContentHash != CanonicalMetadataProposalContentHash(got) {
		t.Fatalf("backfilled proposal = %+v", got)
	}
}

func TestProposalRevisionMigrationInitializesLegacyRevision(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "proposal-revision-migration"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	base := []migration.Migration{
		ontologyRegistryMigration(),
		ontologyMappingRuleGovernanceMigration(),
		ontologyMetadataProposalMigration(),
		ontologyMetadataProposalReviewMigration(),
		ontologyMetadataProposalSourceTaxonomyMigration(),
	}
	if err := migration.NewRunner(base).Up(ctx, store); err != nil {
		t.Fatalf("base migration up: %v", err)
	}
	if _, err := store.Exec(ctx, `
INSERT INTO ontology_metadata_proposals (
	id, tenant_id, target_kind, target_id, metadata_key, proposed_value, source,
	confidence, status, reason, evidence_refs_json, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"proposal-legacy", "tenant-1", "object_type", "customer", "owner_domain", "crm", "deterministic.owner",
		1, MetadataProposalStatusProposed, "legacy", "[]", int64(1), int64(1)); err != nil {
		t.Fatalf("insert legacy proposal: %v", err)
	}
	if err := migration.NewRunner([]migration.Migration{ontologyMetadataProposalRevisionHashMigration()}).Up(ctx, store); err != nil {
		t.Fatalf("revision migration up: %v", err)
	}
	var revision int64
	var contentHash string
	if err := store.QueryRow(ctx, `SELECT proposal_revision, proposal_content_hash FROM ontology_metadata_proposals WHERE id = ?`, "proposal-legacy").Scan(&revision, &contentHash); err != nil {
		t.Fatalf("query migrated proposal: %v", err)
	}
	if revision != 1 || contentHash != "" {
		t.Fatalf("migrated revision/hash = %d/%q", revision, contentHash)
	}
}

func newProposalRevisionTestStore(t *testing.T, ctx context.Context, name string) (*db.Store, func()) {
	t.Helper()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: name})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	migrations := []migration.Migration{
		ontologyRegistryMigration(),
		ontologyMappingRuleGovernanceMigration(),
		ontologyMetadataProposalMigration(),
		ontologyMetadataProposalReviewMigration(),
		ontologyMetadataProposalSourceTaxonomyMigration(),
		ontologyMetadataProposalRevisionHashMigration(),
	}
	if err := migration.NewRunner(migrations).Up(ctx, store); err != nil {
		store.Close()
		t.Fatalf("migration up: %v", err)
	}
	return store, func() { _ = store.Close() }
}

func proposalRevisionFixture(id string) OntologyMetadataProposal {
	return OntologyMetadataProposal{
		ID:            id,
		TenantID:      "tenant-1",
		TargetKind:    "object_type",
		TargetID:      "customer",
		MetadataKey:   "owner_domain",
		ProposedValue: "crm",
		Source:        "deterministic.owner",
		Reason:        "derived from source ownership",
		EvidenceRefs:  []MetadataEvidenceRef{{Kind: "document", ID: "doc-1"}},
	}
}
