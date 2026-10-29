package core

import (
	"encoding/json"
	"strings"
	"time"
)

type ExecutionPrincipalKind string

const (
	ExecutionPrincipalUser   ExecutionPrincipalKind = "user"
	ExecutionPrincipalAgent  ExecutionPrincipalKind = "agent"
	ExecutionPrincipalSystem ExecutionPrincipalKind = "system"
)

const (
	RuntimePrincipalIDHeader     = "X-Orrery-Principal-ID"
	RuntimePrincipalKindHeader   = "X-Orrery-Principal-Kind"
	RuntimePermissionScopeHeader = "X-Orrery-Permission-Scope"
	RuntimeExecutionSourceHeader = "X-Orrery-Execution-Source"
	// RuntimeRequireExplicitToolScopesProviderKey is set by the trusted runtime
	// adapter from a responsibility context. Providers use it to disable native
	// tool surfaces that cannot enforce task-scoped authorization themselves.
	RuntimeRequireExplicitToolScopesProviderKey = "responsibility_require_explicit_tool_scopes"
	// RuntimeConversationHILModeProviderKey is set by the trusted discussion
	// runtime so providers can preserve an explicit conversation approval gate.
	RuntimeConversationHILModeProviderKey = "conversation_hil_mode"
)

type ExecutionPrincipal struct {
	Kind            ExecutionPrincipalKind `json:"kind"`
	ID              string                 `json:"id"`
	TenantID        string                 `json:"tenant_id"`
	Source          string                 `json:"source,omitempty"`
	PermissionScope []string               `json:"permission_scope,omitempty"`
}

func NormalizeExecutionPrincipal(principal ExecutionPrincipal, tenantID string, agentID AgentID) ExecutionPrincipal {
	tenantID = strings.TrimSpace(tenantID)
	principal.ID = strings.TrimSpace(principal.ID)
	principal.TenantID = strings.TrimSpace(principal.TenantID)
	principal.Source = strings.TrimSpace(principal.Source)
	principal.PermissionScope = normalizeExecutionPermissionScopes(principal.PermissionScope)
	if principal.ID == "" {
		principal.Kind = ExecutionPrincipalAgent
		principal.ID = strings.TrimSpace(agentID.String())
	}
	if principal.Kind != ExecutionPrincipalUser && principal.Kind != ExecutionPrincipalAgent && principal.Kind != ExecutionPrincipalSystem {
		principal.Kind = ExecutionPrincipalAgent
	}
	if tenantID != "" {
		principal.TenantID = tenantID
	}
	return principal
}

func RuntimeExecutionPrincipalHeaders(principal ExecutionPrincipal, tenantID string, agentID AgentID) map[string]string {
	principal = NormalizeExecutionPrincipal(principal, tenantID, agentID)
	headers := map[string]string{
		RuntimePrincipalIDHeader:   principal.ID,
		RuntimePrincipalKindHeader: string(principal.Kind),
	}
	if principal.Source != "" {
		headers[RuntimeExecutionSourceHeader] = principal.Source
	}
	if len(principal.PermissionScope) > 0 {
		headers[RuntimePermissionScopeHeader] = strings.Join(principal.PermissionScope, ",")
	}
	return headers
}

func normalizeExecutionPermissionScopes(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

// RuntimeRequest carries the server-owned execution envelope alongside the
// legacy context projection consumed by existing providers.
type RuntimeRequest struct {
	TenantID           string
	RunID              AgentRunID
	AgentID            AgentID
	ExecutionEnvelope  *ExecutionEnvelope
	ExecutionPrincipal ExecutionPrincipal
	MissionID          string
	TaskID             string
	ContextPack        ContextPack
	InputMessages      []RuntimeChatMessage
	InputMedia         []ContentBlock
	ToolBindings       []RuntimeToolBinding
	SkillBindings      []RuntimeSkillBinding
	RuntimeOptions     RuntimeOptions
	Deadline           time.Time
	TracePolicy        TracePolicy
}

type RuntimeToolBinding struct {
	ToolID             ToolID
	Alias              string
	Version            string
	GrantedPermissions []string
	SecretEnv          map[string]string
	Source             string
	ServerToolID       string
	DownstreamToolName string
	Summary            string
	InputSchema        map[string]any
	OutputSchema       map[string]any
}

type RuntimeSkillBinding struct {
	SkillID       string
	Alias         string
	Version       string
	Summary       string
	PackageDigest string
}

type RuntimeOptions struct {
	WorkingDirectory          string
	Model                     string
	SystemPrompt              string
	SystemContextPrompt       string
	PromptBlocks              []RuntimePromptBlock
	MaxTurns                  int
	Timeout                   time.Duration
	SemanticInactivityTimeout time.Duration
	ResumeSessionID           string
	ExtraArgs                 []string
	CustomArgs                []string
	Environment               map[string]string
	ProviderSpecific          map[string]string
	ProviderConfig            json.RawMessage
	MCPConfig                 json.RawMessage
	SandboxProfile            RuntimeSandboxProfile
	WorkspacePolicy           RuntimeWorkspacePolicy
}

type RuntimeWorkspacePolicy struct {
	Root            string
	WorkDir         string
	WritableRoots   []string
	SandboxMode     string
	ApprovalPolicy  string
	EnforceBoundary bool
	Profile         RuntimeSandboxProfile
}

type RuntimeSandboxMode string

const (
	RuntimeSandboxDefault        RuntimeSandboxMode = ""
	RuntimeSandboxReadOnly       RuntimeSandboxMode = "read-only"
	RuntimeSandboxWorkspaceWrite RuntimeSandboxMode = "workspace-write"
	RuntimeSandboxFullAccess     RuntimeSandboxMode = "full-access"
)

type RuntimeApprovalMode string

const (
	RuntimeApprovalDefault   RuntimeApprovalMode = ""
	RuntimeApprovalNever     RuntimeApprovalMode = "never"
	RuntimeApprovalOnRequest RuntimeApprovalMode = "on-request"
	RuntimeApprovalAuto      RuntimeApprovalMode = "auto"
)

type RuntimeNetworkMode string

const (
	RuntimeNetworkDefault  RuntimeNetworkMode = ""
	RuntimeNetworkDisabled RuntimeNetworkMode = "disabled"
	RuntimeNetworkAllowed  RuntimeNetworkMode = "allowed"
)

type RuntimeHomeIsolation string

const (
	RuntimeHomeIsolationDefault RuntimeHomeIsolation = ""
	RuntimeHomeShared           RuntimeHomeIsolation = "shared"
	RuntimeHomeIsolatedPerRun   RuntimeHomeIsolation = "per-run"
)

type RuntimeRetentionPolicy string

const (
	RuntimeRetentionDefault       RuntimeRetentionPolicy = ""
	RuntimeRetentionCleanup       RuntimeRetentionPolicy = "cleanup"
	RuntimeRetentionKeepAll       RuntimeRetentionPolicy = "keep-all"
	RuntimeRetentionKeepOnFailure RuntimeRetentionPolicy = "keep-on-failure"
)

type RuntimeSandboxProfile struct {
	Mode                 RuntimeSandboxMode
	Approval             RuntimeApprovalMode
	Network              RuntimeNetworkMode
	RuntimeHomeIsolation RuntimeHomeIsolation
	Retention            RuntimeRetentionPolicy
	Root                 string
	WorkDir              string
	WritableRoots        []string
	EnforceBoundary      bool
}

func (p RuntimeSandboxProfile) WorkspacePolicy() RuntimeWorkspacePolicy {
	return RuntimeWorkspacePolicy{
		Root:            p.Root,
		WorkDir:         p.WorkDir,
		WritableRoots:   append([]string(nil), p.WritableRoots...),
		SandboxMode:     string(p.Mode),
		ApprovalPolicy:  string(p.Approval),
		EnforceBoundary: p.EnforceBoundary,
		Profile:         p.Clone(),
	}
}

func (p RuntimeSandboxProfile) Clone() RuntimeSandboxProfile {
	p.WritableRoots = append([]string(nil), p.WritableRoots...)
	return p
}

func (p RuntimeSandboxProfile) Empty() bool {
	return p.Mode == "" &&
		p.Approval == "" &&
		p.Network == "" &&
		p.RuntimeHomeIsolation == "" &&
		p.Retention == "" &&
		p.Root == "" &&
		p.WorkDir == "" &&
		len(p.WritableRoots) == 0 &&
		!p.EnforceBoundary
}

func SandboxProfileFromWorkspacePolicy(policy RuntimeWorkspacePolicy) RuntimeSandboxProfile {
	if !policy.Profile.Empty() {
		profile := policy.Profile.Clone()
		if profile.Root == "" {
			profile.Root = policy.Root
		}
		if profile.WorkDir == "" {
			profile.WorkDir = policy.WorkDir
		}
		if len(profile.WritableRoots) == 0 {
			profile.WritableRoots = append([]string(nil), policy.WritableRoots...)
		}
		if profile.Mode == "" {
			profile.Mode = RuntimeSandboxMode(policy.SandboxMode)
		}
		if profile.Approval == "" {
			profile.Approval = RuntimeApprovalMode(policy.ApprovalPolicy)
		}
		profile.EnforceBoundary = profile.EnforceBoundary || policy.EnforceBoundary
		return profile
	}
	return RuntimeSandboxProfile{
		Mode:            RuntimeSandboxMode(policy.SandboxMode),
		Approval:        RuntimeApprovalMode(policy.ApprovalPolicy),
		Root:            policy.Root,
		WorkDir:         policy.WorkDir,
		WritableRoots:   append([]string(nil), policy.WritableRoots...),
		EnforceBoundary: policy.EnforceBoundary,
	}
}

type TracePolicy struct {
	CaptureRawOutput bool
	CaptureMessages  bool
	RedactSecrets    bool
}

type RuntimeStatus string

const (
	RuntimeStatusCompleted RuntimeStatus = "completed"
	RuntimeStatusFailed    RuntimeStatus = "failed"
	RuntimeStatusBlocked   RuntimeStatus = "blocked"
	RuntimeStatusTimeout   RuntimeStatus = "timeout"
	RuntimeStatusCancelled RuntimeStatus = "cancelled"
)

type RuntimeResponse struct {
	RunID            AgentRunID
	Status           RuntimeStatus
	RawOutputRef     string
	Messages         []RuntimeMessage
	ToolCalls        []RuntimeToolCall
	Artifacts        []ArtifactRef
	Usage            map[string]TokenUsage
	Error            RuntimeFailure
	DurationMs       int64
	CompletedAt      time.Time
	ProviderMetadata map[string]string
	OutboundMessages []RuntimeChatMessage
}

type RuntimeFailure struct {
	Code      string
	Message   string
	Retryable bool
}

type RuntimeMessageType string

const (
	RuntimeMessageText       RuntimeMessageType = "text"
	RuntimeMessageThinking   RuntimeMessageType = "thinking"
	RuntimeMessageToolUse    RuntimeMessageType = "tool_use"
	RuntimeMessageToolResult RuntimeMessageType = "tool_result"
	RuntimeMessageStatus     RuntimeMessageType = "status"
	RuntimeMessageError      RuntimeMessageType = "error"
	RuntimeMessageLog        RuntimeMessageType = "log"
	RuntimeMessagePlan       RuntimeMessageType = "plan"
)

type RuntimeMessage struct {
	Type          RuntimeMessageType
	Content       string
	ReasoningKind ReasoningContentKind
	Tool          string
	CallID        string
	Input         map[string]any
	Output        string
	Status        string
	Level         string
	SessionID     string
	Plan          *RuntimePlanState
}

const RuntimeMessageStatusFinal = "final"

type RuntimePlanState struct {
	PlanID string            `json:"plan_id"`
	Status string            `json:"status"`
	Items  []RuntimePlanItem `json:"items"`
}

type RuntimePlanItem struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Status string `json:"status"`
}

type RuntimeToolCall struct {
	ID     string
	Name   string
	Input  map[string]any
	Output string
}

type RuntimeChatMessage struct {
	ContextParts []RuntimeContextPart `json:"-"`
	Role         string               `json:"role"`
	Content      string               `json:"content,omitempty"`
	ToolName     string               `json:"tool_name,omitempty"`
	ToolCallID   string               `json:"tool_call_id,omitempty"`
	ToolCalls    []RuntimeToolCall    `json:"tool_calls,omitempty"`
}

// RuntimeContextPart is a server-rendered unit, never parsed from user text.
// Required parts must survive intact; optional parts are retained by priority.
type RuntimeContextPart struct {
	Content  string
	Priority int
	Required bool
}

type TokenUsage struct {
	// Observed subsets of InputTokens/OutputTokens. Never add these to totals.
	// Known is true only when every aggregated usage delta reported that field.
	CachedInputTokens      int64 `json:",omitempty"`
	ReasoningTokens        int64 `json:",omitempty"`
	CachedInputTokensKnown bool  `json:",omitempty"`
	ReasoningTokensKnown   bool  `json:",omitempty"`
	InputTokens            int64
	OutputTokens           int64
	CacheReadTokens        int64
	CacheWriteTokens       int64
}

func (s RuntimeStatus) IsTerminal() bool {
	switch s {
	case RuntimeStatusCompleted, RuntimeStatusFailed, RuntimeStatusBlocked, RuntimeStatusTimeout, RuntimeStatusCancelled:
		return true
	default:
		return false
	}
}
