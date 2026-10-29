package session

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"

	agentcore "orrery_v2/internal/agent/core"
)

// WorkspaceBinding is server-owned runtime launch material. Only Ref crosses
// the canonical SessionConfig boundary; paths, environment and credentials stay
// inside the broker and provider resolver.
type WorkspaceBinding struct {
	Ref     string
	Request agentcore.RuntimeRequest
	MCP     []RuntimeMCPBinding
	Launch  RuntimeLaunchBinding
	Auth    RuntimeAuthBinding
}

type WorkspaceBroker struct {
	mu       sync.RWMutex
	bindings map[string]WorkspaceBinding
}

func NewWorkspaceBroker() *WorkspaceBroker {
	return &WorkspaceBroker{bindings: make(map[string]WorkspaceBinding)}
}

func (b *WorkspaceBroker) Bind(ctx context.Context, request agentcore.RuntimeRequest) (WorkspaceBinding, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return WorkspaceBinding{}, err
	}
	if strings.TrimSpace(request.TenantID) == "" || request.RunID == "" || request.AgentID.IsZero() {
		return WorkspaceBinding{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "runtime workspace binding requires tenant, run, and agent identity", false)
	}
	if err := validateBoundWorkspace(request.RuntimeOptions); err != nil {
		return WorkspaceBinding{}, err
	}
	mcp, err := runtimeMCPBindings(request)
	if err != nil {
		return WorkspaceBinding{}, err
	}
	launch, auth, err := runtimeLaunchBinding(request)
	if err != nil {
		return WorkspaceBinding{}, err
	}
	ref := "ws-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	binding := WorkspaceBinding{Ref: ref, Request: cloneRuntimeRequest(request), MCP: cloneRuntimeMCPBindings(mcp), Launch: cloneRuntimeLaunchBinding(launch), Auth: cloneRuntimeAuthBinding(auth)}
	b.mu.Lock()
	if b.bindings == nil {
		b.bindings = make(map[string]WorkspaceBinding)
	}
	b.bindings[ref] = binding
	b.mu.Unlock()
	return WorkspaceBinding{Ref: ref, Request: cloneRuntimeRequest(request), MCP: cloneRuntimeMCPBindings(mcp), Launch: cloneRuntimeLaunchBinding(launch), Auth: cloneRuntimeAuthBinding(auth)}, nil
}

func (b *WorkspaceBroker) Resolve(ref string) (WorkspaceBinding, error) {
	if b == nil || strings.TrimSpace(ref) == "" {
		return WorkspaceBinding{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "runtime workspace reference is invalid", false)
	}
	b.mu.RLock()
	binding, ok := b.bindings[ref]
	b.mu.RUnlock()
	if !ok {
		return WorkspaceBinding{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "runtime workspace reference is unavailable", false)
	}
	return WorkspaceBinding{Ref: binding.Ref, Request: cloneRuntimeRequest(binding.Request), MCP: cloneRuntimeMCPBindings(binding.MCP), Launch: cloneRuntimeLaunchBinding(binding.Launch), Auth: cloneRuntimeAuthBinding(binding.Auth)}, nil
}

func (b *WorkspaceBroker) Release(ref string) {
	if b == nil || strings.TrimSpace(ref) == "" {
		return
	}
	b.mu.Lock()
	delete(b.bindings, ref)
	b.mu.Unlock()
}

// BoundExecutionEnvelope exposes only the authority of a currently bound
// session, including while its provider is opening the MCP connection. Launch
// paths, credentials and environment never leave the broker through this API.
func (b *WorkspaceBroker) BoundExecutionEnvelope(runID agentcore.AgentRunID) (*agentcore.ExecutionEnvelope, *agentcore.ResponsibilityContext, bool) {
	if b == nil || runID == "" {
		return nil, nil, false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	var matched *WorkspaceBinding
	for _, binding := range b.bindings {
		if binding.Request.RunID != runID || binding.Request.ExecutionEnvelope == nil {
			continue
		}
		if matched != nil {
			// An ambiguous run identity must never broaden authority.
			return nil, nil, false
		}
		copy := binding
		matched = &copy
	}
	if matched == nil {
		return nil, nil, false
	}
	return agentcore.CloneExecutionEnvelope(matched.Request.ExecutionEnvelope), agentcore.CloneResponsibilityContext(matched.Request.ContextPack.ResponsibilityContext), true
}

func (b *WorkspaceBroker) Len() int {
	if b == nil {
		return 0
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.bindings)
}

func validateBoundWorkspace(options agentcore.RuntimeOptions) error {
	workDir := strings.TrimSpace(options.WorkingDirectory)
	policy := options.WorkspacePolicy
	if strings.TrimSpace(policy.WorkDir) != "" {
		workDir = strings.TrimSpace(policy.WorkDir)
	}
	if workDir == "" || !filepath.IsAbs(workDir) {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "runtime workspace must resolve to an absolute working directory", false)
	}
	root := strings.TrimSpace(policy.Root)
	if root == "" && !options.SandboxProfile.Empty() {
		root = strings.TrimSpace(options.SandboxProfile.Root)
	}
	if root == "" {
		root = workDir
	}
	if !filepath.IsAbs(root) {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "runtime workspace root must be absolute", false)
	}
	if policy.EnforceBoundary || options.SandboxProfile.EnforceBoundary {
		inside, err := pathWithinRuntimeWorkspace(workDir, root)
		if err != nil || !inside {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeWorkspaceUnavailable, "runtime working directory is outside its authorized workspace", false)
		}
	}
	return nil
}

func pathWithinRuntimeWorkspace(path, root string) (bool, error) {
	cleanPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return false, fmt.Errorf("resolve runtime working directory: %w", err)
	}
	cleanRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return false, fmt.Errorf("resolve runtime workspace root: %w", err)
	}
	relative, err := filepath.Rel(cleanRoot, cleanPath)
	if err != nil {
		return false, err
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)), nil
}

func cloneRuntimeRequest(request agentcore.RuntimeRequest) agentcore.RuntimeRequest {
	out := request
	out.ExecutionEnvelope = agentcore.CloneExecutionEnvelope(request.ExecutionEnvelope)
	out.InputMessages = append([]agentcore.RuntimeChatMessage(nil), request.InputMessages...)
	for index := range out.InputMessages {
		out.InputMessages[index].ToolCalls = append([]agentcore.RuntimeToolCall(nil), request.InputMessages[index].ToolCalls...)
	}
	out.InputMedia = append([]agentcore.ContentBlock(nil), request.InputMedia...)
	for index := range out.InputMedia {
		out.InputMedia[index].Data = append([]byte(nil), request.InputMedia[index].Data...)
	}
	out.ToolBindings = append([]agentcore.RuntimeToolBinding(nil), request.ToolBindings...)
	for index := range out.ToolBindings {
		out.ToolBindings[index].GrantedPermissions = append([]string(nil), request.ToolBindings[index].GrantedPermissions...)
		out.ToolBindings[index].SecretEnv = cloneStringMap(request.ToolBindings[index].SecretEnv)
		out.ToolBindings[index].InputSchema = cloneAnyMap(request.ToolBindings[index].InputSchema)
		out.ToolBindings[index].OutputSchema = cloneAnyMap(request.ToolBindings[index].OutputSchema)
	}
	out.SkillBindings = append([]agentcore.RuntimeSkillBinding(nil), request.SkillBindings...)
	out.RuntimeOptions.ExtraArgs = append([]string(nil), request.RuntimeOptions.ExtraArgs...)
	out.RuntimeOptions.CustomArgs = append([]string(nil), request.RuntimeOptions.CustomArgs...)
	out.RuntimeOptions.Environment = cloneStringMap(request.RuntimeOptions.Environment)
	out.RuntimeOptions.ProviderSpecific = cloneStringMap(request.RuntimeOptions.ProviderSpecific)
	out.RuntimeOptions.ProviderConfig = append([]byte(nil), request.RuntimeOptions.ProviderConfig...)
	out.RuntimeOptions.MCPConfig = append([]byte(nil), request.RuntimeOptions.MCPConfig...)
	out.RuntimeOptions.PromptBlocks = append([]agentcore.RuntimePromptBlock(nil), request.RuntimeOptions.PromptBlocks...)
	out.RuntimeOptions.WorkspacePolicy.WritableRoots = append([]string(nil), request.RuntimeOptions.WorkspacePolicy.WritableRoots...)
	out.RuntimeOptions.WorkspacePolicy.Profile = request.RuntimeOptions.WorkspacePolicy.Profile.Clone()
	out.RuntimeOptions.SandboxProfile = request.RuntimeOptions.SandboxProfile.Clone()
	return out
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	out := make(map[string]string, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

func cloneAnyMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	out := make(map[string]any, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}
