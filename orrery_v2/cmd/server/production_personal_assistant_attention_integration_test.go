package main

import (
	"context"
	"errors"
	"testing"
	"time"

	todoapp "orrery_v2/internal/app/todo"
	gwbindings "orrery_v2/internal/gateway/binding"
	"orrery_v2/internal/gateway/channel"
	gwoutbound "orrery_v2/internal/gateway/outbound"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/outbox"
	"orrery_v2/internal/platform/testkit"
)

type attentionExternalSender struct {
	deliveries []channel.Delivery
	err        error
}

func (s *attentionExternalSender) Send(_ context.Context, delivery channel.Delivery) (channel.DeliveryResult, error) {
	s.deliveries = append(s.deliveries, delivery)
	if s.err != nil {
		return channel.DeliveryResult{}, s.err
	}
	return channel.DeliveryResult{ExternalMessageID: "weixin-test-receipt"}, nil
}

func TestPersonalAssistantAttentionRecoversExhaustedNotificationEndToEnd(t *testing.T) {
	for _, resolvedBeforeSend := range []bool{false, true} {
		name := "provider_restored"
		if resolvedBeforeSend {
			name = "resolved_before_send"
		}
		t.Run(name, func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			ctx := context.Background()
			bindings := gwbindings.NewStore(env.DB)
			if _, err := bindings.UpsertVerified(ctx, gwbindings.UpsertInput{TenantID: "tenant-a", ChannelKey: "weixin", AccountID: "weixin-a", ExternalTenantID: "tenant-a", ExternalUserID: "wx-alice", HumanProfileID: "human-a"}); err != nil {
				t.Fatal(err)
			}
			jobs, err := env.Databases.Store(db.RoleJobs)
			if err != nil {
				t.Fatal(err)
			}
			sender := &attentionExternalSender{err: errors.New("provider unavailable")}
			deliveries := gwoutbound.NewService(jobs, bindings, sender)
			scan, todos, _, _, auth := attentionFixture()
			todos.items = []todoapp.TodoItem{humanAttentionTodo("task")}
			scan.deliveries = deliveries
			notices, _ := attachAttentionRecovery(&scan, auth)
			scan.recovery.deliveries = deliveries
			deliveries.SetDeliveryValidator(productionPersonalAssistantNotificationGuard{attention: scan, replies: scan.replies}.Validate)
			if err := scan.Sweep(ctx); err != nil {
				t.Fatal(err)
			}
			queue := outbox.New(jobs)
			for i := 0; i < 12; i++ {
				job, ok, err := queue.Claim(ctx, "test", time.Now().Add(time.Second))
				if err != nil || !ok {
					t.Fatalf("claim %d: %v %v", i, ok, err)
				}
				if err := deliveries.HandleOutbox(ctx, job); err == nil {
					t.Fatal("expected channel failure")
				}
				if err := queue.Fail(ctx, job.ID, "test", "channel failed", time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			key := personalAssistantAttentionKey(scan.recipients[0], "todo", "task", "assigned")
			original, err := deliveries.ListByIdempotencyKey(ctx, "tenant-a", "human-a", key)
			if err != nil || len(original) != 1 || original[0].Status != "dead_letter" {
				t.Fatalf("not exhausted: %+v %v", original, err)
			}
			if _, err := jobs.Exec(ctx, `UPDATE gateway_message_deliveries SET updated_at = ? WHERE id = ?`, time.Now().Add(-gwoutbound.RecoveryCooldown-time.Second).UnixNano(), original[0].ID); err != nil {
				t.Fatal(err)
			}
			// Reconstruct the service after failure; no inbound chat or new event is needed.
			deliveries = gwoutbound.NewService(jobs, bindings, sender)
			deliveries.SetDeliveryValidator(productionPersonalAssistantNotificationGuard{attention: scan, replies: scan.replies}.Validate)
			scan.deliveries, scan.recovery.deliveries = deliveries, deliveries
			if err := scan.Sweep(ctx); err != nil {
				t.Fatal(err)
			}
			if len(notices.messages) != 2 {
				t.Fatalf("missing reminder/failure notice: %+v", notices.messages)
			}
			if resolvedBeforeSend {
				todos.items[0].Status = todoapp.TodoStatusDone
			}
			sender.err = nil
			job, ok, err := queue.Claim(ctx, "recovered", time.Now().Add(time.Second))
			if err != nil || !ok {
				t.Fatalf("recovery not queued: %v %v", ok, err)
			}
			if err := deliveries.HandleOutbox(ctx, job); err != nil {
				t.Fatal(err)
			}
			if err := queue.Complete(ctx, job.ID, "recovered"); err != nil {
				t.Fatal(err)
			}
			final, err := deliveries.Get(ctx, "tenant-a", original[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			wantStatus, wantCalls := "delivered", 13
			if resolvedBeforeSend {
				wantStatus, wantCalls = "failed", 12
			}
			if final.Status != wantStatus || final.Attempts != 13 || len(sender.deliveries) != wantCalls {
				t.Fatalf("final=%+v sends=%d", final, len(sender.deliveries))
			}
			if err := scan.Sweep(ctx); err != nil {
				t.Fatal(err)
			}
			if _, more, err := queue.Claim(ctx, "extra", time.Now().Add(time.Hour)); err != nil || more {
				t.Fatalf("duplicate job: %v %v", more, err)
			}
		})
	}
}

func TestPersonalAssistantAttentionTodoCommitToDurableWeixinDelivery(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	bindings := gwbindings.NewStore(env.DB)
	if err := bindings.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := bindings.UpsertVerified(ctx, gwbindings.UpsertInput{
		TenantID: "tenant-a", ChannelKey: "weixin", AccountID: "weixin-a", ExternalTenantID: "tenant-a", ExternalUserID: "wx-alice", HumanProfileID: "human-a",
	}); err != nil {
		t.Fatal(err)
	}
	jobs, err := env.Databases.Store(db.RoleJobs)
	if err != nil {
		t.Fatal(err)
	}
	sender := &attentionExternalSender{}
	delivery := gwoutbound.NewService(jobs, bindings, sender)
	if err := delivery.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	todos := todoapp.NewService(todoapp.ServiceOptions{Repository: todoapp.NewRepository(env.DBRouter), Clock: env.Clock, IDGenerator: env.IDGenerator})
	scan, _, _, _, _ := attentionFixture()
	scan.todos, scan.deliveries = todos, delivery
	heartbeat := newProductionPersonalAssistantHeartbeat()
	todos.SetNotificationWake(heartbeat.Wake)
	scanned := make(chan error, 4)
	heartbeat.Start(ctx, time.Hour, time.Second, func(ctx context.Context) error { err := scan.Sweep(ctx); scanned <- err; return err }, nil)
	defer heartbeat.Stop()
	wait := func() {
		t.Helper()
		select {
		case err := <-scanned:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("event did not wake notification scan")
		}
	}
	wait()
	item, err := todos.CreateTodo(ctx, todoapp.ViewerRef{TenantID: "tenant-a", ViewerID: "alice", ViewerKind: todoapp.ViewerKindUser, AuthorizationSubjectID: "human-a"}, todoapp.CreateTodoRequest{
		Title: "Confirm customer quote", Description: "Provide the approved amount", Status: todoapp.TodoStatusPlanned, AssigneeID: "human-a", AssigneeKind: todoapp.AssigneeKindHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	wait()
	heartbeat.Stop()
	key := personalAssistantAttentionKey(scan.recipients[0], "todo", item.TodoID, "assigned")
	if exists, err := delivery.HasIdempotencyKey(ctx, "tenant-a", "human-a", key); err != nil || !exists {
		t.Fatalf("missing durable request: %v", err)
	}
	queue := outbox.New(jobs)
	job, ok, err := queue.Claim(ctx, "notification-test", time.Now().Add(time.Second))
	if err != nil || !ok {
		t.Fatalf("missing outbound job: %v", err)
	}
	if err := delivery.HandleOutbox(ctx, job); err != nil {
		t.Fatal(err)
	}
	if len(sender.deliveries) != 1 || sender.deliveries[0].RecipientID != "wx-alice" || sender.deliveries[0].AccountID != "weixin-a" {
		t.Fatalf("wrong delivery: %+v", sender.deliveries)
	}
	// Reconstructed delivery service and scanner share only persisted state.
	scan.deliveries = gwoutbound.NewService(jobs, bindings, sender)
	if err := scan.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if _, more, err := queue.Claim(ctx, "notification-test-2", time.Now().Add(2*time.Second)); err != nil || more {
		t.Fatalf("restart duplicated request: more=%v %v", more, err)
	}
	if len(sender.deliveries) != 1 {
		t.Fatal("duplicate external delivery")
	}
}
