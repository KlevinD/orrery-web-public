package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/db"
)

func TestScheduledReportRecoveryRejectsIncompleteTraceAndMissingModelIdentity(t *testing.T) {
	for _, scenario := range []string{"partial", "running", "missing-model", "foreign-profile"} {
		t.Run(scenario, func(t *testing.T) {
			recovery, run, objects := scheduledReportSourceFixture(t)
			spy := &scheduledReportRuntimeSpy{}
			recovery.registry = scheduledReportRegistrySpy{runtime: spy}
			key := "agent-runs/runtime-a/trace-v2/runtime-trace.json"
			var trace map[string]any
			if err := json.Unmarshal(objects.objects[key], &trace); err != nil {
				t.Fatal(err)
			}
			expected := "source_trace_not_terminal"
			switch scenario {
			case "partial":
				trace["partial_output"] = true
			case "running":
				trace["status"] = "running"
			default:
				job := recovery.jobs.(scheduledReportJobFixture).job
				original, err := runtimedaemon.DecodeAgentRunPayload(job.Payload)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "missing-model" {
					delete(original.Request.RuntimeOptions.ProviderSpecific, "llm_profile_id")
					expected = "report_model_binding_missing"
				} else {
					original.AgentProfileID = "another-agent"
					recovery.profiles = scheduledReportProfileFixture{agentcore.AgentProfile{ID: "another-agent", Enabled: true}}
					expected = "source_identity_mismatch"
				}
				job.Payload, _ = json.Marshal(original)
				recovery.jobs = scheduledReportJobFixture{job}
			}
			objects.objects[key], _ = json.Marshal(trace)
			author, failure := recovery.Recover(context.Background(), run, "scheduled-report-boundary-fixture")
			if failure != expected || spy.calls != 0 || author.Body != "" {
				t.Fatalf("unverified source ran a model: failure=%s expected=%s calls=%d", failure, expected, spy.calls)
			}
		})
	}
}

func TestScheduledReportsFileRestartPreservesActivationAndCommittedReport(t *testing.T) {
	ctx := context.Background()
	stage, repo, now := scheduledReportsFixture(t)
	config := db.Config{Driver: db.DriverFile, URL: "file://" + filepath.Join(t.TempDir(), "reports.db")}
	store, err := db.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	stage.store = store
	if err := stage.activate(ctx); err != nil {
		t.Fatal(err)
	}
	activation := stage.since
	repo.runs[0].CompletedAt = now.Add(time.Second)
	*now = now.Add(2 * time.Second)
	calls := 0
	stage.recovery = scheduledReportRecoveryFunc(func(_ context.Context, _ schedulerapp.ScheduledRun, id string) (scheduledReportAuthor, string) {
		calls++
		return scheduledReportAuthor{RuntimeRunID: id, Body: "原任务未完成；这是基于已保存记录的报告。", Source: "supplement"}, ""
	})
	if err := stage.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	first := readScheduledReport(t, stage, repo.runs[0])
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = db.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	stage.store = store
	*now = now.Add(time.Hour)
	if err := stage.activate(ctx); err != nil {
		t.Fatal(err)
	}
	if !stage.since.Equal(activation) {
		t.Fatal("restart moved the activation fence")
	}
	if err := stage.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	report, ok, err := stage.Report(ctx, repo.runs[0])
	if err != nil || !ok || calls != 1 || report.Body != first.Body || report.RuntimeRunID != first.AuthorRuntimeRunID {
		t.Fatalf("restart lost committed authorship: %+v ok=%v calls=%d err=%v", report, ok, calls, err)
	}
	key, _ := scheduledReportIdentity(repo.runs[0])
	first.Body += " tampered"
	if err := store.PutRecord(ctx, scheduledReportTable, key, scheduledReportDBRecord(first)); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := stage.Report(ctx, repo.runs[0]); err != nil || ok {
		t.Fatalf("tampered report accepted: %v", err)
	}
}
