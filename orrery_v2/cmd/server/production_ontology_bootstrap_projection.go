package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/graphprotocol"
)

func applyDefaultOntologyBootstrapProjections(ctx context.Context, cfg config.Config, runner *ontology.MappingRuleProjectionRunner) error {
	if runner == nil {
		return fmt.Errorf("ontology projection runner is required")
	}
	events, err := defaultOntologyBootstrapEvents(cfg)
	if err != nil {
		return err
	}
	for _, event := range events {
		if _, err := runner.ApplyEvent(ctx, event); err != nil {
			return fmt.Errorf("project bootstrap event %s: %w", event.ID, err)
		}
	}
	return nil
}

func defaultOntologyBootstrapEvents(cfg config.Config) ([]graphprotocol.DomainEvent, error) {
	tenantID := defaultSystemSupportOrganizationID
	events := []graphprotocol.DomainEvent{
		bootstrapOntologyEvent(tenantID, "bootstrap:organization:"+tenantID, "organization.created", tenantID, map[string]any{
			"id": tenantID, "name": "Minvia", "status": "active", "code": "minvia", "knowledge_base_id": "",
		}),
		bootstrapOntologyEvent(tenantID, "bootstrap:department:"+defaultSystemSupportDepartmentID, "people.department.created", defaultSystemSupportDepartmentID, map[string]any{
			"id": defaultSystemSupportDepartmentID, "name": "Orrery 系统运行支持", "status": "active", "organization_id": tenantID,
		}),
	}
	for _, position := range defaultSystemSupportPositions() {
		id := string(position.PositionID)
		events = append(events, bootstrapOntologyEvent(tenantID, "bootstrap:position:"+id, "people.position.created", id, map[string]any{
			"id": id, "name": position.Name, "status": "active", "department_id": string(position.DepartmentID),
		}))
	}
	profiles, err := defaultCoordinatorAgentProfilesFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	for _, profile := range profiles {
		profileID := profile.ID.String()
		personID := workforcePersonOntologyID(profileID)
		agentID := workforceAgentOntologyID(profileID)
		positionID := string(profile.OrgIdentity.PositionRef.ID)
		displayName := strings.TrimSpace(profile.DisplayName)
		if displayName == "" {
			displayName = strings.TrimSpace(profile.RealName)
		}
		events = append(events,
			bootstrapOntologyEvent(tenantID, "bootstrap:person:"+personID, "people.person.updated", personID, map[string]any{
				"id": personID, "display_name": displayName, "role": profile.RoleName, "email": profile.UserAccount.Email, "status": "active",
			}),
			bootstrapOntologyEvent(tenantID, "bootstrap:agent:"+agentID, "people.agent.updated", agentID, map[string]any{
				"id": agentID, "display_name": displayName, "runtime_kind": string(profile.RuntimeSpec.Kind),
			}),
		)
		if positionID != "" {
			events = append(events, bootstrapOntologyEvent(tenantID, "bootstrap:assignment:"+profileID, "people.position.assigned", profileID+":"+positionID, map[string]any{
				"id": profileID + ":" + positionID, "person_id": personID, "agent_id": agentID, "position_id": positionID,
				"role_id": profile.RoleName, "scope": string(profile.OrgIdentity.DepartmentRef.ID), "status": "active",
			}))
		}
		memoryProfile := defaultCoordinatorMemoryProfile(profileID, profile.RoleName)
		events = append(events,
			bootstrapOntologyEvent(tenantID, "bootstrap:memory-profile:"+string(memoryProfile.ID), "memory.profile.created", string(memoryProfile.ID), map[string]any{
				"id": string(memoryProfile.ID), "owner_agent_id": string(memoryProfile.OwnerRef.AgentID), "memory_kind": string(memoryProfile.MemoryKind),
				"scope_kind": string(memoryProfile.DefaultScope.Kind), "scope_id": memoryProfile.DefaultScope.ID, "enabled": memoryProfile.Enabled,
			}),
			bootstrapOntologyEvent(tenantID, "bootstrap:knowledge-base:"+profileID, "knowledge.base.created", "default-knowledge:"+profileID, map[string]any{
				"id": "default-knowledge:" + profileID, "name": profileID + " default knowledge", "owner_type": "agent", "owner_id": profileID,
				"visibility": "private", "status": "active",
			}),
		)
	}
	for _, tool := range defaultSystemToolDefinitions() {
		id := string(tool.ID)
		events = append(events, bootstrapResourceOntologyEvent(tenantID, "bootstrap:tool:"+id, "resource.tool.registered", id, map[string]any{
			"id": id, "name": tool.Name, "risk_level": string(tool.RiskLevel),
		}))
		if tool.MCP != nil {
			events = append(events, bootstrapResourceOntologyEvent(tenantID, "bootstrap:mcp-server:"+id, "resource.mcp_server.registered", id, map[string]any{
				"id": id, "server_name": tool.Name, "transport": string(tool.MCP.Transport),
			}))
		}
	}
	for _, skill := range defaultStandardSkillDefinitions() {
		id := string(skill.ID)
		events = append(events, bootstrapResourceOntologyEvent(tenantID, "bootstrap:skill:"+id, "resource.skill.registered", id, map[string]any{
			"id": id, "name": skill.Name, "description": skill.Description, "version": skill.Version,
			"risk_level": string(skill.RiskLevel), "enabled": skill.Enabled,
		}))
	}
	return events, nil
}

func bootstrapResourceOntologyEvent(tenantID, id, eventType, streamID string, payload map[string]any) graphprotocol.DomainEvent {
	event := bootstrapOntologyEvent(tenantID, id, eventType, streamID, payload)
	event.SourceAccessRef = trustedOntologySourceRef("resource", streamID, "system")
	return event
}

func bootstrapOntologyEvent(tenantID, id, eventType, streamID string, payload map[string]any) graphprotocol.DomainEvent {
	source := organizationOntologySourceRef(tenantID)
	if tenantID == "system" {
		source = trustedOntologySourceRef("resource", streamID, tenantID)
	}
	return graphprotocol.DomainEvent{
		ID: id, TenantID: tenantID, Type: eventType, StreamID: streamID, Version: 1, Payload: payload,
		OccurredAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), PublishedBy: graphprotocol.ActorRef{Kind: "system", ID: "ontology-bootstrap"},
		SourceAccessRef: source,
	}
}
