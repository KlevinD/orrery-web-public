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
)

func approvalBatchCalls() []llm.ToolCall {
	read := toolCall("read-before", `{"kb":"public"}`)
	read.Function.Name = "document_read"
	secondRead := read
	secondRead.ID = "read-second"
	return []llm.ToolCall{read, secondRead, toolCall("write-one", `{"kb":"public"}`), toolCall("write-two", `{"kb":"public"}`)}
}

func approvalBatchConfig(t *testing.T, c *Config, req *agentcore.RuntimeRequest) {
	t.Helper()
	checkpointConfig(t, c)
	read := searchBinding()
	read.ToolID, read.Alias = "document.read", "document_read"
	req.ToolBindings = append(req.ToolBindings, read)
	c.ApprovalToolIDs = []string{"knowledge.search"}
	c.ParallelToolIDs, c.MaxParallelTools = []string{"document.read"}, 2
}

func approvalBatchModel(t *testing.T) *modelFunc {
	t.Helper()
	chat := parallelReply(approvalBatchCalls()...)
	original := chat.stream
	chat.stream = func(ctx context.Context, messages []*llm.Message, opts ...llm.Option) (*llm.MessageStream, error) {
		if messages[len(messages)-1].Role == llm.ToolRole {
			for i, call := range approvalBatchCalls() {
				result := messages[len(messages)-4+i]
				if result.Role != llm.ToolRole || result.ToolCallID != call.ID {
					t.Error("recovered batch lost its original result order")
					return nil, ErrProtocol
				}
			}
		}
		return original(ctx, messages, opts...)
	}
	return chat
}

func TestApprovalBatchCollectsDecisionsBeforeAnyDispatch(t *testing.T) {
	for _, mode := range []string{"approved", "deny-last", "cancel-last", "revoke-last", "handler-failure", "resolved-failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var dispatched, approvals atomic.Int32
			var revoked atomic.Bool
			chat := approvalBatchModel(t)
			runner, input := openRunner(t, chat, func(c *Config, req *agentcore.RuntimeRequest) {
				approvalBatchConfig(t, c, req)
				c.AuthorizeTool = func(_ context.Context, call runtimecore.RuntimeToolCallRequest) error {
					if call.CallID == "write-two" && revoked.Load() {
						return ErrToolDenied
					}
					return nil
				}
				c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(_ context.Context, call runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
					if approvals.Load() != 2 || call.Input["kb"] != "public" {
						t.Error("batch executed before all decisions or modified its inputs")
					}
					dispatched.Add(1)
					return runtimecore.RuntimeToolCallResult{Output: map[string]any{"ok": true}}, nil
				})
			})
			ids := map[string]bool{}
			_, err := runner.Run(ctx, input, func(event agentcore.SessionEvent) error {
				if event.Kind == agentcore.EventPermissionResolved && mode == "resolved-failure" && approvals.Load() == 2 {
					return ErrCapacity
				}
				if event.Kind != agentcore.EventPermission {
					return nil
				}
				count := approvals.Add(1)
				window := runner.checkpoint.ApprovalWait
				if window == nil || len(window.Calls) != 4 || dispatched.Load() != 0 || chat.calls.Load() != 1 || ids[event.Permission.RequestID] {
					t.Error("batch was not durably held before a fresh approval")
				}
				ids[event.Permission.RequestID] = true
				journal := runner.config.ToolJournal.(*SQLToolJournal)
				page, listErr := journal.ListRun(ctx, runner.request.TenantID, runner.request.RunID, "", 10)
				if listErr != nil || len(page.Items) != 0 {
					t.Error("batch claimed an execution while still waiting")
				}
				runner.approvalMu.Lock()
				deadline, bounded := runner.pendingApproval.ctx.Deadline()
				runner.approvalMu.Unlock()
				if !bounded || !deadline.Equal(window.ExpiresAt) {
					t.Error("a later approval renewed the batch deadline")
				}
				decision := agentcore.PermissionAllowAlways
				if count == 2 {
					switch mode {
					case "deny-last":
						decision = agentcore.PermissionDenied
					case "cancel-last":
						cancel()
						return nil
					case "revoke-last":
						revoked.Store(true)
					case "handler-failure":
						return ErrCapacity
					}
				}
				return runner.AnswerPermission(ctx, agentcore.AnswerPermissionCommand{RequestID: event.Permission.RequestID, Decision: decision})
			})
			if approvals.Load() != 2 || runner.checkpoint.ApprovalWait != nil {
				t.Fatal("batch failed to close its approval window")
			}
			if mode == "approved" {
				if err != nil || dispatched.Load() != 4 || runner.toolCalls != 4 || runner.modelCalls != 2 {
					t.Fatalf("approved batch tools=%d err=%v", dispatched.Load(), err)
				}
				return
			}
			want := ErrToolDenied
			if mode == "cancel-last" {
				want = context.Canceled
			} else if strings.Contains(mode, "failure") {
				want = ErrCapacity
			}
			if !errors.Is(err, want) || dispatched.Load() != 0 || runner.checkpoint.Phase != CheckpointToolsPending {
				t.Fatalf("stopped batch tools=%d err=%v want=%v", dispatched.Load(), err, want)
			}
			c := runner.checkpoint
			if _, claimErr := runner.config.Checkpoints.Claim(context.Background(), c.TenantID, c.AgentID, c.ID, c.ScopeHash, "after-stop", c.RunID); !errors.Is(claimErr, ErrUnsafeResume) {
				t.Fatalf("stopped batch became replayable: %v", claimErr)
			}
		})
	}
}

func TestApprovalBatchProcessDeathReapprovesEveryWrite(t *testing.T) {
	for _, mode := range []string{"approved", "reduced-budget"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			path := filepath.Join(t.TempDir(), "batch.db")
			journal, _ := openTestJournal(t, path)
			var dispatched atomic.Int32
			runner, input := openRunner(t, approvalBatchModel(t), func(c *Config, req *agentcore.RuntimeRequest) {
				c.ToolJournal = journal
				approvalBatchConfig(t, c, req)
				automaticTaskConfig(t, c, req)
				c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(_ context.Context, call runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
					if call.Input["kb"] != "public" {
						t.Error("recovery changed original arguments")
					}
					dispatched.Add(1)
					return runtimecore.RuntimeToolCallResult{Output: map[string]any{"ok": true}}, nil
				})
			})
			requestFile := filepath.Join(t.TempDir(), "request.json")
			raw, _ := json.Marshal(runner.request)
			if err := os.WriteFile(requestFile, raw, 0600); err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestApprovalRecoveryProcessHelper$")
			command.Env = append(os.Environ(), "ORRERY_APPROVAL_CRASH_HELPER=1", "ORRERY_APPROVAL_BATCH_HELPER=1", "ORRERY_APPROVAL_REQUEST_FILE="+requestFile, "ORRERY_APPROVAL_DB="+path)
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			oldIDs := strings.Split(strings.TrimPrefix(string(output), "approval:"), ",")
			if !errors.As(err, &exit) || exit.ExitCode() != 42 || !strings.HasPrefix(string(output), "approval:") || len(oldIDs) != 2 {
				t.Fatalf("batch process did not die at its second approval: %v %s", err, output)
			}
			if mode == "reduced-budget" {
				runner.config.MaxToolCalls, runner.config.LongTaskMaxToolCalls = 2, 2
			}
			chat := approvalBatchModel(t)
			recovered, err := reopenTaskRunner(t, runner, runner.request, chat)
			if err != nil {
				t.Fatal(err)
			}
			before := *recovered.checkpoint
			if before.ApprovalWait == nil || len(before.ApprovalWait.Calls) != 4 || before.ModelCalls != 1 || before.ToolCalls != 0 {
				t.Fatal("crashed batch lost proposals or consumed budget")
			}
			var requests []string
			_, err = recovered.Run(ctx, input, func(event agentcore.SessionEvent) error {
				if event.Kind != agentcore.EventPermission {
					return nil
				}
				if event.Permission.RequestID == oldIDs[0] || event.Permission.RequestID == oldIDs[1] || dispatched.Load() != 0 || chat.calls.Load() != 0 || !recovered.checkpoint.ApprovalWait.ExpiresAt.Equal(before.ApprovalWait.ExpiresAt) {
					t.Error("recovery reused a decision, renewed its lifetime or regenerated the batch")
				}
				requests = append(requests, event.Permission.RequestID)
				for _, id := range oldIDs {
					if oldErr := recovered.AnswerPermission(ctx, agentcore.AnswerPermissionCommand{RequestID: id, Decision: agentcore.PermissionApproved}); oldErr == nil {
						t.Error("dead process approved a new request")
					}
				}
				return recovered.AnswerPermission(ctx, agentcore.AnswerPermissionCommand{RequestID: event.Permission.RequestID, Decision: agentcore.PermissionApproved})
			})
			if mode == "reduced-budget" {
				if !errors.Is(err, ErrUnsafeResume) || len(requests) != 0 || dispatched.Load() != 0 || chat.calls.Load() != 0 {
					t.Fatalf("reduced budget admitted batch: %v", err)
				}
				return
			}
			if err != nil || len(requests) != 2 || requests[0] == requests[1] || dispatched.Load() != 4 || chat.calls.Load() != 1 || recovered.modelCalls != 2 || recovered.toolCalls != 4 || recovered.checkpoint.Phase != CheckpointCompleted {
				t.Fatalf("recovered tools=%d approvals=%d models=%d err=%v", dispatched.Load(), len(requests), chat.calls.Load(), err)
			}
		})
	}
}

func TestApprovalBatchCannotResumeAfterPartialExecution(t *testing.T) {
	var dispatched atomic.Int32
	runner, input := openRunner(t, approvalBatchModel(t), func(c *Config, req *agentcore.RuntimeRequest) {
		approvalBatchConfig(t, c, req)
		automaticTaskConfig(t, c, req)
		c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
			dispatched.Add(1)
			return runtimecore.RuntimeToolCallResult{Output: map[string]any{"ok": true}}, nil
		})
	})
	_, err := runner.Run(context.Background(), input, func(event agentcore.SessionEvent) error {
		if event.Kind == agentcore.EventPermission {
			return runner.AnswerPermission(context.Background(), agentcore.AnswerPermissionCommand{RequestID: event.Permission.RequestID, Decision: agentcore.PermissionApproved})
		}
		if event.Kind == agentcore.EventToolCall {
			if runner.checkpoint.ApprovalWait != nil {
				t.Error("dispatch started before closing the approval window")
			}
			if event.ToolCall.ToolUseID == "write-two" {
				return ErrCapacity
			}
		}
		return nil
	})
	if !errors.Is(err, ErrCapacity) || dispatched.Load() != 3 {
		t.Fatalf("partial batch tools=%d err=%v", dispatched.Load(), err)
	}
	if _, err := reopenTaskRunner(t, runner, runner.request, approvalBatchModel(t)); !errors.Is(err, ErrUnsafeResume) {
		t.Fatalf("partially executed batch was replayed: %v", err)
	}
}

func TestApprovalBatchRejectsMalformedPersistedProposals(t *testing.T) {
	for _, mode := range []string{"missing-member", "reordered", "duplicate", "invalid-digest", "mixed-formats", "single-in-list"} {
		t.Run(mode, func(t *testing.T) {
			c := testCheckpoint(t)
			c.Phase, c.ModelCalls, c.Revision = CheckpointToolsPending, 1, 1
			c.Messages = append(c.Messages, reply("", "tool_calls", toolCall("one", `{"kb":"public"}`), toolCall("two", `{"kb":"public"}`)))
			wait := &ApprovalWait{Calls: []ApprovalCall{{CallID: "one", Fingerprint: strings.Repeat("a", 64)}, {CallID: "two", Fingerprint: strings.Repeat("b", 64)}}, ExpiresAt: time.Now().Add(time.Minute)}
			c.ApprovalWait = wait
			if _, err := checkpointRecord(c); err != nil {
				t.Fatal("valid batch fixture rejected")
			}
			switch mode {
			case "missing-member":
				wait.Calls = wait.Calls[:1]
			case "reordered":
				wait.Calls[0], wait.Calls[1] = wait.Calls[1], wait.Calls[0]
			case "duplicate":
				wait.Calls[1].CallID = "one"
			case "invalid-digest":
				wait.Calls[1].Fingerprint = "invalid"
			case "mixed-formats":
				wait.CallID, wait.Fingerprint = "one", wait.Calls[0].Fingerprint
			case "single-in-list":
				wait.Calls = wait.Calls[:1]
				c.Messages[len(c.Messages)-1].ToolCalls = c.Messages[len(c.Messages)-1].ToolCalls[:1]
			}
			if _, err := checkpointRecord(c); !errors.Is(err, ErrCheckpoint) {
				t.Fatalf("ambiguous or incomplete proposal accepted: %v", err)
			}
		})
	}
}

func TestApprovalBatchResumeRejectsChangedInstruction(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		t.Run(map[bool]string{false: "explicit", true: "automatic"}[automatic], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var dispatched atomic.Int32
			runner, input := openRunner(t, approvalBatchModel(t), func(c *Config, req *agentcore.RuntimeRequest) {
				approvalBatchConfig(t, c, req)
				if automatic {
					automaticTaskConfig(t, c, req)
				}
				c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
					dispatched.Add(1)
					return runtimecore.RuntimeToolCallResult{Output: map[string]any{"ok": true}}, nil
				})
			})
			requested, done := make(chan struct{}, 1), make(chan error, 1)
			go func() {
				_, err := runner.Run(ctx, input, func(event agentcore.SessionEvent) error {
					if event.Kind == agentcore.EventPermission {
						requested <- struct{}{}
					}
					return nil
				})
				done <- err
			}()
			select {
			case <-requested:
			case <-time.After(5 * time.Second):
				t.Fatal("batch did not request approval")
			}
			req := runner.request
			req.InputMessages = append([]agentcore.RuntimeChatMessage(nil), req.InputMessages...)
			req.InputMessages[len(req.InputMessages)-1].Content = "Cancel those writes; inspect something else"
			chat := approvalBatchModel(t)
			want, previousWant := ErrInput, ErrCheckpoint
			var recovered *Runner
			var err error
			if automatic {
				recovered, err = reopenTaskRunner(t, runner, req, chat)
				want, previousWant = ErrCheckpoint, context.Canceled
			} else {
				recovered, err = resumeTestRunner(t, runner, req, chat)
			}
			cancel()
			if previousErr := waitCallbackError(t, done); !errors.Is(previousErr, previousWant) {
				t.Errorf("previous owner: %v, want %v", previousErr, previousWant)
			}
			if !errors.Is(err, want) || recovered != nil || dispatched.Load() != 0 || chat.calls.Load() != 0 {
				t.Fatalf("changed instruction executed the previous batch: tools=%d err=%v", dispatched.Load(), err)
			}
		})
	}
}
