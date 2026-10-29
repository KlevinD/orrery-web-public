package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/hil"
)

// Permanently inaccessible notices must not occupy every slot in the next
// batch. Preserve the review history, but never mark these notices delivered.
func (e *productionPersonalAssistantDelegationEngine) suppressNotice(ctx context.Context, key string) error {
	_, err := e.core.Exec(ctx, `UPDATE personal_assistant_approval_reviews SET notice_sent=-1 WHERE review_key=? AND notice_sent=0`, key)
	return err
}

func (e *productionPersonalAssistantDelegationEngine) visibleDecision(ctx context.Context, tenant, id, human string) (hil.DecisionRequest, bool, error) {
	repo := hil.NewPlatformRepository(e.core)
	decision, err := repo.GetDecision(ctx, tenant, id)
	if errors.Is(err, hil.ErrNotFound) {
		return hil.DecisionRequest{}, false, nil
	}
	if err != nil {
		return hil.DecisionRequest{}, false, err
	}
	if decision.Route.Mode != hil.ModeApproval && decision.Route.Mode != hil.ModeHIL {
		return hil.DecisionRequest{}, false, nil
	}
	page, err := repo.ListDecisions(ctx, hil.DecisionListQuery{TenantID: tenant, DecisionID: id, Mode: decision.Route.Mode, Mine: true, ActionableBy: hil.ActorRef{Kind: hil.ActorHuman, ID: human}, Limit: 1})
	return decision, len(page.Items) == 1, err
}

func (e *productionPersonalAssistantDelegationEngine) humanReviewSummary(ctx context.Context, candidate productionPersonalAssistantApprovalCandidate) (string, bool, error) {
	if e == nil {
		return "", false, nil
	}
	d, err := e.decisions.GetDecision(ctx, candidate.ApprovalTenant, candidate.DecisionID)
	if err != nil {
		return "", false, err
	}
	var status, summary string
	var createdAt int64
	err = e.core.QueryRow(ctx, `SELECT status,summary,created_at FROM personal_assistant_approval_reviews WHERE tenant_id=? AND decision_id=? AND task_id=? AND human_profile_id=? AND fingerprint=? ORDER BY created_at DESC LIMIT 1`, candidate.ApprovalTenant, candidate.DecisionID, candidate.TaskID, candidate.HumanProfileID, hil.DecisionReviewFingerprint(d)).Scan(&status, &summary, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if status != "needs_human" {
		summary = ""
	}
	return summary, status == "reviewing" && e.now().Sub(time.Unix(0, createdAt)) < time.Minute, err
}

// A process may die after the HIL transaction but before its review ledger is
// updated. Recover from the authoritative receipt/task audit, never re-run the
// model and never infer success from its proposed decision.
func (e *productionPersonalAssistantDelegationEngine) reconcileInterruptedReviews(ctx context.Context) error {
	rows, err := e.core.Query(ctx, `SELECT review_key,tenant_id,owner_id,human_profile_id,decision_id,task_id FROM personal_assistant_approval_reviews WHERE status='reviewing' AND created_at<? ORDER BY created_at LIMIT 20`, e.now().Add(-time.Minute).UnixNano())
	if err != nil {
		return err
	}
	type item struct{ key, tenant, owner, human, decision, task string }
	var items []item
	for rows.Next() {
		var value item
		if err = rows.Scan(&value.key, &value.tenant, &value.owner, &value.human, &value.decision, &value.task); err != nil {
			break
		}
		items = append(items, value)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	repo := hil.NewPlatformRepository(e.core)
	for _, value := range items {
		status, summary := "needs_human", "审阅被中断，未自动重试。请核对审批当前状态后决定。"
		key := "assistant-delegation:" + value.key
		agent := hil.ActorRef{Kind: hil.ActorAgent, ID: discussionsession.PersonalAssistantAgentID(value.tenant, value.owner)}
		if value.task != "" {
			detail, err := repo.GetWorkflowInstanceByDecisionID(ctx, value.tenant, value.decision)
			if err != nil && !errors.Is(err, hil.ErrNotFound) {
				return err
			}
			for _, audit := range detail.Audit {
				if audit.IdempotencyKey == key && audit.TaskID == value.task && audit.Actor == agent && (audit.EventType == "task.approve" || audit.EventType == "task.reject") {
					status = strings.TrimPrefix(audit.EventType, "task.")
					summary = "个人助理的代理审批已提交（从审批审计恢复）：" + status + "。依据：" + productionPersonalAssistantApprovalDetailText(audit.Reason, 600) + "。不代表整体流程或业务执行已完成。"
				}
			}
		} else {
			receipts, err := repo.ListDecisionReceipts(ctx, value.tenant, value.decision)
			if err != nil {
				return err
			}
			for _, receipt := range receipts {
				var evidence hil.DelegationEvidence
				raw, _ := json.Marshal(receipt.ResolverSummary["delegation"])
				if json.Unmarshal(raw, &evidence) != nil || evidence.DecisionKey != key || evidence.Agent != agent || evidence.OnBehalfOf != (hil.ActorRef{Kind: hil.ActorHuman, ID: value.human}) {
					continue
				}
				if receipt.Effect == hil.EffectAuthorize {
					status = "approve"
				} else if receipt.Effect == hil.EffectDeny {
					status = "reject"
				} else {
					continue
				}
				summary = "个人助理的代理审批已提交（从审批回执恢复）：" + status + "。依据：" + productionPersonalAssistantApprovalDetailText(receipt.ResolverReason, 600) + "。不代表业务执行已完成。"
			}
		}
		if _, err := e.core.Exec(ctx, `UPDATE personal_assistant_approval_reviews SET status=?,summary=?,updated_at=? WHERE review_key=? AND status='reviewing'`, status, summary, e.now().UnixNano(), value.key); err != nil {
			return err
		}
	}
	return nil
}
