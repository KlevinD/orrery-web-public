package outputpolicy

import "strings"

// ChatText filters an Agent's reply without inventing fallback dialogue.
// Lifecycle status belongs in status fields, never in an assistant message.
func ChatText(value string) string {
	value = FilterPrivateReasoning(value).Text
	if start := strings.LastIndex(value, "```orrery-public-summary"); start >= 0 {
		rest := value[start+len("```orrery-public-summary"):]
		if end := strings.Index(rest, "```"); end >= 0 {
			value = rest[:end]
		}
	}
	for {
		start := strings.Index(value, "```orrery-agent-result")
		if start < 0 {
			break
		}
		rest := value[start+len("```orrery-agent-result"):]
		end := strings.Index(rest, "```")
		if end < 0 {
			value = value[:start]
			break
		}
		value = value[:start] + rest[end+3:]
	}
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
		lower := strings.ToLower(strings.TrimSpace(line))
		private := false
		for _, marker := range []string{"orrery-agent-result", "document_id=", "provider_document_id=", "job_id=", "evidence_id", "source_ref=", "tool_call_id", "tool_call=", "runtime_output", "context_bundle", "adjustment_status=", "artifact_id=", "local_path="} {
			private = private || strings.Contains(lower, marker)
		}
		for _, prefix := range []string{"call_", "fc_", "run_"} {
			private = private || strings.HasPrefix(lower, prefix)
		}
		private = private || strings.Contains(lower, "|fc_") || strings.Contains(lower, "|call_")
		if !private {
			lines = append(lines, publicReplyLine(line))
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
