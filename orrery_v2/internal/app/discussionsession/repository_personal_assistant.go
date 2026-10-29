package discussionsession

import (
	"context"
	"strings"
)

func (r *Repository) findLatestPersonalAssistantConversation(ctx context.Context, tenantID, viewerID, ownerID string, identityIDs ...string) (ConversationRecord, bool, error) {
	if r == nil || r.router == nil {
		return ConversationRecord{}, false, ErrServiceDependency
	}
	tenantID = strings.TrimSpace(tenantID)
	viewerID = strings.TrimSpace(viewerID)
	ownerID = strings.TrimSpace(ownerID)
	if viewerID == "" || ownerID == "" {
		return ConversationRecord{}, false, ErrInvalidRequest
	}
	records, err := snapshot(ctx, r.router, conversationTable)
	if err != nil {
		return ConversationRecord{}, false, err
	}
	var latest ConversationRecord
	found := false
	identities := map[string]bool{viewerID: true}
	for _, id := range identityIDs {
		if id = strings.TrimSpace(id); id != "" {
			identities[id] = true
		}
	}
	for _, raw := range records {
		var conversation ConversationRecord
		if err := decodePayload(raw, &conversation); err != nil {
			return ConversationRecord{}, false, err
		}
		if !isPersonalAssistantConversationKind(conversation.ConversationKind) || !identities[strings.TrimSpace(conversation.CreatedBy)] {
			continue
		}
		// Alternate identities may select only explicitly owner/tenant-bound chats.
		if conversation.CreatedBy != viewerID && (conversation.TenantID != tenantID || conversation.OwnerID != ownerID) {
			continue
		}
		if recordTenantID := strings.TrimSpace(conversation.TenantID); recordTenantID != "" && recordTenantID != tenantID {
			continue
		}
		if recordOwnerID := strings.TrimSpace(conversation.OwnerID); recordOwnerID != "" && recordOwnerID != ownerID {
			continue
		}
		if !found || conversation.UpdatedAt.After(latest.UpdatedAt) || (conversation.UpdatedAt.Equal(latest.UpdatedAt) && conversation.ConversationID < latest.ConversationID) {
			latest = conversation
			found = true
		}
	}
	return latest, found, nil
}
