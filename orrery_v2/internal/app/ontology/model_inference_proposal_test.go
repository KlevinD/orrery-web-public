package ontology

import (
	"context"
	"errors"
	"testing"
)

func TestInferModelInferenceMetadataProposalsBuildsTraceableProposals(t *testing.T) {
	result, err := InferModelInferenceMetadataProposals(ModelInferenceMetadataProposalRequest{
		TenantID:      "tenant-1",
		Engine:        "ontology_designer",
		Model:         "gpt-5",
		ModelVersion:  "2026-06-21",
		RunID:         "run-1",
		InputHash:     "sha256:input",
		PromptHash:    "sha256:prompt",
		SourceRef:     "ontology-designer-v2",
		SourceVersion: "2026-06-21",
		Candidates: []ModelInferenceMetadataCandidate{{
			TargetKind:    "object_type",
			TargetID:      "customer",
			MetadataKey:   "owner_domain",
			ProposedValue: "crm",
			Confidence:    0.91,
			Reason:        "Customer records are owned by the CRM domain.",
			EvidenceRefs: []MetadataEvidenceRef{{
				Kind: "model_run",
				ID:   "run-1",
				Metadata: map[string]string{
					"model": "stale-trace",
				},
			}, {
				Kind: "knowledge_document",
				ID:   "doc-1",
				Metadata: map[string]string{
					"section": "ownership",
				},
			}},
		}, {
			TargetKind:    "mapping_rule",
			TargetID:      "customer.created",
			MetadataKey:   "source_domain",
			ProposedValue: "crm",
			Confidence:    0.87,
			EvidenceRefs:  []MetadataEvidenceRef{{Kind: "context_graph", ID: "customer.created"}},
		}},
	})
	if err != nil {
		t.Fatalf("InferModelInferenceMetadataProposals: %v", err)
	}
	if result.ValidCandidateCount != 2 || result.SkippedInvalidCount != 0 || len(result.ValidationErrors) != 0 {
		t.Fatalf("result counters = %+v", result)
	}
	if len(result.Proposals) != 2 {
		t.Fatalf("proposal count = %d, want 2: %+v", len(result.Proposals), result.Proposals)
	}

	for _, proposal := range result.Proposals {
		if proposal.ID == "" || proposal.TenantID != "tenant-1" {
			t.Fatalf("proposal identity = %+v", proposal)
		}
		if proposal.Source != "model_inference.ontology_designer" ||
			proposal.SourceKind != MetadataProposalSourceModelInference ||
			proposal.SourceRef != "ontology-designer-v2" ||
			proposal.SourceVersion != "2026-06-21" ||
			proposal.Status != MetadataProposalStatusProposed {
			t.Fatalf("proposal source taxonomy = %+v", proposal)
		}
		if proposal.Confidence <= 0 || proposal.Confidence > 1 {
			t.Fatalf("proposal confidence = %+v", proposal)
		}
		if !metadataEvidenceContains(proposal.EvidenceRefs, "model_run", "run-1") {
			t.Fatalf("proposal missing model_run evidence = %+v", proposal.EvidenceRefs)
		}
		modelRun := metadataEvidenceByKind(proposal.EvidenceRefs, "model_run")
		if modelRun.Metadata["model"] != "gpt-5" ||
			modelRun.Metadata["version"] != "2026-06-21" ||
			modelRun.Metadata["input_hash"] != "sha256:input" ||
			modelRun.Metadata["prompt_hash"] != "sha256:prompt" ||
			modelRun.Metadata["engine"] != "ontology_designer" {
			t.Fatalf("model_run metadata = %+v", modelRun)
		}

		report := AnalyzeMetadataProposalConflicts([]OntologyMetadataProposal{proposal})
		if report.BlockingCount != 0 {
			t.Fatalf("traceable proposal should pass model inference analysis: %+v", report)
		}
	}
}

func TestInferModelInferenceMetadataProposalsRequiresTrace(t *testing.T) {
	_, err := InferModelInferenceMetadataProposals(ModelInferenceMetadataProposalRequest{
		Engine:       "ontology_designer",
		Model:        "gpt-5",
		ModelVersion: "2026-06-21",
		Candidates: []ModelInferenceMetadataCandidate{{
			TargetKind:    "object_type",
			TargetID:      "customer",
			MetadataKey:   "owner_domain",
			ProposedValue: "crm",
			Confidence:    0.91,
			EvidenceRefs:  []MetadataEvidenceRef{{Kind: "knowledge_document", ID: "doc-1"}},
		}},
	})
	if !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("InferModelInferenceMetadataProposals error = %v, want ErrInvalidDefinition", err)
	}
}

func TestIngestModelInferenceMetadataProposalsPersistsAndAnalyzes(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryMetadataProposalStore()

	result, err := IngestModelInferenceMetadataProposals(ctx, store, ModelInferenceMetadataProposalRequest{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		Model:        "gpt-5",
		ModelVersion: "2026-06-21",
		RunID:        "run-1",
		InputHash:    "sha256:input",
		PromptHash:   "sha256:prompt",
		Candidates: []ModelInferenceMetadataCandidate{{
			TargetKind:    "object_type",
			TargetID:      "customer",
			MetadataKey:   "owner_domain",
			ProposedValue: "crm",
			Confidence:    0.91,
			EvidenceRefs:  []MetadataEvidenceRef{{Kind: "knowledge_document", ID: "doc-1"}},
		}, {
			TargetKind:    "object_type",
			TargetID:      "",
			MetadataKey:   "owner_domain",
			ProposedValue: "crm",
			Confidence:    0.91,
		}},
	})
	if err != nil {
		t.Fatalf("IngestModelInferenceMetadataProposals: %v", err)
	}
	if len(result.Proposals) != 1 || result.Analysis.Total != 1 || result.Analysis.BlockingCount != 0 {
		t.Fatalf("ingest result = %+v", result)
	}
	if result.Generation.ValidCandidateCount != 1 ||
		result.Generation.SkippedInvalidCount != 1 ||
		len(result.Generation.ValidationErrors) != 1 {
		t.Fatalf("generation result = %+v", result.Generation)
	}

	page, err := store.ListMetadataProposalPage(ctx, MetadataProposalQuery{
		TenantID:   "tenant-1",
		SourceKind: MetadataProposalSourceModelInference,
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("ListMetadataProposalPage: %v", err)
	}
	if page.Page.Total != 1 || page.Proposals[0].TargetID != "customer" {
		t.Fatalf("stored proposals = %+v", page)
	}
}

func TestIngestModelInferenceMetadataProposalsKeepsRunScopedProposalEvidence(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryMetadataProposalStore()
	ledger := NewMemoryModelRunLedger()
	for _, runID := range []string{"run-repeat-1", "run-repeat-2"} {
		if _, err := IngestModelInferenceMetadataProposalsWithLedger(ctx, store, ledger, ModelInferenceMetadataProposalRequest{
			TenantID:     "tenant-1",
			Engine:       "ontology_designer",
			Model:        "gpt-5",
			ModelVersion: "2026-06-21",
			RunID:        runID,
			InputHash:    "sha256:" + runID + ":input",
			PromptHash:   "sha256:" + runID + ":prompt",
			Candidates: []ModelInferenceMetadataCandidate{{
				TargetKind:    "object_type",
				TargetID:      "customer",
				MetadataKey:   "owner_domain",
				ProposedValue: "crm",
				Confidence:    0.91,
				EvidenceRefs:  []MetadataEvidenceRef{{Kind: "knowledge_document", ID: "doc-1"}},
			}},
		}); err != nil {
			t.Fatalf("IngestModelInferenceMetadataProposalsWithLedger(%s): %v", runID, err)
		}
	}

	first, err := ListModelRunProposals(ctx, ledger, store, ModelRunProposalQuery{
		TenantID: "tenant-1",
		Engine:   "ontology_designer",
		RunID:    "run-repeat-1",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunProposals run-repeat-1: %v", err)
	}
	second, err := ListModelRunProposals(ctx, ledger, store, ModelRunProposalQuery{
		TenantID: "tenant-1",
		Engine:   "ontology_designer",
		RunID:    "run-repeat-2",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunProposals run-repeat-2: %v", err)
	}
	if first.Page.Total != 1 || len(first.Proposals) != 1 || !metadataEvidenceContains(first.Proposals[0].EvidenceRefs, "model_run", "run-repeat-1") {
		t.Fatalf("first run proposals = %+v", first)
	}
	if second.Page.Total != 1 || len(second.Proposals) != 1 || !metadataEvidenceContains(second.Proposals[0].EvidenceRefs, "model_run", "run-repeat-2") {
		t.Fatalf("second run proposals = %+v", second)
	}
	if first.Proposals[0].ID == second.Proposals[0].ID {
		t.Fatalf("model inference proposals must be run-scoped: first=%s second=%s", first.Proposals[0].ID, second.Proposals[0].ID)
	}
}

func TestIngestModelInferenceMetadataProposalsRecordsModelRunLedger(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryMetadataProposalStore()
	ledger := NewMemoryModelRunLedger()

	result, err := IngestModelInferenceMetadataProposalsWithLedger(ctx, store, ledger, ModelInferenceMetadataProposalRequest{
		TenantID:      "tenant-1",
		Engine:        "ontology_designer",
		Model:         "gpt-5",
		ModelVersion:  "2026-06-21",
		RunID:         "run-ledger-1",
		InputHash:     "sha256:input",
		PromptHash:    "sha256:prompt",
		SourceRef:     "ontology-designer-v2",
		SourceVersion: "2026-06-21",
		Candidates: []ModelInferenceMetadataCandidate{{
			TargetKind:    "object_type",
			TargetID:      "customer",
			MetadataKey:   "owner_domain",
			ProposedValue: "crm",
			Confidence:    0.91,
			EvidenceRefs:  []MetadataEvidenceRef{{Kind: "knowledge_document", ID: "doc-1"}},
		}, {
			TargetKind:    "object_type",
			TargetID:      "",
			MetadataKey:   "owner_domain",
			ProposedValue: "crm",
			Confidence:    0.91,
		}},
	})
	if err != nil {
		t.Fatalf("IngestModelInferenceMetadataProposalsWithLedger: %v", err)
	}
	if len(result.Proposals) != 1 || result.Generation.ValidCandidateCount != 1 || result.Generation.SkippedInvalidCount != 1 {
		t.Fatalf("ingest result = %+v", result)
	}

	runs, err := ledger.ListModelRuns(ctx, ModelRunQuery{TenantID: "tenant-1", Engine: "ontology_designer", RunID: "run-ledger-1", Limit: 10})
	if err != nil {
		t.Fatalf("ListModelRuns: %v", err)
	}
	if runs.Page.Total != 1 || len(runs.Runs) != 1 {
		t.Fatalf("model run page = %+v", runs)
	}
	run := runs.Runs[0]
	if run.Status != ModelRunStatusSucceeded ||
		run.CandidateCount != 2 ||
		run.ValidCandidateCount != 1 ||
		run.SkippedCandidateCount != 1 ||
		run.ProposalCount != 1 ||
		run.AnalysisTotal != 1 ||
		run.AnalysisBlockingCount != 0 ||
		run.Engine != "ontology_designer" ||
		run.Model != "gpt-5" ||
		run.SourceRef != "ontology-designer-v2" {
		t.Fatalf("model run = %+v", run)
	}
	if run.RawPrompt != "" || run.RawInput != "" || run.RawOutput != "" {
		t.Fatalf("model run leaked raw payload fields: %+v", run)
	}
}

func metadataEvidenceByKind(refs []MetadataEvidenceRef, kind string) MetadataEvidenceRef {
	for _, ref := range refs {
		if ref.Kind == kind {
			return ref
		}
	}
	return MetadataEvidenceRef{}
}
