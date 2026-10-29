package toolbridge

import (
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

func RuntimeToolName(binding agentcore.RuntimeToolBinding) string {
	if alias := NormalizeToolName(binding.Alias); alias != "" {
		return alias
	}
	if NormalizeToolName(string(binding.ToolID)) == "personal_assistant_shell_exec" {
		return "run_terminal"
	}
	return NormalizeToolName(string(binding.ToolID))
}

func NormalizeToolName(name string) string {
	name = strings.TrimSpace(name)
	var normalized strings.Builder
	separator := false
	for _, value := range name {
		if asciiToolNameCharacter(value) {
			normalized.WriteRune(value)
			separator = false
			continue
		}
		if normalized.Len() > 0 && !separator {
			normalized.WriteByte('_')
			separator = true
		}
	}
	return strings.Trim(normalized.String(), "_")
}

func asciiToolNameCharacter(value rune) bool {
	return value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' ||
		value == '_'
}

func RuntimeToolBindingForCall(name string, bindings []agentcore.RuntimeToolBinding) (agentcore.RuntimeToolBinding, bool) {
	normalized := NormalizeToolName(name)
	if normalized == "" {
		return agentcore.RuntimeToolBinding{}, false
	}
	for _, binding := range bindings {
		if NormalizeToolName(binding.Alias) == normalized ||
			NormalizeToolName(string(binding.ToolID)) == normalized ||
			RuntimeToolName(binding) == normalized {
			return binding, true
		}
	}
	return agentcore.RuntimeToolBinding{}, false
}
