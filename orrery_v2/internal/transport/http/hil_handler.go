package httptransport

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/hil"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

type createHILDecisionBody struct {
	Action           string           `json:"action"`
	ActionTypeID     string           `json:"action_type_id,omitempty"`
	Resource         hil.ObjectRef    `json:"resource"`
	ObjectRefs       []hil.ObjectRef  `json:"object_refs,omitempty"`
	ArgsHash         string           `json:"args_hash"`
	Purpose          string           `json:"purpose,omitempty"`
	RiskLevel        hil.RiskLevel    `json:"risk_level,omitempty"`
	PreferredMode    hil.DecisionMode `json:"preferred_mode,omitempty"`
	ExplicitDeny     bool             `json:"explicit_deny,omitempty"`
	HILRequired      bool             `json:"hil_required,omitempty"`
	ApprovalRequired bool             `json:"approval_required,omitempty"`
	CouncilRequired  bool             `json:"council_required,omitempty"`
	SubjectRefs      []hil.ObjectRef  `json:"subject_refs"`
	OntologyQuery    string           `json:"ontology_query,omitempty"`
	PayloadPreview   map[string]any   `json:"payload_preview,omitempty"`
	ExpiresAt        time.Time        `json:"expires_at,omitempty"`
}

type submitHILContributionBody struct {
	Position     string   `json:"position"`
	Reasoning    string   `json:"reasoning"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
}

type castHILVoteBody struct {
	Choice hil.VoteChoice `json:"choice"`
	Reason string         `json:"reason,omitempty"`
}

type resolveHILDecisionBody struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
	ActionID string `json:"action_id,omitempty"`
}

func (r *Router) handleCreateHILDecision(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.hilViewer(w, req)
	if !ok {
		return
	}
	key := idempotencyKey(req)
	if key == "" {
		status, body := invalidRequest(cid, "idempotency_key", "idempotency key is required for mutating commands")
		writeJSON(w, status, body)
		return
	}
	var body createHILDecisionBody
	if err := decodeStrictJSON(req, &body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	result, err := r.hil.CreateDecision(req.Context(), hil.CreateDecisionInput{
		TenantID: viewer.Tenant(), Requester: hilActorFromViewer(viewer), Action: body.Action, ActionTypeID: body.ActionTypeID,
		Resource: body.Resource, ObjectRefs: body.ObjectRefs, ArgsHash: body.ArgsHash, Purpose: body.Purpose,
		RiskLevel: body.RiskLevel, PreferredMode: body.PreferredMode, ExplicitDeny: body.ExplicitDeny,
		HILRequired: body.HILRequired, ApprovalRequired: body.ApprovalRequired, CouncilRequired: body.CouncilRequired,
		SubjectRefs: body.SubjectRefs, OntologyQuery: body.OntologyQuery, PayloadPreview: body.PayloadPreview,
		IdempotencyKey: key, ExpiresAt: body.ExpiresAt,
	})
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (r *Router) handleListHILDecisions(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.hilViewer(w, req)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(req.URL.Query().Get("offset"))
	mine := false
	if raw := strings.TrimSpace(req.URL.Query().Get("mine")); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			status, response := invalidRequest(cid, "mine", "mine must be a boolean")
			writeJSON(w, status, response)
			return
		}
		mine = parsed
	}
	requestedByMe := false
	if raw := strings.TrimSpace(req.URL.Query().Get("requested_by_me")); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			status, response := invalidRequest(cid, "requested_by_me", "requested_by_me must be a boolean")
			writeJSON(w, status, response)
			return
		}
		requestedByMe = parsed
	}
	mode := hil.DecisionMode(req.URL.Query().Get("mode"))
	canManageApprovals := viewer.CanSeeAny([]string{permissionApprovalsManage})
	if requestedByMe {
		mine = false
	} else if !canManageApprovals {
		mine = true
	}
	if mine && mode != hil.ModeCouncil && mode != hil.ModeHIL && mode != hil.ModeApproval {
		status, response := invalidRequest(cid, "mine", "mine requires a HIL, approval, or council decision mode")
		writeJSON(w, status, response)
		return
	}
	actor := hilActorFromViewer(viewer)
	query := hil.DecisionListQuery{
		TenantID: viewer.Tenant(), Mode: mode, Status: hil.DecisionStatus(req.URL.Query().Get("status")),
		Mine: mine, ActionableBy: actor, Limit: limit, Offset: offset,
	}
	if requestedByMe {
		query.RequestedBy = actor
	}
	if (mine || requestedByMe) && actor.Kind == hil.ActorHuman && viewer.Tenant() != hil.BuiltInWorkflowTenantID {
		query.AdditionalTenantIDs = []string{hil.BuiltInWorkflowTenantID}
	}
	page, err := r.hil.ListDecisions(req.Context(), query)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	if r.discussions != nil {
		discussionViewer, viewerOK := discussionViewerFromRequest(w, req)
		if !viewerOK {
			return
		}
		page = r.enrichHILDecisionPage(req.Context(), discussionViewer, page)
	}
	writeJSON(w, http.StatusOK, page)
}

func (r *Router) handleGetHILDecision(w http.ResponseWriter, req *http.Request, decisionID string) {
	cid := correlationID(req)
	viewer, ok := r.hilViewer(w, req)
	if !ok {
		return
	}
	decision, _, err := r.getHILDecisionForViewer(req.Context(), viewer, decisionID)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	if !hil.DecisionResolutionActorAllowed(decision, hilActorFromViewer(viewer)) {
		status, response := MapError(hil.ErrParticipantRequired, cid)
		writeJSON(w, status, response)
		return
	}
	if r.discussions != nil {
		discussionViewer, viewerOK := discussionViewerFromRequest(w, req)
		if !viewerOK {
			return
		}
		decision = r.enrichHILDecision(req.Context(), discussionViewer, decision, map[string]discussionsession.ConversationDTO{}, map[string]bool{})
	}
	writeJSON(w, http.StatusOK, decision)
}

func (r *Router) enrichHILDecisionPage(ctx context.Context, viewer discussionsession.ViewerRef, page hil.DecisionPage) hil.DecisionPage {
	conversations := map[string]discussionsession.ConversationDTO{}
	loaded := map[string]bool{}
	for index := range page.Items {
		page.Items[index] = r.enrichHILDecision(ctx, viewer, page.Items[index], conversations, loaded)
	}
	return page
}

func (r *Router) enrichHILDecision(
	ctx context.Context,
	viewer discussionsession.ViewerRef,
	decision hil.DecisionRequest,
	conversations map[string]discussionsession.ConversationDTO,
	loaded map[string]bool,
) hil.DecisionRequest {
	if r == nil || r.discussions == nil || hilDecisionHasReadablePayload(decision.PayloadPreview) {
		return decision
	}
	conversationID := hilDecisionConversationID(decision)
	if conversationID == "" {
		return decision
	}
	conversation, exists := conversations[conversationID]
	if !loaded[conversationID] {
		loaded[conversationID] = true
		var err error
		conversation, err = r.discussions.GetConversation(ctx, viewer, conversationID)
		if err != nil {
			return decision
		}
		conversations[conversationID] = conversation
	} else if !exists {
		return decision
	}

	metadata := hilDecisionPayloadMetadata(decision.PayloadPreview)
	toolCall := discussionApprovalToolCall(conversation, metadata)
	toolArguments := map[string]string{}
	if toolCall != nil {
		toolArguments = toolCall.Arguments
		if strings.TrimSpace(metadata["approval_tool_name"]) == "" {
			metadata["approval_tool_name"] = strings.TrimSpace(toolCall.Name)
		}
	}
	kind := firstHTTPString(metadata["approval_kind"], decision.ActionTypeID)
	toolName := firstHTTPString(metadata["approval_tool_name"], metadata["approval_native_tool_name"])
	preview := discussionApprovalPayloadPreview(kind, toolName, metadata, toolArguments)
	requestContext := discussionApprovalRequestContext(conversation, metadata)
	content := strings.TrimSpace(preview.Content)
	if content == "" {
		content = requestContext
	}
	if content == "" {
		return decision
	}

	payload := make(map[string]any, len(decision.PayloadPreview)+5)
	for key, value := range decision.PayloadPreview {
		payload[key] = value
	}
	payload["content"] = content
	if _, ok := payload["title"]; !ok {
		payload["title"] = firstHTTPString(strings.TrimSpace(conversation.Title), strings.TrimSpace(preview.Title))
	}
	if strings.TrimSpace(preview.Language) != "" {
		if _, ok := payload["language"]; !ok {
			payload["language"] = preview.Language
		}
	}
	if requestContext != "" && requestContext != content {
		payload["request_context"] = requestContext
	}
	if safeArguments := discussionApprovalSafeArguments(toolArguments); len(safeArguments) > 0 {
		payload["fields"] = mergeHILReadableFields(payload["fields"], safeArguments)
	}
	decision.PayloadPreview = payload
	return decision
}

func hilDecisionHasReadablePayload(payload map[string]any) bool {
	metadata := hilDecisionPayloadMetadata(payload)
	for _, key := range append([]string{"content", "payload_preview", "approval_summary", "approval_description"}, discussionApprovalIntentFieldKeys...) {
		if hilApprovalDisplayValueIsReadable(metadata[key]) {
			return true
		}
	}
	if fields, ok := payload["fields"].(map[string]any); ok {
		for _, key := range discussionApprovalIntentFieldKeys {
			if hilApprovalDisplayValueIsReadable(hilPayloadString(fields[key])) {
				return true
			}
		}
	} else if fields, ok := payload["fields"].(map[string]string); ok {
		for _, key := range discussionApprovalIntentFieldKeys {
			if hilApprovalDisplayValueIsReadable(fields[key]) {
				return true
			}
		}
	}
	return false
}

func hilApprovalDisplayValueIsReadable(value string) bool {
	value = strings.TrimSpace(value)
	return approvalDisplayValueIsSafe(value) && !strings.HasPrefix(strings.ToLower(value), "runtime approval required for")
}

func hilDecisionConversationID(decision hil.DecisionRequest) string {
	if value := hilPayloadString(decision.PayloadPreview["approval_conversation_id"]); value != "" {
		return value
	}
	for _, ref := range decision.ObjectRefs {
		if strings.EqualFold(strings.TrimSpace(ref.Kind), "conversation") {
			return strings.TrimSpace(ref.ID)
		}
	}
	return ""
}

func hilDecisionPayloadMetadata(payload map[string]any) map[string]string {
	metadata := map[string]string{}
	for key, value := range payload {
		if text := hilPayloadString(value); text != "" {
			metadata[key] = text
		}
	}
	return metadata
}

func hilPayloadString(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func discussionApprovalRequestContext(conversation discussionsession.ConversationDTO, metadata map[string]string) string {
	run := discussionApprovalRun(conversation, metadata)
	if run == nil || strings.TrimSpace(run.TurnID) == "" {
		return ""
	}
	for index := len(conversation.Messages) - 1; index >= 0; index-- {
		message := conversation.Messages[index]
		if strings.TrimSpace(message.TurnID) != strings.TrimSpace(run.TurnID) {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role != "user" && role != "human" {
			continue
		}
		if content := strings.TrimSpace(message.Content); approvalDisplayValueIsSafe(content) {
			return content
		}
	}
	return ""
}

func mergeHILReadableFields(existing any, readable map[string]string) map[string]any {
	fields := map[string]any{}
	if current, ok := existing.(map[string]any); ok {
		for key, value := range current {
			fields[key] = value
		}
	} else if current, ok := existing.(map[string]string); ok {
		for key, value := range current {
			fields[key] = value
		}
	}
	for key, value := range readable {
		fields[key] = value
	}
	return fields
}

func (r *Router) handleResolveHILDecision(w http.ResponseWriter, req *http.Request, decisionID string) {
	cid := correlationID(req)
	viewer, ok := r.hilMutationViewer(w, req)
	if !ok {
		return
	}
	resolver, ok := r.hil.(HILDecisionResolver)
	if !ok || resolver == nil {
		status, response := MapError(errServiceDependency(), cid)
		writeJSON(w, status, response)
		return
	}
	var body resolveHILDecisionBody
	if err := decodeStrictJSON(req, &body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	_, decisionTenantID, err := r.getHILDecisionForViewer(req.Context(), viewer, decisionID)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	actor := hilActorFromViewer(viewer)
	resolution, err := resolver.ResolveDecision(req.Context(), decisionTenantID, decisionID, actor, hil.ResolveDecisionInput{
		Decision: body.Decision, Reason: body.Reason, IdempotencyKey: idempotencyKey(req),
	})
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, resolution)
}

func (r *Router) getHILDecisionForViewer(ctx context.Context, viewer missioncontrol.ViewerRef, decisionID string) (hil.DecisionRequest, string, error) {
	tenantID := viewer.Tenant()
	decision, err := r.hil.GetDecision(ctx, tenantID, decisionID)
	if err == nil || !errors.Is(err, hil.ErrNotFound) || tenantID == hil.BuiltInWorkflowTenantID {
		return decision, tenantID, err
	}
	actor := hilActorFromViewer(viewer)
	if actor.Kind != hil.ActorHuman || strings.TrimSpace(actor.ID) == "" {
		return hil.DecisionRequest{}, tenantID, hil.ErrNotFound
	}
	decision, err = r.hil.GetDecision(ctx, hil.BuiltInWorkflowTenantID, decisionID)
	if err != nil {
		return decision, hil.BuiltInWorkflowTenantID, err
	}
	page, err := r.hil.ListDecisions(ctx, hil.DecisionListQuery{
		TenantID: hil.BuiltInWorkflowTenantID, DecisionID: decision.ID, Mode: decision.Route.Mode,
		Mine: true, ActionableBy: actor, Limit: 1,
	})
	if err != nil {
		return hil.DecisionRequest{}, hil.BuiltInWorkflowTenantID, err
	}
	for _, actionable := range page.Items {
		if actionable.ID == decision.ID {
			return decision, hil.BuiltInWorkflowTenantID, nil
		}
	}
	return hil.DecisionRequest{}, tenantID, hil.ErrNotFound
}

func (r *Router) handleGetHILCouncil(w http.ResponseWriter, req *http.Request, councilID string) {
	cid := correlationID(req)
	viewer, ok := r.hilViewer(w, req)
	if !ok {
		return
	}
	council, err := r.hil.GetCouncil(req.Context(), viewer.Tenant(), councilID)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, redactHILCouncil(council, hilActorFromViewer(viewer)))
}

func (r *Router) handleSubmitHILContribution(w http.ResponseWriter, req *http.Request, councilID string) {
	cid := correlationID(req)
	viewer, ok := r.hilMutationViewer(w, req)
	if !ok {
		return
	}
	var body submitHILContributionBody
	if err := decodeStrictJSON(req, &body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	err := r.hil.SubmitContribution(req.Context(), viewer.Tenant(), councilID, hilActorFromViewer(viewer), hil.SubmitContributionInput{
		Position: body.Position, Reasoning: body.Reasoning, EvidenceRefs: body.EvidenceRefs, IdempotencyKey: idempotencyKey(req),
	})
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	council, err := r.hil.GetCouncil(req.Context(), viewer.Tenant(), councilID)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, redactHILCouncil(council, hilActorFromViewer(viewer)))
}

func (r *Router) handleCastHILVote(w http.ResponseWriter, req *http.Request, councilID string) {
	cid := correlationID(req)
	viewer, ok := r.hilMutationViewer(w, req)
	if !ok {
		return
	}
	var body castHILVoteBody
	if err := decodeStrictJSON(req, &body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	council, err := r.hil.CastVote(req.Context(), viewer.Tenant(), councilID, hilActorFromViewer(viewer), hil.CastVoteInput{
		Choice: body.Choice, Reason: body.Reason, IdempotencyKey: idempotencyKey(req),
	})
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, redactHILCouncil(council, hilActorFromViewer(viewer)))
}

func (r *Router) hilViewer(w http.ResponseWriter, req *http.Request) (missioncontrol.ViewerRef, bool) {
	cid := correlationID(req)
	if r.hil == nil {
		status, response := MapError(errServiceDependency(), cid)
		writeJSON(w, status, response)
		return missioncontrol.ViewerRef{}, false
	}
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return missioncontrol.ViewerRef{}, false
	}
	if strings.TrimSpace(viewer.TenantID) == "" {
		viewer.TenantID = "system"
	}
	return viewer, true
}

func (r *Router) hilMutationViewer(w http.ResponseWriter, req *http.Request) (missioncontrol.ViewerRef, bool) {
	viewer, ok := r.hilViewer(w, req)
	if !ok {
		return missioncontrol.ViewerRef{}, false
	}
	if idempotencyKey(req) == "" {
		status, response := invalidRequest(correlationID(req), "idempotency_key", "idempotency key is required for mutating commands")
		writeJSON(w, status, response)
		return missioncontrol.ViewerRef{}, false
	}
	return viewer, true
}

func hilActorFromViewer(viewer missioncontrol.ViewerRef) hil.ActorRef {
	kind := hil.ActorService
	id := strings.TrimSpace(viewer.ViewerID)
	switch viewer.ViewerKind {
	case missioncontrol.ViewerKindAgent:
		kind = hil.ActorAgent
	case missioncontrol.ViewerKindHuman, missioncontrol.ViewerKindUser:
		kind = hil.ActorHuman
		if subjectID := strings.TrimSpace(viewer.AuthorizationSubjectID); subjectID != "" {
			id = subjectID
		}
	}
	return hil.ActorRef{Kind: kind, ID: id}
}

func redactHILCouncil(council hil.CouncilSession, actor hil.ActorRef) hil.CouncilSession {
	if council.Status != hil.CouncilOpen || len(council.Contributions) >= len(council.Participants) {
		return council
	}
	actorKey := string(actor.Kind) + ":" + actor.ID
	contributions := make([]hil.CouncilContribution, 0, 1)
	for _, contribution := range council.Contributions {
		if string(contribution.Actor.Kind)+":"+contribution.Actor.ID == actorKey {
			contributions = append(contributions, contribution)
		}
	}
	votes := make([]hil.CouncilVote, 0, 1)
	for _, vote := range council.Votes {
		if string(vote.Actor.Kind)+":"+vote.Actor.ID == actorKey {
			votes = append(votes, vote)
		}
	}
	council.Contributions = contributions
	council.Votes = votes
	return council
}
