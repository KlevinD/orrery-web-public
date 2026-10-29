package main

import (
	"context"
	"errors"
	"fmt"

	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/reliability"
	gwoutbound "orrery_v2/internal/gateway/outbound"
)

type productionPersonalAssistantNoticeSink interface {
	AppendPersonalAssistantNotice(context.Context, discussionsession.ViewerRef, string, string) error
}

type productionPersonalAssistantReplySink interface {
	AppendPersonalAssistantReply(context.Context, discussionsession.ViewerRef, string, string) error
}

type productionPersonalAssistantRecoverableDeliveries interface {
	ListByIdempotencyKey(context.Context, string, string, string) ([]gwoutbound.Delivery, error)
	RecoverDeadLetter(context.Context, string, string, string, string) (bool, error)
}

type productionPersonalAssistantNotificationRecovery struct {
	recipients      []productionPersonalAssistantAttentionRecipient
	accounts        productionGatewayAccountLookup
	scopes          productionGatewayPermissionScopeResolver
	notices         productionPersonalAssistantNoticeSink
	deliveries      productionPersonalAssistantRecoverableDeliveries
	preserveContent bool
	failures        productionNotificationFailureCapture
}

// Maintain is called only after the source has been reread and is still
// actionable. In-app persistence and external recovery fail independently.
func (r *productionPersonalAssistantNotificationRecovery) Maintain(ctx context.Context, recipient productionPersonalAssistantAttentionRecipient, target productionPersonalAssistantWeixinTarget, key, permission, content string) error {
	return r.maintain(ctx, recipient, target, key, permission, content, true)
}

// Assigned approvals already authorize their notification. Unlike optional
// attention reminders, they do not depend on the attention opt-in list.
func (r *productionPersonalAssistantNotificationRecovery) MaintainApproval(ctx context.Context, source productionPersonalAssistantApprovalNotificationSource, candidate productionPersonalAssistantApprovalCandidate, target productionPersonalAssistantWeixinTarget, key, content string) error {
	if r == nil {
		return nil
	}
	if source == nil || candidate.HumanProfileID == "" || key != productionPersonalAssistantApprovalNotificationKey(candidate) {
		return errors.New("approval notification recovery identity is invalid")
	}
	pending, err := source.IsPending(ctx, candidate)
	if err != nil || !pending {
		return err
	}
	current, found, err := source.WeixinTarget(ctx, target.TenantID, candidate.HumanProfileID)
	if err != nil || !found || current != target {
		return err
	}
	return r.maintain(ctx, productionPersonalAssistantAttentionRecipient{TenantID: candidate.ApprovalTenant, HumanProfileID: candidate.HumanProfileID}, target, key, "approvals.read", content, false)
}

func (r *productionPersonalAssistantNotificationRecovery) maintain(ctx context.Context, recipient productionPersonalAssistantAttentionRecipient, target productionPersonalAssistantWeixinTarget, key, permission, content string, requireOptIn bool) error {
	if r == nil {
		return nil
	}
	enabled := false
	for _, allowed := range r.recipients {
		if allowed.HumanProfileID == recipient.HumanProfileID && (allowed.TenantID == recipient.TenantID || (recipient.TenantID == productionHILTenantID && allowed.TenantID == target.TenantID)) {
			enabled = true
			break
		}
	}
	if requireOptIn && !enabled {
		return nil
	}
	if r.accounts == nil || r.scopes == nil || r.notices == nil || r.deliveries == nil {
		return fmt.Errorf("personal assistant notification recovery dependencies are required")
	}
	account, err := r.accounts.GetAccountByAgentProfileID(ctx, recipient.HumanProfileID)
	if err != nil {
		return err
	}
	if !account.Enabled || account.AgentProfileID != recipient.HumanProfileID || firstNonBlank(account.UserID, account.Username, account.AgentProfileID) != target.OwnerID {
		return nil
	}
	principal := identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: identity.ProviderLocal, Subject: recipient.HumanProfileID, UserID: account.UserID, Username: account.Username,
		AgentProfileID: account.AgentProfileID, ViewerKind: identity.ViewerKindUser, TenantID: recipient.TenantID,
	})
	scopes, err := r.scopes.ResolvePermissionScopes(ctx, principal, account.PermissionScope)
	if err != nil {
		return err
	}
	canRead := false
	for _, scope := range scopes {
		if scope == permission || scope == "*" {
			canRead = true
			break
		}
	}
	if !canRead {
		return nil
	}
	viewer := discussionsession.ViewerRef{
		TenantID: target.TenantID, OwnerID: target.OwnerID, ViewerID: firstNonBlank(account.Username, account.UserID),
		ViewerKind: discussionsession.ViewerKindUser, AgentProfileID: recipient.HumanProfileID,
		IdentityIDs: []string{account.UserID, account.Username, recipient.HumanProfileID}, PermissionScope: scopes,
	}
	var failures []error
	appendMessage := r.notices.AppendPersonalAssistantNotice
	if r.preserveContent {
		replies, ok := r.notices.(productionPersonalAssistantReplySink)
		if !ok {
			return errors.New("personal assistant model reply sink is unavailable")
		}
		appendMessage = replies.AppendPersonalAssistantReply
	}
	if err := appendMessage(ctx, viewer, key, content); err != nil {
		failures = append(failures, err)
	}
	deliveries, err := r.deliveries.ListByIdempotencyKey(ctx, target.TenantID, recipient.HumanProfileID, key)
	if err != nil {
		return errors.Join(append(failures, err)...)
	}
	for _, delivery := range deliveries {
		if delivery.ChannelKey != "weixin" || delivery.AccountID != target.AccountID || delivery.TenantID != target.TenantID || delivery.HumanProfileID != recipient.HumanProfileID {
			continue
		}
		if delivery.Status != "dead_letter" && delivery.Status != "failed" {
			continue
		}
		if delivery.LastError != "notification is no longer actionable or allowed" {
			if err := captureProductionNotificationFailure(ctx, r.failures, productionNotificationFailure{
				ID:       fmt.Sprintf("gateway-notification:%s:%d:terminal", delivery.ID, delivery.Attempts),
				TenantID: delivery.TenantID, Channel: delivery.ChannelKey, AccountID: delivery.AccountID, HumanProfileID: delivery.HumanProfileID,
				Source: "gateway_notification", Code: "delivery_" + delivery.Status, Message: delivery.LastError,
				Category: reliability.FailureCategoryUnknown, CorrelationID: delivery.ID, OccurredAt: delivery.UpdatedAt,
			}); err != nil {
				failures = append(failures, err)
				continue
			}
		}
		failureContent := content
		if !r.preserveContent {
			failureContent = "微信提醒曾发送失败，这项事项仍需要你处理。\n\n" + content
		}
		if err := appendMessage(ctx, viewer, key+":external-failure", failureContent); err != nil {
			failures = append(failures, err)
		}
		if delivery.Status == "dead_letter" {
			if _, err := r.deliveries.RecoverDeadLetter(ctx, target.TenantID, recipient.HumanProfileID, target.AccountID, delivery.ID); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}
