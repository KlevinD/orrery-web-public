package openclaw

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimesession "orrery_v2/internal/agent/runtime/session"
)

func TestConnectionProjectsRemoteHTTPResultIntoCanonicalEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/responses" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"backend-session-1","status":"completed","model":"openclaw/planner","output":[{"type":"message","content":[{"type":"output_text","text":"canonical answer"}]}],"usage":{"input_tokens":7,"output_tokens":3}}`))
	}))
	defer server.Close()

	connection, err := NewConnection(ConnectionConfig{
		ResolveWorkspace: WorkspaceResolverFunc(func(context.Context, string) (NativeWorkspace, error) {
			return NativeWorkspace{Request: agentcore.RuntimeRequest{
				TenantID: "tenant-1",
				RunID:    "run-1",
				AgentID:  "agent-1",
				RuntimeOptions: agentcore.RuntimeOptions{
					WorkingDirectory: t.TempDir(),
					ProviderConfig:   []byte(`{"mode":"remote_http","gateway_url":"` + server.URL + `","agent_id":"planner"}`),
				},
			}}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	backend, err := connection.OpenSession(context.Background(), agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "logical-session-1"}, agentcore.SessionConfig{WorkspaceRef: "workspace-1"})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := backend.Dispatch(context.Background(), agentcore.RuntimeCommand{
		CommandID: "send-1",
		Kind:      agentcore.RuntimeCommandSend,
		Send: &agentcore.SendCommand{
			Content:  []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: "hello"}},
			Metadata: agentcore.CommandMetadata{ClientMsgID: "message-1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Accepted || receipt.Admission != agentcore.AdmissionStarted || receipt.TurnGeneration != 1 {
		t.Fatalf("receipt = %#v", receipt)
	}

	events := collectOpenClawEvents(t, backend.Events())
	seen := make(map[agentcore.SessionEventKind]bool)
	var terminal agentcore.TurnResultEvent
	for index, envelope := range events {
		if err := envelope.Validate(); err != nil {
			t.Fatalf("event %d validation: %v", index, err)
		}
		if envelope.Sequence != uint64(index+1) {
			t.Fatalf("event %d sequence = %d", index, envelope.Sequence)
		}
		seen[envelope.Event.Kind] = true
		if envelope.Event.TurnResult != nil {
			terminal = *envelope.Event.TurnResult
		}
	}
	for _, kind := range []agentcore.SessionEventKind{
		agentcore.EventPromptAccepted,
		agentcore.EventMessageDelta,
		agentcore.EventUsageDelta,
		agentcore.EventConfigChanged,
		agentcore.EventBackendBound,
		agentcore.EventTurnResult,
	} {
		if !seen[kind] {
			t.Fatalf("missing canonical event %q: %#v", kind, events)
		}
	}
	if terminal.ResultText != "canonical answer" || terminal.IsError || terminal.Outcome.Kind != agentcore.TurnOutcomeEndTurn {
		t.Fatalf("terminal = %#v", terminal)
	}
}

func TestConnectionProjectsTransportIntoRuntimeResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"backend-session-2","status":"completed","model":"openclaw/planner","output":[{"type":"message","content":[{"type":"output_text","text":"runtime answer"}]}]}`))
	}))
	defer server.Close()

	workspaces := runtimesession.NewWorkspaceBroker()
	connection, err := NewConnection(ConnectionConfig{ResolveWorkspace: WorkspaceResolverFunc(func(_ context.Context, ref string) (NativeWorkspace, error) {
		binding, err := workspaces.Resolve(ref)
		if err != nil {
			return NativeWorkspace{}, err
		}
		return NativeWorkspace{Request: binding.Request, Version: binding.Launch.Version}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := runtimesession.NewRuntime(runtimesession.RuntimeOptions{Kind: agentcore.RuntimeKindOpenClaw, Connection: connection, Workspaces: workspaces})
	if err != nil {
		t.Fatal(err)
	}
	response, err := runtime.RunSession(context.Background(), agentcore.RuntimeRequest{
		TenantID:      "tenant-2",
		RunID:         "run-2",
		AgentID:       "agent-2",
		InputMessages: []agentcore.RuntimeChatMessage{{Role: "user", Content: "hello"}},
		RuntimeOptions: agentcore.RuntimeOptions{
			WorkingDirectory: t.TempDir(),
			ProviderConfig:   []byte(`{"mode":"remote_http","gateway_url":"` + server.URL + `","agent_id":"planner"}`),
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != agentcore.RuntimeStatusCompleted || response.ProviderMetadata["transport"] != "remote_http" || response.ProviderMetadata["session_id"] != "backend-session-2" {
		t.Fatalf("response = %#v", response)
	}
}

func collectOpenClawEvents(t *testing.T, events <-chan agentcore.SessionEnvelope) []agentcore.SessionEnvelope {
	t.Helper()
	collected := make([]agentcore.SessionEnvelope, 0, 8)
	deadline := time.After(3 * time.Second)
	for {
		select {
		case envelope, ok := <-events:
			if !ok {
				t.Fatal("event stream closed before terminal result")
			}
			collected = append(collected, envelope)
			if envelope.Event.Kind == agentcore.EventTurnResult {
				return collected
			}
		case <-deadline:
			t.Fatal("timed out waiting for terminal OpenClaw event")
		}
	}
}
