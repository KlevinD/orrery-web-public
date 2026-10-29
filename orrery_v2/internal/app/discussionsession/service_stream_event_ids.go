package discussionsession

import (
	"strings"
	"time"
)

func discussionStreamEventID(turnID string, event StreamEventDTO) string {
	parts := []string{"discussion", strings.ReplaceAll(strings.TrimSpace(event.Kind), "_", "-"), strings.TrimSpace(turnID)}
	if runID := strings.TrimSpace(event.RunID); runID != "" {
		parts = append(parts, runID)
	}
	if event.Sequence > 0 {
		parts = append(parts, time.Unix(0, int64(event.Sequence)).UTC().Format("150405.000000000"))
	} else if event.Message != nil {
		parts = append(parts, event.Message.MessageID)
	} else if event.ToolCall != nil {
		parts = append(parts, event.ToolCall.ToolCallID, event.ToolCall.Status)
	}
	return strings.Join(nonEmptyStrings(parts), "-")
}
