package discussionsession

import (
	"context"

	"orrery_v2/internal/platform/db"
)

// Keep single-conversation reads independent of every other conversation's
// transcripts and runtime traces. Use the same conversation metadata that the
// writers persist, with bound parameters for the conversation identifier.
// Record-key order preserves the previous keyset snapshot's tie ordering;
// callers retain their existing chronological sort and history semantics.
func (r *Repository) conversationRecords(ctx context.Context, logicalTable, conversationID string) ([]db.Record, error) {
	store, _, err := r.router.StoreForTable(logicalTable)
	if err != nil {
		return nil, err
	}
	predicate := historyJSONField(store.Dialect(), "conversation_id", false) + " = ?"
	return r.historyRecords(ctx, logicalTable, predicate, "record_key ASC", []any{conversationID}, 0, 0)
}
