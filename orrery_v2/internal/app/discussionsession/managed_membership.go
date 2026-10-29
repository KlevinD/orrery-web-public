package discussionsession

import (
	"strings"
)

func normalizeConversationMemberManagedSources(member ConversationMemberRecord) ConversationMemberRecord {
	sources := make([]string, 0, len(member.ManagedSources)+1)
	if source := strings.TrimSpace(member.ManagedSource); source != "" {
		sources = append(sources, source)
	}
	sources = append(sources, member.ManagedSources...)
	seen := make(map[string]struct{}, len(sources))
	member.ManagedSources = member.ManagedSources[:0]
	for _, source := range sources {
		source = strings.TrimSpace(source)
		if source == "" {
			continue
		}
		if _, ok := seen[source]; ok {
			continue
		}
		seen[source] = struct{}{}
		member.ManagedSources = append(member.ManagedSources, source)
	}
	member.ManagedSource = ""
	if len(member.ManagedSources) > 0 {
		member.ManagedSource = member.ManagedSources[0]
	}
	return member
}

func conversationMemberManagedSources(member ConversationMemberRecord) []string {
	return normalizeConversationMemberManagedSources(member).ManagedSources
}

func conversationMemberHasManagedSource(member ConversationMemberRecord, source string) bool {
	source = strings.TrimSpace(source)
	for _, candidate := range conversationMemberManagedSources(member) {
		if candidate == source {
			return true
		}
	}
	return false
}

func grantConversationMemberManagedSource(member ConversationMemberRecord, source string) ConversationMemberRecord {
	member.ManagedSources = conversationMemberManagedSources(member)
	member.ManagedSource = strings.TrimSpace(source)
	return normalizeConversationMemberManagedSources(member)
}

func revokeConversationMemberManagedSource(member ConversationMemberRecord, source string) ConversationMemberRecord {
	source = strings.TrimSpace(source)
	retained := make([]string, 0, len(member.ManagedSources))
	for _, candidate := range conversationMemberManagedSources(member) {
		if candidate != source {
			retained = append(retained, candidate)
		}
	}
	member.ManagedSource = ""
	member.ManagedSources = retained
	return normalizeConversationMemberManagedSources(member)
}
