package improvement

import (
	"context"
	"time"

	"orrery_v2/internal/platform/graphprotocol"
)

// RuntimeDependenciesForCase loads the original immutable terminal, rather
// than accepting a model-supplied receipt or reconstructing from live bindings.
func (s *Store) RuntimeDependenciesForCase(ctx context.Context, c Case) (*graphprotocol.DomainEvent, error) {
	r, err := s.RuntimeReviewForCase(ctx, c)
	if err != nil {
		return nil, err
	}
	end, err := s.RuntimeEvidence().GetTerminal(ctx, c.TenantID, r.AttemptID)
	if err != nil {
		return nil, err
	}
	if end.ID != r.TerminalID {
		return nil, ErrInvalid
	}
	d := end.Dependencies
	if d == nil {
		return nil, nil
	}
	models, tools := 0, 0
	for _, entry := range d.Entries {
		if entry.Kind == "model_dispatch" {
			models++
		}
		if entry.Kind == "tool_dispatch" {
			tools++
		}
	}
	event := graphprotocol.DomainEvent{
		ID: "runtime-dependencies-projection:" + d.ID, TenantID: c.TenantID,
		Type: "improvement.runtime_dependencies.upserted", StreamID: d.ID, Version: 1, OccurredAt: d.CapturedAt,
		PublishedBy: graphprotocol.ActorRef{Kind: "system", ID: "runtime-dependency-observer"}, SourceAccessRef: c.Observation.SourceAccessRef,
		Payload: map[string]any{"id": d.ID, "case_id": c.ID, "runtime_review_id": r.ID, "content_hash": Digest(d),
			"model_dispatches": models, "tool_dispatches": tools, "dropped": d.Dropped, "invalid": d.Invalid,
			"runner_returned": d.RunnerReturned, "baseline_ready": false, "captured_at": d.CapturedAt.Format(time.RFC3339Nano)},
	}
	return &event, nil
}
