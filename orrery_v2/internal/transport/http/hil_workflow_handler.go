package httptransport

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/app/hil"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

type saveHILWorkflowTemplateBody struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Definition  hil.WorkflowDefinition `json:"definition"`
}

type decideHILWorkflowTaskBody struct {
	Decision                hil.WorkflowTaskDecision `json:"decision"`
	Reason                  string                   `json:"reason,omitempty"`
	ExpectedInstanceVersion int64                    `json:"expected_instance_version"`
}

type transferHILWorkflowTaskBody struct {
	NewAssignee             hil.ActorRef `json:"new_assignee"`
	Reason                  string       `json:"reason"`
	ExpectedInstanceVersion int64        `json:"expected_instance_version"`
}

type publishHILWorkflowTemplateBody struct {
	Bindings []struct {
		ActionTypeID string        `json:"action_type_id"`
		ToolName     string        `json:"tool_name,omitempty"`
		RiskLevel    hil.RiskLevel `json:"risk_level,omitempty"`
		Active       bool          `json:"active"`
	} `json:"bindings,omitempty"`
}

type disableHILWorkflowTemplateBody struct {
	Reason string `json:"reason"`
}

type putHILWorkflowTemplateBindingBody struct {
	TemplateID string        `json:"template_id"`
	ToolName   string        `json:"tool_name,omitempty"`
	RiskLevel  hil.RiskLevel `json:"risk_level,omitempty"`
	Active     bool          `json:"active"`
	Reason     string        `json:"reason"`
}

type resubmitHILWorkflowBody struct {
	ArgsHash                string         `json:"args_hash"`
	MaterialHash            string         `json:"material_hash"`
	PayloadPreview          map[string]any `json:"payload_preview"`
	ExpiresAt               time.Time      `json:"expires_at"`
	ExpectedInstanceVersion int64          `json:"expected_instance_version"`
}

func (r *Router) handleCreateHILWorkflowTemplate(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.hilMutationViewer(w, req)
	if !ok {
		return
	}
	workflow, ok := r.hil.(HILWorkflowService)
	if !ok || workflow == nil {
		status, response := MapError(errServiceDependency(), cid)
		writeJSON(w, status, response)
		return
	}
	var body saveHILWorkflowTemplateBody
	if err := decodeStrictJSON(req, &body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	template, err := workflow.SaveWorkflowTemplateDraft(req.Context(), hilActorFromViewer(viewer), hil.SaveTemplateDraftInput{
		TenantID: viewer.Tenant(), Name: strings.TrimSpace(body.Name), Description: strings.TrimSpace(body.Description),
		Definition: body.Definition, IdempotencyKey: idempotencyKey(req),
	})
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusCreated, template)
}

func (r *Router) handleListHILWorkflowTemplates(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.hilViewer(w, req)
	if !ok {
		return
	}
	workflow, ok := r.hil.(HILWorkflowService)
	if !ok || workflow == nil {
		status, response := MapError(errServiceDependency(), cid)
		writeJSON(w, status, response)
		return
	}
	items, err := listReadableHILWorkflowTemplates(
		req.Context(), workflow, viewer.Tenant(), hil.WorkflowTemplateStatus(strings.TrimSpace(req.URL.Query().Get("status"))),
	)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (r *Router) handleGetHILWorkflowTemplate(w http.ResponseWriter, req *http.Request, templateID string) {
	cid := correlationID(req)
	viewer, workflow, ok := r.hilWorkflowViewer(w, req, false)
	if !ok {
		return
	}
	template, err := getReadableHILWorkflowTemplate(req.Context(), workflow, viewer.Tenant(), templateID)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, template)
}

func listReadableHILWorkflowTemplates(ctx context.Context, workflow HILWorkflowService, tenantID string, status hil.WorkflowTemplateStatus) ([]hil.WorkflowTemplateSummary, error) {
	items, err := workflow.ListWorkflowTemplates(ctx, hil.WorkflowTemplateListQuery{TenantID: tenantID, Status: status})
	if err != nil || tenantID == hil.BuiltInWorkflowTenantID {
		return items, err
	}
	shared, err := workflow.ListWorkflowTemplates(ctx, hil.WorkflowTemplateListQuery{TenantID: hil.BuiltInWorkflowTenantID, Status: status})
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		seen[item.ID] = struct{}{}
	}
	for _, item := range shared {
		if !hil.IsBuiltInWorkflowTemplateID(item.ID) {
			continue
		}
		if _, exists := seen[item.ID]; exists {
			continue
		}
		items = append(items, item)
	}
	return items, nil
}

func getReadableHILWorkflowTemplate(ctx context.Context, workflow HILWorkflowService, tenantID, templateID string) (hil.WorkflowTemplate, error) {
	template, err := workflow.GetWorkflowTemplate(ctx, tenantID, templateID)
	if err == nil || tenantID == hil.BuiltInWorkflowTenantID || !errors.Is(err, hil.ErrNotFound) || !hil.IsBuiltInWorkflowTemplateID(templateID) {
		return template, err
	}
	return workflow.GetWorkflowTemplate(ctx, hil.BuiltInWorkflowTenantID, templateID)
}

func (r *Router) handleUpdateHILWorkflowTemplateDraft(w http.ResponseWriter, req *http.Request, templateID string) {
	cid := correlationID(req)
	viewer, workflow, ok := r.hilWorkflowViewer(w, req, true)
	if !ok {
		return
	}
	current, err := workflow.GetWorkflowTemplate(req.Context(), viewer.Tenant(), templateID)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	var body saveHILWorkflowTemplateBody
	if err := decodeStrictJSON(req, &body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	template, err := workflow.SaveWorkflowTemplateDraft(req.Context(), hilActorFromViewer(viewer), hil.SaveTemplateDraftInput{
		ID: current.ID, TenantID: current.TenantID, Name: strings.TrimSpace(body.Name), Description: strings.TrimSpace(body.Description),
		Definition: body.Definition, Status: current.Status, LatestPublishedVersion: current.LatestPublishedVersion,
		IdempotencyKey: idempotencyKey(req),
	})
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, template)
}

func (r *Router) handlePublishHILWorkflowTemplate(w http.ResponseWriter, req *http.Request, templateID string) {
	cid := correlationID(req)
	viewer, workflow, ok := r.hilWorkflowViewer(w, req, true)
	if !ok {
		return
	}
	var body publishHILWorkflowTemplateBody
	if err := decodeStrictJSON(req, &body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	bindings := make([]hil.WorkflowTemplateBinding, 0, len(body.Bindings))
	for _, binding := range body.Bindings {
		bindings = append(bindings, hil.WorkflowTemplateBinding{
			TenantID: viewer.Tenant(), ActionTypeID: strings.TrimSpace(binding.ActionTypeID),
			ToolName: strings.TrimSpace(binding.ToolName), RiskLevel: binding.RiskLevel,
			TemplateID: templateID, Active: binding.Active,
		})
	}
	version, err := workflow.PublishWorkflowTemplate(req.Context(), hilActorFromViewer(viewer), viewer.Tenant(), templateID, bindings, idempotencyKey(req))
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, version)
}

func (r *Router) handleDisableHILWorkflowTemplate(w http.ResponseWriter, req *http.Request, templateID string) {
	cid := correlationID(req)
	viewer, workflow, ok := r.hilWorkflowViewer(w, req, true)
	if !ok {
		return
	}
	var body disableHILWorkflowTemplateBody
	if err := decodeStrictJSON(req, &body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	if err := workflow.DisableWorkflowTemplate(req.Context(), hilActorFromViewer(viewer), hil.DisableTemplateInput{
		TenantID: viewer.Tenant(), TemplateID: templateID, Reason: strings.TrimSpace(body.Reason),
	}); err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r *Router) handlePutHILWorkflowTemplateBinding(w http.ResponseWriter, req *http.Request, actionTypeID string) {
	cid := correlationID(req)
	viewer, workflow, ok := r.hilWorkflowViewer(w, req, true)
	if !ok {
		return
	}
	var body putHILWorkflowTemplateBindingBody
	if err := decodeStrictJSON(req, &body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	binding, err := workflow.PutWorkflowTemplateBinding(req.Context(), hilActorFromViewer(viewer), hil.PutTemplateBindingInput{
		Binding: hil.WorkflowTemplateBinding{
			TenantID: viewer.Tenant(), ActionTypeID: strings.TrimSpace(actionTypeID), ToolName: strings.TrimSpace(body.ToolName),
			RiskLevel: body.RiskLevel, TemplateID: strings.TrimSpace(body.TemplateID), Active: body.Active,
		},
		Reason: strings.TrimSpace(body.Reason),
	})
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, binding)
}

func (r *Router) handleDecideHILWorkflowTask(w http.ResponseWriter, req *http.Request, taskID string) {
	cid := correlationID(req)
	viewer, ok := r.hilMutationViewer(w, req)
	if !ok {
		return
	}
	workflow, ok := r.hil.(HILWorkflowService)
	if !ok || workflow == nil {
		status, response := MapError(errServiceDependency(), cid)
		writeJSON(w, status, response)
		return
	}
	var body decideHILWorkflowTaskBody
	if err := decodeStrictJSON(req, &body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	detail, err := workflow.DecideWorkflowTask(req.Context(), viewer.Tenant(), taskID, hilActorFromViewer(viewer), hil.DecideWorkflowTaskInput{
		Decision: body.Decision, Reason: strings.TrimSpace(body.Reason), IdempotencyKey: idempotencyKey(req),
		ExpectedInstanceVersion: body.ExpectedInstanceVersion,
	})
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (r *Router) handleGetHILWorkflowInstance(w http.ResponseWriter, req *http.Request, instanceID string) {
	cid := correlationID(req)
	viewer, ok := r.hilViewer(w, req)
	if !ok {
		return
	}
	workflow, ok := r.hil.(HILWorkflowService)
	if !ok || workflow == nil {
		status, response := MapError(errServiceDependency(), cid)
		writeJSON(w, status, response)
		return
	}
	detail, err := workflow.GetWorkflowInstance(req.Context(), viewer.Tenant(), instanceID)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	if !hilWorkflowInstanceVisible(detail, hilActorFromViewer(viewer), viewerHasScope(viewer.PermissionScope, permissionApprovalsManage, permissionApprovalsManage)) {
		status, response := MapError(hil.ErrParticipantRequired, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (r *Router) handleListHILWorkflowInstances(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, workflow, ok := r.hilWorkflowViewer(w, req, false)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(req.URL.Query().Get("offset"))
	actor := hilActorFromViewer(viewer)
	query := hil.WorkflowListQuery{
		TenantID: viewer.Tenant(), Status: hil.WorkflowInstanceStatus(strings.TrimSpace(req.URL.Query().Get("status"))),
		DecisionRequestID: strings.TrimSpace(req.URL.Query().Get("decision_id")),
		Limit:             limit, Offset: offset,
	}
	view := strings.TrimSpace(req.URL.Query().Get("view"))
	if view == "my_tasks" {
		query.ActionableBy = actor
	} else if view != "" && view != "started_by_me" && view != "all" {
		status, response := invalidRequest(cid, "view", "view is invalid")
		writeJSON(w, status, response)
		return
	}
	administrator := viewerHasScope(viewer.PermissionScope, permissionApprovalsManage, permissionApprovalsManage)
	page, err := listVisibleWorkflowInstances(req.Context(), workflow, query, actor, administrator, view == "started_by_me")
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// Apply viewer visibility before pagination so a filtered first page cannot hide
// later authorized workflows or report a page-sized total as the complete count.
func listVisibleWorkflowInstances(ctx context.Context, workflow HILWorkflowService, query hil.WorkflowListQuery, actor hil.ActorRef, administrator, startedByMe bool) (hil.WorkflowInstancePage, error) {
	limit, offset := query.Limit, query.Offset
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	result := hil.WorkflowInstancePage{Items: []hil.WorkflowInstanceDetail{}}
	query.Limit, query.Offset = 100, 0
	for {
		page, err := workflow.ListWorkflowInstances(ctx, query)
		if err != nil {
			return hil.WorkflowInstancePage{}, err
		}
		for _, item := range page.Items {
			if startedByMe && (item.Case.Requester.Kind != actor.Kind || item.Case.Requester.ID != actor.ID) {
				continue
			}
			if !hilWorkflowInstanceVisible(item, actor, administrator) {
				continue
			}
			if result.Total >= offset && len(result.Items) < limit {
				result.Items = append(result.Items, item)
			}
			result.Total++
		}
		query.Offset += len(page.Items)
		if query.Offset >= page.Total {
			return result, nil
		}
		if len(page.Items) == 0 {
			return hil.WorkflowInstancePage{}, hil.ErrConflict
		}
		if err := ctx.Err(); err != nil {
			return hil.WorkflowInstancePage{}, err
		}
	}
}

func (r *Router) handleHILWorkflowViewed(w http.ResponseWriter, req *http.Request, instanceID string) {
	cid := correlationID(req)
	viewer, workflow, ok := r.hilWorkflowViewer(w, req, true)
	if !ok {
		return
	}
	var body struct{}
	if err := decodeStrictJSON(req, &body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	detail, err := workflow.GetWorkflowInstance(req.Context(), viewer.Tenant(), instanceID)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	actor := hilActorFromViewer(viewer)
	if !hilWorkflowInstanceVisible(detail, actor, viewerHasScope(viewer.PermissionScope, permissionApprovalsManage, permissionApprovalsManage)) {
		status, response := MapError(hil.ErrParticipantRequired, cid)
		writeJSON(w, status, response)
		return
	}
	if err := workflow.RecordWorkflowViewed(req.Context(), viewer.Tenant(), instanceID, actor, idempotencyKey(req)); err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r *Router) handleResubmitHILWorkflow(w http.ResponseWriter, req *http.Request, instanceID string) {
	cid := correlationID(req)
	viewer, workflow, ok := r.hilWorkflowViewer(w, req, true)
	if !ok {
		return
	}
	var body resubmitHILWorkflowBody
	if err := decodeStrictJSON(req, &body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	detail, err := workflow.ResubmitWorkflow(req.Context(), viewer.Tenant(), instanceID, hilActorFromViewer(viewer), hil.ResubmitWorkflowInput{
		ArgsHash: strings.TrimSpace(body.ArgsHash), MaterialHash: strings.TrimSpace(body.MaterialHash), PayloadPreview: body.PayloadPreview,
		ExpiresAt: body.ExpiresAt, IdempotencyKey: idempotencyKey(req), ExpectedInstanceVersion: body.ExpectedInstanceVersion,
	})
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (r *Router) handleTransferHILWorkflowTask(w http.ResponseWriter, req *http.Request, taskID string, administrator bool) {
	cid := correlationID(req)
	viewer, ok := r.hilMutationViewer(w, req)
	if !ok {
		return
	}
	workflow, ok := r.hil.(HILWorkflowService)
	if !ok || workflow == nil {
		status, response := MapError(errServiceDependency(), cid)
		writeJSON(w, status, response)
		return
	}
	var body transferHILWorkflowTaskBody
	if err := decodeStrictJSON(req, &body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	input := hil.TransferWorkflowTaskInput{
		NewAssignee: body.NewAssignee, Reason: strings.TrimSpace(body.Reason),
		IdempotencyKey: idempotencyKey(req), ExpectedInstanceVersion: body.ExpectedInstanceVersion,
	}
	actor := hilActorFromViewer(viewer)
	var detail hil.WorkflowInstanceDetail
	var err error
	if administrator {
		detail, err = workflow.ReassignWorkflowTask(req.Context(), viewer.Tenant(), taskID, actor, input)
	} else {
		detail, err = workflow.TransferWorkflowTask(req.Context(), viewer.Tenant(), taskID, actor, input)
	}
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func hilWorkflowInstanceVisible(detail hil.WorkflowInstanceDetail, actor hil.ActorRef, administrator bool) bool {
	if administrator || (actor.Kind == detail.Case.Requester.Kind && actor.ID == detail.Case.Requester.ID) {
		return true
	}
	for _, stage := range detail.Stages {
		for _, participant := range stage.ResolvedParticipants {
			if participant.Kind == actor.Kind && participant.ID == actor.ID {
				return true
			}
		}
		for _, participant := range stage.ResolvedEscalationActors {
			if participant.Kind == actor.Kind && participant.ID == actor.ID {
				return true
			}
		}
	}
	for _, task := range detail.Tasks {
		if (task.CurrentAssignee.Kind == actor.Kind && task.CurrentAssignee.ID == actor.ID) || (task.OriginalAssignee.Kind == actor.Kind && task.OriginalAssignee.ID == actor.ID) {
			return true
		}
	}
	return false
}

func (r *Router) hilWorkflowViewer(w http.ResponseWriter, req *http.Request, mutation bool) (missioncontrol.ViewerRef, HILWorkflowService, bool) {
	var viewer missioncontrol.ViewerRef
	var ok bool
	if mutation {
		viewer, ok = r.hilMutationViewer(w, req)
	} else {
		viewer, ok = r.hilViewer(w, req)
	}
	if !ok {
		return missioncontrol.ViewerRef{}, nil, false
	}
	workflow, ok := r.hil.(HILWorkflowService)
	if !ok || workflow == nil {
		status, response := MapError(errServiceDependency(), correlationID(req))
		writeJSON(w, status, response)
		return missioncontrol.ViewerRef{}, nil, false
	}
	return viewer, workflow, true
}
