package main

import (
	"context"
	"strings"
	"time"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	"orrery_v2/internal/app/reliability"
	httptransport "orrery_v2/internal/transport/http"
)

func (a *productionOntologyReliabilityAPI) RequestRuleActivationReview(ctx context.Context, principal identity.Principal, tenantID, ruleID string, request httptransport.OntologyReliabilityActivationReviewRequest) (httptransport.OntologyReliabilityActivationReviewDTO, error) {
	if err := a.authorize(ctx, principal, tenantID, true); err != nil {
		return httptransport.OntologyReliabilityActivationReviewDTO{}, err
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" || len(strings.TrimSpace(request.Purpose)) > 1024 {
		return httptransport.OntologyReliabilityActivationReviewDTO{}, ontologycontract.ErrInvalidRequest
	}
	material, binding, err := a.activationMaterial(ctx, tenantID, ruleID)
	if err != nil {
		return httptransport.OntologyReliabilityActivationReviewDTO{}, ontologyReliabilityError(err)
	}
	if !binding.activationReady || binding.preflight == nil || binding.activationDiagnosis == nil {
		return httptransport.OntologyReliabilityActivationReviewDTO{}, ontologycontract.ErrInvalidRequest
	}
	actor, err := reliabilityHILActor(principal)
	if err != nil {
		return httptransport.OntologyReliabilityActivationReviewDTO{}, ontologycontract.ErrInvalidRequest
	}
	if actor.Kind != hil.ActorHuman {
		return httptransport.OntologyReliabilityActivationReviewDTO{}, ontologyscope.ErrAccessDenied
	}
	purpose := strings.TrimSpace(request.Purpose)
	if purpose == "" {
		purpose = "Activate an evidence-backed repair rule after a current preflight"
	}
	refs := repairRuleActivationObjectRefs(binding.rule, material.incident, *binding.activationDiagnosis, *binding.preflight)
	expiresAt := a.nowUTC().Add(30 * time.Minute)
	if binding.preflight.ExpiresAt.Before(expiresAt) {
		expiresAt = binding.preflight.ExpiresAt
	}
	result, err := a.decisions.CreateDecision(ctx, hil.CreateDecisionInput{
		TenantID: tenantID, Requester: actor, Action: productionRepairRuleActivationAction,
		ActionTypeID: productionRepairRuleActivationActionTypeID, Resource: hil.ObjectRef{Kind: "repair_rule", ID: ruleID}, ObjectRefs: refs,
		ArgsHash: binding.activationArgsHash, Purpose: purpose, RiskLevel: hil.RiskHigh, PreferredMode: hil.ModeApproval,
		ApprovalRequired: true, SubjectRefs: refs, IdempotencyKey: request.IdempotencyKey,
		PayloadPreview: map[string]any{
			"source": "ontology_reliability_rule_activation", "approval_assigned_to": actor.ID,
			"rule_id": ruleID, "rule_version": binding.rule.Version, "incident_id": material.incident.ID,
			"incident_version": material.incident.Version, "diagnosis_case_id": binding.activationDiagnosis.ID,
			"diagnosis_version": binding.activationDiagnosis.Version, "preflight_evidence_id": binding.preflight.EvidenceID,
			"strategy": string(binding.rule.Strategy), "auto_apply": false,
		},
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return httptransport.OntologyReliabilityActivationReviewDTO{}, ontologyReliabilityError(err)
	}
	return reliabilityActivationReviewDTO(result.Decision, binding, material.incident.ID, true), nil
}

func (a *productionOntologyReliabilityAPI) ResolveRuleActivationReview(ctx context.Context, principal identity.Principal, tenantID, ruleID, decisionID string, request httptransport.OntologyReliabilityAuthorizationDecisionRequest) (httptransport.OntologyReliabilityActivationReviewResolutionDTO, error) {
	if err := a.authorize(ctx, principal, tenantID, true); err != nil {
		return httptransport.OntologyReliabilityActivationReviewResolutionDTO{}, err
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return httptransport.OntologyReliabilityActivationReviewResolutionDTO{}, ontologycontract.ErrInvalidRequest
	}
	material, binding, _, current, err := a.currentActivationDecision(ctx, tenantID, ruleID, decisionID)
	if err != nil {
		return httptransport.OntologyReliabilityActivationReviewResolutionDTO{}, ontologyReliabilityError(err)
	}
	if !current {
		return httptransport.OntologyReliabilityActivationReviewResolutionDTO{}, ontologycontract.ErrStateConflict
	}
	actor, err := reliabilityHILActor(principal)
	if err != nil {
		return httptransport.OntologyReliabilityActivationReviewResolutionDTO{}, ontologycontract.ErrInvalidRequest
	}
	resolution, err := a.decisions.ResolveDecision(ctx, tenantID, decisionID, actor, hil.ResolveDecisionInput{
		Decision: request.Decision, Reason: strings.TrimSpace(request.Reason), IdempotencyKey: request.IdempotencyKey,
	})
	if err != nil {
		return httptransport.OntologyReliabilityActivationReviewResolutionDTO{}, ontologyReliabilityError(err)
	}
	return httptransport.OntologyReliabilityActivationReviewResolutionDTO{
		Review: reliabilityActivationReviewDTO(resolution.Decision, binding, material.incident.ID, true), Effect: string(resolution.Receipt.Effect),
	}, nil
}

func (a *productionOntologyReliabilityAPI) ActivateRepairRule(ctx context.Context, principal identity.Principal, tenantID, ruleID, decisionID string, request httptransport.OntologyReliabilityActivationRequest) (httptransport.OntologyReliabilityRepairRuleDTO, error) {
	if err := a.authorize(ctx, principal, tenantID, true); err != nil {
		return httptransport.OntologyReliabilityRepairRuleDTO{}, err
	}
	if a.control == nil || strings.TrimSpace(request.IdempotencyKey) == "" {
		return httptransport.OntologyReliabilityRepairRuleDTO{}, ontologycontract.ErrInvalidRequest
	}
	existing, err := a.repository.GetRepairRule(ctx, tenantID, ruleID)
	if err != nil {
		return httptransport.OntologyReliabilityRepairRuleDTO{}, ontologyReliabilityError(err)
	}
	if existing.Status == reliability.RepairRuleStatusActive && existing.ActivationDecisionID == decisionID {
		material, loadErr := a.loadMaterial(ctx, tenantID, existing.SourceIncidentID)
		if loadErr != nil {
			return httptransport.OntologyReliabilityRepairRuleDTO{}, ontologyReliabilityError(loadErr)
		}
		binding, bindErr := a.bindingForRule(material, ruleID)
		if bindErr != nil {
			return httptransport.OntologyReliabilityRepairRuleDTO{}, ontologyReliabilityError(bindErr)
		}
		return reliabilityRuleDTO(binding), nil
	}
	material, binding, decision, current, err := a.currentActivationDecision(ctx, tenantID, ruleID, decisionID)
	if err != nil {
		return httptransport.OntologyReliabilityRepairRuleDTO{}, ontologyReliabilityError(err)
	}
	if !current || decision.Status != hil.DecisionResolved || binding.preflight == nil {
		return httptransport.OntologyReliabilityRepairRuleDTO{}, ontologycontract.ErrStateConflict
	}
	actor, err := reliabilityHILActor(principal)
	if err != nil {
		return httptransport.OntologyReliabilityRepairRuleDTO{}, ontologycontract.ErrInvalidRequest
	}
	receipt, err := a.decisions.GetActionDecisionReceipt(ctx, tenantID, decisionID, actor)
	if err != nil {
		return httptransport.OntologyReliabilityRepairRuleDTO{}, ontologyReliabilityError(err)
	}
	domainActor, err := reliabilityDomainActor(principal)
	if err != nil {
		return httptransport.OntologyReliabilityRepairRuleDTO{}, ontologycontract.ErrInvalidRequest
	}
	candidate := binding.rule
	candidate.Status = reliability.RepairRuleStatusActive
	candidate.AutoApply = false
	activated, err := a.control.UpsertRepairRule(ctx, reliability.RepairRuleCommand{
		ID: request.IdempotencyKey, Rule: candidate, Actor: domainActor,
		Activation: &reliability.RepairRuleActivationAuthorization{
			DecisionID: decision.ID, ReceiptID: receipt.ID, ArgsHash: decision.ArgsHash,
			Purpose: decision.Purpose, PreflightEvidenceID: binding.preflight.EvidenceID,
		},
	})
	if err != nil {
		return httptransport.OntologyReliabilityRepairRuleDTO{}, ontologyReliabilityError(err)
	}
	updatedMaterial, err := a.loadMaterial(ctx, tenantID, material.incident.ID)
	if err != nil {
		return httptransport.OntologyReliabilityRepairRuleDTO{}, ontologyReliabilityError(err)
	}
	updatedBinding, err := a.bindingForRule(updatedMaterial, activated.ID)
	if err != nil {
		return httptransport.OntologyReliabilityRepairRuleDTO{}, ontologyReliabilityError(err)
	}
	return reliabilityRuleDTO(updatedBinding), nil
}

func (a *productionOntologyReliabilityAPI) RequestRepairAuthorization(ctx context.Context, principal identity.Principal, tenantID, incidentID, ruleID string, request httptransport.OntologyReliabilityAuthorizationRequest) (httptransport.OntologyReliabilityAuthorizationDTO, error) {
	if err := a.authorize(ctx, principal, tenantID, true); err != nil {
		return httptransport.OntologyReliabilityAuthorizationDTO{}, err
	}
	mode, err := reliabilityExecutionMode(request.ExecutionMode)
	if err != nil || strings.TrimSpace(request.IdempotencyKey) == "" || len(strings.TrimSpace(request.Purpose)) > 1024 {
		return httptransport.OntologyReliabilityAuthorizationDTO{}, ontologycontract.ErrInvalidRequest
	}
	material, err := a.loadMaterial(ctx, tenantID, incidentID)
	if err != nil {
		return httptransport.OntologyReliabilityAuthorizationDTO{}, ontologyReliabilityError(err)
	}
	binding, err := a.bindingForRule(material, ruleID)
	if err != nil || mode == actioncontract.ExecutionModeDryRun && !binding.dryRunOK || mode == actioncontract.ExecutionModeApply && len(binding.blockers) > 0 {
		return httptransport.OntologyReliabilityAuthorizationDTO{}, ontologycontract.ErrInvalidRequest
	}
	actor, err := reliabilityHILActor(principal)
	if err != nil {
		return httptransport.OntologyReliabilityAuthorizationDTO{}, ontologycontract.ErrInvalidRequest
	}
	purpose := strings.TrimSpace(request.Purpose)
	if purpose == "" {
		purpose = "Validate exact repair preconditions"
		if mode == actioncontract.ExecutionModeApply {
			purpose = "Execute exact governed repair"
		}
	}
	refs := reliabilityActionObjectRefs(incidentID, ruleID)
	result, err := a.decisions.CreateDecision(ctx, hil.CreateDecisionInput{
		TenantID: tenantID, Requester: actor, Action: productionOntologyActionDecisionPrefix + string(mode),
		ActionTypeID: string(productionRepairActionTypeID), Resource: refs[0], ObjectRefs: refs,
		ArgsHash: binding.argsHash, Purpose: purpose, RiskLevel: hil.RiskHigh, PreferredMode: hil.ModeApproval,
		ApprovalRequired: true, SubjectRefs: refs, IdempotencyKey: request.IdempotencyKey,
		PayloadPreview: map[string]any{
			"source": "ontology_reliability_workspace", "approval_assigned_to": actor.ID,
			"incident_id": incidentID, "rule_id": ruleID, "execution_mode": string(mode),
			"strategy": string(binding.rule.Strategy), "target_run_id": reliabilityTargetRunID(material.target),
		},
		ExpiresAt: a.nowUTC().Add(30 * time.Minute),
	})
	if err != nil {
		return httptransport.OntologyReliabilityAuthorizationDTO{}, ontologyReliabilityError(err)
	}
	return reliabilityAuthorizationDTO(result.Decision, ruleID, true), nil
}

func (a *productionOntologyReliabilityAPI) ResolveRepairAuthorization(ctx context.Context, principal identity.Principal, tenantID, incidentID, ruleID, decisionID string, request httptransport.OntologyReliabilityAuthorizationDecisionRequest) (httptransport.OntologyReliabilityAuthorizationResolutionDTO, error) {
	if err := a.authorize(ctx, principal, tenantID, true); err != nil {
		return httptransport.OntologyReliabilityAuthorizationResolutionDTO{}, err
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return httptransport.OntologyReliabilityAuthorizationResolutionDTO{}, ontologycontract.ErrInvalidRequest
	}
	_, _, current, err := a.currentDecision(ctx, tenantID, incidentID, ruleID, decisionID)
	if err != nil {
		return httptransport.OntologyReliabilityAuthorizationResolutionDTO{}, ontologyReliabilityError(err)
	}
	if !current {
		return httptransport.OntologyReliabilityAuthorizationResolutionDTO{}, ontologycontract.ErrStateConflict
	}
	actor, err := reliabilityHILActor(principal)
	if err != nil {
		return httptransport.OntologyReliabilityAuthorizationResolutionDTO{}, ontologycontract.ErrInvalidRequest
	}
	resolution, err := a.decisions.ResolveDecision(ctx, tenantID, decisionID, actor, hil.ResolveDecisionInput{
		Decision: request.Decision, Reason: strings.TrimSpace(request.Reason), IdempotencyKey: request.IdempotencyKey,
	})
	if err != nil {
		return httptransport.OntologyReliabilityAuthorizationResolutionDTO{}, ontologyReliabilityError(err)
	}
	return httptransport.OntologyReliabilityAuthorizationResolutionDTO{
		Authorization: reliabilityAuthorizationDTO(resolution.Decision, ruleID, true), Effect: string(resolution.Receipt.Effect),
	}, nil
}

func (a *productionOntologyReliabilityAPI) InvokeAuthorizedRepair(ctx context.Context, principal identity.Principal, tenantID, incidentID, ruleID, decisionID string, request httptransport.OntologyReliabilityInvokeRequest) (httptransport.OntologyActionRunDTO, error) {
	if err := a.authorize(ctx, principal, tenantID, true); err != nil {
		return httptransport.OntologyActionRunDTO{}, err
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" || a.actions == nil {
		return httptransport.OntologyActionRunDTO{}, ontologycontract.ErrInvalidRequest
	}
	material, decision, current, err := a.currentDecision(ctx, tenantID, incidentID, ruleID, decisionID)
	if err != nil {
		return httptransport.OntologyActionRunDTO{}, ontologyReliabilityError(err)
	}
	if !current {
		return httptransport.OntologyActionRunDTO{}, ontologycontract.ErrStateConflict
	}
	if existing, found, findErr := a.actionRunForDecision(ctx, tenantID, decisionID); findErr != nil {
		return httptransport.OntologyActionRunDTO{}, ontologyReliabilityError(findErr)
	} else if found {
		return ontologyActionRunDTO(existing), nil
	}
	actor, err := reliabilityHILActor(principal)
	if err != nil {
		return httptransport.OntologyActionRunDTO{}, ontologycontract.ErrInvalidRequest
	}
	receipt, err := a.decisions.GetActionDecisionReceipt(ctx, tenantID, decisionID, actor)
	if err != nil {
		return httptransport.OntologyActionRunDTO{}, ontologyReliabilityError(err)
	}
	mode, err := reliabilityExecutionModeFromAction(decision.Action)
	if err != nil {
		return httptransport.OntologyActionRunDTO{}, ontologycontract.ErrStateConflict
	}
	binding, err := a.bindingForRule(material, ruleID)
	if err != nil {
		return httptransport.OntologyActionRunDTO{}, ontologyReliabilityError(err)
	}
	evidence := []httptransport.OntologyActionRefDTO{}
	if mode == actioncontract.ExecutionModeApply {
		if binding.preflight == nil || len(binding.blockers) > 0 {
			return httptransport.OntologyActionRunDTO{}, ontologycontract.ErrStateConflict
		}
		evidence = append(evidence, httptransport.OntologyActionRefDTO{Kind: "evidence_item", ID: binding.preflight.EvidenceID})
	}
	result, err := a.actions.InvokeAction(ctx, principal, string(productionRepairActionTypeID), httptransport.OntologyActionInvokeRequest{
		TenantID: tenantID, ExecutionMode: string(mode), RuntimeKind: "system", RuntimeID: "reliability-control-plane",
		Purpose: decision.Purpose, ArgsHash: decision.ArgsHash, ApprovalID: decision.ID, HILTokenID: receipt.ID,
		ObjectRefs:   []httptransport.OntologyActionRefDTO{{Kind: "failure_incident", ID: incidentID}, {Kind: "repair_rule", ID: ruleID}},
		EvidenceRefs: evidence,
	})
	if err != nil {
		if existing, found, _ := a.actionRunForDecision(ctx, tenantID, decisionID); found {
			return ontologyActionRunDTO(existing), nil
		}
		return httptransport.OntologyActionRunDTO{}, ontologyReliabilityError(err)
	}
	return result, nil
}
