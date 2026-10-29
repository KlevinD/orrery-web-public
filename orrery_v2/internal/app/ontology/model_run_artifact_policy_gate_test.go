package ontology

import (
	"context"
	"testing"
)

func TestListModelRunProposalsWithArtifactPolicyBlocksMissingRequiredArtifact(t *testing.T) {
	ctx := context.Background()
	ledger, proposals, artifacts := seededArtifactPolicyStores(t, ctx)

	page, err := ListModelRunProposalsWithArtifacts(ctx, ledger, proposals, artifacts, ModelRunProposalQuery{
		TenantID:             "tenant-1",
		Engine:               "ontology_designer",
		RunID:                "run-artifact-policy",
		RequiredArtifactKind: "evaluation_report",
		Limit:                10,
	})
	if err != nil {
		t.Fatalf("ListModelRunProposalsWithArtifacts: %v", err)
	}
	if page.EvaluationGate.Status != ModelRunEvaluationGateBlocked ||
		!modelRunGateHasReason(page.EvaluationGate, ModelRunEvaluationReasonMissingRequiredArtifact) {
		t.Fatalf("gate = %+v", page.EvaluationGate)
	}
}

func TestListModelRunProposalsWithArtifactPolicyPassesMatchingArtifact(t *testing.T) {
	ctx := context.Background()
	ledger, proposals, artifacts := seededArtifactPolicyStores(t, ctx)
	if err := artifacts.SaveModelRunEvaluationArtifact(ctx, ModelRunEvaluationArtifact{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RunID:        "run-artifact-policy",
		ArtifactKind: "evaluation_report",
		ArtifactURI:  "s3://orrery-eval/run-artifact-policy/report.json",
		ArtifactHash: "sha256:report",
		Metadata: map[string]string{
			"dataset_ref":    "ontology-eval-20260621",
			"policy_version": "v1",
		},
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationArtifact: %v", err)
	}

	page, err := ListModelRunProposalsWithArtifacts(ctx, ledger, proposals, artifacts, ModelRunProposalQuery{
		TenantID:              "tenant-1",
		Engine:                "ontology_designer",
		RunID:                 "run-artifact-policy",
		RequiredArtifactKind:  "evaluation_report",
		RequiredDatasetRef:    "ontology-eval-20260621",
		RequiredPolicyVersion: "v1",
		Limit:                 10,
	})
	if err != nil {
		t.Fatalf("ListModelRunProposalsWithArtifacts: %v", err)
	}
	if page.EvaluationGate.Status != ModelRunEvaluationGatePassed || len(page.EvaluationGate.Reasons) != 0 {
		t.Fatalf("gate = %+v", page.EvaluationGate)
	}
}

func TestListModelRunProposalsWithArtifactPolicyBlocksDatasetAndPolicyMismatch(t *testing.T) {
	ctx := context.Background()
	ledger, proposals, artifacts := seededArtifactPolicyStores(t, ctx)
	if err := artifacts.SaveModelRunEvaluationArtifact(ctx, ModelRunEvaluationArtifact{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RunID:        "run-artifact-policy",
		ArtifactKind: "evaluation_report",
		ArtifactURI:  "s3://orrery-eval/run-artifact-policy/report.json",
		ArtifactHash: "sha256:report",
		Metadata: map[string]string{
			"dataset_ref":    "older-eval",
			"policy_version": "v0",
		},
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationArtifact: %v", err)
	}

	page, err := ListModelRunProposalsWithArtifacts(ctx, ledger, proposals, artifacts, ModelRunProposalQuery{
		TenantID:              "tenant-1",
		Engine:                "ontology_designer",
		RunID:                 "run-artifact-policy",
		RequiredArtifactKind:  "evaluation_report",
		RequiredDatasetRef:    "ontology-eval-20260621",
		RequiredPolicyVersion: "v1",
		Limit:                 10,
	})
	if err != nil {
		t.Fatalf("ListModelRunProposalsWithArtifacts: %v", err)
	}
	if page.EvaluationGate.Status != ModelRunEvaluationGateBlocked ||
		!modelRunGateHasReason(page.EvaluationGate, ModelRunEvaluationReasonDatasetRefMismatch) ||
		!modelRunGateHasReason(page.EvaluationGate, ModelRunEvaluationReasonPolicyVersionMismatch) {
		t.Fatalf("gate = %+v", page.EvaluationGate)
	}
}

func TestListModelRunProposalsWithArtifactPolicyDoesNotMixArtifacts(t *testing.T) {
	ctx := context.Background()
	ledger, proposals, artifacts := seededArtifactPolicyStores(t, ctx)
	for _, artifact := range []ModelRunEvaluationArtifact{
		{
			ID:           "evaluation-dataset-match",
			TenantID:     "tenant-1",
			Engine:       "ontology_designer",
			RunID:        "run-artifact-policy",
			ArtifactKind: "evaluation_report",
			ArtifactURI:  "s3://orrery-eval/run-artifact-policy/dataset-match.json",
			Metadata: map[string]string{
				"dataset_ref":    "ontology-eval-20260621",
				"policy_version": "old-policy",
				"accuracy":       "0.99",
			},
		},
		{
			ID:           "evaluation-policy-match",
			TenantID:     "tenant-1",
			Engine:       "ontology_designer",
			RunID:        "run-artifact-policy",
			ArtifactKind: "evaluation_report",
			ArtifactURI:  "s3://orrery-eval/run-artifact-policy/policy-match.json",
			Metadata: map[string]string{
				"dataset_ref":    "old-dataset",
				"policy_version": "v1",
				"accuracy":       "0.99",
			},
		},
	} {
		if err := artifacts.SaveModelRunEvaluationArtifact(ctx, artifact); err != nil {
			t.Fatalf("SaveModelRunEvaluationArtifact(%s): %v", artifact.ID, err)
		}
	}

	page, err := ListModelRunProposalsWithArtifacts(ctx, ledger, proposals, artifacts, ModelRunProposalQuery{
		TenantID:              "tenant-1",
		Engine:                "ontology_designer",
		RunID:                 "run-artifact-policy",
		RequiredArtifactKind:  "evaluation_report",
		RequiredDatasetRef:    "ontology-eval-20260621",
		RequiredPolicyVersion: "v1",
		MetricThresholds: []ModelRunEvaluationMetricThreshold{{
			RuleID:    "accuracy-minimum",
			MetricKey: "accuracy",
			Operator:  ModelRunEvaluationMetricOperatorGTE,
			Threshold: 0.95,
			Blocking:  true,
		}},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListModelRunProposalsWithArtifacts: %v", err)
	}
	if page.EvaluationGate.Status != ModelRunEvaluationGateBlocked ||
		!modelRunGateHasReason(page.EvaluationGate, ModelRunEvaluationReasonMissingMetric) {
		t.Fatalf("gate = %+v, want blocked without mixing artifacts", page.EvaluationGate)
	}
}

func seededArtifactPolicyStores(t *testing.T, ctx context.Context) (*MemoryModelRunLedger, *MemoryMetadataProposalStore, *MemoryModelRunEvaluationArtifactStore) {
	t.Helper()
	ledger := NewMemoryModelRunLedger()
	proposals := NewMemoryMetadataProposalStore()
	artifacts := NewMemoryModelRunEvaluationArtifactStore()
	if err := ledger.SaveModelRun(ctx, OntologyModelRun{
		TenantID:      "tenant-1",
		Engine:        "ontology_designer",
		Model:         "gpt-5",
		ModelVersion:  "2026-06-21",
		RunID:         "run-artifact-policy",
		InputHash:     "sha256:input",
		PromptHash:    "sha256:prompt",
		Status:        ModelRunStatusSucceeded,
		ProposalCount: 1,
	}); err != nil {
		t.Fatalf("SaveModelRun: %v", err)
	}
	if err := proposals.SaveMetadataProposal(ctx, OntologyMetadataProposal{
		TenantID:      "tenant-1",
		TargetKind:    "object_type",
		TargetID:      "customer",
		MetadataKey:   "owner_domain",
		ProposedValue: "crm",
		Source:        "model_inference.ontology_designer",
		SourceKind:    MetadataProposalSourceModelInference,
		Confidence:    0.91,
		Status:        MetadataProposalStatusProposed,
		EvidenceRefs: []MetadataEvidenceRef{{
			Kind: "model_run",
			ID:   "run-artifact-policy",
			Metadata: map[string]string{
				"engine":      "ontology_designer",
				"model":       "gpt-5",
				"version":     "2026-06-21",
				"input_hash":  "sha256:input",
				"prompt_hash": "sha256:prompt",
			},
		}},
	}); err != nil {
		t.Fatalf("SaveMetadataProposal: %v", err)
	}
	return ledger, proposals, artifacts
}

func modelRunGateHasReason(gate ModelRunEvaluationGate, reason ModelRunEvaluationReasonCode) bool {
	for _, got := range gate.Reasons {
		if got == reason {
			return true
		}
	}
	return false
}
