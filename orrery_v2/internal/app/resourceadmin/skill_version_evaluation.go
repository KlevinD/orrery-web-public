package resourceadmin

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform/db"
)

// SkillVersionEvaluationStore is populated by a trusted evaluator and read by
// the promotion command. Keeping the report separate from the promotion
// request prevents callers from asserting their own scores and pass/fail
// results at the point where the active Skill is switched.
type SkillVersionEvaluationStore interface {
	SaveSkillVersionEvaluation(ctx context.Context, skillID agentcore.SkillID, evaluation SkillVersionEvaluation) error
	GetSkillVersionEvaluation(ctx context.Context, skillID agentcore.SkillID, evaluationRef string) (SkillVersionEvaluation, error)
}

const (
	SkillVersionEvaluationStatusPassed = "passed"
	SkillVersionEvaluationStatusFailed = "failed"
)

// RecordSkillVersionEvaluationFromOntologyArtifact is the trusted bridge from
// Ontology's immutable evaluation artifact to the RSI evaluation ledger. It is
// intentionally an internal service method; callers cannot submit this report
// through the promotion HTTP request.
func (s *CommandService) RecordSkillVersionEvaluationFromOntologyArtifact(ctx context.Context, skillID agentcore.SkillID, artifact ontology.ModelRunEvaluationArtifact) error {
	if s == nil || s.skillEvaluations == nil {
		return ErrServiceDependency
	}
	evaluation, err := skillVersionEvaluationFromOntologyArtifact(artifact)
	if err != nil {
		return err
	}
	return s.RecordSkillVersionEvaluation(ctx, skillID, evaluation)
}

func skillVersionEvaluationFromOntologyArtifact(artifact ontology.ModelRunEvaluationArtifact) (SkillVersionEvaluation, error) {
	if strings.TrimSpace(artifact.ID) == "" {
		return SkillVersionEvaluation{}, fmt.Errorf("%w: ontology evaluation artifact id is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(artifact.ArtifactKind) != ontology.ModelRunEvaluationArtifactKindSkillVersionEvaluation {
		return SkillVersionEvaluation{}, fmt.Errorf("%w: ontology artifact kind must be %s", ErrInvalidRequest, ontology.ModelRunEvaluationArtifactKindSkillVersionEvaluation)
	}
	if strings.TrimSpace(artifact.Metadata["artifact_metadata_schema_status"]) != "passed" {
		return SkillVersionEvaluation{}, fmt.Errorf("%w: ontology evaluation artifact metadata schema has not passed", ErrInvalidRequest)
	}
	metadata := artifact.Metadata
	evaluation := SkillVersionEvaluation{
		EvaluationRef:           firstNonEmptyMetadata(metadata, "evaluation_ref", artifact.ID),
		CandidateVersion:        strings.TrimSpace(metadata["candidate_version"]),
		BaselineVersion:         strings.TrimSpace(metadata["baseline_version"]),
		CandidateDefinitionHash: strings.TrimSpace(metadata["candidate_definition_hash"]),
		BaselineDefinitionHash:  strings.TrimSpace(metadata["baseline_definition_hash"]),
		DatasetRef:              strings.TrimSpace(metadata["dataset_ref"]),
		PolicyVersion:           strings.TrimSpace(metadata["policy_version"]),
		EvaluatorRef:            strings.TrimSpace(metadata["evaluator_ref"]),
		Status:                  strings.ToLower(strings.TrimSpace(metadata["status"])),
		FailureReasons:          splitSkillVersionEvaluationMetadata(metadata["failure_reasons"]),
		EvidenceRefs:            splitSkillVersionEvaluationMetadata(metadata["evidence_refs"]),
	}
	var err error
	if evaluation.BaselineScore, err = parseSkillVersionEvaluationFloat(metadata, "baseline_score"); err != nil {
		return SkillVersionEvaluation{}, err
	}
	if evaluation.CandidateScore, err = parseSkillVersionEvaluationFloat(metadata, "candidate_score"); err != nil {
		return SkillVersionEvaluation{}, err
	}
	if evaluation.HeldOutPassed, err = parseSkillVersionEvaluationBool(metadata, "held_out_passed"); err != nil {
		return SkillVersionEvaluation{}, err
	}
	if evaluation.SafetyPassed, err = parseSkillVersionEvaluationBool(metadata, "safety_passed"); err != nil {
		return SkillVersionEvaluation{}, err
	}
	if evaluation.RegressionCount, err = parseSkillVersionEvaluationInt(metadata, "regression_count"); err != nil {
		return SkillVersionEvaluation{}, err
	}
	if !containsSkillVersionEvaluationString(evaluation.EvidenceRefs, artifact.ID) {
		evaluation.EvidenceRefs = append(evaluation.EvidenceRefs, artifact.ID)
	}
	return normalizeSkillVersionEvaluation(evaluation)
}

func firstNonEmptyMetadata(metadata map[string]string, key, fallback string) string {
	if value := strings.TrimSpace(metadata[key]); value != "" {
		return value
	}
	return strings.TrimSpace(fallback)
}

func splitSkillVersionEvaluationMetadata(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return cleanStringList(strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n'
	}))
}

func parseSkillVersionEvaluationFloat(metadata map[string]string, key string) (float64, error) {
	value := strings.TrimSpace(metadata[key])
	if value == "" {
		return 0, fmt.Errorf("%w: ontology evaluation artifact metadata %s is required", ErrInvalidRequest, key)
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0, fmt.Errorf("%w: ontology evaluation artifact metadata %s must be a finite number", ErrInvalidRequest, key)
	}
	return parsed, nil
}

func parseSkillVersionEvaluationBool(metadata map[string]string, key string) (bool, error) {
	value := strings.TrimSpace(metadata[key])
	if value == "" {
		return false, fmt.Errorf("%w: ontology evaluation artifact metadata %s is required", ErrInvalidRequest, key)
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%w: ontology evaluation artifact metadata %s must be boolean", ErrInvalidRequest, key)
	}
	return parsed, nil
}

func parseSkillVersionEvaluationInt(metadata map[string]string, key string) (int, error) {
	value := strings.TrimSpace(metadata[key])
	if value == "" {
		return 0, fmt.Errorf("%w: ontology evaluation artifact metadata %s is required", ErrInvalidRequest, key)
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%w: ontology evaluation artifact metadata %s must be an integer", ErrInvalidRequest, key)
	}
	return parsed, nil
}

func containsSkillVersionEvaluationString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type MemorySkillVersionEvaluationStore struct {
	mu          sync.RWMutex
	evaluations map[string]SkillVersionEvaluation
}

func NewMemorySkillVersionEvaluationStore() *MemorySkillVersionEvaluationStore {
	return &MemorySkillVersionEvaluationStore{evaluations: map[string]SkillVersionEvaluation{}}
}

func (s *MemorySkillVersionEvaluationStore) SaveSkillVersionEvaluation(ctx context.Context, skillID agentcore.SkillID, evaluation SkillVersionEvaluation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	normalized, err := normalizeSkillVersionEvaluation(evaluation)
	if err != nil {
		return err
	}
	key, err := skillVersionEvaluationKey(skillID, normalized.EvaluationRef)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.evaluations[key]; ok && !skillVersionEvaluationsEqual(existing, normalized) {
		return fmt.Errorf("%w: evaluation %s already exists", ErrConflict, normalized.EvaluationRef)
	}
	s.evaluations[key] = cloneSkillVersionEvaluation(normalized)
	return nil
}

func (s *MemorySkillVersionEvaluationStore) GetSkillVersionEvaluation(ctx context.Context, skillID agentcore.SkillID, evaluationRef string) (SkillVersionEvaluation, error) {
	if err := ctx.Err(); err != nil {
		return SkillVersionEvaluation{}, err
	}
	key, err := skillVersionEvaluationKey(skillID, evaluationRef)
	if err != nil {
		return SkillVersionEvaluation{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	evaluation, ok := s.evaluations[key]
	if !ok {
		return SkillVersionEvaluation{}, fmt.Errorf("%w: skill evaluation %s", ErrNotFound, evaluationRef)
	}
	return cloneSkillVersionEvaluation(evaluation), nil
}

type PlatformSkillVersionEvaluationStore struct {
	store *db.Store
}

const skillVersionEvaluationTable = "resource_skill_version_evaluations"

func NewPlatformSkillVersionEvaluationStore(store *db.Store) *PlatformSkillVersionEvaluationStore {
	return &PlatformSkillVersionEvaluationStore{store: store}
}

func (s *PlatformSkillVersionEvaluationStore) SaveSkillVersionEvaluation(ctx context.Context, skillID agentcore.SkillID, evaluation SkillVersionEvaluation) error {
	if s == nil || s.store == nil {
		return ErrServiceDependency
	}
	normalized, err := normalizeSkillVersionEvaluation(evaluation)
	if err != nil {
		return err
	}
	key, err := skillVersionEvaluationKey(skillID, normalized.EvaluationRef)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return fmt.Errorf("encode skill version evaluation: %w", err)
	}
	created, err := s.store.CreateRecord(ctx, skillVersionEvaluationTable, key, db.Record{"payload": string(payload)})
	if err != nil {
		return err
	}
	if created {
		return nil
	}
	existing, err := s.GetSkillVersionEvaluation(ctx, skillID, normalized.EvaluationRef)
	if err != nil {
		return err
	}
	if skillVersionEvaluationsEqual(existing, normalized) {
		return nil
	}
	return fmt.Errorf("%w: evaluation %s already exists", ErrConflict, normalized.EvaluationRef)
}

func (s *PlatformSkillVersionEvaluationStore) GetSkillVersionEvaluation(ctx context.Context, skillID agentcore.SkillID, evaluationRef string) (SkillVersionEvaluation, error) {
	if s == nil || s.store == nil {
		return SkillVersionEvaluation{}, ErrServiceDependency
	}
	key, err := skillVersionEvaluationKey(skillID, evaluationRef)
	if err != nil {
		return SkillVersionEvaluation{}, err
	}
	record, ok, err := s.store.GetRecord(ctx, skillVersionEvaluationTable, key)
	if err != nil {
		return SkillVersionEvaluation{}, err
	}
	if !ok {
		return SkillVersionEvaluation{}, fmt.Errorf("%w: skill evaluation %s", ErrNotFound, evaluationRef)
	}
	raw, ok := record["payload"].(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return SkillVersionEvaluation{}, fmt.Errorf("%w: skill evaluation payload missing", ErrInvalidRequest)
	}
	var evaluation SkillVersionEvaluation
	if err := json.Unmarshal([]byte(raw), &evaluation); err != nil {
		return SkillVersionEvaluation{}, fmt.Errorf("decode skill version evaluation: %w", err)
	}
	return normalizeSkillVersionEvaluation(evaluation)
}

func (s *CommandService) SetSkillVersionEvaluationStore(store SkillVersionEvaluationStore) {
	if s != nil {
		s.skillEvaluations = store
	}
}

// RecordSkillVersionEvaluation is called by a trusted evaluator after it has
// written the corresponding Ontology model-run artifacts. It is deliberately
// absent from the HTTP command interface.
func (s *CommandService) RecordSkillVersionEvaluation(ctx context.Context, skillID agentcore.SkillID, evaluation SkillVersionEvaluation) error {
	if s == nil || s.skillEvaluations == nil {
		return ErrServiceDependency
	}
	return s.skillEvaluations.SaveSkillVersionEvaluation(ctx, skillID, evaluation)
}

func normalizeSkillVersionEvaluation(evaluation SkillVersionEvaluation) (SkillVersionEvaluation, error) {
	evaluation.EvaluationRef = strings.TrimSpace(evaluation.EvaluationRef)
	evaluation.Status = strings.ToLower(strings.TrimSpace(evaluation.Status))
	evaluation.CandidateVersion = strings.TrimSpace(evaluation.CandidateVersion)
	evaluation.BaselineVersion = strings.TrimSpace(evaluation.BaselineVersion)
	evaluation.DatasetRef = strings.TrimSpace(evaluation.DatasetRef)
	evaluation.PolicyVersion = strings.TrimSpace(evaluation.PolicyVersion)
	evaluation.EvaluatorRef = strings.TrimSpace(evaluation.EvaluatorRef)
	evaluation.CandidateDefinitionHash = strings.TrimSpace(evaluation.CandidateDefinitionHash)
	evaluation.BaselineDefinitionHash = strings.TrimSpace(evaluation.BaselineDefinitionHash)
	evaluation.FailureReasons = cleanStringList(evaluation.FailureReasons)
	evaluation.EvidenceRefs = cleanStringList(evaluation.EvidenceRefs)
	if evaluation.EvaluationRef == "" || evaluation.CandidateVersion == "" || evaluation.BaselineVersion == "" || evaluation.DatasetRef == "" || evaluation.PolicyVersion == "" || evaluation.EvaluatorRef == "" || len(evaluation.EvidenceRefs) == 0 || !validSkillDefinitionHash(evaluation.CandidateDefinitionHash) || !validSkillDefinitionHash(evaluation.BaselineDefinitionHash) {
		return SkillVersionEvaluation{}, fmt.Errorf("%w: evaluation reference, versions, content hashes, dataset, policy, evaluator, and evidence are required", ErrInvalidRequest)
	}
	if math.IsNaN(evaluation.BaselineScore) || math.IsInf(evaluation.BaselineScore, 0) || math.IsNaN(evaluation.CandidateScore) || math.IsInf(evaluation.CandidateScore, 0) {
		return SkillVersionEvaluation{}, fmt.Errorf("%w: evaluation scores must be finite", ErrInvalidRequest)
	}
	passed := evaluation.CandidateScore >= evaluation.BaselineScore && evaluation.RegressionCount == 0 && evaluation.HeldOutPassed && evaluation.SafetyPassed
	if evaluation.Status == "" {
		if passed {
			evaluation.Status = SkillVersionEvaluationStatusPassed
		} else {
			evaluation.Status = SkillVersionEvaluationStatusFailed
		}
	}
	switch evaluation.Status {
	case SkillVersionEvaluationStatusPassed:
		if !passed {
			return SkillVersionEvaluation{}, fmt.Errorf("%w: a passed evaluation must satisfy every gate", ErrInvalidRequest)
		}
	case SkillVersionEvaluationStatusFailed:
		if len(evaluation.FailureReasons) == 0 {
			evaluation.FailureReasons = skillVersionEvaluationFailureReasons(evaluation)
		}
	default:
		return SkillVersionEvaluation{}, fmt.Errorf("%w: evaluation status must be passed or failed", ErrInvalidRequest)
	}
	return evaluation, nil
}

func skillVersionEvaluationFailureReasons(evaluation SkillVersionEvaluation) []string {
	reasons := []string{}
	if !evaluation.HeldOutPassed {
		reasons = append(reasons, "held_out_failed")
	}
	if !evaluation.SafetyPassed {
		reasons = append(reasons, "safety_failed")
	}
	if evaluation.RegressionCount != 0 {
		reasons = append(reasons, "regression_detected")
	}
	if evaluation.CandidateScore < evaluation.BaselineScore {
		reasons = append(reasons, "score_regression")
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "evaluator_rejected")
	}
	return reasons
}

func validSkillDefinitionHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func skillVersionEvaluationKey(skillID agentcore.SkillID, evaluationRef string) (string, error) {
	id := strings.TrimSpace(string(skillID))
	ref := strings.TrimSpace(evaluationRef)
	if id == "" || ref == "" || strings.ContainsAny(id, "\x00\n\r") || strings.ContainsAny(ref, "\x00\n\r") {
		return "", fmt.Errorf("%w: evaluation key is invalid", ErrInvalidRequest)
	}
	return id + "::" + ref, nil
}

func cloneSkillVersionEvaluation(evaluation SkillVersionEvaluation) SkillVersionEvaluation {
	evaluation.FailureReasons = append([]string(nil), evaluation.FailureReasons...)
	evaluation.EvidenceRefs = append([]string(nil), evaluation.EvidenceRefs...)
	return evaluation
}

func skillVersionEvaluationsEqual(left, right SkillVersionEvaluation) bool {
	left = cloneSkillVersionEvaluation(left)
	right = cloneSkillVersionEvaluation(right)
	if left.EvaluationRef != right.EvaluationRef || left.Status != right.Status || left.CandidateVersion != right.CandidateVersion || left.BaselineVersion != right.BaselineVersion || left.CandidateDefinitionHash != right.CandidateDefinitionHash || left.BaselineDefinitionHash != right.BaselineDefinitionHash || left.DatasetRef != right.DatasetRef || left.PolicyVersion != right.PolicyVersion || left.EvaluatorRef != right.EvaluatorRef || left.BaselineScore != right.BaselineScore || left.CandidateScore != right.CandidateScore || left.HeldOutPassed != right.HeldOutPassed || left.SafetyPassed != right.SafetyPassed || left.RegressionCount != right.RegressionCount || len(left.FailureReasons) != len(right.FailureReasons) || len(left.EvidenceRefs) != len(right.EvidenceRefs) {
		return false
	}
	for index := range left.FailureReasons {
		if left.FailureReasons[index] != right.FailureReasons[index] {
			return false
		}
	}
	for index := range left.EvidenceRefs {
		if left.EvidenceRefs[index] != right.EvidenceRefs[index] {
			return false
		}
	}
	return true
}

var _ SkillVersionEvaluationStore = (*MemorySkillVersionEvaluationStore)(nil)
var _ SkillVersionEvaluationStore = (*PlatformSkillVersionEvaluationStore)(nil)
