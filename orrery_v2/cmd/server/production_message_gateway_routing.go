package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/operations"
	gwbindings "orrery_v2/internal/gateway/binding"
	gwconfig "orrery_v2/internal/gateway/config"
	gwprotocol "orrery_v2/internal/gateway/protocol"
)

type productionGatewayRouteOptions struct {
	AgentBindings             map[string]string
	ActiveAgentBindings       map[string]string
	UserBindings              map[string]string
	PersonalAssistantBindings map[string]string
	AccountRoutes             map[string]string
	LegacySenderRoutes        map[string]string
}

type productionGatewayRoute struct {
	AgentID           string
	ViewerID          string
	UserID            string
	TenantID          string
	OwnerID           string
	PersonalAssistant bool
}

func productionGatewayRouteOptionsFromConfig(cfg gwconfig.Config) productionGatewayRouteOptions {
	extra := cfg.Adapters.Weixin.Extra
	if extra == nil {
		return productionGatewayRouteOptions{}
	}
	routes := productionGatewayRouteOptions{
		AgentBindings:             agentBindingsRouteExtra(extra["agentBindings"]),
		ActiveAgentBindings:       agentBindingsRouteExtra(extra["activeAgentBindings"]),
		UserBindings:              stringBindingsRouteExtra(extra["weixinUserBindings"]),
		PersonalAssistantBindings: stringBindingsRouteExtra(extra["personalAssistantBindings"]),
	}
	routes.AccountRoutes = weixinAccountRoutes(extra["weixinAccounts"])
	routes.LegacySenderRoutes = legacyWeixinSenderRoutes(routes.UserBindings)
	return routes
}

const unregisteredWeixinUserMessage = "当前微信用户未注册，请先登录 Orrery，在头像菜单的个人管理中完成微信绑定并选择默认 Bot。"

type productionGatewayWeixinIdentity struct {
	UserID            string
	ViewerID          string
	DefaultAgentID    string
	ActiveAgentID     string
	AgentID           string
	OwnerID           string
	PersonalAssistant bool
	TenantID          string
	BindingKey        string
}

func (r *productionMessageGatewayRunner) resolveRoute(ctx context.Context, input gwprotocol.SubmitTurnInput) (productionGatewayRoute, bool, error) {
	if input.ChannelKey == gwprotocol.ChannelWebsite {
		if !verifiedWebsiteIdentity(input) {
			return productionGatewayRoute{}, false, fmt.Errorf("%w: website identity is not verified", errGatewayRouteInvalid)
		}
		agentID := defaultWebsitePublicReceptionistAgentID
		if input.AdvisorTier == gwprotocol.AdvisorTierCustomer {
			agentID = defaultWebsiteCustomerReceptionistAgentID
		}
		return productionGatewayRoute{AgentID: agentID.String()}, true, nil
	}
	if input.ChannelKey == gwprotocol.ChannelFeishu || input.ChannelKey == gwprotocol.ChannelDingTalk {
		identity, ok := verifiedGatewayIdentity(input)
		if !ok || !strings.EqualFold(strings.TrimSpace(identity.ChatKind), "direct") {
			return productionGatewayRoute{}, false, nil
		}
		r.mu.Lock()
		bindings := r.bindings
		r.mu.Unlock()
		if bindings == nil {
			return productionGatewayRoute{}, false, errors.New("gateway binding store is unavailable")
		}
		var owner gwbindings.AgentChannelBinding
		if input.ChannelKey == gwprotocol.ChannelFeishu {
			var err error
			owner, err = bindings.ResolveAgentChannelBinding(ctx, string(gwprotocol.ChannelFeishu), identity.AccountID)
			if errors.Is(err, gwbindings.ErrNotFound) || err == nil && !owner.InboundEnabled {
				return productionGatewayRoute{}, false, nil
			}
			if err != nil {
				return productionGatewayRoute{}, false, fmt.Errorf("resolve Feishu account owner: %w", err)
			}
		}
		binding, err := bindings.Resolve(ctx, gwbindings.ExternalIdentity{
			ChannelKey: string(input.ChannelKey), AccountID: identity.AccountID, ExternalTenantID: identity.ExternalTenantID,
			ExternalUserID: identity.ExternalUserID,
		})
		if errors.Is(err, gwbindings.ErrNotFound) {
			return productionGatewayRoute{}, false, nil
		}
		if err != nil {
			return productionGatewayRoute{}, false, fmt.Errorf("resolve gateway binding: %w", err)
		}
		if binding.CurrentAgentID() == "" {
			return productionGatewayRoute{}, false, nil
		}
		if input.ChannelKey == gwprotocol.ChannelFeishu && (binding.TenantID != owner.TenantID || binding.CurrentAgentID() != owner.AgentProfileID) {
			return productionGatewayRoute{}, false, nil
		}
		agentID := binding.CurrentAgentID()
		if input.ChannelKey == gwprotocol.ChannelFeishu {
			agentID = owner.AgentProfileID
		}
		return productionGatewayRoute{
			AgentID: agentID, ViewerID: binding.HumanProfileID, UserID: binding.ExternalUserID, TenantID: binding.TenantID,
		}, true, nil
	}
	if input.ChannelKey != gwprotocol.ChannelWeixin {
		return productionGatewayRoute{}, true, nil
	}
	identity := r.weixinIdentity(input)
	if identity.UserID == "" || identity.AgentID == "" && !identity.PersonalAssistant {
		return productionGatewayRoute{}, false, nil
	}
	return productionGatewayRoute{AgentID: identity.AgentID, ViewerID: identity.ViewerID, UserID: identity.UserID, TenantID: identity.TenantID, OwnerID: identity.OwnerID, PersonalAssistant: identity.PersonalAssistant}, true, nil
}

func gatewayBindingCommand(message string) (string, bool) {
	fields := strings.Fields(strings.TrimSpace(message))
	if len(fields) != 2 || (fields[0] != "/绑定" && !strings.EqualFold(fields[0], "/bind")) {
		return "", false
	}
	return strings.TrimSpace(fields[1]), true
}

func (r *productionMessageGatewayRunner) handleGatewayBindingCommand(ctx context.Context, input gwprotocol.SubmitTurnInput, code string, out chan<- gwprotocol.Event) {
	identity, ok := verifiedGatewayIdentity(input)
	if !ok || !strings.EqualFold(strings.TrimSpace(identity.ChatKind), "direct") {
		r.recordFeishuBindingAudit(ctx, input, gwbindings.Binding{}, gwbindings.AgentChannelBinding{}, "failed", "binding_rejected")
		out <- gwprotocol.Event{Type: gwprotocol.EventError, Code: gwprotocol.ErrorCodeGatewayPermissionDenied}
		out <- gwprotocol.Event{Type: gwprotocol.EventTurnCompleted, FinishReason: "binding_rejected"}
		return
	}
	r.mu.Lock()
	bindings := r.bindings
	r.mu.Unlock()
	if bindings == nil {
		emitGatewayRouteUnavailable(out)
		return
	}
	externalIdentity := gwbindings.ExternalIdentity{
		ChannelKey: string(input.ChannelKey), AccountID: identity.AccountID, ExternalTenantID: identity.ExternalTenantID,
		ExternalUserID: identity.ExternalUserID, DisplayName: identity.DisplayName,
	}
	var owner gwbindings.AgentChannelBinding
	if input.ChannelKey == gwprotocol.ChannelFeishu {
		var ownerErr error
		owner, ownerErr = bindings.ResolveAgentChannelBinding(ctx, string(gwprotocol.ChannelFeishu), identity.AccountID)
		if errors.Is(ownerErr, gwbindings.ErrNotFound) || ownerErr == nil && !owner.InboundEnabled {
			r.recordFeishuBindingAudit(ctx, input, gwbindings.Binding{}, owner, "failed", "binding_owner_mismatch")
			out <- gwprotocol.Event{Type: gwprotocol.EventError, Code: gwprotocol.ErrorCodeGatewayPermissionDenied}
			out <- gwprotocol.Event{Type: gwprotocol.EventTurnCompleted, FinishReason: "binding_owner_mismatch"}
			return
		}
		if ownerErr != nil {
			emitGatewayRouteUnavailable(out)
			return
		}
	}
	var binding gwbindings.Binding
	var err error
	if input.ChannelKey == gwprotocol.ChannelFeishu {
		binding, err = bindings.ConsumeCodeForAgent(ctx, code, externalIdentity, owner.AgentProfileID)
	} else {
		binding, err = bindings.ConsumeCode(ctx, code, externalIdentity)
	}
	if err != nil {
		finishReason := "binding_failed"
		if errors.Is(err, gwbindings.ErrAgentMismatch) {
			finishReason = "binding_owner_mismatch"
		} else if errors.Is(err, gwbindings.ErrConflict) {
		} else if !errors.Is(err, gwbindings.ErrInvalidCode) && !errors.Is(err, gwbindings.ErrInvalidInput) {
			emitGatewayRouteUnavailable(out)
			return
		}
		r.recordFeishuBindingAudit(ctx, input, gwbindings.Binding{}, owner, "failed", finishReason)
		out <- gwprotocol.Event{Type: gwprotocol.EventError, Code: gwprotocol.ErrorCodeGatewayRequestInvalid}
		out <- gwprotocol.Event{Type: gwprotocol.EventTurnCompleted, FinishReason: finishReason}
		return
	}
	if input.ChannelKey == gwprotocol.ChannelFeishu && binding.TenantID != owner.TenantID {
		if revokeErr := bindings.Revoke(ctx, binding.TenantID, binding.HumanProfileID, binding.ID); revokeErr != nil {
			emitGatewayRouteUnavailable(out)
			return
		}
		r.recordFeishuBindingAudit(ctx, input, binding, owner, "failed", "binding_tenant_mismatch")
		out <- gwprotocol.Event{Type: gwprotocol.EventError, Code: gwprotocol.ErrorCodeGatewayPermissionDenied}
		out <- gwprotocol.Event{Type: gwprotocol.EventTurnCompleted, FinishReason: "binding_tenant_mismatch"}
		return
	}
	if err := r.validateGatewayRouteTenant(ctx, productionGatewayRoute{
		AgentID: binding.CurrentAgentID(), ViewerID: binding.HumanProfileID, UserID: binding.ExternalUserID, TenantID: binding.TenantID,
	}); err != nil {
		if !errors.Is(err, errGatewayRouteInvalid) {
			emitGatewayRouteUnavailable(out)
			return
		}
		if revokeErr := bindings.Revoke(ctx, binding.TenantID, binding.HumanProfileID, binding.ID); revokeErr != nil {
			emitGatewayRouteUnavailable(out)
			return
		}
		r.recordFeishuBindingAudit(ctx, input, binding, owner, "failed", "binding_tenant_mismatch")
		out <- gwprotocol.Event{Type: gwprotocol.EventError, Code: gwprotocol.ErrorCodeGatewayPermissionDenied}
		out <- gwprotocol.Event{Type: gwprotocol.EventTurnCompleted, FinishReason: "binding_tenant_mismatch"}
		return
	}
	r.recordFeishuBindingAudit(ctx, input, binding, owner, "succeeded", "binding_completed")
	out <- gwprotocol.Event{Type: gwprotocol.EventAssistantTextDelta, Text: "绑定成功，现在可以在这里使用 Orrery。"}
	out <- gwprotocol.Event{Type: gwprotocol.EventTurnCompleted, FinishReason: "binding_completed"}
}

func (r *productionMessageGatewayRunner) recordFeishuBindingAudit(ctx context.Context, input gwprotocol.SubmitTurnInput, binding gwbindings.Binding, owner gwbindings.AgentChannelBinding, result, reason string) {
	if r == nil || input.ChannelKey != gwprotocol.ChannelFeishu {
		return
	}
	r.mu.Lock()
	audit := r.audit
	r.mu.Unlock()
	if audit == nil {
		return
	}
	accountID := owner.AccountID
	if accountID == "" && input.Identity != nil {
		accountID = input.Identity.AccountID
	}
	tenantID := firstNonBlank(binding.TenantID, owner.TenantID)
	agentID := firstNonBlank(binding.CurrentAgentID(), owner.AgentProfileID)
	humanID := binding.HumanProfileID
	metadata := map[string]string{
		"channel_key":   "feishu",
		"account_id":    gatewayAuditValue(accountID, 128),
		"agent_id":      gatewayAuditValue(agentID, 256),
		"human_id":      gatewayAuditValue(humanID, 256),
		"finish_reason": gatewayAuditValue(reason, 64),
	}
	for key, value := range metadata {
		if value == "" {
			delete(metadata, key)
		}
	}
	severity := "warning"
	if result == "succeeded" {
		severity = "info"
	}
	_ = audit.RecordAuditEvent(context.WithoutCancel(ctx), operations.AuditEventDTO{
		TenantID:   gatewayAuditValue(tenantID, 256),
		ActorID:    firstNonBlank(gatewayAuditValue(humanID, 256), gatewayAuditValue(agentID, 256), "gateway"),
		ViewerID:   gatewayAuditValue(humanID, 256),
		EntityType: "gateway_personal_binding",
		EntityID:   "feishu:" + firstNonBlank(gatewayAuditValue(accountID, 128), "unknown"),
		Action:     "gateway.feishu_binding.consume",
		Result:     result,
		Severity:   severity,
		Summary:    "Feishu personal binding " + reason,
		Metadata:   metadata,
		OccurredAt: time.Now().UTC(),
	})
}

func gatewayAuditValue(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if limit > 0 && len(value) > limit {
		value = value[:limit]
	}
	return value
}

func verifiedGatewayIdentity(input gwprotocol.SubmitTurnInput) (gwprotocol.InboundIdentity, bool) {
	if input.Identity == nil || !input.Identity.Verified || strings.TrimSpace(input.Identity.AccountID) == "" || strings.TrimSpace(input.Identity.ExternalUserID) == "" {
		return gwprotocol.InboundIdentity{}, false
	}
	return *input.Identity, true
}

func verifiedWebsiteIdentity(input gwprotocol.SubmitTurnInput) bool {
	return input.Identity != nil && input.Identity.Verified &&
		strings.EqualFold(strings.TrimSpace(input.Identity.ChatKind), "direct") &&
		strings.TrimSpace(input.Identity.ExternalUserID) != ""
}

func unregisteredGatewayUserMessage(channelKey gwprotocol.ChannelKey) string {
	name := "当前渠道"
	if channelKey == gwprotocol.ChannelFeishu {
		name = "飞书"
	} else if channelKey == gwprotocol.ChannelDingTalk {
		name = "钉钉"
	}
	return name + "账号尚未绑定。请在 Orrery 个人管理中生成绑定码，然后在这里发送 /绑定 绑定码。"
}

func isBoundDirectGatewayChannel(channelKey gwprotocol.ChannelKey) bool {
	return channelKey == gwprotocol.ChannelWeixin || channelKey == gwprotocol.ChannelFeishu || channelKey == gwprotocol.ChannelDingTalk || channelKey == gwprotocol.ChannelWebsite
}

func (r *productionMessageGatewayRunner) weixinIdentity(input gwprotocol.SubmitTurnInput) productionGatewayWeixinIdentity {
	if input.ChannelKey != gwprotocol.ChannelWeixin {
		return productionGatewayWeixinIdentity{}
	}
	if !strings.EqualFold(strings.TrimSpace(input.Telemetry["weixin_sender_verified"]), "true") ||
		strings.EqualFold(strings.TrimSpace(input.Telemetry["weixin_group"]), "true") {
		return productionGatewayWeixinIdentity{}
	}
	r.mu.Lock()
	routes := r.routes
	r.mu.Unlock()
	userID := strings.TrimSpace(input.Telemetry["weixin_sender_id"])
	accountID := strings.TrimSpace(input.Telemetry["weixin_account_id"])
	bindingKey := strings.TrimSpace(routes.AccountRoutes[weixinAccountRouteKey(accountID, userID)])
	if bindingKey == "" {
		bindingKey = strings.TrimSpace(routes.LegacySenderRoutes[userID])
	}
	tenantID, scopedUserID, ok := gwconfig.ParseWeixinBindingKey(bindingKey)
	if !ok || scopedUserID != userID {
		return productionGatewayWeixinIdentity{}
	}
	defaultAgentID := strings.TrimSpace(routes.AgentBindings[bindingKey])
	activeAgentID := strings.TrimSpace(routes.ActiveAgentBindings[bindingKey])
	viewerID := strings.TrimSpace(routes.UserBindings[bindingKey])
	ownerID := strings.TrimSpace(routes.PersonalAssistantBindings[bindingKey])
	personalAssistant := ownerID != ""
	if userID == "" || viewerID == "" || !personalAssistant && defaultAgentID == "" && activeAgentID == "" {
		return productionGatewayWeixinIdentity{}
	}
	agentID := activeAgentID
	if agentID == "" {
		agentID = defaultAgentID
	}
	return productionGatewayWeixinIdentity{
		UserID:            userID,
		TenantID:          tenantID,
		BindingKey:        bindingKey,
		ViewerID:          viewerID,
		DefaultAgentID:    defaultAgentID,
		ActiveAgentID:     activeAgentID,
		AgentID:           agentID,
		OwnerID:           ownerID,
		PersonalAssistant: personalAssistant,
	}
}

func weixinSwitchCommand(message string) (string, bool) {
	text := strings.TrimSpace(message)
	if text == "/换人" {
		return "", true
	}
	fields := strings.Fields(text)
	if len(fields) >= 2 && fields[0] == "/换人" {
		return strings.TrimSpace(strings.TrimPrefix(text, fields[0])), true
	}
	return "", false
}

func (r *productionMessageGatewayRunner) handleWeixinSwitchCommand(ctx context.Context, input gwprotocol.SubmitTurnInput, identity productionGatewayWeixinIdentity, arg string, out chan<- gwprotocol.Event) {
	if identity.UserID == "" {
		out <- gwprotocol.Event{Type: gwprotocol.EventError, Code: gwprotocol.ErrorCodeGatewayPermissionDenied}
		out <- gwprotocol.Event{Type: gwprotocol.EventTurnCompleted, FinishReason: "unregistered"}
		return
	}
	bots, err := r.availableGatewayBots(ctx, identity.TenantID)
	if err != nil || len(bots) == 0 {
		out <- gwprotocol.Event{Type: gwprotocol.EventError, Code: gwprotocol.ErrorCodeGatewayRuntimeUnavailable, Recoverable: err != nil}
		out <- gwprotocol.Event{Type: gwprotocol.EventTurnCompleted, FinishReason: "bot_switch_unavailable"}
		return
	}
	arg = strings.TrimSpace(arg)
	if arg == "" {
		out <- gwprotocol.Event{Type: gwprotocol.EventAssistantTextDelta, Text: formatGatewayBotList(bots, identity)}
		out <- gwprotocol.Event{Type: gwprotocol.EventTurnCompleted, FinishReason: "bot_switch_list"}
		return
	}
	bot, ok := findGatewayBot(bots, arg)
	if !ok {
		out <- gwprotocol.Event{Type: gwprotocol.EventError, Code: gwprotocol.ErrorCodeGatewayRequestInvalid}
		out <- gwprotocol.Event{Type: gwprotocol.EventTurnCompleted, FinishReason: "bot_switch_not_found"}
		return
	}
	if err := r.setActiveGatewayAgent(ctx, identity.TenantID, identity.UserID, identity.BindingKey, bot.AgentID); err != nil {
		out <- gwprotocol.Event{Type: gwprotocol.EventError, Code: gwprotocol.ErrorCodeGatewayRuntimeUnavailable, Recoverable: true}
		out <- gwprotocol.Event{Type: gwprotocol.EventTurnCompleted, FinishReason: "bot_switch_failed"}
		return
	}
	_ = input
	out <- gwprotocol.Event{Type: gwprotocol.EventAssistantTextDelta, Text: "已切换到 bot：" + bot.DisplayName + "（" + bot.AgentID + "）。"}
	out <- gwprotocol.Event{Type: gwprotocol.EventTurnCompleted, FinishReason: "bot_switch_completed"}
}

type productionGatewayBot struct {
	AgentID     string
	DisplayName string
	RealName    string
	RoleName    string
}

func (r *productionMessageGatewayRunner) availableGatewayBots(ctx context.Context, tenantID string) ([]productionGatewayBot, error) {
	r.mu.Lock()
	agents := r.agents
	r.mu.Unlock()
	if agents == nil {
		return nil, fmt.Errorf("agent profile directory is not configured")
	}
	page, err := agents.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{
		Organization:  agentcore.OrganizationID(tenantID),
		EnabledOnly:   true,
		IncludeAI:     true,
		IncludeHumans: false,
		Placement:     agentcore.TalentPlacementFilterInternal,
		Page:          agentcore.Page{Limit: 500},
	})
	if err != nil {
		return nil, err
	}
	bots := make([]productionGatewayBot, 0, len(page.Entries))
	for _, entry := range page.Entries {
		if entry.AgentKind != agentcore.AgentKindAI || !entry.Enabled || strings.TrimSpace(entry.AgentProfileID.String()) == "" {
			continue
		}
		agentID := entry.AgentProfileID.String()
		bots = append(bots, productionGatewayBot{
			AgentID:     agentID,
			DisplayName: firstNonBlank(entry.DisplayName, entry.RealName, agentID),
			RealName:    strings.TrimSpace(entry.RealName),
			RoleName:    strings.TrimSpace(entry.RoleName),
		})
	}
	return bots, nil
}

func findGatewayBot(bots []productionGatewayBot, query string) (productionGatewayBot, bool) {
	query = strings.TrimSpace(query)
	if query == "" {
		return productionGatewayBot{}, false
	}
	for _, bot := range bots {
		if bot.AgentID == query {
			return bot, true
		}
	}
	for _, bot := range bots {
		if strings.EqualFold(bot.DisplayName, query) || strings.EqualFold(bot.RealName, query) || strings.EqualFold(bot.RoleName, query) {
			return bot, true
		}
	}
	return productionGatewayBot{}, false
}

func formatGatewayBotList(bots []productionGatewayBot, identity productionGatewayWeixinIdentity) string {
	var b strings.Builder
	b.WriteString("可用 bot：")
	current := strings.TrimSpace(identity.AgentID)
	for _, bot := range bots {
		b.WriteString("\n- ")
		b.WriteString(bot.DisplayName)
		b.WriteString("（")
		b.WriteString(bot.AgentID)
		b.WriteString("）")
		var marks []string
		if bot.AgentID == identity.DefaultAgentID {
			marks = append(marks, "默认")
		}
		if bot.AgentID == current {
			marks = append(marks, "当前")
		}
		if len(marks) > 0 {
			b.WriteString(" ")
			b.WriteString(strings.Join(marks, "，"))
		}
	}
	b.WriteString("\n发送 /换人 botID 或 /换人 名称 切换。")
	return b.String()
}

func (r *productionMessageGatewayRunner) setActiveGatewayAgent(ctx context.Context, tenantID string, userID string, bindingKey string, agentID string) error {
	userID = strings.TrimSpace(userID)
	tenantID = strings.TrimSpace(tenantID)
	agentID = strings.TrimSpace(agentID)
	if tenantID == "" || userID == "" || agentID == "" {
		return fmt.Errorf("tenant, weixin user, and agent are required")
	}
	bindingKey = strings.TrimSpace(bindingKey)
	if bindingKey == "" {
		bindingKey = gwconfig.WeixinBindingKey(tenantID, userID)
	}
	r.mu.Lock()
	store := r.configStore
	r.mu.Unlock()
	if store == nil {
		return fmt.Errorf("gateway configuration store is not configured")
	}
	saved, err := store.Update(func(cfg *gwconfig.Config) error {
		if cfg.Adapters.Weixin.Extra == nil {
			cfg.Adapters.Weixin.Extra = map[string]any{}
		}
		upsertRouteStringBinding(cfg.Adapters.Weixin.Extra, "activeAgentBindings", bindingKey, agentID)
		return nil
	})
	if err != nil {
		return err
	}
	r.UpdateConfig(saved.Config)
	_ = ctx
	return nil
}

func agentBindingsRouteExtra(value any) map[string]string {
	return stringBindingsRouteExtra(value)
}

func weixinAccountRoutes(value any) map[string]string {
	accounts, ok := value.(map[string]any)
	if !ok || len(accounts) == 0 {
		return nil
	}
	out := map[string]string{}
	ambiguous := map[string]struct{}{}
	for bindingKey, raw := range accounts {
		account, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		tenantID, keyUserID, ok := gwconfig.ParseWeixinBindingKey(bindingKey)
		if !ok {
			continue
		}
		accountTenantID := firstNonBlank(routeStringBindingValue(account["tenantId"]), routeStringBindingValue(account["tenant_id"]), tenantID)
		userID := firstNonBlank(routeStringBindingValue(account["userId"]), routeStringBindingValue(account["user_id"]), keyUserID)
		botID := firstNonBlank(routeStringBindingValue(account["botId"]), routeStringBindingValue(account["bot_id"]), routeStringBindingValue(account["accountId"]))
		canonicalKey := gwconfig.WeixinBindingKey(accountTenantID, userID)
		routeKey := weixinAccountRouteKey(botID, userID)
		if accountTenantID != tenantID || userID != keyUserID || canonicalKey == "" || routeKey == "" {
			continue
		}
		if existing := out[routeKey]; existing != "" && existing != canonicalKey {
			ambiguous[routeKey] = struct{}{}
			continue
		}
		out[routeKey] = canonicalKey
	}
	for routeKey := range ambiguous {
		delete(out, routeKey)
	}
	return out
}

func legacyWeixinSenderRoutes(bindings map[string]string) map[string]string {
	out := map[string]string{}
	for key := range bindings {
		tenantID, userID, ok := gwconfig.ParseWeixinBindingKey(key)
		if !ok || tenantID != gwconfig.DefaultTenantID || key != userID {
			continue
		}
		out[userID] = key
	}
	return out
}

func weixinAccountRouteKey(accountID string, userID string) string {
	accountID = strings.TrimSpace(accountID)
	userID = strings.TrimSpace(userID)
	if accountID == "" || userID == "" {
		return ""
	}
	return accountID + "\x00" + userID
}

func stringBindingsRouteExtra(value any) map[string]string {
	raw, ok := value.(map[string]any)
	if !ok {
		if typed, ok := value.(map[string]string); ok {
			raw = make(map[string]any, len(typed))
			for key, value := range typed {
				raw[key] = value
			}
			ok = true
		}
	}
	if !ok {
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			_ = json.Unmarshal([]byte(text), &raw)
		}
	}
	if len(raw) == 0 {
		return nil
	}
	out := map[string]string{}
	for key, value := range raw {
		targetID := routeStringBindingValue(value)
		if targetID == "" {
			continue
		}
		out[strings.TrimSpace(key)] = targetID
	}
	return out
}

func routeStringBindingValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}

func upsertRouteStringBinding(extra map[string]any, key string, bindingKey string, bindingValue string) {
	bindingKey = strings.TrimSpace(bindingKey)
	bindingValue = strings.TrimSpace(bindingValue)
	if extra == nil || strings.TrimSpace(key) == "" || bindingKey == "" || bindingValue == "" {
		return
	}
	bindings := map[string]any{}
	switch typed := extra[key].(type) {
	case map[string]any:
		bindings = typed
	case map[string]string:
		for k, v := range typed {
			bindings[k] = v
		}
	}
	bindings[bindingKey] = bindingValue
	extra[key] = bindings
}
