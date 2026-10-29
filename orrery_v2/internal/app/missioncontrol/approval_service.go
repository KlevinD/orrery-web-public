package missioncontrol

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	missioncore "orrery_v2/internal/mission/core"
	discussion "orrery_v2/internal/mission/discussion"
	"orrery_v2/internal/mission/projection"
)

type ApprovalService struct {
	repo          ProjectionRepository
	commands      ApprovalCommandPort
	authorization *authorization.Service
}

func (s *ApprovalService) SetAuthorizationService(service *authorization.Service) {
	if s != nil {
		s.authorization = service
	}
}

type ApprovalCommandPort interface {
	SubmitQADecision(ctx context.Context, viewer ViewerRef, cmd QADecisionCommand) (CommandReceiptDTO, error)
	MarkInboxRead(ctx context.Context, viewer ViewerRef, cmd InboxItemCommand) (CommandReceiptDTO, error)
	CloseInboxItem(ctx context.Context, viewer ViewerRef, cmd InboxItemCommand) (CommandReceiptDTO, error)
	DecideBusinessApproval(ctx context.Context, viewer ViewerRef, cmd BusinessApprovalDecisionCommand) (CommandReceiptDTO, error)
}

func NewApprovalService(repo ProjectionRepository, commands ApprovalCommandPort) *ApprovalService {
	return &ApprovalService{repo: repo, commands: commands}
}

type ApprovalListQuery struct {
	MissionID missioncore.MissionID
	Status    string
	Limit     int
	Offset    int
}

type ApprovalPageDTO struct {
	Items []ApprovalItemDTO `json:"items"`
	Page  PageDTO           `json:"page"`
}

type ApprovalItemDTO struct {
	ApprovalID       string              `json:"approval_id"`
	ApprovalType     string              `json:"approval_type"`
	DisplayKind      string              `json:"display_kind,omitempty"`
	Status           string              `json:"status"`
	Priority         string              `json:"priority"`
	MissionID        string              `json:"mission_id,omitempty"`
	TaskID           string              `json:"task_id,omitempty"`
	SourceType       string              `json:"source_type"`
	SourceID         string              `json:"source_id"`
	Provider         string              `json:"provider,omitempty"`
	ToolName         string              `json:"tool_name,omitempty"`
	Title            string              `json:"title"`
	Summary          string              `json:"summary"`
	Details          string              `json:"details,omitempty"`
	RiskSummary      string              `json:"risk_summary,omitempty"`
	RequestedBy      string              `json:"requested_by"`
	TriggeredBy      string              `json:"triggered_by,omitempty"`
	DecidedBy        string              `json:"decided_by,omitempty"`
	AssignedTo       string              `json:"assigned_to,omitempty"`
	OrganizationCode string              `json:"organization_code,omitempty"`
	OrganizationName string              `json:"organization_name,omitempty"`
	RequestedByActor ApprovalActorRefDTO `json:"requested_by_actor,omitempty"`
	TriggeredByActor ApprovalActorRefDTO `json:"triggered_by_actor,omitempty"`
	DecidedByActor   ApprovalActorRefDTO `json:"decided_by_actor,omitempty"`
	TargetActor      ApprovalActorRefDTO `json:"target_actor,omitempty"`
	PolicyID         string              `json:"policy_id,omitempty"`
	RiskLevel        string              `json:"risk_level,omitempty"`
	DecisionEffect   string              `json:"decision_effect,omitempty"`
	PayloadPreview   ApprovalPayloadDTO  `json:"payload_preview,omitempty"`
	Choices          []ApprovalChoiceDTO `json:"choices,omitempty"`
	AvailableActions []ApprovalActionDTO `json:"available_actions"`
	EvidenceRefs     []string            `json:"evidence_refs,omitempty"`
	CreatedAt        time.Time           `json:"created_at"`
	DecidedAt        time.Time           `json:"decided_at,omitempty"`
	ExpiresAt        time.Time           `json:"expires_at,omitempty"`
}

type ApprovalActorRefDTO struct {
	ActorType   string `json:"actor_type"`
	ActorID     string `json:"actor_id"`
	DisplayName string `json:"display_name,omitempty"`
}

type ApprovalPayloadDTO struct {
	Kind     string            `json:"kind,omitempty"`
	Language string            `json:"language,omitempty"`
	Title    string            `json:"title,omitempty"`
	Content  string            `json:"content,omitempty"`
	Fields   map[string]string `json:"fields,omitempty"`
}

type ApprovalChoiceDTO struct {
	ChoiceID    string `json:"choice_id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type ApprovalActionDTO struct {
	ActionID             string `json:"action_id"`
	Label                string `json:"label"`
	Style                string `json:"style,omitempty"`
	Decision             string `json:"decision,omitempty"`
	Effect               string `json:"effect,omitempty"`
	InputKind            string `json:"input_kind,omitempty"`
	DefaultMessage       string `json:"default_message,omitempty"`
	Method               string `json:"method"`
	Path                 string `json:"path"`
	RequiresBody         bool   `json:"requires_body"`
	ConfirmationRequired bool   `json:"confirmation_required"`
	ReasonRequired       bool   `json:"reason_required"`
	AllowsModification   bool   `json:"allows_modification,omitempty"`
	RememberAllowed      bool   `json:"remember_allowed,omitempty"`
}

type ApprovalDecisionCommand struct {
	RequestID      string
	IdempotencyKey string
	ApprovalID     string
	ActionID       string
	Message        string
	ModifiedInput  string
	ChoiceID       string
	Remember       bool
	Metadata       map[string]string
	TaskID         missioncore.TaskID
	ReplyMessageID string
}

type BusinessApprovalDecisionCommand struct {
	RequestID      string
	IdempotencyKey string
	ApprovalType   string
	MissionID      missioncore.MissionID
	ApprovalRef    string
	Decision       string
	ActionID       string
	Message        string
	ModifiedInput  string
	ChoiceID       string
	Remember       bool
	Metadata       map[string]string
}

func (s *ApprovalService) ListApprovals(ctx context.Context, viewer ViewerRef, query ApprovalListQuery) (ApprovalPageDTO, error) {
	if s == nil || s.repo == nil || s.authorization == nil {
		return ApprovalPageDTO{}, ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return ApprovalPageDTO{}, err
	}
	var items []ApprovalItemDTO
	if query.MissionID.IsZero() {
		repo, ok := s.repo.(ProjectionListRepository)
		if !ok {
			return ApprovalPageDTO{}, ErrServiceDependency
		}
		var models []projection.ReadModel
		var err error
		if tenantRepo, ok := s.repo.(TenantProjectionListRepository); ok {
			models, err = tenantRepo.ListMissionControlsForTenant(ctx, viewer.Tenant())
		} else {
			models, err = repo.ListMissionControls(ctx)
			if err == nil {
				filtered := models[:0]
				for _, model := range models {
					if missionModelTenantID(model) == viewer.Tenant() {
						filtered = append(filtered, model)
					}
				}
				models = filtered
			}
		}
		if err != nil {
			return ApprovalPageDTO{}, err
		}
		checks := make([]authorization.CheckRequest, 0, len(models))
		principal := missionPrincipal(viewer)
		for _, model := range models {
			checks = append(checks, authorization.CheckRequest{
				Principal: principal,
				Action:    "mission.view",
				Object:    identity.ObjectRef{Type: "mission", ID: model.Mission.MissionID.String(), TenantID: viewer.Tenant()},
			})
		}
		decisions, err := s.authorization.BatchCan(ctx, checks)
		if err != nil {
			return ApprovalPageDTO{}, err
		}
		if len(decisions) != len(models) {
			return ApprovalPageDTO{}, ErrServiceDependency
		}
		for i, model := range models {
			if !decisions[i].Allowed {
				continue
			}
			items = append(items, approvalItemsFromModel(model, viewer)...)
		}
	} else {
		allowed, err := s.canAccessMission(ctx, viewer, query.MissionID, "mission.view")
		if err != nil {
			return ApprovalPageDTO{}, err
		}
		if !allowed {
			return ApprovalPageDTO{}, ErrProjectionNotFound
		}
		model, err := loadMissionProjection(ctx, s.repo, viewer.Tenant(), query.MissionID)
		if err != nil {
			return ApprovalPageDTO{}, err
		}
		items = approvalItemsFromModel(model, viewer)
	}
	if query.Status != "" {
		items = filterApprovalsByStatus(items, query.Status)
	}
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
	return ApprovalPageDTO{
		Items: items[offset:end],
		Page:  PageDTO{Limit: query.Limit, Offset: query.Offset, Total: total, Returned: end - offset, HasMore: end < total},
	}, nil
}

func (s *ApprovalService) DecideApproval(ctx context.Context, viewer ViewerRef, cmd ApprovalDecisionCommand) (CommandReceiptDTO, error) {
	if s == nil || s.commands == nil || s.authorization == nil {
		return CommandReceiptDTO{}, ErrServiceDependency
	}
	if err := viewer.Validate(); err != nil {
		return CommandReceiptDTO{}, err
	}
	if cmd.IdempotencyKey == "" || cmd.ApprovalID == "" || cmd.ActionID == "" {
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
	kind, missionID, sourceID, err := parseApprovalID(cmd.ApprovalID)
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	allowed, err := s.canAccessMission(ctx, viewer, missioncore.MissionID(missionID), "mission.qa_approve")
	if err != nil {
		return CommandReceiptDTO{}, err
	}
	if !allowed {
		return CommandReceiptDTO{}, ErrProjectionNotFound
	}
	switch kind {
	case "qa":
		taskID, err := s.resolveQATaskID(ctx, viewer, missioncore.MissionID(missionID), sourceID)
		if err != nil {
			return CommandReceiptDTO{}, err
		}
		if !cmd.TaskID.IsZero() && cmd.TaskID != taskID {
			return CommandReceiptDTO{}, ErrInvalidRequest
		}
		decision := "approved"
		if cmd.ActionID == "reject" {
			decision = "rejected"
		} else if cmd.ActionID != "approve" {
			return CommandReceiptDTO{}, ErrInvalidRequest
		}
		return s.commands.SubmitQADecision(ctx, viewer, QADecisionCommand{
			RequestID:      cmd.RequestID,
			IdempotencyKey: cmd.IdempotencyKey,
			MissionID:      missioncore.MissionID(missionID),
			TaskID:         taskID,
			QARef:          sourceID,
			Decision:       decision,
			Message:        cmd.Message,
		})
	case "inbox":
		inboxCmd := InboxItemCommand{RequestID: cmd.RequestID, IdempotencyKey: cmd.IdempotencyKey, InboxItemID: discussion.InboxItemID(sourceID), ReplyMessageID: discussion.MessageID(cmd.ReplyMessageID)}
		switch cmd.ActionID {
		case "read":
			return s.commands.MarkInboxRead(ctx, viewer, inboxCmd)
		case "close":
			return s.commands.CloseInboxItem(ctx, viewer, inboxCmd)
		default:
			return CommandReceiptDTO{}, ErrInvalidRequest
		}
	case "permission_request", "tool_access", "evidence_review", "task_completion", "command_execution", "file_change", "mcp_elicitation", "tool_use", "network_access", "plan_approval", "user_input":
		request, err := s.resolveBusinessApprovalRequest(ctx, viewer, missioncore.MissionID(missionID), kind, sourceID)
		if err != nil {
			return CommandReceiptDTO{}, err
		}
		// HIL-backed requests retain a legacy projection record only to resume
		// the runtime. Their authoritative decision must go through HIL.
		if strings.TrimSpace(request.Metadata["hil_decision_id"]) != "" {
			return CommandReceiptDTO{}, ErrConflict
		}
		action, ok := approvalActionForRequest(kind, request.Metadata, cmd.ActionID)
		if !ok {
			return CommandReceiptDTO{}, ErrInvalidRequest
		}
		decision := firstString(action.Decision, "approved")
		return s.commands.DecideBusinessApproval(ctx, viewer, BusinessApprovalDecisionCommand{
			RequestID:      cmd.RequestID,
			IdempotencyKey: cmd.IdempotencyKey,
			ApprovalType:   kind,
			MissionID:      missioncore.MissionID(missionID),
			ApprovalRef:    sourceID,
			Decision:       decision,
			ActionID:       cmd.ActionID,
			Message:        cmd.Message,
			ModifiedInput:  cmd.ModifiedInput,
			ChoiceID:       cmd.ChoiceID,
			Remember:       cmd.Remember,
			Metadata:       decisionMetadata(request.Metadata, cmd, action),
		})
	default:
		return CommandReceiptDTO{}, ErrInvalidRequest
	}
}

func (s *ApprovalService) canAccessMission(ctx context.Context, viewer ViewerRef, missionID missioncore.MissionID, action string) (bool, error) {
	if s == nil || s.authorization == nil {
		return false, ErrServiceDependency
	}
	decision, err := s.authorization.Check(ctx, authorization.CheckRequest{
		Principal: missionPrincipal(viewer),
		Action:    action,
		Object:    identity.ObjectRef{Type: "mission", ID: missionID.String(), TenantID: viewer.Tenant()},
	})
	if errors.Is(err, authorization.ErrDenied) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return decision.Allowed, nil
}

func (s *ApprovalService) resolveBusinessApprovalRequest(ctx context.Context, viewer ViewerRef, missionID missioncore.MissionID, approvalType, requestID string) (projection.ApprovalRequestView, error) {
	if s == nil || s.repo == nil {
		return projection.ApprovalRequestView{}, ErrServiceDependency
	}
	model, err := loadMissionProjection(ctx, s.repo, viewer.Tenant(), missionID)
	if err != nil {
		return projection.ApprovalRequestView{}, err
	}
	for _, request := range model.ApprovalRequests {
		if request.RequestID == requestID && request.ApprovalType == approvalType {
			if request.MissionID.IsZero() || request.MissionID == missionID {
				return request, nil
			}
		}
	}
	return projection.ApprovalRequestView{}, ErrInvalidRequest
}

func approvalItemsFromModel(model projection.ReadModel, viewer ViewerRef) []ApprovalItemDTO {
	items := make([]ApprovalItemDTO, 0, len(model.QA.Checks)+len(model.ApprovalRequests))
	for _, check := range model.QA.Checks {
		if check.Status != projection.QAStatusRequested {
			continue
		}
		items = append(items, ApprovalItemDTO{
			ApprovalID:   "qa:" + model.Mission.MissionID.String() + ":" + check.QARef,
			ApprovalType: "qa",
			Status:       "pending",
			Priority:     "normal",
			MissionID:    model.Mission.MissionID.String(),
			TaskID:       check.TaskID.String(),
			SourceType:   "qa",
			SourceID:     check.QARef,
			Title:        "QA decision for " + check.TaskID.String(),
			Summary:      check.Message,
			RequestedBy:  "qa",
			AssignedTo:   viewer.ViewerID,
			AvailableActions: []ApprovalActionDTO{
				approvalAction("approve", "Approve", "/api/approvals/qa:"+model.Mission.MissionID.String()+":"+check.QARef+"/decision", false),
				approvalAction("reject", "Reject", "/api/approvals/qa:"+model.Mission.MissionID.String()+":"+check.QARef+"/decision", true),
			},
			CreatedAt: check.CreatedAt,
		})
	}
	for _, request := range model.ApprovalRequests {
		// Runtime requests delegated to HIL must be resolved through the HIL
		// decision endpoint, not through the legacy mission projection.
		if strings.TrimSpace(request.Metadata["hil_decision_id"]) != "" {
			continue
		}
		if request.Status != "" && request.Status != "pending" && request.Status != "open" {
			continue
		}
		approvalType := request.ApprovalType
		approvalID := approvalType + ":" + firstString(request.MissionID.String(), model.Mission.MissionID.String()) + ":" + request.RequestID
		actionPath := approvalDecisionActionPath(approvalID, request.Metadata)
		item := ApprovalItemDTO{
			ApprovalID:       approvalID,
			ApprovalType:     approvalType,
			Status:           "pending",
			Priority:         firstString(request.Priority, "normal"),
			MissionID:        firstString(request.MissionID.String(), model.Mission.MissionID.String()),
			TaskID:           request.TaskID.String(),
			SourceType:       request.SourceType,
			SourceID:         request.SourceID,
			Title:            request.Title,
			Summary:          request.Summary,
			RequestedBy:      request.RequestedBy.String(),
			AssignedTo:       request.AssignedTo.String(),
			AvailableActions: approvalActionsForRequest(approvalType, actionPath, request.Metadata),
			EvidenceRefs:     append([]string(nil), request.EvidenceRefs...),
			CreatedAt:        request.CreatedAt,
			DecidedAt:        request.DecidedAt,
		}
		applyApprovalRequestMetadata(&item, request.Metadata)
		items = append(items, item)
	}
	return items
}

func approvalDecisionActionPath(approvalID string, metadata map[string]string) string {
	conversationID := strings.TrimSpace(metadata["approval_conversation_id"])
	requestID := strings.TrimSpace(metadata["approval_request_id"])
	if conversationID != "" && requestID != "" {
		return "/api/discussions/" + conversationID + "/approvals/" + requestID + "/decision"
	}
	return "/api/approvals/" + approvalID + "/decision"
}

func applyApprovalRequestMetadata(item *ApprovalItemDTO, metadata map[string]string) {
	if item == nil || len(metadata) == 0 {
		return
	}
	item.Provider = firstString(strings.TrimSpace(metadata["approval_source"]), strings.TrimSpace(metadata["approval_provider"]))
	item.ToolName = strings.TrimSpace(metadata["approval_tool_name"])
	item.DisplayKind = approvalDisplayKind(item.ApprovalType, metadata)
	item.Details = approvalDetails(metadata)
	item.RiskSummary = approvalRiskSummary(item.RiskLevel, metadata)
	item.OrganizationCode = strings.TrimSpace(metadata["organization_code"])
	item.OrganizationName = strings.TrimSpace(metadata["organization_name"])
	item.PolicyID = strings.TrimSpace(metadata["policy_id"])
	item.RiskLevel = strings.TrimSpace(metadata["risk_level"])
	item.DecisionEffect = strings.TrimSpace(metadata["decision_effect"])
	item.RequestedByActor = approvalActorFromMetadata(metadata, "requested_by")
	item.TargetActor = approvalActorFromMetadata(metadata, "target")
	item.PayloadPreview = approvalPayloadPreview(metadata)
	item.Choices = approvalChoices(metadata)
	if raw := strings.TrimSpace(metadata["expires_at"]); raw != "" {
		if expiresAt, err := time.Parse(time.RFC3339, raw); err == nil {
			item.ExpiresAt = expiresAt
		}
	}
}

func approvalDisplayKind(approvalType string, metadata map[string]string) string {
	if value := strings.TrimSpace(metadata["approval_kind"]); value != "" {
		switch value {
		case "command_execution":
			return "command_execution"
		case "file_change":
			return "file_change"
		case "mcp_elicitation":
			return "user_input"
		default:
			return value
		}
	}
	switch approvalType {
	case "command_execution":
		return "command_execution"
	case "file_change":
		return "file_change"
	case "mcp_elicitation", "user_input":
		return "user_input"
	case "tool_access", "tool_use":
		return "tool_use"
	case "network_access":
		return "network_access"
	case "plan_approval":
		return "plan_approval"
	case "evidence_review":
		return "evidence_review"
	case "task_completion":
		return "task_completion"
	case "qa":
		return "qa"
	default:
		return "permission"
	}
}

func approvalDetails(metadata map[string]string) string {
	for _, key := range []string{"approval_details", "details", "reason", "approval_reason"} {
		if value := strings.TrimSpace(metadata[key]); value != "" {
			return value
		}
	}
	return ""
}

func approvalRiskSummary(riskLevel string, metadata map[string]string) string {
	if value := strings.TrimSpace(metadata["risk_summary"]); value != "" {
		return value
	}
	if scope := strings.TrimSpace(metadata["scope"]); scope != "" && strings.TrimSpace(riskLevel) != "" {
		return "Scope " + scope + " is marked " + strings.TrimSpace(riskLevel) + " risk."
	}
	return ""
}

func approvalPayloadPreview(metadata map[string]string) ApprovalPayloadDTO {
	kind := firstString(strings.TrimSpace(metadata["payload_kind"]), approvalDisplayKind(strings.TrimSpace(metadata["approval_type"]), metadata))
	preview := ApprovalPayloadDTO{
		Kind:     kind,
		Language: strings.TrimSpace(metadata["payload_language"]),
		Title:    strings.TrimSpace(metadata["payload_title"]),
		Content:  firstString(strings.TrimSpace(metadata["payload_preview"]), strings.TrimSpace(metadata["command"]), strings.TrimSpace(metadata["diff"]), strings.TrimSpace(metadata["prompt"]), strings.TrimSpace(metadata["question"])),
	}
	fields := make(map[string]string)
	for _, key := range []string{"approval_conversation_id", "approval_request_id", "approval_run_id", "approval_session_id", "approval_turn_id", "approval_tool_name", "approval_native_tool_name", "codex_method", "item_type", "scope", "path", "url", "server", "tool_id", "hil_decision_id"} {
		if value := strings.TrimSpace(metadata[key]); value != "" {
			fields[key] = value
		}
	}
	if len(fields) > 0 {
		preview.Fields = fields
	}
	if preview.Kind == "" && preview.Language == "" && preview.Title == "" && preview.Content == "" && len(preview.Fields) == 0 {
		return ApprovalPayloadDTO{}
	}
	return preview
}

func approvalChoices(metadata map[string]string) []ApprovalChoiceDTO {
	raw := strings.TrimSpace(metadata["choices_json"])
	if raw == "" {
		return nil
	}
	var decoded []ApprovalChoiceDTO
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil
	}
	out := make([]ApprovalChoiceDTO, 0, len(decoded))
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

func approvalActorFromMetadata(metadata map[string]string, prefix string) ApprovalActorRefDTO {
	return ApprovalActorRefDTO{
		ActorType: strings.TrimSpace(metadata[prefix+"_actor_type"]),
		ActorID:   strings.TrimSpace(metadata[prefix+"_actor_id"]),
	}
}

func filterApprovalsByStatus(items []ApprovalItemDTO, status string) []ApprovalItemDTO {
	filtered := items[:0]
	for _, item := range items {
		if item.Status == status {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func (s *ApprovalService) resolveQATaskID(ctx context.Context, viewer ViewerRef, missionID missioncore.MissionID, qaRef string) (missioncore.TaskID, error) {
	if s == nil || s.repo == nil {
		return "", ErrServiceDependency
	}
	model, err := loadMissionProjection(ctx, s.repo, viewer.Tenant(), missionID)
	if err != nil {
		return "", err
	}
	for _, check := range model.QA.Checks {
		if check.QARef == qaRef {
			return check.TaskID, nil
		}
	}
	for _, approval := range model.QA.Approvals {
		if approval.QARef == qaRef {
			return approval.TaskID, nil
		}
	}
	for _, rejection := range model.QA.Rejections {
		if rejection.QARef == qaRef {
			return rejection.TaskID, nil
		}
	}
	return "", ErrInvalidRequest
}

func approvalAction(actionID, label, path string, reasonRequired bool) ApprovalActionDTO {
	return ApprovalActionDTO{
		ActionID:             actionID,
		Label:                label,
		Style:                actionStyle(actionID),
		Decision:             actionDecision(actionID),
		Effect:               actionEffect(actionID),
		InputKind:            actionInputKind(actionID, reasonRequired),
		Method:               "POST",
		Path:                 path,
		RequiresBody:         true,
		ConfirmationRequired: true,
		ReasonRequired:       reasonRequired,
	}
}

func approvalActionsForRequest(approvalType, path string, metadata map[string]string) []ApprovalActionDTO {
	return []ApprovalActionDTO{
		actionWithOptions("approve_once", "Approve once", path, false, "primary", "approved", "once", "message"),
		actionWithOptions("approve_session", "Approve for session", path, false, "primary", "approved", "session", "message").withRemember(),
		actionWithOptions("deny", "Deny", path, false, "danger", "denied", "deny", "message"),
		actionWithOptions("other", "Other", path, false, "secondary", "approved", "once", "message"),
	}
}

func approvalActionForRequest(approvalType string, metadata map[string]string, actionID string) (ApprovalActionDTO, bool) {
	for _, action := range approvalActionsForRequest(approvalType, "", metadata) {
		if action.ActionID == actionID {
			return action, true
		}
	}
	switch actionID {
	case "approve":
		return actionWithOptions("approve", "Approve", "", false, "primary", "approved", "once", "message"), true
	case "reject":
		return actionWithOptions("reject", "Reject", "", true, "danger", "denied", "reject", "reason"), true
	}
	return ApprovalActionDTO{}, false
}

func actionWithOptions(actionID, label, path string, reasonRequired bool, style, decision, effect, inputKind string) ApprovalActionDTO {
	return ApprovalActionDTO{
		ActionID:             actionID,
		Label:                label,
		Style:                style,
		Decision:             decision,
		Effect:               effect,
		InputKind:            inputKind,
		Method:               "POST",
		Path:                 path,
		RequiresBody:         true,
		ConfirmationRequired: true,
		ReasonRequired:       reasonRequired,
	}
}

func (a ApprovalActionDTO) withModification() ApprovalActionDTO {
	a.AllowsModification = true
	return a
}

func (a ApprovalActionDTO) withRemember() ApprovalActionDTO {
	a.RememberAllowed = true
	return a
}

func actionStyle(actionID string) string {
	switch actionID {
	case "deny", "reject", "dismiss":
		return "danger"
	case "modify", "needs_context", "other":
		return "secondary"
	default:
		return "primary"
	}
}

func actionDecision(actionID string) string {
	switch actionID {
	case "deny", "reject", "dismiss":
		return "denied"
	case "modify", "needs_context":
		return "needs_input"
	case "answer":
		return "answered"
	default:
		return "approved"
	}
}

func actionEffect(actionID string) string {
	switch actionID {
	case "approve_session":
		return "session"
	case "approve_edited":
		return "modified"
	case "modify":
		return "modify"
	case "needs_context":
		return "needs_context"
	case "deny", "reject":
		return "deny"
	default:
		return "once"
	}
}

func actionInputKind(actionID string, reasonRequired bool) string {
	switch actionID {
	case "modify", "approve_edited":
		return "modified_input"
	case "answer":
		return "choice_or_text"
	case "deny", "reject", "needs_context":
		return "reason"
	default:
		if reasonRequired {
			return "reason"
		}
		return "message"
	}
}

func decisionMetadata(requestMetadata map[string]string, cmd ApprovalDecisionCommand, action ApprovalActionDTO) map[string]string {
	metadata := cloneStringMap(requestMetadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	putDecisionMetadata(metadata, "decision_action_id", cmd.ActionID)
	putDecisionMetadata(metadata, "decision_effect", action.Effect)
	putDecisionMetadata(metadata, "decision_input_kind", action.InputKind)
	putDecisionMetadata(metadata, "decision_message", cmd.Message)
	putDecisionMetadata(metadata, "decision_choice_id", cmd.ChoiceID)
	putDecisionMetadata(metadata, "decision_modified_input", cmd.ModifiedInput)
	if cmd.Remember {
		metadata["decision_remember"] = "true"
	}
	return metadata
}

func putDecisionMetadata(metadata map[string]string, key, value string) {
	if strings.TrimSpace(value) != "" {
		metadata[key] = strings.TrimSpace(value)
	}
}

func parseApprovalID(approvalID string) (string, string, string, error) {
	parts := strings.Split(approvalID, ":")
	if len(parts) == 2 && parts[0] == "inbox" {
		return parts[0], "", parts[1], nil
	}
	if len(parts) == 3 && isApprovalKind(parts[0]) {
		return parts[0], parts[1], parts[2], nil
	}
	return "", "", "", ErrInvalidRequest
}

func isApprovalKind(kind string) bool {
	switch kind {
	case "qa", "permission_request", "tool_access", "evidence_review", "task_completion", "command_execution", "file_change", "mcp_elicitation", "tool_use", "network_access", "plan_approval", "user_input":
		return true
	default:
		return false
	}
}
