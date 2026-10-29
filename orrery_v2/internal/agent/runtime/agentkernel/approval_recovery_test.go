package agentkernel

import (
	"context"
	"encoding/json"
	"errors"
	"orrery_v2/internal/agent/llm"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/session"
	"orrery_v2/internal/platform/db"
)

func TestApprovalRecoveryProcessHelper(t *testing.T) {
	if os.Getenv("ORRERY_APPROVAL_CRASH_HELPER") != "1" {
		t.Skip("subprocess crash helper")
	}
	ctx := context.Background()
	raw, err := os.ReadFile(os.Getenv("ORRERY_APPROVAL_REQUEST_FILE"))
	var request agentcore.RuntimeRequest
	if err != nil || json.Unmarshal(raw, &request) != nil {
		t.Fatal("synthetic request unavailable")
	}
	// RawMessage fields encode an absent value as JSON null across processes.
	request.RuntimeOptions.ProviderConfig = nil
	request.RuntimeOptions.MCPConfig = nil
	store, err := db.Open(ctx, db.Config{Driver: db.DriverFile, URL: "file://" + os.Getenv("ORRERY_APPROVAL_DB")})
	if err != nil {
		t.Fatal(err)
	}
	journal, _ := NewSQLToolJournal(store)
	checkpoints, _ := NewSQLCheckpointStore(store)
	workspaces := session.NewWorkspaceBroker()
	chat := toolThenAnswerModel()
	batch := os.Getenv("ORRERY_APPROVAL_BATCH_HELPER") == "1"
	if batch {
		chat = parallelReply(approvalBatchCalls()...)
	}
	engine, err := NewEngine(Config{Workspaces: workspaces, Models: kernelTestModelFactory{Model: chat}, ToolJournal: journal, Checkpoints: checkpoints, ApprovalToolIDs: []string{"knowledge.search"},
		AuthorizeTool: func(context.Context, runtimecore.RuntimeToolCallRequest) error { return nil },
		Tools: runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
			panic("tool dispatched before approval")
		})})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := workspaces.Bind(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := engine.OpenSession(ctx, agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "crashing-process"}, agentcore.SessionConfig{WorkspaceRef: binding.Ref})
	if err != nil {
		t.Fatal(err)
	}
	var approvals []string
	_, err = backend.Run(ctx, agentcore.SendCommand{Content: []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: "hello"}}}, func(event agentcore.SessionEvent) error {
		if event.Kind == agentcore.EventPermission {
			approvals = append(approvals, event.Permission.RequestID)
			if batch && len(approvals) == 1 {
				return backend.(*Runner).AnswerPermission(ctx, agentcore.AnswerPermissionCommand{RequestID: event.Permission.RequestID, Decision: agentcore.PermissionApproved})
			}
			// Exit without Close/deferred cleanup to reproduce a real process loss.
			_, _ = os.Stdout.WriteString("approval:" + strings.Join(approvals, ","))
			os.Exit(42)
		}
		return nil
	})
	t.Fatalf("helper did not crash at approval: %v", err)
}

func TestApprovalRecoveryAfterProcessDeathReapprovesOriginalProposal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "approval.db")
	journal, _ := openTestJournal(t, path)
	var dispatched atomic.Int32
	runner, input := openRunner(t, toolThenAnswerModel(), func(c *Config, req *agentcore.RuntimeRequest) {
		c.ToolJournal = journal
		automaticTaskConfig(t, c, req)
		c.ApprovalToolIDs = []string{"knowledge.search"}
		c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(_ context.Context, request runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
			if request.Input["kb"] != "public" {
				t.Error("recovery modified original arguments")
			}
			dispatched.Add(1)
			return runtimecore.RuntimeToolCallResult{Output: map[string]any{"answer": "public"}}, nil
		})
	})
	requestFile := filepath.Join(t.TempDir(), "request.json")
	raw, _ := json.Marshal(runner.request)
	if err := os.WriteFile(requestFile, raw, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestApprovalRecoveryProcessHelper$")
	command.Env = append(os.Environ(), "ORRERY_APPROVAL_CRASH_HELPER=1", "ORRERY_APPROVAL_REQUEST_FILE="+requestFile, "ORRERY_APPROVAL_DB="+path)
	output, err := command.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 42 || !strings.HasPrefix(string(output), "approval:") {
		t.Fatalf("crash helper failed: %v %s", err, output)
	}
	oldID := strings.TrimPrefix(string(output), "approval:")
	chat := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, _ ...llm.Option) (*llm.MessageStream, error) {
		if messages[len(messages)-1].Role != llm.ToolRole {
			t.Error("recovery regenerated the proposal instead of completing the saved call")
		}
		return llm.NewMessageStream([]*llm.Message{reply("saved", "stop")}), nil
	}}
	recovered, err := reopenTaskRunner(t, runner, runner.request, chat)
	if err != nil {
		t.Fatal(err)
	}
	before := *recovered.checkpoint
	if before.ApprovalWait == nil || before.ModelCalls != 1 || before.ToolCalls != 0 {
		t.Fatal("crashed proposal or budgets were not preserved")
	}
	result, err := recovered.Run(ctx, input, func(event agentcore.SessionEvent) error {
		if event.Kind == agentcore.EventPermission {
			if event.Permission.RequestID == oldID || dispatched.Load() != 0 || chat.calls.Load() != 0 || recovered.checkpoint.ApprovalWait.ExpiresAt != before.ApprovalWait.ExpiresAt {
				t.Error("recovery reused a stale decision, renewed its deadline or called the model")
			}
			if err := recovered.AnswerPermission(ctx, agentcore.AnswerPermissionCommand{RequestID: oldID, Decision: agentcore.PermissionApproved}); err == nil {
				t.Error("dead-process approval answered the new proposal")
			}
			return recovered.AnswerPermission(ctx, agentcore.AnswerPermissionCommand{RequestID: event.Permission.RequestID, Decision: agentcore.PermissionApproved})
		}
		return nil
	})
	if err != nil || result.ResultText != "saved" || dispatched.Load() != 1 || chat.calls.Load() != 1 || recovered.checkpoint.ModelCalls != 2 || recovered.checkpoint.ToolCalls != 1 || recovered.checkpoint.Phase != CheckpointCompleted {
		t.Fatalf("recovered result=%+v calls=%d model=%d err=%v", result, dispatched.Load(), chat.calls.Load(), err)
	}
}

func TestApprovalRecoveryFencesOldLiveOwnerBeforeDispatch(t *testing.T) {
	var dispatched atomic.Int32
	runner, input := openRunner(t, toolThenAnswerModel(), func(c *Config, _ *agentcore.RuntimeRequest) {
		checkpointConfig(t, c)
		c.ApprovalToolIDs = []string{"knowledge.search"}
		c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
			dispatched.Add(1)
			return runtimecore.RuntimeToolCallResult{Output: map[string]any{"ok": true}}, nil
		})
	})
	requested, done := make(chan string, 1), make(chan error, 1)
	go func() {
		_, err := runner.Run(context.Background(), input, func(event agentcore.SessionEvent) error {
			if event.Kind == agentcore.EventPermission {
				requested <- event.Permission.RequestID
			}
			return nil
		})
		done <- err
	}()
	var id string
	select {
	case id = <-requested:
	case <-time.After(5 * time.Second):
		t.Fatal("no approval request")
	}
	chat := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
		return llm.NewMessageStream([]*llm.Message{reply("done", "stop")}), nil
	}}
	recovered, err := resumeTestRunner(t, runner, runner.request, chat)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.AnswerPermission(context.Background(), agentcore.AnswerPermissionCommand{RequestID: id, Decision: agentcore.PermissionApproved}); err != nil {
		t.Fatal(err)
	}
	if err := waitCallbackError(t, done); !errors.Is(err, ErrCheckpoint) || dispatched.Load() != 0 {
		t.Fatalf("old approved owner dispatched: %v", err)
	}
	result, err := recovered.Run(context.Background(), input, func(event agentcore.SessionEvent) error {
		if event.Kind == agentcore.EventPermission {
			return recovered.AnswerPermission(context.Background(), agentcore.AnswerPermissionCommand{RequestID: event.Permission.RequestID, Decision: agentcore.PermissionApproved})
		}
		return nil
	})
	if err != nil || result.ResultText != "done" || dispatched.Load() != 1 || chat.calls.Load() != 1 {
		t.Fatalf("new owner dispatches=%d err=%v", dispatched.Load(), err)
	}
}

func TestApprovalRecoveryKeepsUnverifiedBatchesBlocked(t *testing.T) {
	for _, mode := range []string{"separate-stores", "wrapped-checkpoints"} {
		t.Run(mode, func(t *testing.T) {
			chat := parallelReply(toolCall("one", `{"kb":"public"}`), toolCall("two", `{"kb":"public"}`))
			runner, input := openRunner(t, chat, func(c *Config, _ *agentcore.RuntimeRequest) {
				checkpointConfig(t, c)
				c.ApprovalToolIDs = []string{"knowledge.search"}
				if mode == "separate-stores" {
					c.Checkpoints, _ = NewSQLCheckpointStore(newTestJournal(t).store)
				} else {
					c.Checkpoints = struct{ CheckpointStore }{c.Checkpoints}
				}
			})
			_, err := runner.Run(context.Background(), input, func(event agentcore.SessionEvent) error {
				if event.Kind == agentcore.EventPermission {
					c := *runner.checkpoint
					if c.ApprovalWait != nil {
						t.Error("unverified batch marked recoverable")
					}
					if _, err := runner.config.Checkpoints.Claim(context.Background(), c.TenantID, c.AgentID, c.ID, c.ScopeHash, "new-owner", c.RunID); !errors.Is(err, ErrUnsafeResume) {
						t.Errorf("unverified batch resumed: %v", err)
					}
					return runner.AnswerPermission(context.Background(), agentcore.AnswerPermissionCommand{RequestID: event.Permission.RequestID, Decision: agentcore.PermissionDenied})
				}
				return nil
			})
			if !errors.Is(err, ErrToolDenied) {
				t.Fatalf("denied batch: %v", err)
			}
		})
	}
}
