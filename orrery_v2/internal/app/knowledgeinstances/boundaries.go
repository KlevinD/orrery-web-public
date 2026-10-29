package knowledgeinstances

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/outbox"
)

const BoundaryTopic = "ontology.document.validity_boundary"

type BoundaryJob struct {
	TenantID    string `json:"tenant_id"`
	WatchID     string `json:"watch_id"`
	Version     int64  `json:"version"`
	PlanHash    string `json:"plan_hash"`
	SourceStamp string `json:"source_stamp"`
	DueAt       int64  `json:"due_at"`
}

func (j BoundaryJob) EventID() string { return stableID("knowledge-boundary:", j) }

type BoundaryReceipt struct {
	EventID   string    `json:"event_id"`
	WatchID   string    `json:"watch_id"`
	EntityIDs []string  `json:"entity_ids"`
	AppliedAt time.Time `json:"applied_at"`
}

func scheduleBoundary(ctx context.Context, tx db.Tx, j BoundaryJob, claims []Claim, now time.Time) error {
	var due time.Time
	for _, c := range claims {
		start, end, err := c.Validity.Bounds()
		if err != nil {
			return err
		}
		bounds := []time.Time{start}
		if end != nil {
			bounds = append(bounds, *end)
		}
		for _, at := range bounds {
			if at.After(now) && (due.IsZero() || at.Before(due)) {
				due = at
			}
		}
	}
	if due.IsZero() {
		_, err := tx.ExecContext(ctx, `DELETE FROM ontology_document_boundaries WHERE tenant_id=? AND watch_id=?`, j.TenantID, j.WatchID)
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO ontology_document_boundaries(tenant_id,watch_id,watch_version,plan_hash,source_stamp,due_at,dispatch_after) VALUES(?,?,?,?,?,?,?) ON CONFLICT(tenant_id,watch_id) DO UPDATE SET watch_version=excluded.watch_version,plan_hash=excluded.plan_hash,source_stamp=excluded.source_stamp,due_at=excluded.due_at,dispatch_after=excluded.dispatch_after,sequence=0`, j.TenantID, j.WatchID, j.Version, j.PlanHash, j.SourceStamp, due.UnixNano(), due.UnixNano())
	return err
}

// The due cursor and outbox dispatch commit together. Retries have separate
// delivery IDs and one semantic event ID, recovering after exhausted retries.
func (s *Store) ObserveBoundaries(ctx context.Context, now time.Time, limit int) error {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	rows, err := s.Core.Query(ctx, `SELECT b.tenant_id,b.watch_id,b.watch_version,b.plan_hash,b.source_stamp,b.due_at,b.sequence FROM ontology_document_boundaries b JOIN ontology_document_watches w ON w.tenant_id=b.tenant_id AND w.id=b.watch_id WHERE w.active=TRUE AND b.dispatch_after<=? AND b.due_at<=? ORDER BY b.due_at,b.tenant_id,b.watch_id LIMIT ?`, now.UnixNano(), now.UnixNano(), limit)
	if err != nil {
		return err
	}
	type pending struct {
		job BoundaryJob
		seq int64
	}
	var work []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.job.TenantID, &p.job.WatchID, &p.job.Version, &p.job.PlanHash, &p.job.SourceStamp, &p.job.DueAt, &p.seq); err != nil {
			rows.Close()
			return err
		}
		work = append(work, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, p := range work {
		if err := s.Core.WithTx(ctx, func(tx db.Tx) error {
			r, err := tx.ExecContext(ctx, `UPDATE ontology_document_boundaries SET dispatch_after=?,sequence=sequence+1 WHERE tenant_id=? AND watch_id=? AND plan_hash=? AND due_at=? AND sequence=? AND dispatch_after<=?`, now.Add(30*time.Second).UnixNano(), p.job.TenantID, p.job.WatchID, p.job.PlanHash, p.job.DueAt, p.seq, now.UnixNano())
			if err != nil {
				return err
			}
			n, err := r.RowsAffected()
			if err != nil || n == 0 {
				return err
			}
			id := stableID("knowledge-boundary-delivery:", []any{p.job.EventID(), p.seq + 1})
			return outbox.NewTx(tx).Enqueue(ctx, outbox.Job{ID: id, Topic: BoundaryTopic, Payload: []byte(body(p.job)), IdempotencyKey: id, MaxAttempts: 5})
		}); err != nil {
			return err
		}
	}
	return nil
}

type BoundaryCommitContext struct {
	CommitContext
	QueueChanges func(context.Context, db.Tx, string, string) error
}

// MaintainBoundary recomputes current views and requests model review without
// inventing a new business claim. Projection, task, and receipt share one TX.
func (s *Store) MaintainBoundary(ctx context.Context, j BoundaryJob, trusted BoundaryCommitContext) (BoundaryReceipt, error) {
	var receipt BoundaryReceipt
	if trusted.Guard == nil || trusted.AgentID == "" || j.TenantID == "" || j.WatchID == "" || j.PlanHash == "" || j.Version < 1 || j.DueAt <= 0 || trusted.Now.IsZero() {
		return receipt, ErrInvalid
	}
	err := s.Core.WithTx(ctx, func(tx db.Tx) error {
		if err := trusted.Guard(ctx, tx); err != nil {
			return err
		}
		if s.Core.Dialect() == db.DialectPostgres {
			if _, err := tx.ExecContext(ctx, `LOCK TABLE context_graph_objects,knowledge_instances,knowledge_instance_claims,ontology_document_batches,ontology_document_receipts IN SHARE ROW EXCLUSIVE MODE`); err != nil {
				return err
			}
		}
		var raw string
		err := tx.QueryRowContext(ctx, `SELECT body_json FROM ontology_boundary_receipts WHERE tenant_id=? AND event_id=?`, j.TenantID, j.EventID()).Scan(&raw)
		if err == nil {
			return json.Unmarshal([]byte(raw), &receipt)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var version, due int64
		var plan, stamp string
		err = tx.QueryRowContext(ctx, `SELECT watch_version,plan_hash,source_stamp,due_at FROM ontology_document_boundaries WHERE tenant_id=? AND watch_id=?`, j.TenantID, j.WatchID).Scan(&version, &plan, &stamp, &due)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if version != j.Version || plan != j.PlanHash || stamp != j.SourceStamp || due != j.DueAt {
			return nil
		}
		if trusted.Now.UnixNano() < due {
			return ErrConflict
		}
		if err := (&WatchStore{Store: s.Core}).CheckActiveTx(ctx, tx, j.TenantID, j.WatchID, trusted.AgentID, j.Version); err != nil {
			return err
		}
		own, err := claimsTx(ctx, tx, j.TenantID, "watch_id", j.WatchID)
		if err != nil {
			return err
		}
		before := map[string]int64{}
		for _, c := range own {
			before[c.EntityID] = 0
			if c.TargetID != "" {
				before[c.TargetID] = 0
			}
		}
		all, err := ownedClaims(ctx, tx.QueryContext, j.TenantID, before)
		if err != nil {
			return err
		}
		owners := map[string]string{}
		for _, c := range all {
			owners[c.Claim.ID] = c.WatchID
			table := "context_graph_facts"
			if c.Claim.TargetID != "" {
				table = "context_graph_links"
			}
			hash, err := projectionHash(ctx, tx, table, j.TenantID, c.Claim.ID, "knowledge-document:"+j.TenantID+":"+c.WatchID)
			if err != nil || hash != c.Claim.ProjectionHash {
				return ErrConflict
			}
		}
		for id := range before {
			if err := tx.QueryRowContext(ctx, `SELECT body_json FROM knowledge_instances WHERE tenant_id=? AND id=?`, j.TenantID, id).Scan(&raw); err != nil {
				return err
			}
			var e Entity
			if err := json.Unmarshal([]byte(raw), &e); err != nil {
				return err
			}
			hash, err := projectionHash(ctx, tx, "context_graph_objects", j.TenantID, id, "knowledge-instance:"+id)
			if err != nil || hash != e.ProjectionHash {
				return ErrConflict
			}
			claims, err := claimsTx(ctx, tx, j.TenantID, "entity_id", id)
			if err != nil {
				return err
			}
			batch := boundaryResolutionBatch(j, e, claims, activeClaimsAt(claims, trusted.Now), trusted.Now)
			e, err = s.adjudicateEntity(ctx, tx, batch, e, claims, owners, "", trusted.Now)
			if err != nil {
				return err
			}
			e.Version++
			object := contextgraph.Object{ID: e.ID, TenantID: e.TenantID, ObjectType: EntityType, CanonicalKey: EntityType + ":" + e.ID, Label: e.Name, Properties: e.Properties, Status: e.Status, Sensitivity: "internal", Confidence: 1, Provenance: contextgraph.Ref{Kind: Domain, ID: e.ID}, OwnerAccessRef: e.Access, SourceStreamID: "knowledge-instance:" + e.ID, SourceVersion: e.Version, UpdatedAt: trusted.Now}
			if err := contextgraph.NewPlatformStoreTx(tx).SaveObject(ctx, object); err != nil {
				return err
			}
			e.ProjectionHash, err = projectionHash(ctx, tx, "context_graph_objects", j.TenantID, id, object.SourceStreamID)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE knowledge_instances SET version=?,body_json=? WHERE tenant_id=? AND id=?`, e.Version, body(e), j.TenantID, id); err != nil {
				return err
			}
			if err := syncIdentityIndexTx(ctx, tx, e, j.EventID()); err != nil {
				return err
			}
			receipt.EntityIDs = append(receipt.EntityIDs, id)
		}
		if err := scheduleBoundary(ctx, tx, j, own, trusted.Now); err != nil {
			return err
		}
		if err := supersedeSourceBatches(ctx, tx, j.TenantID, j.WatchID); err != nil {
			return err
		}
		if err := enqueueDocumentJob(ctx, tx, DocumentTopic, j.EventID()+":review", DocumentJob{TenantID: j.TenantID, WatchID: j.WatchID, Version: j.Version}); err != nil {
			return err
		}
		if trusted.QueueChanges != nil {
			if err := trusted.QueueChanges(ctx, tx, j.TenantID, j.EventID()); err != nil {
				return err
			}
		}
		sort.Strings(receipt.EntityIDs)
		receipt.EventID, receipt.WatchID, receipt.AppliedAt = j.EventID(), j.WatchID, trusted.Now
		_, err = tx.ExecContext(ctx, `INSERT INTO ontology_boundary_receipts(tenant_id,event_id,body_json) VALUES(?,?,?)`, j.TenantID, j.EventID(), body(receipt))
		return err
	})
	return receipt, err
}

// Keep an existing model choice on expiration while selected support survives.
// A new activation requires another model decision before choosing a value.
func boundaryResolutionBatch(j BoundaryJob, e Entity, all, active []Claim, now time.Time) Batch {
	b := Batch{ID: j.EventID(), TenantID: j.TenantID, Decision: &Decision{ModelID: "retained-model-resolution"}}
	var source []SourceClaim
	for _, c := range active {
		source = append(source, SourceClaim{Claim: c})
	}
	b.Plan.Conflicts = conflictCases(source)
	for _, r := range e.Resolutions {
		newActivation := slices.ContainsFunc(all, func(c Claim) bool {
			start, _, _ := c.Validity.Bounds()
			return c.Predicate == r.Predicate && !start.IsZero() && start.UnixNano() >= j.DueAt && !start.After(now)
		})
		if newActivation {
			continue
		}
		selected := slices.IndexFunc(active, func(c Claim) bool { return slices.Contains(r.SupportingClaimIDs, c.ID) })
		if selected < 0 || !slices.ContainsFunc(b.Plan.Conflicts, func(c ConflictCase) bool { return c.Predicate == r.Predicate }) {
			continue
		}
		choice := r.ResolutionChoice
		choice.ClaimID = active[selected].ID
		b.Decision.Resolutions = append(b.Decision.Resolutions, choice)
	}
	return b
}
