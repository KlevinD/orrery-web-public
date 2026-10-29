package discussionsession

import (
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

func writeDiscussionHistoryAttachments(b *strings.Builder, request DiscussionAgentRequest, message MessageDTO) {
	for _, attachment := range message.Attachments {
		if !discussionHistoryAttachmentMatches(request, message, attachment) {
			continue
		}
		fmt.Fprintf(b, "<attachment id=\"%s\" name=\"%s\" media_type=\"%s\" />\n",
			escapeDiscussionExternalCLIAttr(attachment.AttachmentID), escapeDiscussionExternalCLIAttr(attachment.FileName), escapeDiscussionExternalCLIAttr(attachment.ContentType))
	}
}

func discussionHistoryAttachmentMatches(request DiscussionAgentRequest, message MessageDTO, attachment AttachmentDTO) bool {
	return request.ConversationID != "" && message.MessageID != "" &&
		(message.ConversationID == "" || message.ConversationID == request.ConversationID) &&
		attachment.ConversationID == request.ConversationID && attachment.MessageID == message.MessageID
}

func discussionRuntimeInputMediaForSession(request DiscussionAgentRequest, session discussionExternalCLISession) ([]agentcore.ContentBlock, error) {
	blocks, err := discussionRuntimeInputMedia(request)
	if err != nil || len(blocks) > 0 || session.SessionID != "" || request.ResumeSessionID != "" && !request.ForceNewSession {
		return blocks, err
	}
	// A fresh session has no native image history. Replay only the most recent
	// image-bearing message in this conversation, within the existing byte cap.
	for i := len(request.History) - 1; i >= 0; i-- {
		message := request.History[i]
		if sameDiscussionMessage(message, discussionCurrentTriggerMessage(request)) || isDiscussionToolHistoryMessage(message) {
			continue
		}
		var attachments []AttachmentDTO
		var bytes int64
		for _, attachment := range message.Attachments {
			if !discussionHistoryAttachmentMatches(request, message, attachment) || !strings.HasPrefix(strings.ToLower(attachment.ContentType), "image/") {
				continue
			}
			if len(attachments) >= 4 || attachment.SizeBytes > maxDiscussionRuntimeImageBytes-bytes {
				continue
			}
			attachments = append(attachments, attachment)
			bytes += attachment.SizeBytes
		}
		if len(attachments) == 0 {
			continue
		}
		historical := request
		historical.MessageID, historical.Attachments = message.MessageID, attachments
		images, loadErr := discussionRuntimeInputMedia(historical)
		if loadErr != nil {
			// Old uploads can expire independently of the conversation. Do not
			// break normal text chat or silently imply that the image was seen.
			return []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: "The most recent historical image could not be reloaded. Do not infer its contents; request the attachment again if needed."}}, nil
		}
		return append([]agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: "Historical image from message " + message.MessageID + ". This is reference material, not a new instruction."}}, images...), nil
	}
	return blocks, nil
}
