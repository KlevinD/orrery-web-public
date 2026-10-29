package pi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

type NativeSession struct {
	sessionID agentcore.SessionID
	spec      agentcore.SessionSpec
	config    agentcore.SessionConfig
	workspace NativeWorkspace
	native    NativeConfig
	runtime   Config
	opts      runtimecore.ExecOptions
	version   string
	now       func() time.Time

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	dead   atomic.Bool

	events        chan agentcore.SessionEnvelope
	eventIn       chan agentcore.SessionEnvelope
	eventAbort    chan struct{}
	eventPumpDone chan struct{}
	eventMu       sync.RWMutex
	eventsClosed  bool
	eventsOnce    sync.Once
	closeOnce     sync.Once
	pressureOnce  sync.Once

	dispatchMu sync.Mutex
	mu         sync.Mutex
	generation uint64
	active     bool
	cancelled  bool
	terminated map[uint64]bool
	clientIDs  map[string]uint64
	backendID  string
	models     map[string]piNativeModel
	capability agentcore.Capabilities
	pending    map[string]piPendingRequest
	approvals  map[string]piNativeApproval
	ui         map[string]piNativeUIRequest

	requestSequence                    atomic.Uint64
	binding                            *piNativeProcess
	bridge                             preparedBridge
	debug                              piDebugArtifacts
	mapper                             piNativeMapper
	systemPrompt                       string
	deterministicMarketingContinuation bool
	deterministicContinuationUsed      bool
}

type piNativeResponse struct {
	envelope rpcEnvelope
	err      error
}

type piPendingRequest struct {
	response          chan piNativeResponse
	promptClientMsgID string
	promptGeneration  uint64
}

type piNativeApproval struct {
	request   runtimecore.ApprovalRequest
	responder runtimecore.ApprovalResponder
	done      chan struct{}
}

type piNativeUIRequest struct {
	method  string
	title   string
	wireID  string
	options []string
}

type piNativeModel struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	Reasoning bool   `json:"reasoning"`
}

type piNativeState struct {
	SessionID     string         `json:"sessionId"`
	ThinkingLevel string         `json:"thinkingLevel"`
	Model         *piNativeModel `json:"model"`
	IsStreaming   bool           `json:"isStreaming"`
}

type piNativeModels struct {
	Models []piNativeModel `json:"models"`
}

type piNativeCommand struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
}

type piNativeCommands struct {
	Commands []piNativeCommand `json:"commands"`
}

type piNativeStats struct {
	Tokens struct {
		Input      uint64 `json:"input"`
		Output     uint64 `json:"output"`
		CacheRead  uint64 `json:"cacheRead"`
		CacheWrite uint64 `json:"cacheWrite"`
		Total      uint64 `json:"total"`
	} `json:"tokens"`
	Cost         float64 `json:"cost"`
	ContextUsage *struct {
		Tokens        *uint64 `json:"tokens"`
		ContextWindow uint64  `json:"contextWindow"`
	} `json:"contextUsage"`
}

func openNativePiSession(parent context.Context, native NativeConfig, runtimeConfig Config, spec agentcore.SessionSpec, config agentcore.SessionConfig, workspace NativeWorkspace, opts runtimecore.ExecOptions, systemPrompt, executable, version string, debugArtifacts piDebugArtifacts) (*NativeSession, error) {
	ctx, cancel := context.WithCancel(parent)
	session := &NativeSession{
		sessionID: spec.SessionID, spec: spec, config: config, workspace: workspace, native: native, runtime: runtimeConfig,
		opts: opts, version: version, now: native.Now, ctx: ctx, cancel: cancel, done: make(chan struct{}),
		events: make(chan agentcore.SessionEnvelope, native.EventCapacity+2), eventIn: make(chan agentcore.SessionEnvelope, native.EventCapacity),
		eventAbort: make(chan struct{}), eventPumpDone: make(chan struct{}), terminated: make(map[uint64]bool),
		clientIDs: make(map[string]uint64), models: make(map[string]piNativeModel), pending: make(map[string]piPendingRequest),
		approvals: make(map[string]piNativeApproval), ui: make(map[string]piNativeUIRequest), capability: nativePiCapabilities(version),
		debug: debugArtifacts, systemPrompt: systemPrompt,
	}
	session.capability.CurrentModel = strings.TrimSpace(config.Model)
	if effort := strings.TrimSpace(config.ConfigOptions["reasoning_effort"]); effort != "" {
		session.capability.ConfigOptions[0].Current = effort
	}
	session.mapper.reset(0)
	go session.runEventPump()

	bridge, cleanup, err := prepareBridge(ctx, runtimeConfig.ToolDispatcher, nativePiApprovalBroker{session: session}, opts)
	if err != nil {
		cancel()
		session.closeEvents()
		<-session.eventPumpDone
		return nil, err
	}
	session.bridge = bridge
	session.opts.Environment = mergePiEnvironment(session.opts.Environment, bridge.Environment)
	args := BuildArgs("", session.opts)
	binding, err := startPiNativeProcess(session, executable, args, mergePiEnvironment(runtimeConfig.Env, session.opts.Environment))
	if err != nil {
		cleanup()
		cancel()
		session.closeEvents()
		<-session.eventPumpDone
		return nil, err
	}
	session.binding = binding

	handshakeCtx, handshakeCancel := context.WithTimeout(parent, native.HandshakeTimeout)
	err = session.handshake(handshakeCtx)
	handshakeCancel()
	if err != nil {
		_ = binding.Stop(context.Background())
		cleanup()
		cancel()
		session.closeEvents()
		<-session.eventPumpDone
		return nil, err
	}
	return session, nil
}

func (s *NativeSession) Dispatch(ctx context.Context, command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := command.Validate(); err != nil {
		return agentcore.CommandReceipt{}, err
	}
	if !s.Capabilities().Supports(command) {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, string(command.Kind), false)
	}
	if s.dead.Load() {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "pi RPC process is detached", false)
	}
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	switch command.Kind {
	case agentcore.RuntimeCommandSend:
		return s.dispatchSend(ctx, command)
	case agentcore.RuntimeCommandCancel:
		return s.dispatchCancel(ctx, command)
	case agentcore.RuntimeCommandSteer:
		return s.dispatchSteer(ctx, command)
	case agentcore.RuntimeCommandAnswerPermission:
		return s.dispatchAnswerPermission(command)
	case agentcore.RuntimeCommandAcknowledge:
		return s.noTurnReceipt(), nil
	case agentcore.RuntimeCommandSetModel:
		return s.dispatchSetModel(ctx, command.SetModel.Model)
	case agentcore.RuntimeCommandSetConfigOption:
		return s.dispatchSetEffort(ctx, command.SetConfigOption.OptionID, command.SetConfigOption.Value)
	case agentcore.RuntimeCommandQuerySessionInfo:
		return s.dispatchSessionInfo(ctx, command.QuerySessionInfo.Kind)
	default:
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, string(command.Kind), false)
	}
}

func (s *NativeSession) Events() <-chan agentcore.SessionEnvelope { return s.events }

func (s *NativeSession) BackendSessionID() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(s.backendID)
}

func (s *NativeSession) Capabilities() agentcore.Capabilities {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.capability.Clone()
}

func (s *NativeSession) PendingPermissionRequests() []agentcore.PendingPermissionView {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]agentcore.PendingPermissionView, 0, len(s.approvals)+len(s.ui))
	for id, pending := range s.approvals {
		out = append(out, agentcore.PendingPermissionView{RequestID: id, ToolName: pending.request.ToolName})
	}
	for id := range s.ui {
		out = append(out, agentcore.PendingPermissionView{RequestID: id, ToolName: "AskUserQuestion"})
	}
	return out
}

func (s *NativeSession) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var closeErr error
	s.closeOnce.Do(func() {
		s.dead.Store(true)
		if s.binding != nil {
			closeErr = s.binding.Stop(ctx)
		}
		s.cancel()
		s.failPending(agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "pi session closed", false))
		s.failApprovals("pi session closed")
		s.bridge.Cleanup()
		s.debug.Cleanup()
		s.closeEvents()
		<-s.eventPumpDone
		close(s.done)
	})
	select {
	case <-s.done:
		return closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *NativeSession) dispatchSend(ctx context.Context, command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	if s.deterministicMarketingContinuation {
		return s.dispatchDeterministicMarketingContinuation(ctx, command)
	}
	message, images, err := piPromptPayload(command.Send.Content)
	if err != nil {
		return agentcore.CommandReceipt{}, err
	}
	s.mu.Lock()
	if s.active {
		s.mu.Unlock()
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "pi already has an active turn", false)
	}
	generation := s.generation + 1
	s.generation = generation
	s.active = true
	s.cancelled = false
	s.terminated[generation] = false
	if id := strings.TrimSpace(command.Send.Metadata.ClientMsgID); id != "" {
		s.clientIDs[id] = generation
	}
	s.mapper.reset(generation)
	s.mu.Unlock()

	if err := writeDynamicSystemPrompt(s.opts, s.systemPrompt); err != nil {
		s.rollbackSend(generation, command.Send.Metadata.ClientMsgID)
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "pi system prompt could not be prepared", false)
	}
	debugOpts := s.opts
	debugOpts.Environment = mergePiEnvironment(s.runtime.Env, s.opts.Environment)
	if err := s.debug.WriteRuntimeInput(message, BuildArgs("", s.opts), debugOpts); err != nil {
		s.rollbackSend(generation, command.Send.Metadata.ClientMsgID)
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "pi runtime input debug artifact could not be written", false)
	}
	if s.bridge.Server != nil {
		s.bridge.Server.Update(ctx, s.runtime.ToolDispatcher, nativePiApprovalBroker{session: s}, s.opts)
	}
	fields := map[string]any{"message": message}
	if len(images) != 0 {
		fields["images"] = images
	}
	promptTimeout := s.native.PromptAckTimeout
	if configured := piRPCPromptAckTimeout(s.opts); configured != piRPCDefaultPromptAckTimeout {
		promptTimeout = configured
	}
	requestCtx, cancel := context.WithTimeout(ctx, promptTimeout)
	_, requestErr := s.requestWithPromptAcceptance(requestCtx, "prompt", fields, command.Send.Metadata.ClientMsgID, generation)
	cancel()
	if requestErr != nil {
		s.rollbackSend(generation, command.Send.Metadata.ClientMsgID)
		var rejected *piRPCRejectedError
		if !errors.As(requestErr, &rejected) {
			go func() { _ = s.Close(context.Background()) }()
		}
		if errors.Is(requestErr, context.DeadlineExceeded) {
			return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeHandshakeTimeout, "pi prompt acknowledgement timed out", true)
		}
		if errors.Is(requestErr, context.Canceled) {
			return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "pi prompt admission was cancelled before acknowledgement", true)
		}
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeBackendRejected, sanitizePiDiagnostic(requestErr.Error()), false)
	}
	return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionStarted, TurnGeneration: generation}, nil
}

func (s *NativeSession) dispatchCancel(ctx context.Context, _ agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	s.mu.Lock()
	if !s.active {
		receipt := agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn, TurnGeneration: s.generation}
		s.mu.Unlock()
		return receipt, nil
	}
	generation := s.generation
	s.cancelled = true
	s.mu.Unlock()
	requestCtx, cancel := context.WithTimeout(ctx, s.native.RequestTimeout)
	_, err := s.request(requestCtx, "abort", nil)
	cancel()
	if err != nil {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeBackendRejected, sanitizePiDiagnostic(err.Error()), true)
	}
	return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn, TurnGeneration: generation}, nil
}

func (s *NativeSession) dispatchSteer(ctx context.Context, command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	message, images, err := piPromptPayload(command.Steer.Content)
	if err != nil {
		return agentcore.CommandReceipt{}, err
	}
	s.mu.Lock()
	active := s.active
	generation := s.generation
	s.mu.Unlock()
	if !active {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "pi steer requires an active turn", false)
	}
	fields := map[string]any{"message": message}
	if len(images) != 0 {
		fields["images"] = images
	}
	requestCtx, cancel := context.WithTimeout(ctx, s.native.RequestTimeout)
	_, err = s.request(requestCtx, "steer", fields)
	cancel()
	if err != nil {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeBackendRejected, sanitizePiDiagnostic(err.Error()), false)
	}
	return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn, TurnGeneration: generation}, nil
}

func (s *NativeSession) dispatchAnswerPermission(command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	answer := command.AnswerPermission
	s.mu.Lock()
	approval, approvalOK := s.approvals[answer.RequestID]
	ui, uiOK := s.ui[answer.RequestID]
	if approvalOK && uiOK {
		s.mu.Unlock()
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "pi permission request ID is ambiguous", false)
	}
	if uiOK && ui.method == "select" && answer.Decision != agentcore.PermissionDenied {
		value := firstPiAnswerValue(*answer)
		if !piUIOptionAllowed(value, ui.options) {
			s.mu.Unlock()
			return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "pi extension selection is not one of the offered options", false)
		}
	}
	if !approvalOK && !uiOK {
		s.mu.Unlock()
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnknownPermissionRequest, "unknown pi permission request", false)
	}
	if approvalOK {
		decision := runtimecore.ApprovalDecisionDenied
		remember := false
		if answer.Decision == agentcore.PermissionApproved || answer.Decision == agentcore.PermissionAllowAlways {
			decision = runtimecore.ApprovalDecisionApproved
			remember = answer.Decision == agentcore.PermissionAllowAlways
		}
		if err := approval.responder(runtimecore.ApprovalResponse{Decision: decision, Remember: remember, Reason: answer.Selected}); err != nil {
			s.mu.Unlock()
			return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeBackendRejected, "pi approval response was rejected", false)
		}
		delete(s.approvals, answer.RequestID)
		close(approval.done)
	}
	if uiOK {
		frame := map[string]any{"type": "extension_ui_response", "id": ui.wireID}
		if answer.Decision == agentcore.PermissionDenied {
			frame["cancelled"] = true
		} else if ui.method == "confirm" {
			frame["confirmed"] = true
		} else {
			frame["value"] = firstPiAnswerValue(*answer)
		}
		if err := s.writeFrame(frame); err != nil {
			s.mu.Unlock()
			return agentcore.CommandReceipt{}, err
		}
		delete(s.ui, answer.RequestID)
	}
	generation := s.generation
	s.mu.Unlock()
	s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventPermissionResolved, PermissionResolved: &agentcore.PermissionResolvedEvent{RequestID: answer.RequestID, Kind: agentcore.PermissionKindTool}})
	return s.noTurnReceipt(), nil
}

func (s *NativeSession) dispatchSetModel(ctx context.Context, modelID string) (agentcore.CommandReceipt, error) {
	modelID = strings.TrimSpace(modelID)
	s.mu.Lock()
	model, exists := s.models[modelID]
	generation := s.generation
	s.mu.Unlock()
	if !exists {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "pi model is not in the discovered catalog", false)
	}
	requestCtx, cancel := context.WithTimeout(ctx, s.native.RequestTimeout)
	_, err := s.request(requestCtx, "set_model", map[string]any{"provider": model.Provider, "modelId": model.ID})
	cancel()
	if err != nil {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeBackendRejected, sanitizePiDiagnostic(err.Error()), false)
	}
	s.mu.Lock()
	s.capability.CurrentModel = modelID
	s.mu.Unlock()
	s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventConfigChanged, ConfigChanged: &agentcore.ConfigChangedEvent{Model: modelID}})
	return s.noTurnReceipt(), nil
}

func (s *NativeSession) dispatchSetEffort(ctx context.Context, optionID, value string) (agentcore.CommandReceipt, error) {
	if optionID != "reasoning_effort" && optionID != "effort" && optionID != "thought_level" {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, "set_config_option:"+optionID, false)
	}
	value = strings.TrimSpace(value)
	if !piEffortAllowed(value) {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "pi reasoning effort is not supported", false)
	}
	requestCtx, cancel := context.WithTimeout(ctx, s.native.RequestTimeout)
	_, err := s.request(requestCtx, "set_thinking_level", map[string]any{"level": value})
	cancel()
	if err != nil {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeBackendRejected, sanitizePiDiagnostic(err.Error()), false)
	}
	s.mu.Lock()
	s.capability.ConfigOptions[0].Current = value
	generation := s.generation
	s.mu.Unlock()
	s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventConfigChanged, ConfigChanged: &agentcore.ConfigChangedEvent{Options: map[string]string{"reasoning_effort": value}}})
	return s.noTurnReceipt(), nil
}

func (s *NativeSession) dispatchSessionInfo(ctx context.Context, kind agentcore.SessionInfoKind) (agentcore.CommandReceipt, error) {
	requestCtx, cancel := context.WithTimeout(ctx, s.native.RequestTimeout)
	response, err := s.request(requestCtx, "get_session_stats", nil)
	cancel()
	if err != nil {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeBackendRejected, sanitizePiDiagnostic(err.Error()), false)
	}
	var stats piNativeStats
	if err := json.Unmarshal(response.Data, &stats); err != nil {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "pi session statistics are invalid", false)
	}
	event := agentcore.SessionInfoEvent{}
	switch kind {
	case agentcore.SessionInfoContextUsage:
		if stats.ContextUsage == nil || stats.ContextUsage.Tokens == nil {
			return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeBackendRejected, "pi context usage is unavailable", false)
		}
		event.ContextUsage = &agentcore.ContextUsage{Used: *stats.ContextUsage.Tokens, Max: stats.ContextUsage.ContextWindow}
	case agentcore.SessionInfoSessionCost:
		event.CostText = fmt.Sprintf("%.6f USD", stats.Cost)
	default:
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "unknown pi session information query", false)
	}
	s.emit(s.currentGeneration(), agentcore.SessionEvent{Kind: agentcore.EventSessionInfo, SessionInfo: &event})
	return s.noTurnReceipt(), nil
}

func (s *NativeSession) noTurnReceipt() agentcore.CommandReceipt {
	return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn, TurnGeneration: s.currentGeneration()}
}

func (s *NativeSession) currentGeneration() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generation
}

func (s *NativeSession) rollbackSend(generation uint64, clientID string) {
	s.mu.Lock()
	if s.generation == generation && s.active {
		s.generation--
		s.active = false
		s.cancelled = false
		delete(s.terminated, generation)
	}
	delete(s.clientIDs, strings.TrimSpace(clientID))
	s.mapper.reset(s.generation)
	s.mu.Unlock()
}

func piPromptPayload(content []agentcore.ContentBlock) (string, []map[string]string, error) {
	var textParts []string
	var images []map[string]string
	for _, block := range content {
		switch block.Kind {
		case agentcore.ContentBlockText:
			if text := strings.TrimSpace(block.Text); text != "" {
				textParts = append(textParts, block.Text)
			}
		case agentcore.ContentBlockImage:
			images = append(images, map[string]string{"type": "image", "data": base64.StdEncoding.EncodeToString(block.Data), "mimeType": block.MediaType})
		default:
			return "", nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, "pi prompt block is unsupported", false)
		}
	}
	message := strings.TrimSpace(strings.Join(textParts, "\n\n"))
	if message == "" && len(images) == 0 {
		return "", nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "pi prompt content is empty", false)
	}
	return message, images, nil
}

func piEffortAllowed(value string) bool {
	switch value {
	case "off", "minimal", "low", "medium", "high", "xhigh":
		return true
	default:
		return false
	}
}

func firstPiAnswerValue(answer agentcore.AnswerPermissionCommand) string {
	if value := strings.TrimSpace(answer.Selected); value != "" {
		return value
	}
	for _, item := range answer.Answers {
		if len(item.Labels) != 0 {
			return strings.TrimSpace(item.Labels[0])
		}
	}
	return ""
}

func (s *NativeSession) emit(generation uint64, event agentcore.SessionEvent) {
	if err := event.Validate(); err != nil {
		event = agentcore.SessionEvent{Kind: agentcore.EventAdapterSpecific, AdapterSpecific: &agentcore.AdapterSpecificEvent{Tag: "pi_invalid_mapping", Summary: "provider event was rejected by canonical validation"}}
	}
	envelope := s.envelope(generation, event)
	s.eventMu.RLock()
	defer s.eventMu.RUnlock()
	if s.eventsClosed {
		return
	}
	select {
	case s.eventIn <- envelope:
	case <-s.eventAbort:
	case <-s.ctx.Done():
	}
}

func (s *NativeSession) envelope(generation uint64, event agentcore.SessionEvent) agentcore.SessionEnvelope {
	projection := agentcore.EventProjectionCanonical
	if event.Kind == agentcore.EventAdapterSpecific {
		projection = agentcore.EventProjectionDiagnostic
	}
	return agentcore.SessionEnvelope{SessionID: s.sessionID, TurnGeneration: generation, Timestamp: s.now().UTC(), Source: agentcore.EventSourceProvider, Projection: projection, Event: event}
}

func (s *NativeSession) closeEvents() {
	s.eventsOnce.Do(func() {
		s.eventMu.Lock()
		s.eventsClosed = true
		close(s.eventIn)
		s.eventMu.Unlock()
	})
}

func (s *NativeSession) runEventPump() {
	defer close(s.eventPumpDone)
	defer close(s.events)
	capacity := s.native.EventCapacity
	queue := make([]agentcore.SessionEnvelope, 0, capacity+2)
	input := s.eventIn
	var pending *agentcore.SessionEnvelope
	var blockedSince time.Time
	for input != nil || pending != nil || len(queue) != 0 {
		if pending != nil && len(queue) < capacity {
			queue = append(queue, *pending)
			pending = nil
			blockedSince = time.Time{}
		}
		var output chan agentcore.SessionEnvelope
		var next agentcore.SessionEnvelope
		if len(queue) != 0 {
			output, next = s.events, queue[0]
		}
		var receive <-chan agentcore.SessionEnvelope
		if pending == nil {
			receive = input
		}
		var timeout <-chan time.Time
		var timer *time.Timer
		if pending != nil {
			remaining := s.native.BackpressureWait - time.Since(blockedSince)
			if remaining <= 0 {
				remaining = time.Nanosecond
			}
			timer = time.NewTimer(remaining)
			timeout = timer.C
		}
		select {
		case envelope, ok := <-receive:
			if !ok {
				input = nil
				break
			}
			if len(queue) < capacity {
				queue = append(queue, envelope)
			} else if !coalescePiDelta(queue, envelope) {
				pending = &envelope
				blockedSince = time.Now()
			}
		case output <- next:
			queue[0] = agentcore.SessionEnvelope{}
			queue = queue[1:]
		case <-timeout:
			queue = append(queue, s.backpressureEvents()...)
			pending, input = nil, nil
			s.stopForBackpressure()
		case <-s.ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

func coalescePiDelta(queue []agentcore.SessionEnvelope, incoming agentcore.SessionEnvelope) bool {
	for index := len(queue) - 1; index >= 0; index-- {
		candidate := &queue[index]
		if candidate.TurnGeneration != incoming.TurnGeneration || candidate.Event.Kind != incoming.Event.Kind {
			continue
		}
		switch incoming.Event.Kind {
		case agentcore.EventMessageDelta:
			if candidate.Event.MessageDelta.ItemID == incoming.Event.MessageDelta.ItemID && len(candidate.Event.MessageDelta.Text)+len(incoming.Event.MessageDelta.Text) <= 16<<20 {
				candidate.Event.MessageDelta.Text += incoming.Event.MessageDelta.Text
				return true
			}
		case agentcore.EventThoughtDelta:
			if candidate.Event.ThoughtDelta.ItemID == incoming.Event.ThoughtDelta.ItemID && len(candidate.Event.ThoughtDelta.Text)+len(incoming.Event.ThoughtDelta.Text) <= 16<<20 {
				candidate.Event.ThoughtDelta.Text += incoming.Event.ThoughtDelta.Text
				return true
			}
		case agentcore.EventToolOutputDelta:
			if candidate.Event.ToolOutputDelta.ItemID == incoming.Event.ToolOutputDelta.ItemID && len(candidate.Event.ToolOutputDelta.Text)+len(incoming.Event.ToolOutputDelta.Text) <= 16<<20 {
				candidate.Event.ToolOutputDelta.Text += incoming.Event.ToolOutputDelta.Text
				return true
			}
		}
		return false
	}
	return false
}

func (s *NativeSession) backpressureEvents() []agentcore.SessionEnvelope {
	s.dead.Store(true)
	s.mu.Lock()
	generation, active := s.generation, s.active && !s.terminated[s.generation]
	if active {
		s.active = false
		s.terminated[generation] = true
	}
	s.mu.Unlock()
	events := make([]agentcore.SessionEnvelope, 0, 2)
	if active {
		events = append(events, s.envelope(generation, piFailedTurn(generation, "pi event backpressure limit exceeded")))
	}
	events = append(events, s.envelope(generation, agentcore.SessionEvent{Kind: agentcore.EventDetached, Detached: &agentcore.DetachedEvent{RedactedSummary: "pi event backpressure limit exceeded"}}))
	return events
}

func (s *NativeSession) stopForBackpressure() {
	s.pressureOnce.Do(func() {
		close(s.eventAbort)
		s.failPending(agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeQueueFull, "pi event queue is full", true))
		if s.binding != nil {
			go func() { _ = s.binding.Stop(context.Background()) }()
		}
	})
}

func (s *NativeSession) failPending(err error) {
	s.mu.Lock()
	pending := s.pending
	s.pending = make(map[string]piPendingRequest)
	s.mu.Unlock()
	for _, request := range pending {
		select {
		case request.response <- piNativeResponse{err: err}:
		default:
		}
	}
}

func (s *NativeSession) failApprovals(reason string) {
	s.mu.Lock()
	approvals := s.approvals
	s.approvals = make(map[string]piNativeApproval)
	s.ui = make(map[string]piNativeUIRequest)
	s.mu.Unlock()
	for _, pending := range approvals {
		close(pending.done)
		_ = pending.responder(runtimecore.ApprovalResponse{Decision: runtimecore.ApprovalDecisionDenied, Reason: reason})
	}
}

func piUIOptionAllowed(value string, options []string) bool {
	value = strings.TrimSpace(value)
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}

func piFailedTurn(generation uint64, message string) agentcore.SessionEvent {
	return agentcore.SessionEvent{Kind: agentcore.EventTurnResult, TurnResult: &agentcore.TurnResultEvent{Epoch: generation, IsError: true, ResultText: sanitizePiText(message), Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeFailed}}}
}

var _ agentcore.SessionBackend = (*NativeSession)(nil)
