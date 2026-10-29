package httptransport

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	gwbindings "orrery_v2/internal/gateway/binding"
	"orrery_v2/internal/gateway/channel/dingtalk"
	"orrery_v2/internal/gateway/channel/feishu"
	gwconfig "orrery_v2/internal/gateway/config"
	"orrery_v2/internal/gateway/protocol"
)

const maxGatewayBindingRequestBytes = 64 << 10

type profileGatewayBindingsDTO struct {
	Bindings []gwbindings.Binding              `json:"bindings"`
	Accounts []profileGatewayBindingAccountDTO `json:"accounts"`
}

type profileGatewayBindingAccountDTO struct {
	ChannelKey       string `json:"channel_key"`
	AccountID        string `json:"account_id"`
	AgentID          string `json:"agent_id"`
	AgentDisplayName string `json:"agent_display_name,omitempty"`
	Enabled          bool   `json:"enabled"`
	Status           string `json:"status"`
	InboundEnabled   bool   `json:"inbound_enabled"`
}

type profileGatewayBindingCodeRequest struct {
	AccountID string `json:"account_id,omitempty"`
	AgentID   string `json:"agent_id"`
}

type profileGatewayBindingCodeDTO struct {
	ChannelKey string `json:"channel_key"`
	AccountID  string `json:"account_id"`
	Code       string `json:"code"`
	Command    string `json:"command"`
	ExpiresAt  string `json:"expires_at"`
}

func (r *Router) handleGetProfileGatewayBindings(w http.ResponseWriter, req *http.Request) {
	humanID, _, ok := r.profileWeixinHuman(w, req)
	if !ok {
		return
	}
	tenantID, ok := r.profileWeixinTenant(w, req)
	if !ok {
		return
	}
	if r.gatewayChannelBindings == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: "gateway_bindings_unavailable", Message: "Gateway binding service is unavailable.", CorrelationID: correlationID(req)})
		return
	}
	bindings, err := r.gatewayChannelBindings.ListByHuman(req.Context(), tenantID, humanID)
	if err != nil {
		status := http.StatusServiceUnavailable
		code := "gateway_bindings_unavailable"
		message := "Gateway binding service is temporarily unavailable."
		if errors.Is(err, gwbindings.ErrInvalidInput) {
			status = http.StatusBadRequest
			code = "gateway_bindings_invalid"
			message = err.Error()
		}
		writeJSON(w, status, ErrorResponse{Code: code, Message: message, CorrelationID: correlationID(req)})
		return
	}
	accounts, err := r.profileGatewayBindingAccounts(req, tenantID)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: "gateway_binding_accounts_unavailable", Message: "Gateway account assignments are temporarily unavailable.", CorrelationID: correlationID(req)})
		return
	}
	writeJSON(w, http.StatusOK, profileGatewayBindingsDTO{Bindings: bindings, Accounts: accounts})
}

func (r *Router) handleCreateProfileGatewayBindingCode(w http.ResponseWriter, req *http.Request, channelKey string) {
	humanID, _, ok := r.profileWeixinHuman(w, req)
	if !ok {
		return
	}
	tenantID, ok := r.profileWeixinTenant(w, req)
	if !ok {
		return
	}
	if r.gatewayChannelBindings == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: "gateway_bindings_unavailable", Message: "Gateway binding service is unavailable.", CorrelationID: correlationID(req)})
		return
	}
	if req.Body != nil {
		req.Body = http.MaxBytesReader(w, req.Body, maxGatewayBindingRequestBytes)
	}
	var body profileGatewayBindingCodeRequest
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "invalid_json", Message: "The request body is invalid.", CorrelationID: correlationID(req)})
		return
	}
	if err := rejectTrailingGatewayJSON(decoder); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "invalid_json", Message: "The request body is invalid.", CorrelationID: correlationID(req)})
		return
	}
	channelKey = strings.ToLower(strings.TrimSpace(channelKey))
	if channelKey != string(protocol.ChannelFeishu) && channelKey != string(protocol.ChannelDingTalk) {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "unsupported_gateway_binding_channel", Message: "Only Feishu and DingTalk binding codes are supported.", CorrelationID: correlationID(req)})
		return
	}
	agentID := strings.TrimSpace(body.AgentID)
	if agentID == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "gateway_binding_agent_required", Message: "Select a default Bot before binding the channel.", CorrelationID: correlationID(req)})
		return
	}
	record, err := r.gatewayStore().LoadOrDefault()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: "gateway_settings_unavailable", Message: "Gateway settings are temporarily unavailable.", CorrelationID: correlationID(req)})
		return
	}
	accountID := ""
	switch channelKey {
	case string(protocol.ChannelFeishu):
		accountID = strings.TrimSpace(body.AccountID)
		if accountID == "" {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "gateway_binding_account_required", Message: "Select a Feishu account before creating a binding code.", CorrelationID: correlationID(req)})
			return
		}
		if !r.validateProfileFeishuBindingAccount(w, req, record.Config.Adapters.Feishu, tenantID, accountID, agentID) {
			return
		}
	case string(protocol.ChannelDingTalk):
		if !record.Config.Adapters.DingTalk.Enabled {
			writeJSON(w, http.StatusPreconditionFailed, ErrorResponse{Code: "dingtalk_gateway_disabled", Message: "DingTalk Gateway is not enabled.", CorrelationID: correlationID(req)})
			return
		}
		accountID = dingtalk.OptionsFromConfig(record.Config.Adapters.DingTalk).AccountID()
	}
	if accountID == "" {
		writeJSON(w, http.StatusPreconditionFailed, ErrorResponse{Code: "gateway_channel_account_missing", Message: "The channel application is not configured.", CorrelationID: correlationID(req)})
		return
	}
	if r.gatewayAccounts == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: "gateway_channel_status_unavailable", Message: "Gateway channel status is unavailable.", CorrelationID: correlationID(req)})
		return
	}
	if !r.profileGatewayAccountConnected(channelKey, accountID) {
		writeJSON(w, http.StatusPreconditionFailed, ErrorResponse{Code: "gateway_channel_not_connected", Message: "The channel application is not connected.", CorrelationID: correlationID(req)})
		return
	}
	if _, found, err := r.profileWeixinBotByID(req, tenantID, agentID); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: "gateway_binding_agents_unavailable", Message: "Gateway Bot validation is temporarily unavailable.", CorrelationID: correlationID(req)})
		return
	} else if !found {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "gateway_binding_agent_not_found", Message: "The selected Bot is not available in this tenant.", CorrelationID: correlationID(req)})
		return
	}
	code, err := r.gatewayChannelBindings.CreateCode(req.Context(), gwbindings.CreateCodeInput{
		TenantID: tenantID, ChannelKey: channelKey, AccountID: accountID, HumanProfileID: humanID, DefaultAgentID: agentID,
	})
	if err != nil {
		status := http.StatusServiceUnavailable
		code := "gateway_binding_code_unavailable"
		message := "Gateway binding code service is temporarily unavailable."
		if errors.Is(err, gwbindings.ErrInvalidInput) {
			status = http.StatusBadRequest
			code = "gateway_binding_code_invalid"
			message = err.Error()
		}
		writeJSON(w, status, ErrorResponse{Code: code, Message: message, CorrelationID: correlationID(req)})
		return
	}
	writeJSON(w, http.StatusOK, profileGatewayBindingCodeDTO{
		ChannelKey: channelKey, AccountID: accountID, Code: code.Value, Command: "/绑定 " + code.Value, ExpiresAt: code.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
	})
}

func (r *Router) validateProfileFeishuBindingAccount(w http.ResponseWriter, req *http.Request, config gwconfig.FeishuAdapterConfig, tenantID, accountID, agentID string) bool {
	if !config.Enabled {
		writeJSON(w, http.StatusPreconditionFailed, ErrorResponse{Code: "feishu_gateway_disabled", Message: "Feishu Gateway is not enabled.", CorrelationID: correlationID(req)})
		return false
	}
	if len(config.Accounts) > 0 {
		account, configured := config.Accounts[accountID]
		if !configured {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "gateway_channel_account_missing", Message: "The Feishu account is not configured.", CorrelationID: correlationID(req)})
			return false
		}
		if !account.Enabled {
			writeJSON(w, http.StatusPreconditionFailed, ErrorResponse{Code: "feishu_gateway_account_disabled", Message: "The Feishu account is disabled.", CorrelationID: correlationID(req)})
			return false
		}
	} else {
		legacyAccountID := feishu.OptionsFromConfig(config).AccountID()
		if legacyAccountID == "" || accountID != legacyAccountID {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "gateway_channel_account_missing", Message: "The Feishu account is not configured.", CorrelationID: correlationID(req)})
			return false
		}
	}
	if r.gatewayAccounts == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: "gateway_channel_status_unavailable", Message: "Gateway channel status is unavailable.", CorrelationID: correlationID(req)})
		return false
	}
	if !r.profileGatewayAccountConnected(string(protocol.ChannelFeishu), accountID) {
		writeJSON(w, http.StatusPreconditionFailed, ErrorResponse{Code: "gateway_channel_not_connected", Message: "The channel application is not connected.", CorrelationID: correlationID(req)})
		return false
	}
	ownership, err := r.gatewayChannelBindings.ResolveAgentChannelBinding(req.Context(), string(protocol.ChannelFeishu), accountID)
	if errors.Is(err, gwbindings.ErrNotFound) {
		writeJSON(w, http.StatusPreconditionFailed, ErrorResponse{Code: "feishu_gateway_account_unowned", Message: "The Feishu account is not assigned to an employee.", CorrelationID: correlationID(req)})
		return false
	}
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: "gateway_binding_accounts_unavailable", Message: "Gateway account assignments are temporarily unavailable.", CorrelationID: correlationID(req)})
		return false
	}
	if ownership.TenantID != tenantID {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "gateway_channel_account_missing", Message: "The Feishu account is not configured.", CorrelationID: correlationID(req)})
		return false
	}
	if ownership.AgentProfileID != agentID {
		writeJSON(w, http.StatusConflict, ErrorResponse{Code: "feishu_gateway_account_owner_mismatch", Message: "The Feishu account is assigned to a different employee.", CorrelationID: correlationID(req)})
		return false
	}
	return true
}

func (r *Router) profileGatewayAccountConnected(channelKey, accountID string) bool {
	if r == nil || r.gatewayAccounts == nil {
		return false
	}
	for _, account := range r.gatewayAccounts.GatewayAccountStatuses() {
		if strings.EqualFold(strings.TrimSpace(account.ChannelKey), channelKey) && strings.TrimSpace(account.AccountID) == accountID && account.Enabled && account.Status == "connected" {
			return true
		}
	}
	return false
}

func (r *Router) profileGatewayBindingAccounts(req *http.Request, tenantID string) ([]profileGatewayBindingAccountDTO, error) {
	if r == nil || r.gatewaySettings == nil || r.gatewayChannelBindings == nil || r.agentProfiles == nil {
		return nil, errors.New("profile Gateway account dependencies are unavailable")
	}
	record, err := r.gatewaySettings.LoadOrDefault()
	if err != nil {
		return nil, err
	}
	accounts := make([]profileGatewayBindingAccountDTO, 0, len(record.Config.Adapters.Feishu.Accounts))
	for accountID, config := range record.Config.Adapters.Feishu.Accounts {
		accountID = strings.TrimSpace(accountID)
		if accountID == "" {
			continue
		}
		ownership, err := r.gatewayChannelBindings.ResolveAgentChannelBinding(req.Context(), string(protocol.ChannelFeishu), accountID)
		if errors.Is(err, gwbindings.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if ownership.TenantID != tenantID {
			continue
		}
		owner, err := r.agentProfiles.GetProfile(req.Context(), agentcore.AgentProfileID(ownership.AgentProfileID))
		if errors.Is(err, agentcore.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		status := "unavailable"
		if r.gatewayAccounts != nil {
			for _, candidate := range r.gatewayAccounts.GatewayAccountStatuses() {
				if strings.EqualFold(strings.TrimSpace(candidate.ChannelKey), string(protocol.ChannelFeishu)) && strings.TrimSpace(candidate.AccountID) == accountID {
					status = firstNonEmpty(strings.TrimSpace(candidate.Status), status)
					break
				}
			}
		}
		accounts = append(accounts, profileGatewayBindingAccountDTO{
			ChannelKey: string(protocol.ChannelFeishu), AccountID: accountID, AgentID: ownership.AgentProfileID,
			AgentDisplayName: workforceChannelProfileName(owner), Enabled: record.Config.Adapters.Feishu.Enabled && config.Enabled,
			Status: status, InboundEnabled: ownership.InboundEnabled,
		})
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].AccountID < accounts[j].AccountID })
	return accounts, nil
}

func (r *Router) handleDeleteProfileGatewayBinding(w http.ResponseWriter, req *http.Request, bindingID string) {
	humanID, _, ok := r.profileWeixinHuman(w, req)
	if !ok {
		return
	}
	tenantID, ok := r.profileWeixinTenant(w, req)
	if !ok {
		return
	}
	if r.gatewayChannelBindings == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: "gateway_bindings_unavailable", Message: "Gateway binding service is unavailable.", CorrelationID: correlationID(req)})
		return
	}
	if err := r.gatewayChannelBindings.Revoke(req.Context(), tenantID, humanID, strings.TrimSpace(bindingID)); err != nil {
		status := http.StatusServiceUnavailable
		code := "gateway_bindings_unavailable"
		message := "Gateway binding service is temporarily unavailable."
		if errors.Is(err, gwbindings.ErrNotFound) {
			status = http.StatusNotFound
			code = "gateway_binding_not_found"
			message = err.Error()
		} else if errors.Is(err, gwbindings.ErrInvalidInput) {
			status = http.StatusBadRequest
			code = "gateway_binding_delete_invalid"
			message = err.Error()
		}
		writeJSON(w, status, ErrorResponse{Code: code, Message: message, CorrelationID: correlationID(req)})
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}
