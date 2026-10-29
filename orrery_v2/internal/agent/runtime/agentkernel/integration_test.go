package agentkernel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/llmprofile"
	"orrery_v2/internal/agent/runtime/providers"
	"orrery_v2/internal/agent/runtime/providers/kernelcompat"
	"orrery_v2/internal/agent/runtime/providers/pi"
	"orrery_v2/internal/agent/runtime/providers/profilemodel"
	"orrery_v2/internal/agent/runtime/session"
)

// Exercise the real existing HTTP model adapter, the new loop, tool policy and
// canonical SessionRuntime together. Only the remote model service is a fixture.
func TestKernelCanonicalHTTPModelToolLoopAndKnowledgeBoundary(t *testing.T) {
	for _, mode := range []string{"public", "internal", "denied"} {
		t.Run(mode, func(t *testing.T) {
			var requests, tools atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count := requests.Add(1)
				if r.Header.Get("Authorization") != "Bearer test-model-key" {
					t.Error("model credentials missing")
				}
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					http.Error(w, "bad request", 400)
					return
				}
				if strings.Contains(string(body["tools"]), "never-send-to-model") {
					t.Error("tool credentials leaked")
				}
				var modelName string
				_ = json.Unmarshal(body["model"], &modelName)
				if modelName != "trusted-model" {
					t.Errorf("wrong model: %s", modelName)
				}
				message := map[string]any{"role": "assistant", "content": "public answer"}
				finish := "stop"
				if count == 1 {
					message["content"] = ""
					message["tool_calls"] = []any{map[string]any{"id": "http-call", "type": "function", "function": map[string]any{"name": "knowledge_search", "arguments": `{"kb":"` + mode + `"}`}}}
					finish = "tool_calls"
				} else if !strings.Contains(string(body["messages"]), "approved public source") {
					t.Error("tool result missing from next model request")
				}
				if string(body["stream"]) != "true" {
					t.Error("AgentCore did not request SSE streaming")
				}
				if calls, ok := message["tool_calls"].([]any); ok {
					for index, call := range calls {
						call.(map[string]any)["index"] = index
					}
				}
				w.Header().Set("Content-Type", "text/event-stream")
				chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": message, "finish_reason": finish}}})
				_, _ = w.Write(append(append([]byte("data: "), chunk...), []byte("\n\n")...))
				_, _ = w.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":3,\"total_tokens\":13}}\n\ndata: [DONE]\n\n"))
			}))
			defer server.Close()
			workspaces := session.NewWorkspaceBroker()
			journal := newTestJournal(t)
			factory := profilemodel.NewProfileModelFactory(profilemodel.ProfileModelFactoryConfig{Profiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{ProfileID: "test", Provider: "openai", BaseURL: server.URL + "/v1", APIKey: "test-model-key", Model: "profile-default", Enabled: true, GlobalDefault: true}}}, Client: server.Client()})
			engine, err := NewEngine(Config{Workspaces: workspaces, Models: factory,
				ToolJournal: journal,
				AuthorizeTool: func(_ context.Context, call runtimecore.RuntimeToolCallRequest) error {
					if call.TenantID != "tenant-public" || call.AgentID != "A000014" || call.Input["kb"] != "public" {
						return ErrToolDenied
					}
					return nil
				},
				Tools: runtimecore.RuntimeToolDispatcherFunc(func(_ context.Context, call runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
					tools.Add(1)
					if call.Input["kb"] != "public" {
						t.Error("internal knowledge reached dispatcher")
					}
					return runtimecore.RuntimeToolCallResult{Output: map[string]any{"source": "approved public source"}}, nil
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			registry, err := providers.NewSessionRegistry(providers.SessionRegistryConfig{Workspaces: workspaces,
				ManagedKernel: &kernelcompat.Config{Engine: engine},
				Authorizer: session.RuntimeAuthorizerFunc(func(context.Context, session.RuntimeAuthorization) error {
					if mode == "denied" {
						return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeSessionMismatch, "denied", false)
					}
					return nil
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = registry.Close() })
			runtime, err := registry.ResolveRuntime(context.Background(), agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			var observations []agentcore.RuntimeDependencyObservation
			var observationMu sync.Mutex
			ctx = agentcore.WithRuntimeDependencyObserver(ctx, func(o agentcore.RuntimeDependencyObservation) {
				observationMu.Lock()
				defer observationMu.Unlock()
				observations = append(observations, o)
			})
			response, err := runtime.RunSession(ctx, requestForTest(t), func(_ context.Context, envelope agentcore.SessionEnvelope) error { return envelope.Validate() })
			observationMu.Lock()
			modelsSeen, toolsSeen := 0, 0
			for _, o := range observations {
				if o.TenantID != "tenant-public" || o.RunID != "kernel-run" || o.AgentID != "A000014" {
					t.Fatalf("wrong dependency scope: %+v", o)
				}
				if o.Kind == "model_dispatch" {
					modelsSeen++
					if o.InputSHA256 == "" || o.ToolsSHA256 == "" {
						t.Fatal("model dependency digests missing")
					}
				}
				if o.Kind == "tool_dispatch" {
					toolsSeen++
					if o.ToolID != "knowledge.search" || o.BindingSHA256 == "" || o.InputSHA256 == "" || o.CallSHA256 == "" {
						t.Fatalf("tool dependency missing: %+v", o)
					}
				}
			}
			observationMu.Unlock()
			if int32(modelsSeen) != requests.Load() || int32(toolsSeen) != tools.Load() {
				t.Fatalf("dependency observations differ from actual callbacks: models %d/%d, tools %d/%d", modelsSeen, requests.Load(), toolsSeen, tools.Load())
			}
			hasAnswer := false
			answerCopies := 0
			for _, message := range response.Messages {
				if message.Content == "public answer" {
					answerCopies++
					hasAnswer = message.Status == agentcore.RuntimeMessageStatusFinal
				}
			}
			switch mode {
			case "public":
				if err != nil || response.Status != agentcore.RuntimeStatusCompleted || !hasAnswer || answerCopies != 1 || requests.Load() != 2 || tools.Load() != 1 {
					t.Fatalf("public run=%+v, %v, model=%d, tools=%d", response, err, requests.Load(), tools.Load())
				}
				record, found, journalErr := journal.Get(ctx, ToolExecutionKey{TenantID: "tenant-public", RunID: "kernel-run", CallID: "http-call"})
				if journalErr != nil || !found || record.State != ToolExecutionCompleted {
					t.Fatalf("canonical run did not persist tool outcome: %+v, %v", record, journalErr)
				}
			case "internal":
				if err != nil || response.Status != agentcore.RuntimeStatusFailed || requests.Load() != 1 || tools.Load() != 0 {
					t.Fatalf("internal knowledge was not blocked: %+v, %v", response, err)
				}
				if _, found, journalErr := journal.Get(ctx, ToolExecutionKey{TenantID: "tenant-public", RunID: "kernel-run", CallID: "http-call"}); journalErr != nil || found {
					t.Fatalf("denied knowledge operation acquired an execution claim: %v, %v", found, journalErr)
				}
			case "denied":
				if !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeSessionMismatch) || requests.Load() != 0 || tools.Load() != 0 {
					t.Fatalf("denied task reached model: %v", err)
				}
			}
			connection, err := registry.Resolve(agentcore.RuntimeKindPi)
			if _, ok := connection.(*pi.NativeConnection); err != nil || !ok {
				t.Fatalf("PI changed: %T, %v", connection, err)
			}
		})
	}
}
