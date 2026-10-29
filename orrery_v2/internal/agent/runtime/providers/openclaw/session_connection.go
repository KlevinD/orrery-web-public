package openclaw

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

const defaultOpenClawEventCapacity = 1024

// ConnectionConfig contains only server-owned OpenClaw launch state. The
// public SessionConfig carries an opaque workspace reference instead of paths,
// credentials, or provider configuration.
type ConnectionConfig struct {
	Runtime          Config
	Version          string
	EventCapacity    int
	ResolveWorkspace WorkspaceResolver
	Now              func() time.Time
}

type WorkspaceResolver interface {
	ResolveOpenClawWorkspace(context.Context, string) (NativeWorkspace, error)
}

type WorkspaceResolverFunc func(context.Context, string) (NativeWorkspace, error)

func (f WorkspaceResolverFunc) ResolveOpenClawWorkspace(ctx context.Context, ref string) (NativeWorkspace, error) {
	return f(ctx, ref)
}

// NativeWorkspace is provider-private launch material resolved from the
// session workspace broker. It never crosses the SessionConfig boundary.
type NativeWorkspace struct {
	Request agentcore.RuntimeRequest
	Version string
}

// Connection adapts the retained OpenClaw CLI/remote transports to the
// canonical SessionBackend contract used by all runtime consumers.
type Connection struct {
	config ConnectionConfig

	mu       sync.Mutex
	sessions map[agentcore.SessionID]*Session
}

func NewConnection(config ConnectionConfig) (*Connection, error) {
	if config.EventCapacity < 1 {
		config.EventCapacity = defaultOpenClawEventCapacity
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	config.Runtime.Env = cloneOpenClawEnvironment(config.Runtime.Env)
	return &Connection{config: config, sessions: make(map[agentcore.SessionID]*Session)}, nil
}

func (c *Connection) OpenSession(ctx context.Context, spec agentcore.SessionSpec, config agentcore.SessionConfig) (agentcore.SessionBackend, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if c == nil || c.config.ResolveWorkspace == nil {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "openclaw workspace resolver is not configured", false)
	}
	workspace, err := c.config.ResolveWorkspace.ResolveOpenClawWorkspace(ctx, config.WorkspaceRef)
	if err != nil {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "openclaw workspace reference could not be resolved", false)
	}
	opts, err := runtimecore.PrepareExecOptions(workspace.Request, agentcore.RuntimeKindOpenClaw, nil)
	if err != nil {
		return nil, err
	}
	if spec.Mode == agentcore.SessionOpenResume {
		opts.ResumeSessionID = strings.TrimSpace(spec.BackendSessionID)
	}
	if model := strings.TrimSpace(config.Model); model != "" {
		opts.Model = model
	}
	runtimeConfig := cloneOpenClawConfig(c.config.Runtime)
	backend := NewBackend(runtimeConfig)
	if err := backend.ValidateProviderConfig(opts.ProviderConfig); err != nil {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "openclaw provider configuration is invalid", false)
	}

	c.mu.Lock()
	if _, exists := c.sessions[spec.SessionID]; exists {
		c.mu.Unlock()
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "openclaw logical session is already open", false)
	}
	session := newSession(spec.SessionID, backend, opts, firstNonEmpty(workspace.Version, c.config.Version), c.config.EventCapacity, c.config.Now)
	c.sessions[spec.SessionID] = session
	c.mu.Unlock()
	return session, nil
}

func (c *Connection) CloseSession(ctx context.Context, sessionID agentcore.SessionID) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if c == nil {
		return nil
	}
	c.mu.Lock()
	session := c.sessions[sessionID]
	delete(c.sessions, sessionID)
	c.mu.Unlock()
	if session == nil {
		return nil
	}
	return session.Close(ctx)
}

func (c *Connection) Capabilities(context.Context) agentcore.Capabilities {
	if c == nil {
		return agentcore.Capabilities{}
	}
	return openClawCapabilities(c.config.Version)
}

func (c *Connection) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	sessions := make([]*Session, 0, len(c.sessions))
	for _, session := range c.sessions {
		sessions = append(sessions, session)
	}
	c.sessions = make(map[agentcore.SessionID]*Session)
	c.mu.Unlock()
	for _, session := range sessions {
		_ = session.Close(context.Background())
	}
	return nil
}

func openClawCapabilities(version string) agentcore.Capabilities {
	return agentcore.Capabilities{
		Tier: agentcore.CapabilityParsed,
		SupportedCommands: agentcore.CommandSet{
			Send: true, CancelTurn: true, CancelSession: true,
		},
		PromptBlocks:      agentcore.BlockSet{Text: true},
		PromptAccepted:    agentcore.PromptAcceptedSynthesized,
		Signals:           agentcore.SignalSet{ToolLifecycle: true, TerminalResult: true, Usage: true, Plan: true},
		SupportsMultiTurn: true,
		SupportsResume:    true,
		PayloadLimitBytes: 16 << 20,
		Transport:         agentcore.TransportEvidence{Provider: "openclaw", Protocol: "cli-json-or-openai-http", Version: strings.TrimSpace(version)},
	}
}

type Session struct {
	sessionID  agentcore.SessionID
	backend    *Backend
	opts       runtimecore.ExecOptions
	capability agentcore.Capabilities
	now        func() time.Time

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	events chan agentcore.SessionEnvelope

	mu         sync.Mutex
	closed     bool
	active     bool
	generation uint64
	turnCancel context.CancelFunc
	sequence   atomic.Uint64
	closeOnce  sync.Once
	wg         sync.WaitGroup
}

func newSession(sessionID agentcore.SessionID, backend *Backend, opts runtimecore.ExecOptions, version string, eventCapacity int, now func() time.Time) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	return &Session{
		sessionID:  sessionID,
		backend:    backend,
		opts:       cloneOpenClawExecOptions(opts),
		capability: openClawCapabilities(version),
		now:        now,
		ctx:        ctx,
		cancel:     cancel,
		done:       make(chan struct{}),
		events:     make(chan agentcore.SessionEnvelope, eventCapacity),
	}
}

func (s *Session) Dispatch(ctx context.Context, command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return agentcore.CommandReceipt{}, err
	}
	if err := command.Validate(); err != nil {
		return agentcore.CommandReceipt{}, err
	}
	if !s.Capabilities().Supports(command) {
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, string(command.Kind), false)
	}
	switch command.Kind {
	case agentcore.RuntimeCommandSend:
		return s.dispatchSend(command)
	case agentcore.RuntimeCommandCancel:
		return s.dispatchCancel(command)
	default:
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, string(command.Kind), false)
	}
}

func (s *Session) Events() <-chan agentcore.SessionEnvelope { return s.events }

func (s *Session) Capabilities() agentcore.Capabilities { return s.capability.Clone() }

func (s *Session) PendingPermissionRequests() []agentcore.PendingPermissionView { return nil }

func (s *Session) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		turnCancel := s.turnCancel
		s.mu.Unlock()
		if turnCancel != nil {
			turnCancel()
		}
		s.cancel()
		go func() {
			s.wg.Wait()
			close(s.events)
			close(s.done)
		}()
	})
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Session) dispatchSend(command agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	prompt, err := openClawPrompt(command.Send.Content)
	if err != nil {
		return agentcore.CommandReceipt{}, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "openclaw session is closed", false)
	}
	if s.active {
		s.mu.Unlock()
		return agentcore.CommandReceipt{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "openclaw already has an active turn", false)
	}
	s.generation++
	generation := s.generation
	turnCtx, turnCancel := context.WithCancel(s.ctx)
	s.active = true
	s.turnCancel = turnCancel
	opts := cloneOpenClawExecOptions(s.opts)
	clientMsgID := strings.TrimSpace(command.Send.Metadata.ClientMsgID)
	s.wg.Add(1)
	s.mu.Unlock()

	go s.runTurn(turnCtx, turnCancel, generation, clientMsgID, prompt, opts)
	return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionStarted, TurnGeneration: generation}, nil
}

func (s *Session) dispatchCancel(_ agentcore.RuntimeCommand) (agentcore.CommandReceipt, error) {
	s.mu.Lock()
	generation := s.generation
	active := s.active
	turnCancel := s.turnCancel
	s.mu.Unlock()
	if !active {
		return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn, TurnGeneration: generation}, nil
	}
	if turnCancel != nil {
		turnCancel()
	}
	return agentcore.CommandReceipt{Accepted: true, Admission: agentcore.AdmissionNoTurn, TurnGeneration: generation}, nil
}

var _ agentcore.BackendConnection = (*Connection)(nil)
var _ agentcore.SessionBackend = (*Session)(nil)
