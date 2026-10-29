package operations

import (
	"fmt"
	"strings"
	"time"
)

const (
	ApprovalModeSerial      = "serial"
	ApprovalModeParallel    = "parallel"
	ApprovalModeAnyOne      = "any_one"
	ApprovalModeQuorum      = "quorum"
	ApprovalModeConditional = "conditional"

	ApprovalTargetActor            = "actor"
	ApprovalTargetOrgOwner         = "org_owner"
	ApprovalTargetAncestorOrgOwner = "ancestor_org_owner"
	ApprovalTargetOrgRole          = "org_role"
	ApprovalTargetResourceOwner    = "resource_owner"
	ApprovalTargetMissionOwner     = "mission_owner"
	ApprovalTargetSystemAdmin      = "system_admin"

	HILActionApproveOnce    = "approve_once"
	HILActionApproveSession = "approve_session"
	HILActionReject         = "reject"
	HILActionRequestInput   = "request_input"
)

type ApprovalPolicyDTO struct {
	PolicyID         string                      `json:"policy_id"`
	Name             string                      `json:"name"`
	AppliesToAction  string                      `json:"applies_to_action"`
	ResourceType     string                      `json:"resource_type,omitempty"`
	OrganizationCode string                      `json:"organization_code,omitempty"`
	RiskLevel        string                      `json:"risk_level,omitempty"`
	Mode             string                      `json:"mode"`
	Nodes            []ApprovalNodeDefinitionDTO `json:"nodes"`
	TimeoutPolicy    ApprovalTimeoutPolicyDTO    `json:"timeout_policy,omitempty"`
	Enabled          bool                        `json:"enabled"`
}

type ApprovalNodeDefinitionDTO struct {
	NodeID       string `json:"node_id"`
	TargetType   string `json:"target_type"`
	TargetRef    string `json:"target_ref,omitempty"`
	RequiredRole string `json:"required_role,omitempty"`
	Order        int    `json:"order,omitempty"`
	Required     bool   `json:"required"`
}

type ApprovalTimeoutPolicyDTO struct {
	DurationSeconds int    `json:"duration_seconds,omitempty"`
	OnTimeout       string `json:"on_timeout,omitempty"`
	EscalateTo      string `json:"escalate_to,omitempty"`
}

func ResolveApprover(req ApprovalRequirementDTO, graph OrgGraphDTO, roles []RoleAssignmentDTO, risk string) (ActorRefDTO, string, error) {
	req.OrganizationCode = normalizeOrganizationCodeForQuery(req.OrganizationCode)
	risk = strings.ToLower(strings.TrimSpace(risk))
	if actor, ok := explicitApprovalActor(req); ok {
		return actor, "direct approval actor configured", nil
	}
	nodes, _ := buildOrganizationNodes(graph)
	if actor, reason, ok := resolveApproverByRequirement(req, nodes, roles); ok {
		return actor, reason, nil
	}
	if !isHighRiskApproval(risk) {
		return ActorRefDTO{ActorType: "org_role", ActorID: "org.admin"}, "fallback to organization admin role", nil
	}
	return ActorRefDTO{}, "", fmt.Errorf("%w: no approver resolved for high-risk request", ErrPermissionDenied)
}

func explicitApprovalActor(req ApprovalRequirementDTO) (ActorRefDTO, bool) {
	if req.ApproverType != ApprovalTargetActor || strings.TrimSpace(req.ApproverRef) == "" {
		return ActorRefDTO{}, false
	}
	actorType, actorID, ok := strings.Cut(req.ApproverRef, ":")
	if !ok {
		return ActorRefDTO{ActorType: "human", ActorID: strings.TrimSpace(req.ApproverRef)}, true
	}
	return ActorRefDTO{ActorType: strings.ToLower(strings.TrimSpace(actorType)), ActorID: strings.TrimSpace(actorID)}, true
}

func resolveApproverByRequirement(req ApprovalRequirementDTO, nodes []OrganizationNodeDTO, roles []RoleAssignmentDTO) (ActorRefDTO, string, bool) {
	switch req.ApproverType {
	case ApprovalTargetOrgRole:
		roleID := strings.TrimSpace(req.ApproverRef)
		if roleID == "" {
			roleID = strings.TrimSpace(req.PolicyID)
		}
		return resolveApproverFromRole(roleID, req.OrganizationCode, roles)
	case ApprovalTargetAncestorOrgOwner:
		return resolveAncestorOwner(req.OrganizationCode, nodes)
	case ApprovalTargetSystemAdmin:
		return ActorRefDTO{ActorType: "org_role", ActorID: "org.admin"}, "system admin fallback configured", true
	case "", ApprovalTargetOrgOwner:
		if actor, reason, ok := resolveCurrentOwner(req.OrganizationCode, nodes); ok {
			return actor, reason, true
		}
		return resolveAncestorOwner(req.OrganizationCode, nodes)
	default:
		if actor, reason, ok := resolveCurrentOwner(req.OrganizationCode, nodes); ok {
			return actor, reason, true
		}
		if actor, reason, ok := resolveAncestorOwner(req.OrganizationCode, nodes); ok {
			return actor, reason, true
		}
		return resolveApproverFromRole(strings.TrimSpace(req.ApproverRef), req.OrganizationCode, roles)
	}
}

func resolveCurrentOwner(code string, nodes []OrganizationNodeDTO) (ActorRefDTO, string, bool) {
	node, ok := findOrganizationNodeByCode(nodes, code)
	if !ok || strings.TrimSpace(node.OwnerProfileID) == "" {
		return ActorRefDTO{}, "", false
	}
	return ActorRefDTO{ActorType: "human", ActorID: strings.TrimSpace(node.OwnerProfileID)}, "current organization owner", true
}

func resolveAncestorOwner(code string, nodes []OrganizationNodeDTO) (ActorRefDTO, string, bool) {
	code = normalizeOrganizationCodeForQuery(code)
	for len(code) > 2 {
		code = code[:len(code)-2]
		node, ok := findOrganizationNodeByCode(nodes, code)
		if ok && strings.TrimSpace(node.OwnerProfileID) != "" {
			return ActorRefDTO{ActorType: "human", ActorID: strings.TrimSpace(node.OwnerProfileID)}, "nearest ancestor organization owner", true
		}
	}
	return ActorRefDTO{}, "", false
}

func resolveApproverFromRole(roleID, code string, roles []RoleAssignmentDTO) (ActorRefDTO, string, bool) {
	roleID = strings.TrimSpace(roleID)
	code = normalizeOrganizationCodeForQuery(code)
	if roleID == "" {
		return ActorRefDTO{}, "", false
	}
	for _, assignment := range effectiveRoleAssignments(roles, code, time.Now().UTC()) {
		if assignment.RoleID != roleID {
			continue
		}
		if assignment.Actor.ActorType == "" || assignment.Actor.ActorID == "" {
			continue
		}
		return assignment.Actor, "organization role approver " + roleID, true
	}
	return ActorRefDTO{}, "", false
}

func isHighRiskApproval(risk string) bool {
	switch strings.ToLower(strings.TrimSpace(risk)) {
	case "high", "critical":
		return true
	default:
		return false
	}
}

func ApprovalDecisionEffect(action string, req PermissionDecisionRequestDTO) PermissionDecisionDTO {
	req = normalizePermissionDecisionRequest(req)
	action = strings.ToLower(strings.TrimSpace(action))
	now := time.Now().UTC()
	switch action {
	case HILActionApproveSession:
		expiresAt := now.Add(4 * time.Hour)
		source := PermissionDecisionSourceDTO{
			SourceType:       permissionDecisionSourceGrant,
			SourceID:         approvalDecisionEffectID(action, req),
			OrganizationCode: req.OrganizationCode,
			ResourceID:       req.ResourceID,
			Effect:           PermissionDecisionAllow,
			Reason:           "session-scoped temporary grant for " + permissionDecisionScope(req),
		}
		decision := permissionDecision(PermissionDecisionAllow, "approval created a scoped temporary grant", source)
		decision.ExpiresAt = expiresAt
		return decision
	case HILActionReject:
		return permissionDecision(PermissionDecisionDeny, "approval rejected request", PermissionDecisionSourceDTO{
			SourceType:       permissionDecisionSourceApproval,
			SourceID:         approvalDecisionEffectID(action, req),
			OrganizationCode: req.OrganizationCode,
			ResourceID:       req.ResourceID,
			Effect:           PermissionDecisionDeny,
			Reason:           "human reviewer rejected request",
		})
	case HILActionRequestInput:
		return permissionDecision("needs_input", "reviewer requested more input", PermissionDecisionSourceDTO{
			SourceType:       permissionDecisionSourceApproval,
			SourceID:         approvalDecisionEffectID(action, req),
			OrganizationCode: req.OrganizationCode,
			ResourceID:       req.ResourceID,
			Effect:           "needs_input",
			Reason:           "human reviewer requested more input",
		})
	default:
		expiresAt := now.Add(15 * time.Minute)
		decision := permissionDecision(PermissionDecisionAllow, "approval allows one execution", PermissionDecisionSourceDTO{
			SourceType:       permissionDecisionSourceApproval,
			SourceID:         approvalDecisionEffectID(HILActionApproveOnce, req),
			OrganizationCode: req.OrganizationCode,
			ResourceID:       req.ResourceID,
			Effect:           PermissionDecisionAllow,
			Reason:           "one-call approval for " + permissionDecisionScope(req),
		})
		decision.ExpiresAt = expiresAt
		return decision
	}
}

func approvalDecisionEffectID(action string, req PermissionDecisionRequestDTO) string {
	parts := []string{
		"approval",
		strings.TrimSpace(action),
		firstNonEmpty(req.ActionTypeID, req.Action),
		req.Subject.ActorID,
		req.OnBehalfOf.ActorID,
		req.ResourceID,
		permissionDecisionScope(req),
		req.ArgsHash,
	}
	for _, ref := range req.ObjectRefs {
		if key := permissionDecisionObjectRefKey(ref.ResourceType, ref.ResourceID); key != "" {
			parts = append(parts, key)
		}
	}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.ReplaceAll(strings.TrimSpace(part), ":", "_")
		if part != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, ":")
}
