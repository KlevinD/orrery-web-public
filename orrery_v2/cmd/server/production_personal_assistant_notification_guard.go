package main

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"orrery_v2/internal/app/hil"
	todoapp "orrery_v2/internal/app/todo"
	gwoutbound "orrery_v2/internal/gateway/outbound"
)

type productionPersonalAssistantPendingApproval interface {
	pendingCandidate(context.Context, productionPersonalAssistantApprovalNotificationReference, string) (productionPersonalAssistantApprovalCandidate, int64, bool, error)
}

type productionPersonalAssistantNotificationGuard struct {
	attention     productionPersonalAssistantAttentionSweeper
	approvals     productionPersonalAssistantPendingApproval
	scheduled     *productionPersonalAssistantScheduledResultSweeper
	replies       productionAgentNotificationReplyWriter
	systemUpdates *productionPersonalAssistantSystemUpdates
}

// Validate fences every send, including jobs queued before an item was resolved
// or notification preferences were changed. It never executes a user action.
func (g productionPersonalAssistantNotificationGuard) Validate(ctx context.Context, delivery gwoutbound.Delivery) (bool, error) {
	key := delivery.IdempotencyKey
	if strings.HasPrefix(key, personalAssistantSystemUpdatePrefix) {
		if g.systemUpdates == nil {
			return false, nil
		}
		return g.systemUpdates.Validate(ctx, delivery)
	}
	// Already queued pre-upgrade repair templates must also fail at send time.
	for _, prefix := range []string{"scheduled-repair-final:", "scheduled-repair-business-verified:"} {
		if strings.HasPrefix(key, prefix) {
			return strings.HasPrefix(key, prefix+"agent:v1:") && delivery.Title == "" && agentNotificationPublicBody(delivery.Body), nil
		}
	}
	if strings.HasPrefix(key, "personal-assistant:scheduled-result:") {
		if g.scheduled == nil {
			return false, nil
		}
		return g.scheduled.Validate(ctx, delivery)
	}
	if !strings.HasPrefix(key, "personal-assistant:attention:") && !strings.HasPrefix(key, "personal-assistant:approval:") {
		return true, nil
	}
	if delivery.ChannelKey != "weixin" {
		return false, nil
	}
	if g.replies == nil {
		return false, nil
	}
	if valid, err := g.replies.Validate(ctx, delivery); err != nil || !valid {
		return false, err
	}
	if strings.HasPrefix(key, "personal-assistant:approval:") {
		ref, ok := parseProductionPersonalAssistantApprovalNotificationKey(key)
		if !ok || ref.HumanProfileID != delivery.HumanProfileID {
			return false, nil
		}
		viewer, target, found, err := g.attention.recipient(ctx, productionPersonalAssistantAttentionRecipient{TenantID: ref.ApprovalTenant, HumanProfileID: ref.HumanProfileID})
		if err != nil || !found || !notificationTargetMatchesDelivery(target, delivery) || !attentionHasScope(viewer, "approvals.read") {
			return false, err
		}
		_, _, pending, err := g.approvals.pendingCandidate(ctx, ref, ref.HumanProfileID)
		return pending, err
	}
	parts := strings.Split(strings.TrimPrefix(key, "personal-assistant:attention:v2:"), ":")
	if len(parts) != 6 || parts[4] != "recipient" {
		return false, nil
	}
	tenant, err := url.QueryUnescape(parts[0])
	if err != nil {
		return false, nil
	}
	id, err := url.QueryUnescape(parts[2])
	if err != nil {
		return false, nil
	}
	human, err := url.QueryUnescape(parts[5])
	if err != nil {
		return false, nil
	}
	recipient := productionPersonalAssistantAttentionRecipient{TenantID: tenant, HumanProfileID: human}
	if id == "" || human != delivery.HumanProfileID || personalAssistantAttentionKey(recipient, parts[1], id, parts[3]) != key {
		return false, nil
	}
	enabled := false
	for _, allowed := range g.attention.recipients {
		if allowed == recipient {
			enabled = true
			break
		}
	}
	if !enabled {
		return false, nil
	}
	viewer, target, found, err := g.attention.recipient(ctx, recipient)
	if err != nil || !found || !notificationTargetMatchesDelivery(target, delivery) {
		return false, err
	}
	switch parts[1] {
	case "todo":
		if !attentionHasScope(viewer, "todo.read") {
			return false, nil
		}
		item, err := g.attention.todos.GetTodo(ctx, viewer, id)
		if errors.Is(err, todoapp.ErrNotFound) || errors.Is(err, todoapp.ErrForbidden) {
			return false, nil
		}
		return err == nil && parts[3] != "" && personalAssistantTodoAttention(item, viewer) == parts[3], err
	case "council":
		if parts[3] != "vote" || !attentionHasScope(viewer, "approvals.read") {
			return false, nil
		}
		actor := hil.ActorRef{Kind: hil.ActorHuman, ID: human}
		page, err := g.attention.decisions.ListDecisions(ctx, hil.DecisionListQuery{TenantID: tenant, DecisionID: id, Mode: hil.ModeCouncil, Status: hil.DecisionPending, Mine: true, ActionableBy: actor, Limit: 1})
		if err != nil || len(page.Items) != 1 {
			return false, err
		}
		now := time.Now().UTC()
		if g.attention.now != nil {
			now = g.attention.now()
		}
		decision := page.Items[0]
		if decision.ID != id || decision.TenantID != tenant || decision.Status != hil.DecisionPending || (!decision.ExpiresAt.IsZero() && !now.Before(decision.ExpiresAt)) {
			return false, nil
		}
		council, err := g.attention.decisions.GetCouncilByRequestID(ctx, tenant, id)
		if errors.Is(err, hil.ErrNotFound) {
			return false, nil
		}
		if err != nil || council.TenantID != tenant || council.RequestID != id || council.Status != hil.CouncilOpen || (!council.ExpiresAt.IsZero() && !now.Before(council.ExpiresAt)) {
			return false, err
		}
		for _, vote := range council.Votes {
			if vote.Actor == actor {
				return false, nil
			}
		}
		for _, participant := range council.Participants {
			if participant.Actor == actor {
				return true, nil
			}
		}
	}
	return false, nil
}

func notificationTargetMatchesDelivery(target productionPersonalAssistantWeixinTarget, delivery gwoutbound.Delivery) bool {
	return target.TenantID == delivery.TenantID && target.AccountID == delivery.AccountID
}
