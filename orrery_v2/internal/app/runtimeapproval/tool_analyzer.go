package runtimeapproval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

type ToolRequest struct {
	Kind           string
	ToolID         string
	NativeToolName string
	Effects        []string
	Risk           string
	Metadata       map[string]string
	Input          any
}

type ToolAnalysis struct {
	Safe                  bool
	ForcedHuman           bool
	ConfiguredForcedHuman bool
	AutoApprove           bool
	Reason                string
	MatchedRuleID         string
	Effects               []string
	Risk                  string
	SanitizedInput        any
}

func AnalyzeTool(request ToolRequest, policy ToolPolicy, forced ForcedHumanPolicy) ToolAnalysis {
	request.Kind = strings.TrimSpace(request.Kind)
	request.ToolID = strings.TrimSpace(request.ToolID)
	request.NativeToolName = strings.TrimSpace(request.NativeToolName)
	analysis := ToolAnalysis{
		Effects:        normalizedEffects(request.Effects, request.Metadata),
		Risk:           normalizedRisk(request.Risk, request.Metadata),
		SanitizedInput: SanitizeReviewInput(request.Input),
	}
	for _, toolID := range forced.ToolIDs {
		if strings.TrimSpace(toolID) == request.ToolID && request.ToolID != "" {
			analysis.ForcedHuman = true
			analysis.ConfiguredForcedHuman = true
			analysis.Reason = "forced_configured_tool"
			return analysis
		}
	}
	if reason := builtInForcedHumanReason(request, analysis.Effects, analysis.Risk); reason != "" {
		analysis.ForcedHuman = true
		analysis.Reason = reason
		return analysis
	}

	policy = normalizeToolPolicy(policy)
	var matched *ToolRule
	for i := range policy.Allowlist {
		if policy.Allowlist[i].ToolID == request.ToolID {
			matched = &policy.Allowlist[i]
			break
		}
	}
	if matched != nil {
		analysis.MatchedRuleID = matched.RuleID
		analysis.AutoApprove = matched.AutoApprove
		analysis.Effects = mergeEffects(analysis.Effects, matched.Effects)
		analysis.Risk = maxRisk(analysis.Risk, matched.Risk)
		if reason := builtInForcedHumanReason(request, analysis.Effects, analysis.Risk); reason != "" {
			analysis.ForcedHuman = true
			analysis.Reason = reason
			return analysis
		}
	}

	switch policy.Security {
	case SecurityDeny:
		analysis.Reason = "tool_policy_deny"
		return analysis
	case SecurityFull:
		analysis.Safe = true
		analysis.Reason = "tool_full"
		return analysis
	case SecurityAllowlist:
	default:
		analysis.Reason = "tool_policy_invalid"
		return analysis
	}
	if matched == nil {
		analysis.Reason = "tool_allowlist_miss"
		return analysis
	}
	if len(analysis.Effects) == 0 {
		analysis.Effects = []string{"unknown"}
	}
	if analysis.Risk == "" {
		analysis.Risk = "unknown"
	}
	if containsString(analysis.Effects, "unknown") {
		analysis.Reason = "tool_effect_unknown"
		return analysis
	}
	if analysis.Risk == "unknown" {
		analysis.Reason = "tool_risk_unknown"
		return analysis
	}
	analysis.Safe = true
	analysis.Reason = "tool_allowlist_match"
	return analysis
}

func SanitizeReviewInput(value any) any {
	sanitized := sanitizeReviewValue(value, 0)
	encoded, err := json.Marshal(sanitized)
	if err != nil {
		return map[string]any{"_unavailable": true}
	}
	if len(encoded) <= 8192 {
		return sanitized
	}
	sum := sha256.Sum256(encoded)
	previewLength := 4096
	if len(encoded) < previewLength {
		previewLength = len(encoded)
	}
	return map[string]any{
		"_truncated": true,
		"_digest":    hex.EncodeToString(sum[:]),
		"preview":    string(encoded[:previewLength]),
	}
}

func builtInForcedHumanReason(request ToolRequest, effects []string, risk string) string {
	if request.Kind == "mcp_elicitation" || strings.EqualFold(request.Kind, "user_input") {
		return "forced_mcp_elicitation"
	}
	if strings.EqualFold(strings.TrimSpace(request.Metadata["approval_force_manual"]), "true") {
		return "forced_explicit"
	}
	if request.ToolID == "marketing_wechat_draft_create" {
		return "forced_marketing_wechat"
	}
	return ""
}

func normalizedEffects(effects []string, metadata map[string]string) []string {
	values := append([]string(nil), effects...)
	for _, key := range []string{"approval_effects", "effects"} {
		for _, value := range strings.Split(metadata[key], ",") {
			values = append(values, value)
		}
	}
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func normalizedRisk(risk string, metadata map[string]string) string {
	values := []string{risk, metadata["approval_risk"], metadata["risk"]}
	out := ""
	for _, value := range values {
		out = maxRisk(out, strings.ToLower(strings.TrimSpace(value)))
	}
	return out
}

func mergeEffects(left, right []string) []string {
	return normalizedEffects(append(append([]string(nil), left...), right...), nil)
}

func maxRisk(left, right string) string {
	ranks := map[string]int{"": 0, "low": 1, "unknown": 2, "medium": 3, "high": 4, "critical": 5}
	left = strings.ToLower(strings.TrimSpace(left))
	right = strings.ToLower(strings.TrimSpace(right))
	if ranks[right] > ranks[left] {
		return right
	}
	return left
}

func sanitizeReviewValue(value any, depth int) any {
	if depth >= 5 {
		return "[TRUNCATED]"
	}
	switch value := value.(type) {
	case nil, bool, float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return value
	case string:
		if len(value) > 512 {
			return value[:512] + "...[truncated]"
		}
		return value
	case []any:
		limit := len(value)
		if limit > 20 {
			limit = 20
		}
		out := make([]any, 0, limit+1)
		for _, item := range value[:limit] {
			out = append(out, sanitizeReviewValue(item, depth+1))
		}
		if len(value) > limit {
			out = append(out, "[TRUNCATED]")
		}
		return out
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if len(keys) > 32 {
			keys = keys[:32]
		}
		out := make(map[string]any, len(keys)+1)
		for _, key := range keys {
			if secretFieldName(key) {
				out[key] = "[REDACTED]"
				continue
			}
			out[key] = sanitizeReviewValue(value[key], depth+1)
		}
		if len(value) > len(keys) {
			out["_truncated"] = true
		}
		return out
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return "[UNAVAILABLE]"
		}
		var generic any
		if err := json.Unmarshal(encoded, &generic); err != nil {
			return "[UNAVAILABLE]"
		}
		return sanitizeReviewValue(generic, depth+1)
	}
}

func secretFieldName(value string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "_", " ", "_").Replace(value))
	for _, marker := range []string{"secret", "token", "password", "credential", "private_key", "api_key", "authorization"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}
