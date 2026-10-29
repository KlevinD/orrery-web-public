package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
)

const (
	scheduledRunOntologyRetentionCandidateLimit = 2000
	scheduledRunOntologyRetentionDeleteBatch    = 500
	scheduledRunOntologyRetentionSweepInterval  = 6 * time.Hour
)

var scheduledRunOntologyEventTypes = []string{
	ontologyEventTypePrefix + "scheduler.run.started",
	ontologyEventTypePrefix + "scheduler.run.completed",
	ontologyEventTypePrefix + "scheduler.run.agent_resolved",
}

type scheduledRunProjectionRef struct {
	tenantID string
	runID    string
}

type productionScheduledRunOntologyRetentionSweeper struct {
	core *db.Store
	now  func() time.Time
}

func registerProductionScheduledRunOntologyRetentionSweeper(rt *platform.Runtime, core *db.Store) error {
	if rt == nil || core == nil {
		return fmt.Errorf("scheduled run ontology retention dependencies are required")
	}
	sweeper := productionScheduledRunOntologyRetentionSweeper{core: core, now: rt.Clock.Now}
	return rt.RegisterSweeper("ontology.scheduler_run_retention", newProductionCadencedSweeper(
		platform.SweeperFunc(sweeper.Sweep), rt.Clock.Now, scheduledRunOntologyRetentionSweepInterval,
	))
}

func (s productionScheduledRunOntologyRetentionSweeper) Sweep(ctx context.Context) error {
	if s.core == nil {
		return fmt.Errorf("scheduled run ontology retention store is required")
	}
	now := time.Now().UTC()
	if s.now != nil {
		now = s.now().UTC()
	}
	cutoff := now.Add(-scheduledRunOntologyRetention)
	candidates, err := s.listGraphCandidates(ctx, cutoff)
	if err != nil {
		return err
	}
	eventIDs, err := s.listEventCandidates(ctx, candidates)
	if err != nil {
		return err
	}
	return s.core.WithTx(ctx, func(tx db.Tx) error {
		if err := deleteScheduledRunGraphCandidates(ctx, tx, candidates); err != nil {
			return err
		}
		if err := deleteIDsInBatches(ctx, tx, "context_graph_evidence", "id", eventIDs); err != nil {
			return fmt.Errorf("delete scheduled run ontology evidence: %w", err)
		}
		if err := deleteIDsInBatches(ctx, tx, "platform_event_idempotency", "event_id", eventIDs); err != nil {
			return fmt.Errorf("delete scheduled run ontology event idempotency: %w", err)
		}
		if err := deleteIDsInBatches(ctx, tx, "platform_events", "event_id", eventIDs); err != nil {
			return fmt.Errorf("delete scheduled run ontology events: %w", err)
		}
		return nil
	})
}

func (s productionScheduledRunOntologyRetentionSweeper) listGraphCandidates(ctx context.Context, cutoff time.Time) ([]scheduledRunProjectionRef, error) {
	rows, err := s.core.Query(ctx, `
SELECT tenant_id, id
FROM (
	SELECT DISTINCT o.tenant_id, o.id
	FROM context_graph_objects o
JOIN context_graph_links l
  ON l.tenant_id = o.tenant_id
 AND l.to_object_id = o.id
 AND l.link_type = 'scheduled_task_has_run'
WHERE o.object_type = 'scheduled_run'
  AND l.from_object_id = ?
UNION
SELECT DISTINCT o.tenant_id, o.id
FROM context_graph_objects o
	WHERE o.object_type = 'scheduled_run'
	  AND o.updated_at < ?
	) candidates
	ORDER BY tenant_id, id
	LIMIT ?`,
		defaultResponsibilityReconcileScheduledTaskID, cutoff.UTC().UnixNano(), scheduledRunOntologyRetentionCandidateLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("list scheduled run ontology retention candidates: %w", err)
	}
	defer rows.Close()
	var candidates []scheduledRunProjectionRef
	for rows.Next() {
		var candidate scheduledRunProjectionRef
		if err := rows.Scan(&candidate.tenantID, &candidate.runID); err != nil {
			return nil, fmt.Errorf("scan scheduled run ontology retention candidate: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scheduled run ontology retention candidates: %w", err)
	}
	return candidates, nil
}

func (s productionScheduledRunOntologyRetentionSweeper) listEventCandidates(ctx context.Context, candidates []scheduledRunProjectionRef) ([]string, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	eventIDs := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		prefix := "scheduler-run-projected:" + strings.TrimSpace(candidate.runID) + ":"
		rows, err := s.core.Query(ctx, `
SELECT id
FROM context_graph_evidence
WHERE tenant_id = ?
  AND id >= ?
  AND id < ?
  AND source_kind IN (?, ?, ?)
ORDER BY id`,
			candidate.tenantID, prefix, prefix+"~",
			strings.TrimPrefix(scheduledRunOntologyEventTypes[0], ontologyEventTypePrefix),
			strings.TrimPrefix(scheduledRunOntologyEventTypes[1], ontologyEventTypePrefix),
			strings.TrimPrefix(scheduledRunOntologyEventTypes[2], ontologyEventTypePrefix),
		)
		if err != nil {
			return nil, fmt.Errorf("list scheduled run ontology evidence retention candidates: %w", err)
		}
		for rows.Next() {
			var eventID string
			if err := rows.Scan(&eventID); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan scheduled run ontology evidence retention candidate: %w", err)
			}
			if _, exists := seen[eventID]; exists {
				continue
			}
			seen[eventID] = struct{}{}
			eventIDs = append(eventIDs, eventID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("iterate scheduled run ontology evidence retention candidates: %w", err)
		}
		rows.Close()
	}
	return eventIDs, nil
}

func deleteScheduledRunGraphCandidates(ctx context.Context, tx db.Tx, candidates []scheduledRunProjectionRef) error {
	byTenant := make(map[string][]string)
	for _, candidate := range candidates {
		byTenant[candidate.tenantID] = append(byTenant[candidate.tenantID], candidate.runID)
	}
	for tenantID, runIDs := range byTenant {
		for start := 0; start < len(runIDs); start += scheduledRunOntologyRetentionDeleteBatch {
			end := min(start+scheduledRunOntologyRetentionDeleteBatch, len(runIDs))
			batch := runIDs[start:end]
			placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
			args := make([]any, 0, len(batch)+1)
			args = append(args, tenantID)
			for _, runID := range batch {
				args = append(args, runID)
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM context_graph_fact_assertions WHERE tenant_id = ? AND subject_kind = 'scheduled_run' AND subject_id IN (`+placeholders+`)`, args...); err != nil {
				return fmt.Errorf("delete scheduled run ontology fact assertions: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM context_graph_facts WHERE tenant_id = ? AND subject_kind = 'scheduled_run' AND subject_id IN (`+placeholders+`)`, args...); err != nil {
				return fmt.Errorf("delete scheduled run ontology facts: %w", err)
			}
			linkArgs := append(append([]any(nil), args...), args[1:]...)
			if _, err := tx.ExecContext(ctx, `DELETE FROM context_graph_links WHERE tenant_id = ? AND (from_object_id IN (`+placeholders+`) OR to_object_id IN (`+placeholders+`))`, linkArgs...); err != nil {
				return fmt.Errorf("delete scheduled run ontology links: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM context_graph_objects WHERE tenant_id = ? AND object_type = 'scheduled_run' AND id IN (`+placeholders+`)`, args...); err != nil {
				return fmt.Errorf("delete scheduled run ontology objects: %w", err)
			}
		}
	}
	return nil
}

func deleteIDsInBatches(ctx context.Context, tx db.Tx, table, column string, ids []string) error {
	allowed := map[string]map[string]bool{
		"context_graph_evidence":     {"id": true},
		"platform_event_idempotency": {"event_id": true},
		"platform_events":            {"event_id": true},
	}
	if !allowed[table][column] {
		return fmt.Errorf("unsupported retention delete target %s.%s", table, column)
	}
	for start := 0; start < len(ids); start += scheduledRunOntologyRetentionDeleteBatch {
		end := min(start+scheduledRunOntologyRetentionDeleteBatch, len(ids))
		batch := ids[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, 0, len(batch))
		for _, id := range batch {
			args = append(args, id)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE "+column+" IN ("+placeholders+")", args...); err != nil {
			return err
		}
	}
	return nil
}
