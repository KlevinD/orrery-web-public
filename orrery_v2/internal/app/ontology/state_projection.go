package ontology

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrStateProjectionConflict = errors.New("ontology state projection conflict")

type StateProjectionOutcome string

const (
	StateProjectionChanged      StateProjectionOutcome = "changed"
	StateProjectionSnapshotOnly StateProjectionOutcome = "snapshot_only"
	StateProjectionNoop         StateProjectionOutcome = "noop"
	StateProjectionStale        StateProjectionOutcome = "stale"
)

type StateProjectionInput struct {
	TenantID        string
	EventType       string
	EntityID        string
	Payload         map[string]any
	SourceVersion   int64
	SourceUpdatedAt time.Time
	SnapshotAt      time.Time
	// AllowEqualOrderRepair is reserved for authoritative reconciliation from
	// the current source of truth. Live event projection must leave it false.
	AllowEqualOrderRepair bool
}

type CanonicalStateProjectionInput struct {
	StateProjectionInput
	PayloadJSON  string
	SemanticHash string
	SnapshotDate string
}

type StateProjectionReport struct {
	Outcome      StateProjectionOutcome
	SemanticHash string
	SnapshotDate string
}

func CanonicalStateProjection(input StateProjectionInput) (CanonicalStateProjectionInput, error) {
	input.TenantID = strings.TrimSpace(input.TenantID)
	input.EventType = strings.TrimSpace(input.EventType)
	input.EntityID = strings.TrimSpace(input.EntityID)
	if input.TenantID == "" || input.EventType == "" || input.EntityID == "" {
		return CanonicalStateProjectionInput{}, fmt.Errorf("ontology state tenant, event type, and entity are required")
	}
	if len(input.Payload) == 0 {
		return CanonicalStateProjectionInput{}, fmt.Errorf("ontology state payload is required")
	}
	if input.SourceVersion < 0 {
		return CanonicalStateProjectionInput{}, fmt.Errorf("ontology state source version cannot be negative")
	}
	if input.SnapshotAt.IsZero() {
		return CanonicalStateProjectionInput{}, fmt.Errorf("ontology state snapshot time is required")
	}
	if input.SourceVersion == 0 && input.SourceUpdatedAt.IsZero() {
		return CanonicalStateProjectionInput{}, fmt.Errorf("unversioned ontology state requires source update time")
	}
	payload, err := json.Marshal(input.Payload)
	if err != nil {
		return CanonicalStateProjectionInput{}, fmt.Errorf("encode ontology state payload: %w", err)
	}
	digest := sha256.Sum256(payload)
	input.SourceUpdatedAt = input.SourceUpdatedAt.UTC()
	input.SnapshotAt = input.SnapshotAt.UTC()
	return CanonicalStateProjectionInput{
		StateProjectionInput: input,
		PayloadJSON:          string(payload),
		SemanticHash:         hex.EncodeToString(digest[:]),
		SnapshotDate:         input.SnapshotAt.Format("2006-01-02"),
	}, nil
}
