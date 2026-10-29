package ontologyreasoning

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/db"
)

func planDerivedLink(ctx context.Context, tx db.Tx, tenant string, state CandidateState, trusted WorkflowContext) (LinkPlan, error) {
	plan := LinkPlan{ID: "inference-plan:" + randomRunSuffix(), Actor: trusted.Actor, SupportHash: state.SupportHash, AuthorizationHash: trusted.AuthorizationHash, ExpiresAt: trusted.Now.Add(10 * time.Minute), Effect: "assert"}
	if state.Receipt != nil {
		owned, err := readOwnedInferenceLink(ctx, tx, tenant, state, trusted)
		if err != nil {
			return plan, err
		}
		plan.BeforeHash, plan.OwnedActionRunID = ownedLinkHash(owned), state.Receipt.ActionRunID
		plan.LinkType, plan.From, plan.To = owned.LinkType, ObjectRef{ID: owned.FromObjectID}, ObjectRef{ID: owned.ToObjectID}
		plan.Effect = "revise"
		if state.Status == "retraction_approved" {
			plan.Effect = "retract"
		}
		if state.Receipt.Effect == "retract" {
			plan.Effect = "reactivate"
		}
		for _, o := range trusted.Fresh.ContextPack.CandidateObjects {
			if o.ID == plan.From.ID {
				plan.From.Type = o.ObjectType
			}
			if o.ID == plan.To.ID {
				plan.To.Type = o.ObjectType
			}
		}
		if plan.Effect == "retract" {
			// Source loss may disconnect the endpoints from eligible premises.
			// The host still rechecks their current visibility before preserving
			// these identity-only refs; use them only to remove our owned link.
			for _, o := range trusted.Fresh.ContextPack.RetractionEndpoints {
				if o.ID == plan.From.ID {
					plan.From.Type = o.Type
				}
				if o.ID == plan.To.ID {
					plan.To.Type = o.Type
				}
			}
		}
		if plan.From.Type == "" || plan.To.Type == "" {
			return plan, ErrStateConflict
		}
	}
	if plan.Effect != "retract" {
		candidate, err := executableLink(trusted.Fresh, state.CandidateID)
		if err != nil {
			return plan, err
		}
		if state.Receipt != nil && (candidate.LinkType != plan.LinkType || candidate.From.ID != plan.From.ID || candidate.To.ID != plan.To.ID) {
			return plan, ErrStateConflict
		}
		plan.LinkType, plan.From, plan.To = candidate.LinkType, *candidate.From, *candidate.To
	}
	return plan, nil
}

func executeDerivedLink(ctx context.Context, tx db.Tx, run ReasoningRun, state CandidateState, req WorkflowRequest, trusted WorkflowContext) (LinkReceipt, error) {
	plan := state.Plan
	if plan.Effect != "retract" {
		candidate, err := executableLink(trusted.Fresh, state.CandidateID)
		if err != nil {
			return LinkReceipt{}, err
		}
		if candidate.LinkType != plan.LinkType || *candidate.From != plan.From || *candidate.To != plan.To {
			return LinkReceipt{}, ErrStateConflict
		}
	}
	if state.Receipt == nil {
		if plan.Effect != "assert" {
			return LinkReceipt{}, ErrStateConflict
		}
		receipt, err := assertDerivedLink(ctx, tx, run, state, req, trusted)
		receipt.Effect = "assert"
		return receipt, err
	}
	owned, err := readOwnedInferenceLink(ctx, tx, run.TenantID, state, trusted)
	if err != nil {
		return LinkReceipt{}, err
	}
	if plan.BeforeHash != ownedLinkHash(owned) || plan.OwnedActionRunID != state.Receipt.ActionRunID {
		return LinkReceipt{}, ErrStateConflict
	}
	receipt := LinkReceipt{Actor: trusted.Actor, ActorKind: string(trusted.ActorKind), ID: "inference-receipt:" + randomRunSuffix(), PlanID: plan.ID, IdempotencyKey: req.IdempotencyKey, ActionRunID: "inference-action:" + randomRunSuffix(), LinkID: owned.ID, AppliedAt: trusted.Now, Effect: plan.Effect}
	evidenceID := stableID("inference-evidence:", []string{run.TenantID, receipt.ID})
	graph := contextgraph.NewPlatformStoreTx(tx)
	if err := graph.SaveEvidence(ctx, contextgraph.Evidence{ID: evidenceID, TenantID: run.TenantID, SourceRef: contextgraph.Ref{Kind: "ontology_inference_run", ID: run.ID}, SourceAccessRef: trusted.SourceAccessRef, Summary: state.DecisionKind + "-approved " + plan.Effect + "; support " + state.SupportHash, ObservedAt: trusted.Now}); err != nil {
		return LinkReceipt{}, err
	}
	before := ownedLinkHash(owned)
	owned.Status, owned.UpdatedAt = "confirmed", trusted.Now
	owned.EvidenceRefs = []contextgraph.Ref{{Kind: "evidence", ID: evidenceID}}
	owned.SourceVersion++
	if plan.Effect == "retract" {
		owned.Status = "retracted"
		if err := graph.DeleteLink(ctx, run.TenantID, owned.ID, owned.SourceStreamID); err != nil {
			return LinkReceipt{}, err
		}
	} else if err := graph.SaveLink(ctx, owned); err != nil {
		return LinkReceipt{}, err
	}
	encoded, err := json.Marshal(ownedInferenceRecord{Link: owned, Owner: owned.SourceAccessRef, Stream: owned.SourceStreamID, Version: owned.SourceVersion})
	if err != nil {
		return LinkReceipt{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE ontology_inference_links SET support_hash=?,approved_by=?,action_run_id=?,link_json=? WHERE tenant_id=? AND id=? AND candidate_id=? AND action_run_id=?`, state.SupportHash, state.DecisionBy, receipt.ActionRunID, string(encoded), run.TenantID, owned.ID, state.CandidateID, state.Receipt.ActionRunID)
	if err != nil {
		return LinkReceipt{}, err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return LinkReceipt{}, ErrStateConflict
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
	_, _, err = actions.CompleteActionRun(ctx, actioncontract.CompleteRunRequest{RunID: actioncontract.ActionRunID(receipt.ActionRunID), ResultRef: actioncontract.Ref{Kind: "ontology_inference_assertion", ID: owned.ID}, EvidenceRefs: []actioncontract.Ref{{Kind: "evidence", ID: evidenceID}}, Writeback: &actioncontract.WritebackReceipt{ID: actioncontract.WritebackReceiptID(receipt.ID), TargetSystem: "ontology_inference", TargetResourceRefs: []actioncontract.ObjectRef{{Kind: "context_graph_link", ID: owned.ID}}, BeforeHash: before, AfterHash: ownedLinkHash(owned), ChangedFields: []string{"relationship", plan.Effect}, IdempotencyKey: req.IdempotencyKey, Status: actioncontract.WritebackStatusSucceeded}})
	return receipt, err
}

// Ownership requires the current receipt, assertion row and projection to agree.
// A changed or foreign projection is never overwritten, even with the same ID.
func readOwnedInferenceLink(ctx context.Context, tx db.Tx, tenant string, state CandidateState, trusted WorkflowContext) (contextgraph.Link, error) {
	var owned contextgraph.Link
	if state.Receipt == nil || state.Receipt.Actor != trusted.Actor {
		return owned, ErrStateConflict
	}
	var body, candidateID, actionID string
	err := tx.QueryRowContext(ctx, `SELECT candidate_id,action_run_id,link_json FROM ontology_inference_links WHERE tenant_id=? AND id=?`, tenant, state.Receipt.LinkID).Scan(&candidateID, &actionID, &body)
	if err != nil {
		return owned, err
	}
	if candidateID != state.CandidateID || actionID != state.Receipt.ActionRunID {
		return owned, ErrStateConflict
	}
	var record ownedInferenceRecord
	if err := json.Unmarshal([]byte(body), &record); err != nil {
		return owned, err
	}
	owned = record.Link
	owned.SourceAccessRef, owned.SourceStreamID, owned.SourceVersion = record.Owner, record.Stream, record.Version
	if owned.ID != state.Receipt.LinkID || owned.TenantID != tenant || owned.Provenance != (contextgraph.Ref{Kind: "ontology_inference_assertion", ID: owned.ID}) {
		return owned, ErrStateConflict
	}
	projected, err := readInferenceProjection(ctx, tx, tenant, owned.ID)
	if state.Receipt.Effect == "retract" {
		if !errors.Is(err, sql.ErrNoRows) || owned.Status != "retracted" {
			return owned, ErrStateConflict
		}
		if owned.SourceAccessRef != trusted.SourceAccessRef || owned.SourceStreamID != "ontology-inference:"+owned.ID || owned.SourceVersion < 1 {
			return owned, ErrStateConflict
		}
		return owned, nil
	}
	if err != nil {
		return owned, err
	}
	// Old assertion rows predate canonical temporal fields in their JSON. The
	// projection writer always filled these exact defaults from UpdatedAt.
	if owned.Temporal.Mode == "" {
		owned.Temporal.Mode = "static"
		owned.Temporal.SourceRecordedAt, owned.Temporal.OrreryIngestedAt = owned.UpdatedAt, owned.UpdatedAt
	}
	if hashJSON(projected) != hashJSON(owned) || projected.SourceStreamID != "ontology-inference:"+owned.ID || projected.SourceAccessRef != trusted.SourceAccessRef {
		return owned, ErrStateConflict
	}
	return projected, nil
}

type ownedInferenceRecord struct {
	contextgraph.Link
	Owner   contextgraph.AccessRef `json:"owner_access_ref"`
	Stream  string                 `json:"source_stream_id"`
	Version int64                  `json:"source_version"`
}

func ownedLinkHash(link contextgraph.Link) string {
	return hashJSON(struct {
		Link    contextgraph.Link
		Access  contextgraph.AccessRef
		Stream  string
		Version int64
	}{link, link.SourceAccessRef, link.SourceStreamID, link.SourceVersion})
}

func readInferenceProjection(ctx context.Context, tx db.Tx, tenant, id string) (contextgraph.Link, error) {
	link := contextgraph.Link{TenantID: tenant, ID: id}
	var properties, provenance, evidence, temporal string
	var updated int64
	err := tx.QueryRowContext(ctx, `SELECT link_type,from_object_id,to_object_id,properties_json,confidence,status,provenance_json,evidence_refs_json,source_access_kind,source_access_id,source_access_tenant_id,source_stream_id,source_version,temporal_json,updated_at FROM context_graph_links WHERE tenant_id=? AND id=?`, tenant, id).Scan(&link.LinkType, &link.FromObjectID, &link.ToObjectID, &properties, &link.Confidence, &link.Status, &provenance, &evidence, &link.SourceAccessRef.Kind, &link.SourceAccessRef.ID, &link.SourceAccessRef.TenantID, &link.SourceStreamID, &link.SourceVersion, &temporal, &updated)
	if err != nil {
		return link, err
	}
	for _, value := range []struct {
		raw    string
		target any
	}{{properties, &link.Properties}, {provenance, &link.Provenance}, {evidence, &link.EvidenceRefs}, {temporal, &link.Temporal}} {
		if err := json.Unmarshal([]byte(value.raw), value.target); err != nil {
			return link, err
		}
	}
	link.UpdatedAt = unixNanoToTime(updated)
	return link, nil
}

func saveInferenceReceipt(ctx context.Context, tx db.Tx, key SessionKey, candidateID string, receipt LinkReceipt) error {
	body, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ontology_inference_receipts (tenant_id,actor_hash,query_hash,idempotency_key,candidate_id,plan_id,receipt_json) VALUES (?,?,?,?,?,?,?)`, key.Tenant, key.Actor, key.Query, receipt.IdempotencyKey, candidateID, receipt.PlanID, string(body))
	return err
}

func replayInferenceReceipt(ctx context.Context, tx db.Tx, key SessionKey, req WorkflowRequest, actor string) (LinkReceipt, bool, error) {
	var body, candidateID, planID string
	err := tx.QueryRowContext(ctx, `SELECT candidate_id,plan_id,receipt_json FROM ontology_inference_receipts WHERE tenant_id=? AND actor_hash=? AND query_hash=? AND idempotency_key=?`, key.Tenant, key.Actor, key.Query, req.IdempotencyKey).Scan(&candidateID, &planID, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return LinkReceipt{}, false, nil
	}
	if err != nil {
		return LinkReceipt{}, false, err
	}
	var receipt LinkReceipt
	if err := json.Unmarshal([]byte(body), &receipt); err != nil {
		return receipt, false, err
	}
	if candidateID != req.CandidateID || planID != req.PlanID || receipt.Actor != actor {
		return receipt, false, ErrStateConflict
	}
	return receipt, true, nil
}
