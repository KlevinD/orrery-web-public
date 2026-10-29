package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/eventstore"
)

const EventVersion = 1

type PlatformEventRecorder interface {
	Record(ctx context.Context, message platform.EventMessage) (string, error)
}

type PlatformEventReader interface {
	Load(ctx context.Context, streamID string) ([]platform.RecordedEvent, error)
}

type Recorder interface {
	RecordMemoryAudit(ctx context.Context, event Event) error
}

type EventRecorder struct {
	Recorder PlatformEventRecorder
}

func NewEventRecorder(recorder PlatformEventRecorder) *EventRecorder {
	return &EventRecorder{Recorder: recorder}
}

func (r *EventRecorder) RecordMemoryAudit(ctx context.Context, event Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r == nil || r.Recorder == nil {
		return fmt.Errorf("memory audit event recorder is not configured")
	}
	if strings.TrimSpace(event.SubjectID) == "" {
		return fmt.Errorf("memory audit subject_id is required")
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode memory audit event: %w", err)
	}
	streamID := event.MissionID
	if streamID == "" {
		streamID = "memory-audit"
	}
	idempotencyKey := strings.TrimSpace(event.IdempotencyKey)
	if idempotencyKey == "" {
		idempotencyKey = event.Type.String() + "|" + event.SubjectID
	}
	for attempt := 0; attempt < 3; attempt++ {
		streamVersion, err := r.nextStreamVersion(ctx, streamID)
		if err != nil {
			return fmt.Errorf("load memory audit event stream: %w", err)
		}
		_, err = r.Recorder.Record(ctx, platform.EventMessage{
			StreamID:       streamID,
			StreamVersion:  streamVersion,
			EventType:      event.Type.String(),
			EventVersion:   EventVersion,
			Payload:        payload,
			IdempotencyKey: idempotencyKey,
		})
		if err == nil {
			return nil
		}
		if !errors.Is(err, eventstore.ErrConflict) {
			return fmt.Errorf("record memory audit event: %w", err)
		}
		if attempt == 2 {
			return fmt.Errorf("record memory audit event: %w", err)
		}
	}
	return nil
}

func (r *EventRecorder) nextStreamVersion(ctx context.Context, streamID string) (int, error) {
	reader, ok := r.Recorder.(PlatformEventReader)
	if !ok {
		return 1, nil
	}
	events, err := reader.Load(ctx, streamID)
	if err != nil {
		return 0, err
	}
	return len(events) + 1, nil
}
