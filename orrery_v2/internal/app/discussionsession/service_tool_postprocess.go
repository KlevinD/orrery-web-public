package discussionsession

import toolstd "orrery_v2/internal/resource/tool/std"

func defaultDiscussionToolPostProcess(toolID string) *bool {
	value := defaultDiscussionToolRequiresPostProcess(toolID)
	return &value
}

func toolCallRequiresPostProcess(call ToolCallDTO, boundTools []DiscussionAgentBoundTool) bool {
	name := normalizeToolName(call.Name)
	if name == "" {
		return false
	}
	for _, tool := range boundTools {
		if !toolMatchesCallName(tool, name) {
			continue
		}
		if tool.RequiresPostProcess == nil {
			return defaultDiscussionToolRequiresPostProcess(name)
		}
		return *tool.RequiresPostProcess
	}
	return defaultDiscussionToolRequiresPostProcess(name)
}

func toolMatchesCallName(tool DiscussionAgentBoundTool, name string) bool {
	return normalizeToolName(tool.ToolID) == name || normalizeToolName(tool.Alias) == name
}

func defaultDiscussionToolRequiresPostProcess(toolID string) bool {
	switch normalizeToolName(toolID) {
	case string(toolstd.DiscussionAck), string(toolstd.SessionSearch), string(toolstd.SessionRead):
		return false
	default:
		return true
	}
}
