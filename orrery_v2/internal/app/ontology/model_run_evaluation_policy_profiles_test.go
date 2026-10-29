package ontology

import (
	"context"
	"testing"
)

func TestMemoryModelRunEvaluationPolicyProfileStoreSaveListAndResolve(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationPolicyProfileStore()
	exact := ModelRunEvaluationPolicyProfile{
		TenantID:              "tenant-1",
		Engine:                "ontology_designer",
		Model:                 "gpt-5",
		ModelVersion:          "2026-06-21",
		RequiredArtifactKind:  "evaluation_report",
		RequiredDatasetRef:    "ontology-eval-20260621",
		RequiredPolicyVersion: "v1",
		MetricThresholds: []ModelRunEvaluationMetricThreshold{{
			MetricKey:   "accuracy",
			Operator:    ModelRunEvaluationMetricOperatorGTE,
			Threshold:   0.95,
			Blocking:    true,
			Description: "minimum accepted accuracy",
		}},
		Enabled: true,
	}
	wildcard := ModelRunEvaluationPolicyProfile{
		TenantID:              "tenant-1",
		Engine:                "ontology_designer",
		RequiredArtifactKind:  "evaluation_report",
		RequiredPolicyVersion: "wildcard-v1",
		Enabled:               true,
	}
	if err := store.SaveModelRunEvaluationPolicyProfile(ctx, exact); err != nil {
		t.Fatalf("SaveModelRunEvaluationPolicyProfile exact: %v", err)
	}
	if err := store.SaveModelRunEvaluationPolicyProfile(ctx, wildcard); err != nil {
		t.Fatalf("SaveModelRunEvaluationPolicyProfile wildcard: %v", err)
	}
	if err := store.SaveModelRunEvaluationPolicyProfile(ctx, ModelRunEvaluationPolicyProfile{
		TenantID:              "tenant-2",
		Engine:                "ontology_designer",
		RequiredArtifactKind:  "evaluation_report",
		RequiredPolicyVersion: "other-v1",
		Enabled:               true,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationPolicyProfile tenant-2: %v", err)
	}

	page, err := store.ListModelRunEvaluationPolicyProfiles(ctx, ModelRunEvaluationPolicyProfileQuery{TenantID: "tenant-1", Engine: "ontology_designer", Limit: 10})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationPolicyProfiles: %v", err)
	}
	if page.Page.Total != 2 || len(page.Profiles) != 2 {
		t.Fatalf("page = %+v", page)
	}
	resolved, ok, err := ResolveModelRunEvaluationPolicyProfile(ctx, store, OntologyModelRun{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		Model:        "gpt-5",
		ModelVersion: "2026-06-21",
	})
	if err != nil {
		t.Fatalf("ResolveModelRunEvaluationPolicyProfile exact: %v", err)
	}
	if !ok || resolved.RequiredPolicyVersion != "v1" || resolved.RequiredDatasetRef != "ontology-eval-20260621" ||
		len(resolved.MetricThresholds) != 1 || resolved.MetricThresholds[0].MetricKey != "accuracy" ||
		resolved.MetricThresholds[0].Operator != ModelRunEvaluationMetricOperatorGTE ||
		resolved.MetricThresholds[0].Threshold != 0.95 ||
		!resolved.MetricThresholds[0].Blocking ||
		resolved.MetricThresholds[0].RuleID == "" ||
		resolved.MetricThresholds[0].Severity != ModelRunEvaluationThresholdSeverityCritical {
		t.Fatalf("resolved exact = %+v ok=%v", resolved, ok)
	}
	resolved, ok, err = ResolveModelRunEvaluationPolicyProfile(ctx, store, OntologyModelRun{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		Model:        "other-model",
		ModelVersion: "other-version",
	})
	if err != nil {
		t.Fatalf("ResolveModelRunEvaluationPolicyProfile wildcard: %v", err)
	}
	if !ok || resolved.RequiredPolicyVersion != "wildcard-v1" {
		t.Fatalf("resolved wildcard = %+v ok=%v", resolved, ok)
	}
}

func TestModelRunEvaluationPolicyProfileNormalizesMetricThresholdRuleIdentity(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationPolicyProfileStore()

	if err := store.SaveModelRunEvaluationPolicyProfile(ctx, ModelRunEvaluationPolicyProfile{
		TenantID:              "tenant-1",
		Engine:                "ontology_designer",
		RequiredArtifactKind:  "evaluation_report",
		RequiredPolicyVersion: "v1",
		MetricThresholds: []ModelRunEvaluationMetricThreshold{{
			MetricKey:   " accuracy ",
			Operator:    ModelRunEvaluationMetricOperatorGTE,
			Threshold:   0.95,
			Blocking:    true,
			DisplayName: " Accuracy release gate ",
		}, {
			RuleID:      " drift-warning ",
			MetricKey:   "drift_score",
			Operator:    ModelRunEvaluationMetricOperatorLTE,
			Threshold:   0.2,
			Blocking:    false,
			Severity:    ModelRunEvaluationThresholdSeverityInfo,
			Description: " drift should stay low ",
		}},
		Enabled: true,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationPolicyProfile: %v", err)
	}

	page, err := store.ListModelRunEvaluationPolicyProfiles(ctx, ModelRunEvaluationPolicyProfileQuery{TenantID: "tenant-1", Engine: "ontology_designer", Limit: 10})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationPolicyProfiles: %v", err)
	}
	if len(page.Profiles) != 1 || len(page.Profiles[0].MetricThresholds) != 2 {
		t.Fatalf("page = %+v", page)
	}
	first := page.Profiles[0].MetricThresholds[0]
	if first.RuleID == "" ||
		first.DisplayName != "Accuracy release gate" ||
		first.Severity != ModelRunEvaluationThresholdSeverityCritical {
		t.Fatalf("first threshold = %+v", first)
	}
	second := page.Profiles[0].MetricThresholds[1]
	if second.RuleID != "drift-warning" ||
		second.Severity != ModelRunEvaluationThresholdSeverityInfo ||
		second.Description != "drift should stay low" {
		t.Fatalf("second threshold = %+v", second)
	}
}

func TestMemoryModelRunEvaluationPolicyProfileStoreDoesNotResolveDisabledProfile(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationPolicyProfileStore()
	if err := store.SaveModelRunEvaluationPolicyProfile(ctx, ModelRunEvaluationPolicyProfile{
		TenantID:              "tenant-1",
		Engine:                "ontology_designer",
		Model:                 "gpt-5",
		ModelVersion:          "2026-06-21",
		RequiredArtifactKind:  "evaluation_report",
		RequiredPolicyVersion: "disabled-v1",
		Enabled:               false,
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationPolicyProfile disabled: %v", err)
	}

	resolved, ok, err := ResolveModelRunEvaluationPolicyProfile(ctx, store, OntologyModelRun{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		Model:        "gpt-5",
		ModelVersion: "2026-06-21",
	})
	if err != nil {
		t.Fatalf("ResolveModelRunEvaluationPolicyProfile: %v", err)
	}
	if ok {
		t.Fatalf("resolved disabled profile = %+v", resolved)
	}
}

func TestModelRunEvaluationPolicyProfileValidation(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryModelRunEvaluationPolicyProfileStore()
	for name, profile := range map[string]ModelRunEvaluationPolicyProfile{
		"tenant": {
			Engine:                "ontology_designer",
			RequiredArtifactKind:  "evaluation_report",
			RequiredPolicyVersion: "v1",
		},
		"engine": {
			TenantID:              "tenant-1",
			RequiredArtifactKind:  "evaluation_report",
			RequiredPolicyVersion: "v1",
		},
		"artifact": {
			TenantID:              "tenant-1",
			Engine:                "ontology_designer",
			RequiredPolicyVersion: "v1",
		},
		"policy_version": {
			TenantID:             "tenant-1",
			Engine:               "ontology_designer",
			RequiredArtifactKind: "evaluation_report",
		},
		"metric_key": {
			TenantID:              "tenant-1",
			Engine:                "ontology_designer",
			RequiredArtifactKind:  "evaluation_report",
			RequiredPolicyVersion: "v1",
			MetricThresholds: []ModelRunEvaluationMetricThreshold{{
				Operator:  ModelRunEvaluationMetricOperatorGTE,
				Threshold: 0.95,
				Blocking:  true,
			}},
		},
		"metric_operator": {
			TenantID:              "tenant-1",
			Engine:                "ontology_designer",
			RequiredArtifactKind:  "evaluation_report",
			RequiredPolicyVersion: "v1",
			MetricThresholds: []ModelRunEvaluationMetricThreshold{{
				MetricKey: "accuracy",
				Operator:  "around",
				Threshold: 0.95,
				Blocking:  true,
			}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := store.SaveModelRunEvaluationPolicyProfile(ctx, profile); err == nil {
				t.Fatalf("expected validation error")
			}
		})
	}
}
