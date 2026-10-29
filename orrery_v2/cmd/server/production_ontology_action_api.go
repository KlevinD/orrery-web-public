package main

import (
	"context"
	"errors"
	"strings"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyreasoning"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	"orrery_v2/internal/app/reliability"
	httptransport "orrery_v2/internal/transport/http"
)

type ontologyActionIDGenerator interface {
	NewIDContext() (string, error)
}

type ontologyActionAuthorizer interface {
	Can(context.Context, identity.Principal, string, identity.ObjectRef) error
}

type productionOntologyActionAPI struct {
	repository actioncontract.Repository
	service    *actioncontract.Service
	authorizer ontologyActionAuthorizer
	ids        ontologyActionIDGenerator
	executions *productionOntologyExecutionAPI
	repairs    ontologyRepairExecutor
}

func newProductionOntologyActionAPI(repository actioncontract.Repository, service *actioncontract.Service, authorizer ontologyActionAuthorizer, ids ontologyActionIDGenerator, executions *productionOntologyExecutionAPI, repairs ontologyRepairExecutor) *productionOntologyActionAPI {
	return &productionOntologyActionAPI{repository: repository, service: service, authorizer: authorizer, ids: ids, executions: executions, repairs: repairs}
}

func (a *productionOntologyActionAPI) InvokeAction(ctx context.Context, principal identity.Principal, actionTypeID string, request httptransport.OntologyActionInvokeRequest) (httptransport.OntologyActionRunDTO, error) {
	if strings.TrimSpace(actionTypeID) == ontologyreasoning.DerivedLinkActionID {
		return httptransport.OntologyActionRunDTO{}, ontologycontract.ErrInvalidRequest
	}
	if a == nil || a.repository == nil || a.service == nil || a.authorizer == nil || a.ids == nil || a.executions == nil || a.executions.context == nil {
		return httptransport.OntologyActionRunDTO{}, ontologyscope.ErrDependencyUnavailable
	}
	principal = identity.NormalizePrincipal(principal)
	if !principal.Authenticated() || strings.TrimSpace(request.TenantID) == "" || len(request.ObjectRefs) == 0 || len(request.ObjectRefs) > 100 ||
		len(request.EvidenceRefs) > 100 || len(request.RequestedFields) > 100 || len(strings.TrimSpace(request.Purpose)) > 1024 ||
		strings.TrimSpace(request.ArgsHash) == "" || len(strings.TrimSpace(request.ArgsHash)) > 256 {
		return httptransport.OntologyActionRunDTO{}, ontologycontract.ErrInvalidRequest
	}
	actionType, err := a.repository.GetActionType(ctx, actioncontract.ActionTypeID(strings.TrimSpace(actionTypeID)))
	if err != nil {
		return httptransport.OntologyActionRunDTO{}, ontologyActionError(err)
	}
	approvalID := strings.TrimSpace(request.ApprovalID)
	hilTokenID := strings.TrimSpace(request.HILTokenID)
	if len(approvalID) > 256 || len(hilTokenID) > 256 {
		return httptransport.OntologyActionRunDTO{}, ontologycontract.ErrInvalidRequest
	}
	highRisk := actionType.RiskLevel == actioncontract.RiskHigh || actionType.RiskLevel == actioncontract.RiskCritical
	if highRisk && (approvalID == "" || hilTokenID == "") {
		return httptransport.OntologyActionRunDTO{}, ontologycontract.ErrInvalidRequest
	}
	if !highRisk && (approvalID != "" || hilTokenID != "") {
		return httptransport.OntologyActionRunDTO{}, ontologycontract.ErrInvalidRequest
	}
	executionMode, err := productionActionExecutionMode(request.ExecutionMode)
	if err != nil {
		return httptransport.OntologyActionRunDTO{}, ontologycontract.ErrInvalidRequest
	}
	if actionType.ID == productionRepairActionTypeID {
		if a.repairs == nil {
			return httptransport.OntologyActionRunDTO{}, ontologyscope.ErrDependencyUnavailable
		}
	}
	objectRefs := make([]actioncontract.ObjectRef, 0, len(request.ObjectRefs))
	for _, ref := range request.ObjectRefs {
		kind, id := strings.TrimSpace(ref.Kind), strings.TrimSpace(ref.ID)
		if kind == "" || id == "" || len(kind) > 256 || len(id) > 256 {
			return httptransport.OntologyActionRunDTO{}, ontologycontract.ErrInvalidRequest
		}
		if _, supported := productionActionTargetEventTypes[kind]; !supported {
			return httptransport.OntologyActionRunDTO{}, ontologycontract.ErrInvalidRequest
		}
		if err := a.authorizer.Can(ctx, principal, actionType.PermissionScope, identity.ObjectRef{Type: kind, ID: id, TenantID: request.TenantID}); err != nil {
			return httptransport.OntologyActionRunDTO{}, ontologyActionError(err)
		}
		detail, err := a.executions.context.GetObject(ctx, principal, request.TenantID, id)
		if err != nil {
			return httptransport.OntologyActionRunDTO{}, err
		}
		if detail.ObjectType != kind {
			return httptransport.OntologyActionRunDTO{}, ontologycontract.ErrUnavailable
		}
		objectRefs = append(objectRefs, actioncontract.ObjectRef{Kind: kind, ID: id})
	}
	runtime := actioncontract.RuntimeRef{Kind: actioncontract.RuntimeKind(strings.TrimSpace(request.RuntimeKind)), ID: strings.TrimSpace(request.RuntimeID)}
	if !supportedOntologyActionRuntime(runtime.Kind) || runtime.ID == "" || len(runtime.ID) > 256 {
		return httptransport.OntologyActionRunDTO{}, ontologycontract.ErrInvalidRequest
	}
	if runtime.Kind == actioncontract.RuntimeAgent {
		if a.executions == nil {
			return httptransport.OntologyActionRunDTO{}, ontologyscope.ErrDependencyUnavailable
		}
		if _, err := a.executions.GetRun(ctx, principal, request.TenantID, runtime.ID); err != nil {
			return httptransport.OntologyActionRunDTO{}, err
		}
	}
	requestedFields := make([]string, 0, len(request.RequestedFields))
	for _, field := range request.RequestedFields {
		field = strings.TrimSpace(field)
		if field == "" || len(field) > 256 {
			return httptransport.OntologyActionRunDTO{}, ontologycontract.ErrInvalidRequest
		}
		requestedFields = append(requestedFields, field)
	}
	generated, err := a.ids.NewIDContext()
	if err != nil {
		return httptransport.OntologyActionRunDTO{}, err
	}
	policyDecisionID := "policy-decision:" + generated
	if highRisk {
		policyDecisionID = approvalID
	}
	evidence := make([]actioncontract.Ref, 0, len(request.EvidenceRefs))
	for _, ref := range request.EvidenceRefs {
		kind, id := strings.TrimSpace(ref.Kind), strings.TrimSpace(ref.ID)
		if kind != "evidence_item" || id == "" || len(id) > 256 {
			return httptransport.OntologyActionRunDTO{}, ontologycontract.ErrInvalidRequest
		}
		detail, err := a.executions.context.GetObject(ctx, principal, request.TenantID, id)
		if err != nil {
			return httptransport.OntologyActionRunDTO{}, err
		}
		if detail.ObjectType != "evidence_item" {
			return httptransport.OntologyActionRunDTO{}, ontologycontract.ErrUnavailable
		}
		evidence = append(evidence, actioncontract.Ref{Kind: kind, ID: id})
	}
	if actionType.ID == productionRepairActionTypeID {
		if err := a.repairs.Validate(ctx, repairInvocation{
			TenantID: request.TenantID, ExecutionMode: executionMode, ArgsHash: strings.TrimSpace(request.ArgsHash),
			ObjectRefs: objectRefs, EvidenceRefs: evidence,
		}); err != nil {
			return httptransport.OntologyActionRunDTO{}, ontologyActionError(err)
		}
	}
	actorKind := actioncontract.SubjectHuman
	actorID := firstNonBlank(principal.UserID, principal.AgentProfileID, principal.Username, principal.Subject)
	effectiveUser := actioncontract.ActionSubject{Kind: actioncontract.SubjectHuman, ID: actorID, Name: principal.DisplayName}
	switch principal.ViewerKind {
	case identity.ViewerKindAgent:
		actorKind = actioncontract.SubjectAgent
		actorID = firstNonBlank(principal.AgentProfileID, principal.Subject, principal.UserID, principal.Username)
		effectiveUser = actioncontract.ActionSubject{}
	case identity.ViewerKindSystem:
		actorKind = actioncontract.SubjectService
		actorID = firstNonBlank(principal.Subject, principal.UserID, principal.Username)
		effectiveUser = actioncontract.ActionSubject{}
	}
	run, err := a.service.StartActionRun(ctx, actioncontract.StartRunRequest{
		RunID: actioncontract.ActionRunID("action-run:" + generated), TenantID: request.TenantID, ActionTypeID: actionType.ID,
		Actor:         actioncontract.ActionSubject{Kind: actorKind, ID: actorID, Name: principal.DisplayName},
		EffectiveUser: effectiveUser,
		Runtime:       runtime, Purpose: strings.TrimSpace(request.Purpose), ObjectRefs: objectRefs,
		RequestedFields: requestedFields, ArgsHash: strings.TrimSpace(request.ArgsHash),
		ExecutionMode:    executionMode,
		Approval:         actioncontract.Ref{Kind: "approval", ID: approvalID},
		PolicyDecisionID: policyDecisionID, HILTokenID: hilTokenID, EvidenceRefs: evidence,
	})
	if err != nil {
		return httptransport.OntologyActionRunDTO{}, ontologyActionError(err)
	}
	if actionType.ID == productionRepairActionTypeID {
		run, err = a.repairs.Execute(ctx, run)
		if err != nil {
			return httptransport.OntologyActionRunDTO{}, ontologyActionError(err)
		}
	}
	return ontologyActionRunDTO(run), nil
}

func supportedOntologyActionRuntime(kind actioncontract.RuntimeKind) bool {
	switch kind {
	case actioncontract.RuntimeWebApp, actioncontract.RuntimeAgent, actioncontract.RuntimeMCPGateway,
		actioncontract.RuntimeIRIS, actioncontract.RuntimeWorker, actioncontract.RuntimeSystem:
		return true
	default:
		return false
	}
}

func ontologyActionRunDTO(run actioncontract.ActionRun) httptransport.OntologyActionRunDTO {
	objects := make([]httptransport.OntologyActionRefDTO, 0, len(run.ObjectRefs))
	for _, ref := range run.ObjectRefs {
		objects = append(objects, httptransport.OntologyActionRefDTO{Kind: ref.Kind, ID: ref.ID})
	}
	return httptransport.OntologyActionRunDTO{
		ID: string(run.ID), ActionTypeID: string(run.ActionTypeID), Status: string(run.Status), RiskLevel: string(run.RiskLevel),
		ExecutionMode: string(run.ExecutionMode),
		Runtime:       httptransport.OntologyActionRefDTO{Kind: string(run.Runtime.Kind), ID: run.Runtime.ID}, ObjectRefs: objects,
		PolicyDecisionID: run.PolicyDecisionID, ApprovalID: run.Approval.ID, ErrorCode: run.ErrorCode,
		CreatedAt: run.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
	}
}

func ontologyActionError(err error) error {
	switch {
	case errors.Is(err, authorization.ErrDenied):
		return ontologyscope.ErrAccessDenied
	case errors.Is(err, actioncontract.ErrNotFound):
		return ontologycontract.ErrUnavailable
	case errors.Is(err, actioncontract.ErrInvalidRequest), errors.Is(err, authorization.ErrInvalidRequest), errors.Is(err, reliability.ErrInvalidRequest):
		return ontologycontract.ErrInvalidRequest
	case errors.Is(err, reliability.ErrConflict):
		return ontologycontract.ErrStateConflict
	case errors.Is(err, reliability.ErrNotFound):
		return ontologycontract.ErrUnavailable
	default:
		return err
	}
}
