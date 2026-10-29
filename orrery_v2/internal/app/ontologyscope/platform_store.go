package ontologyscope

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

type PlatformBindingStore struct {
	store *db.Store
	now   func() time.Time
}

func NewPlatformBindingStore(store *db.Store) *PlatformBindingStore {
	return &PlatformBindingStore{store: store, now: time.Now}
}

func (s *PlatformBindingStore) GetBinding(ctx context.Context, tenantID string) (ScopeBinding, error) {
	if s == nil || s.store == nil {
		return ScopeBinding{}, ErrDependencyUnavailable
	}
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return ScopeBinding{}, ErrInvalidBinding
	}
	binding, err := scanBinding(s.store.QueryRow(ctx, bindingSelectSQL()+` WHERE tenant_id = ?`, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return ScopeBinding{}, ErrBindingNotFound
	}
	return binding, err
}

func (s *PlatformBindingStore) GetBindings(ctx context.Context, tenantIDs []string) (map[string]ScopeBinding, error) {
	if s == nil || s.store == nil || len(tenantIDs) > maxBindingResolveBatch {
		return nil, ErrDependencyUnavailable
	}
	unique := make([]string, 0, len(tenantIDs))
	seen := make(map[string]struct{}, len(tenantIDs))
	for _, tenantID := range tenantIDs {
		tenantID = strings.TrimSpace(tenantID)
		if tenantID == "" {
			return nil, ErrInvalidBinding
		}
		if _, exists := seen[tenantID]; exists {
			continue
		}
		seen[tenantID] = struct{}{}
		unique = append(unique, tenantID)
	}
	out := make(map[string]ScopeBinding, len(unique))
	const queryBatch = 500
	for start := 0; start < len(unique); start += queryBatch {
		end := min(start+queryBatch, len(unique))
		batch := unique[start:end]
		placeholders := make([]string, len(batch))
		args := make([]any, len(batch))
		for index, tenantID := range batch {
			placeholders[index] = "?"
			args[index] = tenantID
		}
		rows, err := s.store.Query(ctx, bindingSelectSQL()+` WHERE tenant_id IN (`+strings.Join(placeholders, ",")+`) ORDER BY tenant_id ASC`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			binding, scanErr := scanBinding(rows)
			if scanErr != nil {
				rows.Close()
				return nil, scanErr
			}
			out[binding.TenantID] = binding
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

func (s *PlatformBindingStore) SaveBinding(ctx context.Context, binding ScopeBinding, expectedVersion int64, changedBy string) (ScopeBinding, error) {
	if s == nil || s.store == nil {
		return ScopeBinding{}, ErrDependencyUnavailable
	}
	binding = NormalizeBinding(binding)
	if err := ValidateBinding(binding); err != nil {
		return ScopeBinding{}, err
	}
	if binding.TenantID == "system" {
		return ScopeBinding{}, fmt.Errorf("%w: system binding is built in and cannot be persisted", ErrInvalidBinding)
	}
	if expectedVersion < 0 {
		return ScopeBinding{}, ErrBindingConflict
	}
	changedBy = strings.TrimSpace(changedBy)
	if changedBy == "" {
		return ScopeBinding{}, fmt.Errorf("%w: changed_by is required", ErrInvalidBinding)
	}
	now := time.Now().UTC()
	if s.now != nil {
		now = s.now().UTC()
	}
	var saved ScopeBinding
	err := s.store.WithTx(ctx, func(tx db.Tx) error {
		var saveErr error
		saved, saveErr = saveBindingTx(ctx, tx, binding, expectedVersion, changedBy, now)
		return saveErr
	})
	if err != nil {
		return ScopeBinding{}, err
	}
	return saved, nil
}

func saveBindingTx(ctx context.Context, tx db.Tx, binding ScopeBinding, expectedVersion int64, changedBy string, now time.Time) (ScopeBinding, error) {
	current, currentErr := scanBinding(tx.QueryRowContext(ctx, bindingSelectSQL()+` WHERE tenant_id = ?`, binding.TenantID))
	switch {
	case errors.Is(currentErr, sql.ErrNoRows):
		if expectedVersion != 0 {
			return ScopeBinding{}, ErrBindingConflict
		}
		binding.BindingVersion = 1
		binding.CreatedAt = now
	case currentErr != nil:
		return ScopeBinding{}, currentErr
	default:
		if current.BindingVersion != expectedVersion {
			return ScopeBinding{}, ErrBindingConflict
		}
		if bindingsEquivalent(current, binding) {
			return current, nil
		}
		binding.BindingVersion = current.BindingVersion + 1
		binding.CreatedAt = current.CreatedAt
	}
	binding.UpdatedAt = now
	if _, err := tx.ExecContext(ctx, `
INSERT INTO ontology_scope_bindings (
	tenant_id, kind, source_object_type, source_object_id, authorization_tenant_id,
	read_action, manage_action, label_object_type, label_object_id, label_tenant_id,
	binding_version, state, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id) DO UPDATE SET
	kind = excluded.kind,
	source_object_type = excluded.source_object_type,
	source_object_id = excluded.source_object_id,
	authorization_tenant_id = excluded.authorization_tenant_id,
	read_action = excluded.read_action,
	manage_action = excluded.manage_action,
	label_object_type = excluded.label_object_type,
	label_object_id = excluded.label_object_id,
	label_tenant_id = excluded.label_tenant_id,
	binding_version = excluded.binding_version,
	state = excluded.state,
	updated_at = excluded.updated_at`, bindingArgs(binding)...); err != nil {
		return ScopeBinding{}, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO ontology_scope_binding_audits (
	tenant_id, binding_version, kind, source_object_type, source_object_id,
	authorization_tenant_id, read_action, manage_action, label_object_type,
	label_object_id, label_tenant_id, state, changed_by, changed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		binding.TenantID, binding.BindingVersion, binding.Kind, binding.SourceObject.Type, binding.SourceObject.ID,
		binding.AuthorizationTenantID, binding.ReadAction, binding.ManageAction, binding.LabelRef.Type,
		binding.LabelRef.ID, binding.LabelRef.TenantID, binding.State, changedBy, now.UnixMilli()); err != nil {
		return ScopeBinding{}, err
	}
	return binding, nil
}

func (s *PlatformBindingStore) ListBindings(ctx context.Context, query BindingQuery) ([]ScopeBinding, error) {
	if s == nil || s.store == nil {
		return nil, ErrDependencyUnavailable
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	clauses := []string{"tenant_id > ?"}
	args := []any{strings.TrimSpace(query.AfterTenantID)}
	if kind := Kind(strings.ToLower(strings.TrimSpace(string(query.Kind)))); kind != "" {
		if !validKind(kind) {
			return nil, ErrInvalidBinding
		}
		clauses = append(clauses, "kind = ?")
		args = append(args, kind)
	}
	if state := BindingState(strings.ToLower(strings.TrimSpace(string(query.State)))); state != "" {
		if !validState(state) {
			return nil, ErrInvalidBinding
		}
		clauses = append(clauses, "state = ?")
		args = append(args, state)
	}
	args = append(args, limit)
	rows, err := s.store.Query(ctx, bindingSelectSQL()+` WHERE `+strings.Join(clauses, " AND ")+` ORDER BY tenant_id ASC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bindings := make([]ScopeBinding, 0, limit)
	for rows.Next() {
		binding, scanErr := scanBinding(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		bindings = append(bindings, binding)
	}
	return bindings, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanBinding(row rowScanner) (ScopeBinding, error) {
	var binding ScopeBinding
	var kind string
	var state string
	var createdAt int64
	var updatedAt int64
	err := row.Scan(
		&binding.TenantID,
		&kind,
		&binding.SourceObject.Type,
		&binding.SourceObject.ID,
		&binding.AuthorizationTenantID,
		&binding.ReadAction,
		&binding.ManageAction,
		&binding.LabelRef.Type,
		&binding.LabelRef.ID,
		&binding.LabelRef.TenantID,
		&binding.BindingVersion,
		&state,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return ScopeBinding{}, err
	}
	binding.Kind = Kind(kind)
	binding.State = BindingState(state)
	binding.CreatedAt = time.UnixMilli(createdAt).UTC()
	binding.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return NormalizeBinding(binding), nil
}

func bindingSelectSQL() string {
	return `SELECT tenant_id, kind, source_object_type, source_object_id, authorization_tenant_id,
	read_action, manage_action, label_object_type, label_object_id, label_tenant_id,
	binding_version, state, created_at, updated_at FROM ontology_scope_bindings`
}

func bindingArgs(binding ScopeBinding) []any {
	return []any{
		binding.TenantID,
		binding.Kind,
		binding.SourceObject.Type,
		binding.SourceObject.ID,
		binding.AuthorizationTenantID,
		binding.ReadAction,
		binding.ManageAction,
		binding.LabelRef.Type,
		binding.LabelRef.ID,
		binding.LabelRef.TenantID,
		binding.BindingVersion,
		binding.State,
		binding.CreatedAt.UnixMilli(),
		binding.UpdatedAt.UnixMilli(),
	}
}
