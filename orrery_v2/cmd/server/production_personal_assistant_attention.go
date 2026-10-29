package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/identity"
	todoapp "orrery_v2/internal/app/todo"
	gwoutbound "orrery_v2/internal/gateway/outbound"
)

// Explicit rollout scope; existing approval preferences do not implicitly opt
// every user into new classes of external notifications.
type productionPersonalAssistantAttentionRecipient struct {
	TenantID       string `json:"tenant_id"`
	HumanProfileID string `json:"human_profile_id"`
}

func productionPersonalAssistantAttentionRecipients(raw string) ([]productionPersonalAssistantAttentionRecipient, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var recipients []productionPersonalAssistantAttentionRecipient
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&recipients); err != nil {
		return nil, fmt.Errorf("invalid personal assistant attention recipients configuration")
	}
	if err := decoder.Decode(new(any)); err != io.EOF || len(recipients) > 100 {
		return nil, fmt.Errorf("invalid personal assistant attention recipients configuration")
	}
	seen := map[productionPersonalAssistantAttentionRecipient]bool{}
	for i := range recipients {
		r := &recipients[i]
		r.TenantID, r.HumanProfileID = strings.TrimSpace(r.TenantID), strings.TrimSpace(r.HumanProfileID)
		if r.TenantID == "" || r.HumanProfileID == "" || len(r.TenantID) > 128 || len(r.HumanProfileID) > 128 || seen[*r] {
			return nil, fmt.Errorf("invalid personal assistant attention recipient identity")
		}
		seen[*r] = true
	}
	return recipients, nil
}

type productionPersonalAssistantAttentionTodos interface {
	ListTodos(context.Context, todoapp.ViewerRef, todoapp.ListTodosQuery) (todoapp.TodoPage, error)
	GetTodo(context.Context, todoapp.ViewerRef, string) (todoapp.TodoItem, error)
}

type productionPersonalAssistantAttentionDecisions interface {
	ListDecisions(context.Context, hil.DecisionListQuery) (hil.DecisionPage, error)
	GetCouncilByRequestID(context.Context, string, string) (hil.CouncilSession, error)
}

type productionPersonalAssistantAttentionTarget interface {
	WeixinTarget(context.Context, string, string) (productionPersonalAssistantWeixinTarget, bool, error)
}

type productionPersonalAssistantAttentionDeliveries interface {
	productionHILGatewayDeliveries
	productionPersonalAssistantApprovalDeliveryLookup
}

type productionPersonalAssistantAttentionSweeper struct {
	recipients   []productionPersonalAssistantAttentionRecipient
	profiles     productionHILProfileDirectory
	accounts     productionGatewayAccountLookup
	scopes       productionGatewayPermissionScopeResolver
	targets      productionPersonalAssistantAttentionTarget
	todos        productionPersonalAssistantAttentionTodos
	decisions    productionPersonalAssistantAttentionDecisions
	deliveries   productionPersonalAssistantAttentionDeliveries
	recovery     *productionPersonalAssistantNotificationRecovery
	replies      productionAgentNotificationReplyWriter
	publicOrigin string
	now          func() time.Time
}

func (s productionPersonalAssistantAttentionSweeper) recipient(ctx context.Context, recipient productionPersonalAssistantAttentionRecipient) (todoapp.ViewerRef, productionPersonalAssistantWeixinTarget, bool, error) {
	profile, err := s.profiles.GetProfile(ctx, agentcore.AgentProfileID(recipient.HumanProfileID))
	if err != nil {
		return todoapp.ViewerRef{}, productionPersonalAssistantWeixinTarget{}, false, err
	}
	if !profile.Enabled || profile.AgentKind != agentcore.AgentKindHuman || !productionHILTenantAllowsProfile(recipient.TenantID, profile.OrgIdentity) {
		return todoapp.ViewerRef{}, productionPersonalAssistantWeixinTarget{}, false, nil
	}
	account, err := s.accounts.GetAccountByAgentProfileID(ctx, recipient.HumanProfileID)
	if err != nil {
		return todoapp.ViewerRef{}, productionPersonalAssistantWeixinTarget{}, false, err
	}
	if !account.Enabled || account.AgentProfileID != recipient.HumanProfileID {
		return todoapp.ViewerRef{}, productionPersonalAssistantWeixinTarget{}, false, nil
	}
	principal := identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: identity.ProviderLocal, Subject: recipient.HumanProfileID,
		UserID: account.UserID, Username: account.Username, AgentProfileID: account.AgentProfileID,
		ViewerKind: identity.ViewerKindUser, TenantID: recipient.TenantID,
	})
	scopes, err := s.scopes.ResolvePermissionScopes(ctx, principal, account.PermissionScope)
	if err != nil {
		return todoapp.ViewerRef{}, productionPersonalAssistantWeixinTarget{}, false, err
	}
	viewer := todoapp.ViewerRef{
		TenantID: recipient.TenantID, ViewerID: firstNonBlank(account.Username, account.UserID),
		ViewerKind: todoapp.ViewerKindUser, AuthorizationSubjectID: recipient.HumanProfileID, PermissionScope: scopes,
	}
	for _, tenant := range productionHILNotificationBindingTenants(recipient.TenantID, profile) {
		target, found, err := s.targets.WeixinTarget(ctx, tenant, recipient.HumanProfileID)
		if err != nil {
			return viewer, target, false, err
		}
		if !found {
			continue
		}
		owner := firstNonBlank(account.UserID, account.Username, account.AgentProfileID)
		if target.TenantID != tenant || target.OwnerID != owner {
			return viewer, target, false, nil
		}
		return viewer, target, true, nil
	}
	return viewer, productionPersonalAssistantWeixinTarget{}, false, nil
}

func (s productionPersonalAssistantAttentionSweeper) Sweep(ctx context.Context) error {
	var failures []error
	for _, recipient := range s.recipients {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		viewer, _, found, err := s.recipient(ctx, recipient)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !found {
			continue
		}
		if attentionHasScope(viewer, "todo.read") {
			if err := s.sweepTodos(ctx, recipient, viewer); err != nil {
				failures = append(failures, err)
			}
		}
		if attentionHasScope(viewer, "approvals.read") {
			if err := s.sweepCouncils(ctx, recipient); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}

func attentionHasScope(viewer todoapp.ViewerRef, scope string) bool {
	for _, granted := range viewer.PermissionScope {
		if granted == scope || granted == "*" {
			return true
		}
	}
	return false
}

func personalAssistantTodoAttention(item todoapp.TodoItem, viewer todoapp.ViewerRef) string {
	if item.TenantID != viewer.TenantID || item.Status == todoapp.TodoStatusDone || item.Status == todoapp.TodoStatusCanceled {
		return ""
	}
	matches := func(id string) bool {
		return id != "" && (id == viewer.ViewerID || id == viewer.AuthorizationSubjectID)
	}
	if item.Status == todoapp.TodoStatusReview {
		if matches(item.ReviewerID) {
			return "review"
		}
		return ""
	}
	if item.Status == todoapp.TodoStatusBlocked && (matches(item.ReviewerID) || matches(item.CreatedBy) || matches(item.CreatedBySubjectID)) {
		return "blocked"
	}
	if item.AssigneeKind == todoapp.AssigneeKindHuman && matches(item.AssigneeID) {
		if item.Status == todoapp.TodoStatusBlocked {
			return "blocked"
		}
		if item.AutomationMode == todoapp.TodoAutomationModeManual {
			return "assigned"
		}
	}
	return ""
}

func personalAssistantAttentionKey(recipient productionPersonalAssistantAttentionRecipient, kind, id, state string) string {
	return "personal-assistant:attention:v2:" + url.QueryEscape(recipient.TenantID) + ":" + kind + ":" + url.QueryEscape(id) + ":" + state + ":recipient:" + url.QueryEscape(recipient.HumanProfileID)
}

func (s productionPersonalAssistantAttentionSweeper) sweepTodos(ctx context.Context, recipient productionPersonalAssistantAttentionRecipient, viewer todoapp.ViewerRef) error {
	var failures []error
	for offset := 0; ; {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		page, err := s.todos.ListTodos(ctx, viewer, todoapp.ListTodosQuery{Limit: 100, Offset: offset})
		if err != nil {
			return errors.Join(append(failures, err)...)
		}
		for _, item := range page.Todos {
			state := personalAssistantTodoAttention(item, viewer)
			if state == "" {
				continue
			}
			err := s.queue(ctx, recipient, personalAssistantAttentionKey(recipient, "todo", item.TodoID, state), "todo.read", func(current todoapp.ViewerRef) (string, string, bool, error) {
				fresh, err := s.todos.GetTodo(ctx, current, item.TodoID)
				if errors.Is(err, todoapp.ErrNotFound) || errors.Is(err, todoapp.ErrForbidden) {
					return "", "", false, nil
				}
				if err != nil || personalAssistantTodoAttention(fresh, current) != state {
					return "", "", false, err
				}
				label := map[string]string{"assigned": "有一项待办需要你处理", "review": "有一项待办需要你验收", "blocked": "有一项任务遇到阻塞，需要你处理"}[state]
				title := productionPersonalAssistantApprovalDetailText(fresh.Title, 160)
				body := label + "：" + title + "\n\n" + productionPersonalAssistantApprovalDetailText(fresh.Description, 800)
				if fresh.BlockedReason != "" {
					body += "\n\n阻塞原因：" + productionPersonalAssistantApprovalDetailText(fresh.BlockedReason, 400)
				}
				body += "\n\n请在 Orrery 中处理并填写结果。此提醒不会自动批准或启动任务。" + s.link("/todos/"+url.PathEscape(fresh.TodoID))
				return "待办：" + title, body, true, nil
			})
			if err != nil {
				failures = append(failures, err)
			}
		}
		offset += len(page.Todos)
		if !page.HasMore || len(page.Todos) == 0 {
			return errors.Join(failures...)
		}
	}
}

func (s productionPersonalAssistantAttentionSweeper) sweepCouncils(ctx context.Context, recipient productionPersonalAssistantAttentionRecipient) error {
	query := hil.DecisionListQuery{TenantID: recipient.TenantID, Mode: hil.ModeCouncil, Status: hil.DecisionPending, Mine: true, ActionableBy: hil.ActorRef{Kind: hil.ActorHuman, ID: recipient.HumanProfileID}, Limit: 100}
	var failures []error
	for {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		page, err := s.decisions.ListDecisions(ctx, query)
		if err != nil {
			return errors.Join(append(failures, err)...)
		}
		for _, decision := range page.Items {
			key := personalAssistantAttentionKey(recipient, "council", decision.ID, "vote")
			err := s.queue(ctx, recipient, key, "approvals.read", func(_ todoapp.ViewerRef) (string, string, bool, error) {
				current := query
				current.DecisionID, current.Offset = decision.ID, 0
				fresh, err := s.decisions.ListDecisions(ctx, current)
				if err != nil || len(fresh.Items) != 1 {
					return "", "", false, err
				}
				d := fresh.Items[0]
				now := time.Now().UTC()
				if s.now != nil {
					now = s.now()
				}
				if d.TenantID != recipient.TenantID || d.Status != hil.DecisionPending || (!d.ExpiresAt.IsZero() && !now.Before(d.ExpiresAt)) {
					return "", "", false, nil
				}
				council, err := s.decisions.GetCouncilByRequestID(ctx, d.TenantID, d.ID)
				if errors.Is(err, hil.ErrNotFound) {
					return "", "", false, nil
				}
				if err != nil {
					return "", "", false, err
				}
				if council.TenantID != d.TenantID || council.RequestID != d.ID || council.Status != hil.CouncilOpen || (!council.ExpiresAt.IsZero() && !now.Before(council.ExpiresAt)) {
					return "", "", false, nil
				}
				canVote := false
				actor := hil.ActorRef{Kind: hil.ActorHuman, ID: recipient.HumanProfileID}
				for _, participant := range council.Participants {
					if participant.Actor == actor {
						canVote = true
						break
					}
				}
				for _, vote := range council.Votes {
					if vote.Actor == actor {
						canVote = false
						break
					}
				}
				if !canVote {
					return "", "", false, nil
				}
				presentation := productionPersonalAssistantApprovalPresentationFor(productionPersonalAssistantApprovalCandidate{Purpose: d.Purpose, Action: d.Action, ActionTypeID: d.ActionTypeID, Payload: d.PayloadPreview})
				title := productionPersonalAssistantApprovalDetailText(presentation.Title, 160)
				return "会审：" + title, "有一项会审等待你的意见：" + title + "\n\n请在 Orrery 会审中查看材料并投票。此提醒不会代你投票。" + s.link("/approvals?mode=council"), true, nil
			})
			if err != nil {
				failures = append(failures, err)
			}
		}
		query.Offset += len(page.Items)
		if len(page.Items) == 0 || query.Offset >= page.Total {
			return errors.Join(failures...)
		}
	}
}

func (s productionPersonalAssistantAttentionSweeper) link(path string) string {
	if origin := strings.TrimRight(strings.TrimSpace(s.publicOrigin), "/"); origin != "" {
		return "\n\n" + origin + path
	}
	return ""
}

func (s productionPersonalAssistantAttentionSweeper) queue(ctx context.Context, recipient productionPersonalAssistantAttentionRecipient, key, permission string, content func(todoapp.ViewerRef) (string, string, bool, error)) error {
	viewer, target, found, err := s.recipient(ctx, recipient)
	if err != nil || !found || !attentionHasScope(viewer, permission) {
		return err
	}
	title, body, pending, err := content(viewer)
	if err != nil || !pending {
		return err
	}
	if delivered, err := priorAgentNotificationDelivered(ctx, s.deliveries, target.TenantID, recipient.HumanProfileID, key); err != nil || delivered {
		return err
	}
	if s.replies == nil {
		return errors.New("personal assistant Agent notification writer unavailable")
	}
	body, err = s.replies.Reply(ctx, target, recipient.HumanProfileID, key, body)
	if err != nil {
		return err
	}
	// Generation may take time. Re-read the source before persisting or sending.
	_, _, pending, err = content(viewer)
	if err != nil || !pending {
		return err
	}
	title = ""
	recoveryErr := s.recovery.Maintain(ctx, recipient, target, key, permission, body)
	exists, err := s.deliveries.HasIdempotencyKey(ctx, target.TenantID, recipient.HumanProfileID, key)
	if err != nil || exists {
		return errors.Join(recoveryErr, err)
	}
	_, err = s.deliveries.Enqueue(ctx, gwoutbound.EnqueueCommand{
		TenantID: target.TenantID, HumanProfileID: recipient.HumanProfileID, AccountID: target.AccountID,
		ChannelKeys: []string{"weixin"}, EventType: "mention", Title: title, Body: body, Format: "text", IdempotencyKey: key,
	})
	return errors.Join(recoveryErr, err)
}
