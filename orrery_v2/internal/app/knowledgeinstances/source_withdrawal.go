package knowledgeinstances

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/db"
)

const WithdrawalActionID = "ontology.knowledge_instances.withdraw_source"

type SourceWithdrawal struct {
	EventID   string `json:"event_id"`
	Source    Source `json:"source"`
	EventType string `json:"event_type"`
	Sequence  int64  `json:"sequence"`
}

type WithdrawalReceipt struct {
	ID          string    `json:"id"`
	EventID     string    `json:"event_id"`
	InputHash   string    `json:"input_hash"`
	ActionRunID string    `json:"action_run_id"`
	WatchIDs    []string  `json:"watch_ids"`
	EntityIDs   []string  `json:"entity_ids"`
	ClaimCount  int       `json:"claim_count"`
	AppliedAt   time.Time `json:"applied_at"`
}

// WithdrawSourceTx is trusted source maintenance, not an agent/model tool.
// The host must fence a durable provider attestation in the same transaction.
// Pause, denied reads, and provider errors are never withdrawal attestations.
func (s *Store) WithdrawSourceTx(ctx context.Context, tx db.Tx, input SourceWithdrawal, guard func(context.Context, db.Tx) error) (WithdrawalReceipt, error) {
	var receipt WithdrawalReceipt
	if guard == nil || input.EventID == "" || input.Source.TenantID == "" || input.Source.KnowledgeBaseID == "" || input.Source.DocumentID == "" || input.Source.Generation < 1 || input.Source.Access.Kind == "" || input.Source.Access.ID == "" || input.Source.Access.TenantID == "" || input.Source.Content != "" || (input.EventType != "document.deleted" && input.EventType != "document.unpublished" && input.EventType != "document.missing") {
		return receipt, ErrInvalid
	}
	if err := guard(ctx, tx); err != nil {
		return receipt, err
	}
	if s.Core.Dialect() == db.DialectPostgres {
		if _, err := tx.ExecContext(ctx, `LOCK TABLE context_graph_objects,knowledge_instances,knowledge_instance_claims,ontology_document_batches,ontology_document_receipts,ontology_document_withdrawals IN SHARE ROW EXCLUSIVE MODE`); err != nil {
			return receipt, err
		}
	}
	var raw, inputHash string
	err := tx.QueryRowContext(ctx, `SELECT input_hash,body_json FROM ontology_document_withdrawals WHERE tenant_id=? AND event_id=?`, input.Source.TenantID, input.EventID).Scan(&inputHash, &raw)
	if err == nil {
		if inputHash != Digest(input) {
			return receipt, ErrConflict
		}
		err = json.Unmarshal([]byte(raw), &receipt)
		return receipt, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return receipt, err
	}
	removed, err := s.withdrawalClaims(ctx, tx, input.Source)
	if err != nil {
		return receipt, err
	}
	before := map[string]int64{}
	roots := map[string]string{}
	for _, c := range removed {
		before[c.Claim.EntityID] = 1
		if c.Claim.TargetID != "" {
			before[c.Claim.TargetID] = 1
		}
		roots[c.WatchID] = c.Claim.Source.RootID
	}
	for id := range before {
		var version int64
		if err := tx.QueryRowContext(ctx, `SELECT version FROM knowledge_instances WHERE tenant_id=? AND id=?`, input.Source.TenantID, id).Scan(&version); err != nil {
			return receipt, err
		}
		before[id] = version
	}
	plan := Plan{Source: input.Source, Before: before}
	entities, err := s.checkEntities(ctx, tx, plan)
	if err != nil {
		return receipt, err
	}
	all, err := ownedClaims(ctx, tx.QueryContext, input.Source.TenantID, before)
	if err != nil {
		return receipt, err
	}
	if err := checkConflictPreview(ctx, tx, plan, all); err != nil {
		return receipt, err
	}
	graph := contextgraph.NewPlatformStoreTx(tx)
	now := time.Now().UTC()
	for _, c := range removed {
		stream := "knowledge-document:" + input.Source.TenantID + ":" + c.WatchID
		if err := closeClaimHistory(ctx, tx, input.Source.TenantID, c.Claim.ID, stream, c.Claim.TargetID != "", now); err != nil {
			return receipt, err
		}
		if c.Claim.TargetID != "" {
			err = graph.DeleteLink(ctx, input.Source.TenantID, c.Claim.ID, stream)
		} else {
			err = graph.DeleteFact(ctx, input.Source.TenantID, c.Claim.ID, stream)
		}
		if err != nil {
			return receipt, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM knowledge_instance_claims WHERE tenant_id=? AND watch_id=? AND id=?`, input.Source.TenantID, c.WatchID, c.Claim.ID); err != nil {
			return receipt, err
		}
	}
	remaining, err := ownedClaims(ctx, tx.QueryContext, input.Source.TenantID, before)
	if err != nil {
		return receipt, err
	}
	owners := map[string]string{}
	for _, c := range remaining {
		owners[c.Claim.ID] = c.WatchID
	}
	// Re-aggregate from surviving sources and clear every old resolution before
	// asking models to resolve any remaining multi-source conflict again.
	batch := Batch{TenantID: input.Source.TenantID, Decision: &Decision{}}
	for id, e := range entities {
		for watch, root := range roots {
			link := stableID("knowledge-document-entity:", []any{input.Source.TenantID, watch, id})
			stream := "knowledge-document:" + input.Source.TenantID + ":" + watch
			var owner, typ, from, to string
			err := tx.QueryRowContext(ctx, `SELECT source_stream_id,link_type,from_object_id,to_object_id FROM context_graph_links WHERE tenant_id=? AND id=?`, input.Source.TenantID, link).Scan(&owner, &typ, &from, &to)
			if err == nil && (owner != stream || typ != DocumentLink || from != root || to != id) {
				return receipt, ErrConflict
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return receipt, err
			}
			if err := closeClaimHistory(ctx, tx, input.Source.TenantID, link, stream, true, now); err != nil {
				return receipt, err
			}
			if err := graph.DeleteLink(ctx, input.Source.TenantID, link, stream); err != nil {
				return receipt, err
			}
		}
		claims, err := claimsTx(ctx, tx, input.Source.TenantID, "entity_id", id)
		if err != nil {
			return receipt, err
		}
		e, err = s.adjudicateEntity(ctx, tx, batch, e, claims, owners, "", now)
		if err != nil {
			return receipt, err
		}
		e.Version++
		object := contextgraph.Object{ID: e.ID, TenantID: e.TenantID, ObjectType: EntityType, CanonicalKey: EntityType + ":" + e.ID, Label: e.Name, Properties: e.Properties, Status: e.Status, Sensitivity: "internal", Confidence: 1, Provenance: contextgraph.Ref{Kind: Domain, ID: e.ID}, OwnerAccessRef: e.Access, SourceStreamID: "knowledge-instance:" + e.ID, SourceVersion: e.Version, UpdatedAt: now}
		if err := graph.SaveObject(ctx, object); err != nil {
			return receipt, err
		}
		e.ProjectionHash, err = projectionHash(ctx, tx, "context_graph_objects", e.TenantID, e.ID, object.SourceStreamID)
		if err != nil {
			return receipt, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE knowledge_instances SET version=?,body_json=? WHERE tenant_id=? AND id=?`, e.Version, body(e), e.TenantID, e.ID); err != nil {
			return receipt, err
		}
		if err := syncIdentityIndexTx(ctx, tx, e, ""); err != nil {
			return receipt, err
		}
	}
	for watch := range roots {
		if _, err := tx.ExecContext(ctx, `DELETE FROM ontology_document_boundaries WHERE tenant_id=? AND watch_id=?`, input.Source.TenantID, watch); err != nil {
			return receipt, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE ontology_document_watches SET observed_stamp='',next_check_at=0 WHERE tenant_id=? AND id=?`, input.Source.TenantID, watch); err != nil {
			return receipt, err
		}
		if err := supersedeSourceBatches(ctx, tx, input.Source.TenantID, watch); err != nil {
			return receipt, err
		}
	}
	if err := queueSurvivingSources(ctx, tx, input, remaining, roots); err != nil {
		return receipt, err
	}
	receipt = WithdrawalReceipt{ID: stableID("knowledge-withdrawal-receipt:", input.EventID), EventID: input.EventID, InputHash: Digest(input), ActionRunID: stableID("knowledge-withdrawal-action:", input.EventID), ClaimCount: len(removed), AppliedAt: now}
	for watch := range roots {
		receipt.WatchIDs = append(receipt.WatchIDs, watch)
	}
	for id := range entities {
		receipt.EntityIDs = append(receipt.EntityIDs, id)
	}
	sort.Strings(receipt.WatchIDs)
	sort.Strings(receipt.EntityIDs)
	if err := completeWithdrawal(ctx, tx, input, receipt, removed); err != nil {
		return receipt, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ontology_document_withdrawals(tenant_id,event_id,input_hash,body_json) VALUES(?,?,?,?)`, input.Source.TenantID, input.EventID, receipt.InputHash, body(receipt))
	return receipt, err
}

func (s *Store) withdrawalClaims(ctx context.Context, tx db.Tx, source Source) ([]SourceClaim, error) {
	// New claims use the document index; older claims are upgraded lazily from
	// their immutable applied batches, without a lossy or destructive migration.
	rows, err := tx.QueryContext(ctx, `SELECT c.watch_id,c.body_json FROM knowledge_instance_claims c LEFT JOIN knowledge_instance_claim_sources s ON s.tenant_id=c.tenant_id AND s.claim_id=c.id WHERE c.tenant_id=? AND ((s.knowledge_base_id=? AND s.document_id=?) OR s.claim_id IS NULL) ORDER BY c.watch_id,c.id`, source.TenantID, source.KnowledgeBaseID, source.DocumentID)
	if err != nil {
		return nil, err
	}
	var all []SourceClaim
	for rows.Next() {
		var c SourceClaim
		var raw string
		if err := rows.Scan(&c.WatchID, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &c.Claim); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	legacy := map[string]*Source{}
	var result []SourceClaim
	for _, c := range all {
		if c.Claim.Source == nil {
			if _, ok := legacy[c.WatchID]; !ok {
				var raw string
				if err := tx.QueryRowContext(ctx, `SELECT body_json FROM ontology_document_batches WHERE tenant_id=? AND watch_id=? AND status='applied' ORDER BY created_at DESC,id DESC LIMIT 1`, source.TenantID, c.WatchID).Scan(&raw); err != nil {
					return nil, ErrConflict
				}
				var b Batch
				if err := json.Unmarshal([]byte(raw), &b); err != nil {
					return nil, err
				}
				b.Plan.Source.Content = ""
				legacy[c.WatchID] = &b.Plan.Source
			}
			c.Claim.Source = legacy[c.WatchID]
		}
		v := c.Claim.Source
		if err := indexClaimSource(ctx, tx, source.TenantID, c.Claim.ID, *v); err != nil {
			return nil, err
		}
		if v.TenantID != source.TenantID || v.KnowledgeBaseID != source.KnowledgeBaseID || v.DocumentID != source.DocumentID {
			continue
		}
		if v.Access != source.Access || (source.RootID != "" && source.RootID != v.RootID) || v.Generation > source.Generation || (source.Revision > 0 && v.Revision > source.Revision) {
			return nil, ErrConflict
		}
		var raw string
		if err := tx.QueryRowContext(ctx, `SELECT body_json FROM ontology_document_watches WHERE tenant_id=? AND id=?`, source.TenantID, c.WatchID).Scan(&raw); err != nil {
			return nil, ErrConflict
		}
		var w Watch
		if err := json.Unmarshal([]byte(raw), &w); err != nil {
			return nil, err
		}
		if w.RootID != v.RootID || w.TenantID != source.TenantID {
			return nil, ErrConflict
		}
		result = append(result, c)
	}
	return result, nil
}

func indexClaimSource(ctx context.Context, tx db.Tx, tenant, claim string, source Source) error {
	if source.TenantID != tenant || source.KnowledgeBaseID == "" || source.DocumentID == "" {
		return ErrConflict
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO knowledge_instance_claim_sources(tenant_id,claim_id,knowledge_base_id,document_id) VALUES(?,?,?,?) ON CONFLICT(tenant_id,claim_id) DO UPDATE SET knowledge_base_id=excluded.knowledge_base_id,document_id=excluded.document_id`, tenant, claim, source.KnowledgeBaseID, source.DocumentID)
	return err
}

func supersedeSourceBatches(ctx context.Context, tx db.Tx, tenant, watch string) error {
	rows, err := tx.QueryContext(ctx, `SELECT body_json FROM ontology_document_batches WHERE tenant_id=? AND watch_id=? AND status IN ('prepared','reviewed')`, tenant, watch)
	if err != nil {
		return err
	}
	var batches []Batch
	for rows.Next() {
		var raw string
		var b Batch
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		if err := json.Unmarshal([]byte(raw), &b); err != nil {
			rows.Close()
			return err
		}
		batches = append(batches, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, b := range batches {
		b.Status = "superseded"
		if _, err := tx.ExecContext(ctx, `UPDATE ontology_document_batches SET status=?,body_json=? WHERE tenant_id=? AND id=?`, b.Status, body(b), tenant, b.ID); err != nil {
			return err
		}
	}
	return nil
}

func queueSurvivingSources(ctx context.Context, tx db.Tx, input SourceWithdrawal, remaining []SourceClaim, removed map[string]string) error {
	seen := map[string]bool{}
	for _, c := range remaining {
		if seen[c.WatchID] || removed[c.WatchID] != "" {
			continue
		}
		seen[c.WatchID] = true
		var raw string
		if err := tx.QueryRowContext(ctx, `SELECT body_json FROM ontology_document_watches WHERE tenant_id=? AND id=?`, input.Source.TenantID, c.WatchID).Scan(&raw); err != nil {
			return err
		}
		var w Watch
		if err := json.Unmarshal([]byte(raw), &w); err != nil {
			return err
		}
		if w.Active {
			if err := enqueueDocumentJob(ctx, tx, DocumentTopic, stableID("document-source-withdrawal:", []any{input.EventID, w.ID, w.Version}), DocumentJob{TenantID: w.TenantID, WatchID: w.ID, Version: w.Version}); err != nil {
				return err
			}
		}
	}
	return nil
}

func completeWithdrawal(ctx context.Context, tx db.Tx, input SourceWithdrawal, r WithdrawalReceipt, removed []SourceClaim) error {
	svc := actioncontract.NewService(actioncontract.NewPlatformRepositoryTx(tx), actioncontract.WithClock(func() time.Time { return r.AppliedAt }))
	var objects []actioncontract.ObjectRef
	if input.Source.RootID != "" {
		objects = append(objects, actioncontract.ObjectRef{Kind: "knowledge_document", ID: input.Source.RootID})
	}
	for _, id := range r.EntityIDs {
		objects = append(objects, actioncontract.ObjectRef{Kind: EntityType, ID: id})
	}
	evidence := []actioncontract.Ref{{Kind: "knowledge_source_withdrawal", ID: input.EventID}}
	_, err := svc.StartActionRun(ctx, actioncontract.StartRunRequest{RunID: actioncontract.ActionRunID(r.ActionRunID), TenantID: input.Source.TenantID, ActionTypeID: actioncontract.ActionTypeID(WithdrawalActionID), Actor: actioncontract.ActionSubject{Kind: actioncontract.SubjectWorker, ID: "knowledge-source-lifecycle"}, Runtime: actioncontract.RuntimeRef{Kind: actioncontract.RuntimeWorker, ID: Domain}, Purpose: Domain, ObjectRefs: objects, ArgsHash: r.InputHash, EvidenceRefs: evidence})
	if err != nil {
		return err
	}
	_, _, err = svc.CompleteActionRun(ctx, actioncontract.CompleteRunRequest{RunID: actioncontract.ActionRunID(r.ActionRunID), ResultRef: actioncontract.Ref{Kind: "ontology_document_withdrawal", ID: r.EventID}, EvidenceRefs: []actioncontract.Ref{{Kind: "ontology_document_withdrawal_receipt", ID: r.ID}}, Writeback: &actioncontract.WritebackReceipt{ID: actioncontract.WritebackReceiptID(r.ID), TargetSystem: Domain, TargetResourceRefs: objects, BeforeHash: Digest(removed), AfterHash: Digest(r.EntityIDs), ChangedFields: []string{"source_assertions", "knowledge_instances", "fact_adjudications"}, IdempotencyKey: input.EventID, Status: actioncontract.WritebackStatusSucceeded}})
	return err
}
