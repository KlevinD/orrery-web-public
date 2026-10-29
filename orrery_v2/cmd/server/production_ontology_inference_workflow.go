package main

import (
	"context"
	"strings"
	"time"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyreasoning"
	"orrery_v2/internal/app/ontologyscope"
	"orrery_v2/internal/app/permissionscope"
)

func (a *productionOntologyInferenceAPI) CandidateWorkflow(ctx context.Context, p identity.Principal, operation string, req ontologyreasoning.WorkflowRequest) (ontologyreasoning.CandidateState, error) {
	if a == nil || a.gate == nil || a.service == nil || a.lifecycle == nil {
		return ontologyreasoning.CandidateState{}, ontologyscope.ErrDependencyUnavailable
	}
	p = identity.NormalizePrincipal(p)
	if (p.ViewerKind != identity.ViewerKindUser && p.ViewerKind != identity.ViewerKindAgent) || (p.CredentialScopeRestricted && !permissionscope.AnyAllows(p.CredentialScopes, permissionscope.OntologyManage)) {
		return ontologyreasoning.CandidateState{}, ontologyscope.ErrAccessDenied
	}
	actorKind, actorID := actioncontract.SubjectHuman, firstNonBlank(p.UserID, p.Username, p.Subject)
	if p.ViewerKind == identity.ViewerKindAgent {
		actorKind, actorID = actioncontract.SubjectAgent, strings.TrimSpace(p.AgentProfileID)
		if actorID == "" {
			return ontologyreasoning.CandidateState{}, ontologyscope.ErrAccessDenied
		}
		if operation == "review" && strings.TrimSpace(req.Reason) == "" {
			return ontologyreasoning.CandidateState{}, ontologyreasoning.ErrInvalidRequest
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	authorized, err := a.gate.AuthorizeManage(ctx, p, req.TenantID)
	if err != nil {
		return ontologyreasoning.CandidateState{}, err
	}
	original, err := a.service.GetRun(ctx, authorized.Binding().TenantID, req.RunID)
	if err != nil {
		return ontologyreasoning.CandidateState{}, err
	}
	if original.Request.Evaluation == nil || original.Request.Evaluation.RootID == "" || original.Request.Evaluation.Historical {
		return ontologyreasoning.CandidateState{}, ontologyreasoning.ErrInvalidRequest
	}
	// Rebuild the input using the current reviewer and current access policy.
	// A memory store keeps this verification out of the historical run ledger.
	verifier := *a
	verifier.service = ontologyreasoning.NewService(ontologyreasoning.ServiceOptions{Store: ontologyreasoning.NewMemoryStore()})
	verifier.lifecycle = nil
	stamp, err := a.lifecycle.InputStamp(ctx, original.TenantID)
	if err != nil {
		return ontologyreasoning.CandidateState{}, err
	}
	fresh, err := verifier.Evaluate(ctx, p, ontologyreasoning.EvaluateRequest{TenantID: original.TenantID, RootID: original.Request.Evaluation.RootID, RuleSetID: original.Request.Evaluation.RuleSetID, RuleSetVersion: original.Request.Evaluation.RuleSetVersion, Rules: original.Request.Evaluation.Rules})
	if err != nil {
		return ontologyreasoning.CandidateState{}, err
	}
	// Evaluate above performs policy filtering; bind its input to the same raw
	// source snapshot as the original run without trusting a client hash.
	fresh.Request.Evaluation.InputStamp = stamp
	if err = a.checkFence(ctx, p, authorized); err != nil {
		return ontologyreasoning.CandidateState{}, err
	}
	binding, snapshot := authorized.Binding(), authorized.Snapshot()
	actor := snapshot.PrincipalKey
	return a.lifecycle.Workflow(ctx, operation, req, original, ontologyreasoning.WorkflowContext{Actor: actor, ActorID: actorID, ActorKind: actorKind, Fresh: fresh, AuthorizationHash: inferenceDigest([]any{actor, snapshot.AuthorizationRevision, snapshot.PolicyRevision, binding.BindingVersion, binding.PersistedBindingVersion}), SourceAccessRef: contextgraph.AccessRef{Kind: binding.SourceObject.Type, ID: binding.SourceObject.ID, TenantID: binding.AuthorizationTenantID}, Guard: a.inferenceRuleSetGuard(authorized, stamp, p, fresh.Request.Evaluation), Now: time.Now().UTC()})
}
