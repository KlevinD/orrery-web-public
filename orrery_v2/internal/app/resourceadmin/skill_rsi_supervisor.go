package resourceadmin

import (
	"context"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	skillres "orrery_v2/internal/resource/skill"
)

const (
	SkillRSICycleStatusAwaitingPromotion = "awaiting_promotion"
	SkillRSICycleStatusEvaluationFailed  = "evaluation_failed"
	SkillRSICycleStatusPromoted          = "promoted"
)

// SkillVersionCandidateGenerator supplies one immutable candidate from the
// current active definition. The generator may be backed by an LLM, a patch
// worker, or a deterministic migration; the supervisor does not assume which.
type SkillVersionCandidateGenerator interface {
	GenerateSkillVersion(ctx context.Context, skillID agentcore.SkillID, baseline skillres.SkillDefinition) (skillres.SkillDefinition, error)
}

// SkillVersionEvaluator evaluates a candidate against the exact baseline that
// the supervisor observed. Implementations should write their detailed
// evidence to Ontology and return its immutable evaluation reference.
type SkillVersionEvaluator interface {
	EvaluateSkillVersion(ctx context.Context, skillID agentcore.SkillID, baseline, candidate skillres.SkillDefinition) (SkillVersionEvaluation, error)
}

type SkillRSICycleRequest struct {
	PromoteOnPass bool
	Reason        string
}

type SkillRSICycleResult struct {
	SkillID         agentcore.SkillID
	BaselineVersion string
	Candidate       SkillVersionDTO
	Evaluation      SkillVersionEvaluation
	Promotion       *SkillVersionDTO
	Status          string
}

// RunSkillRSICycle executes one bounded RSI iteration. Candidate generation
// and evaluation are injected so this boundary can reuse the existing agent
// and Ontology workers. A successful evaluation is only promoted when the
// caller explicitly sets PromoteOnPass; the default result remains pending.
func (s *CommandService) RunSkillRSICycle(ctx context.Context, viewer ViewerRef, skillID string, generator SkillVersionCandidateGenerator, evaluator SkillVersionEvaluator, req SkillRSICycleRequest) (SkillRSICycleResult, error) {
	if s == nil || s.skills == nil {
		return SkillRSICycleResult{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceManage); err != nil {
		return SkillRSICycleResult{}, err
	}
	if s.skillEvaluations == nil {
		return SkillRSICycleResult{}, fmt.Errorf("%w: skill version evaluation store is required", ErrServiceDependency)
	}
	if generator == nil || evaluator == nil {
		return SkillRSICycleResult{}, fmt.Errorf("%w: RSI candidate generator and evaluator are required", ErrServiceDependency)
	}
	id := agentcore.SkillID(strings.TrimSpace(skillID))
	if id == "" {
		return SkillRSICycleResult{}, fmt.Errorf("%w: skill id is required", ErrInvalidRequest)
	}
	registry, err := s.versionedSkillRegistry()
	if err != nil {
		return SkillRSICycleResult{}, err
	}
	baseline, err := registry.Get(ctx, id)
	if err != nil {
		return SkillRSICycleResult{}, mapSkillPackageStoreError(err)
	}
	result := SkillRSICycleResult{SkillID: id, BaselineVersion: baseline.Version}
	candidate, err := generator.GenerateSkillVersion(ctx, id, baseline.Clone())
	if err != nil {
		return result, err
	}
	if candidate.ID != id {
		return result, fmt.Errorf("%w: generated candidate skill id does not match requested skill", ErrInvalidRequest)
	}
	if strings.TrimSpace(candidate.Version) == "" || candidate.Version == baseline.Version {
		return result, fmt.Errorf("%w: generated candidate must use a new version", ErrInvalidRequest)
	}
	candidateDTO, err := s.RegisterSkillVersion(ctx, viewer, SkillVersionCandidateRequest{Definition: candidate})
	if err != nil {
		return result, err
	}
	result.Candidate = candidateDTO
	candidate, err = registry.GetVersion(ctx, id, candidate.Version)
	if err != nil {
		return result, err
	}
	evaluation, err := evaluator.EvaluateSkillVersion(ctx, id, baseline.Clone(), candidate.Clone())
	if err != nil {
		return result, err
	}
	if err := s.RecordSkillVersionEvaluation(ctx, id, evaluation); err != nil {
		return result, err
	}
	if evaluation, err = s.skillEvaluations.GetSkillVersionEvaluation(ctx, id, evaluation.EvaluationRef); err != nil {
		return result, err
	}
	result.Evaluation = evaluation
	if evaluation.Status != SkillVersionEvaluationStatusPassed {
		result.Status = SkillRSICycleStatusEvaluationFailed
		return result, nil
	}
	if !req.PromoteOnPass {
		result.Status = SkillRSICycleStatusAwaitingPromotion
		return result, nil
	}
	promoted, err := s.PromoteSkillVersion(ctx, viewer, string(id), candidate.Version, PromoteSkillVersionRequest{
		ExpectedActiveVersion: baseline.Version,
		EvaluationRef:         evaluation.EvaluationRef,
		Reason:                req.Reason,
	})
	if err != nil {
		return result, err
	}
	result.Promotion = &promoted
	result.Status = SkillRSICycleStatusPromoted
	return result, nil
}
