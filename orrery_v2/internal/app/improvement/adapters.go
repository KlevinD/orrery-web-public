package improvement

import (
	"context"
	"fmt"
)

// GovernedAction is passed to the owning domain, which must resolve and consume
// the action authorization. Mere possession of this structure grants nothing.
type GovernedAction struct {
	TenantID      string
	CaseID        string
	ChangeSetHash string
	EvaluationID  string
	ActionRunID   string
	DecisionID    string
	ArgsHash      string
}

// DomainAdapter changes actual runtime resources. Implementations must verify
// current versions, tenant authorization, idempotency and completed receipts.
// Cross-domain publication needs a durable coordinator; this registry alone
// does not provide a transaction across tools, code releases and indexes.
type DomainAdapter interface {
	Kind() TargetKind
	Inspect(context.Context, string, string) (ResourceVersion, error)
	ValidateCandidate(context.Context, string, TargetChange) error
	Apply(context.Context, GovernedAction, TargetChange) (TargetReceipt, error)
	Rollback(context.Context, GovernedAction, TargetChange) (TargetReceipt, error)
}

type AdapterRegistry struct{ adapters map[TargetKind]DomainAdapter }

func NewAdapterRegistry(adapters ...DomainAdapter) (*AdapterRegistry, error) {
	r := &AdapterRegistry{adapters: map[TargetKind]DomainAdapter{}}
	for _, adapter := range adapters {
		if adapter == nil || !kindOK(adapter.Kind()) {
			return nil, ErrInvalid
		}
		if _, exists := r.adapters[adapter.Kind()]; exists {
			return nil, ErrConflict
		}
		r.adapters[adapter.Kind()] = adapter
	}
	return r, nil
}

func (r *AdapterRegistry) Resolve(kind TargetKind) (DomainAdapter, error) {
	if r != nil {
		if adapter := r.adapters[kind]; adapter != nil {
			return adapter, nil
		}
	}
	return nil, fmt.Errorf("%w: no registered %s adapter", ErrUnavailable, kind)
}

type Capability struct {
	Kind      TargetKind `json:"kind"`
	Available bool       `json:"available"`
}

func (r *AdapterRegistry) Capabilities() []Capability {
	out := make([]Capability, 0, len(TargetKinds()))
	for _, kind := range TargetKinds() {
		_, err := r.Resolve(kind)
		out = append(out, Capability{Kind: kind, Available: err == nil})
	}
	return out
}
