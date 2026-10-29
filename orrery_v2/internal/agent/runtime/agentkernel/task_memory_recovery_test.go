package agentkernel

import (
	"context"
	"errors"
	"orrery_v2/internal/agent/llm"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/providers/kernelcompat"
	"orrery_v2/internal/retrievalevidence"
)

func taskMemoryRequest(q *agentcore.RuntimeRequest, checked time.Time) {
	q.ContextPack.PersonalMemoryDigestRef = agentcore.MemoryDigestRef{
		ID: "private-digest", SummaryRef: "Keep the verified article and images.",
		Evidence: []retrievalevidence.Item{retrievalevidence.Seal(retrievalevidence.Item{
			Purpose: retrievalevidence.PurposeMemory, TruthState: retrievalevidence.TruthUnknown,
			Source:      retrievalevidence.Source{Kind: "memory_record", ID: "record-1", ContainerID: "private-memory", Version: "v1"},
			SourceState: "record_verified", AccessHash: "same-authority",
		}, checked)},
	}
	q.InputMessages = []agentcore.RuntimeChatMessage{{Role: "system", Content: "Use authorized sources."},
		{Role: "user", Content: agentcore.BuildMemoryContextPrompt(q.ContextPack)}, {Role: "user", Content: "finish the same task"}}
}

func TestAutomaticTaskMemoryRecheckResumesStoredContextWithoutReplayingTools(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "current"
		if legacy {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			dispatches := 0
			checked := time.Date(2026, 10, 18, 7, 33, 8, 0, time.UTC)
			first := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, _ ...llm.Option) (*llm.MessageStream, error) {
				if messages[len(messages)-1].Role == llm.ToolRole {
					return nil, errors.New("model offline after successful tool")
				}
				return llm.NewMessageStream([]*llm.Message{reply("", "tool_calls", toolCall("saved-once", `{"kb":"public"}`))}), nil
			}}
			r, input := openRunner(t, first, func(c *Config, q *agentcore.RuntimeRequest) {
				automaticTaskConfig(t, c, q)
				taskMemoryRequest(q, checked)
				c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
					dispatches++
					return runtimecore.RuntimeToolCallResult{Output: map[string]any{"receipt": "existing-artifact"}}, nil
				})
			})
			if legacy {
				// A genuine pre-upgrade checkpoint has no optional evidence field.
				store := r.config.Checkpoints.(*SQLCheckpointStore)
				c := *r.checkpoint
				c.InitialMemoryEvidence = ""
				record, err := checkpointRecord(c)
				if err != nil {
					t.Fatal(err)
				}
				if err := store.store.PutRecord(ctx, checkpointTable, checkpointKey(c.TenantID, c.AgentID, c.ID), record); err != nil {
					t.Fatal(err)
				}
				r.checkpoint = &c
			}
			if _, err := r.Run(ctx, input, discard); !errors.Is(err, ErrModel) {
				t.Fatal(err)
			}
			before := *r.checkpoint
			_ = r.Close(ctx)
			q := r.request
			taskMemoryRequest(&q, checked.Add(10*time.Minute))
			answer := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, _ ...llm.Option) (*llm.MessageStream, error) {
				if messages[1].Content != agentcore.BuildMemoryContextPrompt(r.request.ContextPack) || messages[len(messages)-1].Role != llm.ToolRole {
					t.Fatal("recovery replaced the initial snapshot or lost a saved tool receipt")
				}
				return llm.NewMessageStream([]*llm.Message{reply("continued from saved evidence", "stop")}), nil
			}}
			next, err := reopenTaskRunner(t, r, q, answer)
			if err != nil {
				t.Fatal(err)
			}
			if next.modelCalls != before.ModelCalls || next.toolCalls != before.ToolCalls || !next.deadline.Equal(before.Deadline) {
				t.Fatal("recovery replenished counters/deadline")
			}
			resumedInput := agentcore.SendCommand{Content: []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: requestPrompt(q)}}}
			result, err := next.Run(ctx, resumedInput, discard)
			if err != nil || result.ResultText != "continued from saved evidence" || dispatches != 1 {
				t.Fatalf("resume: %v, dispatches=%d", err, dispatches)
			}
			if next.checkpoint.FirstPrompt != before.FirstPrompt || next.checkpoint.InitialMemoryEvidence != before.InitialMemoryEvidence {
				t.Fatal("recovery rewrote the initial snapshot")
			}
			// Another delivery after completion can also refresh audit times. It
			// must return the saved result without new inference or tool work.
			taskMemoryRequest(&q, checked.Add(20*time.Minute))
			never := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
				t.Fatal("completed task repeated inference")
				return nil, nil
			}}
			replayed, err := reopenTaskRunner(t, next, q, never)
			if err != nil {
				t.Fatal(err)
			}
			resumedInput.Content[0].Text = requestPrompt(q)
			result, err = replayed.Run(ctx, resumedInput, discard)
			if err != nil || result.ResultText != "continued from saved evidence" || dispatches != 1 {
				t.Fatalf("completed replay: %v", err)
			}
		})
	}
}

func TestAutomaticTaskMemoryRecheckRejectsRealInputAndAuthorityChanges(t *testing.T) {
	checked := time.Date(2026, 10, 18, 7, 33, 8, 0, time.UTC)
	failed := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
		return nil, errors.New("offline")
	}}
	r, input := openRunner(t, failed, func(c *Config, q *agentcore.RuntimeRequest) {
		automaticTaskConfig(t, c, q)
		taskMemoryRequest(q, checked)
	})
	_, _ = r.Run(context.Background(), input, discard)
	for name, mutate := range map[string]func(*agentcore.RuntimeRequest){
		"summary": func(q *agentcore.RuntimeRequest) {
			q.ContextPack.PersonalMemoryDigestRef.SummaryRef = "different memory"
		},
		"source-version": func(q *agentcore.RuntimeRequest) {
			q.ContextPack.PersonalMemoryDigestRef.Evidence[0].Source.Version = "v2"
		},
		"access-fingerprint": func(q *agentcore.RuntimeRequest) {
			q.ContextPack.PersonalMemoryDigestRef.Evidence[0].InputHash = strings.Repeat("b", 64)
		},
		"truth": func(q *agentcore.RuntimeRequest) {
			q.ContextPack.PersonalMemoryDigestRef.Evidence[0].TruthState = "disputed"
		},
		"user-checked-at": func(q *agentcore.RuntimeRequest) {
			q.InputMessages[2].Content += ` {"checked_at":"2026-10-18T07:43:08Z"}`
		},
		"principal":  func(q *agentcore.RuntimeRequest) { q.ExecutionPrincipal.ID = "other" },
		"permission": func(q *agentcore.RuntimeRequest) { q.ContextPack.PermissionScope = "different" },
		"tool":       func(q *agentcore.RuntimeRequest) { q.ToolBindings = nil },
		"skill": func(q *agentcore.RuntimeRequest) {
			q.SkillBindings = []agentcore.RuntimeSkillBinding{{SkillID: "changed-skill", Version: "v2"}}
		},
		"workspace": func(q *agentcore.RuntimeRequest) {
			q.RuntimeOptions.WorkspacePolicy.Root = q.RuntimeOptions.WorkingDirectory
		},
		"system":               func(q *agentcore.RuntimeRequest) { q.InputMessages[0].Content = "different system policy" },
		"host-message-missing": func(q *agentcore.RuntimeRequest) { q.InputMessages[1].Content += "untrusted suffix" },
	} {
		t.Run(name, func(t *testing.T) {
			q := r.request
			taskMemoryRequest(&q, checked.Add(10*time.Minute))
			mutate(&q)
			// Update only the message generated by the host, except in the test
			// where it deliberately cannot be proven to be that host message.
			if name != "host-message-missing" {
				q.InputMessages[1].Content = agentcore.BuildMemoryContextPrompt(q.ContextPack)
			}
			if _, err := reopenTaskRunner(t, r, q, failed); !errors.Is(err, ErrCheckpoint) {
				t.Fatalf("changed input accepted: %v", err)
			}
		})
	}
	if failed.calls.Load() != 1 {
		t.Fatal("rejected input reached model")
	}
}

func TestManagedOpenFailureKeepsInternalCauseAndSafeCategory(t *testing.T) {
	failed := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
		return nil, errors.New("offline")
	}}
	r, input := openRunner(t, failed, automaticTaskConfigForTest(t))
	_, _ = r.Run(context.Background(), input, discard)
	q := r.request
	q.InputMessages = append([]agentcore.RuntimeChatMessage(nil), q.InputMessages...)
	q.InputMessages[len(q.InputMessages)-1].Content = "different goal"
	engine, err := NewEngine(r.config)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := kernelcompat.NewConnection(kernelcompat.Config{Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	binding, err := r.config.Workspaces.Bind(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	defer r.config.Workspaces.Release(binding.Ref)
	_, err = connection.OpenSession(context.Background(), agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "changed"}, agentcore.SessionConfig{WorkspaceRef: binding.Ref})
	if !errors.Is(err, ErrTaskInputChanged) || !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeBackendRejected) || !strings.Contains(err.Error(), "checkpoint_input_changed") || strings.Contains(err.Error(), "different goal") {
		t.Fatalf("classification/cause lost: %v", err)
	}
	_, err = connection.OpenSession(context.Background(), agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "missing"}, agentcore.SessionConfig{WorkspaceRef: "missing-reference"})
	if !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeWorkspaceUnavailable) {
		t.Fatalf("real workspace failure misclassified: %v", err)
	}
}
