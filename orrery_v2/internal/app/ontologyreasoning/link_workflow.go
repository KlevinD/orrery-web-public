package ontologyreasoning

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform/db"
)

// WorkflowContext is supplied only by the authenticated host. Fresh is a new
// evaluation using current policy-filtered inputs, never a caller's candidate.
type WorkflowContext struct {
	Actor             string
	ActorID           string
	ActorKind         actioncontract.SubjectKind
	AuthorizationHash string
	Fresh             ReasoningRun
	SourceAccessRef   contextgraph.AccessRef
	Guard             func(context.Context, db.Tx) error
	Now               time.Time
}

func (s *LifecycleStore) Workflow(ctx context.Context, operation string, req WorkflowRequest, original ReasoningRun, trusted WorkflowContext) (CandidateState, error) {
	if original.Request.Evaluation == nil || original.Request.Evaluation.Historical || (trusted.Fresh.Request.Evaluation != nil && trusted.Fresh.Request.Evaluation.Historical) {
		return CandidateState{}, ErrInvalidRequest
	}
	if trusted.ActorKind == "" {
		trusted.ActorKind = actioncontract.SubjectHuman
	}
	if trusted.ActorKind != actioncontract.SubjectHuman && trusted.ActorKind != actioncontract.SubjectAgent {
		return CandidateState{}, ErrInvalidRequest
	}
	if original.Request.Evaluation == nil || req.TenantID != original.TenantID || req.RunID != original.ID || trusted.Actor == "" || trusted.AuthorizationHash == "" || trusted.Guard == nil || trusted.Now.IsZero() || req.CandidateID == "" || req.ExpectedRevision < 1 || req.SupportHash == "" || len(req.Reason) > 2000 {
		return CandidateState{}, ErrInvalidRequest
	}
	if operation != "review" && operation != "plan" && operation != "apply" {
		return CandidateState{}, ErrInvalidRequest
	}
	key := SessionFor(original)
	current, err := s.Get(ctx, key)
	if err != nil {
		return CandidateState{}, err
	}
	var result CandidateState
	stale := false
	_, err = s.Update(ctx, key, current.Revision, trusted.Guard, func(tx db.Tx, session *CandidateSession) error {
		state, ok := session.Candidates[req.CandidateID]
		if !ok {
			return ErrNotFound
		}
		if operation == "apply" && req.IdempotencyKey != "" {
			receipt, found, err := replayInferenceReceipt(ctx, tx, key, req, trusted.Actor)
			if err != nil {
				return err
			}
			if found {
				result = CandidateState{CandidateID: state.CandidateID, Revision: state.Revision, Status: state.Status, SupportHash: state.SupportHash, RunID: state.RunID, Receipt: &receipt}
				return nil
			}
		}
		// Replays return the committed receipt even if the caller lost the reply.
		if operation == "apply" && state.Receipt != nil && state.Receipt.PlanID == req.PlanID && state.Receipt.IdempotencyKey == req.IdempotencyKey && state.Receipt.Actor == trusted.Actor {
			result = state
			result.Plan = nil
			// Current permission is sufficient to retrieve this opaque receipt,
			// but never to replay historical free-text review notes or a preview.
			result.DecisionBy = ""
			result.DecisionKind = ""
			result.DecisionReason = ""
			return nil
		}
		if state.Revision != req.ExpectedRevision || state.SupportHash != req.SupportHash {
			return ErrStateConflict
		}
		freshHash := ""
		if trusted.Fresh.Request.Evaluation != nil {
			freshHash = trusted.Fresh.Request.Evaluation.SupportHashes[req.CandidateID]
		}
		retracting := (operation == "review" && req.Decision == "retract") || state.Status == "retraction_approved"
		if freshHash != state.SupportHash && !(retracting && HasActiveAssertion(state) && freshHash == "") {
			state.Status = "withdrawn"
			if HasActiveAssertion(state) {
				state.Status = "revision_required"
			}
			state.Plan = nil
			state.DecisionBy = ""
			state.DecisionKind = ""
			state.DecisionReason = ""
			state.Revision++
			state.UpdatedAt = trusted.Now
			session.Candidates[state.CandidateID] = state
			stale = true
			return nil
		}
		switch operation {
		case "review":
			if req.Decision != "approve" && req.Decision != "reject" && req.Decision != "retract" {
				return ErrInvalidRequest
			}
			if state.Status == "withdrawn" || (req.Decision == "retract" && !HasActiveAssertion(state)) || (req.Decision == "reject" && HasActiveAssertion(state)) {
				return ErrStateConflict
			}
			state.Status = "approved"
			if req.Decision == "reject" {
				state.Status = "rejected"
			}
			if req.Decision == "retract" {
				state.Status = "retraction_approved"
			}
			state.DecisionBy = trusted.ActorID
			if state.DecisionBy == "" {
				state.DecisionBy = trusted.Actor
			}
			state.DecisionReason = strings.TrimSpace(req.Reason)
			state.DecisionKind = string(trusted.ActorKind)
			state.Plan = nil
		case "plan":
			if state.Status != "approved" && state.Status != "retraction_approved" {
				return ErrStateConflict
			}
			plan, err := planDerivedLink(ctx, tx, original.TenantID, state, trusted)
			if err != nil {
				return err
			}
			state.Plan = &plan
		case "apply":
			if !req.Confirmed || strings.TrimSpace(req.IdempotencyKey) == "" || len(req.IdempotencyKey) > 128 {
				return ErrInvalidRequest
			}
			if (state.Status != "approved" && state.Status != "retraction_approved") || state.Plan == nil || state.Plan.ID != req.PlanID || state.Plan.Actor != trusted.Actor || state.Plan.AuthorizationHash != trusted.AuthorizationHash || state.Plan.SupportHash != state.SupportHash || !trusted.Now.Before(state.Plan.ExpiresAt) {
				return ErrStateConflict
			}
			receipt, err := executeDerivedLink(ctx, tx, original, state, req, trusted)
			if err != nil {
				return err
			}
			if err := saveInferenceReceipt(ctx, tx, key, state.CandidateID, receipt); err != nil {
				return err
			}
			if state.Receipt != nil {
				state.PreviousReceiptIDs = append(state.PreviousReceiptIDs, state.Receipt.ID)
				if len(state.PreviousReceiptIDs) > 32 {
					state.PreviousReceiptIDs = state.PreviousReceiptIDs[len(state.PreviousReceiptIDs)-32:]
				}
			}
			state.Receipt = &receipt
			state.Status = "applied"
			if receipt.Effect == "retract" {
				state.Status = "retracted"
			}
			state.Plan = nil
		}
		state.Revision++
		state.UpdatedAt = trusted.Now
		session.Candidates[state.CandidateID] = state
		result = state
		return nil
	})
	if err != nil {
		return CandidateState{}, err
	}
	if stale {
		return CandidateState{}, ErrStateConflict
	}
	return result, nil
}

func executableLink(run ReasoningRun, id string) (Candidate, error) {
	if run.Request.Evaluation == nil || len(run.Request.Evaluation.Documents) > 0 {
		return Candidate{}, fmt.Errorf("%w: document candidates require attested source versions before execution", ErrValidationFailed)
	}
	for _, c := range run.Candidates {
		if c.ID != id {
			continue
		}
		if c.Kind != CandidateLink || c.From == nil || c.To == nil {
			return Candidate{}, fmt.Errorf("%w: only existing-object relationship assertions are executable", ErrValidationFailed)
		}
		for _, l := range run.ContextPack.AllowedLinkTypes {
			if l.ID == c.LinkType && definitionVisibleToTenant(l.TenantID, run.TenantID) && l.SuggestedAllowed && l.Cardinality == ontology.CardinalityManyToMany && slices.Contains(l.AllowedActions, DerivedLinkActionID) {
				return c, nil
			}
		}
		return Candidate{}, fmt.Errorf("%w: relation type must explicitly allow the derived-link command", ErrValidationFailed)
	}
	return Candidate{}, ErrNotFound
}

// This command owns its assertion and projection. It never updates an edge
// owned by a business source or writes a business object's canonical fields.
func assertDerivedLink(ctx context.Context, tx db.Tx, run ReasoningRun, state CandidateState, req WorkflowRequest, trusted WorkflowContext) (LinkReceipt, error) {
	if !trusted.SourceAccessRef.Resolved() {
		return LinkReceipt{}, ErrInvalidRequest
	}
	plan := state.Plan
	id := stableID("inference-link:", []string{run.TenantID, state.CandidateID})
	var existing string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM ontology_inference_links WHERE tenant_id=? AND id=?`, run.TenantID, id).Scan(&existing); err == nil {
		return LinkReceipt{}, ErrStateConflict
	} else if err != sql.ErrNoRows {
		return LinkReceipt{}, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM context_graph_links WHERE tenant_id=? AND id=?`, run.TenantID, id).Scan(&existing); err == nil {
		return LinkReceipt{}, ErrStateConflict
	} else if err != sql.ErrNoRows {
		return LinkReceipt{}, err
	}
	receipt := LinkReceipt{Actor: trusted.Actor, ActorKind: string(trusted.ActorKind), ID: "inference-receipt:" + randomRunSuffix(), PlanID: plan.ID, IdempotencyKey: req.IdempotencyKey, ActionRunID: "inference-action:" + randomRunSuffix(), LinkID: id, AppliedAt: trusted.Now}
	evidenceID := stableID("inference-evidence:", []string{run.TenantID, receipt.ID})
	graph := contextgraph.NewPlatformStoreTx(tx)
	if err := graph.SaveEvidence(ctx, contextgraph.Evidence{ID: evidenceID, TenantID: run.TenantID, SourceRef: contextgraph.Ref{Kind: "ontology_inference_run", ID: run.ID}, SourceAccessRef: trusted.SourceAccessRef, Summary: state.DecisionKind + "-approved inference; support " + state.SupportHash, ObservedAt: trusted.Now}); err != nil {
		return LinkReceipt{}, err
	}
	link := contextgraph.Link{ID: id, TenantID: run.TenantID, LinkType: plan.LinkType, FromObjectID: plan.From.ID, ToObjectID: plan.To.ID, Status: "confirmed", Provenance: contextgraph.Ref{Kind: "ontology_inference_assertion", ID: id}, EvidenceRefs: []contextgraph.Ref{{Kind: "evidence", ID: evidenceID}}, SourceAccessRef: trusted.SourceAccessRef, SourceStreamID: "ontology-inference:" + id, SourceVersion: 1, UpdatedAt: trusted.Now}
	body, err := json.Marshal(link)
	if err != nil {
		return LinkReceipt{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO ontology_inference_links (tenant_id,id,candidate_id,support_hash,approved_by,action_run_id,link_json) VALUES (?,?,?,?,?,?,?)`, run.TenantID, id, state.CandidateID, state.SupportHash, state.DecisionBy, receipt.ActionRunID, string(body)); err != nil {
		return LinkReceipt{}, err
	}
	actions := actioncontract.NewService(actioncontract.NewPlatformRepositoryTx(tx), actioncontract.WithClock(func() time.Time { return trusted.Now }))
	actorID := trusted.ActorID
	if actorID == "" {
		actorID = trusted.Actor
	}
	runtimeKind := actioncontract.RuntimeWebApp
	if trusted.ActorKind == actioncontract.SubjectAgent {
		runtimeKind = actioncontract.RuntimeAgent
	}
	_, err = actions.StartActionRun(ctx, actioncontract.StartRunRequest{RunID: actioncontract.ActionRunID(receipt.ActionRunID), TenantID: run.TenantID, ActionTypeID: actioncontract.ActionTypeID(DerivedLinkActionID), Actor: actioncontract.ActionSubject{Kind: trusted.ActorKind, ID: actorID}, Runtime: actioncontract.RuntimeRef{Kind: runtimeKind, ID: "ontology_inference"}, Approval: actioncontract.Ref{Kind: "ontology_inference_approval", ID: state.CandidateID + ":" + state.SupportHash}, Purpose: "ontology_inference", ObjectRefs: []actioncontract.ObjectRef{{Kind: plan.From.Type, ID: plan.From.ID}, {Kind: plan.To.Type, ID: plan.To.ID}}, ArgsHash: hashJSON(plan), EvidenceRefs: []actioncontract.Ref{{Kind: "ontology_inference_run", ID: run.ID}}})
	if err != nil {
		return LinkReceipt{}, err
	}
	if err := graph.SaveLink(ctx, link); err != nil {
		return LinkReceipt{}, err
	}
	_, _, err = actions.CompleteActionRun(ctx, actioncontract.CompleteRunRequest{RunID: actioncontract.ActionRunID(receipt.ActionRunID), ResultRef: actioncontract.Ref{Kind: "context_graph_link", ID: id}, EvidenceRefs: []actioncontract.Ref{{Kind: "evidence", ID: evidenceID}}, Writeback: &actioncontract.WritebackReceipt{ID: actioncontract.WritebackReceiptID(receipt.ID), TargetSystem: "ontology_inference", TargetResourceRefs: []actioncontract.ObjectRef{{Kind: "context_graph_link", ID: id}}, AfterHash: hashJSON(link), ChangedFields: []string{"relationship"}, IdempotencyKey: req.IdempotencyKey, Status: actioncontract.WritebackStatusSucceeded}})
	return receipt, err
}
