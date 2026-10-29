package operations

import (
	"context"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/rebac"
)

const (
	PermissionDecisionAllow              = "allow"
	PermissionDecisionDeny               = "deny"
	PermissionDecisionNeedsApproval      = "needs_approval"
	PermissionDecisionNeedsHIL           = "needs_hil"
	PermissionDecisionNeedsCouncil       = "needs_council"
	permissionDecisionSourceValidation   = "validation"
	permissionDecisionSourceDenyPolicy   = "deny_policy"
	permissionDecisionSourceOrgStatus    = "organization_status"
	permissionDecisionSourceBinding      = "resource_binding"
	permissionDecisionSourceGrant        = "temporary_grant"
	permissionDecisionSourceApproval     = "approval_decision"
	permissionDecisionSourceRole         = "role_assignment"
	permissionDecisionSourceAncestorRole = "ancestor_role_assignment"
	permissionDecisionSourceRiskPolicy   = "risk_policy"
	permissionDecisionSourceReBAC        = "rebac_relation"
	permissionDecisionSourceMissingReBAC = "missing_rebac_relation"
	permissionDecisionSourceMissing      = "missing_policy"
)

type PermissionDecisionRequestDTO struct {
	Subject          ActorRefDTO              `json:"subject"`
	OnBehalfOf       ActorRefDTO              `json:"on_behalf_of,omitempty"`
	Action           string                   `json:"action"`
	ActionTypeID     string                   `json:"action_type_id,omitempty"`
	ResourceType     string                   `json:"resource_type"`
	ResourceID       string                   `json:"resource_id"`
	OrganizationCode string                   `json:"organization_code,omitempty"`
	MissionID        string                   `json:"mission_id,omitempty"`
	TaskID           string                   `json:"task_id,omitempty"`
	RiskLevel        string                   `json:"risk_level,omitempty"`
	Environment      string                   `json:"environment,omitempty"`
	RuntimeRef       PermissionRuntimeRefDTO  `json:"runtime_ref,omitempty"`
	DelegationRef    PermissionRefDTO         `json:"delegation_ref,omitempty"`
	HumanIntentRef   PermissionRefDTO         `json:"human_intent_ref,omitempty"`
	ObjectRefs       []PermissionObjectRefDTO `json:"object_refs,omitempty"`
	RequestedFields  []string                 `json:"requested_fields,omitempty"`
	ArgsHash         string                   `json:"args_hash,omitempty"`
	Metadata         map[string]string        `json:"metadata,omitempty"`
}

type PermissionRuntimeRefDTO struct {
	RuntimeKind string `json:"runtime_kind"`
	RuntimeID   string `json:"runtime_id"`
}

type PermissionRefDTO struct {
	RefType string `json:"ref_type"`
	RefID   string `json:"ref_id"`
}

type PermissionObjectRefDTO struct {
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
}

type PermissionDecisionDTO struct {
	Decision          string                        `json:"decision"`
	Reason            string                        `json:"reason"`
	Sources           []PermissionDecisionSourceDTO `json:"sources"`
	RequiredApprovals []ApprovalRequirementDTO      `json:"required_approvals,omitempty"`
	ExpiresAt         time.Time                     `json:"expires_at,omitempty"`
	AuditID           string                        `json:"audit_id,omitempty"`
}

type PermissionDecisionSourceDTO struct {
	SourceType       string `json:"source_type"`
	SourceID         string `json:"source_id,omitempty"`
	RoleID           string `json:"role_id,omitempty"`
	OrganizationCode string `json:"organization_code,omitempty"`
	ResourceID       string `json:"resource_id,omitempty"`
	Effect           string `json:"effect"`
	Reason           string `json:"reason,omitempty"`
}

type ApprovalRequirementDTO struct {
	RequirementID    string `json:"requirement_id"`
	PolicyID         string `json:"policy_id"`
	ApprovalType     string `json:"approval_type"`
	OrganizationCode string `json:"organization_code,omitempty"`
	ApproverType     string `json:"approver_type"`
	ApproverRef      string `json:"approver_ref,omitempty"`
	Reason           string `json:"reason"`
}

func (s *QueryService) DecidePermission(ctx context.Context, viewer ViewerRef, req PermissionDecisionRequestDTO) (PermissionDecisionDTO, error) {
	if err := requireOperationsPermission(viewer, PermissionPermissionsRead); err != nil {
		return PermissionDecisionDTO{}, err
	}
	if err := s.authorizePlatformOperation(ctx, viewer, PermissionPermissionsRead); err != nil {
		return PermissionDecisionDTO{}, err
	}
	req = normalizePermissionDecisionRequest(req)
	if invalid := invalidPermissionDecisionSource(req); invalid.SourceType != "" {
		return permissionDecision(PermissionDecisionDeny, invalid.Reason, invalid), nil
	}
	if source := denyPolicySource(req); source.SourceType != "" {
		return permissionDecision(PermissionDecisionDeny, "explicit deny policy matched request", source), nil
	}
	if source, ok, err := s.organizationStatusSource(ctx, req); err != nil {
		return PermissionDecisionDTO{}, err
	} else if ok {
		return permissionDecision(PermissionDecisionDeny, source.Reason, source), nil
	}
	if source := s.agentResourceBindingSource(ctx, req); source.SourceType != "" {
		return permissionDecision(PermissionDecisionDeny, source.Reason, source), nil
	}
	if source, expiresAt := s.activeTemporaryGrantSource(req, time.Now().UTC()); source.SourceType != "" {
		if rebacDecision, ok, err := s.rebacDecision(ctx, req, source); err != nil {
			return PermissionDecisionDTO{}, err
		} else if ok {
			return rebacDecision, nil
		}
		decision := permissionDecision(PermissionDecisionAllow, "active temporary grant allows request", source)
		decision.ExpiresAt = expiresAt
		return decision, nil
	}
	if source, err := s.approvalDecisionSource(ctx, req); err != nil {
		return PermissionDecisionDTO{}, err
	} else if source.SourceType != "" {
		if rebacDecision, ok, err := s.rebacDecision(ctx, req, source); err != nil {
			return PermissionDecisionDTO{}, err
		} else if ok {
			return rebacDecision, nil
		}
		return permissionDecision(PermissionDecisionAllow, "approved decision allows request", source), nil
	}
	if source, ok, err := s.roleDecisionSource(ctx, req); err != nil {
		return PermissionDecisionDTO{}, err
	} else if ok {
		if rebacDecision, ok, err := s.rebacDecision(ctx, req, source); err != nil {
			return PermissionDecisionDTO{}, err
		} else if ok {
			return rebacDecision, nil
		}
		if risk, err := s.riskPolicyRequirement(ctx, req, source); err != nil {
			return PermissionDecisionDTO{}, err
		} else if risk.Decision != "" {
			return risk, nil
		}
		return permissionDecision(PermissionDecisionAllow, "role assignment allows request", source), nil
	}
	if source := s.expiredTemporaryGrantSource(req, time.Now().UTC()); source.SourceType != "" {
		return permissionDecision(PermissionDecisionDeny, source.Reason, source), nil
	}
	return permissionDecision(PermissionDecisionDeny, "missing policy denies request", PermissionDecisionSourceDTO{
		SourceType:       permissionDecisionSourceMissing,
		OrganizationCode: req.OrganizationCode,
		ResourceID:       req.ResourceID,
		Effect:           PermissionDecisionDeny,
		Reason:           "no role, grant, approval, or policy matched request",
	}), nil
}

func normalizePermissionDecisionRequest(req PermissionDecisionRequestDTO) PermissionDecisionRequestDTO {
	req.Subject.ActorType = strings.ToLower(strings.TrimSpace(req.Subject.ActorType))
	req.Subject.ActorID = strings.TrimSpace(req.Subject.ActorID)
	req.OnBehalfOf.ActorType = strings.ToLower(strings.TrimSpace(req.OnBehalfOf.ActorType))
	req.OnBehalfOf.ActorID = strings.TrimSpace(req.OnBehalfOf.ActorID)
	req.Action = strings.TrimSpace(req.Action)
	req.ActionTypeID = strings.TrimSpace(req.ActionTypeID)
	if req.Action == "" {
		req.Action = req.ActionTypeID
	}
	req.ResourceType = strings.ToLower(strings.TrimSpace(req.ResourceType))
	req.ResourceID = strings.TrimSpace(req.ResourceID)
	req.OrganizationCode = normalizeOrganizationCodeForQuery(req.OrganizationCode)
	req.MissionID = strings.TrimSpace(req.MissionID)
	req.TaskID = strings.TrimSpace(req.TaskID)
	req.RiskLevel = strings.ToLower(strings.TrimSpace(req.RiskLevel))
	req.Environment = strings.ToLower(strings.TrimSpace(req.Environment))
	req.RuntimeRef.RuntimeKind = strings.ToLower(strings.TrimSpace(req.RuntimeRef.RuntimeKind))
	req.RuntimeRef.RuntimeID = strings.TrimSpace(req.RuntimeRef.RuntimeID)
	req.DelegationRef.RefType = strings.ToLower(strings.TrimSpace(req.DelegationRef.RefType))
	req.DelegationRef.RefID = strings.TrimSpace(req.DelegationRef.RefID)
	req.HumanIntentRef.RefType = strings.ToLower(strings.TrimSpace(req.HumanIntentRef.RefType))
	req.HumanIntentRef.RefID = strings.TrimSpace(req.HumanIntentRef.RefID)
	for i := range req.ObjectRefs {
		req.ObjectRefs[i].ResourceType = strings.ToLower(strings.TrimSpace(req.ObjectRefs[i].ResourceType))
		req.ObjectRefs[i].ResourceID = strings.TrimSpace(req.ObjectRefs[i].ResourceID)
	}
	if (req.ResourceType == "" || req.ResourceID == "") && len(req.ObjectRefs) > 0 {
		req.ResourceType = firstNonEmpty(req.ResourceType, req.ObjectRefs[0].ResourceType)
		req.ResourceID = firstNonEmpty(req.ResourceID, req.ObjectRefs[0].ResourceID)
	}
	for i := range req.RequestedFields {
		req.RequestedFields[i] = strings.ToLower(strings.TrimSpace(req.RequestedFields[i]))
	}
	req.ArgsHash = strings.TrimSpace(req.ArgsHash)
	return req
}

func invalidPermissionDecisionSource(req PermissionDecisionRequestDTO) PermissionDecisionSourceDTO {
	if req.Subject.ActorType == "" || req.Subject.ActorID == "" || req.Action == "" || req.ResourceType == "" || req.ResourceID == "" {
		return PermissionDecisionSourceDTO{
			SourceType:       permissionDecisionSourceValidation,
			OrganizationCode: req.OrganizationCode,
			ResourceID:       req.ResourceID,
			Effect:           PermissionDecisionDeny,
			Reason:           "subject, action, resource_type, and resource_id are required",
		}
	}
	if req.ActionTypeID != "" && req.ArgsHash == "" {
		return PermissionDecisionSourceDTO{
			SourceType:       permissionDecisionSourceValidation,
			OrganizationCode: req.OrganizationCode,
			ResourceID:       req.ResourceID,
			Effect:           PermissionDecisionDeny,
			Reason:           "args_hash is required for action_type_id permission decisions",
		}
	}
	if req.OrganizationCode != "" && !validOrganizationCode(req.OrganizationCode) {
		return PermissionDecisionSourceDTO{
			SourceType:       permissionDecisionSourceValidation,
			OrganizationCode: req.OrganizationCode,
			ResourceID:       req.ResourceID,
			Effect:           PermissionDecisionDeny,
			Reason:           "organization_code is invalid",
		}
	}
	return PermissionDecisionSourceDTO{}
}

func denyPolicySource(req PermissionDecisionRequestDTO) PermissionDecisionSourceDTO {
	if metadataFlag(req.Metadata, "deny_policy") || metadataFlag(req.Metadata, "deny") {
		return PermissionDecisionSourceDTO{
			SourceType:       permissionDecisionSourceDenyPolicy,
			OrganizationCode: req.OrganizationCode,
			ResourceID:       req.ResourceID,
			Effect:           PermissionDecisionDeny,
			Reason:           "explicit deny policy",
		}
	}
	for _, action := range splitDecisionList(req.Metadata["deny_actions"]) {
		if action == "*" || action == req.Action {
			return PermissionDecisionSourceDTO{
				SourceType:       permissionDecisionSourceDenyPolicy,
				OrganizationCode: req.OrganizationCode,
				ResourceID:       req.ResourceID,
				Effect:           PermissionDecisionDeny,
				Reason:           "action denied by policy",
			}
		}
	}
	return PermissionDecisionSourceDTO{}
}

func (s *QueryService) organizationStatusSource(ctx context.Context, req PermissionDecisionRequestDTO) (PermissionDecisionSourceDTO, bool, error) {
	if s == nil || s.orgs == nil || req.OrganizationCode == "" {
		return PermissionDecisionSourceDTO{}, false, nil
	}
	graph, err := s.orgs.ListOrganizations(ctx, OrgGraphQuery{})
	if err != nil {
		return PermissionDecisionSourceDTO{}, false, err
	}
	nodes, _ := buildOrganizationNodes(graph)
	node, ok := findOrganizationNodeByCode(nodes, req.OrganizationCode)
	if !ok {
		return PermissionDecisionSourceDTO{
			SourceType:       permissionDecisionSourceOrgStatus,
			OrganizationCode: req.OrganizationCode,
			ResourceID:       req.ResourceID,
			Effect:           PermissionDecisionDeny,
			Reason:           "organization context is unknown",
		}, true, nil
	}
	if node.Status != organizationStatusActive && req.Action != "org.read" {
		return PermissionDecisionSourceDTO{
			SourceType:       permissionDecisionSourceOrgStatus,
			SourceID:         node.ID,
			OrganizationCode: node.Code,
			ResourceID:       req.ResourceID,
			Effect:           PermissionDecisionDeny,
			Reason:           "organization status " + node.Status + " does not grant active permissions",
		}, true, nil
	}
	return PermissionDecisionSourceDTO{}, false, nil
}

func (s *QueryService) agentResourceBindingSource(ctx context.Context, req PermissionDecisionRequestDTO) PermissionDecisionSourceDTO {
	if req.Subject.ActorType != "agent" || req.ResourceType != "tool" {
		return PermissionDecisionSourceDTO{}
	}
	if metadataFlag(req.Metadata, "resource_binding") || metadataFlag(req.Metadata, "tool_bound") || metadataFlag(req.Metadata, "bound") {
		return PermissionDecisionSourceDTO{}
	}
	if s != nil && s.agentHasResourceBinding(ctx, req.Subject.ActorID, req.ResourceID) {
		return PermissionDecisionSourceDTO{}
	}
	return PermissionDecisionSourceDTO{
		SourceType:       permissionDecisionSourceBinding,
		OrganizationCode: req.OrganizationCode,
		ResourceID:       req.ResourceID,
		Effect:           PermissionDecisionDeny,
		Reason:           "agent tool/resource binding is required",
	}
}

func (s *QueryService) agentHasResourceBinding(ctx context.Context, actorID, resourceID string) bool {
	if s == nil || s.agents == nil {
		return false
	}
	page, err := s.agents.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{EnabledOnly: true, IncludeAI: true, IncludeHumans: true})
	if err != nil {
		return false
	}
	for _, entry := range page.Entries {
		if !operationAgentMatches(entry, actorID) {
			continue
		}
		return agentHasTool(entry, resourceID)
	}
	return false
}

func (s *QueryService) activeTemporaryGrantSource(req PermissionDecisionRequestDTO, now time.Time) (PermissionDecisionSourceDTO, time.Time) {
	for _, grant := range s.matchingTemporaryGrants(req) {
		if grant.ExpiresAt.IsZero() || grant.ExpiresAt.After(now) {
			return PermissionDecisionSourceDTO{
				SourceType:       permissionDecisionSourceGrant,
				SourceID:         grant.GrantID,
				OrganizationCode: req.OrganizationCode,
				ResourceID:       grant.ToolID,
				Effect:           PermissionDecisionAllow,
				Reason:           firstNonEmpty(grant.Reason, "active temporary grant"),
			}, grant.ExpiresAt
		}
	}
	return PermissionDecisionSourceDTO{}, time.Time{}
}

func (s *QueryService) expiredTemporaryGrantSource(req PermissionDecisionRequestDTO, now time.Time) PermissionDecisionSourceDTO {
	for _, grant := range s.matchingTemporaryGrants(req) {
		if !grant.ExpiresAt.IsZero() && !grant.ExpiresAt.After(now) {
			return PermissionDecisionSourceDTO{
				SourceType:       permissionDecisionSourceGrant,
				SourceID:         grant.GrantID,
				OrganizationCode: req.OrganizationCode,
				ResourceID:       grant.ToolID,
				Effect:           PermissionDecisionDeny,
				Reason:           "temporary grant expired",
			}
		}
	}
	return PermissionDecisionSourceDTO{}
}

func (s *QueryService) matchingTemporaryGrants(req PermissionDecisionRequestDTO) []TemporaryPermissionGrantDTO {
	if s == nil {
		return nil
	}
	scope := permissionDecisionScope(req)
	out := make([]TemporaryPermissionGrantDTO, 0)
	for _, grant := range s.grants {
		if !temporaryGrantMatches(grant, req, scope) {
			continue
		}
		out = append(out, grant)
	}
	return out
}

func temporaryGrantMatches(grant TemporaryPermissionGrantDTO, req PermissionDecisionRequestDTO, scope string) bool {
	if grant.AgentID != req.Subject.ActorID || grant.ToolID != req.ResourceID || grant.Scope != scope {
		return false
	}
	if grant.EffectiveUserID != "" && grant.EffectiveUserID != req.OnBehalfOf.ActorID {
		return false
	}
	if grant.ActionTypeID != "" && grant.ActionTypeID != firstNonEmpty(req.ActionTypeID, req.Action) {
		return false
	}
	if grant.ArgsHash != "" && grant.ArgsHash != req.ArgsHash {
		return false
	}
	if len(grant.ObjectRefs) > 0 && !permissionDecisionObjectRefsMatch(grant.ObjectRefs, req.ObjectRefs, req.ResourceType, req.ResourceID) {
		return false
	}
	if len(grant.RequestedFields) > 0 && !permissionDecisionFieldSetsMatch(grant.RequestedFields, req.RequestedFields) {
		return false
	}
	return true
}

func (s *QueryService) approvalDecisionSource(ctx context.Context, req PermissionDecisionRequestDTO) (PermissionDecisionSourceDTO, error) {
	if s == nil || s.audit == nil {
		return PermissionDecisionSourceDTO{}, nil
	}
	page, err := s.audit.ListAuditEvents(ctx, AuditLogQuery{EntityType: "approval"})
	if err != nil {
		return PermissionDecisionSourceDTO{}, err
	}
	scope := permissionDecisionScope(req)
	for _, event := range page.Events {
		if event.Result != "succeeded" || !strings.HasPrefix(event.Action, "approval.") || !strings.HasSuffix(event.Action, ".approved") {
			continue
		}
		metadata := event.Metadata
		if !permissionDecisionMetadataMatches(metadata, req, scope) {
			continue
		}
		return PermissionDecisionSourceDTO{
			SourceType:       permissionDecisionSourceApproval,
			SourceID:         firstNonEmpty(event.AuditID, event.EntityID),
			OrganizationCode: req.OrganizationCode,
			ResourceID:       req.ResourceID,
			Effect:           PermissionDecisionAllow,
			Reason:           "approval decision approved matching request",
		}, nil
	}
	return PermissionDecisionSourceDTO{}, nil
}

func permissionDecisionMetadataMatches(metadata map[string]string, req PermissionDecisionRequestDTO, scope string) bool {
	if len(metadata) == 0 {
		return false
	}
	subjectID := firstNonEmpty(metadata["subject_id"], metadata["actor_id"], metadata["agent_id"])
	resourceID := firstNonEmpty(metadata["resource_id"], metadata["tool_id"])
	if subjectID != req.Subject.ActorID || resourceID != req.ResourceID || metadata["scope"] != scope {
		return false
	}
	if actorType := strings.TrimSpace(metadata["actor_type"]); actorType != "" && actorType != req.Subject.ActorType {
		return false
	}
	if actionTypeID := strings.TrimSpace(metadata["action_type_id"]); actionTypeID != "" && actionTypeID != firstNonEmpty(req.ActionTypeID, req.Action) {
		return false
	}
	if effectiveUserType := strings.ToLower(strings.TrimSpace(metadata["effective_user_type"])); effectiveUserType != "" && effectiveUserType != req.OnBehalfOf.ActorType {
		return false
	}
	if effectiveUserID := strings.TrimSpace(metadata["effective_user_id"]); effectiveUserID != "" && effectiveUserID != req.OnBehalfOf.ActorID {
		return false
	}
	if argsHash := strings.TrimSpace(metadata["args_hash"]); argsHash != "" && argsHash != req.ArgsHash {
		return false
	}
	if runtimeKind := strings.ToLower(strings.TrimSpace(metadata["runtime_kind"])); runtimeKind != "" && runtimeKind != req.RuntimeRef.RuntimeKind {
		return false
	}
	if runtimeID := strings.TrimSpace(metadata["runtime_id"]); runtimeID != "" && runtimeID != req.RuntimeRef.RuntimeID {
		return false
	}
	if requestedFields := strings.TrimSpace(metadata["requested_fields"]); requestedFields != "" && !permissionDecisionFieldsMatch(requestedFields, req.RequestedFields) {
		return false
	}
	if code := normalizeOrganizationCodeForQuery(metadata["organization_code"]); code != "" && req.OrganizationCode != "" && code != req.OrganizationCode {
		return false
	}
	return true
}

func (s *QueryService) rebacDecision(ctx context.Context, req PermissionDecisionRequestDTO, allowSource PermissionDecisionSourceDTO) (PermissionDecisionDTO, bool, error) {
	if !permissionDecisionRequiresReBAC(req) {
		return PermissionDecisionDTO{}, false, nil
	}
	if s == nil || s.rebac == nil {
		return permissionDecision(PermissionDecisionDeny, "missing ReBAC relation denies request", allowSource, PermissionDecisionSourceDTO{
			SourceType:       permissionDecisionSourceMissingReBAC,
			OrganizationCode: req.OrganizationCode,
			ResourceID:       req.ResourceID,
			Effect:           PermissionDecisionDeny,
			Reason:           "rebac store is not configured",
		}), true, nil
	}
	check := permissionDecisionReBACCheck(req)
	allowed, tuple, err := s.rebac.CheckRelation(ctx, check)
	if err != nil {
		return PermissionDecisionDTO{}, false, err
	}
	if !allowed {
		return permissionDecision(PermissionDecisionDeny, "missing ReBAC relation denies request", allowSource, PermissionDecisionSourceDTO{
			SourceType:       permissionDecisionSourceMissingReBAC,
			OrganizationCode: req.OrganizationCode,
			ResourceID:       req.ResourceID,
			Effect:           PermissionDecisionDeny,
			Reason:           "no relation " + check.Subject.Kind + ":" + check.Subject.ID + "#" + check.Relation + "@" + check.Object.Kind + ":" + check.Object.ID,
		}), true, nil
	}
	return permissionDecision(PermissionDecisionAllow, "ReBAC relation allows request", allowSource, PermissionDecisionSourceDTO{
		SourceType:       permissionDecisionSourceReBAC,
		SourceID:         tuple.ID,
		OrganizationCode: req.OrganizationCode,
		ResourceID:       req.ResourceID,
		Effect:           PermissionDecisionAllow,
		Reason:           "matched relation " + tuple.Relation,
	}), true, nil
}

func permissionDecisionRequiresReBAC(req PermissionDecisionRequestDTO) bool {
	if metadataFlag(req.Metadata, "rebac_required") {
		return true
	}
	return req.ActionTypeID != ""
}

func permissionDecisionReBACCheck(req PermissionDecisionRequestDTO) rebac.RelationCheck {
	return rebac.RelationCheck{
		TenantID: firstNonEmpty(strings.TrimSpace(req.Metadata["tenant_id"]), "default"),
		Subject:  permissionDecisionReBACSubject(req),
		Relation: firstNonEmpty(strings.TrimSpace(req.Metadata["rebac_relation"]), permissionDecisionDefaultRelation(req)),
		Object: rebac.Ref{
			Kind: req.ResourceType,
			ID:   req.ResourceID,
		},
		At: time.Now().UTC(),
	}
}

func permissionDecisionReBACSubject(req PermissionDecisionRequestDTO) rebac.Ref {
	if req.OnBehalfOf.ActorType != "" && req.OnBehalfOf.ActorID != "" {
		return rebac.Ref{Kind: req.OnBehalfOf.ActorType, ID: req.OnBehalfOf.ActorID}
	}
	return rebac.Ref{Kind: req.Subject.ActorType, ID: req.Subject.ActorID}
}

func permissionDecisionDefaultRelation(req PermissionDecisionRequestDTO) string {
	action := firstNonEmpty(req.ActionTypeID, req.Action)
	action = strings.ToLower(strings.TrimSpace(action))
	if strings.Contains(action, ".read") || strings.Contains(action, ".view") || strings.Contains(action, ".list") || strings.Contains(action, ".search") {
		return "viewer"
	}
	return "editor"
}

func (s *QueryService) riskPolicyRequirement(ctx context.Context, req PermissionDecisionRequestDTO, roleSource PermissionDecisionSourceDTO) (PermissionDecisionDTO, error) {
	if !permissionDecisionRequiresReview(req) {
		return PermissionDecisionDTO{}, nil
	}
	decision := PermissionDecisionNeedsApproval
	approvalType := "approval"
	approverType := "org_owner"
	councilRequired := permissionDecisionRequiresCouncil(req)
	if councilRequired && !isHighRiskApproval(req.RiskLevel) {
		decision = PermissionDecisionNeedsCouncil
		approvalType = "council"
		approverType = "council"
	} else if !councilRequired && (req.ResourceType == "tool" || metadataFlag(req.Metadata, "hil_required")) {
		decision = PermissionDecisionNeedsHIL
		approvalType = "hil"
	}
	source := PermissionDecisionSourceDTO{
		SourceType:       permissionDecisionSourceRiskPolicy,
		SourceID:         "risk:" + firstNonEmpty(req.RiskLevel, req.Environment, "policy"),
		RoleID:           roleSource.RoleID,
		OrganizationCode: req.OrganizationCode,
		ResourceID:       req.ResourceID,
		Effect:           decision,
		Reason:           "risk policy requires human review",
	}
	out := permissionDecision(decision, "risk policy requires human review", roleSource, source)
	requirement := ApprovalRequirementDTO{
		RequirementID:    "approval:" + req.Subject.ActorID + ":" + req.ResourceID + ":" + permissionDecisionScope(req),
		PolicyID:         source.SourceID,
		ApprovalType:     approvalType,
		OrganizationCode: req.OrganizationCode,
		ApproverType:     approverType,
		Reason:           source.Reason,
	}
	if approvalType == "council" {
		out.RequiredApprovals = []ApprovalRequirementDTO{requirement}
		return out, nil
	}
	if approver, _, err := s.resolveApprovalRequirementApprover(ctx, requirement, req.RiskLevel); err != nil {
		if isHighRiskApproval(req.RiskLevel) {
			return PermissionDecisionDTO{}, err
		}
	} else if approver.ActorType != "" && approver.ActorID != "" {
		requirement.ApproverRef = approver.ActorType + ":" + approver.ActorID
	}
	out.RequiredApprovals = []ApprovalRequirementDTO{requirement}
	return out, nil
}

func (s *QueryService) resolveApprovalRequirementApprover(ctx context.Context, req ApprovalRequirementDTO, risk string) (ActorRefDTO, string, error) {
	if s == nil || s.orgs == nil {
		if !isHighRiskApproval(risk) {
			return ActorRefDTO{ActorType: "org_role", ActorID: "org.admin"}, "fallback to organization admin role", nil
		}
		return ActorRefDTO{}, "", nil
	}
	graph, err := s.orgs.ListOrganizations(ctx, OrgGraphQuery{})
	if err != nil {
		return ActorRefDTO{}, "", err
	}
	var roles []RoleAssignmentDTO
	if s.roles != nil {
		roles, err = s.roles.ListRoleAssignments(ctx, RoleAssignmentQuery{})
		if err != nil {
			return ActorRefDTO{}, "", err
		}
	}
	return ResolveApprover(req, graph, roles, risk)
}

func permissionDecisionRequiresReview(req PermissionDecisionRequestDTO) bool {
	if metadataFlag(req.Metadata, "approval_required") || metadataFlag(req.Metadata, "hil_required") || permissionDecisionRequiresCouncil(req) {
		return true
	}
	switch req.RiskLevel {
	case "high", "critical":
		return true
	}
	return req.Environment == "production" && req.ResourceType == "tool"
}

func permissionDecisionRequiresCouncil(req PermissionDecisionRequestDTO) bool {
	return metadataFlag(req.Metadata, "council_required") || metadataFlag(req.Metadata, "ontology_conflict_verified")
}

func permissionDecision(decision string, reason string, sources ...PermissionDecisionSourceDTO) PermissionDecisionDTO {
	out := PermissionDecisionDTO{Decision: decision, Reason: reason}
	for _, source := range sources {
		if source.SourceType == "" {
			continue
		}
		if source.Effect == "" {
			source.Effect = decision
		}
		out.Sources = append(out.Sources, source)
	}
	return out
}

func permissionDecisionScope(req PermissionDecisionRequestDTO) string {
	return firstNonEmpty(strings.TrimSpace(req.Metadata["scope"]), req.ActionTypeID, req.Action)
}

func permissionDecisionFieldsMatch(raw string, fields []string) bool {
	return permissionDecisionFieldSetsMatch(splitDecisionList(raw), fields)
}

func permissionDecisionFieldSetsMatch(required []string, fields []string) bool {
	if len(required) == 0 {
		return true
	}
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		field = strings.ToLower(strings.TrimSpace(field))
		if field != "" {
			seen[field] = struct{}{}
		}
	}
	if len(seen) != len(required) {
		return false
	}
	for _, field := range required {
		if _, ok := seen[strings.ToLower(strings.TrimSpace(field))]; !ok {
			return false
		}
	}
	return true
}

func permissionDecisionObjectRefsMatch(required []PermissionObjectRefDTO, refs []PermissionObjectRefDTO, resourceType, resourceID string) bool {
	if len(required) == 0 {
		return true
	}
	seen := make(map[string]struct{}, len(refs)+1)
	for _, ref := range refs {
		key := permissionDecisionObjectRefKey(ref.ResourceType, ref.ResourceID)
		if key != "" {
			seen[key] = struct{}{}
		}
	}
	if key := permissionDecisionObjectRefKey(resourceType, resourceID); key != "" {
		seen[key] = struct{}{}
	}
	if len(seen) < len(required) {
		return false
	}
	for _, ref := range required {
		key := permissionDecisionObjectRefKey(ref.ResourceType, ref.ResourceID)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; !ok {
			return false
		}
	}
	return true
}

func permissionDecisionObjectRefKey(resourceType, resourceID string) string {
	resourceType = strings.ToLower(strings.TrimSpace(resourceType))
	resourceID = strings.TrimSpace(resourceID)
	if resourceType == "" || resourceID == "" {
		return ""
	}
	return resourceType + ":" + resourceID
}

func metadataFlag(metadata map[string]string, key string) bool {
	value := strings.ToLower(strings.TrimSpace(metadata[key]))
	switch value {
	case "1", "true", "yes", "y", "allow", "allowed", "bound", "deny", "denied", "required":
		return true
	default:
		return false
	}
}

func splitDecisionList(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '|' || r == ' '
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func findOrganizationNodeByCode(nodes []OrganizationNodeDTO, code string) (OrganizationNodeDTO, bool) {
	code = normalizeOrganizationCodeForQuery(code)
	for _, node := range nodes {
		if node.Code == code {
			return node, true
		}
		if child, ok := findOrganizationNodeByCode(node.Children, code); ok {
			return child, true
		}
	}
	return OrganizationNodeDTO{}, false
}
