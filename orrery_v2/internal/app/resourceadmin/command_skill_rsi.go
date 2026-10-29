package resourceadmin

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	skillres "orrery_v2/internal/resource/skill"
)

const (
	skillVersionStateCandidate = "candidate"
	skillVersionStateActive    = "active"
)

// RegisterSkillVersion stores a candidate for a later evaluation. It is the
// write path intended for RSI workers; the active definition is untouched.
func (s *CommandService) RegisterSkillVersion(ctx context.Context, viewer ViewerRef, req SkillVersionCandidateRequest) (SkillVersionDTO, error) {
	if s == nil || s.skills == nil {
		return SkillVersionDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceManage); err != nil {
		return SkillVersionDTO{}, err
	}
	registry, err := s.versionedSkillRegistry()
	if err != nil {
		return SkillVersionDTO{}, err
	}
	definition := req.Definition.Clone()
	if strings.TrimSpace(string(definition.ID)) == "" || strings.TrimSpace(definition.Version) == "" {
		return SkillVersionDTO{}, fmt.Errorf("%w: skill id and version are required", ErrInvalidRequest)
	}
	if err := registry.RegisterVersion(ctx, definition); err != nil {
		return SkillVersionDTO{}, mapSkillPackageStoreError(err)
	}
	definition, err = registry.GetVersion(ctx, definition.ID, definition.Version)
	if err != nil {
		return SkillVersionDTO{}, mapSkillPackageStoreError(err)
	}
	active, err := registry.Get(ctx, definition.ID)
	if err != nil {
		return SkillVersionDTO{}, mapSkillPackageStoreError(err)
	}
	state := skillVersionStateCandidate
	if active.Version == definition.Version {
		state = skillVersionStateActive
	}
	if err := s.recordAuditWithMetadata(ctx, viewer, "resource.skill.version.register", string(definition.ID), "registered immutable skill version candidate", map[string]string{
		"version": definition.Version,
		"state":   state,
	}); err != nil {
		return SkillVersionDTO{}, err
	}
	return SkillVersionDTO{Skill: mapSkill(definition), State: state, Active: state == skillVersionStateActive, ActiveVersion: active.Version}, nil
}

// PromoteSkillVersion is the controlled RSI boundary. A candidate must carry
// a matching evaluation artifact, pass held-out and safety checks, and show no
// score regression before the active pointer is switched.
func (s *CommandService) PromoteSkillVersion(ctx context.Context, viewer ViewerRef, skillID string, version string, req PromoteSkillVersionRequest) (SkillVersionDTO, error) {
	if s == nil || s.skills == nil {
		return SkillVersionDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceManage); err != nil {
		return SkillVersionDTO{}, err
	}
	s.skillVersionMu.Lock()
	defer s.skillVersionMu.Unlock()
	registry, err := s.versionedSkillRegistry()
	if err != nil {
		return SkillVersionDTO{}, err
	}
	id := agentcore.SkillID(strings.TrimSpace(skillID))
	version = strings.TrimSpace(version)
	if id == "" || version == "" {
		return SkillVersionDTO{}, fmt.Errorf("%w: skill id and version are required", ErrInvalidRequest)
	}
	active, err := registry.Get(ctx, id)
	if err != nil {
		return SkillVersionDTO{}, mapSkillPackageStoreError(err)
	}
	if expected := strings.TrimSpace(req.ExpectedActiveVersion); expected != "" && expected != active.Version {
		return SkillVersionDTO{}, fmt.Errorf("%w: active skill version changed from %s to %s", ErrConflict, expected, active.Version)
	}
	candidate, err := registry.GetVersion(ctx, id, version)
	if err != nil {
		return SkillVersionDTO{}, mapSkillPackageStoreError(err)
	}
	if s.skillEvaluations == nil {
		return SkillVersionDTO{}, fmt.Errorf("%w: skill version evaluation store is required", ErrServiceDependency)
	}
	evaluationRef := strings.TrimSpace(req.EvaluationRef)
	if evaluationRef == "" {
		return SkillVersionDTO{}, fmt.Errorf("%w: evaluation_ref is required", ErrInvalidRequest)
	}
	evaluation, err := s.skillEvaluations.GetSkillVersionEvaluation(ctx, id, evaluationRef)
	if err != nil {
		return SkillVersionDTO{}, mapSkillPackageStoreError(err)
	}
	if err := validateSkillVersionEvaluation(evaluation, active, candidate); err != nil {
		return SkillVersionDTO{}, err
	}
	metadata := skillVersionEvaluationMetadata(evaluation)
	metadata["previous_active_version"] = active.Version
	metadata["reason"] = strings.TrimSpace(req.Reason)
	if err := s.switchAuditedSkillVersion(ctx, viewer, registry, active, candidate, false, "resource.skill.version.promote", "promoted evaluated skill version", metadata); err != nil {
		return SkillVersionDTO{}, err
	}
	return SkillVersionDTO{Skill: mapSkill(candidate), State: skillVersionStateActive, Active: true, ActiveVersion: candidate.Version}, nil
}

// RollbackSkillVersion switches the active pointer to a known immutable
// version. It remains explicit and audited so an RSI supervisor cannot make
// an unreviewed change by silently mutating the active definition.
func (s *CommandService) RollbackSkillVersion(ctx context.Context, viewer ViewerRef, skillID string, version string, req RollbackSkillVersionRequest) (SkillVersionDTO, error) {
	if s == nil || s.skills == nil {
		return SkillVersionDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceManage); err != nil {
		return SkillVersionDTO{}, err
	}
	s.skillVersionMu.Lock()
	defer s.skillVersionMu.Unlock()
	registry, err := s.versionedSkillRegistry()
	if err != nil {
		return SkillVersionDTO{}, err
	}
	id := agentcore.SkillID(strings.TrimSpace(skillID))
	version = strings.TrimSpace(version)
	if id == "" || version == "" {
		return SkillVersionDTO{}, fmt.Errorf("%w: skill id and version are required", ErrInvalidRequest)
	}
	active, err := registry.Get(ctx, id)
	if err != nil {
		return SkillVersionDTO{}, mapSkillPackageStoreError(err)
	}
	if expected := strings.TrimSpace(req.ExpectedActiveVersion); expected != "" && expected != active.Version {
		return SkillVersionDTO{}, fmt.Errorf("%w: active skill version changed from %s to %s", ErrConflict, expected, active.Version)
	}
	target, err := registry.GetVersion(ctx, id, version)
	if err != nil {
		return SkillVersionDTO{}, mapSkillPackageStoreError(err)
	}
	wasActive, err := registry.WasActiveVersion(ctx, id, target.Version)
	if err != nil {
		return SkillVersionDTO{}, mapSkillPackageStoreError(err)
	}
	if !wasActive {
		return SkillVersionDTO{}, fmt.Errorf("%w: %s@%s was never active", skillres.ErrVersionMismatch, id, target.Version)
	}
	metadata := map[string]string{
		"previous_active_version": active.Version,
		"target_version":          target.Version,
		"reason":                  strings.TrimSpace(req.Reason),
	}
	if err := s.switchAuditedSkillVersion(ctx, viewer, registry, active, target, true, "resource.skill.version.rollback", "rolled back skill version", metadata); err != nil {
		return SkillVersionDTO{}, err
	}
	return SkillVersionDTO{Skill: mapSkill(target), State: skillVersionStateActive, Active: true, ActiveVersion: target.Version}, nil
}

func (s *CommandService) versionedSkillRegistry() (skillres.VersionedRegistry, error) {
	registry, ok := s.skills.(skillres.VersionedRegistry)
	if !ok {
		return nil, ErrServiceDependency
	}
	return registry, nil
}

func validateSkillVersionEvaluation(evaluation SkillVersionEvaluation, active, candidate skillres.SkillDefinition) error {
	if evaluation.Status != SkillVersionEvaluationStatusPassed {
		return fmt.Errorf("%w: only a passed evaluation can promote a skill version", ErrInvalidRequest)
	}
	if strings.TrimSpace(evaluation.CandidateVersion) != candidate.Version {
		return fmt.Errorf("%w: evaluation candidate version does not match requested version", ErrInvalidRequest)
	}
	if strings.TrimSpace(evaluation.BaselineVersion) != active.Version {
		return fmt.Errorf("%w: evaluation baseline version does not match active version", ErrInvalidRequest)
	}
	if evaluation.CandidateDefinitionHash != skillres.DefinitionHash(candidate) || evaluation.BaselineDefinitionHash != skillres.DefinitionHash(active) {
		return fmt.Errorf("%w: evaluation content hash does not match active or candidate Skill", ErrInvalidRequest)
	}
	if strings.TrimSpace(evaluation.DatasetRef) == "" || strings.TrimSpace(evaluation.PolicyVersion) == "" || strings.TrimSpace(evaluation.EvaluatorRef) == "" {
		return fmt.Errorf("%w: dataset, policy version, and evaluator are required", ErrInvalidRequest)
	}
	if len(evaluation.EvidenceRefs) == 0 {
		return fmt.Errorf("%w: evaluation evidence is required", ErrInvalidRequest)
	}
	if math.IsNaN(evaluation.BaselineScore) || math.IsInf(evaluation.BaselineScore, 0) || math.IsNaN(evaluation.CandidateScore) || math.IsInf(evaluation.CandidateScore, 0) {
		return fmt.Errorf("%w: evaluation scores must be finite", ErrInvalidRequest)
	}
	if evaluation.CandidateScore < evaluation.BaselineScore {
		return fmt.Errorf("%w: candidate score regresses from %s to %s", ErrInvalidRequest, formatSkillScore(evaluation.BaselineScore), formatSkillScore(evaluation.CandidateScore))
	}
	if !evaluation.HeldOutPassed || !evaluation.SafetyPassed || evaluation.RegressionCount != 0 {
		return fmt.Errorf("%w: held-out, safety, and regression gates must pass", ErrInvalidRequest)
	}
	return nil
}

func skillVersionEvaluationMetadata(evaluation SkillVersionEvaluation) map[string]string {
	return map[string]string{
		"evaluation_ref":    evaluation.EvaluationRef,
		"status":            evaluation.Status,
		"candidate_version": evaluation.CandidateVersion,
		"baseline_version":  evaluation.BaselineVersion,
		"candidate_hash":    evaluation.CandidateDefinitionHash,
		"baseline_hash":     evaluation.BaselineDefinitionHash,
		"dataset_ref":       evaluation.DatasetRef,
		"policy_version":    evaluation.PolicyVersion,
		"evaluator_ref":     evaluation.EvaluatorRef,
		"baseline_score":    formatSkillScore(evaluation.BaselineScore),
		"candidate_score":   formatSkillScore(evaluation.CandidateScore),
		"held_out_passed":   strconv.FormatBool(evaluation.HeldOutPassed),
		"safety_passed":     strconv.FormatBool(evaluation.SafetyPassed),
		"regression_count":  strconv.Itoa(evaluation.RegressionCount),
		"failure_reasons":   strings.Join(evaluation.FailureReasons, ","),
		"evidence_refs":     strings.Join(evaluation.EvidenceRefs, ","),
	}
}

func formatSkillScore(score float64) string {
	return strconv.FormatFloat(score, 'f', -1, 64)
}
