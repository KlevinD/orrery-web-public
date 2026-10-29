package notification

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/db"
)

type recoveredReportReaderFunc func(context.Context, scheduler.ScheduledRun) (scheduler.AgentReport, bool, error)

func (f recoveredReportReaderFunc) Report(ctx context.Context, run scheduler.ScheduledRun) (scheduler.AgentReport, bool, error) {
	return f(ctx, run)
}

func TestRecoveredReportFileRestartPreservesPublicationAndAuthor(t *testing.T) {
	s, tasks, profiles, gateway, artifacts, viewer := fixture(t)
	ctx := context.Background()
	config := db.Config{Driver: db.DriverFile, URL: "file://" + filepath.ToSlash(filepath.Join(t.TempDir(), "recovered-notifications.db"))}
	store, err := db.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	clock := s.now
	s = New(store, tasks, profiles, gateway, artifacts, nil, "")
	s.SetClock(clock)
	report := scheduler.AgentReport{AgentID: "agent", RuntimeRunID: "scheduled-report-durable", Body: "This report preserves the saved original outcome."}
	reader := recoveredReportReaderFunc(func(context.Context, scheduler.ScheduledRun) (scheduler.AgentReport, bool, error) {
		return report, true, nil
	})
	s.SetReportReader(reader)
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	publishRule(t, s, viewer, "all")
	if _, err := s.SavePreferences(ctx, viewer, Preferences{Channels: []string{"feishu"}}); err != nil {
		t.Fatal(err)
	}
	addRun(s, tasks, "durable-supplement", scheduler.RunStatusCompleted, false)
	tasks.runs[0].Result.AgentReport = nil
	later(s)
	mustSweep(t, s)
	if err := s.MarkRead(ctx, viewer, mustInbox(t, s, viewer).Items[0].ID, true); err != nil {
		t.Fatal(err)
	}
	clock = s.now
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = db.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	s = New(store, tasks, profiles, gateway, artifacts, nil, "")
	s.SetClock(clock)
	s.SetReportReader(reader)
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	mustSweep(t, s)
	page := mustInbox(t, s, viewer)
	if page.Total != 1 || page.Unread != 0 || *page.Items[0].Event.AgentReport != report || len(gateway.requests) != 1 {
		t.Fatalf("restart lost authorship/read state or duplicated: %+v", page)
	}
	for _, delivery := range gateway.deliveries {
		if allowed, err := s.ValidateDelivery(ctx, delivery); err != nil || !allowed {
			t.Fatalf("durable origin proof lost: %v %v", allowed, err)
		}
	}
}

func TestRecoveredReportCrossTenantSharingRequiresCurrentExplicitGrant(t *testing.T) {
	s, tasks, _, gateway, _, _ := fixture(t)
	ctx := context.Background()
	viewer := Viewer{TenantID: "other", HumanID: "foreign", Manage: true, System: true}
	source, err := s.SaveSource(ctx, viewer, Source{TaskID: "task", TenantID: "other", OriginTenant: "org", Enabled: true, Audience: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePreferences(ctx, viewer, Preferences{Channels: []string{"feishu"}}); err != nil {
		t.Fatal(err)
	}
	addRun(s, tasks, "shared", scheduler.RunStatusCompleted, false)
	tasks.runs[0].Result.AgentReport = nil
	s.SetReportReader(recoveredReportReaderFunc(func(_ context.Context, r scheduler.ScheduledRun) (scheduler.AgentReport, bool, error) {
		if r.TenantID != "org" {
			t.Fatal("reader received the recipient tenant as origin")
		}
		return scheduler.AgentReport{AgentID: r.AgentID, RuntimeRunID: "scheduled-report-shared", Body: "Explicitly shared report."}, true, nil
	}))
	later(s)
	mustSweep(t, s)
	page := mustInbox(t, s, viewer)
	if page.Total != 1 || page.Items[0].Event.TenantID != "other" || page.Items[0].Event.OriginTenant != "org" {
		t.Fatalf("wrong shared origin: %+v", page)
	}
	if _, err := s.Item(ctx, Viewer{TenantID: "org", HumanID: "h1"}, page.Items[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign inbox readable: %v", err)
	}
	source.Enabled = false
	if _, err := s.SaveSource(ctx, viewer, source); err != nil {
		t.Fatal(err)
	}
	for _, delivery := range gateway.deliveries {
		if allowed, err := s.ValidateDelivery(ctx, delivery); err != nil || allowed {
			t.Fatalf("revoked cross-tenant grant accepted: %v %v", allowed, err)
		}
	}
}

func TestRecoveredReportWaitsForDurableTextAndPreservesBusinessOutcome(t *testing.T) {
	s, tasks, _, gateway, _, viewer := fixture(t)
	ctx := context.Background()
	source := publishRule(t, s, viewer, "all")
	source.IncludeFailures = true
	if _, err := s.SaveSource(ctx, viewer, source); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePreferences(ctx, viewer, Preferences{Channels: []string{"feishu"}}); err != nil {
		t.Fatal(err)
	}
	addRun(s, tasks, "recovered", scheduler.RunStatusFailed, true)
	tasks.runs[0].Result.AgentReport = nil
	ready := false
	const body = "已核对已保存记录；资料源未恢复，原任务仍未完成。"
	s.SetReportReader(recoveredReportReaderFunc(func(_ context.Context, run scheduler.ScheduledRun) (scheduler.AgentReport, bool, error) {
		if run.TenantID != viewer.TenantID || run.ScheduledRunID != "recovered" || run.RuntimeRunID != "runtime-recovered" {
			t.Fatal("reader lost original identity")
		}
		return scheduler.AgentReport{AgentID: run.AgentID, RuntimeRunID: "scheduled-report-author", Body: body}, ready, nil
	}))
	later(s)
	mustSweep(t, s)
	if mustInbox(t, s, viewer).Total != 0 || len(gateway.requests) != 0 {
		t.Fatal("missing report became a platform substitute")
	}
	ready = true
	for i := 0; i < 2; i++ {
		mustSweep(t, s)
	}
	page := mustInbox(t, s, viewer)
	if page.Total != 1 {
		t.Fatalf("report fanout count=%d", page.Total)
	}
	event := page.Items[0].Event
	if event.RunID != "recovered" || event.AgentReport.RuntimeRunID != "scheduled-report-author" || event.AgentReport.AgentID != "agent" || event.Summary != body || event.Status != "partial" {
		t.Fatalf("report replaced original execution or outcome: %+v", event)
	}
	if tasks.runs[0].Status != scheduler.RunStatusFailed || tasks.runs[0].Result.AgentReport != nil {
		t.Fatal("publication rewrote the business result")
	}
	if len(gateway.requests) != 1 || gateway.requests[0].Body != body || gateway.requests[0].Title != "" {
		t.Fatal("report rewritten or queued twice")
	}
	if _, err := s.OpenArtifact(ctx, viewer, page.Items[0].ID, event.Artifacts[0].ID); err != nil {
		t.Fatal(err)
	}
	var checks int
	if err := s.store.QueryRow(ctx, "SELECT COUNT(*) FROM notification_publication_checks WHERE source_id=? AND run_id=?", source.ID, "recovered").Scan(&checks); err != nil || checks != 0 {
		t.Fatalf("recovered publication still withheld: %d %v", checks, err)
	}
}

func TestRecoveredReportReaderFailsClosedAndDoesNotReplaceInvalidOriginal(t *testing.T) {
	for _, scenario := range []string{"missing", "foreign-agent", "empty-author", "unsafe", "reader-error", "invalid-original"} {
		t.Run(scenario, func(t *testing.T) {
			s, tasks, _, gateway, _, viewer := fixture(t)
			publishRule(t, s, viewer, "all")
			addRun(s, tasks, "boundary", scheduler.RunStatusCompleted, false)
			tasks.runs[0].Result.AgentReport = nil
			if scenario == "invalid-original" {
				tasks.runs[0].Result.AgentReport = &scheduler.AgentReport{AgentID: "foreign", RuntimeRunID: "foreign", Body: "Unverified original."}
			}
			calls := 0
			s.SetReportReader(recoveredReportReaderFunc(func(_ context.Context, r scheduler.ScheduledRun) (scheduler.AgentReport, bool, error) {
				calls++
				report := scheduler.AgentReport{AgentID: r.AgentID, RuntimeRunID: "scheduled-report-boundary", Body: "Verified saved result."}
				switch scenario {
				case "missing":
					return report, false, nil
				case "foreign-agent":
					report.AgentID = "other-agent"
				case "empty-author":
					report.RuntimeRunID = ""
				case "unsafe":
					report.Body = "Bearer private-fixture-token"
				case "reader-error":
					return report, false, context.DeadlineExceeded
				}
				return report, true, nil
			}))
			later(s)
			err := s.Sweep(context.Background())
			if scenario == "reader-error" {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("reader error lost: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if mustInbox(t, s, viewer).Total != 0 || len(gateway.requests) != 0 {
				t.Fatal("unverified report escaped")
			}
			if scenario == "invalid-original" && calls != 0 {
				t.Fatal("invalid original silently replaced")
			}
		})
	}
}

func TestRecoveredReportRetryRechecksCommittedSupplement(t *testing.T) {
	for _, change := range []string{"body", "author", "missing", "error"} {
		t.Run(change, func(t *testing.T) {
			s, tasks, _, gateway, _, viewer := fixture(t)
			publishRule(t, s, viewer, "all")
			if _, err := s.SavePreferences(context.Background(), viewer, Preferences{Channels: []string{"feishu"}}); err != nil {
				t.Fatal(err)
			}
			addRun(s, tasks, "supplement", scheduler.RunStatusCompleted, false)
			tasks.runs[0].Result.AgentReport = nil
			report := scheduler.AgentReport{AgentID: "agent", RuntimeRunID: "scheduled-report-first", Body: "Original committed supplement."}
			available := true
			var readErr error
			s.SetReportReader(recoveredReportReaderFunc(func(context.Context, scheduler.ScheduledRun) (scheduler.AgentReport, bool, error) {
				return report, available, readErr
			}))
			later(s)
			mustSweep(t, s)
			if len(gateway.deliveries) != 1 {
				t.Fatal("missing queued supplement")
			}
			switch change {
			case "body":
				report.Body = "A different public supplement."
			case "author":
				report.RuntimeRunID = "scheduled-report-other"
			case "missing":
				available = false
			case "error":
				readErr = context.DeadlineExceeded
			}
			for _, delivery := range gateway.deliveries {
				allowed, err := s.ValidateDelivery(context.Background(), delivery)
				if allowed || (change == "error" && !errors.Is(err, readErr)) || (change != "error" && err != nil) {
					t.Fatalf("changed supplement accepted: %v %v", allowed, err)
				}
			}
		})
	}
}
