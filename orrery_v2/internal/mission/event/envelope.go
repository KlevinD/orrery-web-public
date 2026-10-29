package event

import (
	"errors"
	"fmt"
	"strings"
	"time"

	missioncore "orrery_v2/internal/mission/core"
)

var (
	ErrInvalidEnvelope     = errors.New("invalid mission event envelope")
	ErrUnknownEventType    = errors.New("unknown mission event type")
	ErrUnknownEventVersion = errors.New("unknown mission event version")
)

type Envelope struct {
	EventID        string
	TenantID       string
	MissionID      missioncore.MissionID
	StreamID       string
	StreamVersion  int
	EventType      EventType
	EventVersion   int
	Actor          ActorRef
	CausationID    string
	CorrelationID  string
	IdempotencyKey string
	OccurredAt     time.Time
	Payload        Payload
	Metadata       map[string]string
}

func (e Envelope) Validate() error {
	var errs []error
	if e.EventID == "" {
		errs = append(errs, fmt.Errorf("%w: event_id is required", ErrInvalidEnvelope))
	}
	if e.MissionID.IsZero() {
		errs = append(errs, fmt.Errorf("%w: mission_id is required", ErrInvalidEnvelope))
	}
	if streamID := strings.TrimSpace(e.StreamID); streamID != "" && streamID != MissionStreamID(e.Tenant(), e.MissionID) {
		errs = append(errs, fmt.Errorf("%w: stream_id does not match tenant and mission", ErrInvalidEnvelope))
	}
	if e.StreamVersion <= 0 {
		errs = append(errs, fmt.Errorf("%w: positive stream_version is required", ErrInvalidEnvelope))
	}
	if e.EventType == "" || !IsKnownEventType(e.EventType) {
		errs = append(errs, fmt.Errorf("%w: %s", ErrUnknownEventType, e.EventType))
	}
	if e.EventVersion != 1 {
		errs = append(errs, fmt.Errorf("%w: %s version %d", ErrUnknownEventVersion, e.EventType, e.EventVersion))
	}
	if e.Actor.IsZero() || !e.Actor.Kind.IsValid() || e.Actor.ID == "" {
		errs = append(errs, fmt.Errorf("%w: actor_ref is required", ErrInvalidEnvelope))
	}
	if e.OccurredAt.IsZero() {
		errs = append(errs, fmt.Errorf("%w: occurred_at is required", ErrInvalidEnvelope))
	}
	if e.Payload == nil {
		errs = append(errs, fmt.Errorf("%w: versioned payload is required", ErrInvalidEnvelope))
	} else if e.Payload.EventPayloadVersion() != e.EventVersion {
		errs = append(errs, fmt.Errorf("%w: payload version %d does not match envelope version %d", ErrInvalidEnvelope, e.Payload.EventPayloadVersion(), e.EventVersion))
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

func (e Envelope) StreamIDOrDefault() string {
	if e.StreamID != "" {
		return e.StreamID
	}
	return MissionStreamID(e.Tenant(), e.MissionID)
}

func (e Envelope) Tenant() string {
	tenantID := strings.TrimSpace(e.TenantID)
	if tenantID == "" {
		return "global"
	}
	return tenantID
}

func MissionStreamID(tenantID string, missionID missioncore.MissionID) string {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" || tenantID == "global" {
		return missionID.String()
	}
	return tenantID + "/" + missionID.String()
}
