package device

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/providers/toolbridge"
	"orrery_v2/internal/platform/testkit"
)

func TestWorkspaceToolRequestRejectsUnsafeVariants(t *testing.T) {
	for _, name := range []string{"../a.xlsx", "C:/a.xlsx", "/a.xlsx", "a\\b.xlsx", "a//b.xlsx", "a/./b.xlsx", "a. "} {
		if ValidWorkspaceRelativeName(name) {
			t.Fatalf("accepted %q", name)
		}
	}
	good := WorkspaceToolCall{Version: 1, CallID: "call_test_001", Operation: "list_files"}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	order := testWorkOrder(time.Now().UTC())
	order.Task = WorkOrderTask{Kind: "workspace_tool", ToolCall: &good}
	order.RequiredCapabilities = good.Capabilities()
	order.RequestedArtifactClasses = []ArtifactClass{}
	if _, err := MarshalEnvelope(Envelope{ProtocolVersion: "1.0", MessageID: "msg_tool_test", ConnectionEpoch: "connection_fixture", Type: MessageWorkOrderOffer, SentAt: time.Now().UTC(), Payload: order}, CloudToDevice, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"shell", "readFile", "agent_turn"} {
		c := good
		c.Operation = op
		if c.Validate() == nil {
			t.Fatal("accepted", op)
		}
	}
}

func TestWorkspaceToolsRejectLegacyClientBeforePersisting(t *testing.T) {
	ctx := context.Background()
	repository, err := NewSQLRepository(ctx, testkit.NewTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	broker, err := NewDurableBroker(BrokerOptions{Repository: repository})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	now := time.Now().UTC()
	link, err := broker.AttachDevice("tenant_test", "device_test", "connection_test")
	if err != nil {
		t.Fatal(err)
	}
	sendDeviceEnvelope(t, link, now, "message_hello", MessageDeviceHello, DeviceHelloPayload{DeviceID: "device_test", ProtocolMin: ProtocolVersion, ProtocolMax: ProtocolVersion, ClientVersion: "legacy", AionCoreVersion: "test", CapabilityDigest: strings.Repeat("a", 64), ActiveWorkOrders: []ActiveWorkOrder{}})
	_ = receiveOutbound(t, link)
	order := testWorkOrder(now)
	order.Task = WorkOrderTask{Kind: "workspace_tool", ToolCall: &WorkspaceToolCall{Version: 1, CallID: "call_test", Operation: "list_files"}}
	order.RequiredCapabilities = []Capability{CapabilityWorkspaceRead}
	order.RequestedArtifactClasses = []ArtifactClass{}
	if _, sub, err := broker.Offer(ctx, OfferRequest{TenantID: "tenant_test", DeviceID: "device_test", SessionID: "session_test", Order: order}); err == nil {
		sub.Close()
		t.Fatal("accepted tools on legacy client")
	}
	if _, found, err := repository.GetWorkOrder(ctx, "tenant_test", "device_test", order.WorkOrderID); err != nil || found {
		t.Fatal("persisted an unsupported tool", found, err)
	}
}

type workspaceReplayBroker struct {
	record             WorkOrderRecord
	events             []BrokerEvent
	offered, cancelled int
	terminal           bool
}
type workspaceReplaySubscription struct{ events chan BrokerEvent }

func (s workspaceReplaySubscription) Events() <-chan BrokerEvent { return s.events }
func (s workspaceReplaySubscription) Close()                     {}
func (b *workspaceReplayBroker) Subscribe(context.Context, string, string, string, int64) (WorkOrderRecord, WorkOrderSubscription, error) {
	if b.record.Order.WorkOrderID == "" {
		return WorkOrderRecord{}, nil, &ProtocolError{Code: ProtocolDeviceMismatch}
	}
	events := make(chan BrokerEvent, len(b.events))
	for _, e := range b.events {
		events <- e
	}
	return b.record, workspaceReplaySubscription{events}, nil
}
func (b *workspaceReplayBroker) Offer(ctx context.Context, r OfferRequest) (WorkOrderRecord, WorkOrderSubscription, error) {
	b.offered++
	b.record = WorkOrderRecord{Order: r.Order, State: WorkOrderRunning}
	return b.Subscribe(ctx, "", "", "", 0)
}
func (b *workspaceReplayBroker) Cancel(context.Context, string, string, string, string) error {
	b.cancelled++
	return nil
}

func TestWorkspaceWriteTimeoutUncertainAndExpiredReceiptReplay(t *testing.T) {
	broker := &workspaceReplayBroker{}
	dispatcher := WorkspaceToolDispatcher{Broker: broker, Resolve: func(_ context.Context, _ runtimecore.RuntimeToolCallRequest, d, g string) (WorkspaceToolTarget, error) {
		return WorkspaceToolTarget{DeviceID: d, GrantID: g, ProjectID: "project_test", ActorID: "agent_test"}, nil
	}}
	call := runtimecore.RuntimeToolCallRequest{TenantID: "tenant_test", RunID: "run_test", AgentID: "agent_test", Name: "desktop_write_excel", CallID: "call_write_test", Input: map[string]any{"device_id": "device_test", "workspace_grant_id": "grant_test", "relative_name": "result.xlsx", "source_relative_name": "source.xlsx", "expected_sha256": strings.Repeat("a", 64), "sheet_name": "结果", "rows": [][]any{{"value"}}}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	result, err := dispatcher.CallRuntimeTool(ctx, call)
	if err != nil || !result.OutcomeUncertain || result.Output["code"] != "desktop_timeout_or_cancelled" || broker.cancelled != 1 {
		t.Fatalf("uncertain write: %+v %v", result, err)
	}
	broker.record.State = WorkOrderSucceeded
	payload, _ := json.Marshal(map[string]any{"version": 1, "status": "completed", "call_id": broker.record.Order.Task.ToolCall.CallID, "sha256": strings.Repeat("b", 64)})
	broker.events = []BrokerEvent{{Frame: &WorkOrderFramePayload{Frame: RuntimeFrame{Kind: "tool_result", CallID: broker.record.Order.Task.ToolCall.CallID, Content: string(payload)}}}, {Terminal: &WorkOrderTerminalPayload{Status: TerminalSucceeded}}}
	originalDeadline := broker.record.Order.Deadline
	replay, err := dispatcher.CallRuntimeTool(context.Background(), call)
	if err != nil || replay.OutcomeUncertain || replay.Output["status"] != "completed" || broker.offered != 1 || broker.record.Order.Deadline != originalDeadline {
		t.Fatalf("expired receipt was re-executed or lost: %+v %v", replay, err)
	}
}

// This test crosses the durable Go broker -> actual TS protocol/coordinator ->
// physical .xlsx executor -> original runtime tool result. Enable with the
// bundled contract peer, so normal Go unit tests need no Node installation.
func TestWorkspaceToolActualDesktopRoundTrip(t *testing.T) {
	peer := os.Getenv("ORRERY_WORKSPACE_TOOL_CONTRACT")
	if peer == "" {
		t.Skip("set ORRERY_WORKSPACE_TOOL_CONTRACT to built desktop contract peer")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	root := t.TempDir()
	if export := os.Getenv("ORRERY_WORKSPACE_TOOL_ACCEPTANCE_DIR"); export != "" {
		root = filepath.Join(export, fmt.Sprintf("run-%d", time.Now().UnixNano()))
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
	}
	node := os.Getenv("ORRERY_WORKSPACE_TOOL_NODE")
	if node == "" {
		node = "node"
	}
	command := exec.CommandContext(ctx, node, peer, root)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { stdin.Close(); _ = command.Wait() }()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	if !scanner.Scan() {
		t.Fatal("desktop did not start")
	}
	var ready struct {
		Type    string `json:"type"`
		GrantID string `json:"grant_id"`
	}
	if json.Unmarshal(scanner.Bytes(), &ready) != nil || ready.Type != "ready" {
		t.Fatal("invalid ready message", scanner.Text())
	}
	repository, err := NewSQLRepository(ctx, testkit.NewTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	broker, err := NewDurableBroker(BrokerOptions{Repository: repository})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	now := time.Now().UTC()
	link, err := broker.AttachDevice("tenant_fixture", "device_fixture", "connection_fixture")
	if err != nil {
		t.Fatal(err)
	}
	sendDeviceEnvelope(t, link, now, "message_hello_fixture", MessageDeviceHello, DeviceHelloPayload{DeviceID: "device_fixture", ProtocolMin: ProtocolVersion, ProtocolMax: ProtocolVersion, ClientVersion: "test", AionCoreVersion: "test", CapabilityDigest: strings.Repeat("a", 64), ActiveWorkOrders: []ActiveWorkOrder{}, WorkspaceToolsVersion: 1})
	_ = receiveOutbound(t, link)
	errors := make(chan error, 1)
	go func() {
		for scanner.Scan() {
			t.Log("desktop response", scanner.Text())
			var wire struct {
				Type    MessageType     `json:"type"`
				Payload json.RawMessage `json:"payload"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &wire); err != nil {
				errors <- err
				return
			}
			raw, _ := json.Marshal(map[string]any{"protocol_version": "1.0", "message_id": newProtocolID("message"), "connection_epoch": "connection_fixture", "type": wire.Type, "sent_at": time.Now().UTC(), "payload": wire.Payload})
			if err := link.Receive(ctx, raw); err != nil {
				errors <- err
				return
			}
		}
	}()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case envelope, open := <-link.Outbound():
				if !open {
					return
				}
				t.Log("cloud offer", envelope.Type)
				raw, _ := json.Marshal(envelope)
				if _, err := stdin.Write(append(raw, '\n')); err != nil {
					errors <- err
					return
				}
			}
		}
	}()
	dispatcher := WorkspaceToolDispatcher{Broker: broker, Resolve: func(_ context.Context, call runtimecore.RuntimeToolCallRequest, device, grant string) (WorkspaceToolTarget, error) {
		return WorkspaceToolTarget{DeviceID: device, GrantID: grant, ProjectID: "project_fixture", ActorID: "agent_fixture"}, nil
	}}
	request := runtimecore.RuntimeToolCallRequest{TenantID: "tenant_fixture", RunID: agentcore.AgentRunID("run_fixture"), AgentID: agentcore.AgentID("agent_fixture"), Name: "desktop_read_excel", CallID: "call_read_001", Input: map[string]any{"device_id": "device_fixture", "workspace_grant_id": ready.GrantID, "relative_name": "订单.xlsx", "sheet_name": "订单"}}
	read, err := dispatcher.CallRuntimeTool(ctx, request)
	if err != nil || read.Output["status"] != "completed" {
		select {
		case err := <-errors:
			t.Fatal(err)
		default:
		}
		t.Fatalf("read: %#v %v", read, err)
	}
	if len(read.Output["rows"].([]any)) != 4 {
		t.Fatal("missing workbook rows", read.Output)
	}
	// Exercise the actual authenticated MCP transport shared by native Codex and
	// Claude against this same physical desktop executor, without a model fixture
	// pretending to supply file contents.
	bridge, err := toolbridge.StartDesktopMCP(ctx, dispatcher, agentcore.RuntimeRequest{TenantID: request.TenantID, RunID: request.RunID, AgentID: request.AgentID, ToolBindings: []agentcore.RuntimeToolBinding{{ToolID: "desktop_read_excel", InputSchema: map[string]any{"type": "object"}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	input, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "native-read", "method": "tools/call", "params": map[string]any{"name": "desktop_read_excel", "arguments": request.Input}})
	httpRequest, _ := http.NewRequestWithContext(ctx, http.MethodPost, bridge.URL, bytes.NewReader(input))
	httpRequest.Header.Set("Authorization", bridge.Authorization)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json, text/event-stream")
	httpResponse, err := http.DefaultClient.Do(httpRequest)
	if err != nil {
		t.Fatal(err)
	}
	var nativeResult struct {
		Result struct {
			StructuredContent map[string]any `json:"structuredContent"`
		} `json:"result"`
	}
	err = json.NewDecoder(httpResponse.Body).Decode(&nativeResult)
	httpResponse.Body.Close()
	if err != nil || httpResponse.StatusCode != 200 || nativeResult.Result.StructuredContent["status"] != "completed" || len(nativeResult.Result.StructuredContent["rows"].([]any)) != 4 {
		t.Fatalf("native MCP did not reach desktop: %+v %v", nativeResult, err)
	}
	totals := map[string]float64{}
	for _, rawRow := range read.Output["rows"].([]any)[1:] {
		row := rawRow.([]any)
		totals[row[0].(string)] += row[1].(float64)
	}
	original, err := os.ReadFile(filepath.Join(root, "订单.xlsx"))
	if err != nil {
		t.Fatal(err)
	}
	request.Name, request.CallID = "desktop_write_excel", "call_write_001"
	request.Input = map[string]any{"device_id": "device_fixture", "workspace_grant_id": ready.GrantID, "relative_name": "汇总.xlsx", "source_relative_name": "订单.xlsx", "expected_sha256": read.Output["sha256"], "sheet_name": "汇总", "rows": [][]any{{"客户", "总金额"}, {"甲", totals["甲"]}, {"乙", totals["乙"]}}}
	written, err := dispatcher.CallRuntimeTool(ctx, request)
	if err != nil || written.Output["status"] != "completed" {
		t.Fatalf("write: %#v %v", written, err)
	}
	repeat, err := dispatcher.CallRuntimeTool(ctx, request)
	if err != nil || repeat.Output["sha256"] != written.Output["sha256"] {
		t.Fatalf("replay: %#v %v", repeat, err)
	}
	request.Input["rows"] = [][]any{{"changed"}}
	conflict, err := dispatcher.CallRuntimeTool(ctx, request)
	if err != nil || conflict.Output["code"] != "duplicate_conflict" {
		t.Fatalf("conflict: %#v %v", conflict, err)
	}
	request.Name, request.CallID = "desktop_read_excel", "call_followup_001"
	request.Input = map[string]any{"device_id": "device_fixture", "workspace_grant_id": ready.GrantID, "relative_name": "汇总.xlsx", "sheet_name": "汇总"}
	followup, err := dispatcher.CallRuntimeTool(ctx, request)
	if err != nil || followup.Output["status"] != "completed" {
		t.Fatalf("followup: %#v %v", followup, err)
	}
	rows := followup.Output["rows"].([]any)
	if len(rows) != 3 || rows[1].([]any)[1] != float64(150) || rows[2].([]any)[1] != float64(200) {
		t.Fatal("wrong transformation", rows)
	}
	unchanged, _ := os.ReadFile(filepath.Join(root, "订单.xlsx"))
	if string(original) != string(unchanged) {
		t.Fatal("source workbook changed")
	}
	encoded, _ := json.Marshal(map[string]any{"read": read.Output, "write": written.Output, "followup": followup.Output, "replay_sha_match": true, "duplicate_conflict": true, "native_mcp_read_verified": true, "second_llm_calls": 0})
	if strings.Contains(string(encoded), root) {
		t.Fatal("absolute local path leaked")
	}
	if err := os.WriteFile(filepath.Join(root, "RESULT.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("real desktop Excel round trip passed: %s", root)
}
