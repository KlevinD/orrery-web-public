package rebac

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

type PlatformStore struct {
	store    *db.Store
	observer AuthorizationRevisionObserver
}

type AuthorizationRevisionObserver interface {
	AuthorizationRevisionAdvanced(int64)
}

type PlatformStoreOption func(*PlatformStore)

func WithAuthorizationRevisionObserver(observer AuthorizationRevisionObserver) PlatformStoreOption {
	return func(store *PlatformStore) {
		store.observer = observer
	}
}

func NewPlatformStore(store *db.Store, options ...PlatformStoreOption) *PlatformStore {
	result := &PlatformStore{store: store}
	for _, option := range options {
		if option != nil {
			option(result)
		}
	}
	return result
}

func (s *PlatformStore) SaveRelationTuple(ctx context.Context, tuple RelationTuple) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("rebac platform store is required")
	}
	tuple = normalizeRelationTuple(tuple)
	if err := validateRelationTuple(tuple); err != nil {
		return err
	}
	var revision int64
	changed := false
	err := s.store.WithTx(ctx, func(tx db.Tx) error {
		var err error
		changed, err = saveRelationTupleTx(ctx, tx, tuple)
		if err != nil || !changed {
			return err
		}
		revision, err = advanceAuthorizationRevisionTx(ctx, tx)
		return err
	})
	if err != nil {
		return err
	}
	if changed && s.observer != nil {
		s.observer.AuthorizationRevisionAdvanced(revision)
	}
	return nil
}

func (s *PlatformStore) DeleteRelationTuple(ctx context.Context, tuple RelationTuple) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("rebac platform store is required")
	}
	tuple = normalizeRelationTuple(tuple)
	if err := validateRelationTuple(tuple); err != nil {
		return err
	}
	var revision int64
	changed := false
	err := s.store.WithTx(ctx, func(tx db.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM rebac_relation_tuples WHERE id = ?`, tuple.ID)
		if err != nil {
			return fmt.Errorf("delete rebac relation tuple: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read deleted rebac relation tuple count: %w", err)
		}
		if affected == 0 {
			return nil
		}
		changed = true
		revision, err = advanceAuthorizationRevisionTx(ctx, tx)
		return err
	})
	if err != nil {
		return err
	}
	if changed && s.observer != nil {
		s.observer.AuthorizationRevisionAdvanced(revision)
	}
	return nil
}

func (s *PlatformStore) ListRelationTuples(ctx context.Context) ([]RelationTuple, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("rebac platform store is required")
	}
	rows, err := s.store.Query(ctx, `
SELECT id, tenant_id, subject_kind, subject_id, subject_ref_json, relation,
	object_kind, object_id, object_ref_json, source_ref_json, valid_from, valid_to, created_at
FROM rebac_relation_tuples
ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list rebac relation tuples: %w", err)
	}
	defer rows.Close()
	out := []RelationTuple{}
	for rows.Next() {
		tuple, err := scanRelationTuple(rows)
		if err != nil {
			return nil, fmt.Errorf("scan rebac relation tuple: %w", err)
		}
		out = append(out, tuple)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rebac relation tuples: %w", err)
	}
	return out, nil
}

func (s *PlatformStore) ListRelationTuplesByTenant(ctx context.Context, tenantID string) ([]RelationTuple, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("rebac platform store is required")
	}
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return nil, fmt.Errorf("relation tuple tenant_id is required")
	}
	rows, err := s.store.Query(ctx, `
SELECT id, tenant_id, subject_kind, subject_id, subject_ref_json, relation,
	object_kind, object_id, object_ref_json, source_ref_json, valid_from, valid_to, created_at
FROM rebac_relation_tuples
WHERE tenant_id = ?
ORDER BY id ASC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list rebac relation tuples by tenant: %w", err)
	}
	defer rows.Close()
	out := []RelationTuple{}
	for rows.Next() {
		tuple, err := scanRelationTuple(rows)
		if err != nil {
			return nil, fmt.Errorf("scan rebac relation tuple: %w", err)
		}
		out = append(out, tuple)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rebac relation tuples: %w", err)
	}
	return out, nil
}

func (s *PlatformStore) ListRelationTuplesByTenants(ctx context.Context, tenantIDs []string) ([]RelationTuple, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("rebac platform store is required")
	}
	unique := make([]string, 0, len(tenantIDs))
	seen := make(map[string]struct{}, len(tenantIDs))
	for _, tenantID := range tenantIDs {
		tenantID = strings.TrimSpace(tenantID)
		if tenantID == "" {
			return nil, fmt.Errorf("relation tuple tenant_id is required")
		}
		if _, exists := seen[tenantID]; exists {
			continue
		}
		seen[tenantID] = struct{}{}
		unique = append(unique, tenantID)
	}
	if len(unique) == 0 {
		return []RelationTuple{}, nil
	}
	if len(unique) > db.MaxRecordPageLimit {
		return nil, fmt.Errorf("relation tuple tenant batch exceeds its limit")
	}
	placeholders := make([]string, len(unique))
	args := make([]any, len(unique))
	for index, tenantID := range unique {
		placeholders[index] = "?"
		args[index] = tenantID
	}
	rows, err := s.store.Query(ctx, `
SELECT id, tenant_id, subject_kind, subject_id, subject_ref_json, relation,
	object_kind, object_id, object_ref_json, source_ref_json, valid_from, valid_to, created_at
FROM rebac_relation_tuples
WHERE tenant_id IN (`+strings.Join(placeholders, ",")+`)
ORDER BY tenant_id ASC, id ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("list rebac relation tuples by tenants: %w", err)
	}
	defer rows.Close()
	out := []RelationTuple{}
	for rows.Next() {
		tuple, err := scanRelationTuple(rows)
		if err != nil {
			return nil, fmt.Errorf("scan rebac relation tuple: %w", err)
		}
		out = append(out, tuple)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rebac relation tuples: %w", err)
	}
	return out, nil
}

func saveRelationTupleTx(ctx context.Context, tx db.Tx, tuple RelationTuple) (bool, error) {
	result, err := tx.ExecContext(ctx, `
INSERT INTO rebac_relation_tuples (
	id, tenant_id, subject_kind, subject_id, subject_ref_json, relation,
	object_kind, object_id, object_ref_json, source_ref_json, valid_from, valid_to, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	tenant_id = excluded.tenant_id,
	subject_kind = excluded.subject_kind,
	subject_id = excluded.subject_id,
	subject_ref_json = excluded.subject_ref_json,
	relation = excluded.relation,
	object_kind = excluded.object_kind,
	object_id = excluded.object_id,
	object_ref_json = excluded.object_ref_json,
	source_ref_json = excluded.source_ref_json,
	valid_from = excluded.valid_from,
	valid_to = excluded.valid_to,
	created_at = excluded.created_at
WHERE rebac_relation_tuples.tenant_id <> excluded.tenant_id
	OR rebac_relation_tuples.subject_kind <> excluded.subject_kind
	OR rebac_relation_tuples.subject_id <> excluded.subject_id
	OR rebac_relation_tuples.subject_ref_json <> excluded.subject_ref_json
	OR rebac_relation_tuples.relation <> excluded.relation
	OR rebac_relation_tuples.object_kind <> excluded.object_kind
	OR rebac_relation_tuples.object_id <> excluded.object_id
	OR rebac_relation_tuples.object_ref_json <> excluded.object_ref_json
	OR rebac_relation_tuples.source_ref_json <> excluded.source_ref_json
	OR rebac_relation_tuples.valid_from IS DISTINCT FROM excluded.valid_from
	OR rebac_relation_tuples.valid_to IS DISTINCT FROM excluded.valid_to`,
		tuple.ID,
		tuple.TenantID,
		tuple.Subject.Kind,
		tuple.Subject.ID,
		mustRefJSON(tuple.Subject),
		tuple.Relation,
		tuple.Object.Kind,
		tuple.Object.ID,
		mustRefJSON(tuple.Object),
		mustRefJSON(tuple.Source),
		optionalTimeToUnixNano(tuple.ValidFrom),
		optionalTimeToUnixNano(tuple.ValidTo),
		timeToUnixNano(tuple.CreatedAt),
	)
	if err != nil {
		return false, fmt.Errorf("save rebac relation tuple: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read saved rebac relation tuple count: %w", err)
	}
	return affected == 1, nil
}

func advanceAuthorizationRevisionTx(ctx context.Context, tx db.Tx) (int64, error) {
	result, err := tx.ExecContext(ctx, `UPDATE rebac_authorization_revision SET revision = revision + 1 WHERE id = 'global'`)
	if err != nil {
		return 0, fmt.Errorf("advance authorization revision: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read authorization revision update count: %w", err)
	}
	if affected != 1 {
		return 0, fmt.Errorf("advance authorization revision: revision state is not initialized")
	}
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM rebac_authorization_revision WHERE id = 'global'`).Scan(&revision); err != nil {
		return 0, fmt.Errorf("read authorization revision: %w", err)
	}
	return revision, nil
}

func (s *PlatformStore) AuthorizationRevision(ctx context.Context) (int64, error) {
	if s == nil || s.store == nil {
		return 0, fmt.Errorf("rebac platform store is required")
	}
	var revision int64
	if err := s.store.QueryRow(ctx, `SELECT revision FROM rebac_authorization_revision WHERE id = 'global'`).Scan(&revision); err != nil {
		return 0, fmt.Errorf("read authorization revision: %w", err)
	}
	if revision < 0 {
		return 0, fmt.Errorf("authorization revision is invalid")
	}
	return revision, nil
}

func (s *PlatformStore) CheckRelation(ctx context.Context, check RelationCheck) (bool, RelationTuple, error) {
	if s == nil || s.store == nil {
		return false, RelationTuple{}, fmt.Errorf("rebac platform store is required")
	}
	check = normalizeRelationCheck(check)
	if err := validateRelationCheck(check); err != nil {
		return false, RelationTuple{}, err
	}
	at := timeToUnixNano(check.At)
	row := s.store.QueryRow(ctx, `
SELECT id, tenant_id, subject_kind, subject_id, subject_ref_json, relation,
	object_kind, object_id, object_ref_json, source_ref_json, valid_from, valid_to, created_at
FROM rebac_relation_tuples
WHERE tenant_id = ?
	AND subject_kind = ?
	AND subject_id = ?
	AND relation = ?
	AND object_kind = ?
	AND object_id = ?
	AND (valid_from IS NULL OR valid_from <= ?)
	AND (valid_to IS NULL OR valid_to > ?)
ORDER BY created_at DESC, id ASC
LIMIT 1`,
		check.TenantID,
		check.Subject.Kind,
		check.Subject.ID,
		check.Relation,
		check.Object.Kind,
		check.Object.ID,
		at,
		at,
	)
	tuple, err := scanRelationTuple(row)
	if errors.Is(err, sql.ErrNoRows) {
		return false, RelationTuple{}, nil
	}
	if err != nil {
		return false, RelationTuple{}, err
	}
	return true, tuple, nil
}

type relationTupleScanner interface {
	Scan(dest ...any) error
}

func scanRelationTuple(scanner relationTupleScanner) (RelationTuple, error) {
	var tuple RelationTuple
	var subjectRefJSON, objectRefJSON, sourceRefJSON string
	var validFrom, validTo sql.NullInt64
	var createdAt int64
	if err := scanner.Scan(
		&tuple.ID,
		&tuple.TenantID,
		&tuple.Subject.Kind,
		&tuple.Subject.ID,
		&subjectRefJSON,
		&tuple.Relation,
		&tuple.Object.Kind,
		&tuple.Object.ID,
		&objectRefJSON,
		&sourceRefJSON,
		&validFrom,
		&validTo,
		&createdAt,
	); err != nil {
		return RelationTuple{}, err
	}
	decodeRefJSON(subjectRefJSON, &tuple.Subject)
	decodeRefJSON(objectRefJSON, &tuple.Object)
	decodeRefJSON(sourceRefJSON, &tuple.Source)
	tuple.ValidFrom = unixNanoToOptionalTime(validFrom)
	tuple.ValidTo = unixNanoToOptionalTime(validTo)
	tuple.CreatedAt = unixNanoToTime(createdAt)
	return tuple, nil
}

func mustRefJSON(ref Ref) string {
	data, err := json.Marshal(ref)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func decodeRefJSON(raw string, out *Ref) {
	if raw == "" {
		raw = "{}"
	}
	_ = json.Unmarshal([]byte(raw), out)
	*out = normalizeRef(*out)
}

func timeToUnixNano(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().UnixNano()
}

func optionalTimeToUnixNano(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.UTC().UnixNano()
}

func unixNanoToOptionalTime(value sql.NullInt64) *time.Time {
	if !value.Valid || value.Int64 == 0 {
		return nil
	}
	t := unixNanoToTime(value.Int64)
	return &t
}

func unixNanoToTime(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(0, value).UTC()
}

var _ Store = (*PlatformStore)(nil)
