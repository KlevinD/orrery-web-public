package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/peopleingest"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestPeopleIngestionHTTPHandlerVerifiesTransportContract(t *testing.T) {
	batch := productionPeopleDeliveryBatchFixture()
	body, err := json.Marshal(batch)
	if err != nil {
		t.Fatalf("marshal batch: %v", err)
	}
	ack := peopleingest.DeliveryAcknowledgement{
		Stream: batch.Stream, TenantID: batch.TenantID, BatchID: batch.BatchID, LastCursor: batch.LastCursor,
		AcceptedEventIDs: []string{batch.Events[0].EventID}, EventStoreAckCursor: batch.LastCursor, ProjectionCursor: batch.LastCursor,
	}
	ingestor := &recordingPeopleBatchIngestor{ack: ack}
	handler := &peopleIngestionHTTPHandler{
		expectedAuthorization: "Bearer 0123456789abcdef0123456789abcdef",
		ingestor:              ingestor,
		next:                  http.NotFoundHandler(),
	}

	valid := newPeopleDeliveryRequest(t, body, batch.BatchID)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, valid)
	if response.Code != http.StatusOK || ingestor.calls != 1 || ingestor.batch.BatchID != batch.BatchID {
		t.Fatalf("valid response=%d calls=%d batch=%+v body=%s", response.Code, ingestor.calls, ingestor.batch, response.Body.String())
	}

	tests := []struct {
		name       string
		mutate     func(*http.Request)
		body       []byte
		wantStatus int
	}{
		{name: "credential", mutate: func(request *http.Request) { request.Header.Set("Authorization", "Bearer wrong") }, body: body, wantStatus: http.StatusUnauthorized},
		{name: "digest", mutate: func(request *http.Request) { request.Header.Set("Digest", "SHA-256=invalid") }, body: body, wantStatus: http.StatusBadRequest},
		{name: "idempotency", mutate: func(request *http.Request) { request.Header.Set("Idempotency-Key", "other-batch") }, body: body, wantStatus: http.StatusBadRequest},
		{name: "content type", mutate: func(request *http.Request) { request.Header.Set("Content-Type", "text/plain") }, body: body, wantStatus: http.StatusUnsupportedMediaType},
		{name: "unknown field", body: append(body[:len(body)-1:len(body)-1], []byte(`,"unexpected":true}`)...), wantStatus: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := ingestor.calls
			request := newPeopleDeliveryRequest(t, test.body, batch.BatchID)
			if test.mutate != nil {
				test.mutate(request)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus || ingestor.calls != before {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, ingestor.calls, response.Body.String())
			}
		})
	}
}

func TestProductionPeopleIngestionProjectsMappedEventsAndReplaysIdempotently(t *testing.T) {
	ctx := context.Background()
	core := newProductionPeopleIngestionTestCore(t)
	handler := newProductionPeopleIngestionTestHandler(t, core)
	batch := productionPeopleDeliveryBatchFixture()
	batch.BatchID = "batch-mapped-people-v2"
	batch.FirstCursor = "cursor-mapped-1"
	batch.LastCursor = "cursor-mapped-8"
	batch.Events = nil
	for index, test := range productionPeopleProjectionCases() {
		batch.Events = append(batch.Events, peopleingest.DeliveryEvent{
			TenantID: batch.TenantID, EventID: "event-" + test.name, EventType: test.eventType,
			SchemaVersion: 2, AggregateType: test.aggregateType, AggregateID: test.aggregateID, AggregateRevision: 1,
			Operation: "created", Payload: json.RawMessage(test.payload),
			OccurredAt: time.Date(2026, 8, 9, 8, index, 0, 0, time.UTC), CorrelationID: "correlation-1", CausationID: "change-set-1",
		})
	}
	body, err := json.Marshal(batch)
	if err != nil {
		t.Fatalf("marshal batch: %v", err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, newPeopleDeliveryRequest(t, body, batch.BatchID))
		if response.Code != http.StatusOK {
			var lastError string
			_ = core.QueryRow(ctx, `SELECT last_error FROM people_ingestion_batches WHERE tenant_id = ? AND batch_id = ?`, batch.TenantID, batch.BatchID).Scan(&lastError)
			t.Fatalf("attempt %d status=%d body=%s last_error=%s", attempt+1, response.Code, response.Body.String(), lastError)
		}
	}
	objects, links, facts, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, batch.TenantID)
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	for index, test := range productionPeopleProjectionCases() {
		t.Run(test.name, func(t *testing.T) {
			assertContextGraphObject(t, objects, test.objectType, test.objectID)
			assertContextGraphFact(t, facts, test.objectType, test.objectID, test.factPredicate, test.factValue)
			if test.linkType != "" {
				assertContextGraphLink(t, links, test.linkType, test.linkFromID, test.linkToID)
			}
			assertContextGraphEvidence(t, evidence, batch.Events[index].EventID)

			stored, err := newProductionOntologyEventLog(core).Get(ctx, peopleOntologyEventID(batch.TenantID, batch.Events[index].EventID))
			if err != nil {
				t.Fatalf("get ontology event: %v", err)
			}
			if stored.SourceEventID != batch.Events[index].EventID || stored.Operation != "created" || stored.CorrelationID != "correlation-1" || stored.CausationID != "change-set-1" ||
				stored.SourceAccessRef.Kind != "organization" || stored.SourceAccessRef.ID != batch.TenantID {
				t.Fatalf("stored ontology event = %+v", stored)
			}
			var ontologyEvents int
			if err := core.QueryRow(ctx, `SELECT COUNT(*) FROM platform_events WHERE event_type = ?`, "ontology."+test.eventType).Scan(&ontologyEvents); err != nil {
				t.Fatalf("count ontology events: %v", err)
			}
			if ontologyEvents != 1 {
				t.Fatalf("ontology events = %d, want 1", ontologyEvents)
			}
		})
	}
	var sourceEvents int
	if err := core.QueryRow(ctx, `SELECT COUNT(*) FROM people_ingestion_events`).Scan(&sourceEvents); err != nil {
		t.Fatalf("count People source events: %v", err)
	}
	if sourceEvents != len(batch.Events) {
		t.Fatalf("source events = %d, want %d", sourceEvents, len(batch.Events))
	}
}

func TestProductionPeopleIngestionProjectsChildOrganizationBeforeParent(t *testing.T) {
	ctx := context.Background()
	core := newProductionPeopleIngestionTestCore(t)
	handler := newProductionPeopleIngestionTestHandler(t, core)

	send := func(batchID, cursor string, event peopleingest.DeliveryEvent) {
		t.Helper()
		batch := peopleingest.DeliveryBatch{
			Stream: peopleingest.EventStream, TenantID: event.TenantID, BatchID: batchID,
			FirstCursor: cursor, LastCursor: cursor, Events: []peopleingest.DeliveryEvent{event},
		}
		body, err := json.Marshal(batch)
		if err != nil {
			t.Fatalf("marshal batch %s: %v", batchID, err)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, newPeopleDeliveryRequest(t, body, batchID))
		if response.Code != http.StatusOK {
			t.Fatalf("batch %s status=%d body=%s", batchID, response.Code, response.Body.String())
		}
	}

	child := peopleingest.DeliveryEvent{
		TenantID: "tenant-a", EventID: "event-org-child", EventType: "people.org_unit.v2.changed",
		SchemaVersion: 2, AggregateType: "org_unit", AggregateID: "org-child", AggregateRevision: 1,
		Operation: "created", Payload: json.RawMessage(`{"id":"org-child","parent_id":"org-parent","code":"CHILD","display_name":"Child","status":"active","revision":1}`),
		OccurredAt: time.Date(2026, 8, 9, 8, 0, 0, 0, time.UTC), CorrelationID: "org-bootstrap",
	}
	send("batch-org-child", "cursor-org-1", child)

	objects, links, _, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, child.TenantID)
	if err != nil {
		t.Fatalf("snapshot child organization: %v", err)
	}
	placeholder := assertContextGraphObject(t, objects, "organization_unit", "org-parent")
	if placeholder.Status != "reference_pending" || placeholder.Properties["_orrery_reference"] != "pending" {
		t.Fatalf("parent placeholder = %+v", placeholder)
	}
	assertContextGraphLink(t, links, "organization_unit_part_of_organization_unit", "org-child", "org-parent")

	parent := child
	parent.EventID = "event-org-parent"
	parent.AggregateID = "org-parent"
	parent.Payload = json.RawMessage(`{"id":"org-parent","code":"PARENT","display_name":"Parent","status":"active","revision":1}`)
	send("batch-org-parent", "cursor-org-2", parent)

	objects, links, _, _, err = contextgraph.NewPlatformStore(core).Snapshot(ctx, child.TenantID)
	if err != nil {
		t.Fatalf("snapshot parent organization: %v", err)
	}
	upgraded := assertContextGraphObject(t, objects, "organization_unit", "org-parent")
	if upgraded.Label != "Parent" || upgraded.CanonicalKey != "org-parent" {
		t.Fatalf("upgraded parent = %+v", upgraded)
	}
	if _, pending := upgraded.Properties["_orrery_reference"]; pending {
		t.Fatalf("upgraded parent retained placeholder marker: %+v", upgraded)
	}
	assertContextGraphLink(t, links, "organization_unit_part_of_organization_unit", "org-child", "org-parent")
}

func TestProductionPeopleIngestionProjectsTransferredAssignmentEnvelope(t *testing.T) {
	ctx := context.Background()
	core := newProductionPeopleIngestionTestCore(t)
	handler := newProductionPeopleIngestionTestHandler(t, core)
	batch := productionPeopleDeliveryBatchFixture()
	batch.BatchID = "batch-transferred-assignment"
	batch.FirstCursor = "cursor-assignment-transfer"
	batch.LastCursor = batch.FirstCursor
	batch.Events = []peopleingest.DeliveryEvent{{
		TenantID: batch.TenantID, EventID: "event-assignment-transfer", EventType: "people.assignment.v2.changed",
		SchemaVersion: 2, AggregateType: "assignment", AggregateID: "assignment-old", AggregateRevision: 2,
		Operation: "transferred",
		Payload: json.RawMessage(`{
			"ended":{"ID":"assignment-old","Kind":"primary","Revision":2,"TenantID":"tenant-a","Effective":{"End":"2026-08-25T10:01:15.416Z","Start":"2026-06-21T00:00:00Z"},"OrgUnitID":"org-old","SubjectID":"subject-1","Allocation":10000,"PositionID":"position-old","PredecessorID":"","TransitionKind":"start"},
			"replacement":{"ID":"assignment-new","Kind":"primary","Revision":1,"TenantID":"tenant-a","Effective":{"End":null,"Start":"2026-08-25T10:01:15.416Z"},"OrgUnitID":"org-new","SubjectID":"subject-1","Allocation":10000,"PositionID":"position-new","PredecessorID":"assignment-old","TransitionKind":"transfer"}
		}`),
		OccurredAt: time.Date(2026, 8, 25, 10, 1, 15, 416000000, time.UTC), CorrelationID: "assignment-transfer", CausationID: "change-set-transfer",
	}}
	body, err := json.Marshal(batch)
	if err != nil {
		t.Fatalf("marshal transfer batch: %v", err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, newPeopleDeliveryRequest(t, body, batch.BatchID))
	if response.Code != http.StatusOK {
		var lastError string
		_ = core.QueryRow(ctx, `SELECT last_error FROM people_ingestion_batches WHERE tenant_id = ? AND batch_id = ?`, batch.TenantID, batch.BatchID).Scan(&lastError)
		t.Fatalf("status=%d body=%s last_error=%s", response.Code, response.Body.String(), lastError)
	}

	objects, links, facts, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, batch.TenantID)
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	assertContextGraphObject(t, objects, "workforce_assignment", "assignment-old")
	assertContextGraphObject(t, objects, "workforce_assignment", "assignment-new")
	assertContextGraphFact(t, facts, "workforce_assignment", "assignment-old", "revision", float64(2))
	assertContextGraphFact(t, facts, "workforce_assignment", "assignment-new", "transition_kind", "transfer")
	assertContextGraphLink(t, links, "workforce_assignment_for_subject", "assignment-old", "subject-1")
	assertContextGraphLink(t, links, "workforce_assignment_for_subject", "assignment-new", "subject-1")
	assertContextGraphLink(t, links, "workforce_assignment_succeeds_assignment", "assignment-new", "assignment-old")
	assertContextGraphEvidence(t, evidence, "event-assignment-transfer")
}

func TestProductionPeopleIngestionRejectsIncompleteMappedPayloads(t *testing.T) {
	ctx := context.Background()
	core := newProductionPeopleIngestionTestCore(t)
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatalf("new ontology projection runner: %v", err)
	}
	consumer := productionPeopleEventConsumer{events: newProductionOntologyEventLog(core), projections: runner}
	for _, test := range productionPeopleProjectionCases() {
		t.Run(test.name, func(t *testing.T) {
			event := productionPeopleDeliveryBatchFixture().Events[0]
			event.EventID = "incomplete-" + test.name
			event.EventType = test.eventType
			event.AggregateType = test.aggregateType
			event.AggregateID = test.aggregateID
			event.Payload = json.RawMessage(`{}`)
			if err := consumer.ConsumePeopleEvent(ctx, event); err == nil {
				t.Fatal("incomplete mapped payload was accepted")
			}
		})
	}
}

func TestProductionPeopleIngestionRetainsUnmappedEventsWithoutGraphProjection(t *testing.T) {
	ctx := context.Background()
	core := newProductionPeopleIngestionTestCore(t)
	handler := newProductionPeopleIngestionTestHandler(t, core)
	contracts := []struct {
		eventType     string
		aggregateType string
	}{
		{"people.subject_fact.v2.changed", "subject_fact"},
		{"people.capability_assertion.v2.changed", "capability_assertion"},
		{"people.segment.v2.changed", "segment"},
		{"people.source_system.v2.changed", "source_system"},
		{"people.source_authority_rule.v2.changed", "source_authority_rule"},
		{"people.quality_issue.v2.changed", "quality_issue"},
		{"people.employee_profile.v2.changed", "profile_record"},
		{"people.change_set.v2.changed", "change_set"},
		{"people.approval_definition.v2.changed", "approval_definition"},
		{"people.approval_instance.v2.changed", "approval_instance"},
	}
	batch := productionPeopleDeliveryBatchFixture()
	batch.BatchID = "batch-unmapped-people-v2"
	batch.FirstCursor = "cursor-unmapped-1"
	batch.LastCursor = "cursor-unmapped-8"
	batch.Events = nil
	for index, contract := range contracts {
		id := "unmapped-" + contract.aggregateType
		batch.Events = append(batch.Events, peopleingest.DeliveryEvent{
			TenantID: batch.TenantID, EventID: "event-" + id, EventType: contract.eventType,
			SchemaVersion: 2, AggregateType: contract.aggregateType, AggregateID: id, AggregateRevision: 1,
			Operation: "created", Payload: json.RawMessage(`{"id":"` + id + `","revision":1}`),
			OccurredAt: time.Date(2026, 8, 9, 10, index, 0, 0, time.UTC), CorrelationID: "correlation-unmapped",
		})
	}
	body, err := json.Marshal(batch)
	if err != nil {
		t.Fatalf("marshal unmapped batch: %v", err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, newPeopleDeliveryRequest(t, body, batch.BatchID))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	objects, links, facts, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, batch.TenantID)
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	if len(objects) != 0 || len(links) != 0 || len(facts) != 0 || len(evidence) != 0 {
		t.Fatalf("unmapped events changed graph: objects=%d links=%d facts=%d evidence=%d", len(objects), len(links), len(facts), len(evidence))
	}
	var sourceEvents, ontologyEvents int
	if err := core.QueryRow(ctx, `SELECT COUNT(*) FROM people_ingestion_events`).Scan(&sourceEvents); err != nil {
		t.Fatalf("count People source events: %v", err)
	}
	if err := core.QueryRow(ctx, `SELECT COUNT(*) FROM platform_events WHERE event_type LIKE ?`, "ontology.people.%").Scan(&ontologyEvents); err != nil {
		t.Fatalf("count ontology events: %v", err)
	}
	if sourceEvents != len(contracts) || ontologyEvents != len(contracts) {
		t.Fatalf("retained events source=%d ontology=%d, want %d/%d", sourceEvents, ontologyEvents, len(contracts), len(contracts))
	}
}

func TestProductionPeopleEventConsumerAppliesEmploymentWorkerNumber(t *testing.T) {
	ctx := context.Background()
	core := newProductionPeopleIngestionTestCore(t)
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatalf("new ontology projection runner: %v", err)
	}
	profiles := &fakePeopleEmploymentProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"H000001": {
			ID: "H000001", AgentKind: agentcore.AgentKindHuman,
			PersonSource: agentcore.PersonSourceFacts{Source: "people", SourceRecordID: "member-dingpi", SourceTenantID: "tenant-preview"},
		},
	}}
	consumer := productionPeopleEventConsumer{events: newProductionOntologyEventLog(core), projections: runner, profiles: profiles}
	event := productionPeopleDeliveryBatchFixture().Events[0]
	event.TenantID = "tenant-preview"
	event.EventID = "employment-created"
	event.EventType = "people.employment.v2.changed"
	event.AggregateType = "employment"
	event.AggregateID = "employment-dingpi"
	event.Payload = json.RawMessage(`{"id":"employment-dingpi","subject_id":"member-dingpi","worker_number":"H000001","revision":1}`)

	if err := consumer.ConsumePeopleEvent(ctx, event); err != nil {
		t.Fatalf("ConsumePeopleEvent: %v", err)
	}
	got := profiles.profiles["H000001"].PersonSource
	if got.EmployeeNumber != "H000001" || profiles.saveCalls != 1 {
		t.Fatalf("employment projection source=%+v save_calls=%d", got, profiles.saveCalls)
	}
	if err := consumer.ConsumePeopleEvent(ctx, event); err != nil {
		t.Fatalf("replay ConsumePeopleEvent: %v", err)
	}
	if profiles.saveCalls != 1 {
		t.Fatalf("idempotent replay save_calls=%d, want 1", profiles.saveCalls)
	}
}

func TestProductionPeopleEventConsumerRejectsMismatchedWorkerNumberProfileID(t *testing.T) {
	ctx := context.Background()
	core := newProductionPeopleIngestionTestCore(t)
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatalf("new ontology projection runner: %v", err)
	}
	profiles := &fakePeopleEmploymentProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"human-quzheng": {
			ID: "human-quzheng", AgentKind: agentcore.AgentKindHuman,
			PersonSource: agentcore.PersonSourceFacts{Source: "people", SourceRecordID: "member-quzong", SourceTenantID: "tenant-preview"},
		},
	}}
	consumer := productionPeopleEventConsumer{events: newProductionOntologyEventLog(core), projections: runner, profiles: profiles}
	event := productionPeopleDeliveryBatchFixture().Events[0]
	event.TenantID = "tenant-preview"
	event.EventID = "employment-profile-id-mismatch"
	event.EventType = "people.employment.v2.changed"
	event.AggregateType = "employment"
	event.AggregateID = "employment-quzheng"
	event.Payload = json.RawMessage(`{"id":"employment-quzheng","subject_id":"member-quzong","worker_number":"H000002","revision":1}`)

	if err := consumer.ConsumePeopleEvent(ctx, event); !errors.Is(err, peopleingest.ErrConflict) {
		t.Fatalf("ConsumePeopleEvent error = %v, want ErrConflict", err)
	}
	if profiles.saveCalls != 0 {
		t.Fatalf("mismatched profile was saved %d times", profiles.saveCalls)
	}
}

type fakePeopleEmploymentProfileStore struct {
	profiles  map[agentcore.AgentProfileID]agentcore.AgentProfile
	saveCalls int
}

func (store *fakePeopleEmploymentProfileStore) GetProfile(_ context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	profile, ok := store.profiles[id]
	if !ok {
		return agentcore.AgentProfile{}, agentcore.ErrNotFound
	}
	return profile, nil
}

func (store *fakePeopleEmploymentProfileStore) QueryDirectory(_ context.Context, _ agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	entries := make([]agentcore.AgentDirectoryEntry, 0, len(store.profiles))
	for id, profile := range store.profiles {
		entries = append(entries, agentcore.AgentDirectoryEntry{AgentProfileID: id, AgentKind: profile.AgentKind})
	}
	return agentcore.AgentDirectoryPage{Entries: entries, Total: len(entries)}, nil
}

func (store *fakePeopleEmploymentProfileStore) SaveProfile(_ context.Context, profile agentcore.AgentProfile) error {
	store.saveCalls++
	store.profiles[profile.ID] = profile
	return nil
}

func TestProductionPeopleEventConsumerRejectsOntologyEventIdentityCollision(t *testing.T) {
	ctx := context.Background()
	core := newProductionPeopleIngestionTestCore(t)
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatalf("new ontology projection runner: %v", err)
	}
	consumer := productionPeopleEventConsumer{events: newProductionOntologyEventLog(core), projections: runner}
	event := productionPeopleDeliveryBatchFixture().Events[0]
	if err := consumer.ConsumePeopleEvent(ctx, event); err != nil {
		t.Fatalf("first ConsumePeopleEvent: %v", err)
	}
	event.Payload = json.RawMessage(`{"id":"subject-1","type":"human","display_name":"Other","status":"active","revision":1,"valid_from":"2026-08-09T08:00:00Z","recorded_at":"2026-08-09T08:00:00Z"}`)
	if err := consumer.ConsumePeopleEvent(ctx, event); !errors.Is(err, peopleingest.ErrConflict) {
		t.Fatalf("collision error = %v, want ErrConflict", err)
	}
}

func TestProductionPeopleEventConsumerScopesSourceEventIdentityByTenant(t *testing.T) {
	ctx := context.Background()
	core := newProductionPeopleIngestionTestCore(t)
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatalf("new ontology projection runner: %v", err)
	}
	consumer := productionPeopleEventConsumer{events: newProductionOntologyEventLog(core), projections: runner}
	first := productionPeopleDeliveryBatchFixture().Events[0]
	if err := consumer.ConsumePeopleEvent(ctx, first); err != nil {
		t.Fatalf("first tenant ConsumePeopleEvent: %v", err)
	}
	second := first
	second.TenantID = "tenant-b"
	if err := consumer.ConsumePeopleEvent(ctx, second); err != nil {
		t.Fatalf("second tenant ConsumePeopleEvent: %v", err)
	}
	var ontologyEvents int
	if err := core.QueryRow(ctx, `SELECT COUNT(*) FROM platform_events WHERE event_type = ?`, "ontology.people.subject.v2.changed").Scan(&ontologyEvents); err != nil {
		t.Fatalf("count ontology events: %v", err)
	}
	if ontologyEvents != 2 {
		t.Fatalf("ontology events = %d, want 2", ontologyEvents)
	}
}

type productionPeopleProjectionCase struct {
	name          string
	eventType     string
	aggregateType string
	aggregateID   string
	payload       string
	objectType    string
	objectID      string
	factPredicate string
	factValue     any
	linkType      string
	linkFromID    string
	linkToID      string
}

func productionPeopleProjectionCases() []productionPeopleProjectionCase {
	return []productionPeopleProjectionCase{
		{
			name: "subject", eventType: "people.subject.v2.changed", aggregateType: "subject", aggregateID: "subject-1",
			payload:    `{"id":"subject-1","type":"human","display_name":"Ada","status":"active","revision":1,"valid_from":"2026-08-09T08:00:00Z","recorded_at":"2026-08-09T08:00:00Z"}`,
			objectType: "workforce_subject", objectID: "subject-1", factPredicate: "display_name", factValue: "Ada",
		},
		{
			name: "org-unit", eventType: "people.org_unit.v2.changed", aggregateType: "org_unit", aggregateID: "org-1",
			payload:    `{"id":"org-1","code":"ENG","display_name":"Engineering","status":"active","revision":1,"valid_from":"2026-08-09T08:00:00Z","recorded_at":"2026-08-09T08:00:00Z"}`,
			objectType: "organization_unit", objectID: "org-1", factPredicate: "display_name", factValue: "Engineering",
		},
		{
			name: "job-profile", eventType: "people.job_profile.v2.changed", aggregateType: "job_profile", aggregateID: "job-1",
			payload:    `{"id":"job-1","code":"SWE","display_name":"Engineer","job_family":"Engineering","job_level":"L4","status":"active","revision":1,"valid_from":"2026-08-09T08:00:00Z","recorded_at":"2026-08-09T08:00:00Z"}`,
			objectType: "job_profile", objectID: "job-1", factPredicate: "display_name", factValue: "Engineer",
		},
		{
			name: "position", eventType: "people.position.v2.changed", aggregateType: "position", aggregateID: "position-1",
			payload:    `{"id":"position-1","code":"SWE-01","display_name":"Staff Engineer","org_unit_id":"org-1","job_profile_id":"job-1","approved_headcount":1,"status":"active","revision":1,"valid_from":"2026-08-09T08:00:00Z","recorded_at":"2026-08-09T08:00:00Z"}`,
			objectType: "position", objectID: "position-1", factPredicate: "name", factValue: "Staff Engineer",
			linkType: "position_in_organization_unit", linkFromID: "position-1", linkToID: "org-1",
		},
		{
			name: "assignment", eventType: "people.assignment.v2.changed", aggregateType: "assignment", aggregateID: "assignment-1",
			payload:    `{"id":"assignment-1","subject_id":"subject-1","org_unit_id":"org-1","position_id":"position-1","assignment_kind":"primary","allocation_bps":10000,"revision":1,"valid_from":"2026-08-09T08:00:00Z"}`,
			objectType: "workforce_assignment", objectID: "assignment-1", factPredicate: "assignment_kind", factValue: "primary",
			linkType: "workforce_assignment_for_subject", linkFromID: "assignment-1", linkToID: "subject-1",
		},
		{
			name: "reporting", eventType: "people.reporting_relationship.v2.changed", aggregateType: "reporting_relationship", aggregateID: "reporting-1",
			payload:    `{"id":"reporting-1","subject_id":"subject-1","manager_id":"subject-1","reporting_type":"line","revision":1,"valid_from":"2026-08-09T08:00:00Z"}`,
			objectType: "reporting_relationship", objectID: "reporting-1", factPredicate: "reporting_type", factValue: "line",
			linkType: "reporting_relationship_to_manager", linkFromID: "reporting-1", linkToID: "subject-1",
		},
		{
			name: "responsibility", eventType: "people.responsibility.v2.changed", aggregateType: "responsibility", aggregateID: "responsibility-1",
			payload:    `{"id":"responsibility-1","code":"SHIP","title":"Ship safely","description":"Own release quality","revision":1,"valid_from":"2026-08-09T08:00:00Z"}`,
			objectType: "responsibility", objectID: "responsibility-1", factPredicate: "title", factValue: "Ship safely",
		},
		{
			name: "responsibility-binding", eventType: "people.responsibility_binding.v2.changed", aggregateType: "responsibility_binding", aggregateID: "binding-1",
			payload:    `{"id":"binding-1","responsibility_id":"responsibility-1","target_type":"position","target_id":"position-1","revision":1,"valid_from":"2026-08-09T08:00:00Z"}`,
			objectType: "responsibility_binding", objectID: "binding-1", factPredicate: "target_type", factValue: "position",
			linkType: "responsibility_binding_for_responsibility", linkFromID: "binding-1", linkToID: "responsibility-1",
		},
	}
}

type recordingPeopleBatchIngestor struct {
	ack   peopleingest.DeliveryAcknowledgement
	err   error
	calls int
	batch peopleingest.DeliveryBatch
}

func (ingestor *recordingPeopleBatchIngestor) Ingest(_ context.Context, batch peopleingest.DeliveryBatch, _ string) (peopleingest.DeliveryAcknowledgement, error) {
	ingestor.calls++
	ingestor.batch = batch
	return ingestor.ack, ingestor.err
}

func newPeopleDeliveryRequest(t *testing.T, body []byte, batchID string) *http.Request {
	t.Helper()
	digest := sha256.Sum256(body)
	request := httptest.NewRequest(http.MethodPost, peopleIngestionPath, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer 0123456789abcdef0123456789abcdef")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", batchID)
	request.Header.Set("Digest", "SHA-256="+base64.StdEncoding.EncodeToString(digest[:]))
	return request
}

func productionPeopleDeliveryBatchFixture() peopleingest.DeliveryBatch {
	return peopleingest.DeliveryBatch{
		Stream: peopleingest.EventStream, TenantID: "tenant-a", BatchID: "batch-people-1",
		FirstCursor: "cursor-1", LastCursor: "cursor-1",
		Events: []peopleingest.DeliveryEvent{{
			TenantID: "tenant-a", EventID: "event-people-1", EventType: "people.subject.v2.changed",
			SchemaVersion: 2, AggregateType: "subject", AggregateID: "subject-1", AggregateRevision: 1,
			Operation:  "created",
			Payload:    json.RawMessage(`{"id":"subject-1","type":"human","display_name":"Ada","status":"active","revision":1,"valid_from":"2026-08-09T08:00:00Z","recorded_at":"2026-08-09T08:00:00Z"}`),
			OccurredAt: time.Date(2026, 8, 9, 8, 0, 0, 0, time.UTC), CorrelationID: "correlation-1", CausationID: "change-set-1",
		}},
	}
}

func newProductionPeopleIngestionTestHandler(t *testing.T, core *db.Store) http.Handler {
	t.Helper()
	ctx := context.Background()
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatalf("new ontology projection runner: %v", err)
	}
	service, err := peopleingest.NewService(peopleingest.ServiceOptions{
		Store: peopleingest.NewStore(core),
		Consumer: productionPeopleEventConsumer{
			events: newProductionOntologyEventLog(core), projections: runner,
		},
		Now: func() time.Time { return time.Date(2026, 8, 9, 9, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("new People ingestion service: %v", err)
	}
	return &peopleIngestionHTTPHandler{
		expectedAuthorization: "Bearer 0123456789abcdef0123456789abcdef",
		ingestor:              service,
		next:                  http.NotFoundHandler(),
	}
}

func newProductionPeopleIngestionTestCore(t *testing.T) *db.Store {
	t.Helper()
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: t.Name()},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	t.Cleanup(func() { _ = databases.Close() })
	core, ok := databases.Get(db.RoleCore)
	if !ok || core == nil {
		t.Fatal("core database missing")
	}
	coordinator := migration.NewCoordinator(databases)
	if err := ontology.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register ontology migrations: %v", err)
	}
	if err := contextgraph.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register context graph migrations: %v", err)
	}
	if err := peopleingest.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register People ingestion migrations: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("migrate core database: %v", err)
	}
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, ontology.NewPlatformRegistry(core)); err != nil {
		t.Fatalf("install default ontology: %v", err)
	}
	return core
}
