package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/platform/db"
	platformrealtime "orrery_v2/internal/platform/realtime"
)

func TestProductionDiscussionRealtimePublisherAppendsOutboxEvent(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "discussion-realtime-publisher"})
	if err != nil {
		t.Fatalf("db.Open returned error: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("store.Close returned error: %v", err)
		}
	})
	outbox, err := platformrealtime.NewStore(ctx, store, platformrealtime.StoreOptions{
		Now: func() time.Time { return time.Date(2026, 6, 10, 16, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}
	publisher := productionDiscussionRealtimePublisher{store: outbox}

	err = publisher.AppendRealtimeEvent(ctx, discussionsession.RealtimeEvent{
		EventID:       "discussion-message-created-msg-1",
		Type:          discussionsession.RealtimeEventDiscussionMessageCreated,
		Channel:       "conversation:conv-1",
		AggregateType: discussionsession.RealtimeAggregateConversation,
		AggregateID:   "conv-1",
		Payload:       json.RawMessage(`{"message_id":"msg-1"}`),
	})
	if err != nil {
		t.Fatalf("AppendRealtimeEvent returned error: %v", err)
	}

	claimed, err := outbox.Claim(ctx, "test-worker", 1)
	if err != nil {
		t.Fatalf("Claim returned error: %v", err)
	}
	if len(claimed) != 1 || claimed[0].Envelope.EventID != "discussion-message-created-msg-1" || claimed[0].Envelope.Version != 1 {
		t.Fatalf("claimed outbox events = %+v", claimed)
	}
}

func TestProductionDiscussionRealtimePublisherRejectsUnsupportedChannel(t *testing.T) {
	publisher := productionDiscussionRealtimePublisher{}

	err := publisher.AppendRealtimeEvent(context.Background(), discussionsession.RealtimeEvent{
		EventID:       "mission-event",
		Type:          "mission.snapshot.invalidated",
		Channel:       "mission:mission-1",
		AggregateType: "mission",
		AggregateID:   "mission-1",
		Payload:       json.RawMessage(`{}`),
	})
	if err == nil || !strings.Contains(err.Error(), "outbox store") {
		t.Fatalf("AppendRealtimeEvent err = %v, want missing store guard before channel append", err)
	}

	store, err := db.Open(context.Background(), db.Config{Driver: db.DriverMemory, Name: "discussion-realtime-reject"})
	if err != nil {
		t.Fatalf("db.Open returned error: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("store.Close returned error: %v", err)
		}
	})
	outbox, err := platformrealtime.NewStore(context.Background(), store, platformrealtime.StoreOptions{})
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}
	publisher = productionDiscussionRealtimePublisher{store: outbox}
	err = publisher.AppendRealtimeEvent(context.Background(), discussionsession.RealtimeEvent{
		EventID:       "mission-event",
		Type:          "mission.snapshot.invalidated",
		Channel:       "mission:mission-1",
		AggregateType: "mission",
		AggregateID:   "mission-1",
		Payload:       json.RawMessage(`{}`),
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported discussion realtime channel") {
		t.Fatalf("AppendRealtimeEvent err = %v, want unsupported channel", err)
	}
}
