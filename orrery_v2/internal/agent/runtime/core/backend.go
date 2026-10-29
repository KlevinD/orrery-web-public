package core

import (
	"context"
	"encoding/json"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type ExecOptions struct {
	TenantID                  string
	RunID                     agentcore.AgentRunID
	AgentID                   agentcore.AgentID
	ExecutionEnvelope         *agentcore.ExecutionEnvelope
	ExecutionPrincipal        agentcore.ExecutionPrincipal
	ContextPack               agentcore.ContextPack
	Cwd                       string
	Model                     string
	SystemPrompt              string
	MaxTurns                  int
	Timeout                   time.Duration
	SemanticInactivityTimeout time.Duration
	ResumeSessionID           string
	ExtraArgs                 []string
	CustomArgs                []string
	MCPConfig                 json.RawMessage
	ProviderConfig            json.RawMessage
	ProviderSpecific          map[string]string
	Environment               map[string]string
	ToolBindings              []agentcore.RuntimeToolBinding
	SkillBindings             []agentcore.RuntimeSkillBinding
	SandboxProfile            agentcore.RuntimeSandboxProfile
	WorkspacePolicy           agentcore.RuntimeWorkspacePolicy
}

type RuntimeToolDispatcher interface {
	CallRuntimeTool(ctx context.Context, call RuntimeToolCallRequest) (RuntimeToolCallResult, error)
}

const (
	ToolCallTimeoutSecondsProviderKey = "runtime_tool_call_timeout_seconds"
	LLMModelOverrideProviderKey       = "llm_model_override"
)

type RuntimeToolDispatcherFunc func(ctx context.Context, call RuntimeToolCallRequest) (RuntimeToolCallResult, error)

func (f RuntimeToolDispatcherFunc) CallRuntimeTool(ctx context.Context, call RuntimeToolCallRequest) (RuntimeToolCallResult, error) {
	return f(ctx, call)
}

type RuntimeToolCallRequest struct {
	TenantID              string
	RunID                 agentcore.AgentRunID
	AgentID               agentcore.AgentID
	ExecutionEnvelope     *agentcore.ExecutionEnvelope
	ExecutionPrincipal    agentcore.ExecutionPrincipal
	Name                  string
	CallID                string
	Input                 map[string]any
	RequestMeta           map[string]any
	ToolBindings          []agentcore.RuntimeToolBinding
	SkillBindings         []agentcore.RuntimeSkillBinding
	ResponsibilityContext *agentcore.ResponsibilityContext
	ProviderSpecific      map[string]string
	WorkspaceRoot         string
	WorkingDirectory      string
	WorkspacePolicy       agentcore.RuntimeWorkspacePolicy
}

type RuntimeToolCallResult struct {
	Output map[string]any
	// OutcomeUncertain is trusted dispatcher metadata. It is not derived from
	// model/tool text and forbids treating an unrecorded operation as a failure
	// that can be corrected by issuing another call.
	OutcomeUncertain bool
}

type RuntimeDiaryFlushAttemptRequest struct {
	TenantID            string
	AgentID             agentcore.AgentID
	SessionID           string
	ContextRevision     string
	ContextWindowTokens int
	ReserveTokens       int
	ProjectedTokens     int
	TranscriptBytes     int64
}

type RuntimeDiaryFlushAttemptDecision struct {
	Allowed bool
	Attempt int
	Reason  string
}

type RuntimeDiaryFlushOutcomeRequest struct {
	TenantID        string
	AgentID         agentcore.AgentID
	SessionID       string
	ContextRevision string
	Status          string
	Failure         string
	Date            string
}

type RuntimeDiaryFlushController interface {
	BeginRuntimeDiaryFlush(ctx context.Context, req RuntimeDiaryFlushAttemptRequest) (RuntimeDiaryFlushAttemptDecision, error)
	CompleteRuntimeDiaryFlush(ctx context.Context, req RuntimeDiaryFlushOutcomeRequest) error
}
