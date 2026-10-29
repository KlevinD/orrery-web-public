package missioncontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/mission/projection"
	"orrery_v2/internal/platform/db"
)

const projectionTable = "core.mission_control_projection"

type DBProjectionRepository struct {
	router *db.Router
}

func NewDBProjectionRepository(router *db.Router) *DBProjectionRepository {
	return &DBProjectionRepository{router: router}
}

func (r *DBProjectionRepository) Save(ctx context.Context, model projection.ReadModel) error {
	if missionModelTenantID(model) != "global" {
		return ErrServiceDependency
	}
	return r.SaveForTenant(ctx, "global", model)
}

func (r *DBProjectionRepository) SaveForTenant(ctx context.Context, tenantID string, model projection.ReadModel) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	tenantID = missionTenantID(tenantID)
	if !validMissionProjectionIdentity(tenantID, model.Mission.MissionID.String()) {
		return ErrInvalidRequest
	}
	model.TenantID = tenantID
	model.Mission.TenantID = tenantID
	payload, err := json.Marshal(model)
	if err != nil {
		return fmt.Errorf("encode mission control projection: %w", err)
	}
	store, table, err := r.router.StoreForTable(projectionTable)
	if err != nil {
		return err
	}
	key := missionProjectionKey(tenantID, model.Mission.MissionID)
	record := db.Record{
		"tenant_id":  tenantID,
		"mission_id": model.Mission.MissionID.String(),
		"version":    model.Checkpoint.Version,
		"payload":    string(payload),
	}
	return store.WithTx(ctx, func(tx db.Tx) error {
		if err := db.LockRecordKeyTx(ctx, tx, table, key); err != nil {
			return err
		}
		// Mission IDs are not reused. A worker that read the projection before
		// deletion must not recreate it, even after another instance restarts.
		if _, deleted, err := db.GetRecordTx(ctx, tx, table+"_deletions", key); err != nil {
			return err
		} else if deleted {
			return ErrProjectionNotFound
		}
		current, found, err := db.GetRecordTx(ctx, tx, table, key)
		if err != nil {
			return err
		}
		if found {
			currentVersion := recordInt(current["version"])
			if currentVersion > model.Checkpoint.Version {
				return ErrConflict
			}
			if currentVersion == model.Checkpoint.Version {
				if currentPayload, _ := current["payload"].(string); currentPayload == string(payload) {
					return nil
				}
				return ErrConflict
			}
		}
		return db.PutRecordTx(ctx, tx, table, key, record)
	})
}

func (r *DBProjectionRepository) repairRuntimeProjection(ctx context.Context, tenantID string, expected, replacement projection.ReadModel) (bool, error) {
	if r == nil || r.router == nil {
		return false, ErrServiceDependency
	}
	tenantID = missionTenantID(tenantID)
	missionID := expected.Mission.MissionID
	if !validMissionProjectionIdentity(tenantID, missionID.String()) || replacement.Mission.MissionID != missionID || replacement.Checkpoint.Version != expected.Checkpoint.Version {
		return false, ErrInvalidRequest
	}
	expected.TenantID = tenantID
	expected.Mission.TenantID = tenantID
	replacement.TenantID = tenantID
	replacement.Mission.TenantID = tenantID
	expectedPayload, err := json.Marshal(expected)
	if err != nil {
		return false, fmt.Errorf("encode expected mission control projection: %w", err)
	}
	replacementPayload, err := json.Marshal(replacement)
	if err != nil {
		return false, fmt.Errorf("encode replacement mission control projection: %w", err)
	}
	expectedRecord := db.Record{
		"tenant_id":  tenantID,
		"mission_id": missionID.String(),
		"version":    expected.Checkpoint.Version,
		"payload":    string(expectedPayload),
	}
	replacementRecord := db.Record{
		"tenant_id":  tenantID,
		"mission_id": missionID.String(),
		"version":    replacement.Checkpoint.Version,
		"payload":    string(replacementPayload),
	}
	return r.router.CompareAndSwapRecord(ctx, projectionTable, missionProjectionKey(tenantID, missionID), expectedRecord, replacementRecord)
}

func (r *DBProjectionRepository) GetMissionControl(ctx context.Context, missionID missioncore.MissionID) (projection.ReadModel, error) {
	return r.GetMissionControlForTenant(ctx, "global", missionID)
}

func (r *DBProjectionRepository) GetMissionControlForTenant(ctx context.Context, tenantID string, missionID missioncore.MissionID) (projection.ReadModel, error) {
	if r == nil || r.router == nil {
		return projection.ReadModel{}, ErrServiceDependency
	}
	tenantID = missionTenantID(tenantID)
	if !validMissionProjectionIdentity(tenantID, missionID.String()) {
		return projection.ReadModel{}, ErrInvalidRequest
	}
	key := missionProjectionKey(tenantID, missionID)
	record, ok, err := r.router.GetRecord(ctx, projectionTable, key)
	if err != nil {
		return projection.ReadModel{}, err
	}
	if !ok && tenantID == "global" {
		key = missionID.String()
		record, ok, err = r.router.GetRecord(ctx, projectionTable, key)
		if err != nil {
			return projection.ReadModel{}, err
		}
	}
	if !ok {
		return projection.ReadModel{}, ErrProjectionNotFound
	}
	model, err := missionProjectionFromRecord(record, tenantID, missionID.String())
	if err != nil {
		return projection.ReadModel{}, err
	}
	if key != missionProjectionKey(tenantID, missionID) && (tenantID != "global" || key != missionID.String()) {
		return projection.ReadModel{}, ErrServiceDependency
	}
	return model, nil
}

func (r *DBProjectionRepository) ListMissionControls(ctx context.Context) ([]projection.ReadModel, error) {
	return r.ListMissionControlsForTenant(ctx, "global")
}

func (r *DBProjectionRepository) ListMissionControlsForTenant(ctx context.Context, tenantID string) ([]projection.ReadModel, error) {
	if r == nil || r.router == nil {
		return nil, ErrServiceDependency
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store, table, err := r.router.StoreForTable(projectionTable)
	if err != nil {
		return nil, err
	}
	records, err := store.ListRecordTable(ctx, table)
	if err != nil {
		return nil, err
	}
	models := make([]projection.ReadModel, 0, len(records))
	for key, record := range records {
		model, err := missionProjectionFromRecord(record, "", "")
		if err != nil {
			return nil, err
		}
		if key != missionProjectionKey(missionModelTenantID(model), model.Mission.MissionID) && (missionModelTenantID(model) != "global" || key != model.Mission.MissionID.String()) {
			return nil, ErrServiceDependency
		}
		if missionModelTenantID(model) != missionTenantID(tenantID) {
			continue
		}
		models = append(models, model)
	}
	return models, nil
}

func (r *DBProjectionRepository) ListMissionCatalog(ctx context.Context, afterID string, limit int) ([]projection.ReadModel, bool, error) {
	if r == nil || r.router == nil {
		return nil, false, ErrServiceDependency
	}
	page, err := r.router.ListRecords(ctx, projectionTable, afterID, limit)
	if err != nil {
		return nil, false, err
	}
	models := make([]projection.ReadModel, 0, len(page.Items))
	for _, item := range page.Items {
		model, decodeErr := missionProjectionFromRecord(item.Record, "", "")
		if decodeErr != nil {
			return nil, false, decodeErr
		}
		if item.Key != missionProjectionKey(missionModelTenantID(model), model.Mission.MissionID) && (missionModelTenantID(model) != "global" || item.Key != model.Mission.MissionID.String()) {
			return nil, false, ErrServiceDependency
		}
		models = append(models, model)
	}
	return models, page.HasMore, nil
}

type MissionCatalogEntry struct {
	Seek      string
	TenantID  string
	MissionID missioncore.MissionID
	Title     string
	UpdatedAt time.Time
}

// ListMissionCatalogEntries reads only the fields needed by scope discovery.
// Mission projections can contain large task, evidence, and runtime payloads;
// transferring those payloads just to build a label makes ontology discovery
// proportional to unrelated mission execution history.
func (r *DBProjectionRepository) ListMissionCatalogEntries(ctx context.Context, afterID string, limit int) ([]MissionCatalogEntry, bool, error) {
	if r == nil || r.router == nil {
		return nil, false, ErrServiceDependency
	}
	afterID = strings.TrimSpace(afterID)
	if len(afterID) > 512 || limit <= 0 || limit > db.MaxRecordPageLimit {
		return nil, false, ErrInvalidRequest
	}
	store, table, err := r.router.StoreForTable(projectionTable)
	if err != nil {
		return nil, false, err
	}
	query := `
SELECT record_key,
	COALESCE(json_extract(payload_json, '$.tenant_id'), ''),
	COALESCE(json_extract(payload_json, '$.mission_id'), ''),
	COALESCE(json_extract(json_extract(payload_json, '$.payload'), '$.Mission.Title'), ''),
	COALESCE(json_extract(json_extract(payload_json, '$.payload'), '$.UpdatedAt'), '')
FROM platform_records
WHERE table_name = ? AND record_key > ?
ORDER BY record_key ASC
LIMIT ?`
	if store.Dialect() == db.DialectPostgres {
		query = `
SELECT record_key,
	COALESCE(payload_json::jsonb ->> 'tenant_id', ''),
	COALESCE(payload_json::jsonb ->> 'mission_id', ''),
	COALESCE((payload_json::jsonb ->> 'payload')::jsonb #>> '{Mission,Title}', ''),
	COALESCE((payload_json::jsonb ->> 'payload')::jsonb #>> '{UpdatedAt}', '')
FROM platform_records
WHERE table_name = ? AND record_key > ?
ORDER BY record_key ASC
LIMIT ?`
	}
	rows, err := store.Query(ctx, query, table, afterID, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list mission catalog entries: %w", err)
	}
	defer rows.Close()
	type catalogRow struct {
		key       string
		tenantID  string
		missionID string
		title     string
		updatedAt string
	}
	page := make([]catalogRow, 0, limit+1)
	for rows.Next() {
		var row catalogRow
		if err := rows.Scan(&row.key, &row.tenantID, &row.missionID, &row.title, &row.updatedAt); err != nil {
			return nil, false, fmt.Errorf("scan mission catalog entry: %w", err)
		}
		page = append(page, row)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate mission catalog entries: %w", err)
	}
	hasMore := len(page) > limit
	if hasMore {
		page = page[:limit]
	}
	entries := make([]MissionCatalogEntry, 0, len(page))
	for _, row := range page {
		tenantID := strings.TrimSpace(row.tenantID)
		missionID := strings.TrimSpace(row.missionID)
		if row.tenantID != tenantID || row.missionID != missionID || !validMissionProjectionIdentity(tenantID, missionID) ||
			(row.key != missionProjectionKey(tenantID, missioncore.MissionID(missionID)) && (tenantID != "global" || row.key != missionID)) {
			return nil, false, ErrServiceDependency
		}
		var updatedAt time.Time
		if value := strings.TrimSpace(row.updatedAt); value != "" {
			updatedAt, err = time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return nil, false, ErrServiceDependency
			}
		}
		entries = append(entries, MissionCatalogEntry{
			Seek:      row.key,
			TenantID:  tenantID,
			MissionID: missioncore.MissionID(missionID),
			Title:     row.title,
			UpdatedAt: updatedAt,
		})
	}
	return entries, hasMore, nil
}

func (r *DBProjectionRepository) Delete(ctx context.Context, missionID missioncore.MissionID) error {
	return r.DeleteForTenant(ctx, "global", missionID)
}

func (r *DBProjectionRepository) DeleteForTenant(ctx context.Context, tenantID string, missionID missioncore.MissionID) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	if missionID.IsZero() {
		return ErrInvalidRequest
	}
	tenantID = missionTenantID(tenantID)
	if !validMissionProjectionIdentity(tenantID, missionID.String()) {
		return ErrInvalidRequest
	}
	store, table, err := r.router.StoreForTable(projectionTable)
	if err != nil {
		return err
	}
	key := missionProjectionKey(tenantID, missionID)
	return store.WithTx(ctx, func(tx db.Tx) error {
		// Use the same canonical key lock as SaveForTenant, including when
		// deleting a legacy global record stored under an unqualified key.
		if err := db.LockRecordKeyTx(ctx, tx, table, key); err != nil {
			return err
		}
		keys := []string{key}
		if tenantID == "global" {
			keys = append(keys, missionID.String())
		}
		found := false
		for _, candidate := range keys {
			record, ok, err := db.GetRecordTx(ctx, tx, table, candidate)
			if err != nil {
				return err
			}
			if ok {
				if _, err := missionProjectionFromRecord(record, tenantID, missionID.String()); err != nil {
					return err
				}
				found = true
			}
		}
		if !found {
			return ErrProjectionNotFound
		}
		// The marker shares the projection's store and transaction. Keep it
		// outside the projection table so catalog and control reads stay absent.
		if err := db.PutRecordTx(ctx, tx, table+"_deletions", key, db.Record{
			"tenant_id": tenantID, "mission_id": missionID.String(),
		}); err != nil {
			return err
		}
		for _, candidate := range keys {
			if err := db.DeleteRecordTx(ctx, tx, table, candidate); err != nil {
				return err
			}
		}
		return nil
	})
}

func recordInt(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	default:
		return 0
	}
}

func missionProjectionFromRecord(record db.Record, expectedTenantID, expectedMissionID string) (projection.ReadModel, error) {
	payload, ok := record["payload"].(string)
	if !ok || payload == "" {
		return projection.ReadModel{}, fmt.Errorf("mission control projection payload is missing")
	}
	var model projection.ReadModel
	if err := json.Unmarshal([]byte(payload), &model); err != nil {
		return projection.ReadModel{}, fmt.Errorf("decode mission control projection: %w", err)
	}
	modelTenantID := missionModelTenantID(model)
	missionTenant := missionTenantID(model.Mission.TenantID)
	modelTopTenant := missionTenantID(model.TenantID)
	missionID := model.Mission.MissionID.String()
	recordTenantID := missionTenantID(recordString(record["tenant_id"]))
	recordMissionID := strings.TrimSpace(recordString(record["mission_id"]))
	if !validMissionProjectionIdentity(modelTenantID, missionID) || modelTopTenant != modelTenantID || missionTenant != modelTenantID || recordTenantID != modelTenantID || recordMissionID != missionID ||
		(expectedTenantID != "" && modelTenantID != missionTenantID(expectedTenantID)) ||
		(expectedMissionID != "" && missionID != strings.TrimSpace(expectedMissionID)) ||
		(!model.Checkpoint.MissionID.IsZero() && model.Checkpoint.MissionID.String() != missionID) {
		return projection.ReadModel{}, ErrServiceDependency
	}
	model.TenantID = modelTenantID
	model.Mission.TenantID = modelTenantID
	return model, nil
}

func validMissionProjectionIdentity(tenantID, missionID string) bool {
	tenantID = strings.TrimSpace(tenantID)
	missionID = strings.TrimSpace(missionID)
	return tenantID != "" && len(tenantID) <= 256 && !strings.ContainsAny(tenantID, "/\x00\r\n") && missionID != "" && len(missionID) <= 256 && !strings.ContainsAny(missionID, "\x00\r\n")
}

func recordString(value any) string {
	text, _ := value.(string)
	return text
}
