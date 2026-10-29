package ontologyreasoning

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

var ErrStateConflict = errors.New("ontology inference state changed")

const DerivedLinkActionID = "ontology.derived_link.assert"

type CandidateState struct {
	SupportPresent     bool         `json:"support_present"`
	SupportRevision    int64        `json:"support_revision"`
	CandidateID        string       `json:"candidate_id"`
	SupportHash        string       `json:"support_hash"`
	Revision           int64        `json:"revision"`
	Status             string       `json:"status"`
	RunID              string       `json:"run_id"`
	DecisionBy         string       `json:"decision_by,omitempty"`
	DecisionKind       string       `json:"decision_kind,omitempty"`
	DecisionReason     string       `json:"decision_reason,omitempty"`
	UpdatedAt          time.Time    `json:"updated_at"`
	Plan               *LinkPlan    `json:"plan,omitempty"`
	Receipt            *LinkReceipt `json:"receipt,omitempty"`
	PreviousReceiptIDs []string     `json:"previous_receipt_ids,omitempty"`
}

type SessionKey struct{ Tenant, Actor, Query string }

type CandidateSession struct {
	Revision   int64                     `json:"revision"`
	RunID      string                    `json:"run_id"`
	Candidates map[string]CandidateState `json:"candidates"`
}

type LinkPlan struct {
	Effect            string    `json:"effect,omitempty"`
	BeforeHash        string    `json:"before_hash,omitempty"`
	OwnedActionRunID  string    `json:"owned_action_run_id,omitempty"`
	ID                string    `json:"id"`
	Actor             string    `json:"actor"`
	SupportHash       string    `json:"support_hash"`
	AuthorizationHash string    `json:"authorization_hash"`
	ExpiresAt         time.Time `json:"expires_at"`
	LinkType          string    `json:"link_type"`
	From              ObjectRef `json:"from"`
	To                ObjectRef `json:"to"`
}

type LinkReceipt struct {
	Effect         string    `json:"effect,omitempty"`
	Actor          string    `json:"actor"`
	ActorKind      string    `json:"actor_kind,omitempty"`
	ID             string    `json:"id"`
	PlanID         string    `json:"plan_id"`
	IdempotencyKey string    `json:"idempotency_key"`
	ActionRunID    string    `json:"action_run_id"`
	LinkID         string    `json:"link_id"`
	AppliedAt      time.Time `json:"applied_at"`
}

// WorkflowRequest uses opaque references. The host always obtains the caller,
// current input, authorization and source access references itself.
type WorkflowRequest struct {
	TenantID         string `json:"tenant_id"`
	RunID            string `json:"run_id"`
	CandidateID      string `json:"candidate_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	SupportHash      string `json:"support_hash"`
	Decision         string `json:"decision,omitempty"`
	Reason           string `json:"reason,omitempty"`
	PlanID           string `json:"plan_id,omitempty"`
	IdempotencyKey   string `json:"idempotency_key,omitempty"`
	Confirmed        bool   `json:"confirmed,omitempty"`
}

type LifecycleStore struct{ Store *db.Store }

func inferenceLifecycleMigration() migration.Migration {
	return migration.Migration{Version: "0238_ontology_inference_lifecycle", Description: "persist current inference candidates separately from historical runs", Statements: []string{
		`CREATE TABLE IF NOT EXISTS ontology_inference_sessions (tenant_id TEXT NOT NULL, actor_hash TEXT NOT NULL, query_hash TEXT NOT NULL, revision BIGINT NOT NULL, run_id TEXT NOT NULL, state_json TEXT NOT NULL, PRIMARY KEY (tenant_id, actor_hash, query_hash))`,
		`CREATE TABLE IF NOT EXISTS ontology_inference_links (tenant_id TEXT NOT NULL, id TEXT NOT NULL, candidate_id TEXT NOT NULL, support_hash TEXT NOT NULL, approved_by TEXT NOT NULL, action_run_id TEXT NOT NULL, link_json TEXT NOT NULL, PRIMARY KEY (tenant_id,id))`,
	}}
}

func (s *LifecycleStore) Get(ctx context.Context, key SessionKey) (CandidateSession, error) {
	if s == nil || s.Store == nil {
		return CandidateSession{}, ErrInvalidRequest
	}
	var body string
	err := s.Store.QueryRow(ctx, `SELECT state_json FROM ontology_inference_sessions WHERE tenant_id=? AND actor_hash=? AND query_hash=?`, key.Tenant, key.Actor, key.Query).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return CandidateSession{Candidates: map[string]CandidateState{}}, nil
	}
	if err != nil {
		return CandidateSession{}, err
	}
	var state CandidateSession
	err = json.Unmarshal([]byte(body), &state)
	return state, err
}

func (s *LifecycleStore) Update(ctx context.Context, key SessionKey, expected int64, guard func(context.Context, db.Tx) error, change func(db.Tx, *CandidateSession) error) (CandidateSession, error) {
	if s == nil || s.Store == nil || key.Tenant == "" || key.Actor == "" || key.Query == "" || expected < 0 || change == nil {
		return CandidateSession{}, ErrInvalidRequest
	}
	var state CandidateSession
	err := s.Store.WithTx(ctx, func(tx db.Tx) error {
		// A write fence serializes a session on both SQLite and PostgreSQL.
		if _, err := tx.ExecContext(ctx, `INSERT INTO ontology_inference_sessions (tenant_id,actor_hash,query_hash,revision,run_id,state_json) VALUES (?,?,?,0,'','{"candidates":{}}') ON CONFLICT(tenant_id,actor_hash,query_hash) DO NOTHING`, key.Tenant, key.Actor, key.Query); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE ontology_inference_sessions SET revision=revision WHERE tenant_id=? AND actor_hash=? AND query_hash=? AND revision=?`, key.Tenant, key.Actor, key.Query, expected)
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
		var body string
		if err := tx.QueryRowContext(ctx, `SELECT state_json FROM ontology_inference_sessions WHERE tenant_id=? AND actor_hash=? AND query_hash=?`, key.Tenant, key.Actor, key.Query).Scan(&body); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(body), &state); err != nil {
			return err
		}
		if state.Candidates == nil {
			state.Candidates = map[string]CandidateState{}
		}
		if guard != nil {
			if err := guard(ctx, tx); err != nil {
				return err
			}
		}
		if err := change(tx, &state); err != nil {
			return err
		}
		state.Revision = expected + 1
		encoded, err := json.Marshal(state)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE ontology_inference_sessions SET revision=?,run_id=?,state_json=? WHERE tenant_id=? AND actor_hash=? AND query_hash=? AND revision=?`, state.Revision, state.RunID, string(encoded), key.Tenant, key.Actor, key.Query, expected)
		return err
	})
	return state, err
}

func SessionFor(run ReasoningRun) SessionKey {
	e := run.Request.Evaluation
	return SessionKey{Tenant: run.TenantID, Actor: e.ActorHash, Query: e.QueryHash}
}

func (s *LifecycleStore) Track(ctx context.Context, run ReasoningRun, expected int64, guard func(context.Context, db.Tx) error) ([]CandidateState, error) {
	e := run.Request.Evaluation
	if e == nil || e.Historical {
		return nil, ErrInvalidRequest
	}
	state, err := s.Update(ctx, SessionFor(run), expected, guard, func(_ db.Tx, current *CandidateSession) error {
		next := map[string]CandidateState{}
		for id, hash := range e.SupportHashes {
			old, ok := current.Candidates[id]
			if !ok || old.SupportHash != hash || !old.SupportPresent || old.Status == "withdrawn" {
				status := "proposed"
				if HasActiveAssertion(old) {
					status = "revision_required"
				}
				old = CandidateState{CandidateID: id, SupportHash: hash, SupportRevision: old.SupportRevision + 1, Revision: old.Revision + 1, Status: status, UpdatedAt: run.UpdatedAt, Receipt: old.Receipt, PreviousReceiptIDs: old.PreviousReceiptIDs}
			}
			old.SupportPresent = true
			old.RunID = run.ID
			next[id] = old
		}
		for id, old := range current.Candidates {
			if _, ok := next[id]; ok {
				continue
			}
			if old.SupportPresent {
				old.SupportPresent = false
				old.SupportRevision++
				old.Revision++
				old.Plan = nil
				old.DecisionBy = ""
				old.DecisionKind = ""
				old.DecisionReason = ""
			}
			if old.Status == "withdrawn" || old.Status == "revision_required" || old.Status == "retracted" || old.Status == "retraction_approved" {
				if old.Receipt != nil {
					old.RunID = run.ID
					next[id] = old
				}
				continue
			}
			old.Status = "withdrawn"
			if HasActiveAssertion(old) {
				old.Status = "revision_required"
			}
			old.Revision++
			old.Plan = nil
			old.DecisionReason = ""
			old.DecisionBy = ""
			old.DecisionKind = ""
			old.UpdatedAt = run.UpdatedAt
			old.RunID = run.ID
			next[id] = old
		}
		// Bound current metadata independently of the historical run ledger.
		if len(next) > 2*maxReasoningCandidates {
			return fmt.Errorf("%w: current candidate budget exceeded", ErrValidationFailed)
		}
		current.Candidates = next
		current.RunID = run.ID
		return nil
	})
	if err != nil {
		return nil, err
	}
	result := make([]CandidateState, 0, len(state.Candidates))
	for _, c := range state.Candidates {
		result = append(result, c)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CandidateID < result[j].CandidateID })
	return result, nil
}

func HasActiveAssertion(state CandidateState) bool {
	return state.Receipt != nil && state.Receipt.Effect != "retract"
}
