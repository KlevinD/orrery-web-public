package outbound

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	gwbindings "orrery_v2/internal/gateway/binding"
	"orrery_v2/internal/gateway/channel"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/outbox"
)

type fakeSender struct {
	deliveries []channel.Delivery
	err        error
	receipt    string
}

type failingResolveBindingStore struct {
	BindingStore
	err error
}

func (s failingResolveBindingStore) Resolve(context.Context, gwbindings.ExternalIdentity) (gwbindings.Binding, error) {
	return gwbindings.Binding{}, s.err
}

func (s *fakeSender) Send(_ context.Context, delivery channel.Delivery) (channel.DeliveryResult, error) {
	s.deliveries = append(s.deliveries, delivery)
	if s.err != nil {
		return channel.DeliveryResult{}, s.err
	}
	receipt := s.receipt
	if receipt == "" {
		receipt = "external-1"
	}
	return channel.DeliveryResult{ExternalMessageID: receipt}, nil
}

func newDeliveryFixture(t *testing.T) (*Service, *gwbindings.Store, *outbox.Store, *fakeSender, gwbindings.Binding) {
	t.Helper()
	ctx := context.Background()
	core, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name() + "-core"})
	if err != nil {
		t.Fatalf("open core: %v", err)
	}
	jobs, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name() + "-jobs"})
	if err != nil {
		t.Fatalf("open jobs: %v", err)
	}
	t.Cleanup(func() { _ = core.Close(); _ = jobs.Close() })
	bindings := gwbindings.NewStore(core)
	code, err := bindings.CreateCode(ctx, gwbindings.CreateCodeInput{
		TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "feishu:app", HumanProfileID: "human-a", DefaultAgentID: "agent-a",
	})
	if err != nil {
		t.Fatalf("CreateCode: %v", err)
	}
	binding, err := bindings.ConsumeCode(ctx, code.Value, gwbindings.ExternalIdentity{
		ChannelKey: "feishu", AccountID: "feishu:app", ExternalTenantID: "fs-tenant", ExternalUserID: "open-a",
	})
	if err != nil {
		t.Fatalf("ConsumeCode: %v", err)
	}
	sender := &fakeSender{}
	service := NewService(jobs, bindings, sender)
	service.now = func() time.Time { return time.Date(2026, 7, 15, 1, 0, 0, 0, time.UTC) }
	return service, bindings, outbox.New(jobs), sender, binding
}

func TestDeliveryFailureObserverKeepsProviderCausePrivate(t *testing.T) {
	ctx := context.Background()
	service, _, queue, sender, _ := newDeliveryFixture(t)
	sender.err = errors.New("provider timeout token=private")
	var observed []DeliveryFailure
	service.SetFailureObserver(func(_ context.Context, failure DeliveryFailure) error {
		observed = append(observed, failure)
		return nil
	})
	deliveries, err := service.Enqueue(ctx, EnqueueCommand{TenantID: "tenant-a", HumanProfileID: "human-a", AccountID: "feishu:app", ChannelKeys: []string{"feishu"}, Body: "original content", IdempotencyKey: "failure-observation"})
	if err != nil {
		t.Fatal(err)
	}
	job, ok, err := queue.Claim(ctx, "worker", service.now())
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if err := service.HandleOutbox(ctx, job); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatalf("unsafe relay error: %v", err)
	} else {
		var captured interface{ DiagnosticCaptured() bool }
		if !errors.As(err, &captured) || !captured.DiagnosticCaptured() {
			t.Fatal("relay would capture a duplicate generic incident")
		}
	}
	stored, err := service.Get(ctx, "tenant-a", deliveries[0].ID)
	if err != nil || strings.Contains(stored.LastError, "private") || stored.Status != "retrying" {
		t.Fatalf("unsafe public state: %+v %v", stored, err)
	}
	if len(observed) != 1 || observed[0].Delivery.ID != deliveries[0].ID || observed[0].Attempt != 1 || observed[0].Code != "channel_send_failed" || !errors.Is(observed[0].Cause, sender.err) {
		t.Fatalf("provider cause lost: %+v", observed)
	}
}

func TestDeliveryEnqueueIsIdempotentAndSendsBoundRecipient(t *testing.T) {
	ctx := context.Background()
	service, _, queue, sender, _ := newDeliveryFixture(t)
	command := EnqueueCommand{
		TenantID: "tenant-a", HumanProfileID: "human-a", ChannelKeys: []string{"feishu"},
		Body: "hello", Format: "text", IdempotencyKey: "event-1",
	}
	if exists, err := service.HasIdempotencyKey(ctx, command.TenantID, command.HumanProfileID, command.IdempotencyKey); err != nil || exists {
		t.Fatalf("key before enqueue exists=%v err=%v", exists, err)
	}
	first, err := service.Enqueue(ctx, command)
	if err != nil || len(first) != 1 {
		t.Fatalf("first Enqueue = %+v, %v", first, err)
	}
	if exists, err := service.HasIdempotencyKey(ctx, command.TenantID, command.HumanProfileID, command.IdempotencyKey); err != nil || !exists {
		t.Fatalf("key after enqueue exists=%v err=%v", exists, err)
	}
	if exists, err := service.HasIdempotencyKey(ctx, command.TenantID, "human-b", command.IdempotencyKey); err != nil || exists {
		t.Fatalf("cross-human key exists=%v err=%v", exists, err)
	}
	second, err := service.Enqueue(ctx, command)
	if err != nil || len(second) != 1 || second[0].ID != first[0].ID {
		t.Fatalf("second Enqueue = %+v, %v", second, err)
	}
	job, ok, err := queue.Claim(ctx, "worker", service.now())
	if err != nil || !ok {
		t.Fatalf("Claim ok=%v err=%v", ok, err)
	}
	if err := service.HandleOutbox(ctx, job); err != nil {
		t.Fatalf("HandleOutbox: %v", err)
	}
	if len(sender.deliveries) != 1 || sender.deliveries[0].RecipientID != "open-a" || sender.deliveries[0].AccountID != "feishu:app" {
		t.Fatalf("deliveries = %+v", sender.deliveries)
	}
	stored, err := service.Get(ctx, "tenant-a", first[0].ID)
	if err != nil || stored.Status != "delivered" || stored.ExternalMessageID != "external-1" {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
}

func TestDeliveryEnqueueSelectsRequestedAccount(t *testing.T) {
	ctx := context.Background()
	service, bindings, _, _, _ := newDeliveryFixture(t)
	if _, err := bindings.UpsertVerified(ctx, gwbindings.UpsertInput{
		TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "orrery", ExternalTenantID: "fs-tenant",
		ExternalUserID: "open-orrery", HumanProfileID: "human-a", DefaultAgentID: "orrery",
	}); err != nil {
		t.Fatalf("UpsertVerified: %v", err)
	}
	deliveries, err := service.Enqueue(ctx, EnqueueCommand{
		TenantID: "tenant-a", HumanProfileID: "human-a", ChannelKeys: []string{"feishu"}, AccountID: "orrery",
		Body: "approval required", Format: "text", IdempotencyKey: "event-orrery-account",
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if len(deliveries) != 1 || deliveries[0].AccountID != "orrery" || deliveries[0].RecipientID != "open-orrery" {
		t.Fatalf("deliveries = %+v, want orrery binding", deliveries)
	}
}

func TestDeliveryDoesNotSendAfterBindingRevocation(t *testing.T) {
	ctx := context.Background()
	service, bindings, queue, sender, binding := newDeliveryFixture(t)
	deliveries, err := service.Enqueue(ctx, EnqueueCommand{
		TenantID: "tenant-a", HumanProfileID: "human-a", ChannelKeys: []string{"feishu"}, Body: "hello", IdempotencyKey: "event-2",
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := bindings.Revoke(ctx, "tenant-a", "human-a", binding.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	job, ok, err := queue.Claim(ctx, "worker", service.now())
	if err != nil || !ok {
		t.Fatalf("Claim ok=%v err=%v", ok, err)
	}
	if err := service.HandleOutbox(ctx, job); err != nil {
		t.Fatalf("HandleOutbox: %v", err)
	}
	if len(sender.deliveries) != 0 {
		t.Fatalf("revoked binding was sent: %+v", sender.deliveries)
	}
	stored, _ := service.Get(ctx, "tenant-a", deliveries[0].ID)
	if stored.Status != "failed" {
		t.Fatalf("stored = %+v", stored)
	}
}

func TestDeliveryRetriesTransientBindingStoreFailure(t *testing.T) {
	ctx := context.Background()
	service, _, queue, sender, _ := newDeliveryFixture(t)
	deliveries, err := service.Enqueue(ctx, EnqueueCommand{
		TenantID: "tenant-a", HumanProfileID: "human-a", ChannelKeys: []string{"feishu"}, Body: "hello", IdempotencyKey: "event-binding-store",
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	service.bindings = failingResolveBindingStore{BindingStore: service.bindings, err: errors.New("database temporarily unavailable: password=secret-value")}
	job, ok, err := queue.Claim(ctx, "worker", service.now())
	if err != nil || !ok {
		t.Fatalf("Claim ok=%v err=%v", ok, err)
	}
	handleErr := service.HandleOutbox(ctx, job)
	if handleErr == nil {
		t.Fatal("transient binding resolution failure should be retried")
	}
	if strings.Contains(handleErr.Error(), "secret-value") || strings.Contains(handleErr.Error(), "password") {
		t.Fatalf("outbox-visible binding error leaked diagnostics: %q", handleErr)
	}
	if len(sender.deliveries) != 0 {
		t.Fatalf("delivery sent without revalidating its binding: %+v", sender.deliveries)
	}
	stored, err := service.Get(ctx, "tenant-a", deliveries[0].ID)
	if err != nil || stored.Status != "retrying" || stored.Attempts != 1 {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
	if stored.LastError == "" || strings.Contains(stored.LastError, "secret-value") || strings.Contains(stored.LastError, "password") {
		t.Fatalf("stored binding error leaked diagnostics: %q", stored.LastError)
	}
}

func TestDeliveryMarksRetryAndDeadLetter(t *testing.T) {
	ctx := context.Background()
	service, _, queue, sender, _ := newDeliveryFixture(t)
	sender.err = errors.New("temporary outage; access_token=secret-value")
	deliveries, _ := service.Enqueue(ctx, EnqueueCommand{
		TenantID: "tenant-a", HumanProfileID: "human-a", ChannelKeys: []string{"feishu"}, Body: "hello", IdempotencyKey: "event-3",
	})
	job, ok, _ := queue.Claim(ctx, "worker", service.now())
	if !ok {
		t.Fatal("delivery job was not queued")
	}
	job.Attempts = job.MaxAttempts - 1
	handleErr := service.HandleOutbox(ctx, job)
	if handleErr == nil {
		t.Fatal("HandleOutbox should return sender error")
	}
	if strings.Contains(handleErr.Error(), "secret-value") || strings.Contains(handleErr.Error(), "access_token") {
		t.Fatalf("outbox-visible provider error leaked diagnostics: %q", handleErr)
	}
	stored, _ := service.Get(ctx, "tenant-a", deliveries[0].ID)
	if stored.Status != "dead_letter" || stored.Attempts != job.MaxAttempts {
		t.Fatalf("stored = %+v", stored)
	}
}

func TestDeliveryDoesNotRetryPermanentChannelFailure(t *testing.T) {
	ctx := context.Background()
	service, _, queue, sender, _ := newDeliveryFixture(t)
	sender.err = channel.PermanentDeliveryError(errors.New("recipient no longer exists; access_token=secret-value"))
	deliveries, err := service.Enqueue(ctx, EnqueueCommand{
		TenantID: "tenant-a", HumanProfileID: "human-a", ChannelKeys: []string{"feishu"}, Body: "hello", IdempotencyKey: "event-permanent",
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	job, ok, err := queue.Claim(ctx, "worker", service.now())
	if err != nil || !ok {
		t.Fatalf("Claim ok=%v err=%v", ok, err)
	}
	if err := service.HandleOutbox(ctx, job); err != nil {
		t.Fatalf("permanent failure should complete the outbox job: %v", err)
	}
	stored, err := service.Get(ctx, "tenant-a", deliveries[0].ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Status != "failed" || stored.Attempts != 1 || stored.LastError == "" {
		t.Fatalf("stored = %+v", stored)
	}
	if strings.Contains(stored.LastError, "secret-value") || strings.Contains(stored.LastError, "access_token") {
		t.Fatalf("stored provider error leaked diagnostics: %q", stored.LastError)
	}
}

func TestDeliveryDoesNotReportSuccessWithoutExternalReceipt(t *testing.T) {
	ctx := context.Background()
	service, _, queue, sender, _ := newDeliveryFixture(t)
	sender.receipt = " "
	deliveries, err := service.Enqueue(ctx, EnqueueCommand{
		TenantID: "tenant-a", HumanProfileID: "human-a", ChannelKeys: []string{"feishu"}, Body: "hello", IdempotencyKey: "event-no-receipt",
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	job, ok, err := queue.Claim(ctx, "worker", service.now())
	if err != nil || !ok {
		t.Fatalf("Claim ok=%v err=%v", ok, err)
	}
	if err := service.HandleOutbox(ctx, job); err != nil {
		t.Fatalf("receipt contract failure should complete the outbox job: %v", err)
	}
	stored, err := service.Get(ctx, "tenant-a", deliveries[0].ID)
	if err != nil || stored.Status != "failed" || stored.ExternalMessageID != "" {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
}

func TestDeliveryStatusReconcilesTerminalOutboxWithoutReceipt(t *testing.T) {
	for _, test := range []struct {
		jobStatus      outbox.Status
		deliveryStatus string
		wantStatus     string
	}{
		{jobStatus: outbox.StatusDone, deliveryStatus: "pending", wantStatus: "failed"},
		{jobStatus: outbox.StatusDeadLetter, deliveryStatus: "sending", wantStatus: "dead_letter"},
	} {
		t.Run(string(test.jobStatus), func(t *testing.T) {
			ctx := context.Background()
			service, _, _, _, _ := newDeliveryFixture(t)
			deliveries, err := service.Enqueue(ctx, EnqueueCommand{
				TenantID: "tenant-a", HumanProfileID: "human-a", ChannelKeys: []string{"feishu"},
				Body: "hello", IdempotencyKey: "event-terminal-" + string(test.jobStatus),
			})
			if err != nil {
				t.Fatalf("Enqueue: %v", err)
			}
			if _, err := service.jobs.Exec(ctx, `UPDATE gateway_message_deliveries SET status = ? WHERE id = ?`, test.deliveryStatus, deliveries[0].ID); err != nil {
				t.Fatalf("set delivery status: %v", err)
			}
			if _, err := service.jobs.Exec(ctx, `UPDATE platform_outbox_jobs SET status = ?
				WHERE topic = ? AND idempotency_key = ?`, string(test.jobStatus), Topic, "gateway-delivery:"+deliveries[0].ID); err != nil {
				t.Fatalf("set outbox status: %v", err)
			}

			stored, err := service.Get(ctx, "tenant-a", deliveries[0].ID)
			if err != nil || stored.Status != test.wantStatus || stored.LastError == "" {
				t.Fatalf("stored = %+v, %v", stored, err)
			}
		})
	}
}

func TestDeliveryOutboxDoesNotAcknowledgeMissingDurableDelivery(t *testing.T) {
	ctx := context.Background()
	service, _, _, _, _ := newDeliveryFixture(t)
	if err := service.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	err := service.HandleOutbox(ctx, outbox.Job{
		ID: "job-missing-delivery", Topic: Topic, Payload: []byte(`{"delivery_id":"gwd_missing"}`), MaxAttempts: 12,
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("HandleOutbox error = %v, want ErrNotFound", err)
	}
}

func TestDeliverySendingClaimPreventsConcurrentDuplicateSend(t *testing.T) {
	ctx := context.Background()
	service, _, queue, sender, _ := newDeliveryFixture(t)
	deliveries, err := service.Enqueue(ctx, EnqueueCommand{
		TenantID: "tenant-a", HumanProfileID: "human-a", ChannelKeys: []string{"feishu"}, Body: "hello", IdempotencyKey: "event-claim",
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	job, ok, err := queue.Claim(ctx, "worker", service.now())
	if err != nil || !ok {
		t.Fatalf("Claim ok=%v err=%v", ok, err)
	}
	claimed, err := service.claimDeliveryAttempt(ctx, deliveries[0].ID, 1)
	if err != nil || !claimed {
		t.Fatalf("claimDeliveryAttempt claimed=%v err=%v", claimed, err)
	}
	if err := service.HandleOutbox(ctx, job); !errors.Is(err, ErrInProgress) {
		t.Fatalf("HandleOutbox error = %v, want ErrInProgress", err)
	}
	if len(sender.deliveries) != 0 {
		t.Fatalf("concurrent delivery was sent: %+v", sender.deliveries)
	}
}

func TestDeliveryRejectsPartialChannelResolution(t *testing.T) {
	ctx := context.Background()
	service, _, queue, _, _ := newDeliveryFixture(t)
	_, err := service.Enqueue(ctx, EnqueueCommand{
		TenantID: "tenant-a", HumanProfileID: "human-a", ChannelKeys: []string{"feishu", "dingtalk"},
		Body: "hello", IdempotencyKey: "event-partial",
	})
	if !errors.Is(err, ErrBindingMissing) {
		t.Fatalf("Enqueue error = %v, want ErrBindingMissing", err)
	}
	if _, ok, claimErr := queue.Claim(ctx, "worker", service.now()); claimErr != nil || ok {
		t.Fatalf("partial delivery queued a job: ok=%v err=%v", ok, claimErr)
	}
}

func TestDeliveryRejectsBodyThatCannotFinishWithinOneProviderAttempt(t *testing.T) {
	ctx := context.Background()
	service, _, _, _, _ := newDeliveryFixture(t)
	_, err := service.Enqueue(ctx, EnqueueCommand{
		TenantID: "tenant-a", HumanProfileID: "human-a", ChannelKeys: []string{"feishu"},
		Body: strings.Repeat("x", MaxDeliveryBodyRunes+1), IdempotencyKey: "event-oversized",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized Enqueue error = %v, want ErrInvalidInput", err)
	}
}

func TestDeliveryRejectsIdempotencyKeyReuseWithDifferentContent(t *testing.T) {
	ctx := context.Background()
	service, _, _, _, _ := newDeliveryFixture(t)
	command := EnqueueCommand{
		TenantID: "tenant-a", HumanProfileID: "human-a", ChannelKeys: []string{"feishu"},
		Body: "first", IdempotencyKey: "event-conflict",
	}
	if _, err := service.Enqueue(ctx, command); err != nil {
		t.Fatalf("first Enqueue: %v", err)
	}
	command.Body = "second"
	if _, err := service.Enqueue(ctx, command); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("second Enqueue error = %v, want ErrIdempotencyConflict", err)
	}
}

func TestDeliveryRejectsIdempotencyKeyReuseWithDifferentChannelSet(t *testing.T) {
	ctx := context.Background()
	service, bindings, _, _, _ := newDeliveryFixture(t)
	if _, err := bindings.UpsertVerified(ctx, gwbindings.UpsertInput{
		TenantID: "tenant-a", ChannelKey: "dingtalk", AccountID: "dingtalk:app", ExternalTenantID: "corp-a",
		ExternalUserID: "staff-a", HumanProfileID: "human-a", DefaultAgentID: "agent-a",
	}); err != nil {
		t.Fatalf("UpsertVerified: %v", err)
	}
	command := EnqueueCommand{
		TenantID: "tenant-a", HumanProfileID: "human-a", ChannelKeys: []string{"feishu"},
		Body: "same content", IdempotencyKey: "event-channel-set",
	}
	if _, err := service.Enqueue(ctx, command); err != nil {
		t.Fatalf("first Enqueue: %v", err)
	}
	command.ChannelKeys = []string{"feishu", "dingtalk"}
	if _, err := service.Enqueue(ctx, command); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expanded Enqueue error = %v, want ErrIdempotencyConflict", err)
	}
}

func TestDeliveryIdempotentRetryReturnsOriginalAfterRecipientRebind(t *testing.T) {
	ctx := context.Background()
	service, bindings, _, _, binding := newDeliveryFixture(t)
	command := EnqueueCommand{
		TenantID: "tenant-a", HumanProfileID: "human-a", ChannelKeys: []string{"feishu"},
		Body: "same content", IdempotencyKey: "event-rebound-target",
	}
	first, err := service.Enqueue(ctx, command)
	if err != nil {
		t.Fatalf("first Enqueue: %v", err)
	}
	if err := bindings.Revoke(ctx, "tenant-a", "human-a", binding.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	code, err := bindings.CreateCode(ctx, gwbindings.CreateCodeInput{
		TenantID: "tenant-a", ChannelKey: "feishu", AccountID: "feishu:app", HumanProfileID: "human-a", DefaultAgentID: "agent-a",
	})
	if err != nil {
		t.Fatalf("CreateCode: %v", err)
	}
	if _, err := bindings.ConsumeCode(ctx, code.Value, gwbindings.ExternalIdentity{
		ChannelKey: "feishu", AccountID: "feishu:app", ExternalTenantID: "fs-tenant", ExternalUserID: "open-b",
	}); err != nil {
		t.Fatalf("ConsumeCode: %v", err)
	}
	retried, err := service.Enqueue(ctx, command)
	if err != nil || len(retried) != 1 || retried[0].ID != first[0].ID || retried[0].RecipientID != "open-a" {
		t.Fatalf("rebound idempotent retry = %+v, %v; first = %+v", retried, err, first)
	}
}

func TestDeliveryOldAttemptCannotOverwriteNewSendingClaim(t *testing.T) {
	ctx := context.Background()
	service, _, _, _, _ := newDeliveryFixture(t)
	deliveries, err := service.Enqueue(ctx, EnqueueCommand{
		TenantID: "tenant-a", HumanProfileID: "human-a", ChannelKeys: []string{"feishu"},
		Body: "hello", IdempotencyKey: "event-stale-claim",
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if claimed, err := service.claimDeliveryAttempt(ctx, deliveries[0].ID, 1); err != nil || !claimed {
		t.Fatalf("first claim = %v, %v", claimed, err)
	}
	service.now = func() time.Time { return time.Date(2026, 7, 15, 1, 6, 0, 0, time.UTC) }
	if claimed, err := service.claimDeliveryAttempt(ctx, deliveries[0].ID, 2); err != nil || !claimed {
		t.Fatalf("second claim = %v, %v", claimed, err)
	}
	if err := service.updateClaimedFailure(ctx, deliveries[0].ID, 1, "old failure", "failed"); err == nil {
		t.Fatal("old attempt overwrote the current sending claim")
	}
	stored, err := service.Get(ctx, "tenant-a", deliveries[0].ID)
	if err != nil || stored.Status != "sending" || stored.Attempts != 2 {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
}
