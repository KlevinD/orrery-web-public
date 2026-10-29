package main

import (
	"context"
	"io"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/platform/testkit"
)

func TestScheduledBlockedReportIsDownloadableWithoutCompletingContract(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	input := scheduledMaterializerTestInput(t, "/missing/completed-report.md")
	input.RuntimeOptions.ProviderSpecific["scheduled_run_id"] = "scheduled-blocked-report"
	input.RuntimeOptions.ProviderSpecific["scheduled_task_id"] = "scheduled-task-blocked-report"
	archiver := newProductionSchedulerArtifactStore("", env.Config.FileStore, env.FileStore, nil)
	normalizer := missionTaskAgentResultNormalizer{scheduledArtifacts: archiver, workspaceFiles: func(context.Context) []missionResultWorkspaceFile {
		t.Fatal("blocked reports must not read agent-selected workspace files")
		return nil
	}}
	response := agentcore.RuntimeResponse{RunID: input.RunID, Status: agentcore.RuntimeStatusCompleted, Messages: []agentcore.RuntimeMessage{{
		Type: agentcore.RuntimeMessageText, Status: agentcore.RuntimeMessageStatusFinal, Content: materializerManifestOutput(
			"<think>private analysis</think>\nFAQ input could not be read. Bearer private-test-token",
			`{"version":1,"outcome":"blocked","summary":"Input read failed.","deliverables":[],"evidence":[]}`),
	}}}
	result, err := normalizer.NormalizeContext(ctx, input, response, env.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	result.Blocker.Summary = "<think>private blocker details</think>\nInput read failed.\ndocument_id=private-doc"
	run := runtimedaemon.AgentRun{RunID: input.RunID, AgentID: result.AgentID, MissionID: input.Task.MissionID, TaskID: input.Task.ID,
		Request: agentcore.RuntimeRequest{TenantID: input.TenantID, RuntimeOptions: input.RuntimeOptions}}
	workerSink := &capturingSchedulerRunLifecycle{}
	if err := (productionAgentRunWorker{scheduler: workerSink}).completeCommittedScheduledResult(ctx, run, result, response, "worker"); err != nil {
		t.Fatal(err)
	}
	if workerSink.blockedCalls != 1 || workerSink.completedCalls != 0 || len(workerSink.blockedResult.Artifacts) != 1 || len(result.Deliverables) != 0 || len(result.Evidence) != 0 {
		t.Fatalf("blocked report changed acceptance or disappeared: result=%+v sink=%+v", result, workerSink)
	}
	artifact := workerSink.blockedResult.Artifacts[0]
	content, err := archiver.OpenFinalizedArtifact(ctx, input.TenantID, "scheduled-blocked-report", artifact)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(content.Reader)
	content.Reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "Input read failed.") || !strings.Contains(text, "FAQ input could not be read.") || strings.Contains(text, "private analysis") || strings.Contains(text, "private blocker details") || strings.Contains(text, "private-test-token") || strings.Contains(text, "private-doc") || strings.Contains(text, "orrery-agent-result") {
		t.Fatalf("unexpected public blocked report: %s", text)
	}
	for _, identity := range [][2]string{{"other-tenant", "scheduled-blocked-report"}, {input.TenantID, "other-run"}} {
		if content, err := archiver.OpenFinalizedArtifact(ctx, identity[0], identity[1], artifact); err == nil {
			content.Reader.Close()
			t.Fatal("foreign identity could read blocked report")
		}
	}
	for _, tenant := range []string{"other-tenant", input.TenantID} {
		foreign := run
		foreign.Request.TenantID = tenant
		foreign.Request.RuntimeOptions.ProviderSpecific = map[string]string{"scheduled_run_id": "foreign-run"}
		if _, err := scheduledRunResultFromCommittedAgentResult(foreign, result); err == nil {
			t.Fatal("foreign report accepted during result recovery")
		}
	}
}

func TestScheduledBlockedReportRequiresConfiguredArchiveAndIdentity(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	input := materializerTestInput()
	result := agentcore.AgentResult{Blocker: &agentcore.Blocker{Summary: "Blocked"}}
	for _, normalizer := range []missionTaskAgentResultNormalizer{{}, {scheduledArtifacts: newProductionSchedulerArtifactStore("", env.Config.FileStore, env.FileStore, nil)}} {
		if _, err := normalizer.archiveScheduledBlockedReport(context.Background(), input, result, "Blocked"); err == nil {
			t.Fatal("missing archive or scheduled run identity was accepted")
		}
	}
}
