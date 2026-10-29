package daemon

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/execution"
)

func TestPlatformRunStoreCompletesQueueAndPersistsResponse(t *testing.T) {
	queue := newFakeQueue()
	responses := &fakeResponses{}
	store := PlatformRunStore{Queue: queue, Responses: responses}
	if err := store.Enqueue(testContext(), AgentRun{RunID: "run-1", AgentID: "agent-1", AgentProfileID: "profile-1", AgentInstanceID: "agent-1:runtime"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, _, err := store.Claim(testContext(), "worker-1"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := store.Complete(testContext(), "run-1", agentcore.RuntimeResponse{RunID: "run-1", Status: agentcore.RuntimeStatusCompleted}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if !queue.completed || responses.runID != "run-1" {
		t.Fatalf("queue completed=%v response=%q", queue.completed, responses.runID)
	}
}

func TestPlatformRunStoreBlocksQueueWithoutRetryingJob(t *testing.T) {
	queue := newFakeQueue()
	responses := &fakeResponses{}
	store := PlatformRunStore{Queue: queue, Responses: responses}
	if err := store.Enqueue(testContext(), AgentRun{RunID: "run-blocked", AgentID: "agent-1", AgentProfileID: "profile-1", AgentInstanceID: "agent-1:runtime"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, _, err := store.Claim(testContext(), "worker-1"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := store.Block(testContext(), "run-blocked", "waiting for approval", agentcore.RuntimeResponse{Status: agentcore.RuntimeStatusBlocked}); err != nil {
		t.Fatalf("block: %v", err)
	}
	if !queue.completed || queue.failed {
		t.Fatalf("blocked queue should complete active job without fail retry, completed=%v failed=%v", queue.completed, queue.failed)
	}
	if responses.runID != "run-blocked" || responses.status != agentcore.RuntimeStatusBlocked {
		t.Fatalf("blocked response = run %q status %q", responses.runID, responses.status)
	}
}

func TestPlatformRunStoreRejectsRunsWithoutCompleteAgentIdentity(t *testing.T) {
	store := PlatformRunStore{Queue: newFakeQueue()}
	cases := []struct {
		name string
		run  AgentRun
	}{
		{name: "missing agent id", run: AgentRun{RunID: "run-missing-agent", AgentProfileID: "profile-1", AgentInstanceID: "agent-1:runtime"}},
		{name: "missing profile id", run: AgentRun{RunID: "run-missing-profile", AgentID: "agent-1", AgentInstanceID: "agent-1:runtime"}},
		{name: "missing instance id", run: AgentRun{RunID: "run-missing-instance", AgentID: "agent-1", AgentProfileID: "profile-1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := store.Enqueue(testContext(), tc.run); err == nil {
				t.Fatalf("expected identity validation error")
			}
		})
	}
}

func TestExecutionJobFromAgentRunUsesRuntimeQueueIDAsPlatformQueueKey(t *testing.T) {
	job, err := ExecutionJobFromAgentRun(AgentRun{
		RunID:           "run-queue-key",
		AgentID:         "agent-1",
		AgentProfileID:  "profile-1",
		AgentInstanceID: "agent-1:runtime",
		MissionID:       "mission-1",
		TaskID:          "task-1",
		Request: agentcore.RuntimeRequest{
			RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{"queue_id": "discussion:group-1"}},
		},
	})
	if err != nil {
		t.Fatalf("ExecutionJobFromAgentRun returned error: %v", err)
	}
	if job.QueueKey != "discussion:group-1" {
		t.Fatalf("job queue key = %q", job.QueueKey)
	}
}

func testContext() context.Context { return context.Background() }

type fakeQueue struct {
	jobs      []execution.Job
	claimed   map[string]execution.Job
	completed bool
	failed    bool
}

func newFakeQueue() *fakeQueue { return &fakeQueue{claimed: map[string]execution.Job{}} }

func (q *fakeQueue) Enqueue(_ context.Context, job execution.Job) error {
	q.jobs = append(q.jobs, job)
	return nil
}

func (q *fakeQueue) Claim(_ context.Context, workerID string) (execution.Job, bool, error) {
	if len(q.jobs) == 0 {
		return execution.Job{}, false, nil
	}
	job := q.jobs[0]
	q.jobs = q.jobs[1:]
	job.WorkerID = workerID
	q.claimed[job.ID] = job
	return job, true, nil
}

func (q *fakeQueue) Complete(_ context.Context, jobID string) error {
	q.completed = true
	delete(q.claimed, jobID)
	return nil
}

func (q *fakeQueue) Fail(_ context.Context, jobID string) error {
	q.failed = true
	delete(q.claimed, jobID)
	return nil
}

type fakeLiveness struct{ touched string }

func (l *fakeLiveness) Touch(_ context.Context, runtimeID string, _ time.Duration, _ time.Time) {
	l.touched = runtimeID
}
func (l *fakeLiveness) IsAliveBatch(context.Context, []string, time.Time) map[string]bool {
	return map[string]bool{}
}
func (l *fakeLiveness) Forget(context.Context, string) {}

type fakeResponses struct {
	runID  string
	status agentcore.RuntimeStatus
}

func (r *fakeResponses) SaveResponse(_ context.Context, runID agentcore.AgentRunID, response agentcore.RuntimeResponse) error {
	r.runID = runID.String()
	r.status = response.Status
	return nil
}

func (r *fakeResponses) SaveFailure(_ context.Context, runID agentcore.AgentRunID, _ string) error {
	r.runID = runID.String()
	return nil
}
