package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"orrery_v2/internal/platform/db"
)

// RecordAuditEventIdempotent preserves the event identity and timestamp from
// a durable publication journal. A delivery retry cannot overwrite evidence.
func (s *AuditStore) RecordAuditEventIdempotent(ctx context.Context, event AuditEventDTO) error {
	if s == nil || s.router == nil {
		return ErrServiceDependency
	}
	if strings.TrimSpace(event.AuditID) == "" || strings.TrimSpace(event.ActorID) == "" || strings.TrimSpace(event.EntityType) == "" || strings.TrimSpace(event.EntityID) == "" || strings.TrimSpace(event.Action) == "" || event.OccurredAt.IsZero() {
		return fmt.Errorf("%w: stable audit id, actor, entity, action, and timestamp are required", ErrInvalidRequest)
	}
	store, table, err := s.router.StoreForTable(auditLogTable)
	if err != nil {
		return err
	}
	record := db.SanitizeRecordText(auditRecord(event))
	created, err := store.CreateRecord(ctx, table, event.AuditID, record)
	if err != nil || created {
		return err
	}
	existing, exists, err := store.GetRecord(ctx, table, event.AuditID)
	if err != nil {
		return err
	}
	expectedJSON, err := json.Marshal(record)
	if err != nil {
		return err
	}
	existingJSON, err := json.Marshal(db.SanitizeRecordText(existing))
	if err != nil {
		return err
	}
	if !exists || !bytes.Equal(expectedJSON, existingJSON) {
		return fmt.Errorf("%w: audit id already records another event", ErrVersionConflict)
	}
	return nil
}
