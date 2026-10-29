package discussionsession

import "time"

func assistantMessageRecordWithDisplayTimeline(messageID string, conversationID string, authorID string, authorName string, content string, response DiscussionAgentResponse, createdAt time.Time, viewer ViewerRef) MessageRecord {
	return MessageRecord{
		MessageID:      messageID,
		ConversationID: conversationID,
		Role:           "assistant",
		AuthorID:       authorID,
		AuthorName:     authorName,
		Content:        content,
		Metadata:       displayTimelineMessageMetadata(response, content, viewer),
		CreatedAt:      createdAt,
	}
}

func correlateMessageToRun(message MessageRecord, run RunRecord) MessageRecord {
	message.TurnID = run.TurnID
	message.RunID = run.RunID
	message.MissionID = run.MissionID
	message.MissionTaskID = run.MissionTaskID
	return message
}
