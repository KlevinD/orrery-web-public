package providers

import (
	"context"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/providers/acp"
	"orrery_v2/internal/agent/runtime/providers/claude"
	"orrery_v2/internal/agent/runtime/providers/codex"
	deviceprovider "orrery_v2/internal/agent/runtime/providers/device"
	"orrery_v2/internal/agent/runtime/providers/kernelcompat"
	"orrery_v2/internal/agent/runtime/providers/openclaw"
	"orrery_v2/internal/agent/runtime/providers/pi"
	runtimesession "orrery_v2/internal/agent/runtime/session"
)

type SessionRegistryConfig struct {
	ManagedKernel      *kernelcompat.Config
	NativeCodex        codex.NativeConfig
	NativeClaude       claude.NativeConfig
	NativePi           pi.NativeConfig
	OpenClaw           openclaw.ConnectionConfig
	ACP                acp.Config
	Device             deviceprovider.ConnectionConfig
	Workspaces         *runtimesession.WorkspaceBroker
	Authorizer         runtimesession.RuntimeAuthorizer
	ModelResolver      runtimesession.RuntimeModelResolver
	PermissionHandler  agentcore.SessionPermissionHandler
	AuthHandler        agentcore.SessionAuthHandler
	DefaultEnvironment map[string]string
}

// NewSessionRegistry registers every migrated native provider and explicitly
// marks all remaining runtime kinds unavailable. There is no legacy fallback.
func NewSessionRegistry(config SessionRegistryConfig) (*runtimesession.ConnectionRegistry, error) {
	registry := runtimesession.NewConnectionRegistryWithOptions(runtimesession.ConnectionRegistryOptions{Workspaces: config.Workspaces, Authorizer: config.Authorizer, ModelResolver: config.ModelResolver, PermissionHandler: config.PermissionHandler, AuthHandler: config.AuthHandler, DefaultEnvironment: config.DefaultEnvironment})
	if config.NativeCodex.ResolveWorkspace == nil {
		config.NativeCodex.ResolveWorkspace = codex.WorkspaceResolverFunc(func(ctx context.Context, ref string) (codex.NativeWorkspace, error) {
			return nativeCodexWorkspace(ctx, registry.Workspaces(), ref)
		})
	}
	codexConnection, err := codex.NewNativeConnection(config.NativeCodex)
	if err != nil {
		return nil, err
	}
	if err := registry.Register(agentcore.RuntimeKindCodex, codexConnection); err != nil {
		_ = codexConnection.Close()
		return nil, err
	}
	if config.NativeClaude.ResolveWorkspace == nil {
		config.NativeClaude.ResolveWorkspace = claude.NativeWorkspaceResolverFunc(func(ctx context.Context, ref string) (claude.NativeWorkspace, error) {
			return nativeClaudeWorkspace(ctx, registry.Workspaces(), ref)
		})
	}
	claudeConnection, err := claude.NewNativeConnection(config.NativeClaude)
	if err != nil {
		_ = registry.Close()
		return nil, err
	}
	if err := registry.Register(agentcore.RuntimeKindClaude, claudeConnection); err != nil {
		_ = claudeConnection.Close()
		_ = registry.Close()
		return nil, err
	}
	if config.NativePi.ResolveWorkspace == nil {
		config.NativePi.ResolveWorkspace = pi.NativeWorkspaceResolverFunc(func(ctx context.Context, ref string) (pi.NativeWorkspace, error) {
			return nativePiWorkspace(ctx, registry.Workspaces(), ref)
		})
	}
	piConnection, err := pi.NewNativeConnection(config.NativePi)
	if err != nil {
		_ = registry.Close()
		return nil, err
	}
	if err := registry.Register(agentcore.RuntimeKindPi, piConnection); err != nil {
		_ = piConnection.Close()
		_ = registry.Close()
		return nil, err
	}
	if config.OpenClaw.ResolveWorkspace == nil {
		config.OpenClaw.ResolveWorkspace = openclaw.WorkspaceResolverFunc(func(ctx context.Context, ref string) (openclaw.NativeWorkspace, error) {
			return nativeOpenClawWorkspace(ctx, registry.Workspaces(), ref)
		})
	}
	openClawConnection, err := openclaw.NewConnection(config.OpenClaw)
	if err != nil {
		_ = registry.Close()
		return nil, err
	}
	if err := registry.Register(agentcore.RuntimeKindOpenClaw, openClawConnection); err != nil {
		_ = openClawConnection.Close()
		_ = registry.Close()
		return nil, err
	}
	if config.ACP.ResolveWorkspace == nil {
		config.ACP.ResolveWorkspace = acp.WorkspaceResolverFunc(func(ctx context.Context, ref string) (acp.Workspace, error) {
			return nativeACPWorkspace(ctx, registry.Workspaces(), ref)
		})
	}
	acpConnection, err := acp.NewConnection(config.ACP)
	if err != nil {
		_ = registry.Close()
		return nil, err
	}
	if err := registry.Register(agentcore.RuntimeKindACP, acpConnection); err != nil {
		_ = acpConnection.Close()
		_ = registry.Close()
		return nil, err
	}
	if config.ManagedKernel != nil {
		managedConnection, err := kernelcompat.NewConnection(*config.ManagedKernel)
		if err != nil {
			_ = registry.Close()
			return nil, err
		}
		if err := registry.Register(agentcore.RuntimeKindManaged, managedConnection); err != nil {
			_ = managedConnection.Close()
			_ = registry.Close()
			return nil, err
		}
	}
	if config.Device.Broker != nil {
		connection, err := deviceprovider.NewConnection(config.Device)
		if err != nil {
			_ = registry.Close()
			return nil, err
		}
		if err := registry.Register(agentcore.RuntimeKindDevice, connection); err != nil {
			_ = connection.Close()
			_ = registry.Close()
			return nil, err
		}
	}
	for _, kind := range []agentcore.RuntimeKind{
		agentcore.RuntimeKindGemini,
		agentcore.RuntimeKindCursor,
		agentcore.RuntimeKindOpenCode,
		agentcore.RuntimeKindHermes,
		agentcore.RuntimeKindHuman,
		agentcore.RuntimeKindHTTP,
		agentcore.RuntimeKindBrowser,
		agentcore.RuntimeKindManaged,
	} {
		if kind == agentcore.RuntimeKindManaged && config.ManagedKernel != nil {
			continue
		}
		if err := registry.RegisterUnavailable(kind, "runtime provider has not completed native SessionBackend conformance"); err != nil {
			_ = registry.Close()
			return nil, err
		}
	}
	if config.Device.Broker == nil {
		if err := registry.RegisterUnavailable(agentcore.RuntimeKindDevice, "device runtime requires a durable DeviceWorkOrderBroker"); err != nil {
			_ = registry.Close()
			return nil, err
		}
	}
	return registry, nil
}

func nativeOpenClawWorkspace(ctx context.Context, workspaces *runtimesession.WorkspaceBroker, ref string) (openclaw.NativeWorkspace, error) {
	if err := ctx.Err(); err != nil {
		return openclaw.NativeWorkspace{}, err
	}
	binding, err := workspaces.Resolve(ref)
	if err != nil {
		return openclaw.NativeWorkspace{}, err
	}
	return openclaw.NativeWorkspace{Request: binding.Request, Version: binding.Launch.Version}, nil
}

func nativePiWorkspace(ctx context.Context, workspaces *runtimesession.WorkspaceBroker, ref string) (pi.NativeWorkspace, error) {
	if err := ctx.Err(); err != nil {
		return pi.NativeWorkspace{}, err
	}
	binding, err := workspaces.Resolve(ref)
	if err != nil {
		return pi.NativeWorkspace{}, err
	}
	return pi.NativeWorkspace{Request: binding.Request, Version: binding.Launch.Version}, nil
}

func nativeACPWorkspace(ctx context.Context, workspaces *runtimesession.WorkspaceBroker, ref string) (acp.Workspace, error) {
	if err := ctx.Err(); err != nil {
		return acp.Workspace{}, err
	}
	binding, err := workspaces.Resolve(ref)
	if err != nil {
		return acp.Workspace{}, err
	}
	directory := strings.TrimSpace(binding.Request.RuntimeOptions.WorkspacePolicy.WorkDir)
	if directory == "" {
		directory = strings.TrimSpace(binding.Request.RuntimeOptions.WorkingDirectory)
	}
	servers := make([]acp.MCPServer, 0, len(binding.MCP))
	for _, resolved := range binding.MCP {
		servers = append(servers, acp.MCPServer{
			Name: resolved.Spec.Name, Transport: resolved.Spec.Transport,
			Command: resolved.Command, Args: append([]string(nil), resolved.Args...), Environment: cloneProviderEnvironment(resolved.Environment),
			URL: resolved.URL, Headers: cloneProviderEnvironment(resolved.Headers),
		})
	}
	return acp.Workspace{
		Directory: directory, Executable: binding.Launch.Executable, Args: append([]string(nil), binding.Launch.ExtraArgs...),
		Environment: cloneProviderEnvironment(binding.Launch.Environment), Version: binding.Launch.Version, MCPServers: servers,
	}, nil
}

func nativeCodexWorkspace(ctx context.Context, workspaces *runtimesession.WorkspaceBroker, ref string) (codex.NativeWorkspace, error) {
	if err := ctx.Err(); err != nil {
		return codex.NativeWorkspace{}, err
	}
	binding, err := workspaces.Resolve(ref)
	if err != nil {
		return codex.NativeWorkspace{}, err
	}
	request := binding.Request
	policy := request.RuntimeOptions.WorkspacePolicy
	directory := strings.TrimSpace(policy.WorkDir)
	if directory == "" {
		directory = strings.TrimSpace(request.RuntimeOptions.WorkingDirectory)
	}
	sandbox := strings.TrimSpace(policy.SandboxMode)
	if sandbox == "" {
		sandbox = string(request.RuntimeOptions.SandboxProfile.Mode)
	}
	approval := strings.TrimSpace(policy.ApprovalPolicy)
	if approval == "" {
		approval = string(request.RuntimeOptions.SandboxProfile.Approval)
	}
	mcpServers := make(map[string]codex.NativeMCPServer, len(binding.MCP))
	for _, binding := range binding.MCP {
		mcpServers[binding.Spec.Name] = codex.NativeMCPServer{
			Command: binding.Command, Args: append([]string(nil), binding.Args...), Environment: cloneProviderEnvironment(binding.Environment),
			URL: binding.URL, BearerTokenEnvVar: binding.BearerTokenEnvVar, HTTPHeaders: cloneProviderEnvironment(binding.Headers),
		}
	}
	return codex.NativeWorkspace{
		Request:   request,
		Directory: directory, Environment: cloneProviderEnvironment(binding.Launch.Environment), Sandbox: sandbox, ApprovalPolicy: approval,
		Root: policy.Root, EnforceBoundary: policy.EnforceBoundary,
		WritableRoots: append([]string(nil), policy.WritableRoots...),
		Launch:        codex.NativeLaunch{Executable: binding.Launch.Executable, Version: binding.Launch.Version, ExtraArgs: append([]string(nil), binding.Launch.ExtraArgs...)},
		MCPServers:    mcpServers,
	}, nil
}

func nativeClaudeWorkspace(ctx context.Context, workspaces *runtimesession.WorkspaceBroker, ref string) (claude.NativeWorkspace, error) {
	if err := ctx.Err(); err != nil {
		return claude.NativeWorkspace{}, err
	}
	binding, err := workspaces.Resolve(ref)
	if err != nil {
		return claude.NativeWorkspace{}, err
	}
	request := binding.Request
	directory := strings.TrimSpace(request.RuntimeOptions.WorkspacePolicy.WorkDir)
	if directory == "" {
		directory = strings.TrimSpace(request.RuntimeOptions.WorkingDirectory)
	}
	mcpServers := make(map[string]claude.NativeMCPServer, len(binding.MCP))
	for _, binding := range binding.MCP {
		mcpServers[binding.Spec.Name] = claude.NativeMCPServer{
			Command: binding.Command, Args: append([]string(nil), binding.Args...), Environment: cloneProviderEnvironment(binding.Environment),
			URL: binding.URL, Headers: cloneProviderEnvironment(binding.Headers),
		}
	}
	return claude.NativeWorkspace{
		Request:   request,
		Directory: directory, Environment: cloneProviderEnvironment(binding.Launch.Environment),
		Launch:     claude.NativeLaunch{Executable: binding.Launch.Executable, Version: binding.Launch.Version, ExtraArgs: append([]string(nil), binding.Launch.ExtraArgs...)},
		MCPServers: mcpServers,
	}, nil
}

func cloneProviderEnvironment(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	out := make(map[string]string, len(source))
	for key, value := range source {
		if strings.TrimSpace(key) != "" {
			out[key] = value
		}
	}
	return out
}
