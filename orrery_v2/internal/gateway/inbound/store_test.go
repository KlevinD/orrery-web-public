package inbound

import (
	"context"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/gateway/channel"
	platformdb "orrery_v2/internal/platform/db"
)

func newInboundTestStore(t *testing.T) *Store {
	t.Helper()
	database, err := platformdb.Open(context.Background(), platformdb.Config{Driver: platformdb.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store := NewStore(database)
	store.now = func() time.Time { return time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC) }
	return store
}

func TestInboundJobQueueDeduplicatesAndCompletes(t *testing.T) {
	ctx := context.Background()
	store := newInboundTestStore(t)
	job := channel.InboundJob{ChannelKey: "feishu", AccountID: "feishu:app", EventID: "event-1", Payload: []byte(`{"text":"hello"}`)}

	inserted, err := store.EnqueueInboundJob(ctx, job)
	if err != nil || !inserted {
		t.Fatalf("enqueue first job: inserted=%v err=%v", inserted, err)
	}
	inserted, err = store.EnqueueInboundJob(ctx, job)
	if err != nil || inserted {
		t.Fatalf("enqueue duplicate job: inserted=%v err=%v", inserted, err)
	}

	claimed, found, err := store.ClaimInboundJob(ctx, "feishu", "feishu:app")
	if err != nil || !found || claimed.ClaimToken == "" || claimed.Attempts != 1 || string(claimed.Payload) != string(job.Payload) {
		t.Fatalf("claim = %+v found=%v err=%v", claimed, found, err)
	}
	store.now = func() time.Time { return time.Date(2026, 7, 16, 0, 4, 0, 0, time.UTC) }
	if err := store.RenewInboundJob(ctx, claimed); err != nil {
		t.Fatalf("renew job: %v", err)
	}
	store.now = func() time.Time { return time.Date(2026, 7, 16, 0, 6, 0, 0, time.UTC) }
	if _, found, err := store.ClaimInboundJob(ctx, "feishu", "feishu:app"); err != nil || found {
		t.Fatalf("renewed job was reclaimed: found=%v err=%v", found, err)
	}
	if err := store.CompleteInboundJob(ctx, claimed); err != nil {
		t.Fatalf("complete job: %v", err)
	}
	if _, found, err := store.ClaimInboundJob(ctx, "feishu", "feishu:app"); err != nil || found {
		t.Fatalf("completed job was reclaimed: found=%v err=%v", found, err)
	}
}

func TestListRecentInboundJobPayloadsIsAccountScopedAndNewestFirst(t *testing.T) {
	ctx := context.Background()
	store := newInboundTestStore(t)
	base := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return base }
	if inserted, err := store.EnqueueInboundJob(ctx, channel.InboundJob{
		ChannelKey: "weixin", AccountID: "bot-a", EventID: "older", Payload: []byte(`{"context":"older"}`),
	}); err != nil || !inserted {
		t.Fatalf("enqueue older payload: inserted=%v err=%v", inserted, err)
	}
	store.now = func() time.Time { return base.Add(time.Second) }
	if inserted, err := store.EnqueueInboundJob(ctx, channel.InboundJob{
		ChannelKey: "weixin", AccountID: "bot-a", EventID: "newer", Payload: []byte(`{"context":"newer"}`),
	}); err != nil || !inserted {
		t.Fatalf("enqueue newer payload: inserted=%v err=%v", inserted, err)
	}
	if inserted, err := store.EnqueueInboundJob(ctx, channel.InboundJob{
		ChannelKey: "weixin", AccountID: "bot-b", EventID: "private", Payload: []byte(`{"context":"other-account"}`),
	}); err != nil || !inserted {
		t.Fatalf("enqueue other account payload: inserted=%v err=%v", inserted, err)
	}
	payloads, err := store.ListRecentInboundJobPayloads(ctx, "weixin", "bot-a", 10)
	if err != nil {
		t.Fatalf("ListRecentInboundJobPayloads: %v", err)
	}
	if len(payloads) != 2 || string(payloads[0]) != `{"context":"newer"}` || string(payloads[1]) != `{"context":"older"}` {
		t.Fatalf("payloads = %q", payloads)
	}
}

func TestInboundJobQueueRetriesWithClaimFencing(t *testing.T) {
	ctx := context.Background()
	store := newInboundTestStore(t)
	base := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	job := channel.InboundJob{ChannelKey: "dingtalk", AccountID: "dingtalk:app", EventID: "message-1", Payload: []byte(`{"text":"hello"}`)}
	if inserted, err := store.EnqueueInboundJob(ctx, job); err != nil || !inserted {
		t.Fatalf("enqueue: inserted=%v err=%v", inserted, err)
	}
	first, found, err := store.ClaimInboundJob(ctx, job.ChannelKey, job.AccountID)
	if err != nil || !found {
		t.Fatalf("claim first: found=%v err=%v", found, err)
	}
	if err := store.FailInboundJob(ctx, first, strings.Repeat("failure ", 100), base.Add(time.Minute)); err != nil {
		t.Fatalf("fail first attempt: %v", err)
	}
	store.now = func() time.Time { return base.Add(30 * time.Second) }
	if _, found, err := store.ClaimInboundJob(ctx, job.ChannelKey, job.AccountID); err != nil || found {
		t.Fatalf("job retried before schedule: found=%v err=%v", found, err)
	}
	store.now = func() time.Time { return base.Add(time.Minute) }
	second, found, err := store.ClaimInboundJob(ctx, job.ChannelKey, job.AccountID)
	if err != nil || !found || second.Attempts != 2 || second.ClaimToken == first.ClaimToken {
		t.Fatalf("claim retry = %+v found=%v err=%v", second, found, err)
	}
	if err := store.CompleteInboundJob(ctx, first); err == nil {
		t.Fatal("stale claim completed a newer attempt")
	}
	if err := store.CompleteInboundJob(ctx, second); err != nil {
		t.Fatalf("complete retry: %v", err)
	}
}

func TestInboundJobQueueReclaimsExpiredLeaseWithFencing(t *testing.T) {
	ctx := context.Background()
	store := newInboundTestStore(t)
	job := channel.InboundJob{ChannelKey: "feishu", AccountID: "feishu:app", EventID: "stale-event", Payload: []byte(`{"text":"hello"}`)}
	if inserted, err := store.EnqueueInboundJob(ctx, job); err != nil || !inserted {
		t.Fatalf("enqueue: inserted=%v err=%v", inserted, err)
	}
	first, found, err := store.ClaimInboundJob(ctx, job.ChannelKey, job.AccountID)
	if err != nil || !found {
		t.Fatalf("claim first: found=%v err=%v", found, err)
	}
	store.now = func() time.Time { return time.Date(2026, 7, 16, 0, 6, 0, 0, time.UTC) }
	second, found, err := store.ClaimInboundJob(ctx, job.ChannelKey, job.AccountID)
	if err != nil || !found || second.ClaimToken == first.ClaimToken || second.Attempts != 2 {
		t.Fatalf("reclaim = %+v found=%v err=%v", second, found, err)
	}
	if err := store.RenewInboundJob(ctx, first); err == nil {
		t.Fatal("expired claim renewed the replacement lease")
	}
	if err := store.CompleteInboundJob(ctx, first); err == nil {
		t.Fatal("expired claim completed the replacement lease")
	}
	if err := store.CompleteInboundJob(ctx, second); err != nil {
		t.Fatalf("complete replacement claim: %v", err)
	}
}

func TestInboundJobQueueReleaseRetriesImmediatelyWithoutConsumingAttempt(t *testing.T) {
	ctx := context.Background()
	store := newInboundTestStore(t)
	job := channel.InboundJob{ChannelKey: "weixin", AccountID: "bot-1", EventID: "restart-event", Payload: []byte(`{"text":"hello"}`)}
	if inserted, err := store.EnqueueInboundJob(ctx, job); err != nil || !inserted {
		t.Fatalf("enqueue: inserted=%v err=%v", inserted, err)
	}
	first, found, err := store.ClaimInboundJob(ctx, job.ChannelKey, job.AccountID)
	if err != nil || !found || first.Attempts != 1 {
		t.Fatalf("claim first = %+v found=%v err=%v", first, found, err)
	}
	if err := store.ReleaseInboundJob(ctx, first); err != nil {
		t.Fatalf("release: %v", err)
	}
	second, found, err := store.ClaimInboundJob(ctx, job.ChannelKey, job.AccountID)
	if err != nil || !found || second.Attempts != 1 || second.ClaimToken == first.ClaimToken {
		t.Fatalf("claim released = %+v found=%v err=%v", second, found, err)
	}
	if err := store.CompleteInboundJob(ctx, second); err != nil {
		t.Fatalf("complete retry: %v", err)
	}
}

func TestInboundJobQueueDeadLettersAfterMaxAttempts(t *testing.T) {
	ctx := context.Background()
	store := newInboundTestStore(t)
	job := channel.InboundJob{ChannelKey: "feishu", AccountID: "feishu:app", EventID: "poison-event", Payload: []byte(`{"invalid":true}`), MaxAttempts: 1}
	if inserted, err := store.EnqueueInboundJob(ctx, job); err != nil || !inserted {
		t.Fatalf("enqueue: inserted=%v err=%v", inserted, err)
	}
	claimed, found, err := store.ClaimInboundJob(ctx, job.ChannelKey, job.AccountID)
	if err != nil || !found || claimed.MaxAttempts != 1 {
		t.Fatalf("claim=%+v found=%v err=%v", claimed, found, err)
	}
	if err := store.FailInboundJob(ctx, claimed, "invalid payload", store.now().Add(time.Minute)); err != nil {
		t.Fatalf("dead letter: %v", err)
	}
	if _, found, err := store.ClaimInboundJob(ctx, job.ChannelKey, job.AccountID); err != nil || found {
		t.Fatalf("dead-lettered job was reclaimed: found=%v err=%v", found, err)
	}
	var status string
	var nextAttempt int64
	if err := store.db.QueryRow(ctx, `SELECT status, next_attempt_at FROM gateway_inbound_jobs WHERE channel_key = ? AND account_id = ? AND event_id = ?`, job.ChannelKey, job.AccountID, job.EventID).Scan(&status, &nextAttempt); err != nil {
		t.Fatalf("load dead letter: %v", err)
	}
	if status != "dead_letter" || nextAttempt != 0 {
		t.Fatalf("dead letter status=%q next_attempt_at=%d", status, nextAttempt)
	}
}

func TestInboundJobQueueTerminatesPermanentFailureBeforeAttemptLimit(t *testing.T) {
	ctx := context.Background()
	store := newInboundTestStore(t)
	job := channel.InboundJob{ChannelKey: "weixin", AccountID: "bot", EventID: "forbidden", Payload: []byte(`{}`), MaxAttempts: 3}
	if _, err := store.EnqueueInboundJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	claimed, found, err := store.ClaimInboundJob(ctx, job.ChannelKey, job.AccountID)
	if err != nil || !found {
		t.Fatalf("claim=%+v found=%v error=%v", claimed, found, err)
	}
	if err := store.FailInboundJob(ctx, claimed, "permission_denied", time.Time{}); err != nil {
		t.Fatal(err)
	}
	var status string
	var attempts, maxAttempts int
	if err := store.db.QueryRow(ctx, `SELECT status, attempts, max_attempts FROM gateway_inbound_jobs WHERE channel_key = ? AND account_id = ? AND event_id = ?`, job.ChannelKey, job.AccountID, job.EventID).Scan(&status, &attempts, &maxAttempts); err != nil {
		t.Fatal(err)
	}
	if status != "dead_letter" || attempts != 1 || maxAttempts != 3 {
		t.Fatalf("state=%s attempts=%d max=%d", status, attempts, maxAttempts)
	}
}

func TestInboundEventClaimsAreDurableAndFenced(t *testing.T) {
	ctx := context.Background()
	store := newInboundTestStore(t)
	first, err := store.ClaimInboundEvent(ctx, "feishu", "feishu:app", "event-1")
	if err != nil || first.State != channel.InboundEventAcquired || first.Token == "" {
		t.Fatalf("first claim = %+v, %v", first, err)
	}
	busy, err := store.ClaimInboundEvent(ctx, "feishu", "feishu:app", "event-1")
	if err != nil || busy.State != channel.InboundEventBusy {
		t.Fatalf("busy claim = %+v, %v", busy, err)
	}
	store.now = func() time.Time { return time.Date(2026, 7, 16, 0, 6, 0, 0, time.UTC) }
	second, err := store.ClaimInboundEvent(ctx, "feishu", "feishu:app", "event-1")
	if err != nil || second.State != channel.InboundEventAcquired || second.Token == first.Token {
		t.Fatalf("reclaimed = %+v, %v", second, err)
	}
	if err := store.CompleteInboundEvent(ctx, first); err == nil {
		t.Fatal("stale claim completed a newer inbound attempt")
	}
	if err := store.CompleteInboundEvent(ctx, second); err != nil {
		t.Fatalf("complete current claim: %v", err)
	}
	completed, err := store.ClaimInboundEvent(ctx, "feishu", "feishu:app", "event-1")
	if err != nil || completed.State != channel.InboundEventCompleted {
		t.Fatalf("completed claim = %+v, %v", completed, err)
	}
}

func TestReleasedInboundEventCanBeRetriedImmediately(t *testing.T) {
	ctx := context.Background()
	store := newInboundTestStore(t)
	claim, err := store.ClaimInboundEvent(ctx, "dingtalk", "dingtalk:app", "message-1")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := store.ReleaseInboundEvent(ctx, claim); err != nil {
		t.Fatalf("release: %v", err)
	}
	retry, err := store.ClaimInboundEvent(ctx, "dingtalk", "dingtalk:app", "message-1")
	if err != nil || retry.State != channel.InboundEventAcquired || retry.Token == claim.Token {
		t.Fatalf("retry claim = %+v, %v", retry, err)
	}
}

func TestRenewedInboundEventCannotBeReclaimedAtOriginalLeaseExpiry(t *testing.T) {
	ctx := context.Background()
	store := newInboundTestStore(t)
	claim, err := store.ClaimInboundEvent(ctx, "feishu", "feishu:app", "event-renewed")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	store.now = func() time.Time { return time.Date(2026, 7, 16, 0, 4, 0, 0, time.UTC) }
	if err := store.RenewInboundEvent(ctx, claim); err != nil {
		t.Fatalf("renew: %v", err)
	}
	store.now = func() time.Time { return time.Date(2026, 7, 16, 0, 6, 0, 0, time.UTC) }
	busy, err := store.ClaimInboundEvent(ctx, "feishu", "feishu:app", "event-renewed")
	if err != nil || busy.State != channel.InboundEventBusy {
		t.Fatalf("claim after original expiry = %+v, %v", busy, err)
	}
	store.now = func() time.Time { return time.Date(2026, 7, 16, 0, 10, 0, 0, time.UTC) }
	newClaim, err := store.ClaimInboundEvent(ctx, "feishu", "feishu:app", "event-renewed")
	if err != nil || newClaim.State != channel.InboundEventAcquired {
		t.Fatalf("claim after renewed expiry = %+v, %v", newClaim, err)
	}
	if err := store.RenewInboundEvent(ctx, claim); err == nil {
		t.Fatal("stale claim renewed a newer inbound attempt")
	}
}
