package ontologyreasoning

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

const InferenceEvaluationTopic = "ontology.inference.evaluate"
const InferenceDecisionTopic = "ontology.inference.decide"

type RuleSet struct {
	TenantID    string    `json:"tenant_id"`
	ID          string    `json:"id"`
	AgentID     string    `json:"agent_id"`
	Version     int64     `json:"version"`
	RootID      string    `json:"root_id"`
	Rules       []Rule    `json:"rules"`
	Active      bool      `json:"active"`
	PublishedAt time.Time `json:"published_at"`
}

type RuleSetRequest struct {
	Operation       string `json:"operation"`
	TenantID        string `json:"tenant_id"`
	ID              string `json:"rule_set_id,omitempty"`
	ExpectedVersion int64  `json:"expected_version,omitempty"`
	RootID          string `json:"root_id,omitempty"`
	Rules           []Rule `json:"rules,omitempty"`
}

type InferenceJob struct {
	SupportRevision int64  `json:"support_revision,omitempty"`
	TenantID        string `json:"tenant_id"`
	RuleSetID       string `json:"rule_set_id"`
	Version         int64  `json:"version"`
	CandidateID     string `json:"candidate_id,omitempty"`
	SupportHash     string `json:"support_hash,omitempty"`
	ReceiptID       string `json:"receipt_id,omitempty"`
}

type RuleSetStore struct{ Store *db.Store }

func (s *RuleSetStore) Get(ctx context.Context, tenant, id string) (RuleSet, error) {
	var body string
	err := s.Store.QueryRow(ctx, `SELECT body_json FROM ontology_inference_rule_sets WHERE tenant_id=? AND id=?`, tenant, id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return RuleSet{}, ErrNotFound
	}
	if err != nil {
		return RuleSet{}, err
	}
	var set RuleSet
	err = json.Unmarshal([]byte(body), &set)
	return set, err
}

func (s *RuleSetStore) List(ctx context.Context, tenant, agent string) ([]RuleSet, error) {
	rows, err := s.Store.Query(ctx, `SELECT body_json FROM ontology_inference_rule_sets WHERE tenant_id=? AND agent_id=? ORDER BY id LIMIT 101`, tenant, agent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sets []RuleSet
	for rows.Next() {
		var body string
		var set RuleSet
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(body), &set); err != nil {
			return nil, err
		}
		sets = append(sets, set)
	}
	if len(sets) > 100 {
		return nil, fmt.Errorf("%w: published rule set budget exceeded", ErrValidationFailed)
	}
	return sets, rows.Err()
}

// Publish atomically persists the active version and its first durable task.
// Equal retries return that version; a different change requires its version.
func (s *RuleSetStore) Publish(ctx context.Context, set RuleSet, expected int64, guard func(context.Context, db.Tx) error) (RuleSet, error) {
	if set.TenantID == "" || set.AgentID == "" || strings.TrimSpace(set.ID) == "" || len(set.ID) > 128 || set.RootID == "" || expected < 0 || guard == nil {
		return RuleSet{}, ErrInvalidRequest
	}
	if err := ValidateRules(set.Rules); err != nil {
		return RuleSet{}, err
	}
	var result RuleSet
	err := s.Store.WithTx(ctx, func(tx db.Tx) error {
		if err := guard(ctx, tx); err != nil {
			return err
		}
		// The guard serializes source changes. A per-tenant insertion fence also
		// protects publication quotas and concurrent first publications.
		if s.Store.Dialect() == db.DialectPostgres {
			if _, err := tx.ExecContext(ctx, `LOCK TABLE ontology_inference_rule_sets IN SHARE ROW EXCLUSIVE MODE`); err != nil {
				return err
			}
		}
		var body string
		err := tx.QueryRowContext(ctx, `SELECT body_json FROM ontology_inference_rule_sets WHERE tenant_id=? AND id=?`, set.TenantID, set.ID).Scan(&body)
		var old RuleSet
		if err == nil {
			if err := json.Unmarshal([]byte(body), &old); err != nil {
				return err
			}
			if old.AgentID != set.AgentID {
				return ErrStateConflict
			}
			if old.RootID != set.RootID {
				return fmt.Errorf("%w: a published rule set's root is immutable", ErrInvalidRequest)
			}
			if old.Active == set.Active && old.RootID == set.RootID && hashJSON(old.Rules) == hashJSON(set.Rules) {
				result = old
				return nil
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if old.Version != expected {
			return ErrStateConflict
		}
		if old.Version == 0 {
			var count int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM ontology_inference_rule_sets WHERE tenant_id=? AND agent_id=?`, set.TenantID, set.AgentID).Scan(&count); err != nil {
				return err
			}
			if count >= 100 {
				return fmt.Errorf("%w: published rule set budget exceeded", ErrValidationFailed)
			}
		}
		set.Version = expected + 1
		encoded, err := json.Marshal(set)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO ontology_inference_rule_set_versions(tenant_id,rule_set_id,version,body_json) VALUES(?,?,?,?)`, set.TenantID, set.ID, set.Version, string(encoded)); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO ontology_inference_rule_sets(tenant_id,id,agent_id,version,active,body_json,next_check_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(tenant_id,id) DO UPDATE SET version=excluded.version,active=excluded.active,body_json=excluded.body_json,observed_stamp='',sequence=0,next_check_at=excluded.next_check_at`, set.TenantID, set.ID, set.AgentID, set.Version, set.Active, string(encoded), set.PublishedAt.Add(30*time.Second).UnixNano())
		if err != nil {
			return err
		}
		if set.Active {
			job := InferenceJob{TenantID: set.TenantID, RuleSetID: set.ID, Version: set.Version}
			if err := enqueueInferenceJob(ctx, tx, InferenceEvaluationTopic, stableID("inference-eval:", []any{set.TenantID, set.ID, set.Version, "publication"}), job); err != nil {
				return err
			}
		}
		result = set
		return nil
	})
	return result, err
}

// CheckActiveTx is a write fence against pause/republication racing a commit.
func (s *RuleSetStore) CheckActiveTx(ctx context.Context, tx db.Tx, tenant, id, agent string, version int64) error {
	result, err := tx.ExecContext(ctx, `UPDATE ontology_inference_rule_sets SET version=version WHERE tenant_id=? AND id=? AND agent_id=? AND version=? AND active=TRUE`, tenant, id, agent, version)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrStateConflict
	}
	return nil
}

// Observe polls bounded due rule sets; the observation cursor and queued task
// commit together, so restart cannot lose a detected source/policy change.
func (s *RuleSetStore) Observe(ctx context.Context, now time.Time, limit int, stamp func(context.Context, RuleSet) (string, error)) error {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.Store.Query(ctx, `SELECT body_json,observed_stamp,sequence FROM ontology_inference_rule_sets WHERE active=TRUE AND next_check_at<=? ORDER BY next_check_at,tenant_id,id LIMIT ?`, now.UnixNano(), limit)
	if err != nil {
		return err
	}
	type observation struct {
		set      RuleSet
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
			result, err := tx.ExecContext(ctx, `UPDATE ontology_inference_rule_sets SET next_check_at=?,observed_stamp=?,sequence=? WHERE tenant_id=? AND id=? AND version=? AND active=TRUE AND sequence=? AND observed_stamp=? AND next_check_at<=?`, now.Add(30*time.Second).UnixNano(), chooseObservationStamp(stampErr, actual, o.old), sequence, o.set.TenantID, o.set.ID, o.set.Version, o.sequence, o.old, now.UnixNano())
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if n == 1 && sequence != o.sequence {
				return enqueueInferenceJob(ctx, tx, InferenceEvaluationTopic, stableID("inference-eval:", []any{o.set.TenantID, o.set.ID, o.set.Version, sequence}), InferenceJob{TenantID: o.set.TenantID, RuleSetID: o.set.ID, Version: o.set.Version})
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

func enqueueInferenceJob(ctx context.Context, tx db.Tx, topic, id string, payload InferenceJob) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return outbox.NewTx(tx).Enqueue(ctx, outbox.Job{ID: id, Topic: topic, Payload: encoded, IdempotencyKey: id, MaxAttempts: 5})
}

func (s *RuleSetStore) QueueDecisions(ctx context.Context, set RuleSet, states []CandidateState, dependencies map[string][]Dependency) error {
	return s.Store.WithTx(ctx, func(tx db.Tx) error {
		if err := s.CheckActiveTx(ctx, tx, set.TenantID, set.ID, set.AgentID, set.Version); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM ontology_inference_dependencies WHERE tenant_id=? AND rule_set_id=?`, set.TenantID, set.ID); err != nil {
			return err
		}
		for candidateID, items := range dependencies {
			for _, item := range items {
				if _, err := tx.ExecContext(ctx, `INSERT INTO ontology_inference_dependencies(tenant_id,rule_set_id,candidate_id,dependency_ref,dependency_hash) VALUES(?,?,?,?,?)`, set.TenantID, set.ID, candidateID, item.Ref, item.Hash); err != nil {
					return err
				}
			}
		}
		for _, state := range states {
			if state.Status == "applied" || state.Status == "retracted" || state.Status == "rejected" || state.Status == "withdrawn" {
				continue
			}
			receiptID := ""
			if state.Receipt != nil {
				receiptID = state.Receipt.ID
			}
			job := InferenceJob{TenantID: set.TenantID, RuleSetID: set.ID, Version: set.Version, CandidateID: state.CandidateID, SupportHash: state.SupportHash, SupportRevision: state.SupportRevision, ReceiptID: receiptID}
			if err := enqueueInferenceJob(ctx, tx, InferenceDecisionTopic, stableID("inference-decision:", job), job); err != nil {
				return err
			}
		}
		return nil
	})
}
