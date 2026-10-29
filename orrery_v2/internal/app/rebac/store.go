package rebac

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

type Store interface {
	SaveRelationTuple(ctx context.Context, tuple RelationTuple) error
	CheckRelation(ctx context.Context, check RelationCheck) (bool, RelationTuple, error)
}

type MemoryStore struct {
	mu       sync.RWMutex
	tuples   map[string]RelationTuple
	revision int64
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{tuples: map[string]RelationTuple{}}
}

func (s *MemoryStore) SaveRelationTuple(ctx context.Context, tuple RelationTuple) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tuple = normalizeRelationTuple(tuple)
	if err := validateRelationTuple(tuple); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, found := s.tuples[tuple.ID]; found && relationTupleAuthorizationEqual(existing, tuple) {
		return nil
	}
	s.tuples[tuple.ID] = cloneRelationTuple(tuple)
	s.revision++
	return nil
}

func (s *MemoryStore) DeleteRelationTuple(ctx context.Context, tuple RelationTuple) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tuple = normalizeRelationTuple(tuple)
	if err := validateRelationTuple(tuple); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, found := s.tuples[tuple.ID]; !found {
		return nil
	}
	delete(s.tuples, tuple.ID)
	s.revision++
	return nil
}

func relationTupleAuthorizationEqual(left, right RelationTuple) bool {
	left = normalizeRelationTuple(left)
	right = normalizeRelationTuple(right)
	return left.ID == right.ID && left.TenantID == right.TenantID &&
		left.Subject == right.Subject && left.Relation == right.Relation &&
		left.Object == right.Object && left.Source == right.Source &&
		reflect.DeepEqual(left.ValidFrom, right.ValidFrom) && reflect.DeepEqual(left.ValidTo, right.ValidTo)
}

func (s *MemoryStore) ListRelationTuples(ctx context.Context) ([]RelationTuple, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("rebac memory store is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]RelationTuple, 0, len(s.tuples))
	for _, tuple := range s.tuples {
		out = append(out, cloneRelationTuple(tuple))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *MemoryStore) ListRelationTuplesByTenant(ctx context.Context, tenantID string) ([]RelationTuple, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("rebac memory store is required")
	}
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return nil, fmt.Errorf("relation tuple tenant_id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]RelationTuple, 0)
	for _, tuple := range s.tuples {
		if tuple.TenantID == tenantID {
			out = append(out, cloneRelationTuple(tuple))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *MemoryStore) ListRelationTuplesByTenants(ctx context.Context, tenantIDs []string) ([]RelationTuple, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("rebac memory store is required")
	}
	wanted := make(map[string]struct{}, len(tenantIDs))
	for _, tenantID := range tenantIDs {
		tenantID = strings.TrimSpace(tenantID)
		if tenantID == "" {
			return nil, fmt.Errorf("relation tuple tenant_id is required")
		}
		wanted[tenantID] = struct{}{}
	}
	if len(wanted) == 0 {
		return []RelationTuple{}, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]RelationTuple, 0)
	for _, tuple := range s.tuples {
		if _, ok := wanted[tuple.TenantID]; ok {
			out = append(out, cloneRelationTuple(tuple))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TenantID != out[j].TenantID {
			return out[i].TenantID < out[j].TenantID
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (s *MemoryStore) AuthorizationRevision(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if s == nil {
		return 0, fmt.Errorf("rebac memory store is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.revision, nil
}

func (s *MemoryStore) CheckRelation(ctx context.Context, check RelationCheck) (bool, RelationTuple, error) {
	if err := ctx.Err(); err != nil {
		return false, RelationTuple{}, err
	}
	check = normalizeRelationCheck(check)
	if err := validateRelationCheck(check); err != nil {
		return false, RelationTuple{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	matches := make([]RelationTuple, 0, 1)
	for _, tuple := range s.tuples {
		if relationTupleMatches(tuple, check) {
			matches = append(matches, cloneRelationTuple(tuple))
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].CreatedAt.After(matches[j].CreatedAt)
	})
	if len(matches) == 0 {
		return false, RelationTuple{}, nil
	}
	return true, matches[0], nil
}

func relationTupleMatches(tuple RelationTuple, check RelationCheck) bool {
	if tuple.TenantID != check.TenantID {
		return false
	}
	if tuple.Subject != check.Subject || tuple.Relation != check.Relation || tuple.Object != check.Object {
		return false
	}
	return relationTupleValidAt(tuple, check.At)
}

func relationTupleValidAt(tuple RelationTuple, at time.Time) bool {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if tuple.ValidFrom != nil && at.Before(tuple.ValidFrom.UTC()) {
		return false
	}
	if tuple.ValidTo != nil && !at.Before(tuple.ValidTo.UTC()) {
		return false
	}
	return true
}

func normalizeRelationTuple(tuple RelationTuple) RelationTuple {
	tuple.ID = strings.TrimSpace(tuple.ID)
	tuple.TenantID = strings.TrimSpace(tuple.TenantID)
	tuple.Subject = normalizeRef(tuple.Subject)
	tuple.Relation = strings.ToLower(strings.TrimSpace(tuple.Relation))
	tuple.Object = normalizeRef(tuple.Object)
	tuple.Source = normalizeRef(tuple.Source)
	if !tuple.CreatedAt.IsZero() {
		tuple.CreatedAt = tuple.CreatedAt.UTC()
	}
	if tuple.ValidFrom != nil {
		validFrom := tuple.ValidFrom.UTC()
		tuple.ValidFrom = &validFrom
	}
	if tuple.ValidTo != nil {
		validTo := tuple.ValidTo.UTC()
		tuple.ValidTo = &validTo
	}
	return tuple
}

func normalizeRelationCheck(check RelationCheck) RelationCheck {
	check.TenantID = strings.TrimSpace(check.TenantID)
	check.Subject = normalizeRef(check.Subject)
	check.Relation = strings.ToLower(strings.TrimSpace(check.Relation))
	check.Object = normalizeRef(check.Object)
	if !check.At.IsZero() {
		check.At = check.At.UTC()
	}
	return check
}

func normalizeRef(ref Ref) Ref {
	ref.Kind = strings.ToLower(strings.TrimSpace(ref.Kind))
	ref.ID = strings.TrimSpace(ref.ID)
	return ref
}

func validateRelationTuple(tuple RelationTuple) error {
	if tuple.ID == "" {
		return fmt.Errorf("relation tuple id is required")
	}
	if tuple.TenantID == "" {
		return fmt.Errorf("relation tuple tenant_id is required")
	}
	if tuple.Subject.Kind == "" || tuple.Subject.ID == "" {
		return fmt.Errorf("relation tuple subject is required")
	}
	if tuple.Relation == "" {
		return fmt.Errorf("relation tuple relation is required")
	}
	if tuple.Object.Kind == "" || tuple.Object.ID == "" {
		return fmt.Errorf("relation tuple object is required")
	}
	return nil
}

func validateRelationCheck(check RelationCheck) error {
	if check.TenantID == "" {
		return fmt.Errorf("relation check tenant_id is required")
	}
	if check.Subject.Kind == "" || check.Subject.ID == "" {
		return fmt.Errorf("relation check subject is required")
	}
	if check.Relation == "" {
		return fmt.Errorf("relation check relation is required")
	}
	if check.Object.Kind == "" || check.Object.ID == "" {
		return fmt.Errorf("relation check object is required")
	}
	return nil
}

func cloneRelationTuple(src RelationTuple) RelationTuple {
	if src.ValidFrom != nil {
		validFrom := *src.ValidFrom
		src.ValidFrom = &validFrom
	}
	if src.ValidTo != nil {
		validTo := *src.ValidTo
		src.ValidTo = &validTo
	}
	return src
}

var _ Store = (*MemoryStore)(nil)
