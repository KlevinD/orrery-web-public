package main

import (
	"encoding/json"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	discussionsession "orrery_v2/internal/app/discussionsession"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/operations"
	missionteam "orrery_v2/internal/mission/team"
)

type defaultAgentTemplate struct {
	Slug         string `json:"slug"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Instructions string `json:"instructions"`
	AgentType    string `json:"agent_type"`
	RoleKind     string `json:"role_kind"`
	WorkerConfig struct {
		Role           string `json:"role"`
		AuthorityScope string `json:"authority_scope"`
		QA             struct {
			Enabled bool `json:"enabled"`
		} `json:"qa"`
	} `json:"worker_config"`
	ConversationMember struct {
		Enabled bool   `json:"enabled"`
		Role    string `json:"role"`
	} `json:"conversation_member"`
	MissionRole struct {
		Enabled           bool   `json:"enabled"`
		Role              string `json:"role"`
		Responsibility    string `json:"responsibility"`
		AuthorityScope    string `json:"authority_scope"`
		IdempotencySuffix string `json:"idempotency_suffix"`
	} `json:"mission_role"`
	Skills []struct {
		SourceURL         string `json:"source_url"`
		CachedName        string `json:"cached_name"`
		CachedDescription string `json:"cached_description"`
	} `json:"skills"`
	Tools []struct {
		ToolID            string `json:"tool_id"`
		Alias             string `json:"alias"`
		VersionConstraint string `json:"version_constraint"`
		PermissionScope   string `json:"permission_scope"`
		BindingPolicy     string `json:"binding_policy"`
	} `json:"tools"`
}

func readDefaultAgentTemplate(fileName string) (defaultAgentTemplate, error) {
	var template defaultAgentTemplate
	raw, err := defaultAgentTemplateFS.ReadFile("agent_templates/" + fileName)
	if err != nil {
		return template, err
	}
	if err := json.Unmarshal(raw, &template); err != nil {
		return template, err
	}
	if strings.TrimSpace(template.Slug) == "" || strings.TrimSpace(template.Name) == "" || strings.TrimSpace(template.Instructions) == "" {
		return template, operations.ErrInvalidRequest
	}
	return template, nil
}

func defaultTemplateSkillRefs(template defaultAgentTemplate) []agentcore.SkillRef {
	refs := make([]agentcore.SkillRef, 0, len(template.Skills))
	for _, skill := range template.Skills {
		id := defaultStandardSkillID(firstNonBlank(skill.CachedName, skill.SourceURL))
		if id == "" {
			continue
		}
		refs = append(refs, agentcore.SkillRef{
			SkillID: agentcore.SkillID(id),
			Alias:   strings.TrimSpace(skill.CachedName),
		})
	}
	return refs
}

func defaultTemplateToolRefs(template defaultAgentTemplate) []agentcore.ToolRef {
	refs := make([]agentcore.ToolRef, 0, len(template.Tools))
	for _, tool := range template.Tools {
		id := strings.TrimSpace(tool.ToolID)
		if id == "" {
			continue
		}
		refs = append(refs, agentcore.ToolRef{
			ToolID:            agentcore.ToolID(id),
			Alias:             strings.TrimSpace(tool.Alias),
			VersionConstraint: strings.TrimSpace(tool.VersionConstraint),
			PermissionScope:   strings.TrimSpace(tool.PermissionScope),
			BindingPolicy:     strings.TrimSpace(tool.BindingPolicy),
		})
	}
	return refs
}

func initialAgentMemberSeedsFromTemplates(primaryAgentID string, templates []defaultAgentTemplate) []discussionsession.InitialAgentMemberSeed {
	members := make([]discussionsession.InitialAgentMemberSeed, 0, len(templates))
	for _, template := range templates {
		if !template.ConversationMember.Enabled {
			continue
		}
		agentID := strings.TrimSpace(template.Slug)
		if len(members) == 0 && strings.TrimSpace(primaryAgentID) != "" {
			agentID = strings.TrimSpace(primaryAgentID)
		}
		role := strings.TrimSpace(template.ConversationMember.Role)
		if agentID == "" || role == "" {
			continue
		}
		members = append(members, discussionsession.InitialAgentMemberSeed{AgentID: agentID, Role: role})
	}
	return members
}

func defaultSystemRoleBootstrapsFromTemplates(primaryAgentID string, templates []defaultAgentTemplate) []missioncontrol.SystemRoleBootstrap {
	roles := make([]missioncontrol.SystemRoleBootstrap, 0, len(templates))
	for _, template := range templates {
		if !template.MissionRole.Enabled {
			continue
		}
		agentID := strings.TrimSpace(template.Slug)
		if len(roles) == 0 && strings.TrimSpace(primaryAgentID) != "" {
			agentID = strings.TrimSpace(primaryAgentID)
		}
		role := missionteam.MissionRole(strings.TrimSpace(template.MissionRole.Role))
		if agentID == "" || !role.IsValid() {
			continue
		}
		roles = append(roles, missioncontrol.SystemRoleBootstrap{
			ProfileID:         agentcore.AgentProfileID(agentID),
			Role:              role,
			Responsibility:    strings.TrimSpace(template.MissionRole.Responsibility),
			AuthorityScope:    strings.TrimSpace(template.MissionRole.AuthorityScope),
			IdempotencySuffix: strings.TrimSpace(template.MissionRole.IdempotencySuffix),
		})
	}
	return roles
}
