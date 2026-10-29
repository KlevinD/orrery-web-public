package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	contextview "orrery_v2/internal/app/ontologyview/context"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	"orrery_v2/internal/app/reliability"
	"orrery_v2/internal/platform/logging"
	httptransport "orrery_v2/internal/transport/http"
)

const reliabilityWorkspaceHistoryLimit = 25

type productionOntologyReliabilityAPI struct {
	repository reliability.WorkspaceRepository
	control    *reliability.Service
	context    ontologyExecutionContext
	gate       *ontologyscope.ScopeAccessGate
	decisions  reliabilityDecisionService
	actions    *productionOntologyActionAPI
	now        func() time.Time
	capture    *reliability.CaptureInbox
}

type reliabilityDecisionService interface {
	CreateDecision(context.Context, hil.CreateDecisionInput) (hil.CreateDecisionResult, error)
	ListDecisions(context.Context, hil.DecisionListQuery) (hil.DecisionPage, error)
	GetDecision(context.Context, string, string) (hil.DecisionRequest, error)
	ResolveDecision(context.Context, string, string, hil.ActorRef, hil.ResolveDecisionInput) (hil.DecisionResolution, error)
	GetActionDecisionReceipt(context.Context, string, string, hil.ActorRef) (hil.DecisionReceipt, error)
}

type reliabilityWorkspaceMaterial struct {
	incident   reliability.FailureIncident
	target     *reliability.RepairTarget
	diagnoses  []reliability.DiagnosisCase
	rules      []reliability.RepairRule
	preflights []reliability.RepairPreflight
	executions []reliability.RepairExecution
}

type reliabilityRuleBinding struct {
	rule                reliability.RepairRule
	argsHash            string
	preflight           *reliability.RepairPreflight
	dryRunOK            bool
	blockers            []string
	activationArgsHash  string
	activationReady     bool
	activationBlockers  []string
	activationDiagnosis *reliability.DiagnosisCase
}

func newProductionOntologyReliabilityAPI(repository reliability.WorkspaceRepository, control *reliability.Service, contextAPI ontologyExecutionContext, gate *ontologyscope.ScopeAccessGate, decisions reliabilityDecisionService, actions *productionOntologyActionAPI, now func() time.Time) *productionOntologyReliabilityAPI {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &productionOntologyReliabilityAPI{repository: repository, control: control, context: contextAPI, gate: gate, decisions: decisions, actions: actions, now: now}
}

func (a *productionOntologyReliabilityAPI) GetIncident(ctx context.Context, principal identity.Principal, tenantID, incidentID string) (httptransport.OntologyReliabilityIncidentDetailDTO, error) {
	if err := a.authorize(ctx, principal, tenantID, false); err != nil {
		return httptransport.OntologyReliabilityIncidentDetailDTO{}, err
	}
	material, err := a.loadMaterial(ctx, tenantID, incidentID)
	if err != nil {
		return httptransport.OntologyReliabilityIncidentDetailDTO{}, ontologyReliabilityError(err)
	}
	decisions, err := a.decisions.ListDecisions(ctx, hil.DecisionListQuery{TenantID: tenantID, Limit: 100})
	if err != nil {
		return httptransport.OntologyReliabilityIncidentDetailDTO{}, ontologyReliabilityError(err)
	}
	evidence, err := a.availableEvidence(ctx, principal, tenantID, incidentID)
	if err != nil {
		return httptransport.OntologyReliabilityIncidentDetailDTO{}, ontologyReliabilityError(err)
	}
	detail := a.detailDTO(material, evidence, decisions.Items)
	if a.capture != nil {
		if _, accessErr := a.gate.AuthorizeManage(ctx, identity.NormalizePrincipal(principal), strings.TrimSpace(tenantID)); accessErr != nil {
			detail.DiagnosticsWarning = "manage_access_required"
			return detail, nil
		}
		repair, repairErr := a.capture.AutomaticRepair(tenantID, incidentID)
		if repairErr != nil {
			detail.DiagnosticsWarning = "unavailable"
		} else if repair != nil {
			detail.AutomaticRepair = &httptransport.OntologyReliabilityAutomaticRepairDTO{
				JobID: repair.JobID, Status: repair.Status, Summary: repair.Summary, Evidence: repair.Evidence,
				RepairRunID: repair.RepairRunID, UpdatedAt: repair.UpdatedAt.UTC().Format(time.RFC3339Nano),
				Verified: repair.Verified && repair.CurrentResult(material.incident), Current: repair.CurrentResult(material.incident),
			}
		}
		var proof *reliability.DependencyRecovery
		var proofErr error
		if reader, ok := a.repository.(interface {
			DependencyRecovery(context.Context, reliability.FailureIncident) (*reliability.DependencyRecovery, error)
		}); ok {
			proof, proofErr = reader.DependencyRecovery(ctx, material.incident)
		}
		if proofErr != nil {
			detail.DiagnosticsWarning = "unavailable"
		}
		if proof != nil {
			encoded, _ := json.Marshal(proof)
			detail.Diagnostics = append(detail.Diagnostics, httptransport.OntologyReliabilityDiagnosticDTO{
				ID: proof.Key(), Source: "dependency-recovery-verifier", OccurredAt: proof.VerifiedAt.UTC().Format(time.RFC3339Nano),
				Details: map[string]string{"dependency_recovery": string(encoded), "scope": "infrastructure recovery; business completion and channel delivery require their own receipts"},
			})
		}
		records, captureErr := a.capture.Diagnostics(tenantID, incidentID)
		if captureErr != nil {
			detail.DiagnosticsWarning = "unavailable"
		} else {
			for _, record := range records {
				detail.Diagnostics = append(detail.Diagnostics, httptransport.OntologyReliabilityDiagnosticDTO{
					ID: record.Observation.ID, Source: record.Observation.Source,
					OccurredAt:    record.Observation.OccurredAt.UTC().Format(time.RFC3339Nano),
					CorrelationID: logging.RedactDiagnostic(record.Observation.CorrelationID, 128), Details: record.Diagnostics,
				})
			}
		}
	}
	return detail, nil
}

func (a *productionOntologyReliabilityAPI) CreateDiagnosis(ctx context.Context, principal identity.Principal, tenantID, incidentID string, request httptransport.OntologyReliabilityDiagnosisCreateRequest) (httptransport.OntologyReliabilityDiagnosisDTO, error) {
	if err := a.authorize(ctx, principal, tenantID, true); err != nil {
		return httptransport.OntologyReliabilityDiagnosisDTO{}, err
	}
	if a.control == nil || a.context == nil || strings.TrimSpace(request.IdempotencyKey) == "" {
		return httptransport.OntologyReliabilityDiagnosisDTO{}, ontologycontract.ErrInvalidRequest
	}
	evidence, err := a.availableEvidence(ctx, principal, tenantID, incidentID)
	if err != nil {
		return httptransport.OntologyReliabilityDiagnosisDTO{}, ontologyReliabilityError(err)
	}
	visible := make(map[string]struct{}, len(evidence))
	for _, item := range evidence {
		visible[item.ID] = struct{}{}
	}
	for _, evidenceID := range request.EvidenceIDs {
		if _, ok := visible[strings.TrimSpace(evidenceID)]; !ok {
			return httptransport.OntologyReliabilityDiagnosisDTO{}, ontologycontract.ErrInvalidRequest
		}
	}
	actor, err := reliabilityDomainActor(principal)
	if err != nil {
		return httptransport.OntologyReliabilityDiagnosisDTO{}, ontologycontract.ErrInvalidRequest
	}
	diagnosis, err := a.control.CreateDiagnosisCase(ctx, reliability.DiagnosisCaseCommand{
		ID: request.IdempotencyKey, Actor: actor,
		Diagnosis: reliability.DiagnosisCase{
			TenantID: tenantID, IncidentID: incidentID, RootCauseCategory: reliability.FailureCategory(request.RootCauseCategory),
			RedactedSummary: request.RedactedSummary, Confidence: request.Confidence, EvidenceIDs: append([]string(nil), request.EvidenceIDs...),
		},
	})
	if err != nil {
		return httptransport.OntologyReliabilityDiagnosisDTO{}, ontologyReliabilityError(err)
	}
	return reliabilityDiagnosisDTO(diagnosis), nil
}

func (a *productionOntologyReliabilityAPI) DecideDiagnosis(ctx context.Context, principal identity.Principal, tenantID, incidentID, diagnosisID string, request httptransport.OntologyReliabilityDiagnosisDecisionRequest) (httptransport.OntologyReliabilityDiagnosisDTO, error) {
	if err := a.authorize(ctx, principal, tenantID, true); err != nil {
		return httptransport.OntologyReliabilityDiagnosisDTO{}, err
	}
	if a.control == nil || strings.TrimSpace(request.IdempotencyKey) == "" {
		return httptransport.OntologyReliabilityDiagnosisDTO{}, ontologycontract.ErrInvalidRequest
	}
	if _, err := a.diagnosisForIncident(ctx, tenantID, incidentID, diagnosisID); err != nil {
		return httptransport.OntologyReliabilityDiagnosisDTO{}, ontologyReliabilityError(err)
	}
	actor, err := reliabilityDomainActor(principal)
	if err != nil {
		return httptransport.OntologyReliabilityDiagnosisDTO{}, ontologycontract.ErrInvalidRequest
	}
	if actor.Kind != "human" {
		return httptransport.OntologyReliabilityDiagnosisDTO{}, ontologyscope.ErrAccessDenied
	}
	diagnosis, err := a.control.DecideDiagnosisCase(ctx, reliability.DiagnosisDecisionCommand{
		ID: request.IdempotencyKey, TenantID: tenantID, DiagnosisCaseID: diagnosisID,
		Decision: reliability.DiagnosisDecision(request.Decision), Reason: request.Reason, Actor: actor,
	})
	if err != nil {
		return httptransport.OntologyReliabilityDiagnosisDTO{}, ontologyReliabilityError(err)
	}
	return reliabilityDiagnosisDTO(diagnosis), nil
}

func (a *productionOntologyReliabilityAPI) ProposeRepairRule(ctx context.Context, principal identity.Principal, tenantID, incidentID, diagnosisID string, request httptransport.OntologyReliabilityRuleProposalRequest) (httptransport.OntologyReliabilityRepairRuleDTO, error) {
	if err := a.authorize(ctx, principal, tenantID, true); err != nil {
		return httptransport.OntologyReliabilityRepairRuleDTO{}, err
	}
	if a.control == nil || strings.TrimSpace(request.IdempotencyKey) == "" {
		return httptransport.OntologyReliabilityRepairRuleDTO{}, ontologycontract.ErrInvalidRequest
	}
	if _, err := a.diagnosisForIncident(ctx, tenantID, incidentID, diagnosisID); err != nil {
		return httptransport.OntologyReliabilityRepairRuleDTO{}, ontologyReliabilityError(err)
	}
	actor, err := reliabilityDomainActor(principal)
	if err != nil {
		return httptransport.OntologyReliabilityRepairRuleDTO{}, ontologycontract.ErrInvalidRequest
	}
	strategy := reliability.RepairStrategy(strings.ToLower(strings.TrimSpace(request.Strategy)))
	rule, err := a.control.ProposeRepairRuleFromDiagnosis(ctx, reliability.RepairRuleCommand{
		ID: request.IdempotencyKey, Actor: actor,
		Rule: reliability.RepairRule{
			TenantID: tenantID, DiagnosisCaseID: diagnosisID, Strategy: strategy,
			ActionTypeID: string(productionRepairActionTypeID), RiskLevel: reliabilityProposalRisk(strategy),
			Reversible: request.Reversible, Idempotent: request.Idempotent,
		},
	})
	if err != nil {
		return httptransport.OntologyReliabilityRepairRuleDTO{}, ontologyReliabilityError(err)
	}
	material, err := a.loadMaterial(ctx, tenantID, incidentID)
	if err != nil {
		return httptransport.OntologyReliabilityRepairRuleDTO{}, ontologyReliabilityError(err)
	}
	binding, err := a.bindingForRule(material, rule.ID)
	if err != nil {
		return httptransport.OntologyReliabilityRepairRuleDTO{}, ontologyReliabilityError(err)
	}
	return reliabilityRuleDTO(binding), nil
}

func (a *productionOntologyReliabilityAPI) authorize(ctx context.Context, principal identity.Principal, tenantID string, manage bool) error {
	if a == nil || a.repository == nil || a.gate == nil || a.decisions == nil || strings.TrimSpace(tenantID) == "" {
		return ontologyscope.ErrDependencyUnavailable
	}
	var err error
	if manage {
		_, err = a.gate.AuthorizeManage(ctx, identity.NormalizePrincipal(principal), strings.TrimSpace(tenantID))
	} else {
		_, err = a.gate.AuthorizeRead(ctx, identity.NormalizePrincipal(principal), strings.TrimSpace(tenantID))
	}
	if !manage && ontologyscope.IsNonDisclosingReadError(err) {
		return ontologycontract.ErrUnavailable
	}
	return err
}

func (a *productionOntologyReliabilityAPI) loadMaterial(ctx context.Context, tenantID, incidentID string) (reliabilityWorkspaceMaterial, error) {
	incident, err := a.repository.GetIncident(ctx, tenantID, incidentID)
	if err != nil {
		return reliabilityWorkspaceMaterial{}, err
	}
	material := reliabilityWorkspaceMaterial{incident: incident}
	if target, targetErr := a.repository.GetLatestRepairTarget(ctx, tenantID, incidentID); targetErr == nil {
		material.target = &target
	} else if !errors.Is(targetErr, reliability.ErrNotFound) {
		return reliabilityWorkspaceMaterial{}, targetErr
	}
	material.diagnoses, err = a.repository.ListDiagnosisCasesByIncident(ctx, tenantID, incidentID, reliabilityWorkspaceHistoryLimit)
	if err != nil {
		return reliabilityWorkspaceMaterial{}, err
	}
	material.rules, err = a.repository.ListRepairRules(ctx, tenantID, incident.Fingerprint)
	if err != nil {
		return reliabilityWorkspaceMaterial{}, err
	}
	material.preflights, err = a.repository.ListRepairPreflightsByIncident(ctx, tenantID, incidentID, reliabilityWorkspaceHistoryLimit)
	if err != nil {
		return reliabilityWorkspaceMaterial{}, err
	}
	material.executions, err = a.repository.ListRepairExecutionsByIncident(ctx, tenantID, incidentID, reliabilityWorkspaceHistoryLimit)
	return material, err
}

func (a *productionOntologyReliabilityAPI) availableEvidence(ctx context.Context, principal identity.Principal, tenantID, incidentID string) ([]httptransport.OntologyReliabilityEvidenceDTO, error) {
	if a == nil || a.context == nil {
		return []httptransport.OntologyReliabilityEvidenceDTO{}, nil
	}
	neighborhood, err := a.context.Neighborhood(ctx, principal, contextview.NeighborhoodQuery{
		TenantID: tenantID, RootID: incidentID, LinkTypes: []string{"evidence_item_supports_failure_incident"},
		Depth: 1, MaxNodes: 100, MaxEdges: 200,
	})
	if err != nil {
		return nil, err
	}
	evidence := make([]httptransport.OntologyReliabilityEvidenceDTO, 0, len(neighborhood.Nodes))
	for _, node := range neighborhood.Nodes {
		if node.ObjectType != "evidence_item" {
			continue
		}
		item := httptransport.OntologyReliabilityEvidenceDTO{
			ID: node.ID, Label: node.Label, Status: node.Status, Confidence: node.Confidence,
		}
		if node.UpdatedAt != nil {
			item.UpdatedAt = reliabilityTime(*node.UpdatedAt)
		}
		evidence = append(evidence, item)
	}
	sort.Slice(evidence, func(i, j int) bool { return evidence[i].ID < evidence[j].ID })
	return evidence, nil
}

func (a *productionOntologyReliabilityAPI) diagnosisForIncident(ctx context.Context, tenantID, incidentID, diagnosisID string) (reliability.DiagnosisCase, error) {
	diagnosis, err := a.repository.GetDiagnosisCase(ctx, tenantID, diagnosisID)
	if err != nil {
		return reliability.DiagnosisCase{}, err
	}
	if diagnosis.IncidentID != incidentID {
		return reliability.DiagnosisCase{}, reliability.ErrNotFound
	}
	return diagnosis, nil
}

func (a *productionOntologyReliabilityAPI) bindingForRule(material reliabilityWorkspaceMaterial, ruleID string) (reliabilityRuleBinding, error) {
	var rule reliability.RepairRule
	for _, candidate := range material.rules {
		if candidate.ID == ruleID {
			rule = candidate
			break
		}
	}
	if rule.ID == "" {
		return reliabilityRuleBinding{}, reliability.ErrNotFound
	}
	binding := reliabilityRuleBinding{rule: rule}
	binding.argsHash = repairInvocationArgsHash(material.incident, rule, material.target)
	binding.dryRunOK = rule.ActionTypeID == string(productionRepairActionTypeID) && rule.SelectorFingerprint == material.incident.Fingerprint &&
		(rule.Strategy != reliability.RepairStrategyRetry || currentRepairTarget(material.incident, material.target))
	for index := range material.preflights {
		candidate := material.preflights[index]
		if currentReliabilityPreflight(candidate, material.incident, rule, material.target, binding.argsHash, a.nowUTC()) {
			copy := candidate
			binding.preflight = &copy
			binding.argsHash = candidate.ArgsHash
			break
		}
	}
	binding.blockers = repairApplyBlockers(material, binding, a.nowUTC())
	for index := range material.diagnoses {
		if material.diagnoses[index].ID == rule.DiagnosisCaseID {
			copy := material.diagnoses[index]
			binding.activationDiagnosis = &copy
			break
		}
	}
	binding.activationBlockers = repairRuleActivationBlockers(rule, material.incident, binding.activationDiagnosis, binding.preflight, material.target, a.nowUTC())
	if binding.activationDiagnosis != nil && binding.preflight != nil {
		binding.activationArgsHash = repairRuleActivationArgsHash(rule, material.incident, *binding.activationDiagnosis, *binding.preflight)
	}
	binding.activationReady = len(binding.activationBlockers) == 0 && binding.activationArgsHash != ""
	return binding, nil
}

func (a *productionOntologyReliabilityAPI) activationMaterial(ctx context.Context, tenantID, ruleID string) (reliabilityWorkspaceMaterial, reliabilityRuleBinding, error) {
	rule, err := a.repository.GetRepairRule(ctx, tenantID, ruleID)
	if err != nil {
		return reliabilityWorkspaceMaterial{}, reliabilityRuleBinding{}, err
	}
	material, err := a.loadMaterial(ctx, tenantID, rule.SourceIncidentID)
	if err != nil {
		return reliabilityWorkspaceMaterial{}, reliabilityRuleBinding{}, err
	}
	binding, err := a.bindingForRule(material, ruleID)
	return material, binding, err
}

func (a *productionOntologyReliabilityAPI) currentActivationDecision(ctx context.Context, tenantID, ruleID, decisionID string) (reliabilityWorkspaceMaterial, reliabilityRuleBinding, hil.DecisionRequest, bool, error) {
	material, binding, err := a.activationMaterial(ctx, tenantID, ruleID)
	if err != nil {
		return reliabilityWorkspaceMaterial{}, reliabilityRuleBinding{}, hil.DecisionRequest{}, false, err
	}
	decision, err := a.decisions.GetDecision(ctx, tenantID, decisionID)
	if err != nil {
		return reliabilityWorkspaceMaterial{}, reliabilityRuleBinding{}, hil.DecisionRequest{}, false, err
	}
	current := binding.activationReady && decision.Action == productionRepairRuleActivationAction &&
		decision.ActionTypeID == productionRepairRuleActivationActionTypeID && decision.ArgsHash == binding.activationArgsHash &&
		binding.activationDiagnosis != nil && binding.preflight != nil &&
		reliabilityActivationDecisionTargets(decision, binding.rule, material.incident, *binding.activationDiagnosis, *binding.preflight)
	return material, binding, decision, current, nil
}

func (a *productionOntologyReliabilityAPI) currentDecision(ctx context.Context, tenantID, incidentID, ruleID, decisionID string) (reliabilityWorkspaceMaterial, hil.DecisionRequest, bool, error) {
	material, err := a.loadMaterial(ctx, tenantID, incidentID)
	if err != nil {
		return reliabilityWorkspaceMaterial{}, hil.DecisionRequest{}, false, err
	}
	decision, err := a.decisions.GetDecision(ctx, tenantID, decisionID)
	if err != nil {
		return reliabilityWorkspaceMaterial{}, hil.DecisionRequest{}, false, err
	}
	binding, err := a.bindingForRule(material, ruleID)
	if err != nil {
		return reliabilityWorkspaceMaterial{}, hil.DecisionRequest{}, false, err
	}
	mode, modeErr := reliabilityExecutionModeFromAction(decision.Action)
	current := modeErr == nil && decision.ActionTypeID == string(productionRepairActionTypeID) && decision.ArgsHash == binding.argsHash &&
		reliabilityDecisionTargets(decision, incidentID, ruleID) && (mode != actioncontract.ExecutionModeApply || len(binding.blockers) == 0)
	return material, decision, current, nil
}

func (a *productionOntologyReliabilityAPI) detailDTO(material reliabilityWorkspaceMaterial, evidence []httptransport.OntologyReliabilityEvidenceDTO, decisions []hil.DecisionRequest) httptransport.OntologyReliabilityIncidentDetailDTO {
	detail := httptransport.OntologyReliabilityIncidentDetailDTO{
		Incident: reliabilityIncidentDTO(material.incident), AvailableEvidence: evidence,
		Diagnoses: []httptransport.OntologyReliabilityDiagnosisDTO{}, Rules: []httptransport.OntologyReliabilityRepairRuleDTO{},
		Preflights: []httptransport.OntologyReliabilityPreflightDTO{}, Executions: []httptransport.OntologyReliabilityExecutionDTO{},
		ActivationReviews: []httptransport.OntologyReliabilityActivationReviewDTO{}, Authorizations: []httptransport.OntologyReliabilityAuthorizationDTO{},
	}
	if material.target != nil {
		target := reliabilityTargetDTO(*material.target)
		detail.Target = &target
	}
	for _, diagnosis := range material.diagnoses {
		detail.Diagnoses = append(detail.Diagnoses, reliabilityDiagnosisDTO(diagnosis))
	}
	bindings := make(map[string]reliabilityRuleBinding, len(material.rules))
	for _, rule := range material.rules {
		binding, _ := a.bindingForRule(material, rule.ID)
		bindings[rule.ID] = binding
		detail.Rules = append(detail.Rules, reliabilityRuleDTO(binding))
	}
	for _, preflight := range material.preflights {
		var rule reliability.RepairRule
		if binding, ok := bindings[preflight.RuleID]; ok {
			rule = binding.rule
		}
		detail.Preflights = append(detail.Preflights, reliabilityPreflightDTO(preflight, currentReliabilityPreflight(preflight, material.incident, rule, material.target, bindings[preflight.RuleID].argsHash, a.nowUTC())))
	}
	for _, execution := range material.executions {
		detail.Executions = append(detail.Executions, reliabilityExecutionDTO(execution))
	}
	for _, decision := range decisions {
		if decision.Action == productionRepairRuleActivationAction {
			ruleID, ok := reliabilityActivationDecisionRuleID(decision, material.incident.ID)
			binding, exists := bindings[ruleID]
			if !ok || !exists {
				continue
			}
			current := binding.activationReady && binding.activationDiagnosis != nil && binding.preflight != nil &&
				decision.ArgsHash == binding.activationArgsHash &&
				reliabilityActivationDecisionTargets(decision, binding.rule, material.incident, *binding.activationDiagnosis, *binding.preflight)
			detail.ActivationReviews = append(detail.ActivationReviews, reliabilityActivationReviewDTO(decision, binding, material.incident.ID, current))
			continue
		}
		ruleID, ok := reliabilityDecisionRuleID(decision, material.incident.ID)
		if !ok {
			continue
		}
		binding, exists := bindings[ruleID]
		if !exists {
			continue
		}
		mode, modeErr := reliabilityExecutionModeFromAction(decision.Action)
		current := modeErr == nil && decision.ArgsHash == binding.argsHash && (mode != actioncontract.ExecutionModeApply || len(binding.blockers) == 0)
		detail.Authorizations = append(detail.Authorizations, reliabilityAuthorizationDTO(decision, ruleID, current))
	}
	sort.Slice(detail.ActivationReviews, func(i, j int) bool {
		return detail.ActivationReviews[i].CreatedAt > detail.ActivationReviews[j].CreatedAt
	})
	sort.Slice(detail.Authorizations, func(i, j int) bool { return detail.Authorizations[i].CreatedAt > detail.Authorizations[j].CreatedAt })
	return detail
}

func (a *productionOntologyReliabilityAPI) actionRunForDecision(ctx context.Context, tenantID, decisionID string) (actioncontract.ActionRun, bool, error) {
	if a == nil || a.actions == nil || a.actions.repository == nil {
		return actioncontract.ActionRun{}, false, nil
	}
	for offset := 0; offset < 1000; offset += 100 {
		page, err := a.actions.repository.ListActionRuns(ctx, actioncontract.ActionRunQuery{
			TenantID: tenantID, ActionTypeID: productionRepairActionTypeID, Limit: 100, Offset: offset,
		})
		if err != nil {
			return actioncontract.ActionRun{}, false, err
		}
		for _, run := range page.Runs {
			if run.PolicyDecisionID == decisionID {
				return run, true, nil
			}
		}
		if offset+page.Page.Returned >= page.Page.Total || page.Page.Returned == 0 {
			break
		}
	}
	return actioncontract.ActionRun{}, false, nil
}

func repairApplyBlockers(material reliabilityWorkspaceMaterial, binding reliabilityRuleBinding, now time.Time) []string {
	var blockers []string
	rule, incident := binding.rule, material.incident
	if rule.ActionTypeID != string(productionRepairActionTypeID) || rule.SelectorFingerprint != incident.Fingerprint {
		blockers = append(blockers, "rule_binding_mismatch")
	}
	if rule.Status != reliability.RepairRuleStatusActive {
		blockers = append(blockers, "rule_not_active")
	}
	if rule.Strategy != reliability.RepairStrategyRetry {
		blockers = append(blockers, "strategy_not_executable")
	}
	if !rule.Reversible {
		blockers = append(blockers, "rule_not_reversible")
	}
	if !rule.Idempotent {
		blockers = append(blockers, "rule_not_idempotent")
	}
	if !incident.Retryable {
		blockers = append(blockers, "incident_not_retryable")
	}
	if rule.ExpiresAt != nil && !rule.ExpiresAt.After(now) {
		blockers = append(blockers, "rule_expired")
	}
	switch incident.Status {
	case reliability.IncidentStatusOpen, reliability.IncidentStatusContained, reliability.IncidentStatusDiagnosed,
		reliability.IncidentStatusEscalated, reliability.IncidentStatusQuarantined:
	default:
		blockers = append(blockers, "incident_state_ineligible")
	}
	if material.target == nil || material.target.Kind != reliability.RepairTargetMissionAgentRun || !material.target.OccurredAt.Equal(incident.LastSeenAt) {
		blockers = append(blockers, "exact_target_required")
	}
	if binding.preflight == nil {
		blockers = append(blockers, "current_preflight_required")
	}
	for _, execution := range material.executions {
		if execution.Status == reliability.RepairExecutionDispatching || execution.Status == reliability.RepairExecutionMonitoring {
			blockers = append(blockers, "execution_active")
			break
		}
	}
	return blockers
}

func currentReliabilityPreflight(preflight reliability.RepairPreflight, incident reliability.FailureIncident, rule reliability.RepairRule, target *reliability.RepairTarget, expectedArgsHash string, now time.Time) bool {
	if !preflight.Passed || !preflight.ExpiresAt.After(now) || preflight.IncidentID != incident.ID || preflight.IncidentVersion != incident.Version ||
		preflight.RuleID != rule.ID || preflight.RuleVersion != rule.Version || preflight.Strategy != rule.Strategy ||
		strings.TrimSpace(expectedArgsHash) == "" || preflight.ArgsHash != expectedArgsHash {
		return false
	}
	if rule.Strategy != reliability.RepairStrategyRetry {
		return true
	}
	return target != nil && preflight.TargetObservationID == target.ObservationID && preflight.TargetKind == target.Kind && preflight.TargetRunID == target.RunID
}

func reliabilityExecutionMode(value string) (actioncontract.ExecutionMode, error) {
	switch actioncontract.ExecutionMode(strings.ToLower(strings.TrimSpace(value))) {
	case actioncontract.ExecutionModeDryRun:
		return actioncontract.ExecutionModeDryRun, nil
	case actioncontract.ExecutionModeApply:
		return actioncontract.ExecutionModeApply, nil
	default:
		return "", fmt.Errorf("invalid repair execution mode")
	}
}

func reliabilityExecutionModeFromAction(action string) (actioncontract.ExecutionMode, error) {
	return reliabilityExecutionMode(strings.TrimPrefix(strings.TrimSpace(action), productionOntologyActionDecisionPrefix))
}

func reliabilityHILActor(principal identity.Principal) (hil.ActorRef, error) {
	principal = identity.NormalizePrincipal(principal)
	actor := hil.ActorRef{ID: firstNonBlank(principal.UserID, principal.AgentProfileID, principal.Username, principal.Subject)}
	switch principal.ViewerKind {
	case identity.ViewerKindUser:
		actor.Kind = hil.ActorHuman
	case identity.ViewerKindAgent:
		actor.Kind = hil.ActorAgent
	case identity.ViewerKindSystem:
		actor.Kind = hil.ActorService
	default:
		return hil.ActorRef{}, fmt.Errorf("unsupported reliability actor")
	}
	if actor.ID == "" {
		return hil.ActorRef{}, fmt.Errorf("reliability actor is required")
	}
	return actor, nil
}

func reliabilityDomainActor(principal identity.Principal) (reliability.ActorRef, error) {
	actor, err := reliabilityHILActor(principal)
	if err != nil {
		return reliability.ActorRef{}, err
	}
	return reliability.ActorRef{Kind: string(actor.Kind), ID: actor.ID}, nil
}

func reliabilityProposalRisk(strategy reliability.RepairStrategy) reliability.RiskLevel {
	switch strategy {
	case reliability.RepairStrategyReconfigure, reliability.RepairStrategyPatch,
		reliability.RepairStrategyPermissionChange, reliability.RepairStrategyDataMutation:
		return reliability.RiskCritical
	default:
		return reliability.RiskHigh
	}
}

func reliabilityActionObjectRefs(incidentID, ruleID string) []hil.ObjectRef {
	return []hil.ObjectRef{{Kind: "failure_incident", ID: incidentID}, {Kind: "repair_rule", ID: ruleID}}
}

func reliabilityDecisionTargets(decision hil.DecisionRequest, incidentID, ruleID string) bool {
	if decision.Resource != (hil.ObjectRef{Kind: "failure_incident", ID: incidentID}) || len(decision.ObjectRefs) != 2 {
		return false
	}
	wanted := reliabilityActionObjectRefs(incidentID, ruleID)
	for _, ref := range wanted {
		found := false
		for _, candidate := range decision.ObjectRefs {
			found = found || candidate == ref
		}
		if !found {
			return false
		}
	}
	return true
}

func reliabilityDecisionRuleID(decision hil.DecisionRequest, incidentID string) (string, bool) {
	if decision.ActionTypeID != string(productionRepairActionTypeID) || decision.Resource != (hil.ObjectRef{Kind: "failure_incident", ID: incidentID}) {
		return "", false
	}
	var ruleID string
	for _, ref := range decision.ObjectRefs {
		if ref.Kind == "repair_rule" {
			if ruleID != "" {
				return "", false
			}
			ruleID = ref.ID
		}
	}
	return ruleID, ruleID != "" && reliabilityDecisionTargets(decision, incidentID, ruleID)
}

func reliabilityActivationDecisionTargets(decision hil.DecisionRequest, rule reliability.RepairRule, incident reliability.FailureIncident, diagnosis reliability.DiagnosisCase, preflight reliability.RepairPreflight) bool {
	if decision.Resource != (hil.ObjectRef{Kind: "repair_rule", ID: rule.ID}) {
		return false
	}
	wanted := repairRuleActivationObjectRefs(rule, incident, diagnosis, preflight)
	if len(decision.ObjectRefs) != len(wanted) {
		return false
	}
	for _, ref := range wanted {
		found := false
		for _, candidate := range decision.ObjectRefs {
			found = found || candidate == ref
		}
		if !found {
			return false
		}
	}
	return true
}

func reliabilityActivationDecisionRuleID(decision hil.DecisionRequest, incidentID string) (string, bool) {
	if decision.Action != productionRepairRuleActivationAction || decision.ActionTypeID != productionRepairRuleActivationActionTypeID ||
		decision.Resource.Kind != "repair_rule" || strings.TrimSpace(decision.Resource.ID) == "" {
		return "", false
	}
	hasIncident := false
	for _, ref := range decision.ObjectRefs {
		hasIncident = hasIncident || ref == (hil.ObjectRef{Kind: "failure_incident", ID: incidentID})
	}
	return decision.Resource.ID, hasIncident
}

func (a *productionOntologyReliabilityAPI) nowUTC() time.Time {
	if a == nil || a.now == nil {
		return time.Now().UTC()
	}
	return a.now().UTC()
}
