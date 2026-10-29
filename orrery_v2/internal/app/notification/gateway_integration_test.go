package notification

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/gateway/binding"
	"orrery_v2/internal/gateway/channel"
	"orrery_v2/internal/gateway/outbound"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/outbox"
)

type integrationSender struct{ sent []channel.Delivery }

func (s *integrationSender) Send(_ context.Context, d channel.Delivery) (channel.DeliveryResult, error) {
	s.sent = append(s.sent, d)
	return channel.DeliveryResult{ExternalMessageID: "verified-receipt"}, nil
}

func TestSubscriptionPublishesThroughRealGateway(t *testing.T) {
	for _, channelKey := range []string{"weixin", "feishu"} {
		t.Run(channelKey, func(t *testing.T) {
			ctx := context.Background()
			s, tasks, _, _, _, viewer := fixture(t)
			jobs, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name() + "-jobs"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = jobs.Close() })
			bindings := binding.NewStore(s.store)
			_, err = bindings.UpsertVerified(ctx, binding.UpsertInput{
				TenantID: viewer.TenantID, HumanProfileID: viewer.HumanID,
				ChannelKey: channelKey, AccountID: "personal-assistant",
				ExternalUserID: "bound-owner", DefaultAgentID: "assistant",
			})
			if err != nil {
				t.Fatal(err)
			}
			sender := &integrationSender{}
			gateway := outbound.NewService(jobs, bindings, sender)
			gateway.AddDeliveryValidator(s.ValidateDelivery)
			s.gateway = gateway
			source := publishRule(t, s, viewer, "subscribers")
			if _, err = s.Subscribe(ctx, viewer, source.ID, Subscription{Enabled: true, Channels: []string{channelKey}}); err != nil {
				t.Fatal(err)
			}
			addRun(s, tasks, "verified-run", scheduler.RunStatusCompleted, true)
			later(s)
			mustSweep(t, s)
			queue := outbox.New(jobs)
			job, found, err := queue.Claim(ctx, "notification-test", time.Now().Add(time.Second))
			if err != nil || !found {
				t.Fatalf("notification did not reach durable delivery queue: found=%v err=%v", found, err)
			}
			if err = gateway.HandleOutbox(ctx, job); err != nil {
				t.Fatal(err)
			}
			if err = gateway.HandleOutbox(ctx, job); err != nil {
				t.Fatal(err)
			}
			mustSweep(t, s)
			page := mustInbox(t, s, viewer)
			if page.Total != 1 || len(page.Items[0].Pushes) != 1 || page.Items[0].Pushes[0].Status != "delivered" {
				t.Fatalf("missing verified delivery receipt: %+v", page)
			}
			if len(sender.sent) != 1 || sender.sent[0].RecipientID != "bound-owner" || sender.sent[0].AccountID != "personal-assistant" {
				t.Fatalf("incorrect or duplicate recipient: %+v", sender.sent)
			}
			stored, err := gateway.Get(ctx, viewer.TenantID, sender.sent[0].ID)
			if err != nil || stored.Body != "The report is ready." || stored.Title != "" {
				t.Fatalf("report notification content: %+v err=%v", stored, err)
			}
		})
	}
}
