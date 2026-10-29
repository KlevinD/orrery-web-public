package discussionsession

import "strings"

const (
	directTargetKindAgent = "agent"
	directTargetKindUser  = "user"
)

type directConversationTargetRef struct {
	Kind string
	ID   string
}

func directTargetFromCommand(cmd SendMessageCommand) directConversationTargetRef {
	target := directConversationTargetRef{
		Kind: normalizeDirectTargetKind(cmd.DirectTargetKind),
		ID:   strings.TrimSpace(cmd.DirectTargetID),
	}
	if target.ID != "" {
		return target
	}
	if agentID := strings.TrimSpace(cmd.DirectAgentID); agentID != "" {
		return directConversationTargetRef{Kind: directTargetKindAgent, ID: agentID}
	}
	return directConversationTargetRef{}
}

func directTargetFromRecord(record ConversationRecord) directConversationTargetRef {
	target := directConversationTargetRef{
		Kind: normalizeDirectTargetKind(record.DirectTargetKind),
		ID:   strings.TrimSpace(record.DirectTargetID),
	}
	if target.ID != "" {
		if target.Kind == "" && strings.TrimSpace(record.DirectAgentID) != "" {
			target.Kind = directTargetKindAgent
		}
		return target
	}
	if agentID := strings.TrimSpace(record.DirectAgentID); agentID != "" {
		return directConversationTargetRef{Kind: directTargetKindAgent, ID: agentID}
	}
	return directConversationTargetRef{}
}

func directAgentIDFromRecord(record ConversationRecord) string {
	if agentID := strings.TrimSpace(record.DirectAgentID); agentID != "" {
		return agentID
	}
	target := directTargetFromRecord(record)
	if target.Kind == directTargetKindAgent {
		return target.ID
	}
	return ""
}

func normalizeDirectTargetKind(kind string) string {
	switch strings.TrimSpace(strings.ToLower(kind)) {
	case "":
		return ""
	case directTargetKindAgent:
		return directTargetKindAgent
	case directTargetKindUser, "human", "person":
		return directTargetKindUser
	default:
		return strings.TrimSpace(strings.ToLower(kind))
	}
}

func directTargetMemberKind(kind string) string {
	if normalizeDirectTargetKind(kind) == directTargetKindAgent {
		return "agent"
	}
	return "user"
}
