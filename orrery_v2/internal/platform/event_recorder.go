package platform

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/eventstore"
	"orrery_v2/internal/platform/idgen"
)

type EventMessage struct {
	StreamID       string
	StreamVersion  int
	EventType      string
	EventVersion   int
	Payload        []byte
	Metadata       []byte
	IdempotencyKey string
	CorrelationID  string
	CausationID    string
}

type RecordedEvent struct {
	EventID        string
	StreamID       string
	StreamVersion  int
	EventType      string
	EventVersion   int
	Payload        []byte
	Metadata       []byte
	IdempotencyKey string
	CorrelationID  string
	CausationID    string
	CreatedAt      time.Time
}

type EventRecorder struct {
	store       *eventstore.Store
	idGenerator idgen.ErrorGenerator
}

func NewEventRecorder(store *eventstore.Store, generator idgen.ErrorGenerator) *EventRecorder {
	return &EventRecorder{store: store, idGenerator: generator}
}

func (r *EventRecorder) Record(ctx context.Context, message EventMessage) (string, error) {
	if r == nil || r.store == nil {
		return "", fmt.Errorf("platform event recorder is not configured")
	}
	event, err := buildEventEnvelope(message, r.idGenerator)
	if err != nil {
		return "", err
	}
	if err := r.store.Append(ctx, event); err != nil {
		return "", err
	}
	return event.EventID, nil
}

func (r *EventRecorder) Load(ctx context.Context, streamID string) ([]RecordedEvent, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("platform event recorder is not configured")
	}
	events, err := r.store.Load(ctx, streamID)
	if err != nil {
		return nil, err
	}
	recorded := make([]RecordedEvent, len(events))
	for i, event := range events {
		recorded[i] = recordedEventFromEnvelope(event)
	}
	return recorded, nil
}

func buildEventEnvelope(message EventMessage, generator idgen.ErrorGenerator) (eventstore.Envelope, error) {
	if strings.TrimSpace(message.StreamID) == "" {
		return eventstore.Envelope{}, fmt.Errorf("event stream id is required")
	}
	if strings.TrimSpace(message.EventType) == "" {
		return eventstore.Envelope{}, fmt.Errorf("event type is required")
	}
	if message.StreamVersion <= 0 {
		return eventstore.Envelope{}, fmt.Errorf("event stream version must be positive")
	}
	if message.EventVersion <= 0 {
		return eventstore.Envelope{}, fmt.Errorf("event version must be positive")
	}
	if generator == nil {
		return eventstore.Envelope{}, fmt.Errorf("platform id generator is not configured")
	}
	id, err := generator.NewIDContext()
	if err != nil {
		return eventstore.Envelope{}, fmt.Errorf("generate event id: %w", err)
	}
	if strings.TrimSpace(id) == "" {
		return eventstore.Envelope{}, fmt.Errorf("generated event id is empty")
	}
	return eventstore.Envelope{
		EventID:        id,
		StreamID:       strings.TrimSpace(message.StreamID),
		StreamVersion:  message.StreamVersion,
		EventType:      strings.TrimSpace(message.EventType),
		EventVersion:   message.EventVersion,
		Payload:        append([]byte(nil), message.Payload...),
		Metadata:       append([]byte(nil), message.Metadata...),
		IdempotencyKey: message.IdempotencyKey,
		CorrelationID:  message.CorrelationID,
		CausationID:    message.CausationID,
	}, nil
}

func recordedEventFromEnvelope(event eventstore.Envelope) RecordedEvent {
	return RecordedEvent{
		EventID:        event.EventID,
		StreamID:       event.StreamID,
		StreamVersion:  event.StreamVersion,
		EventType:      event.EventType,
		EventVersion:   event.EventVersion,
		Payload:        append([]byte(nil), event.Payload...),
		Metadata:       append([]byte(nil), event.Metadata...),
		IdempotencyKey: event.IdempotencyKey,
		CorrelationID:  event.CorrelationID,
		CausationID:    event.CausationID,
		CreatedAt:      event.CreatedAt,
	}
}
