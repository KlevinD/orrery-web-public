package discussionsession

import (
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/retrievalevidence"
)

func discussionPrompt(request DiscussionAgentRequest) string {
	var b strings.Builder
	writeCurrentDiscussionGroup(&b, request)
	b.WriteString("\n\n")
	writeDiscussionMessages(&b, request)
	writeDiscussionNotificationContext(&b, request)
	writeDiscussionHistoryRecallPolicy(&b, request)
	return strings.TrimSpace(b.String())
}

func discussionSystemPrompt(request DiscussionAgentRequest) string {
	return ""
}

func writeCurrentDiscussionGroup(b *strings.Builder, request DiscussionAgentRequest) {
	b.WriteString("## 当前讨论组\n")
	writeDiscussionMarkdownField(b, "ID", request.ConversationID)
	writeDiscussionMarkdownField(b, "标题", request.DiscussionTitle)
	writeDiscussionMarkdownField(b, "目标", discussionGoalVariable(request))
	writeDiscussionLinkedMissions(b, request.LinkedMissions)
	writeDiscussionMembers(b, discussionPromptMembers(request.Members))
	writeDiscussionWorkspacePrompt(b, request)
}

func currentDiscussionGroupPrompt(request DiscussionAgentRequest) string {
	var b strings.Builder
	writeCurrentDiscussionGroup(&b, request)
	return strings.TrimSpace(b.String())
}

func discussionRuntimeVariables(request DiscussionAgentRequest) map[string]string {
	variables := map[string]string{
		agentcore.PromptBlockDiscussionGoal:    discussionGoalPrompt(request),
		agentcore.PromptBlockDiscussionMembers: discussionMembersPrompt(request),
		agentcore.PromptBlockWorkspaceInfo:     discussionWorkspaceInfoPrompt(request),
		agentcore.PromptBlockKnowledgeContext:  discussionKnowledgeContextPrompt(request),
	}
	for key, value := range variables {
		variables[key] = strings.TrimSpace(value)
	}
	return variables
}

func discussionMemoryContextPrompt(request DiscussionAgentRequest) string {
	pack := agentcore.ContextPack{}
	applyDiscussionAgentMemoryToContextPack(&pack, request.Memory)
	return agentcore.BuildMemoryContextPrompt(pack)
}

func writeDiscussionWorkspacePrompt(b *strings.Builder, request DiscussionAgentRequest) {
	context := discussionWorkspaceRuntimeContext(request)
	if strings.TrimSpace(context) == "" {
		return
	}
	b.WriteByte('\n')
	b.WriteString(context)
	b.WriteByte('\n')
}

func discussionWorkspaceRuntimeContext(request DiscussionAgentRequest) string {
	return discussionWorkspaceInfoPrompt(request)
}

func discussionWorkspaceInfoPrompt(request DiscussionAgentRequest) string {
	workspace := request.Workspace
	if strings.TrimSpace(workspace.RootDir) == "" && len(request.Attachments) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## 工作区信息\n")
	writeDiscussionMarkdownField(&b, "会话工作区根目录", workspace.RootDir)
	writeDiscussionMarkdownField(&b, "附件目录", workspace.AttachmentsDir)
	writeDiscussionMarkdownField(&b, "当前 Agent 工作目录", workspace.AgentWorkDir)
	writeDiscussionMarkdownField(&b, "其他 Agent 工作目录父目录", workspace.AgentsDir)
	b.WriteString("\nDelivery rule: user-facing files must use a durable Orrery attachment download or preview URL. Never expose server file paths, localhost/127.0.0.1 URLs, or temporary HTTP server ports.\n")
	if len(workspace.MemberWorkDirs) > 0 {
		b.WriteString("\n成员工作目录：\n")
		for _, item := range workspace.MemberWorkDirs {
			if strings.TrimSpace(item.Path) == "" {
				continue
			}
			name := firstNonBlank(item.DisplayName, item.AgentID)
			var details []string
			if item.Current {
				details = append(details, "当前 Agent")
			}
			if strings.TrimSpace(item.Role) != "" {
				details = append(details, "身份 "+strings.TrimSpace(item.Role))
			}
			suffix := ""
			if len(details) > 0 {
				suffix = "（" + strings.Join(details, "，") + "）"
			}
			fmt.Fprintf(&b, "- %s%s：%s\n", name, suffix, item.Path)
		}
	}
	if attachments := currentMessageAttachments(request); len(attachments) > 0 {
		b.WriteString("\n当前消息附件：\n")
		for _, attachment := range attachments {
			fileName := strings.TrimSpace(attachment.FileName)
			if fileName == "" {
				continue
			}
			fmt.Fprintf(&b, "- %s", fileName)
			if attachment.SizeBytes > 0 {
				fmt.Fprintf(&b, "（%d bytes）", attachment.SizeBytes)
			}
			if path := strings.TrimSpace(attachment.StoragePath); path != "" {
				fmt.Fprintf(&b, "，本地路径：%s", path)
			}
			if url := strings.TrimSpace(attachment.DownloadURL); url != "" {
				fmt.Fprintf(&b, "，下载地址：%s", url)
			}
			if url := strings.TrimSpace(attachment.PreviewURL); url != "" {
				fmt.Fprintf(&b, ", preview URL: %s", url)
			}
			if uploader := firstNonBlank(attachment.UploadedByName, attachment.UploadedBy); uploader != "" {
				fmt.Fprintf(&b, "，上传者：%s", uploader)
			}
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}

func currentMessageAttachments(request DiscussionAgentRequest) []AttachmentDTO {
	messageID := firstNonBlank(request.MessageID, request.TriggerMessage.MessageID)
	if strings.TrimSpace(messageID) == "" {
		return append([]AttachmentDTO(nil), request.Attachments...)
	}
	out := make([]AttachmentDTO, 0, len(request.Attachments))
	for _, attachment := range request.Attachments {
		attachmentMessageID := strings.TrimSpace(attachment.MessageID)
		if attachmentMessageID != "" && !strings.EqualFold(attachmentMessageID, messageID) {
			continue
		}
		out = append(out, attachment)
	}
	return out
}

func discussionGoalPrompt(request DiscussionAgentRequest) string {
	goal := discussionGoalVariable(request)
	if strings.TrimSpace(goal) == "" {
		return ""
	}
	return "## 讨论组目标\n" + strings.TrimSpace(goal)
}

func discussionMembersPrompt(request DiscussionAgentRequest) string {
	var b strings.Builder
	writeDiscussionMembers(&b, discussionPromptMembers(request.Members))
	return strings.TrimSpace(b.String())
}

func discussionKnowledgeContextPrompt(request DiscussionAgentRequest) string {
	if len(request.Knowledge.Results) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## 相关知识\n")
	b.WriteString(retrievalevidence.Guidance + "\n")
	wrote := false
	for _, result := range request.Knowledge.Results {
		snippet := strings.TrimSpace(result.Snippet)
		if snippet == "" {
			continue
		}
		source := firstNonBlank(result.DocumentName, result.DocumentID, result.ChunkID, "知识片段")
		fmt.Fprintf(&b, "- %s", source)
		var refs []string
		if id := strings.TrimSpace(result.KnowledgeBaseID); id != "" {
			refs = append(refs, "知识库 "+id)
		}
		if id := strings.TrimSpace(result.DocumentID); id != "" {
			refs = append(refs, "文档 "+id)
		}
		if id := strings.TrimSpace(result.ChunkID); id != "" {
			refs = append(refs, "片段 "+id)
		}
		if len(refs) > 0 {
			fmt.Fprintf(&b, "（%s）", strings.Join(refs, "，"))
		}
		if result.Score > 0 {
			fmt.Fprintf(&b, "，相关度 %.2f", result.Score)
		}
		b.WriteString("：")
		b.WriteString(snippet)
		b.WriteByte('\n')
		if result.Evidence != nil {
			b.WriteString(retrievalevidence.Prompt([]retrievalevidence.Item{*result.Evidence}) + "\n")
		}
		wrote = true
	}
	if !wrote {
		return ""
	}
	return strings.TrimSpace(b.String())
}

func discussionInputMessages(request DiscussionAgentRequest) []agentcore.RuntimeChatMessage {
	var messages []agentcore.RuntimeChatMessage
	for _, message := range discussionPromptHistory(request) {
		if isDiscussionToolHistoryMessage(message) {
			continue
		}
		if content := discussionSpeakerMessageContent(message.AuthorName, message.AuthorID, message.Content); content != "" {
			messages = append(messages, agentcore.RuntimeChatMessage{
				Role:    discussionRuntimeMessageRoleForRequest(request, message),
				Content: content,
			})
		}
	}
	var notificationContext strings.Builder
	writeDiscussionNotificationContext(&notificationContext, request)
	if notificationContext.Len() > 0 {
		messages = append(messages, agentcore.RuntimeChatMessage{Role: "user", Content: notificationContext.String()})
	}
	trigger := discussionCurrentTriggerMessage(request)
	if content := discussionSpeakerMessageContent(trigger.AuthorName, trigger.AuthorID, trigger.Content); content != "" {
		messages = append(messages, agentcore.RuntimeChatMessage{Role: "user", Content: content})
	}
	return messages
}

func discussionCurrentUserID(request DiscussionAgentRequest) string {
	if trigger := discussionCurrentTriggerMessage(request); strings.TrimSpace(trigger.AuthorID) != "" {
		return strings.TrimSpace(trigger.AuthorID)
	}
	if viewerID := strings.TrimSpace(request.Viewer.ViewerID); viewerID != "" {
		return viewerID
	}
	for _, member := range request.Members {
		if discussionPromptMemberKind(member.Kind) == "user" && strings.TrimSpace(member.ID) != "" {
			return strings.TrimSpace(member.ID)
		}
	}
	return ""
}

func discussionCurrentUserDisplayName(request DiscussionAgentRequest) string {
	trigger := discussionCurrentTriggerMessage(request)
	if strings.TrimSpace(trigger.AuthorName) != "" {
		return strings.TrimSpace(trigger.AuthorName)
	}
	if strings.TrimSpace(trigger.AuthorID) != "" {
		for _, member := range request.Members {
			if strings.EqualFold(strings.TrimSpace(member.ID), strings.TrimSpace(trigger.AuthorID)) &&
				strings.TrimSpace(member.DisplayName) != "" {
				return strings.TrimSpace(member.DisplayName)
			}
		}
	}
	if strings.TrimSpace(request.Viewer.DisplayName) != "" {
		return request.Viewer.DisplayName
	}
	viewerID := strings.TrimSpace(request.Viewer.ViewerID)
	if viewerID == "" {
		for _, member := range request.Members {
			if discussionPromptMemberKind(member.Kind) == "user" && strings.TrimSpace(member.DisplayName) != "" {
				return member.DisplayName
			}
		}
		return "用户"
	}
	for _, member := range request.Members {
		if strings.EqualFold(strings.TrimSpace(member.ID), viewerID) {
			return member.DisplayName
		}
	}
	return ""
}

func discussionCurrentTriggerMessage(request DiscussionAgentRequest) MessageDTO {
	trigger := request.TriggerMessage
	if strings.TrimSpace(trigger.Content) == "" {
		trigger.Content = request.Content
	}
	if strings.TrimSpace(trigger.MessageID) == "" {
		trigger.MessageID = request.MessageID
	}
	if strings.TrimSpace(trigger.ConversationID) == "" {
		trigger.ConversationID = request.ConversationID
	}
	if strings.TrimSpace(trigger.AuthorID) == "" {
		trigger.AuthorID = firstNonBlank(discussionViewerID(request.Viewer), discussionDefaultUserMemberID(request))
	}
	if strings.TrimSpace(trigger.AuthorName) == "" {
		trigger.AuthorName = firstNonBlank(discussionViewerDisplayName(request), discussionMemberDisplayName(request, trigger.AuthorID))
	}
	if strings.TrimSpace(trigger.Role) == "" {
		trigger.Role = string(discussionViewerKind(request.Viewer))
		if trigger.Role == "" || trigger.Role == string(ViewerKindHuman) {
			trigger.Role = "user"
		}
	}
	return trigger
}

func discussionViewerID(viewer ViewerRef) string {
	return strings.TrimSpace(viewer.ViewerID)
}

func discussionViewerDisplayName(request DiscussionAgentRequest) string {
	if name := strings.TrimSpace(request.Viewer.DisplayName); name != "" {
		return name
	}
	viewerID := strings.TrimSpace(request.Viewer.ViewerID)
	if viewerID == "" {
		return ""
	}
	for _, member := range request.Members {
		if strings.EqualFold(strings.TrimSpace(member.ID), viewerID) && strings.TrimSpace(member.DisplayName) != "" {
			return strings.TrimSpace(member.DisplayName)
		}
	}
	return ""
}

func discussionDefaultUserMemberID(request DiscussionAgentRequest) string {
	for _, member := range request.Members {
		if discussionPromptMemberKind(member.Kind) == "user" && strings.TrimSpace(member.ID) != "" {
			return strings.TrimSpace(member.ID)
		}
	}
	return ""
}

func discussionMemberDisplayName(request DiscussionAgentRequest, memberID string) string {
	memberID = strings.TrimSpace(memberID)
	if memberID == "" {
		return ""
	}
	for _, member := range request.Members {
		if strings.EqualFold(strings.TrimSpace(member.ID), memberID) && strings.TrimSpace(member.DisplayName) != "" {
			return strings.TrimSpace(member.DisplayName)
		}
	}
	return ""
}

func discussionViewerKind(viewer ViewerRef) ViewerKind {
	if viewer.ViewerKind != "" {
		return viewer.ViewerKind
	}
	return ViewerKindUser
}

func discussionRuntimeMessageRole(agentID string, message MessageDTO) string {
	if strings.TrimSpace(agentID) != "" &&
		strings.EqualFold(strings.TrimSpace(message.AuthorID), strings.TrimSpace(agentID)) {
		return "assistant"
	}
	return "user"
}

func discussionRuntimeMessageRoleForRequest(request DiscussionAgentRequest, message MessageDTO) string {
	if isPersonalAssistantConversationKind(request.ConversationKind) {
		for _, agentID := range []string{request.LogicalAgentID, request.AgentID} {
			if discussionRuntimeMessageRole(agentID, message) == "assistant" {
				return "assistant"
			}
		}
		return "user"
	}
	return discussionRuntimeMessageRole(request.AgentID, message)
}

func discussionSpeakerMessageContent(authorName, authorID, content string) string {
	body := strings.TrimSpace(content)
	if body == "" {
		return ""
	}
	speaker := firstNonBlank(authorName, authorID, "未知成员")
	if id := strings.TrimSpace(authorID); id != "" {
		return "消息记录：作者=" + strings.TrimSpace(speaker) + "，ID=" + id + "\n内容：\n" + body
	}
	return "消息记录：作者=" + strings.TrimSpace(speaker) + "\n内容：\n" + body
}

func writeDiscussionMessages(b *strings.Builder, request DiscussionAgentRequest) {
	b.WriteString("## 讨论消息\n")
	wroteHistory := false
	for _, message := range discussionPromptHistory(request) {
		if isDiscussionToolHistoryMessage(message) {
			continue
		}
		if content := discussionSpeakerMessageContent(message.AuthorName, message.AuthorID, message.Content); content != "" {
			if !wroteHistory {
				b.WriteString("历史消息：\n")
				wroteHistory = true
			}
			b.WriteString("- ")
			b.WriteString(strings.ReplaceAll(content, "\n", "\n  "))
			b.WriteByte('\n')
		}
	}
	if !wroteHistory {
		b.WriteString("历史消息：无。\n")
	}
	if content := discussionSpeakerMessageContent(discussionCurrentUserDisplayName(request), discussionCurrentUserID(request), request.Content); content != "" {
		b.WriteString("\n当前用户消息：\n")
		b.WriteString(content)
		b.WriteByte('\n')
	}
}

func discussionGroupGoal(request DiscussionAgentRequest) string {
	if goal := discussionGoalVariable(request); goal != "" {
		return goal
	}
	return strings.TrimSpace(request.DiscussionTitle)
}

func discussionGoalVariable(request DiscussionAgentRequest) string {
	if goal := strings.TrimSpace(request.DiscussionGoal); goal != "" {
		return goal
	}
	if len(request.LinkedMissions) > 0 {
		var parts []string
		for _, mission := range request.LinkedMissions {
			missionID := strings.TrimSpace(mission.MissionID)
			if missionID == "" {
				continue
			}
			title := firstNonBlank(mission.Title, missionID)
			status := firstNonBlank(mission.Status, "unknown")
			parts = append(parts, fmt.Sprintf("%s：%s（%s）", missionID, title, status))
		}
		if len(parts) > 0 {
			return "当前讨论组已绑定 Mission：" + strings.Join(parts, "；")
		}
	}
	return ""
}

func writeDiscussionLinkedMissions(b *strings.Builder, missions []LinkedMissionDTO) {
	if len(missions) == 0 {
		return
	}
	b.WriteString("\n已绑定 Mission：\n")
	for _, mission := range missions {
		if strings.TrimSpace(mission.MissionID) == "" {
			continue
		}
		fmt.Fprintf(b, "- %s：%s（%s）\n", strings.TrimSpace(mission.MissionID), firstNonBlank(mission.Title, mission.MissionID), firstNonBlank(mission.Status, "unknown"))
	}
}

type discussionPromptMember struct {
	Kind        string
	ID          string
	DisplayName string
	Role        string
}

func discussionPromptMembers(members []ConversationMemberDTO) []discussionPromptMember {
	out := make([]discussionPromptMember, 0, len(members))
	seen := map[string]bool{}
	for _, member := range members {
		kind := discussionPromptMemberKind(member.Kind)
		id := strings.TrimSpace(member.ID)
		displayName := strings.TrimSpace(member.DisplayName)
		if id == "" || displayName == "" {
			continue
		}
		key := kind + "\x00" + id
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, discussionPromptMember{
			Kind:        kind,
			ID:          id,
			DisplayName: displayName,
			Role:        strings.TrimSpace(member.Role),
		})
	}
	return out
}

func discussionPromptMemberKind(kind string) string {
	if strings.EqualFold(strings.TrimSpace(kind), "agent") {
		return "agent"
	}
	return "user"
}

func writeDiscussionMembers(b *strings.Builder, members []discussionPromptMember) {
	b.WriteString("\n当前讨论组成员：\n")
	if len(members) == 0 {
		b.WriteString("- 暂无已提供成员。\n")
		b.WriteString(discussionMentionGuidance())
		return
	}
	for _, member := range members {
		display := "@" + strings.TrimPrefix(member.DisplayName, "@")
		line := fmt.Sprintf("- %s：%s %s", display, member.Kind, member.ID)
		if strings.TrimSpace(member.Role) != "" {
			line += "，身份 " + strings.TrimSpace(member.Role)
		}
		if mention := discussionPromptMemberLink(member); mention != "" {
			line += "，通知链接 " + mention
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteString(discussionMentionGuidance())
}

func discussionMentionGuidance() string {
	return "\n注意：普通提及时只写 @显示名；凡是希望对方看到、回应、确认、冒泡、接手、处理、执行或被明确点名参与时，都必须原样使用成员列表中完整的 Markdown 通知链接，例如 [@显示名](mention://agent/profile-id)，不能只输出裸 mention://agent/... URI。比如“出来冒个泡”“请确认”“帮忙看一下”都属于期待对方回应或处理，必须用完整通知链接。\n"
}

func discussionPromptMemberLink(member discussionPromptMember) string {
	if member.Kind != "agent" && member.Kind != "user" {
		return ""
	}
	label := strings.TrimPrefix(firstNonBlank(member.DisplayName, member.ID), "@")
	return fmt.Sprintf("[@%s](mention://%s/%s)", label, member.Kind, member.ID)
}

func writeDiscussionMarkdownField(b *strings.Builder, label string, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	fmt.Fprintf(b, "- %s：%s\n", label, strings.TrimSpace(value))
}
