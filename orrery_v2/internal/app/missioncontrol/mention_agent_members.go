package missioncontrol

import (
	"fmt"
	"strings"
)

type mentionPromptMember struct {
	Kind        string
	ID          string
	DisplayName string
	RoleName    string
	MissionRole string
	Status      string
	Source      string
}

const maxMentionPromptMembers = 50

func writeMentionDiscussionMembers(b *strings.Builder, members []mentionPromptMember) {
	b.WriteString("\n当前讨论组成员：\n")
	limit := len(members)
	if limit > maxMentionPromptMembers {
		limit = maxMentionPromptMembers
	}
	if limit == 0 {
		b.WriteString("- 暂无已提供成员。\n")
		b.WriteString(mentionPromptGuidance())
		return
	}
	for _, member := range members[:limit] {
		b.WriteString("- ")
		b.WriteString(mentionPromptMemberDisplay(member))
		b.WriteString("：")
		b.WriteString(strings.TrimSpace(member.Kind))
		b.WriteByte(' ')
		b.WriteString(strings.TrimSpace(member.ID))
		if role := firstNonEmptyMentionValue(member.MissionRole, member.RoleName); role != "" {
			b.WriteString("，身份 ")
			b.WriteString(role)
		}
		if status := strings.TrimSpace(member.Status); status != "" {
			b.WriteString("，状态 ")
			b.WriteString(status)
		}
		if source := strings.TrimSpace(member.Source); source != "" {
			b.WriteString("，来源 ")
			b.WriteString(source)
		}
		if mention := mentionPromptMemberLink(member); mention != "" {
			b.WriteString("，通知链接 ")
			b.WriteString(mention)
		}
		b.WriteByte('\n')
	}
	if len(members) > limit {
		fmt.Fprintf(b, "- 另有 %d 位成员因长度限制省略。\n", len(members)-limit)
	}
	b.WriteString(mentionPromptGuidance())
}

func mentionPromptGuidance() string {
	return "\n注意：普通提及时只写 @显示名；凡是希望对方看到、回应、确认、冒泡、接手、处理、执行或被明确点名参与时，都必须使用对应的 mention:// 通知链接。比如“出来冒个泡”“请确认”“帮忙看一下”都属于期待对方回应或处理，必须用通知链接。\n"
}

func mentionDiscussionMembers(view MissionControlViewDTO, parent MessageDTO) []mentionPromptMember {
	var members []mentionPromptMember
	seen := map[string]struct{}{}
	userIDByDisplayName := mentionDiscussionUserIDsByDisplayName(view, parent)
	add := func(member mentionPromptMember) {
		member.Kind = strings.TrimSpace(member.Kind)
		member.ID = strings.TrimSpace(member.ID)
		if member.Kind == "" || member.ID == "" {
			return
		}
		member.DisplayName = firstNonEmptyMentionValue(member.DisplayName, member.ID)
		member.RoleName = strings.TrimSpace(member.RoleName)
		member.MissionRole = strings.TrimSpace(member.MissionRole)
		member.Status = strings.TrimSpace(member.Status)
		member.Source = strings.TrimSpace(member.Source)
		key := member.Kind + "\x00" + member.ID
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		members = append(members, member)
	}
	for _, member := range view.Team.Members {
		kind := "agent"
		id := member.AgentID
		if mentionTeamMemberIsHuman(member) {
			kind = "user"
			if userID := mentionUserIDFromHumanAgentID(member.AgentID); userID != "" {
				id = userID
			} else if userID := userIDByDisplayName[strings.ToLower(firstNonEmptyMentionValue(member.DisplayName, member.RealName))]; userID != "" {
				id = userID
			}
		}
		add(mentionPromptMember{
			Kind:        kind,
			ID:          id,
			DisplayName: firstNonEmptyMentionValue(member.DisplayName, member.RealName, member.AgentID),
			RoleName:    member.RoleName,
			MissionRole: member.MissionRole,
			Status:      member.Status,
			Source:      "mission_team",
		})
	}
	addMentionMessageAuthor := func(message MessageDTO) {
		kind := strings.ToLower(strings.TrimSpace(message.AuthorType))
		switch kind {
		case "human":
			kind = "user"
		case "user", "agent":
		default:
			if strings.Contains(strings.ToLower(message.AuthorID), "agent") {
				kind = "agent"
			} else {
				kind = "user"
			}
		}
		add(mentionPromptMember{
			Kind:        kind,
			ID:          message.AuthorID,
			DisplayName: firstNonEmptyMentionValue(message.AuthorDisplayName, message.AuthorID),
			Source:      "discussion_author",
		})
	}
	addMentionMessageAuthor(parent)
	for _, message := range view.Discussion.Messages {
		addMentionMessageAuthor(message)
	}
	return members
}

func mentionDiscussionUserIDsByDisplayName(view MissionControlViewDTO, parent MessageDTO) map[string]string {
	out := map[string]string{}
	remember := func(message MessageDTO) {
		kind := strings.ToLower(strings.TrimSpace(message.AuthorType))
		if kind == "human" {
			kind = "user"
		}
		if kind != "user" || strings.TrimSpace(message.AuthorID) == "" {
			return
		}
		name := strings.ToLower(firstNonEmptyMentionValue(message.AuthorDisplayName, message.AuthorID))
		if name != "" {
			out[name] = strings.TrimSpace(message.AuthorID)
		}
	}
	remember(parent)
	for _, message := range view.Discussion.Messages {
		remember(message)
	}
	return out
}

func mentionTeamMemberIsHuman(member TeamMemberDTO) bool {
	switch strings.ToLower(strings.TrimSpace(member.AgentKind)) {
	case "human":
		return true
	case "ai", "agent", "composite":
		return false
	}
	joined := strings.ToLower(strings.Join([]string{member.AgentID, member.DisplayName, member.RealName, member.RoleName, member.MissionRole, member.Department, member.Position}, " "))
	return strings.Contains(joined, "human") ||
		strings.Contains(joined, "realhuman") ||
		strings.Contains(joined, "real human") ||
		strings.Contains(joined, "真人")
}

func mentionUserIDFromHumanAgentID(agentID string) string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || !strings.HasPrefix(agentID, "human-") {
		return ""
	}
	id := strings.TrimPrefix(agentID, "human-")
	if before, _, ok := strings.Cut(id, ":"); ok {
		id = before
	}
	return strings.TrimSpace(id)
}

func mentionPromptMemberLink(member mentionPromptMember) string {
	kind := strings.TrimSpace(member.Kind)
	if kind != "agent" && kind != "user" {
		return ""
	}
	id := strings.TrimSpace(member.ID)
	if id == "" {
		return ""
	}
	label := strings.TrimPrefix(firstNonEmptyMentionValue(member.DisplayName, member.RoleName, member.ID), "@")
	if label == "" {
		label = id
	}
	return fmt.Sprintf("[@%s](mention://%s/%s)", label, kind, id)
}

func mentionPromptMemberDisplay(member mentionPromptMember) string {
	label := strings.TrimPrefix(firstNonEmptyMentionValue(member.DisplayName, member.RoleName, member.ID), "@")
	if label == "" {
		return ""
	}
	return "@" + label
}
