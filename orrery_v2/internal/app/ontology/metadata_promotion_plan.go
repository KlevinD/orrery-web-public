package ontology

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type MetadataPromotionPlanState string

const (
	MetadataPromotionPlanActive      MetadataPromotionPlanState = "active"
	MetadataPromotionPlanConsumed    MetadataPromotionPlanState = "consumed"
	MetadataPromotionPlanInvalidated MetadataPromotionPlanState = "invalidated"
	MetadataPromotionPlanExpired     MetadataPromotionPlanState = "expired"
)

type MetadataPromotionPlanTarget struct {
	Kind               string `json:"kind"`
	ID                 string `json:"id"`
	DefinitionTenantID string `json:"definition_tenant_id,omitempty"`
	Version            int    `json:"version"`
	ContentHash        string `json:"content_hash"`
}

type MetadataPromotionPlanRecord struct {
	PlanID                string                            `json:"plan_id"`
	PrincipalKey          string                            `json:"principal_key"`
	TenantID              string                            `json:"tenant_id"`
	BindingVersion        int64                             `json:"binding_version"`
	ProposalID            string                            `json:"proposal_id"`
	ProposalRevision      int64                             `json:"proposal_revision"`
	ProposalContentHash   string                            `json:"proposal_content_hash"`
	Targets               []MetadataPromotionPlanTarget     `json:"targets"`
	DiffHash              string                            `json:"diff_hash"`
	Diff                  []MetadataProposalPromotionChange `json:"diff"`
	AuthorizationRevision string                            `json:"authorization_revision"`
	PolicyRevision        string                            `json:"policy_revision"`
	ExpiresAt             time.Time                         `json:"expires_at"`
	State                 MetadataPromotionPlanState        `json:"state"`
	IdempotencyKey        string                            `json:"idempotency_key,omitempty"`
	ReceiptID             string                            `json:"receipt_id,omitempty"`
	InvalidationReason    string                            `json:"invalidation_reason,omitempty"`
	CreatedAt             time.Time                         `json:"created_at"`
	UpdatedAt             time.Time                         `json:"updated_at"`
}

type MetadataPromotionPlanTransition struct {
	PlanID             string
	ExpectedState      MetadataPromotionPlanState
	State              MetadataPromotionPlanState
	IdempotencyKey     string
	ReceiptID          string
	InvalidationReason string
}

func NewMetadataPromotionPlanID() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate metadata promotion plan id: %w", err)
	}
	return "opplan_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func CanonicalMetadataPromotionPlanDiff(changes []MetadataProposalPromotionChange) ([]MetadataProposalPromotionChange, string, error) {
	canonical := append([]MetadataProposalPromotionChange(nil), changes...)
	seen := make(map[string]struct{}, len(canonical))
	for index := range canonical {
		canonical[index].TargetKind = normalizeID(canonical[index].TargetKind)
		canonical[index].TargetID = normalizeID(canonical[index].TargetID)
		canonical[index].Field = normalizeID(canonical[index].Field)
		canonical[index].CurrentValue = strings.TrimSpace(canonical[index].CurrentValue)
		canonical[index].ProposedValue = strings.TrimSpace(canonical[index].ProposedValue)
		if canonical[index].TargetKind == "" || canonical[index].TargetID == "" || canonical[index].Field == "" {
			return nil, "", fmt.Errorf("%w: promotion diff target kind, id, and field are required", ErrInvalidDefinition)
		}
		switch canonical[index].Operation {
		case PromotionOperationCreate, PromotionOperationAppend, PromotionOperationUpdate, PromotionOperationNoop:
		default:
			return nil, "", fmt.Errorf("%w: unsupported promotion diff operation %s", ErrInvalidDefinition, canonical[index].Operation)
		}
		key := strings.Join([]string{canonical[index].TargetKind, canonical[index].TargetID, canonical[index].Field}, "\x00")
		if _, exists := seen[key]; exists {
			return nil, "", fmt.Errorf("%w: duplicate promotion diff target %s/%s/%s", ErrInvalidDefinition, canonical[index].TargetKind, canonical[index].TargetID, canonical[index].Field)
		}
		seen[key] = struct{}{}
	}
	sort.SliceStable(canonical, func(i, j int) bool {
		left := canonical[i]
		right := canonical[j]
		if left.TargetKind != right.TargetKind {
			return left.TargetKind < right.TargetKind
		}
		if left.TargetID != right.TargetID {
			return left.TargetID < right.TargetID
		}
		if left.Field != right.Field {
			return left.Field < right.Field
		}
		return left.Operation < right.Operation
	})
	if len(canonical) == 0 {
		return nil, "", fmt.Errorf("%w: promotion diff is required", ErrInvalidDefinition)
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return nil, "", fmt.Errorf("%w: promotion diff cannot be encoded", ErrInvalidDefinition)
	}
	return canonical, secureMetadataPromotionHash(raw), nil
}

func CanonicalMetadataPromotionPlanTargets(targets []MetadataPromotionPlanTarget) ([]MetadataPromotionPlanTarget, error) {
	canonical := append([]MetadataPromotionPlanTarget(nil), targets...)
	seen := make(map[string]struct{}, len(canonical))
	for index := range canonical {
		canonical[index].Kind = normalizeID(canonical[index].Kind)
		canonical[index].ID = normalizeID(canonical[index].ID)
		canonical[index].DefinitionTenantID = strings.TrimSpace(canonical[index].DefinitionTenantID)
		canonical[index].ContentHash = strings.TrimSpace(canonical[index].ContentHash)
		if canonical[index].Kind == "" || canonical[index].ID == "" || canonical[index].Version < 0 || canonical[index].ContentHash == "" {
			return nil, fmt.Errorf("%w: promotion target kind, id, non-negative version, and content hash are required", ErrInvalidDefinition)
		}
		switch canonical[index].Kind {
		case "object_type", "link_type", "property_type", "mapping_rule":
		default:
			return nil, fmt.Errorf("%w: unsupported promotion target kind %s", ErrInvalidDefinition, canonical[index].Kind)
		}
		if canonical[index].Version == 0 && canonical[index].ContentHash != "absent" {
			return nil, fmt.Errorf("%w: absent promotion target must use the absent content hash", ErrInvalidDefinition)
		}
		if canonical[index].Version > 0 && !isSecureMetadataPromotionHash(canonical[index].ContentHash) {
			return nil, fmt.Errorf("%w: persisted promotion target content hash is invalid", ErrInvalidDefinition)
		}
		key := strings.Join([]string{canonical[index].Kind, canonical[index].ID}, "\x00")
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("%w: duplicate promotion target %s/%s", ErrInvalidDefinition, canonical[index].Kind, canonical[index].ID)
		}
		seen[key] = struct{}{}
	}
	sort.SliceStable(canonical, func(i, j int) bool {
		if canonical[i].Kind != canonical[j].Kind {
			return canonical[i].Kind < canonical[j].Kind
		}
		if canonical[i].ID != canonical[j].ID {
			return canonical[i].ID < canonical[j].ID
		}
		return canonical[i].DefinitionTenantID < canonical[j].DefinitionTenantID
	})
	if len(canonical) == 0 {
		return nil, fmt.Errorf("%w: at least one promotion target is required", ErrInvalidDefinition)
	}
	return canonical, nil
}

func normalizeMetadataPromotionPlan(record MetadataPromotionPlanRecord, now func() time.Time) (MetadataPromotionPlanRecord, error) {
	record.PlanID = strings.TrimSpace(record.PlanID)
	record.PrincipalKey = strings.TrimSpace(record.PrincipalKey)
	record.TenantID = strings.TrimSpace(record.TenantID)
	record.ProposalID = strings.TrimSpace(record.ProposalID)
	record.ProposalContentHash = strings.TrimSpace(record.ProposalContentHash)
	record.AuthorizationRevision = strings.TrimSpace(record.AuthorizationRevision)
	record.PolicyRevision = strings.TrimSpace(record.PolicyRevision)
	record.DiffHash = strings.TrimSpace(record.DiffHash)
	record.IdempotencyKey = strings.TrimSpace(record.IdempotencyKey)
	record.ReceiptID = strings.TrimSpace(record.ReceiptID)
	record.InvalidationReason = strings.TrimSpace(record.InvalidationReason)
	if record.State == "" {
		record.State = MetadataPromotionPlanActive
	}
	if record.PlanID == "" || len(record.PlanID) > 256 || record.PrincipalKey == "" || len(record.PrincipalKey) > 256 ||
		record.TenantID == "" || len(record.TenantID) > 256 || record.BindingVersion <= 0 || record.ProposalID == "" || record.AuthorizationRevision == "" ||
		record.ProposalRevision <= 0 || !isSecureMetadataPromotionHash(record.ProposalContentHash) || record.PolicyRevision == "" {
		return MetadataPromotionPlanRecord{}, fmt.Errorf("%w: promotion plan identity, binding, proposal, and policy revision are required", ErrInvalidDefinition)
	}
	canonicalDiff, diffHash, err := CanonicalMetadataPromotionPlanDiff(record.Diff)
	if err != nil {
		return MetadataPromotionPlanRecord{}, err
	}
	if record.DiffHash != "" && record.DiffHash != diffHash {
		return MetadataPromotionPlanRecord{}, fmt.Errorf("%w: promotion plan diff hash does not match the canonical diff", ErrInvalidDefinition)
	}
	record.Diff = canonicalDiff
	record.DiffHash = diffHash
	record.Targets, err = CanonicalMetadataPromotionPlanTargets(record.Targets)
	if err != nil {
		return MetadataPromotionPlanRecord{}, err
	}
	targets := make(map[string]bool, len(record.Targets))
	for _, target := range record.Targets {
		targets[strings.Join([]string{target.Kind, target.ID}, "\x00")] = false
	}
	for _, change := range record.Diff {
		key := strings.Join([]string{change.TargetKind, change.TargetID}, "\x00")
		if _, exists := targets[key]; !exists {
			return MetadataPromotionPlanRecord{}, fmt.Errorf("%w: promotion diff target %s/%s has no persisted authorization target", ErrInvalidDefinition, change.TargetKind, change.TargetID)
		}
		targets[key] = true
	}
	for key, referenced := range targets {
		if !referenced {
			return MetadataPromotionPlanRecord{}, fmt.Errorf("%w: persisted promotion target %q is not present in the canonical diff", ErrInvalidDefinition, key)
		}
	}
	if now == nil {
		now = time.Now
	}
	at := now().UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = at
	} else {
		record.CreatedAt = record.CreatedAt.UTC()
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = record.CreatedAt
	} else {
		record.UpdatedAt = record.UpdatedAt.UTC()
	}
	if record.ExpiresAt.IsZero() || !record.ExpiresAt.After(record.CreatedAt) {
		return MetadataPromotionPlanRecord{}, fmt.Errorf("%w: promotion plan expiry must be after creation", ErrInvalidDefinition)
	}
	record.ExpiresAt = record.ExpiresAt.UTC()
	switch record.State {
	case MetadataPromotionPlanActive:
		if record.IdempotencyKey != "" || record.ReceiptID != "" || record.InvalidationReason != "" {
			return MetadataPromotionPlanRecord{}, fmt.Errorf("%w: active promotion plan cannot have terminal metadata", ErrInvalidDefinition)
		}
	case MetadataPromotionPlanConsumed:
		if record.IdempotencyKey == "" || record.ReceiptID == "" || record.InvalidationReason != "" {
			return MetadataPromotionPlanRecord{}, fmt.Errorf("%w: consumed promotion plan requires idempotency key and receipt", ErrInvalidDefinition)
		}
	case MetadataPromotionPlanInvalidated, MetadataPromotionPlanExpired:
		if record.InvalidationReason == "" || record.IdempotencyKey != "" || record.ReceiptID != "" {
			return MetadataPromotionPlanRecord{}, fmt.Errorf("%w: invalidated or expired promotion plan requires a reason only", ErrInvalidDefinition)
		}
	default:
		return MetadataPromotionPlanRecord{}, fmt.Errorf("%w: unsupported promotion plan state %s", ErrInvalidDefinition, record.State)
	}
	return record, nil
}

func normalizeMetadataPromotionPlanTransition(transition MetadataPromotionPlanTransition) (MetadataPromotionPlanTransition, error) {
	transition.PlanID = strings.TrimSpace(transition.PlanID)
	transition.IdempotencyKey = strings.TrimSpace(transition.IdempotencyKey)
	transition.ReceiptID = strings.TrimSpace(transition.ReceiptID)
	transition.InvalidationReason = strings.TrimSpace(transition.InvalidationReason)
	if transition.PlanID == "" || transition.ExpectedState != MetadataPromotionPlanActive {
		return MetadataPromotionPlanTransition{}, fmt.Errorf("%w: active promotion plan transition is required", ErrInvalidDefinition)
	}
	switch transition.State {
	case MetadataPromotionPlanConsumed:
		if transition.IdempotencyKey == "" || transition.ReceiptID == "" || transition.InvalidationReason != "" {
			return MetadataPromotionPlanTransition{}, fmt.Errorf("%w: consumed promotion plan transition requires idempotency key and receipt", ErrInvalidDefinition)
		}
	case MetadataPromotionPlanInvalidated, MetadataPromotionPlanExpired:
		if transition.InvalidationReason == "" || transition.IdempotencyKey != "" || transition.ReceiptID != "" {
			return MetadataPromotionPlanTransition{}, fmt.Errorf("%w: invalidated or expired promotion plan transition requires a reason only", ErrInvalidDefinition)
		}
	default:
		return MetadataPromotionPlanTransition{}, fmt.Errorf("%w: unsupported promotion plan transition state %s", ErrInvalidDefinition, transition.State)
	}
	return transition, nil
}

func cloneMetadataPromotionPlan(record MetadataPromotionPlanRecord) MetadataPromotionPlanRecord {
	record.Targets = append([]MetadataPromotionPlanTarget(nil), record.Targets...)
	record.Diff = append([]MetadataProposalPromotionChange(nil), record.Diff...)
	return record
}

func secureMetadataPromotionHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func isSecureMetadataPromotionHash(value string) bool {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}
