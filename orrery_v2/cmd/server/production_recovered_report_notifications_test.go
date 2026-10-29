package main

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/notification"
	"orrery_v2/internal/app/scheduler"
	todoapp "orrery_v2/internal/app/todo"
	"orrery_v2/internal/gateway/binding"
	"orrery_v2/internal/gateway/outbound"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/outbox"
	"orrery_v2/internal/platform/testkit"
)

// Exercise the real report CAS store, scheduler repository, production
// notification wiring and both send guards. No sender/model may be invoked.
func TestProductionRecoveredReportUsesOneRelayPerChannelAndFencesChangedOrigin(t *testing.T) {
	ctx := context.Background()
	rt := testkit.NewTestRuntime(t)
	core, err := rt.Databases.Store(db.RoleCore)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := rt.Databases.Store(db.RoleJobs)
	if err != nil {
		t.Fatal(err)
	}
	profiles := agentcore.NewPlatformProfileRepository(rt.UnitOfWork)
	human := defaultHumanAgentProfile("human-a", "Report owner")
	human.OrgIdentity.OrganizationRef.ID = "tenant-a"
	if err := profiles.SaveProfile(ctx, human); err != nil {
		t.Fatal(err)
	}
	bindings := binding.NewStore(core)
	for _, ch := range []string{"weixin", "feishu"} {
		if _, err := bindings.UpsertVerified(ctx, binding.UpsertInput{TenantID: "tenant-a", HumanProfileID: "human-a", ChannelKey: ch, AccountID: "assistant", ExternalUserID: "owner-a"}); err != nil {
			t.Fatal(err)
		}
	}
	deliveries := outbound.NewService(jobs, bindings, scheduledResultNeverSender{})
	if err := deliveries.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	tasks := scheduler.NewRepository(rt.DBRouter)
	if err := tasks.SaveTask(ctx, scheduler.ScheduledTask{ScheduledTaskID: "task", TenantID: "tenant-a", Title: "Saved report", Target: scheduler.Target{Mode: scheduler.TargetModeAgentTask, AgentID: "report-agent"}}); err != nil {
		t.Fatal(err)
	}
	now := rt.Clock.Now().Add(-time.Hour)
	recipient := productionPersonalAssistantAttentionRecipient{TenantID: "tenant-a", HumanProfileID: "human-a"}
	owner := &productionPersonalAssistantScheduledResultSweeper{config: personalAssistantScheduledResultConfig{recipients: []productionPersonalAssistantAttentionRecipient{recipient}, since: now, replay: map[string]bool{}},
		repository: tasks, jobs: jobs, deliveries: deliveries, now: func() time.Time { return now }, replies: newProductionScheduledResultReplies(jobs),
		resolve: func(context.Context, productionPersonalAssistantAttentionRecipient) (todoapp.ViewerRef, productionPersonalAssistantWeixinTarget, bool, error) {
			return todoapp.ViewerRef{PermissionScope: []string{"scheduler.read"}}, productionPersonalAssistantWeixinTarget{TenantID: "tenant-a", OwnerID: "owner-a", AccountID: "assistant"}, true, nil
		},
	}
	reports := &productionScheduledReports{store: jobs, owner: owner}
	owner.replies.reports = reports
	if err := reports.activate(ctx); err != nil {
		t.Fatal(err)
	}
	now = rt.Clock.Now()
	run := scheduler.ScheduledRun{TenantID: "tenant-a", ScheduledTaskID: "task", ScheduledRunID: "run", TargetMode: scheduler.TargetModeAgentTask, AgentID: "report-agent", RuntimeRunID: "original-runtime", Status: scheduler.RunStatusFailed, CreatedAt: now.Add(-30 * time.Minute), CompletedAt: now.Add(-20 * time.Minute), FailureReason: "source unavailable"}
	if err := tasks.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	const body = "已查看原执行记录，资料源尚未恢复；任务仍未完成。"
	recoveryCalls := 0
	reports.recovery = scheduledReportRecoveryFunc(func(_ context.Context, origin scheduler.ScheduledRun, id string) (scheduledReportAuthor, string) {
		recoveryCalls++
		if origin.RuntimeRunID != run.RuntimeRunID || origin.AgentID != run.AgentID {
			t.Fatal("recovery changed the original identity")
		}
		return scheduledReportAuthor{RuntimeRunID: id, Body: body, Source: "supplement", TraceRef: "fixture-trace"}, ""
	})
	if err := reports.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	guard := productionPersonalAssistantNotificationGuard{scheduled: owner}
	guardCalls := 0
	deliveries.SetDeliveryValidator(func(ctx context.Context, d outbound.Delivery) (bool, error) {
		guardCalls++
		return guard.Validate(ctx, d)
	})
	gateway := productionGatewayLifecycle{manager: &productionMessageGatewayManager{bindingStore: bindings}, deliveries: deliveries}
	t.Setenv("ORRERY_PERSONAL_ASSISTANT_SCHEDULED_RESULT_RECIPIENTS", `[{"tenant_id":"tenant-a","human_profile_id":"human-a"}]`)
	t.Setenv("ORRERY_PERSONAL_ASSISTANT_SCHEDULED_RESULTS_SINCE", now.Add(-time.Hour).Format(time.RFC3339))
	t.Setenv("ORRERY_PERSONAL_ASSISTANT_SCHEDULED_RESULTS_REPLAY_RUN_IDS", "[]")
	service, err := startProductionTaskNotifications(ctx, rt, core, tasks, profiles, gateway, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	viewer := notification.Viewer{TenantID: "tenant-a", HumanID: "human-a", Manage: true}
	service.SetClock(func() time.Time { return now.Add(-time.Hour) })
	if _, err := service.SaveSource(ctx, viewer, notification.Source{TaskID: "task", Enabled: true, Audience: "all", IncludeFailures: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SavePreferences(ctx, viewer, notification.Preferences{Channels: []string{"weixin", "feishu"}}); err != nil {
		t.Fatal(err)
	}
	service.SetClock(func() time.Time { return now })
	for i := 0; i < 2; i++ {
		if err := owner.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
		if err := service.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
	}
	page, err := service.Inbox(ctx, viewer, false, 25, 0)
	if err != nil || page.Total != 1 {
		t.Fatalf("missing recovered organization report: %+v %v", page, err)
	}
	event := page.Items[0].Event
	if event.RunID != run.ScheduledRunID || event.AgentID != run.AgentID || event.AgentReport.RuntimeRunID == run.RuntimeRunID || event.Summary != body || event.Status != "failed" {
		t.Fatalf("wrong origin, author or outcome: %+v", event)
	}
	if len(page.Items[0].Pushes) != 1 || page.Items[0].Pushes[0].Channel != "feishu" {
		t.Fatal("organization fanout duplicated the private Weixin relay")
	}
	var count int
	if err := jobs.QueryRow(ctx, "SELECT COUNT(*) FROM gateway_message_deliveries").Scan(&count); err != nil || count != 2 {
		t.Fatalf("relay duplicates: %d %v", count, err)
	}
	if recoveryCalls != 1 {
		t.Fatalf("notification called report recovery: %d", recoveryCalls)
	}
	run.FailureReason = "origin revised after queueing"
	if err := tasks.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	queue := outbox.New(jobs)
	for i := 0; i < 2; i++ {
		job, found, err := queue.Claim(ctx, "report-notification-test", now.Add(time.Second))
		if err != nil || !found {
			t.Fatalf("missing queued relay %d: %v %v", i, found, err)
		}
		if err := deliveries.HandleOutbox(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	if guardCalls != 2 {
		t.Fatalf("existing guard overwritten: %d", guardCalls)
	}
	if err := jobs.QueryRow(ctx, "SELECT COUNT(*) FROM gateway_message_deliveries WHERE status='failed' AND last_error='notification is no longer actionable or allowed'").Scan(&count); err != nil || count != 2 {
		t.Fatalf("changed origin reached IO: withheld=%d %v", count, err)
	}
	if err := service.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	page, err = service.Inbox(ctx, viewer, false, 25, 0)
	if err != nil || page.Items[0].Pushes[0].Status != "cancelled" {
		t.Fatalf("revoked push still actionable: %+v %v", page, err)
	}
}
