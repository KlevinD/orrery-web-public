package acp

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

const (
	defaultHandshakeTimeout = 30 * time.Second
	defaultRequestTimeout   = 15 * time.Second
	defaultEventCapacity    = 1024
	defaultMaxRecordBytes   = 32 * 1024 * 1024
)

// Config controls the generic ACP JSON-RPC client. Process launch material is
// resolved from the server-owned workspace binding, never from SessionConfig.
type Config struct {
	HandshakeTimeout time.Duration
	RequestTimeout   time.Duration
	EventCapacity    int
	MaxRecordBytes   int
	ResolveWorkspace WorkspaceResolver
	Now              func() time.Time
}

type WorkspaceResolver interface {
	ResolveACPWorkspace(context.Context, string) (Workspace, error)
}

type WorkspaceResolverFunc func(context.Context, string) (Workspace, error)

func (f WorkspaceResolverFunc) ResolveACPWorkspace(ctx context.Context, ref string) (Workspace, error) {
	return f(ctx, ref)
}

type Workspace struct {
	Directory   string
	Executable  string
	Args        []string
	Environment map[string]string
	Version     string
	MCPServers  []MCPServer
}

type MCPServer struct {
	Name        string
	Transport   agentcore.MCPTransportKind
	Command     string
	Args        []string
	Environment map[string]string
	URL         string
	Headers     map[string]string
}

type Connection struct {
	config Config

	mu       sync.Mutex
	sessions map[agentcore.SessionID]*Session
}

func NewConnection(config Config) (*Connection, error) {
	if config.HandshakeTimeout <= 0 {
		config.HandshakeTimeout = defaultHandshakeTimeout
	}
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = defaultRequestTimeout
	}
	if config.EventCapacity < 1 {
		config.EventCapacity = defaultEventCapacity
	}
	if config.MaxRecordBytes < 1 {
		config.MaxRecordBytes = defaultMaxRecordBytes
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Connection{config: config, sessions: make(map[agentcore.SessionID]*Session)}, nil
}

func (c *Connection) OpenSession(ctx context.Context, spec agentcore.SessionSpec, sessionConfig agentcore.SessionConfig) (agentcore.SessionBackend, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if err := sessionConfig.Validate(); err != nil {
		return nil, err
	}
	if c == nil || c.config.ResolveWorkspace == nil {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "ACP workspace resolver is not configured", false)
	}
	workspace, err := c.config.ResolveWorkspace.ResolveACPWorkspace(ctx, sessionConfig.WorkspaceRef)
	if err != nil {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "ACP workspace reference could not be resolved", false)
	}
	if err := validateWorkspace(workspace, spec); err != nil {
		return nil, err
	}

	c.mu.Lock()
	if _, exists := c.sessions[spec.SessionID]; exists {
		c.mu.Unlock()
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "ACP logical session is already open", false)
	}
	c.mu.Unlock()

	backend, err := openSession(ctx, c.config, spec, sessionConfig.Clone(), cloneWorkspace(workspace))
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if _, exists := c.sessions[spec.SessionID]; exists {
		c.mu.Unlock()
		_ = backend.Close(context.Background())
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "ACP logical session was opened concurrently", false)
	}
	c.sessions[spec.SessionID] = backend
	c.mu.Unlock()
	return backend, nil
}

func (c *Connection) CloseSession(ctx context.Context, sessionID agentcore.SessionID) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	backend := c.sessions[sessionID]
	delete(c.sessions, sessionID)
	c.mu.Unlock()
	if backend == nil {
		return nil
	}
	return backend.Close(ctx)
}

func (c *Connection) Capabilities(context.Context) agentcore.Capabilities {
	capabilities := baseCapabilities("")
	capabilities.PayloadLimitBytes = uint64(c.config.MaxRecordBytes)
	return capabilities
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

func validateWorkspace(workspace Workspace, spec agentcore.SessionSpec) error {
	if strings.TrimSpace(workspace.Executable) == "" {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "ACP runtime profile requires an executable", false)
	}
	if strings.TrimSpace(workspace.Directory) == "" || !filepath.IsAbs(workspace.Directory) {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "ACP workspace must be an absolute local directory", false)
	}
	if spec.Mode == agentcore.SessionOpenResume && strings.TrimSpace(spec.BackendSessionID) == "" {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "ACP resume requires backend_session_id", false)
	}
	return nil
}

func cloneWorkspace(source Workspace) Workspace {
	out := source
	out.Args = append([]string(nil), source.Args...)
	out.Environment = cloneStrings(source.Environment)
	out.MCPServers = make([]MCPServer, len(source.MCPServers))
	for index, server := range source.MCPServers {
		out.MCPServers[index] = server
		out.MCPServers[index].Args = append([]string(nil), server.Args...)
		out.MCPServers[index].Environment = cloneStrings(server.Environment)
		out.MCPServers[index].Headers = cloneStrings(server.Headers)
	}
	return out
}

func cloneStrings(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	out := make(map[string]string, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

func baseCapabilities(version string) agentcore.Capabilities {
	return agentcore.Capabilities{
		Tier: agentcore.CapabilityParsed,
		SupportedCommands: agentcore.CommandSet{
			Send: true, CancelTurn: true, CancelSession: true, AnswerPermission: true,
			SetMode: true, SetModel: true, SetConfigOption: true,
		},
		PromptBlocks:      agentcore.BlockSet{Text: true, Image: true, Audio: true, Resource: true},
		PromptAccepted:    agentcore.PromptAcceptedSynthesized,
		Signals:           agentcore.SignalSet{ToolLifecycle: true, TerminalResult: true, Plan: true},
		SupportsMultiTurn: true,
		SupportsResume:    true,
		PendingLimit:      1,
		PayloadLimitBytes: defaultMaxRecordBytes,
		Transport:         agentcore.TransportEvidence{Provider: "acp", Protocol: "agent-client-protocol-jsonrpc-stdio", Version: strings.TrimSpace(version)},
	}
}

var _ agentcore.BackendConnection = (*Connection)(nil)
