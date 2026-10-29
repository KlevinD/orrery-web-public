package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
)

func TestDispatcherPublishesClaimedOutboxEvents(t *testing.T) {
	ctx := context.Background()
	store, outbox := newRealtimeDispatcherStore(t, "publish-success")
	channel, _ := ConversationChannel("conv-1")
	if _, err := outbox.Append(ctx, AppendEvent{
		EventID:       "evt-dispatch",
		Type:          EventDiscussionMessageCreated,
		Channel:       channel,
		AggregateType: AggregateConversation,
		AggregateID:   "conv-1",
		Payload:       json.RawMessage(`{"message_id":"msg-1"}`),
	}); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}
	target := &capturingPublishTarget{}
	dispatcher, err := NewDispatcher(DispatcherOptions{
		Store:    outbox,
		Target:   target,
		WorkerID: "dispatcher-1",
	})
	if err != nil {
		t.Fatalf("NewDispatcher returned error: %v", err)
	}

	result, err := dispatcher.DispatchOnce(ctx)
	if err != nil {
		t.Fatalf("DispatchOnce returned error: %v", err)
	}
	if result.Claimed != 1 || result.Published != 1 || result.Failed != 0 {
		t.Fatalf("result = %+v, want claimed=1 published=1 failed=0", result)
	}
	if len(target.envelopes) != 1 || target.envelopes[0].EventID != "evt-dispatch" {
		t.Fatalf("published envelopes = %+v", target.envelopes)
	}
	stored, ok, err := outbox.Get(ctx, "evt-dispatch")
	if err != nil || !ok {
		t.Fatalf("Get ok=%v err=%v", ok, err)
	}
	if stored.Status != StatusPublished || stored.WorkerID != "" || stored.PublishedAt.IsZero() {
		t.Fatalf("stored event after dispatch = %+v", stored)
	}

	var published int
	if err := store.QueryRow(ctx, `SELECT COUNT(*) FROM realtime_outbox WHERE status = ?`, string(StatusPublished)).Scan(&published); err != nil {
		t.Fatalf("count published: %v", err)
	}
	if published != 1 {
		t.Fatalf("published rows = %d, want 1", published)
	}
}

func TestDispatcherMarksFailedPublishForRetry(t *testing.T) {
	ctx := context.Background()
	_, outbox := newRealtimeDispatcherStore(t, "publish-failure")
	channel, _ := ConversationChannel("conv-1")
	if _, err := outbox.Append(ctx, AppendEvent{
		EventID:       "evt-retry",
		Type:          EventDiscussionRunFailed,
		Channel:       channel,
		AggregateType: AggregateConversation,
		AggregateID:   "conv-1",
		Payload:       json.RawMessage(`{"run_id":"run-1"}`),
	}); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}
	target := &capturingPublishTarget{err: fmt.Errorf("centrifuge unavailable")}
	dispatcher, err := NewDispatcher(DispatcherOptions{
		Store:    outbox,
		Target:   target,
		WorkerID: "dispatcher-1",
	})
	if err != nil {
		t.Fatalf("NewDispatcher returned error: %v", err)
	}

	result, err := dispatcher.DispatchOnce(ctx)
	if err != nil {
		t.Fatalf("DispatchOnce returned error: %v", err)
	}
	if result.Claimed != 1 || result.Published != 0 || result.Failed != 1 {
		t.Fatalf("result = %+v, want claimed=1 published=0 failed=1", result)
	}
	stored, ok, err := outbox.Get(ctx, "evt-retry")
	if err != nil || !ok {
		t.Fatalf("Get ok=%v err=%v", ok, err)
	}
	if stored.Status != StatusPending || stored.Attempts != 1 || stored.LastError != "centrifuge unavailable" {
		t.Fatalf("stored event after failed dispatch = %+v", stored)
	}
	if stored.WorkerID != "" || !stored.LeaseUntil.IsZero() {
		t.Fatalf("failed event retained claim fields: %+v", stored)
	}
}

func TestDispatcherRecoversExpiredPublishingLeaseBeforeClaim(t *testing.T) {
	ctx := context.Background()
	store := newRealtimeTestStore(t, "dispatcher-recover-expired")
	now := time.Date(2026, 6, 10, 16, 0, 0, 0, time.UTC)
	outbox, err := NewStore(ctx, store, StoreOptions{
		Now:         func() time.Time { return now },
		LeaseTTL:    time.Minute,
		MaxAttempts: 3,
	})
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}
	channel, _ := ConversationChannel("conv-1")
	if _, err := outbox.Append(ctx, AppendEvent{
		EventID:       "evt-expired-publishing",
		Type:          EventDiscussionRunCompleted,
		Channel:       channel,
		AggregateType: AggregateConversation,
		AggregateID:   "conv-1",
		Payload:       json.RawMessage(`{"run_id":"run-1"}`),
	}); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}
	claimed, err := outbox.Claim(ctx, "stale-dispatcher", 1)
	if err != nil {
		t.Fatalf("Claim returned error: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed = %d, want 1", len(claimed))
	}
	now = now.Add(2 * time.Minute)
	target := &capturingPublishTarget{}
	dispatcher, err := NewDispatcher(DispatcherOptions{
		Store:    outbox,
		Target:   target,
		WorkerID: "dispatcher-1",
	})
	if err != nil {
		t.Fatalf("NewDispatcher returned error: %v", err)
	}

	result, err := dispatcher.DispatchOnce(ctx)
	if err != nil {
		t.Fatalf("DispatchOnce returned error: %v", err)
	}
	if result.Claimed != 1 || result.Published != 1 {
		t.Fatalf("result = %+v, want recovered expired event to be claimed and published", result)
	}
	if len(target.envelopes) != 1 || target.envelopes[0].EventID != "evt-expired-publishing" {
		t.Fatalf("published envelopes = %+v", target.envelopes)
	}
}

func newRealtimeDispatcherStore(t *testing.T, name string) (*db.Store, *Store) {
	t.Helper()
	ctx := context.Background()
	store := newRealtimeTestStore(t, "dispatcher-"+name)
	now := time.Date(2026, 6, 10, 16, 0, 0, 0, time.UTC)
	outbox, err := NewStore(ctx, store, StoreOptions{
		Now:         func() time.Time { return now },
		LeaseTTL:    time.Minute,
		MaxAttempts: 3,
	})
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}
	return store, outbox
}

type capturingPublishTarget struct {
	envelopes []Envelope
	err       error
}

func (t *capturingPublishTarget) Publish(_ context.Context, envelope Envelope) error {
	if t.err != nil {
		return t.err
	}
	t.envelopes = append(t.envelopes, envelope)
	return nil
}
