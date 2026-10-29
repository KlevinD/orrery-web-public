package discussionsession

import "strings"

const maxMessageMentions = 64

func normalizeMessageMentions(mentions []MessageMentionDTO, members []ConversationMemberRecord) ([]MessageMentionRecord, error) {
	if len(mentions) == 0 {
		return nil, nil
	}
	if len(mentions) > maxMessageMentions {
		return nil, ErrInvalidRequest
	}
	byTarget := make(map[string]ConversationMemberRecord, len(members))
	for _, member := range members {
		kind := strings.TrimSpace(member.MemberKind)
		id := strings.TrimSpace(member.MemberID)
		if kind != "" && id != "" {
			byTarget[kind+"\x00"+id] = member
		}
	}
	out := make([]MessageMentionRecord, 0, len(mentions))
	seen := make(map[string]bool, len(mentions))
	for _, mention := range mentions {
		kind := strings.ToLower(strings.TrimSpace(mention.TargetKind))
		id := strings.TrimSpace(mention.TargetID)
		if kind != "agent" && kind != "user" || id == "" {
			return nil, ErrInvalidRequest
		}
		key := kind + "\x00" + id
		member, ok := byTarget[key]
		if !ok {
			return nil, ErrForbidden
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, MessageMentionRecord{TargetKind: kind, TargetID: id, DisplayName: member.DisplayName})
	}
	return out, nil
}
