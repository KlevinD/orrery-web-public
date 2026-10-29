package missioncontrol_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/mission/projection"
)

type taskRestartGuardStub struct {
	err   error
	calls [][3]string
}

func (g *taskRestartGuardStub) CheckTaskRestart(_ context.Context, tenant, mission, task string) error {
	g.calls = append(g.calls, [3]string{tenant, mission, task})
	return g.err
}

func TestTaskRestartGuardProtectsStartRetryAndResponsibilityDispatch(t *testing.T) {
	for _, entry := range []string{"start", "retry", "dispatch"} {
		t.Run(entry, func(t *testing.T) {
			ctx := context.Background()
			at := time.Now().UTC()
			repo := missioncontrol.NewMemoryProjectionRepository()
			read := appRuntimeReadModel(at)
			read.RuntimeSessions = []projection.RuntimeSessionView{{RunID: "old-run", AgentID: "agent-1", TaskID: "task-1", RuntimeKind: "managed", Status: projection.RuntimeSessionFailed, UpdatedAt: at}}
			if err := repo.Save(ctx, read); err != nil {
				t.Fatal(err)
			}
			queue := &capturingRunStore{}
			events := &capturingMissionEventSink{}
			service := newAuthorizedCommandService(nil)
			service.SetRuntimeCommandPorts(queue, repo, events, &sequenceContextIDGenerator{}, fixedClock{at: at})
			guard := &taskRestartGuardStub{err: missioncontrol.ErrConflict}
			service.SetRuntimeTaskRestartGuard(guard)
			viewer := missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}
			cmd := missioncontrol.StartAgentRunCommand{MissionID: "mission-1", TaskID: "task-1", AgentID: "agent-1", RuntimeKind: "codex", Prompt: "Do the task", RequestID: "request", IdempotencyKey: "retry-new-identity"}
			var err error
			switch entry {
			case "start":
				_, err = service.StartAgentRun(ctx, viewer, cmd)
			case "retry":
				_, err = service.RetryAgentRun(ctx, viewer, missioncontrol.RetryAgentRunCommand{MissionID: "mission-1", RunID: "old-run", RequestID: "request", IdempotencyKey: "retry"})
			case "dispatch":
				viewer.ViewerID, viewer.ViewerKind = "agent-1", missioncontrol.ViewerKindAgent
				cmd.RunID, cmd.ResponsibilityContext = "new-run", testResponsibilityExecutionContext("agent-1")
				_, err = service.DispatchResponsibilityRun(ctx, viewer, cmd)
			}
			if !errors.Is(err, missioncontrol.ErrConflict) || len(guard.calls) != 1 || len(queue.runs) != 0 || len(events.events) != 0 {
				t.Fatalf("entry bypassed barrier: %s calls=%v err=%v", entry, guard.calls, err)
			}
			if guard.calls[0] != [3]string{viewer.Tenant(), "mission-1", "task-1"} {
				t.Fatal("guard received caller-controlled execution identity")
			}
			// Object authorization must run before checking private runtime state.
			service.SetAuthorizationService(nil)
			guard.calls = nil
			if _, err = service.StartAgentRun(ctx, viewer, cmd); err == nil || len(guard.calls) != 0 {
				t.Fatal("unauthorized command inspected private task state")
			}
		})
	}
}
