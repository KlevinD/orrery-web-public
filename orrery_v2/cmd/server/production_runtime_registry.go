package main

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/llmprofile"
	agentproviders "orrery_v2/internal/agent/runtime/providers"
	claudeprovider "orrery_v2/internal/agent/runtime/providers/claude"
	codexprovider "orrery_v2/internal/agent/runtime/providers/codex"
	deviceprovider "orrery_v2/internal/agent/runtime/providers/device"
	openclawprovider "orrery_v2/internal/agent/runtime/providers/openclaw"
	piprovider "orrery_v2/internal/agent/runtime/providers/pi"
	runtimesession "orrery_v2/internal/agent/runtime/session"
	"orrery_v2/internal/platform/config"
)

func newProductionRuntimeRegistry(deps productionDiscussionAgentDependencies) *runtimesession.ConnectionRegistry {
	if deps.RuntimeRegistry != nil {
		return deps.RuntimeRegistry
	}
	approvalBroker := deps.ApprovalBroker
	if approvalBroker == nil {
		approvalBroker = runtimecore.DeferredApprovalBroker{}
	}
	workspaces := runtimesession.NewWorkspaceBroker()
	registry, err := agentproviders.NewSessionRegistry(agentproviders.SessionRegistryConfig{
		Workspaces:    workspaces,
		ManagedKernel: productionAgentCoreConnectionConfig(deps, workspaces),
		NativeClaude:  claudeprovider.NativeConfig{Executable: productionClaudeExecutable(), ToolDispatcher: deps.ToolDispatcher},
		NativeCodex:   codexprovider.NativeConfig{ToolDispatcher: deps.ToolDispatcher},
		NativePi:      agentprovidersPiNativeConfig(deps),
		ModelResolver: productionModelRouter{profiles: deps.LLMProfiles},
		OpenClaw:      agentprovidersOpenClawConnectionConfig(approvalBroker),
		Device:        deviceRuntimeConnectionConfig(deps.DeviceBroker),
		Authorizer:    productionRuntimeAuthorizer{}, PermissionHandler: runtimesession.ApprovalPermissionHandler{Broker: approvalBroker},
		AuthHandler:        runtimesession.CredentialAuthHandler{Broker: productionRuntimeCredentialBroker{secrets: deps.Secrets}},
		DefaultEnvironment: productionRuntimeEnvironment(deps.Config),
	})
	if err != nil {
		panic(err)
	}
	return registry
}

func agentprovidersPiNativeConfig(deps productionDiscussionAgentDependencies) piprovider.NativeConfig {
	return piprovider.NativeConfig{Runtime: piprovider.Config{
		Executable:     productionPiExecutable(),
		ToolDispatcher: deps.ToolDispatcher,
		SecretResolver: deps.Secrets,
		LLMProfiles:    deps.LLMProfiles,
	}}
}

func productionPiExecutable() string {
	root := strings.TrimSpace(os.Getenv("ORRERY_BASE_DIR"))
	if root == "" {
		return ""
	}
	binDir := filepath.Join(root, "runtime", "pi-coding-agent", "node_modules", ".bin")
	for _, name := range []string{"pi", "pi.cmd", "pi.exe"} {
		path := filepath.Join(binDir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}

func agentprovidersOpenClawConnectionConfig(approvalBroker runtimecore.ApprovalBroker) openclawprovider.ConnectionConfig {
	return openclawprovider.ConnectionConfig{Runtime: openclawprovider.Config{ApprovalBroker: approvalBroker}}
}

func deviceRuntimeConnectionConfig(broker deviceprovider.DeviceWorkOrderBroker) deviceprovider.ConnectionConfig {
	if broker == nil {
		return deviceprovider.ConnectionConfig{}
	}
	return deviceprovider.ConnectionConfig{Broker: broker, DefaultDeadline: 30 * time.Minute, CloseBroker: true}
}

type productionRuntimeAuthorizer struct{}

func (productionRuntimeAuthorizer) AuthorizeRuntime(ctx context.Context, request runtimesession.RuntimeAuthorization) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(request.TenantID) == "" || request.RunID == "" || request.AgentID.IsZero() || !request.RuntimeKind.IsValid() {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "production runtime authorization identity is incomplete", false)
	}
	if request.ContextAgentID.IsZero() || request.ContextAgentID != request.AgentID {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeSessionMismatch, "production runtime context agent does not match the executing agent", false)
	}
	if request.MissionID != "" && request.ContextMissionID != "" && request.MissionID != request.ContextMissionID {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeSessionMismatch, "production runtime mission scope is inconsistent", false)
	}
	if request.TaskID != "" && request.ContextTaskID != "" && request.TaskID != request.ContextTaskID {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeSessionMismatch, "production runtime task scope is inconsistent", false)
	}
	return nil
}

type productionRuntimeCredentialBroker struct {
	secrets llmprofile.SecretResolver
}

func (b productionRuntimeCredentialBroker) ResolveRuntimeCredential(ctx context.Context, tenantID string, ref string) (json.RawMessage, error) {
	if b.secrets == nil || !runtimeCredentialRefBelongsToTenant(ref, tenantID) {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "tenant-scoped runtime credential is unavailable", false)
	}
	value, err := b.secrets.ResolveSecret(ctx, ref)
	if err != nil {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "tenant-scoped runtime credential is unavailable", false)
	}
	return json.RawMessage(strings.TrimSpace(value)), nil
}

func runtimeCredentialRefBelongsToTenant(ref string, tenantID string) bool {
	parsed, err := url.Parse(strings.TrimSpace(ref))
	if err != nil || (parsed.Scheme != "secret" && parsed.Scheme != "settings") || parsed.Host != "tenants" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(segments) < 2 {
		return false
	}
	boundTenant, err := url.PathUnescape(segments[0])
	return err == nil && strings.TrimSpace(boundTenant) == strings.TrimSpace(tenantID) && strings.TrimSpace(segments[1]) != ""
}

func productionRuntimeEnvironment(cfg config.Config) map[string]string {
	environment := map[string]string{
		"ORRERY_EXECENV_ROOT":            strings.TrimSpace(cfg.Runtime.ExecEnvRoot),
		"ORRERY_RUNTIME_MCP_URL":         strings.TrimSpace(cfg.Runtime.MCP.URL),
		"ORRERY_RUNTIME_MCP_SERVER_NAME": strings.TrimSpace(cfg.Runtime.MCP.ServerName),
		"ORRERY_RUNTIME_MCP_TRANSPORT":   strings.TrimSpace(cfg.Runtime.MCP.Transport),
	}
	for _, name := range []string{"ORRERY_RUNTIME_MCP_TOKEN", "ORRERY_RUNTIME_MCP_AUTHORIZATION"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			environment[name] = value
		}
	}
	if _, hasToken := environment["ORRERY_RUNTIME_MCP_TOKEN"]; !hasToken {
		if _, hasAuthorization := environment["ORRERY_RUNTIME_MCP_AUTHORIZATION"]; !hasAuthorization {
			if authorization := productionAuthorizationFromEnv("ORRERY_RUNTIME_MCP_AUTHORIZATION", "ORRERY_RUNTIME_MCP_TOKEN"); authorization != "" {
				environment["ORRERY_RUNTIME_MCP_AUTHORIZATION"] = authorization
			}
		}
	}
	return environment
}

func productionAuthorizationFromEnv(authorizationEnv, tokenEnv string) string {
	if authorization := productionSecretFromEnv(authorizationEnv); authorization != "" {
		return authorization
	}
	if token := productionSecretFromEnv(tokenEnv); token != "" {
		return "Bearer " + token
	}
	return ""
}

func productionSecretFromEnv(name string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	path := strings.TrimSpace(os.Getenv(name + "_FILE"))
	if path == "" {
		return ""
	}
	value, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(value))
}
