package notification

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/app/scheduler"
)

func TestNotificationRetryRechecksOriginalRunAgainstPublishedSnapshot(t *testing.T) {
	for _, change := range []string{"tenant", "agent", "runtime", "status", "receipt", "body", "missing"} {
		t.Run(change, func(t *testing.T) {
			s, tasks, _, gateway, _, viewer := fixture(t)
			publishRule(t, s, viewer, "all")
			if _, err := s.SavePreferences(context.Background(), viewer, Preferences{Channels: []string{"feishu"}}); err != nil {
				t.Fatal(err)
			}
			addRun(s, tasks, "origin", scheduler.RunStatusCompleted, false)
			later(s)
			mustSweep(t, s)
			if len(gateway.deliveries) != 1 {
				t.Fatal("missing queued report fixture")
			}
			switch change {
			case "tenant":
				tasks.runs[0].TenantID = "other"
			case "agent":
				tasks.runs[0].AgentID = "other-agent"
			case "runtime":
				tasks.runs[0].RuntimeRunID = "replacement-runtime"
			case "status":
				tasks.runs[0].Status = scheduler.RunStatusRunning
			case "receipt":
				tasks.runs[0].Result.Summary = "different execution evidence"
			case "body":
				tasks.runs[0].Result.AgentReport.Body = "Different final report."
			case "missing":
				tasks.runs = nil
			}
			for _, delivery := range gateway.deliveries {
				if allowed, err := s.ValidateDelivery(context.Background(), delivery); err != nil || allowed {
					t.Fatalf("changed origin accepted for %s: allowed=%v err=%v", change, allowed, err)
				}
			}
		})
	}
}

func TestNotificationRetryPreservesOriginalRunReadFailure(t *testing.T) {
	s, tasks, _, gateway, _, viewer := fixture(t)
	ctx := context.Background()
	publishRule(t, s, viewer, "all")
	if _, err := s.SavePreferences(ctx, viewer, Preferences{Channels: []string{"feishu"}}); err != nil {
		t.Fatal(err)
	}
	addRun(s, tasks, "read-error", scheduler.RunStatusCompleted, false)
	later(s)
	mustSweep(t, s)
	tasks.readErr = context.DeadlineExceeded
	for _, delivery := range gateway.deliveries {
		if allowed, err := s.ValidateDelivery(ctx, delivery); allowed || !errors.Is(err, tasks.readErr) {
			t.Fatalf("read failure ignored: %v %v", allowed, err)
		}
	}
	if err := s.Sweep(ctx); !errors.Is(err, tasks.readErr) {
		t.Fatalf("reconciliation lost read failure: %v", err)
	}
}

func TestNotificationOriginAllowsReceiptProgressButWithholdsLegacyAndTamperedEvents(t *testing.T) {
	for _, change := range []string{"delivery-receipt", "trace-availability", "missing-origin", "event-status", "event-title"} {
		t.Run(change, func(t *testing.T) {
			s, tasks, _, gateway, _, viewer := fixture(t)
			publishRule(t, s, viewer, "all")
			ctx := context.Background()
			if _, err := s.SavePreferences(ctx, viewer, Preferences{Channels: []string{"feishu"}}); err != nil {
				t.Fatal(err)
			}
			addRun(s, tasks, "stable", scheduler.RunStatusCompleted, false)
			later(s)
			mustSweep(t, s)
			item := mustInbox(t, s, viewer).Items[0]
			switch change {
			case "delivery-receipt":
				tasks.runs[0].DeliveryStatus = "delivered"
				tasks.runs[0].ExternalMessageID = "fixture-receipt"
			case "trace-availability":
				tasks.runs[0].TraceAvailable = true
				tasks.runs[0].TraceStatus = "available"
			case "missing-origin":
				if _, err := s.store.Exec(ctx, "DELETE FROM notification_report_origins WHERE event_id=?", item.Event.ID); err != nil {
					t.Fatal(err)
				}
			default:
				if change == "event-status" {
					item.Event.Status = "failed"
				} else {
					item.Event.Title = "Altered source label"
				}
				if _, err := s.store.Exec(ctx, "UPDATE notification_events SET payload=? WHERE id=?", encode(item.Event), item.Event.ID); err != nil {
					t.Fatal(err)
				}
			}
			want := change == "delivery-receipt" || change == "trace-availability"
			for _, delivery := range gateway.deliveries {
				if allowed, err := s.ValidateDelivery(ctx, delivery); err != nil || allowed != want {
					t.Fatalf("origin guard mismatch: %v %v", allowed, err)
				}
			}
		})
	}
}

func TestNotificationOriginAndFanoutCommitAtomically(t *testing.T) {
	s, tasks, _, _, _, viewer := fixture(t)
	publishRule(t, s, viewer, "all")
	addRun(s, tasks, "atomic-origin", scheduler.RunStatusCompleted, false)
	later(s)
	ctx := context.Background()
	if _, err := s.store.Exec(ctx, "CREATE TRIGGER reject_report_origin BEFORE INSERT ON notification_report_origins BEGIN SELECT RAISE(ABORT, 'origin unavailable'); END"); err != nil {
		t.Fatal(err)
	}
	if err := s.Sweep(ctx); err == nil {
		t.Fatal("origin persistence failure ignored")
	}
	for _, table := range []string{"notification_events", "notification_report_origins", "notification_inbox", "notification_pushes"} {
		var count int
		if err := s.store.QueryRow(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("partial publication in %s: %d %v", table, count, err)
		}
	}
	if _, err := s.store.Exec(ctx, "DROP TRIGGER reject_report_origin"); err != nil {
		t.Fatal(err)
	}
	mustSweep(t, s)
	if mustInbox(t, s, viewer).Total != 1 {
		t.Fatal("retry lost report publication")
	}
}
