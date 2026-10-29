package observability

import (
	"context"
	"fmt"

	"orrery_v2/internal/knowledge/vector"
)

type HealthDeps struct {
	MetadataReady func(context.Context) error
	RootReady     func(context.Context) error
	ActiveBases   func(context.Context) (int64, error)
	VectorReady   func(context.Context) error
}

type HealthProbe struct {
	deps HealthDeps
}

type HealthStatus struct {
	OK      bool
	Message string
}

func NewHealthProbe(deps HealthDeps) *HealthProbe {
	return &HealthProbe{deps: deps}
}

func (p *HealthProbe) Check(ctx context.Context) HealthStatus {
	if p == nil {
		return HealthStatus{OK: false, Message: "knowledge health probe is not configured"}
	}
	if p.deps.MetadataReady != nil {
		if err := p.deps.MetadataReady(ctx); err != nil {
			return HealthStatus{OK: false, Message: "knowledge metadata unavailable"}
		}
	}
	if p.deps.RootReady != nil {
		if err := p.deps.RootReady(ctx); err != nil {
			return HealthStatus{OK: false, Message: "knowledge root unavailable"}
		}
	}
	if p.deps.ActiveBases != nil {
		count, err := p.deps.ActiveBases(ctx)
		if err != nil {
			return HealthStatus{OK: false, Message: "knowledge metadata unavailable"}
		}
		if count == 0 {
			return HealthStatus{OK: true, Message: "knowledge disabled"}
		}
	}
	if p.deps.VectorReady != nil {
		if err := p.deps.VectorReady(ctx); err != nil {
			return HealthStatus{OK: false, Message: "knowledge vector unavailable"}
		}
	}
	return HealthStatus{OK: true, Message: "knowledge ready"}
}

type VectorProbeStore interface {
	EnsureKnowledgeBase(context.Context, vector.Descriptor) error
	DropKnowledgeBase(context.Context, vector.Descriptor) error
}

func CheckVectorAdapter(ctx context.Context, store VectorProbeStore, descriptor vector.Descriptor) error {
	if store == nil {
		return fmt.Errorf("knowledge vector store is not configured")
	}
	if err := store.EnsureKnowledgeBase(ctx, descriptor); err != nil {
		return err
	}
	return store.DropKnowledgeBase(ctx, descriptor)
}
