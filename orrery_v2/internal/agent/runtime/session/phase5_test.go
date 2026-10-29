package session

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func canonicalTestRequest(t *testing.T, runID string) agentcore.RuntimeRequest {
	t.Helper()
	workspace := t.TempDir()
	return agentcore.RuntimeRequest{
		TenantID: "tenant-a",
		RunID:    agentcore.AgentRunID(runID),
		AgentID:  agentcore.AgentID("agent-a"),
		InputMessages: []agentcore.RuntimeChatMessage{
			{Role: "user", Content: "run canonical session test"},
		},
		RuntimeOptions: agentcore.RuntimeOptions{
			WorkingDirectory: workspace,
			WorkspacePolicy: agentcore.RuntimeWorkspacePolicy{
				Root:            workspace,
				WorkDir:         workspace,
				EnforceBoundary: true,
			},
		},
		Deadline: time.Now().Add(time.Minute),
	}
}

func TestWorkspaceBrokerOwnsValidatedLaunchAndAuthMaterial(t *testing.T) {
	request := canonicalTestRequest(t, "launch-binding")
	request.RuntimeOptions.ProviderConfig = json.RawMessage(`{"executable":"codex-private","version":"0.145.0","extra_args":["--enable","feature-a"],"env":{"PROFILE_SECRET":"private-profile"},"auth":{"method_id":"refresh","credential_ref":"secret://tenants/tenant-a/codex-auth"}}`)
	request.RuntimeOptions.ExtraArgs = []string{"--strict-config"}
	request.RuntimeOptions.Environment = map[string]string{"REQUEST_SECRET": "private-request"}

	broker := NewWorkspaceBroker()
	binding, err := broker.Bind(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if binding.Launch.Executable != "codex-private" || binding.Launch.Version != "0.145.0" || len(binding.Launch.ExtraArgs) != 3 {
		t.Fatalf("launch = %#v", binding.Launch)
	}
	if binding.Launch.Environment["PROFILE_SECRET"] != "private-profile" || binding.Launch.Environment["REQUEST_SECRET"] != "private-request" {
		t.Fatalf("launch environment = %#v", binding.Launch.Environment)
	}
	if binding.Auth.MethodID != "refresh" || binding.Auth.CredentialRef != "secret://tenants/tenant-a/codex-auth" {
		t.Fatalf("auth binding = %#v", binding.Auth)
	}

	binding.Launch.ExtraArgs[0] = "mutated"
	binding.Launch.Environment["PROFILE_SECRET"] = "mutated"
	binding.Auth.CredentialRef = "secret://tenants/other/changed"
	resolved, err := broker.Resolve(binding.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Launch.ExtraArgs[0] == "mutated" || resolved.Launch.Environment["PROFILE_SECRET"] == "mutated" || resolved.Auth.CredentialRef != "secret://tenants/tenant-a/codex-auth" {
		t.Fatalf("broker did not defensively retain launch material: %#v", resolved)
	}

	canonical, err := json.Marshal(runtimeSessionConfig(request, resolved.Ref, resolved.MCP))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private-profile", "private-request", "codex-auth", "codex-private"} {
		if strings.Contains(string(canonical), secret) {
			t.Fatalf("private launch material leaked into canonical config: %s", canonical)
		}
	}
}

func TestWorkspaceBrokerRejectsOversizedOrMalformedLaunchMaterial(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*agentcore.RuntimeRequest)
	}{
		{name: "argument", mutate: func(request *agentcore.RuntimeRequest) {
			request.RuntimeOptions.ExtraArgs = []string{strings.Repeat("x", maxRuntimeLaunchArgument+1)}
		}},
		{name: "environment name", mutate: func(request *agentcore.RuntimeRequest) {
			request.RuntimeOptions.Environment = map[string]string{strings.Repeat("N", maxRuntimeLaunchEnvName+1): "value"}
		}},
		{name: "environment value", mutate: func(request *agentcore.RuntimeRequest) {
			request.RuntimeOptions.Environment = map[string]string{"TOKEN": strings.Repeat("x", maxRuntimeLaunchEnvValue+1)}
		}},
		{name: "auth literal", mutate: func(request *agentcore.RuntimeRequest) {
			request.RuntimeOptions.ProviderConfig = json.RawMessage(`{"auth":{"method_id":"refresh","credential_ref":"private-token"}}`)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := canonicalTestRequest(t, "invalid-"+strings.ReplaceAll(test.name, " ", "-"))
			test.mutate(&request)
			if _, err := NewWorkspaceBroker().Bind(context.Background(), request); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeInvalidPayload) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestApprovalPermissionHandlerMapsDecisionRememberAndQuestions(t *testing.T) {
	questions := []agentcore.PermissionQuestion{{Question: "Pick one", Options: []agentcore.PermissionQuestionOption{{Label: "A"}}}, {Question: "Pick many", MultiSelect: true, Options: []agentcore.PermissionQuestionOption{{Label: "X"}, {Label: "Y"}}}}
	broker := runtimecore.ApprovalBrokerFunc(func(_ context.Context, request runtimecore.ApprovalRequest, respond runtimecore.ApprovalResponder) error {
		if request.Kind != runtimecore.ApprovalKindMCPElicitation || request.Metadata[runtimecore.ApprovalMetadataRunID] != "run-a" {
			t.Fatalf("approval request = %#v", request)
		}
		return respond(runtimecore.ApprovalResponse{
			Decision: runtimecore.ApprovalDecisionApproved, Remember: true,
			Metadata: map[string]any{"selected": "A", "modified_input": map[string]any{"Pick one": "A", "Pick many": []any{"X", "Y"}}},
		})
	})
	handler := ApprovalPermissionHandler{Broker: broker}
	var answer agentcore.AnswerPermissionCommand
	err := handler.RequestSessionPermission(context.Background(), agentcore.SessionPermissionRequest{
		TenantID: "tenant-a", RunID: "run-a", AgentID: "agent-a", RuntimeKind: agentcore.RuntimeKindCodex,
		Envelope: agentcore.SessionEnvelope{SessionID: "session-a", TurnGeneration: 3, Event: agentcore.SessionEvent{Kind: agentcore.EventPermission, Permission: &agentcore.PermissionEvent{RequestID: "permission-a", Kind: agentcore.PermissionKindTool, ToolName: "AskUserQuestion", Questions: questions}}},
	}, func(value agentcore.AnswerPermissionCommand) error { answer = value; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if answer.Decision != agentcore.PermissionAllowAlways || answer.Selected != "A" || len(answer.Answers) != 2 || len(answer.Answers[1].Labels) != 2 {
		t.Fatalf("answer = %#v", answer)
	}
}

type approvalContextBackend struct {
	*scriptedSessionBackend
	request runtimecore.ApprovalRequest
}

func (b *approvalContextBackend) PendingRuntimeApprovalRequest(requestID string) (runtimecore.ApprovalRequest, bool) {
	return b.request, requestID == b.request.RequestID
}

func TestApprovalPermissionHandlerPreservesProviderApprovalMaterialContext(t *testing.T) {
	original := runtimecore.ApprovalRequest{
		Source:        "pi",
		RequestID:     "permission-material",
		Kind:          runtimecore.ApprovalKindTool,
		ToolName:      "mcp_marketing_marketing_wechat_draft_create",
		Payload:       json.RawMessage(`{"input":{"title":"Reviewed article","article_md_path":"article.md"}}`),
		Metadata:      map[string]string{"approval_force_manual": "true"},
		WorkspaceRoot: "/srv/private/runtime-workspace",
	}
	backend := &approvalContextBackend{scriptedSessionBackend: newScriptedSessionBackend(), request: original}
	ctx := sessionPermissionContext(context.Background(), backend, original.RequestID)
	handler := ApprovalPermissionHandler{Broker: runtimecore.ApprovalBrokerFunc(func(_ context.Context, request runtimecore.ApprovalRequest, respond runtimecore.ApprovalResponder) error {
		if request.Source != original.Source || request.RequestID != original.RequestID || request.Kind != original.Kind || request.ToolName != original.ToolName ||
			string(request.Payload) != string(original.Payload) || request.Metadata["approval_force_manual"] != "true" ||
			request.Metadata[runtimecore.ApprovalMetadataRunID] != "run-material" || request.WorkspaceRoot != original.WorkspaceRoot {
			t.Fatalf("approval request = %#v", request)
		}
		return respond(runtimecore.ApprovalResponse{Decision: runtimecore.ApprovalDecisionDenied})
	})}
	err := handler.RequestSessionPermission(ctx, agentcore.SessionPermissionRequest{
		TenantID: "tenant-a", RunID: "run-material", AgentID: "agent-a", RuntimeKind: agentcore.RuntimeKindPi,
		Envelope: agentcore.SessionEnvelope{SessionID: "session-material", TurnGeneration: 2, Event: agentcore.SessionEvent{Kind: agentcore.EventPermission, Permission: &agentcore.PermissionEvent{
			RequestID: original.RequestID, Kind: agentcore.PermissionKindTool, ToolName: original.ToolName,
		}}},
	}, func(agentcore.AnswerPermissionCommand) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
}

func TestApprovalPermissionHandlerMapsDenialWithoutQuestionAnswers(t *testing.T) {
	handler := ApprovalPermissionHandler{Broker: runtimecore.ApprovalBrokerFunc(func(_ context.Context, _ runtimecore.ApprovalRequest, respond runtimecore.ApprovalResponder) error {
		return respond(runtimecore.ApprovalResponse{Decision: runtimecore.ApprovalDecisionDenied, Content: map[string]any{"Question": "must not be used"}})
	})}
	var answer agentcore.AnswerPermissionCommand
	err := handler.RequestSessionPermission(context.Background(), agentcore.SessionPermissionRequest{
		RunID: "run-a", AgentID: "agent-a", RuntimeKind: agentcore.RuntimeKindClaude,
		Envelope: agentcore.SessionEnvelope{Event: agentcore.SessionEvent{Kind: agentcore.EventPermission, Permission: &agentcore.PermissionEvent{RequestID: "permission-a", Kind: agentcore.PermissionKindTool, ToolName: "Bash", Questions: []agentcore.PermissionQuestion{{Question: "Question", Options: []agentcore.PermissionQuestionOption{{Label: "yes"}}}}}}},
	}, func(value agentcore.AnswerPermissionCommand) error { answer = value; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if answer.Decision != agentcore.PermissionDenied || len(answer.Answers) != 0 {
		t.Fatalf("answer = %#v", answer)
	}
}

type runtimeCredentialBrokerFunc func(context.Context, string, string) (json.RawMessage, error)

func (f runtimeCredentialBrokerFunc) ResolveRuntimeCredential(ctx context.Context, tenantID string, ref string) (json.RawMessage, error) {
	return f(ctx, tenantID, ref)
}

func TestCredentialAuthHandlerResolvesByTenantAndKeepsCredentialsOffEvent(t *testing.T) {
	handler := CredentialAuthHandler{Broker: runtimeCredentialBrokerFunc(func(_ context.Context, tenantID string, ref string) (json.RawMessage, error) {
		if tenantID != "tenant-a" || ref != "secret://tenants/tenant-a/codex-auth" {
			t.Fatalf("credential lookup = %q %q", tenantID, ref)
		}
		return json.RawMessage(`{"access_token":"private-token","chatgpt_account_id":"acct"}`), nil
	})}
	envelope := agentcore.SessionEnvelope{Event: agentcore.SessionEvent{Kind: agentcore.EventPermission, Permission: &agentcore.PermissionEvent{RequestID: "auth-a", Kind: agentcore.PermissionKindAuth}}}
	var answer agentcore.AnswerAuthCommand
	err := handler.RequestSessionAuth(context.Background(), agentcore.SessionAuthRequest{
		TenantID: "tenant-a", RunID: "run-a", AgentID: "agent-a", RuntimeKind: agentcore.RuntimeKindCodex,
		Envelope: envelope, MethodID: "refresh", CredentialRef: "secret://tenants/tenant-a/codex-auth",
	}, func(value agentcore.AnswerAuthCommand) error { answer = value; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if answer.RequestID != "auth-a" || answer.MethodID != "refresh" || !strings.Contains(string(answer.Credentials), "private-token") {
		t.Fatalf("answer = %#v", answer)
	}
	encoded, _ := json.Marshal(envelope)
	if strings.Contains(string(encoded), "private-token") || strings.Contains(string(encoded), "codex-auth") {
		t.Fatalf("credential leaked into canonical event: %s", encoded)
	}
}

func TestRuntimeDispatchesAuthThroughApplicationCredentialBoundary(t *testing.T) {
	connection := &authDispatchConnection{}
	handler := CredentialAuthHandler{Broker: runtimeCredentialBrokerFunc(func(_ context.Context, tenantID string, ref string) (json.RawMessage, error) {
		if tenantID != "tenant-a" || ref != "secret://tenants/tenant-a/codex-auth" {
			t.Fatalf("credential lookup = %q %q", tenantID, ref)
		}
		return json.RawMessage(`{"access_token":"private-runtime-token"}`), nil
	})}
	runtime, err := NewRuntime(RuntimeOptions{
		Kind: agentcore.RuntimeKindCodex, Connection: connection, Workspaces: NewWorkspaceBroker(), AuthHandler: handler,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := canonicalTestRequest(t, "auth-dispatch")
	request.RuntimeOptions.ProviderConfig = json.RawMessage(`{"auth":{"method_id":"refresh","credential_ref":"secret://tenants/tenant-a/codex-auth"}}`)
	response, err := runtime.RunSession(context.Background(), request, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != agentcore.RuntimeStatusCompleted {
		t.Fatalf("response = %#v", response)
	}
	backend := connection.Backend()
	if backend == nil || !backend.AuthAnswerWasValid() || backend.CancelCount() != 0 {
		t.Fatalf("auth dispatch valid=%v cancels=%d", backend != nil && backend.AuthAnswerWasValid(), backend.CancelCount())
	}
	encoded, err := json.Marshal(runtime.RuntimeEvents(request.RunID))
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"private-runtime-token", "secret://tenants/tenant-a/codex-auth"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("auth material leaked into canonical events: %s", encoded)
		}
	}
}

func TestRuntimeDeduplicatesRepeatedPermissionEventsBeforeApplicationHandling(t *testing.T) {
	connection := &authDispatchConnection{duplicatePermission: true}
	brokerCalls := 0
	handler := CredentialAuthHandler{Broker: runtimeCredentialBrokerFunc(func(_ context.Context, tenantID string, ref string) (json.RawMessage, error) {
		brokerCalls++
		return json.RawMessage(`{"access_token":"private-runtime-token"}`), nil
	})}
	runtime, err := NewRuntime(RuntimeOptions{
		Kind: agentcore.RuntimeKindCodex, Connection: connection, Workspaces: NewWorkspaceBroker(), AuthHandler: handler,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := canonicalTestRequest(t, "duplicate-permission")
	request.RuntimeOptions.ProviderConfig = json.RawMessage(`{"auth":{"method_id":"refresh","credential_ref":"secret://tenants/tenant-a/codex-auth"}}`)
	response, err := runtime.RunSession(context.Background(), request, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != agentcore.RuntimeStatusCompleted || brokerCalls != 1 {
		t.Fatalf("response status=%q broker calls=%d, want completed/1", response.Status, brokerCalls)
	}
	permissionEvents := 0
	for _, envelope := range runtime.RuntimeEvents(request.RunID) {
		if envelope.Event.Kind == agentcore.EventPermission {
			permissionEvents++
		}
	}
	if permissionEvents != 1 {
		t.Fatalf("recorded permission events = %d, want 1", permissionEvents)
	}
}

func TestRuntimeOpenFencesExplicitScopeResponsibilityBeforeProviderLaunch(t *testing.T) {
	for _, test := range []struct {
		name     string
		kind     agentcore.RuntimeKind
		rejected bool
	}{
		{name: "unfenced codex", kind: agentcore.RuntimeKindCodex, rejected: true},
		{name: "brokered pi", kind: agentcore.RuntimeKindPi},
	} {
		t.Run(test.name, func(t *testing.T) {
			connection := &authDispatchConnection{}
			runtime, err := NewRuntime(RuntimeOptions{Kind: test.kind, Connection: connection, Workspaces: NewWorkspaceBroker()})
			if err != nil {
				t.Fatal(err)
			}
			request := canonicalTestRequest(t, "explicit-scope-"+string(test.kind))
			request.ContextPack.ResponsibilityContext = &agentcore.ResponsibilityContext{
				ContextID: "responsibility-context", ResponsibilityID: "responsibility-1", ResponsibilityVersion: 1,
				RequireExplicitToolScopes: true, TaskPermissionScopes: []string{"repository.read"},
			}
			opened, err := runtime.open(context.Background(), request)
			if test.rejected {
				if !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeUnsupportedCommand) || opened != nil || connection.Backend() != nil {
					t.Fatalf("unfenced open=%+v backend=%+v err=%v", opened, connection.Backend(), err)
				}
				return
			}
			if err != nil || opened == nil || connection.Backend() == nil {
				t.Fatalf("brokered open=%+v backend=%+v err=%v", opened, connection.Backend(), err)
			}
			runtime.closeOpen(context.Background(), opened)
		})
	}
}

func TestRuntimePrewarmCannotBypassExplicitScopeResponsibilityFence(t *testing.T) {
	connection := &authDispatchConnection{}
	runtime, err := NewRuntime(RuntimeOptions{Kind: agentcore.RuntimeKindCodex, Connection: connection, Workspaces: NewWorkspaceBroker()})
	if err != nil {
		t.Fatal(err)
	}
	request := canonicalTestRequest(t, "prewarm-explicit-scope-fence")
	if err := runtime.PrewarmSession(context.Background(), request); err != nil {
		t.Fatalf("PrewarmSession: %v", err)
	}
	request.ContextPack.ResponsibilityContext = &agentcore.ResponsibilityContext{
		ContextID: "responsibility-context", ResponsibilityID: "responsibility-1", ResponsibilityVersion: 1,
		RequireExplicitToolScopes: true, TaskPermissionScopes: []string{"repository.read"},
	}
	opened, err := runtime.takeOrOpen(context.Background(), request)
	if !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeUnsupportedCommand) || opened != nil {
		t.Fatalf("prewarmed explicit-scope open=%+v err=%v", opened, err)
	}
	runtime.mu.Lock()
	remaining := len(runtime.prewarmed)
	runtime.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("prewarmed sessions remaining=%d, want 0", remaining)
	}
}

func TestRuntimeAuthFailureCancelsAndFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		handler agentcore.SessionAuthHandler
		code    agentcore.RuntimeBridgeErrorCode
	}{
		{name: "missing handler", code: agentcore.RuntimeBridgeUnsupportedCommand},
		{name: "credential resolution failed", code: agentcore.RuntimeBridgeWorkspaceUnavailable, handler: CredentialAuthHandler{Broker: runtimeCredentialBrokerFunc(func(context.Context, string, string) (json.RawMessage, error) {
			return nil, errors.New("secret store unavailable")
		})}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			connection := &authDispatchConnection{}
			runtime, err := NewRuntime(RuntimeOptions{
				Kind: agentcore.RuntimeKindCodex, Connection: connection, Workspaces: NewWorkspaceBroker(), AuthHandler: test.handler,
			})
			if err != nil {
				t.Fatal(err)
			}
			request := canonicalTestRequest(t, "auth-fail-"+strings.ReplaceAll(test.name, " ", "-"))
			request.RuntimeOptions.ProviderConfig = json.RawMessage(`{"auth":{"method_id":"refresh","credential_ref":"secret://tenants/tenant-a/codex-auth"}}`)
			_, runErr := runtime.RunSession(context.Background(), request, nil)
			if !agentcore.IsRuntimeBridgeError(runErr, test.code) {
				t.Fatalf("error = %v", runErr)
			}
			backend := connection.Backend()
			if backend == nil || backend.AuthAnswerWasValid() || backend.CancelCount() != 1 {
				t.Fatalf("fail-closed auth valid=%v cancels=%d", backend != nil && backend.AuthAnswerWasValid(), backend.CancelCount())
			}
		})
	}
}

type authDispatchConnection struct {
	mu                  sync.Mutex
	backend             *authDispatchBackend
	duplicatePermission bool
}

func (c *authDispatchConnection) OpenSession(_ context.Context, spec agentcore.SessionSpec, _ agentcore.SessionConfig) (agentcore.SessionBackend, error) {
	backend := &authDispatchBackend{sessionID: spec.SessionID, events: make(chan agentcore.SessionEnvelope, 8), duplicatePermission: c.duplicatePermission}
	c.mu.Lock()
	c.backend = backend
	c.mu.Unlock()
	return backend, nil
}

func (c *authDispatchConnection) CloseSession(context.Context, agentcore.SessionID) error {
	if backend := c.Backend(); backend != nil {
		backend.Close()
	}
	return nil
}

func (c *authDispatchConnection) Capabilities(context.Context) agentcore.Capabilities {
	return authDispatchCapabilities()
}

func (c *authDispatchConnection) Backend() *authDispatchBackend {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.backend
}

type authDispatchBackend struct {
	sessionID           agentcore.SessionID
	events              chan agentcore.SessionEnvelope
	mu                  sync.Mutex
	pending             bool
	answered            bool
	cancels             int
	closed              bool
	duplicatePermission bool
}

func (b *authDispatchBackend) Dispatch(_ context.Context, command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	switch command.Kind {
	case agentcore.RuntimeCommandSend:
		b.mu.Lock()
		b.pending = true
		b.mu.Unlock()
		b.emit(agentcore.SessionEvent{Kind: agentcore.EventPromptAccepted, PromptAccepted: &agentcore.PromptAcceptedEvent{ClientMsgID: command.Send.Metadata.ClientMsgID}})
		b.emit(agentcore.SessionEvent{Kind: agentcore.EventPermission, Permission: &agentcore.PermissionEvent{RequestID: "auth-request", Kind: agentcore.PermissionKindAuth}})
		if b.duplicatePermission {
			b.emit(agentcore.SessionEvent{Kind: agentcore.EventPermission, Permission: &agentcore.PermissionEvent{RequestID: "auth-request", Kind: agentcore.PermissionKindAuth}})
		}
		return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionStarted, TurnGeneration: 1}, nil
	case agentcore.RuntimeCommandAnswerAuth:
		valid := command.AnswerAuth.RequestID == "auth-request" && command.AnswerAuth.MethodID == "refresh" && strings.Contains(string(command.AnswerAuth.Credentials), "private-runtime-token")
		b.mu.Lock()
		b.answered = valid
		b.pending = false
		b.mu.Unlock()
		if !valid {
			return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeBackendRejected, "auth answer did not match request", false)
		}
		b.emit(agentcore.SessionEvent{Kind: agentcore.EventPermissionResolved, PermissionResolved: &agentcore.PermissionResolvedEvent{RequestID: "auth-request", Kind: agentcore.PermissionKindAuth}})
		b.emit(agentcore.SessionEvent{Kind: agentcore.EventMessageFinalized, MessageFinalized: &agentcore.MessageFinalizedEvent{Message: agentcore.FinalizedMessage{ItemID: "message-a", Kind: agentcore.ItemText, Content: "authenticated", Sequence: 1}}})
		b.emit(agentcore.SessionEvent{Kind: agentcore.EventTurnResult, TurnResult: &agentcore.TurnResultEvent{Epoch: 1, Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeCompleted, StopReason: &agentcore.StopReason{Kind: agentcore.StopReasonEndTurn}}}})
		return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn, TurnGeneration: 1}, nil
	case agentcore.RuntimeCommandCancel:
		b.mu.Lock()
		b.pending = false
		b.cancels++
		b.mu.Unlock()
		return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn, TurnGeneration: 1}, nil
	default:
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, string(command.Kind), false)
	}
}

func (b *authDispatchBackend) Events() <-chan agentcore.SessionEnvelope { return b.events }
func (b *authDispatchBackend) Capabilities() agentcore.Capabilities {
	return authDispatchCapabilities()
}
func (b *authDispatchBackend) PendingPermissionRequests() []agentcore.PendingPermissionView {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.pending {
		return nil
	}
	return []agentcore.PendingPermissionView{{RequestID: "auth-request"}}
}
func (b *authDispatchBackend) emit(event agentcore.SessionEvent) {
	b.events <- agentcore.SessionEnvelope{SessionID: b.sessionID, TurnGeneration: 1, Timestamp: time.Now().UTC(), Source: agentcore.EventSourceProvider, Projection: agentcore.EventProjectionCanonical, Event: event}
}
func (b *authDispatchBackend) AuthAnswerWasValid() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.answered
}
func (b *authDispatchBackend) CancelCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cancels
}
func (b *authDispatchBackend) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.closed {
		b.closed = true
		close(b.events)
	}
}

func authDispatchCapabilities() agentcore.Capabilities {
	return agentcore.Capabilities{
		Tier: agentcore.CapabilityHook,
		SupportedCommands: agentcore.CommandSet{
			Send: true, CancelTurn: true, AnswerAuth: true,
		},
		PromptBlocks: agentcore.BlockSet{Text: true}, PromptAccepted: agentcore.PromptAcceptedNative,
		Signals: agentcore.SignalSet{TerminalResult: true}, AuthMethods: []string{"refresh"},
		Transport: agentcore.TransportEvidence{Provider: "auth-test", Protocol: "typed"},
	}
}

var _ agentcore.BackendConnection = (*authDispatchConnection)(nil)
var _ agentcore.SessionBackend = (*authDispatchBackend)(nil)
