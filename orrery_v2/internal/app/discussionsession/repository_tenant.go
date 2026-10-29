package discussionsession

import (
	"context"
	"fmt"
	"strings"

	"orrery_v2/internal/platform/db"
)

// ReassignTenant moves conversation ownership while retaining conversation IDs
// and all child-record references.
func (r *Repository) ReassignTenant(ctx context.Context, fromID, toID string) (int, error) {
	if r == nil || r.router == nil {
		return 0, ErrServiceDependency
	}
	fromID, toID = strings.TrimSpace(fromID), strings.TrimSpace(toID)
	if fromID == "" || toID == "" || fromID == toID {
		return 0, nil
	}
	store, table, err := r.router.StoreForTable(conversationTable)
	if err != nil {
		return 0, err
	}
	migrated := 0
	afterKey := ""
	for {
		page, err := store.ListRecords(ctx, table, afterKey, db.MaxRecordPageLimit)
		if err != nil {
			return migrated, err
		}
		for _, item := range page.Items {
			afterKey = item.Key
			var indexed ConversationRecord
			if err := decodePayload(item.Record, &indexed); err != nil {
				return migrated, err
			}
			if strings.TrimSpace(indexed.TenantID) != fromID {
				continue
			}
			changed := false
			if err := store.WithTx(ctx, func(tx db.Tx) error {
				if err := db.LockRecordKeyTx(ctx, tx, table, item.Key); err != nil {
					return err
				}
				currentRecord, ok, err := db.GetRecordTx(ctx, tx, table, item.Key)
				if err != nil || !ok {
					return err
				}
				var conversation ConversationRecord
				if err := decodePayload(currentRecord, &conversation); err != nil {
					return err
				}
				if strings.TrimSpace(conversation.TenantID) != fromID {
					return nil
				}
				conversation.TenantID = toID
				conversation.Revision++
				if conversation.Revision <= 0 {
					conversation.Revision = 1
				}
				payload, err := encodePayload(conversation)
				if err != nil {
					return err
				}
				if err := db.PutRecordTx(ctx, tx, table, item.Key, db.Record{
					"conversation_id":   conversation.ConversationID,
					"tenant_id":         conversation.TenantID,
					"purpose_kind":      conversation.PurposeKind,
					"purpose_id":        conversation.PurposeID,
					"status":            conversation.Status,
					"created_by":        conversation.CreatedBy,
					"owner_id":          conversation.OwnerID,
					"updated_at":        formatTime(conversation.UpdatedAt),
					"revision":          conversation.Revision,
					conversationPayload: payload,
				}); err != nil {
					return err
				}
				changed = true
				return nil
			}); err != nil {
				return migrated, fmt.Errorf("reassign conversation %s tenant: %w", item.Key, err)
			}
			if changed {
				migrated++
			}
		}
		if !page.HasMore {
			return migrated, nil
		}
		if len(page.Items) == 0 {
			return migrated, fmt.Errorf("discussion tenant reassignment made no progress")
		}
	}
}
