package pageindex

import (
	"fmt"
	"strings"

	memcore "orrery_v2/internal/memory/core"
)

func RenderDocument(record memcore.MemoryRecord) string {
	record = record.Normalized().Clone()
	var out strings.Builder
	title := memoryDocumentTitle(record)
	out.WriteString("# ")
	out.WriteString(title)
	out.WriteString("\n\n")
	writeParagraph(&out, record.Summary)
	body := firstDistinct(record.Summary, record.Narrative, record.Content)
	writeParagraph(&out, body)
	writeList(&out, "Facts", record.Facts)
	writeList(&out, "Lessons", record.Lessons)
	writeList(&out, "Preferences", record.Preferences)
	writeList(&out, "Warnings", record.Warnings)
	writeList(&out, "Decisions", record.Decisions)
	return strings.TrimSpace(out.String())
}

func memoryDocumentTitle(record memcore.MemoryRecord) string {
	topic := strings.TrimSpace(record.Topic)
	switch {
	case strings.HasPrefix(topic, "memory/"):
		date := strings.TrimSuffix(strings.TrimPrefix(topic, "memory/"), ".md")
		return firstNonBlank(date+" daily memory", "Daily memory")
	case topic == "personal_assistant_exchange":
		return "Conversation memory"
	case topic == "agent_result":
		return "Task outcome"
	case record.Layer == memcore.MemoryLayerExperience:
		return firstNonBlank("Experience: "+topic, "Experience")
	case record.Layer == memcore.MemoryLayerWorld:
		return firstNonBlank("Organization memory: "+topic, "Organization memory")
	case topic != "":
		return topic
	default:
		return "Memory"
	}
}

func writeParagraph(out *strings.Builder, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if out.Len() > 0 && !strings.HasSuffix(out.String(), "\n\n") {
		out.WriteString("\n\n")
	}
	out.WriteString(value)
	out.WriteString("\n\n")
}

func writeList(out *strings.Builder, heading string, values []string) {
	items := distinctNonBlank(values)
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(out, "## %s\n\n", heading)
	for _, item := range items {
		out.WriteString("- ")
		out.WriteString(item)
		out.WriteString("\n")
	}
	out.WriteString("\n")
}

func firstDistinct(summary string, values ...string) string {
	summary = strings.TrimSpace(summary)
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && value != summary {
			return value
		}
	}
	return ""
}

func distinctNonBlank(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
