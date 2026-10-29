package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/hil"
	gwoutbound "orrery_v2/internal/gateway/outbound"
	"orrery_v2/internal/platform/logging"
)

func (s productionPersonalAssistantApprovalNotificationStore) ListPending(ctx context.Context, limit, offset int) ([]productionPersonalAssistantApprovalCandidate, error) {
	if s.store == nil {
		return nil, fmt.Errorf("personal assistant approval notification store is unavailable")
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.store.Query(ctx, `
SELECT decision.id, decision.tenant_id, decision.requester_kind, decision.requester_id,
 decision.action, decision.action_type_id, decision.purpose, decision.risk_level, decision.route_mode,
 decision.payload_preview_json, decision.created_at, decision.expires_at,
 workflow.id, task.id, task.current_assignee_kind, task.current_assignee_id
FROM hil_decision_requests AS decision
LEFT JOIN hil_workflow_instances AS workflow
 ON workflow.tenant_id = decision.tenant_id AND workflow.decision_request_id = decision.id
LEFT JOIN hil_workflow_tasks AS task
 ON task.tenant_id = workflow.tenant_id AND task.workflow_instance_id = workflow.id AND task.status = ?
WHERE decision.status = ? AND decision.route_mode IN (?, ?)
 AND (workflow.id IS NULL OR task.id IS NOT NULL)
ORDER BY COALESCE(task.created_at, decision.created_at) DESC, decision.id, task.id
LIMIT ? OFFSET ?`, hil.TaskPending, hil.DecisionPending, hil.ModeHIL, hil.ModeApproval, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list pending personal assistant approvals: %w", err)
	}
	defer rows.Close()

	candidates := make([]productionPersonalAssistantApprovalCandidate, 0)
	for rows.Next() {
		var candidate productionPersonalAssistantApprovalCandidate
		var payloadJSON string
		var createdAt int64
		var expiresAt sql.NullInt64
		var workflowID, taskID, recipientKind, recipientID sql.NullString
		if err := rows.Scan(
			&candidate.DecisionID, &candidate.ApprovalTenant, &candidate.Requester.Kind, &candidate.Requester.ID,
			&candidate.Action, &candidate.ActionTypeID, &candidate.Purpose, &candidate.RiskLevel, &candidate.RouteMode,
			&payloadJSON, &createdAt, &expiresAt, &workflowID, &taskID, &recipientKind, &recipientID,
		); err != nil {
			return nil, fmt.Errorf("scan pending personal assistant approval: %w", err)
		}
		if err := json.Unmarshal([]byte(payloadJSON), &candidate.Payload); err != nil {
			return nil, fmt.Errorf("decode pending personal assistant approval %s: %w", candidate.DecisionID, err)
		}
		candidate.CreatedAt = time.Unix(0, createdAt).UTC()
		if expiresAt.Valid && expiresAt.Int64 != 0 {
			candidate.ExpiresAt = time.Unix(0, expiresAt.Int64).UTC()
		}
		candidate.WorkflowID = strings.TrimSpace(workflowID.String)
		candidate.TaskID = strings.TrimSpace(taskID.String)
		if candidate.WorkflowID != "" {
			if hil.ActorKind(strings.TrimSpace(recipientKind.String)) == hil.ActorHuman {
				candidate.HumanProfileID = strings.TrimSpace(recipientID.String)
			}
		} else {
			candidate.HumanProfileID = productionPersonalAssistantApprovalAssignee(candidate.Payload)
		}
		// Keep unassigned rows in the page so pagination follows the database
		// result set. The sweeper rejects them before resolving a private target.
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending personal assistant approvals: %w", err)
	}
	return candidates, nil
}

func (s productionPersonalAssistantApprovalNotificationStore) IsPending(ctx context.Context, candidate productionPersonalAssistantApprovalCandidate) (bool, error) {
	if s.store == nil || strings.TrimSpace(candidate.DecisionID) == "" || strings.TrimSpace(candidate.ApprovalTenant) == "" {
		return false, fmt.Errorf("personal assistant approval notification candidate is invalid")
	}
	var count int
	if candidate.WorkflowID == "" {
		var payloadJSON string
		err := s.store.QueryRow(ctx, `
SELECT payload_preview_json FROM hil_decision_requests
WHERE tenant_id = ? AND id = ? AND status = ? AND route_mode IN (?, ?)
 AND (expires_at IS NULL OR expires_at = 0 OR expires_at > ?)`,
			candidate.ApprovalTenant, candidate.DecisionID, hil.DecisionPending, hil.ModeHIL, hil.ModeApproval, time.Now().UTC().UnixNano()).Scan(&payloadJSON)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
			return false, err
		}
		return candidate.HumanProfileID != "" && productionPersonalAssistantApprovalAssignee(payload) == candidate.HumanProfileID, nil
	}
	err := s.store.QueryRow(ctx, `
SELECT COUNT(*)
FROM hil_decision_requests AS decision
JOIN hil_workflow_instances AS workflow
 ON workflow.tenant_id = decision.tenant_id AND workflow.decision_request_id = decision.id
JOIN hil_workflow_tasks AS task
 ON task.tenant_id = workflow.tenant_id AND task.workflow_instance_id = workflow.id
WHERE decision.tenant_id = ? AND decision.id = ? AND decision.status = ?
 AND (decision.expires_at IS NULL OR decision.expires_at = 0 OR decision.expires_at > ?)
 AND workflow.id = ? AND task.id = ? AND task.status = ?
 AND task.current_assignee_kind = ? AND task.current_assignee_id = ?`,
		candidate.ApprovalTenant, candidate.DecisionID, hil.DecisionPending, time.Now().UTC().UnixNano(), candidate.WorkflowID, candidate.TaskID,
		hil.TaskPending, hil.ActorHuman, candidate.HumanProfileID).Scan(&count)
	return count == 1, err
}

func (s productionPersonalAssistantApprovalNotificationStore) WeixinTarget(ctx context.Context, tenantID, humanProfileID string) (productionPersonalAssistantWeixinTarget, bool, error) {
	return s.weixinTarget(ctx, tenantID, humanProfileID, false)
}

func (s productionPersonalAssistantApprovalNotificationStore) weixinTarget(ctx context.Context, tenantID, humanProfileID string, scheduledResults bool) (productionPersonalAssistantWeixinTarget, bool, error) {
	if s.store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(humanProfileID) == "" {
		return productionPersonalAssistantWeixinTarget{}, false, fmt.Errorf("personal assistant Weixin target is invalid")
	}
	var target productionPersonalAssistantWeixinTarget
	err := s.store.QueryRow(ctx, `
SELECT personal.tenant_id, personal.owner_id, channel.account_id
FROM gateway_weixin_user_bindings AS user_binding
JOIN gateway_weixin_personal_assistant_bindings AS personal
 ON personal.tenant_id = user_binding.tenant_id AND personal.weixin_user_id = user_binding.weixin_user_id
JOIN gateway_weixin_accounts AS account
 ON account.tenant_id = user_binding.tenant_id AND account.weixin_user_id = user_binding.weixin_user_id
JOIN gateway_channel_bindings AS channel
 ON channel.tenant_id = user_binding.tenant_id
 AND channel.channel_key = 'weixin'
 AND channel.account_id = account.bot_id
 AND channel.external_user_id = user_binding.weixin_user_id
 AND channel.human_profile_id = user_binding.human_profile_id
 AND channel.status = 'active'
LEFT JOIN personal_assistant_hosting AS hosting
 ON hosting.tenant_id = personal.tenant_id AND hosting.owner_id = personal.owner_id
LEFT JOIN personal_assistant_notification_preferences AS preference
 ON preference.tenant_id = personal.tenant_id AND preference.owner_id = personal.owner_id
WHERE user_binding.tenant_id = ? AND user_binding.human_profile_id = ?
 AND COALESCE(hosting.assistant_enabled, 1) = 1
 AND (? = 1 OR COALESCE(preference.approval_notifications_enabled, 1) = 1)
ORDER BY personal.updated_at DESC, user_binding.weixin_user_id
LIMIT 1`, strings.TrimSpace(tenantID), strings.TrimSpace(humanProfileID), personalAssistantNotificationBool(scheduledResults)).Scan(&target.TenantID, &target.OwnerID, &target.AccountID)
	if errors.Is(err, sql.ErrNoRows) {
		return productionPersonalAssistantWeixinTarget{}, false, nil
	}
	if err != nil {
		return productionPersonalAssistantWeixinTarget{}, false, fmt.Errorf("resolve personal assistant Weixin target: %w", err)
	}
	target.TenantID = strings.TrimSpace(target.TenantID)
	target.OwnerID = strings.TrimSpace(target.OwnerID)
	target.AccountID = strings.TrimSpace(target.AccountID)
	if target.TenantID == "" || target.OwnerID == "" || target.AccountID == "" {
		return productionPersonalAssistantWeixinTarget{}, false, nil
	}
	return target, true, nil
}

func personalAssistantNotificationBool(value bool) int {
	if value {
		return 1
	}
	return 0
}

type productionPersonalAssistantApprovalNotificationSweeper struct {
	delegation   *productionPersonalAssistantDelegationEngine
	store        productionPersonalAssistantApprovalNotificationSource
	profiles     productionHILProfileDirectory
	deliveries   productionHILGatewayDeliveries
	reviewer     productionPersonalAssistantApprovalReviewer
	replies      productionAgentNotificationReplyWriter
	recovery     *productionPersonalAssistantNotificationRecovery
	logger       logging.Logger
	publicOrigin string
	batchSize    int
}

func (s productionPersonalAssistantApprovalNotificationSweeper) Sweep(ctx context.Context) error {
	if s.store == nil || s.profiles == nil || s.deliveries == nil {
		return fmt.Errorf("personal assistant approval notification dependencies are required")
	}
	pageSize := s.batchSize
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	var sweepErrors []error
	if err := s.delegation.FlushNotices(ctx); err != nil {
		sweepErrors = append(sweepErrors, err)
	}
	for offset := 0; ; {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(sweepErrors, err)...)
		}
		candidates, err := s.store.ListPending(ctx, pageSize, offset)
		if err != nil {
			return errors.Join(append(sweepErrors, err)...)
		}
		resolvedInPage := false
		for _, candidate := range candidates {
			if strings.TrimSpace(candidate.HumanProfileID) == "" {
				continue
			}
			profile, profileErr := s.profiles.GetProfile(ctx, agentcore.AgentProfileID(candidate.HumanProfileID))
			if profileErr != nil || !profile.Enabled || profile.AgentKind != agentcore.AgentKindHuman || !productionHILTenantAllowsProfile(candidate.ApprovalTenant, profile.OrgIdentity) {
				continue
			}
			var target productionPersonalAssistantWeixinTarget
			found := false
			for _, tenantID := range productionHILNotificationBindingTenants(candidate.ApprovalTenant, profile) {
				target, found, err = s.store.WeixinTarget(ctx, tenantID, candidate.HumanProfileID)
				if err != nil || found {
					break
				}
			}
			if err != nil {
				sweepErrors = append(sweepErrors, err)
				continue
			}
			if !found {
				continue
			}
			pending, pendingErr := s.store.IsPending(ctx, candidate)
			if pendingErr != nil {
				sweepErrors = append(sweepErrors, pendingErr)
				continue
			}
			if !pending {
				continue
			}
			idempotencyKey := productionPersonalAssistantApprovalNotificationKey(candidate)
			if handled, err := s.delegation.Handle(ctx, candidate, target); handled {
				resolvedInPage = true
				if err != nil {
					sweepErrors = append(sweepErrors, err)
				}
				continue
			} else if err != nil {
				sweepErrors = append(sweepErrors, err)
			}
			inAppContent := "有一项审批需要你决定。\n\n" + productionPersonalAssistantApprovalConversationMaterial(candidate)
			explanation, reviewing, reviewErr := s.delegation.humanReviewSummary(ctx, candidate)
			if reviewErr != nil {
				sweepErrors = append(sweepErrors, reviewErr)
			}
			if reviewing {
				continue
			}
			if s.delegation != nil {
				pending, pendingErr = s.store.IsPending(ctx, candidate)
				if pendingErr != nil {
					sweepErrors = append(sweepErrors, pendingErr)
					continue
				}
				if !pending {
					continue
				}
			}
			if explanation != "" {
				inAppContent += "\n\n" + explanation
			}
			if link := productionPersonalAssistantApprovalURL(candidate, s.publicOrigin); link != "" {
				inAppContent += "\n\n请在审批中心查看并处理：\n" + link
			}
			if delivered, err := priorAgentNotificationDelivered(ctx, s.deliveries, target.TenantID, candidate.HumanProfileID, idempotencyKey); err != nil || delivered {
				if err != nil {
					sweepErrors = append(sweepErrors, err)
				}
				continue
			}
			if s.replies == nil {
				sweepErrors = append(sweepErrors, errors.New("personal assistant Agent notification writer unavailable"))
				continue
			}
			body, replyErr := s.replies.Reply(ctx, target, candidate.HumanProfileID, idempotencyKey, inAppContent)
			if replyErr != nil {
				sweepErrors = append(sweepErrors, replyErr)
				continue
			}
			pending, pendingErr = s.store.IsPending(ctx, candidate)
			if pendingErr != nil {
				sweepErrors = append(sweepErrors, pendingErr)
				continue
			}
			if !pending {
				continue
			}
			inAppContent = body
			if recoveryErr := s.recovery.MaintainApproval(ctx, s.store, candidate, target, idempotencyKey, inAppContent); recoveryErr != nil {
				sweepErrors = append(sweepErrors, recoveryErr)
			}
			if lookup, ok := s.deliveries.(productionPersonalAssistantApprovalDeliveryLookup); ok {
				exists, lookupErr := lookup.HasIdempotencyKey(ctx, target.TenantID, candidate.HumanProfileID, idempotencyKey)
				if lookupErr != nil {
					sweepErrors = append(sweepErrors, fmt.Errorf("check personal assistant approval delivery %s: %w", candidate.DecisionID, lookupErr))
					continue
				}
				if exists {
					continue
				}
			}
			pending, pendingErr = s.store.IsPending(ctx, candidate)
			if pendingErr != nil {
				sweepErrors = append(sweepErrors, pendingErr)
				continue
			}
			if !pending {
				continue
			}
			_, enqueueErr := s.deliveries.Enqueue(ctx, gwoutbound.EnqueueCommand{
				TenantID: target.TenantID, HumanProfileID: candidate.HumanProfileID,
				ChannelKeys: []string{"weixin"}, AccountID: target.AccountID,
				EventType: "approval", Body: body, Format: "text",
				IdempotencyKey: idempotencyKey,
			})
			if enqueueErr != nil {
				sweepErrors = append(sweepErrors, fmt.Errorf("queue personal assistant approval %s: %w", candidate.DecisionID, enqueueErr))
			}
		}
		if len(candidates) < pageSize {
			break
		}
		if resolvedInPage {
			offset = 0
		} else {
			offset += len(candidates)
		}
	}
	if err := s.delegation.FlushNotices(ctx); err != nil {
		sweepErrors = append(sweepErrors, err)
	}
	return errors.Join(sweepErrors...)
}

func productionPersonalAssistantApprovalAssignee(payload map[string]any) string {
	value, _ := payload["approval_assigned_to"].(string)
	return strings.TrimSpace(value)
}

func productionPersonalAssistantApprovalNotificationKey(candidate productionPersonalAssistantApprovalCandidate) string {
	key := "personal-assistant:approval:v5:" + strings.TrimSpace(candidate.ApprovalTenant) + ":" + strings.TrimSpace(candidate.DecisionID)
	if candidate.TaskID != "" {
		key += ":task:" + strings.TrimSpace(candidate.TaskID)
	}
	return key + ":recipient:" + strings.TrimSpace(candidate.HumanProfileID)
}
