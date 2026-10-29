package core

import (
	"sort"
	"strings"
)

func runtimeManagesNativeContext(kind RuntimeKind) bool {
	switch kind {
	case RuntimeKindPi, RuntimeKindCodex, RuntimeKindClaude, RuntimeKindOpenClaw, RuntimeKindACP:
		return true
	default:
		return false
	}
}

func RenderRuntimeContextParts(parts []RuntimeContextPart) string {
	contents := make([]string, 0, len(parts))
	for _, part := range parts {
		if content := strings.TrimSpace(part.Content); content != "" {
			contents = append(contents, content)
		}
	}
	return strings.Join(contents, "\n\n")
}

func appendStructuredRuntimeContext(messages *[]RuntimeChatMessage, parts []RuntimeContextPart) bool {
	if len(parts) == 0 {
		return false
	}
	*messages = append(*messages, RuntimeChatMessage{
		Role: RuntimePromptRoleUser, Content: RenderRuntimeContextParts(parts),
		ContextParts: append([]RuntimeContextPart(nil), parts...),
	})
	return true
}

func limitStructuredRuntimeMessages(messages []RuntimeChatMessage, budget int) ([]RuntimeChatMessage, RuntimeContextLimitStats) {
	out := cloneRuntimeChatMessages(messages)
	stats := RuntimeContextLimitStats{LimitTokens: budget, EstimatedTokensBefore: EstimateRuntimeChatMessagesTokens(out)}
	type candidate struct{ message, part, priority int }
	var candidates []candidate
	parts := make([][]RuntimeContextPart, len(out))
	for i := range out {
		parts[i] = append([]RuntimeContextPart(nil), out[i].ContextParts...)
		// Never persist or forward omitted source text through diagnostic metadata.
		out[i].ContextParts = nil
		for j, part := range parts[i] {
			if !part.Required {
				candidates = append(candidates, candidate{i, j, part.Priority})
			}
		}
	}
	if budget <= 0 || stats.EstimatedTokensBefore <= budget {
		stats.EstimatedTokensAfter = stats.EstimatedTokensBefore
		return out, stats
	}
	kept := make([][]RuntimeContextPart, len(parts))
	for i := range parts {
		kept[i] = append([]RuntimeContextPart(nil), parts[i]...)
		for j := range kept[i] {
			if !kept[i][j].Required {
				kept[i][j].Content = ""
			}
		}
		if len(parts[i]) > 0 {
			out[i].Content = RenderRuntimeContextParts(kept[i])
		}
	}
	remaining := budget - EstimateRuntimeChatMessagesTokens(out)
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].priority > candidates[j].priority })
	for _, candidate := range candidates {
		part := parts[candidate.message][candidate.part]
		// Two tokens conservatively cover separators and ASCII token rounding.
		cost := EstimateRuntimeTextTokens(part.Content) + 2
		if cost <= remaining {
			kept[candidate.message][candidate.part] = part
			remaining -= cost
		} else {
			stats.Truncated = true
		}
	}
	for i := range kept {
		if len(kept[i]) > 0 {
			out[i].Content = RenderRuntimeContextParts(kept[i])
		}
	}
	stats.EstimatedTokensAfter = EstimateRuntimeChatMessagesTokens(out)
	// Keep current instructions and security boundaries intact. The caller must
	// reject overflow before invoking a runtime, not execute a half-cut instruction.
	stats.RequiredOverflow = budget > 0 && stats.EstimatedTokensAfter > budget
	return out, stats
}
