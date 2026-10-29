package ontology

import (
	"context"
	"testing"
)

func TestListModelRunProposalsWithEvaluationPolicyProfilesAppliesPersistedProfile(t *testing.T) {
	ctx := context.Background()
	ledger, proposals, artifacts := seededArtifactPolicyStores(t, ctx)
	policies := NewMemoryModelRunEvaluationPolicyProfileStore()
	if err := policies.SaveModelRunEvaluationPolicyProfile(ctx, ModelRunEvaluationPolicyProfile{
		TenantID:              "tenant-1",
		Engine:                "ontology_designer",
		Model:                 "gpt-5",
		ModelVersion:          "2026-06-21",
		RequiredArtifactKind:  "evaluation_report",
		RequiredDatasetRef:    "ontology-eval-20260621",
		RequiredPolicyVersion: "v1",
		Enabled:               true,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationPolicyProfile: %v", err)
	}

	blocked, err := ListModelRunProposalsWithEvaluationPolicyProfiles(ctx, ledger, proposals, artifacts, policies, ModelRunProposalQuery{
		TenantID: "tenant-1",
		Engine:   "ontology_designer",
		RunID:    "run-artifact-policy",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunProposalsWithEvaluationPolicyProfiles blocked: %v", err)
	}
	if blocked.EvaluationGate.Status != ModelRunEvaluationGateBlocked ||
		!modelRunGateHasReason(blocked.EvaluationGate, ModelRunEvaluationReasonMissingRequiredArtifact) {
		t.Fatalf("blocked gate = %+v", blocked.EvaluationGate)
	}

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
	passed, err := ListModelRunProposalsWithEvaluationPolicyProfiles(ctx, ledger, proposals, artifacts, policies, ModelRunProposalQuery{
		TenantID: "tenant-1",
		Engine:   "ontology_designer",
		RunID:    "run-artifact-policy",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunProposalsWithEvaluationPolicyProfiles passed: %v", err)
	}
	if passed.EvaluationGate.Status != ModelRunEvaluationGatePassed || len(passed.EvaluationGate.Reasons) != 0 {
		t.Fatalf("passed gate = %+v", passed.EvaluationGate)
	}
}

func TestListModelRunProposalsWithEvaluationPolicyProfilesKeepsExplicitQueryPolicy(t *testing.T) {
	ctx := context.Background()
	ledger, proposals, artifacts := seededArtifactPolicyStores(t, ctx)
	policies := NewMemoryModelRunEvaluationPolicyProfileStore()
	if err := policies.SaveModelRunEvaluationPolicyProfile(ctx, ModelRunEvaluationPolicyProfile{
		TenantID:              "tenant-1",
		Engine:                "ontology_designer",
		RequiredArtifactKind:  "evaluation_report",
		RequiredDatasetRef:    "profile-dataset",
		RequiredPolicyVersion: "profile-v1",
		Enabled:               true,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationPolicyProfile: %v", err)
	}
	if err := artifacts.SaveModelRunEvaluationArtifact(ctx, ModelRunEvaluationArtifact{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RunID:        "run-artifact-policy",
		ArtifactKind: "evaluation_report",
		ArtifactURI:  "s3://orrery-eval/run-artifact-policy/report.json",
		ArtifactHash: "sha256:report",
		Metadata: map[string]string{
			"dataset_ref":    "query-dataset",
			"policy_version": "query-v1",
		},
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationArtifact: %v", err)
	}

	page, err := ListModelRunProposalsWithEvaluationPolicyProfiles(ctx, ledger, proposals, artifacts, policies, ModelRunProposalQuery{
		TenantID:              "tenant-1",
		Engine:                "ontology_designer",
		RunID:                 "run-artifact-policy",
		RequiredArtifactKind:  "evaluation_report",
		RequiredDatasetRef:    "query-dataset",
		RequiredPolicyVersion: "query-v1",
		Limit:                 10,
	})
	if err != nil {
		t.Fatalf("ListModelRunProposalsWithEvaluationPolicyProfiles: %v", err)
	}
	if page.EvaluationGate.Status != ModelRunEvaluationGatePassed {
		t.Fatalf("gate = %+v", page.EvaluationGate)
	}
}

func TestListModelRunProposalsWithEvaluationPolicyProfilesBlocksFailedMetricThreshold(t *testing.T) {
	ctx := context.Background()
	ledger, proposals, artifacts := seededArtifactPolicyStores(t, ctx)
	policies := NewMemoryModelRunEvaluationPolicyProfileStore()
	if err := policies.SaveModelRunEvaluationPolicyProfile(ctx, ModelRunEvaluationPolicyProfile{
		TenantID:              "tenant-1",
		Engine:                "ontology_designer",
		Model:                 "gpt-5",
		ModelVersion:          "2026-06-21",
		RequiredArtifactKind:  "evaluation_report",
		RequiredDatasetRef:    "ontology-eval-20260621",
		RequiredPolicyVersion: "v1",
		MetricThresholds: []ModelRunEvaluationMetricThreshold{{
			MetricKey: "accuracy",
			Operator:  ModelRunEvaluationMetricOperatorGTE,
			Threshold: 0.95,
			Blocking:  true,
		}},
		Enabled: true,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationPolicyProfile: %v", err)
	}
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
			"accuracy":       "0.91",
		},
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationArtifact low metric: %v", err)
	}

	blocked, err := ListModelRunProposalsWithEvaluationPolicyProfiles(ctx, ledger, proposals, artifacts, policies, ModelRunProposalQuery{
		TenantID: "tenant-1",
		Engine:   "ontology_designer",
		RunID:    "run-artifact-policy",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunProposalsWithEvaluationPolicyProfiles blocked: %v", err)
	}
	if blocked.EvaluationGate.Status != ModelRunEvaluationGateBlocked ||
		!modelRunGateHasReason(blocked.EvaluationGate, ModelRunEvaluationReasonMetricThresholdFailed) {
		t.Fatalf("blocked gate = %+v", blocked.EvaluationGate)
	}
}

func TestListModelRunProposalsWithEvaluationPolicyProfilesPassesMetricThreshold(t *testing.T) {
	ctx := context.Background()
	ledger, proposals, artifacts := seededArtifactPolicyStores(t, ctx)
	policies := NewMemoryModelRunEvaluationPolicyProfileStore()
	if err := policies.SaveModelRunEvaluationPolicyProfile(ctx, ModelRunEvaluationPolicyProfile{
		TenantID:              "tenant-1",
		Engine:                "ontology_designer",
		Model:                 "gpt-5",
		ModelVersion:          "2026-06-21",
		RequiredArtifactKind:  "evaluation_report",
		RequiredDatasetRef:    "ontology-eval-20260621",
		RequiredPolicyVersion: "v1",
		MetricThresholds: []ModelRunEvaluationMetricThreshold{{
			MetricKey: "accuracy",
			Operator:  ModelRunEvaluationMetricOperatorGTE,
			Threshold: 0.95,
			Blocking:  true,
		}},
		Enabled: true,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationPolicyProfile: %v", err)
	}
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
			"accuracy":       "0.97",
		},
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationArtifact passing metric: %v", err)
	}

	passed, err := ListModelRunProposalsWithEvaluationPolicyProfiles(ctx, ledger, proposals, artifacts, policies, ModelRunProposalQuery{
		TenantID: "tenant-1",
		Engine:   "ontology_designer",
		RunID:    "run-artifact-policy",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunProposalsWithEvaluationPolicyProfiles passed: %v", err)
	}
	if passed.EvaluationGate.Status != ModelRunEvaluationGatePassed || len(passed.EvaluationGate.Reasons) != 0 {
		t.Fatalf("passed gate = %+v", passed.EvaluationGate)
	}
}
