package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/eventstore"
	"orrery_v2/internal/platform/graphprotocol"
)

const ontologyEventTypePrefix = "ontology."

type productionOntologyEventLog struct {
	store *eventstore.Store
}

type ontologyEventMetadata struct {
	TenantID        string                  `json:"tenant_id"`
	SourceEventID   string                  `json:"source_event_id,omitempty"`
	StreamID        string                  `json:"stream_id,omitempty"`
	Version         int64                   `json:"version,omitempty"`
	SchemaVersion   string                  `json:"schema_version,omitempty"`
	Operation       string                  `json:"operation,omitempty"`
	OccurredAt      string                  `json:"occurred_at,omitempty"`
	PublishedBy     graphprotocol.ActorRef  `json:"published_by,omitempty"`
	SourceAccessRef graphprotocol.AccessRef `json:"source_access_ref,omitempty"`
}

func newProductionOntologyEventLog(core *db.Store) productionOntologyEventLog {
	return productionOntologyEventLog{store: eventstore.New(core)}
}

func (l productionOntologyEventLog) Record(ctx context.Context, event graphprotocol.DomainEvent) error {
	if l.store == nil {
		return fmt.Errorf("ontology event store is required")
	}
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return fmt.Errorf("marshal ontology event payload: %w", err)
	}
	occurredAt := ""
	if !event.OccurredAt.IsZero() {
		occurredAt = event.OccurredAt.UTC().Format(time.RFC3339Nano)
	}
	metadata, err := json.Marshal(ontologyEventMetadata{
		TenantID:        event.TenantID,
		SourceEventID:   event.SourceEventID,
		StreamID:        event.StreamID,
		Version:         event.Version,
		SchemaVersion:   event.SchemaVersion,
		Operation:       event.Operation,
		OccurredAt:      occurredAt,
		PublishedBy:     event.PublishedBy,
		SourceAccessRef: event.SourceAccessRef,
	})
	if err != nil {
		return fmt.Errorf("marshal ontology event metadata: %w", err)
	}
	return l.store.Append(ctx, eventstore.Envelope{
		EventID:        event.ID,
		StreamID:       ontologyEventStreamID(event),
		StreamVersion:  1,
		EventType:      ontologyEventTypePrefix + event.Type,
		EventVersion:   1,
		Payload:        payload,
		Metadata:       metadata,
		IdempotencyKey: ontologyEventTypePrefix + event.ID,
		CorrelationID:  event.CorrelationID,
		CausationID:    event.CausationID,
		CreatedAt:      event.OccurredAt,
	})
}

func (l productionOntologyEventLog) Get(ctx context.Context, eventID string) (graphprotocol.DomainEvent, error) {
	if l.store == nil {
		return graphprotocol.DomainEvent{}, fmt.Errorf("ontology event store is required")
	}
	envelope, err := l.store.GetByID(ctx, strings.TrimSpace(eventID))
	if err != nil {
		return graphprotocol.DomainEvent{}, err
	}
	if !strings.HasPrefix(envelope.EventType, ontologyEventTypePrefix) {
		return graphprotocol.DomainEvent{}, fmt.Errorf("event %s is not an ontology event", eventID)
	}
	return ontologyEventFromEnvelope(envelope)
}

func ontologyEventStreamID(event graphprotocol.DomainEvent) string {
	streamID := strings.TrimSpace(event.ID)
	if streamID == "" {
		streamID = strings.TrimSpace(event.StreamID)
	}
	return ontologyEventTypePrefix + streamID
}

func ontologyEventFromEnvelope(envelope eventstore.Envelope) (graphprotocol.DomainEvent, error) {
	var payload map[string]any
	if len(envelope.Payload) > 0 {
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return graphprotocol.DomainEvent{}, fmt.Errorf("decode ontology event payload: %w", err)
		}
	}
	var metadata ontologyEventMetadata
	if len(envelope.Metadata) > 0 {
		if err := json.Unmarshal(envelope.Metadata, &metadata); err != nil {
			return graphprotocol.DomainEvent{}, fmt.Errorf("decode ontology event metadata: %w", err)
		}
	}
	eventType := strings.TrimPrefix(envelope.EventType, ontologyEventTypePrefix)
	streamID := metadata.StreamID
	if streamID == "" {
		streamID = strings.TrimPrefix(envelope.StreamID, ontologyEventTypePrefix)
	}
	version := metadata.Version
	if version == 0 {
		version = int64(envelope.StreamVersion)
	}
	occurredAt := envelope.CreatedAt
	if metadata.OccurredAt != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, metadata.OccurredAt); err == nil {
			occurredAt = parsed
		}
	}
	return graphprotocol.DomainEvent{
		ID:              envelope.EventID,
		SourceEventID:   metadata.SourceEventID,
		TenantID:        metadata.TenantID,
		Type:            eventType,
		StreamID:        streamID,
		Version:         version,
		SchemaVersion:   metadata.SchemaVersion,
		Operation:       metadata.Operation,
		CorrelationID:   envelope.CorrelationID,
		CausationID:     envelope.CausationID,
		OccurredAt:      occurredAt,
		Payload:         payload,
		PublishedBy:     metadata.PublishedBy,
		SourceAccessRef: metadata.SourceAccessRef,
	}, nil
}
