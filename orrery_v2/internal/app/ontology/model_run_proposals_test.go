package ontology

import (
	"context"
	"testing"
)

func TestListModelRunProposalsFindsRunGeneratedProposals(t *testing.T) {
	ctx := context.Background()
	proposals := NewMemoryMetadataProposalStore()
	ledger := NewMemoryModelRunLedger()

	if err := ledger.SaveModelRun(ctx, OntologyModelRun{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		Model:        "gpt-5",
		ModelVersion: "2026-06-21",
		RunID:        "run-1",
		InputHash:    "sha256:input",
		PromptHash:   "sha256:prompt",
		Status:       ModelRunStatusSucceeded,
	}); err != nil {
		t.Fatalf("SaveModelRun: %v", err)
	}
	for _, proposal := range []OntologyMetadataProposal{{
		TenantID:      "tenant-1",
		TargetKind:    "object_type",
		TargetID:      "customer",
		MetadataKey:   "owner_domain",
		ProposedValue: "crm",
		Source:        "model_inference.ontology_designer",
		SourceKind:    MetadataProposalSourceModelInference,
		SourceRef:     "ontology-designer-v2",
		SourceVersion: "2026-06-21",
		Confidence:    0.91,
		EvidenceRefs: []MetadataEvidenceRef{{
			Kind: "model_run",
			ID:   "run-1",
			Metadata: map[string]string{
				"engine":      "ontology_designer",
				"model":       "gpt-5",
				"version":     "2026-06-21",
				"input_hash":  "sha256:input",
				"prompt_hash": "sha256:prompt",
			},
		}},
	}, {
		TenantID:      "tenant-1",
		TargetKind:    "mapping_rule",
		TargetID:      "customer.created",
		MetadataKey:   "source_domain",
		ProposedValue: "crm",
		Source:        "model_inference.ontology_designer",
		SourceKind:    MetadataProposalSourceModelInference,
		Confidence:    0.88,
		EvidenceRefs: []MetadataEvidenceRef{{
			Kind: "model_run",
			ID:   "run-1",
			Metadata: map[string]string{
				"engine":      "ontology_designer",
				"model":       "gpt-5",
				"version":     "2026-06-21",
				"input_hash":  "sha256:input",
				"prompt_hash": "sha256:prompt",
			},
		}},
	}, {
		TenantID:      "tenant-1",
		TargetKind:    "object_type",
		TargetID:      "vendor",
		MetadataKey:   "owner_domain",
		ProposedValue: "erp",
		Source:        "model_inference.ontology_designer",
		SourceKind:    MetadataProposalSourceModelInference,
		Confidence:    0.9,
		EvidenceRefs:  []MetadataEvidenceRef{{Kind: "model_run", ID: "other-run"}},
	}, {
		TenantID:      "tenant-2",
		TargetKind:    "object_type",
		TargetID:      "customer",
		MetadataKey:   "owner_domain",
		ProposedValue: "tenant-2-crm",
		Source:        "model_inference.ontology_designer",
		SourceKind:    MetadataProposalSourceModelInference,
		Confidence:    0.9,
		EvidenceRefs:  []MetadataEvidenceRef{{Kind: "model_run", ID: "run-1"}},
	}} {
		if err := proposals.SaveMetadataProposal(ctx, proposal); err != nil {
			t.Fatalf("SaveMetadataProposal: %v", err)
		}
	}

	page, err := ListModelRunProposals(ctx, ledger, proposals, ModelRunProposalQuery{
		TenantID: "tenant-1",
		Engine:   "ontology_designer",
		RunID:    "run-1",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunProposals: %v", err)
	}
	if page.Run.RunID != "run-1" || page.Run.Engine != "ontology_designer" {
		t.Fatalf("run = %+v", page.Run)
	}
	if page.Page.Total != 2 || len(page.Proposals) != 2 || page.Analysis.Total != 2 {
		t.Fatalf("page = %+v", page)
	}
	for _, proposal := range page.Proposals {
		if proposal.TenantID != "tenant-1" || !metadataEvidenceContains(proposal.EvidenceRefs, "model_run", "run-1") {
			t.Fatalf("proposal = %+v", proposal)
		}
	}
}

func TestListModelRunProposalsSummarizesRunQuality(t *testing.T) {
	ctx := context.Background()
	proposals := NewMemoryMetadataProposalStore()
	ledger := NewMemoryModelRunLedger()

	if err := ledger.SaveModelRun(ctx, OntologyModelRun{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		Model:        "gpt-5",
		ModelVersion: "2026-06-21",
		RunID:        "run-quality-1",
		InputHash:    "sha256:input",
		PromptHash:   "sha256:prompt",
		Status:       ModelRunStatusSucceeded,
	}); err != nil {
		t.Fatalf("SaveModelRun: %v", err)
	}
	for _, proposal := range []OntologyMetadataProposal{{
		TenantID:      "tenant-1",
		TargetKind:    "object_type",
		TargetID:      "customer",
		MetadataKey:   "owner_domain",
		ProposedValue: "crm",
		Source:        "model_inference.ontology_designer",
		SourceKind:    MetadataProposalSourceModelInference,
		Confidence:    0.91,
		Status:        MetadataProposalStatusProposed,
		EvidenceRefs:  []MetadataEvidenceRef{modelRunEvidence("run-quality-1")},
	}, {
		TenantID:      "tenant-1",
		TargetKind:    "mapping_rule",
		TargetID:      "customer.created",
		MetadataKey:   "source_domain",
		ProposedValue: "crm",
		Source:        "model_inference.ontology_designer",
		SourceKind:    MetadataProposalSourceModelInference,
		Confidence:    0.42,
		Status:        MetadataProposalStatusProposed,
		EvidenceRefs:  []MetadataEvidenceRef{modelRunEvidence("run-quality-1")},
	}, {
		TenantID:      "tenant-1",
		TargetKind:    "object_type",
		TargetID:      "order",
		MetadataKey:   "owner_domain",
		ProposedValue: "commerce",
		Source:        "model_inference.ontology_designer",
		SourceKind:    MetadataProposalSourceModelInference,
		Confidence:    0.93,
		Status:        MetadataProposalStatusAccepted,
		EvidenceRefs:  []MetadataEvidenceRef{modelRunEvidence("run-quality-1")},
	}} {
		if err := proposals.SaveMetadataProposal(ctx, proposal); err != nil {
			t.Fatalf("SaveMetadataProposal: %v", err)
		}
	}

	page, err := ListModelRunProposals(ctx, ledger, proposals, ModelRunProposalQuery{
		TenantID: "tenant-1",
		Engine:   "ontology_designer",
		RunID:    "run-quality-1",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunProposals: %v", err)
	}
	if page.Summary.ProposalTotal != 3 {
		t.Fatalf("summary = %+v", page.Summary)
	}
	if page.Summary.StatusCounts[MetadataProposalStatusProposed] != 2 ||
		page.Summary.StatusCounts[MetadataProposalStatusAccepted] != 1 {
		t.Fatalf("status counts = %+v", page.Summary.StatusCounts)
	}
	if page.Summary.BlockingIssueCounts[MetadataProposalIssueLowConfidence] != 1 {
		t.Fatalf("blocking issue counts = %+v", page.Summary.BlockingIssueCounts)
	}
	if page.Summary.WarningIssueCounts == nil {
		t.Fatalf("warning issue counts must be initialized")
	}
	if page.EvaluationGate.Status != ModelRunEvaluationGateBlocked ||
		!modelRunEvaluationGateHasReason(page.EvaluationGate, ModelRunEvaluationReasonBlockingIssues) {
		t.Fatalf("gate = %+v", page.EvaluationGate)
	}
}

func TestListModelRunProposalsBlocksEvaluationGateForFailedRun(t *testing.T) {
	ctx := context.Background()
	proposals := NewMemoryMetadataProposalStore()
	ledger := NewMemoryModelRunLedger()

	if err := ledger.SaveModelRun(ctx, OntologyModelRun{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		Model:        "gpt-5",
		ModelVersion: "2026-06-21",
		RunID:        "run-failed-1",
		InputHash:    "sha256:input",
		PromptHash:   "sha256:prompt",
		Status:       ModelRunStatusFailed,
	}); err != nil {
		t.Fatalf("SaveModelRun: %v", err)
	}

	page, err := ListModelRunProposals(ctx, ledger, proposals, ModelRunProposalQuery{
		TenantID: "tenant-1",
		Engine:   "ontology_designer",
		RunID:    "run-failed-1",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunProposals: %v", err)
	}
	if page.EvaluationGate.Status != ModelRunEvaluationGateBlocked {
		t.Fatalf("gate = %+v", page.EvaluationGate)
	}
	if !modelRunEvaluationGateHasReason(page.EvaluationGate, ModelRunEvaluationReasonRunFailed) ||
		!modelRunEvaluationGateHasReason(page.EvaluationGate, ModelRunEvaluationReasonNoProposals) {
		t.Fatalf("gate reasons = %+v", page.EvaluationGate.Reasons)
	}
}

func TestListModelRunProposalsReportsNonBlockingMetricThresholdWarnings(t *testing.T) {
	ctx := context.Background()
	proposals := NewMemoryMetadataProposalStore()
	ledger := NewMemoryModelRunLedger()
	artifacts := NewMemoryModelRunEvaluationArtifactStore()

	if err := ledger.SaveModelRun(ctx, OntologyModelRun{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		Model:        "gpt-5",
		ModelVersion: "2026-06-21",
		RunID:        "run-nonblocking-warning-1",
		InputHash:    "sha256:input",
		PromptHash:   "sha256:prompt",
		Status:       ModelRunStatusSucceeded,
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
		Confidence:    0.98,
		Status:        MetadataProposalStatusProposed,
		EvidenceRefs:  []MetadataEvidenceRef{modelRunEvidence("run-nonblocking-warning-1")},
	}); err != nil {
		t.Fatalf("SaveMetadataProposal: %v", err)
	}
	if err := artifacts.SaveModelRunEvaluationArtifact(ctx, ModelRunEvaluationArtifact{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RunID:        "run-nonblocking-warning-1",
		ArtifactKind: "evaluation_report",
		ArtifactURI:  "s3://orrery-eval/run-nonblocking-warning-1/report.json",
		Metadata: map[string]string{
			"accuracy": "0.91",
		},
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationArtifact: %v", err)
	}

	page, err := ListModelRunProposalsWithArtifacts(ctx, ledger, proposals, artifacts, ModelRunProposalQuery{
		TenantID:             "tenant-1",
		Engine:               "ontology_designer",
		RunID:                "run-nonblocking-warning-1",
		RequiredArtifactKind: "evaluation_report",
		MetricThresholds: []ModelRunEvaluationMetricThreshold{{
			MetricKey: "accuracy",
			Operator:  ModelRunEvaluationMetricOperatorGTE,
			Threshold: 0.95,
			Blocking:  false,
		}},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListModelRunProposalsWithArtifacts: %v", err)
	}
	if page.EvaluationGate.Status != ModelRunEvaluationGatePassed || len(page.EvaluationGate.Reasons) != 0 {
		t.Fatalf("gate should pass without blocking reasons, got %+v", page.EvaluationGate)
	}
	if !modelRunEvaluationGateHasWarning(page.EvaluationGate, ModelRunEvaluationReasonMetricThresholdFailed) {
		t.Fatalf("gate warnings = %+v", page.EvaluationGate.Warnings)
	}
}

func TestListModelRunProposalsReportsMetricThresholdResults(t *testing.T) {
	ctx := context.Background()
	proposals := NewMemoryMetadataProposalStore()
	ledger := NewMemoryModelRunLedger()
	artifacts := NewMemoryModelRunEvaluationArtifactStore()

	if err := ledger.SaveModelRun(ctx, OntologyModelRun{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		Model:        "gpt-5",
		ModelVersion: "2026-06-21",
		RunID:        "run-threshold-results-1",
		InputHash:    "sha256:input",
		PromptHash:   "sha256:prompt",
		Status:       ModelRunStatusSucceeded,
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
		Confidence:    0.98,
		Status:        MetadataProposalStatusProposed,
		EvidenceRefs:  []MetadataEvidenceRef{modelRunEvidence("run-threshold-results-1")},
	}); err != nil {
		t.Fatalf("SaveMetadataProposal: %v", err)
	}
	if err := artifacts.SaveModelRunEvaluationArtifact(ctx, ModelRunEvaluationArtifact{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RunID:        "run-threshold-results-1",
		ArtifactKind: "evaluation_report",
		ArtifactURI:  "s3://orrery-eval/run-threshold-results-1/report.json",
		Metadata: map[string]string{
			"accuracy":    "0.91",
			"drift_score": "0.10",
		},
	}); err != nil {
		t.Fatalf("SaveModelRunEvaluationArtifact: %v", err)
	}

	page, err := ListModelRunProposalsWithArtifacts(ctx, ledger, proposals, artifacts, ModelRunProposalQuery{
		TenantID:             "tenant-1",
		Engine:               "ontology_designer",
		RunID:                "run-threshold-results-1",
		RequiredArtifactKind: "evaluation_report",
		MetricThresholds: []ModelRunEvaluationMetricThreshold{{
			RuleID:      "accuracy-release-gate",
			DisplayName: "Accuracy release gate",
			MetricKey:   "accuracy",
			Operator:    ModelRunEvaluationMetricOperatorGTE,
			Threshold:   0.95,
			Blocking:    true,
			Severity:    ModelRunEvaluationThresholdSeverityCritical,
		}, {
			RuleID:    "drift-warning",
			MetricKey: "drift_score",
			Operator:  ModelRunEvaluationMetricOperatorLTE,
			Threshold: 0.2,
			Blocking:  false,
			Severity:  ModelRunEvaluationThresholdSeverityWarning,
		}, {
			RuleID:    "latency-missing",
			MetricKey: "latency_ms",
			Operator:  ModelRunEvaluationMetricOperatorLTE,
			Threshold: 1200,
			Blocking:  false,
		}},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListModelRunProposalsWithArtifacts: %v", err)
	}
	results := page.EvaluationGate.MetricThresholdResults
	if len(results) != 3 {
		t.Fatalf("metric threshold results = %+v", results)
	}
	if results[0].RuleID != "accuracy-release-gate" ||
		results[0].DisplayName != "Accuracy release gate" ||
		results[0].Severity != ModelRunEvaluationThresholdSeverityCritical ||
		results[0].Status != ModelRunEvaluationThresholdResultFailed ||
		results[0].Reason != ModelRunEvaluationReasonMetricThresholdFailed ||
		results[0].ObservedValue == nil ||
		*results[0].ObservedValue != 0.91 {
		t.Fatalf("accuracy result = %+v", results[0])
	}
	if results[1].RuleID != "drift-warning" ||
		results[1].Status != ModelRunEvaluationThresholdResultPassed ||
		results[1].Reason != "" ||
		results[1].ObservedValue == nil ||
		*results[1].ObservedValue != 0.10 {
		t.Fatalf("drift result = %+v", results[1])
	}
	if results[2].RuleID != "latency-missing" ||
		results[2].Severity != ModelRunEvaluationThresholdSeverityWarning ||
		results[2].Status != ModelRunEvaluationThresholdResultMissing ||
		results[2].Reason != ModelRunEvaluationReasonMissingMetric ||
		results[2].ObservedValue != nil {
		t.Fatalf("latency result = %+v", results[2])
	}
}

func modelRunEvaluationGateHasReason(gate ModelRunEvaluationGate, reason ModelRunEvaluationReasonCode) bool {
	for _, candidate := range gate.Reasons {
		if candidate == reason {
			return true
		}
	}
	return false
}

func modelRunEvaluationGateHasWarning(gate ModelRunEvaluationGate, warning ModelRunEvaluationReasonCode) bool {
	for _, candidate := range gate.Warnings {
		if candidate == warning {
			return true
		}
	}
	return false
}
