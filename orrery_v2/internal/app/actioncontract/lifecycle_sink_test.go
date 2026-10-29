package actioncontract

import (
	"context"
	"errors"
	"testing"
)

type recordingActionRunLifecycleSink struct {
	runs     []ActionRun
	receipts []WritebackReceipt
	runErr   error
}

func (s *recordingActionRunLifecycleSink) ProjectActionRun(_ context.Context, run ActionRun) error {
	s.runs = append(s.runs, run)
	return s.runErr
}

func TestServiceClosesRunWhenStartProjectionFails(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	if err := InstallDefaultActionTypes(ctx, NewService(repository)); err != nil {
		t.Fatal(err)
	}
	projectionErr := errors.New("projection unavailable")
	service := NewService(repository, WithRunLifecycleSink(&recordingActionRunLifecycleSink{runErr: projectionErr}))
	_, err := service.StartActionRun(ctx, StartRunRequest{
		RunID: "action-run-projection-failed", TenantID: "tenant-1", ActionTypeID: "mcp.tool.call",
		Actor: ActionSubject{Kind: SubjectService, ID: "gateway"}, Runtime: RuntimeRef{Kind: RuntimeMCPGateway, ID: "gateway"},
		ObjectRefs: []ObjectRef{{Kind: "tool", ID: "tool-1"}}, ArgsHash: "sha256:projection",
	})
	if !errors.Is(err, projectionErr) {
		t.Fatalf("StartActionRun error = %v, want projection error", err)
	}
	run, err := repository.GetActionRun(ctx, "action-run-projection-failed")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != RunStatusFailed || run.ErrorCode != "lifecycle_projection_failed" || run.CompletedAt == nil {
		t.Fatalf("closed action run = %+v", run)
	}
}

func (s *recordingActionRunLifecycleSink) ProjectWritebackReceipt(_ context.Context, _ ActionRun, receipt WritebackReceipt) error {
	s.receipts = append(s.receipts, receipt)
	return nil
}

func TestServiceProjectsStartedAndCompletedActionRuns(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	if err := InstallDefaultActionTypes(ctx, NewService(repository)); err != nil {
		t.Fatal(err)
	}
	sink := &recordingActionRunLifecycleSink{}
	service := NewService(repository, WithRunLifecycleSink(sink))
	run, err := service.StartActionRun(ctx, StartRunRequest{
		RunID: "action-run-1", TenantID: "tenant-1", ActionTypeID: "task.complete",
		Actor: ActionSubject{Kind: SubjectHuman, ID: "human-1"}, Runtime: RuntimeRef{Kind: RuntimeWebApp, ID: "orrery-web"},
		ObjectRefs: []ObjectRef{{Kind: "task", ID: "task-1"}}, ArgsHash: "sha256:args",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.CompleteActionRun(ctx, CompleteRunRequest{RunID: run.ID}); err != nil {
		t.Fatal(err)
	}
	if len(sink.runs) != 2 || sink.runs[0].Status != RunStatusStarted || sink.runs[1].Status != RunStatusCompleted {
		t.Fatalf("projected runs = %+v", sink.runs)
	}
}

func TestServiceProjectsFailedRunAndWritebackReceipt(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	if err := InstallDefaultActionTypes(ctx, NewService(repository)); err != nil {
		t.Fatal(err)
	}
	sink := &recordingActionRunLifecycleSink{}
	service := NewService(repository, WithRunLifecycleSink(sink))
	failedRun, err := service.StartActionRun(ctx, StartRunRequest{
		RunID: "action-run-failed", TenantID: "tenant-1", ActionTypeID: "mcp.tool.call",
		Actor: ActionSubject{Kind: SubjectAgent, ID: "agent-1"}, Runtime: RuntimeRef{Kind: RuntimeMCPGateway, ID: "gateway"},
		ObjectRefs: []ObjectRef{{Kind: "tool", ID: "tool-1"}}, ArgsHash: "sha256:failed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.FailActionRun(ctx, FailRunRequest{RunID: failedRun.ID, ErrorCode: "tool_failed"}); err != nil {
		t.Fatal(err)
	}
	completedRun, err := service.StartActionRun(ctx, StartRunRequest{
		RunID: "action-run-writeback", TenantID: "tenant-1", ActionTypeID: "mcp.tool.call",
		Actor: ActionSubject{Kind: SubjectAgent, ID: "agent-1"}, Runtime: RuntimeRef{Kind: RuntimeMCPGateway, ID: "gateway"},
		ObjectRefs: []ObjectRef{{Kind: "tool", ID: "tool-1"}}, ArgsHash: "sha256:writeback",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.CompleteActionRun(ctx, CompleteRunRequest{
		RunID:     completedRun.ID,
		Writeback: &WritebackReceipt{ID: "receipt-1", TargetSystem: "mcp", Status: WritebackStatusSucceeded},
	}); err != nil {
		t.Fatal(err)
	}
	if len(sink.runs) != 4 || sink.runs[1].Status != RunStatusFailed || sink.runs[3].Status != RunStatusCompleted {
		t.Fatalf("projected runs = %+v", sink.runs)
	}
	if len(sink.receipts) != 1 || sink.receipts[0].ID != "receipt-1" {
		t.Fatalf("projected receipts = %+v", sink.receipts)
	}
}
