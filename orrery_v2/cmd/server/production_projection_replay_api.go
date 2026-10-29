package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
	projectadmin "orrery_v2/internal/app/projectadmin"
)

func (p productionAPI) computeProjectionAuditAcknowledgement(_ context.Context, audit contextgraph.ProjectionAudit, req contextgraph.ProjectionAuditAcknowledgeRequest) (contextgraph.ProjectionAudit, error) {
	if strings.TrimSpace(audit.Status) != "skipped" {
		return contextgraph.ProjectionAudit{}, fmt.Errorf("%w: projection audit %s status %q cannot be acknowledged", projectadmin.ErrInvalidRequest, audit.ID, audit.Status)
	}
	if audit.Details == nil {
		audit.Details = map[string]any{}
	}
	audit.Status = "acknowledged"
	audit.Reason = req.Reason
	audit.ObservedAt = time.Now().UTC()
	if req.ActorID != "" {
		audit.Details["acknowledged_by"] = req.ActorID
	}
	if req.ActorKind != "" {
		audit.Details["acknowledged_by_kind"] = req.ActorKind
	}
	if req.Reason != "" {
		audit.Details["acknowledged_reason"] = req.Reason
	}
	if !audit.ObservedAt.IsZero() {
		audit.Details["acknowledged_at"] = audit.ObservedAt.Format(time.RFC3339Nano)
	}
	return audit, nil
}

func (p productionAPI) computeProjectionAuditReplay(ctx context.Context, audit contextgraph.ProjectionAudit) (contextgraph.ProjectionAudit, error) {
	result, plan, err := p.prepareProjectionAuditReplay(ctx, audit)
	if err != nil {
		return contextgraph.ProjectionAudit{}, err
	}
	if err := p.ontologyProject.ApplyPlan(ctx, plan); err != nil {
		return contextgraph.ProjectionAudit{}, err
	}
	return result, nil
}

func (p productionAPI) prepareProjectionAuditReplay(ctx context.Context, audit contextgraph.ProjectionAudit) (contextgraph.ProjectionAudit, ontology.MappingRuleProjectionPlan, error) {
	switch audit.SourceEventType {
	case "project.organization.linked":
		return p.prepareProjectOrganizationProjectionAuditReplay(ctx, audit)
	case "memory.scope.linked", "memory.lineage.linked", "memory.knowledge.citation.added", "memory.knowledge_chunk.citation.added", "memory.knowledge_anchor.citation.added":
		if p.ontologyProject == nil {
			return contextgraph.ProjectionAudit{}, ontology.MappingRuleProjectionPlan{}, projectadmin.ErrServiceDependency
		}
		return productionMemoryRecordOntologySink{runner: p.ontologyProject, core: p.contextGraphCore, events: p.ontologyEvents}.prepareProjectionAuditReplay(ctx, audit)
	case "knowledge.document.status.updated", "knowledge.chunk.metadata.updated", "knowledge.page.metadata.updated", "knowledge.anchor.metadata.updated":
		if p.ontologyProject == nil {
			return contextgraph.ProjectionAudit{}, ontology.MappingRuleProjectionPlan{}, projectadmin.ErrServiceDependency
		}
		return productionDocumentOntologySink{runner: p.ontologyProject, core: p.contextGraphCore, events: p.ontologyEvents}.prepareProjectionAuditReplay(ctx, audit)
	default:
		return contextgraph.ProjectionAudit{}, ontology.MappingRuleProjectionPlan{}, fmt.Errorf("%w: projection audit %s event type %s is not replayable", projectadmin.ErrInvalidRequest, audit.ID, audit.SourceEventType)
	}
}
