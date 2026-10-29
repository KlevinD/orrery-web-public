package main

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/notification"
	"orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/gateway/binding"
	"orrery_v2/internal/gateway/outbound"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/outbox"
	"orrery_v2/internal/platform/testkit"
)

func TestProductionTaskNotificationWiringReservesOnlyActiveAssistantRelay(t *testing.T) {
	for _, tc := range []struct {
		name                                                    string
		configured, beforeActivation, replay, disabled, reserve bool
	}{
		{"active", true, false, false, false, true},
		{"before_activation", true, true, false, false, false},
		{"explicit_replay", true, true, true, false, true},
		{"workers_disabled", true, false, false, true, false},
		{"unconfigured", false, false, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
			profile := defaultHumanAgentProfile("human-a", "Report owner")
			profile.OrgIdentity.OrganizationRef.ID = "tenant-a"
			if err := profiles.SaveProfile(ctx, profile); err != nil {
				t.Fatal(err)
			}
			bindings := binding.NewStore(core)
			for _, ch := range []string{"weixin", "feishu"} {
				if _, err := bindings.UpsertVerified(ctx, binding.UpsertInput{TenantID: "tenant-a", HumanProfileID: "human-a", ChannelKey: ch, AccountID: "assistant", ExternalUserID: "owner"}); err != nil {
					t.Fatal(err)
				}
			}
			deliveries := outbound.NewService(jobs, bindings, scheduledResultNeverSender{})
			priorGuardCalls := 0
			deliveries.SetDeliveryValidator(func(context.Context, outbound.Delivery) (bool, error) { priorGuardCalls++; return true, nil })
			gateway := productionGatewayLifecycle{manager: &productionMessageGatewayManager{bindingStore: bindings}, deliveries: deliveries}
			tasks := scheduler.NewRepository(rt.DBRouter)
			if err := tasks.SaveTask(ctx, scheduler.ScheduledTask{ScheduledTaskID: "task", TenantID: "tenant-a", Title: "Daily report", Target: scheduler.Target{Mode: scheduler.TargetModeAgentTask, AgentID: "report-agent"}}); err != nil {
				t.Fatal(err)
			}
			now := rt.Clock.Now()
			since := now.Add(-45 * time.Minute)
			if tc.beforeActivation {
				since = now.Add(-10 * time.Minute)
			}
			recipients := "[]"
			if tc.configured {
				recipients = `[{"tenant_id":"tenant-a","human_profile_id":"human-a"}]`
			}
			t.Setenv("ORRERY_PERSONAL_ASSISTANT_SCHEDULED_RESULT_RECIPIENTS", recipients)
			t.Setenv("ORRERY_PERSONAL_ASSISTANT_SCHEDULED_RESULTS_SINCE", since.Format(time.RFC3339))
			replay := "[]"
			if tc.replay {
				replay = `["run"]`
			}
			t.Setenv("ORRERY_PERSONAL_ASSISTANT_SCHEDULED_RESULTS_REPLAY_RUN_IDS", replay)
			service, err := startProductionTaskNotifications(ctx, rt, core, tasks, profiles, gateway, nil, tc.disabled)
			if err != nil {
				t.Fatal(err)
			}
			service.SetClock(func() time.Time { return now.Add(-time.Hour) })
			viewer := notification.Viewer{TenantID: "tenant-a", HumanID: "human-a", Manage: true}
			source, err := service.SaveSource(ctx, viewer, notification.Source{TaskID: "task", Enabled: true, Audience: "all"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.SavePreferences(ctx, viewer, notification.Preferences{Channels: []string{"weixin", "feishu"}}); err != nil {
				t.Fatal(err)
			}
			run := scheduler.ScheduledRun{ScheduledRunID: "run", ScheduledTaskID: "task", TenantID: "tenant-a", TargetMode: scheduler.TargetModeAgentTask, AgentID: "report-agent", RuntimeRunID: "runtime-run", Status: scheduler.RunStatusCompleted, CreatedAt: now.Add(-30 * time.Minute), CompletedAt: now.Add(-20 * time.Minute), Result: scheduler.RunResult{Summary: "Internal receipt", AgentReport: &scheduler.AgentReport{AgentID: "report-agent", RuntimeRunID: "runtime-run", Body: "Three sources were checked."}}}
			if err := tasks.SaveRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			service.SetClock(func() time.Time { return now })
			for i := 0; i < 2; i++ {
				if err := service.Sweep(ctx); err != nil {
					t.Fatal(err)
				}
			}
			page, err := service.Inbox(ctx, viewer, false, 25, 0)
			wantPushes := 2
			if tc.reserve {
				wantPushes = 1
			}
			if err != nil || page.Total != 1 || len(page.Items[0].Pushes) != wantPushes {
				t.Fatalf("wiring/fanout: %+v %v", page, err)
			}
			if tc.reserve && page.Items[0].Pushes[0].Channel != "feishu" {
				t.Fatal("assistant reservation blocked the wrong channel")
			}
			channels, err := service.Channels(ctx, viewer)
			if err != nil || len(channels) != 2 {
				t.Fatalf("binding reader not wired: %v %v", channels, err)
			}
			// Revoke the source after queueing; the production validator must stop IO.
			source.Enabled = false
			if _, err := service.SaveSource(ctx, viewer, source); err != nil {
				t.Fatal(err)
			}
			queue := outbox.New(jobs)
			job, found, err := queue.Claim(ctx, "notification-wiring", now.Add(time.Second))
			if err != nil || !found {
				t.Fatalf("missing durable delivery: %v %v", found, err)
			}
			if err := deliveries.HandleOutbox(ctx, job); err != nil {
				t.Fatal(err)
			}
			if priorGuardCalls != 1 {
				t.Fatalf("existing gateway guard overwritten: calls=%d", priorGuardCalls)
			}
		})
	}
}
