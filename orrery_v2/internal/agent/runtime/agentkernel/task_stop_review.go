package agentkernel

import (
	"encoding/json"
	"regexp"
	"strings"
)

const blockedTaskReviewProtocol = "orrery-blocked-task-review-v1"

const blockedTaskStopReview = "Task stopping review: your proposed final result reports outcome=blocked. Recheck the original task and the actual tool evidence before stopping. If an authorized next step remains and the existing budget and deadline allow it, perform a bounded correction and verify the result. Unwritten output, incomplete stages, duplicate content, and a first failed quality check alone do not prove an external blocker. Keep successful work and receipts; do not repeat completed or uncertain side effects, retry unchanged failures, expand permissions, bypass approval or quality checks, or invent evidence. If necessary input, permission, human approval, or an unrecoverable dependency really prevents progress, keep outcome=blocked and explain the concrete evidence, attempted corrections, unfinished goal, and what must change to resume. Preserve the required result contract and write the public answer yourself. This stopping review runs at most once in this task turn and consumes the existing model budget."

var blockedTaskResultFence = regexp.MustCompile("(?m)^```orrery-agent-result[\\t ]*\\r?\\n([\\s\\S]*?)^```[\\t ]*$")

// Only a task's explicit result protocol triggers review. Ordinary chat,
// quoted blockers and tool output never become host continuation requests.
// Full result validation and business acceptance remain with the task host.
func proposedBlockedTaskResult(text string) bool {
	blocks := blockedTaskResultFence.FindAllStringSubmatch(strings.TrimSpace(text), -1)
	if len(blocks) != 1 {
		return false
	}
	var result struct {
		Version json.RawMessage `json:"version"`
		Outcome string          `json:"outcome"`
		Summary string          `json:"summary"`
	}
	if json.Unmarshal([]byte(blocks[0][1]), &result) != nil || result.Outcome != "blocked" || strings.TrimSpace(result.Summary) == "" {
		return false
	}
	switch strings.TrimSpace(string(result.Version)) {
	case "1", "1.0", `"1"`, `"1.0"`:
		return true
	default:
		return false
	}
}
