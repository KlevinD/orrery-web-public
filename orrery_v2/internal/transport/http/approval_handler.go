package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/app/discussionsession"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/todo"
	missioncore "orrery_v2/internal/mission/core"
)

func (r *Router) handleListApprovals(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.approvals == nil {
		status, body := MapError(errServiceDependency(), cid)
		writeJSON(w, status, body)
		return
	}
	limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(req.URL.Query().Get("offset"))
	query := missioncontrol.ApprovalListQuery{
		MissionID: missioncore.MissionID(req.URL.Query().Get("mission_id")),
		Status:    req.URL.Query().Get("status"),
		Limit:     limit,
		Offset:    offset,
	}
	baseQuery := query
	if r.discussions != nil || r.todoAutomation != nil {
		baseQuery.Status = ""
		baseQuery.Limit = 0
		baseQuery.Offset = 0
	}
	page, err := r.approvals.ListApprovals(req.Context(), viewer, baseQuery)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	page, err = r.withTodoAutomationApprovals(req.Context(), req, viewer, page, query)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	page, err = r.withDiscussionRuntimeApprovals(req.Context(), viewer, page, query)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (r *Router) handleApprovalDecision(w http.ResponseWriter, req *http.Request, approvalID string) {
	cid := correlationID(req)
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if idempotencyKey(req) == "" {
		status, body := invalidRequest(cid, "idempotency_key", "idempotency key is required for mutating commands")
		writeJSON(w, status, body)
		return
	}
	if r.approvals == nil {
		status, body := MapError(errServiceDependency(), cid)
		writeJSON(w, status, body)
		return
	}
	cmd, err := decodeApprovalDecision(req, approvalID)
	if err != nil {
		status, body := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, body)
		return
	}
	if strings.HasPrefix(strings.TrimSpace(approvalID), "todo-execution-") {
		if r.todoAutomation == nil {
			status, body := MapError(errServiceDependency(), cid)
			writeJSON(w, status, body)
			return
		}
		state, decisionErr := r.todoAutomation.DecideTodoAutomationApproval(req.Context(), approvalTodoViewer(req, viewer), todo.DecideTodoAutomationApprovalRequest{
			ApprovalID:     approvalID,
			ActionID:       cmd.ActionID,
			Message:        cmd.Message,
			PlanDigest:     cmd.Metadata["plan_digest"],
			IdempotencyKey: cmd.IdempotencyKey,
		})
		receipt := missioncontrol.CommandReceiptDTO{
			CommandID:  state.ApprovalID,
			Status:     "accepted",
			TaskID:     state.TodoID,
			Version:    int(state.TodoVersion),
			AcceptedAt: time.Now().UTC(),
		}
		writeCommandResult(w, cid, receipt, decisionErr)
		return
	}
	receipt, err := r.approvals.DecideApproval(req.Context(), viewer, cmd)
	writeCommandResult(w, cid, receipt, err)
}

func (r *Router) withTodoAutomationApprovals(ctx context.Context, req *http.Request, viewer missioncontrol.ViewerRef, page missioncontrol.ApprovalPageDTO, query missioncontrol.ApprovalListQuery) (missioncontrol.ApprovalPageDTO, error) {
	if r.todoAutomation == nil || !query.MissionID.IsZero() {
		return page, nil
	}
	items, err := r.todoAutomation.ListTodoAutomationApprovals(ctx, approvalTodoViewer(req, viewer), todo.ListTodoAutomationApprovalsQuery{})
	if err != nil {
		return page, err
	}
	for _, approval := range items {
		page.Items = append(page.Items, todoAutomationApprovalItem(approval))
	}
	return page, nil
}

func approvalTodoViewer(req *http.Request, viewer missioncontrol.ViewerRef) todo.ViewerRef {
	tenantID := "system"
	organizationID := ""
	departmentID := ""
	displayName := viewer.ViewerID
	if req != nil {
		tenantID = firstHTTPString(req.Header.Get("X-Tenant-ID"), req.Header.Get("X-Tenant"), "system")
		organizationID = strings.TrimSpace(req.Header.Get("X-Organization-ID"))
		departmentID = strings.TrimSpace(req.Header.Get("X-Department-ID"))
		displayName = firstHTTPString(displayNameFromRequest(req), viewer.ViewerID)
	}
	return todo.ViewerRef{
		TenantID:               tenantID,
		OrganizationID:         organizationID,
		DepartmentID:           departmentID,
		TimeZone:               "UTC",
		ViewerID:               viewer.ViewerID,
		ViewerKind:             todo.ViewerKind(viewer.ViewerKind),
		AuthorizationSubjectID: viewer.AuthorizationSubjectID,
		DisplayName:            displayName,
		PermissionScope:        append([]string(nil), viewer.PermissionScope...),
	}
}

func todoAutomationApprovalItem(approval todo.TodoAutomationApproval) missioncontrol.ApprovalItemDTO {
	actions := []missioncontrol.ApprovalActionDTO(nil)
	if approval.Status == "pending" {
		path := "/api/approvals/" + approval.ApprovalID + "/decision"
		actions = []missioncontrol.ApprovalActionDTO{
			{ActionID: "approve", Label: "Approve plan", Style: "primary", Decision: "approved", Effect: "once", InputKind: "message", Method: http.MethodPost, Path: path, RequiresBody: true, ConfirmationRequired: true},
			{ActionID: "reject", Label: "Reject", Style: "danger", Decision: "denied", Effect: "once", InputKind: "message", Method: http.MethodPost, Path: path, RequiresBody: true, ConfirmationRequired: true, ReasonRequired: true},
		}
	}
	fields := map[string]string{
		"todo_id":         approval.TodoID,
		"todo_version":    strconv.FormatInt(approval.TodoVersion, 10),
		"conversation_id": approval.ConversationID,
		"plan_digest":     approval.PlanDigest,
	}
	if hilDecisionID := strings.TrimSpace(approval.HILDecisionID); hilDecisionID != "" {
		fields["hil_decision_id"] = hilDecisionID
	}
	if approval.Plan.ResourceSnapshotDigest != "" {
		fields["resource_snapshot_digest"] = approval.Plan.ResourceSnapshotDigest
	}
	planPayload, _ := json.MarshalIndent(approval.Plan, "", "  ")
	return missioncontrol.ApprovalItemDTO{
		ApprovalID:       approval.ApprovalID,
		ApprovalType:     "todo_execution_plan",
		DisplayKind:      "plan_approval",
		Status:           approval.Status,
		Priority:         "normal",
		TaskID:           approval.TodoID,
		SourceType:       "todo",
		SourceID:         approval.TodoID,
		Provider:         "todo_automation",
		Title:            "Todo execution plan: " + approval.TodoTitle,
		Summary:          approval.Plan.Summary,
		Details:          string(planPayload),
		RiskSummary:      strings.Join(approval.Plan.RiskSummary, "; "),
		RequestedBy:      approval.RequestedBy,
		AssignedTo:       approval.ApproverID,
		RequestedByActor: missioncontrol.ApprovalActorRefDTO{ActorType: "agent", ActorID: approval.RequestedBy},
		PayloadPreview:   missioncontrol.ApprovalPayloadDTO{Kind: "plan_approval", Language: "json", Title: approval.TodoTitle, Content: string(planPayload), Fields: fields},
		AvailableActions: actions,
		EvidenceRefs:     []string{"/todos/" + approval.TodoID, "/discussions/" + approval.ConversationID},
		CreatedAt:        approval.CreatedAt,
		DecidedAt:        approval.DecidedAt,
	}
}

func (r *Router) withDiscussionRuntimeApprovals(ctx context.Context, viewer missioncontrol.ViewerRef, page missioncontrol.ApprovalPageDTO, query missioncontrol.ApprovalListQuery) (missioncontrol.ApprovalPageDTO, error) {
	if r.discussions == nil && r.todoAutomation == nil {
		return page, nil
	}
	if r.discussions != nil {
		discussionViewerID := strings.TrimSpace(viewer.ViewerID)
		agentProfileID := ""
		if viewer.ViewerKind == missioncontrol.ViewerKindUser || viewer.ViewerKind == missioncontrol.ViewerKindHuman {
			discussionViewerID = strings.TrimSpace(viewer.AuthorizationSubjectID)
			agentProfileID = discussionViewerID
		}
		discussionViewer := discussionsession.ViewerRef{
			ViewerID:       discussionViewerID,
			AgentProfileID: agentProfileID,
			ViewerKind:     discussionsession.ViewerKind(viewer.ViewerKind),
			TenantID:       viewer.Tenant(),
		}
		discussionConversations, err := r.discussions.ListApprovalConversations(ctx, discussionViewer)
		if err != nil {
			return page, err
		}
		var visibleDiscussionItems []missioncontrol.ApprovalItemDTO
		for _, detail := range discussionConversations {
			visibleMissionID, visibleForQuery := discussionApprovalMissionID(detail, query.MissionID)
			for _, event := range detail.Events {
				item, ok := discussionRuntimeApprovalItem(detail, event)
				if !ok {
					continue
				}
				item = normalizeDiscussionApprovalAvailability(detail, item)
				if visibleForQuery {
					item.MissionID = visibleMissionID
					visibleDiscussionItems = upsertApprovalItemByIdentity(visibleDiscussionItems, item)
				}
			}
		}
		page.Items = removeApprovalItemsOverriddenByDiscussion(page.Items, visibleDiscussionItems)
		for _, item := range visibleDiscussionItems {
			page.Items = upsertApprovalItemByIdentity(page.Items, item)
		}
	}
	items := filterApprovalItemsByStatus(page.Items, query.Status)
	total := len(items)
	offset := query.Offset
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	limit := query.Limit
	if limit <= 0 {
		limit = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	page.Items = items[offset:end]
	page.Page = missioncontrol.PageDTO{Limit: query.Limit, Offset: query.Offset, Total: total, Returned: end - offset, HasMore: end < total}
	return page, nil
}

func discussionApprovalMissionID(conversation discussionsession.ConversationDTO, missionID missioncore.MissionID) (string, bool) {
	if !missionID.IsZero() {
		target := missionID.String()
		for _, linked := range conversation.LinkedMissions {
			if strings.EqualFold(strings.TrimSpace(linked.MissionID), target) {
				return target, true
			}
		}
		return "", false
	}
	for _, linked := range conversation.LinkedMissions {
		if value := strings.TrimSpace(linked.MissionID); value != "" {
			return value, true
		}
	}
	return strings.TrimSpace(conversation.ConversationID), true
}

func discussionRuntimeApprovalItem(conversation discussionsession.ConversationDTO, event discussionsession.EventDTO) (missioncontrol.ApprovalItemDTO, bool) {
	if event.Kind != "discussion_runtime_approval_requested" {
		return missioncontrol.ApprovalItemDTO{}, false
	}
	metadata := event.Metadata
	if metadata == nil {
		metadata = map[string]string{}
	}
	// HIL owns the durable decision and receipt once this bridge is present.
	// Do not expose a second, legacy approval card for the same operation.
	if strings.TrimSpace(metadata["hil_decision_id"]) != "" {
		return missioncontrol.ApprovalItemDTO{}, false
	}
	requestID := firstHTTPString(strings.TrimSpace(metadata["approval_request_id"]), strings.TrimSpace(event.EventID))
	approvalID := firstHTTPString(strings.TrimSpace(event.EventID), requestID)
	if approvalID == "" {
		return missioncontrol.ApprovalItemDTO{}, false
	}
	status := strings.TrimSpace(event.Status)
	if status == "" {
		status = "pending"
	}
	approvalType := firstHTTPString(strings.TrimSpace(metadata["approval_type"]), strings.TrimSpace(metadata["approval_kind"]), "permission_request")
	displayKind := discussionsession.RuntimeApprovalDisplayKind(firstHTTPString(strings.TrimSpace(metadata["approval_kind"]), approvalType))
	toolCall := discussionApprovalToolCall(conversation, metadata)
	toolCallName := ""
	var toolArguments map[string]string
	if toolCall != nil {
		toolCallName = strings.TrimSpace(toolCall.Name)
		toolArguments = toolCall.Arguments
	}
	toolName := firstHTTPString(strings.TrimSpace(metadata["approval_tool_name"]), strings.TrimSpace(metadata["approval_native_tool_name"]), toolCallName, "runtime")
	conversationID := firstHTTPString(strings.TrimSpace(event.ConversationID), strings.TrimSpace(conversation.ConversationID), strings.TrimSpace(metadata["approval_conversation_id"]))
	runtimeRun := discussionApprovalRun(conversation, metadata)
	requestedBy := strings.TrimSpace(conversation.CreatedBy)
	triggeredBy := strings.TrimSpace(metadata["approval_agent_id"])
	triggeredByName := ""
	if runtimeRun != nil {
		triggeredBy = firstHTTPString(triggeredBy, strings.TrimSpace(runtimeRun.AgentID))
		triggeredByName = strings.TrimSpace(runtimeRun.AgentName)
	}
	triggeredBy = firstHTTPString(triggeredBy, strings.TrimSpace(conversation.DirectAgentID))
	decidedBy := strings.TrimSpace(metadata["approval_decided_by"])
	requestedByActor := discussionApprovalActorRef(conversation, requestedBy, "human", conversation.CreatedByName)
	triggeredByActor := discussionApprovalActorRef(conversation, triggeredBy, "agent", triggeredByName)
	decidedByActor := discussionApprovalActorRef(conversation, decidedBy, "human", "")
	path := discussionsession.RuntimeApprovalDecisionPath(conversationID, approvalID)
	item := missioncontrol.ApprovalItemDTO{
		ApprovalID:       approvalID,
		ApprovalType:     approvalType,
		DisplayKind:      displayKind,
		Status:           status,
		Priority:         discussionApprovalPriority(displayKind),
		MissionID:        conversationID,
		TaskID:           strings.TrimSpace(metadata["approval_run_id"]),
		SourceType:       "discussion",
		SourceID:         conversationID,
		Provider:         firstHTTPString(strings.TrimSpace(metadata["approval_provider"]), strings.TrimSpace(metadata["approval_source"]), "runtime"),
		ToolName:         toolName,
		Title:            discussionApprovalTitle(displayKind, toolName),
		Summary:          firstHTTPString(strings.TrimSpace(metadata["approval_summary"]), strings.TrimSpace(metadata["summary"]), strings.TrimSpace(event.Summary), discussionApprovalTitle(displayKind, toolName)),
		Details:          firstHTTPString(strings.TrimSpace(metadata["approval_details"]), strings.TrimSpace(metadata["details"]), strings.TrimSpace(metadata["reason"]), strings.TrimSpace(metadata["approval_reason"])),
		RiskSummary:      discussionRuntimeApprovalRiskSummary(metadata),
		RequestedBy:      requestedBy,
		TriggeredBy:      triggeredBy,
		DecidedBy:        decidedBy,
		AssignedTo:       strings.TrimSpace(metadata["approval_assigned_to"]),
		RequestedByActor: requestedByActor,
		TriggeredByActor: triggeredByActor,
		DecidedByActor:   decidedByActor,
		PayloadPreview:   discussionApprovalPayloadPreview(displayKind, toolName, metadata, toolArguments),
		Choices:          discussionApprovalChoices(metadata),
		AvailableActions: discussionRuntimeApprovalActions(
			discussionsession.RuntimeApprovalActions(path, displayKind, status, metadata["approval_allowed_actions"]),
			path,
			displayKind,
			status,
		),
		CreatedAt: event.CreatedAt,
	}
	if triggeredBy != "" {
		item.TargetActor = triggeredByActor
	}
	if decidedAt := discussionApprovalDecidedAt(metadata); !decidedAt.IsZero() {
		item.DecidedAt = decidedAt
	}
	return item, true
}

func discussionRuntimeApprovalRiskSummary(metadata map[string]string) string {
	base := strings.TrimSpace(metadata["risk_summary"])
	source := strings.TrimSpace(metadata["approval_decision_source"])
	var parts []string
	switch source {
	case "allowlist":
		parts = append(parts, "Allowlist")
		if ruleID := strings.TrimSpace(metadata["approval_rule_id"]); ruleID != "" {
			parts = append(parts, "rule "+ruleID)
		}
	case "full":
		parts = append(parts, "Full-trust policy")
	case "ai_reviewer":
		parts = append(parts, "AI reviewer")
		if risk := strings.TrimSpace(metadata["approval_reviewer_risk"]); risk != "" {
			parts = append(parts, risk+" risk")
		}
		if rationale := firstHTTPString(
			strings.TrimSpace(metadata["approval_reviewer_rationale"]),
			strings.TrimSpace(metadata["approval_reviewer_reason"]),
		); rationale != "" {
			parts = append(parts, rationale)
		}
	case "forced_human":
		parts = append(parts, "Forced human review")
	case "policy":
		parts = append(parts, "Runtime approval policy")
	case "human":
		parts = append(parts, "Human review")
	}
	if len(parts) > 0 && source != "ai_reviewer" {
		if reason := strings.TrimSpace(metadata["approval_decision_reason"]); reason != "" {
			parts = append(parts, reason)
		}
	}
	provenance := strings.Join(parts, " | ")
	if base == "" {
		return provenance
	}
	if provenance == "" || strings.Contains(base, provenance) {
		return base
	}
	return base + " | " + provenance
}

func normalizeDiscussionApprovalAvailability(conversation discussionsession.ConversationDTO, item missioncontrol.ApprovalItemDTO) missioncontrol.ApprovalItemDTO {
	if !discussionApprovalIsPending(item.Status) {
		item.AvailableActions = nil
		return item
	}
	runStatus := discussionApprovalRunStatus(conversation, item.TaskID)
	if strings.EqualFold(runStatus, "blocked") {
		return item
	}
	if discussionConversationIsTerminal(conversation.Status) || discussionRunIsTerminal(runStatus) {
		item.Status = "unavailable"
		item.AvailableActions = nil
		return item
	}
	if runStatus != "" && !strings.EqualFold(runStatus, "blocked") && !strings.EqualFold(runStatus, "running") {
		item.AvailableActions = nil
	}
	return item
}

func discussionApprovalRunStatus(conversation discussionsession.ConversationDTO, runID string) string {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return ""
	}
	for _, run := range conversation.Runs {
		if strings.EqualFold(strings.TrimSpace(run.RunID), runID) {
			return strings.TrimSpace(run.Status)
		}
	}
	return ""
}

func discussionConversationIsTerminal(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "failed", "cancelled", "canceled":
		return true
	default:
		return false
	}
}

func discussionRunIsTerminal(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "failed", "cancelled", "canceled":
		return true
	default:
		return false
	}
}

func discussionRuntimeApprovalActions(actions []discussionsession.ApprovalActionDTO, path, displayKind, status string) []missioncontrol.ApprovalActionDTO {
	if len(actions) == 0 {
		actions = discussionsession.RuntimeApprovalActions(path, displayKind, status)
	}
	out := make([]missioncontrol.ApprovalActionDTO, 0, len(actions))
	for _, action := range actions {
		out = append(out, missioncontrol.ApprovalActionDTO{
			ActionID:             action.ActionID,
			Label:                action.Label,
			Style:                action.Style,
			Decision:             action.Decision,
			Effect:               action.Effect,
			InputKind:            action.InputKind,
			DefaultMessage:       action.DefaultMessage,
			Method:               action.Method,
			Path:                 action.Path,
			RequiresBody:         action.RequiresBody,
			ConfirmationRequired: action.ConfirmationRequired,
			ReasonRequired:       action.ReasonRequired,
			AllowsModification:   action.AllowsModification,
			RememberAllowed:      action.RememberAllowed,
		})
	}
	return out
}

func removeApprovalItemsOverriddenByDiscussion(items []missioncontrol.ApprovalItemDTO, discussionItems []missioncontrol.ApprovalItemDTO) []missioncontrol.ApprovalItemDTO {
	overrides := map[string]struct{}{}
	for _, item := range discussionItems {
		for _, key := range approvalIdentityKeys(item) {
			overrides[key] = struct{}{}
		}
	}
	if len(overrides) == 0 {
		return append([]missioncontrol.ApprovalItemDTO(nil), items...)
	}
	out := make([]missioncontrol.ApprovalItemDTO, 0, len(items))
	for _, item := range items {
		if approvalSharesIdentity(item, overrides) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func upsertApprovalItemByIdentity(items []missioncontrol.ApprovalItemDTO, item missioncontrol.ApprovalItemDTO) []missioncontrol.ApprovalItemDTO {
	keys := approvalIdentityKeySet(item)
	for idx, existing := range items {
		if approvalSharesIdentity(existing, keys) {
			if approvalItemShouldReplace(item, existing) {
				items[idx] = item
			}
			return items
		}
	}
	return append(items, item)
}

func approvalItemShouldReplace(next missioncontrol.ApprovalItemDTO, current missioncontrol.ApprovalItemDTO) bool {
	nextPending := discussionApprovalIsPending(next.Status)
	currentPending := discussionApprovalIsPending(current.Status)
	if !nextPending && currentPending {
		return true
	}
	if nextPending && !currentPending {
		return false
	}
	return next.CreatedAt.After(current.CreatedAt) || next.CreatedAt.Equal(current.CreatedAt)
}

func approvalSharesIdentity(item missioncontrol.ApprovalItemDTO, keys map[string]struct{}) bool {
	for _, key := range approvalIdentityKeys(item) {
		if _, ok := keys[key]; ok {
			return true
		}
	}
	return false
}

func approvalIdentityKeySet(item missioncontrol.ApprovalItemDTO) map[string]struct{} {
	keys := map[string]struct{}{}
	for _, key := range approvalIdentityKeys(item) {
		keys[key] = struct{}{}
	}
	return keys
}

func approvalIdentityKeys(item missioncontrol.ApprovalItemDTO) []string {
	keys := map[string]struct{}{}
	add := func(key string) {
		key = strings.TrimSpace(key)
		if key != "" {
			keys[key] = struct{}{}
		}
	}
	approvalID := strings.TrimSpace(item.ApprovalID)
	conversationID := firstHTTPString(approvalPayloadField(item, "approval_conversation_id"), discussionSourceConversationID(item))
	requestID := approvalPayloadField(item, "approval_request_id")
	runID := firstHTTPString(approvalPayloadField(item, "approval_run_id"), strings.TrimSpace(item.TaskID))
	if approvalID != "" {
		add("id:" + approvalID)
	}
	if conversationID != "" && approvalID != "" {
		add("discussion:" + conversationID + ":approval:" + approvalID)
	}
	if conversationID != "" && requestID != "" {
		add("discussion:" + conversationID + ":request:" + requestID)
	}
	if conversationID != "" && runID != "" && requestID != "" {
		add("discussion:" + conversationID + ":run:" + runID + ":request:" + requestID)
	}
	for _, action := range item.AvailableActions {
		actionConversationID, actionApprovalID, ok := discussionApprovalActionPathParts(action.Path)
		if !ok {
			continue
		}
		add("discussion:" + actionConversationID + ":approval:" + actionApprovalID)
		add("discussion:" + actionConversationID + ":request:" + actionApprovalID)
		if runID != "" {
			add("discussion:" + actionConversationID + ":run:" + runID + ":request:" + actionApprovalID)
		}
		if requestID != "" {
			add("discussion:" + actionConversationID + ":request:" + requestID)
		}
	}
	out := make([]string, 0, len(keys))
	for key := range keys {
		out = append(out, key)
	}
	return out
}

func approvalPayloadField(item missioncontrol.ApprovalItemDTO, key string) string {
	if len(item.PayloadPreview.Fields) == 0 {
		return ""
	}
	target := normalizeApprovalFieldKey(key)
	for fieldKey, value := range item.PayloadPreview.Fields {
		if normalizeApprovalFieldKey(fieldKey) == target {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func discussionSourceConversationID(item missioncontrol.ApprovalItemDTO) string {
	if strings.EqualFold(strings.TrimSpace(item.SourceType), "discussion") {
		return strings.TrimSpace(item.SourceID)
	}
	return ""
}

func discussionApprovalActionPathParts(path string) (string, string, bool) {
	path = strings.TrimSpace(path)
	if idx := strings.Index(path, "?"); idx >= 0 {
		path = path[:idx]
	}
	const prefix = "/api/discussions/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) < 4 || parts[1] != "approvals" || parts[3] != "decision" {
		return "", "", false
	}
	conversationID := strings.TrimSpace(parts[0])
	approvalID := strings.TrimSpace(parts[2])
	return conversationID, approvalID, conversationID != "" && approvalID != ""
}

func normalizeApprovalFieldKey(key string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToLower(strings.TrimSpace(key)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteRune('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func filterApprovalItemsByStatus(items []missioncontrol.ApprovalItemDTO, status string) []missioncontrol.ApprovalItemDTO {
	status = strings.TrimSpace(status)
	if status == "" {
		return items
	}
	out := make([]missioncontrol.ApprovalItemDTO, 0, len(items))
	for _, item := range items {
		if approvalStatusMatches(item.Status, status) {
			out = append(out, item)
		}
	}
	return out
}

func approvalStatusMatches(status string, want string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	want = strings.ToLower(strings.TrimSpace(want))
	switch want {
	case "pending":
		return status == "" || status == "pending" || status == "open"
	case "denied":
		return status == "denied" || status == "rejected"
	default:
		return status == want
	}
}

func firstHTTPString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func discussionApprovalPriority(kind string) string {
	switch strings.TrimSpace(kind) {
	case "command_execution", "file_change":
		return "high"
	default:
		return "normal"
	}
}

func discussionApprovalTitle(kind, toolName string) string {
	switch strings.TrimSpace(kind) {
	case "command_execution":
		return "Command approval: " + toolName
	case "file_change":
		return "File approval: " + toolName
	case "user_input":
		return "Runtime question"
	default:
		return "Runtime approval: " + toolName
	}
}

var approvalOpaqueIdentifierPattern = regexp.MustCompile(`(?i)^(?:call|toolu|tool_call|request|req)[_-][a-z0-9_-]{8,}$`)
var approvalUUIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

var discussionApprovalIntentFieldKeys = []string{
	"title",
	"final_goal",
	"root_goal",
	"goal",
	"task_goal",
	"business_direction",
	"plan_summary",
	"query",
	"prompt",
	"command",
	"path",
	"url",
	"content",
	"summary",
	"description",
	"message",
	"reason",
	"role",
	"capability",
}

func discussionApprovalToolCall(conversation discussionsession.ConversationDTO, metadata map[string]string) *discussionsession.ToolCallDTO {
	runID := strings.TrimSpace(metadata["approval_run_id"])
	requestID := strings.TrimSpace(metadata["approval_request_id"])
	for runIndex := range conversation.Runs {
		run := &conversation.Runs[runIndex]
		if runID != "" && strings.TrimSpace(run.RunID) != runID {
			continue
		}
		for toolIndex := range run.ToolCalls {
			tool := &run.ToolCalls[toolIndex]
			if requestID != "" && strings.TrimSpace(tool.ToolCallID) == requestID {
				return tool
			}
		}
	}
	toolName := firstHTTPString(strings.TrimSpace(metadata["approval_tool_name"]), strings.TrimSpace(metadata["approval_native_tool_name"]))
	if runID == "" || toolName == "" {
		return nil
	}
	var matched *discussionsession.ToolCallDTO
	for runIndex := range conversation.Runs {
		run := &conversation.Runs[runIndex]
		if strings.TrimSpace(run.RunID) != runID {
			continue
		}
		for toolIndex := range run.ToolCalls {
			tool := &run.ToolCalls[toolIndex]
			if strings.TrimSpace(tool.Name) != toolName {
				continue
			}
			if matched != nil {
				return nil
			}
			matched = tool
		}
	}
	return matched
}

func discussionApprovalRun(conversation discussionsession.ConversationDTO, metadata map[string]string) *discussionsession.RunDTO {
	runID := strings.TrimSpace(metadata["approval_run_id"])
	if runID == "" {
		return nil
	}
	for index := range conversation.Runs {
		if strings.EqualFold(strings.TrimSpace(conversation.Runs[index].RunID), runID) {
			return &conversation.Runs[index]
		}
	}
	return nil
}

func discussionApprovalActorRef(conversation discussionsession.ConversationDTO, actorID, fallbackType, fallbackName string) missioncontrol.ApprovalActorRefDTO {
	actorID = strings.TrimSpace(actorID)
	if actorID == "" {
		return missioncontrol.ApprovalActorRefDTO{}
	}
	actorType := strings.TrimSpace(fallbackType)
	displayName := strings.TrimSpace(fallbackName)
	if strings.EqualFold(actorID, strings.TrimSpace(conversation.CreatedBy)) {
		displayName = firstHTTPString(displayName, strings.TrimSpace(conversation.CreatedByName))
	}
	for _, member := range conversation.Members {
		if !strings.EqualFold(strings.TrimSpace(member.ID), actorID) {
			continue
		}
		actorType = firstHTTPString(strings.TrimSpace(member.Kind), actorType)
		displayName = firstHTTPString(strings.TrimSpace(member.DisplayName), displayName)
		break
	}
	return missioncontrol.ApprovalActorRefDTO{
		ActorType:   actorType,
		ActorID:     actorID,
		DisplayName: firstHTTPString(displayName, actorID),
	}
}

func discussionApprovalPayloadPreview(kind, toolName string, metadata, toolArguments map[string]string) missioncontrol.ApprovalPayloadDTO {
	safeArguments := discussionApprovalSafeArguments(metadata)
	for key, value := range discussionApprovalSafeArguments(toolArguments) {
		safeArguments[key] = value
	}
	preview := missioncontrol.ApprovalPayloadDTO{
		Kind:     kind,
		Language: "",
		Title:    firstHTTPString(strings.TrimSpace(metadata["payload_title"]), discussionApprovalTitle(kind, toolName)),
		Content:  discussionApprovalPayloadContent(kind, metadata, safeArguments),
	}
	if language := strings.TrimSpace(metadata["payload_language"]); language != "" {
		preview.Language = language
	} else if kind == "command_execution" {
		preview.Language = "shell"
	}
	fields := map[string]string{}
	for _, key := range []string{"sandbox_permissions", "scope", "path", "url", "server", "tool_id", "approval_conversation_id", "approval_session_id", "approval_turn_id", "approval_run_id", "approval_request_id", "approval_tool_name", "approval_native_tool_name", "hil_decision_id"} {
		if value := strings.TrimSpace(metadata[key]); value != "" {
			fields[key] = value
		}
	}
	for key, value := range safeArguments {
		fields[key] = value
	}
	if len(fields) > 0 {
		preview.Fields = fields
	}
	return preview
}

func discussionApprovalSafeArguments(arguments map[string]string) map[string]string {
	fields := map[string]string{}
	for _, key := range discussionApprovalIntentFieldKeys {
		if value := strings.TrimSpace(arguments[key]); approvalDisplayValueIsSafe(value) {
			fields[key] = value
		}
	}
	return fields
}

func discussionApprovalPayloadContent(kind string, metadata, arguments map[string]string) string {
	var values []string
	switch {
	case kind == "command_execution":
		values = []string{metadata["command"], metadata["payload_preview"], metadata["prompt"], metadata["approval_summary"], arguments["command"]}
	case kind == "file_change":
		values = []string{metadata["diff"], metadata["payload_preview"], metadata["prompt"], metadata["path"], metadata["approval_summary"], arguments["content"], arguments["path"]}
	case kind == "network_access" || strings.TrimSpace(metadata["url"]) != "" || strings.TrimSpace(arguments["url"]) != "":
		values = []string{metadata["prompt"], metadata["payload_preview"], metadata["approval_summary"], metadata["url"], arguments["query"], arguments["prompt"], arguments["url"]}
	default:
		values = []string{metadata["payload_preview"], metadata["prompt"], metadata["question"], metadata["command"], metadata["path"], metadata["url"], metadata["approval_summary"]}
		for _, key := range discussionApprovalIntentFieldKeys {
			values = append(values, arguments[key])
		}
	}
	for _, value := range values {
		if value = strings.TrimSpace(value); approvalDisplayValueIsSafe(value) {
			return value
		}
	}
	return ""
}

func approvalDisplayValueIsSafe(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && !approvalOpaqueIdentifierPattern.MatchString(value) && !approvalUUIDPattern.MatchString(value)
}

func discussionApprovalChoices(metadata map[string]string) []missioncontrol.ApprovalChoiceDTO {
	raw := strings.TrimSpace(metadata["choices_json"])
	if raw == "" {
		return nil
	}
	var decoded []missioncontrol.ApprovalChoiceDTO
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil
	}
	out := make([]missioncontrol.ApprovalChoiceDTO, 0, len(decoded))
	for _, choice := range decoded {
		choice.ChoiceID = strings.TrimSpace(choice.ChoiceID)
		choice.Label = strings.TrimSpace(choice.Label)
		choice.Description = strings.TrimSpace(choice.Description)
		if choice.ChoiceID == "" || choice.Label == "" {
			continue
		}
		out = append(out, choice)
	}
	return out
}

func discussionApprovalIsPending(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "pending", "open":
		return true
	default:
		return false
	}
}

func discussionApprovalDecidedAt(metadata map[string]string) time.Time {
	for _, key := range []string{"approval_decided_at", "decision_at", "decided_at"} {
		if value := strings.TrimSpace(metadata[key]); value != "" {
			if parsed, err := time.Parse(time.RFC3339, value); err == nil {
				return parsed
			}
		}
	}
	return time.Time{}
}
