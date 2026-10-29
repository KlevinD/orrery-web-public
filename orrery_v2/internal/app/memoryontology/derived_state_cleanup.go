package memoryontology

import (
	"context"
	"fmt"
	"strings"

	"orrery_v2/internal/platform/db"
)

// DeleteDerivedStateForMemories removes only bridge-owned extraction state.
// Durable memory records and Context Graph resources are not modified.
func (s *PlatformStore) DeleteDerivedStateForMemories(ctx context.Context, memoryIDs []string) (int, error) {
	if s == nil || s.store == nil {
		return 0, fmt.Errorf("memory ontology platform store is required")
	}
	unique := make([]string, 0, len(memoryIDs))
	seen := map[string]struct{}{}
	for _, memoryID := range memoryIDs {
		memoryID = strings.TrimSpace(memoryID)
		if memoryID == "" {
			continue
		}
		if _, exists := seen[memoryID]; exists {
			continue
		}
		seen[memoryID] = struct{}{}
		unique = append(unique, memoryID)
	}
	deleted := 0
	err := s.store.WithTx(ctx, func(tx db.Tx) error {
		for _, memoryID := range unique {
			for _, table := range []string{"memory_ontology_mentions", "memory_ontology_runs"} {
				result, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE memory_id = ?", memoryID)
				if err != nil {
					return fmt.Errorf("delete %s for memory %s: %w", table, memoryID, err)
				}
				affected, err := result.RowsAffected()
				if err != nil {
					return fmt.Errorf("count deleted %s for memory %s: %w", table, memoryID, err)
				}
				deleted += int(affected)
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("delete memory ontology derived state: %w", err)
	}
	return deleted, nil
}
