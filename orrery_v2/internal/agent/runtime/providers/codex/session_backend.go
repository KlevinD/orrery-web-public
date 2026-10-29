// Historical source: AionCore aionui-session, Copyright 2025 AionUi
// (aionui.com), Apache-2.0. Go adaptation and modifications: Minvia, 2026.
// See docs/runtime/runtime-source-provenance.md for scope and the full license.
package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/process"
	"orrery_v2/internal/agent/runtime/providers/toolbridge"
	runtimesession "orrery_v2/internal/agent/runtime/session"
)

// Reference: aionui-session/backend/codex_conn.rs@v0.1.52.

type NativeSession struct {
	sessionID agentcore.SessionID
	spec      agentcore.SessionSpec
	config    agentcore.SessionConfig
	workspace NativeWorkspace
	native    NativeConfig
	version   string
	now       func() time.Time

	ctx           context.Context
	cancel        context.CancelFunc
	events        chan agentcore.SessionEnvelope
	eventIn       chan agentcore.SessionEnvelope
	eventAbort    chan struct{}
	eventPumpDone chan struct{}
	done          chan struct{}

	closeOnce        sync.Once
	eventsCloseOnce  sync.Once
	backpressureOnce sync.Once
	eventMu          sync.RWMutex
	eventsClosed     bool
	timerWG          sync.WaitGroup
	dead             atomic.Bool

	bindingMu sync.RWMutex
	binding   *nativeProcessBinding
	suspend   *runtimesession.SuspendController

	dispatchMu sync.Mutex
	writeMu    sync.Mutex
	nextID     atomic.Uint64

	mu                 sync.Mutex
	threadID           string
	activeTurnID       string
	turnGeneration     uint64
	turnInFlight       bool
	turnGenerations    map[string]uint64
	terminated         map[uint64]bool
	idlePending        map[uint64]bool
	pendingRPC         map[string]*nativePendingRPC
	pendingPermissions map[string]nativePendingPermission
	pendingAuth        map[string]nativePendingAuth
	resolvedRequests   map[string]agentcore.PermissionKind
	capabilities       agentcore.Capabilities
	requestedModel     string
	requestedMode      string
	messageText        map[string]string
	messageGeneration  uint64
	fileChangePaths    map[string][]string
}

type nativePendingPermission struct {
	requestID string
	wireID    json.RawMessage
	payload   json.RawMessage
	toolName  string
	eliciting bool
	questions bool
	answerIDs map[string]string
	view      []agentcore.PermissionQuestion
	filePaths []string
}

type nativePendingAuth struct {
	requestID string
	wireID    json.RawMessage
}

func openNativeSession(parent context.Context, native NativeConfig, version string, spec agentcore.SessionSpec, config agentcore.SessionConfig, workspace NativeWorkspace) (*NativeSession, error) {
	ctx, cancel := context.WithCancel(context.Background())
	backend := &NativeSession{
		sessionID:          spec.SessionID,
		spec:               spec,
		config:             config,
		workspace:          cloneNativeWorkspace(workspace),
		native:             native,
		version:            version,
		now:                native.Now,
		ctx:                ctx,
		cancel:             cancel,
		events:             make(chan agentcore.SessionEnvelope, native.EventCapacity+2),
		eventIn:            make(chan agentcore.SessionEnvelope, native.EventCapacity),
		eventAbort:         make(chan struct{}),
		eventPumpDone:      make(chan struct{}),
		done:               make(chan struct{}),
		turnGenerations:    make(map[string]uint64),
		terminated:         make(map[uint64]bool),
		idlePending:        make(map[uint64]bool),
		pendingRPC:         make(map[string]*nativePendingRPC),
		pendingPermissions: make(map[string]nativePendingPermission),
		pendingAuth:        make(map[string]nativePendingAuth),
		resolvedRequests:   make(map[string]agentcore.PermissionKind),
		capabilities:       nativeCodexCapabilities(version),
		requestedModel:     strings.TrimSpace(config.Model),
		requestedMode:      strings.TrimSpace(config.Mode),
	}
	if err := backend.configureDesktopMCP(); err != nil {
		cancel()
		return nil, err
	}
	go backend.runEventPump()
	backend.capabilities.CurrentModel = strings.TrimSpace(config.Model)
	backend.capabilities.CurrentMode = nativeModeCatalogValue(config.Mode)

	binding, err := backend.startProcess()
	if err != nil {
		cancel()
		return nil, err
	}
	backend.setBinding(binding)
	handshakeCtx, handshakeCancel := context.WithTimeout(parent, native.HandshakeTimeout)
	err = backend.runHandshake(handshakeCtx, resumeThreadID(spec))
	handshakeCancel()
	if err != nil {
		_ = binding.Stop(context.Background())
		cancel()
		backend.closeEvents()
		<-backend.eventPumpDone
		return nil, err
	}
	controller, err := runtimesession.NewSuspendController(binding, config.IdleTTL, backend.now())
	if err != nil {
		_ = binding.Stop(context.Background())
		cancel()
		backend.closeEvents()
		<-backend.eventPumpDone
		return nil, err
	}
	backend.suspend = controller
	if config.IdleTTL > 0 {
		backend.timerWG.Add(1)
		go func() {
			defer backend.timerWG.Done()
			controller.RunIdleTimer(backend.ctx, 0, backend.now, backend.turnActive, func() {
				backend.emit(backend.currentGeneration(), agentcore.SessionEvent{Kind: agentcore.EventBackendSuspended, BackendSuspended: &agentcore.BackendSuspendedEvent{}})
			})
		}()
	}
	return backend, nil
}

func (s *NativeSession) configureDesktopMCP() error {
	bridge, err := toolbridge.StartDesktopMCP(s.ctx, s.native.ToolDispatcher, s.workspace.Request)
	if err != nil || bridge == nil {
		return err
	}
	const name = "orrery_desktop"
	if _, exists := s.workspace.MCPServers[name]; exists {
		bridge.Close()
		return fmt.Errorf("reserved desktop MCP name is already bound")
	}
	if s.workspace.MCPServers == nil {
		s.workspace.MCPServers = map[string]NativeMCPServer{}
	}
	s.workspace.MCPServers[name] = NativeMCPServer{URL: bridge.URL, HTTPHeaders: map[string]string{"Authorization": bridge.Authorization}}
	s.config.MCPServers = append(s.config.MCPServers, agentcore.MCPServerSpec{Name: name, Transport: agentcore.MCPTransportHTTP, Endpoint: bridge.URL, HeaderRefs: map[string]string{"Authorization": "runtime-owned"}})
	return nil
}

func (s *NativeSession) Dispatch(ctx context.Context, command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := command.Validate(); err != nil {
		return agentcore.CommandReceipt{}, err
	}
	capabilities := s.Capabilities()
	if !capabilities.Supports(command) {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, string(command.Kind), false)
	}
	if s.dead.Load() {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "codex app-server is detached", false)
	}
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if err := s.ensureAwake(ctx); err != nil {
		return agentcore.CommandReceipt{}, err
	}

	switch command.Kind {
	case agentcore.RuntimeCommandSend:
		return s.dispatchSend(command)
	case agentcore.RuntimeCommandCancel:
		return s.dispatchCancel(command)
	case agentcore.RuntimeCommandSteer:
		return s.dispatchSteer(command)
	case agentcore.RuntimeCommandAnswerPermission:
		return s.dispatchAnswerPermission(command)
	case agentcore.RuntimeCommandAnswerAuth:
		return s.dispatchAnswerAuth(command)
	case agentcore.RuntimeCommandAcknowledge:
		return s.noTurnReceipt(), nil
	case agentcore.RuntimeCommandSetMode:
		return s.dispatchSetting("mode", nativeModeProfileID(command.SetMode.Mode))
	case agentcore.RuntimeCommandSetModel:
		return s.dispatchSetting("model", command.SetModel.Model)
	case agentcore.RuntimeCommandSetConfigOption:
		if command.SetConfigOption.OptionID != "effort" && command.SetConfigOption.OptionID != "reasoning_effort" && command.SetConfigOption.OptionID != "thought_level" {
			return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, "set_config_option:"+command.SetConfigOption.OptionID, false)
		}
		return s.dispatchSetting("effort", command.SetConfigOption.Value)
	case agentcore.RuntimeCommandRewind:
		return s.dispatchRewind(command)
	case agentcore.RuntimeCommandListCheckpoints:
		return s.dispatchCheckpoints()
	default:
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, string(command.Kind), false)
	}
}

func (s *NativeSession) Events() <-chan agentcore.SessionEnvelope { return s.events }

func (s *NativeSession) Capabilities() agentcore.Capabilities {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.capabilities.Clone()
}

func (s *NativeSession) PendingPermissionRequests() []agentcore.PendingPermissionView {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]agentcore.PendingPermissionView, 0, len(s.pendingPermissions)+len(s.pendingAuth))
	for _, pending := range s.pendingPermissions {
		out = append(out, agentcore.PendingPermissionView{RequestID: pending.requestID, ToolName: pending.toolName, Questions: cloneNativeQuestions(pending.view)})
	}
	for _, pending := range s.pendingAuth {
		out = append(out, agentcore.PendingPermissionView{RequestID: pending.requestID, ToolName: "codex_auth_refresh"})
	}
	return out
}

func (s *NativeSession) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var closeErr error
	s.closeOnce.Do(func() {
		if s.suspend != nil {
			closeErr = s.suspend.Close(ctx)
		} else if binding := s.currentBinding(); binding != nil {
			closeErr = binding.Stop(ctx)
		}
		s.cancel()
		s.timerWG.Wait()
		s.failPending(agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "codex session closed", false))
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

func (s *NativeSession) ensureAwake(ctx context.Context) error {
	if s.suspend == nil {
		return nil
	}
	return s.suspend.EnsureAwake(ctx, s.now(), func(wakeCtx context.Context) (runtimesession.SuspendBinding, error) {
		binding, err := s.startProcess()
		if err != nil {
			return nil, err
		}
		s.setBinding(binding)
		threadID := s.boundThread()
		handshakeCtx, cancel := context.WithTimeout(wakeCtx, s.native.HandshakeTimeout)
		err = s.runHandshake(handshakeCtx, threadID)
		cancel()
		if err != nil {
			_ = binding.Stop(context.Background())
			return nil, err
		}
		return binding, nil
	})
}

func (s *NativeSession) turnActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.turnInFlight
}

func (s *NativeSession) currentGeneration() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.turnGeneration
}

func (s *NativeSession) boundThread() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.threadID
}

func (s *NativeSession) noTurnReceipt() agentcore.CommandReceipt {
	return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn, TurnGeneration: s.currentGeneration()}
}

func (s *NativeSession) emit(generation uint64, event agentcore.SessionEvent) {
	if err := event.Validate(); err != nil {
		event = agentcore.SessionEvent{Kind: agentcore.EventAdapterSpecific, AdapterSpecific: &agentcore.AdapterSpecificEvent{Tag: "codex_invalid_mapping", Summary: "provider event was rejected by canonical validation"}}
	}
	envelope := agentcore.SessionEnvelope{
		SessionID: s.sessionID, TurnGeneration: generation, Timestamp: s.now().UTC(),
		Source: agentcore.EventSourceProvider, Projection: agentcore.EventProjectionCanonical, Event: event,
	}
	if event.Kind == agentcore.EventAdapterSpecific {
		envelope.Projection = agentcore.EventProjectionDiagnostic
	}
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

func (s *NativeSession) closeEvents() {
	s.eventsCloseOnce.Do(func() {
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
	if capacity < 1 {
		capacity = cap(s.eventIn)
	}
	if capacity < 1 {
		capacity = defaultNativeEventCapacity
	}
	backpressureWait := s.native.BackpressureWait
	if backpressureWait <= 0 {
		backpressureWait = defaultNativeBackpressureWait
	}
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
			output = s.events
			next = queue[0]
		}
		var receive <-chan agentcore.SessionEnvelope
		if pending == nil {
			receive = input
		}
		var timeout <-chan time.Time
		var timer *time.Timer
		if pending != nil {
			remaining := backpressureWait - time.Since(blockedSince)
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
			} else if !coalesceNativeDelta(queue, envelope) {
				pending = &envelope
				blockedSince = time.Now()
			}
		case output <- next:
			queue[0] = agentcore.SessionEnvelope{}
			queue = queue[1:]
		case <-timeout:
			queue = append(queue, s.nativeBackpressureEvents()...)
			pending = nil
			input = nil
			s.stopAfterBackpressure()
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

func coalesceNativeDelta(queue []agentcore.SessionEnvelope, incoming agentcore.SessionEnvelope) bool {
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

func (s *NativeSession) nativeBackpressureEvents() []agentcore.SessionEnvelope {
	s.dead.Store(true)
	s.mu.Lock()
	generation := s.turnGeneration
	active := generation != 0 && s.turnInFlight && !s.terminated[generation]
	if active {
		s.terminated[generation] = true
		delete(s.idlePending, generation)
		s.turnInFlight = false
		s.activeTurnID = ""
	}
	s.mu.Unlock()
	events := make([]agentcore.SessionEnvelope, 0, 2)
	if active {
		events = append(events, s.nativeEnvelope(generation, failedTurnResult(generation, "codex event backpressure limit exceeded", nil)))
	}
	events = append(events, s.nativeEnvelope(generation, agentcore.SessionEvent{Kind: agentcore.EventDetached, Detached: &agentcore.DetachedEvent{RedactedSummary: "codex event backpressure limit exceeded"}}))
	return events
}

func (s *NativeSession) stopAfterBackpressure() {
	s.backpressureOnce.Do(func() {
		if s.eventAbort != nil {
			close(s.eventAbort)
		}
		s.failPending(agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeQueueFull, "codex event queue is full", true))
		if binding := s.currentBinding(); binding != nil {
			go func() { _ = binding.Stop(context.Background()) }()
		}
	})
}

func (s *NativeSession) nativeEnvelope(generation uint64, event agentcore.SessionEvent) agentcore.SessionEnvelope {
	projection := agentcore.EventProjectionCanonical
	if event.Kind == agentcore.EventAdapterSpecific {
		projection = agentcore.EventProjectionDiagnostic
	}
	return agentcore.SessionEnvelope{
		SessionID: s.sessionID, TurnGeneration: generation, Timestamp: s.now().UTC(),
		Source: agentcore.EventSourceProvider, Projection: projection, Event: event,
	}
}

func (s *NativeSession) setBinding(binding *nativeProcessBinding) {
	s.bindingMu.Lock()
	s.binding = binding
	s.bindingMu.Unlock()
}

func (s *NativeSession) currentBinding() *nativeProcessBinding {
	s.bindingMu.RLock()
	defer s.bindingMu.RUnlock()
	return s.binding
}

func (s *NativeSession) startProcess() (*nativeProcessBinding, error) {
	executable := s.native.Executable
	if _, err := exec.LookPath(executable); err != nil {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "codex executable was not found", false)
	}
	processCtx, cancel := context.WithCancel(s.ctx)
	args := []string{"app-server", "--listen", "stdio://"}
	args = append(args, nativeCodexArgs(s.native.ExtraArgs)...)
	cmd := exec.CommandContext(processCtx, executable, args...)
	process.ConfigureProcessTree(cmd)
	cmd.Dir = s.workspace.Directory
	cmd.Env = process.BuildEnv(mergeNativeEnvironment(s.native.Environment, s.workspace.Environment))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "codex stdout pipe failed", true)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "codex stdin pipe failed", true)
	}
	stderr := process.NewStderrTail(nil, 4096)
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "codex app-server failed to start", true)
	}
	if err := process.AttachProcessTree(cmd); err != nil {
		_ = process.TerminateProcessTree(cmd)
		cancel()
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "codex process tree could not be attached", true)
	}
	binding := &nativeProcessBinding{session: s, cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr, cancel: cancel, done: make(chan struct{})}
	go binding.readLoop()
	return binding, nil
}

type nativeProcessBinding struct {
	session     *NativeSession
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	stdout      io.ReadCloser
	stderr      interface{ Tail() string }
	cancel      context.CancelFunc
	done        chan struct{}
	stopOnce    sync.Once
	intentional atomic.Bool
}

func (b *nativeProcessBinding) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	b.stopOnce.Do(func() {
		b.intentional.Store(true)
		_ = b.stdin.Close()
	})
	grace := time.NewTimer(time.Second)
	defer grace.Stop()
	select {
	case <-b.done:
		b.cancel()
		return nil
	case <-ctx.Done():
		_ = process.TerminateProcessTree(b.cmd)
		b.cancel()
		return ctx.Err()
	case <-grace.C:
		_ = process.TerminateProcessTree(b.cmd)
		b.cancel()
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	select {
	case <-b.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-deadline.C:
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "codex process did not stop after termination", true)
	}
}

func (b *nativeProcessBinding) readLoop() {
	defer close(b.done)
	scanner := bufio.NewScanner(b.stdout)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		if err := b.session.handleNativeLine(line); err != nil {
			b.session.handleProtocolError(err)
		}
	}
	scanErr := scanner.Err()
	waitErr := b.cmd.Wait()
	process.ReleaseProcessTree(b.cmd)
	b.session.processExited(b, waitErr, scanErr)
}

func (s *NativeSession) processExited(binding *nativeProcessBinding, waitErr, scanErr error) {
	if binding.intentional.Load() || s.ctx.Err() != nil {
		return
	}
	s.dead.Store(true)
	s.flushPendingIdleTerminal()
	s.failPending(agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "codex app-server exited", true))
	exit := &agentcore.ExitStatus{}
	if waitErr == nil {
		zero := 0
		exit.Code = &zero
	} else {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			code := exitErr.ExitCode()
			exit.Code = &code
		}
	}
	summary := "codex app-server exited"
	if scanErr != nil {
		summary = "codex app-server output stream failed"
	}
	s.emit(s.currentGeneration(), agentcore.SessionEvent{Kind: agentcore.EventDetached, Detached: &agentcore.DetachedEvent{Exit: exit, RedactedSummary: summary}})
	s.closeEvents()
}

func (s *NativeSession) handleProtocolError(_ error) {
	s.mu.Lock()
	active := s.turnInFlight
	generation := s.turnGeneration
	if active && !s.terminated[generation] {
		s.terminated[generation] = true
		s.turnInFlight = false
		s.activeTurnID = ""
	}
	s.mu.Unlock()
	if active {
		s.emit(generation, failedTurnResult(generation, "codex protocol error", nil))
		return
	}
	s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventAdapterSpecific, AdapterSpecific: &agentcore.AdapterSpecificEvent{Tag: "codex_protocol_error", Summary: "invalid app-server frame"}})
}

func (s *NativeSession) failPending(err error) {
	s.mu.Lock()
	pending := s.pendingRPC
	s.pendingRPC = make(map[string]*nativePendingRPC)
	s.mu.Unlock()
	for _, call := range pending {
		if call.response != nil {
			select {
			case call.response <- nativeRPCResponse{err: err}:
			default:
			}
			close(call.response)
		}
	}
}

func resumeThreadID(spec agentcore.SessionSpec) string {
	if spec.Mode == agentcore.SessionOpenResume {
		return strings.TrimSpace(spec.BackendSessionID)
	}
	return ""
}

func cloneNativeWorkspace(source NativeWorkspace) NativeWorkspace {
	out := source
	out.WritableRoots = append([]string(nil), source.WritableRoots...)
	out.Environment = mergeNativeEnvironment(nil, source.Environment)
	out.Launch.ExtraArgs = append([]string(nil), source.Launch.ExtraArgs...)
	out.MCPServers = make(map[string]NativeMCPServer, len(source.MCPServers))
	for name, server := range source.MCPServers {
		server.Args = append([]string(nil), server.Args...)
		server.Environment = mergeNativeEnvironment(nil, server.Environment)
		server.HTTPHeaders = mergeNativeEnvironment(nil, server.HTTPHeaders)
		out.MCPServers[name] = server
	}
	return out
}

func mergeNativeEnvironment(base, overlay map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(overlay))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range overlay {
		out[key] = value
	}
	return out
}

func (s *NativeSession) String() string { return fmt.Sprintf("codex session %s", s.sessionID) }
