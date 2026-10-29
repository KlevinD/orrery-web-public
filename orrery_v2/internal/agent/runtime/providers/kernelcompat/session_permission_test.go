package kernelcompat

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

type permissionTestEngine struct{ runner Runner }

func (e permissionTestEngine) SupportsPermissionAnswers() bool { return true }
func (e permissionTestEngine) OpenSession(context.Context, agentcore.SessionSpec, agentcore.SessionConfig) (Runner, error) {
	return e.runner, nil
}

type permissionTestRunner struct {
	*testRunner
	mu       sync.Mutex
	answered bool
	answer   chan struct{}
	answers  atomic.Int32
}

func (r *permissionTestRunner) PendingPermissionRequests() []agentcore.PendingPermissionView {
	return []agentcore.PendingPermissionView{{RequestID: "permission-1", ToolName: "knowledge_add"}}
}
func (r *permissionTestRunner) PendingRuntimeApprovalRequest(id string) (runtimecore.ApprovalRequest, bool) {
	return runtimecore.ApprovalRequest{RequestID: "permission-1", ToolName: "knowledge_add", Payload: []byte(`{"content":"private material"}`), Metadata: map[string]string{"approval_immutable_input": "true"}}, id == "permission-1"
}
func (r *permissionTestRunner) AnswerPermission(_ context.Context, _ agentcore.AnswerPermissionCommand) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.answered {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "already answered", false)
	}
	r.answered = true
	r.answers.Add(1)
	close(r.answer)
	return nil
}

func TestPermissionCapabilityRequiresMatchingRunner(t *testing.T) {
	runner := &testRunner{}
	connection, err := NewConnection(Config{Engine: permissionTestEngine{runner: runner}})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.OpenSession(context.Background(), agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "wrong-runner"}, agentcore.SessionConfig{WorkspaceRef: "workspace"}); err == nil || runner.closes.Load() != 1 {
		t.Fatal("permission capability accepted a runner without the protocol")
	}
}

func TestPermissionCommandsAreIdempotentAndDoNotStartAnotherTurn(t *testing.T) {
	runner := &permissionTestRunner{testRunner: &testRunner{}, answer: make(chan struct{})}
	runner.run = func(ctx context.Context, _ agentcore.SendCommand, emit Emit) (agentcore.TurnResultEvent, error) {
		if err := emit(agentcore.SessionEvent{Kind: agentcore.EventPermission, Permission: &agentcore.PermissionEvent{RequestID: "permission-1", Kind: agentcore.PermissionKindTool, ToolName: "knowledge_add"}}); err != nil {
			return agentcore.TurnResultEvent{}, err
		}
		select {
		case <-runner.answer:
		case <-ctx.Done():
			return agentcore.TurnResultEvent{}, ctx.Err()
		}
		if err := emit(agentcore.SessionEvent{Kind: agentcore.EventPermissionResolved, PermissionResolved: &agentcore.PermissionResolvedEvent{RequestID: "permission-1", Kind: agentcore.PermissionKindTool}}); err != nil {
			return agentcore.TurnResultEvent{}, err
		}
		return completed("done"), nil
	}
	connection, err := NewConnection(Config{Engine: permissionTestEngine{runner: runner}})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	backend, err := connection.OpenSession(context.Background(), agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "permission-session"}, agentcore.SessionConfig{WorkspaceRef: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	session := backend.(*Session)
	if _, err := session.Dispatch(context.Background(), send("start", "write")); err != nil {
		t.Fatal(err)
	}
	for event := range session.Events() {
		if event.Event.Kind == agentcore.EventPermission {
			break
		}
	}
	if len(session.PendingPermissionRequests()) != 1 {
		t.Fatal("pending request unavailable to canonical session")
	}
	material, found := session.PendingRuntimeApprovalRequest("permission-1")
	if !found || len(material.Payload) == 0 {
		t.Fatal("server material port unavailable")
	}
	material.Metadata["approval_immutable_input"] = "mutated"
	again, _ := session.PendingRuntimeApprovalRequest("permission-1")
	if again.Metadata["approval_immutable_input"] != "true" {
		t.Fatal("server material was not copied")
	}
	command := agentcore.RuntimeCommand{CommandID: "decision", Kind: agentcore.RuntimeCommandAnswerPermission, AnswerPermission: &agentcore.AnswerPermissionCommand{RequestID: "permission-1", Decision: agentcore.PermissionApproved}}
	first, err := session.Dispatch(context.Background(), command)
	if err != nil || !first.Accepted || first.Admission != agentcore.AdmissionNoTurn {
		t.Fatalf("answer admission=%+v err=%v", first, err)
	}
	if repeated, err := session.Dispatch(context.Background(), command); err != nil || repeated != first || runner.answers.Load() != 1 {
		t.Fatal("duplicate decision was delivered twice")
	}
	conflict := command
	changed := *command.AnswerPermission
	changed.Decision = agentcore.PermissionDenied
	conflict.AnswerPermission = &changed
	if _, err := session.Dispatch(context.Background(), conflict); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeCommandConflict) {
		t.Fatalf("conflicting decision=%v", err)
	}
	events := collectTurn(t, session)
	if terminal(events).Outcome.Kind != agentcore.TurnOutcomeCompleted || runner.calls.Load() != 1 || len(session.PendingPermissionRequests()) != 0 {
		t.Fatal("answer created a turn or left pending state")
	}
	command.CommandID = "late-decision"
	if _, err := session.Dispatch(context.Background(), command); err == nil {
		t.Fatal("late answer was accepted")
	}
}

func TestPermissionLifecycleRejectsUnboundEventsAndUnfinishedCompletion(t *testing.T) {
	for _, mode := range []string{"unknown-request", "different-tool", "unfinished", "unknown-resolution"} {
		t.Run(mode, func(t *testing.T) {
			runner := &permissionTestRunner{testRunner: &testRunner{}, answer: make(chan struct{})}
			runner.run = func(_ context.Context, _ agentcore.SendCommand, emit Emit) (agentcore.TurnResultEvent, error) {
				event := agentcore.SessionEvent{Kind: agentcore.EventPermission, Permission: &agentcore.PermissionEvent{RequestID: "permission-1", Kind: agentcore.PermissionKindTool, ToolName: "knowledge_add"}}
				switch mode {
				case "unknown-request":
					event.Permission.RequestID = "forged"
				case "different-tool":
					event.Permission.ToolName = "email_send"
				case "unknown-resolution":
					event = agentcore.SessionEvent{Kind: agentcore.EventPermissionResolved, PermissionResolved: &agentcore.PermissionResolvedEvent{RequestID: "permission-1", Kind: agentcore.PermissionKindTool}}
				}
				if err := emit(event); err != nil {
					return agentcore.TurnResultEvent{}, err
				}
				return completed("must fail"), nil
			}
			_, session := openTestSession(t, runner.testRunner, func(c *Config) { c.Engine = permissionTestEngine{runner: runner} })
			if _, err := session.Dispatch(context.Background(), send("start", "write")); err != nil {
				t.Fatal(err)
			}
			if terminal(collectTurn(t, session)).Outcome.Kind != agentcore.TurnOutcomeFailed {
				t.Fatal("invalid permission lifecycle completed successfully")
			}
		})
	}
}
