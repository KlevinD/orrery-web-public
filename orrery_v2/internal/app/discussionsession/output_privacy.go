package discussionsession

import (
	"encoding/json"
	"strconv"
	"strings"

	"orrery_v2/internal/agent/outputpolicy"
)

const (
	discussionOutputPrivacyPolicyKey      = "output_privacy_policy"
	discussionOutputPrivacyPolicyVersion  = "reasoning-v1"
	discussionReasoningSuppressedKey      = "private_reasoning_suppressed"
	discussionReasoningSuppressedBytesKey = "private_reasoning_suppressed_bytes"
	discussionReasoningSummaryKey         = "reasoning_summary_visibility"
	discussionReasoningSummaryPublic      = "public"
	discussionPartialMessageFallback      = "任务正在执行，完成后会发送结果。"
)

func applyDiscussionResponseOutputPolicy(response *DiscussionAgentResponse) {
	if response == nil {
		return
	}
	response.ProviderMetadata = ensureOutputPrivacyMetadata(response.ProviderMetadata)
	result := outputpolicy.FilterPrivateReasoning(response.AssistantMessage)
	response.AssistantMessage = result.Text
	recordPrivateReasoningSuppression(response.ProviderMetadata, result)

	// RuntimeResponse is exposed by the run-debug API, so it must cross the
	// same public-output boundary as the assistant message.
	response.RuntimeResponse = outputpolicy.FilterPrivateReasoning(response.RuntimeResponse).Text
	filterDisplayTimeline(response.DisplayTimeline)
}

func applyStreamOutputPrivacyMetadata(response *DiscussionAgentResponse, tokens *discussionStreamTokenBuffer) {
	if response == nil || tokens == nil {
		return
	}
	response.ProviderMetadata = ensureOutputPrivacyMetadata(response.ProviderMetadata)
	recordPrivateReasoningSuppression(response.ProviderMetadata, tokens.privacyResult())
}

func ensureOutputPrivacyMetadata(metadata map[string]string) map[string]string {
	if metadata == nil {
		metadata = make(map[string]string)
	}
	metadata[discussionOutputPrivacyPolicyKey] = discussionOutputPrivacyPolicyVersion
	return metadata
}

func recordPrivateReasoningSuppression(metadata map[string]string, result outputpolicy.FilterResult) {
	if metadata == nil || !result.Suppressed {
		return
	}
	metadata[discussionReasoningSuppressedKey] = "true"
	current, _ := strconv.ParseInt(metadata[discussionReasoningSuppressedBytesKey], 10, 64)
	if result.SuppressedBytes > current {
		metadata[discussionReasoningSuppressedBytesKey] = strconv.FormatInt(result.SuppressedBytes, 10)
	}
}

func mergeOutputPrivacyMetadata(existing, incoming map[string]string) map[string]string {
	out := cloneStringMap(existing)
	if out == nil {
		out = make(map[string]string)
	}
	for key, value := range incoming {
		out[key] = value
	}
	if existing[discussionReasoningSuppressedKey] == "true" {
		out[discussionReasoningSuppressedKey] = "true"
	}
	existingBytes, _ := strconv.ParseInt(existing[discussionReasoningSuppressedBytesKey], 10, 64)
	incomingBytes, _ := strconv.ParseInt(incoming[discussionReasoningSuppressedBytesKey], 10, 64)
	if existingBytes > incomingBytes {
		out[discussionReasoningSuppressedBytesKey] = strconv.FormatInt(existingBytes, 10)
	}
	return ensureOutputPrivacyMetadata(out)
}

func markRunReasoningSummaryPublic(run *RunRecord) {
	if run == nil {
		return
	}
	run.ProviderMetadata = ensureOutputPrivacyMetadata(run.ProviderMetadata)
	run.ProviderMetadata[discussionReasoningSummaryKey] = discussionReasoningSummaryPublic
}

func publicRunThinkingSummary(run RunRecord) string {
	if strings.EqualFold(strings.TrimSpace(run.ProviderMetadata[discussionReasoningSummaryKey]), discussionReasoningSummaryPublic) {
		return run.ThinkingSummary
	}
	return ""
}

func publicMessageContent(record MessageRecord) string {
	// Partial assistant records are durable checkpoints for stream recovery,
	// not user-facing reports. Older workers stored the whole execution diary
	// here, so hide it at read time while keeping the raw record for audit.
	if strings.EqualFold(strings.TrimSpace(record.Metadata[discussionPartialMessageMetadataKey]), "true") {
		return discussionPartialMessageFallback
	}
	kind := strings.ToLower(strings.TrimSpace(record.Metadata["kind"]))
	if kind == "agent_result" || strings.HasPrefix(kind, "todo_execution_run_") {
		// Result notices may have been written by an older worker before the
		// public-output boundary was enforced. Sanitize them on read as well so
		// historical conversations become readable without a data migration.
		return outputpolicy.PublicText(record.Content, "任务结果已记录到 Orrery。")
	}
	if !strings.EqualFold(strings.TrimSpace(record.Role), "assistant") {
		return record.Content
	}
	return outputpolicy.FilterPrivateReasoning(record.Content).Text
}

func publicMessageMetadata(record MessageRecord) map[string]string {
	metadata := cloneStringMap(record.Metadata)
	if !strings.EqualFold(strings.TrimSpace(record.Role), "assistant") || metadata == nil {
		return metadata
	}
	if strings.EqualFold(strings.TrimSpace(metadata[discussionPartialMessageMetadataKey]), "true") {
		// The timeline is where the older partial-message writer persisted the
		// natural-language execution diary. Keep the marker for UI state, but do
		// not expose the raw timeline to clients.
		delete(metadata, discussionDisplayTimelineMetadataKey)
		return metadata
	}
	raw := strings.TrimSpace(metadata[discussionDisplayTimelineMetadataKey])
	if raw == "" {
		return metadata
	}
	var segments []DiscussionDisplayTimelineSegment
	if err := json.Unmarshal([]byte(raw), &segments); err != nil {
		delete(metadata, discussionDisplayTimelineMetadataKey)
		return metadata
	}
	filterDisplayTimeline(segments)
	filtered, err := json.Marshal(segments)
	if err != nil {
		delete(metadata, discussionDisplayTimelineMetadataKey)
		return metadata
	}
	metadata[discussionDisplayTimelineMetadataKey] = string(filtered)
	return metadata
}

func publicOptionalText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return outputpolicy.PublicText(value, fallback)
}

func publicRunDebug(debug RunDebugDTO) RunDebugDTO {
	debug.OutboundPrompt = publicRunDebugText(debug.OutboundPrompt, "任务请求已记录到 Orrery。")
	debug.RuntimeResponse = publicRunDebugText(debug.RuntimeResponse, "运行结果已记录到 Orrery。")
	return debug
}

func publicRunDebugText(value, fallback string) string {
	cleaned := publicOptionalText(value, fallback)
	if cleaned == "" || cleaned == fallback {
		return cleaned
	}
	lower := strings.ToLower(cleaned)
	for _, marker := range []string{"role:", "tool_use", "tool_result", "call_id:", "tool_call"} {
		if strings.Contains(lower, marker) {
			return fallback
		}
	}
	return cleaned
}

func filterDisplayTimeline(segments []DiscussionDisplayTimelineSegment) {
	filter := &outputpolicy.PrivateReasoningFilter{}
	lastText := -1
	for index := range segments {
		if segments[index].Kind != "text" {
			continue
		}
		segments[index].Text = filter.Write(segments[index].Text)
		lastText = index
	}
	if tail := filter.Finish(); tail != "" && lastText >= 0 {
		segments[lastText].Text += tail
	}
}
