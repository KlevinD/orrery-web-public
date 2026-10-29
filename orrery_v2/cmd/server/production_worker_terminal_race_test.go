package main

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/app/missioncontrol"
)

func TestScheduledWorkerPreservesWinningTerminalOutcome(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		name := "completed"
		if blocked {
			name = "blocked"
		}
		t.Run(name, func(t *testing.T) {
			run := runtimedaemon.AgentRun{
				RunID: "run-terminal", AgentID: "agent-terminal", MissionID: "mission-terminal", TaskID: "task-terminal",
				Request: agentcore.RuntimeRequest{TenantID: "system", RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{
					"run_source": schedulerAgentTaskRunSource, "scheduled_task_id": "schedule-terminal", "scheduled_run_id": "scheduled-run-terminal", "delivery_mode": "announce",
				}}},
			}
			result := agentcore.AgentResult{
				ID: "result-terminal", RunID: run.RunID, AgentID: run.AgentID, MissionID: "mission-terminal", TaskID: "task-terminal",
				Summary: agentcore.Summary{Text: "late result"},
			}
			if blocked {
				result.Blocker = &agentcore.Blocker{Summary: "late blocker"}
			}
			scheduler := &capturingSchedulerRunLifecycle{}
			responsibility := &capturingResponsibilityRunLifecycle{}
			outbox := &capturingScheduledDeliveryOutbox{}
			worker := productionAgentRunWorker{lifecycle: terminalWinningWorkerLifecycle{}, scheduler: scheduler, responsibility: responsibility, scheduledDeliveryOutbox: outbox}
			if err := worker.completeCommittedScheduledResult(context.Background(), run, result, agentcore.RuntimeResponse{}, "worker"); err != nil {
				t.Fatal(err)
			}
			if scheduler.completedCalls != 0 || scheduler.blockedCalls != 0 || responsibility.completedCalls != 0 || responsibility.failedCalls != 0 || outbox.calls != 0 {
				t.Fatalf("late result changed terminal state: scheduler=%+v responsibility=%+v deliveries=%d", scheduler, responsibility, outbox.calls)
			}
		})
	}
}

type terminalWinningWorkerLifecycle struct{}

func (terminalWinningWorkerLifecycle) PublishRuntimeLifecycle(context.Context, runtimedaemon.RuntimeLifecycleEvent) error {
	return missioncontrol.ErrAgentRunTerminal
}

func (terminalWinningWorkerLifecycle) PublishRuntimeFailure(context.Context, runtimedaemon.RuntimeFailureEvent) error {
	return missioncontrol.ErrAgentRunTerminal
}
