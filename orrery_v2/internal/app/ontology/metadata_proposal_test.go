package ontology

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestInferMappingRuleMetadataProposals(t *testing.T) {
	proposals, err := InferMappingRuleMetadataProposals(MappingRule{
		ID:                 "knowledge.document.metadata.updated",
		EventType:          "knowledge.document.metadata.updated",
		SourceDomain:       "knowledge",
		SourceRef:          "internal/app/knowledgeadmin",
		EventSchemaVersion: "v1",
		RuleHash:           "rulehash",
	})
	if err != nil {
		t.Fatalf("InferMappingRuleMetadataProposals: %v", err)
	}
	if len(proposals) != 4 {
		t.Fatalf("proposal count = %d, want 4: %+v", len(proposals), proposals)
	}
	wantValues := map[string]string{
		"source_domain":        "knowledge",
		"source_ref":           "internal/app/knowledgeadmin",
		"event_schema_version": "v1",
		"rule_hash":            "rulehash",
	}
	for _, proposal := range proposals {
		if proposal.ID == "" || proposal.TargetKind != "mapping_rule" || proposal.TargetID != "knowledge.document.metadata.updated" {
			t.Fatalf("proposal identity = %+v", proposal)
		}
		if proposal.Source != "deterministic.mapping_rule_metadata" || proposal.Status != MetadataProposalStatusProposed {
			t.Fatalf("proposal status/source = %+v", proposal)
		}
		if proposal.SourceKind != MetadataProposalSourceDeterministic || proposal.SourceRef != "mapping_rule_metadata" || proposal.SourceVersion != "v1" {
			t.Fatalf("proposal source taxonomy = %+v", proposal)
		}
		if proposal.Confidence <= 0 || proposal.Confidence > 1 {
			t.Fatalf("proposal confidence = %+v", proposal)
		}
		if proposal.Reason == "" || len(proposal.EvidenceRefs) == 0 {
			t.Fatalf("proposal evidence/reason = %+v", proposal)
		}
		if wantValues[proposal.MetadataKey] != proposal.ProposedValue {
			t.Fatalf("proposal value for %s = %q, want %q", proposal.MetadataKey, proposal.ProposedValue, wantValues[proposal.MetadataKey])
		}
		delete(wantValues, proposal.MetadataKey)
	}
	if len(wantValues) != 0 {
		t.Fatalf("missing metadata proposals: %+v", wantValues)
	}
}

func TestMemoryMetadataProposalStoreRoundTrips(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryMetadataProposalStore()
	proposals, err := InferMappingRuleMetadataProposals(MappingRule{
		ID:                 "memory.record.accepted",
		EventType:          "memory.record.accepted",
		SourceDomain:       "memory",
		SourceRef:          "internal/app/memoryadmin",
		EventSchemaVersion: "v1",
		RuleHash:           "hash-memory",
	})
	if err != nil {
		t.Fatalf("InferMappingRuleMetadataProposals: %v", err)
	}
	for _, proposal := range proposals {
		if err := store.SaveMetadataProposal(ctx, proposal); err != nil {
			t.Fatalf("SaveMetadataProposal: %v", err)
		}
	}

	page, err := store.ListMetadataProposalPage(ctx, MetadataProposalQuery{
		TargetKind: "mapping_rule",
		TargetID:   "memory.record.accepted",
		SourceKind: MetadataProposalSourceDeterministic,
		Status:     string(MetadataProposalStatusProposed),
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("ListMetadataProposalPage: %v", err)
	}
	if page.Page.Total != 4 || page.Page.Returned != 4 || len(page.Proposals) != 4 {
		t.Fatalf("page = %+v", page)
	}
	if page.Proposals[0].TargetID != "memory.record.accepted" || page.Proposals[0].Status != MetadataProposalStatusProposed {
		t.Fatalf("first proposal = %+v", page.Proposals[0])
	}
	for _, proposal := range page.Proposals {
		if proposal.SourceKind != MetadataProposalSourceDeterministic || proposal.SourceRef != "mapping_rule_metadata" || proposal.SourceVersion != "v1" {
			t.Fatalf("proposal source taxonomy = %+v", proposal)
		}
	}
}

func TestMemoryMetadataProposalStoreFiltersBySourceKind(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryMetadataProposalStore()
	for _, proposal := range []OntologyMetadataProposal{
		{
			ID:            "proposal-deterministic",
			TenantID:      "tenant-1",
			TargetKind:    "mapping_rule",
			TargetID:      "project.created",
			MetadataKey:   "source_domain",
			ProposedValue: "mission_control",
			Source:        "deterministic.mapping_rule_metadata",
			SourceKind:    MetadataProposalSourceDeterministic,
		},
		{
			ID:            "proposal-model",
			TenantID:      "tenant-1",
			TargetKind:    "object_type",
			TargetID:      "customer",
			MetadataKey:   "owner_domain",
			ProposedValue: "crm",
			Source:        "model_inference.ontology_designer",
			SourceKind:    MetadataProposalSourceModelInference,
			SourceRef:     "ontology-designer-v1",
			SourceVersion: "2026-06-20",
		},
	} {
		if err := store.SaveMetadataProposal(ctx, proposal); err != nil {
			t.Fatalf("SaveMetadataProposal(%s): %v", proposal.ID, err)
		}
	}

	page, err := store.ListMetadataProposalPage(ctx, MetadataProposalQuery{
		TenantID:   "tenant-1",
		SourceKind: MetadataProposalSourceModelInference,
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("ListMetadataProposalPage: %v", err)
	}
	if page.Page.Total != 1 || len(page.Proposals) != 1 || page.Proposals[0].ID != "proposal-model" {
		t.Fatalf("model source page = %+v", page)
	}
	if page.Proposals[0].SourceRef != "ontology-designer-v1" || page.Proposals[0].SourceVersion != "2026-06-20" {
		t.Fatalf("model proposal source detail = %+v", page.Proposals[0])
	}
}

func TestMemoryMetadataProposalStorePreservesModelInferenceTrace(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryMetadataProposalStore()
	for _, proposal := range []OntologyMetadataProposal{{
		ID:            "proposal-model",
		TenantID:      "tenant-1",
		TargetKind:    "object_type",
		TargetID:      "customer",
		MetadataKey:   "owner_domain",
		ProposedValue: "crm",
		Source:        "model_inference.ontology_designer",
		SourceKind:    MetadataProposalSourceModelInference,
		SourceRef:     "ontology-designer-v1",
		SourceVersion: "2026-06-20",
		Status:        MetadataProposalStatusProposed,
		EvidenceRefs: []MetadataEvidenceRef{{
			Kind: "model_run",
			ID:   "run-1",
			Metadata: map[string]string{
				"model":       "ontology-designer",
				"version":     "2026-06-20",
				"input_hash":  "input-hash-1",
				"prompt_hash": "prompt-hash-1",
			},
		}},
	}, {
		ID:            "proposal-negative-confidence",
		TenantID:      "tenant-1",
		TargetKind:    "object_type",
		TargetID:      "account",
		MetadataKey:   "owner_domain",
		ProposedValue: "sales",
		Source:        "model_inference.ontology_designer",
		SourceKind:    MetadataProposalSourceModelInference,
		Confidence:    -0.2,
		Status:        MetadataProposalStatusProposed,
	}} {
		if err := store.SaveMetadataProposal(ctx, proposal); err != nil {
			t.Fatalf("SaveMetadataProposal(%s): %v", proposal.ID, err)
		}
	}

	page, err := store.ListMetadataProposalPage(ctx, MetadataProposalQuery{
		TenantID:   "tenant-1",
		SourceKind: MetadataProposalSourceModelInference,
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("ListMetadataProposalPage: %v", err)
	}
	if len(page.Proposals) != 2 {
		t.Fatalf("proposal page = %+v", page)
	}
	var got OntologyMetadataProposal
	var negative OntologyMetadataProposal
	for _, proposal := range page.Proposals {
		if proposal.ID == "proposal-model" {
			got = proposal
		}
		if proposal.ID == "proposal-negative-confidence" {
			negative = proposal
		}
	}
	if got.Confidence != 0 {
		t.Fatalf("model inference confidence defaulted to %v, want missing confidence preserved as 0", got.Confidence)
	}
	if negative.Confidence != 0 {
		t.Fatalf("negative model inference confidence normalized to %v, want 0", negative.Confidence)
	}
	if len(got.EvidenceRefs) != 1 ||
		got.EvidenceRefs[0].Kind != "model_run" ||
		got.EvidenceRefs[0].ID != "run-1" ||
		got.EvidenceRefs[0].Metadata["model"] != "ontology-designer" ||
		got.EvidenceRefs[0].Metadata["input_hash"] != "input-hash-1" {
		t.Fatalf("model evidence trace = %+v", got.EvidenceRefs)
	}
}

func TestMemoryMetadataProposalStoreDecidesProposal(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryMetadataProposalStore()
	if err := store.SaveMetadataProposal(ctx, OntologyMetadataProposal{
		ID:            "proposal-1",
		TenantID:      "tenant-1",
		TargetKind:    "mapping_rule",
		TargetID:      "project.created",
		MetadataKey:   "source_domain",
		ProposedValue: "mission_control",
		Source:        "deterministic.mapping_rule_metadata",
	}); err != nil {
		t.Fatalf("SaveMetadataProposal: %v", err)
	}

	decided, err := store.DecideMetadataProposal(ctx, MetadataProposalDecision{
		ProposalID:       "proposal-1",
		ExpectedRevision: 1,
		Status:           MetadataProposalStatusAccepted,
		ActorID:          "operator-1",
		ActorKind:        "user",
		Reason:           "Matches source domain governance.",
		EvidenceRefs: []MetadataEvidenceRef{{
			Kind: "review_note",
			ID:   "note-1",
		}},
	})
	if err != nil {
		t.Fatalf("DecideMetadataProposal: %v", err)
	}
	if decided.Status != MetadataProposalStatusAccepted ||
		decided.PreviousStatus != MetadataProposalStatusProposed ||
		decided.ReviewedBy != "operator-1" ||
		decided.ReviewedByKind != "user" ||
		decided.ReviewReason != "Matches source domain governance." ||
		len(decided.ReviewEvidenceRefs) != 1 ||
		decided.ReviewedAt.IsZero() {
		t.Fatalf("decided proposal = %+v", decided)
	}

	if _, err := store.DecideMetadataProposal(ctx, MetadataProposalDecision{
		ProposalID:       "proposal-1",
		ExpectedRevision: 1,
		Status:           MetadataProposalStatusRejected,
		ActorID:          "operator-2",
		Reason:           "Second decision should be blocked.",
	}); err == nil {
		t.Fatalf("DecideMetadataProposal allowed a second decision")
	}
}

func TestMemoryMetadataProposalStoreRejectsAcceptWhenAnalysisHasBlockingIssue(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryMetadataProposalStore()
	if err := store.SaveMetadataProposal(ctx, OntologyMetadataProposal{
		ID:            "proposal-model-missing-trace",
		TenantID:      "tenant-1",
		TargetKind:    "object_type",
		TargetID:      "customer",
		MetadataKey:   "owner_domain",
		ProposedValue: "crm",
		Source:        "model_inference.ontology_designer",
		SourceKind:    MetadataProposalSourceModelInference,
		Confidence:    0.91,
		Status:        MetadataProposalStatusProposed,
		EvidenceRefs:  []MetadataEvidenceRef{{Kind: "document", ID: "brief-1"}},
	}); err != nil {
		t.Fatalf("SaveMetadataProposal: %v", err)
	}

	if _, err := store.DecideMetadataProposal(ctx, MetadataProposalDecision{
		ProposalID:       "proposal-model-missing-trace",
		ExpectedRevision: 1,
		Status:           MetadataProposalStatusAccepted,
		ActorID:          "operator-1",
		Reason:           "Accepting without model run trace should be blocked.",
	}); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("DecideMetadataProposal accept error = %v, want ErrInvalidDefinition", err)
	}

	decided, err := store.DecideMetadataProposal(ctx, MetadataProposalDecision{
		ProposalID:       "proposal-model-missing-trace",
		ExpectedRevision: 1,
		Status:           MetadataProposalStatusRejected,
		ActorID:          "operator-1",
		Reason:           "Missing model run trace.",
	})
	if err != nil {
		t.Fatalf("DecideMetadataProposal reject: %v", err)
	}
	if decided.Status != MetadataProposalStatusRejected || decided.ReviewReason != "Missing model run trace." {
		t.Fatalf("rejected proposal = %+v", decided)
	}
}

func TestMemoryMetadataProposalPromotionReceiptStoreRoundTrips(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryMetadataProposalPromotionReceiptStore()
	receipt := MetadataProposalPromotionReceipt{
		ReceiptID:      "receipt-1",
		TenantID:       "tenant-1",
		ProposalID:     "proposal-1",
		ActorID:        "operator-1",
		ActorKind:      "user",
		IdempotencyKey: "apply-1",
		RequestHash:    "request-hash-1",
		Status:         PromotionReceiptStatusApplied,
		Applied:        true,
		Changes: []MetadataProposalPromotionChange{{
			Operation:     PromotionOperationAppend,
			TargetKind:    "object_type",
			TargetID:      "task",
			Field:         "allowed_actions",
			CurrentValue:  "task.complete",
			ProposedValue: "mission.plan",
		}},
	}
	if err := store.SavePromotionReceipt(ctx, receipt); err != nil {
		t.Fatalf("SavePromotionReceipt: %v", err)
	}

	stored, err := store.GetPromotionReceipt(ctx, "receipt-1")
	if err != nil {
		t.Fatalf("GetPromotionReceipt: %v", err)
	}
	if stored.ReceiptID != "receipt-1" ||
		stored.TenantID != "tenant-1" ||
		stored.RequestHash != "request-hash-1" ||
		stored.Status != PromotionReceiptStatusApplied ||
		!stored.Applied ||
		len(stored.Changes) != 1 ||
		stored.AppliedAt.IsZero() {
		t.Fatalf("stored receipt = %+v", stored)
	}

	byKey, err := store.GetPromotionReceiptByIdempotencyKey(ctx, "tenant-1", "proposal-1", "apply-1")
	if err != nil {
		t.Fatalf("GetPromotionReceiptByIdempotencyKey: %v", err)
	}
	if byKey.ReceiptID != "receipt-1" || byKey.RequestHash != "request-hash-1" {
		t.Fatalf("receipt by idempotency = %+v", byKey)
	}
}

func TestMemoryMetadataProposalPromotionReceiptStoreFindsIdempotencyByTenant(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryMetadataProposalPromotionReceiptStore()
	for _, receipt := range []MetadataProposalPromotionReceipt{
		testPromotionReceipt("receipt-tenant-1", "tenant-1", "proposal-1", "operator-1", "apply-1", PromotionReceiptStatusApplied, 10),
		testPromotionReceipt("receipt-tenant-2", "tenant-2", "proposal-1", "operator-2", "apply-1", PromotionReceiptStatusApplied, 20),
	} {
		if err := store.SavePromotionReceipt(ctx, receipt); err != nil {
			t.Fatalf("SavePromotionReceipt(%s): %v", receipt.ReceiptID, err)
		}
	}

	byKey, err := store.GetPromotionReceiptByIdempotencyKey(ctx, "tenant-2", "proposal-1", "apply-1")
	if err != nil {
		t.Fatalf("GetPromotionReceiptByIdempotencyKey: %v", err)
	}
	if byKey.ReceiptID != "receipt-tenant-2" || byKey.TenantID != "tenant-2" {
		t.Fatalf("receipt by tenant idempotency = %+v", byKey)
	}

	if _, err := store.GetPromotionReceiptByIdempotencyKey(ctx, "tenant-3", "proposal-1", "apply-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong tenant idempotency error = %v", err)
	}
}

func TestMemoryMetadataProposalPromotionReceiptStoreListsReceipts(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryMetadataProposalPromotionReceiptStore()
	receipts := []MetadataProposalPromotionReceipt{
		testPromotionReceipt("receipt-1", "tenant-1", "proposal-1", "operator-1", "apply-1", PromotionReceiptStatusApplied, 10),
		testPromotionReceipt("receipt-2", "tenant-1", "proposal-1", "operator-2", "apply-2", PromotionReceiptStatusBlocked, 20),
		testPromotionReceipt("receipt-3", "tenant-2", "proposal-2", "operator-1", "apply-3", PromotionReceiptStatusApplied, 30),
	}
	for _, receipt := range receipts {
		if err := store.SavePromotionReceipt(ctx, receipt); err != nil {
			t.Fatalf("SavePromotionReceipt(%s): %v", receipt.ReceiptID, err)
		}
	}

	page, err := store.ListPromotionReceipts(ctx, PromotionReceiptQuery{
		TenantID:   "tenant-1",
		ProposalID: "proposal-1",
		Limit:      1,
		Offset:     0,
	})
	if err != nil {
		t.Fatalf("ListPromotionReceipts: %v", err)
	}
	if page.Page.Total != 2 || page.Page.Returned != 1 || !page.Page.HasMore || len(page.Receipts) != 1 || page.Receipts[0].ReceiptID != "receipt-2" {
		t.Fatalf("page = %+v", page)
	}

	page, err = store.ListPromotionReceipts(ctx, PromotionReceiptQuery{
		TenantID:       "tenant-1",
		ActorID:        "operator-1",
		Status:         PromotionReceiptStatusApplied,
		IdempotencyKey: "apply-1",
		Limit:          10,
	})
	if err != nil {
		t.Fatalf("ListPromotionReceipts filtered: %v", err)
	}
	if page.Page.Total != 1 || len(page.Receipts) != 1 || page.Receipts[0].ReceiptID != "receipt-1" {
		t.Fatalf("filtered page = %+v", page)
	}
}

func TestPlatformMetadataProposalStoreRoundTrips(t *testing.T) {
	ctx := context.Background()
	sqlStore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "ontology-metadata-proposals"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer sqlStore.Close()
	if err := migration.NewRunner([]migration.Migration{
		ontologyRegistryMigration(),
		ontologyMappingRuleGovernanceMigration(),
		ontologyMetadataProposalMigration(),
		ontologyMetadataProposalReviewMigration(),
		ontologyMetadataProposalSourceTaxonomyMigration(),
		ontologyMetadataProposalRevisionHashMigration(),
	}).Up(ctx, sqlStore); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	store := NewPlatformMetadataProposalStore(sqlStore)
	proposals, err := InferMappingRuleMetadataProposals(MappingRule{
		ID:                 "project.created",
		EventType:          "project.created",
		SourceDomain:       "mission_control",
		SourceRef:          "internal/app/projectadmin",
		EventSchemaVersion: "v1",
		RuleHash:           "hash-project",
	})
	if err != nil {
		t.Fatalf("InferMappingRuleMetadataProposals: %v", err)
	}
	for _, proposal := range proposals {
		if err := store.SaveMetadataProposal(ctx, proposal); err != nil {
			t.Fatalf("SaveMetadataProposal: %v", err)
		}
	}

	page, err := store.ListMetadataProposalPage(ctx, MetadataProposalQuery{
		TargetKind:  "mapping_rule",
		TargetID:    "project.created",
		MetadataKey: "source_domain",
		Status:      string(MetadataProposalStatusProposed),
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("ListMetadataProposalPage: %v", err)
	}
	if page.Page.Total != 1 || page.Page.Returned != 1 || len(page.Proposals) != 1 {
		t.Fatalf("page = %+v", page)
	}
	got := page.Proposals[0]
	if got.ProposedValue != "mission_control" ||
		got.Source != "deterministic.mapping_rule_metadata" ||
		got.SourceKind != MetadataProposalSourceDeterministic ||
		got.SourceRef != "mapping_rule_metadata" ||
		got.SourceVersion != "v1" ||
		len(got.EvidenceRefs) == 0 {
		t.Fatalf("proposal = %+v", got)
	}
}

func TestPlatformMetadataProposalStoreGetsProposalForTenant(t *testing.T) {
	ctx := context.Background()
	sqlStore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "ontology-metadata-proposals-tenant-get"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer sqlStore.Close()
	if err := migration.NewRunner([]migration.Migration{
		ontologyRegistryMigration(),
		ontologyMappingRuleGovernanceMigration(),
		ontologyMetadataProposalMigration(),
		ontologyMetadataProposalReviewMigration(),
		ontologyMetadataProposalSourceTaxonomyMigration(),
		ontologyMetadataProposalRevisionHashMigration(),
	}).Up(ctx, sqlStore); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	store := NewPlatformMetadataProposalStore(sqlStore)
	if err := store.SaveMetadataProposal(ctx, OntologyMetadataProposal{
		ID:            "proposal-tenant-1",
		TenantID:      "tenant-1",
		TargetKind:    "mapping_rule",
		TargetID:      "project.created",
		MetadataKey:   "source_domain",
		ProposedValue: "mission_control",
		Source:        "deterministic.mapping_rule_metadata",
		Status:        MetadataProposalStatusProposed,
	}); err != nil {
		t.Fatalf("SaveMetadataProposal: %v", err)
	}

	got, err := store.GetMetadataProposalForTenant(ctx, "tenant-1", "proposal-tenant-1")
	if err != nil {
		t.Fatalf("GetMetadataProposalForTenant matching tenant: %v", err)
	}
	if got.ID != "proposal-tenant-1" || got.TenantID != "tenant-1" {
		t.Fatalf("matching tenant proposal = %+v", got)
	}
	if _, err := store.GetMetadataProposalForTenant(ctx, "", "proposal-tenant-1"); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("GetMetadataProposalForTenant empty tenant error = %v, want ErrInvalidDefinition", err)
	}
	if _, err := store.GetMetadataProposalForTenant(ctx, "tenant-2", "proposal-tenant-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetMetadataProposalForTenant tenant mismatch error = %v, want ErrNotFound", err)
	}
}

func TestPlatformMetadataProposalPromotionReceiptStoreRoundTrips(t *testing.T) {
	ctx := context.Background()
	sqlStore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "ontology-metadata-promotion-receipts"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer sqlStore.Close()
	if err := migration.NewRunner([]migration.Migration{
		ontologyRegistryMigration(),
		ontologyMappingRuleGovernanceMigration(),
		ontologyMetadataProposalMigration(),
		ontologyMetadataProposalReviewMigration(),
		ontologyMetadataProposalSourceTaxonomyMigration(),
		ontologyMetadataProposalRevisionHashMigration(),
		ontologyActionBindingsMigration(),
		ontologyMetadataProposalPromotionReceiptMigration(),
		ontologyMetadataPromotionReceiptRequestHashMigration(),
		ontologyMetadataPromotionReceiptTenantIdempotencyMigration(),
		ontologyMetadataReceiptProposalBindingMigration(),
	}).Up(ctx, sqlStore); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	store := NewPlatformMetadataProposalPromotionReceiptStore(sqlStore)
	receipt := MetadataProposalPromotionReceipt{
		ReceiptID:      "receipt-1",
		TenantID:       "tenant-1",
		ProposalID:     "proposal-1",
		ActorID:        "operator-1",
		ActorKind:      "user",
		IdempotencyKey: "apply-1",
		RequestHash:    "request-hash-1",
		Status:         PromotionReceiptStatusApplied,
		Applied:        true,
		Changes: []MetadataProposalPromotionChange{{
			Operation:     PromotionOperationUpdate,
			TargetKind:    "mapping_rule",
			TargetID:      "mission.created",
			Field:         "source_domain",
			CurrentValue:  "mission_control",
			ProposedValue: "planning",
		}},
	}
	if err := store.SavePromotionReceipt(ctx, receipt); err != nil {
		t.Fatalf("SavePromotionReceipt: %v", err)
	}

	stored, err := store.GetPromotionReceipt(ctx, "receipt-1")
	if err != nil {
		t.Fatalf("GetPromotionReceipt: %v", err)
	}
	if stored.ReceiptID != "receipt-1" ||
		stored.TenantID != "tenant-1" ||
		stored.RequestHash != "request-hash-1" ||
		stored.Status != PromotionReceiptStatusApplied ||
		!stored.Applied ||
		len(stored.Changes) != 1 ||
		stored.AppliedAt.IsZero() {
		t.Fatalf("stored receipt = %+v", stored)
	}

	byKey, err := store.GetPromotionReceiptByIdempotencyKey(ctx, "tenant-1", "proposal-1", "apply-1")
	if err != nil {
		t.Fatalf("GetPromotionReceiptByIdempotencyKey: %v", err)
	}
	if byKey.ReceiptID != "receipt-1" || byKey.RequestHash != "request-hash-1" {
		t.Fatalf("receipt by idempotency = %+v", byKey)
	}
}

func TestPlatformMetadataProposalPromotionReceiptStoreDoesNotReplayAcrossTenant(t *testing.T) {
	ctx := context.Background()
	sqlStore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "ontology-metadata-promotion-receipts-tenant-replay"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer sqlStore.Close()
	if err := migration.NewRunner([]migration.Migration{
		ontologyRegistryMigration(),
		ontologyMappingRuleGovernanceMigration(),
		ontologyMetadataProposalMigration(),
		ontologyMetadataProposalReviewMigration(),
		ontologyMetadataProposalSourceTaxonomyMigration(),
		ontologyMetadataProposalRevisionHashMigration(),
		ontologyActionBindingsMigration(),
		ontologyMetadataProposalPromotionReceiptMigration(),
		ontologyMetadataPromotionReceiptRequestHashMigration(),
		ontologyMetadataPromotionReceiptTenantIdempotencyMigration(),
		ontologyMetadataReceiptProposalBindingMigration(),
	}).Up(ctx, sqlStore); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	store := NewPlatformMetadataProposalPromotionReceiptStore(sqlStore)
	if err := store.SavePromotionReceipt(ctx, testPromotionReceipt("receipt-1", "tenant-1", "proposal-1", "operator-1", "apply-1", PromotionReceiptStatusApplied, 10)); err != nil {
		t.Fatalf("SavePromotionReceipt: %v", err)
	}

	byKey, err := store.GetPromotionReceiptByIdempotencyKey(ctx, "tenant-1", "proposal-1", "apply-1")
	if err != nil {
		t.Fatalf("GetPromotionReceiptByIdempotencyKey: %v", err)
	}
	if byKey.ReceiptID != "receipt-1" || byKey.TenantID != "tenant-1" {
		t.Fatalf("receipt by tenant idempotency = %+v", byKey)
	}

	if _, err := store.GetPromotionReceiptByIdempotencyKey(ctx, "tenant-2", "proposal-1", "apply-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong tenant idempotency error = %v", err)
	}
}

func TestPlatformMetadataProposalPromotionReceiptStoreAllowsSameIdempotencyAcrossTenants(t *testing.T) {
	ctx := context.Background()
	sqlStore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "ontology-metadata-promotion-receipts-tenant-idempotency"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer sqlStore.Close()
	if err := migration.NewRunner([]migration.Migration{
		ontologyRegistryMigration(),
		ontologyMappingRuleGovernanceMigration(),
		ontologyMetadataProposalMigration(),
		ontologyMetadataProposalReviewMigration(),
		ontologyMetadataProposalSourceTaxonomyMigration(),
		ontologyMetadataProposalRevisionHashMigration(),
		ontologyActionBindingsMigration(),
		ontologyMetadataProposalPromotionReceiptMigration(),
		ontologyMetadataPromotionReceiptRequestHashMigration(),
		ontologyMetadataPromotionReceiptTenantIdempotencyMigration(),
		ontologyMetadataReceiptProposalBindingMigration(),
	}).Up(ctx, sqlStore); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	store := NewPlatformMetadataProposalPromotionReceiptStore(sqlStore)
	tenant1 := testPromotionReceipt("receipt-tenant-1", "tenant-1", "proposal-1", "operator-1", "apply-1", PromotionReceiptStatusApplied, 10)
	tenant1.RequestHash = "request-hash-tenant-1"
	tenant2 := testPromotionReceipt("receipt-tenant-2", "tenant-2", "proposal-1", "operator-2", "apply-1", PromotionReceiptStatusApplied, 20)
	tenant2.RequestHash = "request-hash-tenant-2"
	if err := store.SavePromotionReceipt(ctx, tenant1); err != nil {
		t.Fatalf("SavePromotionReceipt tenant1: %v", err)
	}
	if err := store.SavePromotionReceipt(ctx, tenant2); err != nil {
		t.Fatalf("SavePromotionReceipt tenant2: %v", err)
	}

	byTenant1, err := store.GetPromotionReceiptByIdempotencyKey(ctx, "tenant-1", "proposal-1", "apply-1")
	if err != nil {
		t.Fatalf("GetPromotionReceiptByIdempotencyKey tenant1: %v", err)
	}
	if byTenant1.ReceiptID != "receipt-tenant-1" || byTenant1.RequestHash != "request-hash-tenant-1" {
		t.Fatalf("tenant1 receipt = %+v", byTenant1)
	}
	byTenant2, err := store.GetPromotionReceiptByIdempotencyKey(ctx, "tenant-2", "proposal-1", "apply-1")
	if err != nil {
		t.Fatalf("GetPromotionReceiptByIdempotencyKey tenant2: %v", err)
	}
	if byTenant2.ReceiptID != "receipt-tenant-2" || byTenant2.RequestHash != "request-hash-tenant-2" {
		t.Fatalf("tenant2 receipt = %+v", byTenant2)
	}
}

func TestPlatformMetadataProposalPromotionReceiptStoreRejectsDuplicateIdempotencyWithinTenant(t *testing.T) {
	ctx := context.Background()
	sqlStore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "ontology-metadata-promotion-receipts-tenant-idempotency-duplicate"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer sqlStore.Close()
	if err := migration.NewRunner([]migration.Migration{
		ontologyRegistryMigration(),
		ontologyMappingRuleGovernanceMigration(),
		ontologyMetadataProposalMigration(),
		ontologyMetadataProposalReviewMigration(),
		ontologyMetadataProposalSourceTaxonomyMigration(),
		ontologyMetadataProposalRevisionHashMigration(),
		ontologyActionBindingsMigration(),
		ontologyMetadataProposalPromotionReceiptMigration(),
		ontologyMetadataPromotionReceiptRequestHashMigration(),
		ontologyMetadataPromotionReceiptTenantIdempotencyMigration(),
		ontologyMetadataReceiptProposalBindingMigration(),
	}).Up(ctx, sqlStore); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	store := NewPlatformMetadataProposalPromotionReceiptStore(sqlStore)
	first := testPromotionReceipt("receipt-1", "tenant-1", "proposal-1", "operator-1", "apply-1", PromotionReceiptStatusApplied, 10)
	second := testPromotionReceipt("receipt-2", "tenant-1", "proposal-1", "operator-2", "apply-1", PromotionReceiptStatusApplied, 20)
	if err := store.SavePromotionReceipt(ctx, first); err != nil {
		t.Fatalf("SavePromotionReceipt first: %v", err)
	}
	if err := store.SavePromotionReceipt(ctx, second); err == nil {
		t.Fatal("SavePromotionReceipt duplicate idempotency within tenant succeeded, want error")
	}
}

func TestOntologyMetadataPromotionReceiptMigrationsInstallTenantAwareIdempotencyIndex(t *testing.T) {
	ctx := context.Background()
	sqlStore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "ontology-metadata-promotion-receipts-tenant-index"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer sqlStore.Close()
	if err := migration.NewRunner([]migration.Migration{
		ontologyRegistryMigration(),
		ontologyMappingRuleGovernanceMigration(),
		ontologyMetadataProposalMigration(),
		ontologyMetadataProposalReviewMigration(),
		ontologyMetadataProposalSourceTaxonomyMigration(),
		ontologyMetadataProposalRevisionHashMigration(),
		ontologyActionBindingsMigration(),
		ontologyMetadataProposalPromotionReceiptMigration(),
		ontologyMetadataPromotionReceiptRequestHashMigration(),
		ontologyMetadataPromotionReceiptTenantIdempotencyMigration(),
		ontologyMetadataReceiptProposalBindingMigration(),
	}).Up(ctx, sqlStore); err != nil {
		t.Fatalf("migration up: %v", err)
	}

	indexes := sqliteIndexColumns(t, ctx, sqlStore, "ontology_metadata_promotion_receipts")
	if _, ok := indexes["idx_ontology_metadata_promotion_receipts_idempotency"]; ok {
		t.Fatalf("old idempotency index still exists: %+v", indexes["idx_ontology_metadata_promotion_receipts_idempotency"])
	}
	got := strings.Join(indexes["idx_ontology_metadata_promotion_receipts_tenant_idempotency"], ",")
	if got != "tenant_id,proposal_id,idempotency_key" {
		t.Fatalf("tenant idempotency index columns = %q", got)
	}
}

func TestPlatformMetadataProposalPromotionReceiptStoreListsReceipts(t *testing.T) {
	ctx := context.Background()
	sqlStore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "ontology-metadata-promotion-receipts-list"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer sqlStore.Close()
	if err := migration.NewRunner([]migration.Migration{
		ontologyRegistryMigration(),
		ontologyMappingRuleGovernanceMigration(),
		ontologyMetadataProposalMigration(),
		ontologyMetadataProposalReviewMigration(),
		ontologyMetadataProposalSourceTaxonomyMigration(),
		ontologyMetadataProposalRevisionHashMigration(),
		ontologyActionBindingsMigration(),
		ontologyMetadataProposalPromotionReceiptMigration(),
		ontologyMetadataPromotionReceiptRequestHashMigration(),
		ontologyMetadataPromotionReceiptTenantIdempotencyMigration(),
		ontologyMetadataReceiptProposalBindingMigration(),
	}).Up(ctx, sqlStore); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	store := NewPlatformMetadataProposalPromotionReceiptStore(sqlStore)
	receipts := []MetadataProposalPromotionReceipt{
		testPromotionReceipt("receipt-1", "tenant-1", "proposal-1", "operator-1", "apply-1", PromotionReceiptStatusApplied, 10),
		testPromotionReceipt("receipt-2", "tenant-1", "proposal-1", "operator-2", "apply-2", PromotionReceiptStatusBlocked, 20),
		testPromotionReceipt("receipt-3", "tenant-2", "proposal-2", "operator-1", "apply-3", PromotionReceiptStatusApplied, 30),
	}
	for _, receipt := range receipts {
		if err := store.SavePromotionReceipt(ctx, receipt); err != nil {
			t.Fatalf("SavePromotionReceipt(%s): %v", receipt.ReceiptID, err)
		}
	}

	page, err := store.ListPromotionReceipts(ctx, PromotionReceiptQuery{
		TenantID:   "tenant-1",
		ProposalID: "proposal-1",
		Limit:      2,
	})
	if err != nil {
		t.Fatalf("ListPromotionReceipts: %v", err)
	}
	if page.Page.Total != 2 || page.Page.Returned != 2 || page.Page.HasMore || len(page.Receipts) != 2 || page.Receipts[0].ReceiptID != "receipt-2" || page.Receipts[1].ReceiptID != "receipt-1" {
		t.Fatalf("page = %+v", page)
	}

	page, err = store.ListPromotionReceipts(ctx, PromotionReceiptQuery{
		TenantID:       "tenant-1",
		ActorID:        "operator-2",
		Status:         PromotionReceiptStatusBlocked,
		IdempotencyKey: "apply-2",
		Limit:          10,
	})
	if err != nil {
		t.Fatalf("ListPromotionReceipts filtered: %v", err)
	}
	if page.Page.Total != 1 || len(page.Receipts) != 1 || page.Receipts[0].ReceiptID != "receipt-2" {
		t.Fatalf("filtered page = %+v", page)
	}
}

func TestPlatformMetadataProposalStoreDecidesProposal(t *testing.T) {
	ctx := context.Background()
	sqlStore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "ontology-metadata-proposals-decision"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer sqlStore.Close()
	if err := migration.NewRunner([]migration.Migration{
		ontologyRegistryMigration(),
		ontologyMappingRuleGovernanceMigration(),
		ontologyMetadataProposalMigration(),
		ontologyMetadataProposalReviewMigration(),
		ontologyMetadataProposalSourceTaxonomyMigration(),
		ontologyMetadataProposalRevisionHashMigration(),
	}).Up(ctx, sqlStore); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	store := NewPlatformMetadataProposalStore(sqlStore)
	if err := store.SaveMetadataProposal(ctx, OntologyMetadataProposal{
		ID:            "proposal-1",
		TenantID:      "tenant-1",
		TargetKind:    "mapping_rule",
		TargetID:      "project.created",
		MetadataKey:   "source_domain",
		ProposedValue: "mission_control",
		Source:        "deterministic.mapping_rule_metadata",
	}); err != nil {
		t.Fatalf("SaveMetadataProposal: %v", err)
	}

	decided, err := store.DecideMetadataProposal(ctx, MetadataProposalDecision{
		TenantID:         "tenant-1",
		ProposalID:       "proposal-1",
		ExpectedRevision: 1,
		Status:           MetadataProposalStatusRejected,
		ActorID:          "operator-1",
		ActorKind:        "user",
		Reason:           "Source domain should remain knowledge.",
		EvidenceRefs: []MetadataEvidenceRef{{
			Kind: "review_note",
			ID:   "note-1",
		}},
	})
	if err != nil {
		t.Fatalf("DecideMetadataProposal: %v", err)
	}
	if decided.Status != MetadataProposalStatusRejected ||
		decided.PreviousStatus != MetadataProposalStatusProposed ||
		decided.ReviewedBy != "operator-1" ||
		decided.ReviewReason != "Source domain should remain knowledge." ||
		len(decided.ReviewEvidenceRefs) != 1 ||
		decided.ReviewedAt.IsZero() {
		t.Fatalf("decided proposal = %+v", decided)
	}

	page, err := store.ListMetadataProposalPage(ctx, MetadataProposalQuery{
		Status: string(MetadataProposalStatusRejected),
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("ListMetadataProposalPage: %v", err)
	}
	if page.Page.Total != 1 || page.Proposals[0].ReviewedBy != "operator-1" {
		t.Fatalf("decision round trip page = %+v", page)
	}
}

func TestPlatformMetadataProposalStoreDecidesProposalForTenant(t *testing.T) {
	ctx := context.Background()
	sqlStore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "ontology-metadata-proposals-decision-tenant"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer sqlStore.Close()
	if err := migration.NewRunner([]migration.Migration{
		ontologyRegistryMigration(),
		ontologyMappingRuleGovernanceMigration(),
		ontologyMetadataProposalMigration(),
		ontologyMetadataProposalReviewMigration(),
		ontologyMetadataProposalSourceTaxonomyMigration(),
		ontologyMetadataProposalRevisionHashMigration(),
	}).Up(ctx, sqlStore); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	store := NewPlatformMetadataProposalStore(sqlStore)
	if err := store.SaveMetadataProposal(ctx, OntologyMetadataProposal{
		ID:            "proposal-tenant-1",
		TenantID:      "tenant-1",
		TargetKind:    "mapping_rule",
		TargetID:      "project.created",
		MetadataKey:   "source_domain",
		ProposedValue: "mission_control",
		Source:        "deterministic.mapping_rule_metadata",
	}); err != nil {
		t.Fatalf("SaveMetadataProposal tenant-1: %v", err)
	}
	if err := store.SaveMetadataProposal(ctx, OntologyMetadataProposal{
		ID:            "proposal-tenant-2",
		TenantID:      "tenant-2",
		TargetKind:    "mapping_rule",
		TargetID:      "mission.created",
		MetadataKey:   "source_domain",
		ProposedValue: "mission_control",
		Source:        "deterministic.mapping_rule_metadata",
	}); err != nil {
		t.Fatalf("SaveMetadataProposal tenant-2: %v", err)
	}

	decided, err := store.DecideMetadataProposalForTenant(ctx, "tenant-1", MetadataProposalDecision{
		ProposalID:       "proposal-tenant-1",
		ExpectedRevision: 1,
		Status:           MetadataProposalStatusRejected,
		ActorID:          "operator-1",
		Reason:           "Tenant-scoped review.",
	})
	if err != nil {
		t.Fatalf("DecideMetadataProposalForTenant matching tenant: %v", err)
	}
	if decided.ID != "proposal-tenant-1" || decided.TenantID != "tenant-1" || decided.Status != MetadataProposalStatusRejected {
		t.Fatalf("matching tenant decision = %+v", decided)
	}
	if _, err := store.DecideMetadataProposalForTenant(ctx, "tenant-1", MetadataProposalDecision{
		ProposalID:       "proposal-tenant-2",
		ExpectedRevision: 1,
		Status:           MetadataProposalStatusRejected,
		ActorID:          "operator-1",
		Reason:           "Wrong tenant.",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DecideMetadataProposalForTenant tenant mismatch error = %v, want ErrNotFound", err)
	}
	if _, err := store.DecideMetadataProposalForTenant(ctx, "", MetadataProposalDecision{
		ProposalID:       "proposal-tenant-2",
		ExpectedRevision: 1,
		Status:           MetadataProposalStatusRejected,
		ActorID:          "operator-1",
		Reason:           "Missing tenant.",
	}); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("DecideMetadataProposalForTenant empty tenant error = %v, want ErrInvalidDefinition", err)
	}
	tenant2, err := store.GetMetadataProposalForTenant(ctx, "tenant-2", "proposal-tenant-2")
	if err != nil {
		t.Fatalf("GetMetadataProposalForTenant tenant-2: %v", err)
	}
	if tenant2.Status != MetadataProposalStatusProposed {
		t.Fatalf("tenant mismatch decision modified proposal: %+v", tenant2)
	}
}

func TestPlatformMetadataProposalStoreDecisionDetectsNoRowsUpdated(t *testing.T) {
	ctx := context.Background()
	sqlStore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "ontology-metadata-proposals-decision-no-rows"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer sqlStore.Close()
	if err := migration.NewRunner([]migration.Migration{
		ontologyRegistryMigration(),
		ontologyMappingRuleGovernanceMigration(),
		ontologyMetadataProposalMigration(),
		ontologyMetadataProposalReviewMigration(),
		ontologyMetadataProposalSourceTaxonomyMigration(),
		ontologyMetadataProposalRevisionHashMigration(),
	}).Up(ctx, sqlStore); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	store := NewPlatformMetadataProposalStore(sqlStore)
	if err := store.SaveMetadataProposal(ctx, OntologyMetadataProposal{
		ID:            "proposal-no-update",
		TenantID:      "tenant-1",
		TargetKind:    "mapping_rule",
		TargetID:      "project.created",
		MetadataKey:   "source_domain",
		ProposedValue: "mission_control",
		Source:        "deterministic.mapping_rule_metadata",
	}); err != nil {
		t.Fatalf("SaveMetadataProposal: %v", err)
	}
	if _, err := sqlStore.Exec(ctx, `
CREATE TRIGGER ontology_metadata_proposals_ignore_decision_update
BEFORE UPDATE ON ontology_metadata_proposals
FOR EACH ROW
WHEN OLD.id = 'proposal-no-update'
BEGIN
	SELECT RAISE(IGNORE);
END`); err != nil {
		t.Fatalf("create ignore update trigger: %v", err)
	}

	if _, err := store.DecideMetadataProposalForTenant(ctx, "tenant-1", MetadataProposalDecision{
		ProposalID:       "proposal-no-update",
		ExpectedRevision: 1,
		Status:           MetadataProposalStatusRejected,
		ActorID:          "operator-1",
		Reason:           "Decision write must affect one row.",
	}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("DecideMetadataProposalForTenant ignored update error = %v, want ErrVersionConflict", err)
	}
	stored, err := store.GetMetadataProposalForTenant(ctx, "tenant-1", "proposal-no-update")
	if err != nil {
		t.Fatalf("GetMetadataProposalForTenant: %v", err)
	}
	if stored.Status != MetadataProposalStatusProposed || stored.ReviewedBy != "" {
		t.Fatalf("ignored decision should leave proposal unchanged: %+v", stored)
	}
}

func TestPlatformMetadataProposalStoreDoesNotResetDecidedProposal(t *testing.T) {
	ctx := context.Background()
	sqlStore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "ontology-metadata-proposals-decision-upsert"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer sqlStore.Close()
	if err := migration.NewRunner([]migration.Migration{
		ontologyRegistryMigration(),
		ontologyMappingRuleGovernanceMigration(),
		ontologyMetadataProposalMigration(),
		ontologyMetadataProposalReviewMigration(),
		ontologyMetadataProposalSourceTaxonomyMigration(),
		ontologyMetadataProposalRevisionHashMigration(),
	}).Up(ctx, sqlStore); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	store := NewPlatformMetadataProposalStore(sqlStore)
	proposal := OntologyMetadataProposal{
		ID:            "proposal-1",
		TenantID:      "tenant-1",
		TargetKind:    "mapping_rule",
		TargetID:      "project.created",
		MetadataKey:   "source_domain",
		ProposedValue: "mission_control",
		Source:        "deterministic.mapping_rule_metadata",
	}
	if err := store.SaveMetadataProposal(ctx, proposal); err != nil {
		t.Fatalf("SaveMetadataProposal: %v", err)
	}
	if _, err := store.DecideMetadataProposal(ctx, MetadataProposalDecision{
		TenantID:         "tenant-1",
		ProposalID:       "proposal-1",
		ExpectedRevision: 1,
		Status:           MetadataProposalStatusAccepted,
		ActorID:          "operator-1",
		Reason:           "Accepted by governance review.",
	}); err != nil {
		t.Fatalf("DecideMetadataProposal: %v", err)
	}
	if err := store.SaveMetadataProposal(ctx, proposal); err != nil {
		t.Fatalf("SaveMetadataProposal second write: %v", err)
	}

	page, err := store.ListMetadataProposalPage(ctx, MetadataProposalQuery{Status: string(MetadataProposalStatusAccepted), Limit: 10})
	if err != nil {
		t.Fatalf("ListMetadataProposalPage: %v", err)
	}
	if page.Page.Total != 1 || page.Proposals[0].Status != MetadataProposalStatusAccepted || page.Proposals[0].ReviewedBy != "operator-1" {
		t.Fatalf("decided proposal was reset by save: %+v", page)
	}
}

func testPromotionReceipt(receiptID, tenantID, proposalID, actorID, idempotencyKey string, status PromotionReceiptStatus, unixSeconds int64) MetadataProposalPromotionReceipt {
	applied := status == PromotionReceiptStatusApplied
	return MetadataProposalPromotionReceipt{
		ReceiptID:      receiptID,
		TenantID:       tenantID,
		ProposalID:     proposalID,
		ActorID:        actorID,
		ActorKind:      "user",
		IdempotencyKey: idempotencyKey,
		Status:         status,
		Applied:        applied,
		Changes: []MetadataProposalPromotionChange{{
			Operation:     PromotionOperationAppend,
			TargetKind:    "object_type",
			TargetID:      "task",
			Field:         "allowed_actions",
			CurrentValue:  "task.complete",
			ProposedValue: "mission.plan",
		}},
		AppliedAt: time.Unix(unixSeconds, 0).UTC(),
		CreatedAt: time.Unix(unixSeconds, 0).UTC(),
		UpdatedAt: time.Unix(unixSeconds, 0).UTC(),
	}
}

func sqliteIndexColumns(t *testing.T, ctx context.Context, store *db.Store, table string) map[string][]string {
	t.Helper()
	rows, err := store.Query(ctx, `PRAGMA index_list(`+table+`)`)
	if err != nil {
		t.Fatalf("PRAGMA index_list: %v", err)
	}
	defer rows.Close()
	indexNames := []string{}
	for rows.Next() {
		var seq int
		var name string
		var unique int
		var origin string
		var partial int
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			t.Fatalf("scan index_list: %v", err)
		}
		indexNames = append(indexNames, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("index_list rows: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close index_list rows: %v", err)
	}
	indexes := map[string][]string{}
	for _, name := range indexNames {
		indexes[name] = sqliteIndexInfo(t, ctx, store, name)
	}
	return indexes
}

func sqliteIndexInfo(t *testing.T, ctx context.Context, store *db.Store, indexName string) []string {
	t.Helper()
	rows, err := store.Query(ctx, `PRAGMA index_info(`+indexName+`)`)
	if err != nil {
		t.Fatalf("PRAGMA index_info(%s): %v", indexName, err)
	}
	defer rows.Close()
	columns := []string{}
	for rows.Next() {
		var seqno int
		var cid int
		var name string
		if err := rows.Scan(&seqno, &cid, &name); err != nil {
			t.Fatalf("scan index_info(%s): %v", indexName, err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("index_info(%s) rows: %v", indexName, err)
	}
	return columns
}
