package httptransport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/userauth"
)

func (r *Router) handleListWorkforceMembers(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.prepareOperationsQuery(w, req, cid)
	if !ok {
		return
	}
	limit, offset := listPage(req)
	page, err := r.operations.ListWorkforceMembers(req.Context(), viewer, operations.WorkforceQuery{
		OrganizationID: req.URL.Query().Get("organization_id"),
		DepartmentID:   req.URL.Query().Get("department_id"),
		EnabledOnly:    req.URL.Query().Get("enabled_only") == "true",
		IncludeDetails: parseBoolQuery(req.URL.Query().Get("include_details")),
		Limit:          limit,
		Offset:         offset,
	})
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (r *Router) handleListTalentMarketMembers(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.prepareOperationsQuery(w, req, cid)
	if !ok {
		return
	}
	limit, offset := listPage(req)
	page, err := r.operations.ListTalentMarketMembers(req.Context(), viewer, operations.WorkforceQuery{
		EnabledOnly:    req.URL.Query().Get("enabled_only") == "true",
		IncludeDetails: parseBoolQuery(req.URL.Query().Get("include_details")),
		Limit:          limit,
		Offset:         offset,
	})
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (r *Router) handleUpsertWorkforceMember(w http.ResponseWriter, req *http.Request, profileID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareOperationsQuery(w, req, cid)
	if !ok {
		return
	}
	if req.Method == http.MethodPut {
		if err := r.authorizeWorkforceCreation(req); err != nil {
			status, response := MapError(err, cid)
			writeJSON(w, status, response)
			return
		}
	}
	var body operations.UpsertWorkforceMemberCommand
	if req.Method == http.MethodPatch {
		decoded, err := decodeWorkforceMemberPatch(req)
		if err != nil {
			status, response := invalidRequest(cid, "body", "request body is invalid")
			writeJSON(w, status, response)
			return
		}
		body = decoded
	} else {
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			status, response := invalidRequest(cid, "body", "request body is invalid")
			writeJSON(w, status, response)
			return
		}
	}
	body.AgentProfileID = profileID
	member, err := r.operations.UpsertWorkforceMember(req.Context(), viewer, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, member)
}

func (r *Router) handleCreateWorkforceMember(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.prepareOperationsQuery(w, req, cid)
	if !ok {
		return
	}
	if err := r.authorizeWorkforceCreation(req); err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	var body operations.UpsertWorkforceMemberCommand
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	body.AgentProfileID = ""
	r.applyGlobalDefaultLLMProfile(req, &body)
	member, err := r.operations.CreateWorkforceMember(req.Context(), viewer, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusCreated, member)
}

func (r *Router) authorizeWorkforceCreation(req *http.Request) error {
	if r == nil || req == nil || r.agentProfiles == nil || identity.TrustedInternalServiceFromContext(req.Context()) {
		return operations.ErrPermissionDenied
	}
	principal, ok := identity.PrincipalFromContext(req.Context())
	if !ok || principal.ViewerKind != identity.ViewerKindUser || strings.TrimSpace(principal.AgentProfileID) == "" {
		return operations.ErrPermissionDenied
	}
	if userauth.IsSystemPrincipalID(principal.AgentProfileID) && strings.EqualFold(strings.TrimSpace(principal.Username), userauth.SuperadminUsername) {
		return nil
	}
	profileID := agentcore.AgentProfileID(strings.TrimSpace(principal.AgentProfileID))
	profile, err := r.agentProfiles.GetProfile(req.Context(), profileID)
	if err != nil || profile.ID != profileID || profile.AgentKind != agentcore.AgentKindHuman || !profile.Enabled {
		return operations.ErrPermissionDenied
	}
	return nil
}

func decodeWorkforceMemberPatch(req *http.Request) (operations.UpsertWorkforceMemberCommand, error) {
	var cmd operations.UpsertWorkforceMemberCommand
	mask, err := decodeJSONWithUpdateMask(req.Body, &cmd, "user_account", "persona")
	if err != nil {
		return cmd, err
	}
	cmd.UpdateMask = mask
	return cmd, nil
}

func (r *Router) applyGlobalDefaultLLMProfile(req *http.Request, cmd *operations.UpsertWorkforceMemberCommand) {
	if r == nil || r.settings == nil || cmd == nil {
		return
	}
	if !shouldApplyGlobalDefaultLLMProfile(*cmd) {
		return
	}
	cmd.BaseAgentLLMProfile = agentcore.BaseAgentLLMProfileSpec{
		ProfileID: agentcore.PlatformDefaultLLMProfileID,
	}
}

func shouldApplyGlobalDefaultLLMProfile(cmd operations.UpsertWorkforceMemberCommand) bool {
	kind := strings.TrimSpace(cmd.AgentKind)
	return (kind == "" || kind == string(agentcore.AgentKindAI)) &&
		strings.TrimSpace(cmd.RuntimeKind) == "" &&
		strings.TrimSpace(cmd.RuntimeConfigRef) == "" &&
		len(cmd.RuntimeConfig) == 0
}

func baseAgentLLMProfileFromSettings(profile SettingsLLMProfileDTO) agentcore.BaseAgentLLMProfileSpec {
	apiKeyRef := strings.TrimSpace(profile.APIKeyRef)
	if apiKeyRef == "" && strings.TrimSpace(profile.APIKey) != "" {
		apiKeyRef = "settings://llm_profiles/" + strings.TrimSpace(profile.ProfileID) + "/api_key"
	}
	return agentcore.BaseAgentLLMProfileSpec{
		ProfileID:     strings.TrimSpace(profile.ProfileID),
		DisplayName:   strings.TrimSpace(profile.DisplayName),
		Provider:      strings.TrimSpace(profile.Provider),
		BaseURL:       strings.TrimSpace(profile.BaseURL),
		APIKeyRef:     apiKeyRef,
		Model:         strings.TrimSpace(profile.Model),
		Temperature:   profile.Temperature,
		ContextLength: profile.ContextLength,
	}
}

func (r *Router) handleSetWorkforceMemberEnabled(w http.ResponseWriter, req *http.Request, profileID string, enabled bool) {
	cid := correlationID(req)
	viewer, ok := r.prepareOperationsQuery(w, req, cid)
	if !ok {
		return
	}
	if err := r.operations.SetWorkforceMemberEnabled(req.Context(), viewer, operations.SetWorkforceMemberEnabledCommand{AgentProfileID: profileID, Enabled: enabled}); err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	status := "disabled"
	if enabled {
		status = "enabled"
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": status, "agent_profile_id": profileID})
}

func (r *Router) handleReleaseWorkforceMember(w http.ResponseWriter, req *http.Request, profileID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareOperationsQuery(w, req, cid)
	if !ok {
		return
	}
	var body operations.ReleaseWorkforceMemberCommand
	if req.Body != nil {
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			status, response := invalidRequest(cid, "body", "request body is invalid")
			writeJSON(w, status, response)
			return
		}
	}
	body.AgentProfileID = profileID
	if err := r.operations.ReleaseWorkforceMember(req.Context(), viewer, body); err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "released_to_market", "agent_profile_id": profileID})
}

func (r *Router) handleHireTalentFromMarket(w http.ResponseWriter, req *http.Request, profileID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareOperationsQuery(w, req, cid)
	if !ok {
		return
	}
	var body operations.HireTalentFromMarketCommand
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	body.AgentProfileID = profileID
	member, err := r.operations.HireTalentFromMarket(req.Context(), viewer, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, member)
}

func (r *Router) handleDeleteTalentMarketMember(w http.ResponseWriter, req *http.Request, profileID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareOperationsQuery(w, req, cid)
	if !ok {
		return
	}
	var body operations.DeleteTalentMarketMemberCommand
	if req.Body != nil {
		err := json.NewDecoder(req.Body).Decode(&body)
		if err != nil && err != io.EOF {
			status, response := invalidRequest(cid, "body", "request body is invalid")
			writeJSON(w, status, response)
			return
		}
	}
	body.AgentProfileID = profileID
	if err := r.operations.DeleteTalentMarketMember(req.Context(), viewer, body); err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "deleted", "agent_profile_id": profileID})
}

func (r *Router) handleDeleteTalentMarketMembers(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.prepareOperationsQuery(w, req, cid)
	if !ok {
		return
	}
	var body operations.DeleteTalentMarketMembersCommand
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	result, err := r.operations.DeleteTalentMarketMembers(req.Context(), viewer, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (r *Router) handlePreviewTalentMarketImport(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.prepareOperationsQuery(w, req, cid)
	if !ok {
		return
	}
	body, err := readTalentMarketImportPreviewRequest(req)
	if err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		if mappedStatus, mapped := MapError(err, cid); mappedStatus != http.StatusInternalServerError {
			status, response = mappedStatus, mapped
		}
		writeJSON(w, status, response)
		return
	}
	preview, err := r.operations.PreviewTalentMarketImport(req.Context(), viewer, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (r *Router) handleCommitTalentMarketImport(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.prepareOperationsQuery(w, req, cid)
	if !ok {
		return
	}
	if err := r.authorizeWorkforceCreation(req); err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	var body operations.TalentMarketImportCommitRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	result, err := r.operations.CommitTalentMarketImport(req.Context(), viewer, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (r *Router) handleExportTalentMarket(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.prepareOperationsQuery(w, req, cid)
	if !ok {
		return
	}
	var body operations.TalentMarketExportRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	pkg, err := r.operations.ExportTalentMarket(req.Context(), viewer, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	if err := writeTalentMarketExportZip(w, pkg); err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
	}
}

func (r *Router) handleOrgGraph(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.prepareOperationsQuery(w, req, cid)
	if !ok {
		return
	}
	graph, err := r.operations.GetOrgGraph(req.Context(), viewer, operations.OrgGraphQuery{
		OrganizationID: req.URL.Query().Get("organization_id"),
		IncludeMembers: parseBoolQuery(req.URL.Query().Get("include_members")),
	})
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, graph)
}

func (r *Router) handleListOrganizationRoles(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.prepareOperationsQuery(w, req, cid)
	if !ok {
		return
	}
	page, err := r.operations.ListRoleDefinitions(req.Context(), viewer)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (r *Router) handleListOrganizationRoleAssignments(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.prepareOperationsQuery(w, req, cid)
	if !ok {
		return
	}
	query := operations.RoleAssignmentQuery{
		ActorType:        req.URL.Query().Get("actor_type"),
		ActorID:          req.URL.Query().Get("actor_id"),
		RoleID:           req.URL.Query().Get("role_id"),
		OrganizationCode: req.URL.Query().Get("organization_code"),
		EffectiveOnly:    req.URL.Query().Get("effective_only") == "true",
	}
	page, err := r.operations.ListRoleAssignments(req.Context(), viewer, query)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (r *Router) handlePutOrganizationRoleAssignments(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.prepareOperationsQuery(w, req, cid)
	if !ok {
		return
	}
	var body operations.PutRoleAssignmentsCommand
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	page, err := r.operations.PutRoleAssignments(req.Context(), viewer, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (r *Router) handleDeleteOrganizationRoleAssignment(w http.ResponseWriter, req *http.Request, assignmentID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareOperationsQuery(w, req, cid)
	if !ok {
		return
	}
	if err := r.operations.DeleteRoleAssignment(req.Context(), viewer, strings.TrimSpace(assignmentID)); err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r *Router) handleCreateOrganization(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.prepareOperationsQuery(w, req, cid)
	if !ok {
		return
	}
	var body operations.CreateOrganizationCommand
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	organization, err := r.operations.CreateOrganization(req.Context(), viewer, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusCreated, organization)
}

func (r *Router) handleCreateOrganizationUnit(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.prepareOperationsQuery(w, req, cid)
	if !ok {
		return
	}
	var body operations.CreateOrganizationUnitCommand
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	unit, err := r.operations.CreateOrganizationUnit(req.Context(), viewer, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusCreated, unit)
}

func (r *Router) handleCreateOrganizationPosition(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.prepareOperationsQuery(w, req, cid)
	if !ok {
		return
	}
	var body operations.CreateOrganizationPositionCommand
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	position, err := r.operations.CreateOrganizationPosition(req.Context(), viewer, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusCreated, position)
}

func (r *Router) handleDeleteOrganization(w http.ResponseWriter, req *http.Request, organizationID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareOperationsQuery(w, req, cid)
	if !ok {
		return
	}
	if err := r.operations.DeleteOrganization(req.Context(), viewer, organizationID); err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "deleted", "organization_id": organizationID})
}

func (r *Router) handleDeleteOrganizationUnit(w http.ResponseWriter, req *http.Request, departmentID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareOperationsQuery(w, req, cid)
	if !ok {
		return
	}
	if err := r.operations.DeleteOrganizationUnit(req.Context(), viewer, departmentID); err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "deleted", "department_id": departmentID})
}

func (r *Router) handleDeleteOrganizationPosition(w http.ResponseWriter, req *http.Request, positionID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareOperationsQuery(w, req, cid)
	if !ok {
		return
	}
	if err := r.operations.DeleteOrganizationPosition(req.Context(), viewer, positionID); err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "deleted", "position_id": positionID})
}

func (r *Router) handleUpdateOrganizationOwner(w http.ResponseWriter, req *http.Request, organizationNodeID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareOperationsQuery(w, req, cid)
	if !ok {
		return
	}
	var body operations.UpdateOrganizationOwnerCommand
	if err := decodeStrictJSON(req, &body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	body.OrganizationNodeID = strings.TrimSpace(organizationNodeID)
	node, err := r.operations.UpdateOrganizationOwner(req.Context(), viewer, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, node)
}

func (r *Router) handlePermissionMatrix(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.prepareOperationsQuery(w, req, cid)
	if !ok {
		return
	}
	matrix, err := r.operations.GetPermissionMatrix(req.Context(), viewer, operations.PermissionMatrixQuery{
		AgentID: req.URL.Query().Get("agent_id"),
		ToolID:  req.URL.Query().Get("tool_id"),
	})
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, matrix)
}

func (r *Router) handlePermissionDecision(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.prepareOperationsQuery(w, req, cid)
	if !ok {
		return
	}
	var body operations.PermissionDecisionRequestDTO
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	decision, err := r.operations.DecidePermission(req.Context(), viewer, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, decision)
}

func (r *Router) handlePermissionCapabilities(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if !viewerHasScope(viewer.PermissionScope, operations.PermissionPermissionsRead, operations.PermissionPermissionsManage) {
		status, body := MapError(operations.ErrPermissionDenied, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, PermissionCapabilitiesDTO{Capabilities: append([]PermissionCapabilityDTO(nil), r.permissions...)})
}

func (r *Router) handleAuditLog(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.prepareOperationsQuery(w, req, cid)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(req.URL.Query().Get("offset"))
	page, err := r.operations.ListAuditLog(req.Context(), viewer, operations.AuditLogQuery{
		ActorID:    req.URL.Query().Get("actor_id"),
		Action:     req.URL.Query().Get("action"),
		Result:     req.URL.Query().Get("result"),
		EntityType: req.URL.Query().Get("entity_type"),
		EntityID:   req.URL.Query().Get("entity_id"),
		MissionID:  req.URL.Query().Get("mission_id"),
		Severity:   req.URL.Query().Get("severity"),
		Limit:      limit,
		Offset:     offset,
	})
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (r *Router) handleListAuthIPLocks(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if !viewerHasScope(viewer.PermissionScope, operations.PermissionAuditRead, operations.PermissionAuditManage) {
		status, body := MapError(operations.ErrPermissionDenied, cid)
		writeJSON(w, status, body)
		return
	}
	if r.auth == nil {
		status, body := MapError(userauth.ErrServiceDependency, cid)
		writeJSON(w, status, body)
		return
	}
	limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(req.URL.Query().Get("offset"))
	page, err := r.auth.ListIPLocks(req.Context(), userauth.IPBlockQuery{
		Status: req.URL.Query().Get("status"),
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (r *Router) handleUnlockAuthIPLock(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if !viewerHasScope(viewer.PermissionScope, operations.PermissionAuditManage, operations.PermissionAuditManage) {
		status, body := MapError(operations.ErrPermissionDenied, cid)
		writeJSON(w, status, body)
		return
	}
	if r.auth == nil {
		status, body := MapError(userauth.ErrServiceDependency, cid)
		writeJSON(w, status, body)
		return
	}
	var body struct {
		IPAddress string `json:"ip_address"`
		Reason    string `json:"reason,omitempty"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	if err := r.auth.UnlockIP(req.Context(), body.IPAddress, viewer.ViewerID, body.Reason); err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "unlocked", "ip_address": strings.TrimSpace(body.IPAddress)})
}

func (r *Router) handleEvaluations(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.prepareOperationsQuery(w, req, cid)
	if !ok {
		return
	}
	page, err := r.operations.ListEvaluations(req.Context(), viewer, operations.EvaluationQuery{
		MissionID:  req.URL.Query().Get("mission_id"),
		EntityType: req.URL.Query().Get("entity_type"),
		EntityID:   req.URL.Query().Get("entity_id"),
	})
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (r *Router) handleEvaluationsStream(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: ErrorInternal, Message: "streaming response is not supported", CorrelationID: cid})
		return
	}
	viewer, ok := r.prepareOperationsQuery(w, req, cid)
	if !ok {
		return
	}
	query := operations.EvaluationQuery{
		MissionID:  req.URL.Query().Get("mission_id"),
		EntityType: req.URL.Query().Get("entity_type"),
		EntityID:   req.URL.Query().Get("entity_id"),
	}
	page, err := r.operations.ListEvaluations(req.Context(), viewer, query)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	encoded, err := writeEvaluationSSE(w, "snapshot", page)
	if err != nil {
		return
	}
	flusher.Flush()
	streamEvaluationUpdates(req, w, flusher, r.operations, viewer, query, encoded)
}

func streamEvaluationUpdates(req *http.Request, w http.ResponseWriter, flusher http.Flusher, service OperationsService, viewer operations.ViewerRef, query operations.EvaluationQuery, previous []byte) {
	ctx := req.Context()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			page, err := service.ListEvaluations(ctx, viewer, query)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				status, response := MapError(err, correlationID(req))
				payload, _ := json.Marshal(response)
				fmt.Fprintf(w, "event: error\n")
				fmt.Fprintf(w, "data: %s\n\n", payload)
				flusher.Flush()
				if status >= http.StatusInternalServerError {
					return
				}
				continue
			}
			encoded, err := json.Marshal(page)
			if err != nil {
				return
			}
			if bytes.Equal(encoded, previous) {
				continue
			}
			if _, err := writeEvaluationSSEWithPayload(w, "update", encoded); err != nil {
				return
			}
			previous = encoded
			flusher.Flush()
		}
	}
}

func writeEvaluationSSE(w http.ResponseWriter, event string, page operations.EvaluationPageDTO) ([]byte, error) {
	payload, err := json.Marshal(page)
	if err != nil {
		return nil, err
	}
	return writeEvaluationSSEWithPayload(w, event, payload)
}

func writeEvaluationSSEWithPayload(w http.ResponseWriter, event string, payload []byte) ([]byte, error) {
	if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
		return nil, err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (r *Router) prepareOperationsQuery(w http.ResponseWriter, req *http.Request, cid string) (operations.ViewerRef, bool) {
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return operations.ViewerRef{}, false
	}
	if r.operations == nil {
		status, body := MapError(errServiceDependency(), cid)
		writeJSON(w, status, body)
		return operations.ViewerRef{}, false
	}
	agentProfileID := strings.TrimSpace(req.Header.Get("X-Agent-Profile-ID"))
	authorizationScope := append([]string{}, viewer.PermissionScope...)
	if principal, ok := identity.PrincipalFromContext(req.Context()); ok {
		agentProfileID = strings.TrimSpace(firstNonEmpty(principal.AgentProfileID, agentProfileID))
		authorizationScope = append([]string{}, principal.CoarseScopes...)
	}
	permissionScope := append([]string(nil), viewer.PermissionScope...)
	return operations.ViewerRef{
		ViewerID:           viewer.ViewerID,
		ViewerKind:         string(viewer.ViewerKind),
		DisplayName:        displayNameFromRequest(req),
		AgentProfileID:     agentProfileID,
		PermissionScope:    permissionScope,
		AuthorizationScope: authorizationScope,
		RequestID:          cid,
		IdempotencyKey:     idempotencyKey(req),
	}, true
}

func mergePermissionScopes(groups ...[]string) []string {
	out := []string{}
	seen := map[string]struct{}{}
	for _, group := range groups {
		for _, scope := range group {
			scope = strings.TrimSpace(scope)
			if scope == "" {
				continue
			}
			if _, ok := seen[scope]; ok {
				continue
			}
			seen[scope] = struct{}{}
			out = append(out, scope)
		}
	}
	return out
}
