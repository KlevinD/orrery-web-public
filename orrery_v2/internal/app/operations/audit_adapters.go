package operations

import (
	"context"

	developmentproject "orrery_v2/internal/app/developmentproject"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	resourceadmin "orrery_v2/internal/app/resourceadmin"
	"orrery_v2/internal/app/scmconnection"
)

type MissionCommandAuditRecorder struct {
	store *AuditStore
}

func NewMissionCommandAuditRecorder(store *AuditStore) *MissionCommandAuditRecorder {
	return &MissionCommandAuditRecorder{store: store}
}

func (r *MissionCommandAuditRecorder) RecordAuditEvent(ctx context.Context, event missioncontrol.AuditEvent) error {
	return r.store.RecordAuditEvent(ctx, AuditEventDTO{
		AuditID:       event.AuditID,
		TenantID:      event.TenantID,
		ActorID:       event.ActorID,
		ViewerID:      event.ViewerID,
		MissionID:     event.MissionID,
		EntityType:    event.EntityType,
		EntityID:      event.EntityID,
		Action:        event.Action,
		Result:        event.Result,
		Severity:      event.Severity,
		CorrelationID: event.CorrelationID,
		Summary:       event.Summary,
		Metadata:      event.Metadata,
		OccurredAt:    event.OccurredAt,
	})
}

type ResourceCommandAuditRecorder struct {
	store *AuditStore
}

func NewResourceCommandAuditRecorder(store *AuditStore) *ResourceCommandAuditRecorder {
	return &ResourceCommandAuditRecorder{store: store}
}

func (r *ResourceCommandAuditRecorder) RecordAuditEvent(ctx context.Context, event resourceadmin.AuditEvent) error {
	mapped := AuditEventDTO{
		AuditID:       event.AuditID,
		ActorID:       event.ActorID,
		ViewerID:      event.ViewerID,
		MissionID:     event.MissionID,
		EntityType:    event.EntityType,
		EntityID:      event.EntityID,
		Action:        event.Action,
		Result:        event.Result,
		Severity:      event.Severity,
		CorrelationID: event.CorrelationID,
		Summary:       event.Summary,
		Metadata:      event.Metadata,
		OccurredAt:    event.OccurredAt,
	}
	if event.AuditID != "" {
		return r.store.RecordAuditEventIdempotent(ctx, mapped)
	}
	return r.store.RecordAuditEvent(ctx, mapped)
}

type DevelopmentProjectAuditRecorder struct{ store *AuditStore }

func NewDevelopmentProjectAuditRecorder(store *AuditStore) *DevelopmentProjectAuditRecorder {
	return &DevelopmentProjectAuditRecorder{store: store}
}

func (r *DevelopmentProjectAuditRecorder) RecordDevelopmentProjectAudit(ctx context.Context, event developmentproject.AuditEvent) error {
	metadata := cloneAuditMetadata(event.Metadata)
	metadata["tenant_id"] = event.TenantID
	return r.store.RecordAuditEvent(ctx, AuditEventDTO{
		AuditID:  event.AuditID,
		TenantID: event.TenantID, ActorID: event.ActorID, ViewerID: event.ActorID, EntityType: "development_project", EntityID: event.EntityID,
		Action: event.Action, Result: event.Result, Severity: "info", Summary: event.Summary,
		Metadata: metadata, OccurredAt: event.OccurredAt,
	})
}

type SCMConnectionAuditRecorder struct{ store *AuditStore }

func NewSCMConnectionAuditRecorder(store *AuditStore) *SCMConnectionAuditRecorder {
	return &SCMConnectionAuditRecorder{store: store}
}

func (r *SCMConnectionAuditRecorder) RecordSCMConnectionAudit(ctx context.Context, event scmconnection.AuditEvent) error {
	metadata := cloneAuditMetadata(event.Metadata)
	metadata["tenant_id"] = event.TenantID
	return r.store.RecordAuditEvent(ctx, AuditEventDTO{
		AuditID:  event.AuditID,
		TenantID: event.TenantID, ActorID: event.ActorID, ViewerID: event.ActorID, EntityType: "scm_connection", EntityID: event.EntityID,
		Action: event.Action, Result: event.Result, Severity: "info", Summary: event.Summary,
		Metadata: metadata, OccurredAt: event.OccurredAt,
	})
}

func cloneAuditMetadata(metadata map[string]string) map[string]string {
	out := make(map[string]string, len(metadata)+1)
	for key, value := range metadata {
		out[key] = value
	}
	return out
}
