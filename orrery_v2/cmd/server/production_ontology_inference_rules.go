package main

import (
	"context"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyreasoning"
	"orrery_v2/internal/app/ontologyscope"
	"orrery_v2/internal/app/permissionscope"
	"orrery_v2/internal/platform/db"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func (a *productionOntologyInferenceAPI) InferenceRules(ctx context.Context, p identity.Principal, req ontologyreasoning.RuleSetRequest) ([]ontologyreasoning.RuleSet, error) {
	if a == nil || a.ruleSets == nil || a.lifecycle == nil {
		return nil, ontologyscope.ErrDependencyUnavailable
	}
	p = identity.NormalizePrincipal(p)
	if p.ViewerKind != identity.ViewerKindAgent || p.AgentProfileID == "" || !permissionscope.AnyAllows(p.CredentialScopes, permissionscope.OntologyManage) {
		return nil, ontologyscope.ErrAccessDenied
	}
	if _, err := a.continuousInferencePrincipal(ctx, req.TenantID, p.AgentProfileID); err != nil {
		return nil, err
	}
	authorized, err := a.gate.AuthorizeManage(ctx, p, req.TenantID)
	if err != nil {
		return nil, err
	}
	tenant := authorized.Binding().TenantID
	if req.Operation == "list" {
		return a.ruleSets.List(ctx, tenant, p.AgentProfileID)
	}
	if req.Operation != "publish" && req.Operation != "pause" {
		return nil, ontologyreasoning.ErrInvalidRequest
	}
	set := ontologyreasoning.RuleSet{TenantID: tenant, ID: req.ID, AgentID: p.AgentProfileID, RootID: req.RootID, Rules: req.Rules, Active: true, PublishedAt: time.Now().UTC()}
	if req.Operation == "pause" {
		set, err = a.ruleSets.Get(ctx, tenant, req.ID)
		if err != nil {
			return nil, err
		}
		if set.AgentID != p.AgentProfileID {
			return nil, ontologyscope.ErrAccessDenied
		}
		set.Active = false
		set.PublishedAt = time.Now().UTC()
	} else {
		// Validate the new rules against current authorized input before making
		// them active. Publication does not alter an ad-hoc candidate session.
		verifier := *a
		verifier.lifecycle = nil
		verifier.service = ontologyreasoning.NewService(ontologyreasoning.ServiceOptions{Store: ontologyreasoning.NewMemoryStore()})
		if _, err = verifier.Evaluate(ctx, p, ontologyreasoning.EvaluateRequest{TenantID: tenant, RootID: set.RootID, Rules: set.Rules}); err != nil {
			return nil, err
		}
	}
	stamp, err := a.lifecycle.InputStamp(ctx, tenant)
	if err != nil {
		return nil, err
	}
	set, err = a.ruleSets.Publish(ctx, set, req.ExpectedVersion, a.inferenceCommitGuard(authorized, stamp))
	if err != nil {
		return nil, err
	}
	return []ontologyreasoning.RuleSet{set}, nil
}

// Reconstruct identity from the stored owner and current tool grants, never a
// queued credential snapshot or model-supplied permissions.
func (a *productionOntologyInferenceAPI) continuousInferencePrincipal(ctx context.Context, tenant, agentID string) (identity.Principal, error) {
	if a.profiles == nil {
		return identity.Principal{}, ontologyscope.ErrDependencyUnavailable
	}
	profile, err := a.profiles.GetProfile(ctx, agentcore.AgentProfileID(agentID))
	if err != nil {
		return identity.Principal{}, err
	}
	if !profile.Enabled || string(profile.ID) != agentID || profile.AgentKind == agentcore.AgentKindHuman {
		return identity.Principal{}, ontologyscope.ErrAccessDenied
	}
	for _, id := range []agentcore.ToolID{toolstd.OntologyInfer, toolstd.OntologyInferenceWorkflow, toolstd.OntologyInferenceRules} {
		allowed := false
		for _, ref := range profile.ToolRefs {
			if ref.ToolID == id && permissionscope.AnyAllows([]string{string(ref.PermissionScope)}, permissionscope.OntologyManage) {
				allowed = true
			}
		}
		if !allowed {
			return identity.Principal{}, ontologyscope.ErrAccessDenied
		}
	}
	return identity.NormalizePrincipal(identity.Principal{SubjectProvider: "orrery-agent-runtime", Subject: agentID, AgentProfileID: agentID, ViewerKind: identity.ViewerKindAgent, TenantID: tenant, CoarseScopes: []string{permissionscope.OntologyManage}, CredentialScopes: []string{permissionscope.OntologyManage}, CredentialScopeRestricted: true}), nil
}

func (a *productionOntologyInferenceAPI) inferenceRuleSetGuard(authorized ontologyscope.AuthorizedScopeContext, stamp string, p identity.Principal, e *ontologyreasoning.EvaluationRecord) func(context.Context, db.Tx) error {
	return func(ctx context.Context, tx db.Tx) error {
		if e != nil && (e.Historical || (e.NextBoundary != nil && !time.Now().UTC().Before(*e.NextBoundary))) {
			return ontologyreasoning.ErrStateConflict
		}
		if err := a.inferenceCommitGuard(authorized, stamp)(ctx, tx); err != nil {
			return err
		}
		if e != nil && e.RuleSetID != "" {
			if a.ruleSets == nil {
				return ontologyscope.ErrDependencyUnavailable
			}
			return a.ruleSets.CheckActiveTx(ctx, tx, authorized.Binding().TenantID, e.RuleSetID, p.AgentProfileID, e.RuleSetVersion)
		}
		return nil
	}
}

func (a *productionOntologyInferenceAPI) inferencePreviewContext(ctx context.Context, p identity.Principal, run ontologyreasoning.ReasoningRun) (ontologyreasoning.WorkflowContext, error) {
	if _, err := a.continuousInferencePrincipal(ctx, run.TenantID, p.AgentProfileID); err != nil {
		return ontologyreasoning.WorkflowContext{}, err
	}
	authorized, err := a.gate.AuthorizeManage(ctx, p, run.TenantID)
	if err != nil {
		return ontologyreasoning.WorkflowContext{}, err
	}
	binding, snapshot := authorized.Binding(), authorized.Snapshot()
	return ontologyreasoning.WorkflowContext{Actor: snapshot.PrincipalKey, ActorID: strings.TrimSpace(p.AgentProfileID), ActorKind: actioncontract.SubjectAgent, Fresh: run, SourceAccessRef: contextgraph.AccessRef{Kind: binding.SourceObject.Type, ID: binding.SourceObject.ID, TenantID: binding.AuthorizationTenantID}, Guard: a.inferenceRuleSetGuard(authorized, run.Request.Evaluation.InputStamp, p, run.Request.Evaluation), Now: time.Now().UTC()}, nil
}
