package device

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type RuntimeConfig struct {
	DeviceID                 string          `json:"device_id"`
	WorkspaceGrantID         string          `json:"workspace_grant_id"`
	RequiredCapabilities     []Capability    `json:"required_capabilities,omitempty"`
	RequestedArtifactClasses []ArtifactClass `json:"requested_artifact_classes,omitempty"`
}

type ConnectionConfig struct {
	Broker          DeviceWorkOrderBroker
	DefaultDeadline time.Duration
	Now             func() time.Time
	CloseBroker     bool
}

type Connection struct {
	broker          DeviceWorkOrderBroker
	defaultDeadline time.Duration
	now             func() time.Time
	closeBroker     bool

	mu       sync.Mutex
	bindings map[agentcore.SessionID]sessionBinding
	sessions map[agentcore.SessionID]*SessionBackend
	closed   bool
}

type sessionBinding struct {
	tenantID             string
	runID                agentcore.AgentRunID
	agentID              agentcore.AgentID
	deviceID             string
	workspaceGrantID     string
	projectID            string
	originActorID        string
	requiredCapabilities []Capability
	requestedArtifacts   []ArtifactClass
	deadline             time.Time
}

func NewConnection(config ConnectionConfig) (*Connection, error) {
	if config.Broker == nil {
		return nil, errors.New("device connection requires DeviceWorkOrderBroker")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.DefaultDeadline <= 0 {
		config.DefaultDeadline = 30 * time.Minute
	}
	return &Connection{broker: config.Broker, defaultDeadline: config.DefaultDeadline, now: config.Now, closeBroker: config.CloseBroker, bindings: map[agentcore.SessionID]sessionBinding{}, sessions: map[agentcore.SessionID]*SessionBackend{}}, nil
}

func (c *Connection) BindRuntimeRequest(sessionID agentcore.SessionID, request agentcore.RuntimeRequest) error {
	if strings.TrimSpace(string(sessionID)) == "" || strings.TrimSpace(request.TenantID) == "" || request.RunID == "" || request.AgentID.IsZero() {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "device runtime identity is incomplete", false)
	}
	config, err := parseRuntimeConfig(request.RuntimeOptions.ProviderConfig)
	if err != nil {
		return err
	}
	if !validIdentifier(config.DeviceID) || !validIdentifier(config.WorkspaceGrantID) {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "device runtime requires valid device_id and workspace_grant_id", false)
	}
	for _, capability := range config.RequiredCapabilities {
		if !validCapability(capability) {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "device runtime capability is invalid", false)
		}
	}
	for _, class := range config.RequestedArtifactClasses {
		if !validArtifactClass(class) {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "device runtime artifact class is invalid", false)
		}
	}
	deadline := request.Deadline
	if request.RuntimeOptions.Timeout > 0 {
		candidate := c.now().Add(request.RuntimeOptions.Timeout)
		if deadline.IsZero() || candidate.Before(deadline) {
			deadline = candidate
		}
	}
	if deadline.IsZero() {
		deadline = c.now().Add(c.defaultDeadline)
	}
	projectSource := firstNonBlank(request.MissionID, request.TaskID, request.RunID.String())
	originSource := request.AgentID.String()
	binding := sessionBinding{
		tenantID: strings.TrimSpace(request.TenantID), runID: request.RunID, agentID: request.AgentID,
		deviceID: config.DeviceID, workspaceGrantID: config.WorkspaceGrantID,
		projectID: opaqueIdentifier(projectSource, "project"), originActorID: opaqueIdentifier(originSource, "actor"),
		requiredCapabilities: append([]Capability(nil), config.RequiredCapabilities...), requestedArtifacts: append([]ArtifactClass(nil), config.RequestedArtifactClasses...), deadline: deadline.UTC(),
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "device connection is closed", false)
	}
	if _, exists := c.sessions[sessionID]; exists {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "device session is already open", false)
	}
	if existing, exists := c.bindings[sessionID]; exists && existing.runID != binding.runID {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "device session binding changed", false)
	}
	c.bindings[sessionID] = binding
	return nil
}

func (c *Connection) OpenSession(ctx context.Context, spec agentcore.SessionSpec, config agentcore.SessionConfig) (agentcore.SessionBackend, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if len(config.MCPServers) != 0 || strings.TrimSpace(config.Preset) != "" || strings.TrimSpace(config.Model) != "" || strings.TrimSpace(config.Mode) != "" || len(config.ConfigOptions) != 0 {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "device session cannot receive provider, model, prompt preset, or MCP configuration", false)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "device connection is closed", false)
	}
	if _, exists := c.sessions[spec.SessionID]; exists {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "device session is already open", false)
	}
	binding, exists := c.bindings[spec.SessionID]
	if !exists {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "device runtime request is not bound", false)
	}
	workOrderID := deviceWorkOrderID(binding.tenantID, binding.runID)
	if spec.Mode == agentcore.SessionOpenResume && strings.TrimSpace(spec.BackendSessionID) != workOrderID {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeSessionMismatch, "device resume identity does not match the run WorkOrder", false)
	}
	backend := newSessionBackend(spec.SessionID, workOrderID, binding, c.broker, c.now)
	c.sessions[spec.SessionID] = backend
	delete(c.bindings, spec.SessionID)
	return backend, nil
}

func (c *Connection) PrepareSessionConfig(_ context.Context, _ agentcore.SessionSpec, config agentcore.SessionConfig) (agentcore.SessionConfig, error) {
	// Device execution policy is owned by the local Workspace Grant and local
	// Agent Profile. The cloud may pass only the opaque workspace binding.
	return agentcore.SessionConfig{WorkspaceRef: config.WorkspaceRef, IdleTTL: config.IdleTTL}, nil
}

func (c *Connection) CloseSession(_ context.Context, sessionID agentcore.SessionID) error {
	c.mu.Lock()
	backend := c.sessions[sessionID]
	delete(c.sessions, sessionID)
	delete(c.bindings, sessionID)
	c.mu.Unlock()
	if backend != nil {
		backend.close()
	}
	return nil
}

func (c *Connection) Capabilities(context.Context) agentcore.Capabilities {
	return deviceCapabilities()
}

func (c *Connection) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	sessions := make([]*SessionBackend, 0, len(c.sessions))
	for _, session := range c.sessions {
		sessions = append(sessions, session)
	}
	c.sessions, c.bindings = map[agentcore.SessionID]*SessionBackend{}, map[agentcore.SessionID]sessionBinding{}
	c.mu.Unlock()
	for _, session := range sessions {
		session.close()
	}
	if c.closeBroker {
		if closer, ok := c.broker.(interface{ Close() error }); ok {
			return closer.Close()
		}
	}
	return nil
}

type SessionBackend struct {
	sessionID   agentcore.SessionID
	workOrderID string
	binding     sessionBinding
	broker      DeviceWorkOrderBroker
	now         func() time.Time

	ctx    context.Context
	cancel context.CancelFunc
	events chan agentcore.SessionEnvelope

	mu           sync.Mutex
	sent         bool
	generation   uint64
	subscription WorkOrderSubscription
	pending      map[string]agentcore.PendingPermissionView
	closed       bool
	closeOnce    sync.Once
	operations   sync.WaitGroup
}

type admissionResult struct {
	accepted bool
	rejected *WorkOrderRejectedPayload
	err      error
}

func newSessionBackend(sessionID agentcore.SessionID, workOrderID string, binding sessionBinding, broker DeviceWorkOrderBroker, now func() time.Time) *SessionBackend {
	ctx, cancel := context.WithCancel(context.Background())
	return &SessionBackend{sessionID: sessionID, workOrderID: workOrderID, binding: binding, broker: broker, now: now, ctx: ctx, cancel: cancel, events: make(chan agentcore.SessionEnvelope, 1024), pending: map[string]agentcore.PendingPermissionView{}}
}

func (s *SessionBackend) Dispatch(ctx context.Context, command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	if err := command.Validate(); err != nil {
		return agentcore.CommandReceipt{}, err
	}
	if !s.Capabilities().Supports(command) {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, string(command.Kind), false)
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "device session is closed", false)
	}
	s.operations.Add(1)
	s.mu.Unlock()
	defer s.operations.Done()
	switch command.Kind {
	case agentcore.RuntimeCommandSend:
		return s.dispatchSend(ctx, command)
	case agentcore.RuntimeCommandCancel:
		reason := sanitizeReason(command.Cancel.Reason, 512)
		if reason == "" {
			reason = "cancelled by cloud runtime"
		}
		if err := s.broker.Cancel(ctx, s.binding.tenantID, s.binding.deviceID, s.workOrderID, reason); err != nil {
			return agentcore.CommandReceipt{}, err
		}
		return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn, TurnGeneration: s.currentGeneration()}, nil
	default:
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, string(command.Kind), false)
	}
}

func (s *SessionBackend) dispatchSend(ctx context.Context, command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "device session is closed", false)
	}
	if s.sent {
		s.mu.Unlock()
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "a Device Session maps to exactly one agent_turn WorkOrder", false)
	}
	s.sent = true
	s.generation = 1
	s.mu.Unlock()
	instruction, err := deviceInstruction(command.Send.Content)
	if err != nil {
		s.releaseSendReservation()
		return agentcore.CommandReceipt{}, err
	}
	order := WorkOrder{
		WorkOrderID: s.workOrderID, IdempotencyKey: deviceIdempotencyKey(s.binding.tenantID, s.binding.runID),
		ProjectID: s.binding.projectID, OriginActorID: s.binding.originActorID,
		Task: WorkOrderTask{Kind: "agent_turn", Instruction: instruction}, WorkspaceGrantID: s.binding.workspaceGrantID,
		RequiredCapabilities: append([]Capability(nil), s.binding.requiredCapabilities...), RequestedArtifactClasses: append([]ArtifactClass(nil), s.binding.requestedArtifacts...), Deadline: s.binding.deadline,
	}
	_, subscription, err := s.broker.Offer(ctx, OfferRequest{TenantID: s.binding.tenantID, DeviceID: s.binding.deviceID, SessionID: string(s.sessionID), Order: order})
	if err != nil {
		s.releaseSendReservation()
		if errors.Is(err, ErrRemoteExecutionDisabled) {
			return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeBackendRejected, err.Error(), false)
		}
		return agentcore.CommandReceipt{}, err
	}
	s.mu.Lock()
	s.subscription = subscription
	s.mu.Unlock()
	admission := make(chan admissionResult, 1)
	s.operations.Add(1)
	go func() {
		defer s.operations.Done()
		s.pump(subscription, command.Send.Metadata.ClientMsgID, admission)
	}()
	select {
	case result := <-admission:
		if result.err != nil {
			return agentcore.CommandReceipt{}, result.err
		}
		if result.rejected != nil {
			return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeBackendRejected, "device rejected WorkOrder: "+string(result.rejected.ReasonCode), false)
		}
		if !result.accepted {
			return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "device WorkOrder admission ended before acceptance", true)
		}
		return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionStarted, TurnGeneration: 1}, nil
	case <-ctx.Done():
		_ = s.broker.Cancel(context.WithoutCancel(ctx), s.binding.tenantID, s.binding.deviceID, s.workOrderID, "device WorkOrder admission context cancelled")
		return agentcore.CommandReceipt{}, ctx.Err()
	case <-s.ctx.Done():
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "device session closed during admission", true)
	}
}

func (s *SessionBackend) releaseSendReservation() {
	s.mu.Lock()
	if s.subscription == nil {
		s.sent = false
		s.generation = 0
	}
	s.mu.Unlock()
}

func (s *SessionBackend) pump(subscription WorkOrderSubscription, clientMsgID string, admission chan<- admissionResult) {
	defer subscription.Close()
	admitted := false
	notifyAdmission := func(result admissionResult) {
		if admitted {
			return
		}
		admitted = true
		admission <- result
	}
	for {
		select {
		case <-s.ctx.Done():
			notifyAdmission(admissionResult{err: agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "device session closed", true)})
			return
		case event, ok := <-subscription.Events():
			if !ok {
				notifyAdmission(admissionResult{err: agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "device WorkOrder subscription closed", true)})
				return
			}
			switch event.Kind {
			case BrokerAccepted:
				s.emit(event.OccurredAt, agentcore.EventProjectionCanonical, agentcore.SessionEvent{Kind: agentcore.EventPromptAccepted, PromptAccepted: &agentcore.PromptAcceptedEvent{ClientMsgID: clientMsgID}})
				notifyAdmission(admissionResult{accepted: true})
			case BrokerRejected:
				notifyAdmission(admissionResult{rejected: event.Rejected})
				return
			case BrokerFrame:
				if !admitted {
					notifyAdmission(admissionResult{err: agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "device emitted frame before WorkOrder acceptance", false)})
					return
				}
				s.mapFrame(event)
			case BrokerTerminal:
				if !admitted {
					notifyAdmission(admissionResult{err: agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "device emitted terminal before WorkOrder acceptance", false)})
					return
				}
				s.mapTerminal(event)
				return
			case BrokerArtifact:
				s.mapArtifact(event)
			}
		}
	}
}

func (s *SessionBackend) mapFrame(event BrokerEvent) {
	if event.Frame == nil {
		return
	}
	frame := event.Frame.Frame
	var canonical agentcore.SessionEvent
	switch frame.Kind {
	case "text":
		text := sanitizeDeviceText(frame.Content)
		if frame.Channel == "thinking" {
			canonical = agentcore.SessionEvent{Kind: agentcore.EventThoughtDelta, ThoughtDelta: &agentcore.ThoughtDeltaEvent{ItemID: "device-thinking", Text: text, ReasoningKind: agentcore.ReasoningContentRaw}}
		} else {
			canonical = agentcore.SessionEvent{Kind: agentcore.EventMessageDelta, MessageDelta: &agentcore.MessageDeltaEvent{ItemID: "device-output", Text: text}}
		}
	case "tool":
		if frame.Phase == "started" {
			canonical = agentcore.SessionEvent{Kind: agentcore.EventToolCall, ToolCall: &agentcore.ToolCallEvent{ToolUseID: frame.CallID, Name: sanitizeDeviceText(frame.ToolName)}}
		} else {
			content := sanitizeDeviceText(frame.Summary)
			var resultContent []agentcore.ToolResultContent
			if content != "" {
				resultContent = []agentcore.ToolResultContent{{Kind: agentcore.ToolResultText, Text: content}}
			}
			canonical = agentcore.SessionEvent{Kind: agentcore.EventToolResult, ToolResult: &agentcore.ToolResultEvent{ToolUseID: frame.CallID, IsError: frame.Phase == "failed", Content: resultContent}}
		}
	case "permission":
		if frame.Phase == "waiting" {
			view := agentcore.PendingPermissionView{RequestID: frame.CallID, ToolName: "local." + frame.Category}
			s.mu.Lock()
			s.pending[frame.CallID] = view
			s.mu.Unlock()
			canonical = agentcore.SessionEvent{Kind: agentcore.EventPermission, Permission: &agentcore.PermissionEvent{RequestID: frame.CallID, Kind: agentcore.PermissionKindTool, ToolName: view.ToolName, Context: &agentcore.PermissionContext{ServerName: "device-local"}}}
		} else {
			s.mu.Lock()
			delete(s.pending, frame.CallID)
			s.mu.Unlock()
			canonical = agentcore.SessionEvent{Kind: agentcore.EventPermissionResolved, PermissionResolved: &agentcore.PermissionResolvedEvent{RequestID: frame.CallID, Kind: agentcore.PermissionKindTool}}
		}
	case "usage":
		input, output := uint64(*frame.InputTokens), uint64(*frame.OutputTokens)
		canonical = agentcore.SessionEvent{Kind: agentcore.EventUsageDelta, UsageDelta: &agentcore.UsageDeltaEvent{InputTokens: input, OutputTokens: output, TotalTokens: input + output, CostUSD: frame.CostUSD}}
	case "detached":
		// Device detach is recoverable. Canonical Detached is terminal in the
		// reference reducer, so this state is surfaced as a non-terminal notice;
		// only WorkOrder terminal is allowed to finish the turn.
		canonical = agentcore.SessionEvent{Kind: agentcore.EventNotice, Notice: &agentcore.NoticeEvent{Level: agentcore.NoticeWarning, Message: "local device runtime detached; waiting for recovery"}}
	default:
		return
	}
	s.emit(event.OccurredAt, agentcore.EventProjectionCanonical, canonical)
}

func (s *SessionBackend) mapTerminal(event BrokerEvent) {
	if event.Terminal == nil {
		return
	}
	terminal := event.Terminal
	resultText := sanitizeDeviceText(terminal.Summary)
	outcome := agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeCompleted, StopReason: &agentcore.StopReason{Kind: agentcore.StopReasonEndTurn}}
	isError := false
	switch terminal.Status {
	case TerminalFailed:
		isError = true
		outcome = agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeFailed}
		if resultText == "" {
			resultText = "device work order failed: " + string(terminal.ReasonCode)
		}
	case TerminalCancelled:
		outcome = agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeCancelled, CancelReason: agentcore.CancelReasonUserCancel}
		if resultText == "" {
			resultText = "device work order cancelled"
		}
	}
	s.emit(event.OccurredAt, agentcore.EventProjectionCanonical, agentcore.SessionEvent{Kind: agentcore.EventTurnResult, TurnResult: &agentcore.TurnResultEvent{IsError: isError, ResultText: resultText, Epoch: s.currentGeneration(), Outcome: outcome}})
}

func (s *SessionBackend) mapArtifact(event BrokerEvent) {
	if event.Artifact == nil {
		return
	}
	s.emit(event.OccurredAt, agentcore.EventProjectionDiagnostic, agentcore.SessionEvent{Kind: agentcore.EventAdapterSpecific, AdapterSpecific: &agentcore.AdapterSpecificEvent{Tag: "device_artifact_manifest", Summary: "artifact class " + string(event.Artifact.Class)}})
}

func (s *SessionBackend) emit(at time.Time, projection agentcore.EventProjection, event agentcore.SessionEvent) {
	if at.IsZero() {
		at = s.now().UTC()
	}
	envelope := agentcore.SessionEnvelope{SessionID: s.sessionID, TurnGeneration: s.currentGeneration(), Timestamp: at.UTC(), Source: agentcore.EventSourceProvider, Projection: projection, Event: event}
	select {
	case s.events <- envelope:
	case <-s.ctx.Done():
	}
}

func (s *SessionBackend) Events() <-chan agentcore.SessionEnvelope { return s.events }
func (s *SessionBackend) Capabilities() agentcore.Capabilities     { return deviceCapabilities() }
func (s *SessionBackend) PendingPermissionRequests() []agentcore.PendingPermissionView {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]agentcore.PendingPermissionView, 0, len(s.pending))
	for _, value := range s.pending {
		value.Questions = append([]agentcore.PermissionQuestion(nil), value.Questions...)
		out = append(out, value)
	}
	return out
}
func (s *SessionBackend) currentGeneration() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generation
}
func (s *SessionBackend) close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		subscription := s.subscription
		s.mu.Unlock()
		s.cancel()
		if subscription != nil {
			subscription.Close()
		}
		s.operations.Wait()
		close(s.events)
	})
}

func deviceCapabilities() agentcore.Capabilities {
	return agentcore.Capabilities{
		Tier:              agentcore.CapabilityHook,
		SupportedCommands: agentcore.CommandSet{Send: true, CancelTurn: true, CancelSession: true},
		PromptBlocks:      agentcore.BlockSet{Text: true}, PromptAccepted: agentcore.PromptAcceptedNative,
		Signals:           agentcore.SignalSet{ToolLifecycle: true, TerminalResult: true, Usage: true},
		SupportsMultiTurn: false, SupportsResume: true, PendingLimit: 1, PayloadLimitBytes: ProtocolMaxFrameBytes,
		Transport: agentcore.TransportEvidence{Provider: "device", Protocol: "orrery-device-work-order", Version: ProtocolVersion},
	}
}

func parseRuntimeConfig(raw json.RawMessage) (RuntimeConfig, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return RuntimeConfig{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "device runtime configuration is required", false)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var config RuntimeConfig
	if err := decoder.Decode(&config); err != nil {
		return RuntimeConfig{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "device runtime configuration is invalid", false)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return RuntimeConfig{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "device runtime configuration has trailing data", false)
	}
	return config, nil
}

func deviceInstruction(content []agentcore.ContentBlock) (string, error) {
	parts := make([]string, 0, len(content))
	for _, block := range content {
		if block.Kind != agentcore.ContentBlockText {
			return "", agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, "Device Runtime accepts text instructions only", false)
		}
		parts = append(parts, block.Text)
	}
	instruction := strings.TrimSpace(strings.Join(parts, "\n\n"))
	if !bounded(instruction, 1, 65536) {
		return "", agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "device instruction is empty or exceeds 65536 characters", false)
	}
	return instruction, nil
}

func deviceWorkOrderID(tenantID string, runID agentcore.AgentRunID) string {
	return opaqueHashIdentifier("wo", tenantID+"\x00"+runID.String())
}
func deviceIdempotencyKey(tenantID string, runID agentcore.AgentRunID) string {
	return opaqueHashIdentifier("run", tenantID+"\x00"+runID.String())
}
func opaqueIdentifier(value, prefix string) string {
	value = strings.TrimSpace(value)
	if validIdentifier(value) {
		return value
	}
	return opaqueHashIdentifier(prefix, value)
}
func opaqueHashIdentifier(prefix, value string) string {
	digest := sha256.Sum256([]byte(value))
	return prefix + "_" + hex.EncodeToString(digest[:])
}
func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

var localPathPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b[A-Z]:[\\/][^\s\x00]+`),
	regexp.MustCompile(`\\\\[^\s\\/]+[\\/][^\s\x00]+`),
	regexp.MustCompile(`(^|[\s("'=])/(?:[^\s/'"<>]+/)*[^\s'"<>]+`),
}

var deviceSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\b(?:sk|rk)-[A-Za-z0-9_-]{20,}\b`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`),
	regexp.MustCompile(`(?i)\b(?:api[_-]?key|password|passwd|secret|token)\s*[:=]\s*[^\s,;]{8,}`),
}

func sanitizeDeviceText(value string) string {
	value = strings.ReplaceAll(strings.ReplaceAll(value, "\x00", ""), "\r", "")
	for _, pattern := range localPathPatterns {
		value = pattern.ReplaceAllString(value, "${1}[local-path-redacted]")
	}
	for _, pattern := range deviceSecretPatterns {
		value = pattern.ReplaceAllString(value, "[credential-redacted]")
	}
	return value
}

var _ agentcore.BackendConnection = (*Connection)(nil)
var _ agentcore.SessionConfigPolicy = (*Connection)(nil)
var _ agentcore.SessionBackend = (*SessionBackend)(nil)
