package httptransport

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/identity"
	devicedirectory "orrery_v2/internal/device/directory"
	gwconfig "orrery_v2/internal/gateway/config"
)

var errProfileWeixinNotBound = errors.New("profile Weixin account is not bound")

const (
	profileWeixinTargetAgent             = "agent"
	profileWeixinTargetPersonalAssistant = "personal_assistant"
)

type profileWeixinDTO struct {
	TenantID       string                `json:"tenant_id"`
	HumanID        string                `json:"human_id"`
	WeixinBound    bool                  `json:"weixin_bound"`
	WeixinUserID   string                `json:"weixin_user_id,omitempty"`
	DefaultAgentID string                `json:"default_agent_id,omitempty"`
	ActiveAgentID  string                `json:"active_agent_id,omitempty"`
	CurrentAgentID string                `json:"current_agent_id,omitempty"`
	TargetKind     string                `json:"target_kind"`
	Agents         []profileWeixinBotDTO `json:"agents"`
}

type profileWeixinBotDTO struct {
	AgentID     string `json:"agent_id"`
	DisplayName string `json:"display_name"`
	RealName    string `json:"real_name,omitempty"`
	RoleName    string `json:"role_name,omitempty"`
	Enabled     bool   `json:"enabled"`
	Default     bool   `json:"default"`
	Current     bool   `json:"current"`
}

type profileWeixinDefaultAgentRequest struct {
	AgentID    string `json:"agent_id,omitempty"`
	TargetKind string `json:"target_kind,omitempty"`
}

type profileWeixinLoginStartRequest struct {
	AgentID    string `json:"agent_id,omitempty"`
	TargetKind string `json:"target_kind,omitempty"`
}

func (r *Router) handleGetProfileWeixin(w http.ResponseWriter, req *http.Request) {
	humanID, _, ok := r.profileWeixinHuman(w, req)
	if !ok {
		return
	}
	tenantID, ok := r.profileWeixinTenant(w, req)
	if !ok {
		return
	}
	dto, err := r.profileWeixinView(req, tenantID, humanID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "profile_weixin_unavailable", Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

func (r *Router) handlePutProfileWeixinDefaultAgent(w http.ResponseWriter, req *http.Request) {
	humanID, _, ok := r.profileWeixinHuman(w, req)
	if !ok {
		return
	}
	tenantID, ok := r.profileWeixinTenant(w, req)
	if !ok {
		return
	}
	var body profileWeixinDefaultAgentRequest
	if req.Body != nil {
		req.Body = http.MaxBytesReader(w, req.Body, maxGatewayWeixinLoginRequestBytes)
	}
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "invalid_json", Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	if err := rejectTrailingGatewayJSON(decoder); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "invalid_json", Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	targetKind, validTarget := normalizeProfileWeixinTargetKind(body.TargetKind, body.AgentID)
	if !validTarget {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: ErrorInvalidRequest, Message: "Weixin target is invalid", CorrelationID: correlationID(req)})
		return
	}
	ownerID := ""
	var agent profileWeixinBotDTO
	var err error
	if targetKind == profileWeixinTargetPersonalAssistant {
		ownerID, ok = r.profileWeixinPersonalAssistantOwner(w, req, tenantID)
		if !ok || !r.profileWeixinPersonalAssistantReady(w, req, tenantID, ownerID) {
			return
		}
	} else {
		var found bool
		agent, found, err = r.profileWeixinBotByID(req, tenantID, body.AgentID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "profile_weixin_agents_unavailable", Message: err.Error(), CorrelationID: correlationID(req)})
			return
		}
		if !found {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "weixin_bot_not_found", Message: "没有这个 bot。", CorrelationID: correlationID(req)})
			return
		}
	}
	store := r.gatewayStore()
	_, err = store.UpdateAndApply(func(cfg *gwconfig.Config) error {
		extra := ensureWeixinExtra(cfg)
		userID := gatewayWeixinUserIDForHuman(extra, tenantID, humanID)
		if userID == "" {
			return errProfileWeixinNotBound
		}
		if targetKind == profileWeixinTargetPersonalAssistant {
			upsertGatewayWeixinPersonalAssistantBinding(extra, tenantID, userID, ownerID)
		} else {
			removeGatewayWeixinPersonalAssistantBinding(extra, tenantID, userID)
			upsertGatewayWeixinAgentBinding(extra, tenantID, userID, agent.AgentID)
			upsertGatewayWeixinActiveAgentBinding(extra, tenantID, userID, agent.AgentID)
		}
		return nil
	}, func(cfg gwconfig.Config) error {
		return r.reloadGatewayBindings(req, cfg)
	})
	if errors.Is(err, errProfileWeixinNotBound) {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "weixin_not_bound", Message: "请先绑定微信账号后再选择默认 Bot。", CorrelationID: correlationID(req)})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: gatewaySettingsUpdateErrorCode(err), Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	dto, err := r.profileWeixinView(req, tenantID, humanID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "profile_weixin_unavailable", Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

func (r *Router) handleDeleteProfileWeixin(w http.ResponseWriter, req *http.Request) {
	humanID, _, ok := r.profileWeixinHuman(w, req)
	if !ok {
		return
	}
	tenantID, ok := r.profileWeixinTenant(w, req)
	if !ok {
		return
	}
	store := r.gatewayStore()
	_, err := store.UpdateAndApply(func(cfg *gwconfig.Config) error {
		extra := ensureWeixinExtra(cfg)
		if userID := gatewayWeixinUserIDForHuman(extra, tenantID, humanID); userID != "" {
			removeGatewayWeixinBinding(extra, tenantID, userID)
		}
		return nil
	}, func(cfg gwconfig.Config) error {
		return r.reloadGatewayBindings(req, cfg)
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: gatewaySettingsUpdateErrorCode(err), Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	dto, err := r.profileWeixinView(req, tenantID, humanID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "profile_weixin_unavailable", Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

func (r *Router) handleStartProfileWeixinLogin(w http.ResponseWriter, req *http.Request) {
	humanID, displayName, ok := r.profileWeixinHuman(w, req)
	if !ok {
		return
	}
	tenantID, ok := r.profileWeixinTenant(w, req)
	if !ok {
		return
	}
	var body profileWeixinLoginStartRequest
	if req.Body != nil {
		req.Body = http.MaxBytesReader(w, req.Body, maxGatewayWeixinLoginRequestBytes)
	}
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "invalid_json", Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	if err := rejectTrailingGatewayJSON(decoder); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "invalid_json", Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	agentID := strings.TrimSpace(body.AgentID)
	targetKind, validTarget := normalizeProfileWeixinTargetKind(body.TargetKind, agentID)
	if !validTarget {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: ErrorInvalidRequest, Message: "Weixin target is invalid", CorrelationID: correlationID(req)})
		return
	}
	ownerID := ""
	if targetKind == profileWeixinTargetPersonalAssistant {
		ownerID, ok = r.profileWeixinPersonalAssistantOwner(w, req, tenantID)
		if !ok || !r.profileWeixinPersonalAssistantReady(w, req, tenantID, ownerID) {
			return
		}
	} else if agentID != "" {
		if _, found, err := r.profileWeixinBotByID(req, tenantID, agentID); err != nil {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "profile_weixin_agents_unavailable", Message: err.Error(), CorrelationID: correlationID(req)})
			return
		} else if !found {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "weixin_bot_not_found", Message: "没有这个 bot。", CorrelationID: correlationID(req)})
			return
		}
	}
	record, err := r.gatewayStore().LoadOrDefault()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "gateway_settings_invalid", Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	if !record.Config.Adapters.Weixin.Enabled {
		writeJSON(w, http.StatusPreconditionFailed, ErrorResponse{Code: "weixin_gateway_disabled", Message: "微信 Gateway 尚未由管理员启用。", CorrelationID: correlationID(req)})
		return
	}
	config := overlayGatewayWeixinLoginConfig(record.Config.Adapters.Weixin, gatewayWeixinLoginStartRequest{
		AgentID:          agentID,
		OwnerID:          ownerID,
		TargetKind:       targetKind,
		HumanProfileID:   humanID,
		HumanDisplayName: displayName,
		TenantID:         tenantID,
	})
	result, err := r.gatewayWeixinLogins.start(req.Context(), config)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, ErrorResponse{Code: "weixin_login_start_failed", Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (r *Router) handleGetProfileWeixinLoginStatus(w http.ResponseWriter, req *http.Request, sessionKey string) {
	humanID, _, ok := r.profileWeixinHuman(w, req)
	if !ok {
		return
	}
	tenantID, ok := r.profileWeixinTenant(w, req)
	if !ok {
		return
	}
	if !r.profileWeixinSessionMatches(w, req, sessionKey, tenantID, humanID) {
		return
	}
	status, err := r.gatewayWeixinLogins.poll(req.Context(), sessionKey)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, ErrorResponse{Code: "weixin_login_poll_failed", Message: err.Error(), CorrelationID: correlationID(req)})
		return
	}
	if status.Status == "already_connected" {
		record, rebound, err := r.rebindKnownGatewayWeixinAccount(req, status)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "weixin_login_rebind_failed", Message: err.Error(), CorrelationID: correlationID(req)})
			return
		}
		if rebound.BoundUserID != "" || rebound.BoundHumanID != "" {
			writeJSON(w, http.StatusOK, gatewayWeixinLoginStatusResponseDTO{
				SessionKey:   sessionKey,
				Status:       "connected",
				Message:      gatewayWeixinRebindMessage(rebound),
				Connected:    true,
				AccountID:    rebound.AccountID,
				BaseURL:      platformExtraString(record.Config.Adapters.Weixin, "ilinkBaseUrl"),
				UserID:       rebound.UserID,
				BoundUserID:  rebound.BoundUserID,
				AgentID:      rebound.AgentID,
				HumanID:      rebound.HumanID,
				BoundHumanID: rebound.BoundHumanID,
				TenantID:     tenantID,
			})
			r.gatewayWeixinLogins.finish(sessionKey)
			return
		}
		if rebound.Status != "" {
			status = rebound
		}
	}
	if status.Status == "confirmed" {
		record, err := r.saveGatewayWeixinLogin(req, status)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "weixin_login_save_failed", Message: err.Error(), CorrelationID: correlationID(req)})
			return
		}
		bindingAgentID := r.gatewayWeixinLogins.sessionBindingAgentID(sessionKey)
		bindingHumanID := r.gatewayWeixinLogins.sessionBindingHumanID(sessionKey)
		boundUserID := gatewayWeixinFirstNonBlank(status.UserID, status.AccountID)
		writeJSON(w, http.StatusOK, gatewayWeixinLoginStatusResponseDTO{
			SessionKey:   sessionKey,
			Status:       "connected",
			Message:      "扫码成功，微信已绑定到当前账号。",
			Connected:    true,
			AccountID:    status.AccountID,
			BaseURL:      platformExtraString(record.Config.Adapters.Weixin, "ilinkBaseUrl"),
			UserID:       status.UserID,
			BoundUserID:  boundUserID,
			AgentID:      bindingAgentID,
			HumanID:      bindingHumanID,
			BoundHumanID: bindingHumanID,
			TenantID:     tenantID,
		})
		r.gatewayWeixinLogins.finish(sessionKey)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (r *Router) handleSubmitProfileWeixinVerifyCode(w http.ResponseWriter, req *http.Request, sessionKey string) {
	humanID, _, ok := r.profileWeixinHuman(w, req)
	if !ok {
		return
	}
	tenantID, ok := r.profileWeixinTenant(w, req)
	if !ok {
		return
	}
	if !r.profileWeixinSessionMatches(w, req, sessionKey, tenantID, humanID) {
		return
	}
	r.submitProfileWeixinVerifyCode(w, req, sessionKey)
}

func (r *Router) profileWeixinSessionMatches(w http.ResponseWriter, req *http.Request, sessionKey string, tenantID string, humanID string) bool {
	bindingHumanID := r.gatewayWeixinLogins.sessionBindingHumanID(sessionKey)
	bindingTenantID := r.gatewayWeixinLogins.sessionBindingTenantID(sessionKey)
	if bindingHumanID == "" {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "weixin_login_invalid_session", Message: "微信绑定会话不存在或已过期。", CorrelationID: correlationID(req)})
		return false
	}
	if bindingHumanID != humanID || bindingTenantID != tenantID {
		writeJSON(w, http.StatusForbidden, ErrorResponse{Code: "weixin_login_forbidden", Message: "不能访问其他用户的微信绑定会话。", CorrelationID: correlationID(req)})
		return false
	}
	return true
}

func (r *Router) profileWeixinView(req *http.Request, tenantID string, humanID string) (profileWeixinDTO, error) {
	record, err := r.gatewayStore().LoadOrDefault()
	if err != nil {
		return profileWeixinDTO{}, err
	}
	extra := record.Config.Adapters.Weixin.Extra
	userID := gatewayWeixinUserIDForHuman(extra, tenantID, humanID)
	defaultAgentID := ""
	activeAgentID := ""
	targetKind := profileWeixinTargetPersonalAssistant
	if userID != "" {
		targetKind = profileWeixinTargetAgent
		defaultAgentID = profileWeixinScopedBindingValue(profileStringBindingsExtra(extra["agentBindings"]), tenantID, userID)
		activeAgentID = profileWeixinScopedBindingValue(profileStringBindingsExtra(extra["activeAgentBindings"]), tenantID, userID)
		if profileWeixinScopedBindingValue(profileStringBindingsExtra(extra["personalAssistantBindings"]), tenantID, userID) != "" {
			targetKind = profileWeixinTargetPersonalAssistant
		}
	}
	currentAgentID := activeAgentID
	if currentAgentID == "" {
		currentAgentID = defaultAgentID
	}
	bots, err := r.profileWeixinBots(req, tenantID)
	if err != nil {
		return profileWeixinDTO{}, err
	}
	for i := range bots {
		bots[i].Default = bots[i].AgentID == defaultAgentID
		bots[i].Current = bots[i].AgentID == currentAgentID
	}
	return profileWeixinDTO{
		TenantID:       tenantID,
		HumanID:        humanID,
		WeixinBound:    userID != "",
		WeixinUserID:   userID,
		DefaultAgentID: defaultAgentID,
		ActiveAgentID:  activeAgentID,
		CurrentAgentID: currentAgentID,
		TargetKind:     targetKind,
		Agents:         bots,
	}, nil
}

func profileWeixinScopedBindingValue(bindings map[string]string, tenantID string, userID string) string {
	value := strings.TrimSpace(bindings[gwconfig.WeixinBindingKey(tenantID, userID)])
	if value == "" && gwconfig.NormalizeTenantID(tenantID) == gwconfig.DefaultTenantID {
		value = strings.TrimSpace(bindings[strings.TrimSpace(userID)])
	}
	return value
}

func normalizeProfileWeixinTargetKind(targetKind string, agentID string) (string, bool) {
	targetKind = strings.ToLower(strings.TrimSpace(targetKind))
	if targetKind == "" {
		return profileWeixinTargetAgent, true
	}
	switch targetKind {
	case profileWeixinTargetAgent:
		return targetKind, true
	case profileWeixinTargetPersonalAssistant:
		return targetKind, true
	default:
		return "", false
	}
}

func (r *Router) profileWeixinPersonalAssistantOwner(w http.ResponseWriter, req *http.Request, tenantID string) (string, bool) {
	principal, ok := identity.PrincipalFromContext(req.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Code: ErrorUnauthorized, Message: "authorization is required", CorrelationID: correlationID(req)})
		return "", false
	}
	principal = identity.NormalizePrincipal(principal)
	if principal.TenantID != "" && strings.TrimSpace(principal.TenantID) != strings.TrimSpace(tenantID) {
		writeJSON(w, http.StatusForbidden, ErrorResponse{Code: ErrorForbidden, Message: "personal assistant tenant does not match the Weixin binding", CorrelationID: correlationID(req)})
		return "", false
	}
	ownerID := accountOwnerIDFromPrincipal(principal)
	if ownerID == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: ErrorInvalidRequest, Message: "personal assistant owner identity is missing", CorrelationID: correlationID(req)})
		return "", false
	}
	return ownerID, true
}

func (r *Router) profileWeixinPersonalAssistantReady(w http.ResponseWriter, req *http.Request, tenantID string, ownerID string) bool {
	if r.personalAssistant == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "personal assistant configuration is unavailable", CorrelationID: correlationID(req), Retryable: true})
		return false
	}
	view, err := r.personalAssistant.GetPersonalAssistant(req.Context(), devicedirectory.OwnerScope{TenantID: tenantID, OwnerID: ownerID})
	if err != nil {
		writePersonalAssistantError(w, req, err)
		return false
	}
	if strings.TrimSpace(view.AgentProfileID) == "" {
		writeJSON(w, http.StatusPreconditionFailed, ErrorResponse{Code: ErrorInvalidRequest, Message: "personal assistant AgentProfile is not provisioned", CorrelationID: correlationID(req)})
		return false
	}
	return true
}

func (r *Router) profileWeixinBotByID(req *http.Request, tenantID string, agentID string) (profileWeixinBotDTO, bool, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return profileWeixinBotDTO{}, false, nil
	}
	bots, err := r.profileWeixinBots(req, tenantID)
	if err != nil {
		return profileWeixinBotDTO{}, false, err
	}
	for _, bot := range bots {
		if bot.AgentID == agentID {
			return bot, true, nil
		}
	}
	return profileWeixinBotDTO{}, false, nil
}

func (r *Router) profileWeixinBots(req *http.Request, tenantID string) ([]profileWeixinBotDTO, error) {
	if r.agentProfiles == nil {
		return nil, fmt.Errorf("agent profile directory is not configured")
	}
	page, err := r.agentProfiles.QueryDirectory(req.Context(), agentcore.AgentDirectoryQuery{
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
	bots := make([]profileWeixinBotDTO, 0, len(page.Entries))
	for _, entry := range page.Entries {
		if entry.AgentKind != agentcore.AgentKindAI || !entry.Enabled || strings.TrimSpace(entry.AgentProfileID.String()) == "" {
			continue
		}
		bots = append(bots, profileWeixinBotDTO{
			AgentID:     entry.AgentProfileID.String(),
			DisplayName: firstNonEmpty(strings.TrimSpace(entry.DisplayName), strings.TrimSpace(entry.RealName), entry.AgentProfileID.String()),
			RealName:    strings.TrimSpace(entry.RealName),
			RoleName:    strings.TrimSpace(entry.RoleName),
			Enabled:     true,
		})
	}
	sort.SliceStable(bots, func(i, j int) bool {
		return strings.ToLower(bots[i].DisplayName) < strings.ToLower(bots[j].DisplayName)
	})
	return bots, nil
}

func (r *Router) profileWeixinHuman(w http.ResponseWriter, req *http.Request) (string, string, bool) {
	humanID, displayName := profileHumanFromRequest(req)
	if humanID == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "profile_human_missing", Message: "当前账号没有绑定系统真人用户，无法配置微信。", CorrelationID: correlationID(req)})
		return "", "", false
	}
	return humanID, displayName, true
}

func (r *Router) profileWeixinTenant(w http.ResponseWriter, req *http.Request) (string, bool) {
	if principal, ok := identity.PrincipalFromContext(req.Context()); ok {
		principal = identity.NormalizePrincipal(principal)
		tenantID := strings.TrimSpace(principal.TenantID)
		if r.agentProfiles != nil && strings.TrimSpace(principal.AgentProfileID) != "" {
			profile, err := r.agentProfiles.GetProfile(req.Context(), agentcore.AgentProfileID(principal.AgentProfileID))
			if err != nil {
				status := http.StatusServiceUnavailable
				code := "profile_tenant_unavailable"
				message := "Profile tenant validation is temporarily unavailable."
				if errors.Is(err, agentcore.ErrNotFound) {
					status = http.StatusForbidden
					code = "profile_tenant_profile_not_found"
					message = "The authenticated human profile was not found."
				}
				writeJSON(w, status, ErrorResponse{Code: code, Message: message, CorrelationID: correlationID(req)})
				return "", false
			}
			profileTenantID := strings.TrimSpace(string(profile.OrgIdentity.OrganizationRef.ID))
			if tenantID != "" && profileTenantID != "" && tenantID != profileTenantID {
				writeJSON(w, http.StatusForbidden, ErrorResponse{Code: "profile_tenant_mismatch", Message: "认证租户与真人档案组织不一致。", CorrelationID: correlationID(req)})
				return "", false
			}
			if tenantID == "" {
				tenantID = profileTenantID
			}
		}
		if tenantID != "" {
			return tenantID, true
		}
		if principal.ViewerKind != identity.ViewerKindSystem {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "profile_tenant_missing", Message: "当前账号没有租户上下文，无法配置微信。", CorrelationID: correlationID(req)})
			return "", false
		}
	}
	if internalViewerAuthorizationMatches(req) {
		if tenantID := strings.TrimSpace(req.Header.Get("X-Tenant-ID")); tenantID != "" {
			return tenantID, true
		}
	}
	writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "profile_tenant_missing", Message: "当前请求缺少可信租户上下文。", CorrelationID: correlationID(req)})
	return "", false
}

func profileHumanFromRequest(req *http.Request) (string, string) {
	if principal, ok := identity.PrincipalFromContext(req.Context()); ok {
		humanID := strings.TrimSpace(principal.AgentProfileID)
		displayName := firstNonEmpty(strings.TrimSpace(principal.DisplayName), strings.TrimSpace(principal.Username), strings.TrimSpace(principal.UserID))
		return humanID, displayName
	}
	if !internalViewerAuthorizationMatches(req) {
		return "", displayNameFromRequest(req)
	}
	humanID := strings.TrimSpace(req.Header.Get("X-Agent-Profile-ID"))
	displayName := displayNameFromRequest(req)
	if humanID != "" {
		return humanID, displayName
	}
	viewer, err := ViewerFromRequest(req)
	if err == nil {
		return strings.TrimSpace(viewer.ViewerID), displayName
	}
	return "", displayName
}

func ensureWeixinExtra(cfg *gwconfig.Config) map[string]any {
	if cfg.Adapters.Weixin.Extra == nil {
		cfg.Adapters.Weixin.Extra = map[string]any{}
	}
	return cfg.Adapters.Weixin.Extra
}

func gatewayWeixinUserIDForHuman(extra map[string]any, tenantID string, humanID string) string {
	tenantID = gwconfig.NormalizeTenantID(tenantID)
	humanID = strings.TrimSpace(humanID)
	if humanID == "" || extra == nil {
		return ""
	}
	bindings := profileStringBindingsExtra(extra["weixinUserBindings"])
	users := make([]string, 0, len(bindings))
	for bindingKey, boundHumanID := range bindings {
		bindingTenantID, userID, ok := gwconfig.ParseWeixinBindingKey(bindingKey)
		if ok && bindingTenantID == tenantID && strings.TrimSpace(boundHumanID) == humanID {
			users = append(users, userID)
		}
	}
	sort.Strings(users)
	if len(users) == 0 {
		return ""
	}
	return users[0]
}

func replaceGatewayWeixinHumanBinding(extra map[string]any, tenantID string, userID string, humanID string) {
	tenantID = gwconfig.NormalizeTenantID(tenantID)
	userID = strings.TrimSpace(userID)
	humanID = strings.TrimSpace(humanID)
	if userID == "" || humanID == "" {
		return
	}
	for existingKey, boundHumanID := range profileStringBindingsExtra(extra["weixinUserBindings"]) {
		existingTenantID, existingUserID, ok := gwconfig.ParseWeixinBindingKey(existingKey)
		if ok && existingTenantID == tenantID && existingUserID != userID && strings.TrimSpace(boundHumanID) == humanID {
			removeGatewayWeixinBinding(extra, tenantID, existingUserID)
		}
	}
	bindings := map[string]any{}
	for existingUserID, boundHumanID := range profileStringBindingsExtra(extra["weixinUserBindings"]) {
		bindings[existingUserID] = boundHumanID
	}
	if tenantID == gwconfig.DefaultTenantID {
		delete(bindings, userID)
	}
	bindings[gwconfig.WeixinBindingKey(tenantID, userID)] = humanID
	extra["weixinUserBindings"] = bindings
}

func profileStringBindingsExtra(value any) map[string]string {
	raw := map[string]any{}
	switch typed := value.(type) {
	case map[string]any:
		raw = typed
	case map[string]string:
		for key, value := range typed {
			raw[key] = value
		}
	default:
		return nil
	}
	if len(raw) == 0 {
		return nil
	}
	out := map[string]string{}
	for key, value := range raw {
		text := profileStringBindingValue(value)
		if text == "" {
			continue
		}
		out[strings.TrimSpace(key)] = text
	}
	return out
}

func profileStringBindingValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}

func upsertGatewayWeixinActiveAgentBinding(extra map[string]any, tenantID string, userID string, agentID string) {
	bindingKey := gwconfig.WeixinBindingKey(tenantID, userID)
	agentID = strings.TrimSpace(agentID)
	if bindingKey == "" || agentID == "" {
		return
	}
	bindings := map[string]any{}
	switch typed := extra["activeAgentBindings"].(type) {
	case map[string]any:
		bindings = typed
	case map[string]string:
		for key, value := range typed {
			bindings[key] = value
		}
	}
	if gwconfig.NormalizeTenantID(tenantID) == gwconfig.DefaultTenantID {
		delete(bindings, strings.TrimSpace(userID))
	}
	bindings[bindingKey] = agentID
	extra["activeAgentBindings"] = bindings
}

func removeGatewayWeixinBinding(extra map[string]any, tenantID string, userID string) {
	bindingKey := gwconfig.WeixinBindingKey(tenantID, userID)
	if extra == nil || bindingKey == "" {
		return
	}
	for _, key := range []string{"agentBindings", "activeAgentBindings", "personalAssistantBindings", "weixinUserBindings", "weixinAccounts", "accounts"} {
		raw, ok := extra[key].(map[string]any)
		if !ok {
			continue
		}
		delete(raw, bindingKey)
		if gwconfig.NormalizeTenantID(tenantID) == gwconfig.DefaultTenantID {
			delete(raw, strings.TrimSpace(userID))
		}
		extra[key] = raw
	}
}

func upsertGatewayWeixinPersonalAssistantBinding(extra map[string]any, tenantID string, userID string, ownerID string) {
	bindingKey := gwconfig.WeixinBindingKey(tenantID, userID)
	ownerID = strings.TrimSpace(ownerID)
	if bindingKey == "" || ownerID == "" {
		return
	}
	bindings := map[string]any{}
	for key, value := range profileStringBindingsExtra(extra["personalAssistantBindings"]) {
		bindings[key] = value
	}
	if gwconfig.NormalizeTenantID(tenantID) == gwconfig.DefaultTenantID {
		delete(bindings, strings.TrimSpace(userID))
	}
	bindings[bindingKey] = ownerID
	extra["personalAssistantBindings"] = bindings
}

func removeGatewayWeixinPersonalAssistantBinding(extra map[string]any, tenantID string, userID string) {
	if extra == nil {
		return
	}
	bindingKey := gwconfig.WeixinBindingKey(tenantID, userID)
	raw := map[string]any{}
	for key, value := range profileStringBindingsExtra(extra["personalAssistantBindings"]) {
		raw[key] = value
	}
	delete(raw, bindingKey)
	if gwconfig.NormalizeTenantID(tenantID) == gwconfig.DefaultTenantID {
		delete(raw, strings.TrimSpace(userID))
	}
	extra["personalAssistantBindings"] = raw
}
