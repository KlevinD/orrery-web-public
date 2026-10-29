package ontology

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/platform/db"
)

type MetadataPromotionPlanStore interface {
	SaveMetadataPromotionPlan(context.Context, MetadataPromotionPlanRecord) error
	GetMetadataPromotionPlan(context.Context, string) (MetadataPromotionPlanRecord, error)
	GetMetadataPromotionPlanForUpdate(context.Context, string) (MetadataPromotionPlanRecord, error)
	TransitionMetadataPromotionPlan(context.Context, MetadataPromotionPlanTransition) (MetadataPromotionPlanRecord, error)
}

type MemoryMetadataPromotionPlanStore struct {
	mu    sync.RWMutex
	plans map[string]MetadataPromotionPlanRecord
	now   func() time.Time
}

func NewMemoryMetadataPromotionPlanStore() *MemoryMetadataPromotionPlanStore {
	return &MemoryMetadataPromotionPlanStore{plans: map[string]MetadataPromotionPlanRecord{}, now: time.Now}
}

func (s *MemoryMetadataPromotionPlanStore) SaveMetadataPromotionPlan(ctx context.Context, record MetadataPromotionPlanRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	normalized, err := normalizeMetadataPromotionPlan(record, s.now)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.plans[normalized.PlanID]; exists {
		return fmt.Errorf("%w: metadata promotion plan %s already exists", ErrVersionConflict, normalized.PlanID)
	}
	s.plans[normalized.PlanID] = normalized
	return nil
}

func (s *MemoryMetadataPromotionPlanStore) GetMetadataPromotionPlan(ctx context.Context, planID string) (MetadataPromotionPlanRecord, error) {
	if err := ctx.Err(); err != nil {
		return MetadataPromotionPlanRecord{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	plan, exists := s.plans[strings.TrimSpace(planID)]
	if !exists {
		return MetadataPromotionPlanRecord{}, fmt.Errorf("%w: metadata promotion plan %s", ErrNotFound, planID)
	}
	return cloneMetadataPromotionPlan(plan), nil
}

func (s *MemoryMetadataPromotionPlanStore) GetMetadataPromotionPlanForUpdate(ctx context.Context, planID string) (MetadataPromotionPlanRecord, error) {
	return s.GetMetadataPromotionPlan(ctx, planID)
}

func (s *MemoryMetadataPromotionPlanStore) TransitionMetadataPromotionPlan(ctx context.Context, transition MetadataPromotionPlanTransition) (MetadataPromotionPlanRecord, error) {
	if err := ctx.Err(); err != nil {
		return MetadataPromotionPlanRecord{}, err
	}
	normalized, err := normalizeMetadataPromotionPlanTransition(transition)
	if err != nil {
		return MetadataPromotionPlanRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, exists := s.plans[normalized.PlanID]
	if !exists {
		return MetadataPromotionPlanRecord{}, fmt.Errorf("%w: metadata promotion plan %s", ErrNotFound, normalized.PlanID)
	}
	if plan.State != normalized.ExpectedState {
		return MetadataPromotionPlanRecord{}, fmt.Errorf("%w: metadata promotion plan %s is %s", ErrVersionConflict, normalized.PlanID, plan.State)
	}
	plan.State = normalized.State
	plan.IdempotencyKey = normalized.IdempotencyKey
	plan.ReceiptID = normalized.ReceiptID
	plan.InvalidationReason = normalized.InvalidationReason
	plan.UpdatedAt = s.now().UTC()
	plan, err = normalizeMetadataPromotionPlan(plan, s.now)
	if err != nil {
		return MetadataPromotionPlanRecord{}, err
	}
	s.plans[plan.PlanID] = plan
	return cloneMetadataPromotionPlan(plan), nil
}

type PlatformMetadataPromotionPlanStore struct {
	store *db.Store
	tx    db.Tx
	now   func() time.Time
}

func NewPlatformMetadataPromotionPlanStore(store *db.Store) *PlatformMetadataPromotionPlanStore {
	return &PlatformMetadataPromotionPlanStore{store: store, now: time.Now}
}

func NewPlatformMetadataPromotionPlanStoreTx(tx db.Tx) *PlatformMetadataPromotionPlanStore {
	return &PlatformMetadataPromotionPlanStore{tx: tx, now: time.Now}
}

func (s *PlatformMetadataPromotionPlanStore) ready() error {
	if s == nil || s.store == nil && s.tx == nil {
		return fmt.Errorf("%w: metadata promotion plan store is required", ErrInvalidDefinition)
	}
	return nil
}

func (s *PlatformMetadataPromotionPlanStore) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if s.tx != nil {
		return s.tx.ExecContext(ctx, query, args...)
	}
	return s.store.Exec(ctx, query, args...)
}

func (s *PlatformMetadataPromotionPlanStore) queryRow(ctx context.Context, query string, args ...any) rowScanner {
	if s.tx != nil {
		return s.tx.QueryRowContext(ctx, query, args...)
	}
	return s.store.QueryRow(ctx, query, args...)
}

func (s *PlatformMetadataPromotionPlanStore) dialect() db.Dialect {
	if s != nil && s.store != nil {
		return s.store.Dialect()
	}
	if s != nil && s.tx != nil {
		if dialecter, ok := s.tx.(interface{ Dialect() db.Dialect }); ok {
			return dialecter.Dialect()
		}
	}
	return db.DialectSQLite
}

func (s *PlatformMetadataPromotionPlanStore) SaveMetadataPromotionPlan(ctx context.Context, record MetadataPromotionPlanRecord) error {
	if err := s.ready(); err != nil {
		return err
	}
	normalized, err := normalizeMetadataPromotionPlan(record, s.now)
	if err != nil {
		return err
	}
	result, err := s.exec(ctx, `
INSERT INTO ontology_metadata_promotion_plans (
	plan_id, principal_key, tenant_id, binding_version, proposal_id, proposal_revision, proposal_content_hash,
	targets_json, diff_hash, diff_json, authorization_revision, policy_revision, expires_at, state,
	idempotency_key, receipt_id, invalidation_reason, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(plan_id) DO NOTHING`,
		normalized.PlanID, normalized.PrincipalKey, normalized.TenantID, normalized.BindingVersion,
		normalized.ProposalID, normalized.ProposalRevision, normalized.ProposalContentHash,
		mustJSON(normalized.Targets), normalized.DiffHash, mustJSON(normalized.Diff),
		normalized.AuthorizationRevision, normalized.PolicyRevision, timeToUnixNano(normalized.ExpiresAt), normalized.State,
		normalized.IdempotencyKey, normalized.ReceiptID, normalized.InvalidationReason,
		timeToUnixNano(normalized.CreatedAt), timeToUnixNano(normalized.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("save ontology metadata promotion plan: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("%w: metadata promotion plan %s already exists", ErrVersionConflict, normalized.PlanID)
	}
	return nil
}

func (s *PlatformMetadataPromotionPlanStore) GetMetadataPromotionPlan(ctx context.Context, planID string) (MetadataPromotionPlanRecord, error) {
	return s.getMetadataPromotionPlan(ctx, planID, false)
}

func (s *PlatformMetadataPromotionPlanStore) GetMetadataPromotionPlanForUpdate(ctx context.Context, planID string) (MetadataPromotionPlanRecord, error) {
	return s.getMetadataPromotionPlan(ctx, planID, true)
}

func (s *PlatformMetadataPromotionPlanStore) getMetadataPromotionPlan(ctx context.Context, planID string, forUpdate bool) (MetadataPromotionPlanRecord, error) {
	if err := s.ready(); err != nil {
		return MetadataPromotionPlanRecord{}, err
	}
	query := metadataPromotionPlanSelectSQL() + ` WHERE plan_id = ?`
	if forUpdate && s.dialect() == db.DialectPostgres {
		query += ` FOR UPDATE`
	}
	plan, err := scanMetadataPromotionPlan(s.queryRow(ctx, query, strings.TrimSpace(planID)))
	if errors.Is(err, sql.ErrNoRows) {
		return MetadataPromotionPlanRecord{}, fmt.Errorf("%w: metadata promotion plan %s", ErrNotFound, planID)
	}
	if err != nil {
		return MetadataPromotionPlanRecord{}, err
	}
	return plan, nil
}

func (s *PlatformMetadataPromotionPlanStore) TransitionMetadataPromotionPlan(ctx context.Context, transition MetadataPromotionPlanTransition) (MetadataPromotionPlanRecord, error) {
	if err := s.ready(); err != nil {
		return MetadataPromotionPlanRecord{}, err
	}
	normalized, err := normalizeMetadataPromotionPlanTransition(transition)
	if err != nil {
		return MetadataPromotionPlanRecord{}, err
	}
	updatedAt := s.now().UTC()
	result, err := s.exec(ctx, `
UPDATE ontology_metadata_promotion_plans
SET state = ?, idempotency_key = ?, receipt_id = ?, invalidation_reason = ?, updated_at = ?
WHERE plan_id = ? AND state = ?`,
		normalized.State, normalized.IdempotencyKey, normalized.ReceiptID, normalized.InvalidationReason,
		timeToUnixNano(updatedAt), normalized.PlanID, normalized.ExpectedState,
	)
	if err != nil {
		return MetadataPromotionPlanRecord{}, fmt.Errorf("transition ontology metadata promotion plan: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return MetadataPromotionPlanRecord{}, err
	}
	if affected == 0 {
		current, getErr := s.GetMetadataPromotionPlan(ctx, normalized.PlanID)
		if getErr != nil {
			return MetadataPromotionPlanRecord{}, getErr
		}
		return MetadataPromotionPlanRecord{}, fmt.Errorf("%w: metadata promotion plan %s is %s", ErrVersionConflict, normalized.PlanID, current.State)
	}
	return s.GetMetadataPromotionPlan(ctx, normalized.PlanID)
}

func metadataPromotionPlanSelectSQL() string {
	return `SELECT plan_id, principal_key, tenant_id, binding_version, proposal_id, proposal_revision, proposal_content_hash,
targets_json, diff_hash, diff_json, authorization_revision, policy_revision, expires_at, state,
idempotency_key, receipt_id, invalidation_reason, created_at, updated_at
FROM ontology_metadata_promotion_plans`
}

func scanMetadataPromotionPlan(row rowScanner) (MetadataPromotionPlanRecord, error) {
	var plan MetadataPromotionPlanRecord
	var targetsJSON, diffJSON string
	var expiresAt, createdAt, updatedAt int64
	if err := row.Scan(
		&plan.PlanID, &plan.PrincipalKey, &plan.TenantID, &plan.BindingVersion,
		&plan.ProposalID, &plan.ProposalRevision, &plan.ProposalContentHash,
		&targetsJSON, &plan.DiffHash, &diffJSON, &plan.AuthorizationRevision, &plan.PolicyRevision,
		&expiresAt, &plan.State, &plan.IdempotencyKey, &plan.ReceiptID, &plan.InvalidationReason,
		&createdAt, &updatedAt,
	); err != nil {
		return MetadataPromotionPlanRecord{}, err
	}
	if err := json.Unmarshal([]byte(targetsJSON), &plan.Targets); err != nil {
		return MetadataPromotionPlanRecord{}, fmt.Errorf("decode ontology metadata promotion plan targets: %w", err)
	}
	if err := json.Unmarshal([]byte(diffJSON), &plan.Diff); err != nil {
		return MetadataPromotionPlanRecord{}, fmt.Errorf("decode ontology metadata promotion plan diff: %w", err)
	}
	plan.ExpiresAt = unixNanoToTime(expiresAt)
	plan.CreatedAt = unixNanoToTime(createdAt)
	plan.UpdatedAt = unixNanoToTime(updatedAt)
	return normalizeMetadataPromotionPlan(plan, func() time.Time { return plan.UpdatedAt })
}
