package main

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
)

func TestProductionOntologyEventLogRecordsDomainEventsForReplay(t *testing.T) {
	ctx := context.Background()
	core, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "production-ontology-event-log"})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer core.Close()
	log := newProductionOntologyEventLog(core)

	event := graphprotocol.DomainEvent{
		ID:            "project-created:project-1",
		TenantID:      "project:project-1",
		Type:          "project.created",
		SchemaVersion: "v1",
		StreamID:      "project-1",
		Version:       1,
		OccurredAt:    time.Unix(1, 0).UTC(),
		Payload: map[string]any{
			"id":     "project-1",
			"title":  "Ontology event log",
			"status": "active",
		},
		PublishedBy:     graphprotocol.ActorRef{Kind: "user", ID: "planner-1"},
		SourceAccessRef: graphprotocol.AccessRef{Kind: "project", ID: "project-1", TenantID: "org-1"},
	}
	if err := log.Record(ctx, event); err != nil {
		t.Fatalf("Record: %v", err)
	}

	events, err := log.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %+v", events)
	}
	got := events[0]
	if got.ID != event.ID || got.TenantID != event.TenantID || got.Type != event.Type || got.SchemaVersion != event.SchemaVersion || got.StreamID != event.StreamID || got.Version != event.Version {
		t.Fatalf("event identity = %+v, want %+v", got, event)
	}
	if got.Payload["title"] != "Ontology event log" || got.PublishedBy.ID != "planner-1" || got.SourceAccessRef != event.SourceAccessRef {
		t.Fatalf("event payload/actor = %+v", got)
	}
}

func TestProductionKnowledgeReplayRequiresLoggedEventSourceRefMatch(t *testing.T) {
	ctx := context.Background()
	core, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "production-ontology-event-source-ref"})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer core.Close()
	log := newProductionOntologyEventLog(core)
	event := graphprotocol.DomainEvent{
		ID: "knowledge-status:event-1", TenantID: "tenant-1", Type: "knowledge.document.status.updated",
		StreamID: "document-1", Version: 1, OccurredAt: time.Unix(1, 0).UTC(), Payload: map[string]any{"id": "document-1"},
		SourceAccessRef: graphprotocol.AccessRef{Kind: "knowledge_document", ID: "document-1", TenantID: "tenant-1"},
	}
	if err := log.Record(ctx, event); err != nil {
		t.Fatalf("Record: %v", err)
	}
	sink := productionDocumentOntologySink{events: log}
	audit := contextgraph.ProjectionAudit{
		TenantID: event.TenantID, SourceEventID: event.ID, SourceEventType: event.Type,
		SourceAccessRef: contextgraph.AccessRef{Kind: event.SourceAccessRef.Kind, ID: event.SourceAccessRef.ID, TenantID: event.SourceAccessRef.TenantID},
	}
	if got, found, err := sink.knowledgeEventFromProjectionAudit(ctx, audit); err != nil || !found || got.ID != event.ID {
		t.Fatalf("matching event = %+v found=%t err=%v", got, found, err)
	}
	audit.SourceAccessRef.ID = "document-2"
	if _, found, err := sink.knowledgeEventFromProjectionAudit(ctx, audit); err != nil || found {
		t.Fatalf("mismatched source ref found=%t err=%v", found, err)
	}
}

func TestProductionOntologyEventLogReplaysContextGraphProjection(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "production-ontology-event-log-replay")
	log := newProductionOntologyEventLog(core)
	for _, event := range []graphprotocol.DomainEvent{
		{
			ID:         "project-created:project-1",
			TenantID:   "project:project-1",
			Type:       "project.created",
			StreamID:   "project-1",
			Version:    1,
			OccurredAt: time.Unix(1, 0).UTC(),
			Payload: map[string]any{
				"id":     "project-1",
				"title":  "Replay source",
				"status": "active",
			},
			PublishedBy: graphprotocol.ActorRef{Kind: "user", ID: "planner-1"},
		},
		{
			ID:         "project-updated:project-1:rev-2",
			TenantID:   "project:project-1",
			Type:       "project.updated",
			StreamID:   "project-1",
			Version:    2,
			OccurredAt: time.Unix(2, 0).UTC(),
			Payload: map[string]any{
				"id":    "project-1",
				"title": "Replay updated",
			},
			PublishedBy: graphprotocol.ActorRef{Kind: "user", ID: "planner-1"},
		},
	} {
		if err := log.Record(ctx, event); err != nil {
			t.Fatalf("Record %s: %v", event.ID, err)
		}
	}
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatalf("new projection runner: %v", err)
	}

	events, err := log.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, event := range events {
		if _, err := runner.ApplyEvent(ctx, event); err != nil {
			t.Fatalf("ApplyEvent(%s): %v", event.ID, err)
		}
	}
	objects, _, facts, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "project:project-1")
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	assertContextGraphObject(t, objects, "project", "project-1")
	assertContextGraphFact(t, facts, "project", "project-1", "title", "Replay updated")
	assertContextGraphEvidence(t, evidence, "project-created:project-1")
	assertContextGraphEvidence(t, evidence, "project-updated:project-1:rev-2")
}
