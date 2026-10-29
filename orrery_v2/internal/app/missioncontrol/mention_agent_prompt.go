package missioncontrol

import (
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

func mentionAgentPrompt(profile agentcore.AgentProfile, view MissionControlViewDTO, parent MessageDTO) string {
	return mentionAgentPromptWithResources(profile, agentcore.AgentBindings{}, view, parent)
}

func mentionAgentPromptWithResources(_ agentcore.AgentProfile, _ agentcore.AgentBindings, view MissionControlViewDTO, parent MessageDTO) string {
	var b strings.Builder
	writeMentionPromptInstruction(&b)
	b.WriteString("\n\n")
	writeMentionCurrentDiscussionGroup(&b, view, parent)
	b.WriteString("\n\n")
	writeMentionDiscussionMessages(&b, view.Discussion, parent)
	return strings.TrimSpace(b.String())
}

func writeMentionPromptInstruction(b *strings.Builder) {
	b.WriteString("## 当前提及任务\n")
	b.WriteString("你在讨论组中被 @ 提及。请结合已发送的角色设定、能力资源、当前讨论组信息、历史消息和当前用户消息工作，并直接回复这条讨论消息。\n")
}

func writeMentionCurrentDiscussionGroup(b *strings.Builder, view MissionControlViewDTO, parent MessageDTO) {
	b.WriteString("## 当前讨论组\n")
	writeMentionMarkdownField(b, "ID", firstNonEmptyMentionValue(view.Discussion.DiscussionGroupID, parent.DiscussionGroupID))
	writeMentionMarkdownField(b, "标题", view.Mission.Title)
	writeMentionMarkdownField(b, "目标", mentionDiscussionGroupGoal(view, parent))
	writeMentionLinkedMission(b, view.Mission)
	members := mentionDiscussionMembers(view, parent)
	writeMentionDiscussionMembers(b, members)
}

func mentionDiscussionGroupGoal(view MissionControlViewDTO, parent MessageDTO) string {
	if strings.TrimSpace(view.Mission.MissionID) == "" {
		return firstNonEmptyMentionValue(view.Mission.FinalGoal, strings.TrimSpace(parent.Body), "完成当前讨论组任务。")
	}
	title := firstNonEmptyMentionValue(view.Mission.Title, view.Mission.MissionID)
	status := firstNonEmptyMentionValue(view.Mission.Status, "unknown")
	goal := firstNonEmptyMentionValue(view.Mission.FinalGoal, strings.TrimSpace(parent.Body), title)
	return fmt.Sprintf("当前讨论组已绑定 Mission：%s：%s（%s）；目标：%s", view.Mission.MissionID, title, status, goal)
}

func writeMentionLinkedMission(b *strings.Builder, mission MissionDTO) {
	if strings.TrimSpace(mission.MissionID) == "" {
		return
	}
	b.WriteString("\n已绑定 Mission：\n")
	fmt.Fprintf(b, "- %s：%s（%s）", strings.TrimSpace(mission.MissionID), firstNonEmptyMentionValue(mission.Title, mission.MissionID), firstNonEmptyMentionValue(mission.Status, "unknown"))
	if strings.TrimSpace(mission.FinalGoal) != "" {
		b.WriteString("；目标：")
		b.WriteString(strings.TrimSpace(mission.FinalGoal))
	}
	b.WriteByte('\n')
}

func writeMentionDiscussionMessages(b *strings.Builder, discussion DiscussionDTO, parent MessageDTO) {
	b.WriteString("## 讨论消息\n")
	start := len(discussion.Messages) - 5
	if start < 0 {
		start = 0
	}
	wroteHistory := false
	for _, message := range discussion.Messages[start:] {
		if strings.TrimSpace(message.Body) == "" || (strings.TrimSpace(parent.MessageID) != "" && message.MessageID == parent.MessageID) {
			continue
		}
		if !wroteHistory {
			b.WriteString("历史消息：\n")
			wroteHistory = true
		}
		b.WriteString("- ")
		b.WriteString(strings.ReplaceAll(mentionSpeakerMessageContent(message), "\n", "\n  "))
		b.WriteByte('\n')
	}
	if !wroteHistory {
		b.WriteString("历史消息：无。\n")
	}
	if strings.TrimSpace(parent.Body) != "" {
		b.WriteString("\n当前用户消息：\n")
		b.WriteString(mentionSpeakerMessageContent(parent))
		b.WriteByte('\n')
	}
}

func mentionMessageRole(message MessageDTO) string {
	switch strings.ToLower(strings.TrimSpace(message.AuthorType)) {
	case "agent", "assistant":
		return "assistant"
	default:
		return "user"
	}
}

func mentionSpeakerMessageContent(message MessageDTO) string {
	body := strings.TrimSpace(message.Body)
	if body == "" {
		return ""
	}
	speaker := firstNonEmptyMentionValue(message.AuthorDisplayName, message.AuthorID, "未知成员")
	if id := strings.TrimSpace(message.AuthorID); id != "" {
		return strings.TrimSpace(speaker) + " (" + id + "):\n" + body
	}
	return strings.TrimSpace(speaker) + ":\n" + body
}

func writeMentionMarkdownField(b *strings.Builder, label string, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	fmt.Fprintf(b, "- %s：%s\n", label, strings.TrimSpace(value))
}

func discussionSummary(discussion DiscussionDTO, parent MessageDTO) string {
	var parts []string
	for i := len(discussion.Messages) - 1; i >= 0 && len(parts) < 5; i-- {
		body := strings.TrimSpace(discussion.Messages[i].Body)
		if body == "" {
			continue
		}
		parts = append(parts, truncateMentionText(body, 180))
	}
	if len(parts) == 0 {
		return truncateMentionText(parent.Body, 180)
	}
	return strings.Join(parts, " | ")
}

func findDiscussionMessage(messages []MessageDTO, messageID string) MessageDTO {
	for _, message := range messages {
		if message.MessageID == messageID {
			return message
		}
	}
	return MessageDTO{}
}

func firstNonEmptyMentionValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func nonEmptyMentionValues(values ...string) []string {
	var out []string
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func truncateMentionText(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	if max <= 3 {
		return value[:max]
	}
	return value[:max-3] + "..."
}
