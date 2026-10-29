package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/operations"
	knowledgecore "orrery_v2/internal/knowledge/core"
	memorycore "orrery_v2/internal/memory/core"
	memoryStore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform/graphprotocol"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

type ontologyInitializationWorkforceSource interface {
	ListOrganizationCatalog(context.Context, string, int) ([]operations.OrganizationDTO, bool, error)
	ListOrganizations(context.Context, operations.OrgGraphQuery) (operations.OrgGraphDTO, error)
}

type ontologyInitializationAgentSource interface {
	QueryDirectory(context.Context, agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error)
}

type ontologyInitializationWorkforceAdapter struct {
	workforce ontologyInitializationWorkforceSource
	agents    ontologyInitializationAgentSource
	runner    *ontology.MappingRuleProjectionRunner
}

func newOntologyInitializationWorkforceAdapter(workforce ontologyInitializationWorkforceSource, agents ontologyInitializationAgentSource, runner *ontology.MappingRuleProjectionRunner) ontologyInitializationAdapter {
	return ontologyInitializationWorkforceAdapter{workforce: workforce, agents: agents, runner: runner}
}

func (ontologyInitializationWorkforceAdapter) Name() string { return "workforce" }

func (a ontologyInitializationWorkforceAdapter) Reconcile(ctx context.Context, request ontologyInitializationRequest) (ontologyInitializationDomainReport, error) {
	report := ontologyInitializationDomainReport{Name: a.Name()}
	if a.workforce == nil || a.agents == nil || a.runner == nil {
		return report, fmt.Errorf("ontology initialization workforce dependencies are required")
	}
	positionsByTenant := map[string]map[string]struct{}{}
	afterID := ""
	for {
		organizations, hasMore, err := a.workforce.ListOrganizationCatalog(ctx, afterID, request.PageSize)
		if err != nil {
			return report, err
		}
		sort.Slice(organizations, func(i, j int) bool { return organizations[i].OrganizationID < organizations[j].OrganizationID })
		for _, organization := range organizations {
			graph, err := a.workforce.ListOrganizations(ctx, operations.OrgGraphQuery{OrganizationID: organization.OrganizationID})
			if err != nil {
				return report, err
			}
			tenantID := strings.TrimSpace(organization.OrganizationID)
			positionsByTenant[tenantID] = map[string]struct{}{}
			for _, position := range graph.Positions {
				if positionID := strings.TrimSpace(position.PositionID); positionID != "" {
					positionsByTenant[tenantID][positionID] = struct{}{}
				}
			}
			events := organizationOntologyInitializationEvents(organization, graph)
			report.Source += len(events)
			if err := applyOntologyInitializationEvents(ctx, a.runner, events); err != nil {
				report.Failed++
				return report, err
			}
			report.Projected += len(events)
		}
		if !hasMore {
			break
		}
		if len(organizations) == 0 || organizations[len(organizations)-1].OrganizationID == afterID {
			return report, fmt.Errorf("ontology initialization organization catalog did not advance")
		}
		afterID = organizations[len(organizations)-1].OrganizationID
	}

	for offset := 0; ; offset += request.PageSize {
		page, err := a.agents.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{
			IncludeHumans: true, IncludeAI: true, Placement: agentcore.TalentPlacementFilterAll,
			Page: agentcore.Page{Limit: request.PageSize, Offset: offset}, Sort: agentcore.DirectorySortUpdated,
		})
		if err != nil {
			return report, err
		}
		for _, rejected := range page.Rejected {
			report.Skipped++
			report.Issues = append(report.Issues, ontologyInitializationIssue{
				Domain: a.Name(), SourceRef: rejected.AgentProfileID.String(), Code: firstNonBlank(rejected.Code, "directory_entry_rejected"),
			})
		}
		sort.Slice(page.Entries, func(i, j int) bool { return page.Entries[i].AgentProfileID < page.Entries[j].AgentProfileID })
		for _, entry := range page.Entries {
			if strings.TrimSpace(entry.PersonalAssistantOwnerID) != "" || isPersonalAssistantProfileID(entry.AgentProfileID.String()) {
				continue
			}
			profileID := strings.TrimSpace(entry.AgentProfileID.String())
			tenantID := strings.TrimSpace(string(entry.OrgIdentity.OrganizationRef.ID))
			positionID := strings.TrimSpace(string(entry.OrgIdentity.PositionRef.ID))
			if tenantID != "" && positionID != "" {
				_, positionAvailable := positionsByTenant[tenantID][positionID]
				if !positionAvailable {
					entry.OrgIdentity.PositionRef.ID = ""
					report.Source++
					report.Unresolved++
					report.Issues = append(report.Issues, ontologyInitializationIssue{
						Domain: a.Name(), SourceRef: profileID, Code: "historical_position_unavailable",
					})
				}
			}
			events, issue := agentOntologyInitializationEvents(entry)
			if issue != nil {
				report.Source++
				report.Skipped++
				report.Issues = append(report.Issues, *issue)
				continue
			}
			report.Source += len(events)
			if err := applyOntologyInitializationEvents(ctx, a.runner, events); err != nil {
				report.Failed++
				return report, err
			}
			report.Projected += len(events)
		}
		if offset+len(page.Entries) >= page.Total || len(page.Entries) == 0 {
			break
		}
	}
	return report, nil
}

func organizationOntologyInitializationEvents(organization operations.OrganizationDTO, graph operations.OrgGraphDTO) []graphprotocol.DomainEvent {
	tenantID := strings.TrimSpace(organization.OrganizationID)
	events := []graphprotocol.DomainEvent{ontologyInitializationEvent(tenantID, "organization.created", tenantID, time.Time{}, map[string]any{
		"id": tenantID, "name": organization.Name, "status": organization.Status, "code": organization.Code, "knowledge_base_id": organization.KnowledgeBaseID,
	})}
	for _, department := range graph.Departments {
		events = append(events, ontologyInitializationEvent(tenantID, "people.department.created", department.DepartmentID, time.Time{}, map[string]any{
			"id": department.DepartmentID, "name": department.Name, "status": department.Status,
			"owner_profile_id": department.OwnerProfileID, "organization_id": tenantID,
		}))
	}
	for _, position := range graph.Positions {
		events = append(events, ontologyInitializationEvent(tenantID, "people.position.created", position.PositionID, time.Time{}, map[string]any{
			"id": position.PositionID, "name": position.Name, "status": "active", "department_id": position.DepartmentID,
		}))
	}
	return events
}

func agentOntologyInitializationEvents(entry agentcore.AgentDirectoryEntry) ([]graphprotocol.DomainEvent, *ontologyInitializationIssue) {
	profileID := strings.TrimSpace(entry.AgentProfileID.String())
	tenantID := strings.TrimSpace(string(entry.OrgIdentity.OrganizationRef.ID))
	if tenantID == "" {
		return nil, &ontologyInitializationIssue{Domain: "workforce", SourceRef: profileID, Code: "historical_tenant_unavailable"}
	}
	displayName := firstNonBlank(entry.DisplayName, entry.RealName, profileID)
	personID := workforcePersonOntologyID(profileID)
	agentID := workforceAgentOntologyID(profileID)
	status := "inactive"
	if entry.Enabled {
		status = "active"
	}
	events := []graphprotocol.DomainEvent{
		ontologyInitializationEvent(tenantID, "people.person.updated", personID, entry.UpdatedAt, map[string]any{
			"id": personID, "display_name": displayName, "role": entry.RoleName, "email": entry.Email, "status": status,
		}),
		ontologyInitializationEvent(tenantID, "people.agent.updated", agentID, entry.UpdatedAt, map[string]any{
			"id": agentID, "display_name": displayName, "runtime_kind": string(entry.RuntimeSpecSummary.Kind),
		}),
	}
	positionID := strings.TrimSpace(string(entry.OrgIdentity.PositionRef.ID))
	if positionID == "" {
		return events, nil
	}
	roleIDs := make([]string, 0, len(entry.OrgIdentity.RoleRefs)+1)
	for _, role := range entry.OrgIdentity.RoleRefs {
		if id := strings.TrimSpace(string(role.ID)); id != "" {
			roleIDs = append(roleIDs, id)
		}
	}
	if len(roleIDs) == 0 {
		roleIDs = append(roleIDs, firstNonBlank(entry.RoleName, "position.member"))
	}
	sort.Strings(roleIDs)
	for _, roleID := range roleIDs {
		assignmentID := profileID + ":" + positionID + ":" + roleID
		events = append(events, ontologyInitializationEvent(tenantID, "people.position.assigned", assignmentID, entry.UpdatedAt, map[string]any{
			"id": assignmentID, "person_id": personID, "agent_id": agentID, "position_id": positionID,
			"role_id": roleID, "scope": string(entry.OrgIdentity.DepartmentRef.ID), "status": status,
		}))
	}
	return events, nil
}

type ontologyInitializationToolSource interface {
	ListCatalog(context.Context, string, int) (toolres.CatalogPage, error)
}

type ontologyInitializationSkillSource interface {
	List(context.Context) ([]skillres.SkillDefinition, error)
}

type ontologyInitializationResourceAdapter struct {
	tools  ontologyInitializationToolSource
	skills ontologyInitializationSkillSource
	runner *ontology.MappingRuleProjectionRunner
}

func newOntologyInitializationResourceAdapter(tools ontologyInitializationToolSource, skills ontologyInitializationSkillSource, runner *ontology.MappingRuleProjectionRunner) ontologyInitializationAdapter {
	return ontologyInitializationResourceAdapter{tools: tools, skills: skills, runner: runner}
}

func (ontologyInitializationResourceAdapter) Name() string { return "resources" }

func (a ontologyInitializationResourceAdapter) Reconcile(ctx context.Context, request ontologyInitializationRequest) (ontologyInitializationDomainReport, error) {
	report := ontologyInitializationDomainReport{Name: a.Name()}
	if a.tools == nil || a.skills == nil || a.runner == nil {
		return report, fmt.Errorf("ontology initialization resource dependencies are required")
	}
	afterID := ""
	for {
		page, err := a.tools.ListCatalog(ctx, afterID, request.PageSize)
		if err != nil {
			return report, err
		}
		for _, definition := range page.Definitions {
			events := toolOntologyInitializationEvents(definition)
			report.Source += len(events)
			if err := applyOntologyInitializationEvents(ctx, a.runner, events); err != nil {
				report.Failed++
				return report, err
			}
			report.Projected += len(events)
		}
		if !page.HasMore {
			break
		}
		if strings.TrimSpace(page.NextAfterID) == "" || page.NextAfterID == afterID {
			return report, fmt.Errorf("ontology initialization tool catalog did not advance")
		}
		afterID = page.NextAfterID
	}
	skills, err := a.skills.List(ctx)
	if err != nil {
		return report, err
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].ID < skills[j].ID })
	for _, definition := range skills {
		event := skillOntologyInitializationEvent(definition)
		report.Source++
		if err := applyOntologyInitializationEvents(ctx, a.runner, []graphprotocol.DomainEvent{event}); err != nil {
			report.Failed++
			return report, err
		}
		report.Projected++
	}
	return report, nil
}

func toolOntologyInitializationEvents(definition toolres.ToolDefinition) []graphprotocol.DomainEvent {
	tenantID := defaultSystemSupportOrganizationID
	id := strings.TrimSpace(string(definition.ID))
	events := []graphprotocol.DomainEvent{ontologyInitializationResourceEvent(tenantID, "resource.tool.registered", id, definition.UpdatedAt, map[string]any{
		"id": id, "name": definition.Name, "risk_level": string(definition.RiskLevel),
	})}
	if definition.MCP != nil {
		mcpID := firstNonBlank(definition.MCP.ServerName, id)
		events = append(events, ontologyInitializationResourceEvent(tenantID, "resource.mcp_server.registered", mcpID, definition.UpdatedAt, map[string]any{
			"id": mcpID, "server_name": firstNonBlank(definition.MCP.ServerName, definition.Name), "transport": string(definition.MCP.Transport),
		}))
	}
	return events
}

func skillOntologyInitializationEvent(definition skillres.SkillDefinition) graphprotocol.DomainEvent {
	id := strings.TrimSpace(string(definition.ID))
	return ontologyInitializationResourceEvent(defaultSystemSupportOrganizationID, "resource.skill.registered", id, definition.UpdatedAt, map[string]any{
		"id": id, "name": definition.Name, "description": definition.Description, "version": definition.Version,
		"risk_level": string(definition.RiskLevel), "enabled": definition.Enabled,
	})
}

type ontologyInitializationKnowledgeSource interface {
	ListKnowledgeBases(context.Context, knowledgecore.KnowledgeBaseQuery) (knowledgecore.KnowledgeBasePage, error)
	ListDocuments(context.Context, knowledgecore.DocumentQuery) (knowledgecore.DocumentPage, error)
}

type ontologyInitializationKnowledgeAdapter struct {
	source        ontologyInitializationKnowledgeSource
	runner        *ontology.MappingRuleProjectionRunner
	organizations knowledgeOntologyOrganizationCatalog
}

func newOntologyInitializationKnowledgeAdapter(source ontologyInitializationKnowledgeSource, runner *ontology.MappingRuleProjectionRunner, organizations ...knowledgeOntologyOrganizationCatalog) ontologyInitializationAdapter {
	adapter := ontologyInitializationKnowledgeAdapter{source: source, runner: runner}
	if len(organizations) > 0 {
		adapter.organizations = organizations[0]
	}
	return adapter
}

func (ontologyInitializationKnowledgeAdapter) Name() string { return "knowledge" }

func (a ontologyInitializationKnowledgeAdapter) Reconcile(ctx context.Context, _ ontologyInitializationRequest) (ontologyInitializationDomainReport, error) {
	report := ontologyInitializationDomainReport{Name: a.Name()}
	if a.source == nil || a.runner == nil {
		return report, fmt.Errorf("ontology initialization knowledge dependencies are required")
	}
	page, err := a.source.ListKnowledgeBases(ctx, knowledgecore.KnowledgeBaseQuery{})
	if err != nil {
		return report, err
	}
	sort.Slice(page.Items, func(i, j int) bool { return page.Items[i].ID < page.Items[j].ID })
	resolver := productionKnowledgeOntologyOwnerResolver{organizations: a.organizations}
	for _, base := range page.Items {
		tenantID := firstNonBlank(base.OwnerID, string(base.ID))
		if a.organizations != nil {
			resolvedTenantID, _, supported, err := resolver.Resolve(ctx, base)
			if err != nil {
				report.Failed++
				return report, fmt.Errorf("resolve knowledge base %s ontology owner: %w", base.ID, err)
			}
			if supported {
				tenantID = resolvedTenantID
			}
		}
		event := knowledgeBaseOntologyInitializationEventForTenant(base, tenantID)
		report.Source++
		if err := applyOntologyInitializationEvents(ctx, a.runner, []graphprotocol.DomainEvent{event}); err != nil {
			report.Failed++
			return report, err
		}
		report.Projected++
		documents, err := a.source.ListDocuments(ctx, knowledgecore.DocumentQuery{KnowledgeBaseID: base.ID})
		if err != nil {
			return report, err
		}
		sort.Slice(documents.Items, func(i, j int) bool { return documents.Items[i].ID < documents.Items[j].ID })
		for _, document := range documents.Items {
			report.Source++
			if err := applyOntologyInitializationEvents(ctx, a.runner, []graphprotocol.DomainEvent{knowledgeDocumentOntologyInitializationEventForTenant(base, document, tenantID)}); err != nil {
				report.Failed++
				return report, err
			}
			report.Projected++
		}
	}
	return report, nil
}

func knowledgeBaseOntologyInitializationEvent(base knowledgecore.KnowledgeBase) graphprotocol.DomainEvent {
	tenantID := firstNonBlank(base.OwnerID, string(base.ID))
	return knowledgeBaseOntologyInitializationEventForTenant(base, tenantID)
}

func knowledgeBaseOntologyInitializationEventForTenant(base knowledgecore.KnowledgeBase, tenantID string) graphprotocol.DomainEvent {
	return ontologyInitializationTrustedEvent(tenantID, "knowledge_base", "knowledge.base.created", string(base.ID), base.UpdatedAt, map[string]any{
		"id": string(base.ID), "name": base.Name, "description": base.Description, "owner_type": string(base.OwnerType),
		"owner_id": base.OwnerID, "visibility": string(base.Visibility), "status": string(base.Status),
	})
}

func knowledgeDocumentOntologyInitializationEvent(base knowledgecore.KnowledgeBase, document knowledgecore.KnowledgeDocument) graphprotocol.DomainEvent {
	tenantID := firstNonBlank(base.OwnerID, string(base.ID))
	return knowledgeDocumentOntologyInitializationEventForTenant(base, document, tenantID)
}

func knowledgeDocumentOntologyInitializationEventForTenant(base knowledgecore.KnowledgeBase, document knowledgecore.KnowledgeDocument, tenantID string) graphprotocol.DomainEvent {
	provider := firstNonBlank(string(base.Provider), "unknown")
	providerDocumentID := firstNonBlank(document.ProviderDocumentID, string(document.ID))
	return ontologyInitializationTrustedEvent(tenantID, "knowledge_document", "knowledge.document.metadata.updated", string(document.ID), document.UpdatedAt, map[string]any{
		"id": string(document.ID), "knowledge_base_id": string(document.KnowledgeBaseID), "title": firstNonBlank(document.Filename, string(document.ID)),
		"source_uri": "knowledge-document://" + string(document.ID), "provider": provider, "provider_document_id": providerDocumentID,
		"content_type": document.ContentType, "size_bytes": document.SizeBytes, "checksum": document.Checksum, "status": string(document.Status),
	})
}

type ontologyInitializationMemorySource interface {
	ListProfiles(context.Context) ([]memorycore.MemoryProfile, error)
	FindRecords(context.Context, memoryStore.RecordQuery) ([]memorycore.MemoryRecord, error)
}

type ontologyInitializationMemoryAdapter struct {
	source ontologyInitializationMemorySource
	runner *ontology.MappingRuleProjectionRunner
}

func newOntologyInitializationMemoryAdapter(source ontologyInitializationMemorySource, runner *ontology.MappingRuleProjectionRunner) ontologyInitializationAdapter {
	return ontologyInitializationMemoryAdapter{source: source, runner: runner}
}

func (ontologyInitializationMemoryAdapter) Name() string { return "memory" }

func (a ontologyInitializationMemoryAdapter) Reconcile(ctx context.Context, _ ontologyInitializationRequest) (ontologyInitializationDomainReport, error) {
	report := ontologyInitializationDomainReport{Name: a.Name()}
	if a.source == nil || a.runner == nil {
		return report, fmt.Errorf("ontology initialization memory dependencies are required")
	}
	profiles, err := a.source.ListProfiles(ctx)
	if err != nil {
		return report, err
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].ID < profiles[j].ID })
	for _, profile := range profiles {
		if isPersonalAssistantMemoryProfile(profile) {
			continue
		}
		report.Source++
		if err := applyOntologyInitializationEvents(ctx, a.runner, []graphprotocol.DomainEvent{memoryProfileOntologyInitializationEvent(profile)}); err != nil {
			report.Failed++
			return report, err
		}
		report.Projected++
	}
	records, err := a.source.FindRecords(ctx, memoryStore.RecordQuery{IncludeArchived: true})
	if err != nil {
		return report, err
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	for _, record := range records {
		if isPersonalAssistantMemoryRecord(record) {
			continue
		}
		report.Source++
		if err := applyOntologyInitializationEvents(ctx, a.runner, []graphprotocol.DomainEvent{memoryRecordOntologyInitializationEvent(record)}); err != nil {
			report.Failed++
			return report, err
		}
		report.Projected++
	}
	return report, nil
}

func memoryProfileOntologyInitializationEvent(profile memorycore.MemoryProfile) graphprotocol.DomainEvent {
	tenantID := firstNonBlank(profile.OwnerRef.OrganizationID, profile.DefaultScope.ID, string(profile.OwnerRef.AgentID), string(profile.ID))
	ownerAgentID := firstNonBlank(string(profile.OwnerRef.AgentID), profile.OwnerRef.OrganizationID, string(profile.ID))
	event := ontologyInitializationTrustedEvent(tenantID, "memory_profile", "memory.profile.created", string(profile.ID), time.Time{}, map[string]any{
		"id": string(profile.ID), "owner_agent_id": ownerAgentID, "memory_kind": string(profile.MemoryKind),
		"scope_kind": string(profile.DefaultScope.Kind), "scope_id": profile.DefaultScope.ID, "enabled": profile.Enabled,
	})
	if organizationID := strings.TrimSpace(profile.OwnerRef.OrganizationID); organizationID != "" {
		event.SourceAccessRef = organizationOntologySourceRef(organizationID)
	}
	return event
}

func memoryRecordOntologyInitializationEvent(record memorycore.MemoryRecord) graphprotocol.DomainEvent {
	record = record.Normalized()
	tenantID := memoryRecordOntologyInitializationTenantID(record)
	return ontologyInitializationTrustedEvent(tenantID, "memory_record", "memory.record.accepted", record.ID, record.UpdatedAt, map[string]any{
		"id": record.ID, "summary": "Historical memory record " + record.ID, "layer": string(record.Layer), "topic": record.Topic,
		"scope_kind": string(record.Scope.Kind), "scope_id": record.Scope.ID, "lifecycle_status": string(record.LifecycleStatus),
		"confidence": record.Confidence, "source_refs": append([]string(nil), record.SourceRefs...),
		"evidence_refs": append([]string(nil), record.SourceEvidenceRefs...),
	})
}

func memoryRecordOntologyInitializationTenantID(record memorycore.MemoryRecord) string {
	scopeID := strings.TrimSpace(record.Scope.ID)
	switch record.Scope.Kind {
	case memorycore.ScopeOrganization:
		if scopeID != "" {
			return scopeID
		}
	case memorycore.ScopeDepartment, memorycore.ScopeProject, memorycore.ScopeMission, memorycore.ScopeDiscussion, memorycore.ScopeAgent:
		if scopeID != "" {
			return string(record.Scope.Kind) + ":" + scopeID
		}
	}
	return firstNonBlank(scopeID, string(record.ProfileID), record.ID)
}

func ontologyInitializationEvent(tenantID, eventType, sourceID string, occurredAt time.Time, payload map[string]any) graphprotocol.DomainEvent {
	event := ontologyInitializationTrustedEvent(tenantID, "organization", eventType, sourceID, occurredAt, payload)
	event.SourceAccessRef = organizationOntologySourceRef(tenantID)
	return event
}

func ontologyInitializationResourceEvent(tenantID, eventType, sourceID string, occurredAt time.Time, payload map[string]any) graphprotocol.DomainEvent {
	event := ontologyInitializationTrustedEvent(tenantID, "resource", eventType, sourceID, occurredAt, payload)
	event.SourceAccessRef = trustedOntologySourceRef("resource", sourceID, "system")
	return event
}

func ontologyInitializationTrustedEvent(tenantID, sourceKind, eventType, sourceID string, occurredAt time.Time, payload map[string]any) graphprotocol.DomainEvent {
	if occurredAt.IsZero() {
		occurredAt = time.Unix(0, 1).UTC()
	} else {
		occurredAt = occurredAt.UTC()
	}
	tenantID = strings.TrimSpace(tenantID)
	sourceID = strings.TrimSpace(sourceID)
	return graphprotocol.DomainEvent{
		ID:       "ontology-initialize:" + eventType + ":" + tenantID + ":" + sourceID + ":" + strconv.FormatInt(1, 10),
		TenantID: tenantID, Type: eventType, StreamID: sourceID, Version: 1, OccurredAt: occurredAt, Payload: payload,
		PublishedBy:     graphprotocol.ActorRef{Kind: "system", ID: "ontology-initializer"},
		SourceAccessRef: trustedOntologySourceRef(sourceKind, sourceID, tenantID),
	}
}
