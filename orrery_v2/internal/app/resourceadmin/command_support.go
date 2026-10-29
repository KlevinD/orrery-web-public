package resourceadmin

import (
	"context"
	"time"

	"orrery_v2/internal/platform/idgen"
)

type AuditEvent struct {
	AuditID       string
	ActorID       string
	ViewerID      string
	MissionID     string
	EntityType    string
	EntityID      string
	Action        string
	Result        string
	Severity      string
	CorrelationID string
	Summary       string
	Metadata      map[string]string
	OccurredAt    time.Time
}

type AuditRecorderPort interface {
	RecordAuditEvent(ctx context.Context, event AuditEvent) error
}

type ResourceIDGeneratorPort interface {
	NewID(prefix string) (string, error)
}

type PrefixedResourceIDGenerator struct{}

func NewPrefixedResourceIDGenerator() PrefixedResourceIDGenerator {
	return PrefixedResourceIDGenerator{}
}

func (PrefixedResourceIDGenerator) NewID(prefix string) (string, error) {
	return idgen.NewRandom(prefix).NewIDContext()
}

func (s *CommandService) generateResourceID(target *string, prefix string) error {
	generator := s.ids
	if generator == nil {
		generator = NewPrefixedResourceIDGenerator()
	}
	id, err := generator.NewID(prefix)
	if err != nil {
		return err
	}
	*target = id
	return nil
}

func (s *CommandService) recordAudit(ctx context.Context, viewer ViewerRef, action, entityID, summary string) error {
	return s.recordAuditWithOutcome(ctx, viewer, action, entityID, "succeeded", "info", summary)
}

func (s *CommandService) recordAuditWithOutcome(ctx context.Context, viewer ViewerRef, action, entityID, result, severity, summary string) error {
	return s.recordAuditWithOutcomeAndMetadata(ctx, viewer, action, entityID, result, severity, summary, nil)
}

func (s *CommandService) recordAuditWithMetadata(ctx context.Context, viewer ViewerRef, action, entityID, summary string, metadata map[string]string) error {
	return s.recordAuditWithOutcomeAndMetadata(ctx, viewer, action, entityID, "succeeded", "info", summary, metadata)
}

func (s *CommandService) recordAuditWithOutcomeAndMetadata(ctx context.Context, viewer ViewerRef, action, entityID, result, severity, summary string, metadata map[string]string) error {
	if s == nil || s.audit == nil {
		return nil
	}
	return s.audit.RecordAuditEvent(ctx, AuditEvent{
		ActorID:       viewer.ViewerID,
		ViewerID:      viewer.ViewerID,
		EntityType:    "resource",
		EntityID:      entityID,
		Action:        action,
		Result:        result,
		Severity:      severity,
		CorrelationID: resourceCorrelationID(viewer),
		Summary:       summary,
		Metadata:      cloneStringMap(metadata),
		OccurredAt:    time.Now().UTC(),
	})
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func resourceCorrelationID(viewer ViewerRef) string {
	if viewer.RequestID != "" {
		return viewer.RequestID
	}
	return viewer.IdempotencyKey
}
