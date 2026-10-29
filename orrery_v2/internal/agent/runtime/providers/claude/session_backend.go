package claude

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/process"
	"orrery_v2/internal/agent/runtime/providers/toolbridge"
	runtimesession "orrery_v2/internal/agent/runtime/session"
)

type NativeSession struct {
	sessionID agentcore.SessionID
	spec      agentcore.SessionSpec
	config    agentcore.SessionConfig
	workspace NativeWorkspace
	native    NativeConfig
	version   string
	claudeID  string
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
	binding   *claudeProcessBinding
	suspend   *runtimesession.SuspendController

	dispatchMu sync.Mutex
	writeMu    sync.Mutex
	nextID     atomic.Uint64

	mu               sync.Mutex
	backendSessionID string
	backendBound     bool
	turnGeneration   uint64
	turnOpen         uint64
	generationQueue  []uint64
	resultQueue      []uint64
	active           map[uint64]bool
	terminated       map[uint64]bool
	clientGeneration map[string]uint64
	pendingControls  []claudeQueuedControl
	pendingControl   map[string]*claudePendingControl
	pendingSettings  map[string]claudePendingSetting
	pendingPerms     map[string]claudePendingPermission
	capabilities     agentcore.Capabilities
	currentEffort    string

	stream claudeStreamState
	plan   *claudePlanTracker
}

type claudePendingControl struct {
	response chan claudeControlResponse
}

type claudeControlResponse struct {
	response map[string]any
	err      error
}

type claudePendingSetting struct {
	kind  string
	value string
}

type claudeQueuedControl struct {
	id      string
	subtype string
	frame   map[string]any
}

type claudePendingPermission struct {
	requestID string
	toolUseID string
	toolName  string
	input     map[string]any
	questions []agentcore.PermissionQuestion
}

type claudeStreamState struct {
	itemID           string
	blockKinds       map[uint64]string
	streamedText     bool
	streamedThinking bool
}

func openNativeSession(parent context.Context, native NativeConfig, version string, spec agentcore.SessionSpec, config agentcore.SessionConfig, workspace NativeWorkspace, claudeID string, fresh bool) (*NativeSession, error) {
	ctx, cancel := context.WithCancel(context.Background())
	backend := &NativeSession{
		sessionID:        spec.SessionID,
		spec:             spec,
		config:           config,
		workspace:        workspace,
		native:           native,
		version:          version,
		claudeID:         claudeID,
		now:              native.Now,
		ctx:              ctx,
		cancel:           cancel,
		events:           make(chan agentcore.SessionEnvelope, native.EventCapacity+2),
		eventIn:          make(chan agentcore.SessionEnvelope, native.EventCapacity),
		eventAbort:       make(chan struct{}),
		eventPumpDone:    make(chan struct{}),
		done:             make(chan struct{}),
		active:           make(map[uint64]bool),
		terminated:       make(map[uint64]bool),
		clientGeneration: make(map[string]uint64),
		pendingControl:   make(map[string]*claudePendingControl),
		pendingSettings:  make(map[string]claudePendingSetting),
		pendingPerms:     make(map[string]claudePendingPermission),
		capabilities:     nativeClaudeCapabilities(version),
		stream:           claudeStreamState{blockKinds: make(map[uint64]string)},
		plan:             newClaudePlanTracker(),
		backendSessionID: claudeID,
	}
	backend.capabilities.CurrentModel = config.Model
	backend.capabilities.CurrentMode = claudeNormalizedMode(config.Mode)
	if err := backend.configureDesktopMCP(); err != nil {
		cancel()
		return nil, err
	}
	go backend.runEventPump()

	binding, err := backend.startProcess(!fresh)
	if err != nil {
		cancel()
		backend.closeEvents()
		<-backend.eventPumpDone
		return nil, err
	}
	backend.setBinding(binding)
	handshakeCtx, handshakeCancel := context.WithTimeout(parent, native.HandshakeTimeout)
	err = backend.runHandshake(handshakeCtx)
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
	s.workspace.MCPServers[name] = NativeMCPServer{URL: bridge.URL, Headers: map[string]string{"Authorization": bridge.Authorization}}
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
	if !s.Capabilities().Supports(command) {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, string(command.Kind), false)
	}
	if s.dead.Load() {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "claude process is detached", false)
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
	case agentcore.RuntimeCommandAnswerPermission:
		return s.dispatchAnswerPermission(command)
	case agentcore.RuntimeCommandAcknowledge:
		return s.noTurnReceipt(), nil
	case agentcore.RuntimeCommandSetMode:
		return s.dispatchSetting("mode", command.SetMode.Mode)
	case agentcore.RuntimeCommandSetModel:
		return s.dispatchSetting("model", command.SetModel.Model)
	case agentcore.RuntimeCommandSetConfigOption:
		return s.dispatchSetting(command.SetConfigOption.OptionID, command.SetConfigOption.Value)
	case agentcore.RuntimeCommandQuerySessionInfo:
		return s.dispatchSessionInfo(command.QuerySessionInfo.Kind)
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
	out := make([]agentcore.PendingPermissionView, 0, len(s.pendingPerms))
	for _, pending := range s.pendingPerms {
		out = append(out, agentcore.PendingPermissionView{RequestID: pending.requestID, ToolName: pending.toolName, Questions: cloneClaudeQuestions(pending.questions)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RequestID < out[j].RequestID })
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
		s.failPending(agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "claude session closed", false))
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
		binding, err := s.startProcess(true)
		if err != nil {
			return nil, err
		}
		s.setBinding(binding)
		handshakeCtx, cancel := context.WithTimeout(wakeCtx, s.native.HandshakeTimeout)
		err = s.runHandshake(handshakeCtx)
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
	return len(s.active) != 0
}

func (s *NativeSession) currentGeneration() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.turnGeneration
}

func (s *NativeSession) noTurnReceipt() agentcore.CommandReceipt {
	return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn, TurnGeneration: s.currentGeneration()}
}

func (s *NativeSession) setBinding(binding *claudeProcessBinding) {
	s.bindingMu.Lock()
	s.binding = binding
	s.bindingMu.Unlock()
}

func (s *NativeSession) currentBinding() *claudeProcessBinding {
	s.bindingMu.RLock()
	defer s.bindingMu.RUnlock()
	return s.binding
}

func (s *NativeSession) startProcess(resume bool) (*claudeProcessBinding, error) {
	executable, err := resolveClaudeExecutable(s.native.Executable)
	if err != nil {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "claude executable was not found", false)
	}
	args, err := s.processArgs(resume)
	if err != nil {
		return nil, err
	}
	processCtx, cancel := context.WithCancel(s.ctx)
	cmd := exec.CommandContext(processCtx, executable, args...)
	process.ConfigureProcessTree(cmd)
	cmd.Dir = s.workspace.Directory
	cmd.Env = process.BuildEnv(mergeClaudeEnvironment(s.native.Environment, s.workspace.Environment))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "claude stdout pipe failed", true)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "claude stdin pipe failed", true)
	}
	stderr := process.NewStderrTail(nil, 4096)
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "claude process failed to start", true)
	}
	if err := process.AttachProcessTree(cmd); err != nil {
		_ = process.TerminateProcessTree(cmd)
		cancel()
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "claude process tree could not be attached", true)
	}
	binding := &claudeProcessBinding{session: s, cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr, cancel: cancel, done: make(chan struct{})}
	go binding.readLoop()
	return binding, nil
}

func resolveClaudeExecutable(executable string) (string, error) {
	resolved, err := exec.LookPath(executable)
	if err != nil {
		return "", err
	}
	if runtime.GOOS != "windows" {
		return resolved, nil
	}
	extension := strings.ToLower(filepath.Ext(resolved))
	if extension == ".exe" {
		return resolved, nil
	}
	native := filepath.Join(filepath.Dir(resolved), "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe")
	info, statErr := os.Stat(native)
	if statErr == nil && !info.IsDir() {
		return native, nil
	}
	if extension != ".cmd" && extension != ".bat" {
		return resolved, nil
	}
	return resolved, nil
}

type claudeProcessBinding struct {
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

func (b *claudeProcessBinding) Stop(ctx context.Context) error {
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
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "claude process did not stop after termination", true)
	}
}

func (b *claudeProcessBinding) readLoop() {
	defer close(b.done)
	scanner := bufio.NewScanner(b.stdout)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		if err := b.session.handleClaudeLine(line); err != nil {
			b.session.handleClaudeProtocolError(err)
		}
	}
	scanErr := scanner.Err()
	waitErr := b.cmd.Wait()
	process.ReleaseProcessTree(b.cmd)
	b.session.processExited(b, waitErr, scanErr)
}

func (s *NativeSession) processExited(binding *claudeProcessBinding, waitErr, scanErr error) {
	if binding.intentional.Load() || s.ctx.Err() != nil {
		return
	}
	s.dead.Store(true)
	s.failAllActive("claude process exited before turn completion")
	s.failPending(agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "claude process exited", true))
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
	summary := "claude process exited"
	if scanErr != nil {
		summary = "claude output stream failed"
	}
	s.emit(s.currentGeneration(), agentcore.SessionEvent{Kind: agentcore.EventDetached, Detached: &agentcore.DetachedEvent{Exit: exit, RedactedSummary: summary}})
	s.closeEvents()
}

func (s *NativeSession) handleClaudeProtocolError(_ error) {
	s.emitDiagnostic("claude_protocol_error", "invalid Claude stream-json frame")
}

func (s *NativeSession) failPending(err error) {
	s.mu.Lock()
	pending := s.pendingControl
	s.pendingControl = make(map[string]*claudePendingControl)
	s.pendingSettings = make(map[string]claudePendingSetting)
	s.mu.Unlock()
	for _, call := range pending {
		select {
		case call.response <- claudeControlResponse{err: err}:
		default:
		}
		close(call.response)
	}
}

func (s *NativeSession) failAllActive(message string) {
	s.mu.Lock()
	generations := make([]uint64, 0, len(s.active))
	for generation := range s.active {
		if !s.terminated[generation] {
			s.terminated[generation] = true
			generations = append(generations, generation)
		}
	}
	s.active = make(map[uint64]bool)
	s.mu.Unlock()
	sort.Slice(generations, func(i, j int) bool { return generations[i] < generations[j] })
	for _, generation := range generations {
		s.emit(generation, claudeFailedTurn(generation, message))
	}
}

func (s *NativeSession) String() string { return fmt.Sprintf("claude session %s", s.sessionID) }
