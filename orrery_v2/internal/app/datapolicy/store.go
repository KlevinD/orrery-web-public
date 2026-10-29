package datapolicy

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/app/contextgraph"
)

type Store interface {
	contextgraph.PolicyProvider
	SaveFieldPolicy(ctx context.Context, policy FieldPolicy) error
	SavePurposePolicy(ctx context.Context, policy PurposePolicy) error
}

type MemoryStore struct {
	mu       sync.RWMutex
	fields   map[string]FieldPolicy
	purposes map[string]PurposePolicy
	revision int64
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		fields:   map[string]FieldPolicy{},
		purposes: map[string]PurposePolicy{},
	}
}

func (s *MemoryStore) SaveFieldPolicy(ctx context.Context, policy FieldPolicy) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	policy = normalizeFieldPolicy(policy)
	if err := validateFieldPolicy(policy); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fields[policy.ID] = policy
	s.revision++
	return nil
}

func (s *MemoryStore) SavePurposePolicy(ctx context.Context, policy PurposePolicy) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	policy = normalizePurposePolicy(policy)
	if err := validatePurposePolicy(policy); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purposes[policy.ID] = policy
	s.revision++
	return nil
}

func (s *MemoryStore) PolicyRevision(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if s == nil {
		return 0, fmt.Errorf("data policy memory store is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.revision, nil
}

func (s *MemoryStore) DecideContext(ctx context.Context, req contextgraph.BuildContextBundleRequest, objects []contextgraph.Object) (contextgraph.PermissionDecision, error) {
	if err := ctx.Err(); err != nil {
		return contextgraph.PermissionDecision{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	fields := make([]FieldPolicy, 0, len(s.fields))
	for _, policy := range s.fields {
		fields = append(fields, policy)
	}
	purposes := make([]PurposePolicy, 0, len(s.purposes))
	for _, policy := range s.purposes {
		purposes = append(purposes, policy)
	}
	return decideContext(req, objects, fields, purposes), nil
}

func decideContext(req contextgraph.BuildContextBundleRequest, objects []contextgraph.Object, fieldPolicies []FieldPolicy, purposePolicies []PurposePolicy) contextgraph.PermissionDecision {
	req.TenantID = strings.TrimSpace(req.TenantID)
	req.Consumer = strings.TrimSpace(req.Consumer)
	req.Purpose = strings.TrimSpace(req.Purpose)
	decision := contextgraph.PermissionDecision{MaskedFields: map[string][]string{}}
	for _, object := range objects {
		objectType := strings.ToLower(strings.TrimSpace(object.ObjectType))
		for _, policy := range purposePolicies {
			if !purposePolicyMatches(policy, req, objectType) {
				continue
			}
			if policy.Effect == EffectDeny {
				decision.HiddenObjectIDs = append(decision.HiddenObjectIDs, object.ID)
				decision.Constraints = append(decision.Constraints, contextgraph.PermissionConstraint{
					Scope:     policy.Purpose,
					ObjectRef: contextgraph.Ref{Kind: object.ObjectType, ID: object.ID},
					Reason:    firstNonEmpty(policy.Reason, "purpose policy denied object"),
				})
			}
		}
		for _, policy := range fieldPolicies {
			if !fieldPolicyMatches(policy, req, objectType) {
				continue
			}
			if policy.Effect == EffectMask {
				decision.MaskedFields[object.ID] = append(decision.MaskedFields[object.ID], policy.FieldPath)
				decision.Constraints = append(decision.Constraints, contextgraph.PermissionConstraint{
					Scope:        policy.Purpose,
					ObjectRef:    contextgraph.Ref{Kind: object.ObjectType, ID: object.ID},
					MaskedFields: []string{policy.FieldPath},
					Reason:       firstNonEmpty(policy.Reason, "field policy masked field"),
				})
			}
		}
	}
	sort.Strings(decision.HiddenObjectIDs)
	for objectID := range decision.MaskedFields {
		sort.Strings(decision.MaskedFields[objectID])
	}
	sort.Slice(decision.Constraints, func(i, j int) bool {
		left := decision.Constraints[i].ObjectRef.ID + ":" + strings.Join(decision.Constraints[i].MaskedFields, ",") + ":" + decision.Constraints[i].Reason
		right := decision.Constraints[j].ObjectRef.ID + ":" + strings.Join(decision.Constraints[j].MaskedFields, ",") + ":" + decision.Constraints[j].Reason
		return left < right
	})
	return decision
}

func fieldPolicyMatches(policy FieldPolicy, req contextgraph.BuildContextBundleRequest, objectType string) bool {
	return policy.Enabled &&
		policy.TenantID == req.TenantID &&
		policy.Consumer == req.Consumer &&
		policy.Purpose == req.Purpose &&
		policy.ObjectType == objectType
}

func purposePolicyMatches(policy PurposePolicy, req contextgraph.BuildContextBundleRequest, objectType string) bool {
	return policy.Enabled &&
		policy.TenantID == req.TenantID &&
		policy.Consumer == req.Consumer &&
		policy.Purpose == req.Purpose &&
		policy.ObjectType == objectType
}

func normalizeFieldPolicy(policy FieldPolicy) FieldPolicy {
	policy.ID = strings.TrimSpace(policy.ID)
	policy.TenantID = strings.TrimSpace(policy.TenantID)
	policy.Consumer = strings.TrimSpace(policy.Consumer)
	policy.Purpose = strings.TrimSpace(policy.Purpose)
	policy.ObjectType = strings.ToLower(strings.TrimSpace(policy.ObjectType))
	policy.FieldPath = strings.TrimSpace(policy.FieldPath)
	policy.Effect = strings.ToLower(strings.TrimSpace(policy.Effect))
	policy.Reason = strings.TrimSpace(policy.Reason)
	policy.CreatedAt = normalizeTime(policy.CreatedAt)
	policy.UpdatedAt = normalizeTime(policy.UpdatedAt)
	return policy
}

func normalizePurposePolicy(policy PurposePolicy) PurposePolicy {
	policy.ID = strings.TrimSpace(policy.ID)
	policy.TenantID = strings.TrimSpace(policy.TenantID)
	policy.Consumer = strings.TrimSpace(policy.Consumer)
	policy.Purpose = strings.TrimSpace(policy.Purpose)
	policy.ObjectType = strings.ToLower(strings.TrimSpace(policy.ObjectType))
	policy.Effect = strings.ToLower(strings.TrimSpace(policy.Effect))
	policy.Reason = strings.TrimSpace(policy.Reason)
	policy.CreatedAt = normalizeTime(policy.CreatedAt)
	policy.UpdatedAt = normalizeTime(policy.UpdatedAt)
	return policy
}

func validateFieldPolicy(policy FieldPolicy) error {
	if policy.ID == "" || policy.TenantID == "" || policy.Consumer == "" || policy.Purpose == "" || policy.ObjectType == "" || policy.FieldPath == "" {
		return fmt.Errorf("field policy id, tenant_id, consumer, purpose, object_type, and field_path are required")
	}
	if policy.Effect != EffectMask && policy.Effect != EffectDeny && policy.Effect != EffectAllow {
		return fmt.Errorf("field policy effect is invalid")
	}
	return nil
}

func validatePurposePolicy(policy PurposePolicy) error {
	if policy.ID == "" || policy.TenantID == "" || policy.Consumer == "" || policy.Purpose == "" || policy.ObjectType == "" {
		return fmt.Errorf("purpose policy id, tenant_id, consumer, purpose, and object_type are required")
	}
	if policy.Effect != EffectDeny && policy.Effect != EffectAllow {
		return fmt.Errorf("purpose policy effect is invalid")
	}
	return nil
}

func normalizeTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	return t.UTC()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

var _ Store = (*MemoryStore)(nil)
