package discussionsession

import "strings"

func sourceMessageByID(messages []MessageRecord, messageID string) MessageRecord {
	messageID = strings.TrimSpace(messageID)
	if messageID != "" {
		for _, message := range messages {
			if strings.TrimSpace(message.MessageID) == messageID {
				return message
			}
		}
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.TrimSpace(messages[i].Role) == "user" {
			return messages[i]
		}
	}
	if len(messages) > 0 {
		return messages[len(messages)-1]
	}
	return MessageRecord{}
}

func runAgentIDByID(runs []RunRecord, runID string) string {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return ""
	}
	for _, run := range runs {
		if strings.TrimSpace(run.RunID) == runID {
			return strings.TrimSpace(run.AgentID)
		}
	}
	return ""
}
