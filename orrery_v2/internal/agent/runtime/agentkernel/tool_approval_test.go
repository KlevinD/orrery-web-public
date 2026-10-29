package agentkernel

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func TestToolApprovalWaitsBeforeClaimAndRechecksAuthority(t *testing.T) {
	for _, mode := range []string{"approved", "denied", "revoked", "cancelled", "closed", "expired"} {
		t.Run(mode, func(t *testing.T) {
			var dispatched atomic.Int32
			var revoked atomic.Bool
			runner, input := openRunner(t, toolThenAnswerModel(), func(c *Config, _ *agentcore.RuntimeRequest) {
				checkpointConfig(t, c)
				c.ApprovalToolIDs = []string{"knowledge.search"}
				c.ToolTimeout = 200 * time.Millisecond
				if mode == "expired" {
					c.ApprovalTimeout = 100 * time.Millisecond
				}
				c.AuthorizeTool = func(context.Context, runtimecore.RuntimeToolCallRequest) error {
					if revoked.Load() {
						return ErrToolDenied
					}
					return nil
				}
				c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
					dispatched.Add(1)
					return runtimecore.RuntimeToolCallResult{Output: map[string]any{"ok": true}}, nil
				})
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			requested := make(chan string, 1)
			done := make(chan error, 1)
			go func() {
				_, err := runner.Run(ctx, input, func(event agentcore.SessionEvent) error {
					if event.Kind == agentcore.EventPermission {
						requested <- event.Permission.RequestID
					}
					return event.Validate()
				})
				done <- err
			}()
			var id string
			select {
			case id = <-requested:
			case <-time.After(5 * time.Second):
				t.Fatal("approval was not requested")
			}
			journal := runner.config.ToolJournal.(*SQLToolJournal)
			if page, err := journal.ListRun(ctx, runner.request.TenantID, runner.request.RunID, "", 10); err != nil || len(page.Items) != 0 || dispatched.Load() != 0 {
				t.Fatal("approval waiting acquired or dispatched a tool")
			}
			if runner.checkpoint.ApprovalWait == nil || runner.checkpoint.ApprovalWait.CallID == "" {
				t.Fatal("unexecuted proposal was not persisted before requesting approval")
			}
			material, found := runner.PendingRuntimeApprovalRequest(id)
			if !found || !strings.Contains(string(material.Payload), "public") || material.Metadata["approval_tool_use_id"] == "" || strings.Contains(string(material.Payload), "never-send-to-model") {
				t.Fatal("approval material lost operation identity or exposed credentials")
			}
			material.Payload[0] = '!'
			material.Metadata["approval_tool_use_id"] = "mutated"
			again, _ := runner.PendingRuntimeApprovalRequest(id)
			if again.Payload[0] == '!' || again.Metadata["approval_tool_use_id"] == "mutated" {
				t.Fatal("approval material was not copied")
			}
			if err := runner.AnswerPermission(ctx, agentcore.AnswerPermissionCommand{RequestID: "another-operation", Decision: agentcore.PermissionApproved}); err == nil {
				t.Fatal("unrelated operation answered")
			}
			if err := runner.AnswerPermission(ctx, agentcore.AnswerPermissionCommand{RequestID: id, Decision: agentcore.PermissionApproved, Answers: []agentcore.QuestionAnswer{{Question: "modify", Labels: []string{"private"}}}}); err == nil {
				t.Fatal("modified tool arguments accepted")
			}
			var expected error
			if err := runner.AnswerPermission(ctx, agentcore.AnswerPermissionCommand{RequestID: id, Decision: agentcore.PermissionApproved, Selected: "deny"}); err == nil {
				t.Fatal("conflicting approval choices accepted")
			}
			switch mode {
			case "approved":
				// Wait beyond the execution timeout; approval has its own bounded wait.
				select {
				case err := <-done:
					t.Fatalf("execution timeout included human waiting: %v", err)
				case <-time.After(250 * time.Millisecond):
				}
			case "denied":
				expected = ErrToolDenied
			case "revoked":
				revoked.Store(true)
				expected = ErrToolDenied
			case "cancelled":
				cancel()
				expected = context.Canceled
			case "closed":
				_ = runner.Close(context.Background())
				expected = context.Canceled
			case "expired":
				expected = context.DeadlineExceeded
			}
			if mode == "approved" || mode == "denied" || mode == "revoked" {
				decision := agentcore.PermissionApproved
				if mode == "denied" {
					decision = agentcore.PermissionDenied
				}
				if err := runner.AnswerPermission(ctx, agentcore.AnswerPermissionCommand{RequestID: id, Decision: decision}); err != nil {
					t.Fatal(err)
				}
			}
			if err := waitCallbackError(t, done); !errors.Is(err, expected) {
				t.Fatalf("approval outcome=%v want=%v", err, expected)
			}
			wantCalls := int32(0)
			if mode == "approved" {
				wantCalls = 1
			}
			if dispatched.Load() != wantCalls || len(runner.PendingPermissionRequests()) != 0 {
				t.Fatal("approval outcome dispatched unexpectedly or remained pending")
			}
			if mode != "approved" {
				checkpoint := runner.checkpoint
				if _, err := runner.config.Checkpoints.Claim(context.Background(), checkpoint.TenantID, checkpoint.AgentID, checkpoint.ID, checkpoint.ScopeHash, "new-owner", checkpoint.RunID); !errors.Is(err, ErrUnsafeResume) {
					t.Fatalf("stopped approval became restartable: %v", err)
				}
			}
			if err := runner.AnswerPermission(context.Background(), agentcore.AnswerPermissionCommand{RequestID: id, Decision: agentcore.PermissionApproved}); err == nil {
				t.Fatal("late approval resurrected a finished operation")
			}
		})
	}
}

func TestToolApprovalRememberDoesNotAuthorizeAnotherCallOrParallelize(t *testing.T) {
	var dispatched atomic.Int32
	ids := []string{"knowledge.search"}
	runner, input := openRunner(t, parallelReply(toolCall("one", `{"kb":"public"}`), toolCall("two", `{"kb":"public"}`)), func(c *Config, _ *agentcore.RuntimeRequest) {
		c.ApprovalToolIDs, c.ParallelToolIDs, c.MaxParallelTools = ids, ids, 4
		c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
			dispatched.Add(1)
			return runtimecore.RuntimeToolCallResult{Output: map[string]any{"ok": true}}, nil
		})
	})
	ids[0] = "mutated-host-slice"
	requests := make(chan string, 2)
	done := make(chan error, 1)
	go func() {
		_, err := runner.Run(context.Background(), input, func(event agentcore.SessionEvent) error {
			if event.Kind == agentcore.EventPermission {
				requests <- event.Permission.RequestID
			}
			return nil
		})
		done <- err
	}()
	prior := ""
	for i := int32(0); i < 2; i++ {
		select {
		case id := <-requests:
			if id == prior || dispatched.Load() != i || len(runner.PendingPermissionRequests()) != 1 {
				t.Fatal("approval identity reused or tool crossed an approval barrier")
			}
			prior = id
			if err := runner.AnswerPermission(context.Background(), agentcore.AnswerPermissionCommand{RequestID: id, Decision: agentcore.PermissionAllowAlways}); err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("fresh approval was not requested")
		}
	}
	if err := waitCallbackError(t, done); err != nil || dispatched.Load() != 2 {
		t.Fatalf("approved batch: calls=%d err=%v", dispatched.Load(), err)
	}
}
