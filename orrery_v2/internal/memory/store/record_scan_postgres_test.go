package store

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	memcore "orrery_v2/internal/memory/core"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testpg"
)

func TestPostgresStallRecordScannerBoundsPhysicalReadsAndPreservesCursor(t *testing.T) {
	database := testpg.Open(t)
	ctx := context.Background()
	for i := 0; i < 25; i++ {
		id := fmt.Sprintf("stall-memory-%03d", i)
		payload, err := json.Marshal(memcore.MemoryRecord{ID: id, Summary: "test"})
		if err != nil {
			t.Fatal(err)
		}
		if err := database.PutRecord(ctx, recordTable, id, db.Record{"payload": string(payload)}); err != nil {
			t.Fatal(err)
		}
	}
	store := NewPlatformStore(database)
	page, err := store.ScanRecords(ctx, "", 10)
	if err != nil || page.Scanned != 10 || len(page.Items) != 10 || !page.HasMore {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	second, err := store.ScanRecords(ctx, page.NextKey, 10)
	if err != nil || second.Items[0].Key != "stall-memory-010" || second.NextKey != "stall-memory-019" {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	last, err := store.ScanRecords(ctx, second.NextKey, 10)
	if err != nil || last.Scanned != 5 || last.HasMore {
		t.Fatalf("last=%+v err=%v", last, err)
	}
}
