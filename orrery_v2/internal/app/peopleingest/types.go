package peopleingest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	EventStream         = "people.v2"
	EventSchemaVersion  = 2
	MaximumBatchEvents  = 500
	MaximumCursorLength = 4096
)

var (
	ErrInvalidBatch = errors.New("invalid People delivery batch")
	ErrConflict     = errors.New("People delivery conflict")
	ErrBusy         = errors.New("People delivery batch is already being processed")
)

var eventContracts = map[string]string{
	"people.subject.v2.changed":                "subject",
	"people.org_unit.v2.changed":               "org_unit",
	"people.job_profile.v2.changed":            "job_profile",
	"people.position.v2.changed":               "position",
	"people.assignment.v2.changed":             "assignment",
	"people.reporting_relationship.v2.changed": "reporting_relationship",
	"people.responsibility.v2.changed":         "responsibility",
	"people.responsibility_binding.v2.changed": "responsibility_binding",
	"people.subject_fact.v2.changed":           "subject_fact",
	"people.capability_assertion.v2.changed":   "capability_assertion",
	"people.segment.v2.changed":                "segment",
	"people.source_system.v2.changed":          "source_system",
	"people.source_authority_rule.v2.changed":  "source_authority_rule",
	"people.quality_issue.v2.changed":          "quality_issue",
	"people.employee_profile.v2.changed":       "profile_record",
	"people.change_set.v2.changed":             "change_set",
	"people.employment.v2.changed":             "employment",
	"people.approval_definition.v2.changed":    "approval_definition",
	"people.approval_instance.v2.changed":      "approval_instance",
}

type DeliveryEvent struct {
	TenantID          string          `json:"tenant_id"`
	EventID           string          `json:"event_id"`
	EventType         string          `json:"event_type"`
	SchemaVersion     int             `json:"schema_version"`
	AggregateType     string          `json:"aggregate_type"`
	AggregateID       string          `json:"aggregate_id"`
	AggregateRevision int64           `json:"aggregate_revision"`
	Operation         string          `json:"operation"`
	Payload           json.RawMessage `json:"payload"`
	OccurredAt        time.Time       `json:"occurred_at"`
	CorrelationID     string          `json:"correlation_id"`
	CausationID       string          `json:"causation_id,omitempty"`
}

type DeliveryBatch struct {
	Stream      string          `json:"stream"`
	TenantID    string          `json:"tenant_id"`
	BatchID     string          `json:"batch_id"`
	FirstCursor string          `json:"first_cursor"`
	LastCursor  string          `json:"last_cursor"`
	Events      []DeliveryEvent `json:"events"`
}

type DeliveryAcknowledgement struct {
	Stream               string   `json:"stream"`
	TenantID             string   `json:"tenant_id"`
	BatchID              string   `json:"batch_id"`
	LastCursor           string   `json:"last_cursor"`
	AcceptedEventIDs     []string `json:"accepted_event_ids"`
	EventStoreAckCursor  string   `json:"event_store_ack_cursor"`
	ProjectionCursor     string   `json:"projection_cursor"`
	ProjectionLagSeconds *int64   `json:"projection_lag_seconds"`
}

func (batch DeliveryBatch) Validate() error {
	if batch.Stream != EventStream {
		return invalidBatch("stream must be %q", EventStream)
	}
	if !validIdentifier(batch.TenantID, 256) || !validIdentifier(batch.BatchID, 256) {
		return invalidBatch("tenant_id and batch_id are required")
	}
	if !validOpaqueValue(batch.FirstCursor, MaximumCursorLength) || !validOpaqueValue(batch.LastCursor, MaximumCursorLength) {
		return invalidBatch("first_cursor and last_cursor are required")
	}
	if len(batch.Events) == 0 || len(batch.Events) > MaximumBatchEvents {
		return invalidBatch("events must contain between 1 and %d items", MaximumBatchEvents)
	}
	seen := make(map[string]struct{}, len(batch.Events))
	for index, event := range batch.Events {
		if err := event.validate(batch.TenantID); err != nil {
			return fmt.Errorf("%w: events[%d]: %v", ErrInvalidBatch, index, err)
		}
		if _, exists := seen[event.EventID]; exists {
			return invalidBatch("events[%d] repeats event_id %q", index, event.EventID)
		}
		seen[event.EventID] = struct{}{}
	}
	return nil
}

func (event DeliveryEvent) validate(batchTenantID string) error {
	if event.TenantID != batchTenantID {
		return errors.New("event tenant_id does not match batch tenant_id")
	}
	if !validIdentifier(event.EventID, 256) || !validIdentifier(event.AggregateID, 256) {
		return errors.New("event_id and aggregate_id are required")
	}
	wantAggregate, supported := eventContracts[event.EventType]
	if !supported {
		return fmt.Errorf("unsupported event_type %q", event.EventType)
	}
	if event.SchemaVersion != EventSchemaVersion {
		return fmt.Errorf("schema_version must be %d", EventSchemaVersion)
	}
	if event.AggregateType != wantAggregate {
		return fmt.Errorf("aggregate_type %q does not match event_type %q", event.AggregateType, event.EventType)
	}
	if event.AggregateRevision < 1 {
		return errors.New("aggregate_revision must be positive")
	}
	if !validIdentifier(event.Operation, 128) {
		return errors.New("operation is required")
	}
	if event.OccurredAt.IsZero() {
		return errors.New("occurred_at is required")
	}
	if !validIdentifier(event.CorrelationID, 256) || (event.CausationID != "" && !validIdentifier(event.CausationID, 256)) {
		return errors.New("correlation_id is required and causation_id must be valid when present")
	}
	if _, err := DecodePayload(event.Payload); err != nil {
		return err
	}
	return nil
}

func DecodePayload(raw json.RawMessage) (map[string]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.New("payload is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil || payload == nil {
		return nil, errors.New("payload must be a JSON object")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("payload must contain one JSON object")
	}
	return payload, nil
}

func validIdentifier(value string, maximum int) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= maximum && !strings.ContainsAny(value, "\x00\r\n")
}

func validOpaqueValue(value string, maximum int) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= maximum && !strings.ContainsAny(value, "\x00\r\n")
}

func invalidBatch(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidBatch, fmt.Sprintf(format, args...))
}
