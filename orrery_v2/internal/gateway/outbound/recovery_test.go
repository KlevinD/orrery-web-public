package outbound

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/platform/outbox"
)

func exhaustDelivery(t *testing.T, service *Service, queue *outbox.Store, sender *fakeSender, count int) {
	t.Helper()
	ctx := context.Background()
	sender.err = errors.New("temporary provider failure")
	for i := 0; i < count; i++ {
		job, ok, err := queue.Claim(ctx, "worker", service.now())
		if err != nil || !ok {
			t.Fatalf("claim %d: %v %v", i, ok, err)
		}
		if err := service.HandleOutbox(ctx, job); err == nil {
			t.Fatal("expected provider failure")
		}
		if err := queue.Fail(ctx, job.ID, "worker", "temporary failure", service.now()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDeliveryRecoveryPreservesIdentityAndBoundedAttempts(t *testing.T) {
	ctx := context.Background()
	service, _, queue, sender, _ := newDeliveryFixture(t)
	command := EnqueueCommand{TenantID: "tenant-a", HumanProfileID: "human-a", AccountID: "feishu:app", ChannelKeys: []string{"feishu"}, Body: "pending decision", IdempotencyKey: "notice"}
	deliveries, err := service.Enqueue(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	delivery := deliveries[0]
	recover := func() (bool, error) {
		return service.RecoverDeadLetter(ctx, "tenant-a", "human-a", "feishu:app", delivery.ID)
	}
	exhaustDelivery(t, service, queue, sender, 12)
	if ok, err := recover(); err != nil || ok {
		t.Fatalf("recovered inside cooldown: %v %v", ok, err)
	}
	advance := func() { next := service.now().Add(RecoveryCooldown); service.now = func() time.Time { return next } }
	for batch := 0; batch < 3; batch++ {
		advance()
		if ok, err := recover(); err != nil || !ok {
			t.Fatalf("recovery %d: %v %v", batch, ok, err)
		}
		if ok, err := recover(); err != nil || ok {
			t.Fatalf("duplicate recovery: %v %v", ok, err)
		}
		exhaustDelivery(t, service, queue, sender, 4)
	}
	advance()
	if ok, err := recover(); err != nil || ok {
		t.Fatalf("unbounded recovery: %v %v", ok, err)
	}
	stored, err := service.Get(ctx, "tenant-a", delivery.ID)
	if err != nil || stored.Attempts != 24 || stored.Status != "dead_letter" {
		t.Fatalf("stored: %+v %v", stored, err)
	}
	for _, sent := range sender.deliveries {
		if sent.ID != delivery.ID || sent.Text != delivery.Body || sent.IdempotencyKey != command.IdempotencyKey {
			t.Fatal("recovery changed provider identity or content")
		}
	}
	listed, err := service.ListByIdempotencyKey(ctx, "tenant-a", "human-a", command.IdempotencyKey)
	if err != nil || len(listed) != 1 {
		t.Fatalf("request duplicated: %+v %v", listed, err)
	}
}

func TestDeliveryRecoveryCompletesAfterProviderRestoredWithoutInboundMessage(t *testing.T) {
	ctx := context.Background()
	service, _, queue, sender, _ := newDeliveryFixture(t)
	deliveries, err := service.Enqueue(ctx, EnqueueCommand{TenantID: "tenant-a", HumanProfileID: "human-a", ChannelKeys: []string{"feishu"}, Body: "notice", IdempotencyKey: "notice"})
	if err != nil {
		t.Fatal(err)
	}
	exhaustDelivery(t, service, queue, sender, 12)
	now := service.now().Add(RecoveryCooldown)
	service.now = func() time.Time { return now }
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := service.RecoverDeadLetter(ctx, "tenant-a", "human-a", "feishu:app", deliveries[0].ID)
			if err != nil {
				t.Error(err)
			}
			results <- ok
		}()
	}
	wg.Wait()
	close(results)
	count := 0
	for ok := range results {
		if ok {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("concurrent recovery count=%d", count)
	}
	sender.err = nil
	job, ok, err := queue.Claim(ctx, "restored", service.now())
	if err != nil || !ok || job.Attempts != 12 {
		t.Fatalf("claim: %+v %v %v", job, ok, err)
	}
	if err := service.HandleOutbox(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := queue.Complete(ctx, job.ID, "restored"); err != nil {
		t.Fatal(err)
	}
	stored, err := service.Get(ctx, "tenant-a", deliveries[0].ID)
	if err != nil || stored.Status != "delivered" || stored.Attempts != 13 || stored.ExternalMessageID == "" {
		t.Fatalf("delivery: %+v %v", stored, err)
	}
	if ok, err := service.RecoverDeadLetter(ctx, "tenant-a", "human-a", "feishu:app", stored.ID); err != nil || ok {
		t.Fatalf("redelivered: %v %v", ok, err)
	}
}

func TestDeliveryRecoveryRejectsOtherIdentitiesAndRevokedBindings(t *testing.T) {
	ctx := context.Background()
	service, bindings, queue, sender, binding := newDeliveryFixture(t)
	deliveries, err := service.Enqueue(ctx, EnqueueCommand{TenantID: "tenant-a", HumanProfileID: "human-a", ChannelKeys: []string{"feishu"}, Body: "notice", IdempotencyKey: "notice"})
	if err != nil {
		t.Fatal(err)
	}
	exhaustDelivery(t, service, queue, sender, 12)
	now := service.now().Add(RecoveryCooldown)
	service.now = func() time.Time { return now }
	for _, identity := range [][3]string{{"tenant-b", "human-a", "feishu:app"}, {"tenant-a", "human-b", "feishu:app"}, {"tenant-a", "human-a", "other-account"}} {
		if ok, err := service.RecoverDeadLetter(ctx, identity[0], identity[1], identity[2], deliveries[0].ID); err != nil || ok {
			t.Fatalf("identity bypass: %v %v", ok, err)
		}
	}
	if rows, err := service.ListByIdempotencyKey(ctx, "tenant-b", "human-a", "notice"); err != nil || len(rows) != 0 {
		t.Fatalf("tenant leak: %+v %v", rows, err)
	}
	if err := bindings.Revoke(ctx, "tenant-a", "human-a", binding.ID); err != nil {
		t.Fatal(err)
	}
	if ok, err := service.RecoverDeadLetter(ctx, "tenant-a", "human-a", "feishu:app", deliveries[0].ID); err != nil || ok {
		t.Fatalf("revoked binding: %v %v", ok, err)
	}
}

func TestDeliveryValidatorSuppressesResolvedItemsAndRetriesReadFailures(t *testing.T) {
	for _, scenario := range []string{"resolved", "read_failure", "pending"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			service, _, queue, sender, _ := newDeliveryFixture(t)
			deliveries, err := service.Enqueue(ctx, EnqueueCommand{TenantID: "tenant-a", HumanProfileID: "human-a", ChannelKeys: []string{"feishu"}, Body: "notice", IdempotencyKey: "notice"})
			if err != nil {
				t.Fatal(err)
			}
			service.SetDeliveryValidator(func(ctx context.Context, delivery Delivery) (bool, error) {
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("unbounded eligibility check")
				}
				if delivery.ID != deliveries[0].ID {
					t.Fatal("wrong validation target")
				}
				if scenario == "read_failure" {
					return false, errors.New("secret database details")
				}
				return scenario == "pending", nil
			})
			job, ok, err := queue.Claim(ctx, "worker", service.now())
			if err != nil || !ok {
				t.Fatal("missing job")
			}
			err = service.HandleOutbox(ctx, job)
			if (err != nil) != (scenario == "read_failure") {
				t.Fatalf("handler: %v", err)
			}
			stored, err := service.Get(ctx, "tenant-a", deliveries[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"pending": "delivered", "resolved": "failed", "read_failure": "retrying"}[scenario]
			if stored.Status != want || (len(sender.deliveries) == 1) != (scenario == "pending") {
				t.Fatalf("delivery: %+v sends=%d", stored, len(sender.deliveries))
			}
		})
	}
}
