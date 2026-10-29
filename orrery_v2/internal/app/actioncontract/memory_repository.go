package actioncontract

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

type MemoryRepository struct {
	mu       sync.RWMutex
	types    map[ActionTypeID]ActionType
	runs     map[ActionRunID]ActionRun
	receipts map[WritebackReceiptID]WritebackReceipt
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		types:    map[ActionTypeID]ActionType{},
		runs:     map[ActionRunID]ActionRun{},
		receipts: map[WritebackReceiptID]WritebackReceipt{},
	}
}

func (r *MemoryRepository) SaveActionType(ctx context.Context, actionType ActionType) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.types[actionType.ID]; exists {
		return fmt.Errorf("%w: action type %s", ErrAlreadyExists, actionType.ID)
	}
	r.types[actionType.ID] = cloneActionType(actionType)
	return nil
}

func (r *MemoryRepository) GetActionType(ctx context.Context, id ActionTypeID) (ActionType, error) {
	if err := ctx.Err(); err != nil {
		return ActionType{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	actionType, exists := r.types[id]
	if !exists {
		return ActionType{}, fmt.Errorf("%w: action type %s", ErrNotFound, id)
	}
	return cloneActionType(actionType), nil
}

func (r *MemoryRepository) ListActionTypes(ctx context.Context) ([]ActionType, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ActionType, 0, len(r.types))
	for _, actionType := range r.types {
		out = append(out, cloneActionType(actionType))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (r *MemoryRepository) SaveActionRun(ctx context.Context, run ActionRun) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if run.ExecutionMode == "" {
		run.ExecutionMode = ExecutionModeApply
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.runs[run.ID]; ok && existing.TenantID != run.TenantID {
		return fmt.Errorf("%w: action run id is owned by another tenant", ErrInvalidRequest)
	}
	r.runs[run.ID] = cloneActionRun(run)
	return nil
}

func (r *MemoryRepository) GetActionRun(ctx context.Context, id ActionRunID) (ActionRun, error) {
	if err := ctx.Err(); err != nil {
		return ActionRun{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	run, exists := r.runs[id]
	if !exists {
		return ActionRun{}, fmt.Errorf("%w: action run %s", ErrNotFound, id)
	}
	return cloneActionRun(run), nil
}

func (r *MemoryRepository) ListActionRuns(ctx context.Context, query ActionRunQuery) (ActionRunPage, error) {
	if err := ctx.Err(); err != nil {
		return ActionRunPage{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var filtered []ActionRun
	for _, run := range r.runs {
		if !actionRunMatchesQuery(run, query) {
			continue
		}
		filtered = append(filtered, cloneActionRun(run))
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].CreatedAt.Equal(filtered[j].CreatedAt) {
			return filtered[i].ID < filtered[j].ID
		}
		return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
	})
	total := len(filtered)
	limit, offset := normalizePage(query.Limit, query.Offset)
	if offset > len(filtered) {
		filtered = nil
	} else {
		end := offset + limit
		if end > len(filtered) {
			end = len(filtered)
		}
		filtered = filtered[offset:end]
	}
	return ActionRunPage{
		Runs: filtered,
		Page: PageDTO{Limit: limit, Offset: offset, Total: total, Returned: len(filtered)},
	}, nil
}

func (r *MemoryRepository) SaveWritebackReceipt(ctx context.Context, receipt WritebackReceipt) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.receipts[receipt.ID]; exists {
		return fmt.Errorf("%w: writeback receipt %s", ErrAlreadyExists, receipt.ID)
	}
	r.receipts[receipt.ID] = cloneWritebackReceipt(receipt)
	return nil
}

func actionRunMatchesQuery(run ActionRun, query ActionRunQuery) bool {
	if query.TenantID != "" && run.TenantID != query.TenantID {
		return false
	}
	if query.ActionTypeID != "" && run.ActionTypeID != query.ActionTypeID {
		return false
	}
	if query.Status != "" && run.Status != query.Status {
		return false
	}
	if query.ActorID != "" && run.Actor.ID != query.ActorID {
		return false
	}
	if query.RuntimeKind != "" && run.Runtime.Kind != query.RuntimeKind {
		return false
	}
	if query.RuntimeID != "" && run.Runtime.ID != query.RuntimeID {
		return false
	}
	return true
}

func normalizePage(limit int, offset int) (int, int) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func (r *MemoryRepository) ListWritebackReceipts(ctx context.Context, runID ActionRunID) ([]WritebackReceipt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []WritebackReceipt
	for _, receipt := range r.receipts {
		if receipt.ActionRunID == runID {
			out = append(out, cloneWritebackReceipt(receipt))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func cloneActionType(src ActionType) ActionType {
	src.ObjectTypeRefs = append([]string(nil), src.ObjectTypeRefs...)
	src.InputSchema = cloneAnyMap(src.InputSchema)
	src.OutputSchema = cloneAnyMap(src.OutputSchema)
	src.HILPolicy = cloneAnyMap(src.HILPolicy)
	src.WritebackTarget = cloneAnyMap(src.WritebackTarget)
	src.IdempotencyPolicy = cloneAnyMap(src.IdempotencyPolicy)
	src.AuditPolicy = cloneAnyMap(src.AuditPolicy)
	src.FailurePolicy = cloneAnyMap(src.FailurePolicy)
	src.Semantics.Verbs = append([]string(nil), src.Semantics.Verbs...)
	src.Semantics.Roles = append([]SemanticRole(nil), src.Semantics.Roles...)
	src.Semantics.Preconditions = append([]string(nil), src.Semantics.Preconditions...)
	src.Semantics.Effects = append([]string(nil), src.Semantics.Effects...)
	src.Semantics.DecomposesInto = append([]ActionTypeID(nil), src.Semantics.DecomposesInto...)
	src.Semantics.KnowledgeTerms = append([]string(nil), src.Semantics.KnowledgeTerms...)
	src.Semantics.MemoryTerms = append([]string(nil), src.Semantics.MemoryTerms...)
	return src
}

func cloneActionRun(src ActionRun) ActionRun {
	src.ObjectRefs = append([]ObjectRef(nil), src.ObjectRefs...)
	src.RequestedFields = append([]string(nil), src.RequestedFields...)
	src.EvidenceRefs = append([]Ref(nil), src.EvidenceRefs...)
	if src.CompletedAt != nil {
		completedAt := *src.CompletedAt
		src.CompletedAt = &completedAt
	}
	return src
}

func cloneWritebackReceipt(src WritebackReceipt) WritebackReceipt {
	src.TargetResourceRefs = append([]ObjectRef(nil), src.TargetResourceRefs...)
	src.ChangedFields = append([]string(nil), src.ChangedFields...)
	src.EvidenceRefs = append([]Ref(nil), src.EvidenceRefs...)
	return src
}

func cloneAnyMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
