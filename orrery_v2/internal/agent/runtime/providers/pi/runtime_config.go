package pi

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func parseRuntimeConfig(raw json.RawMessage) RuntimeConfig {
	if len(raw) == 0 {
		return RuntimeConfig{}
	}
	var runtimeCfg RuntimeConfig
	_ = json.Unmarshal(raw, &runtimeCfg)
	return runtimeCfg
}

func validatePiProviderConfig(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	if strings.TrimSpace(string(raw)) == "null" {
		return fmt.Errorf("pi provider config must be a JSON object")
	}
	var runtimeCfg RuntimeConfig
	if err := json.Unmarshal(raw, &runtimeCfg); err != nil {
		return fmt.Errorf("decode pi provider config: %w", err)
	}
	return nil
}

func runtimeConfigInt64(value any) int64 {
	switch typed := value.(type) {
	case nil:
		return 0
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}

func piApprovalMode(opts runtimecore.ExecOptions) string {
	// The discussion runtime owns this marker. A manual conversation must keep
	// the approval hook even when the reusable provider profile says never.
	if strings.EqualFold(strings.TrimSpace(opts.ProviderSpecific[agentcore.RuntimeConversationHILModeProviderKey]), "manual") {
		return "on-request"
	}
	runtimeCfg := parseRuntimeConfig(opts.ProviderConfig)
	if mode := normalizePiApprovalMode(runtimeCfg.ApprovalMode); mode != "" {
		return mode
	}
	switch opts.SandboxProfile.Approval {
	case agentcore.RuntimeApprovalAuto:
		return "auto"
	case agentcore.RuntimeApprovalNever:
		return "never"
	case agentcore.RuntimeApprovalOnRequest:
		return "on-request"
	}
	switch normalizePiApprovalMode(opts.WorkspacePolicy.ApprovalPolicy) {
	case "auto":
		return "auto"
	case "never":
		return "never"
	case "on-request":
		return "on-request"
	default:
		return "on-request"
	}
}

func normalizePiApprovalMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "default":
		return ""
	case "auto", "approve", "approved", "always", "bypass":
		return "auto"
	case "never", "deny", "denied", "no", "no-approve":
		return "never"
	case "on-request", "request", "ask", "prompt":
		return "on-request"
	default:
		return ""
	}
}
