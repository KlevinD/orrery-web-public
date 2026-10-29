package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/app/discussionsession"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/platform"
)

func TestProductionHandlerQueuesMissionAgentRunIntoExecutionQueue(t *testing.T) {
	root := t.TempDir()
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", productionTestDatabaseURLs(root))
	rt, handler := newProductionRuntimeHandlerForTest(t)

	create := httptest.NewRequest(http.MethodPost, "/api/missions", jsonBody(`{"title":"Queued agent run","final_goal":"Execute a task through the platform runtime queue"}`))
	create.Header.Set("X-User-ID", "local-user")
	create.Header.Set("X-Viewer-Kind", "user")
	create.Header.Set("X-Agent-Profile-ID", "human-runtime-user")
	create.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	create.Header.Set("Idempotency-Key", "mission-agent-run-queue")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, create)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create mission status = %d body = %s", rec.Code, rec.Body.String())
	}
	var mission missioncontrol.MissionDTO
	if err := json.NewDecoder(rec.Body).Decode(&mission); err != nil {
		t.Fatalf("decode mission: %v", err)
	}

	createTask := httptest.NewRequest(http.MethodPost, "/api/missions/"+mission.MissionID+"/tasks", jsonBody(`{"assignee_id":"`+defaultCoordinatorAgentID+`","goal":"Prepare runtime execution","worker_personal_goal":"Run through the backend execution queue"}`))
	createTask.Header.Set("X-User-ID", "local-user")
	createTask.Header.Set("X-Viewer-Kind", "user")
	createTask.Header.Set("X-Agent-Profile-ID", "human-runtime-user")
	createTask.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	createTask.Header.Set("Idempotency-Key", "task-agent-run-queue")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, createTask)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create task status = %d body = %s", rec.Code, rec.Body.String())
	}
	var task missioncontrol.TaskCardDTO
	if err := json.NewDecoder(rec.Body).Decode(&task); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	model, err := missioncontrol.NewDBProjectionRepository(rt.DBRouter).GetMissionControl(context.Background(), missioncore.MissionID(mission.MissionID))
	if err != nil {
		t.Fatalf("mission projection before agent run: %v", err)
	}
	conversation, err := discussionsession.NewRepository(rt.DBRouter).GetConversation(context.Background(), model.Discussion.ConversationID)
	if err != nil {
		t.Fatalf("canonical conversation before agent run: %v", err)
	}
	foundAssignee := false
	for _, member := range conversation.Members {
		if member.MemberKind == "agent" && member.MemberID == defaultCoordinatorAgentID {
			foundAssignee = true
			wantSource := "mission-task-assignee:global/" + mission.MissionID
			if member.ManagedSource != wantSource {
				t.Fatalf("task assignee managed source = %q, want %q", member.ManagedSource, wantSource)
			}
		}
	}
	if !foundAssignee {
		t.Fatalf("task assignee is not a member of canonical conversation %q", conversation.ConversationID)
	}

	start := httptest.NewRequest(http.MethodPost, "/api/missions/"+mission.MissionID+"/tasks/"+task.TaskID+"/agent-runs", jsonBody(`{"agent_id":"`+defaultCoordinatorAgentID+`","runtime_kind":"codex","prompt":"Execute from the production runtime queue"}`))
	start.Header.Set("X-User-ID", "local-user")
	start.Header.Set("X-Viewer-Kind", "user")
	start.Header.Set("X-Agent-Profile-ID", "human-runtime-user")
	start.Header.Set("X-Permission-Scope", "mission.read,mission.write,agent.run")
	start.Header.Set("Idempotency-Key", "agent-run-queue")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, start)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start agent run status = %d body = %s", rec.Code, rec.Body.String())
	}
	var receipt missioncontrol.CommandReceiptDTO
	if err := json.NewDecoder(rec.Body).Decode(&receipt); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	job, ok, err := rt.ExecutionQueue.GetStatus(context.Background(), receipt.CommandID)
	if err != nil || !ok {
		t.Fatalf("execution queue status ok=%v err=%v", ok, err)
	}
	if job.Topic != runtimedaemon.AgentRunTopic || string(job.Status) != "queued" {
		t.Fatalf("queued job = %+v, want agent runtime queued job", job)
	}

	opsReq := httptest.NewRequest(http.MethodGet, "/api/missions/"+mission.MissionID+"/ops", nil)
	opsReq.Header.Set("X-User-ID", "local-user")
	opsReq.Header.Set("X-Viewer-Kind", "user")
	opsReq.Header.Set("X-Agent-Profile-ID", "human-runtime-user")
	opsReq.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, opsReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("ops status = %d body = %s", rec.Code, rec.Body.String())
	}
	var ops missioncontrol.MissionOpsViewDTO
	if err := json.NewDecoder(rec.Body).Decode(&ops); err != nil {
		t.Fatalf("decode ops: %v", err)
	}
	if len(ops.RuntimeSessions) != 1 || ops.RuntimeSessions[0].RunID != receipt.CommandID || ops.RuntimeSessions[0].Status != "queued" {
		t.Fatalf("runtime sessions = %+v, want queued session for %s", ops.RuntimeSessions, receipt.CommandID)
	}
}

func TestProductionHandlerCancelsQueuedMissionAgentRun(t *testing.T) {
	root := t.TempDir()
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", productionTestDatabaseURLs(root))
	rt, handler := newProductionRuntimeHandlerForTest(t)
	mission, _, receipt := createMissionTaskAndStartAgentRun(t, handler, "managed")

	cancel := httptest.NewRequest(http.MethodPost, "/api/missions/"+mission.MissionID+"/agent-runs/"+receipt.CommandID+"/cancel", jsonBody(`{"reason":"operator stopped duplicate work"}`))
	cancel.Header.Set("X-User-ID", "local-user")
	cancel.Header.Set("X-Viewer-Kind", "user")
	cancel.Header.Set("X-Agent-Profile-ID", "human-runtime-user")
	cancel.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	cancel.Header.Set("Idempotency-Key", "agent-run-cancel")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, cancel)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("cancel agent run status = %d body = %s", rec.Code, rec.Body.String())
	}

	status, ok, err := rt.ExecutionQueue.GetStatus(context.Background(), receipt.CommandID)
	if err != nil || !ok {
		t.Fatalf("execution queue status ok=%v err=%v", ok, err)
	}
	if string(status.Status) != "cancelled" || status.FailureReason != "operator stopped duplicate work" {
		t.Fatalf("execution status = %+v, want cancelled", status)
	}
	ops := getMissionOpsForTest(t, handler, mission.MissionID)
	if len(ops.RuntimeSessions) != 1 || ops.RuntimeSessions[0].RunID != receipt.CommandID || ops.RuntimeSessions[0].Status != "cancelled" {
		t.Fatalf("runtime sessions = %+v, want cancelled session", ops.RuntimeSessions)
	}
}

func TestProductionHandlerRetriesCancelledMissionAgentRun(t *testing.T) {
	root := t.TempDir()
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", productionTestDatabaseURLs(root))
	rt, handler := newProductionRuntimeHandlerForTest(t)
	mission, _, receipt := createMissionTaskAndStartAgentRun(t, handler, "managed")

	cancel := httptest.NewRequest(http.MethodPost, "/api/missions/"+mission.MissionID+"/agent-runs/"+receipt.CommandID+"/cancel", jsonBody(`{"reason":"retry setup"}`))
	cancel.Header.Set("X-User-ID", "local-user")
	cancel.Header.Set("X-Viewer-Kind", "user")
	cancel.Header.Set("X-Agent-Profile-ID", "human-runtime-user")
	cancel.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	cancel.Header.Set("Idempotency-Key", "agent-run-cancel-before-retry")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, cancel)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("cancel agent run status = %d body = %s", rec.Code, rec.Body.String())
	}

	retry := httptest.NewRequest(http.MethodPost, "/api/missions/"+mission.MissionID+"/agent-runs/"+receipt.CommandID+"/retry", nil)
	retry.Header.Set("X-User-ID", "local-user")
	retry.Header.Set("X-Viewer-Kind", "user")
	retry.Header.Set("X-Agent-Profile-ID", "human-runtime-user")
	retry.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	retry.Header.Set("Idempotency-Key", "agent-run-retry")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, retry)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("retry agent run status = %d body = %s", rec.Code, rec.Body.String())
	}
	var retryReceipt missioncontrol.CommandReceiptDTO
	if err := json.NewDecoder(rec.Body).Decode(&retryReceipt); err != nil {
		t.Fatalf("decode retry receipt: %v", err)
	}
	if retryReceipt.CommandID == "" || retryReceipt.CommandID == receipt.CommandID {
		t.Fatalf("retry receipt = %+v, want new run id", retryReceipt)
	}
	status, ok, err := rt.ExecutionQueue.GetStatus(context.Background(), retryReceipt.CommandID)
	if err != nil || !ok {
		t.Fatalf("retry execution queue status ok=%v err=%v", ok, err)
	}
	if string(status.Status) != "queued" || status.Topic != runtimedaemon.AgentRunTopic {
		t.Fatalf("retry execution status = %+v, want queued agent runtime", status)
	}
	ops := getMissionOpsForTest(t, handler, mission.MissionID)
	if !hasRuntimeSessionStatus(ops, receipt.CommandID, "cancelled") || !hasRuntimeSessionStatus(ops, retryReceipt.CommandID, "queued") {
		t.Fatalf("runtime sessions = %+v, want cancelled original and queued retry", ops.RuntimeSessions)
	}
}

func TestProductionExecutionRunnerCompletesMissionAgentRunLifecycle(t *testing.T) {
	root := t.TempDir()
	executable := writePiMissionResultRuntimeFixture(t)
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", productionTestDatabaseURLs(root))
	setTestConfigOverride(t, "discussion.agent.runtime_kind", "pi")
	setTestConfigOverride(t, "discussion.agent.runtime_config", `{"executable":"`+jsonPath(executable)+`"}`)
	rt, handler := newProductionRuntimeHandlerForTest(t)
	configureRuntimeFixtureLLMProfile(t, handler, "pi-test")

	mission, task, receipt := createMissionTaskAndStartAgentRun(t, handler, "pi")
	if err := rt.StartExecutionRunner(context.Background(), platform.ExecutionRunnerRuntimeConfig{WorkerID: "agent-run-test-worker", PollInterval: time.Millisecond}); err != nil {
		t.Fatalf("start execution runner: %v", err)
	}
	waitForRuntimeSessionStatus(t, handler, mission.MissionID, receipt.CommandID, "completed")
	waitForExecutionQueueStatus(t, rt, receipt.CommandID, "completed")
	status, ok, err := rt.ExecutionQueue.GetStatus(context.Background(), receipt.CommandID)
	if err != nil || !ok {
		t.Fatalf("execution queue status ok=%v err=%v", ok, err)
	}
	if string(status.Status) != "completed" || status.Topic != runtimedaemon.AgentRunTopic {
		t.Fatalf("execution status = %+v", status)
	}
	if task.TaskID == "" {
		t.Fatalf("task=%+v", task)
	}
	control := getMissionControlForTest(t, handler, mission.MissionID)
	updatedTask := taskByIDForTest(t, control, task.TaskID)
	if updatedTask.Status != "approved" || updatedTask.LatestResultRef == "" {
		t.Fatalf("task after runtime execution = %+v, want approved with latest result ref", updatedTask)
	}
}

func createMissionTaskAndStartAgentRun(t *testing.T, handler http.Handler, runtimeKind string) (missioncontrol.MissionDTO, missioncontrol.TaskCardDTO, missioncontrol.CommandReceiptDTO) {
	t.Helper()
	create := httptest.NewRequest(http.MethodPost, "/api/missions", jsonBody(`{"title":"Executed agent run","final_goal":"Execute a task through the platform runtime queue"}`))
	create.Header.Set("X-User-ID", "local-user")
	create.Header.Set("X-Viewer-Kind", "user")
	create.Header.Set("X-Agent-Profile-ID", "human-runtime-user")
	create.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	create.Header.Set("Idempotency-Key", "mission-agent-run-exec")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, create)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create mission status = %d body = %s", rec.Code, rec.Body.String())
	}
	var mission missioncontrol.MissionDTO
	if err := json.NewDecoder(rec.Body).Decode(&mission); err != nil {
		t.Fatalf("decode mission: %v", err)
	}

	createTask := httptest.NewRequest(http.MethodPost, "/api/missions/"+mission.MissionID+"/tasks", jsonBody(`{"assignee_id":"`+defaultCoordinatorAgentID+`","goal":"Prepare runtime execution","worker_personal_goal":"Run through the backend execution queue","expected_deliverables":[{"title":"Runtime completion report"}]}`))
	createTask.Header.Set("X-User-ID", "local-user")
	createTask.Header.Set("X-Viewer-Kind", "user")
	createTask.Header.Set("X-Agent-Profile-ID", "human-runtime-user")
	createTask.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	createTask.Header.Set("Idempotency-Key", "task-agent-run-exec")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, createTask)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create task status = %d body = %s", rec.Code, rec.Body.String())
	}
	var task missioncontrol.TaskCardDTO
	if err := json.NewDecoder(rec.Body).Decode(&task); err != nil {
		t.Fatalf("decode task: %v", err)
	}

	start := httptest.NewRequest(http.MethodPost, "/api/missions/"+mission.MissionID+"/tasks/"+task.TaskID+"/agent-runs", jsonBody(`{"agent_id":"`+defaultCoordinatorAgentID+`","runtime_kind":"`+runtimeKind+`","prompt":"Execute from the production runtime queue"}`))
	start.Header.Set("X-User-ID", "local-user")
	start.Header.Set("X-Viewer-Kind", "user")
	start.Header.Set("X-Agent-Profile-ID", "human-runtime-user")
	start.Header.Set("X-Permission-Scope", "mission.read,mission.write,agent.run")
	start.Header.Set("Idempotency-Key", "agent-run-exec")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, start)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start agent run status = %d body = %s", rec.Code, rec.Body.String())
	}
	var receipt missioncontrol.CommandReceiptDTO
	if err := json.NewDecoder(rec.Body).Decode(&receipt); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	return mission, task, receipt
}

func waitForRuntimeSessionStatus(t *testing.T, handler http.Handler, missionID, runID, status string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		opsReq := httptest.NewRequest(http.MethodGet, "/api/missions/"+missionID+"/ops", nil)
		opsReq.Header.Set("X-User-ID", "local-user")
		opsReq.Header.Set("X-Viewer-Kind", "user")
		opsReq.Header.Set("X-Agent-Profile-ID", "human-runtime-user")
		opsReq.Header.Set("X-Permission-Scope", "mission.read,mission.write")
		rec := httptest.NewRecorder()
		serveServerTestHTTP(t, handler, rec, opsReq)
		if rec.Code == http.StatusOK {
			var ops missioncontrol.MissionOpsViewDTO
			if err := json.NewDecoder(rec.Body).Decode(&ops); err == nil {
				for _, session := range ops.RuntimeSessions {
					if session.RunID == runID && session.Status == status {
						return
					}
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("runtime session %s did not reach status %s", runID, status)
}

func waitForExecutionQueueStatus(t *testing.T, runtime *platform.Runtime, runID, status string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, ok, err := runtime.ExecutionQueue.GetStatus(context.Background(), runID)
		if err == nil && ok && string(job.Status) == status {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("execution queue job %s did not reach status %s", runID, status)
}

func getMissionOpsForTest(t *testing.T, handler http.Handler, missionID string) missioncontrol.MissionOpsViewDTO {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/missions/"+missionID+"/ops", nil)
	req.Header.Set("X-User-ID", "local-user")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Agent-Profile-ID", "human-runtime-user")
	req.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mission ops status = %d body = %s", rec.Code, rec.Body.String())
	}
	var ops missioncontrol.MissionOpsViewDTO
	if err := json.NewDecoder(rec.Body).Decode(&ops); err != nil {
		t.Fatalf("decode mission ops: %v", err)
	}
	return ops
}

func hasRuntimeSessionStatus(ops missioncontrol.MissionOpsViewDTO, runID, status string) bool {
	for _, session := range ops.RuntimeSessions {
		if session.RunID == runID && session.Status == status {
			return true
		}
	}
	return false
}

func getMissionControlForTest(t *testing.T, handler http.Handler, missionID string) missioncontrol.MissionControlViewDTO {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/missions/"+missionID+"/control", nil)
	req.Header.Set("X-User-ID", "local-user")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Agent-Profile-ID", "human-runtime-user")
	req.Header.Set("X-Permission-Scope", "mission.read,mission.write")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mission control status = %d body = %s", rec.Code, rec.Body.String())
	}
	var control missioncontrol.MissionControlViewDTO
	if err := json.NewDecoder(rec.Body).Decode(&control); err != nil {
		t.Fatalf("decode mission control: %v", err)
	}
	return control
}

func taskByIDForTest(t *testing.T, control missioncontrol.MissionControlViewDTO, taskID string) missioncontrol.TaskCardDTO {
	t.Helper()
	for _, task := range control.TaskBoard.Tasks {
		if task.TaskID == taskID {
			return task
		}
	}
	t.Fatalf("mission control missing task %s: %+v", taskID, control.TaskBoard.Tasks)
	return missioncontrol.TaskCardDTO{}
}

func newProductionRuntimeHandlerForTest(t *testing.T) (*platform.Runtime, http.Handler) {
	t.Helper()
	t.Setenv("ORRERY_SKIP_DEFAULT_PROFILE_BOOTSTRAP", "false")
	ctx := context.Background()
	cfg := loadTestConfig(t, testConfigOverridesFromProcess())
	rt, err := platform.OpenWithModules(ctx, cfg, productionModules()...)
	if err != nil {
		t.Fatalf("open production test runtime: %v", err)
	}
	t.Cleanup(func() {
		if err := rt.Close(); err != nil {
			t.Fatalf("close production test runtime: %v", err)
		}
	})
	if err := rt.Initialize(ctx); err != nil {
		t.Fatalf("initialize production test runtime: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(rt.UnitOfWork)
	saveAvailableAgentProfile(t, ctx, profiles, defaultHumanAgentProfile("human-runtime-user", "Local User"))
	handler, cleanup, err := newProductionHandlerFromRuntime(ctx, rt)
	if err != nil {
		t.Fatalf("new production handler: %v", err)
	}
	t.Cleanup(cleanup)
	return rt, handler
}
