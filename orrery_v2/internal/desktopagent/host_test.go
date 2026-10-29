package desktopagent

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

type testClient struct {
	input  *io.PipeWriter
	frames chan map[string]json.RawMessage
	done   chan error
	serial int
}

func newClient(t *testing.T, state string) *testClient {
	t.Helper()
	in, input := io.Pipe()
	output, out := io.Pipe()
	c := &testClient{input: input, frames: make(chan map[string]json.RawMessage, 128), done: make(chan error, 1)}
	go func() { err := Serve(in, out, state); _ = out.Close(); c.done <- err }()
	go func() {
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 4096), 2*maxFrame)
		for scanner.Scan() {
			var frame map[string]json.RawMessage
			if json.Unmarshal(scanner.Bytes(), &frame) == nil {
				c.frames <- frame
			}
		}
		close(c.frames)
	}()
	t.Cleanup(func() {
		_ = input.Close()
		select {
		case <-c.done:
		case <-time.After(5 * time.Second):
			t.Error("host did not stop")
		}
		_ = output.Close()
	})
	return c
}

func (c *testClient) send(t *testing.T, id, method string, params any) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.input.Write(append(raw, '\n')); err != nil {
		t.Fatal(err)
	}
}
func (c *testClient) next(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	select {
	case frame := <-c.frames:
		if frame == nil {
			t.Fatal("host closed unexpectedly")
		}
		return frame
	case <-time.After(15 * time.Second):
		t.Fatal("host event timeout")
		return nil
	}
}

func modelFixture(t *testing.T, toolPath string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	calls := new(atomic.Int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer local-test-secret" {
			t.Error("model route or credential mismatch")
		}
		var body struct {
			Messages []map[string]any `json:"messages"`
			Stream   bool             `json:"stream"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid model request")
		}
		calls.Add(1)
		hasTool := false
		for _, message := range body.Messages {
			if message["role"] == "tool" {
				hasTool = true
			}
		}
		delta := map[string]any{"role": "assistant", "content": "Agent wrote the requested file."}
		finish := "stop"
		if !hasTool {
			arguments, _ := json.Marshal(map[string]any{"path": toolPath, "content": "saved by Orrery"})
			delta = map[string]any{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{"index": 0, "id": "write-call", "type": "function", "function": map[string]any{"name": "workspace_write", "arguments": string(arguments)}}}}
			finish = "tool_calls"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
		_, _ = w.Write(append(append([]byte("data: "), chunk...), []byte("\n\ndata: [DONE]\n\n")...))
	}))
	t.Cleanup(server.Close)
	return server, calls
}

func TestHostActualKernelModelToolApprovalAndDurableReplayFence(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(t.TempDir(), "runtime.db")
	server, calls := modelFixture(t, "result.txt")
	c := newClient(t, state)
	c.send(t, "rpc_health", "health", map[string]any{})
	if frame := c.next(t); !strings.Contains(string(frame["result"]), "orrery-agentkernel") {
		t.Fatalf("health=%s", frame["result"])
	}
	start := Start{RunID: "run_write_001", Root: root, Profile: Profile{Provider: "openai", BaseURL: server.URL + "/v1", Model: "test-model", APIKey: "local-test-secret"}, Messages: []Message{{Role: "user", Text: "Write result.txt"}}, Capabilities: []string{"workspace.read", "workspace.write"}}
	c.send(t, "rpc_start", "start", start)
	approved, completed := false, false
	for !completed {
		frame := c.next(t)
		if len(frame["error"]) > 0 {
			t.Fatalf("unexpected RPC error: %s", frame["error"])
		}
		var event struct {
			Kind      string `json:"kind"`
			RequestID string `json:"requestId"`
			Text      string `json:"text"`
			Status    string `json:"status"`
		}
		_ = json.Unmarshal(frame["event"], &event)
		if strings.Contains(string(frame["event"]), "local-test-secret") {
			t.Fatal("secret in event")
		}
		if event.Kind == "permission" {
			if _, err := os.Stat(filepath.Join(root, "result.txt")); !os.IsNotExist(err) {
				t.Fatal("write happened before approval")
			}
			c.send(t, "rpc_wrong", "permission", map[string]any{"runId": start.RunID, "requestId": "wrong-request", "allow": true})
			for {
				reply := c.next(t)
				if string(reply["id"]) == `"rpc_wrong"` {
					if len(reply["error"]) == 0 {
						t.Fatal("wrong approval accepted")
					}
					break
				}
			}
			c.send(t, "rpc_approve", "permission", map[string]any{"runId": start.RunID, "requestId": event.RequestID, "allow": true})
			approved = true
		}
		if event.Kind == "completed" {
			if event.Status != "succeeded" || event.Text != "Agent wrote the requested file." {
				t.Fatalf("terminal=%s", frame["event"])
			}
			completed = true
		}
	}
	if !approved || calls.Load() != 2 {
		t.Fatalf("approved=%v model calls=%d", approved, calls.Load())
	}
	if text, err := os.ReadFile(filepath.Join(root, "result.txt")); err != nil || string(text) != "saved by Orrery" {
		t.Fatalf("file=%q %v", text, err)
	}
	c.send(t, "rpc_replay", "start", start)
	for {
		frame := c.next(t)
		if string(frame["id"]) == `"rpc_replay"` {
			if len(frame["error"]) == 0 {
				t.Fatal("same run restarted")
			}
			break
		}
	}
	if calls.Load() != 2 {
		t.Fatal("replay reached model")
	}
}

func TestHostDenialCancellationAndBoundary(t *testing.T) {
	for _, mode := range []string{"deny", "cancel", "outside"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			target := "result.txt"
			if mode == "outside" {
				target = "../outside.txt"
			}
			server, _ := modelFixture(t, target)
			c := newClient(t, filepath.Join(t.TempDir(), "runtime.db"))
			start := Start{RunID: "run_denied_001", Root: root, Profile: Profile{Provider: "openai", BaseURL: server.URL + "/v1", Model: "test", APIKey: "local-test-secret"}, Messages: []Message{{Role: "user", Text: "perform action"}}, Capabilities: []string{"workspace.write"}}
			c.send(t, "rpc_start", "start", start)
			for {
				frame := c.next(t)
				var event struct {
					Kind      string `json:"kind"`
					RequestID string `json:"requestId"`
					Status    string `json:"status"`
				}
				_ = json.Unmarshal(frame["event"], &event)
				if event.Kind == "permission" {
					if mode == "outside" {
						t.Fatal("outside path reached approval")
					}
					if mode == "deny" {
						c.send(t, "rpc_deny", "permission", map[string]any{"runId": start.RunID, "requestId": event.RequestID, "allow": false})
					} else {
						c.send(t, "rpc_cancel", "cancel", map[string]any{"runId": start.RunID})
					}
				}
				if event.Kind == "completed" {
					if event.Status == "succeeded" {
						t.Fatal("denied/cancelled run succeeded")
					}
					break
				}
			}
			if _, err := os.Stat(filepath.Join(root, "result.txt")); !os.IsNotExist(err) {
				t.Fatal("denied write ran")
			}
		})
	}
}

func TestHostPreservesConversationHistoryOnFollowup(t *testing.T) {
	calls := new(atomic.Int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		calls.Add(1)
		if len(input.Messages) != 4 || input.Messages[1].Content != "first question" || input.Messages[2].Role != "assistant" || input.Messages[2].Content != "first answer" || input.Messages[3].Content != "followup question" {
			t.Errorf("history not preserved: %+v", input.Messages)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"followup answer\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	c := newClient(t, filepath.Join(t.TempDir(), "runtime.db"))
	c.send(t, "rpc_followup", "start", Start{RunID: "run_followup_001", Root: t.TempDir(), Profile: Profile{Provider: "openai", BaseURL: server.URL + "/v1", Model: "test", APIKey: "local-test-secret"}, Messages: []Message{{Role: "user", Text: "first question"}, {Role: "assistant", Text: "first answer"}, {Role: "user", Text: "followup question"}}, Capabilities: []string{"workspace.read"}})
	for {
		frame := c.next(t)
		if len(frame["error"]) > 0 {
			t.Fatalf("RPC error: %s", frame["error"])
		}
		var event struct {
			Kind   string `json:"kind"`
			Status string `json:"status"`
			Text   string `json:"text"`
		}
		_ = json.Unmarshal(frame["event"], &event)
		if event.Kind == "completed" {
			if event.Status != "succeeded" || event.Text != "followup answer" || calls.Load() != 1 {
				t.Fatalf("followup failed: %s calls=%d", frame["event"], calls.Load())
			}
			break
		}
	}
}

func TestHostPublishesSafeModelFailureCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer local-test-secret" {
			t.Error("model route or credential mismatch")
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "provider endpoint=https://private.invalid key=secret")
	}))
	defer server.Close()
	c := newClient(t, filepath.Join(t.TempDir(), "runtime.db"))
	c.send(t, "rpc_model_failure", "start", Start{
		RunID: "run_model_failure_001", Root: t.TempDir(),
		Profile:  Profile{Provider: "openai", BaseURL: server.URL + "/v1", Model: "test", APIKey: "local-test-secret"},
		Messages: []Message{{Role: "user", Text: "make a request"}}, Capabilities: []string{"workspace.read"},
	})
	for {
		frame := c.next(t)
		if len(frame["error"]) > 0 {
			t.Fatalf("RPC error: %s", frame["error"])
		}
		if len(frame["event"]) == 0 {
			continue
		}
		var event struct {
			Kind   string `json:"kind"`
			Status string `json:"status"`
			Code   string `json:"code"`
			Text   string `json:"text"`
		}
		if err := json.Unmarshal(frame["event"], &event); err != nil {
			t.Fatal(err)
		}
		if event.Kind != "completed" {
			continue
		}
		if event.Status != "failed" || event.Code != "model_request_failed" {
			t.Fatalf("terminal=%s", frame["event"])
		}
		if strings.Contains(event.Text, "private.invalid") || strings.Contains(event.Text, "secret") {
			t.Fatalf("provider details leaked in terminal text: %q", event.Text)
		}
		break
	}
}

func TestToolPathsCapabilitiesUnknownFieldsAndSecretBoundary(t *testing.T) {
	root := t.TempDir()
	p, err := newToolPolicy(root, []string{"workspace.read", "workspace.write"})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../out", "C:/windows/system.ini", "folder/file:stream", ".git/config", ".ssh/id_rsa", ".env", "secret.pem", "folder\\file"} {
		if _, err := p.path(path, true); err == nil {
			t.Errorf("accepted path %q", path)
		}
	}
	call := runtimecore.RuntimeToolCallRequest{TenantID: "local-device", AgentID: agentcore.AgentID("A000001"), ExecutionPrincipal: agentcore.ExecutionPrincipal{ID: "local-owner"}, Name: "process.shell", Input: map[string]any{"command": "echo forbidden"}}
	if p.authorize(context.Background(), call) == nil {
		t.Fatal("ungranted shell accepted")
	}
	if decode([]byte(`{"id":"rpc_test","method":"health","params":{},"secret":"not-allowed"}`), &request{}) == nil {
		t.Fatal("unknown field accepted")
	}
	if decode([]byte(`{} {}`), &request{}) == nil {
		t.Fatal("trailing JSON accepted")
	}
	if _, err := newToolPolicy(root, []string{"unknown.capability"}); err == nil {
		t.Fatal("unknown capability accepted")
	}
}
