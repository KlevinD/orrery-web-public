package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	schedulerapp "orrery_v2/internal/app/scheduler"
	gwoutbound "orrery_v2/internal/gateway/outbound"
)

type scheduledReportRecoveryFunc func(context.Context, schedulerapp.ScheduledRun, string) (scheduledReportAuthor, string)

func (f scheduledReportRecoveryFunc) Recover(ctx context.Context, run schedulerapp.ScheduledRun, id string) (scheduledReportAuthor, string) {
	return f(ctx, run, id)
}

func scheduledReportsFixture(t *testing.T) (*productionScheduledReports, *scheduledResultRepositoryStub, *time.Time) {
	t.Helper()
	sweeper, repo, _ := scheduledResultFixture(t)
	now := sweeper.now()
	sweeper.now = func() time.Time { return now }
	stage := &productionScheduledReports{store: sweeper.jobs, owner: sweeper}
	if err := stage.activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	run := scheduledAuthoredRun(sweeper)
	now = now.Add(time.Minute)
	run.CompletedAt = now.Add(-time.Second)
	run.Result.AgentReport = nil
	repo.runs = []schedulerapp.ScheduledRun{run}
	sweeper.replies.reports = stage
	return stage, repo, &now
}

func readScheduledReport(t *testing.T, stage *productionScheduledReports, run schedulerapp.ScheduledRun) scheduledReportRecord {
	t.Helper()
	key, _ := scheduledReportIdentity(run)
	raw, found, err := stage.store.GetRecord(context.Background(), scheduledReportTable, key)
	if err != nil || !found {
		t.Fatalf("missing report record: %v", err)
	}
	record, err := decodeScheduledReport(raw)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestScheduledReportsRecoveryIsSeparateAndDeliveryReusesCommittedText(t *testing.T) {
	ctx := context.Background()
	stage, repo, _ := scheduledReportsFixture(t)
	repo.runs[0].Status = schedulerapp.RunStatusFailed
	const body = "本次资料源无法访问，任务未完成。需要恢复访问权限后再继续。"
	var calls int
	stage.recovery = scheduledReportRecoveryFunc(func(_ context.Context, run schedulerapp.ScheduledRun, id string) (scheduledReportAuthor, string) {
		calls++
		if run.AgentID != "agent-a" || id == run.RuntimeRunID {
			t.Fatal("supplement lost its original Agent or distinct run identity")
		}
		return scheduledReportAuthor{RuntimeRunID: id, Body: body, Source: "supplement", TraceRef: "trace-fixture"}, ""
	})
	if err := stage.owner.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("delivery invoked report recovery")
	}
	if err := stage.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	for retry := 0; retry < 2; retry++ {
		restarted := *stage
		if err := restarted.activate(ctx); err != nil {
			t.Fatal(err)
		}
		if err := restarted.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
		if err := stage.owner.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
	}
	record := readScheduledReport(t, stage, repo.runs[0])
	if record.State != "ready" || record.Attempts != 1 || record.Body != body || record.OriginRuntimeRunID != "runtime-a" || record.AuthorRuntimeRunID == "runtime-a" || calls != 1 {
		t.Fatalf("invalid durable authorship: %+v calls=%d", record, calls)
	}
	if repo.runs[0].Result.AgentReport != nil || repo.runs[0].Status != schedulerapp.RunStatusFailed {
		t.Fatal("report recovery changed the business result")
	}
	var count int
	var saved string
	if err := stage.store.QueryRow(ctx, "SELECT COUNT(*), MAX(body) FROM gateway_message_deliveries").Scan(&count, &saved); err != nil || count != 1 || saved != body {
		t.Fatalf("delivery count=%d body=%q err=%v", count, saved, err)
	}
	recipient := stage.owner.config.recipients[0]
	delivery := gwoutbound.Delivery{TenantID: "tenant-a", HumanProfileID: "human-a", AccountID: "bot-a", ChannelKey: "weixin", Body: body,
		IdempotencyKey: personalAssistantScheduledResultKey(personalAssistantScheduledRunReference(recipient, repo.runs[0]))}
	if valid, err := stage.owner.Validate(ctx, delivery); err != nil || !valid {
		t.Fatalf("committed supplement rejected: %v", err)
	}
	// A changed origin invalidates both the report and an already queued body.
	repo.runs[0].FailureReason = "different execution evidence"
	if _, ok, err := stage.Report(ctx, repo.runs[0]); err != nil || ok {
		t.Fatalf("stale report accepted: %v", err)
	}
	if valid, err := stage.owner.Validate(ctx, delivery); err != nil || valid {
		t.Fatalf("queued supplement ignored changed origin: %v", err)
	}
}

func TestScheduledReportsExpiredLeaseRecoversWithPersistedAttemptCount(t *testing.T) {
	stage, repo, now := scheduledReportsFixture(t)
	key, fingerprint := scheduledReportIdentity(repo.runs[0])
	crashed := scheduledReportRecord{TenantID: "tenant-a", AgentID: "agent-a", ScheduledRunID: repo.runs[0].ScheduledRunID, OriginRuntimeRunID: "runtime-a", SourceSHA256: fingerprint,
		State: "generating", Attempts: 1, LeaseUntil: now.Add(-time.Second), Deadline: now.Add(20 * time.Minute), AuthorRuntimeRunID: "scheduled-report-crashed"}
	if _, err := stage.store.CreateRecord(context.Background(), scheduledReportTable, key, scheduledReportDBRecord(crashed)); err != nil {
		t.Fatal(err)
	}
	stage.recovery = scheduledReportRecoveryFunc(func(_ context.Context, _ schedulerapp.ScheduledRun, id string) (scheduledReportAuthor, string) {
		return scheduledReportAuthor{RuntimeRunID: id, Body: "已根据执行记录补全这次报告。", Source: "supplement"}, ""
	})
	if err := stage.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if record := readScheduledReport(t, stage, repo.runs[0]); record.State != "ready" || record.Attempts != 2 || record.AuthorRuntimeRunID == crashed.AuthorRuntimeRunID {
		t.Fatalf("restart reset recovery budget: %+v", record)
	}
}

func TestScheduledReportsCanonicalReportNeedsNoModel(t *testing.T) {
	stage, repo, _ := scheduledReportsFixture(t)
	run := scheduledAuthoredRun(stage.owner)
	repo.runs = []schedulerapp.ScheduledRun{run}
	stage.recovery = scheduledReportRecoveryFunc(func(context.Context, schedulerapp.ScheduledRun, string) (scheduledReportAuthor, string) {
		t.Fatal("normal report regenerated")
		return scheduledReportAuthor{}, ""
	})
	if err := stage.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	record := readScheduledReport(t, stage, run)
	if record.State != "ready" || record.Source != "agent_final" || record.Attempts != 0 || record.Body != run.Result.AgentReport.Body {
		t.Fatalf("normal report changed: %+v", record)
	}
}

func TestScheduledReportsNeverEnrollHistoryOrUnauthorizedRuns(t *testing.T) {
	for _, reason := range []string{"history", "revoked", "native"} {
		t.Run(reason, func(t *testing.T) {
			stage, repo, _ := scheduledReportsFixture(t)
			stage.recovery = scheduledReportRecoveryFunc(func(context.Context, schedulerapp.ScheduledRun, string) (scheduledReportAuthor, string) {
				t.Fatal("out of scope recovery")
				return scheduledReportAuthor{}, ""
			})
			switch reason {
			case "history":
				repo.runs[0].CompletedAt = stage.since.Add(-time.Second)
				stage.owner.config.replay[repo.runs[0].ScheduledRunID] = true
			case "revoked":
				stage.owner.config.recipients = nil
			case "native":
				repo.runs[0].TargetMode = schedulerapp.TargetModeMemoryLifecycleSweep
			}
			if err := stage.Sweep(context.Background()); err != nil {
				t.Fatal(err)
			}
			key, _ := scheduledReportIdentity(repo.runs[0])
			if _, found, err := stage.store.GetRecord(context.Background(), scheduledReportTable, key); err != nil || found {
				t.Fatalf("out of scope record created: %v", err)
			}
		})
	}
}

func TestScheduledReportsBoundAttemptsBackoffAndDeadline(t *testing.T) {
	stage, repo, now := scheduledReportsFixture(t)
	calls := 0
	stage.recovery = scheduledReportRecoveryFunc(func(context.Context, schedulerapp.ScheduledRun, string) (scheduledReportAuthor, string) {
		calls++
		return scheduledReportAuthor{}, "report_runtime_failed"
	})
	for attempt := 0; attempt < scheduledReportMaxAttempts; attempt++ {
		if err := stage.Sweep(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := stage.Sweep(context.Background()); err != nil {
			t.Fatal(err)
		}
		if calls != attempt+1 {
			t.Fatal("backoff bypassed")
		}
		*now = now.Add(scheduledReportRetry + time.Second)
	}
	if err := stage.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	record := readScheduledReport(t, stage, repo.runs[0])
	if record.State != "failed" || record.Attempts != 3 || calls != 3 || record.Body != "" {
		t.Fatalf("unbounded recovery: %+v", record)
	}
	// A new source version is still bounded by the originating completion time.
	repo.runs[0].FailureReason = "new evidence"
	*now = repo.runs[0].CompletedAt.Add(scheduledReportWindow)
	if err := stage.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 3 || readScheduledReport(t, stage, repo.runs[0]).FailureCode != "recovery_exhausted" {
		t.Fatal("expired source executed")
	}
}

func TestScheduledReportsClaimIsAtomicAndStaleAuthorCannotOverwrite(t *testing.T) {
	stage, repo, now := scheduledReportsFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	stage.recovery = scheduledReportRecoveryFunc(func(_ context.Context, _ schedulerapp.ScheduledRun, id string) (scheduledReportAuthor, string) {
		calls.Add(1)
		close(started)
		<-release
		return scheduledReportAuthor{RuntimeRunID: id, Body: "该次任务的检查结果已经确认。", Source: "supplement"}, ""
	})
	done := make(chan error, 1)
	go func() { done <- stage.Sweep(context.Background()) }()
	<-started
	if err := stage.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate model invocation under lease")
	}
	// Simulate a newer process taking over an expired lease. The first process
	// must fail its final compare-and-swap, even if its model later returns.
	key, _ := scheduledReportIdentity(repo.runs[0])
	raw, _, _ := stage.store.GetRecord(context.Background(), scheduledReportTable, key)
	record, _ := decodeScheduledReport(raw)
	record.AuthorRuntimeRunID, record.Attempts = "scheduled-report-newer", 2
	record.LeaseUntil = now.Add(time.Minute)
	if ok, err := stage.store.CompareAndSwapRecord(context.Background(), scheduledReportTable, key, raw, scheduledReportDBRecord(record)); err != nil || !ok {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := readScheduledReport(t, stage, repo.runs[0]); got.State != "generating" || got.AuthorRuntimeRunID != "scheduled-report-newer" || got.Body != "" {
		t.Fatalf("stale author overwrote lease: %+v", got)
	}
}

func TestScheduledReportsOriginChangeDuringRecoveryWithholdsBody(t *testing.T) {
	stage, repo, _ := scheduledReportsFixture(t)
	origin := repo.runs[0]
	stage.recovery = scheduledReportRecoveryFunc(func(_ context.Context, _ schedulerapp.ScheduledRun, id string) (scheduledReportAuthor, string) {
		repo.runs[0].Status = schedulerapp.RunStatusFailed
		return scheduledReportAuthor{RuntimeRunID: id, Body: "任务检查已完成。", Source: "supplement"}, ""
	})
	if err := stage.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if record := readScheduledReport(t, stage, origin); record.State != "withheld" || record.FailureCode != "origin_or_authority_changed" || record.Body != "" {
		t.Fatalf("changed source accepted: %+v", record)
	}
}

func TestScheduledReportsCannotCommitAfterDeadlineOrAuthorityRevocation(t *testing.T) {
	for _, mode := range []string{"deadline", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			stage, repo, now := scheduledReportsFixture(t)
			origin := repo.runs[0]
			stage.recovery = scheduledReportRecoveryFunc(func(_ context.Context, _ schedulerapp.ScheduledRun, id string) (scheduledReportAuthor, string) {
				if mode == "deadline" {
					*now = origin.CompletedAt.Add(scheduledReportWindow)
				} else {
					stage.owner.config.recipients = nil
				}
				return scheduledReportAuthor{RuntimeRunID: id, Body: "这次报告已经补全。", Source: "supplement"}, ""
			})
			if err := stage.Sweep(context.Background()); err != nil {
				t.Fatal(err)
			}
			record := readScheduledReport(t, stage, origin)
			if record.State == "ready" || record.Body != "" || record.FailureCode == "" {
				t.Fatalf("late or unauthorized report accepted: %+v", record)
			}
		})
	}
}
