package agentkernel

import (
	"context"
	"encoding/json"
	"errors"
	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/llm"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/providers/kernelcompat"
	"orrery_v2/internal/agent/runtime/providers/profilemodel"
	"orrery_v2/internal/agent/runtime/session"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type modelFunc struct {
	stream func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error)
	calls  atomic.Int32
}

func (m *modelFunc) Generate(context.Context, []*llm.Message, ...llm.Option) (*llm.Message, error) {
	return nil, errors.New("unexpected Generate")
}
func (m *modelFunc) Stream(ctx context.Context, input []*llm.Message, opts ...llm.Option) (*llm.MessageStream, error) {
	m.calls.Add(1)
	return m.stream(ctx, input, opts...)
}

func reply(text, finish string, calls ...llm.ToolCall) *llm.Message {
	return &llm.Message{Role: llm.Assistant, Content: text, ToolCalls: calls, ResponseMeta: &llm.ResponseMeta{FinishReason: finish}}
}

func toolCall(id, arguments string) llm.ToolCall {
	return llm.ToolCall{ID: id, Type: "function", Function: llm.FunctionCall{Name: "knowledge_search", Arguments: arguments}}
}

func searchBinding() agentcore.RuntimeToolBinding {
	return agentcore.RuntimeToolBinding{ToolID: "knowledge.search", Alias: "knowledge_search", SecretEnv: map[string]string{"PRIVATE_KEY": "never-send-to-model"}, InputSchema: map[string]any{
		"type": "object", "properties": map[string]any{"kb": map[string]any{"type": "string"}}, "required": []any{"kb"}, "additionalProperties": false,
	}}
}

type kernelTestModelFactory struct{ Model llm.ChatModel }

func (f kernelTestModelFactory) ModelForRequest(ctx context.Context, _ profilemodel.ModelRequest) (llm.ChatModel, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return f.Model, nil
}

func requestForTest(t *testing.T) agentcore.RuntimeRequest {
	t.Helper()
	return agentcore.RuntimeRequest{TenantID: "tenant-public", AgentID: "A000014", RunID: "kernel-run",
		ExecutionPrincipal: agentcore.ExecutionPrincipal{Kind: agentcore.ExecutionPrincipalUser, ID: "public-user", TenantID: "tenant-public", PermissionScope: []string{"knowledge.public.read"}},
		InputMessages:      []agentcore.RuntimeChatMessage{{Role: "system", Content: "Use authorized sources."}, {Role: "user", Content: "hello"}},
		ToolBindings:       []agentcore.RuntimeToolBinding{searchBinding()}, RuntimeOptions: agentcore.RuntimeOptions{WorkingDirectory: t.TempDir(), Model: "trusted-model"}}
}

func openRunner(t *testing.T, m llm.ChatModel, configure func(*Config, *agentcore.RuntimeRequest)) (*Runner, agentcore.SendCommand) {
	t.Helper()
	req := requestForTest(t)
	config := Config{Workspaces: session.NewWorkspaceBroker(), Models: kernelTestModelFactory{Model: m},
		ToolJournal: newTestJournal(t),
		Tools: runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
			return runtimecore.RuntimeToolCallResult{Output: map[string]any{"answer": "public"}}, nil
		}),
		AuthorizeTool: func(context.Context, runtimecore.RuntimeToolCallRequest) error { return nil }}
	if configure != nil {
		configure(&config, &req)
	}
	engine, err := NewEngine(config)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := config.Workspaces.Bind(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := engine.OpenSession(context.Background(), agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "kernel-session"}, agentcore.SessionConfig{WorkspaceRef: binding.Ref, Model: "untrusted-override"})
	if err != nil {
		t.Fatal(err)
	}
	runner := backend.(*Runner)
	t.Cleanup(func() { _ = runner.Close(context.Background()); config.Workspaces.Release(binding.Ref) })
	return runner, agentcore.SendCommand{Content: []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: strings.TrimSpace(agentcore.RuntimePromptMessagesFallbackPrompt(req.InputMessages))}}}
}

func discard(agentcore.SessionEvent) error { return nil }

func TestLoopStreamsToolsAndPreservesStructuredMultiTurnHistory(t *testing.T) {
	var modelCalls, toolCalls, authorizations int
	m := &modelFunc{stream: func(_ context.Context, messages []*llm.Message, opts ...llm.Option) (*llm.MessageStream, error) {
		modelCalls++
		info, _ := json.Marshal(llm.Apply(opts...).Tools)
		if strings.Contains(string(info), "never-send-to-model") {
			t.Fatal("tool credentials leaked to model")
		}
		switch modelCalls {
		case 1:
			if len(messages) != 2 || messages[0].Role != llm.System || messages[1].Content != "hello" {
				t.Fatalf("initial context was flattened or duplicated: %+v", messages)
			}
			index := 0
			first := reply("checking", "", toolCall("call-1", `{"kb":`))
			first.ToolCalls[0].Index = &index
			second := reply("", "tool_calls", llm.ToolCall{Index: &index, Function: llm.FunctionCall{Arguments: `"public"}`}})
			return llm.NewMessageStream([]*llm.Message{first, second}), nil
		case 2:
			if len(messages) != 4 || messages[2].ToolCalls[0].ID != "call-1" || messages[3].ToolCallID != "call-1" || !strings.Contains(messages[3].Content, "public document") {
				t.Fatalf("tool pairing lost: %+v", messages)
			}
			answer := reply("public answer", "stop")
			answer.ResponseMeta.Usage = &llm.TokenUsage{PromptTokens: 10, CompletionTokens: 3, TotalTokens: 13}
			return llm.NewMessageStream([]*llm.Message{answer}), nil
		case 3:
			if len(messages) != 6 || messages[4].Content != "public answer" || messages[5].Content != "follow up" {
				t.Fatalf("multi-turn history lost: %+v", messages)
			}
			return llm.NewMessageStream([]*llm.Message{reply("second answer", "stop")}), nil
		default:
			t.Fatal("unexpected model call")
			return nil, ErrModel
		}
	}}
	runner, input := openRunner(t, m, func(config *Config, req *agentcore.RuntimeRequest) {
		req.ExecutionEnvelope = &agentcore.ExecutionEnvelope{TenantID: req.TenantID, RunID: req.RunID.String(), AgentID: req.AgentID.String(), AgentProfileID: req.AgentID.String(), PrincipalID: req.ExecutionPrincipal.ID, PrincipalKind: req.ExecutionPrincipal.Kind}
		if err := req.ExecutionEnvelope.SealAt(time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		config.AuthorizeTool = func(_ context.Context, call runtimecore.RuntimeToolCallRequest) error {
			authorizations++
			if call.TenantID != "tenant-public" || call.AgentID != "A000014" || call.ExecutionPrincipal.ID != "public-user" || call.Input["kb"] != "public" {
				t.Fatalf("authority changed: %+v", call)
			}
			// Mutating the policy's copy must not alter the actual dispatch.
			call.Input["kb"] = "internal"
			call.ExecutionPrincipal.PermissionScope[0] = "internal.read"
			if call.ExecutionEnvelope == nil {
				t.Fatal("authorization snapshot lost")
			}
			call.ExecutionEnvelope.TenantID = "forged"
			return nil
		}
		config.Tools = runtimecore.RuntimeToolDispatcherFunc(func(_ context.Context, call runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
			toolCalls++
			if call.ExecutionEnvelope == nil || call.ExecutionEnvelope.Verify() != nil || call.ExecutionEnvelope.TenantID != "tenant-public" {
				t.Fatal("dispatch lost or shared the authorization snapshot")
			}
			if call.Name != "knowledge.search" || call.Input["kb"] != "public" || call.ExecutionPrincipal.PermissionScope[0] != "knowledge.public.read" {
				t.Fatalf("validated arguments changed: %+v", call)
			}
			return runtimecore.RuntimeToolCallResult{Output: map[string]any{"answer": "public document"}}, nil
		})
	})
	var events []agentcore.SessionEvent
	result, err := runner.Run(context.Background(), input, func(event agentcore.SessionEvent) error { events = append(events, event); return event.Validate() })
	if err != nil || result.ResultText != "public answer" || result.Outcome.Kind != agentcore.TurnOutcomeCompleted || toolCalls != 1 || authorizations != 1 {
		t.Fatalf("result=%+v, err=%v, tools=%d, policy=%d", result, err, toolCalls, authorizations)
	}
	if len(events) != 5 || events[1].Kind != agentcore.EventToolCall || events[2].Kind != agentcore.EventToolResult || events[4].UsageDelta.TotalTokens != 13 {
		t.Fatalf("events=%+v", events)
	}
	result, err = runner.Run(context.Background(), agentcore.SendCommand{Content: []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: "follow up"}}}, discard)
	if err != nil || result.ResultText != "second answer" {
		t.Fatalf("follow up=%+v, %v", result, err)
	}
}

func TestLoopRejectsWholeInvalidBatchBeforeAnyExecution(t *testing.T) {
	for _, mode := range []string{"unknown", "malformed", "missing", "wrong-type", "extra", "null", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			bad := toolCall("second", `{"kb":"public"}`)
			switch mode {
			case "unknown":
				bad.Function.Name = "internal_admin"
			case "malformed":
				bad.Function.Arguments = "{"
			case "missing":
				bad.Function.Arguments = `{}`
			case "wrong-type":
				bad.Function.Arguments = `{"kb":1}`
			case "extra":
				bad.Function.Arguments = `{"kb":"public","tenant":"other"}`
			case "null":
				bad.Function.Arguments = `null`
			case "duplicate":
				bad.ID = "first"
			}
			m := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
				return llm.NewMessageStream([]*llm.Message{reply("", "tool_calls", toolCall("first", `{"kb":"public"}`), bad)}), nil
			}}
			var calls int
			runner, input := openRunner(t, m, func(c *Config, _ *agentcore.RuntimeRequest) {
				c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
					calls++
					return runtimecore.RuntimeToolCallResult{}, nil
				})
			})
			if _, err := runner.Run(context.Background(), input, discard); err == nil || calls != 0 {
				t.Fatalf("invalid batch executed: %v, calls=%d", err, calls)
			}
		})
	}
}

func TestLoopDeniesInternalKnowledgeAndStopsAfterToolTransportFailure(t *testing.T) {
	for _, mode := range []string{"internal", "transport"} {
		t.Run(mode, func(t *testing.T) {
			m := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
				return llm.NewMessageStream([]*llm.Message{reply("", "tool_calls", toolCall("first", `{"kb":"`+mode+`"}`))}), nil
			}}
			var calls int
			runner, input := openRunner(t, m, func(c *Config, _ *agentcore.RuntimeRequest) {
				c.AuthorizeTool = func(_ context.Context, call runtimecore.RuntimeToolCallRequest) error {
					if call.Input["kb"] == "internal" {
						return ErrToolDenied
					}
					return nil
				}
				c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
					calls++
					return runtimecore.RuntimeToolCallResult{}, errors.New("private credential detail")
				})
			})
			_, err := runner.Run(context.Background(), input, discard)
			if err == nil || strings.Contains(err.Error(), "private credential") || m.calls.Load() != 1 {
				t.Fatalf("unsafe retry or error: %v, calls=%d", err, m.calls.Load())
			}
			if mode == "internal" && calls != 0 || mode == "transport" && calls != 1 {
				t.Fatalf("wrong dispatch count: %d", calls)
			}
			if _, err := runner.Run(context.Background(), input, discard); !errors.Is(err, ErrClosed) {
				t.Fatalf("failed session reused: %v", err)
			}
		})
	}
}

func TestLoopBudgetsStopToolsBeforeUnfinishableExecution(t *testing.T) {
	for _, mode := range []string{"model", "tools", "request-narrows", "request-cannot-expand", "response", "context-after-model"} {
		t.Run(mode, func(t *testing.T) {
			m := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
				return llm.NewMessageStream([]*llm.Message{reply("", "tool_calls", toolCall("first", `{"kb":"public"}`), toolCall("second", `{"kb":"public"}`))}), nil
			}}
			var calls int
			runner, input := openRunner(t, m, func(c *Config, req *agentcore.RuntimeRequest) {
				switch mode {
				case "model":
					c.MaxModelCalls = 1
				case "tools":
					c.MaxToolCalls = 1
				case "request-narrows":
					req.RuntimeOptions.MaxTurns = 1
				case "request-cannot-expand":
					c.MaxModelCalls = 1
					req.RuntimeOptions.MaxTurns = 100
				case "response":
					c.MaxResponseBytes = 10
				case "context-after-model":
					c.MaxContextBytes = 300
				}
				c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
					calls++
					return runtimecore.RuntimeToolCallResult{}, nil
				})
			})
			if _, err := runner.Run(context.Background(), input, discard); !errors.Is(err, ErrBudget) || calls != 0 {
				t.Fatalf("budget ignored: %v, tools=%d", err, calls)
			}
		})
	}
}

func TestLoopIncompleteResponsesAndEmitFailuresNeverBecomeSuccess(t *testing.T) {
	for _, mode := range []string{"missing-finish", "length", "empty", "wrong-role", "nil", "emit"} {
		t.Run(mode, func(t *testing.T) {
			message := reply("answer", "stop")
			switch mode {
			case "missing-finish":
				message.ResponseMeta = nil
			case "length":
				message.ResponseMeta.FinishReason = "length"
			case "empty":
				message.Content = ""
			case "wrong-role":
				message.Role = llm.System
			case "nil":
				message = nil
			}
			m := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
				return llm.NewMessageStream([]*llm.Message{message}), nil
			}}
			runner, input := openRunner(t, m, nil)
			emit := kernelcompat.Emit(discard)
			if mode == "emit" {
				emit = func(agentcore.SessionEvent) error { return errors.New("consumer failed") }
			}
			if result, err := runner.Run(context.Background(), input, emit); err == nil || result.Outcome.Kind == agentcore.TurnOutcomeCompleted {
				t.Fatalf("invalid result became success: %+v, %v", result, err)
			}
		})
	}
}

func TestLoopModelTimeoutAndCloseInterruptBlockedStream(t *testing.T) {
	for _, mode := range []string{"timeout", "close"} {
		t.Run(mode, func(t *testing.T) {
			started := make(chan struct{})
			reader, writer := llm.MessagePipe(0)
			defer writer.Close()
			m := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
				close(started)
				return reader, nil
			}}
			runner, input := openRunner(t, m, func(c *Config, _ *agentcore.RuntimeRequest) {
				if mode == "timeout" {
					c.ModelTimeout = 25 * time.Millisecond
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() { _, err := runner.Run(ctx, input, discard); result <- err }()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("model did not start")
			}
			if mode == "close" {
				if _, err := runner.Run(ctx, input, discard); !errors.Is(err, ErrBusy) {
					t.Fatalf("overlapping run accepted: %v", err)
				}
				_ = runner.Close(context.Background())
			}
			select {
			case err := <-result:
				want := context.DeadlineExceeded
				if mode == "close" {
					want = context.Canceled
				}
				if !errors.Is(err, want) {
					t.Fatalf("cancellation = %v, want %v", err, want)
				}
			case <-ctx.Done():
				t.Fatal("blocked stream was not interrupted")
			}
		})
	}
}

func TestLoopDoesNotReplayToolIDsAcrossModelRounds(t *testing.T) {
	m := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
		return llm.NewMessageStream([]*llm.Message{reply("", "tool_calls", toolCall("same-id", `{"kb":"public"}`))}), nil
	}}
	var calls int
	runner, input := openRunner(t, m, func(c *Config, _ *agentcore.RuntimeRequest) {
		c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
			calls++
			return runtimecore.RuntimeToolCallResult{Output: map[string]any{"answer": "source"}}, nil
		})
	})
	if _, err := runner.Run(context.Background(), input, discard); !errors.Is(err, ErrProtocol) || calls != 1 || m.calls.Load() != 2 {
		t.Fatalf("tool replay: err=%v, tools=%d, model=%d", err, calls, m.calls.Load())
	}
}

func TestLoopToolDeadlineDoesNotRetryOrAdvanceModel(t *testing.T) {
	m := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
		return llm.NewMessageStream([]*llm.Message{reply("", "tool_calls", toolCall("call", `{"kb":"public"}`))}), nil
	}}
	var calls atomic.Int32
	runner, input := openRunner(t, m, func(c *Config, _ *agentcore.RuntimeRequest) {
		c.ToolTimeout = 25 * time.Millisecond
		c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(ctx context.Context, _ runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
			calls.Add(1)
			<-ctx.Done()
			return runtimecore.RuntimeToolCallResult{}, ctx.Err()
		})
	})
	if _, err := runner.Run(context.Background(), input, discard); !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 || m.calls.Load() != 1 {
		t.Fatalf("deadline ignored: %v, tools=%d, model=%d", err, calls.Load(), m.calls.Load())
	}
}

type factoryFunc func(context.Context, profilemodel.ModelRequest) (llm.ChatModel, error)

func (f factoryFunc) ModelForRequest(ctx context.Context, req profilemodel.ModelRequest) (llm.ChatModel, error) {
	return f(ctx, req)
}

func TestEngineRequiresAuthorityAndRejectsInvalidInitialStateBeforeModelCreation(t *testing.T) {
	for _, mode := range []string{"missing-policy", "missing-dispatcher", "missing-journal", "collision", "external-schema", "orphan-history", "context-budget", "explicit-scopes", "unknown-workspace"} {
		t.Run(mode, func(t *testing.T) {
			var models int
			req := requestForTest(t)
			config := Config{Workspaces: session.NewWorkspaceBroker(), Models: factoryFunc(func(context.Context, profilemodel.ModelRequest) (llm.ChatModel, error) { models++; return nil, nil }),
				ToolJournal:   newTestJournal(t),
				AuthorizeTool: func(context.Context, runtimecore.RuntimeToolCallRequest) error { return nil },
				Tools: runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
					t.Error("unexpected tool call")
					return runtimecore.RuntimeToolCallResult{}, nil
				})}
			switch mode {
			case "missing-policy":
				config.AuthorizeTool = nil
			case "missing-dispatcher":
				config.Tools = nil
			case "missing-journal":
				config.ToolJournal = nil
			case "collision":
				req.ToolBindings = append(req.ToolBindings, searchBinding())
			case "external-schema":
				req.ToolBindings[0].InputSchema = map[string]any{"$ref": "https://invalid.example/schema"}
			case "orphan-history":
				req.InputMessages = []agentcore.RuntimeChatMessage{{Role: "tool", ToolCallID: "unknown", Content: "private"}}
			case "context-budget":
				config.MaxContextBytes = 1
			case "explicit-scopes":
				req.ContextPack.ResponsibilityContext = &agentcore.ResponsibilityContext{RequireExplicitToolScopes: true}
			}
			engine, err := NewEngine(config)
			if err != nil {
				t.Fatal(err)
			}
			binding, err := config.Workspaces.Bind(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "unknown-workspace" {
				binding.Ref = "unknown"
			}
			if runner, err := engine.OpenSession(context.Background(), agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "test"}, agentcore.SessionConfig{WorkspaceRef: binding.Ref}); err == nil || runner != nil || models != 0 {
				t.Fatalf("unsafe session opened: %T, %v, models=%d", runner, err, models)
			}
		})
	}
}

func TestEngineUsesTrustedModelAndRejectsChangedInitialPrompt(t *testing.T) {
	m := &modelFunc{stream: func(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
		return llm.NewMessageStream([]*llm.Message{reply("answer", "stop")}), nil
	}}
	runner, input := openRunner(t, m, func(c *Config, _ *agentcore.RuntimeRequest) {
		c.Models = factoryFunc(func(_ context.Context, req profilemodel.ModelRequest) (llm.ChatModel, error) {
			if req.Model != "trusted-model" {
				t.Fatalf("model override expanded authority: %+v", req)
			}
			return m, nil
		})
	})
	input.Content[0].Text = "changed after authorization"
	if _, err := runner.Run(context.Background(), input, discard); !errors.Is(err, ErrInput) || m.calls.Load() != 0 {
		t.Fatalf("changed prompt reached model: %v", err)
	}
}
