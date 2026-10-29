package datapolicy

import (
	"context"
	"fmt"
	"sort"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/db"
)

type PlatformStore struct {
	store    *db.Store
	observer PolicyRevisionObserver
}

type PolicyRevisionObserver interface {
	PolicyRevisionAdvanced(int64)
}

type PlatformStoreOption func(*PlatformStore)

func WithPolicyRevisionObserver(observer PolicyRevisionObserver) PlatformStoreOption {
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

func (s *PlatformStore) SaveFieldPolicy(ctx context.Context, policy FieldPolicy) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("data policy platform store is required")
	}
	policy = normalizeFieldPolicy(policy)
	if err := validateFieldPolicy(policy); err != nil {
		return err
	}
	var revision int64
	err := s.store.WithTx(ctx, func(tx db.Tx) error {
		if err := saveFieldPolicyTx(ctx, tx, policy); err != nil {
			return err
		}
		var err error
		revision, err = advancePolicyRevisionTx(ctx, tx)
		return err
	})
	if err != nil {
		return err
	}
	if s.observer != nil {
		s.observer.PolicyRevisionAdvanced(revision)
	}
	return nil
}

func saveFieldPolicyTx(ctx context.Context, tx db.Tx, policy FieldPolicy) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO field_policies (
	id, tenant_id, consumer, purpose, object_type, field_path, effect, reason,
	enabled, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	tenant_id = excluded.tenant_id,
	consumer = excluded.consumer,
	purpose = excluded.purpose,
	object_type = excluded.object_type,
	field_path = excluded.field_path,
	effect = excluded.effect,
	reason = excluded.reason,
	enabled = excluded.enabled,
	updated_at = excluded.updated_at`,
		policy.ID,
		policy.TenantID,
		policy.Consumer,
		policy.Purpose,
		policy.ObjectType,
		policy.FieldPath,
		policy.Effect,
		policy.Reason,
		policy.Enabled,
		timeToUnixNano(policy.CreatedAt),
		timeToUnixNano(policy.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("save field policy: %w", err)
	}
	return nil
}

func (s *PlatformStore) SavePurposePolicy(ctx context.Context, policy PurposePolicy) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("data policy platform store is required")
	}
	policy = normalizePurposePolicy(policy)
	if err := validatePurposePolicy(policy); err != nil {
		return err
	}
	var revision int64
	err := s.store.WithTx(ctx, func(tx db.Tx) error {
		if err := savePurposePolicyTx(ctx, tx, policy); err != nil {
			return err
		}
		var err error
		revision, err = advancePolicyRevisionTx(ctx, tx)
		return err
	})
	if err != nil {
		return err
	}
	if s.observer != nil {
		s.observer.PolicyRevisionAdvanced(revision)
	}
	return nil
}

func savePurposePolicyTx(ctx context.Context, tx db.Tx, policy PurposePolicy) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO purpose_policies (
	id, tenant_id, consumer, purpose, object_type, effect, reason,
	enabled, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	tenant_id = excluded.tenant_id,
	consumer = excluded.consumer,
	purpose = excluded.purpose,
	object_type = excluded.object_type,
	effect = excluded.effect,
	reason = excluded.reason,
	enabled = excluded.enabled,
	updated_at = excluded.updated_at`,
		policy.ID,
		policy.TenantID,
		policy.Consumer,
		policy.Purpose,
		policy.ObjectType,
		policy.Effect,
		policy.Reason,
		policy.Enabled,
		timeToUnixNano(policy.CreatedAt),
		timeToUnixNano(policy.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("save purpose policy: %w", err)
	}
	return nil
}

func advancePolicyRevisionTx(ctx context.Context, tx db.Tx) (int64, error) {
	result, err := tx.ExecContext(ctx, `UPDATE data_policy_revision SET revision = revision + 1 WHERE id = 'global'`)
	if err != nil {
		return 0, fmt.Errorf("advance data policy revision: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read data policy revision update count: %w", err)
	}
	if affected != 1 {
		return 0, fmt.Errorf("advance data policy revision: revision state is not initialized")
	}
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM data_policy_revision WHERE id = 'global'`).Scan(&revision); err != nil {
		return 0, fmt.Errorf("read data policy revision: %w", err)
	}
	return revision, nil
}

func (s *PlatformStore) PolicyRevision(ctx context.Context) (int64, error) {
	if s == nil || s.store == nil {
		return 0, fmt.Errorf("data policy platform store is required")
	}
	var revision int64
	if err := s.store.QueryRow(ctx, `SELECT revision FROM data_policy_revision WHERE id = 'global'`).Scan(&revision); err != nil {
		return 0, fmt.Errorf("read data policy revision: %w", err)
	}
	if revision < 0 {
		return 0, fmt.Errorf("data policy revision is invalid")
	}
	return revision, nil
}

func (s *PlatformStore) HasActivePurposeDeny(ctx context.Context, tenantID, consumer, purpose string) (bool, error) {
	if s == nil || s.store == nil {
		return false, fmt.Errorf("data policy platform store is required")
	}
	var count int
	if err := s.store.QueryRow(ctx, `
SELECT COUNT(*)
FROM purpose_policies
WHERE tenant_id = ? AND consumer = ? AND purpose = ? AND effect = ? AND enabled = TRUE`,
		tenantID, consumer, purpose, EffectDeny,
	).Scan(&count); err != nil {
		return false, fmt.Errorf("check active purpose deny: %w", err)
	}
	return count > 0, nil
}

func (s *PlatformStore) DecideContext(ctx context.Context, req contextgraph.BuildContextBundleRequest, objects []contextgraph.Object) (contextgraph.PermissionDecision, error) {
	if s == nil || s.store == nil {
		return contextgraph.PermissionDecision{}, fmt.Errorf("data policy platform store is required")
	}
	fields, err := s.listFieldPolicies(ctx, req)
	if err != nil {
		return contextgraph.PermissionDecision{}, err
	}
	purposes, err := s.listPurposePolicies(ctx, req)
	if err != nil {
		return contextgraph.PermissionDecision{}, err
	}
	return decideContext(req, objects, fields, purposes), nil
}

func (s *PlatformStore) listFieldPolicies(ctx context.Context, req contextgraph.BuildContextBundleRequest) ([]FieldPolicy, error) {
	rows, err := s.store.Query(ctx, `
SELECT id, tenant_id, consumer, purpose, object_type, field_path, effect, reason, enabled, created_at, updated_at
FROM field_policies
WHERE tenant_id = ? AND consumer = ? AND purpose = ? AND enabled = TRUE
ORDER BY id`, req.TenantID, req.Consumer, req.Purpose)
	if err != nil {
		return nil, fmt.Errorf("list field policies: %w", err)
	}
	defer rows.Close()
	var out []FieldPolicy
	for rows.Next() {
		var policy FieldPolicy
		var createdAt, updatedAt int64
		if err := rows.Scan(&policy.ID, &policy.TenantID, &policy.Consumer, &policy.Purpose, &policy.ObjectType, &policy.FieldPath, &policy.Effect, &policy.Reason, &policy.Enabled, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan field policy: %w", err)
		}
		policy.CreatedAt = unixNanoToTime(createdAt)
		policy.UpdatedAt = unixNanoToTime(updatedAt)
		out = append(out, normalizeFieldPolicy(policy))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list field policies: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *PlatformStore) listPurposePolicies(ctx context.Context, req contextgraph.BuildContextBundleRequest) ([]PurposePolicy, error) {
	rows, err := s.store.Query(ctx, `
SELECT id, tenant_id, consumer, purpose, object_type, effect, reason, enabled, created_at, updated_at
FROM purpose_policies
WHERE tenant_id = ? AND consumer = ? AND purpose = ? AND enabled = TRUE
ORDER BY id`, req.TenantID, req.Consumer, req.Purpose)
	if err != nil {
		return nil, fmt.Errorf("list purpose policies: %w", err)
	}
	defer rows.Close()
	var out []PurposePolicy
	for rows.Next() {
		var policy PurposePolicy
		var createdAt, updatedAt int64
		if err := rows.Scan(&policy.ID, &policy.TenantID, &policy.Consumer, &policy.Purpose, &policy.ObjectType, &policy.Effect, &policy.Reason, &policy.Enabled, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan purpose policy: %w", err)
		}
		policy.CreatedAt = unixNanoToTime(createdAt)
		policy.UpdatedAt = unixNanoToTime(updatedAt)
		out = append(out, normalizePurposePolicy(policy))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list purpose policies: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func timeToUnixNano(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().UnixNano()
}

func unixNanoToTime(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(0, value).UTC()
}

var _ Store = (*PlatformStore)(nil)
