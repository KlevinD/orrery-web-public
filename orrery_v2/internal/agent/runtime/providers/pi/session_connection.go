package pi

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/process"
)

// PI's documented RPC protocol is the native non-Node integration boundary.
// Protocol reference: @earendil-works/pi-coding-agent@1.3.0 docs/rpc.md.

const (
	defaultNativePiHandshakeTimeout = 30 * time.Second
	defaultNativePiRequestTimeout   = 15 * time.Second
	defaultNativePiPromptTimeout    = 60 * time.Second
	defaultNativePiEventCapacity    = 1024
	defaultNativePiBackpressureWait = 5 * time.Second
	supportedNativePiVersion        = "1.3.0"
)

type NativeConfig struct {
	Runtime          Config
	Version          string
	HandshakeTimeout time.Duration
	RequestTimeout   time.Duration
	PromptAckTimeout time.Duration
	EventCapacity    int
	BackpressureWait time.Duration
	ResolveWorkspace NativeWorkspaceResolver
	Now              func() time.Time
}

type NativeWorkspaceResolver interface {
	ResolvePiWorkspace(context.Context, string) (NativeWorkspace, error)
}

type NativeWorkspaceResolverFunc func(context.Context, string) (NativeWorkspace, error)

func (f NativeWorkspaceResolverFunc) ResolvePiWorkspace(ctx context.Context, ref string) (NativeWorkspace, error) {
	return f(ctx, ref)
}

// NativeWorkspace is provider-private server-owned launch material. It never
// crosses the canonical SessionSpec/SessionConfig boundary.
type NativeWorkspace struct {
	Request agentcore.RuntimeRequest
	Version string
}

type NativeConnection struct {
	config NativeConfig

	mu       sync.Mutex
	versions map[string]string
	sessions map[agentcore.SessionID]*NativeSession
}

func NewNativeConnection(config NativeConfig) (*NativeConnection, error) {
	if config.HandshakeTimeout <= 0 {
		config.HandshakeTimeout = defaultNativePiHandshakeTimeout
	}
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = defaultNativePiRequestTimeout
	}
	if config.PromptAckTimeout <= 0 {
		config.PromptAckTimeout = defaultNativePiPromptTimeout
	}
	if config.EventCapacity < 1 {
		config.EventCapacity = defaultNativePiEventCapacity
	}
	if config.BackpressureWait <= 0 {
		config.BackpressureWait = defaultNativePiBackpressureWait
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	config.Runtime.Env = cloneStringMap(config.Runtime.Env)
	return &NativeConnection{config: config, versions: make(map[string]string), sessions: make(map[agentcore.SessionID]*NativeSession)}, nil
}

func (c *NativeConnection) OpenSession(ctx context.Context, spec agentcore.SessionSpec, sessionConfig agentcore.SessionConfig) (agentcore.SessionBackend, error) {
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
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "pi workspace resolver is not configured", false)
	}
	workspace, err := c.config.ResolveWorkspace.ResolvePiWorkspace(ctx, sessionConfig.WorkspaceRef)
	if err != nil {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "pi workspace reference could not be resolved", false)
	}
	if err := validateNativePiWorkspace(workspace, spec); err != nil {
		return nil, err
	}
	opts, err := runtimecore.PrepareExecOptions(workspace.Request, agentcore.RuntimeKindPi, nil)
	if err != nil {
		return nil, err
	}
	if err := validatePiProviderConfig(opts.ProviderConfig); err != nil {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "pi provider configuration is invalid", false)
	}
	configuredExecutable := c.config.Runtime.Executable
	runtimeConfig := c.config.Runtime
	opts, parsedConfig := applyRuntimeConfig(opts, &runtimeConfig)
	if isMarketingContinuationRequested(opts) {
		if err := validateDeterministicMarketingContinuation(opts); err != nil {
			return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, err.Error(), false)
		}
		c.mu.Lock()
		if _, exists := c.sessions[spec.SessionID]; exists {
			c.mu.Unlock()
			return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "pi logical session is already open", false)
		}
		backend := openDeterministicMarketingContinuationSession(ctx, c.config, runtimeConfig, spec, sessionConfig.Clone(), workspace, opts)
		c.sessions[spec.SessionID] = backend
		c.mu.Unlock()
		return backend, nil
	}
	opts, err = stabilizePiRPCOptions(opts)
	if err != nil {
		return nil, err
	}
	opts, runtimeConfig, err = applyLLMProfileRuntimeEnv(ctx, opts, runtimeConfig)
	if err != nil {
		return nil, err
	}
	if spec.Mode == agentcore.SessionOpenResume {
		opts.ResumeSessionID = strings.TrimSpace(spec.BackendSessionID)
	}
	opts, err = preparePiSessionPersistence(opts, runtimeConfig)
	if err != nil {
		return nil, err
	}
	debugArtifacts, err := prepareDebugArtifacts(ctx, opts, parsedConfig)
	if err != nil {
		return nil, err
	}
	opts.Environment = mergePiEnvironment(opts.Environment, debugArtifacts.Environment())
	if debugArtifacts.ProviderCaptureBaseURL != "" {
		opts, err = isolatePiDebugWorkerOptions(opts)
		if err != nil {
			debugArtifacts.Cleanup()
			return nil, err
		}
	}
	opts, systemPrompt, err := prepareDynamicSystemPrompt(opts)
	if err != nil {
		debugArtifacts.Cleanup()
		return nil, err
	}
	executable := resolvePiExecutable(configuredExecutable, runtimeConfig.Executable, opts.Cwd)
	resolvedExecutable, err := exec.LookPath(executable)
	if err != nil {
		debugArtifacts.Cleanup()
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "supported pi executable is unavailable", false)
	}
	version, err := c.resolveVersion(ctx, resolvedExecutable, mergePiEnvironment(runtimeConfig.Env, opts.Environment), firstNonEmpty(workspace.Version, c.config.Version))
	if err != nil {
		debugArtifacts.Cleanup()
		return nil, err
	}

	c.mu.Lock()
	if _, exists := c.sessions[spec.SessionID]; exists {
		c.mu.Unlock()
		debugArtifacts.Cleanup()
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "pi logical session is already open", false)
	}
	c.mu.Unlock()

	backend, err := openNativePiSession(ctx, c.config, runtimeConfig, spec, sessionConfig.Clone(), workspace, opts, systemPrompt, resolvedExecutable, version, debugArtifacts)
	if err != nil {
		debugArtifacts.Cleanup()
		return nil, err
	}
	c.mu.Lock()
	if _, exists := c.sessions[spec.SessionID]; exists {
		c.mu.Unlock()
		_ = backend.Close(context.Background())
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "pi logical session was opened concurrently", false)
	}
	c.sessions[spec.SessionID] = backend
	c.mu.Unlock()
	return backend, nil
}

func (c *NativeConnection) CloseSession(ctx context.Context, sessionID agentcore.SessionID) error {
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

func (c *NativeConnection) Capabilities(context.Context) agentcore.Capabilities {
	return nativePiCapabilities(strings.TrimSpace(c.config.Version))
}

func (c *NativeConnection) ReleaseRuntimeWorkspace(ctx context.Context, root string) error {
	if c == nil || strings.TrimSpace(root) == "" {
		return nil
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	c.mu.Lock()
	var closeList []*NativeSession
	for id, session := range c.sessions {
		inside, pathErr := nativePiPathWithin(session.opts.Cwd, absRoot)
		if pathErr == nil && inside {
			closeList = append(closeList, session)
			delete(c.sessions, id)
		}
	}
	c.mu.Unlock()
	for _, session := range closeList {
		if err := session.Close(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (c *NativeConnection) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	sessions := make([]*NativeSession, 0, len(c.sessions))
	for _, session := range c.sessions {
		sessions = append(sessions, session)
	}
	c.sessions = make(map[agentcore.SessionID]*NativeSession)
	c.mu.Unlock()
	for _, session := range sessions {
		_ = session.Close(context.Background())
	}
	return nil
}

func (c *NativeConnection) resolveVersion(ctx context.Context, executable string, environment map[string]string, claimed string) (string, error) {
	// PI's CLI version belongs to the executable, not to the per-run model,
	// credential, or isolated-home environment. Including those values caused
	// every scheduled run to repeat the version probe.
	identity := process.LaunchIdentity(executable, nil, nil, claimed)
	c.mu.Lock()
	version := c.versions[identity]
	c.mu.Unlock()
	if version == "" {
		detected, err := process.DetectVersionWithEnvironmentTimeout(ctx, executable, piVersionEnvironment(environment), c.config.HandshakeTimeout)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return "", ctxErr
			}
			return "", agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "supported pi executable is unavailable", false)
		}
		version = strings.TrimSpace(detected)
		if claimed != "" && normalizePiVersion(claimed) != normalizePiVersion(version) {
			return "", agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "configured pi version does not match the executable", false)
		}
	}
	if normalizePiVersion(version) != supportedNativePiVersion {
		return "", agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, fmt.Sprintf("pi version %s is unsupported; expected %s", sanitizePiDiagnostic(version), supportedNativePiVersion), false)
	}
	c.mu.Lock()
	c.versions[identity] = version
	c.mu.Unlock()
	return normalizePiVersion(version), nil
}

var nativePiVersionPattern = regexp.MustCompile(`\b(\d+\.\d+\.\d+)\b`)

func normalizePiVersion(value string) string {
	match := nativePiVersionPattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) == 2 {
		return match[1]
	}
	return ""
}

func validateNativePiWorkspace(workspace NativeWorkspace, spec agentcore.SessionSpec) error {
	request := workspace.Request
	if request.RunID == "" || request.AgentID.IsZero() || strings.TrimSpace(request.TenantID) == "" {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "resolved pi workspace identity is incomplete", false)
	}
	directory := strings.TrimSpace(request.RuntimeOptions.WorkspacePolicy.WorkDir)
	if directory == "" {
		directory = strings.TrimSpace(request.RuntimeOptions.WorkingDirectory)
	}
	if directory == "" || !filepath.IsAbs(directory) {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "resolved pi workspace must be an absolute local directory", false)
	}
	if spec.Mode == agentcore.SessionOpenResume && strings.TrimSpace(spec.BackendSessionID) == "" {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "pi resume requires backend_session_id", false)
	}
	return nil
}

func nativePiCapabilities(version string) agentcore.Capabilities {
	return agentcore.Capabilities{
		Tier: agentcore.CapabilityParsed,
		SupportedCommands: agentcore.CommandSet{
			Send: true, CancelTurn: true, CancelSession: true, Steer: true,
			AnswerPermission: true, Acknowledge: true, SetModel: true,
			SetConfigOption: true, QuerySessionInfo: true,
		},
		PromptBlocks:      agentcore.BlockSet{Text: true, Image: true},
		PromptAccepted:    agentcore.PromptAcceptedNative,
		Signals:           agentcore.SignalSet{ToolLifecycle: true, TerminalResult: true, Usage: true, Plan: true},
		ConfigOptions:     []agentcore.ConfigOptionInfo{{ID: "reasoning_effort", Name: "Reasoning effort", Values: []string{"off", "minimal", "low", "medium", "high", "xhigh"}}},
		SupportsMultiTurn: true,
		SupportsResume:    true,
		PendingLimit:      1,
		PayloadLimitBytes: piRPCMaxRecordBytes,
		Transport:         agentcore.TransportEvidence{Provider: "pi", Protocol: "rpc-jsonl-stdio", Version: strings.TrimSpace(version)},
	}
}

func nativePiPathWithin(path, root string) (bool, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(absPath))
	if err != nil {
		return false, err
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)), nil
}

func mergePiEnvironment(base, overlay map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(overlay))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range overlay {
		out[key] = value
	}
	return out
}

var _ agentcore.BackendConnection = (*NativeConnection)(nil)
