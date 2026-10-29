package core

import (
	"encoding/json"
	"strings"
)

const BaseAgentMemoryModeExtensionKey = "memory_mode"

type BaseAgentMemoryModeSpec struct {
	Mode string `json:"mode"`
}

func BaseAgentUsesSessionOnlyMemory(spec BaseAgentSpec) bool {
	if len(spec.Extensions) == 0 {
		return false
	}
	var mode BaseAgentMemoryModeSpec
	if err := json.Unmarshal(spec.Extensions[BaseAgentMemoryModeExtensionKey], &mode); err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(mode.Mode), "session_only")
}
