package event

import (
	"context"
	"fmt"
	"strings"

	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/platform/eventstore"
)

type EventStore interface {
	Append(ctx context.Context, event eventstore.Envelope) error
	Load(ctx context.Context, streamID string) ([]eventstore.Envelope, error)
}

type Sink interface {
	Append(ctx context.Context, event Envelope) error
}

type Reader interface {
	LoadMission(ctx context.Context, missionID missioncore.MissionID) ([]Envelope, error)
}

type TenantReader interface {
	LoadMissionForTenant(ctx context.Context, tenantID string, missionID missioncore.MissionID) ([]Envelope, error)
}

type StoreSink struct {
	store EventStore
}

type StoreReader struct {
	store EventStore
}

func NewStoreSink(store EventStore) *StoreSink {
	return &StoreSink{store: store}
}

func NewStoreReader(store EventStore) *StoreReader {
	return &StoreReader{store: store}
}

func (s *StoreSink) Append(ctx context.Context, event Envelope) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("mission event sink requires event store")
	}
	if err := event.Validate(); err != nil {
		return err
	}
	payload, err := encodePayload(event.Payload)
	if err != nil {
		return err
	}
	metadata, err := encodeMetadata(event)
	if err != nil {
		return err
	}
	return s.store.Append(ctx, eventstore.Envelope{
		EventID:        event.EventID,
		StreamID:       event.StreamIDOrDefault(),
		StreamVersion:  event.StreamVersion,
		EventType:      string(event.EventType),
		EventVersion:   event.EventVersion,
		Payload:        payload,
		Metadata:       metadata,
		IdempotencyKey: tenantMissionIdempotencyKey(event.Tenant(), event.IdempotencyKey),
		CorrelationID:  event.CorrelationID,
		CausationID:    event.CausationID,
		CreatedAt:      event.OccurredAt,
	})
}

func (r *StoreReader) LoadMission(ctx context.Context, missionID missioncore.MissionID) ([]Envelope, error) {
	return r.LoadMissionForTenant(ctx, "global", missionID)
}

func (r *StoreReader) LoadMissionForTenant(ctx context.Context, tenantID string, missionID missioncore.MissionID) ([]Envelope, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("mission event reader requires event store")
	}
	if missionID.IsZero() {
		return nil, fmt.Errorf("%w: mission_id is required", ErrInvalidEnvelope)
	}
	tenantID = normalizeMissionTenantID(tenantID)
	stored, err := r.store.Load(ctx, MissionStreamID(tenantID, missionID))
	if err != nil {
		return nil, err
	}
	events := make([]Envelope, 0, len(stored))
	for _, raw := range stored {
		eventType := EventType(raw.EventType)
		payload, err := decodePayload(eventType, raw.Payload)
		if err != nil {
			return nil, err
		}
		metadata, err := decodeMetadata(raw.Metadata)
		if err != nil {
			return nil, err
		}
		event := Envelope{
			EventID:        raw.EventID,
			TenantID:       normalizeMissionTenantID(metadata.TenantID),
			MissionID:      missioncore.MissionID(metadata.MissionID),
			StreamID:       raw.StreamID,
			StreamVersion:  raw.StreamVersion,
			EventType:      eventType,
			EventVersion:   raw.EventVersion,
			Actor:          metadata.Actor,
			CausationID:    raw.CausationID,
			CorrelationID:  raw.CorrelationID,
			IdempotencyKey: unscopedTenantMissionIdempotencyKey(tenantID, raw.IdempotencyKey),
			OccurredAt:     raw.CreatedAt,
			Payload:        payload,
			Metadata:       metadata.Metadata,
		}
		if event.MissionID.IsZero() {
			event.MissionID = missionID
		}
		if event.Tenant() != tenantID || event.MissionID != missionID {
			return nil, fmt.Errorf("%w: mission event tenant or mission does not match requested stream", ErrInvalidEnvelope)
		}
		if err := event.Validate(); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}

func normalizeMissionTenantID(tenantID string) string {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return "global"
	}
	return tenantID
}

func tenantMissionIdempotencyKey(tenantID, key string) string {
	key = strings.TrimSpace(key)
	if key == "" || normalizeMissionTenantID(tenantID) == "global" {
		return key
	}
	return normalizeMissionTenantID(tenantID) + "|" + key
}

func TenantScopedIdempotencyKey(tenantID, key string) string {
	return tenantMissionIdempotencyKey(tenantID, key)
}

func unscopedTenantMissionIdempotencyKey(tenantID, key string) string {
	tenantID = normalizeMissionTenantID(tenantID)
	if tenantID == "global" {
		return key
	}
	return strings.TrimPrefix(key, tenantID+"|")
}
