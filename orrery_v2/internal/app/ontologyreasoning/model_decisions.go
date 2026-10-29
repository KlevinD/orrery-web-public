package ontologyreasoning

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"orrery_v2/internal/platform/db"
)

type ModelDecision struct {
	Decision  string   `json:"decision"`
	Reason    string   `json:"reason"`
	ModelID   string   `json:"model_id"`
	InputHash string   `json:"input_hash"`
	Preview   LinkPlan `json:"preview"`
}

func (s *RuleSetStore) GetDecision(ctx context.Context, jobID, tenant string) (ModelDecision, bool, error) {
	var body string
	err := s.Store.QueryRow(ctx, `SELECT decision_json FROM ontology_inference_decisions WHERE job_id=? AND tenant_id=?`, jobID, tenant).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return ModelDecision{}, false, nil
	}
	if err != nil {
		return ModelDecision{}, false, err
	}
	var decision ModelDecision
	err = json.Unmarshal([]byte(body), &decision)
	return decision, true, err
}

func (s *RuleSetStore) SaveDecision(ctx context.Context, jobID string, job InferenceJob, decision ModelDecision, guard func(context.Context, db.Tx) error) (ModelDecision, error) {
	if decision.ModelID == "" || decision.InputHash == "" || decision.Reason == "" || len(decision.Reason) > 2000 || guard == nil {
		return ModelDecision{}, ErrInvalidRequest
	}
	if decision.Decision != "approve" && decision.Decision != "reject" && decision.Decision != "retract" && decision.Decision != "defer" {
		return ModelDecision{}, ErrInvalidRequest
	}
	body, err := json.Marshal(decision)
	if err != nil {
		return ModelDecision{}, err
	}
	err = s.Store.WithTx(ctx, func(tx db.Tx) error {
		if err := guard(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO ontology_inference_decisions(job_id,tenant_id,rule_set_id,candidate_id,support_hash,decision_json,created_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(job_id) DO NOTHING`, jobID, job.TenantID, job.RuleSetID, job.CandidateID, job.SupportHash, string(body), time.Now().UTC().UnixNano())
		return err
	})
	if err != nil {
		return ModelDecision{}, err
	}
	saved, _, err := s.GetDecision(ctx, jobID, job.TenantID)
	return saved, err
}

func (s *LifecycleStore) Preview(ctx context.Context, run ReasoningRun, state CandidateState, trusted WorkflowContext) (LinkPlan, error) {
	var preview LinkPlan
	if run.Request.Evaluation == nil || run.Request.Evaluation.Historical {
		return preview, ErrInvalidRequest
	}
	err := s.Store.WithTx(ctx, func(tx db.Tx) error {
		if trusted.Guard == nil {
			return ErrInvalidRequest
		}
		if err := trusted.Guard(ctx, tx); err != nil {
			return err
		}
		if HasActiveAssertion(state) && run.Request.Evaluation.SupportHashes[state.CandidateID] == "" {
			state.Status = "retraction_approved"
		} else {
			state.Status = "approved"
		}
		var err error
		preview, err = planDerivedLink(ctx, tx, run.TenantID, state, trusted)
		return err
	})
	preview.ID = ""
	preview.Actor = ""
	preview.AuthorizationHash = ""
	preview.ExpiresAt = time.Time{}
	return preview, err
}
