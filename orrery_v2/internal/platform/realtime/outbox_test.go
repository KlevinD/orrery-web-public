package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
)

func TestOutboxAppendAssignsMonotonicChannelVersion(t *testing.T) {
	ctx := context.Background()
	store := newRealtimeTestStore(t, "append-version")
	now := time.Date(2026, 6, 10, 14, 0, 0, 0, time.UTC)
	outbox, err := NewStore(ctx, store, StoreOptions{
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}
	channel, err := ConversationChannel("conv-1")
	if err != nil {
		t.Fatalf("ConversationChannel returned error: %v", err)
	}

	first, err := outbox.Append(ctx, AppendEvent{
		EventID:       "evt-1",
		Type:          EventDiscussionMessageCreated,
		Channel:       channel,
		AggregateType: AggregateConversation,
		AggregateID:   "conv-1",
		Payload:       json.RawMessage(`{"message_id":"msg-1"}`),
	})
	if err != nil {
		t.Fatalf("Append first returned error: %v", err)
	}
	second, err := outbox.Append(ctx, AppendEvent{
		EventID:       "evt-2",
		Type:          EventDiscussionMessageCompleted,
		Channel:       channel,
		AggregateType: AggregateConversation,
		AggregateID:   "conv-1",
		Payload:       json.RawMessage(`{"message_id":"msg-2"}`),
	})
	if err != nil {
		t.Fatalf("Append second returned error: %v", err)
	}

	if first.Version != 1 || second.Version != 2 {
		t.Fatalf("versions = %d,%d want 1,2", first.Version, second.Version)
	}
	if !first.OccurredAt.Equal(now) || !second.OccurredAt.Equal(now) {
		t.Fatalf("occurred_at not set from clock: first=%s second=%s", first.OccurredAt, second.OccurredAt)
	}
}

func TestOutboxClaimPublishAndMarkPublished(t *testing.T) {
	ctx := context.Background()
	store := newRealtimeTestStore(t, "claim-publish")
	now := time.Date(2026, 6, 10, 14, 15, 0, 0, time.UTC)
	outbox, err := NewStore(ctx, store, StoreOptions{
		Now:      func() time.Time { return now },
		LeaseTTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}
	channel, _ := ConversationChannel("conv-1")
	if _, err := outbox.Append(ctx, AppendEvent{
		EventID:       "evt-claim",
		Type:          EventDiscussionRunStarted,
		Channel:       channel,
		AggregateType: AggregateConversation,
		AggregateID:   "conv-1",
		Payload:       json.RawMessage(`{"run_id":"run-1"}`),
	}); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}

	claimed, err := outbox.Claim(ctx, "worker-1", 10)
	if err != nil {
		t.Fatalf("Claim returned error: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed = %d want 1", len(claimed))
	}
	if claimed[0].Status != StatusPublishing || claimed[0].WorkerID != "worker-1" {
		t.Fatalf("claimed event = %+v", claimed[0])
	}
	if claimed[0].Envelope.EventID != "evt-claim" || claimed[0].Envelope.Version != 1 {
		t.Fatalf("claimed envelope = %+v", claimed[0].Envelope)
	}
	if err := outbox.MarkPublished(ctx, claimed[0].ID, "worker-1"); err != nil {
		t.Fatalf("MarkPublished returned error: %v", err)
	}

	again, err := outbox.Claim(ctx, "worker-2", 10)
	if err != nil {
		t.Fatalf("Claim again returned error: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("published event was claimed again: %+v", again)
	}
}

func TestOutboxRequeuesExpiredPublishingLease(t *testing.T) {
	ctx := context.Background()
	store := newRealtimeTestStore(t, "expired-lease")
	now := time.Date(2026, 6, 10, 14, 30, 0, 0, time.UTC)
	outbox, err := NewStore(ctx, store, StoreOptions{
		Now:      func() time.Time { return now },
		LeaseTTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}
	channel, _ := ConversationChannel("conv-1")
	if _, err := outbox.Append(ctx, AppendEvent{
		EventID:       "evt-expired",
		Type:          EventDiscussionToolCall,
		Channel:       channel,
		AggregateType: AggregateConversation,
		AggregateID:   "conv-1",
		Payload:       json.RawMessage(`{"tool_call_id":"tool-1"}`),
	}); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}
	claimed, err := outbox.Claim(ctx, "worker-1", 1)
	if err != nil {
		t.Fatalf("Claim returned error: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed = %d want 1", len(claimed))
	}

	outbox.now = func() time.Time { return now.Add(2 * time.Minute) }
	recovered, err := outbox.RecoverExpired(ctx, 10)
	if err != nil {
		t.Fatalf("RecoverExpired returned error: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered = %d want 1", recovered)
	}
	reclaimed, err := outbox.Claim(ctx, "worker-2", 1)
	if err != nil {
		t.Fatalf("Claim after recover returned error: %v", err)
	}
	if len(reclaimed) != 1 || reclaimed[0].ID != claimed[0].ID || reclaimed[0].WorkerID != "worker-2" {
		t.Fatalf("reclaimed = %+v want same event for worker-2", reclaimed)
	}
}

func TestOutboxClaimSkipsRowsLostToContention(t *testing.T) {
	ctx := context.Background()
	store := newRealtimeTestStore(t, "claim-contention")
	now := time.Date(2026, 6, 10, 14, 45, 0, 0, time.UTC)
	outbox, err := NewStore(ctx, store, StoreOptions{
		Now:      func() time.Time { return now },
		LeaseTTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}
	channel, _ := ConversationChannel("conv-1")
	for _, id := range []string{"evt-lost", "evt-won"} {
		if _, err := outbox.Append(ctx, AppendEvent{
			EventID:       id,
			Type:          EventDiscussionMessageCreated,
			Channel:       channel,
			AggregateType: AggregateConversation,
			AggregateID:   "conv-1",
			Payload:       json.RawMessage(`{"message_id":"msg"}`),
		}); err != nil {
			t.Fatalf("Append %s returned error: %v", id, err)
		}
	}
	if _, err := store.Exec(ctx, `
CREATE TRIGGER realtime_outbox_claim_contention
BEFORE UPDATE OF status ON realtime_outbox
WHEN OLD.id = 'evt-lost' AND OLD.status = 'pending' AND NEW.status = 'publishing'
BEGIN
	UPDATE realtime_outbox
	SET status = 'publishing', worker_id = 'worker-other', locked_until = 42, updated_at = 42
	WHERE id = OLD.id;
	SELECT RAISE(IGNORE);
END`); err != nil {
		t.Fatalf("create contention trigger: %v", err)
	}

	claimed, err := outbox.Claim(ctx, "worker-1", 10)
	if err != nil {
		t.Fatalf("Claim returned error: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != "evt-won" || claimed[0].WorkerID != "worker-1" {
		t.Fatalf("claimed = %+v, want only evt-won for worker-1", claimed)
	}
	lost, ok, err := outbox.Get(ctx, "evt-lost")
	if err != nil || !ok {
		t.Fatalf("Get lost ok=%v err=%v", ok, err)
	}
	if lost.WorkerID != "worker-other" || lost.Status != StatusPublishing {
		t.Fatalf("lost claim row = %+v, want worker-other publishing", lost)
	}
}

func TestOutboxAppendRetriesChannelVersionConflict(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "realtime-outbox.db")
	firstDB, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + path})
	if err != nil {
		t.Fatalf("open first db: %v", err)
	}
	defer firstDB.Close()
	secondDB, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + path})
	if err != nil {
		t.Fatalf("open second db: %v", err)
	}
	defer secondDB.Close()
	now := time.Date(2026, 6, 10, 15, 0, 0, 0, time.UTC)
	first, err := NewStore(ctx, firstDB, StoreOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewStore first returned error: %v", err)
	}
	second, err := NewStore(ctx, secondDB, StoreOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewStore second returned error: %v", err)
	}
	channel, _ := ConversationChannel("conv-1")
	start := make(chan struct{})
	done := make(chan error, 2)
	for i, outbox := range []*Store{first, second} {
		i, outbox := i, outbox
		go func() {
			<-start
			_, err := outbox.Append(ctx, AppendEvent{
				EventID:       fmt.Sprintf("evt-concurrent-%d", i),
				Type:          EventDiscussionMessageCreated,
				Channel:       channel,
				AggregateType: AggregateConversation,
				AggregateID:   "conv-1",
				Payload:       json.RawMessage(`{"message_id":"msg"}`),
			})
			done <- err
		}()
	}
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatalf("Append returned error: %v", err)
		}
	}

	var versionSum int64
	if err := firstDB.QueryRow(ctx, `SELECT SUM(version) FROM realtime_outbox WHERE channel = ?`, channel).Scan(&versionSum); err != nil {
		t.Fatalf("query version sum: %v", err)
	}
	if versionSum != 3 {
		t.Fatalf("version sum = %d, want versions 1 and 2", versionSum)
	}
}

func TestOutboxAppendDuplicateEventIDReturnsExistingEnvelope(t *testing.T) {
	ctx := context.Background()
	store := newRealtimeTestStore(t, "duplicate-event-id")
	now := time.Date(2026, 6, 10, 15, 15, 0, 0, time.UTC)
	outbox, err := NewStore(ctx, store, StoreOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}
	channel, _ := ConversationChannel("conv-1")
	event := AppendEvent{
		EventID:       "evt-duplicate",
		Type:          EventDiscussionMessageCreated,
		Channel:       channel,
		AggregateType: AggregateConversation,
		AggregateID:   "conv-1",
		Payload:       json.RawMessage(`{"message_id":"first"}`),
	}
	first, err := outbox.Append(ctx, event)
	if err != nil {
		t.Fatalf("first Append returned error: %v", err)
	}
	event.Payload = json.RawMessage(`{"message_id":"second"}`)
	second, err := outbox.Append(ctx, event)
	if err != nil {
		t.Fatalf("duplicate Append returned error: %v", err)
	}
	if first.EventID != second.EventID || first.Version != second.Version || string(first.Payload) != string(second.Payload) {
		t.Fatalf("duplicate returned %+v, want existing %+v", second, first)
	}
	var count int
	if err := store.QueryRow(ctx, `SELECT COUNT(*) FROM realtime_outbox WHERE id = ?`, "evt-duplicate").Scan(&count); err != nil {
		t.Fatalf("count duplicate event: %v", err)
	}
	if count != 1 {
		t.Fatalf("duplicate row count = %d, want 1", count)
	}
}

func TestOutboxAppendRejectsInvalidJSONPayload(t *testing.T) {
	ctx := context.Background()
	store := newRealtimeTestStore(t, "invalid-json")
	outbox, err := NewStore(ctx, store, StoreOptions{})
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}
	channel, _ := ConversationChannel("conv-1")

	_, err = outbox.Append(ctx, AppendEvent{
		EventID:       "evt-invalid-json",
		Type:          EventDiscussionMessageCreated,
		Channel:       channel,
		AggregateType: AggregateConversation,
		AggregateID:   "conv-1",
		Payload:       json.RawMessage(`{"message_id":`),
	})
	if err == nil || !strings.Contains(err.Error(), "valid JSON") {
		t.Fatalf("Append err = %v, want invalid JSON error", err)
	}
}

func TestOutboxMarkFailedRetriesThenFailsAfterMaxAttempts(t *testing.T) {
	ctx := context.Background()
	store := newRealtimeTestStore(t, "mark-failed")
	now := time.Date(2026, 6, 10, 15, 30, 0, 0, time.UTC)
	outbox, err := NewStore(ctx, store, StoreOptions{
		Now:         func() time.Time { return now },
		MaxAttempts: 2,
	})
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}
	channel, _ := ConversationChannel("conv-1")
	if _, err := outbox.Append(ctx, AppendEvent{
		EventID:       "evt-failed",
		Type:          EventDiscussionRunFailed,
		Channel:       channel,
		AggregateType: AggregateConversation,
		AggregateID:   "conv-1",
		Payload:       json.RawMessage(`{"run_id":"run-1"}`),
	}); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}

	claimed, err := outbox.Claim(ctx, "worker-1", 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("Claim len=%d err=%v", len(claimed), err)
	}
	if err := outbox.MarkFailed(ctx, claimed[0].ID, "worker-1", "temporary"); err != nil {
		t.Fatalf("first MarkFailed returned error: %v", err)
	}
	failed, ok, err := outbox.Get(ctx, "evt-failed")
	if err != nil || !ok {
		t.Fatalf("Get after first failure ok=%v err=%v", ok, err)
	}
	if failed.Status != StatusPending || failed.Attempts != 1 || failed.LastError != "temporary" || !failed.LeaseUntil.IsZero() {
		t.Fatalf("after first failure = %+v", failed)
	}

	outbox.now = func() time.Time { return now.Add(2 * time.Second) }
	claimed, err = outbox.Claim(ctx, "worker-2", 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("second Claim len=%d err=%v", len(claimed), err)
	}
	if err := outbox.MarkFailed(ctx, claimed[0].ID, "worker-2", "permanent"); err != nil {
		t.Fatalf("second MarkFailed returned error: %v", err)
	}
	dead, ok, err := outbox.Get(ctx, "evt-failed")
	if err != nil || !ok {
		t.Fatalf("Get after second failure ok=%v err=%v", ok, err)
	}
	if dead.Status != StatusFailed || dead.Attempts != 2 || dead.LastError != "permanent" {
		t.Fatalf("after second failure = %+v", dead)
	}
}

func TestOutboxOwnerGuardsRejectWrongWorker(t *testing.T) {
	ctx := context.Background()
	store := newRealtimeTestStore(t, "owner-guards")
	outbox, err := NewStore(ctx, store, StoreOptions{})
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}
	channel, _ := ConversationChannel("conv-1")
	if _, err := outbox.Append(ctx, AppendEvent{
		EventID:       "evt-owner",
		Type:          EventDiscussionRunCompleted,
		Channel:       channel,
		AggregateType: AggregateConversation,
		AggregateID:   "conv-1",
		Payload:       json.RawMessage(`{"run_id":"run-1"}`),
	}); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}
	claimed, err := outbox.Claim(ctx, "worker-1", 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("Claim len=%d err=%v", len(claimed), err)
	}

	if err := outbox.MarkPublished(ctx, claimed[0].ID, "worker-2"); err == nil {
		t.Fatalf("wrong-worker MarkPublished returned nil")
	}
	if err := outbox.MarkFailed(ctx, claimed[0].ID, "worker-2", "wrong worker"); err == nil {
		t.Fatalf("wrong-worker MarkFailed returned nil")
	}
	if err := outbox.MarkPublished(ctx, claimed[0].ID, "worker-1"); err != nil {
		t.Fatalf("owner MarkPublished returned error: %v", err)
	}
}

func TestChannelHelpersRejectEmptyIDs(t *testing.T) {
	cases := []struct {
		name string
		fn   func(string) (string, error)
	}{
		{name: "conversation", fn: ConversationChannel},
		{name: "mission", fn: MissionChannel},
		{name: "user", fn: UserChannel},
		{name: "run", fn: RunChannel},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := tc.fn("  "); err == nil || got != "" {
				t.Fatalf("channel = %q err=%v, want empty channel and error", got, err)
			}
			if got, err := tc.fn("bad:id"); err == nil || got != "" {
				t.Fatalf("colon channel = %q err=%v, want empty channel and error", got, err)
			}
		})
	}
}

func TestConcurrentOutboxClaimOnlyOneWorkerGetsPendingEvent(t *testing.T) {
	ctx := context.Background()
	store := newRealtimeTestStore(t, "concurrent-claim")
	now := time.Date(2026, 6, 10, 15, 45, 0, 0, time.UTC)
	outbox, err := NewStore(ctx, store, StoreOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}
	channel, _ := ConversationChannel("conv-1")
	if _, err := outbox.Append(ctx, AppendEvent{
		EventID:       "evt-concurrent-claim",
		Type:          EventDiscussionMessageCompleted,
		Channel:       channel,
		AggregateType: AggregateConversation,
		AggregateID:   "conv-1",
		Payload:       json.RawMessage(`{"message_id":"msg-1"}`),
	}); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}

	const workers = 100
	var ready sync.WaitGroup
	ready.Add(workers)
	start := make(chan struct{})
	claimedCh := make(chan int, workers)
	errCh := make(chan error, workers)
	for i := 0; i < workers; i++ {
		i := i
		go func() {
			ready.Done()
			<-start
			claimed, err := outbox.Claim(ctx, fmt.Sprintf("worker-%03d", i), 1)
			if err != nil {
				errCh <- err
				claimedCh <- 0
				return
			}
			errCh <- nil
			claimedCh <- len(claimed)
		}()
	}
	ready.Wait()
	close(start)

	claimedCount := 0
	for i := 0; i < workers; i++ {
		if err := <-errCh; err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Claim returned error: %v", err)
		}
		claimedCount += <-claimedCh
	}
	if claimedCount != 1 {
		t.Fatalf("claimed events = %d, want 1", claimedCount)
	}
}

func newRealtimeTestStore(t *testing.T, name string) *db.Store {
	t.Helper()
	store, err := db.Open(context.Background(), db.Config{Driver: db.DriverMemory, Name: "realtime-" + name})
	if err != nil {
		t.Fatalf("db.Open returned error: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("store.Close returned error: %v", err)
		}
	})
	return store
}
