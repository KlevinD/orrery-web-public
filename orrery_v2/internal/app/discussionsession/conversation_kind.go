package discussionsession

import "strings"

const (
	ConversationKindGroup             = "group"
	ConversationKindDirect            = "direct"
	ConversationKindPersonalAssistant = "personal_assistant"
)

func normalizeCreatableConversationKind(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", ConversationKindGroup:
		return ConversationKindGroup, true
	case ConversationKindPersonalAssistant:
		return ConversationKindPersonalAssistant, true
	default:
		return "", false
	}
}

func normalizedStoredConversationKind(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ConversationKindGroup
	}
	return value
}

func isPersonalAssistantConversationKind(value string) bool {
	return normalizedStoredConversationKind(value) == ConversationKindPersonalAssistant
}
