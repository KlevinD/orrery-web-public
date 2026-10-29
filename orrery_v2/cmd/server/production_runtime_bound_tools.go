package main

import (
	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	"strings"
)

func runtimeDiscussionBoundTools(bindings []agentcore.RuntimeToolBinding) []discussionsession.DiscussionAgentBoundTool {
	if len(bindings) == 0 {
		return nil
	}
	out := make([]discussionsession.DiscussionAgentBoundTool, 0, len(bindings))
	for _, binding := range bindings {
		toolID := strings.TrimSpace(string(binding.ToolID))
		if toolID == "" {
			continue
		}
		out = append(out, discussionsession.DiscussionAgentBoundTool{
			ToolID:      toolID,
			Alias:       strings.TrimSpace(binding.Alias),
			Description: strings.TrimSpace(binding.Summary),
		})
	}
	return out
}
