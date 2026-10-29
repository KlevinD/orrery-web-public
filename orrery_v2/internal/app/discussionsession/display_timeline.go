package discussionsession

import (
	"encoding/json"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

const discussionDisplayTimelineMetadataKey = "display_timeline_json"

func runtimeDisplayTimeline(messages []agentcore.RuntimeMessage, runID string) []DiscussionDisplayTimelineSegment {
	segments := make([]DiscussionDisplayTimelineSegment, 0)
	hasFinalText := finalRuntimeText(messages) != ""
	for _, message := range messages {
		switch message.Type {
		case agentcore.RuntimeMessageText:
			if hasFinalText && message.Status != agentcore.RuntimeMessageStatusFinal {
				continue
			}
			segments = appendDisplayTextSegment(segments, message.Content)
		case agentcore.RuntimeMessageToolUse:
			if isEmptyMissionBriefCreateRuntimeTool(message) {
				continue
			}
			segments = appendDisplayToolSegment(segments, runID, firstNonBlank(message.CallID, message.Tool))
			if text := discussionOutputToolText(message); text != "" {
				segments = appendDisplayTextSegment(segments, text)
			}
		case agentcore.RuntimeMessagePlan:
			segments = appendDisplayPlanSegment(segments, runID, message.Plan)
		}
	}
	return compactDisplayTimelineSegments(segments)
}

func streamDisplayTimelineSegments(events []DiscussionAgentStreamEvent, runID string) []DiscussionDisplayTimelineSegment {
	segments := make([]DiscussionDisplayTimelineSegment, 0, len(events))
	for _, event := range events {
		switch event.Kind {
		case DiscussionAgentStreamTokenDelta:
			segments = appendDisplayTextSegment(segments, event.Delta)
		case DiscussionAgentStreamToolCall:
			segments = appendDisplayToolSegment(segments, runID, firstNonBlank(event.ToolCall.ToolCallID, event.ToolCall.Name))
		case DiscussionAgentStreamPlan:
			segments = appendDisplayPlanSegment(segments, runID, event.Plan)
		}
	}
	return compactDisplayTimelineSegments(segments)
}

func completedStreamDisplayTimeline(events []DiscussionAgentStreamEvent, runID string, responseSegments []DiscussionDisplayTimelineSegment) []DiscussionDisplayTimelineSegment {
	streamSegments := streamDisplayTimelineSegments(events, runID)
	responseSegments = displayTimelineSegmentsWithRunID(responseSegments, runID)
	if len(streamSegments) == 0 {
		return responseSegments
	}
	if displayTimelineHasPlanSegment(responseSegments) {
		return responseSegments
	}
	if !displayTimelineHasToolSegment(streamSegments) {
		if displayTimelineHasToolSegment(responseSegments) {
			return responseSegments
		}
		if len(responseSegments) > 0 {
			return responseSegments
		}
		return streamSegments
	}
	if len(responseSegments) == 0 {
		return streamSegments
	}
	remainder, ok := displayTimelineRemainderAfterPrefix(streamSegments, responseSegments)
	if !ok {
		if displayTimelineToolsCoveredBy(streamSegments, responseSegments) {
			return streamSegments
		}
		return mergeDisplayTimelines(streamSegments, responseSegments)
	}
	remainder = trimDuplicateCompletionText(remainder, streamSegments)
	if len(remainder) == 0 {
		return streamSegments
	}
	return mergeDisplayTimelines(streamSegments, remainder)
}

func displayTimelineSegmentsWithRunID(segments []DiscussionDisplayTimelineSegment, runID string) []DiscussionDisplayTimelineSegment {
	runID = strings.TrimSpace(runID)
	normalized := compactDisplayTimelineSegments(segments)
	if runID == "" {
		return normalized
	}
	for i := range normalized {
		if (normalized[i].Kind == "tool" || normalized[i].Kind == "plan") && strings.TrimSpace(normalized[i].RunID) == "" {
			normalized[i].RunID = runID
		}
	}
	return normalized
}

func mergeDisplayTimelines(existing []DiscussionDisplayTimelineSegment, next []DiscussionDisplayTimelineSegment) []DiscussionDisplayTimelineSegment {
	out := append([]DiscussionDisplayTimelineSegment(nil), existing...)
	out = append(out, next...)
	return compactDisplayTimelineSegments(out)
}

func mergeDisplayTimelinesForContinuation(existing []DiscussionDisplayTimelineSegment, next []DiscussionDisplayTimelineSegment) []DiscussionDisplayTimelineSegment {
	existing = compactDisplayTimelineSegments(existing)
	next = compactDisplayTimelineSegments(next)
	if len(existing) == 0 {
		return next
	}
	if len(next) == 0 {
		return existing
	}
	prefix, remainder, ok := displayTimelineContinuationParts(existing, next)
	if !ok {
		return mergeDisplayTimelines(existing, next)
	}
	if len(remainder) == 0 {
		return existing
	}
	return mergeDisplayTimelines(prefix, remainder)
}

func displayTimelineContinuationParts(existing []DiscussionDisplayTimelineSegment, next []DiscussionDisplayTimelineSegment) ([]DiscussionDisplayTimelineSegment, []DiscussionDisplayTimelineSegment, bool) {
	covered := displayTimelineToolKeys(existing)
	lastCoveredNextIndex := -1
	lastCoveredExistingIndex := -1
	for i, segment := range next {
		if segment.Kind != "tool" {
			continue
		}
		key := displayTimelineToolKey(segment)
		if key == "" {
			continue
		}
		if covered[key] {
			lastCoveredNextIndex = i
			lastCoveredExistingIndex = displayTimelineLastToolIndex(existing, key)
		}
	}
	if lastCoveredNextIndex < 0 || lastCoveredExistingIndex < 0 {
		return nil, nil, false
	}
	prefix := compactDisplayTimelineSegments(existing[:lastCoveredExistingIndex+1])
	remainder := compactDisplayTimelineSegments(next[lastCoveredNextIndex+1:])
	return prefix, remainder, true
}

func displayTimelineMessageMetadata(response DiscussionAgentResponse, content string, viewer ViewerRef) map[string]string {
	segments := displayTimelineSegmentsForMessage(response.DisplayTimeline, content, viewer)
	if len(segments) == 0 {
		return nil
	}
	raw, err := json.Marshal(segments)
	if err != nil {
		return nil
	}
	return map[string]string{discussionDisplayTimelineMetadataKey: string(raw)}
}

func displayTimelineSegmentsForMessage(segments []DiscussionDisplayTimelineSegment, content string, viewer ViewerRef) []DiscussionDisplayTimelineSegment {
	_ = viewer // reserved — segments are stored as-is; mention formatting is a frontend concern
	segments = compactDisplayTimelineSegments(segments)
	segments = deduplicateRepeatedDisplayTools(segments)
	// Only reject if no structured execution segments exist (pure text doesn't need display_timeline).
	// Preserve tools and plans; both represent established execution facts.
	if !displayTimelineHasStructuredExecutionSegment(segments) {
		return nil
	}

	// Try to match/adjust, but always store if there are tool segments (segments are authoritative facts)
	content = strings.TrimSpace(content)
	if displayTimelineSegmentsMatchContent(segments, content) {
		return segments
	}
	if adjusted, ok := displayTimelineSegmentsWithContentPrefix(segments, content); ok {
		return adjusted
	}

	// Store regardless of content mismatch - tool segments represent established execution facts.
	return segments
}

func appendDisplayTextSegment(segments []DiscussionDisplayTimelineSegment, text string) []DiscussionDisplayTimelineSegment {
	if strings.TrimSpace(text) == "" {
		return segments
	}
	last := len(segments) - 1
	if last >= 0 && segments[last].Kind == "text" {
		segments[last].Text = strings.TrimSpace(segments[last].Text + text)
		return segments
	}
	return append(segments, DiscussionDisplayTimelineSegment{Kind: "text", Text: text})
}

func appendDisplayToolSegment(segments []DiscussionDisplayTimelineSegment, runID string, toolCallID string) []DiscussionDisplayTimelineSegment {
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" {
		return segments
	}
	return append(segments, DiscussionDisplayTimelineSegment{
		Kind:       "tool",
		RunID:      strings.TrimSpace(runID),
		ToolCallID: toolCallID,
	})
}

func appendDisplayPlanSegment(segments []DiscussionDisplayTimelineSegment, runID string, plan *agentcore.RuntimePlanState) []DiscussionDisplayTimelineSegment {
	if plan == nil || strings.TrimSpace(plan.PlanID) == "" {
		return segments
	}
	copied := cloneRuntimePlanState(plan)
	return append(segments, DiscussionDisplayTimelineSegment{Kind: "plan", RunID: strings.TrimSpace(runID), Plan: &copied})
}

func cloneRuntimePlanState(plan *agentcore.RuntimePlanState) agentcore.RuntimePlanState {
	if plan == nil {
		return agentcore.RuntimePlanState{}
	}
	return agentcore.RuntimePlanState{
		PlanID: strings.TrimSpace(plan.PlanID),
		Status: strings.TrimSpace(plan.Status),
		Items:  append([]agentcore.RuntimePlanItem(nil), plan.Items...),
	}
}

func cloneRuntimePlanStatePointer(plan *agentcore.RuntimePlanState) *agentcore.RuntimePlanState {
	if plan == nil {
		return nil
	}
	cloned := cloneRuntimePlanState(plan)
	return &cloned
}

func compactDisplayTimelineSegments(segments []DiscussionDisplayTimelineSegment) []DiscussionDisplayTimelineSegment {
	out := make([]DiscussionDisplayTimelineSegment, 0, len(segments))
	for _, segment := range segments {
		switch segment.Kind {
		case "text":
			out = appendDisplayTextSegment(out, segment.Text)
		case "tool":
			out = appendDisplayToolSegment(out, segment.RunID, segment.ToolCallID)
		case "plan":
			out = appendDisplayPlanSegment(out, segment.RunID, segment.Plan)
		}
	}
	return out
}

func deduplicateRepeatedDisplayTools(segments []DiscussionDisplayTimelineSegment) []DiscussionDisplayTimelineSegment {
	out := make([]DiscussionDisplayTimelineSegment, 0, len(segments))
	firstToolIndex := map[string]int{}
	for _, segment := range compactDisplayTimelineSegments(segments) {
		key := displayTimelineToolKey(segment)
		if key != "" {
			if index, ok := firstToolIndex[key]; ok && !displayTimelineContainsDifferentTool(out[index+1:], key) {
				out = out[:index+1]
				continue
			}
			firstToolIndex[key] = len(out)
		}
		out = append(out, segment)
	}
	return compactDisplayTimelineSegments(out)
}

func displayTimelineContainsDifferentTool(segments []DiscussionDisplayTimelineSegment, toolKey string) bool {
	for _, segment := range segments {
		key := displayTimelineToolKey(segment)
		if key != "" && key != toolKey {
			return true
		}
	}
	return false
}

func displayTimelineHasToolSegment(segments []DiscussionDisplayTimelineSegment) bool {
	for _, segment := range segments {
		if segment.Kind == "tool" && strings.TrimSpace(segment.ToolCallID) != "" {
			return true
		}
	}
	return false
}

func displayTimelineHasPlanSegment(segments []DiscussionDisplayTimelineSegment) bool {
	for _, segment := range segments {
		if segment.Kind == "plan" && segment.Plan != nil && strings.TrimSpace(segment.Plan.PlanID) != "" {
			return true
		}
	}
	return false
}

func displayTimelineHasStructuredExecutionSegment(segments []DiscussionDisplayTimelineSegment) bool {
	for _, segment := range segments {
		if segment.Kind == "tool" && strings.TrimSpace(segment.ToolCallID) != "" {
			return true
		}
		if segment.Kind == "plan" && segment.Plan != nil && strings.TrimSpace(segment.Plan.PlanID) != "" {
			return true
		}
	}
	return false
}

func displayTimelineText(segments []DiscussionDisplayTimelineSegment, separator string) string {
	parts := make([]string, 0)
	for _, segment := range segments {
		if segment.Kind != "text" {
			continue
		}
		if text := strings.TrimSpace(segment.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, separator))
}

func displayTimelineSegmentsMatchContent(segments []DiscussionDisplayTimelineSegment, content string) bool {
	content = strings.TrimSpace(content)
	for _, separator := range []string{"", "\n"} {
		text := displayTimelineText(segments, separator)
		if text != "" && collapseRuntimeTextWhitespace(text) == collapseRuntimeTextWhitespace(content) {
			return true
		}
	}
	return false
}

func displayTimelineSegmentsWithContentPrefix(segments []DiscussionDisplayTimelineSegment, content string) ([]DiscussionDisplayTimelineSegment, bool) {
	content = strings.TrimSpace(content)
	for _, separator := range []string{"", "\n"} {
		text := displayTimelineText(segments, separator)
		if text == "" || !strings.HasSuffix(content, text) {
			continue
		}
		prefix := strings.TrimSpace(strings.TrimSuffix(content, text))
		if prefix == "" {
			continue
		}
		adjusted := append([]DiscussionDisplayTimelineSegment(nil), segments...)
		for i := range adjusted {
			if adjusted[i].Kind != "text" {
				continue
			}
			adjusted[i].Text = strings.TrimSpace(prefix + " " + adjusted[i].Text)
			if displayTimelineSegmentsMatchContent(adjusted, content) {
				return adjusted, true
			}
			break
		}
	}
	return nil, false
}

func displayTimelineRemainderAfterPrefix(prefix []DiscussionDisplayTimelineSegment, segments []DiscussionDisplayTimelineSegment) ([]DiscussionDisplayTimelineSegment, bool) {
	prefix = compactDisplayTimelineSegments(prefix)
	segments = compactDisplayTimelineSegments(segments)
	if len(prefix) == 0 {
		return segments, true
	}
	if len(prefix) > len(segments) {
		return nil, false
	}
	for i := range prefix {
		if !displayTimelineSegmentEquivalent(prefix[i], segments[i]) {
			if i == len(prefix)-1 && prefix[i].Kind == "text" && segments[i].Kind == "text" {
				remainder := textRemainderAfterPrefix(prefix[i].Text, segments[i].Text)
				if remainder != segments[i].Text {
					out := make([]DiscussionDisplayTimelineSegment, 0, len(segments)-len(prefix)+1)
					if strings.TrimSpace(remainder) != "" {
						out = append(out, DiscussionDisplayTimelineSegment{Kind: "text", Text: remainder})
					}
					out = append(out, segments[len(prefix):]...)
					return out, true
				}
			}
			return nil, false
		}
	}
	return segments[len(prefix):], true
}

func displayTimelineSegmentEquivalent(left, right DiscussionDisplayTimelineSegment) bool {
	if left.Kind != right.Kind {
		return false
	}
	switch left.Kind {
	case "text":
		return collapseRuntimeTextWhitespace(left.Text) == collapseRuntimeTextWhitespace(right.Text)
	case "tool":
		return strings.TrimSpace(left.RunID) == strings.TrimSpace(right.RunID) &&
			strings.TrimSpace(left.ToolCallID) == strings.TrimSpace(right.ToolCallID)
	default:
		return false
	}
}

func displayTimelineToolsCoveredBy(coveredBy []DiscussionDisplayTimelineSegment, segments []DiscussionDisplayTimelineSegment) bool {
	tools := displayTimelineToolKeys(coveredBy)
	for _, segment := range segments {
		if segment.Kind != "tool" {
			continue
		}
		if key := displayTimelineToolKey(segment); key != "" && !tools[key] {
			return false
		}
	}
	return true
}

func displayTimelineToolKeys(segments []DiscussionDisplayTimelineSegment) map[string]bool {
	tools := map[string]bool{}
	for _, segment := range segments {
		if segment.Kind != "tool" {
			continue
		}
		if key := displayTimelineToolKey(segment); key != "" {
			tools[key] = true
		}
	}
	return tools
}

func displayTimelineLastToolIndex(segments []DiscussionDisplayTimelineSegment, toolKey string) int {
	toolKey = strings.TrimSpace(toolKey)
	if toolKey == "" {
		return -1
	}
	for i := len(segments) - 1; i >= 0; i-- {
		if displayTimelineToolKey(segments[i]) == toolKey {
			return i
		}
	}
	return -1
}

func displayTimelineToolKey(segment DiscussionDisplayTimelineSegment) string {
	toolCallID := strings.TrimSpace(segment.ToolCallID)
	if toolCallID == "" {
		return ""
	}
	return strings.TrimSpace(segment.RunID) + "\x00" + toolCallID
}

func trimDuplicateCompletionText(remainder []DiscussionDisplayTimelineSegment, streamSegments []DiscussionDisplayTimelineSegment) []DiscussionDisplayTimelineSegment {
	remainder = compactDisplayTimelineSegments(remainder)
	if len(remainder) == 0 || !displayTimelineOnlyText(remainder) {
		return remainder
	}
	streamText := displayTimelineText(streamSegments, "\n")
	remainderText := displayTimelineText(remainder, "\n")
	if streamText == "" || remainderText == "" {
		return remainder
	}
	if collapseRuntimeTextWhitespace(streamText) == collapseRuntimeTextWhitespace(remainderText) {
		return nil
	}
	for _, prefix := range []string{streamText, displayTimelineText(streamSegments, ""), displayTimelineText(streamSegments, " ")} {
		prefix = strings.TrimSpace(prefix)
		if prefix == "" {
			continue
		}
		if trimmed := strings.TrimSpace(strings.TrimPrefix(remainderText, prefix)); trimmed != remainderText && trimmed != "" {
			return []DiscussionDisplayTimelineSegment{{Kind: "text", Text: trimmed}}
		}
	}
	return remainder
}

func displayTimelineOnlyText(segments []DiscussionDisplayTimelineSegment) bool {
	for _, segment := range segments {
		if segment.Kind != "text" {
			return false
		}
	}
	return len(segments) > 0
}

func textRemainderAfterPrefix(prefix string, value string) string {
	prefix = strings.TrimSpace(prefix)
	value = strings.TrimSpace(value)
	if prefix == "" || value == "" {
		return value
	}
	if strings.HasPrefix(value, prefix) {
		return strings.TrimSpace(strings.TrimPrefix(value, prefix))
	}
	return value
}
