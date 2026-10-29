package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
)

const platformRegistryTable = "resource_tool_definitions"

const maxDefinitionLookupBatch = 4096

type PlatformRegistry struct {
	store *db.Store
}

func NewPlatformRegistry(store *db.Store) *PlatformRegistry {
	return &PlatformRegistry{store: store}
}

func (r *PlatformRegistry) Register(ctx context.Context, definition ToolDefinition) error {
	if err := validateDefinition(definition); err != nil {
		return err
	}
	if _, err := r.Get(ctx, definition.ID); err == nil {
		return fmt.Errorf("%w: %s", ErrAlreadyExists, definition.ID)
	} else if !isNotFound(err) {
		return err
	}
	return r.put(ctx, definition)
}

func (r *PlatformRegistry) UpdateDefinition(ctx context.Context, definition ToolDefinition) error {
	if err := validateDefinition(definition); err != nil {
		return err
	}
	if _, err := r.Get(ctx, definition.ID); err != nil {
		return err
	}
	return r.put(ctx, definition)
}

func (r *PlatformRegistry) Disable(ctx context.Context, id agentcore.ToolID) error {
	definition, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	definition.Enabled = false
	return r.put(ctx, definition)
}

func (r *PlatformRegistry) Delete(ctx context.Context, id agentcore.ToolID) error {
	if _, err := r.Get(ctx, id); err != nil {
		return err
	}
	return r.store.PutRecord(ctx, platformRegistryTable, string(id), db.Record{"deleted": true})
}

func (r *PlatformRegistry) IsDeleted(ctx context.Context, id agentcore.ToolID) (bool, error) {
	record, ok, err := r.store.GetRecord(ctx, platformRegistryTable, string(id))
	if err != nil || !ok {
		return false, err
	}
	deleted, _ := record["deleted"].(bool)
	return deleted, nil
}

func (r *PlatformRegistry) Get(ctx context.Context, id agentcore.ToolID) (ToolDefinition, error) {
	record, ok, err := r.store.GetRecord(ctx, platformRegistryTable, string(id))
	if err != nil {
		return ToolDefinition{}, err
	}
	if !ok {
		return ToolDefinition{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if deleted, _ := record["deleted"].(bool); deleted {
		return ToolDefinition{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return decodeDefinition(record)
}

func (r *PlatformRegistry) GetMany(ctx context.Context, ids []agentcore.ToolID) (map[agentcore.ToolID]ToolDefinition, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("%w: platform registry is unavailable", ErrInvalidDefinition)
	}
	if len(ids) > maxDefinitionLookupBatch {
		return nil, fmt.Errorf("%w: tool definition lookup batch exceeds its limit", ErrInvalidDefinition)
	}
	unique := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		key := strings.TrimSpace(string(id))
		if key == "" {
			return nil, fmt.Errorf("%w: tool_id is required", ErrInvalidDefinition)
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, key)
	}
	out := make(map[agentcore.ToolID]ToolDefinition, len(unique))
	for start := 0; start < len(unique); start += db.MaxRecordPageLimit {
		end := min(start+db.MaxRecordPageLimit, len(unique))
		records, err := r.store.GetRecords(ctx, platformRegistryTable, unique[start:end])
		if err != nil {
			return nil, err
		}
		for key, record := range records {
			if deleted, _ := record["deleted"].(bool); deleted {
				continue
			}
			definition, err := decodeDefinition(record)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(string(definition.ID)) != key {
				return nil, fmt.Errorf("tool definition key mismatch")
			}
			out[definition.ID] = definition
		}
	}
	return out, nil
}

func (r *PlatformRegistry) List(ctx context.Context) ([]ToolDefinition, error) {
	definitions, err := r.all(ctx)
	if err != nil {
		return nil, err
	}
	sortDefinitions(definitions)
	return definitions, nil
}

type CatalogPage struct {
	Definitions []ToolDefinition
	NextAfterID string
	Scanned     int
	HasMore     bool
}

type CatalogEntry struct {
	ID        agentcore.ToolID
	Name      string
	UpdatedAt time.Time
}

type CatalogEntryPage struct {
	Entries     []CatalogEntry
	NextAfterID string
	Scanned     int
	HasMore     bool
}

// ListCatalogEntries reads only the identity and display fields required by
// scope discovery. Full tool definitions may include large schemas and MCP
// manifests that do not belong on the ontology catalog hot path.
func (r *PlatformRegistry) ListCatalogEntries(ctx context.Context, afterID string, limit int) (CatalogEntryPage, error) {
	if r == nil || r.store == nil {
		return CatalogEntryPage{}, fmt.Errorf("%w: platform registry is unavailable", ErrInvalidDefinition)
	}
	afterID = strings.TrimSpace(afterID)
	if len(afterID) > 512 || limit <= 0 || limit > db.MaxRecordPageLimit {
		return CatalogEntryPage{}, fmt.Errorf("%w: tool catalog page is invalid", ErrInvalidDefinition)
	}
	query := `
SELECT record_key,
	COALESCE(json_extract(payload_json, '$.deleted'), 0),
	COALESCE(json_extract(json_extract(payload_json, '$.payload'), '$.tool_id'), ''),
	COALESCE(json_extract(json_extract(payload_json, '$.payload'), '$.name'), ''),
	COALESCE(json_extract(json_extract(payload_json, '$.payload'), '$.updated_at'), '')
FROM platform_records
WHERE table_name = ? AND record_key > ?
ORDER BY record_key ASC
LIMIT ?`
	if r.store.Dialect() == db.DialectPostgres {
		query = `
SELECT record_key,
	COALESCE((payload_json::jsonb ->> 'deleted')::boolean, false),
	COALESCE((payload_json::jsonb ->> 'payload')::jsonb ->> 'tool_id', ''),
	COALESCE((payload_json::jsonb ->> 'payload')::jsonb ->> 'name', ''),
	COALESCE((payload_json::jsonb ->> 'payload')::jsonb ->> 'updated_at', '')
FROM platform_records
WHERE table_name = ? AND record_key > ?
ORDER BY record_key ASC
LIMIT ?`
	}
	rows, err := r.store.Query(ctx, query, platformRegistryTable, afterID, limit+1)
	if err != nil {
		return CatalogEntryPage{}, fmt.Errorf("list tool catalog entries: %w", err)
	}
	defer rows.Close()
	type catalogRow struct {
		key       string
		deleted   bool
		id        string
		name      string
		updatedAt string
	}
	page := make([]catalogRow, 0, limit+1)
	for rows.Next() {
		var row catalogRow
		if err := rows.Scan(&row.key, &row.deleted, &row.id, &row.name, &row.updatedAt); err != nil {
			return CatalogEntryPage{}, fmt.Errorf("scan tool catalog entry: %w", err)
		}
		page = append(page, row)
	}
	if err := rows.Err(); err != nil {
		return CatalogEntryPage{}, fmt.Errorf("iterate tool catalog entries: %w", err)
	}
	scanned := len(page)
	hasMore := scanned > limit
	if hasMore {
		page = page[:limit]
	}
	entries := make([]CatalogEntry, 0, len(page))
	for _, row := range page {
		if row.deleted {
			continue
		}
		id := strings.TrimSpace(row.id)
		if id == "" || row.id != id || row.key != id {
			return CatalogEntryPage{}, fmt.Errorf("tool catalog key mismatch")
		}
		var updatedAt time.Time
		if value := strings.TrimSpace(row.updatedAt); value != "" {
			updatedAt, err = time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return CatalogEntryPage{}, fmt.Errorf("tool catalog updated_at is invalid")
			}
		}
		entries = append(entries, CatalogEntry{ID: agentcore.ToolID(id), Name: row.name, UpdatedAt: updatedAt})
	}
	next := afterID
	if len(page) > 0 {
		next = page[len(page)-1].key
	}
	return CatalogEntryPage{Entries: entries, NextAfterID: next, Scanned: scanned, HasMore: hasMore}, nil
}

func (r *PlatformRegistry) ListCatalog(ctx context.Context, afterID string, limit int) (CatalogPage, error) {
	if r == nil || r.store == nil {
		return CatalogPage{}, fmt.Errorf("%w: platform registry is unavailable", ErrInvalidDefinition)
	}
	page, err := r.store.ListRecords(ctx, platformRegistryTable, afterID, limit)
	if err != nil {
		return CatalogPage{}, err
	}
	definitions := make([]ToolDefinition, 0, len(page.Items))
	for _, item := range page.Items {
		if deleted, _ := item.Record["deleted"].(bool); deleted {
			continue
		}
		definition, err := decodeDefinition(item.Record)
		if err != nil {
			return CatalogPage{}, err
		}
		if string(definition.ID) == "" || string(definition.ID) != item.Key {
			return CatalogPage{}, fmt.Errorf("tool catalog key mismatch")
		}
		definitions = append(definitions, definition)
	}
	next := strings.TrimSpace(afterID)
	if len(page.Items) > 0 {
		next = page.Items[len(page.Items)-1].Key
	}
	return CatalogPage{Definitions: definitions, NextAfterID: next, Scanned: len(page.Items), HasMore: page.HasMore}, nil
}

func (r *PlatformRegistry) ListByKind(ctx context.Context, kind Kind) ([]ToolDefinition, error) {
	definitions, err := r.all(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ToolDefinition, 0)
	for _, definition := range definitions {
		if definition.Kind == kind {
			out = append(out, definition.Clone())
		}
	}
	sortDefinitions(out)
	return out, nil
}

func (r *PlatformRegistry) ListByTag(ctx context.Context, tag string) ([]ToolDefinition, error) {
	definitions, err := r.all(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ToolDefinition, 0)
	for _, definition := range definitions {
		if hasString(definition.Tags, tag) {
			out = append(out, definition.Clone())
		}
	}
	sortDefinitions(out)
	return out, nil
}

func (r *PlatformRegistry) CheckPermission(ctx context.Context, id agentcore.ToolID, granted []string) error {
	definition, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	return checkPermissions(definition.Permissions, granted)
}

func (r *PlatformRegistry) put(ctx context.Context, definition ToolDefinition) error {
	payload, err := json.Marshal(definition.Clone())
	if err != nil {
		return fmt.Errorf("encode tool definition: %w", err)
	}
	return r.store.PutRecord(ctx, platformRegistryTable, string(definition.ID), db.Record{
		"payload": string(payload),
	})
}

func (r *PlatformRegistry) all(ctx context.Context) ([]ToolDefinition, error) {
	definitions := make([]ToolDefinition, 0)
	afterID := ""
	for {
		page, err := r.ListCatalog(ctx, afterID, db.MaxRecordPageLimit)
		if err != nil {
			return nil, err
		}
		definitions = append(definitions, page.Definitions...)
		if !page.HasMore {
			return definitions, nil
		}
		afterID = page.NextAfterID
	}
}

func decodeDefinition(record db.Record) (ToolDefinition, error) {
	raw, ok := record["payload"].(string)
	if !ok || raw == "" {
		return ToolDefinition{}, fmt.Errorf("tool definition payload missing")
	}
	var definition ToolDefinition
	if err := json.Unmarshal([]byte(raw), &definition); err != nil {
		return ToolDefinition{}, fmt.Errorf("decode tool definition: %w", err)
	}
	return definition.Clone(), nil
}

func isNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}
