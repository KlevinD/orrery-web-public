package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/app/hil"
	gwprotocol "orrery_v2/internal/gateway/protocol"
	platformdb "orrery_v2/internal/platform/db"
)

var (
	errPersonalAssistantApprovalReplyNotFound      = errors.New("personal assistant approval reply target was not found")
	errPersonalAssistantApprovalReplyNotActionable = errors.New("personal assistant approval reply target is no longer actionable")
)

type productionPersonalAssistantApprovalReplyInput struct {
	TenantID       string
	HumanProfileID string
	AccountID      string
	MessageID      string
	Decision       hil.WorkflowTaskDecision
}

type productionPersonalAssistantApprovalReplyResult struct {
	Title    string
	Decision hil.WorkflowTaskDecision
}

type productionPersonalAssistantApprovalReplyHandler interface {
	ResolveReply(context.Context, productionPersonalAssistantApprovalReplyInput) (productionPersonalAssistantApprovalReplyResult, error)
}

func (r *productionMessageGatewayRunner) SetPersonalAssistantApprovalReplyHandler(handler productionPersonalAssistantApprovalReplyHandler) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.approvalReplies = handler
}

func (r *productionMessageGatewayRunner) handlePersonalAssistantApprovalReply(
	ctx context.Context,
	input gwprotocol.SubmitTurnInput,
	identity productionGatewayWeixinIdentity,
	decision hil.WorkflowTaskDecision,
	out chan<- gwprotocol.Event,
) string {
	r.mu.Lock()
	handler := r.approvalReplies
	r.mu.Unlock()
	if handler == nil {
		return "The approval service is unavailable. No approval decision was executed."
	}
	result, err := handler.ResolveReply(ctx, productionPersonalAssistantApprovalReplyInput{
		TenantID: identity.TenantID, HumanProfileID: identity.ViewerID,
		AccountID: strings.TrimSpace(input.Telemetry["weixin_account_id"]),
		MessageID: firstNonBlank(strings.TrimSpace(input.Telemetry["weixin_message_id"]), strings.TrimSpace(input.RunID)),
		Decision:  decision,
	})
	if err != nil {
		switch {
		case errors.Is(err, errPersonalAssistantApprovalReplyNotFound):
			return "There is no pending approval for this reply. No approval decision was executed."
		case errors.Is(err, errPersonalAssistantApprovalReplyNotActionable):
			return "The approval is already resolved or expired. This reply did not execute a new approval decision."
		default:
			return "The approval service returned an error. Completion of the approval decision could not be confirmed; do not claim it succeeded or automatically retry it."
		}
	}
	verb := "已同意："
	finishReason := "approval_reply_approved"
	if result.Decision == hil.WorkflowTaskReject {
		verb = "已拒绝："
		finishReason = "approval_reply_rejected"
	}
	emitPersonalAssistantApprovalReplyMessage(out, verb+firstNonBlank(strings.TrimSpace(result.Title), "这项审批")+"。", finishReason)
	return ""
}

func emitPersonalAssistantApprovalReplyMessage(out chan<- gwprotocol.Event, message, finishReason string) {
	out <- gwprotocol.Event{Type: gwprotocol.EventAssistantTextDelta, Text: message}
	out <- gwprotocol.Event{Type: gwprotocol.EventTurnCompleted, FinishReason: finishReason}
}

type productionPersonalAssistantApprovalDecisionService interface {
	ResolveDecision(context.Context, string, string, hil.ActorRef, hil.ResolveDecisionInput) (hil.DecisionResolution, error)
	DecideWorkflowTask(context.Context, string, string, hil.ActorRef, hil.DecideWorkflowTaskInput) (hil.WorkflowInstanceDetail, error)
}

type productionPersonalAssistantApprovalReplyClaim struct {
	ApprovalTenant          string
	DecisionID              string
	TaskID                  string
	ExpectedWorkflowVersion int64
	Decision                hil.WorkflowTaskDecision
	Title                   string
	IdempotencyKey          string
}

type productionPersonalAssistantApprovalReplyClaimStore interface {
	ClaimLatest(context.Context, productionPersonalAssistantApprovalReplyInput) (productionPersonalAssistantApprovalReplyClaim, bool, error)
}

type productionPersonalAssistantApprovalReplyService struct {
	claims    productionPersonalAssistantApprovalReplyClaimStore
	decisions productionPersonalAssistantApprovalDecisionService
}

func (s productionPersonalAssistantApprovalReplyService) ResolveReply(ctx context.Context, input productionPersonalAssistantApprovalReplyInput) (productionPersonalAssistantApprovalReplyResult, error) {
	if s.claims == nil || s.decisions == nil {
		return productionPersonalAssistantApprovalReplyResult{}, fmt.Errorf("personal assistant approval reply dependencies are required")
	}
	claim, found, err := s.claims.ClaimLatest(ctx, input)
	if err != nil {
		return productionPersonalAssistantApprovalReplyResult{}, err
	}
	if !found {
		return productionPersonalAssistantApprovalReplyResult{}, errPersonalAssistantApprovalReplyNotFound
	}
	actor := hil.ActorRef{Kind: hil.ActorHuman, ID: strings.TrimSpace(input.HumanProfileID)}
	reason := ""
	if claim.Decision == hil.WorkflowTaskReject {
		reason = "用户通过微信回复拒绝"
	}
	if claim.TaskID == "" {
		_, err = s.decisions.ResolveDecision(ctx, claim.ApprovalTenant, claim.DecisionID, actor, hil.ResolveDecisionInput{
			Decision: string(claim.Decision), Reason: reason, IdempotencyKey: claim.IdempotencyKey,
		})
	} else {
		_, err = s.decisions.DecideWorkflowTask(ctx, claim.ApprovalTenant, claim.TaskID, actor, hil.DecideWorkflowTaskInput{
			Decision: claim.Decision, Reason: reason, IdempotencyKey: claim.IdempotencyKey,
			ExpectedInstanceVersion: claim.ExpectedWorkflowVersion,
		})
	}
	if err != nil {
		if errors.Is(err, hil.ErrConflict) || errors.Is(err, hil.ErrNotFound) ||
			errors.Is(err, hil.ErrParticipantRequired) || errors.Is(err, hil.ErrInvalidRequest) {
			return productionPersonalAssistantApprovalReplyResult{}, errPersonalAssistantApprovalReplyNotActionable
		}
		return productionPersonalAssistantApprovalReplyResult{}, err
	}
	return productionPersonalAssistantApprovalReplyResult{
		Title: firstNonBlank(strings.TrimSpace(claim.Title), "这项审批"), Decision: claim.Decision,
	}, nil
}

type productionPersonalAssistantApprovalReplyStore struct {
	core *platformdb.Store
	jobs *platformdb.Store
	now  func() time.Time

	schemaMu    sync.Mutex
	schemaReady bool
}

func newProductionPersonalAssistantApprovalReplyStore(core, jobs *platformdb.Store) *productionPersonalAssistantApprovalReplyStore {
	return &productionPersonalAssistantApprovalReplyStore{
		core: core, jobs: jobs, now: func() time.Time { return time.Now().UTC() },
	}
}

func (s *productionPersonalAssistantApprovalReplyStore) EnsureSchema(ctx context.Context) error {
	if s == nil || s.core == nil || s.jobs == nil {
		return fmt.Errorf("personal assistant approval reply store is unavailable")
	}
	s.schemaMu.Lock()
	defer s.schemaMu.Unlock()
	if s.schemaReady {
		return nil
	}
	timeType := "INTEGER"
	if s.jobs.Dialect() == platformdb.DialectPostgres {
		timeType = "BIGINT"
	}
	if _, err := s.jobs.Exec(ctx, `CREATE TABLE IF NOT EXISTS gateway_weixin_approval_reply_claims (
		binding_tenant_id TEXT NOT NULL,
		human_profile_id TEXT NOT NULL,
		account_id TEXT NOT NULL,
		inbound_message_id TEXT NOT NULL,
		approval_tenant_id TEXT NOT NULL,
		decision_id TEXT NOT NULL,
		workflow_task_id TEXT NOT NULL DEFAULT '',
		expected_workflow_version BIGINT NOT NULL DEFAULT 0,
		decision TEXT NOT NULL,
		title TEXT NOT NULL,
		idempotency_key TEXT NOT NULL,
		created_at `+timeType+` NOT NULL,
		PRIMARY KEY (binding_tenant_id, human_profile_id, account_id, inbound_message_id)
	)`); err != nil {
		return fmt.Errorf("ensure personal assistant approval reply schema: %w", err)
	}
	s.schemaReady = true
	return nil
}

func (s *productionPersonalAssistantApprovalReplyStore) ClaimLatest(ctx context.Context, input productionPersonalAssistantApprovalReplyInput) (productionPersonalAssistantApprovalReplyClaim, bool, error) {
	input = normalizeProductionPersonalAssistantApprovalReplyInput(input)
	if !validProductionPersonalAssistantApprovalReplyInput(input) {
		return productionPersonalAssistantApprovalReplyClaim{}, false, fmt.Errorf("personal assistant approval reply input is invalid")
	}
	if err := s.EnsureSchema(ctx); err != nil {
		return productionPersonalAssistantApprovalReplyClaim{}, false, err
	}
	if existing, found, err := s.loadClaim(ctx, input); err != nil || found {
		return existing, found, err
	}

	keys, err := s.recentDeliveredApprovalKeys(ctx, input, 100)
	if err != nil {
		return productionPersonalAssistantApprovalReplyClaim{}, false, err
	}
	for _, key := range keys {
		reference, ok := parseProductionPersonalAssistantApprovalNotificationKey(key)
		if !ok || reference.HumanProfileID != input.HumanProfileID ||
			(reference.ApprovalTenant != input.TenantID && reference.ApprovalTenant != productionHILTenantID) {
			continue
		}
		candidate, workflowVersion, found, candidateErr := s.pendingCandidate(ctx, reference, input.HumanProfileID)
		if candidateErr != nil {
			return productionPersonalAssistantApprovalReplyClaim{}, false, candidateErr
		}
		if !found {
			continue
		}
		claim := productionPersonalAssistantApprovalReplyClaim{
			ApprovalTenant:          reference.ApprovalTenant,
			DecisionID:              reference.DecisionID,
			TaskID:                  reference.TaskID,
			ExpectedWorkflowVersion: workflowVersion,
			Decision:                input.Decision,
			Title:                   productionPersonalAssistantApprovalPresentationFor(candidate).Title,
			IdempotencyKey:          productionPersonalAssistantApprovalReplyIdempotencyKey(input),
		}
		if err := s.insertClaim(ctx, input, claim); err != nil {
			return productionPersonalAssistantApprovalReplyClaim{}, false, err
		}
		return s.loadClaim(ctx, input)
	}
	return productionPersonalAssistantApprovalReplyClaim{}, false, nil
}

func normalizeProductionPersonalAssistantApprovalReplyInput(input productionPersonalAssistantApprovalReplyInput) productionPersonalAssistantApprovalReplyInput {
	input.TenantID = strings.TrimSpace(input.TenantID)
	input.HumanProfileID = strings.TrimSpace(input.HumanProfileID)
	input.AccountID = strings.TrimSpace(input.AccountID)
	input.MessageID = strings.TrimSpace(input.MessageID)
	return input
}

func validProductionPersonalAssistantApprovalReplyInput(input productionPersonalAssistantApprovalReplyInput) bool {
	return input.TenantID != "" && input.HumanProfileID != "" && input.AccountID != "" && input.MessageID != "" &&
		(input.Decision == hil.WorkflowTaskApprove || input.Decision == hil.WorkflowTaskReject)
}

func (s *productionPersonalAssistantApprovalReplyStore) recentDeliveredApprovalKeys(ctx context.Context, input productionPersonalAssistantApprovalReplyInput, limit int) ([]string, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.jobs.Query(ctx, `SELECT idempotency_key
		FROM gateway_message_deliveries
		WHERE tenant_id = ? AND channel_key = 'weixin' AND account_id = ? AND human_profile_id = ?
		 AND event_type = 'approval' AND status = 'delivered'
		ORDER BY delivered_at DESC, created_at DESC, id DESC
		LIMIT ?`, input.TenantID, input.AccountID, input.HumanProfileID, limit)
	if err != nil {
		return nil, fmt.Errorf("list delivered personal assistant approvals: %w", err)
	}
	defer rows.Close()
	keys := make([]string, 0, limit)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("scan delivered personal assistant approval: %w", err)
		}
		keys = append(keys, strings.TrimSpace(key))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate delivered personal assistant approvals: %w", err)
	}
	return keys, nil
}

type productionPersonalAssistantApprovalNotificationReference struct {
	ApprovalTenant string
	DecisionID     string
	TaskID         string
	HumanProfileID string
}

func parseProductionPersonalAssistantApprovalNotificationKey(value string) (productionPersonalAssistantApprovalNotificationReference, bool) {
	const legacyPrefix = "personal-assistant:approval:"
	const oldestVersionedPrefix = "personal-assistant:approval:v2:"
	const previousPrefix = "personal-assistant:approval:v3:"
	const lastTemplatePrefix = "personal-assistant:approval:v4:"
	const currentPrefix = "personal-assistant:approval:v5:"
	const recipientMarker = ":recipient:"
	const taskMarker = ":task:"
	value = strings.TrimSpace(value)
	prefix := ""
	if strings.HasPrefix(value, currentPrefix) {
		prefix = currentPrefix
	} else if strings.HasPrefix(value, lastTemplatePrefix) {
		prefix = lastTemplatePrefix
	} else if strings.HasPrefix(value, previousPrefix) {
		prefix = previousPrefix
	} else if strings.HasPrefix(value, oldestVersionedPrefix) {
		prefix = oldestVersionedPrefix
	} else if strings.HasPrefix(value, legacyPrefix) {
		prefix = legacyPrefix
	}
	if prefix == "" {
		return productionPersonalAssistantApprovalNotificationReference{}, false
	}
	remainder := strings.TrimPrefix(value, prefix)
	recipientIndex := strings.LastIndex(remainder, recipientMarker)
	if recipientIndex <= 0 {
		return productionPersonalAssistantApprovalNotificationReference{}, false
	}
	profileID := strings.TrimSpace(remainder[recipientIndex+len(recipientMarker):])
	identity := strings.TrimSpace(remainder[:recipientIndex])
	taskID := ""
	if taskIndex := strings.LastIndex(identity, taskMarker); taskIndex > 0 {
		taskID = strings.TrimSpace(identity[taskIndex+len(taskMarker):])
		identity = strings.TrimSpace(identity[:taskIndex])
	}
	decisionIndex := strings.LastIndex(identity, ":")
	if decisionIndex <= 0 {
		return productionPersonalAssistantApprovalNotificationReference{}, false
	}
	reference := productionPersonalAssistantApprovalNotificationReference{
		ApprovalTenant: strings.TrimSpace(identity[:decisionIndex]),
		DecisionID:     strings.TrimSpace(identity[decisionIndex+1:]),
		TaskID:         taskID,
		HumanProfileID: profileID,
	}
	if reference.ApprovalTenant == "" || reference.DecisionID == "" || reference.HumanProfileID == "" ||
		strings.Contains(identity, taskMarker) || strings.Contains(remainder[recipientIndex+len(recipientMarker):], recipientMarker) {
		return productionPersonalAssistantApprovalNotificationReference{}, false
	}
	return reference, true
}

func (s *productionPersonalAssistantApprovalReplyStore) pendingCandidate(ctx context.Context, reference productionPersonalAssistantApprovalNotificationReference, humanProfileID string) (productionPersonalAssistantApprovalCandidate, int64, bool, error) {
	const decisionColumns = `decision.id, decision.tenant_id, decision.requester_kind, decision.requester_id,
		decision.action, decision.action_type_id, decision.purpose, decision.risk_level, decision.route_mode,
		decision.payload_preview_json, decision.created_at, decision.expires_at`
	var row *sql.Row
	if reference.TaskID == "" {
		row = s.core.QueryRow(ctx, `SELECT `+decisionColumns+`, '', '', 0, NULL, '', ''
			FROM hil_decision_requests AS decision
			WHERE decision.tenant_id = ? AND decision.id = ? AND decision.status = ?
			 AND decision.route_mode IN (?, ?)
			 AND NOT EXISTS (SELECT 1 FROM hil_workflow_instances AS workflow
			  WHERE workflow.tenant_id = decision.tenant_id AND workflow.decision_request_id = decision.id)
			LIMIT 1`, reference.ApprovalTenant, reference.DecisionID, hil.DecisionPending, hil.ModeHIL, hil.ModeApproval)
	} else {
		row = s.core.QueryRow(ctx, `SELECT `+decisionColumns+`, workflow.id, task.id, workflow.version,
			task.deadline_at, task.current_assignee_kind, task.current_assignee_id
			FROM hil_decision_requests AS decision
			JOIN hil_workflow_instances AS workflow
			 ON workflow.tenant_id = decision.tenant_id AND workflow.decision_request_id = decision.id
			JOIN hil_workflow_tasks AS task
			 ON task.tenant_id = workflow.tenant_id AND task.workflow_instance_id = workflow.id
			WHERE decision.tenant_id = ? AND decision.id = ? AND decision.status = ?
			 AND decision.route_mode IN (?, ?) AND workflow.status = ?
			 AND task.id = ? AND task.status = ? AND task.current_assignee_kind = ? AND task.current_assignee_id = ?
			LIMIT 1`, reference.ApprovalTenant, reference.DecisionID, hil.DecisionPending, hil.ModeHIL, hil.ModeApproval,
			hil.WorkflowRunning, reference.TaskID, hil.TaskPending, hil.ActorHuman, humanProfileID)
	}
	candidate, workflowVersion, taskDeadline, assigneeKind, assigneeID, err := scanProductionPersonalAssistantApprovalReplyCandidate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return productionPersonalAssistantApprovalCandidate{}, 0, false, nil
	}
	if err != nil {
		return productionPersonalAssistantApprovalCandidate{}, 0, false, fmt.Errorf("load personal assistant approval reply target: %w", err)
	}
	if candidate.DecisionID != reference.DecisionID || candidate.ApprovalTenant != reference.ApprovalTenant || candidate.TaskID != reference.TaskID {
		return productionPersonalAssistantApprovalCandidate{}, 0, false, nil
	}
	now := s.now().UTC()
	if (!candidate.ExpiresAt.IsZero() && !now.Before(candidate.ExpiresAt)) ||
		(!taskDeadline.IsZero() && !now.Before(taskDeadline)) {
		return productionPersonalAssistantApprovalCandidate{}, 0, false, nil
	}
	if candidate.TaskID == "" {
		candidate.HumanProfileID = productionPersonalAssistantApprovalAssignee(candidate.Payload)
	} else if hil.ActorKind(assigneeKind) == hil.ActorHuman {
		candidate.HumanProfileID = strings.TrimSpace(assigneeID)
	}
	if candidate.HumanProfileID != humanProfileID {
		return productionPersonalAssistantApprovalCandidate{}, 0, false, nil
	}
	return candidate, workflowVersion, true, nil
}

func scanProductionPersonalAssistantApprovalReplyCandidate(row *sql.Row) (productionPersonalAssistantApprovalCandidate, int64, time.Time, string, string, error) {
	var candidate productionPersonalAssistantApprovalCandidate
	var payloadJSON string
	var createdAt int64
	var expiresAt, taskDeadline sql.NullInt64
	var workflowID, taskID, assigneeKind, assigneeID string
	var workflowVersion int64
	err := row.Scan(
		&candidate.DecisionID, &candidate.ApprovalTenant, &candidate.Requester.Kind, &candidate.Requester.ID,
		&candidate.Action, &candidate.ActionTypeID, &candidate.Purpose, &candidate.RiskLevel, &candidate.RouteMode,
		&payloadJSON, &createdAt, &expiresAt, &workflowID, &taskID, &workflowVersion, &taskDeadline, &assigneeKind, &assigneeID,
	)
	if err != nil {
		return productionPersonalAssistantApprovalCandidate{}, 0, time.Time{}, "", "", err
	}
	if err := json.Unmarshal([]byte(payloadJSON), &candidate.Payload); err != nil {
		return productionPersonalAssistantApprovalCandidate{}, 0, time.Time{}, "", "", err
	}
	candidate.CreatedAt = time.Unix(0, createdAt).UTC()
	if expiresAt.Valid && expiresAt.Int64 > 0 {
		candidate.ExpiresAt = time.Unix(0, expiresAt.Int64).UTC()
	}
	candidate.WorkflowID = strings.TrimSpace(workflowID)
	candidate.TaskID = strings.TrimSpace(taskID)
	var deadline time.Time
	if taskDeadline.Valid && taskDeadline.Int64 > 0 {
		deadline = time.Unix(0, taskDeadline.Int64).UTC()
	}
	return candidate, workflowVersion, deadline, strings.TrimSpace(assigneeKind), strings.TrimSpace(assigneeID), nil
}

func (s *productionPersonalAssistantApprovalReplyStore) insertClaim(ctx context.Context, input productionPersonalAssistantApprovalReplyInput, claim productionPersonalAssistantApprovalReplyClaim) error {
	_, err := s.jobs.Exec(ctx, `INSERT INTO gateway_weixin_approval_reply_claims (
		binding_tenant_id, human_profile_id, account_id, inbound_message_id, approval_tenant_id,
		decision_id, workflow_task_id, expected_workflow_version, decision, title, idempotency_key, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT (binding_tenant_id, human_profile_id, account_id, inbound_message_id) DO NOTHING`,
		input.TenantID, input.HumanProfileID, input.AccountID, input.MessageID, claim.ApprovalTenant,
		claim.DecisionID, claim.TaskID, claim.ExpectedWorkflowVersion, claim.Decision, claim.Title, claim.IdempotencyKey,
		s.now().UTC().UnixNano())
	if err != nil {
		return fmt.Errorf("claim personal assistant approval reply: %w", err)
	}
	return nil
}

func (s *productionPersonalAssistantApprovalReplyStore) loadClaim(ctx context.Context, input productionPersonalAssistantApprovalReplyInput) (productionPersonalAssistantApprovalReplyClaim, bool, error) {
	var claim productionPersonalAssistantApprovalReplyClaim
	err := s.jobs.QueryRow(ctx, `SELECT approval_tenant_id, decision_id, workflow_task_id,
		expected_workflow_version, decision, title, idempotency_key
		FROM gateway_weixin_approval_reply_claims
		WHERE binding_tenant_id = ? AND human_profile_id = ? AND account_id = ? AND inbound_message_id = ?`,
		input.TenantID, input.HumanProfileID, input.AccountID, input.MessageID).Scan(
		&claim.ApprovalTenant, &claim.DecisionID, &claim.TaskID, &claim.ExpectedWorkflowVersion,
		&claim.Decision, &claim.Title, &claim.IdempotencyKey,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return productionPersonalAssistantApprovalReplyClaim{}, false, nil
	}
	if err != nil {
		return productionPersonalAssistantApprovalReplyClaim{}, false, fmt.Errorf("load personal assistant approval reply claim: %w", err)
	}
	if claim.ApprovalTenant == "" || claim.DecisionID == "" || claim.IdempotencyKey == "" ||
		(claim.Decision != hil.WorkflowTaskApprove && claim.Decision != hil.WorkflowTaskReject) {
		return productionPersonalAssistantApprovalReplyClaim{}, false, fmt.Errorf("personal assistant approval reply claim is invalid")
	}
	return claim, true, nil
}

func productionPersonalAssistantApprovalReplyIdempotencyKey(input productionPersonalAssistantApprovalReplyInput) string {
	payload := strings.Join([]string{input.TenantID, input.HumanProfileID, input.AccountID, input.MessageID}, "\x00")
	digest := sha256.Sum256([]byte(payload))
	return "weixin-approval-reply:" + hex.EncodeToString(digest[:])
}

func productionPersonalAssistantApprovalReplyDecision(message string) (hil.WorkflowTaskDecision, bool) {
	message = strings.TrimSpace(message)
	if strings.HasPrefix(message, "[引用:") {
		if end := strings.LastIndex(message, "]\n"); end >= 0 {
			message = strings.TrimSpace(message[end+2:])
		}
	}
	message = strings.TrimSpace(strings.TrimRight(message, "。.!！"))
	switch message {
	case "同意":
		return hil.WorkflowTaskApprove, true
	case "拒绝":
		return hil.WorkflowTaskReject, true
	default:
		return "", false
	}
}
