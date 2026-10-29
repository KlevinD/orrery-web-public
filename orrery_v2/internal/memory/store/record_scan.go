package store

import (
	"context"
	"encoding/json"
	"fmt"

	memcore "orrery_v2/internal/memory/core"
)

func (s *PlatformStore) ScanRecords(ctx context.Context, afterKey string, limit int) (RecordScanPage, error) {
	page, err := s.store.ListRecords(ctx, recordTable, afterKey, limit)
	if err != nil {
		return RecordScanPage{}, err
	}
	result := RecordScanPage{HasMore: page.HasMore, Scanned: len(page.Items)}
	for _, item := range page.Items {
		raw, ok := item.Record["payload"].(string)
		if !ok || raw == "" {
			return RecordScanPage{}, fmt.Errorf("%w: record payload missing", memcore.ErrInvalid)
		}
		var record memcore.MemoryRecord
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return RecordScanPage{}, fmt.Errorf("decode memory record: %w", err)
		}
		result.Items = append(result.Items, RecordScanItem{Key: item.Key, Record: record.Normalized()})
		result.NextKey = item.Key
	}
	return result, nil
}
