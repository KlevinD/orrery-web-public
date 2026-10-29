package core

import (
	"encoding/json"
	"strings"
)

type RuntimeKind string

const (
	RuntimeKindCodex    RuntimeKind = "codex"
	RuntimeKindClaude   RuntimeKind = "claude"
	RuntimeKindCursor   RuntimeKind = "cursor"
	RuntimeKindGemini   RuntimeKind = "gemini"
	RuntimeKindPi       RuntimeKind = "pi"
	RuntimeKindOpenClaw RuntimeKind = "openclaw"
	RuntimeKindOpenCode RuntimeKind = "opencode"
	RuntimeKindHermes   RuntimeKind = "hermes"
	RuntimeKindHuman    RuntimeKind = "human"
	RuntimeKindHTTP     RuntimeKind = "http"
	RuntimeKindBrowser  RuntimeKind = "browser"
	RuntimeKindManaged  RuntimeKind = "managed"
	RuntimeKindDevice   RuntimeKind = "device"
	RuntimeKindACP      RuntimeKind = "acp"
)

type RuntimeSpec struct {
	Kind      RuntimeKind
	ConfigRef string
	Config    json.RawMessage
}

func (s RuntimeSpec) HasKind() bool {
	return s.Kind != ""
}

func (s RuntimeSpec) HasConfig() bool {
	return rawJSONHasValue(s.Config)
}

func rawJSONHasValue(raw json.RawMessage) bool {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return false
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return true
	}
	return value != nil
}

func (k RuntimeKind) IsValid() bool {
	switch k {
	case RuntimeKindCodex, RuntimeKindClaude, RuntimeKindCursor, RuntimeKindGemini, RuntimeKindPi, RuntimeKindOpenClaw, RuntimeKindOpenCode, RuntimeKindHermes, RuntimeKindHuman, RuntimeKindHTTP, RuntimeKindBrowser, RuntimeKindManaged, RuntimeKindDevice, RuntimeKindACP:
		return true
	default:
		return false
	}
}

// SupportsExplicitToolScopeFence reports whether the runtime has a verified
// way to disable every provider-native tool surface for a responsibility task.
// Autonomous plans use this fence so their task-scoped grants cannot be
// bypassed by a provider-local shell, filesystem, or network tool.
func (k RuntimeKind) SupportsExplicitToolScopeFence() bool {
	return k == RuntimeKindPi
}
