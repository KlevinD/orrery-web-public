package acp

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
)

type Session struct {
	sessionID agentcore.SessionID
	spec      agentcore.SessionSpec
	config    agentcore.SessionConfig
	workspace Workspace
	native    Config
	now       func() time.Time

	ctx    context.Context
	cancel context.CancelFunc
	events chan agentcore.SessionEnvelope
	done   chan struct{}

	closeOnce   sync.Once
	eventsOnce  sync.Once
	dead        atomic.Bool
	sequence    atomic.Uint64
	nextID      atomic.Uint64
	dispatchMu  sync.Mutex
	wireOrderMu sync.Mutex

	mu            sync.Mutex
	binding       *processBinding
	backendID     string
	generation    uint64
	active        bool
	cancelled     bool
	terminated    map[uint64]bool
	pending       map[string]*pendingRPC
	permissions   map[string]pendingPermission
	capabilities  agentcore.Capabilities
	messageSerial uint64
}

type pendingRPC struct {
	method          string
	response        chan rpcResponse
	prompt          bool
	generation      uint64
	clientMessageID string
}

type rpcResponse struct {
	result json.RawMessage
	err    error
}

type pendingPermission struct {
	wireID  json.RawMessage
	options []permissionOption
	tool    string
}

type permissionOption struct {
	ID   string
	Kind string
}

func openSession(parent context.Context, native Config, spec agentcore.SessionSpec, config agentcore.SessionConfig, workspace Workspace) (*Session, error) {
	ctx, cancel := context.WithCancel(context.Background())
	session := &Session{
		sessionID: spec.SessionID, spec: spec, config: config, workspace: workspace, native: native, now: native.Now,
		ctx: ctx, cancel: cancel, events: make(chan agentcore.SessionEnvelope, native.EventCapacity), done: make(chan struct{}),
		terminated: make(map[uint64]bool), pending: make(map[string]*pendingRPC), permissions: make(map[string]pendingPermission),
		capabilities: baseCapabilities(workspace.Version),
	}
	session.capabilities.PayloadLimitBytes = uint64(native.MaxRecordBytes)
	binding, err := startProcess(session)
	if err != nil {
		cancel()
		return nil, err
	}
	session.mu.Lock()
	session.binding = binding
	session.mu.Unlock()
	handshakeCtx, handshakeCancel := context.WithTimeout(parent, native.HandshakeTimeout)
	err = session.handshake(handshakeCtx)
	handshakeCancel()
	if err != nil {
		_ = binding.Stop(context.Background())
		cancel()
		session.closeEvents()
		return nil, err
	}
	return session, nil
}

func (s *Session) Dispatch(ctx context.Context, command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
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
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "ACP process is detached", false)
	}
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	switch command.Kind {
	case agentcore.RuntimeCommandSend:
		return s.dispatchSend(command)
	case agentcore.RuntimeCommandCancel:
		return s.dispatchCancel()
	case agentcore.RuntimeCommandAnswerPermission:
		return s.dispatchPermission(command)
	case agentcore.RuntimeCommandSetMode:
		return s.dispatchConfig(ctx, "session/set_mode", map[string]any{"sessionId": s.boundSession(), "modeId": command.SetMode.Mode}, agentcore.ConfigChangedEvent{Mode: command.SetMode.Mode})
	case agentcore.RuntimeCommandSetModel:
		return s.dispatchConfig(ctx, "session/set_model", map[string]any{"sessionId": s.boundSession(), "modelId": command.SetModel.Model}, agentcore.ConfigChangedEvent{Model: command.SetModel.Model})
	case agentcore.RuntimeCommandSetConfigOption:
		return s.dispatchConfig(ctx, "session/set_config_option", map[string]any{"sessionId": s.boundSession(), "configId": command.SetConfigOption.OptionID, "value": command.SetConfigOption.Value}, agentcore.ConfigChangedEvent{Options: map[string]string{command.SetConfigOption.OptionID: command.SetConfigOption.Value}})
	default:
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, string(command.Kind), false)
	}
}

func (s *Session) Events() <-chan agentcore.SessionEnvelope { return s.events }

func (s *Session) Capabilities() agentcore.Capabilities {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.capabilities.Clone()
}

func (s *Session) PendingPermissionRequests() []agentcore.PendingPermissionView {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]agentcore.PendingPermissionView, 0, len(s.permissions))
	for id, pending := range s.permissions {
		out = append(out, agentcore.PendingPermissionView{RequestID: id, ToolName: pending.tool})
	}
	return out
}

func (s *Session) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var closeErr error
	s.closeOnce.Do(func() {
		s.dead.Store(true)
		s.mu.Lock()
		binding := s.binding
		s.mu.Unlock()
		if binding != nil {
			closeErr = binding.Stop(ctx)
		}
		s.cancel()
		s.failPending(agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "ACP session closed", false))
		s.closeEvents()
		close(s.done)
	})
	select {
	case <-s.done:
		return closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Session) dispatchSend(command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	prompt, err := promptBlocks(command.Send.Content)
	if err != nil {
		return agentcore.CommandReceipt{}, err
	}
	s.mu.Lock()
	if s.active {
		s.mu.Unlock()
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "ACP session already has an active turn", false)
	}
	generation := s.generation + 1
	s.generation = generation
	s.active = true
	s.cancelled = false
	s.terminated[generation] = false
	s.mu.Unlock()

	id, err := s.sendRequest("session/prompt", map[string]any{"sessionId": s.boundSession(), "prompt": prompt}, &pendingRPC{
		method: "session/prompt", prompt: true, generation: generation, clientMessageID: strings.TrimSpace(command.Send.Metadata.ClientMsgID),
	})
	if err != nil {
		s.rollbackTurn(generation)
		return agentcore.CommandReceipt{}, err
	}
	_ = id
	if clientID := strings.TrimSpace(command.Send.Metadata.ClientMsgID); clientID != "" {
		s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventPromptAccepted, PromptAccepted: &agentcore.PromptAcceptedEvent{ClientMsgID: clientID}})
	}
	return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionStarted, TurnGeneration: generation}, nil
}

func (s *Session) dispatchCancel() (agentcore.CommandReceipt, error) {
	s.mu.Lock()
	generation, active := s.generation, s.active
	if active {
		s.cancelled = true
	}
	s.mu.Unlock()
	if !active {
		return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn, TurnGeneration: generation}, nil
	}
	if err := s.writeFrame(map[string]any{"jsonrpc": "2.0", "method": "session/cancel", "params": map[string]any{"sessionId": s.boundSession()}}); err != nil {
		return agentcore.CommandReceipt{}, err
	}
	return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn, TurnGeneration: generation}, nil
}

func (s *Session) dispatchPermission(command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	answer := command.AnswerPermission
	s.mu.Lock()
	pending, ok := s.permissions[answer.RequestID]
	if !ok {
		s.mu.Unlock()
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnknownPermissionRequest, "unknown ACP permission request", false)
	}
	optionID, selected := selectPermissionOption(pending.options, answer.Decision, answer.Selected)
	if !selected && answer.Decision != agentcore.PermissionDenied {
		s.mu.Unlock()
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "ACP agent did not offer a compatible permission option", false)
	}
	delete(s.permissions, answer.RequestID)
	generation := s.generation
	s.mu.Unlock()

	result := map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}
	if selected {
		result = map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": optionID}}
	}
	s.wireOrderMu.Lock()
	defer s.wireOrderMu.Unlock()
	if err := s.writeRPCResult(pending.wireID, result); err != nil {
		s.mu.Lock()
		s.permissions[answer.RequestID] = pending
		s.mu.Unlock()
		return agentcore.CommandReceipt{}, err
	}
	s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventPermissionResolved, PermissionResolved: &agentcore.PermissionResolvedEvent{RequestID: answer.RequestID, Kind: agentcore.PermissionKindTool}})
	return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn, TurnGeneration: generation}, nil
}

func (s *Session) dispatchConfig(ctx context.Context, method string, params map[string]any, changed agentcore.ConfigChangedEvent) (agentcore.CommandReceipt, error) {
	requestCtx, cancel := context.WithTimeout(ctx, s.native.RequestTimeout)
	defer cancel()
	if _, err := s.call(requestCtx, method, params); err != nil {
		return agentcore.CommandReceipt{}, err
	}
	s.mu.Lock()
	if changed.Model != "" {
		s.capabilities.CurrentModel = changed.Model
	}
	if changed.Mode != "" {
		s.capabilities.CurrentMode = changed.Mode
	}
	for key, value := range changed.Options {
		for index := range s.capabilities.ConfigOptions {
			if s.capabilities.ConfigOptions[index].ID == key {
				s.capabilities.ConfigOptions[index].Current = value
			}
		}
	}
	generation := s.generation
	s.mu.Unlock()
	s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventConfigChanged, ConfigChanged: &changed})
	return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn, TurnGeneration: generation}, nil
}

func promptBlocks(blocks []agentcore.ContentBlock) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(blocks))
	for _, block := range blocks {
		switch block.Kind {
		case agentcore.ContentBlockText:
			out = append(out, map[string]any{"type": "text", "text": block.Text})
		case agentcore.ContentBlockImage:
			out = append(out, map[string]any{"type": "image", "data": base64.StdEncoding.EncodeToString(block.Data), "mimeType": block.MediaType})
		case agentcore.ContentBlockAudio:
			out = append(out, map[string]any{"type": "audio", "data": base64.StdEncoding.EncodeToString(block.Data), "mimeType": block.MediaType})
		case agentcore.ContentBlockResourceLink:
			out = append(out, map[string]any{"type": "resource_link", "uri": block.URI, "mimeType": block.MediaType})
		default:
			return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, "ACP prompt content type is unsupported", false)
		}
	}
	return out, nil
}

func selectPermissionOption(options []permissionOption, decision agentcore.PermissionDecision, requested string) (string, bool) {
	requested = strings.TrimSpace(requested)
	if requested != "" {
		for _, option := range options {
			if option.ID == requested {
				return option.ID, true
			}
		}
		return "", false
	}
	wanted := []string{"allow_once"}
	if decision == agentcore.PermissionAllowAlways {
		wanted = []string{"allow_always", "allow_once"}
	} else if decision == agentcore.PermissionDenied {
		wanted = []string{"reject_once", "reject_always"}
	}
	for _, kind := range wanted {
		for _, option := range options {
			if option.Kind == kind {
				return option.ID, true
			}
		}
	}
	return "", false
}

func (s *Session) envelope(generation uint64, event agentcore.SessionEvent) agentcore.SessionEnvelope {
	return agentcore.SessionEnvelope{SessionID: s.sessionID, TurnGeneration: generation, Sequence: s.sequence.Add(1), Timestamp: s.now().UTC(), Source: agentcore.EventSourceProvider, Projection: agentcore.EventProjectionCanonical, Event: event}
}

func (s *Session) emit(generation uint64, event agentcore.SessionEvent) {
	if err := event.Validate(); err != nil {
		event = agentcore.SessionEvent{Kind: agentcore.EventAdapterSpecific, AdapterSpecific: &agentcore.AdapterSpecificEvent{Tag: "acp_invalid_update", Summary: "ACP update was rejected by canonical validation"}}
	}
	envelope := s.envelope(generation, event)
	select {
	case s.events <- envelope:
	case <-s.ctx.Done():
	default:
		s.dead.Store(true)
		go func() { _ = s.Close(context.Background()) }()
	}
}

func (s *Session) closeEvents() { s.eventsOnce.Do(func() { close(s.events) }) }

func (s *Session) boundSession() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.backendID
}

func (s *Session) currentGeneration() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generation
}

func (s *Session) rollbackTurn(generation uint64) {
	s.mu.Lock()
	if s.generation == generation && s.active {
		s.active = false
		delete(s.terminated, generation)
	}
	s.mu.Unlock()
}

func (s *Session) completeTurn(generation uint64, result agentcore.TurnResultEvent) {
	s.mu.Lock()
	if generation == 0 || generation != s.generation || !s.active || s.terminated[generation] {
		s.mu.Unlock()
		return
	}
	s.terminated[generation] = true
	s.active = false
	s.cancelled = false
	s.mu.Unlock()
	result.Epoch = generation
	s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventTurnResult, TurnResult: &result})
}

func (s *Session) failPending(err error) {
	s.mu.Lock()
	pending := s.pending
	s.pending = make(map[string]*pendingRPC)
	s.mu.Unlock()
	for _, request := range pending {
		select {
		case request.response <- rpcResponse{err: err}:
		default:
		}
	}
}

func bridgeError(err error, fallback string) error {
	if err == nil {
		return nil
	}
	var bridge *agentcore.RuntimeBridgeError
	if errors.As(err, &bridge) {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeHandshakeTimeout, fallback+" timed out", true)
	}
	return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, fmt.Sprintf("%s failed", fallback), false)
}

var _ agentcore.SessionBackend = (*Session)(nil)
