package core

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// RuntimeContextHardLimitTokens bounds platform-injected context. Native
	// session providers budget their own full history and tool catalog separately.
	RuntimeContextHardLimitTokens = 16 * 1024

	RuntimeContextTokenLimitProviderKey     = "llm_context_token_limit"
	RuntimeContextTruncatedProviderKey      = "llm_context_truncated"
	RuntimeContextOverflowProviderKey       = "llm_context_required_overflow"
	RuntimeAllowedToolIDsProviderKey        = "allowed_tool_ids"
	RuntimeDisableSkillResourcesProviderKey = "disable_skill_resources"

	runtimeContextLengthProviderKey = "llm_context_length"
	runtimeMessageOverheadTokens    = 4
)

type RuntimeContextLimitStats struct {
	LimitTokens           int
	EstimatedTokensBefore int
	EstimatedTokensAfter  int
	Truncated             bool
	RequiredOverflow      bool
}

// Managed loops account for the complete tool catalog at the provider boundary.
// Keep a bounded window while respecting smaller configured model windows.
func EffectiveManagedRuntimeContextTokenLimit(values map[string]string) int {
	limit := 64 * 1024
	for _, key := range []string{runtimeContextLengthProviderKey, RuntimeContextTokenLimitProviderKey} {
		if value, err := strconv.Atoi(strings.TrimSpace(values[key])); err == nil && value > 0 && value < limit {
			limit = value
		}
	}
	return limit
}

func filterRuntimeToolBindingsByAllowedIDs(bindings []RuntimeToolBinding, raw string) []RuntimeToolBinding {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return bindings
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil
	}
	allowed := make(map[ToolID]bool, len(ids))
	for _, id := range ids {
		if id := ToolID(strings.TrimSpace(id)); id != "" {
			allowed[id] = true
		}
	}
	out := make([]RuntimeToolBinding, 0, len(bindings))
	for _, binding := range bindings {
		if allowed[binding.ToolID] {
			out = append(out, binding)
		}
	}
	return out
}

// EffectiveRuntimeContextTokenLimit applies the platform ceiling to the model
// profile window and to an optional, smaller per-run override.
func EffectiveRuntimeContextTokenLimit(providerSpecific map[string]string) int {
	limit := RuntimeContextHardLimitTokens
	for _, key := range []string{runtimeContextLengthProviderKey, RuntimeContextTokenLimitProviderKey} {
		value, err := strconv.Atoi(strings.TrimSpace(providerSpecific[key]))
		if err == nil && value > 0 && value < limit {
			limit = value
		}
	}
	return limit
}

// RuntimeContextOutputReserveTokens leaves room in the context window for the
// model response. The reserve scales down for small local-model windows.
func RuntimeContextOutputReserveTokens(contextTokens int) int {
	if contextTokens <= 0 {
		contextTokens = RuntimeContextHardLimitTokens
	}
	reserve := contextTokens / 4
	if contextTokens >= 4*1024 && reserve < 1024 {
		reserve = 1024
	}
	if reserve > 4*1024 {
		reserve = 4 * 1024
	}
	if reserve >= contextTokens {
		reserve = contextTokens / 4
	}
	return reserve
}

func RuntimePromptTokenBudget(contextTokens int) int {
	if contextTokens <= 0 || contextTokens > RuntimeContextHardLimitTokens {
		contextTokens = RuntimeContextHardLimitTokens
	}
	budget := contextTokens - RuntimeContextOutputReserveTokens(contextTokens)
	if budget < 1 {
		return 1
	}
	return budget
}

func EstimateRuntimeTextTokens(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	tokens := 0
	asciiRun := 0
	flushASCII := func() {
		if asciiRun > 0 {
			// Three bytes per token is deliberately conservative for JSON, code,
			// and identifiers while remaining close enough for natural language.
			tokens += (asciiRun + 2) / 3
			asciiRun = 0
		}
	}
	for _, r := range value {
		if r < utf8.RuneSelf {
			asciiRun++
			continue
		}
		flushASCII()
		tokens++
	}
	flushASCII()
	return tokens
}

func EstimateRuntimeChatMessageTokens(message RuntimeChatMessage) int {
	tokens := runtimeMessageOverheadTokens + EstimateRuntimeTextTokens(message.Role) + EstimateRuntimeTextTokens(message.Content)
	tokens += EstimateRuntimeTextTokens(message.ToolName) + EstimateRuntimeTextTokens(message.ToolCallID)
	if len(message.ToolCalls) > 0 {
		if raw, err := json.Marshal(message.ToolCalls); err == nil {
			tokens += EstimateRuntimeTextTokens(string(raw))
		}
	}
	return tokens
}

func EstimateRuntimeChatMessagesTokens(messages []RuntimeChatMessage) int {
	total := 0
	for _, message := range messages {
		total += EstimateRuntimeChatMessageTokens(message)
	}
	return total
}

func EstimateRuntimeToolBindingsTokens(bindings []RuntimeToolBinding) int {
	if len(bindings) == 0 {
		return 0
	}
	definitions := make([]map[string]any, 0, len(bindings))
	for _, binding := range bindings {
		definitions = append(definitions, map[string]any{
			"name":        firstNonEmpty(binding.Alias, string(binding.ToolID)),
			"description": binding.Summary, "parameters": binding.InputSchema,
		})
	}
	// Output schemas, permissions and secret bindings are server-side routing
	// metadata, not model input. Counting them can erase the conversation budget.
	raw, err := json.Marshal(definitions)
	if err != nil {
		return 0
	}
	return EstimateRuntimeTextTokens(string(raw))
}

func RuntimePromptTokenBudgetAfterTools(promptBudget int, bindings []RuntimeToolBinding) int {
	return RuntimePromptTokenBudgetAfterReserved(promptBudget, EstimateRuntimeToolBindingsTokens(bindings))
}

func RuntimePromptTokenBudgetAfterReserved(promptBudget int, reservedTokens int) int {
	if promptBudget <= 0 {
		return 1
	}
	available := promptBudget - reservedTokens
	minimum := promptBudget / 4
	if minimum > 1024 {
		minimum = 1024
	}
	if minimum < 1 {
		minimum = 1
	}
	if available < minimum {
		return minimum
	}
	return available
}

// LimitRuntimeChatMessages keeps system instructions and the boundary user
// messages, then fills the remaining budget from the newest history backwards.
func LimitRuntimeChatMessages(messages []RuntimeChatMessage, tokenBudget int) ([]RuntimeChatMessage, RuntimeContextLimitStats) {
	stats := RuntimeContextLimitStats{LimitTokens: tokenBudget, EstimatedTokensBefore: EstimateRuntimeChatMessagesTokens(messages)}
	if len(messages) == 0 {
		return nil, stats
	}
	for _, message := range messages {
		if len(message.ContextParts) > 0 {
			return limitStructuredRuntimeMessages(messages, tokenBudget)
		}
	}
	if tokenBudget <= 0 || stats.EstimatedTokensBefore <= tokenBudget {
		out := cloneRuntimeChatMessages(messages)
		stats.EstimatedTokensAfter = stats.EstimatedTokensBefore
		return out, stats
	}

	keep := make(map[int]int)
	systemIndexes := make([]int, 0, len(messages))
	nonSystemIndexes := make([]int, 0, len(messages))
	for i, message := range messages {
		if strings.EqualFold(strings.TrimSpace(message.Role), RuntimePromptRoleSystem) {
			systemIndexes = append(systemIndexes, i)
		} else {
			nonSystemIndexes = append(nonSystemIndexes, i)
		}
	}

	// Instructions are indivisible: clipping their middle can remove a boundary
	// or turn a detailed work policy into an unrelated fragment.
	used := 0
	for _, index := range systemIndexes {
		cost := EstimateRuntimeChatMessageTokens(messages[index])
		keep[index] = cost
		used += cost
	}
	if len(nonSystemIndexes) > 0 {
		last := nonSystemIndexes[len(nonSystemIndexes)-1]
		cost := EstimateRuntimeChatMessageTokens(messages[last])
		keep[last] = cost
		used += cost
	}
	remaining := tokenBudget - used
	if remaining < 0 {
		remaining = 0
	}

	if len(nonSystemIndexes) > 0 && remaining > 0 {
		last := nonSystemIndexes[len(nonSystemIndexes)-1]
		first := nonSystemIndexes[0]
		if first != last && remaining > 0 {
			cost := EstimateRuntimeChatMessageTokens(messages[first])
			if cost <= remaining {
				keep[first] = cost
				remaining -= cost
			}
		}

		for i := len(nonSystemIndexes) - 1; i >= 0 && remaining > 0; i-- {
			index := nonSystemIndexes[i]
			if _, exists := keep[index]; exists {
				continue
			}
			cost := EstimateRuntimeChatMessageTokens(messages[index])
			if cost > remaining {
				continue
			}
			keep[index] = cost
			remaining -= cost
		}
	}

	indexes := make([]int, 0, len(keep))
	for index := range keep {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	out := make([]RuntimeChatMessage, 0, len(indexes))
	for _, index := range indexes {
		out = append(out, limitRuntimeChatMessage(messages[index], keep[index]))
	}
	stats.EstimatedTokensAfter = EstimateRuntimeChatMessagesTokens(out)
	stats.Truncated = stats.EstimatedTokensAfter < stats.EstimatedTokensBefore || len(out) < len(messages)
	stats.RequiredOverflow = stats.EstimatedTokensAfter > tokenBudget
	return out, stats
}

func assignRuntimeMessageBudgets(messages []RuntimeChatMessage, indexes []int, tokenBudget int, keep map[int]int) int {
	if len(indexes) == 0 || tokenBudget <= 0 {
		return 0
	}
	used := 0
	remaining := tokenBudget
	for i, index := range indexes {
		if remaining <= 0 {
			break
		}
		cost := EstimateRuntimeChatMessageTokens(messages[index])
		share := remaining / (len(indexes) - i)
		if cost < share {
			share = cost
		}
		if share < runtimeMessageOverheadTokens+1 {
			continue
		}
		keep[index] = share
		used += share
		remaining -= share
	}
	return used
}

func limitRuntimeChatMessage(message RuntimeChatMessage, tokenBudget int) RuntimeChatMessage {
	out := cloneRuntimeChatMessages([]RuntimeChatMessage{message})[0]
	fixed := EstimateRuntimeChatMessageTokens(RuntimeChatMessage{
		Role:       out.Role,
		ToolName:   out.ToolName,
		ToolCallID: out.ToolCallID,
		ToolCalls:  out.ToolCalls,
	})
	contentBudget := tokenBudget - fixed
	if contentBudget < 0 {
		contentBudget = 0
	}
	out.Content = TruncateRuntimeText(out.Content, contentBudget)
	return out
}

func TruncateRuntimeText(value string, tokenBudget int) string {
	value = strings.TrimSpace(value)
	if value == "" || tokenBudget <= 0 {
		return ""
	}
	if EstimateRuntimeTextTokens(value) <= tokenBudget {
		return value
	}
	marker := "\n[... context truncated ...]\n"
	markerTokens := EstimateRuntimeTextTokens(marker)
	if tokenBudget <= markerTokens {
		return runtimeTextPrefix(value, tokenBudget)
	}
	runes := []rune(value)
	low, high := 0, len(runes)
	best := ""
	for low <= high {
		retained := (low + high) / 2
		headRunes := retained * 3 / 4
		tailRunes := retained - headRunes
		candidate := strings.TrimSpace(string(runes[:headRunes])) + marker + strings.TrimSpace(string(runes[len(runes)-tailRunes:]))
		if EstimateRuntimeTextTokens(candidate) <= tokenBudget {
			best = candidate
			low = retained + 1
		} else {
			high = retained - 1
		}
	}
	if best != "" {
		return best
	}
	return runtimeTextPrefix(value, tokenBudget)
}

func runtimeTextPrefix(value string, tokenBudget int) string {
	runes := []rune(value)
	low, high := 0, len(runes)
	for low < high {
		mid := (low + high + 1) / 2
		if EstimateRuntimeTextTokens(string(runes[:mid])) <= tokenBudget {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return strings.TrimSpace(string(runes[:low]))
}
