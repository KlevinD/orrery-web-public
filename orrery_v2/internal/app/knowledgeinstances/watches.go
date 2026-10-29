package knowledgeinstances

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/outbox"
)

type Watch struct {
	TenantID     string    `json:"tenant_id"`
	ID           string    `json:"id"`
	AgentID      string    `json:"agent_id"`
	Version      int64     `json:"version"`
	RootID       string    `json:"root_id"`
	Active       bool      `json:"active"`
	PublishedAt  time.Time `json:"published_at"`
	InitialStamp string    `json:"-"`
}

type WatchRequest struct {
	Operation       string `json:"operation"`
	TenantID        string `json:"tenant_id"`
	ID              string `json:"watch_id,omitempty"`
	ExpectedVersion int64  `json:"expected_version,omitempty"`
	RootID          string `json:"root_id,omitempty"`
}

type DocumentJob struct {
	TenantID string `json:"tenant_id"`
	WatchID  string `json:"watch_id"`
	Version  int64  `json:"version"`
}

type WatchStore struct{ Store *db.Store }

func (s *WatchStore) Get(ctx context.Context, tenant, id string) (Watch, error) {
	var body string
	err := s.Store.QueryRow(ctx, `SELECT body_json FROM ontology_document_watches WHERE tenant_id=? AND id=?`, tenant, id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return Watch{}, ErrNotFound
	}
	if err != nil {
		return Watch{}, err
	}
	var set Watch
	err = json.Unmarshal([]byte(body), &set)
	return set, err
}

func (s *WatchStore) List(ctx context.Context, tenant, agent string) ([]Watch, error) {
	rows, err := s.Store.Query(ctx, `SELECT body_json FROM ontology_document_watches WHERE tenant_id=? AND agent_id=? ORDER BY id LIMIT 101`, tenant, agent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sets []Watch
	for rows.Next() {
		var body string
		var set Watch
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(body), &set); err != nil {
			return nil, err
		}
		sets = append(sets, set)
	}
	if len(sets) > 100 {
		return nil, fmt.Errorf("%w: document watch budget exceeded", ErrInvalid)
	}
	return sets, rows.Err()
}

// Publish atomically persists the active version and its first durable task.
// Equal retries return that version; a different change requires its version.
func (s *WatchStore) Publish(ctx context.Context, set Watch, expected int64, guard func(context.Context, db.Tx) error) (Watch, error) {
	if set.TenantID == "" || set.AgentID == "" || strings.TrimSpace(set.ID) == "" || len(set.ID) > 128 || set.RootID == "" || expected < 0 || guard == nil {
		return Watch{}, ErrInvalid
	}
	var result Watch
	err := s.Store.WithTx(ctx, func(tx db.Tx) error {
		if err := guard(ctx, tx); err != nil {
			return err
		}
		// The guard serializes source changes. A per-tenant insertion fence also
		// protects publication quotas and concurrent first publications.
		if s.Store.Dialect() == db.DialectPostgres {
			if _, err := tx.ExecContext(ctx, `LOCK TABLE ontology_document_watches IN SHARE ROW EXCLUSIVE MODE`); err != nil {
				return err
			}
		}
		var body string
		err := tx.QueryRowContext(ctx, `SELECT body_json FROM ontology_document_watches WHERE tenant_id=? AND id=?`, set.TenantID, set.ID).Scan(&body)
		var old Watch
		if err == nil {
			if err := json.Unmarshal([]byte(body), &old); err != nil {
				return err
			}
			if old.AgentID != set.AgentID {
				return ErrConflict
			}
			if old.RootID != set.RootID {
				return fmt.Errorf("%w: a document watch's root is immutable", ErrInvalid)
			}
			if old.Active == set.Active && old.RootID == set.RootID {
				result = old
				return nil
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if old.Version != expected {
			return ErrConflict
		}
		if old.Version == 0 {
			var count int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM ontology_document_watches WHERE tenant_id=? AND agent_id=?`, set.TenantID, set.AgentID).Scan(&count); err != nil {
				return err
			}
			if count >= 100 {
				return fmt.Errorf("%w: document watch budget exceeded", ErrInvalid)
			}
		}
		set.Version = expected + 1
		encoded, err := json.Marshal(set)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO ontology_document_watch_versions(tenant_id,watch_id,version,body_json) VALUES(?,?,?,?)`, set.TenantID, set.ID, set.Version, string(encoded)); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO ontology_document_watches(tenant_id,id,agent_id,version,active,body_json,next_check_at,observed_stamp) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(tenant_id,id) DO UPDATE SET version=excluded.version,active=excluded.active,body_json=excluded.body_json,observed_stamp=excluded.observed_stamp,sequence=0,next_check_at=excluded.next_check_at`, set.TenantID, set.ID, set.AgentID, set.Version, set.Active, string(encoded), set.PublishedAt.Add(30*time.Second).UnixNano(), set.InitialStamp)
		if err != nil {
			return err
		}
		if set.Active {
			job := DocumentJob{TenantID: set.TenantID, WatchID: set.ID, Version: set.Version}
			if err := enqueueDocumentJob(ctx, tx, DocumentTopic, stableID("document-extract:", []any{set.TenantID, set.ID, set.Version, "publication"}), job); err != nil {
				return err
			}
		}
		result = set
		return nil
	})
	return result, err
}

// CheckActiveTx is a write fence against pause/republication racing a commit.
func (s *WatchStore) CheckActiveTx(ctx context.Context, tx db.Tx, tenant, id, agent string, version int64) error {
	result, err := tx.ExecContext(ctx, `UPDATE ontology_document_watches SET version=version WHERE tenant_id=? AND id=? AND agent_id=? AND version=? AND active=TRUE`, tenant, id, agent, version)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

// Observe polls bounded due document watches; the observation cursor and queued task
// commit together, so restart cannot lose a detected source/policy change.
func (s *WatchStore) Observe(ctx context.Context, now time.Time, limit int, stamp func(context.Context, Watch) (string, error)) error {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.Store.Query(ctx, `SELECT body_json,observed_stamp,sequence FROM ontology_document_watches WHERE active=TRUE AND next_check_at<=? ORDER BY next_check_at,tenant_id,id LIMIT ?`, now.UnixNano(), limit)
	if err != nil {
		return err
	}
	type observation struct {
		set      Watch
		old      string
		sequence int64
	}
	var pending []observation
	for rows.Next() {
		var body string
		var o observation
		if err = rows.Scan(&body, &o.old, &o.sequence); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal([]byte(body), &o.set); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var failures []error
	for _, o := range pending {
		actual, stampErr := stamp(ctx, o.set)
		err = s.Store.WithTx(ctx, func(tx db.Tx) error {
			sequence := o.sequence
			if stampErr == nil && actual != o.old {
				sequence++
			}
			result, err := tx.ExecContext(ctx, `UPDATE ontology_document_watches SET next_check_at=?,observed_stamp=?,sequence=? WHERE tenant_id=? AND id=? AND version=? AND active=TRUE AND sequence=? AND observed_stamp=? AND next_check_at<=?`, now.Add(30*time.Second).UnixNano(), chooseObservationStamp(stampErr, actual, o.old), sequence, o.set.TenantID, o.set.ID, o.set.Version, o.sequence, o.old, now.UnixNano())
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if n == 1 && sequence != o.sequence {
				return enqueueDocumentJob(ctx, tx, DocumentTopic, stableID("document-extract:", []any{o.set.TenantID, o.set.ID, o.set.Version, sequence}), DocumentJob{TenantID: o.set.TenantID, WatchID: o.set.ID, Version: o.set.Version})
			}
			return nil
		})
		if err != nil {
			failures = append(failures, err)
		}
		if stampErr != nil {
			failures = append(failures, stampErr)
		}
	}
	return errors.Join(failures...)
}

func chooseObservationStamp(err error, actual, previous string) string {
	if err != nil {
		return previous
	}
	return actual
}

func enqueueDocumentJob(ctx context.Context, tx db.Tx, topic, id string, payload DocumentJob) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return outbox.NewTx(tx).Enqueue(ctx, outbox.Job{ID: id, Topic: topic, Payload: encoded, IdempotencyKey: id, MaxAttempts: 5})
}
