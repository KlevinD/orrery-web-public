package httptransport

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/identity"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/operations"
	gwbindings "orrery_v2/internal/gateway/binding"
	"orrery_v2/internal/gateway/channel"
)

type workforceChannelBindingsDTO struct {
	AgentProfileID string                       `json:"agent_profile_id"`
	Accounts       []workforceChannelAccountDTO `json:"accounts"`
}

type workforceChannelAccountDTO struct {
	ChannelKey          string    `json:"channel_key"`
	AccountID           string    `json:"account_id"`
	Enabled             bool      `json:"enabled"`
	Status              string    `json:"status"`
	OwnerAgentProfileID string    `json:"owner_agent_profile_id,omitempty"`
	OwnerDisplayName    string    `json:"owner_display_name,omitempty"`
	InboundEnabled      bool      `json:"inbound_enabled"`
	Revision            int64     `json:"revision"`
	CreatedAt           time.Time `json:"created_at,omitempty"`
	UpdatedAt           time.Time `json:"updated_at,omitempty"`
}

type putWorkforceChannelBindingRequest struct {
	InboundEnabled              bool   `json:"inbound_enabled"`
	ExpectedRevision            int64  `json:"expected_revision"`
	ExpectedOwnerAgentProfileID string `json:"expected_owner_agent_profile_id"`
}

type deleteWorkforceChannelBindingRequest struct {
	ExpectedRevision            int64  `json:"expected_revision"`
	ExpectedOwnerAgentProfileID string `json:"expected_owner_agent_profile_id"`
}

func (r *Router) handleGetWorkforceChannelBindings(w http.ResponseWriter, req *http.Request, agentProfileID string) {
	if !r.authorizeWorkforceChannelSettings(w, req, false) {
		return
	}
	target, viewer, ok := r.workforceChannelTarget(w, req, agentProfileID, false)
	if !ok {
		return
	}
	accounts, err := r.workforceFeishuAccounts(req, viewer.TenantID, target)
	if err != nil {
		writeWorkforceChannelBindingError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, workforceChannelBindingsDTO{
		AgentProfileID: strings.TrimSpace(agentProfileID),
		Accounts:       accounts,
	})
}

func (r *Router) handlePutWorkforceChannelBinding(w http.ResponseWriter, req *http.Request, agentProfileID, accountID string) {
	if !r.authorizeWorkforceChannelSettings(w, req, true) {
		return
	}
	target, viewer, ok := r.workforceChannelTarget(w, req, agentProfileID, true)
	if !ok {
		return
	}
	if r.gatewayChannelBindings == nil || r.gatewaySettings == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: "workforce_channel_bindings_unavailable", Message: "Feishu account ownership is temporarily unavailable.", CorrelationID: correlationID(req)})
		return
	}
	accountID = strings.TrimSpace(accountID)
	status, configured, err := r.workforceFeishuAccountStatus(accountID)
	if err != nil {
		writeWorkforceChannelBindingError(w, req, err)
		return
	}
	if !configured {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "gateway_account_not_found", Message: "The Feishu account is not configured.", CorrelationID: correlationID(req)})
		return
	}
	var body putWorkforceChannelBindingRequest
	if !decodeWorkforceChannelBindingJSON(w, req, &body) {
		return
	}
	body.ExpectedOwnerAgentProfileID = strings.TrimSpace(body.ExpectedOwnerAgentProfileID)

	current, resolveErr := r.gatewayChannelBindings.ResolveAgentChannelBinding(req.Context(), "feishu", accountID)
	switch {
	case resolveErr == nil:
		if r.workforceChannelOwnerIsPersonalAssistant(req, current.AgentProfileID) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "gateway_account_not_found", Message: "The Feishu account is not configured.", CorrelationID: correlationID(req)})
			return
		}
		if current.TenantID != viewer.TenantID {
			writeWorkforceChannelBindingConflict(w, req, "The Feishu account is already owned by another tenant.")
			return
		}
		if current.Revision != body.ExpectedRevision || current.AgentProfileID != body.ExpectedOwnerAgentProfileID {
			writeWorkforceChannelBindingConflict(w, req, "The Feishu account ownership changed. Refresh and try again.")
			return
		}
	case errors.Is(resolveErr, gwbindings.ErrNotFound):
		if body.ExpectedRevision != 0 || body.ExpectedOwnerAgentProfileID != "" {
			writeWorkforceChannelBindingConflict(w, req, "The Feishu account ownership changed. Refresh and try again.")
			return
		}
	case resolveErr != nil:
		writeWorkforceChannelBindingError(w, req, resolveErr)
		return
	}

	binding, err := r.gatewayChannelBindings.PutAgentChannelBinding(req.Context(), gwbindings.PutAgentChannelBindingInput{
		TenantID:               viewer.TenantID,
		ChannelKey:             "feishu",
		AccountID:              accountID,
		AgentProfileID:         strings.TrimSpace(agentProfileID),
		InboundEnabled:         body.InboundEnabled,
		ExpectedRevision:       body.ExpectedRevision,
		ExpectedAgentProfileID: body.ExpectedOwnerAgentProfileID,
	})
	if err != nil {
		writeWorkforceChannelBindingError(w, req, err)
		return
	}
	action := "gateway.feishu_ownership.create"
	if resolveErr == nil {
		switch {
		case current.AgentProfileID != binding.AgentProfileID:
			action = "gateway.feishu_ownership.move"
		case current.InboundEnabled != binding.InboundEnabled:
			action = "gateway.feishu_ownership.toggle_inbound"
		default:
			action = "gateway.feishu_ownership.update"
		}
	}
	r.recordWorkforceChannelBindingAudit(req, viewer, action, binding, current.AgentProfileID)
	writeJSON(w, http.StatusOK, r.workforceChannelAccount(req, status, binding, true, target))
}

func (r *Router) handleDeleteWorkforceChannelBinding(w http.ResponseWriter, req *http.Request, agentProfileID, accountID string) {
	if !r.authorizeWorkforceChannelSettings(w, req, true) {
		return
	}
	_, viewer, ok := r.workforceChannelTarget(w, req, agentProfileID, false)
	if !ok {
		return
	}
	if r.gatewayChannelBindings == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: "workforce_channel_bindings_unavailable", Message: "Feishu account ownership is temporarily unavailable.", CorrelationID: correlationID(req)})
		return
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		writeWorkforceChannelBindingError(w, req, gwbindings.ErrInvalidInput)
		return
	}
	var body deleteWorkforceChannelBindingRequest
	if !decodeWorkforceChannelBindingJSON(w, req, &body) {
		return
	}
	body.ExpectedOwnerAgentProfileID = strings.TrimSpace(body.ExpectedOwnerAgentProfileID)
	current, err := r.gatewayChannelBindings.ResolveAgentChannelBinding(req.Context(), "feishu", accountID)
	if err != nil {
		writeWorkforceChannelBindingError(w, req, err)
		return
	}
	if r.workforceChannelOwnerIsPersonalAssistant(req, current.AgentProfileID) {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "gateway_account_not_found", Message: "The Feishu account is not configured.", CorrelationID: correlationID(req)})
		return
	}
	if current.TenantID != viewer.TenantID || current.AgentProfileID != strings.TrimSpace(agentProfileID) ||
		current.AgentProfileID != body.ExpectedOwnerAgentProfileID || current.Revision != body.ExpectedRevision {
		writeWorkforceChannelBindingConflict(w, req, "The Feishu account ownership changed. Refresh and try again.")
		return
	}
	if err := r.gatewayChannelBindings.DeleteOwnedAgentChannelBinding(req.Context(), viewer.TenantID, "feishu", accountID, body.ExpectedOwnerAgentProfileID, body.ExpectedRevision); err != nil {
		writeWorkforceChannelBindingError(w, req, err)
		return
	}
	r.recordWorkforceChannelBindingAudit(req, viewer, "gateway.feishu_ownership.delete", current, current.AgentProfileID)
	w.WriteHeader(http.StatusNoContent)
}

func (r *Router) recordWorkforceChannelBindingAudit(req *http.Request, viewer missioncontrol.ViewerRef, action string, binding gwbindings.AgentChannelBinding, previousOwner string) {
	if r == nil || r.auditRecorder == nil || req == nil {
		return
	}
	metadata := map[string]string{
		"tenant_id":              auditCleanValue(binding.TenantID, 256),
		"channel_key":            "feishu",
		"account_id":             auditCleanValue(binding.AccountID, 512),
		"agent_profile_id":       auditCleanValue(binding.AgentProfileID, 256),
		"inbound_enabled":        strconv.FormatBool(binding.InboundEnabled),
		"revision":               strconv.FormatInt(binding.Revision, 10),
		"previous_agent_profile": auditCleanValue(previousOwner, 256),
	}
	if metadata["previous_agent_profile"] == "" {
		delete(metadata, "previous_agent_profile")
	}
	actor := auditActorFromRequest(req)
	_ = r.auditRecorder.RecordAuditEvent(req.Context(), operations.AuditEventDTO{
		ActorID:       firstNonEmpty(actor.ActorID, viewer.ViewerID, "unknown"),
		ViewerID:      firstNonEmpty(actor.ViewerID, viewer.ViewerID),
		EntityType:    "gateway_feishu_account_ownership",
		EntityID:      "feishu:" + auditCleanValue(binding.AccountID, 512),
		Action:        action,
		Result:        "succeeded",
		Severity:      "info",
		CorrelationID: correlationID(req),
		Summary:       action + " for Feishu account " + auditCleanValue(binding.AccountID, 512),
		Metadata:      metadata,
		OccurredAt:    time.Now().UTC(),
	})
}

func (r *Router) authorizeWorkforceChannelSettings(w http.ResponseWriter, req *http.Request, manage bool) bool {
	capability := PermissionCapabilityDTO{
		Domain: "settings", Action: "read", Scope: permissionSettingsRead,
		Method: http.MethodGet, Path: "/api/settings/gateway/accounts",
	}
	if manage {
		capability.Action = "manage"
		capability.Scope = permissionSettingsManage
		capability.Method = http.MethodPut
		capability.Path = "/api/settings/gateway"
	}
	capability.CapabilityID = capabilityID(capability.Method, capability.Path, capability.Scope)
	capability.AccessMode = accessMode(capability.Method, capability.Action)
	return r.authorizeAdditionalPermissionCapability(w, req, capability)
}

func (r *Router) workforceChannelTarget(w http.ResponseWriter, req *http.Request, agentProfileID string, requireEligible bool) (agentcore.AgentProfile, missioncontrol.ViewerRef, bool) {
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return agentcore.AgentProfile{}, missioncontrol.ViewerRef{}, false
	}
	if r.agentProfiles == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: "workforce_directory_unavailable", Message: "The workforce directory is temporarily unavailable.", CorrelationID: correlationID(req)})
		return agentcore.AgentProfile{}, missioncontrol.ViewerRef{}, false
	}
	if principal, exists := identity.PrincipalFromContext(req.Context()); exists {
		principal = identity.NormalizePrincipal(principal)
		if actorID := strings.TrimSpace(principal.AgentProfileID); actorID != "" {
			actor, actorErr := r.agentProfiles.GetProfile(req.Context(), agentcore.AgentProfileID(actorID))
			if errors.Is(actorErr, agentcore.ErrNotFound) {
				writeJSON(w, http.StatusForbidden, ErrorResponse{Code: "workforce_channel_actor_not_found", Message: "The authenticated workforce member was not found.", CorrelationID: correlationID(req)})
				return agentcore.AgentProfile{}, missioncontrol.ViewerRef{}, false
			}
			if actorErr != nil {
				writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: "workforce_directory_unavailable", Message: "The workforce directory is temporarily unavailable.", CorrelationID: correlationID(req)})
				return agentcore.AgentProfile{}, missioncontrol.ViewerRef{}, false
			}
			actorTenantID := strings.TrimSpace(string(actor.OrgIdentity.OrganizationRef.ID))
			if viewer.TenantID != "" && actorTenantID != "" && viewer.TenantID != actorTenantID {
				writeJSON(w, http.StatusForbidden, ErrorResponse{Code: "workforce_channel_tenant_mismatch", Message: "Authenticated tenant does not match the workforce organization.", CorrelationID: correlationID(req)})
				return agentcore.AgentProfile{}, missioncontrol.ViewerRef{}, false
			}
			if viewer.TenantID == "" {
				viewer.TenantID = actorTenantID
			}
		}
	}
	target, err := r.agentProfiles.GetProfile(req.Context(), agentcore.AgentProfileID(strings.TrimSpace(agentProfileID)))
	if errors.Is(err, agentcore.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "workforce_member_not_found", Message: "The workforce member was not found.", CorrelationID: correlationID(req)})
		return agentcore.AgentProfile{}, missioncontrol.ViewerRef{}, false
	}
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: "workforce_directory_unavailable", Message: "The workforce directory is temporarily unavailable.", CorrelationID: correlationID(req)})
		return agentcore.AgentProfile{}, missioncontrol.ViewerRef{}, false
	}
	if target.IsPersonalAssistant() {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "workforce_member_not_found", Message: "The workforce member was not found.", CorrelationID: correlationID(req)})
		return agentcore.AgentProfile{}, missioncontrol.ViewerRef{}, false
	}
	if strings.TrimSpace(string(target.OrgIdentity.OrganizationRef.ID)) != strings.TrimSpace(viewer.TenantID) {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "workforce_member_not_found", Message: "The workforce member was not found.", CorrelationID: correlationID(req)})
		return agentcore.AgentProfile{}, missioncontrol.ViewerRef{}, false
	}
	if requireEligible && (target.AgentKind != agentcore.AgentKindAI || !target.Enabled || target.Placement.NormalizedStatus(target.OrgIdentity) != agentcore.TalentPlacementInternal) {
		writeJSON(w, http.StatusUnprocessableEntity, ErrorResponse{Code: "workforce_channel_target_ineligible", Message: "Feishu accounts can only be assigned to enabled internal AI employees.", CorrelationID: correlationID(req)})
		return agentcore.AgentProfile{}, missioncontrol.ViewerRef{}, false
	}
	return target, viewer, true
}

func (r *Router) workforceFeishuAccounts(req *http.Request, tenantID string, target agentcore.AgentProfile) ([]workforceChannelAccountDTO, error) {
	if r.gatewayChannelBindings == nil || r.gatewaySettings == nil {
		return nil, errors.New("Feishu ownership dependencies are unavailable")
	}
	record, err := r.gatewaySettings.LoadOrDefault()
	if err != nil {
		return nil, err
	}
	aliases := make(map[string]bool, len(record.Config.Adapters.Feishu.Accounts))
	for alias, account := range record.Config.Adapters.Feishu.Accounts {
		alias = strings.TrimSpace(alias)
		if alias != "" {
			aliases[alias] = account.Enabled
		}
	}
	statuses := make(map[string]channel.AccountStatus)
	if r.gatewayAccounts != nil {
		for _, status := range r.gatewayAccounts.GatewayAccountStatuses() {
			if strings.EqualFold(strings.TrimSpace(status.ChannelKey), "feishu") && strings.TrimSpace(status.AccountID) != "" {
				status.ChannelKey = "feishu"
				status.AccountID = strings.TrimSpace(status.AccountID)
				statuses[status.AccountID] = status
				if _, exists := aliases[status.AccountID]; !exists {
					aliases[status.AccountID] = status.Enabled
				}
			}
		}
	}
	owned, err := r.gatewayChannelBindings.ListTenantAgentChannelBindings(req.Context(), tenantID)
	if err != nil {
		return nil, err
	}
	for _, binding := range owned {
		if _, exists := aliases[binding.AccountID]; !exists {
			aliases[binding.AccountID] = false
		}
	}

	accounts := make([]workforceChannelAccountDTO, 0, len(aliases))
	for alias, enabled := range aliases {
		binding, resolveErr := r.gatewayChannelBindings.ResolveAgentChannelBinding(req.Context(), "feishu", alias)
		hasBinding := resolveErr == nil
		if resolveErr != nil && !errors.Is(resolveErr, gwbindings.ErrNotFound) {
			return nil, resolveErr
		}
		if hasBinding && binding.TenantID != tenantID {
			continue
		}
		if hasBinding && r.workforceChannelOwnerIsPersonalAssistant(req, binding.AgentProfileID) {
			continue
		}
		status, live := statuses[alias]
		if !live {
			status = channel.AccountStatus{ChannelKey: "feishu", AccountID: alias, Enabled: enabled, Status: "unavailable"}
		}
		accounts = append(accounts, r.workforceChannelAccount(req, status, binding, hasBinding, target))
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].AccountID < accounts[j].AccountID })
	return accounts, nil
}

func (r *Router) workforceChannelOwnerIsPersonalAssistant(req *http.Request, agentProfileID string) bool {
	if r == nil || r.agentProfiles == nil || req == nil || strings.TrimSpace(agentProfileID) == "" {
		return false
	}
	profile, err := r.agentProfiles.GetProfile(req.Context(), agentcore.AgentProfileID(strings.TrimSpace(agentProfileID)))
	return err == nil && profile.IsPersonalAssistant()
}

func (r *Router) workforceFeishuAccountStatus(accountID string) (channel.AccountStatus, bool, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" || r.gatewaySettings == nil {
		return channel.AccountStatus{}, false, nil
	}
	record, err := r.gatewaySettings.LoadOrDefault()
	if err != nil {
		return channel.AccountStatus{}, false, err
	}
	account, configured := record.Config.Adapters.Feishu.Accounts[accountID]
	status := channel.AccountStatus{ChannelKey: "feishu", AccountID: accountID, Enabled: account.Enabled, Status: "unavailable"}
	if r.gatewayAccounts != nil {
		for _, candidate := range r.gatewayAccounts.GatewayAccountStatuses() {
			if strings.EqualFold(strings.TrimSpace(candidate.ChannelKey), "feishu") && strings.TrimSpace(candidate.AccountID) == accountID {
				candidate.ChannelKey = "feishu"
				candidate.AccountID = accountID
				return candidate, true, nil
			}
		}
	}
	return status, configured, nil
}

func (r *Router) workforceChannelAccount(req *http.Request, status channel.AccountStatus, binding gwbindings.AgentChannelBinding, hasBinding bool, target agentcore.AgentProfile) workforceChannelAccountDTO {
	result := workforceChannelAccountDTO{
		ChannelKey: "feishu", AccountID: strings.TrimSpace(status.AccountID), Enabled: status.Enabled,
		Status: firstNonEmpty(strings.TrimSpace(status.Status), "unavailable"),
	}
	if !hasBinding {
		return result
	}
	result.OwnerAgentProfileID = binding.AgentProfileID
	result.InboundEnabled = binding.InboundEnabled
	result.Revision = binding.Revision
	result.CreatedAt = binding.CreatedAt
	result.UpdatedAt = binding.UpdatedAt
	if strings.TrimSpace(binding.AgentProfileID) == strings.TrimSpace(target.ID.String()) {
		result.OwnerDisplayName = workforceChannelProfileName(target)
	} else if r.agentProfiles != nil {
		if owner, err := r.agentProfiles.GetProfile(req.Context(), agentcore.AgentProfileID(binding.AgentProfileID)); err == nil {
			result.OwnerDisplayName = workforceChannelProfileName(owner)
		}
	}
	return result
}

func workforceChannelProfileName(profile agentcore.AgentProfile) string {
	return firstNonEmpty(strings.TrimSpace(profile.DisplayName), strings.TrimSpace(profile.RealName), strings.TrimSpace(profile.RoleName), strings.TrimSpace(profile.ID.String()))
}

func decodeWorkforceChannelBindingJSON(w http.ResponseWriter, req *http.Request, target any) bool {
	if req.Body != nil {
		req.Body = http.MaxBytesReader(w, req.Body, maxGatewayBindingRequestBytes)
	}
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "invalid_json", Message: "The request body is invalid.", CorrelationID: correlationID(req)})
		return false
	}
	if err := rejectTrailingGatewayJSON(decoder); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "invalid_json", Message: "The request body is invalid.", CorrelationID: correlationID(req)})
		return false
	}
	return true
}

func writeWorkforceChannelBindingConflict(w http.ResponseWriter, req *http.Request, message string) {
	writeJSON(w, http.StatusConflict, ErrorResponse{Code: "workforce_channel_binding_conflict", Message: message, CorrelationID: correlationID(req)})
}

func writeWorkforceChannelBindingError(w http.ResponseWriter, req *http.Request, err error) {
	status := http.StatusServiceUnavailable
	code := "workforce_channel_bindings_unavailable"
	message := "Feishu account ownership is temporarily unavailable."
	switch {
	case errors.Is(err, gwbindings.ErrInvalidInput):
		status = http.StatusBadRequest
		code = "workforce_channel_binding_invalid"
		message = "The Feishu account ownership request is invalid."
	case errors.Is(err, gwbindings.ErrNotFound):
		status = http.StatusNotFound
		code = "workforce_channel_binding_not_found"
		message = "The Feishu account ownership was not found."
	case errors.Is(err, gwbindings.ErrConflict):
		status = http.StatusConflict
		code = "workforce_channel_binding_conflict"
		message = "The Feishu account ownership changed. Refresh and try again."
	}
	writeJSON(w, status, ErrorResponse{Code: code, Message: message, CorrelationID: correlationID(req)})
}
