package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestProductionOntologyGovernanceBackfillReportsProposalPhase(t *testing.T) {
	ctx := context.Background()
	core, closeStore := newProductionOntologyGovernanceBackfillStore(t, ctx)
	defer closeStore()
	proposalStore := ontology.NewPlatformMetadataProposalStore(core)
	if err := proposalStore.SaveMetadataProposal(ctx, ontology.OntologyMetadataProposal{
		ID:            "proposal-1",
		TenantID:      "tenant-1",
		TargetKind:    "object_type",
		TargetID:      "task",
		MetadataKey:   "owner_domain",
		ProposedValue: "mission",
		Source:        "deterministic.owner",
	}); err != nil {
		t.Fatalf("SaveMetadataProposal: %v", err)
	}
	if _, err := core.Exec(ctx, `UPDATE ontology_metadata_proposals SET proposal_content_hash = '' WHERE id = ?`, "proposal-1"); err != nil {
		t.Fatalf("make proposal legacy: %v", err)
	}
	var out bytes.Buffer
	if err := runOntologyGovernanceBackfill(ctx, core, productionOntologyGovernanceBackfillOptions{
		Resource: productionOntologyGovernanceBackfillProposals,
		Limit:    10,
	}, &out); err != nil {
		t.Fatalf("runOntologyGovernanceBackfill: %v", err)
	}
	var report productionOntologyGovernanceBackfillReport
	if err := json.NewDecoder(&out).Decode(&report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.Resource != productionOntologyGovernanceBackfillProposals || report.ProposalReport == nil || !report.ProposalReport.DryRun || report.ProposalReport.Resolvable != 1 || report.ReceiptReport != nil {
		t.Fatalf("report = %+v", report)
	}
}

func TestProductionOntologyGovernanceBackfillRequiresOperatorForApply(t *testing.T) {
	ctx := context.Background()
	core, closeStore := newProductionOntologyGovernanceBackfillStore(t, ctx)
	defer closeStore()
	err := runOntologyGovernanceBackfill(ctx, core, productionOntologyGovernanceBackfillOptions{
		Apply:    true,
		Resource: productionOntologyGovernanceBackfillProposals,
	}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "changed-by") {
		t.Fatalf("apply error = %v", err)
	}
}

func TestRunRejectsConflictingOntologyGovernanceBackfillModes(t *testing.T) {
	err := run(context.Background(), []string{"-ontology-governance-backfill", "-ontology-governance-backfill-apply"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("run error = %v", err)
	}
}

func newProductionOntologyGovernanceBackfillStore(t *testing.T, ctx context.Context) (*db.Store, func()) {
	t.Helper()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-governance-backfill"},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	coordinator := migration.NewCoordinator(databases)
	if err := ontology.RegisterMigrations(coordinator); err != nil {
		databases.Close()
		t.Fatalf("RegisterMigrations: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		databases.Close()
		t.Fatalf("migration up: %v", err)
	}
	core, err := databases.Store(db.RoleCore)
	if err != nil {
		databases.Close()
		t.Fatalf("core store: %v", err)
	}
	return core, func() { _ = databases.Close() }
}
