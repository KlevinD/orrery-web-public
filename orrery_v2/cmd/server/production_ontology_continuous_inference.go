package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/ontologyreasoning"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/outbox"
)

type inferenceDecisionInput struct {
	AgentID         string                     `json:"agent_id"`
	CandidateID     string                     `json:"candidate_id"`
	SupportHash     string                     `json:"support_hash"`
	CurrentSupport  bool                       `json:"current_support"`
	ActiveAssertion bool                       `json:"active_assertion"`
	Dependencies    map[string]any             `json:"dependencies"`
	Preview         ontologyreasoning.LinkPlan `json:"relationship_preview"`
}

type inferenceModelJudge interface {
	Judge(context.Context, inferenceDecisionInput) (ontologyreasoning.ModelDecision, error)
}

type productionContinuousInference struct {
	api   *productionOntologyInferenceAPI
	judge inferenceModelJudge
}

func registerProductionContinuousInference(rt *platform.Runtime, core *db.Store, api *productionOntologyInferenceAPI, profiles *agentcore.PlatformProfileRepository, settings productionSettingsStore, secrets platformSecretResolver) error {
	if rt == nil || core == nil || api == nil || profiles == nil {
		return fmt.Errorf("continuous inference dependencies are required")
	}
	api.ruleSets = &ontologyreasoning.RuleSetStore{Store: core}
	api.profiles = profiles
	worker := &productionContinuousInference{api: api, judge: newProductionInferenceModelJudge(settings, secrets)}
	if err := rt.RegisterOutboxHandler(ontologyreasoning.InferenceEvaluationTopic, worker.Evaluate); err != nil {
		return err
	}
	if err := rt.RegisterOutboxHandler(ontologyreasoning.InferenceDecisionTopic, worker.Decide); err != nil {
		return err
	}
	return rt.RegisterSweeper("ontology.inference.source_changes", newProductionCadencedSweeper(platform.SweeperFunc(worker.Observe), rt.Clock.Now, 30*time.Second))
}

func (w *productionContinuousInference) Observe(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	stamps := map[string]string{}
	return w.api.ruleSets.Observe(ctx, time.Now().UTC(), 20, func(ctx context.Context, set ontologyreasoning.RuleSet) (string, error) {
		p, err := w.api.continuousInferencePrincipal(ctx, set.TenantID, set.AgentID)
		if err != nil {
			return "", err
		}
		authorized, err := w.api.gate.AuthorizeManage(ctx, p, set.TenantID)
		if err != nil {
			return "", err
		}
		stamp, ok := stamps[set.TenantID]
		if !ok {
			stamp, err = w.api.lifecycle.InputStamp(ctx, set.TenantID)
			if err != nil {
				return "", err
			}
			stamps[set.TenantID] = stamp
		}
		binding, snapshot := authorized.Binding(), authorized.Snapshot()
		return inferenceDigest([]any{stamp, snapshot.AuthorizationRevision, snapshot.PolicyRevision, binding.BindingVersion, binding.PersistedBindingVersion}), nil
	})
}

func (w *productionContinuousInference) activeJob(ctx context.Context, job outbox.Job) (ontologyreasoning.RuleSet, ontologyreasoning.InferenceJob, bool, error) {
	var input ontologyreasoning.InferenceJob
	if err := json.Unmarshal(job.Payload, &input); err != nil {
		return ontologyreasoning.RuleSet{}, input, false, err
	}
	if input.TenantID == "" || input.RuleSetID == "" || input.Version < 1 {
		return ontologyreasoning.RuleSet{}, input, false, ontologyreasoning.ErrInvalidRequest
	}
	set, err := w.api.ruleSets.Get(ctx, input.TenantID, input.RuleSetID)
	if errors.Is(err, ontologyreasoning.ErrNotFound) {
		return set, input, false, nil
	}
	return set, input, err == nil && set.Active && set.Version == input.Version, err
}

func (w *productionContinuousInference) Evaluate(ctx context.Context, job outbox.Job) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	set, _, active, err := w.activeJob(ctx, job)
	if err != nil || !active {
		return err
	}
	p, err := w.api.continuousInferencePrincipal(ctx, set.TenantID, set.AgentID)
	if err != nil {
		return err
	}
	run, err := w.api.Evaluate(ctx, p, ontologyreasoning.EvaluateRequest{TenantID: set.TenantID, RootID: set.RootID, RuleSetID: set.ID, RuleSetVersion: set.Version})
	if err != nil {
		return err
	}
	var eligible []ontologyreasoning.CandidateState
	for _, state := range run.CurrentCandidates {
		if ontologyreasoning.HasActiveAssertion(state) {
			eligible = append(eligible, state)
			continue
		}
		for _, candidate := range run.Candidates {
			if candidate.ID == state.CandidateID && candidate.Kind == ontologyreasoning.CandidateLink {
				eligible = append(eligible, state)
				break
			}
		}
	}
	return w.api.ruleSets.QueueDecisions(ctx, set, eligible, run.Request.Evaluation.Dependencies)
}

func (w *productionContinuousInference) Decide(ctx context.Context, job outbox.Job) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	set, input, active, err := w.activeJob(ctx, job)
	if err != nil || !active {
		return err
	}
	if input.CandidateID == "" || input.SupportHash == "" {
		return ontologyreasoning.ErrInvalidRequest
	}
	p, err := w.api.continuousInferencePrincipal(ctx, set.TenantID, set.AgentID)
	if err != nil {
		return err
	}
	run, err := w.api.Evaluate(ctx, p, ontologyreasoning.EvaluateRequest{TenantID: set.TenantID, RootID: set.RootID, RuleSetID: set.ID, RuleSetVersion: set.Version})
	if err != nil {
		return err
	}
	var state ontologyreasoning.CandidateState
	for _, candidate := range run.CurrentCandidates {
		if candidate.CandidateID == input.CandidateID {
			state = candidate
			break
		}
	}
	if state.CandidateID == "" || state.SupportHash != input.SupportHash || state.SupportRevision != input.SupportRevision {
		return nil
	}
	receiptID := ""
	if state.Receipt != nil {
		receiptID = state.Receipt.ID
	}
	if receiptID != input.ReceiptID || state.Status == "applied" || state.Status == "rejected" || state.Status == "retracted" || state.Status == "withdrawn" {
		return nil
	}
	trusted, err := w.api.inferencePreviewContext(ctx, p, run)
	if err != nil {
		return err
	}
	preview, err := w.api.lifecycle.Preview(ctx, run, state, trusted)
	if err != nil {
		return err
	}
	modelInput := inferenceDecisionInput{AgentID: set.AgentID, CandidateID: state.CandidateID, SupportHash: state.SupportHash, CurrentSupport: run.Request.Evaluation.SupportHashes[state.CandidateID] != "", ActiveAssertion: ontologyreasoning.HasActiveAssertion(state), Preview: preview}
	if modelInput.CurrentSupport {
		modelInput.Dependencies = ontologyreasoning.CandidateDependencyValues(run, state.CandidateID)
	}
	inputHash := inferenceDigest(modelInput)
	decision, found, err := w.api.ruleSets.GetDecision(ctx, job.ID, set.TenantID)
	if err != nil {
		return err
	}
	if !found {
		decision, err = w.judge.Judge(ctx, modelInput)
		if err != nil {
			return err
		}
		decision.InputHash, decision.Preview = inputHash, preview
		decision, err = w.api.ruleSets.SaveDecision(ctx, job.ID, input, decision, trusted.Guard)
		if err != nil {
			return err
		}
	}
	if decision.InputHash != inputHash {
		return ontologyreasoning.ErrStateConflict
	}
	if decision.Decision == "defer" {
		return fmt.Errorf("inference model deferred this candidate; decision is recorded")
	}
	if (decision.Decision == "approve" && !modelInput.CurrentSupport) || (decision.Decision == "retract" && !modelInput.ActiveAssertion) || (decision.Decision == "reject" && modelInput.ActiveAssertion) {
		return ontologyreasoning.ErrValidationFailed
	}
	req := ontologyreasoning.WorkflowRequest{TenantID: set.TenantID, RunID: run.ID, CandidateID: state.CandidateID, SupportHash: state.SupportHash, ExpectedRevision: state.Revision, Decision: decision.Decision, Reason: decision.Reason}
	// Each step rechecks current identity, policy, source and published version.
	// Repeating a step is safe; applying uses a job-stable idempotency key.
	p, err = w.api.continuousInferencePrincipal(ctx, set.TenantID, set.AgentID)
	if err != nil {
		return err
	}
	state, err = w.api.CandidateWorkflow(ctx, p, "review", req)
	if err != nil {
		return err
	}
	if decision.Decision == "reject" {
		return nil
	}
	req.ExpectedRevision = state.Revision
	p, err = w.api.continuousInferencePrincipal(ctx, set.TenantID, set.AgentID)
	if err != nil {
		return err
	}
	state, err = w.api.CandidateWorkflow(ctx, p, "plan", req)
	if err != nil {
		return err
	}
	expected := decision.Preview
	if decision.Decision == "retract" {
		expected.Effect = "retract"
	}
	if state.Plan == nil || state.Plan.Effect != expected.Effect || state.Plan.LinkType != expected.LinkType || state.Plan.From != expected.From || state.Plan.To != expected.To || state.Plan.BeforeHash != expected.BeforeHash {
		return ontologyreasoning.ErrStateConflict
	}
	req.ExpectedRevision, req.PlanID, req.Confirmed, req.IdempotencyKey = state.Revision, state.Plan.ID, true, job.ID
	p, err = w.api.continuousInferencePrincipal(ctx, set.TenantID, set.AgentID)
	if err != nil {
		return err
	}
	_, err = w.api.CandidateWorkflow(ctx, p, "apply", req)
	return err
}
