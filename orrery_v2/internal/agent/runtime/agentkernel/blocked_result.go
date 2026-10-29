package agentkernel

import (
	"encoding/json"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

const blockedResultStopReview = "Final result review: you proposed a blocked formal task result. Before stopping, check the original task, available authorized tools, actual receipts and remaining budget. Unwritten outputs, an unattempted plan revision, or a first failed quality check do not alone prove an unavoidable blocker. If an authorized correction remains, perform it now and verify the result, preserving completed work. Do not only describe the next step. Do not replay uncertain or completed operations, expand permissions, bypass quality or human approval, or invent evidence. If a concrete necessary input, permission or dependency is unavailable, or reasonable corrections have already failed, return an accurate blocked result with that evidence. This is a single bounded review within the existing budget and deadline; it does not authorize any additional action."

// Only the host's formal result contract enables this review. Quoted tool output
// or ordinary chat mentioning a blocked result cannot activate it.
func hasFormalResultContract(request agentcore.RuntimeRequest) bool {
	for _, block := range request.RuntimeOptions.PromptBlocks {
		if block.Enabled && block.BlockID == "formal_mission_task_result_contract" {
			return true
		}
	}
	return false
}

func proposesBlockedFormalResult(content string) bool {
	const fence = "```orrery-agent-result"
	if strings.Count(content, fence) != 1 {
		return false
	}
	_, tail, found := strings.Cut(content, fence)
	if !found || !strings.HasPrefix(tail, "\n") && !strings.HasPrefix(tail, "\r\n") {
		return false
	}
	body, trailing, found := strings.Cut(tail, "```")
	if !found || strings.TrimSpace(trailing) != "" {
		return false
	}
	var result struct {
		Version int    `json:"version"`
		Outcome string `json:"outcome"`
	}
	return json.Unmarshal([]byte(body), &result) == nil && result.Version == 1 && result.Outcome == "blocked"
}
