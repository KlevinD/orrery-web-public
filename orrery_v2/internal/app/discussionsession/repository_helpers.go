package discussionsession

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

func messageRecordLess(left, right db.Record) bool {
	leftCreatedAt, leftOK := recordTime(left, "created_at")
	rightCreatedAt, rightOK := recordTime(right, "created_at")
	if leftOK && rightOK {
		return leftCreatedAt.Before(rightCreatedAt)
	}
	return false
}

func recordTime(record db.Record, key string) (time.Time, bool) {
	value, _ := record[key].(string)
	if strings.TrimSpace(value) == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

func snapshot(ctx context.Context, router *db.Router, logicalTable string) ([]db.Record, error) {
	store, table, err := router.StoreForTable(logicalTable)
	if err != nil {
		return nil, err
	}
	out := make([]db.Record, 0)
	afterKey := ""
	for {
		page, err := store.ListRecords(ctx, table, afterKey, db.MaxRecordPageLimit)
		if err != nil {
			return nil, err
		}
		for _, item := range page.Items {
			out = append(out, item.Record)
		}
		if !page.HasMore {
			return out, nil
		}
		if len(page.Items) == 0 {
			return nil, fmt.Errorf("discussion repository record page made no progress")
		}
		afterKey = page.Items[len(page.Items)-1].Key
	}
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(timeFormatRFC3339Nano)
}
