package knowledgeinstances

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/db"
)

type Store struct{ Core *db.Store }

func (s *Store) Candidates(ctx context.Context, tenant string, access contextgraph.AccessRef) ([]Entity, error) {
	// Compatibility listing only. Production identity decisions use RecallCandidates,
	// whose metadata reports relevance and truncation instead of claiming completeness.
	rows, err := s.Core.Query(ctx, `SELECT body_json FROM knowledge_instances WHERE tenant_id=? AND access_hash=? ORDER BY id LIMIT 200`, tenant, Digest(access))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Entity
	for rows.Next() {
		var raw string
		var e Entity
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

func (s *Store) Claims(ctx context.Context, tenant, watch string) ([]Claim, error) {
	rows, err := s.Core.Query(ctx, `SELECT body_json FROM knowledge_instance_claims WHERE tenant_id=? AND watch_id=? ORDER BY id`, tenant, watch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Claim
	for rows.Next() {
		var raw string
		var c Claim
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

func (s *Store) GetBatch(ctx context.Context, tenant, id string) (Batch, error) {
	var raw string
	var b Batch
	err := s.Core.QueryRow(ctx, `SELECT body_json FROM ontology_document_batches WHERE tenant_id=? AND id=?`, tenant, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return b, ErrNotFound
	}
	if err != nil {
		return b, err
	}
	err = json.Unmarshal([]byte(raw), &b)
	return b, err
}

// SavePrepared uses first-writer wins. A duplicate job always reuses the same
// preview; a later model response cannot replace an already reviewed plan.
func (s *Store) SavePrepared(ctx context.Context, b Batch) (Batch, error) {
	if b.ID == "" || b.TenantID == "" || b.WatchID == "" || b.Plan.ID == "" {
		return b, ErrInvalid
	}
	b.Status = "prepared"
	_, err := s.Core.Exec(ctx, `INSERT INTO ontology_document_batches(tenant_id,id,watch_id,status,body_json,created_at) VALUES(?,?,?,?,?,?) ON CONFLICT(tenant_id,id) DO NOTHING`, b.TenantID, b.ID, b.WatchID, b.Status, body(b), b.Plan.CreatedAt.UnixNano())
	if err != nil {
		return b, err
	}
	return s.GetBatch(ctx, b.TenantID, b.ID)
}

func (s *Store) SaveDecision(ctx context.Context, b Batch, d Decision) (Batch, error) {
	if err := ValidateResolutionChoices(b.Plan, d); err != nil {
		return b, err
	}
	if d.ModelID == "" || d.Reason == "" || len(d.Reason) > 2000 || d.PlanHash != Digest(b.Plan) || (d.Decision != "approve" && d.Decision != "reject" && d.Decision != "defer") {
		return b, ErrInvalid
	}
	err := s.Core.WithTx(ctx, func(tx db.Tx) error {
		var raw string
		if err := tx.QueryRowContext(ctx, `SELECT body_json FROM ontology_document_batches WHERE tenant_id=? AND id=?`, b.TenantID, b.ID).Scan(&raw); err != nil {
			return err
		}
		var current Batch
		if err := json.Unmarshal([]byte(raw), &current); err != nil {
			return err
		}
		if Digest(current.Plan) != d.PlanHash {
			return ErrConflict
		}
		if current.Status != "prepared" || current.Decision != nil {
			b = current
			return nil
		}
		current.Decision = &d
		current.Status = "reviewed"
		if d.Decision != "approve" {
			current.Status = d.Decision
		}
		result, err := tx.ExecContext(ctx, `UPDATE ontology_document_batches SET status=?,body_json=? WHERE tenant_id=? AND id=? AND status='prepared'`, current.Status, body(current), b.TenantID, b.ID)
		if err != nil {
			return err
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		b = current
		return nil
	})
	return b, err
}

// Supersede schedules a fresh observation instead of retrying a stale approved
// plan forever. Historical plans and decisions remain immutable.
func (s *Store) Supersede(ctx context.Context, b Batch) error {
	return s.Core.WithTx(ctx, func(tx db.Tx) error {
		b.Status = "superseded"
		_, err := tx.ExecContext(ctx, `UPDATE ontology_document_batches SET status=?,body_json=? WHERE tenant_id=? AND id=? AND status IN ('prepared','reviewed')`, b.Status, body(b), b.TenantID, b.ID)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE ontology_document_watches SET observed_stamp='',next_check_at=? WHERE tenant_id=? AND id=? AND version=? AND active=TRUE`, time.Now().UTC().UnixNano(), b.TenantID, b.WatchID, b.Plan.Watch.Version)
		return err
	})
}

func (s *Store) ListBatches(ctx context.Context, tenant, watch string) ([]Batch, error) {
	rows, err := s.Core.Query(ctx, `SELECT body_json FROM ontology_document_batches WHERE tenant_id=? AND watch_id=? ORDER BY created_at DESC,id LIMIT 20`, tenant, watch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Batch
	for rows.Next() {
		var raw string
		var b Batch
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &b); err != nil {
			return nil, err
		}
		result = append(result, b)
	}
	return result, rows.Err()
}
