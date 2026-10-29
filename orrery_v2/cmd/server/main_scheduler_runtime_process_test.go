package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	schedulerapp "orrery_v2/internal/app/scheduler"
)

func TestLiveScheduledAgentTaskUsesConfiguredOpenClawRuntime(t *testing.T) {
	if !strings.EqualFold(os.Getenv("ORRERY_SCHEDULER_LIVE_E2E"), "true") {
		t.Skip("set ORRERY_SCHEDULER_LIVE_E2E=true to run the live scheduler runtime e2e")
	}

	openClawAgentID := strings.TrimSpace(os.Getenv("ORRERY_SCHEDULER_LIVE_AGENT_ID"))
	if openClawAgentID == "" {
		openClawAgentID = "dafu"
	}
	root := t.TempDir()
	t.Setenv(testConfigOverridesEnv, "{}")
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", persistentProductionTestDatabaseURLs(root))
	setTestConfigOverride(t, "discussion.agent.runtime_kind", "openclaw")
	runtimeMode := strings.ToLower(strings.TrimSpace(os.Getenv("ORRERY_SCHEDULER_LIVE_OPENCLAW_MODE")))
	if runtimeMode == "" {
		runtimeMode = "local"
	}
	runtimeOptions := map[string]string{"agent_id": openClawAgentID}
	var tracePath, stdoutPath string
	switch runtimeMode {
	case "local":
		realOpenClaw, err := exec.LookPath("openclaw")
		if err != nil {
			t.Fatalf("locate openclaw: %v", err)
		}
		tracePath = filepath.Join(root, "openclaw-invocations.log")
		stdoutPath = filepath.Join(root, "openclaw-agent.stdout")
		wrapperPath := writeLiveOpenClawTraceWrapper(t, root)
		t.Setenv("ORRERY_SCHEDULER_REAL_OPENCLAW", realOpenClaw)
		t.Setenv("ORRERY_SCHEDULER_E2E_TRACE", tracePath)
		t.Setenv("ORRERY_SCHEDULER_E2E_STDOUT", stdoutPath)
		runtimeOptions["executable"] = wrapperPath
	case "remote_ws":
		const tokenEnv = "ORRERY_SCHEDULER_LIVE_GATEWAY_TOKEN"
		if strings.TrimSpace(os.Getenv(tokenEnv)) == "" {
			t.Fatalf("%s is required for remote_ws", tokenEnv)
		}
		gatewayURL := strings.TrimSpace(os.Getenv("ORRERY_SCHEDULER_LIVE_GATEWAY_URL"))
		if gatewayURL == "" {
			gatewayURL = "wss://127.0.0.1:18789"
		}
		runtimeOptions["mode"] = runtimeMode
		runtimeOptions["gateway_url"] = gatewayURL
		runtimeOptions["token_env"] = tokenEnv
	default:
		t.Fatalf("unsupported OpenClaw live test mode %q", runtimeMode)
	}
	runtimeConfig, err := json.Marshal(runtimeOptions)
	if err != nil {
		t.Fatalf("encode OpenClaw runtime config: %v", err)
	}
	setTestConfigOverride(t, "discussion.agent.runtime_config", string(runtimeConfig))
	setTestConfigOverride(t, "discussion.agent.timeout", "180s")
	configPath := selectTestServerConfig(t)

	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()
	handler, cleanup, err := newServerHandlerWithConfig(serverCtx, false, configPath)
	if err != nil {
		t.Fatalf("start isolated production handler: %v", err)
	}
	defer cleanup()

	disabled := false
	createBody, err := json.Marshal(schedulerapp.CreateTaskRequest{
		Title:       "Orrery scheduler live smoke test",
		Description: "Isolated local end-to-end scheduler and OpenClaw runtime check",
		Schedule: schedulerapp.Schedule{
			Type:     schedulerapp.ScheduleTypeOnce,
			RunAt:    time.Now().UTC().Add(24 * time.Hour),
			Timezone: "UTC",
		},
		Target: schedulerapp.Target{
			Mode:     schedulerapp.TargetModeAgentTask,
			AgentID:  "lead-agent",
			TaskGoal: "Verify the Orrery scheduler can execute an Agent through OpenClaw.",
			Prompt:   "这是隔离的定时任务运行链路自检。不要调用工具，不要读写文件，不要发送消息，只回复 ORRERY_SCHEDULER_SMOKE_OK。",
		},
		Policy: schedulerapp.Policy{
			Concurrency: schedulerapp.ConcurrencySkipIfRunning,
			MaxRetries:  0,
		},
		Enabled:        &disabled,
		IdempotencyKey: "orrery-scheduler-live-smoke-task",
	})
	if err != nil {
		t.Fatalf("encode scheduled task: %v", err)
	}
	create := httptest.NewRequest(http.MethodPost, "/api/scheduled-tasks", strings.NewReader(string(createBody)))
	create.Header.Set("Content-Type", "application/json")
	create.Header.Set("X-User-ID", "superadmin")
	create.Header.Set("X-Viewer-Kind", "user")
	create.Header.Set("X-Agent-Profile-ID", "human-superadmin")
	create.Header.Set("X-Tenant-ID", "system")
	create.Header.Set("Idempotency-Key", "orrery-scheduler-live-smoke-task")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, create)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create smoke task status=%d body=%s", rec.Code, rec.Body.String())
	}
	var task schedulerapp.ScheduledTask
	if err := json.NewDecoder(rec.Body).Decode(&task); err != nil {
		t.Fatalf("decode smoke task: %v", err)
	}
	if task.Enabled || task.Status != schedulerapp.TaskStatusDisabled {
		t.Fatalf("smoke task must remain disabled: %+v", task)
	}

	runNow := httptest.NewRequest(http.MethodPost, "/api/scheduled-tasks/"+task.ScheduledTaskID+"/run-now", strings.NewReader(`{"force":true}`))
	runNow.Header.Set("Content-Type", "application/json")
	runNow.Header.Set("X-User-ID", "superadmin")
	runNow.Header.Set("X-Viewer-Kind", "user")
	runNow.Header.Set("X-Agent-Profile-ID", "human-superadmin")
	runNow.Header.Set("X-Tenant-ID", "system")
	runNow.Header.Set("Idempotency-Key", "orrery-scheduler-live-smoke-run")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, runNow)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("run smoke task status=%d body=%s", rec.Code, rec.Body.String())
	}
	var prepared schedulerapp.ScheduledRun
	if err := json.NewDecoder(rec.Body).Decode(&prepared); err != nil {
		t.Fatalf("decode prepared smoke run: %v", err)
	}
	if prepared.ScheduledRunID == "" {
		t.Fatalf("prepared smoke run missing id: %+v", prepared)
	}

	completed := waitForScheduledRun(t, handler, task.ScheduledTaskID, prepared.ScheduledRunID, 4*time.Minute)
	if completed.Status != schedulerapp.RunStatusCompleted {
		t.Fatalf("scheduled run ended with status=%s reason=%q", completed.Status, completed.FailureReason)
	}

	if runtimeMode == "local" {
		trace, err := os.ReadFile(tracePath)
		if err != nil {
			t.Fatalf("read OpenClaw invocation trace: %v", err)
		}
		traceText := string(trace)
		for _, expected := range []string{"agent", "--local", "--json", "--agent " + openClawAgentID, "ORRERY_SCHEDULER_SMOKE_OK"} {
			if !strings.Contains(traceText, expected) {
				t.Fatalf("OpenClaw invocation trace missing %q: %s", expected, traceText)
			}
		}
		stdout, err := os.ReadFile(stdoutPath)
		if err != nil {
			t.Fatalf("read OpenClaw stdout: %v", err)
		}
		if !strings.Contains(string(stdout), "ORRERY_SCHEDULER_SMOKE_OK") {
			t.Fatalf("OpenClaw response missing smoke marker: %s", string(stdout))
		}
	} else {
		tracePath := filepath.Join(root, "data", "artifacts", "agent-runs", completed.CommandID, "runtime-trace.json")
		trace, err := os.ReadFile(tracePath)
		if err != nil {
			t.Fatalf("read persisted Agent runtime trace: %v", err)
		}
		var persisted struct {
			Messages []struct {
				Content string `json:"Content"`
			} `json:"messages"`
			ProviderMetadata map[string]string `json:"provider_metadata"`
		}
		if err := json.Unmarshal(trace, &persisted); err != nil {
			t.Fatalf("decode persisted Agent runtime trace: %v", err)
		}
		var response strings.Builder
		for _, message := range persisted.Messages {
			response.WriteString(message.Content)
		}
		if !strings.Contains(response.String(), "ORRERY_SCHEDULER_SMOKE_OK") {
			t.Fatalf("persisted Agent response missing smoke marker: %s", response.String())
		}
		if persisted.ProviderMetadata["transport"] != "remote_ws" {
			t.Fatalf("persisted Agent transport = %q, want remote_ws", persisted.ProviderMetadata["transport"])
		}
	}
	t.Logf("scheduled_task_id=%s scheduled_run_id=%s status=%s agent_id=%s runtime_mode=%s", task.ScheduledTaskID, completed.ScheduledRunID, completed.Status, completed.AgentID, runtimeMode)
}

func waitForScheduledRun(t *testing.T, handler http.Handler, taskID, runID string, timeout time.Duration) schedulerapp.ScheduledRun {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var latest schedulerapp.ScheduledRun
	for time.Now().Before(deadline) {
		req := httptest.NewRequest(http.MethodGet, "/api/scheduled-tasks/"+taskID+"/runs?limit=10", nil)
		req.Header.Set("X-User-ID", "superadmin")
		req.Header.Set("X-Viewer-Kind", "user")
		req.Header.Set("X-Agent-Profile-ID", "human-superadmin")
		req.Header.Set("X-Tenant-ID", "system")
		rec := httptest.NewRecorder()
		serveServerTestHTTP(t, handler, rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("list smoke runs status=%d body=%s", rec.Code, rec.Body.String())
		}
		var page schedulerapp.ScheduledRunPage
		if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
			t.Fatalf("decode smoke runs: %v", err)
		}
		for _, run := range page.Runs {
			if run.ScheduledRunID != runID {
				continue
			}
			latest = run
			if run.Status == schedulerapp.RunStatusCompleted || run.Status == schedulerapp.RunStatusFailed || run.Status == schedulerapp.RunStatusSkipped {
				return run
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("scheduled run did not finish within %s; latest=%+v", timeout, latest)
	return schedulerapp.ScheduledRun{}
}

func writeLiveOpenClawTraceWrapper(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "openclaw-trace-wrapper")
	body := `#!/usr/bin/env bash
set -o pipefail
printf '%s\n' "$*" >> "$ORRERY_SCHEDULER_E2E_TRACE"
if [[ "${1:-}" == "agent" ]]; then
  "$ORRERY_SCHEDULER_REAL_OPENCLAW" "$@" | tee -a "$ORRERY_SCHEDULER_E2E_STDOUT"
  exit "${PIPESTATUS[0]}"
fi
exec "$ORRERY_SCHEDULER_REAL_OPENCLAW" "$@"
`
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("write OpenClaw trace wrapper: %v", err)
	}
	return path
}
