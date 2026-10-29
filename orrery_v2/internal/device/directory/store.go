package directory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"orrery_v2/internal/platform/db"
)

type SQLStore struct {
	store *db.Store
}

func NewSQLStore(ctx context.Context, store *db.Store) (*SQLStore, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: core database is required", ErrDependency)
	}
	for _, statement := range schemaStatements(store.Dialect()) {
		if _, err := store.Exec(ctx, statement); err != nil {
			return nil, fmt.Errorf("initialize owned agent directory: %w", err)
		}
	}
	for _, statement := range personalAssistantNotificationSchemaStatements(store.Dialect()) {
		if _, err := store.Exec(ctx, statement); err != nil {
			return nil, fmt.Errorf("initialize personal assistant notifications: %w", err)
		}
	}
	if err := ensurePersonalAssistantAssignedAgentColumn(ctx, store); err != nil {
		return nil, fmt.Errorf("initialize personal assistant assigned agent: %w", err)
	}
	return &SQLStore{store: store}, nil
}

func ensurePersonalAssistantAssignedAgentColumn(ctx context.Context, store *db.Store) error {
	var rows *sql.Rows
	var err error
	if store.Dialect() == db.DialectPostgres {
		rows, err = store.Query(ctx, `SELECT column_name FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'personal_assistant_hosting'`)
	} else {
		rows, err = store.Query(ctx, `PRAGMA table_info(personal_assistant_hosting)`)
	}
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if store.Dialect() == db.DialectPostgres {
			var column string
			if err := rows.Scan(&column); err != nil {
				return err
			}
			if column == "assigned_agent_id" {
				return nil
			}
			continue
		}
		var ordinal int
		var column, columnType string
		var notNull, primaryKey int
		var defaultValue sql.NullString
		if err := rows.Scan(&ordinal, &column, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		if column == "assigned_agent_id" {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = store.Exec(ctx, `ALTER TABLE personal_assistant_hosting ADD COLUMN assigned_agent_id TEXT NOT NULL DEFAULT ''`)
	return err
}

func (s *SQLStore) ReplaceOwnedAgents(ctx context.Context, scope OwnerScope, deviceID, catalogDigest string, observedAt time.Time, profiles []OwnedAgent, now time.Time) error {
	return s.store.WithTx(ctx, func(tx db.Tx) error {
		var deviceExists int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM devices WHERE device_id = ? AND tenant_id = ? AND owner_id = ? AND revoked_at IS NULL`, deviceID, scope.TenantID, scope.OwnerID).Scan(&deviceExists); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return fmt.Errorf("verify owned agent device: %w", err)
		}
		var latestObserved sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT MAX(observed_at) FROM owned_agent_profiles WHERE tenant_id = ? AND owner_id = ? AND device_id = ?`, scope.TenantID, scope.OwnerID, deviceID).Scan(&latestObserved); err != nil {
			return fmt.Errorf("load owned agent catalog revision: %w", err)
		}
		if latestObserved.Valid && latestObserved.Int64 > observedAt.UnixNano() {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE owned_agent_profiles SET available = 0, catalog_digest = ?, observed_at = ?, updated_at = ? WHERE tenant_id = ? AND owner_id = ? AND device_id = ?`, catalogDigest, observedAt.UnixNano(), now.UnixNano(), scope.TenantID, scope.OwnerID, deviceID); err != nil {
			return fmt.Errorf("mark previous owned agent catalog unavailable: %w", err)
		}
		for _, profile := range profiles {
			capabilities, err := json.Marshal(profile.Capabilities)
			if err != nil {
				return fmt.Errorf("encode owned agent capabilities: %w", err)
			}
			workspaceGrants, err := json.Marshal(profile.WorkspaceGrants)
			if err != nil {
				return fmt.Errorf("encode owned agent workspace grants: %w", err)
			}
			available := 0
			if profile.Available {
				available = 1
			}
			_, err = tx.ExecContext(ctx, `
INSERT INTO owned_agent_profiles (
 tenant_id, owner_id, owned_agent_id, device_id, local_profile_id, display_name, runtime_kind,
 capabilities_json, workspace_grants_json, available, catalog_digest, observed_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, owner_id, owned_agent_id) DO UPDATE SET
 device_id = excluded.device_id, local_profile_id = excluded.local_profile_id, display_name = excluded.display_name,
 runtime_kind = excluded.runtime_kind, capabilities_json = excluded.capabilities_json,
 workspace_grants_json = excluded.workspace_grants_json, available = excluded.available,
 catalog_digest = excluded.catalog_digest, observed_at = excluded.observed_at, updated_at = excluded.updated_at`,
				profile.TenantID, profile.OwnerID, profile.OwnedAgentID, profile.DeviceID, profile.LocalProfileID,
				profile.DisplayName, profile.RuntimeKind, string(capabilities), string(workspaceGrants), available, catalogDigest,
				observedAt.UnixNano(), now.UnixNano())
			if err != nil {
				return fmt.Errorf("upsert owned agent profile: %w", err)
			}
		}
		return nil
	})
}

func (s *SQLStore) ListOwnedAgents(ctx context.Context, scope OwnerScope) ([]OwnedAgent, error) {
	rows, err := s.store.Query(ctx, `
SELECT p.tenant_id, p.owner_id, p.owned_agent_id, p.device_id, d.device_name, p.local_profile_id,
 p.display_name, p.runtime_kind, p.capabilities_json, p.workspace_grants_json, p.available, p.catalog_digest,
 p.observed_at, p.updated_at, d.revoked_at
FROM owned_agent_profiles p
JOIN devices d ON d.device_id = p.device_id
WHERE p.tenant_id = ? AND p.owner_id = ?
ORDER BY lower(p.display_name), p.owned_agent_id`, scope.TenantID, scope.OwnerID)
	if err != nil {
		return nil, fmt.Errorf("list owned agents: %w", err)
	}
	defer rows.Close()
	out := []OwnedAgent{}
	for rows.Next() {
		var agent OwnedAgent
		var capabilities, workspaceGrants string
		var available int
		var observedAt, updatedAt int64
		var revokedAt sql.NullInt64
		if err := rows.Scan(&agent.TenantID, &agent.OwnerID, &agent.OwnedAgentID, &agent.DeviceID, &agent.DeviceName,
			&agent.LocalProfileID, &agent.DisplayName, &agent.RuntimeKind, &capabilities, &workspaceGrants, &available,
			&agent.CatalogDigest, &observedAt, &updatedAt, &revokedAt); err != nil {
			return nil, fmt.Errorf("scan owned agent: %w", err)
		}
		if err := json.Unmarshal([]byte(capabilities), &agent.Capabilities); err != nil {
			return nil, fmt.Errorf("decode owned agent capabilities: %w", err)
		}
		if err := json.Unmarshal([]byte(workspaceGrants), &agent.WorkspaceGrants); err != nil {
			return nil, fmt.Errorf("decode owned agent workspace grants: %w", err)
		}
		agent.Available = available != 0
		agent.ObservedAt = time.Unix(0, observedAt).UTC()
		agent.UpdatedAt = time.Unix(0, updatedAt).UTC()
		if revokedAt.Valid {
			agent.RevokedAt = time.Unix(0, revokedAt.Int64).UTC()
		}
		out = append(out, agent)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate owned agents: %w", err)
	}
	return out, nil
}

func (s *SQLStore) LoadHosting(ctx context.Context, scope OwnerScope) (HostingRecord, bool, error) {
	var record HostingRecord
	var assistantEnabled, fallback int
	var updatedAt int64
	err := s.store.QueryRow(ctx, `
SELECT tenant_id, owner_id, assistant_enabled, assigned_agent_id, hosting_mode, owned_agent_id, workspace_grant_id, fallback_to_managed, revision, updated_at
FROM personal_assistant_hosting WHERE tenant_id = ? AND owner_id = ?`, scope.TenantID, scope.OwnerID).Scan(
		&record.TenantID, &record.OwnerID, &assistantEnabled, &record.AssignedAgentID, &record.HostingMode, &record.OwnedAgentID,
		&record.WorkspaceGrantID, &fallback, &record.Revision, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return HostingRecord{}, false, nil
	}
	if err != nil {
		return HostingRecord{}, false, fmt.Errorf("load personal assistant hosting: %w", err)
	}
	record.AssistantEnabled = assistantEnabled != 0
	record.FallbackToManaged = fallback != 0
	record.UpdatedAt = time.Unix(0, updatedAt).UTC()
	return record, true, nil
}

func (s *SQLStore) SaveHosting(ctx context.Context, record HostingRecord, expectedRevision int64) (HostingRecord, error) {
	err := s.store.WithTx(ctx, func(tx db.Tx) error {
		var current int64
		err := tx.QueryRowContext(ctx, `SELECT revision FROM personal_assistant_hosting WHERE tenant_id = ? AND owner_id = ?`, record.TenantID, record.OwnerID).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			if expectedRevision != 0 {
				return ErrConflict
			}
			record.Revision = 1
			_, err = tx.ExecContext(ctx, `
INSERT INTO personal_assistant_hosting (
 tenant_id, owner_id, assistant_enabled, assigned_agent_id, hosting_mode, owned_agent_id, workspace_grant_id,
 fallback_to_managed, revision, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, record.TenantID, record.OwnerID, boolInt(record.AssistantEnabled), record.AssignedAgentID, record.HostingMode,
				record.OwnedAgentID, record.WorkspaceGrantID, boolInt(record.FallbackToManaged), record.Revision, record.UpdatedAt.UnixNano())
			if err != nil {
				return fmt.Errorf("create personal assistant hosting: %w", err)
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("load personal assistant hosting revision: %w", err)
		}
		if current != expectedRevision {
			return ErrConflict
		}
		record.Revision = current + 1
		result, err := tx.ExecContext(ctx, `
UPDATE personal_assistant_hosting SET assistant_enabled = ?, assigned_agent_id = ?, hosting_mode = ?, owned_agent_id = ?, workspace_grant_id = ?,
 fallback_to_managed = ?, revision = ?, updated_at = ?
WHERE tenant_id = ? AND owner_id = ? AND revision = ?`, boolInt(record.AssistantEnabled), record.AssignedAgentID, record.HostingMode,
			record.OwnedAgentID, record.WorkspaceGrantID, boolInt(record.FallbackToManaged), record.Revision, record.UpdatedAt.UnixNano(),
			record.TenantID, record.OwnerID, expectedRevision)
		if err != nil {
			return fmt.Errorf("update personal assistant hosting: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil || affected != 1 {
			return ErrConflict
		}
		return nil
	})
	return record, err
}

func (s *SQLStore) LoadPersonalAssistantNotifications(ctx context.Context, scope OwnerScope) (PersonalAssistantNotificationRecord, bool, error) {
	var record PersonalAssistantNotificationRecord
	var enabled int
	var updatedAt int64
	err := s.store.QueryRow(ctx, `
SELECT tenant_id, owner_id, approval_notifications_enabled, revision, updated_at
FROM personal_assistant_notification_preferences WHERE tenant_id = ? AND owner_id = ?`, scope.TenantID, scope.OwnerID).Scan(
		&record.TenantID, &record.OwnerID, &enabled, &record.Revision, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return PersonalAssistantNotificationRecord{}, false, nil
	}
	if err != nil {
		return PersonalAssistantNotificationRecord{}, false, fmt.Errorf("load personal assistant notifications: %w", err)
	}
	record.ApprovalNotificationsEnabled = enabled != 0
	record.UpdatedAt = time.Unix(0, updatedAt).UTC()
	return record, true, nil
}

func (s *SQLStore) SavePersonalAssistantNotifications(ctx context.Context, record PersonalAssistantNotificationRecord, expectedRevision int64) (PersonalAssistantNotificationRecord, error) {
	err := s.store.WithTx(ctx, func(tx db.Tx) error {
		var current int64
		err := tx.QueryRowContext(ctx, `SELECT revision FROM personal_assistant_notification_preferences WHERE tenant_id = ? AND owner_id = ?`, record.TenantID, record.OwnerID).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			if expectedRevision != 0 {
				return ErrConflict
			}
			record.Revision = 1
			_, err = tx.ExecContext(ctx, `
INSERT INTO personal_assistant_notification_preferences (
 tenant_id, owner_id, approval_notifications_enabled, revision, updated_at
) VALUES (?, ?, ?, ?, ?)`, record.TenantID, record.OwnerID, boolInt(record.ApprovalNotificationsEnabled), record.Revision, record.UpdatedAt.UnixNano())
			if err != nil {
				return fmt.Errorf("create personal assistant notifications: %w", err)
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("load personal assistant notification revision: %w", err)
		}
		if current != expectedRevision {
			return ErrConflict
		}
		record.Revision = current + 1
		result, err := tx.ExecContext(ctx, `
UPDATE personal_assistant_notification_preferences
SET approval_notifications_enabled = ?, revision = ?, updated_at = ?
WHERE tenant_id = ? AND owner_id = ? AND revision = ?`, boolInt(record.ApprovalNotificationsEnabled), record.Revision,
			record.UpdatedAt.UnixNano(), record.TenantID, record.OwnerID, expectedRevision)
		if err != nil {
			return fmt.Errorf("update personal assistant notifications: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil || affected != 1 {
			return ErrConflict
		}
		return nil
	})
	return record, err
}

func (s *SQLStore) LoadPersonalAssistantAvatarInfo(ctx context.Context, scope OwnerScope) (PersonalAssistantAvatarInfo, error) {
	var info PersonalAssistantAvatarInfo
	var updatedAt int64
	err := s.store.QueryRow(ctx, `
SELECT tenant_id, owner_id, content_type, size_bytes, etag, updated_at
FROM personal_assistant_avatars WHERE tenant_id = ? AND owner_id = ?`, scope.TenantID, scope.OwnerID).Scan(
		&info.TenantID, &info.OwnerID, &info.ContentType, &info.Size, &info.ETag, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return PersonalAssistantAvatarInfo{}, ErrNotFound
	}
	if err != nil {
		return PersonalAssistantAvatarInfo{}, fmt.Errorf("load personal assistant avatar metadata: %w", err)
	}
	info.UpdatedAt = time.Unix(0, updatedAt).UTC()
	return info, nil
}

func (s *SQLStore) LoadPersonalAssistantAvatar(ctx context.Context, scope OwnerScope) (PersonalAssistantAvatar, error) {
	var avatar PersonalAssistantAvatar
	var updatedAt int64
	err := s.store.QueryRow(ctx, `
SELECT tenant_id, owner_id, content_type, size_bytes, etag, updated_at, content
FROM personal_assistant_avatars WHERE tenant_id = ? AND owner_id = ?`, scope.TenantID, scope.OwnerID).Scan(
		&avatar.Info.TenantID, &avatar.Info.OwnerID, &avatar.Info.ContentType, &avatar.Info.Size,
		&avatar.Info.ETag, &updatedAt, &avatar.Content,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return PersonalAssistantAvatar{}, ErrNotFound
	}
	if err != nil {
		return PersonalAssistantAvatar{}, fmt.Errorf("load personal assistant avatar: %w", err)
	}
	avatar.Info.UpdatedAt = time.Unix(0, updatedAt).UTC()
	if int64(len(avatar.Content)) != avatar.Info.Size {
		return PersonalAssistantAvatar{}, fmt.Errorf("load personal assistant avatar: stored size mismatch")
	}
	return avatar, nil
}

func (s *SQLStore) SavePersonalAssistantAvatar(ctx context.Context, scope OwnerScope, contentType string, content []byte, etag string, updatedAt time.Time) (PersonalAssistantAvatarInfo, error) {
	info := PersonalAssistantAvatarInfo{
		TenantID: scope.TenantID, OwnerID: scope.OwnerID, ContentType: contentType,
		Size: int64(len(content)), ETag: etag, UpdatedAt: updatedAt.UTC(),
	}
	_, err := s.store.Exec(ctx, `
INSERT INTO personal_assistant_avatars (tenant_id, owner_id, content_type, size_bytes, etag, updated_at, content)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, owner_id) DO UPDATE SET
 content_type = excluded.content_type, size_bytes = excluded.size_bytes, etag = excluded.etag,
 updated_at = excluded.updated_at, content = excluded.content`,
		info.TenantID, info.OwnerID, info.ContentType, info.Size, info.ETag, info.UpdatedAt.UnixNano(), content)
	if err != nil {
		return PersonalAssistantAvatarInfo{}, fmt.Errorf("save personal assistant avatar: %w", err)
	}
	return info, nil
}

func (s *SQLStore) DeletePersonalAssistantAvatar(ctx context.Context, scope OwnerScope) error {
	if _, err := s.store.Exec(ctx, `DELETE FROM personal_assistant_avatars WHERE tenant_id = ? AND owner_id = ?`, scope.TenantID, scope.OwnerID); err != nil {
		return fmt.Errorf("delete personal assistant avatar: %w", err)
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func schemaStatements(dialect db.Dialect) []string {
	timeType := "INTEGER"
	contentType := "BLOB"
	if dialect == db.DialectPostgres {
		timeType = "BIGINT"
		contentType = "BYTEA"
	}
	return []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS owned_agent_profiles (
 tenant_id TEXT NOT NULL, owner_id TEXT NOT NULL, owned_agent_id TEXT NOT NULL, device_id TEXT NOT NULL,
 local_profile_id TEXT NOT NULL, display_name TEXT NOT NULL, runtime_kind TEXT NOT NULL,
 capabilities_json TEXT NOT NULL, workspace_grants_json TEXT NOT NULL, available INTEGER NOT NULL, catalog_digest TEXT NOT NULL,
 observed_at %s NOT NULL, updated_at %[1]s NOT NULL,
 PRIMARY KEY (tenant_id, owner_id, owned_agent_id), UNIQUE (tenant_id, device_id, local_profile_id)
)`, timeType),
		`CREATE INDEX IF NOT EXISTS idx_owned_agent_profiles_device ON owned_agent_profiles (tenant_id, owner_id, device_id, available)`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS personal_assistant_hosting (
 tenant_id TEXT NOT NULL, owner_id TEXT NOT NULL, assistant_enabled INTEGER NOT NULL,
 hosting_mode TEXT NOT NULL, owned_agent_id TEXT NOT NULL, workspace_grant_id TEXT NOT NULL,
 fallback_to_managed INTEGER NOT NULL,
 revision %s NOT NULL, updated_at %[1]s NOT NULL, PRIMARY KEY (tenant_id, owner_id)
)`, timeType),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS personal_assistant_avatars (
 tenant_id TEXT NOT NULL, owner_id TEXT NOT NULL, content_type TEXT NOT NULL,
 size_bytes %s NOT NULL, etag TEXT NOT NULL, updated_at %[1]s NOT NULL,
 content %s NOT NULL, PRIMARY KEY (tenant_id, owner_id)
)`, timeType, contentType),
	}
}

func personalAssistantNotificationSchemaStatements(dialect db.Dialect) []string {
	timeType := "INTEGER"
	if dialect == db.DialectPostgres {
		timeType = "BIGINT"
	}
	return []string{fmt.Sprintf(`CREATE TABLE IF NOT EXISTS personal_assistant_notification_preferences (
 tenant_id TEXT NOT NULL, owner_id TEXT NOT NULL, approval_notifications_enabled INTEGER NOT NULL,
 revision %s NOT NULL, updated_at %[1]s NOT NULL, PRIMARY KEY (tenant_id, owner_id)
)`, timeType)}
}

var _ Store = (*SQLStore)(nil)
var _ PersonalAssistantAvatarStore = (*SQLStore)(nil)
var _ PersonalAssistantNotificationStore = (*SQLStore)(nil)
