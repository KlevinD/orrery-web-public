package ontology

import "testing"

func TestAnalyzeMetadataProposalConflictsFindsConflictingValues(t *testing.T) {
	report := AnalyzeMetadataProposalConflicts([]OntologyMetadataProposal{
		{
			ID:            "proposal-a",
			TenantID:      "tenant-1",
			TargetKind:    "object_type",
			TargetID:      "customer",
			MetadataKey:   "owner_domain",
			ProposedValue: "crm",
			Source:        "model_inference.ontology_designer",
			SourceKind:    MetadataProposalSourceModelInference,
			Confidence:    0.91,
			Status:        MetadataProposalStatusProposed,
			EvidenceRefs:  []MetadataEvidenceRef{modelRunEvidence("run-1")},
		},
		{
			ID:            "proposal-b",
			TenantID:      "tenant-1",
			TargetKind:    "object_type",
			TargetID:      "customer",
			MetadataKey:   "owner_domain",
			ProposedValue: "sales",
			Source:        "model_inference.ontology_designer",
			SourceKind:    MetadataProposalSourceModelInference,
			Confidence:    0.93,
			Status:        MetadataProposalStatusProposed,
			EvidenceRefs:  []MetadataEvidenceRef{modelRunEvidence("run-2")},
		},
	})

	if report.Total != 2 || report.BlockingCount != 1 || len(report.Issues) != 1 {
		t.Fatalf("report = %+v", report)
	}
	issue := report.Issues[0]
	if issue.Code != MetadataProposalIssueConflictingValues ||
		issue.Severity != MetadataProposalIssueBlocking ||
		issue.TargetKind != "object_type" ||
		issue.TargetID != "customer" ||
		issue.MetadataKey != "owner_domain" ||
		len(issue.ProposalIDs) != 2 {
		t.Fatalf("issue = %+v", issue)
	}
}

func TestAnalyzeMetadataProposalConflictsFlagsModelInferenceQualityIssues(t *testing.T) {
	report := AnalyzeMetadataProposalConflicts([]OntologyMetadataProposal{
		{
			ID:            "proposal-low-confidence",
			TargetKind:    "object_type",
			TargetID:      "customer",
			MetadataKey:   "owner_domain",
			ProposedValue: "crm",
			Source:        "model_inference.ontology_designer",
			SourceKind:    MetadataProposalSourceModelInference,
			Confidence:    0.42,
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
		},
		{
			ID:            "proposal-missing-evidence",
			TargetKind:    "object_type",
			TargetID:      "customer",
			MetadataKey:   "lifecycle",
			ProposedValue: "active",
			Source:        "model_inference.ontology_designer",
			SourceKind:    MetadataProposalSourceModelInference,
			Confidence:    0.88,
			Status:        MetadataProposalStatusProposed,
		},
		{
			ID:            "proposal-deterministic-no-evidence",
			TargetKind:    "mapping_rule",
			TargetID:      "project.created",
			MetadataKey:   "source_domain",
			ProposedValue: "mission_control",
			Source:        "deterministic.mapping_rule_metadata",
			SourceKind:    MetadataProposalSourceDeterministic,
			Confidence:    1,
			Status:        MetadataProposalStatusProposed,
		},
	})

	if report.Total != 3 || report.BlockingCount != 3 || len(report.Issues) != 3 {
		t.Fatalf("report = %+v", report)
	}
	if !metadataProposalReportHasIssue(report, MetadataProposalIssueLowConfidence, "proposal-low-confidence") {
		t.Fatalf("missing low confidence issue: %+v", report)
	}
	if !metadataProposalReportHasIssue(report, MetadataProposalIssueMissingEvidence, "proposal-missing-evidence") {
		t.Fatalf("missing evidence issue: %+v", report)
	}
	if !metadataProposalReportHasIssue(report, MetadataProposalIssueMissingModelRunTrace, "proposal-missing-evidence") {
		t.Fatalf("missing model run trace issue: %+v", report)
	}
}

func TestAnalyzeMetadataProposalConflictsRequiresModelRunTrace(t *testing.T) {
	report := AnalyzeMetadataProposalConflicts([]OntologyMetadataProposal{
		{
			ID:            "proposal-generic-evidence",
			TargetKind:    "object_type",
			TargetID:      "customer",
			MetadataKey:   "owner_domain",
			ProposedValue: "crm",
			Source:        "model_inference.ontology_designer",
			SourceKind:    MetadataProposalSourceModelInference,
			Confidence:    0.88,
			Status:        MetadataProposalStatusProposed,
			EvidenceRefs:  []MetadataEvidenceRef{{Kind: "document", ID: "brief-1"}},
		},
		{
			ID:            "proposal-incomplete-model-run",
			TargetKind:    "object_type",
			TargetID:      "customer",
			MetadataKey:   "lifecycle",
			ProposedValue: "active",
			Source:        "model_inference.ontology_designer",
			SourceKind:    MetadataProposalSourceModelInference,
			Confidence:    0.89,
			Status:        MetadataProposalStatusProposed,
			EvidenceRefs: []MetadataEvidenceRef{{
				Kind: "model_run",
				ID:   "run-1",
				Metadata: map[string]string{
					"model": "ontology-designer",
				},
			}},
		},
		{
			ID:            "proposal-complete-model-run",
			TargetKind:    "object_type",
			TargetID:      "account",
			MetadataKey:   "owner_domain",
			ProposedValue: "sales",
			Source:        "model_inference.ontology_designer",
			SourceKind:    MetadataProposalSourceModelInference,
			Confidence:    0.9,
			Status:        MetadataProposalStatusProposed,
			EvidenceRefs: []MetadataEvidenceRef{{
				Kind: "model_run",
				ID:   "run-2",
				Metadata: map[string]string{
					"model":       "ontology-designer",
					"version":     "2026-06-20",
					"input_hash":  "input-hash-2",
					"prompt_hash": "prompt-hash-2",
				},
			}},
		},
	})

	if report.Total != 3 || report.BlockingCount != 2 || len(report.Issues) != 2 {
		t.Fatalf("report = %+v", report)
	}
	if !metadataProposalReportHasIssue(report, MetadataProposalIssueMissingModelRunTrace, "proposal-generic-evidence") {
		t.Fatalf("missing model run trace issue for generic evidence: %+v", report)
	}
	if !metadataProposalReportHasIssue(report, MetadataProposalIssueMissingModelRunTrace, "proposal-incomplete-model-run") {
		t.Fatalf("missing model run trace issue for incomplete trace: %+v", report)
	}
	if metadataProposalReportHasIssue(report, MetadataProposalIssueMissingModelRunTrace, "proposal-complete-model-run") {
		t.Fatalf("complete model run trace should not be flagged: %+v", report)
	}
}

func TestAnalyzeMetadataProposalConflictsUsesCustomConfidenceThreshold(t *testing.T) {
	report := AnalyzeMetadataProposalConflictsWithPolicy([]OntologyMetadataProposal{{
		ID:            "proposal-model",
		TargetKind:    "object_type",
		TargetID:      "customer",
		MetadataKey:   "owner_domain",
		ProposedValue: "crm",
		Source:        "model_inference.ontology_designer",
		SourceKind:    MetadataProposalSourceModelInference,
		Confidence:    0.71,
		Status:        MetadataProposalStatusProposed,
		EvidenceRefs:  []MetadataEvidenceRef{modelRunEvidence("run-1")},
	}}, MetadataProposalConflictPolicy{ModelInferenceMinConfidence: 0.75})

	if !metadataProposalReportHasIssue(report, MetadataProposalIssueLowConfidence, "proposal-model") {
		t.Fatalf("report = %+v", report)
	}
}

func modelRunEvidence(id string) MetadataEvidenceRef {
	return MetadataEvidenceRef{
		Kind: "model_run",
		ID:   id,
		Metadata: map[string]string{
			"model":       "ontology-designer",
			"version":     "2026-06-20",
			"input_hash":  "input-hash-" + id,
			"prompt_hash": "prompt-hash-" + id,
		},
	}
}

func metadataProposalReportHasIssue(report MetadataProposalConflictReport, code MetadataProposalIssueCode, proposalID string) bool {
	for _, issue := range report.Issues {
		if issue.Code != code {
			continue
		}
		for _, id := range issue.ProposalIDs {
			if id == proposalID {
				return true
			}
		}
	}
	return false
}
