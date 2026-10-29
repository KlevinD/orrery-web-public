package device

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimesession "orrery_v2/internal/agent/runtime/session"
	"orrery_v2/internal/platform/testkit"
)

func TestDeviceRuntimeUsesCanonicalOrchestratorAndKeepsPrivateExecutionMaterialLocal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	now := time.Now().UTC().Truncate(time.Millisecond)
	repository, err := NewSQLRepository(ctx, testkit.NewTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	broker, err := NewDurableBroker(BrokerOptions{Repository: repository, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := NewConnection(ConnectionConfig{Broker: broker, Now: func() time.Time { return now }, CloseBroker: true})
	if err != nil {
		t.Fatal(err)
	}
	link := attachWelcomedDevice(t, broker, "tenant-a", "device_test_001", "conn_test_001", now)

	var permissionHandlerCalls atomic.Int64
	registryWithPermission := runtimesession.NewConnectionRegistryWithOptions(runtimesession.ConnectionRegistryOptions{
		PermissionHandler: agentcore.SessionPermissionHandlerFunc(func(context.Context, agentcore.SessionPermissionRequest, agentcore.SessionPermissionResponder) error {
			permissionHandlerCalls.Add(1)
			return nil
		}),
	})
	// Register the same production connection in the registry that owns the
	// application-facing Runtime for this test.
	if err := registryWithPermission.Register(agentcore.RuntimeKindDevice, connection); err != nil {
		t.Fatal(err)
	}
	runtime, err := registryWithPermission.ResolveRuntime(ctx, agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindDevice})
	if err != nil {
		t.Fatal(err)
	}
	defer registryWithPermission.Close()

	config, _ := json.Marshal(RuntimeConfig{DeviceID: "device_test_001", WorkspaceGrantID: "grant_test_001", RequiredCapabilities: []Capability{CapabilityWorkspaceRead}, RequestedArtifactClasses: []ArtifactClass{ArtifactSummary}})
	privateWorkspace := filepath.Join(t.TempDir(), "private-workspace")
	request := agentcore.RuntimeRequest{
		TenantID: "tenant-a", RunID: "run-device-001", AgentID: "agent-device-001", MissionID: "mission-device-001", TaskID: "task-device-001",
		ContextPack:   agentcore.ContextPack{AgentID: "agent-device-001"},
		InputMessages: []agentcore.RuntimeChatMessage{{Role: "user", Content: "Implement the approved change"}},
		RuntimeOptions: agentcore.RuntimeOptions{
			WorkingDirectory: privateWorkspace, SystemPrompt: "private system prompt D:\\private\\workspace", Model: "private-cloud-model",
			ExtraArgs: []string{"--private-argv"}, Environment: map[string]string{"PRIVATE_TOKEN": "credential-value"},
			ProviderSpecific: map[string]string{"credential_ref": "secret://tenant-a/device"}, ProviderConfig: config,
			Timeout: 5 * time.Minute,
		},
		Deadline: now.Add(30 * time.Minute),
	}
	type runtimeResult struct {
		response agentcore.RuntimeResponse
		err      error
	}
	result := make(chan runtimeResult, 1)
	var observedMu sync.Mutex
	var observed []agentcore.SessionEnvelope
	go func() {
		response, runErr := runtime.RunSession(ctx, request, func(_ context.Context, envelope agentcore.SessionEnvelope) error {
			observedMu.Lock()
			observed = append(observed, envelope)
			observedMu.Unlock()
			return nil
		})
		result <- runtimeResult{response: response, err: runErr}
	}()

	offerEnvelope := receiveOutbound(t, link)
	if offerEnvelope.Type != MessageWorkOrderOffer {
		t.Fatalf("offer envelope = %#v", offerEnvelope)
	}
	offer := offerEnvelope.Payload.(WorkOrder)
	encodedOffer, err := MarshalEnvelope(offerEnvelope, CloudToDevice, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{privateWorkspace, `D:\private`, "PRIVATE_TOKEN", "credential-value", "credential_ref", "private-cloud-model", "private system prompt", "--private-argv", "provider_config", "mcp"} {
		if strings.Contains(strings.ToLower(string(encodedOffer)), strings.ToLower(forbidden)) {
			t.Fatalf("device offer retained private material %q: %s", forbidden, encodedOffer)
		}
	}
	if offer.Task.Instruction != "Implement the approved change" || offer.WorkspaceGrantID != "grant_test_001" {
		t.Fatalf("offer = %#v", offer)
	}

	sendDeviceEnvelope(t, link, now, "msg_accept_001", MessageWorkOrderAccepted, WorkOrderAcceptedPayload{WorkOrderID: offer.WorkOrderID, AcceptedAt: now})
	sendDeviceEnvelope(t, link, now, "msg_permission_001", MessageWorkOrderFrame, WorkOrderFramePayload{WorkOrderID: offer.WorkOrderID, Sequence: 1, Frame: RuntimeFrame{Kind: "permission", Phase: "waiting", CallID: "call_permission_001", Category: "shell"}})
	sendDeviceEnvelope(t, link, now, "msg_permission_002", MessageWorkOrderFrame, WorkOrderFramePayload{WorkOrderID: offer.WorkOrderID, Sequence: 2, Frame: RuntimeFrame{Kind: "permission", Phase: "resolved", CallID: "call_permission_001", Category: "shell", Decision: "allow_once"}})
	sendDeviceEnvelope(t, link, now, "msg_detached_001", MessageWorkOrderFrame, WorkOrderFramePayload{WorkOrderID: offer.WorkOrderID, Sequence: 3, Frame: RuntimeFrame{Kind: "detached", Reason: `runtime moved from D:\private\workspace`}})
	sendDeviceEnvelope(t, link, now, "msg_text_001", MessageWorkOrderFrame, WorkOrderFramePayload{WorkOrderID: offer.WorkOrderID, Sequence: 4, Frame: RuntimeFrame{Kind: "text", Channel: "output", Content: `Done in D:\private\workspace\file.txt and /srv/private/file with token=credential-value`}})
	inputTokens, outputTokens, cost := int64(10), int64(4), 0.01
	sendDeviceEnvelope(t, link, now, "msg_usage_001", MessageWorkOrderFrame, WorkOrderFramePayload{WorkOrderID: offer.WorkOrderID, Sequence: 5, Frame: RuntimeFrame{Kind: "usage", InputTokens: &inputTokens, OutputTokens: &outputTokens, CostUSD: &cost}})
	sendDeviceEnvelope(t, link, now, "msg_terminal_001", MessageWorkOrderTerminal, WorkOrderTerminalPayload{WorkOrderID: offer.WorkOrderID, Sequence: 6, Status: TerminalSucceeded, Summary: "Completed"})

	completed := <-result
	if completed.err != nil {
		t.Fatal(completed.err)
	}
	if completed.response.Status != agentcore.RuntimeStatusCompleted || len(completed.response.Usage) == 0 {
		t.Fatalf("response = %#v", completed.response)
	}
	if permissionHandlerCalls.Load() != 0 {
		t.Fatalf("local-only permission reached remote application handler %d times", permissionHandlerCalls.Load())
	}
	if connection.Capabilities(ctx).SupportedCommands.AnswerPermission || connection.Capabilities(ctx).SupportedCommands.AnswerAuth {
		t.Fatal("device runtime advertised remote permission/auth answers")
	}

	observedMu.Lock()
	events := append([]agentcore.SessionEnvelope(nil), observed...)
	observedMu.Unlock()
	var sawPermission, sawResolved, sawDetachedNotice, sawTerminal bool
	for _, envelope := range events {
		data, _ := json.Marshal(envelope)
		if strings.Contains(string(data), `D:\\private`) || strings.Contains(string(data), "/srv/private") || strings.Contains(string(data), "credential-value") {
			t.Fatalf("canonical event retained private material: %s", data)
		}
		switch envelope.Event.Kind {
		case agentcore.EventPermission:
			sawPermission = true
		case agentcore.EventPermissionResolved:
			sawResolved = true
		case agentcore.EventNotice:
			sawDetachedNotice = strings.Contains(envelope.Event.Notice.Message, "waiting for recovery")
		case agentcore.EventTurnResult:
			sawTerminal = true
		}
	}
	if !sawPermission || !sawResolved || !sawDetachedNotice || !sawTerminal {
		t.Fatalf("canonical device event coverage permission=%v resolved=%v detached=%v terminal=%v", sawPermission, sawResolved, sawDetachedNotice, sawTerminal)
	}
	reader := runtime.(agentcore.SessionRuntimeRecordReader)
	recorded, ok := reader.RuntimeRequest(request.RunID)
	if !ok || len(recorded.RuntimeOptions.ProviderConfig) != 0 || len(recorded.RuntimeOptions.Environment) != 0 || recorded.RuntimeOptions.WorkingDirectory != "" {
		t.Fatalf("runtime record retained private device material: %#v", recorded.RuntimeOptions)
	}
}

func TestDeviceSessionInvalidInstructionDoesNotConsumeSingleTurnAndCloseEndsEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	now := time.Now().UTC().Truncate(time.Millisecond)
	repository, err := NewSQLRepository(ctx, testkit.NewTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	broker, err := NewDurableBroker(BrokerOptions{Repository: repository, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := NewConnection(ConnectionConfig{Broker: broker, Now: func() time.Time { return now }, CloseBroker: true})
	if err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(RuntimeConfig{DeviceID: "device_test_001", WorkspaceGrantID: "grant_test_001"})
	request := agentcore.RuntimeRequest{TenantID: "tenant-a", RunID: "run-device-retry", AgentID: "agent-device-retry", RuntimeOptions: agentcore.RuntimeOptions{ProviderConfig: config}, Deadline: now.Add(time.Hour)}
	if err := connection.BindRuntimeRequest("session-device-retry", request); err != nil {
		t.Fatal(err)
	}
	opened, err := connection.OpenSession(ctx, agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "session-device-retry"}, agentcore.SessionConfig{WorkspaceRef: "workspace-device-retry"})
	if err != nil {
		t.Fatal(err)
	}
	backend := opened.(*SessionBackend)
	tooLong := agentcore.RuntimeCommand{CommandID: "send-too-long", Kind: agentcore.RuntimeCommandSend, Send: &agentcore.SendCommand{Content: []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: strings.Repeat("x", 65537)}}}}
	if _, err := backend.Dispatch(ctx, tooLong); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeInvalidPayload) {
		t.Fatalf("oversized instruction error = %v", err)
	}
	link := attachWelcomedDevice(t, broker, "tenant-a", "device_test_001", "conn_test_001", now)
	result := make(chan error, 1)
	valid := agentcore.RuntimeCommand{CommandID: "send-valid", Kind: agentcore.RuntimeCommandSend, Send: &agentcore.SendCommand{Content: []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: "Proceed"}}, Metadata: agentcore.CommandMetadata{ClientMsgID: "client-retry"}}}
	go func() {
		_, dispatchErr := backend.Dispatch(ctx, valid)
		result <- dispatchErr
	}()
	offer := receiveOutbound(t, link).Payload.(WorkOrder)
	sendDeviceEnvelope(t, link, now, "msg_accept_retry_001", MessageWorkOrderAccepted, WorkOrderAcceptedPayload{WorkOrderID: offer.WorkOrderID, AcceptedAt: now})
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := connection.CloseSession(ctx, "session-device-retry"); err != nil {
		t.Fatal(err)
	}
	select {
	case _, ok := <-backend.Events():
		if ok {
			// PromptAccepted may already be buffered; drain until close below.
			for range backend.Events() {
			}
		}
	case <-time.After(time.Second):
		t.Fatal("device session events did not close")
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDeviceRuntimeConfigCannotOverrideServerProjectOrActorIdentity(t *testing.T) {
	raw := json.RawMessage(`{"device_id":"device_test_001","workspace_grant_id":"grant_test_001","project_id":"project_attacker","origin_actor_id":"actor_attacker"}`)
	if _, err := parseRuntimeConfig(raw); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeInvalidPayload) {
		t.Fatalf("identity override error = %v", err)
	}
}

func TestDeviceConnectionRejectsRemotePermissionAuthAndUnsafeConfig(t *testing.T) {
	ctx := context.Background()
	repository, _ := NewSQLRepository(ctx, testkit.NewTestDB(t))
	broker, _ := NewDurableBroker(BrokerOptions{Repository: repository})
	connection, _ := NewConnection(ConnectionConfig{Broker: broker})
	defer connection.Close()
	request := agentcore.RuntimeRequest{TenantID: "tenant-a", RunID: "run-a", AgentID: "agent-a", RuntimeOptions: agentcore.RuntimeOptions{ProviderConfig: json.RawMessage(`{"device_id":"device_test_001","workspace_grant_id":"grant_test_001","cwd":"D:\\private"}`)}}
	if err := connection.BindRuntimeRequest("session-a", request); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeInvalidPayload) {
		t.Fatalf("unsafe config error = %v", err)
	}
	caps := connection.Capabilities(ctx)
	permission := agentcore.RuntimeCommand{CommandID: "permission-a", Kind: agentcore.RuntimeCommandAnswerPermission, AnswerPermission: &agentcore.AnswerPermissionCommand{RequestID: "p-a", Decision: agentcore.PermissionApproved}}
	auth := agentcore.RuntimeCommand{CommandID: "auth-a", Kind: agentcore.RuntimeCommandAnswerAuth, AnswerAuth: &agentcore.AnswerAuthCommand{RequestID: "a-a", MethodID: "local", Credentials: json.RawMessage(`{"token":"secret"}`)}}
	if caps.Supports(permission) || caps.Supports(auth) {
		t.Fatalf("unsafe commands were supported: %#v", caps.SupportedCommands)
	}
}
