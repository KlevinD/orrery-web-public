package ontology

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

type ModelRunProposalQuery struct {
	TenantID              string
	Engine                string
	RunID                 string
	RequiredArtifactKind  string
	RequiredDatasetRef    string
	RequiredPolicyVersion string
	MetricThresholds      []ModelRunEvaluationMetricThreshold
	Limit                 int
	Offset                int
}

type ModelRunProposalPage struct {
	Run            OntologyModelRun               `json:"run"`
	Proposals      []OntologyMetadataProposal     `json:"proposals"`
	Summary        ModelRunProposalSummary        `json:"summary"`
	EvaluationGate ModelRunEvaluationGate         `json:"evaluation_gate"`
	Analysis       MetadataProposalConflictReport `json:"analysis"`
	Page           Page                           `json:"page"`
}

type ModelRunProposalSummary struct {
	ProposalTotal       int                               `json:"proposal_total"`
	StatusCounts        map[MetadataProposalStatus]int    `json:"status_counts"`
	BlockingIssueCounts map[MetadataProposalIssueCode]int `json:"blocking_issue_counts"`
	WarningIssueCounts  map[MetadataProposalIssueCode]int `json:"warning_issue_counts"`
}

type ModelRunEvaluationGateStatus string
type ModelRunEvaluationReasonCode string
type ModelRunEvaluationThresholdResultStatus string

const (
	ModelRunEvaluationGatePassed  ModelRunEvaluationGateStatus = "passed"
	ModelRunEvaluationGateBlocked ModelRunEvaluationGateStatus = "blocked"
	ModelRunEvaluationGatePending ModelRunEvaluationGateStatus = "pending"
)

const (
	ModelRunEvaluationReasonRunFailed               ModelRunEvaluationReasonCode = "run_failed"
	ModelRunEvaluationReasonRunPending              ModelRunEvaluationReasonCode = "run_pending"
	ModelRunEvaluationReasonNoProposals             ModelRunEvaluationReasonCode = "no_proposals"
	ModelRunEvaluationReasonBlockingIssues          ModelRunEvaluationReasonCode = "blocking_issues"
	ModelRunEvaluationReasonMissingRequiredArtifact ModelRunEvaluationReasonCode = "missing_required_artifact"
	ModelRunEvaluationReasonDatasetRefMismatch      ModelRunEvaluationReasonCode = "dataset_ref_mismatch"
	ModelRunEvaluationReasonPolicyVersionMismatch   ModelRunEvaluationReasonCode = "policy_version_mismatch"
	ModelRunEvaluationReasonMissingMetric           ModelRunEvaluationReasonCode = "missing_metric"
	ModelRunEvaluationReasonInvalidMetricValue      ModelRunEvaluationReasonCode = "invalid_metric_value"
	ModelRunEvaluationReasonMetricThresholdFailed   ModelRunEvaluationReasonCode = "metric_threshold_failed"
)

const (
	ModelRunEvaluationThresholdResultPassed  ModelRunEvaluationThresholdResultStatus = "passed"
	ModelRunEvaluationThresholdResultFailed  ModelRunEvaluationThresholdResultStatus = "failed"
	ModelRunEvaluationThresholdResultMissing ModelRunEvaluationThresholdResultStatus = "missing"
	ModelRunEvaluationThresholdResultInvalid ModelRunEvaluationThresholdResultStatus = "invalid"
)

type ModelRunEvaluationGate struct {
	Status                 ModelRunEvaluationGateStatus              `json:"status"`
	Reasons                []ModelRunEvaluationReasonCode            `json:"reasons,omitempty"`
	Warnings               []ModelRunEvaluationReasonCode            `json:"warnings,omitempty"`
	MetricThresholdResults []ModelRunEvaluationMetricThresholdResult `json:"metric_threshold_results,omitempty"`
}

type ModelRunEvaluationMetricThresholdResult struct {
	RuleID        string                                  `json:"rule_id"`
	DisplayName   string                                  `json:"display_name,omitempty"`
	MetricKey     string                                  `json:"metric_key"`
	Operator      ModelRunEvaluationMetricOperator        `json:"operator"`
	Threshold     float64                                 `json:"threshold"`
	Blocking      bool                                    `json:"blocking"`
	Severity      ModelRunEvaluationThresholdSeverity     `json:"severity"`
	Status        ModelRunEvaluationThresholdResultStatus `json:"status"`
	Reason        ModelRunEvaluationReasonCode            `json:"reason,omitempty"`
	ObservedValue *float64                                `json:"observed_value,omitempty"`
}

type MetadataProposalReader interface {
	ListMetadataProposalPage(ctx context.Context, query MetadataProposalQuery) (MetadataProposalPage, error)
}

func ListModelRunProposals(ctx context.Context, ledger ModelRunLedger, proposals MetadataProposalReader, query ModelRunProposalQuery) (ModelRunProposalPage, error) {
	return listModelRunProposals(ctx, ledger, proposals, nil, query)
}

func ListModelRunProposalsWithArtifacts(ctx context.Context, ledger ModelRunLedger, proposals MetadataProposalReader, artifacts ModelRunEvaluationArtifactStore, query ModelRunProposalQuery) (ModelRunProposalPage, error) {
	if modelRunProposalArtifactPolicyRequired(query) && artifacts == nil {
		return ModelRunProposalPage{}, fmt.Errorf("%w: model run evaluation artifact store is required", ErrInvalidDefinition)
	}
	return listModelRunProposals(ctx, ledger, proposals, artifacts, query)
}

func ListModelRunProposalsWithEvaluationPolicyProfiles(ctx context.Context, ledger ModelRunLedger, proposals MetadataProposalReader, artifacts ModelRunEvaluationArtifactStore, policies ModelRunEvaluationPolicyProfileStore, query ModelRunProposalQuery) (ModelRunProposalPage, error) {
	if ledger == nil {
		return ModelRunProposalPage{}, fmt.Errorf("%w: model run ledger is required", ErrInvalidDefinition)
	}
	if policies != nil {
		run, err := modelRunForProposalQuery(ctx, ledger, query)
		if err != nil {
			return ModelRunProposalPage{}, err
		}
		profile, ok, err := ResolveModelRunEvaluationPolicyProfile(ctx, policies, run)
		if err != nil {
			return ModelRunProposalPage{}, err
		}
		if ok {
			query = ModelRunProposalQueryFromPolicyProfile(query, profile)
		}
	}
	return ListModelRunProposalsWithArtifacts(ctx, ledger, proposals, artifacts, query)
}

func listModelRunProposals(ctx context.Context, ledger ModelRunLedger, proposals MetadataProposalReader, artifacts ModelRunEvaluationArtifactStore, query ModelRunProposalQuery) (ModelRunProposalPage, error) {
	if ledger == nil || proposals == nil {
		return ModelRunProposalPage{}, fmt.Errorf("%w: model run ledger and proposal store are required", ErrInvalidDefinition)
	}
	run, err := modelRunForProposalQuery(ctx, ledger, query)
	if err != nil {
		return ModelRunProposalPage{}, err
	}
	page, err := proposals.ListMetadataProposalPage(ctx, MetadataProposalQuery{
		TenantID:   strings.TrimSpace(query.TenantID),
		SourceKind: MetadataProposalSourceModelInference,
		Limit:      200,
	})
	if err != nil {
		return ModelRunProposalPage{}, err
	}
	matched := make([]OntologyMetadataProposal, 0, len(page.Proposals))
	for _, proposal := range page.Proposals {
		if !metadataProposalBelongsToModelRun(proposal, run) {
			continue
		}
		matched = append(matched, cloneMetadataProposal(proposal))
	}
	paged := metadataProposalPage(matched, MetadataProposalQuery{Limit: query.Limit, Offset: query.Offset})
	analysis := AnalyzeMetadataProposalConflicts(matched)
	gate := evaluationGateForModelRunProposals(run, matched, analysis)
	if artifacts != nil && modelRunProposalArtifactPolicyRequired(query) {
		artifactGate, err := evaluationGateForModelRunArtifactPolicy(ctx, artifacts, run, query)
		if err != nil {
			return ModelRunProposalPage{}, err
		}
		gate = mergeModelRunEvaluationGates(gate, artifactGate)
	}
	return ModelRunProposalPage{
		Run:            run,
		Proposals:      paged.Proposals,
		Summary:        summarizeModelRunProposals(matched, analysis),
		EvaluationGate: gate,
		Analysis:       analysis,
		Page:           paged.Page,
	}, nil
}

func summarizeModelRunProposals(proposals []OntologyMetadataProposal, analysis MetadataProposalConflictReport) ModelRunProposalSummary {
	summary := ModelRunProposalSummary{
		ProposalTotal:       len(proposals),
		StatusCounts:        map[MetadataProposalStatus]int{},
		BlockingIssueCounts: map[MetadataProposalIssueCode]int{},
		WarningIssueCounts:  map[MetadataProposalIssueCode]int{},
	}
	for _, proposal := range proposals {
		summary.StatusCounts[proposal.Status]++
	}
	for _, issue := range analysis.Issues {
		switch issue.Severity {
		case MetadataProposalIssueBlocking:
			summary.BlockingIssueCounts[issue.Code]++
		case MetadataProposalIssueWarning:
			summary.WarningIssueCounts[issue.Code]++
		}
	}
	return summary
}

func evaluationGateForModelRunProposals(run OntologyModelRun, proposals []OntologyMetadataProposal, analysis MetadataProposalConflictReport) ModelRunEvaluationGate {
	reasons := []ModelRunEvaluationReasonCode{}
	if run.Status == ModelRunStatusFailed {
		reasons = append(reasons, ModelRunEvaluationReasonRunFailed)
	}
	if run.Status != ModelRunStatusSucceeded && run.Status != ModelRunStatusFailed {
		reasons = append(reasons, ModelRunEvaluationReasonRunPending)
	}
	if len(proposals) == 0 {
		reasons = append(reasons, ModelRunEvaluationReasonNoProposals)
	}
	if analysis.BlockingCount > 0 {
		reasons = append(reasons, ModelRunEvaluationReasonBlockingIssues)
	}
	if len(reasons) > 0 {
		status := ModelRunEvaluationGateBlocked
		if len(reasons) == 1 && reasons[0] == ModelRunEvaluationReasonRunPending {
			status = ModelRunEvaluationGatePending
		}
		return ModelRunEvaluationGate{Status: status, Reasons: reasons}
	}
	return ModelRunEvaluationGate{Status: ModelRunEvaluationGatePassed}
}

func evaluationGateForModelRunArtifactPolicy(ctx context.Context, artifacts ModelRunEvaluationArtifactStore, run OntologyModelRun, query ModelRunProposalQuery) (ModelRunEvaluationGate, error) {
	requiredKind := normalizeID(query.RequiredArtifactKind)
	requiredDataset := strings.TrimSpace(query.RequiredDatasetRef)
	requiredPolicy := strings.TrimSpace(query.RequiredPolicyVersion)
	artifactQuery := ModelRunEvaluationArtifactQuery{
		TenantID: strings.TrimSpace(run.TenantID),
		Engine:   run.Engine,
		RunID:    run.RunID,
		Limit:    100,
	}
	if requiredKind != "" {
		artifactQuery.ArtifactKind = requiredKind
	}
	page, err := artifacts.ListModelRunEvaluationArtifacts(ctx, artifactQuery)
	if err != nil {
		return ModelRunEvaluationGate{}, err
	}
	reasons := []ModelRunEvaluationReasonCode{}
	if requiredKind != "" && len(page.Artifacts) == 0 {
		reasons = append(reasons, ModelRunEvaluationReasonMissingRequiredArtifact)
	}
	matchingArtifacts := page.Artifacts
	if len(page.Artifacts) > 0 && requiredDataset != "" && !modelRunArtifactsHaveMetadata(page.Artifacts, "dataset_ref", requiredDataset) {
		reasons = append(reasons, ModelRunEvaluationReasonDatasetRefMismatch)
	}
	if len(page.Artifacts) > 0 && requiredPolicy != "" && !modelRunArtifactsHaveMetadata(page.Artifacts, "policy_version", requiredPolicy) {
		reasons = append(reasons, ModelRunEvaluationReasonPolicyVersionMismatch)
	}
	// All required metadata and metrics must come from the same evaluation
	// context. Looking up dataset, policy, and metrics independently allows
	// two incompatible artifacts to combine into a false passing gate.
	matchingArtifacts = filterModelRunEvaluationArtifacts(page.Artifacts, requiredDataset, requiredPolicy)
	thresholdReasons, thresholdWarnings, thresholdResults := evaluationGateForMetricThresholds(matchingArtifacts, query.MetricThresholds)
	reasons = append(reasons, thresholdReasons...)
	warnings := append([]ModelRunEvaluationReasonCode(nil), thresholdWarnings...)
	if len(reasons) == 0 {
		return ModelRunEvaluationGate{Status: ModelRunEvaluationGatePassed, Warnings: warnings, MetricThresholdResults: thresholdResults}, nil
	}
	return ModelRunEvaluationGate{Status: ModelRunEvaluationGateBlocked, Reasons: reasons, Warnings: warnings, MetricThresholdResults: thresholdResults}, nil
}

func evaluationGateForMetricThresholds(artifacts []ModelRunEvaluationArtifact, thresholds []ModelRunEvaluationMetricThreshold) ([]ModelRunEvaluationReasonCode, []ModelRunEvaluationReasonCode, []ModelRunEvaluationMetricThresholdResult) {
	reasons := []ModelRunEvaluationReasonCode{}
	warnings := []ModelRunEvaluationReasonCode{}
	results := []ModelRunEvaluationMetricThresholdResult{}
	for _, threshold := range thresholds {
		thresholds := normalizeModelRunEvaluationMetricThresholds([]ModelRunEvaluationMetricThreshold{threshold})
		if len(thresholds) == 0 {
			continue
		}
		threshold = thresholds[0]
		value, found, invalid := modelRunArtifactMetricValue(artifacts, threshold.MetricKey)
		var code ModelRunEvaluationReasonCode
		status := ModelRunEvaluationThresholdResultPassed
		var observedValue *float64
		if !found {
			code = ModelRunEvaluationReasonMissingMetric
			status = ModelRunEvaluationThresholdResultMissing
		} else if invalid {
			code = ModelRunEvaluationReasonInvalidMetricValue
			status = ModelRunEvaluationThresholdResultInvalid
		} else if !modelRunEvaluationMetricThresholdPassed(value, threshold) {
			code = ModelRunEvaluationReasonMetricThresholdFailed
			status = ModelRunEvaluationThresholdResultFailed
			observedValue = &value
		} else {
			observedValue = &value
		}
		results = append(results, ModelRunEvaluationMetricThresholdResult{
			RuleID:        threshold.RuleID,
			DisplayName:   threshold.DisplayName,
			MetricKey:     threshold.MetricKey,
			Operator:      threshold.Operator,
			Threshold:     threshold.Threshold,
			Blocking:      threshold.Blocking,
			Severity:      threshold.Severity,
			Status:        status,
			Reason:        code,
			ObservedValue: observedValue,
		})
		if code == "" {
			continue
		}
		if threshold.Blocking {
			reasons = appendModelRunEvaluationReason(reasons, code)
		} else {
			warnings = appendModelRunEvaluationReason(warnings, code)
		}
	}
	return reasons, warnings, results
}

func modelRunArtifactMetricValue(artifacts []ModelRunEvaluationArtifact, metricKey string) (float64, bool, bool) {
	metricKey = strings.TrimSpace(metricKey)
	for _, artifact := range artifacts {
		raw, ok := artifact.Metadata[metricKey]
		if !ok || strings.TrimSpace(raw) == "" {
			continue
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return 0, true, true
		}
		return value, true, false
	}
	return 0, false, false
}

func modelRunEvaluationMetricThresholdPassed(value float64, threshold ModelRunEvaluationMetricThreshold) bool {
	switch threshold.Operator {
	case ModelRunEvaluationMetricOperatorGTE:
		return value >= threshold.Threshold
	case ModelRunEvaluationMetricOperatorLTE:
		return value <= threshold.Threshold
	case ModelRunEvaluationMetricOperatorGT:
		return value > threshold.Threshold
	case ModelRunEvaluationMetricOperatorLT:
		return value < threshold.Threshold
	case ModelRunEvaluationMetricOperatorEQ:
		return value == threshold.Threshold
	default:
		return false
	}
}

func modelRunArtifactsHaveMetadata(artifacts []ModelRunEvaluationArtifact, key string, value string) bool {
	value = strings.TrimSpace(value)
	for _, artifact := range artifacts {
		if strings.TrimSpace(artifact.Metadata[key]) == value {
			return true
		}
	}
	return false
}

func filterModelRunEvaluationArtifacts(artifacts []ModelRunEvaluationArtifact, datasetRef, policyVersion string) []ModelRunEvaluationArtifact {
	datasetRef = strings.TrimSpace(datasetRef)
	policyVersion = strings.TrimSpace(policyVersion)
	if datasetRef == "" && policyVersion == "" {
		return artifacts
	}
	filtered := make([]ModelRunEvaluationArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		if datasetRef != "" && strings.TrimSpace(artifact.Metadata["dataset_ref"]) != datasetRef {
			continue
		}
		if policyVersion != "" && strings.TrimSpace(artifact.Metadata["policy_version"]) != policyVersion {
			continue
		}
		filtered = append(filtered, artifact)
	}
	return filtered
}

func modelRunProposalArtifactPolicyRequired(query ModelRunProposalQuery) bool {
	return normalizeID(query.RequiredArtifactKind) != "" ||
		strings.TrimSpace(query.RequiredDatasetRef) != "" ||
		strings.TrimSpace(query.RequiredPolicyVersion) != "" ||
		len(query.MetricThresholds) > 0
}

func appendModelRunEvaluationReason(reasons []ModelRunEvaluationReasonCode, reason ModelRunEvaluationReasonCode) []ModelRunEvaluationReasonCode {
	for _, existing := range reasons {
		if existing == reason {
			return reasons
		}
	}
	return append(reasons, reason)
}

func mergeModelRunEvaluationGates(left ModelRunEvaluationGate, right ModelRunEvaluationGate) ModelRunEvaluationGate {
	reasons := append([]ModelRunEvaluationReasonCode(nil), left.Reasons...)
	for _, reason := range right.Reasons {
		found := false
		for _, existing := range reasons {
			if existing == reason {
				found = true
				break
			}
		}
		if !found {
			reasons = append(reasons, reason)
		}
	}
	warnings := append([]ModelRunEvaluationReasonCode(nil), left.Warnings...)
	for _, warning := range right.Warnings {
		warnings = appendModelRunEvaluationReason(warnings, warning)
	}
	thresholdResults := append([]ModelRunEvaluationMetricThresholdResult(nil), left.MetricThresholdResults...)
	thresholdResults = append(thresholdResults, right.MetricThresholdResults...)
	if len(reasons) == 0 {
		if left.Status == ModelRunEvaluationGatePending || right.Status == ModelRunEvaluationGatePending {
			return ModelRunEvaluationGate{Status: ModelRunEvaluationGatePending, Warnings: warnings, MetricThresholdResults: thresholdResults}
		}
		return ModelRunEvaluationGate{Status: ModelRunEvaluationGatePassed, Warnings: warnings, MetricThresholdResults: thresholdResults}
	}
	if left.Status == ModelRunEvaluationGatePending && right.Status != ModelRunEvaluationGateBlocked && len(reasons) == len(left.Reasons) {
		return ModelRunEvaluationGate{Status: ModelRunEvaluationGatePending, Reasons: reasons, Warnings: warnings, MetricThresholdResults: thresholdResults}
	}
	return ModelRunEvaluationGate{Status: ModelRunEvaluationGateBlocked, Reasons: reasons, Warnings: warnings, MetricThresholdResults: thresholdResults}
}

func modelRunForProposalQuery(ctx context.Context, ledger ModelRunLedger, query ModelRunProposalQuery) (OntologyModelRun, error) {
	runID := strings.TrimSpace(query.RunID)
	if runID == "" {
		return OntologyModelRun{}, fmt.Errorf("%w: run_id is required", ErrInvalidDefinition)
	}
	runs, err := ledger.ListModelRuns(ctx, ModelRunQuery{
		TenantID: strings.TrimSpace(query.TenantID),
		Engine:   query.Engine,
		RunID:    runID,
		Limit:    2,
	})
	if err != nil {
		return OntologyModelRun{}, err
	}
	if len(runs.Runs) == 0 {
		return OntologyModelRun{}, fmt.Errorf("%w: model run %s", ErrNotFound, runID)
	}
	return runs.Runs[0], nil
}

func metadataProposalBelongsToModelRun(proposal OntologyMetadataProposal, run OntologyModelRun) bool {
	if strings.TrimSpace(proposal.TenantID) != strings.TrimSpace(run.TenantID) {
		return false
	}
	if proposal.SourceKind != MetadataProposalSourceModelInference {
		return false
	}
	for _, ref := range proposal.EvidenceRefs {
		if strings.TrimSpace(ref.Kind) != "model_run" || strings.TrimSpace(ref.ID) != strings.TrimSpace(run.RunID) {
			continue
		}
		engine := normalizeID(ref.Metadata["engine"])
		return engine == "" || engine == run.Engine
	}
	return false
}
