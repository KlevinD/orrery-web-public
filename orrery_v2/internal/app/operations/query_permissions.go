package operations

import (
	"context"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	toolres "orrery_v2/internal/resource/tool"
)

type PermissionMatrixQuery struct {
	AgentID string
	ToolID  string
}

type TemporaryPermissionGrantDTO struct {
	GrantID         string                   `json:"grant_id"`
	AgentID         string                   `json:"agent_id"`
	EffectiveUserID string                   `json:"effective_user_id,omitempty"`
	ToolID          string                   `json:"tool_id"`
	Scope           string                   `json:"scope"`
	ActionTypeID    string                   `json:"action_type_id,omitempty"`
	ObjectRefs      []PermissionObjectRefDTO `json:"object_refs,omitempty"`
	RequestedFields []string                 `json:"requested_fields,omitempty"`
	ArgsHash        string                   `json:"args_hash,omitempty"`
	Reason          string                   `json:"reason,omitempty"`
	ExpiresAt       time.Time                `json:"expires_at,omitempty"`
}

type PermissionMatrixDTO struct {
	Rows []EffectivePermissionRowDTO `json:"rows"`
}

type EffectivePermissionRowDTO struct {
	AgentID   string                           `json:"agent_id"`
	Resources []EffectiveResourcePermissionDTO `json:"resources"`
}

type EffectiveResourcePermissionDTO struct {
	ToolID       string                              `json:"tool_id"`
	ToolName     string                              `json:"tool_name"`
	Declarations []EffectivePermissionDeclarationDTO `json:"declarations"`
}

type EffectivePermissionDeclarationDTO struct {
	Scope     string                        `json:"scope"`
	Risk      string                        `json:"risk"`
	Required  bool                          `json:"required"`
	Effective bool                          `json:"effective"`
	Source    string                        `json:"source"`
	Decision  string                        `json:"decision,omitempty"`
	Sources   []PermissionDecisionSourceDTO `json:"sources,omitempty"`
}

func (s *QueryService) GetPermissionMatrix(ctx context.Context, viewer ViewerRef, query PermissionMatrixQuery) (PermissionMatrixDTO, error) {
	if s == nil || s.agents == nil || s.tools == nil {
		return PermissionMatrixDTO{}, ErrServiceDependency
	}
	if err := requireOperationsPermission(viewer, PermissionPermissionsRead); err != nil {
		return PermissionMatrixDTO{}, err
	}
	if err := s.authorizePlatformOperation(ctx, viewer, PermissionPermissionsRead); err != nil {
		return PermissionMatrixDTO{}, err
	}
	page, err := s.agents.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{EnabledOnly: true, IncludeAI: true, IncludeHumans: true})
	if err != nil {
		return PermissionMatrixDTO{}, err
	}
	tools, err := s.tools.List(ctx)
	if err != nil {
		return PermissionMatrixDTO{}, err
	}
	approvalDecisions, err := s.permissionApprovalDecisions(ctx)
	if err != nil {
		return PermissionMatrixDTO{}, err
	}
	matrix := PermissionMatrixDTO{}
	for _, agent := range page.Entries {
		agentID := operationAgentID(agent)
		if query.AgentID != "" && !operationAgentMatches(agent, query.AgentID) {
			continue
		}
		row := EffectivePermissionRowDTO{AgentID: agentID}
		for _, tool := range tools {
			toolID := string(tool.ID)
			if query.ToolID != "" && string(tool.ID) != query.ToolID {
				continue
			}
			if !agentHasTool(agent, toolID) {
				continue
			}
			resource := EffectiveResourcePermissionDTO{ToolID: toolID, ToolName: tool.Name}
			for _, declaration := range tool.Permissions {
				effective, source := s.effectivePermissionSource(agent, tool, declaration, approvalDecisions)
				decision := PermissionDecisionDeny
				if effective {
					decision = PermissionDecisionAllow
				}
				resource.Declarations = append(resource.Declarations, EffectivePermissionDeclarationDTO{
					Scope:     declaration.Scope,
					Risk:      string(declaration.Risk),
					Required:  declaration.Required,
					Effective: effective,
					Source:    source,
					Decision:  decision,
					Sources:   permissionMatrixSources(source, agentID, toolID, declaration.Scope, effective),
				})
			}
			row.Resources = append(row.Resources, resource)
		}
		matrix.Rows = append(matrix.Rows, row)
	}
	return matrix, nil
}

func permissionMatrixSources(source, agentID, toolID, scope string, effective bool) []PermissionDecisionSourceDTO {
	effect := PermissionDecisionDeny
	if effective {
		effect = PermissionDecisionAllow
	}
	sourceType := "resource_declaration"
	if strings.Contains(source, "temporary_grant") {
		sourceType = permissionDecisionSourceGrant
	} else if strings.Contains(source, "approval_decision") {
		sourceType = permissionDecisionSourceApproval
	} else if strings.Contains(source, "authority_scope") {
		sourceType = "authority_scope"
	}
	return []PermissionDecisionSourceDTO{{
		SourceType: sourceType,
		SourceID:   agentID + ":" + scope,
		ResourceID: toolID,
		Effect:     effect,
		Reason:     source,
	}}
}

func (s *QueryService) effectivePermissionSource(agent agentcore.AgentDirectoryEntry, tool toolres.ToolDefinition, declaration toolres.PermissionDeclaration, approvals map[permissionKey]struct{}) (bool, string) {
	agentID := operationAgentID(agent)
	toolID := string(tool.ID)
	scope := declaration.Scope
	if scope != "" && scope == agent.OrgIdentity.AuthorityScope {
		return true, "agent_tool_ref+authority_scope"
	}
	if s.hasActiveGrant(agentID, toolID, scope, time.Now().UTC()) {
		return true, "agent_tool_ref+temporary_grant"
	}
	if _, ok := approvals[permissionKey{AgentID: agentID, ToolID: toolID, Scope: scope}]; ok {
		return true, "agent_tool_ref+approval_decision"
	}
	return false, "resource_declaration"
}

func (s *QueryService) hasActiveGrant(agentID, toolID, scope string, now time.Time) bool {
	for _, grant := range s.grants {
		if grant.AgentID != agentID || grant.ToolID != toolID || grant.Scope != scope {
			continue
		}
		if grant.ExpiresAt.IsZero() || grant.ExpiresAt.After(now) {
			return true
		}
	}
	return false
}

func (s *QueryService) permissionApprovalDecisions(ctx context.Context) (map[permissionKey]struct{}, error) {
	approved := map[permissionKey]struct{}{}
	if s == nil || s.audit == nil {
		return approved, nil
	}
	page, err := s.audit.ListAuditEvents(ctx, AuditLogQuery{EntityType: "approval"})
	if err != nil {
		return nil, err
	}
	for _, event := range page.Events {
		if event.Result != "succeeded" || !strings.HasPrefix(event.Action, "approval.") || !strings.HasSuffix(event.Action, ".approved") {
			continue
		}
		fields := event.Metadata
		key := permissionKey{AgentID: fields["agent_id"], ToolID: fields["tool_id"], Scope: fields["scope"]}
		if key.AgentID != "" && key.ToolID != "" && key.Scope != "" {
			approved[key] = struct{}{}
		}
	}
	return approved, nil
}

type permissionKey struct {
	AgentID string
	ToolID  string
	Scope   string
}

func agentHasTool(entry agentcore.AgentDirectoryEntry, toolID string) bool {
	for _, ref := range entry.ToolRefs {
		if string(ref.ToolID) == toolID {
			return true
		}
	}
	return false
}
