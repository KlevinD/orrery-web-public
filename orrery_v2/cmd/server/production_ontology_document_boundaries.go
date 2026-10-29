package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"orrery_v2/internal/app/knowledgeinstances"
	"orrery_v2/internal/app/ontologyreasoning"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/outbox"
)

func (w *productionOntologyDocumentInference) ProcessBoundary(ctx context.Context, job outbox.Job) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var input knowledgeinstances.BoundaryJob
	if err := json.Unmarshal(job.Payload, &input); err != nil {
		return err
	}
	if job.ID == "" || input.TenantID == "" || input.WatchID == "" || input.Version < 1 || input.PlanHash == "" || input.DueAt <= 0 {
		return knowledgeinstances.ErrInvalid
	}
	watch, err := w.watches.Get(ctx, input.TenantID, input.WatchID)
	if errors.Is(err, knowledgeinstances.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !watch.Active || watch.Version != input.Version {
		return nil
	}
	current, err := w.current(ctx, watch)
	if err != nil {
		return err
	}
	if current.source.Stamp() != input.SourceStamp {
		return nil
	}
	stamp, err := w.api.lifecycle.InputStamp(ctx, input.TenantID)
	if err != nil {
		return err
	}
	guard := func(ctx context.Context, tx db.Tx) error {
		if err := w.fenceProfile(ctx, tx, watch.AgentID, current.profileHash); err != nil {
			return err
		}
		if err := w.api.inferenceCommitGuard(current.authorized, stamp)(ctx, tx); err != nil {
			return err
		}
		return w.sources.Fence(ctx, tx, current.source)
	}
	_, err = w.store.MaintainBoundary(ctx, input, knowledgeinstances.BoundaryCommitContext{CommitContext: knowledgeinstances.CommitContext{AgentID: watch.AgentID, Now: time.Now().UTC(), Guard: guard}, QueueChanges: ontologyreasoning.QueueSourceChangeTx})
	return err
}
