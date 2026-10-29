package main

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"orrery_v2/internal/app/contextgraph"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	projectadmin "orrery_v2/internal/app/projectadmin"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
)

func (p productionAPI) projectContextGraphStatus(ctx context.Context, project projectadmin.Project) (projectadmin.ProjectGraphStatus, error) {
	if p.contextGraphCore == nil || project.ProjectID == "" {
		return projectadmin.ProjectGraphStatus{}, nil
	}
	projectProjected, err := contextGraphObjectExists(ctx, p.contextGraphCore, projectScopeTenantID(project.TenantID, project.ProjectID), "project", project.ProjectID)
	if err != nil {
		return projectadmin.ProjectGraphStatus{}, err
	}
	status := projectadmin.ProjectGraphStatus{ProjectTenantProjected: projectProjected}
	if project.OrganizationID == "" {
		return status, nil
	}
	organizationProjected, err := contextGraphObjectExists(ctx, p.contextGraphCore, project.OrganizationID, "organization", project.OrganizationID)
	if err != nil {
		return projectadmin.ProjectGraphStatus{}, err
	}
	organizationViewProjected, err := contextGraphLinkExists(ctx, p.contextGraphCore, project.OrganizationID, "project_belongs_to_organization", projectOrganizationViewProjectID(project), project.OrganizationID)
	if err != nil {
		return projectadmin.ProjectGraphStatus{}, err
	}
	status.OrganizationObjectProjected = organizationProjected
	status.OrganizationViewProjected = organizationViewProjected
	return status, nil
}

func (p productionAPI) publishMissionCreatedToOntology(ctx context.Context, viewer missioncontrol.ViewerRef, mission missioncontrol.MissionDTO) error {
	if p.ontologyProject == nil {
		return nil
	}
	eventID := "mission-created:" + mission.MissionID
	return p.applyOntologyEvent(ctx, graphprotocol.DomainEvent{
		ID:       eventID,
		TenantID: missionScopeTenantID(viewer.Tenant(), mission.MissionID),
		Type:     "mission.created",
		StreamID: mission.MissionID,
		Version:  1,
		Payload: map[string]any{
			"id":     mission.MissionID,
			"title":  mission.Title,
			"status": mission.Status,
		},
		PublishedBy:     graphprotocol.ActorRef{Kind: string(viewer.ViewerKind), ID: viewer.ViewerID},
		OccurredAt:      mission.CreatedAt,
		SourceAccessRef: missionOntologySourceRef(viewer.Tenant(), mission.MissionID),
	})
}

func (p productionAPI) applyOntologyEvent(ctx context.Context, event graphprotocol.DomainEvent) error {
	if p.ontologyProject == nil {
		return nil
	}
	if p.ontologyEvents.store != nil {
		if err := p.ontologyEvents.Record(ctx, event); err != nil {
			return err
		}
	}
	_, err := p.ontologyProject.ApplyEvent(ctx, event)
	return err
}

func (p productionAPI) publishProjectCreatedToOntology(ctx context.Context, viewer projectadmin.ViewerRef, project projectadmin.Project) error {
	if p.ontologyProject == nil {
		return nil
	}
	eventID := "project-created:" + project.ProjectID
	return p.applyOntologyEvent(ctx, graphprotocol.DomainEvent{
		ID:       eventID,
		TenantID: projectScopeTenantID(project.TenantID, project.ProjectID),
		Type:     "project.created",
		StreamID: project.ProjectID,
		Version:  projectEventVersion(project),
		Payload: map[string]any{
			"id":     project.ProjectID,
			"title":  project.Title,
			"status": project.Status,
		},
		PublishedBy:     graphprotocol.ActorRef{Kind: string(viewer.ViewerKind), ID: viewer.ViewerID},
		OccurredAt:      project.CreatedAt,
		SourceAccessRef: projectOntologySourceRef(project.TenantID, project.ProjectID),
	})
}

func (p productionAPI) publishProjectUpdatedToOntology(ctx context.Context, viewer projectadmin.ViewerRef, project projectadmin.Project) error {
	if p.ontologyProject == nil {
		return nil
	}
	eventID := "project-updated:" + project.ProjectID + ":rev-" + projectEventRevision(project)
	return p.applyOntologyEvent(ctx, graphprotocol.DomainEvent{
		ID:       eventID,
		TenantID: projectScopeTenantID(project.TenantID, project.ProjectID),
		Type:     "project.updated",
		StreamID: project.ProjectID,
		Version:  projectEventVersion(project),
		Payload: map[string]any{
			"id":    project.ProjectID,
			"title": project.Title,
		},
		PublishedBy:     graphprotocol.ActorRef{Kind: string(viewer.ViewerKind), ID: viewer.ViewerID},
		OccurredAt:      project.UpdatedAt,
		SourceAccessRef: projectOntologySourceRef(project.TenantID, project.ProjectID),
	})
}

func (p productionAPI) publishProjectArchivedToOntology(ctx context.Context, viewer projectadmin.ViewerRef, project projectadmin.Project) error {
	if p.ontologyProject == nil {
		return nil
	}
	eventID := "project-archived:" + project.ProjectID + ":rev-" + projectEventRevision(project)
	return p.applyOntologyEvent(ctx, graphprotocol.DomainEvent{
		ID:       eventID,
		TenantID: projectScopeTenantID(project.TenantID, project.ProjectID),
		Type:     "project.archived",
		StreamID: project.ProjectID,
		Version:  projectEventVersion(project),
		Payload: map[string]any{
			"id":     project.ProjectID,
			"status": project.Status,
		},
		PublishedBy:     graphprotocol.ActorRef{Kind: string(viewer.ViewerKind), ID: viewer.ViewerID},
		OccurredAt:      project.UpdatedAt,
		SourceAccessRef: projectOntologySourceRef(project.TenantID, project.ProjectID),
	})
}

func (p productionAPI) publishProjectOrganizationLinkedToOntology(ctx context.Context, viewer projectadmin.ViewerRef, project projectadmin.Project) error {
	if p.ontologyProject == nil || p.contextGraphCore == nil || project.OrganizationID == "" {
		return nil
	}
	eventID := "project-organization-linked:" + project.ProjectID + ":" + project.OrganizationID + ":rev-" + projectEventRevision(project)
	event := p.projectOrganizationLinkedEvent(viewer, project, eventID)
	if p.ontologyEvents.store != nil {
		if err := p.ontologyEvents.Record(ctx, event); err != nil {
			return err
		}
	}
	exists, err := contextGraphObjectExists(ctx, p.contextGraphCore, project.OrganizationID, "organization", project.OrganizationID)
	if err != nil {
		return err
	}
	if !exists {
		if err := p.recordProjectOrganizationProjectionSkippedAudit(ctx, viewer, project, eventID); err != nil {
			return err
		}
		return nil
	}
	_, err = p.ontologyProject.ApplyEvent(ctx, event)
	return err
}

func (p productionAPI) recordProjectOrganizationProjectionSkippedAudit(ctx context.Context, viewer projectadmin.ViewerRef, project projectadmin.Project, eventID string) error {
	if p.contextGraphCore == nil || project.OrganizationID == "" {
		return nil
	}
	event := p.projectOrganizationLinkedEvent(viewer, project, eventID)
	return contextgraph.NewPlatformStore(p.contextGraphCore).SaveProjectionAudit(ctx, contextgraph.ProjectionAudit{
		ID:              "projection-skipped:" + event.ID + ":missing-organization-target",
		TenantID:        event.TenantID,
		SourceEventID:   event.ID,
		SourceEventType: event.Type,
		Status:          "skipped",
		ReasonCode:      "missing_organization_target",
		Reason:          "project organization target object is not projected",
		Details: map[string]any{
			"project_id":      project.ProjectID,
			"project_ref_id":  projectOrganizationViewProjectID(project),
			"organization_id": project.OrganizationID,
			"title":           project.Title,
			"status":          string(project.Status),
			"version":         projectEventVersion(project),
			"actor_kind":      string(viewer.ViewerKind),
			"actor_id":        viewer.ViewerID,
			"missing_targets": projectionAuditTargets([]projectionAuditTarget{{ObjectType: "organization", ObjectID: project.OrganizationID}}),
		},
		SourceAccessRef: contextGraphAccessRef(event.SourceAccessRef),
		ObservedAt:      event.OccurredAt,
	})
}

func (p productionAPI) projectOrganizationLinkedEvent(viewer projectadmin.ViewerRef, project projectadmin.Project, eventID string) graphprotocol.DomainEvent {
	return graphprotocol.DomainEvent{
		ID:       eventID,
		TenantID: project.OrganizationID,
		Type:     "project.organization.linked",
		StreamID: project.ProjectID,
		Version:  projectEventVersion(project),
		Payload: map[string]any{
			"project_id":      project.ProjectID,
			"project_ref_id":  projectOrganizationViewProjectID(project),
			"organization_id": project.OrganizationID,
			"title":           project.Title,
			"status":          string(project.Status),
		},
		PublishedBy:     graphprotocol.ActorRef{Kind: string(viewer.ViewerKind), ID: viewer.ViewerID},
		OccurredAt:      project.UpdatedAt,
		SourceAccessRef: projectOntologySourceRef(project.TenantID, project.ProjectID),
	}
}

func projectOrganizationViewProjectID(project projectadmin.Project) string {
	return "project:" + project.ProjectID
}

func contextGraphLinkExists(ctx context.Context, store *db.Store, tenantID string, linkType string, fromID string, toID string) (bool, error) {
	if store == nil {
		return false, nil
	}
	var found string
	err := store.QueryRow(ctx, `SELECT id FROM context_graph_links WHERE tenant_id = ? AND link_type = ? AND from_object_id = ? AND to_object_id = ?`, tenantID, linkType, fromID, toID).Scan(&found)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return found != "", nil
}

func projectOrganizationEventFromProjectionAudit(audit contextgraph.ProjectionAudit) (graphprotocol.DomainEvent, error) {
	projectID, _ := audit.Details["project_id"].(string)
	projectRefID, _ := audit.Details["project_ref_id"].(string)
	organizationID, _ := audit.Details["organization_id"].(string)
	title, _ := audit.Details["title"].(string)
	status, _ := audit.Details["status"].(string)
	actorKind, _ := audit.Details["actor_kind"].(string)
	actorID, _ := audit.Details["actor_id"].(string)
	if projectID == "" || organizationID == "" {
		return graphprotocol.DomainEvent{}, fmt.Errorf("projection audit %s missing project organization ids", audit.ID)
	}
	if projectRefID == "" {
		projectRefID = "project:" + projectID
	}
	if actorKind == "" {
		actorKind = "service"
	}
	if actorID == "" {
		actorID = "projection-audit-replay"
	}
	occurredAt := audit.ObservedAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	return graphprotocol.DomainEvent{
		ID:       audit.SourceEventID,
		TenantID: organizationID,
		Type:     "project.organization.linked",
		StreamID: projectID,
		Version:  auditInt64Value(audit.Details["version"]),
		Payload: map[string]any{
			"project_id":      projectID,
			"project_ref_id":  projectRefID,
			"organization_id": organizationID,
			"title":           title,
			"status":          status,
		},
		PublishedBy:     graphprotocol.ActorRef{Kind: actorKind, ID: actorID},
		OccurredAt:      occurredAt,
		SourceAccessRef: protocolAccessRef(audit.SourceAccessRef),
	}, nil
}

func auditInt64Value(raw any) int64 {
	switch value := raw.(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	default:
		return 1
	}
}

func projectEventRevision(project projectadmin.Project) string {
	return strconv.FormatInt(projectEventVersion(project), 10)
}

func projectEventVersion(project projectadmin.Project) int64 {
	if project.Version > 0 {
		return project.Version
	}
	return 1
}
