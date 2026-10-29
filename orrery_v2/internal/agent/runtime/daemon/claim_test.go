package daemon

import (
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func TestLeaseFromRunExposesAgentRunLeaseContract(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	lease := LeaseFromRun(AgentRun{RunID: "run-1", AgentID: "agent-1", AgentProfileID: "profile-1", AgentInstanceID: "agent-1:runtime", RuntimeID: "codex", MissionID: "mission-1", TaskID: "task-1", Status: RunStatusRunning, ClaimedBy: "worker-1", LeaseExpiresAt: now.Add(time.Minute), HeartbeatAt: now, Attempt: 2})
	if lease.RunID != "run-1" || lease.AgentID != "agent-1" || lease.AgentProfileID != "profile-1" || lease.AgentInstanceID != "agent-1:runtime" || lease.RuntimeID != "codex" || lease.Attempt != 2 {
		t.Fatalf("lease = %#v", lease)
	}
}

func TestPlatformRunStoreClaimsExecutionQueuePayloadAndTouchesLiveness(t *testing.T) {
	queue := newFakeQueue()
	liveness := &fakeLiveness{}
	store := PlatformRunStore{Queue: queue, Liveness: liveness, LeaseTTL: time.Minute, Now: func() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) }}
	if err := store.Enqueue(testContext(), AgentRun{RunID: "run-1", AgentID: "agent-1", AgentProfileID: "profile-1", AgentInstanceID: "agent-1:runtime", Request: agentcore.RuntimeRequest{RunID: "run-1"}}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	run, ok, err := store.Claim(testContext(), "worker-1")
	if err != nil || !ok {
		t.Fatalf("claim = %#v %v %v", run, ok, err)
	}
	if run.Status != RunStatusRunning || run.ClaimedBy != "worker-1" || run.AgentInstanceID != "agent-1:runtime" {
		t.Fatalf("run = %#v", run)
	}
	if liveness.touched != "agent-run:run-1" {
		t.Fatalf("liveness touched = %q", liveness.touched)
	}
}

func TestExecutionJobPayloadDecodesAgentRunAndBuildsAgentInput(t *testing.T) {
	run := AgentRun{
		RunID:           "run-1",
		AgentID:         "agent-1",
		AgentProfileID:  "profile-1",
		AgentInstanceID: "instance-1",
		RuntimeID:       "codex",
		MissionID:       "mission-1",
		TaskID:          "task-1",
		Request: agentcore.RuntimeRequest{
			RunID:     "run-1",
			TenantID:  "tenant-request",
			AgentID:   "agent-1",
			MissionID: "mission-1",
			TaskID:    "task-1",
			ContextPack: agentcore.ContextPack{
				ID:                 "ctx-run-1",
				MissionID:          "mission-1",
				TaskID:             "task-1",
				AgentID:            "agent-1",
				WorkerPersonalGoal: "Execute the backend task",
			},
		},
		Task: agentcore.TaskInput{
			ID:                 "task-1",
			MissionID:          "mission-1",
			Goal:               "Execute queued work",
			WorkerPersonalGoal: "Execute the backend task",
			ExpectedDeliverables: []agentcore.TaskDeliverableInput{{
				ID:    "deliverable-1",
				Title: "Queued work",
			}},
		},
	}

	job, err := ExecutionJobFromAgentRun(run)
	if err != nil {
		t.Fatalf("ExecutionJobFromAgentRun returned error: %v", err)
	}
	decoded, err := DecodeAgentRunPayload(job.Payload)
	if err != nil {
		t.Fatalf("DecodeAgentRunPayload returned error: %v", err)
	}
	input := AgentInputFromRun(decoded)

	if decoded.RunID != run.RunID || decoded.AgentID != run.AgentID || decoded.AgentInstanceID != run.AgentInstanceID {
		t.Fatalf("decoded run = %+v", decoded)
	}
	if decoded.Request.RuntimeOptions.ProviderConfig != nil || decoded.Request.RuntimeOptions.MCPConfig != nil {
		t.Fatalf("nil optional JSON must survive queue round trip: %+v", decoded.Request.RuntimeOptions)
	}
	if input.RunID != run.RunID || input.TenantID != "tenant-request" || input.ContextPack.AgentID != run.AgentID || input.Task.ID != "task-1" || input.Task.WorkerPersonalGoal != "Execute the backend task" {
		t.Fatalf("input = %+v", input)
	}
	if len(input.Task.ExpectedDeliverables) != 1 || input.Task.ExpectedDeliverables[0].ID != "deliverable-1" {
		t.Fatalf("input expected deliverables = %+v", input.Task.ExpectedDeliverables)
	}
}
