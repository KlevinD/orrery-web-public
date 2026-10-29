package agentkernel

import (
	"context"
	"errors"
	"fmt"
	"orrery_v2/internal/agent/llm"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/providers/profilemodel"
)

type memoryModelFactoryFunc func(context.Context, profilemodel.ModelRequest) (llm.ChatModel, error)

func (f memoryModelFactoryFunc) ModelForRequest(ctx context.Context, req profilemodel.ModelRequest) (llm.ChatModel, error) {
	return f(ctx, req)
}

type identifiedMemoryModel struct {
	*modelFunc
	identity string
}

func (m identifiedMemoryModel) ModelIdentity() string { return m.identity }

func TestResumeRejectsResolvedModelChangeUnderSameProfileID(t *testing.T) {
	base := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
		return llm.NewMessageStream([]*llm.Message{reply("answer", "stop")}), nil
	}}
	r, input := openRunner(t, identifiedMemoryModel{modelFunc: base, identity: strings.Repeat("a", 64)}, func(c *Config, _ *agentcore.RuntimeRequest) { checkpointConfig(t, c) })
	if _, err := r.Run(context.Background(), input, discard); err != nil {
		t.Fatal(err)
	}
	if _, err := resumeTestRunner(t, r, r.request, identifiedMemoryModel{modelFunc: base, identity: strings.Repeat("b", 64)}); !errors.Is(err, ErrCheckpoint) {
		t.Fatalf("resolved profile change accepted: %v", err)
	}
	if base.calls.Load() != 1 {
		t.Fatal("rejected resume sent conversation to new model")
	}
}

func testCheckpoint(t *testing.T) Checkpoint {
	t.Helper()
	req := requestForTest(t)
	scope, err := checkpointScope(req)
	if err != nil {
		t.Fatal(err)
	}
	return Checkpoint{ID: newCheckpointID(), TenantID: req.TenantID, AgentID: req.AgentID, RunID: req.RunID,
		ScopeHash: scope, Owner: "original-owner", Phase: CheckpointReady, Messages: []*llm.Message{llm.UserMessage("original goal")}}
}

func TestCheckpointReopenFencesOldOwnerAndScopesClaims(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "checkpoint.db")
	_, db1 := openTestJournal(t, path)
	store, err := NewSQLCheckpointStore(db1)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(ctx, testCheckpoint(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, created); !errors.Is(err, ErrCheckpoint) {
		t.Fatalf("duplicate checkpoint: %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatal(err)
	}
	_, db2 := openTestJournal(t, path)
	store, _ = NewSQLCheckpointStore(db2)
	for _, change := range []func(*Checkpoint){
		func(c *Checkpoint) { c.TenantID = "other-tenant" },
		func(c *Checkpoint) { c.AgentID = "A000099" },
		func(c *Checkpoint) { c.ScopeHash = strings.Repeat("0", 64) },
		func(c *Checkpoint) { c.RunID = "different-run" },
	} {
		c := created
		change(&c)
		if _, err := store.Claim(ctx, c.TenantID, c.AgentID, c.ID, c.ScopeHash, "new-owner", c.RunID); !errors.Is(err, ErrCheckpoint) {
			t.Fatalf("claim crossed boundary: %v", err)
		}
	}
	claimed, err := store.Claim(ctx, created.TenantID, created.AgentID, created.ID, created.ScopeHash, "new-owner", created.RunID)
	if err != nil || claimed.Revision != created.Revision+1 || claimed.Messages[0].Content != "original goal" {
		t.Fatalf("recovery lost state: %+v %v", claimed, err)
	}
	if _, err := store.Save(ctx, created); !errors.Is(err, ErrCheckpoint) {
		t.Fatalf("stale owner still writes: %v", err)
	}
	claimed.ModelCalls++
	claimed, err = store.Save(ctx, claimed)
	if err != nil {
		t.Fatal(err)
	}
	claimed.ModelCalls--
	if _, err := store.Save(ctx, claimed); !errors.Is(err, ErrCheckpoint) {
		t.Fatalf("budget reset: %v", err)
	}
	claimed.Turn++
	if _, err := store.Save(ctx, claimed); !errors.Is(err, ErrCheckpoint) {
		t.Fatalf("unfinished turn advanced to reset budget: %v", err)
	}
}

func TestCheckpointRejectsPendingMalformedAndExpiredResume(t *testing.T) {
	journal := newTestJournal(t)
	store, _ := NewSQLCheckpointStore(journal.store)
	for _, phase := range []CheckpointPhase{CheckpointReady, CheckpointToolsPending} {
		c := testCheckpoint(t)
		c.Phase = phase
		c.Messages = append(c.Messages, reply("", "tool_calls", toolCall("unfinished", `{"kb":"public"}`)))
		created, err := store.Create(context.Background(), c)
		if phase == CheckpointReady {
			if !errors.Is(err, ErrCheckpoint) {
				t.Fatalf("incomplete ready state accepted: %v", err)
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.Claim(context.Background(), c.TenantID, c.AgentID, c.ID, c.ScopeHash, "new", c.RunID); !errors.Is(err, ErrUnsafeResume) {
				t.Fatalf("pending tools replay allowed: %+v %v", created, err)
			}
		}
	}
	c := testCheckpoint(t)
	c.Deadline = time.Now().Add(-time.Second)
	if _, err := store.Create(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(context.Background(), c.TenantID, c.AgentID, c.ID, c.ScopeHash, "new", c.RunID); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired deadline reset: %v", err)
	}
}

func TestCheckpointScopeIncludesAuthorityAndExcludesVolatileHistory(t *testing.T) {
	req := requestForTest(t)
	want, _ := checkpointScope(req)
	changes := map[string]func(*agentcore.RuntimeRequest){
		"principal":    func(r *agentcore.RuntimeRequest) { r.ExecutionPrincipal.ID = "another-user" },
		"permissions":  func(r *agentcore.RuntimeRequest) { r.ContextPack.PermissionScope = "internal" },
		"conversation": func(r *agentcore.RuntimeRequest) { r.ContextPack.ConversationID = "other-thread" },
		"tool scope":   func(r *agentcore.RuntimeRequest) { r.ContextPack.ToolScope = "internal" },
		"grant":        func(r *agentcore.RuntimeRequest) { r.ToolBindings[0].GrantedPermissions = []string{"new-scope"} },
		"model":        func(r *agentcore.RuntimeRequest) { r.RuntimeOptions.Model = "other-model" },
		"system":       func(r *agentcore.RuntimeRequest) { r.InputMessages[0].Content = "different instructions" },
		"approval": func(r *agentcore.RuntimeRequest) {
			r.RuntimeOptions.ProviderSpecific = map[string]string{agentcore.RuntimeConversationHILModeProviderKey: "always"}
		},
		"task recovery": func(r *agentcore.RuntimeRequest) {
			r.RuntimeOptions.ProviderSpecific = map[string]string{"agentcore_task_recovery": "automatic"}
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			r := requestForTest(t)
			change(&r)
			got, err := checkpointScope(r)
			if err != nil || got == want {
				t.Fatalf("authority hash unchanged: %s %v", got, err)
			}
		})
	}
	req.RunID = "next-run"
	req.ContextPack.CreatedAt = time.Now()
	req.InputMessages = append(req.InputMessages, agentcore.RuntimeChatMessage{Role: "user", Content: "follow up"})
	if got, err := checkpointScope(req); err != nil || got != want {
		t.Fatalf("volatile history invalidates cache: %s %v", got, err)
	}
}

func checkpointConfig(t *testing.T, config *Config) {
	t.Helper()
	store, err := NewSQLCheckpointStore(config.ToolJournal.(*SQLToolJournal).store)
	if err != nil {
		t.Fatal(err)
	}
	config.Checkpoints = store
}

func resumeTestRunner(t *testing.T, previous *Runner, req agentcore.RuntimeRequest, m llm.ChatModel) (*Runner, error) {
	t.Helper()
	config := previous.config
	config.Models = kernelTestModelFactory{Model: m}
	engine, err := NewEngine(config)
	if err != nil {
		return nil, err
	}
	binding, err := config.Workspaces.Bind(context.Background(), req)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { config.Workspaces.Release(binding.Ref) })
	backend, err := engine.OpenSession(context.Background(), agentcore.SessionSpec{Mode: agentcore.SessionOpenResume, SessionID: "resumed", BackendSessionID: previous.BackendSessionID()}, agentcore.SessionConfig{WorkspaceRef: binding.Ref})
	if err != nil {
		return nil, err
	}
	r := backend.(*Runner)
	t.Cleanup(func() { _ = r.Close(context.Background()) })
	return r, nil
}

func TestResumeAfterModelFailureReusesToolResultWithoutRedispatch(t *testing.T) {
	var dispatched int
	m := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, _ ...llm.Option) (*llm.MessageStream, error) {
		if messages[len(messages)-1].Role == llm.ToolRole {
			return nil, errors.New("upstream unavailable")
		}
		return llm.NewMessageStream([]*llm.Message{reply("", "tool_calls", toolCall("durable", `{"kb":"public"}`))}), nil
	}}
	r, input := openRunner(t, m, func(c *Config, _ *agentcore.RuntimeRequest) {
		checkpointConfig(t, c)
		c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
			dispatched++
			return runtimecore.RuntimeToolCallResult{Output: map[string]any{"answer": "authorized evidence"}}, nil
		})
	})
	if _, err := r.Run(context.Background(), input, discard); !errors.Is(err, ErrModel) {
		t.Fatalf("expected model failure: %v", err)
	}
	_ = r.Close(context.Background())
	answer := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, _ ...llm.Option) (*llm.MessageStream, error) {
		if len(messages) != 4 || messages[3].ToolCallID != "durable" || !strings.Contains(messages[3].Content, "authorized evidence") {
			t.Fatalf("safe boundary lost: %+v", messages)
		}
		return llm.NewMessageStream([]*llm.Message{reply("recovered answer", "stop")}), nil
	}}
	resumed, err := resumeTestRunner(t, r, r.request, answer)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.modelCalls != 2 || resumed.toolCalls != 1 {
		t.Fatal("resume reset execution budgets")
	}
	result, err := resumed.Run(context.Background(), input, discard)
	if err != nil || result.ResultText != "recovered answer" || dispatched != 1 || resumed.modelCalls != 3 {
		t.Fatalf("resume replayed/lost work: %+v %v dispatches=%d", result, err, dispatched)
	}
}

func TestCompletedResumeAppendsLatestUserOnlyAndKeepsCacheKey(t *testing.T) {
	var cacheKeys []string
	answer := &modelFunc{stream: func(_ context.Context, _ []*llm.Message, _ ...llm.Option) (*llm.MessageStream, error) {
		return llm.NewMessageStream([]*llm.Message{reply("first answer", "stop")}), nil
	}}
	r, input := openRunner(t, answer, func(c *Config, _ *agentcore.RuntimeRequest) {
		checkpointConfig(t, c)
		c.PromptCache = true
		c.Models = memoryModelFactoryFunc(func(_ context.Context, req profilemodel.ModelRequest) (llm.ChatModel, error) {
			cacheKeys = append(cacheKeys, req.PromptCacheKey)
			return answer, nil
		})
	})
	if _, err := r.Run(context.Background(), input, discard); err != nil {
		t.Fatal(err)
	}
	req := r.request
	req.RunID = "next-run"
	req.InputMessages = append(append([]agentcore.RuntimeChatMessage(nil), req.InputMessages...), agentcore.RuntimeChatMessage{Role: "assistant", Content: "first answer"}, agentcore.RuntimeChatMessage{Role: "user", Content: "follow up"})
	second := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, _ ...llm.Option) (*llm.MessageStream, error) {
		if len(messages) != 4 || messages[3].Content != "follow up" {
			t.Fatalf("history duplicated: %+v", messages)
		}
		return llm.NewMessageStream([]*llm.Message{reply("next answer", "stop")}), nil
	}}
	config := r.config
	config.Models = memoryModelFactoryFunc(func(_ context.Context, req profilemodel.ModelRequest) (llm.ChatModel, error) {
		cacheKeys = append(cacheKeys, req.PromptCacheKey)
		return second, nil
	})
	// Keep the recording factory when opening the resumed session.
	engine, _ := NewEngine(config)
	binding, err := config.Workspaces.Bind(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer config.Workspaces.Release(binding.Ref)
	backend, err := engine.OpenSession(context.Background(), agentcore.SessionSpec{Mode: agentcore.SessionOpenResume, SessionID: "next", BackendSessionID: r.BackendSessionID()}, agentcore.SessionConfig{WorkspaceRef: binding.Ref})
	if err != nil {
		t.Fatal(err)
	}
	resumed := backend.(*Runner)
	defer resumed.Close(context.Background())
	command := agentcore.SendCommand{Content: []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: agentcore.RuntimePromptMessagesFallbackPrompt(req.InputMessages)}}}
	if _, err := resumed.Run(context.Background(), command, discard); err != nil {
		t.Fatal(err)
	}
	if len(cacheKeys) != 2 || cacheKeys[0] == "" || cacheKeys[0] != cacheKeys[1] {
		t.Fatalf("unstable cache identity: %v", cacheKeys)
	}
}

type failingCheckpointStore struct {
	CheckpointStore
	fail func(Checkpoint) bool
}

func (s failingCheckpointStore) Save(ctx context.Context, c Checkpoint) (Checkpoint, error) {
	if s.fail(c) {
		return Checkpoint{}, ErrCheckpoint
	}
	return s.CheckpointStore.Save(ctx, c)
}

func TestCheckpointFailureBlocksUnsafeToolReplay(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(fmt.Sprint("after-tool-", after), func(t *testing.T) {
			dispatches := 0
			r, input := openRunner(t, toolThenAnswerModel(), func(c *Config, _ *agentcore.RuntimeRequest) {
				checkpointConfig(t, c)
				c.Checkpoints = failingCheckpointStore{CheckpointStore: c.Checkpoints, fail: func(cp Checkpoint) bool {
					if !after {
						return cp.Phase == CheckpointToolsPending
					}
					return cp.Phase == CheckpointReady && len(cp.Messages) > 0 && cp.Messages[len(cp.Messages)-1].Role == llm.ToolRole
				}}
				c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
					dispatches++
					return runtimecore.RuntimeToolCallResult{Output: map[string]any{"answer": "public"}}, nil
				})
			})
			if _, err := r.Run(context.Background(), input, discard); !errors.Is(err, ErrCheckpoint) {
				t.Fatalf("write failure ignored: %v", err)
			}
			if dispatches != map[bool]int{false: 0, true: 1}[after] {
				t.Fatalf("unexpected dispatch count: %d", dispatches)
			}
			if after {
				if _, err := resumeTestRunner(t, r, r.request, toolThenAnswerModel()); !errors.Is(err, ErrUnsafeResume) {
					t.Fatalf("uncertain batch replay allowed: %v", err)
				}
			}
		})
	}
}
