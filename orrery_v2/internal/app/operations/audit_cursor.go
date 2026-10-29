package operations

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

const maxAuditCursorLimit = 100
const maxAuditMetadataFilters = 8

type AuditEventSeek struct {
	OccurredAt time.Time
	AuditID    string
}

type AuditEventCursorQuery struct {
	ActorID        string
	Action         string
	Result         string
	EntityType     string
	EntityID       string
	MissionID      string
	Severity       string
	MetadataEquals map[string]string
	Limit          int
	After          AuditEventSeek
}

type AuditEventCursorPage struct {
	Events  []AuditEventDTO
	HasMore bool
}

func (s *AuditStore) ListAuditEventCursorPage(ctx context.Context, query AuditEventCursorQuery) (AuditEventCursorPage, error) {
	if s == nil || s.router == nil {
		return AuditEventCursorPage{}, ErrServiceDependency
	}
	query, err := normalizeAuditEventCursorQuery(query)
	if err != nil {
		return AuditEventCursorPage{}, err
	}
	if err := ctx.Err(); err != nil {
		return AuditEventCursorPage{}, err
	}
	store, table, err := s.router.StoreForTable(auditLogTable)
	if err != nil {
		return AuditEventCursorPage{}, err
	}
	records, err := store.ListRecordTable(ctx, table)
	if err != nil {
		return AuditEventCursorPage{}, err
	}
	items := make([]AuditEventDTO, 0)
	filter := AuditLogQuery{
		ActorID: query.ActorID, Action: query.Action, Result: query.Result, EntityType: query.EntityType,
		EntityID: query.EntityID, MissionID: query.MissionID, Severity: query.Severity,
	}
	for _, record := range records {
		event := auditEventFromRecord(record)
		if strings.TrimSpace(event.AuditID) == "" || event.OccurredAt.IsZero() {
			return AuditEventCursorPage{}, fmt.Errorf("%w: persisted audit event is missing its id or timestamp", ErrServiceDependency)
		}
		if !matchesAuditQuery(event, filter) || !matchesAuditMetadata(event.Metadata, query.MetadataEquals) || !auditEventAfterSeek(event, query.After) {
			continue
		}
		items = append(items, event)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].OccurredAt.Equal(items[j].OccurredAt) {
			return items[i].AuditID < items[j].AuditID
		}
		return items[i].OccurredAt.After(items[j].OccurredAt)
	})
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	return AuditEventCursorPage{Events: items, HasMore: hasMore}, nil
}

func normalizeAuditEventCursorQuery(query AuditEventCursorQuery) (AuditEventCursorQuery, error) {
	query.ActorID = strings.TrimSpace(query.ActorID)
	query.Action = strings.TrimSpace(query.Action)
	query.Result = strings.TrimSpace(query.Result)
	query.EntityType = strings.TrimSpace(query.EntityType)
	query.EntityID = strings.TrimSpace(query.EntityID)
	query.MissionID = strings.TrimSpace(query.MissionID)
	query.Severity = strings.TrimSpace(query.Severity)
	query.After.AuditID = strings.TrimSpace(query.After.AuditID)
	if query.Limit <= 0 || query.Limit > maxAuditCursorLimit ||
		len(query.ActorID) > 512 || len(query.Action) > 256 || len(query.Result) > 128 ||
		len(query.EntityType) > 256 || len(query.EntityID) > 512 || len(query.MissionID) > 512 || len(query.Severity) > 128 ||
		len(query.After.AuditID) > 256 || query.After.OccurredAt.IsZero() != (query.After.AuditID == "") {
		return AuditEventCursorQuery{}, fmt.Errorf("%w: audit cursor query requires a valid limit and seek", ErrInvalidRequest)
	}
	query.After.OccurredAt = query.After.OccurredAt.UTC()
	if len(query.MetadataEquals) > maxAuditMetadataFilters {
		return AuditEventCursorQuery{}, fmt.Errorf("%w: audit metadata filter exceeds its bound", ErrInvalidRequest)
	}
	metadata := make(map[string]string, len(query.MetadataEquals))
	for key, value := range query.MetadataEquals {
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if key == "" || value == "" || len(key) > 128 || len(value) > 512 {
			return AuditEventCursorQuery{}, fmt.Errorf("%w: audit metadata filter is invalid", ErrInvalidRequest)
		}
		if _, duplicate := metadata[key]; duplicate {
			return AuditEventCursorQuery{}, fmt.Errorf("%w: duplicate normalized audit metadata filter", ErrInvalidRequest)
		}
		metadata[key] = value
	}
	query.MetadataEquals = metadata
	return query, nil
}

func matchesAuditMetadata(metadata, expected map[string]string) bool {
	for key, value := range expected {
		if strings.TrimSpace(metadata[key]) != value {
			return false
		}
	}
	return true
}

func auditEventAfterSeek(event AuditEventDTO, after AuditEventSeek) bool {
	if after.AuditID == "" {
		return true
	}
	return event.OccurredAt.Before(after.OccurredAt) || event.OccurredAt.Equal(after.OccurredAt) && event.AuditID > after.AuditID
}
