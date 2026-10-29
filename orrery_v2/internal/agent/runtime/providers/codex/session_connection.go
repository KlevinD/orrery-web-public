// Historical source: AionCore aionui-session, Copyright 2025 AionUi
// (aionui.com), Apache-2.0. Go adaptation and modifications: Minvia, 2026.
// See docs/runtime/runtime-source-provenance.md for scope and the full license.
package codex

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/process"
)

// Reference: aionui-session/backend/codex_conn.rs@v0.1.52.

const (
	defaultNativeHandshakeTimeout = 30 * time.Second
	defaultNativeEventCapacity    = 1024
	defaultNativeBackpressureWait = 5 * time.Second
	minSupportedCodexMinor        = 137
	maxSupportedCodexMinor        = 145
)

type NativeConfig struct {
	ToolDispatcher   runtimecore.RuntimeToolDispatcher
	Executable       string
	ExtraArgs        []string
	Environment      map[string]string
	Version          string
	HandshakeTimeout time.Duration
	EventCapacity    int
	BackpressureWait time.Duration
	ResolveWorkspace WorkspaceResolver
	Now              func() time.Time
}

type WorkspaceResolver interface {
	ResolveCodexWorkspace(context.Context, string) (NativeWorkspace, error)
}

type WorkspaceResolverFunc func(context.Context, string) (NativeWorkspace, error)

func (f WorkspaceResolverFunc) ResolveCodexWorkspace(ctx context.Context, ref string) (NativeWorkspace, error) {
	return f(ctx, ref)
}

type NativeWorkspace struct {
	Request         agentcore.RuntimeRequest
	Directory       string
	Root            string
	EnforceBoundary bool
	Environment     map[string]string
	Sandbox         string
	ApprovalPolicy  string
	WritableRoots   []string
	Launch          NativeLaunch
	MCPServers      map[string]NativeMCPServer
}

type NativeLaunch struct {
	Executable string
	Version    string
	ExtraArgs  []string
}

type NativeMCPServer struct {
	Command           string
	Args              []string
	Environment       map[string]string
	URL               string
	BearerTokenEnvVar string
	HTTPHeaders       map[string]string
}

type NativeConnection struct {
	config   NativeConfig
	versions map[string]string

	mu       sync.Mutex
	sessions map[agentcore.SessionID]*NativeSession
}

func NewNativeConnection(config NativeConfig) (*NativeConnection, error) {
	config.ExtraArgs = append([]string(nil), config.ExtraArgs...)
	config.Environment = mergeNativeEnvironment(nil, config.Environment)
	if strings.TrimSpace(config.Executable) == "" {
		config.Executable = "codex"
	}
	if config.HandshakeTimeout <= 0 {
		config.HandshakeTimeout = defaultNativeHandshakeTimeout
	}
	if config.EventCapacity < 1 {
		config.EventCapacity = defaultNativeEventCapacity
	}
	if config.BackpressureWait <= 0 {
		config.BackpressureWait = defaultNativeBackpressureWait
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &NativeConnection{config: config, versions: make(map[string]string), sessions: make(map[agentcore.SessionID]*NativeSession)}, nil
}

func (c *NativeConnection) OpenSession(ctx context.Context, spec agentcore.SessionSpec, config agentcore.SessionConfig) (agentcore.SessionBackend, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if c.config.ResolveWorkspace == nil {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "codex workspace resolver is not configured", false)
	}
	workspace, err := c.config.ResolveWorkspace.ResolveCodexWorkspace(ctx, config.WorkspaceRef)
	if err != nil {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "codex workspace reference could not be resolved", false)
	}
	if err := validateNativeWorkspace(workspace, config); err != nil {
		return nil, err
	}
	native := codexNativeConfig(c.config, workspace.Launch)
	version, err := c.resolveVersion(ctx, native, workspace.Environment)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	if _, exists := c.sessions[spec.SessionID]; exists {
		c.mu.Unlock()
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "codex logical session is already open", false)
	}
	c.mu.Unlock()

	backend, err := openNativeSession(ctx, native, version, spec, config.Clone(), workspace)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if _, exists := c.sessions[spec.SessionID]; exists {
		c.mu.Unlock()
		_ = backend.Close(context.Background())
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "codex logical session was opened concurrently", false)
	}
	c.sessions[spec.SessionID] = backend
	c.mu.Unlock()
	return backend, nil
}

func (c *NativeConnection) CloseSession(ctx context.Context, sessionID agentcore.SessionID) error {
	if ctx == nil {
		ctx = context.Background()
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
	return nativeCodexCapabilities(strings.TrimSpace(c.config.Version))
}

func (c *NativeConnection) Close() error {
	c.mu.Lock()
	sessions := make([]*NativeSession, 0, len(c.sessions))
	for _, backend := range c.sessions {
		sessions = append(sessions, backend)
	}
	c.sessions = make(map[agentcore.SessionID]*NativeSession)
	c.mu.Unlock()
	for _, backend := range sessions {
		_ = backend.Close(context.Background())
	}
	return nil
}

func (c *NativeConnection) resolveVersion(ctx context.Context, native NativeConfig, workspaceEnvironment map[string]string) (string, error) {
	environment := mergeNativeEnvironment(native.Environment, workspaceEnvironment)
	identity := process.LaunchIdentity(native.Executable, native.ExtraArgs, environment, native.Version)
	c.mu.Lock()
	version := c.versions[identity]
	c.mu.Unlock()
	if version == "" {
		detected, err := process.DetectVersionWithEnvironmentTimeout(ctx, native.Executable, environment, native.HandshakeTimeout)
		if err != nil {
			return "", agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "supported codex executable is unavailable", false)
		}
		if claimed := strings.TrimSpace(native.Version); claimed != "" && !sameCodexVersion(claimed, detected) {
			return "", agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "configured codex version does not match the executable", false)
		}
		version = detected
	}
	if err := validateCodexVersion(version); err != nil {
		return "", err
	}
	c.mu.Lock()
	c.versions[identity] = version
	c.mu.Unlock()
	return version, nil
}

func sameCodexVersion(left string, right string) bool {
	leftMatch := codexVersionPattern.FindStringSubmatch(strings.TrimSpace(left))
	rightMatch := codexVersionPattern.FindStringSubmatch(strings.TrimSpace(right))
	return len(leftMatch) == 4 && len(rightMatch) == 4 && leftMatch[1] == rightMatch[1] && leftMatch[2] == rightMatch[2] && leftMatch[3] == rightMatch[3]
}

func codexNativeConfig(base NativeConfig, launch NativeLaunch) NativeConfig {
	out := base
	out.ExtraArgs = append([]string(nil), base.ExtraArgs...)
	out.Environment = mergeNativeEnvironment(nil, base.Environment)
	if executable := strings.TrimSpace(launch.Executable); executable != "" {
		out.Executable = executable
	}
	if version := strings.TrimSpace(launch.Version); version != "" {
		out.Version = version
	}
	out.ExtraArgs = append(out.ExtraArgs, launch.ExtraArgs...)
	return out
}

var codexVersionPattern = regexp.MustCompile(`(?i)(?:codex(?:-cli)?\s+)?(\d+)\.(\d+)\.(\d+)`)

func validateCodexVersion(version string) error {
	matches := codexVersionPattern.FindStringSubmatch(strings.TrimSpace(version))
	if len(matches) != 4 {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "codex version could not be verified", false)
	}
	major, _ := strconv.Atoi(matches[1])
	minor, _ := strconv.Atoi(matches[2])
	if major != 0 || minor < minSupportedCodexMinor || minor > maxSupportedCodexMinor {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, fmt.Sprintf("codex version %s is outside the supported 0.%d-0.%d range", matches[0], minSupportedCodexMinor, maxSupportedCodexMinor), false)
	}
	return nil
}

func validateNativeWorkspace(workspace NativeWorkspace, config agentcore.SessionConfig) error {
	if strings.TrimSpace(workspace.Directory) == "" || !filepath.IsAbs(workspace.Directory) {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "resolved codex workspace must be an absolute local directory", false)
	}
	switch workspace.Sandbox {
	case "", "read-only", "workspace-write", "danger-full-access":
	default:
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "resolved codex sandbox is invalid", false)
	}
	switch workspace.ApprovalPolicy {
	case "", "untrusted", "on-failure", "on-request", "never":
	default:
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "resolved codex approval policy is invalid", false)
	}
	for _, spec := range config.MCPServers {
		resolved, exists := workspace.MCPServers[spec.Name]
		if !exists {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "codex MCP reference is unresolved", false)
		}
		switch spec.Transport {
		case agentcore.MCPTransportStdio:
			if strings.TrimSpace(resolved.Command) == "" || resolved.URL != "" || resolved.BearerTokenEnvVar != "" {
				return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "resolved stdio MCP binding is invalid", false)
			}
		case agentcore.MCPTransportHTTP, agentcore.MCPTransportSSE:
			if strings.TrimSpace(resolved.URL) == "" || resolved.Command != "" || resolved.URL != spec.Endpoint {
				return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "resolved network MCP binding is invalid", false)
			}
			if len(spec.HeaderRefs) != 0 {
				for header := range spec.HeaderRefs {
					if strings.TrimSpace(resolved.HTTPHeaders[header]) == "" && (strings.TrimSpace(resolved.BearerTokenEnvVar) == "" || strings.TrimSpace(workspace.Environment[resolved.BearerTokenEnvVar]) == "") {
						return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "codex MCP credential reference is unresolved", false)
					}
				}
			}
		}
	}
	return nil
}

func nativeCodexArgs(extra []string) []string {
	out := make([]string, 0, len(extra))
	for index := 0; index < len(extra); index++ {
		arg := strings.TrimSpace(extra[index])
		switch {
		case arg == "--strict-config", arg == "--analytics-default-enabled":
			out = append(out, arg)
		case arg == "--enable" || arg == "--disable":
			if index+1 < len(extra) && strings.TrimSpace(extra[index+1]) != "" && !strings.HasPrefix(strings.TrimSpace(extra[index+1]), "-") {
				out = append(out, arg, strings.TrimSpace(extra[index+1]))
				index++
			}
		case strings.HasPrefix(arg, "--enable=") || strings.HasPrefix(arg, "--disable="):
			if _, value, ok := strings.Cut(arg, "="); ok && strings.TrimSpace(value) != "" {
				out = append(out, arg)
			}
		}
	}
	return out
}

func nativeCodexCapabilities(version string) agentcore.Capabilities {
	return agentcore.Capabilities{
		Tier: agentcore.CapabilityHook,
		SupportedCommands: agentcore.CommandSet{
			Send: true, CancelTurn: true, CancelSession: true, Steer: true,
			AnswerPermission: true, AnswerAuth: true, Acknowledge: true,
			SetMode: true, SetModel: true, SetConfigOption: true,
			Rewind: true, ListCheckpoints: true,
		},
		PromptBlocks:        agentcore.BlockSet{Text: true, Image: true, Resource: true},
		PromptAccepted:      agentcore.PromptAcceptedNative,
		Signals:             agentcore.SignalSet{Heartbeat: true, ToolLifecycle: true, TerminalResult: true, Usage: true, Plan: true, Subagent: true},
		AuthMethods:         []string{"chatgptAuthTokens", "refresh"},
		SlashCommands:       nativeCodexSlashCommands(),
		SupportsMultiTurn:   true,
		SupportsResume:      true,
		SupportsIdleSuspend: true,
		PendingLimit:        128,
		PayloadLimitBytes:   16 << 20,
		Transport:           agentcore.TransportEvidence{Provider: "codex", Protocol: "app-server-jsonrpc-stdio", Version: strings.TrimSpace(version)},
	}
}

func nativeCodexSlashCommands() []agentcore.SlashCommandInfo {
	return []agentcore.SlashCommandInfo{
		{Name: "init", Description: "Create or update workspace instructions"},
		{Name: "compact", Description: "Compact the current thread"},
		{Name: "review", Description: "Review the working tree"},
		{Name: "review-branch", Description: "Review a branch"},
		{Name: "review-commit", Description: "Review a commit"},
		{Name: "logout", Description: "Sign out of Codex"},
	}
}
