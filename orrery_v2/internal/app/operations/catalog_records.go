package operations

import (
	"context"

	"orrery_v2/internal/platform/db"
)

func listOperationRecords(ctx context.Context, router *db.Router, logicalTable string) ([]db.Record, error) {
	records := make([]db.Record, 0)
	afterKey := ""
	for {
		page, err := router.ListRecords(ctx, logicalTable, afterKey, db.MaxRecordPageLimit)
		if err != nil {
			return nil, err
		}
		for _, item := range page.Items {
			records = append(records, item.Record)
			afterKey = item.Key
		}
		if !page.HasMore {
			return records, nil
		}
	}
}
