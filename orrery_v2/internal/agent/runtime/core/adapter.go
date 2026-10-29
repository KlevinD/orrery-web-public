package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/execenv"
)

// PrepareExecOptions resolves the server-owned launch material shared by local
// providers. Native SessionBackend implementations use this directly so
// context, tool, skill, MCP, sandbox, and runtime-home preparation is shared
// by every native SessionBackend implementation.
func PrepareExecOptions(req RuntimeRequest, kind agentcore.RuntimeKind, defaultEnvironment map[string]string) (ExecOptions, error) {
	opts := execOptionsFromRequest(req)
	if responsibility := req.ContextPack.ResponsibilityContext; responsibility != nil && responsibility.RequireExplicitToolScopes {
		if opts.ProviderSpecific == nil {
			opts.ProviderSpecific = map[string]string{}
		}
		opts.ProviderSpecific[agentcore.RuntimeRequireExplicitToolScopesProviderKey] = "true"
	}
	opts.Environment = mergeRuntimeEnv(defaultEnvironment, opts.Environment)
	if err := validateJSONObject(opts.ProviderConfig, "runtime_options.provider_config", kind); err != nil {
		return ExecOptions{}, err
	}
	if err := validateJSONObject(opts.MCPConfig, "runtime_options.mcp_config", kind); err != nil {
		return ExecOptions{}, err
	}
	opts = normalizeSandboxProfile(opts)
	var err error
	opts.MCPConfig, err = mcpConfigForRequest(req, opts, kind)
	if err != nil {
		return ExecOptions{}, err
	}
	if kind == agentcore.RuntimeKindManaged || kind == agentcore.RuntimeKindHuman {
		return opts, nil
	}
	if providerConfigIsRemote(opts.ProviderConfig) {
		return opts, nil
	}
	root, err := execEnvRoot(req, opts)
	if err != nil {
		return ExecOptions{}, err
	}
	env, err := execenv.Prepare(execenv.PrepareRequest{
		Root:           root,
		RunID:          req.RunID,
		AgentID:        req.AgentID,
		Runtime:        string(kind),
		WorkDir:        execEnvWorkDir(opts),
		Context:        req.ContextPack,
		Tools:          req.ToolBindings,
		Skills:         req.SkillBindings,
		MCPConfig:      opts.MCPConfig,
		SandboxProfile: opts.SandboxProfile,
	})
	if err != nil {
		return ExecOptions{}, err
	}
	opts.Environment = mergeRuntimeEnv(opts.Environment, env.RuntimeEnv)
	if opts.Cwd == "" {
		opts.Cwd = env.WorkDir
	}
	if opts.WorkspacePolicy.WorkDir == "" {
		opts.WorkspacePolicy.WorkDir = env.WorkDir
	}
	return opts, nil
}

func validateJSONObject(raw []byte, field string, kind agentcore.RuntimeKind) error {
	if len(raw) == 0 {
		return nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return newRuntimeError(CodeRuntimeInvalid, kind, field, field+" must be a valid JSON object", err)
	}
	if object == nil {
		return newRuntimeError(CodeRuntimeInvalid, kind, field, field+" must be a JSON object", nil)
	}
	return nil
}

func providerConfigIsRemote(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	var cfg struct {
		Mode      string `json:"mode"`
		Transport string `json:"transport"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return false
	}
	mode := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(cfg.Mode), "-", "_"))
	if mode == "" {
		mode = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(cfg.Transport), "-", "_"))
	}
	return strings.HasPrefix(mode, "remote_")
}

func mcpConfigForRequest(req RuntimeRequest, opts ExecOptions, kind agentcore.RuntimeKind) ([]byte, error) {
	if len(opts.MCPConfig) > 0 {
		return bindRuntimeMCPConfig(req, opts, kind, req.ContextPack.ResponsibilityContext != nil)
	}
	url := firstRuntimeEnvValue(opts.Environment, "ORRERY_RUNTIME_MCP_URL", "ORRERY_MCP_URL")
	if url == "" {
		return nil, nil
	}
	name := firstRuntimeEnvValue(opts.Environment, "ORRERY_RUNTIME_MCP_SERVER_NAME", "ORRERY_MCP_SERVER_NAME")
	if name == "" {
		name = "orrery"
	}
	transport := firstRuntimeEnvValue(opts.Environment, "ORRERY_RUNTIME_MCP_TRANSPORT", "ORRERY_MCP_TRANSPORT")
	if transport == "" {
		transport = "streamable-http"
	}
	headers := map[string]string{}
	if value := firstRuntimeEnvValue(opts.Environment, "ORRERY_RUNTIME_MCP_AUTHORIZATION", "ORRERY_MCP_AUTHORIZATION"); value != "" {
		headers["Authorization"] = value
	} else if token := firstRuntimeEnvValue(opts.Environment, "ORRERY_RUNTIME_MCP_TOKEN", "ORRERY_MCP_TOKEN"); token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	if req.RunID != "" {
		headers[agentcore.RuntimeRunIDHeader] = req.RunID.String()
	}
	if req.AgentID != "" {
		headers["X-Agent-ID"] = req.AgentID.String()
	}
	if strings.TrimSpace(req.TenantID) != "" {
		headers["X-Orrery-Tenant"] = strings.TrimSpace(req.TenantID)
	}
	for key, value := range agentcore.RuntimeExecutionPrincipalHeaders(req.ExecutionPrincipal, req.TenantID, req.AgentID) {
		headers[key] = value
	}
	for key, value := range agentcore.ExecutionEnvelopeHeaders(req.ExecutionEnvelope) {
		headers[key] = value
	}
	for key, value := range agentcore.ResponsibilityMCPHeaders(req.ContextPack.ResponsibilityContext) {
		headers[key] = value
	}
	server := map[string]any{
		"transport": transport,
		"url":       url,
	}
	if len(headers) > 0 {
		server["headers"] = headers
	}
	raw, _ := json.Marshal(map[string]any{"servers": map[string]any{name: server}})
	return raw, nil
}

func bindRuntimeMCPConfig(req RuntimeRequest, opts ExecOptions, kind agentcore.RuntimeKind, responsibilityOnly bool) ([]byte, error) {
	var config map[string]any
	if err := json.Unmarshal(opts.MCPConfig, &config); err != nil {
		return nil, newRuntimeError(CodeRuntimeInvalid, kind, "runtime_options.mcp_config", "runtime_options.mcp_config must be a valid JSON object", err)
	}
	expectedURL := firstRuntimeEnvValue(opts.Environment, "ORRERY_RUNTIME_MCP_URL", "ORRERY_MCP_URL")
	if expectedURL == "" {
		if responsibilityOnly {
			return nil, responsibilityMCPConfigError(kind)
		}
		return append([]byte(nil), opts.MCPConfig...), nil
	}
	serverCount := 0
	boundCount := 0
	for _, field := range []string{"mcpServers", "servers"} {
		servers, ok := config[field].(map[string]any)
		if !ok {
			continue
		}
		for name, rawServer := range servers {
			server, ok := rawServer.(map[string]any)
			if !ok {
				if responsibilityOnly {
					return nil, responsibilityMCPConfigError(kind)
				}
				continue
			}
			serverCount++
			if !isOrreryResponsibilityMCPServer(server, expectedURL) {
				if responsibilityOnly {
					return nil, responsibilityMCPConfigError(kind)
				}
				continue
			}
			bindRuntimeMCPServerAuthority(server, req)
			servers[name] = server
			boundCount++
		}
	}
	if serverCount == 0 {
		if !isOrreryResponsibilityMCPServer(config, expectedURL) {
			if responsibilityOnly {
				return nil, responsibilityMCPConfigError(kind)
			}
			return append([]byte(nil), opts.MCPConfig...), nil
		}
		bindRuntimeMCPServerAuthority(config, req)
		serverCount = 1
		boundCount = 1
	}
	if responsibilityOnly && (serverCount != 1 || boundCount != 1) {
		return nil, responsibilityMCPConfigError(kind)
	}
	if boundCount == 0 {
		return append([]byte(nil), opts.MCPConfig...), nil
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return nil, newRuntimeError(CodeRuntimeInvalid, kind, "runtime_options.mcp_config", "runtime_options.mcp_config could not be bound to responsibility authority", err)
	}
	return raw, nil
}

func runtimeMCPIdentityHeaders(req RuntimeRequest) map[string]string {
	return agentcore.RuntimeMCPAuthorityHeaders(req)
}

func isOrreryResponsibilityMCPServer(server map[string]any, expectedURL string) bool {
	endpoint := firstMCPServerString(server, "url", "endpoint")
	if endpoint == "" || expectedURL == "" {
		return false
	}
	return strings.TrimRight(endpoint, "/") == strings.TrimRight(expectedURL, "/")
}

func firstMCPServerString(server map[string]any, names ...string) string {
	for _, name := range names {
		if value, ok := server[name].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func mergeRuntimeMCPHeaders(server map[string]any, headers map[string]string) {
	merged, _ := server["headers"].(map[string]any)
	if merged == nil {
		merged = map[string]any{}
	}
	for key, value := range headers {
		merged[key] = value
	}
	server["headers"] = merged
}

func bindRuntimeMCPServerAuthority(server map[string]any, req RuntimeRequest) {
	for _, field := range []string{"headers", "http_headers"} {
		headers, _ := server[field].(map[string]any)
		for key := range headers {
			if agentcore.IsRuntimeMCPAuthorityHeader(strings.TrimSpace(key)) {
				delete(headers, key)
			}
		}
		if headers != nil {
			server[field] = headers
		}
	}
	mergeRuntimeMCPHeaders(server, runtimeMCPIdentityHeaders(req))
}

func responsibilityMCPConfigError(kind agentcore.RuntimeKind) error {
	return newRuntimeError(CodeRuntimeInvalid, kind, "runtime_options.mcp_config", "responsibility runs may use only the server-owned Orrery MCP gateway", nil)
}

func firstRuntimeEnvValue(env map[string]string, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(env[name]); value != "" {
			return value
		}
	}
	return ""
}

func execOptionsFromRequest(req RuntimeRequest) ExecOptions {
	return ExecOptions{
		TenantID:                  req.TenantID,
		RunID:                     req.RunID,
		AgentID:                   req.AgentID,
		ExecutionEnvelope:         agentcore.CloneExecutionEnvelope(req.ExecutionEnvelope),
		ExecutionPrincipal:        req.ExecutionPrincipal,
		ContextPack:               req.ContextPack,
		Cwd:                       req.RuntimeOptions.WorkingDirectory,
		Model:                     req.RuntimeOptions.Model,
		SystemPrompt:              req.RuntimeOptions.SystemPrompt,
		MaxTurns:                  req.RuntimeOptions.MaxTurns,
		Timeout:                   req.RuntimeOptions.Timeout,
		SemanticInactivityTimeout: req.RuntimeOptions.SemanticInactivityTimeout,
		ResumeSessionID:           req.RuntimeOptions.ResumeSessionID,
		ExtraArgs:                 append([]string(nil), req.RuntimeOptions.ExtraArgs...),
		CustomArgs:                append([]string(nil), req.RuntimeOptions.CustomArgs...),
		ProviderConfig:            append([]byte(nil), req.RuntimeOptions.ProviderConfig...),
		ProviderSpecific:          cloneRuntimeEnv(req.RuntimeOptions.ProviderSpecific),
		MCPConfig:                 append([]byte(nil), req.RuntimeOptions.MCPConfig...),
		Environment:               cloneRuntimeEnv(req.RuntimeOptions.Environment),
		ToolBindings:              cloneRuntimeToolBindings(req.ToolBindings),
		SkillBindings:             cloneRuntimeSkillBindings(req.SkillBindings),
		SandboxProfile:            req.RuntimeOptions.SandboxProfile.Clone(),
		WorkspacePolicy:           cloneWorkspacePolicy(req.RuntimeOptions.WorkspacePolicy),
	}
}

func normalizeSandboxProfile(opts ExecOptions) ExecOptions {
	if opts.SandboxProfile.Empty() {
		opts.SandboxProfile = agentcore.SandboxProfileFromWorkspacePolicy(opts.WorkspacePolicy)
	} else {
		opts.SandboxProfile = opts.SandboxProfile.Clone()
		profilePolicy := opts.SandboxProfile.WorkspacePolicy()
		opts.WorkspacePolicy = mergeWorkspacePolicy(opts.WorkspacePolicy, profilePolicy)
	}
	if !opts.SandboxProfile.Empty() {
		opts.WorkspacePolicy.Profile = opts.SandboxProfile.Clone()
	}
	return opts
}

func mergeWorkspacePolicy(base, overlay agentcore.RuntimeWorkspacePolicy) agentcore.RuntimeWorkspacePolicy {
	out := cloneWorkspacePolicy(base)
	if strings.TrimSpace(overlay.Root) != "" {
		out.Root = overlay.Root
	}
	if strings.TrimSpace(overlay.WorkDir) != "" {
		out.WorkDir = overlay.WorkDir
	}
	if len(overlay.WritableRoots) > 0 {
		out.WritableRoots = append([]string(nil), overlay.WritableRoots...)
	}
	if strings.TrimSpace(overlay.SandboxMode) != "" {
		out.SandboxMode = overlay.SandboxMode
	}
	if strings.TrimSpace(overlay.ApprovalPolicy) != "" {
		out.ApprovalPolicy = overlay.ApprovalPolicy
	}
	out.EnforceBoundary = out.EnforceBoundary || overlay.EnforceBoundary
	if !overlay.Profile.Empty() {
		out.Profile = overlay.Profile.Clone()
	}
	return out
}

func execEnvRoot(req RuntimeRequest, opts ExecOptions) (string, error) {
	if value := strings.TrimSpace(opts.Environment["ORRERY_EXECENV_ROOT"]); value != "" {
		return value, nil
	}
	workDir := execEnvWorkDir(opts)
	if strings.TrimSpace(workDir) != "" {
		return filepath.Join(workDir, ".orrery", "execenv"), nil
	}
	base := strings.TrimSpace(req.MissionID)
	if base == "" {
		base = strings.TrimSpace(string(req.ContextPack.MissionID))
	}
	if base == "" {
		base = "runtime"
	}
	return filepath.Join(os.TempDir(), "orrery-execenv", safeExecEnvSegment(base)), nil
}

func execEnvWorkDir(opts ExecOptions) string {
	if strings.TrimSpace(opts.WorkspacePolicy.WorkDir) != "" {
		return strings.TrimSpace(opts.WorkspacePolicy.WorkDir)
	}
	return strings.TrimSpace(opts.Cwd)
}

func mergeRuntimeEnv(base, overlay map[string]string) map[string]string {
	out := cloneRuntimeEnv(base)
	if out == nil {
		out = map[string]string{}
	}
	for key, value := range overlay {
		if strings.TrimSpace(key) != "" {
			out[key] = value
		}
	}
	return out
}

func safeExecEnvSegment(value string) string {
	value = strings.TrimSpace(value)
	value = strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(value)
	if value == "" || value == "." || value == ".." {
		return "runtime"
	}
	return value
}

func cloneWorkspacePolicy(src agentcore.RuntimeWorkspacePolicy) agentcore.RuntimeWorkspacePolicy {
	src.WritableRoots = append([]string(nil), src.WritableRoots...)
	src.Profile = src.Profile.Clone()
	return src
}

func cloneRuntimeToolBindings(src []agentcore.RuntimeToolBinding) []agentcore.RuntimeToolBinding {
	if len(src) == 0 {
		return nil
	}
	out := make([]agentcore.RuntimeToolBinding, len(src))
	copy(out, src)
	for i := range out {
		out[i].GrantedPermissions = append([]string(nil), out[i].GrantedPermissions...)
		out[i].SecretEnv = cloneRuntimeEnv(out[i].SecretEnv)
		out[i].InputSchema = cloneAnyMap(out[i].InputSchema)
		out[i].OutputSchema = cloneAnyMap(out[i].OutputSchema)
	}
	return out
}

func cloneRuntimeSkillBindings(src []agentcore.RuntimeSkillBinding) []agentcore.RuntimeSkillBinding {
	if len(src) == 0 {
		return nil
	}
	out := make([]agentcore.RuntimeSkillBinding, len(src))
	copy(out, src)
	return out
}

func cloneAnyMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func cloneRuntimeEnv(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
