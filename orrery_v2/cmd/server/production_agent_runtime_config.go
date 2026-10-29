package main

import (
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/config"
	toolres "orrery_v2/internal/resource/tool"
)

type conversationAgentRuntimeOptions struct {
	RuntimeSpec      agentcore.RuntimeSpec
	SystemPrompt     string
	Model            string
	WorkingDirectory string
	Timeout          time.Duration
}

func configuredConversationAgentRuntimeOptions(cfg config.Config, agentID string) conversationAgentRuntimeOptions {
	agentPath := "agents." + strings.TrimSpace(agentID)
	opts := conversationAgentRuntimeOptions{}
	if strings.TrimSpace(agentID) != "" {
		opts.RuntimeSpec = runtimeSpecFromConfigPath(cfg, agentPath)
		opts.SystemPrompt, _ = cfg.String(agentPath + ".system_prompt")
		opts.Model, _ = cfg.String(agentPath + ".model")
		opts.WorkingDirectory, _ = cfg.String(agentPath + ".working_directory")
		opts.Timeout, _ = cfg.Duration(agentPath + ".timeout")
	}
	if !opts.RuntimeSpec.HasKind() {
		opts.RuntimeSpec = runtimeSpecFromConfigPath(cfg, "discussion.agent")
	}
	if strings.TrimSpace(opts.SystemPrompt) == "" {
		opts.SystemPrompt, _ = cfg.String("discussion.agent.system_prompt")
	}
	if strings.TrimSpace(opts.Model) == "" {
		opts.Model, _ = cfg.String("discussion.agent.model")
	}
	if strings.TrimSpace(opts.WorkingDirectory) == "" {
		opts.WorkingDirectory, _ = cfg.String("discussion.agent.working_directory")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = configuredDiscussionAgentTimeout(cfg)
	}
	return opts
}

func configuredMCPDiscoverer(cfg config.Config) toolres.Discoverer {
	baseURL, ok := cfg.String("resource.mcp_registry_url")
	if !ok || strings.TrimSpace(baseURL) == "" {
		// TODO(resource-mcp-registry-config): production deployments must set resource.mcp_registry_url in YAML to enable MCP discovery/register-discovered flows.
		return nil
	}
	return toolres.NewRegistryDiscoverer(strings.TrimSpace(baseURL), nil)
}
