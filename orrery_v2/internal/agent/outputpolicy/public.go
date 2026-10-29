package outputpolicy

import (
	"regexp"
	"strings"
)

const (
	publicSummaryFence       = "```orrery-public-summary"
	agentResultManifestFence = "```orrery-agent-result"
)

var (
	publicMarkdownLinkPattern = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	publicLocalPathPattern    = regexp.MustCompile(`(?i)(?:\b[a-z]:[\\/]|/(?:data|home|var|tmp)/)[^\s)\]>,;]+`)
)

// PublicText turns an agent result into text suitable for a human-facing
// channel. Detailed evidence and machine manifests remain in the durable
// Orrery run; they are deliberately removed at this boundary.
func PublicText(value, fallback string) string {
	value = FilterPrivateReasoning(value).Text
	if summary, ok := extractPublicSummary(value); ok {
		value = summary
	}
	value = stripAgentResultBlocks(value)

	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	cleaned := make([]string, 0, len(lines))
	blankLines := 0
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			if len(cleaned) > 0 && blankLines < 2 {
				cleaned = append(cleaned, "")
				blankLines++
			}
			continue
		}
		blankLines = 0
		if publicMachineLine(line) {
			continue
		}
		line = publicReplyLine(line)
		line = strings.TrimSpace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	result := strings.TrimSpace(strings.Join(cleaned, "\n"))
	if result != "" {
		return result
	}
	return publicFallback(fallback)
}

// PublicRuntimeText applies the public-output policy to a runtime lifecycle
// message and supplies a human-readable status when the provider only emitted
// private reasoning or machine metadata.
func PublicRuntimeText(value, lifecycle string) string {
	return PublicText(value, runtimeFallback(lifecycle))
}

func runtimeFallback(lifecycle string) string {
	switch strings.ToLower(strings.TrimSpace(lifecycle)) {
	case "started", "running":
		return "任务正在执行。"
	case "completed", "succeeded", "success":
		return "任务已完成，结果已记录到 Orrery。"
	case "blocked":
		return "任务执行受阻，详细原因已记录到 Orrery。"
	case "timed_out", "timeout", "timedout":
		return "任务执行超时，详细原因已记录到 Orrery。"
	case "cancelled", "canceled":
		return "任务执行已取消。"
	default:
		return "任务执行失败，详细原因已记录到 Orrery。"
	}
}

func extractPublicSummary(value string) (string, bool) {
	start := strings.LastIndex(value, publicSummaryFence)
	if start < 0 {
		return "", false
	}
	raw := value[start+len(publicSummaryFence):]
	end := strings.Index(raw, "```")
	if end < 0 {
		return "", false
	}
	summary := strings.TrimSpace(raw[:end])
	return summary, summary != ""
}

func stripAgentResultBlocks(value string) string {
	for {
		start := strings.Index(value, agentResultManifestFence)
		if start < 0 {
			return value
		}
		raw := value[start+len(agentResultManifestFence):]
		end := strings.Index(raw, "```")
		if end < 0 {
			return value[:start]
		}
		end += start + len(agentResultManifestFence) + len("```")
		value = value[:start] + value[end:]
	}
}

func publicMachineLine(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	for _, marker := range []string{
		"orrery-agent-result", "document_id=", "provider_document_id=", "job_id=",
		"evidence_id", "source_ref=", "tool_call_id", "tool_call=", "runtime_output",
		"context_bundle", "adjustment_status=", "artifact_id=", "local_path=",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	if strings.HasPrefix(lower, "call_") || strings.HasPrefix(lower, "fc_") || strings.HasPrefix(lower, "run_") {
		return true
	}
	return strings.Contains(lower, "|fc_") || strings.Contains(lower, "|call_")
}

func publicFallback(value string) string {
	value = FilterPrivateReasoning(value).Text
	value = strings.TrimSpace(value)
	if value == "" || publicMachineLine(value) {
		return "\u4efb\u52a1\u5df2\u5b8c\u6210\uff0c\u7ed3\u679c\u5df2\u8bb0\u5f55\u5230 Orrery\u3002"
	}
	return value
}
