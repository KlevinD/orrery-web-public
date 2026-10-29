package discussionsession

import (
	"context"
	"strings"

	"orrery_v2/internal/platform/db"
)

// MutateMemberManagedSource atomically grants or revokes one ownership source.
// It prevents concurrent reconcilers from overwriting unrelated sources.
func (r *Repository) MutateMemberManagedSource(ctx context.Context, member ConversationMemberRecord, source string, grant bool) (ConversationMemberRecord, bool, error) {
	if r == nil || r.router == nil {
		return ConversationMemberRecord{}, false, ErrServiceDependency
	}
	member.ConversationID = strings.TrimSpace(member.ConversationID)
	member.MemberKind = strings.TrimSpace(member.MemberKind)
	member.MemberID = strings.TrimSpace(member.MemberID)
	source = strings.TrimSpace(source)
	if member.ConversationID == "" || member.MemberKind == "" || member.MemberID == "" || source == "" {
		return ConversationMemberRecord{}, false, ErrInvalidRequest
	}
	key := conversationMemberKey(member)
	for attempt := 0; attempt < 16; attempt++ {
		currentRecord, exists, err := r.router.GetRecord(ctx, memberTable, key)
		if err != nil {
			return ConversationMemberRecord{}, false, err
		}
		if !exists {
			if !grant {
				return ConversationMemberRecord{}, false, nil
			}
			createdMember := grantConversationMemberManagedSource(member, source)
			replacement, err := conversationMemberDBRecord(createdMember)
			if err != nil {
				return ConversationMemberRecord{}, false, err
			}
			created, err := r.router.CreateRecord(ctx, memberTable, key, replacement)
			if err != nil {
				return ConversationMemberRecord{}, false, err
			}
			if created {
				return createdMember, true, nil
			}
			continue
		}
		var current ConversationMemberRecord
		if err := decodePayload(currentRecord, &current); err != nil {
			return ConversationMemberRecord{}, false, err
		}
		current = normalizeConversationMemberManagedSources(current)
		if grant {
			if strings.TrimSpace(current.ExplicitGrantorID) != "" || conversationMemberHasManagedSource(current, source) {
				return current, true, nil
			}
			current = grantConversationMemberManagedSource(current, source)
		} else {
			if !conversationMemberHasManagedSource(current, source) {
				return current, true, nil
			}
			current = revokeConversationMemberManagedSource(current, source)
			if len(conversationMemberManagedSources(current)) == 0 && strings.TrimSpace(current.ExplicitGrantorID) == "" {
				deleted, err := r.router.CompareAndDeleteRecord(ctx, memberTable, key, currentRecord)
				if err != nil {
					return ConversationMemberRecord{}, false, err
				}
				if deleted {
					return ConversationMemberRecord{}, false, nil
				}
				continue
			}
		}
		replacement, err := conversationMemberDBRecord(current)
		if err != nil {
			return ConversationMemberRecord{}, false, err
		}
		updated, err := r.router.CompareAndSwapRecord(ctx, memberTable, key, currentRecord, replacement)
		if err != nil {
			return ConversationMemberRecord{}, false, err
		}
		if updated {
			return current, true, nil
		}
	}
	return ConversationMemberRecord{}, false, ErrRevisionConflict
}

func conversationMemberKey(member ConversationMemberRecord) string {
	return strings.TrimSpace(member.ConversationID) + "|" + strings.TrimSpace(member.MemberKind) + "|" + strings.TrimSpace(member.MemberID)
}

func conversationMemberDBRecord(member ConversationMemberRecord) (db.Record, error) {
	member = normalizeConversationMemberManagedSources(member)
	payload, err := encodePayload(member)
	if err != nil {
		return nil, err
	}
	return db.Record{
		"conversation_id":   member.ConversationID,
		"member_kind":       member.MemberKind,
		"member_id":         member.MemberID,
		"created_at":        formatTime(member.CreatedAt),
		conversationPayload: payload,
	}, nil
}
