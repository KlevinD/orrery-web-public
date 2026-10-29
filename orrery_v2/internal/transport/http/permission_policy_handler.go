package httptransport

import (
	"encoding/json"
	"net/http"
	"strings"

	"orrery_v2/internal/app/accesscontrol"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/operations"
)

func (r *Router) handlePermissionPolicy(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	if r != nil && r.authorization != nil {
		view, err := r.authoritativePermissionPolicyView(req)
		if err != nil {
			status, body := MapError(err, cid)
			writeJSON(w, status, body)
			return
		}
		writeJSON(w, http.StatusOK, view)
		return
	}
	if r == nil || r.accessControl == nil {
		writeJSON(w, http.StatusOK, accesscontrol.PolicyViewDTO{
			Capabilities:    permissionCapabilitiesForAccess(r.permissions),
			Policy:          accesscontrol.PolicyDTO{},
			Effective:       []accesscontrol.EffectiveGrantDTO{},
			EnforcementMode: "scope_only",
			PolicyMutable:   false,
		})
		return
	}
	view, err := r.accessControl.BuildView(req.Context(), permissionCapabilitiesForAccess(r.permissions))
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	view.EnforcementMode = "local_policy"
	view.PolicyMutable = true
	writeJSON(w, http.StatusOK, view)
}

func (r *Router) authoritativePermissionPolicyView(req *http.Request) (accesscontrol.PolicyViewDTO, error) {
	capabilities := permissionCapabilitiesForAccess(r.permissions)
	view := accesscontrol.PolicyViewDTO{
		Capabilities:    capabilities,
		Policy:          accesscontrol.PolicyDTO{},
		Effective:       []accesscontrol.EffectiveGrantDTO{},
		EnforcementMode: "authorization",
		PolicyMutable:   false,
	}
	if r == nil || r.operations == nil {
		return view, nil
	}
	operationsViewer, err := permissionPolicyOperationsViewer(req, operations.PermissionRolesRead)
	if err != nil {
		return accesscontrol.PolicyViewDTO{}, err
	}
	roles, err := r.operations.ListRoleDefinitions(req.Context(), operationsViewer)
	if err != nil {
		return accesscontrol.PolicyViewDTO{}, err
	}
	assignments, err := r.operations.ListRoleAssignments(req.Context(), operationsViewer, operations.RoleAssignmentQuery{EffectiveOnly: true})
	if err != nil {
		return accesscontrol.PolicyViewDTO{}, err
	}
	view.Policy = accessPolicyFromOperations(roles.Items, assignments.Items)
	view.Effective = accesscontrol.EffectiveGrants(view.Policy, capabilities)
	view.PolicyMutable = true
	return view, nil
}

func permissionPolicyOperationsViewer(req *http.Request, requiredScopes ...string) (operations.ViewerRef, error) {
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		return operations.ViewerRef{}, err
	}
	agentProfileID := ""
	if req != nil {
		agentProfileID = strings.TrimSpace(req.Header.Get("X-Agent-Profile-ID"))
		if principal, ok := identity.PrincipalFromContext(req.Context()); ok {
			agentProfileID = firstNonEmpty(principal.AgentProfileID, agentProfileID)
		}
	}
	permissionScope := append([]string(nil), viewer.PermissionScope...)
	permissionScope = append(permissionScope, requiredScopes...)
	return operations.ViewerRef{
		ViewerID:           viewer.ViewerID,
		ViewerKind:         string(viewer.ViewerKind),
		AgentProfileID:     agentProfileID,
		PermissionScope:    permissionScope,
		AuthorizationScope: append([]string{}, viewer.PermissionScope...),
	}, nil
}

func accessPolicyFromOperations(roles []operations.RoleDefinitionDTO, assignments []operations.RoleAssignmentDTO) accesscontrol.PolicyDTO {
	policy := accesscontrol.PolicyDTO{}
	visibleRoleIDs := map[string]struct{}{}
	for _, role := range roles {
		roleID := strings.TrimSpace(role.RoleID)
		if roleID == "" || !strings.EqualFold(strings.TrimSpace(role.Status), "active") {
			continue
		}
		policy.Roles = append(policy.Roles, accesscontrol.RoleDTO{
			RoleID:  roleID,
			LabelZh: firstNonEmpty(strings.TrimSpace(role.Name), roleID),
			LabelEn: firstNonEmpty(strings.TrimSpace(role.Description), strings.TrimSpace(role.Name), roleID),
			BuiltIn: role.BuiltIn,
		})
		visibleRoleIDs[roleID] = struct{}{}
		for _, scope := range role.PermissionRefs {
			if scope = strings.TrimSpace(scope); scope != "" {
				policy.Grants = append(policy.Grants, accesscontrol.PermissionGrantDTO{SubjectKind: "role", SubjectID: roleID, Scope: scope})
			}
		}
	}
	seenAssignments := map[string]struct{}{}
	for _, assignment := range assignments {
		kind := strings.ToLower(strings.TrimSpace(assignment.Actor.ActorType))
		switch kind {
		case "human":
			kind = "user"
		case "ai":
			kind = "agent"
		}
		principalID := strings.TrimSpace(assignment.Actor.ActorID)
		roleID := strings.TrimSpace(assignment.RoleID)
		if (kind != "user" && kind != "agent") || principalID == "" || roleID == "" {
			continue
		}
		if _, visible := visibleRoleIDs[roleID]; !visible {
			continue
		}
		key := kind + ":" + principalID + ":" + roleID
		if _, ok := seenAssignments[key]; ok {
			continue
		}
		seenAssignments[key] = struct{}{}
		policy.RoleAssignments = append(policy.RoleAssignments, accesscontrol.RoleAssignmentDTO{
			PrincipalKind: kind,
			PrincipalID:   principalID,
			RoleID:        roleID,
		})
	}
	return policy
}

func (r *Router) handleUpdatePermissionPolicy(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	if r != nil && r.authorization != nil {
		if r.operations == nil {
			writeJSON(w, http.StatusConflict, ErrorResponse{
				Code:          ErrorConflict,
				Message:       "permission policy cannot be updated without the operations role service",
				CorrelationID: cid,
			})
			return
		}
		var body accesscontrol.PolicyDTO
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			status, response := invalidRequest(cid, "body", "request body is invalid")
			writeJSON(w, status, response)
			return
		}
		operationsViewer, err := permissionPolicyOperationsViewer(req, operations.PermissionRolesRead, operations.PermissionRolesManage)
		if err != nil {
			status, response := MapError(err, cid)
			writeJSON(w, status, response)
			return
		}
		current, err := r.operations.ListRoleDefinitions(req.Context(), operationsViewer)
		if err != nil {
			status, response := MapError(err, cid)
			writeJSON(w, status, response)
			return
		}
		command := operations.PutRoleDefinitionsCommand{Roles: r.roleDefinitionsFromAccessPolicy(body, current.Items)}
		if _, err := r.operations.PutRoleDefinitions(req.Context(), operationsViewer, command); err != nil {
			status, response := MapError(err, cid)
			writeJSON(w, status, response)
			return
		}
		view, err := r.authoritativePermissionPolicyView(req)
		if err != nil {
			status, response := MapError(err, cid)
			writeJSON(w, status, response)
			return
		}
		writeJSON(w, http.StatusOK, view)
		return
	}
	if r == nil || r.accessControl == nil {
		status, body := MapError(accesscontrol.ErrServiceDependency, cid)
		writeJSON(w, status, body)
		return
	}
	var body accesscontrol.PolicyDTO
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	saved, err := r.accessControl.SavePolicy(req.Context(), body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	view := accesscontrol.PolicyViewDTO{
		Capabilities:    permissionCapabilitiesForAccess(r.permissions),
		Policy:          saved,
		Effective:       accesscontrol.EffectiveGrants(saved, permissionCapabilitiesForAccess(r.permissions)),
		EnforcementMode: "local_policy",
		PolicyMutable:   true,
	}
	writeJSON(w, http.StatusOK, view)
}

func (r *Router) roleDefinitionsFromAccessPolicy(policy accesscontrol.PolicyDTO, existing []operations.RoleDefinitionDTO) []operations.RoleDefinitionDTO {
	existingByID := map[string]operations.RoleDefinitionDTO{}
	for _, role := range existing {
		if roleID := strings.TrimSpace(role.RoleID); roleID != "" {
			existingByID[roleID] = role
		}
	}
	scopeByCapability := map[string]string{}
	for _, capability := range r.permissions {
		scopeByCapability[capability.CapabilityID] = capability.Scope
	}
	grantsByRole := map[string][]string{}
	for _, grant := range policy.Grants {
		if !strings.EqualFold(strings.TrimSpace(grant.SubjectKind), "role") {
			continue
		}
		roleID := strings.TrimSpace(grant.SubjectID)
		scope := firstNonEmpty(strings.TrimSpace(grant.Scope), strings.TrimSpace(scopeByCapability[grant.CapabilityID]))
		if roleID != "" && scope != "" {
			grantsByRole[roleID] = append(grantsByRole[roleID], scope)
		}
	}
	out := make([]operations.RoleDefinitionDTO, 0, len(policy.Roles))
	for _, policyRole := range policy.Roles {
		roleID := strings.ToLower(strings.TrimSpace(policyRole.RoleID))
		if roleID == "" {
			continue
		}
		role, ok := existingByID[roleID]
		if policyRole.BuiltIn || (ok && role.BuiltIn) {
			continue
		}
		if !ok {
			role = operations.RoleDefinitionDTO{
				RoleID:               roleID,
				Category:             "custom",
				Status:               "active",
				AssignableActorTypes: []string{"human", "agent", "squad"},
			}
		}
		role.RoleID = roleID
		role.Name = firstNonEmpty(strings.TrimSpace(policyRole.LabelZh), strings.TrimSpace(policyRole.LabelEn), roleID)
		role.Description = firstNonEmpty(strings.TrimSpace(policyRole.LabelEn), role.Description)
		role.PermissionRefs = append([]string(nil), grantsByRole[roleID]...)
		out = append(out, role)
	}
	return out
}
