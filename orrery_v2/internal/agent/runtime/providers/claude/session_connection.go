// Historical source: AionCore aionui-session, Copyright 2025 AionUi
// (aionui.com), Apache-2.0. Go adaptation and modifications: Minvia, 2026.
// See docs/runtime/runtime-source-provenance.md for scope and the full license.
package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/process"
)

// Reference: aionui-session/backend/claude_conn.rs@v0.1.52.

const (
	defaultClaudeHandshakeTimeout = 30 * time.Second
	defaultClaudeEventCapacity    = 1024
	defaultClaudeBackpressureWait = 5 * time.Second
	minSupportedClaudePatch       = 168
	maxSupportedClaudePatch       = 220
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
	ResolveWorkspace NativeWorkspaceResolver
	Now              func() time.Time
}

type NativeWorkspaceResolver interface {
	ResolveClaudeWorkspace(context.Context, string) (NativeWorkspace, error)
}

type NativeWorkspaceResolverFunc func(context.Context, string) (NativeWorkspace, error)

func (f NativeWorkspaceResolverFunc) ResolveClaudeWorkspace(ctx context.Context, ref string) (NativeWorkspace, error) {
	return f(ctx, ref)
}

type NativeWorkspace struct {
	Request     agentcore.RuntimeRequest
	Directory   string
	Environment map[string]string
	Launch      NativeLaunch
	MCPServers  map[string]NativeMCPServer
}

type NativeLaunch struct {
	Executable string
	Version    string
	ExtraArgs  []string
}

type NativeMCPServer struct {
	Command     string
	Args        []string
	Environment map[string]string
	URL         string
	Headers     map[string]string
}

type NativeConnection struct {
	config   NativeConfig
	versions map[string]string

	mu       sync.Mutex
	sessions map[agentcore.SessionID]*NativeSession
}

func NewNativeConnection(config NativeConfig) (*NativeConnection, error) {
	config.ExtraArgs = append([]string(nil), config.ExtraArgs...)
	config.Environment = mergeClaudeEnvironment(nil, config.Environment)
	if strings.TrimSpace(config.Executable) == "" {
		config.Executable = "claude"
	}
	if config.HandshakeTimeout <= 0 {
		config.HandshakeTimeout = defaultClaudeHandshakeTimeout
	}
	if config.EventCapacity < 1 {
		config.EventCapacity = defaultClaudeEventCapacity
	}
	if config.BackpressureWait <= 0 {
		config.BackpressureWait = defaultClaudeBackpressureWait
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
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "claude workspace resolver is not configured", false)
	}
	workspace, err := c.config.ResolveWorkspace.ResolveClaudeWorkspace(ctx, config.WorkspaceRef)
	if err != nil {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "claude workspace reference could not be resolved", false)
	}
	if err := validateClaudeWorkspace(workspace, config); err != nil {
		return nil, err
	}
	native := applyClaudeNativeLaunch(c.config, workspace.Launch)
	version, err := c.resolveVersion(ctx, native, workspace.Environment)
	if err != nil {
		return nil, err
	}
	claudeID, fresh, err := claudeSessionID(spec)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	if _, exists := c.sessions[spec.SessionID]; exists {
		c.mu.Unlock()
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "claude logical session is already open", false)
	}
	c.mu.Unlock()

	backend, err := openNativeSession(ctx, native, version, spec, config.Clone(), cloneClaudeWorkspace(workspace), claudeID, fresh)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if _, exists := c.sessions[spec.SessionID]; exists {
		c.mu.Unlock()
		_ = backend.Close(context.Background())
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeCommandConflict, "claude logical session was opened concurrently", false)
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
	return nativeClaudeCapabilities(strings.TrimSpace(c.config.Version))
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
	environment := mergeClaudeEnvironment(native.Environment, workspaceEnvironment)
	identity := process.LaunchIdentity(native.Executable, native.ExtraArgs, environment, native.Version)
	c.mu.Lock()
	version := c.versions[identity]
	c.mu.Unlock()
	if version == "" {
		executable, err := resolveClaudeExecutable(native.Executable)
		if err != nil {
			return "", agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "supported claude executable is unavailable", false)
		}
		detected, err := process.DetectVersionWithEnvironmentTimeout(ctx, executable, environment, native.HandshakeTimeout)
		if err != nil {
			return "", agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "supported claude executable is unavailable", false)
		}
		if claimed := strings.TrimSpace(native.Version); claimed != "" && !sameClaudeVersion(claimed, detected) {
			return "", agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "configured claude version does not match the executable", false)
		}
		version = detected
	}
	if err := validateClaudeVersion(version); err != nil {
		return "", err
	}
	c.mu.Lock()
	c.versions[identity] = version
	c.mu.Unlock()
	return version, nil
}

func sameClaudeVersion(left string, right string) bool {
	leftMatch := claudeVersionPattern.FindStringSubmatch(strings.TrimSpace(left))
	rightMatch := claudeVersionPattern.FindStringSubmatch(strings.TrimSpace(right))
	return len(leftMatch) == 4 && len(rightMatch) == 4 && leftMatch[1] == rightMatch[1] && leftMatch[2] == rightMatch[2] && leftMatch[3] == rightMatch[3]
}

func applyClaudeNativeLaunch(base NativeConfig, launch NativeLaunch) NativeConfig {
	out := base
	out.ExtraArgs = append([]string(nil), base.ExtraArgs...)
	out.Environment = mergeClaudeEnvironment(nil, base.Environment)
	if executable := strings.TrimSpace(launch.Executable); executable != "" {
		out.Executable = executable
	}
	if version := strings.TrimSpace(launch.Version); version != "" {
		out.Version = version
	}
	out.ExtraArgs = append(out.ExtraArgs, launch.ExtraArgs...)
	return out
}

var claudeVersionPattern = regexp.MustCompile(`(?i)(\d+)\.(\d+)\.(\d+)(?:\s+\(Claude Code\))?`)

func validateClaudeVersion(version string) error {
	matches := claudeVersionPattern.FindStringSubmatch(strings.TrimSpace(version))
	if len(matches) != 4 {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "claude version could not be verified", false)
	}
	major, _ := strconv.Atoi(matches[1])
	minor, _ := strconv.Atoi(matches[2])
	patch, _ := strconv.Atoi(matches[3])
	if major != 2 || minor != 1 || patch < minSupportedClaudePatch || patch > maxSupportedClaudePatch {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, fmt.Sprintf("claude version %s is outside the supported 2.1.%d-2.1.%d range", matches[0], minSupportedClaudePatch, maxSupportedClaudePatch), false)
	}
	return nil
}

func claudeSessionID(spec agentcore.SessionSpec) (string, bool, error) {
	if spec.Mode == agentcore.SessionOpenResume && strings.TrimSpace(spec.BackendSessionID) != "" {
		value := strings.TrimSpace(spec.BackendSessionID)
		if _, err := uuid.Parse(value); err != nil {
			return "", false, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "claude backend session id must be a UUID", false)
		}
		return value, false, nil
	}
	logical := strings.TrimSpace(string(spec.SessionID))
	if _, err := uuid.Parse(logical); err == nil {
		return logical, true, nil
	}
	return uuid.NewString(), true, nil
}

func validateClaudeWorkspace(workspace NativeWorkspace, config agentcore.SessionConfig) error {
	if strings.TrimSpace(workspace.Directory) == "" || !filepath.IsAbs(workspace.Directory) {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "resolved claude workspace must be an absolute local directory", false)
	}
	for _, spec := range config.MCPServers {
		resolved, exists := workspace.MCPServers[spec.Name]
		if !exists {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "claude MCP reference is unresolved", false)
		}
		switch spec.Transport {
		case agentcore.MCPTransportStdio:
			if strings.TrimSpace(resolved.Command) == "" || resolved.URL != "" || len(resolved.Headers) != 0 {
				return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "resolved claude stdio MCP binding is invalid", false)
			}
		case agentcore.MCPTransportHTTP, agentcore.MCPTransportSSE:
			if strings.TrimSpace(resolved.URL) == "" || resolved.Command != "" || resolved.URL != spec.Endpoint {
				return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "resolved claude network MCP binding is invalid", false)
			}
			if len(resolved.Headers) != len(spec.HeaderRefs) {
				return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "claude MCP credential reference is unresolved", false)
			}
			for header := range spec.HeaderRefs {
				if strings.TrimSpace(resolved.Headers[header]) == "" {
					return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "claude MCP credential reference is unresolved", false)
				}
			}
		}
	}
	return nil
}

func cloneClaudeWorkspace(source NativeWorkspace) NativeWorkspace {
	out := source
	out.Environment = mergeClaudeEnvironment(nil, source.Environment)
	out.Launch.ExtraArgs = append([]string(nil), source.Launch.ExtraArgs...)
	out.MCPServers = make(map[string]NativeMCPServer, len(source.MCPServers))
	for name, server := range source.MCPServers {
		server.Args = append([]string(nil), server.Args...)
		server.Environment = mergeClaudeEnvironment(nil, server.Environment)
		server.Headers = mergeClaudeEnvironment(nil, server.Headers)
		out.MCPServers[name] = server
	}
	return out
}

func mergeClaudeEnvironment(base, overlay map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(overlay))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range overlay {
		out[key] = value
	}
	return out
}

func nativeClaudeCapabilities(version string) agentcore.Capabilities {
	return agentcore.Capabilities{
		Tier: agentcore.CapabilityParsed,
		SupportedCommands: agentcore.CommandSet{
			Send: true, CancelTurn: true, CancelSession: true,
			AnswerPermission: true, Acknowledge: true,
			SetMode: true, SetModel: true, SetConfigOption: true, QuerySessionInfo: true,
		},
		PromptBlocks:          agentcore.BlockSet{Text: true, Image: true, Resource: true},
		PromptAccepted:        agentcore.PromptAcceptedNative,
		Signals:               agentcore.SignalSet{Heartbeat: true, ToolLifecycle: true, TerminalResult: true, Usage: true, Plan: true, Subagent: true},
		AvailableModes:        claudePermissionModes(),
		AcceptsProactiveInput: true,
		SupportsQueue:         true,
		SupportsMultiTurn:     true,
		SupportsResume:        true,
		SupportsIdleSuspend:   true,
		PendingLimit:          128,
		PayloadLimitBytes:     16 << 20,
		Transport:             agentcore.TransportEvidence{Provider: "claude", Protocol: "stream-json-control-stdio", Version: strings.TrimSpace(version)},
	}
}

func claudePermissionModes() []agentcore.ModeInfo {
	return []agentcore.ModeInfo{
		{ID: "auto", Name: "Auto", Description: "Use Claude's automatic permission policy."},
		{ID: "default", Name: "Default", Description: "Ask before sensitive tool use."},
		{ID: "acceptEdits", Name: "Accept Edits", Description: "Allow file edits while retaining other permission checks."},
		{ID: "plan", Name: "Plan", Description: "Plan without making workspace changes."},
		{ID: "dontAsk", Name: "Don't Ask", Description: "Deny operations that require a prompt."},
		{ID: "bypassPermissions", Name: "Bypass Permissions", Description: "Allow tool use without interactive approval."},
	}
}

func nativeClaudeMCPJSON(config agentcore.SessionConfig, workspace NativeWorkspace) (string, error) {
	if len(config.MCPServers) == 0 {
		return "", nil
	}
	servers := make(map[string]any, len(config.MCPServers))
	for _, declared := range config.MCPServers {
		resolved := workspace.MCPServers[declared.Name]
		switch declared.Transport {
		case agentcore.MCPTransportStdio:
			servers[declared.Name] = map[string]any{"command": resolved.Command, "args": append([]string(nil), resolved.Args...), "env": mergeClaudeEnvironment(nil, resolved.Environment)}
		case agentcore.MCPTransportHTTP, agentcore.MCPTransportSSE:
			transport := "http"
			if declared.Transport == agentcore.MCPTransportSSE {
				transport = "sse"
			}
			headers := make(map[string]string, len(declared.HeaderRefs))
			for header := range declared.HeaderRefs {
				headers[header] = resolved.Headers[header]
			}
			servers[declared.Name] = map[string]any{"type": transport, "url": resolved.URL, "headers": headers}
		}
	}
	encoded, err := json.Marshal(map[string]any{"mcpServers": servers})
	if err != nil {
		return "", agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "claude MCP configuration could not be encoded", false)
	}
	return string(encoded), nil
}
