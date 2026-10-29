package discussionsession

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentassembly "orrery_v2/internal/agent/assembly"
	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/actioncontract"
)

type AssembledDiscussionAgentRunnerOptions struct {
	AttachmentProcessor   *RuntimeAttachmentProcessor
	Assembler             agentassembly.AgentAssembler
	Profile               agentcore.AgentProfile
	AgentID               string
	InstanceID            agentcore.AgentInstanceID
	SystemPrompt          string
	PromptBlocks          []agentcore.RuntimePromptBlock
	PromptBlockProvider   PromptBlockProvider
	ActionInterpreter     actioncontract.ActionSemanticInterpreter
	WorkingDirectory      string
	WorkspaceRootProvider WorkspaceRootProvider
	Model                 string
	Timeout               time.Duration
	Permissions           []string
}

func NewAssembledDiscussionAgentRunner(ctx context.Context, opts AssembledDiscussionAgentRunnerOptions) (*BaseAgentDiscussionAgentRunner, error) {
	if opts.Assembler == nil {
		return nil, fmt.Errorf("discussion agent assembler is required")
	}
	if opts.Profile.ID.IsZero() {
		return nil, fmt.Errorf("discussion agent profile is required")
	}
	agentID := strings.TrimSpace(opts.AgentID)
	if agentID == "" {
		agentID = opts.Profile.ID.String()
	}
	if opts.InstanceID.IsZero() {
		return nil, fmt.Errorf("discussion agent instance id is required")
	}
	agent, err := opts.Assembler.Build(ctx, opts.Profile, agentassembly.BuildOptions{
		AgentID:     agentcore.AgentID(agentID),
		InstanceID:  opts.InstanceID,
		Permissions: append([]string(nil), opts.Permissions...),
	})
	if err != nil {
		return nil, fmt.Errorf("assemble discussion agent: %w", err)
	}
	if agent == nil {
		return nil, fmt.Errorf("assemble discussion agent: assembler returned nil agent")
	}
	resolvedSpec := opts.Profile.RuntimeSpec
	if base := agent.Base(); base != nil {
		resolvedSpec = base.RuntimeSpec()
	}
	return NewBaseAgentDiscussionAgentRunner(BaseAgentDiscussionAgentRunnerOptions{
		AttachmentProcessor:   opts.AttachmentProcessor,
		Agent:                 agent,
		AgentID:               agentID,
		RuntimeSpec:           resolvedSpec,
		SystemPrompt:          firstNonBlank(opts.Profile.BaseAgent.SystemPrompt, opts.SystemPrompt),
		PromptBlocks:          append([]agentcore.RuntimePromptBlock(nil), opts.PromptBlocks...),
		PromptBlockProvider:   opts.PromptBlockProvider,
		ActionInterpreter:     opts.ActionInterpreter,
		WorkingDirectory:      opts.WorkingDirectory,
		WorkspaceRootProvider: opts.WorkspaceRootProvider,
		Model:                 opts.Model,
		Timeout:               opts.Timeout,
	}), nil
}
