package peopleingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestServicePersistsProjectsAndReplaysCompletedBatch(t *testing.T) {
	ctx := context.Background()
	consumer := &recordingPeopleConsumer{}
	service, store := newPeopleIngestionTestService(t, consumer)
	batch := peopleDeliveryBatchFixture()
	hash := deliveryBatchHash(t, batch)

	ack, err := service.Ingest(ctx, batch, hash)
	if err != nil {
		t.Fatalf("first Ingest: %v", err)
	}
	if ack.EventStoreAckCursor != batch.LastCursor || ack.ProjectionCursor != batch.LastCursor ||
		ack.ProjectionLagSeconds == nil || *ack.ProjectionLagSeconds != 0 ||
		len(ack.AcceptedEventIDs) != 1 || ack.AcceptedEventIDs[0] != batch.Events[0].EventID {
		t.Fatalf("acknowledgement = %+v", ack)
	}
	if len(consumer.events) != 1 {
		t.Fatalf("consumer calls = %d, want 1", len(consumer.events))
	}

	replayed, err := service.Ingest(ctx, batch, hash)
	if err != nil {
		t.Fatalf("replayed Ingest: %v", err)
	}
	if replayed.BatchID != ack.BatchID || len(consumer.events) != 1 {
		t.Fatalf("replayed acknowledgement = %+v, consumer calls = %d", replayed, len(consumer.events))
	}
	assertPeopleBatchState(t, store, batch, batchStatusComplete, 1, 1)
}

func TestServiceResumesAfterProjectionFailureWithoutLosingSourceEvent(t *testing.T) {
	ctx := context.Background()
	consumer := &recordingPeopleConsumer{err: errors.New("projection unavailable")}
	service, store := newPeopleIngestionTestService(t, consumer)
	batch := peopleDeliveryBatchFixture()
	hash := deliveryBatchHash(t, batch)

	if _, err := service.Ingest(ctx, batch, hash); err == nil {
		t.Fatal("first Ingest error = nil, want projection failure")
	}
	assertPeopleBatchState(t, store, batch, batchStatusPending, 1, 0)
	var sourceEvents int
	if err := store.QueryRow(ctx, `SELECT COUNT(*) FROM people_ingestion_events WHERE tenant_id = ? AND event_id = ?`, batch.TenantID, batch.Events[0].EventID).Scan(&sourceEvents); err != nil {
		t.Fatalf("count source events: %v", err)
	}
	if sourceEvents != 1 {
		t.Fatalf("source event count = %d, want 1", sourceEvents)
	}

	consumer.err = nil
	if _, err := service.Ingest(ctx, batch, hash); err != nil {
		t.Fatalf("retry Ingest: %v", err)
	}
	if len(consumer.events) != 2 {
		t.Fatalf("consumer calls = %d, want failed call plus retry", len(consumer.events))
	}
	assertPeopleBatchState(t, store, batch, batchStatusComplete, 1, 1)
}

func TestServiceScopesEventIdentityByTenant(t *testing.T) {
	ctx := context.Background()
	consumer := &recordingPeopleConsumer{}
	service, store := newPeopleIngestionTestService(t, consumer)
	first := peopleDeliveryBatchFixture()
	if _, err := service.Ingest(ctx, first, deliveryBatchHash(t, first)); err != nil {
		t.Fatalf("first tenant Ingest: %v", err)
	}
	second := peopleDeliveryBatchFixture()
	second.TenantID = "tenant-b"
	second.BatchID = "batch-b"
	second.FirstCursor = "cursor-b"
	second.LastCursor = "cursor-b"
	second.Events[0].TenantID = second.TenantID
	if _, err := service.Ingest(ctx, second, deliveryBatchHash(t, second)); err != nil {
		t.Fatalf("second tenant Ingest: %v", err)
	}
	var sourceEvents int
	if err := store.QueryRow(ctx, `SELECT COUNT(*) FROM people_ingestion_events WHERE event_id = ?`, first.Events[0].EventID).Scan(&sourceEvents); err != nil {
		t.Fatalf("count tenant-scoped source events: %v", err)
	}
	if sourceEvents != 2 || len(consumer.events) != 2 {
		t.Fatalf("source events = %d, consumer calls = %d, want 2/2", sourceEvents, len(consumer.events))
	}
}

func TestServiceRejectsBatchIdempotencyAndEventIdentityConflicts(t *testing.T) {
	ctx := context.Background()
	service, _ := newPeopleIngestionTestService(t, &recordingPeopleConsumer{})
	batch := peopleDeliveryBatchFixture()
	if _, err := service.Ingest(ctx, batch, deliveryBatchHash(t, batch)); err != nil {
		t.Fatalf("seed Ingest: %v", err)
	}

	differentBatchBody := batch
	differentBatchBody.Events = append([]DeliveryEvent(nil), batch.Events...)
	differentBatchBody.Events[0].Payload = json.RawMessage(`{"id":"subject-1","type":"human","display_name":"Grace","status":"active","revision":1,"valid_from":"2026-08-09T08:00:00Z","recorded_at":"2026-08-09T08:00:00Z"}`)
	if _, err := service.Ingest(ctx, differentBatchBody, deliveryBatchHash(t, differentBatchBody)); !errors.Is(err, ErrConflict) {
		t.Fatalf("same batch_id different body error = %v, want ErrConflict", err)
	}

	eventConflict := differentBatchBody
	eventConflict.BatchID = "batch-2"
	eventConflict.FirstCursor = "cursor-3"
	eventConflict.LastCursor = "cursor-3"
	if _, err := service.Ingest(ctx, eventConflict, deliveryBatchHash(t, eventConflict)); !errors.Is(err, ErrConflict) {
		t.Fatalf("same event_id different event error = %v, want ErrConflict", err)
	}
}

func TestDeliveryBatchValidatesCompletePeopleV2Manifest(t *testing.T) {
	wantContracts := map[string]string{
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
	if !reflect.DeepEqual(eventContracts, wantContracts) {
		t.Fatalf("People event contracts = %#v, want %#v", eventContracts, wantContracts)
	}
	for eventType, aggregateType := range wantContracts {
		batch := peopleDeliveryBatchFixture()
		batch.Events[0].EventType = eventType
		batch.Events[0].AggregateType = aggregateType
		if err := batch.Validate(); err != nil {
			t.Fatalf("Validate(%s): %v", eventType, err)
		}
	}
	batch := peopleDeliveryBatchFixture()
	batch.Events[0].EventType = "people.future.v2.changed"
	if err := batch.Validate(); !errors.Is(err, ErrInvalidBatch) {
		t.Fatalf("unsupported event error = %v, want ErrInvalidBatch", err)
	}
}

type recordingPeopleConsumer struct {
	events []DeliveryEvent
	err    error
}

func (consumer *recordingPeopleConsumer) ConsumePeopleEvent(_ context.Context, event DeliveryEvent) error {
	consumer.events = append(consumer.events, event)
	return consumer.err
}

func newPeopleIngestionTestService(t *testing.T, consumer EventConsumer) (*Service, *db.Store) {
	t.Helper()
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := migration.NewRunner([]migration.Migration{ingestionMigration()}).Up(ctx, store); err != nil {
		t.Fatalf("migrate People ingestion store: %v", err)
	}
	now := time.Date(2026, 8, 9, 9, 0, 0, 0, time.UTC)
	service, err := NewService(ServiceOptions{
		Store: NewStore(store), Consumer: consumer, Now: func() time.Time { return now },
		LeaseDuration: time.Minute, NewLeaseToken: func() (string, error) { return "lease-token", nil },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return service, store
}

func peopleDeliveryBatchFixture() DeliveryBatch {
	return DeliveryBatch{
		Stream: EventStream, TenantID: "tenant-a", BatchID: "batch-1",
		FirstCursor: "cursor-1", LastCursor: "cursor-1",
		Events: []DeliveryEvent{{
			TenantID: "tenant-a", EventID: "event-1", EventType: "people.subject.v2.changed",
			SchemaVersion: 2, AggregateType: "subject", AggregateID: "subject-1", AggregateRevision: 1,
			Operation:  "created",
			Payload:    json.RawMessage(`{"id":"subject-1","type":"human","display_name":"Ada","status":"active","revision":1,"valid_from":"2026-08-09T08:00:00Z","recorded_at":"2026-08-09T08:00:00Z"}`),
			OccurredAt: time.Date(2026, 8, 9, 8, 0, 0, 0, time.UTC), CorrelationID: "correlation-1",
		}},
	}
}

func deliveryBatchHash(t *testing.T, batch DeliveryBatch) string {
	t.Helper()
	encoded, err := json.Marshal(batch)
	if err != nil {
		t.Fatalf("marshal delivery batch: %v", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func assertPeopleBatchState(t *testing.T, store *db.Store, batch DeliveryBatch, status string, recorded, projected int) {
	t.Helper()
	var gotStatus, leaseToken string
	var gotRecorded, gotProjected int
	if err := store.QueryRow(context.Background(), `
SELECT status, recorded_count, projected_count, lease_token
FROM people_ingestion_batches
WHERE tenant_id = ? AND stream = ? AND batch_id = ?`,
		batch.TenantID, batch.Stream, batch.BatchID,
	).Scan(&gotStatus, &gotRecorded, &gotProjected, &leaseToken); err != nil {
		t.Fatalf("read batch state: %v", err)
	}
	if gotStatus != status || gotRecorded != recorded || gotProjected != projected || leaseToken != "" {
		t.Fatalf("batch state = status:%s recorded:%d projected:%d lease:%q", gotStatus, gotRecorded, gotProjected, leaseToken)
	}
}
