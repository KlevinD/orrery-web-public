package main

import (
	"context"
	"fmt"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
	projectadmin "orrery_v2/internal/app/projectadmin"
)

func (p productionAPI) CreateProject(ctx context.Context, viewer projectadmin.ViewerRef, cmd projectadmin.CreateProjectCommand) (projectadmin.Project, error) {
	if p.projectCommand == nil {
		return projectadmin.Project{}, projectadmin.ErrServiceDependency
	}
	project, err := p.projectCommand.CreateProject(ctx, viewer, cmd)
	if err != nil {
		return projectadmin.Project{}, err
	}
	if err := p.publishProjectCreatedToOntology(ctx, viewer, project); err != nil {
		return projectadmin.Project{}, err
	}
	if err := p.publishProjectOrganizationLinkedToOntology(ctx, viewer, project); err != nil {
		return projectadmin.Project{}, err
	}
	return project, nil
}

func (p productionAPI) ListProjects(ctx context.Context, viewer projectadmin.ViewerRef, query projectadmin.ListProjectsQuery) (projectadmin.ProjectPage, error) {
	if p.projectCommand == nil {
		return projectadmin.ProjectPage{}, projectadmin.ErrServiceDependency
	}
	page, err := p.projectCommand.ListProjects(ctx, viewer, query)
	if err != nil {
		return projectadmin.ProjectPage{}, err
	}
	for i := range page.Projects {
		status, err := p.projectContextGraphStatus(ctx, page.Projects[i].Project)
		if err != nil {
			return projectadmin.ProjectPage{}, err
		}
		page.Projects[i].ContextGraph = status
	}
	return page, nil
}

func (p productionAPI) GetProjectView(ctx context.Context, viewer projectadmin.ViewerRef, projectID string) (projectadmin.ProjectView, error) {
	if p.projectCommand == nil {
		return projectadmin.ProjectView{}, projectadmin.ErrServiceDependency
	}
	view, err := p.projectCommand.GetProjectView(ctx, viewer, projectID)
	if err != nil {
		return projectadmin.ProjectView{}, err
	}
	status, err := p.projectContextGraphStatus(ctx, view.Project)
	if err != nil {
		return projectadmin.ProjectView{}, err
	}
	view.ContextGraph = status
	return view, nil
}

func (p productionAPI) UpdateProject(ctx context.Context, viewer projectadmin.ViewerRef, cmd projectadmin.UpdateProjectCommand) (projectadmin.Project, error) {
	if p.projectCommand == nil {
		return projectadmin.Project{}, projectadmin.ErrServiceDependency
	}
	project, err := p.projectCommand.UpdateProject(ctx, viewer, cmd)
	if err != nil {
		return projectadmin.Project{}, err
	}
	if err := p.publishProjectUpdatedToOntology(ctx, viewer, project); err != nil {
		return projectadmin.Project{}, err
	}
	if err := p.publishProjectOrganizationLinkedToOntology(ctx, viewer, project); err != nil {
		return projectadmin.Project{}, err
	}
	return project, nil
}

func (p productionAPI) ArchiveProject(ctx context.Context, viewer projectadmin.ViewerRef, cmd projectadmin.ArchiveProjectCommand) (projectadmin.Project, error) {
	if p.projectCommand == nil {
		return projectadmin.Project{}, projectadmin.ErrServiceDependency
	}
	project, err := p.projectCommand.ArchiveProject(ctx, viewer, cmd)
	if err != nil {
		return projectadmin.Project{}, err
	}
	if err := p.publishProjectArchivedToOntology(ctx, viewer, project); err != nil {
		return projectadmin.Project{}, err
	}
	if err := p.publishProjectOrganizationLinkedToOntology(ctx, viewer, project); err != nil {
		return projectadmin.Project{}, err
	}
	return project, nil
}

func (p productionAPI) computeProjectOrganizationProjectionAuditReplay(ctx context.Context, audit contextgraph.ProjectionAudit) (contextgraph.ProjectionAudit, error) {
	result, plan, err := p.prepareProjectOrganizationProjectionAuditReplay(ctx, audit)
	if err != nil {
		return contextgraph.ProjectionAudit{}, err
	}
	if err := p.ontologyProject.ApplyPlan(ctx, plan); err != nil {
		return contextgraph.ProjectionAudit{}, err
	}
	return result, nil
}

func (p productionAPI) prepareProjectOrganizationProjectionAuditReplay(ctx context.Context, audit contextgraph.ProjectionAudit) (contextgraph.ProjectionAudit, ontology.MappingRuleProjectionPlan, error) {
	if p.ontologyProject == nil || p.contextGraphCore == nil {
		return contextgraph.ProjectionAudit{}, ontology.MappingRuleProjectionPlan{}, projectadmin.ErrServiceDependency
	}
	if audit.SourceEventType != "project.organization.linked" {
		return contextgraph.ProjectionAudit{}, ontology.MappingRuleProjectionPlan{}, fmt.Errorf("%w: projection audit %s event type %s is not replayable for project organization", projectadmin.ErrInvalidRequest, audit.ID, audit.SourceEventType)
	}
	event, err := projectOrganizationEventFromProjectionAudit(audit)
	if err != nil {
		return contextgraph.ProjectionAudit{}, ontology.MappingRuleProjectionPlan{}, err
	}
	organizationID, _ := event.Payload["organization_id"].(string)
	if organizationID == "" {
		return contextgraph.ProjectionAudit{}, ontology.MappingRuleProjectionPlan{}, fmt.Errorf("%w: projection audit %s missing organization_id", projectadmin.ErrInvalidRequest, audit.ID)
	}
	exists, err := contextGraphObjectExists(ctx, p.contextGraphCore, organizationID, "organization", organizationID)
	if err != nil {
		return contextgraph.ProjectionAudit{}, ontology.MappingRuleProjectionPlan{}, err
	}
	if !exists {
		return audit, ontology.MappingRuleProjectionPlan{}, nil
	}
	plan, err := p.ontologyProject.PrepareEvent(ctx, event)
	if err != nil {
		return contextgraph.ProjectionAudit{}, ontology.MappingRuleProjectionPlan{}, err
	}
	audit.Status = "replayed"
	audit.Reason = "project organization projection audit replay applied"
	audit.ObservedAt = time.Now().UTC()
	return audit, plan, nil
}
