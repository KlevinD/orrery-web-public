package main

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/platform/testkit"
)

func TestScheduledReportWiringRequiresRecipientsDurabilityAndEnabledWorkers(t *testing.T) {
	for _, scenario := range []string{"enabled", "workers-disabled", "no-recipients", "no-durable-recorder", "no-runtime"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("ORRERY_PERSONAL_ASSISTANT_APPROVAL_DELEGATIONS", "")
			t.Setenv("ORRERY_PERSONAL_ASSISTANT_ATTENTION_RECIPIENTS", "")
			t.Setenv("ORRERY_PERSONAL_ASSISTANT_SCHEDULED_RESULTS_SINCE", "2026-10-18T00:00:00Z")
			t.Setenv("ORRERY_PERSONAL_ASSISTANT_SCHEDULED_RESULTS_REPLAY_RUN_IDS", "")
			recipients := `[{"tenant_id":"tenant-a","human_profile_id":"human-a"}]`
			if scenario == "no-recipients" {
				recipients = ""
			}
			t.Setenv("ORRERY_PERSONAL_ASSISTANT_SCHEDULED_RESULT_RECIPIENTS", recipients)
			e, _, _, _, auth := delegatedApprovalFixture(t)
			w := productionPersonalAssistantNotificationWiring{
				runtime: testkit.NewTestRuntime(t), core: e.core, profiles: e.authority.profiles,
				accounts: auth, scopes: auth, discussions: e.notices, hil: e.decisions,
				workflow:       hil.NewPlatformRepository(e.core),
				reportRegistry: scheduledReportRegistrySpy{runtime: &scheduledReportRuntimeSpy{}},
				reportRecorder: agentcore.StoreTraceRecorder{Store: &scheduledReportObjects{objects: map[string][]byte{}}},
			}
			if scenario == "no-durable-recorder" {
				w.reportRecorder = agentcore.NoopTraceRecorder{}
			}
			if scenario == "no-runtime" {
				w.reportRegistry = nil
			}
			if err := w.Prepare(context.Background(), scenario == "workers-disabled"); err != nil {
				t.Fatal(err)
			}
			reports := w.scheduled.replies.reports
			if (reports != nil) != (scenario == "enabled") {
				t.Fatal("report stage activated without its prerequisites")
			}
			if reports != nil {
				recovery, ok := reports.recovery.(*productionScheduledReportRecovery)
				if !ok || recovery.jobs == nil || recovery.files == nil || recovery.profiles == nil || recovery.registry == nil || recovery.traces == nil || reports.since.IsZero() {
					t.Fatal("production report dependencies or durable activation missing")
				}
			}
		})
	}
}
