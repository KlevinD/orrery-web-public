package ontology

import (
	"sort"
	"strings"
)

type MetadataProposalIssueCode string
type MetadataProposalIssueSeverity string

const (
	MetadataProposalIssueConflictingValues    MetadataProposalIssueCode = "conflicting_values"
	MetadataProposalIssueLowConfidence        MetadataProposalIssueCode = "low_confidence"
	MetadataProposalIssueMissingEvidence      MetadataProposalIssueCode = "missing_evidence"
	MetadataProposalIssueMissingModelRunTrace MetadataProposalIssueCode = "missing_model_run_trace"
)

const (
	MetadataProposalIssueBlocking MetadataProposalIssueSeverity = "blocking"
	MetadataProposalIssueWarning  MetadataProposalIssueSeverity = "warning"
)

type MetadataProposalConflictPolicy struct {
	ModelInferenceMinConfidence float64
}

type MetadataProposalConflictReport struct {
	Total         int                     `json:"total"`
	BlockingCount int                     `json:"blocking_count"`
	WarningCount  int                     `json:"warning_count"`
	Issues        []MetadataProposalIssue `json:"issues,omitempty"`
}

type MetadataProposalIssue struct {
	Code           MetadataProposalIssueCode     `json:"code"`
	Severity       MetadataProposalIssueSeverity `json:"severity"`
	ProposalIDs    []string                      `json:"proposal_ids,omitempty"`
	TenantID       string                        `json:"tenant_id,omitempty"`
	TargetKind     string                        `json:"target_kind,omitempty"`
	TargetID       string                        `json:"target_id,omitempty"`
	MetadataKey    string                        `json:"metadata_key,omitempty"`
	ProposedValues []string                      `json:"proposed_values,omitempty"`
	Message        string                        `json:"message,omitempty"`
}

func AnalyzeMetadataProposalConflicts(proposals []OntologyMetadataProposal) MetadataProposalConflictReport {
	return AnalyzeMetadataProposalConflictsWithPolicy(proposals, MetadataProposalConflictPolicy{})
}

func AnalyzeMetadataProposalConflictsWithPolicy(proposals []OntologyMetadataProposal, policy MetadataProposalConflictPolicy) MetadataProposalConflictReport {
	if policy.ModelInferenceMinConfidence <= 0 {
		policy.ModelInferenceMinConfidence = 0.75
	}
	report := MetadataProposalConflictReport{Total: len(proposals)}
	proposedByTarget := map[string][]OntologyMetadataProposal{}
	for _, proposal := range proposals {
		normalized, err := normalizeMetadataProposal(proposal, nil)
		if err != nil {
			continue
		}
		if normalized.Status != MetadataProposalStatusProposed {
			continue
		}
		key := metadataProposalConflictKey(normalized)
		proposedByTarget[key] = append(proposedByTarget[key], normalized)
		if normalized.SourceKind == MetadataProposalSourceModelInference {
			appendModelInferenceQualityIssues(normalized, policy, &report)
		}
	}
	appendConflictingValueIssues(proposedByTarget, &report)
	sort.Slice(report.Issues, func(i, j int) bool {
		if report.Issues[i].Code != report.Issues[j].Code {
			return report.Issues[i].Code < report.Issues[j].Code
		}
		if report.Issues[i].TargetKind != report.Issues[j].TargetKind {
			return report.Issues[i].TargetKind < report.Issues[j].TargetKind
		}
		if report.Issues[i].TargetID != report.Issues[j].TargetID {
			return report.Issues[i].TargetID < report.Issues[j].TargetID
		}
		return strings.Join(report.Issues[i].ProposalIDs, ",") < strings.Join(report.Issues[j].ProposalIDs, ",")
	})
	return report
}

func appendModelInferenceQualityIssues(proposal OntologyMetadataProposal, policy MetadataProposalConflictPolicy, report *MetadataProposalConflictReport) {
	if proposal.Confidence < policy.ModelInferenceMinConfidence {
		report.addIssue(MetadataProposalIssue{
			Code:        MetadataProposalIssueLowConfidence,
			Severity:    MetadataProposalIssueBlocking,
			ProposalIDs: []string{proposal.ID},
			TenantID:    proposal.TenantID,
			TargetKind:  proposal.TargetKind,
			TargetID:    proposal.TargetID,
			MetadataKey: proposal.MetadataKey,
			Message:     "model inference proposal confidence is below the configured threshold",
		})
	}
	if len(proposal.EvidenceRefs) == 0 {
		report.addIssue(MetadataProposalIssue{
			Code:        MetadataProposalIssueMissingEvidence,
			Severity:    MetadataProposalIssueBlocking,
			ProposalIDs: []string{proposal.ID},
			TenantID:    proposal.TenantID,
			TargetKind:  proposal.TargetKind,
			TargetID:    proposal.TargetID,
			MetadataKey: proposal.MetadataKey,
			Message:     "model inference proposal requires at least one evidence reference",
		})
	}
	if !metadataProposalHasCompleteModelRunTrace(proposal.EvidenceRefs) {
		report.addIssue(MetadataProposalIssue{
			Code:        MetadataProposalIssueMissingModelRunTrace,
			Severity:    MetadataProposalIssueBlocking,
			ProposalIDs: []string{proposal.ID},
			TenantID:    proposal.TenantID,
			TargetKind:  proposal.TargetKind,
			TargetID:    proposal.TargetID,
			MetadataKey: proposal.MetadataKey,
			Message:     "model inference proposal requires a model_run evidence reference with model, version, input_hash, and prompt_hash metadata",
		})
	}
}

func metadataProposalHasCompleteModelRunTrace(refs []MetadataEvidenceRef) bool {
	for _, ref := range refs {
		if strings.TrimSpace(ref.Kind) != "model_run" || strings.TrimSpace(ref.ID) == "" {
			continue
		}
		if strings.TrimSpace(ref.Metadata["model"]) == "" ||
			strings.TrimSpace(ref.Metadata["version"]) == "" ||
			strings.TrimSpace(ref.Metadata["input_hash"]) == "" ||
			strings.TrimSpace(ref.Metadata["prompt_hash"]) == "" {
			continue
		}
		return true
	}
	return false
}

func appendConflictingValueIssues(proposedByTarget map[string][]OntologyMetadataProposal, report *MetadataProposalConflictReport) {
	for _, group := range proposedByTarget {
		if len(group) < 2 {
			continue
		}
		values := map[string]bool{}
		for _, proposal := range group {
			values[proposal.ProposedValue] = true
		}
		if len(values) < 2 {
			continue
		}
		proposedValues := make([]string, 0, len(values))
		for value := range values {
			proposedValues = append(proposedValues, value)
		}
		sort.Strings(proposedValues)
		sort.Slice(group, func(i, j int) bool { return group[i].ID < group[j].ID })
		proposalIDs := make([]string, 0, len(group))
		for _, proposal := range group {
			proposalIDs = append(proposalIDs, proposal.ID)
		}
		first := group[0]
		report.addIssue(MetadataProposalIssue{
			Code:           MetadataProposalIssueConflictingValues,
			Severity:       MetadataProposalIssueBlocking,
			ProposalIDs:    proposalIDs,
			TenantID:       first.TenantID,
			TargetKind:     first.TargetKind,
			TargetID:       first.TargetID,
			MetadataKey:    first.MetadataKey,
			ProposedValues: proposedValues,
			Message:        "multiple proposed values target the same ontology metadata field",
		})
	}
}

func metadataProposalConflictKey(proposal OntologyMetadataProposal) string {
	return strings.Join([]string{
		strings.TrimSpace(proposal.TenantID),
		normalizeID(proposal.TargetKind),
		normalizeID(proposal.TargetID),
		normalizeID(proposal.MetadataKey),
	}, "\x00")
}

func firstBlockingMetadataProposalIssueForProposal(proposal OntologyMetadataProposal) (MetadataProposalIssue, bool) {
	report := AnalyzeMetadataProposalConflicts([]OntologyMetadataProposal{proposal})
	for _, issue := range report.Issues {
		if issue.Severity != MetadataProposalIssueBlocking {
			continue
		}
		for _, proposalID := range issue.ProposalIDs {
			if proposalID == proposal.ID {
				return issue, true
			}
		}
	}
	return MetadataProposalIssue{}, false
}

func (r *MetadataProposalConflictReport) addIssue(issue MetadataProposalIssue) {
	r.Issues = append(r.Issues, issue)
	switch issue.Severity {
	case MetadataProposalIssueBlocking:
		r.BlockingCount++
	case MetadataProposalIssueWarning:
		r.WarningCount++
	}
}
