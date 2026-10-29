package ontology

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const modelInferenceSourcePrefix = "model_inference"

type ModelInferenceMetadataProposalRequest struct {
	TenantID      string                            `json:"tenant_id,omitempty"`
	Engine        string                            `json:"engine"`
	Model         string                            `json:"model"`
	ModelVersion  string                            `json:"model_version"`
	RunID         string                            `json:"run_id"`
	InputHash     string                            `json:"input_hash"`
	PromptHash    string                            `json:"prompt_hash"`
	SourceRef     string                            `json:"source_ref,omitempty"`
	SourceVersion string                            `json:"source_version,omitempty"`
	Candidates    []ModelInferenceMetadataCandidate `json:"candidates"`
}

type ModelInferenceMetadataCandidate struct {
	TargetKind    string                `json:"target_kind"`
	TargetID      string                `json:"target_id"`
	MetadataKey   string                `json:"metadata_key"`
	ProposedValue string                `json:"proposed_value"`
	Confidence    float64               `json:"confidence"`
	Reason        string                `json:"reason,omitempty"`
	EvidenceRefs  []MetadataEvidenceRef `json:"evidence_refs,omitempty"`
}

type ModelInferenceMetadataProposalResult struct {
	Proposals           []OntologyMetadataProposal `json:"proposals"`
	ValidCandidateCount int                        `json:"valid_candidate_count"`
	SkippedInvalidCount int                        `json:"skipped_invalid_count"`
	ValidationErrors    []string                   `json:"validation_errors"`
}

type MetadataProposalIngestResult struct {
	Proposals  []OntologyMetadataProposal           `json:"proposals"`
	Generation ModelInferenceMetadataProposalResult `json:"generation"`
	Analysis   MetadataProposalConflictReport       `json:"analysis"`
}

type MetadataProposalWriter interface {
	SaveMetadataProposal(ctx context.Context, proposal OntologyMetadataProposal) error
}

func IngestModelInferenceMetadataProposals(ctx context.Context, store MetadataProposalWriter, req ModelInferenceMetadataProposalRequest) (MetadataProposalIngestResult, error) {
	return IngestModelInferenceMetadataProposalsWithLedger(ctx, store, nil, req)
}

func IngestModelInferenceMetadataProposalsWithLedger(ctx context.Context, store MetadataProposalWriter, ledger ModelRunLedger, req ModelInferenceMetadataProposalRequest) (MetadataProposalIngestResult, error) {
	if store == nil {
		return MetadataProposalIngestResult{}, fmt.Errorf("%w: metadata proposal store is required", ErrInvalidDefinition)
	}
	started, startErr := modelInferenceRunLedgerRecord(req, ModelRunStatusStarted, ModelInferenceMetadataProposalResult{}, MetadataProposalConflictReport{}, nil)
	if startErr == nil && ledger != nil {
		if err := ledger.SaveModelRun(ctx, started); err != nil {
			return MetadataProposalIngestResult{}, err
		}
	}
	generation, err := InferModelInferenceMetadataProposals(req)
	if err != nil {
		recordModelInferenceLedgerFailure(ctx, ledger, req, err)
		return MetadataProposalIngestResult{}, err
	}
	for _, proposal := range generation.Proposals {
		if err := store.SaveMetadataProposal(ctx, proposal); err != nil {
			recordModelInferenceLedgerFailure(ctx, ledger, req, err)
			return MetadataProposalIngestResult{}, err
		}
	}
	result := MetadataProposalIngestResult{
		Proposals:  append([]OntologyMetadataProposal(nil), generation.Proposals...),
		Generation: generation,
		Analysis:   AnalyzeMetadataProposalConflicts(generation.Proposals),
	}
	if ledger != nil {
		run, err := modelInferenceRunLedgerRecord(req, ModelRunStatusSucceeded, generation, result.Analysis, nil)
		if err != nil {
			return MetadataProposalIngestResult{}, err
		}
		if err := ledger.SaveModelRun(ctx, run); err != nil {
			return MetadataProposalIngestResult{}, err
		}
	}
	return result, nil
}

func InferModelInferenceMetadataProposals(req ModelInferenceMetadataProposalRequest) (ModelInferenceMetadataProposalResult, error) {
	tenantID := strings.TrimSpace(req.TenantID)
	engine := normalizeID(req.Engine)
	model := strings.TrimSpace(req.Model)
	modelVersion := strings.TrimSpace(req.ModelVersion)
	runID := strings.TrimSpace(req.RunID)
	inputHash := strings.TrimSpace(req.InputHash)
	promptHash := strings.TrimSpace(req.PromptHash)
	if engine == "" || model == "" || modelVersion == "" || runID == "" || inputHash == "" || promptHash == "" {
		return ModelInferenceMetadataProposalResult{}, fmt.Errorf("%w: model inference engine, model, model_version, run_id, input_hash, and prompt_hash are required", ErrInvalidDefinition)
	}

	sourceRef := strings.TrimSpace(req.SourceRef)
	if sourceRef == "" {
		sourceRef = engine
	}
	sourceVersion := strings.TrimSpace(req.SourceVersion)
	if sourceVersion == "" {
		sourceVersion = modelVersion
	}
	source := modelInferenceSourcePrefix + "." + engine
	modelRunEvidence := modelInferenceRunEvidence(req, engine, model, modelVersion, runID, inputHash, promptHash)

	result := ModelInferenceMetadataProposalResult{}
	for i, candidate := range req.Candidates {
		targetKind := normalizeID(candidate.TargetKind)
		targetID := normalizeID(candidate.TargetID)
		metadataKey := normalizeID(candidate.MetadataKey)
		proposedValue := strings.TrimSpace(candidate.ProposedValue)
		if targetKind == "" || targetID == "" || metadataKey == "" || proposedValue == "" {
			result.SkippedInvalidCount++
			result.ValidationErrors = append(result.ValidationErrors, fmt.Sprintf("candidate[%d] target_kind, target_id, metadata_key, and proposed_value are required", i))
			continue
		}
		result.ValidCandidateCount++
		evidence := normalizeModelInferenceEvidence(candidate.EvidenceRefs, modelRunEvidence)
		proposal := OntologyMetadataProposal{
			ID:            modelInferenceMetadataProposalID(tenantID, engine, runID, targetKind, targetID, metadataKey, proposedValue),
			TenantID:      tenantID,
			TargetKind:    targetKind,
			TargetID:      targetID,
			MetadataKey:   metadataKey,
			ProposedValue: proposedValue,
			Source:        source,
			SourceKind:    MetadataProposalSourceModelInference,
			SourceRef:     sourceRef,
			SourceVersion: sourceVersion,
			Confidence:    candidate.Confidence,
			Status:        MetadataProposalStatusProposed,
			Reason:        strings.TrimSpace(candidate.Reason),
			EvidenceRefs:  evidence,
		}
		result.Proposals = append(result.Proposals, proposal)
	}
	return result, nil
}

func modelInferenceMetadataProposalID(tenantID string, engine string, runID string, targetKind string, targetID string, key string, value string) string {
	parts := []string{
		strings.TrimSpace(tenantID),
		normalizeID(engine),
		strings.TrimSpace(runID),
		normalizeID(targetKind),
		normalizeID(targetID),
		normalizeID(key),
		strings.TrimSpace(value),
	}
	return "ontology_metadata_proposal:" + hashMappingRule(map[string]any{"parts": parts})
}

func recordModelInferenceLedgerFailure(ctx context.Context, ledger ModelRunLedger, req ModelInferenceMetadataProposalRequest, runErr error) {
	if ledger == nil {
		return
	}
	run, err := modelInferenceRunLedgerRecord(req, ModelRunStatusFailed, ModelInferenceMetadataProposalResult{}, MetadataProposalConflictReport{}, runErr)
	if err != nil {
		return
	}
	_ = ledger.SaveModelRun(ctx, run)
}

func modelInferenceRunLedgerRecord(req ModelInferenceMetadataProposalRequest, status ModelRunStatus, generation ModelInferenceMetadataProposalResult, analysis MetadataProposalConflictReport, runErr error) (OntologyModelRun, error) {
	run := OntologyModelRun{
		TenantID:              strings.TrimSpace(req.TenantID),
		Engine:                normalizeID(req.Engine),
		Model:                 strings.TrimSpace(req.Model),
		ModelVersion:          strings.TrimSpace(req.ModelVersion),
		RunID:                 strings.TrimSpace(req.RunID),
		InputHash:             strings.TrimSpace(req.InputHash),
		PromptHash:            strings.TrimSpace(req.PromptHash),
		SourceRef:             strings.TrimSpace(req.SourceRef),
		SourceVersion:         strings.TrimSpace(req.SourceVersion),
		Status:                status,
		CandidateCount:        len(req.Candidates),
		ValidCandidateCount:   generation.ValidCandidateCount,
		SkippedCandidateCount: generation.SkippedInvalidCount,
		ProposalCount:         len(generation.Proposals),
		AnalysisTotal:         analysis.Total,
		AnalysisBlockingCount: analysis.BlockingCount,
		AnalysisWarningCount:  analysis.WarningCount,
	}
	if run.SourceRef == "" {
		run.SourceRef = run.Engine
	}
	if run.SourceVersion == "" {
		run.SourceVersion = run.ModelVersion
	}
	if runErr != nil {
		run.ErrorMessage = runErr.Error()
	}
	return normalizeModelRun(run, time.Now)
}

func modelInferenceRunEvidence(req ModelInferenceMetadataProposalRequest, engine string, model string, modelVersion string, runID string, inputHash string, promptHash string) MetadataEvidenceRef {
	return MetadataEvidenceRef{
		Kind: "model_run",
		ID:   runID,
		Metadata: map[string]string{
			"engine":      engine,
			"model":       model,
			"version":     modelVersion,
			"input_hash":  inputHash,
			"prompt_hash": promptHash,
		},
	}
}

func normalizeModelInferenceEvidence(refs []MetadataEvidenceRef, modelRun MetadataEvidenceRef) []MetadataEvidenceRef {
	out := make([]MetadataEvidenceRef, 0, len(refs)+1)
	modelRunKey := modelRun.Kind + "\x00" + modelRun.ID
	seen := map[string]bool{modelRunKey: true}
	out = append(out, modelRun)
	for _, ref := range refs {
		kind := normalizeID(ref.Kind)
		id := strings.TrimSpace(ref.ID)
		if kind == "" || id == "" {
			continue
		}
		key := kind + "\x00" + id
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, MetadataEvidenceRef{
			Kind:     kind,
			ID:       id,
			Metadata: cloneMetadataEvidenceMetadata(ref.Metadata),
		})
	}
	return out
}

func cloneMetadataEvidenceMetadata(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := map[string]string{}
	for key, value := range src {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(value)
	}
	return out
}
