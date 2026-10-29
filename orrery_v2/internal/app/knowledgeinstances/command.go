package knowledgeinstances

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/db"
)

type CommitContext struct {
	AgentID string
	Now     time.Time
	Guard   func(context.Context, db.Tx) error
}

// Apply is the domain command. Identities, source claims, graph projections,
// action run and durable receipt share the same database transaction.
func (s *Store) Apply(ctx context.Context, b Batch, trusted CommitContext) (Receipt, error) {
	var receipt Receipt
	if trusted.Guard == nil || trusted.AgentID == "" || trusted.AgentID != b.Plan.Watch.AgentID || b.Decision == nil ||
		b.Decision.Decision != "approve" || b.Decision.PlanHash != Digest(b.Plan) || b.ID != b.Plan.BatchID || b.TenantID != b.Plan.Source.TenantID {
		return receipt, ErrInvalid
	}
	if trusted.Now.IsZero() {
		trusted.Now = time.Now().UTC()
	}
	if err := ValidateResolutionChoices(b.Plan, *b.Decision); err != nil {
		return receipt, err
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
		err := tx.QueryRowContext(ctx, `SELECT body_json FROM ontology_document_receipts WHERE tenant_id=? AND batch_id=?`, b.TenantID, b.ID).Scan(&raw)
		if err == nil {
			if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
				return err
			}
			if receipt.PlanHash != Digest(b.Plan) {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if b.Plan.TemporalProtocol != TemporalProtocol {
			return ErrConflict
		}
		if trusted.Now.Sub(b.Plan.CreatedAt) > 10*time.Minute || b.Plan.CreatedAt.After(trusted.Now.Add(time.Minute)) {
			return ErrConflict
		}
		if err := (&WatchStore{Store: s.Core}).CheckActiveTx(ctx, tx, b.TenantID, b.WatchID, trusted.AgentID, b.Plan.Watch.Version); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT body_json FROM ontology_document_batches WHERE tenant_id=? AND id=? AND status='reviewed'`, b.TenantID, b.ID).Scan(&raw); err != nil {
			return ErrConflict
		}
		var persisted Batch
		if err := json.Unmarshal([]byte(raw), &persisted); err != nil {
			return err
		}
		if Digest(persisted) != Digest(b) {
			return ErrConflict
		}
		entities, err := s.checkEntities(ctx, tx, b.Plan)
		if err != nil {
			return fmt.Errorf("entity fence: %w", err)
		}
		if err := s.checkIdentityAssignments(ctx, tx, b.Plan, entities); err != nil {
			return fmt.Errorf("identity fence: %w", err)
		}
		owned, err := ownedClaims(ctx, tx.QueryContext, b.TenantID, b.Plan.Before)
		if err != nil {
			return err
		}
		if err := checkConflictPreview(ctx, tx, b.Plan, owned); err != nil {
			return fmt.Errorf("claim fence: %w", err)
		}
		if !sameActiveSet(b.Plan, owned, trusted.Now) {
			return ErrConflict
		}
		previous, err := claimsTx(ctx, tx, b.TenantID, "watch_id", b.WatchID)
		if err != nil {
			return err
		}
		if Digest(previous) != Digest(b.Plan.Previous) {
			return fmt.Errorf("previous claim set: %w", ErrConflict)
		}
		graph := contextgraph.NewPlatformStoreTx(tx)
		stream := claimStream(b.Plan)
		// Establish new endpoint identities before writing FK-constrained links.
		// These placeholders and their final properties are in the same TX.
		for _, e := range entities {
			if e.Version == 0 {
				if err := graph.SaveObject(ctx, contextgraph.Object{ID: e.ID, TenantID: e.TenantID, ObjectType: EntityType, CanonicalKey: EntityType + ":" + e.ID, Label: e.Name, Properties: map[string]any{}, Status: "active", OwnerAccessRef: e.Access, SourceStreamID: "knowledge-instance:" + e.ID, SourceVersion: 1, UpdatedAt: trusted.Now}); err != nil {
					return err
				}
			}
		}
		for _, claim := range previous {
			table := "context_graph_facts"
			if claim.TargetID != "" {
				table = "context_graph_links"
			}
			current, err := projectionHash(ctx, tx, table, b.TenantID, claim.ID, stream)
			if err != nil || current != claim.ProjectionHash {
				return ErrConflict
			}
			if err := closeClaimHistory(ctx, tx, b.TenantID, claim.ID, stream, claim.TargetID != "", trusted.Now); err != nil {
				return err
			}
			if claim.TargetID != "" {
				err = graph.DeleteLink(ctx, b.TenantID, claim.ID, stream)
			} else {
				err = graph.DeleteFact(ctx, b.TenantID, claim.ID, stream)
			}
			if err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM knowledge_instance_claims WHERE tenant_id=? AND watch_id=?`, b.TenantID, b.WatchID); err != nil {
			return err
		}
		evidenceID := stableID("knowledge-document-evidence:", b.ID)
		var evidenceCollision int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM context_graph_evidence WHERE tenant_id=? AND id=?`, b.TenantID, evidenceID).Scan(&evidenceCollision); err != nil {
			return err
		}
		if evidenceCollision != 0 {
			return ErrConflict
		}
		summary := map[string]any{"source": b.Plan.Source.Stamp(), "model": b.Decision.ModelID, "reason": b.Decision.Reason, "plan_hash": Digest(b.Plan), "batch_id": b.ID}
		if len(b.Plan.Conflicts) > 0 {
			// Cross-source reasons stay in the immutable batch, whose status API
			// rechecks every source's visibility. Generic evidence readers receive
			// only an audit reference, never unfiltered competing-source text.
			summary["reason"] = "Model reviewed source conflict choices; details require authorized batch access."
		}
		evidence := contextgraph.Evidence{ID: evidenceID, TenantID: b.TenantID, SourceRef: contextgraph.Ref{Kind: "knowledge_document_revision", ID: fmt.Sprintf("%s:%s:%d:%s", b.Plan.Source.KnowledgeBaseID, b.Plan.Source.DocumentID, b.Plan.Source.Revision, b.Plan.Source.ContentHash)}, SourceAccessRef: b.Plan.Source.Access, Summary: body(summary), Confidence: 1, ObservedAt: trusted.Now}
		if err := graph.SaveEvidence(ctx, evidence); err != nil {
			return err
		}
		refs := []contextgraph.Ref{{Kind: "evidence", ID: evidenceID}}
		version := trusted.Now.UnixNano()
		for _, claim := range b.Plan.Claims {
			var collision int
			if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM context_graph_facts WHERE tenant_id=? AND id=?)+(SELECT COUNT(*) FROM context_graph_links WHERE tenant_id=? AND id=?)`, b.TenantID, claim.ID, b.TenantID, claim.ID).Scan(&collision); err != nil {
				return err
			}
			if collision != 0 {
				return ErrConflict
			}
			props := map[string]any{"source_root_id": b.Plan.Source.RootID, "quote": claim.Quote, "document_revision": b.Plan.Source.Revision, "content_hash": b.Plan.Source.ContentHash}
			provenance := contextgraph.Ref{Kind: Domain, ID: claim.ID}
			table := "context_graph_facts"
			if claim.TargetID != "" {
				table = "context_graph_links"
				props["relation"] = claim.Value
				if claim.RelationCode != "" {
					props["relation_code"] = claim.RelationCode
				}
				err = graph.SaveLink(ctx, contextgraph.Link{Temporal: claimTemporal(claim, trusted.Now), ID: claim.ID, TenantID: b.TenantID, LinkType: RelatedLink, FromObjectID: claim.EntityID, ToObjectID: claim.TargetID, Properties: props, Confidence: 1, Status: "active", Provenance: provenance, EvidenceRefs: refs, SourceAccessRef: b.Plan.Source.Access, SourceStreamID: stream, SourceVersion: version, UpdatedAt: trusted.Now})
			} else {
				err = graph.SaveFact(ctx, contextgraph.Fact{Temporal: claimTemporal(claim, trusted.Now), ID: claim.ID, TenantID: b.TenantID, Subject: contextgraph.Ref{Kind: EntityType, ID: claim.EntityID}, Predicate: claim.Predicate, Value: claim.Value, Properties: props, Confidence: 1, Status: "active", Provenance: provenance, EvidenceRefs: refs, SourceAccessRef: b.Plan.Source.Access, SourceStreamID: stream, SourceVersion: version, UpdatedAt: trusted.Now})
			}
			if err != nil {
				return err
			}
			claim.ProjectionHash, err = projectionHash(ctx, tx, table, b.TenantID, claim.ID, stream)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_instance_claims(tenant_id,watch_id,id,entity_id,body_json) VALUES(?,?,?,?,?)`, b.TenantID, b.WatchID, claim.ID, claim.EntityID, body(claim)); err != nil {
				return err
			}
			if err := indexClaimSource(ctx, tx, b.TenantID, claim.ID, b.Plan.Source); err != nil {
				return err
			}
		}
		currentClaims, err := ownedClaims(ctx, tx.QueryContext, b.TenantID, b.Plan.Before)
		if err != nil {
			return err
		}
		owners := map[string]string{}
		for _, c := range currentClaims {
			owners[c.Claim.ID] = c.WatchID
		}
		// A document edge is also source owned. Removed entities lose only this
		// document's edge; their identity and claims from other documents survive.
		for id, e := range entities {
			linkID := stableID("knowledge-document-entity:", []any{b.TenantID, b.WatchID, id})
			var projectedStream, projectedType, from, to string
			err := tx.QueryRowContext(ctx, `SELECT source_stream_id,link_type,from_object_id,to_object_id FROM context_graph_links WHERE tenant_id=? AND id=?`, b.TenantID, linkID).Scan(&projectedStream, &projectedType, &from, &to)
			if err == nil && (projectedStream != stream || projectedType != DocumentLink || from != b.Plan.Source.RootID || to != id) {
				return ErrConflict
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err := closeClaimHistory(ctx, tx, b.TenantID, linkID, stream, true, trusted.Now); err != nil {
				return err
			}
			if err := graph.DeleteLink(ctx, b.TenantID, linkID, stream); err != nil {
				return err
			}
			if slices.ContainsFunc(b.Plan.Entities, func(x Entity) bool { return x.ID == id }) {
				if err := graph.SaveLink(ctx, contextgraph.Link{Properties: map[string]any{"source_root_id": b.Plan.Source.RootID}, Temporal: claimTemporal(Claim{}, trusted.Now), ID: linkID, TenantID: b.TenantID, LinkType: DocumentLink, FromObjectID: b.Plan.Source.RootID, ToObjectID: id, Provenance: contextgraph.Ref{Kind: Domain, ID: linkID}, EvidenceRefs: refs, SourceAccessRef: b.Plan.Source.Access, SourceStreamID: stream, SourceVersion: version, Status: "active", UpdatedAt: trusted.Now}); err != nil {
					return err
				}
			}
			claims, err := claimsTx(ctx, tx, b.TenantID, "entity_id", id)
			if err != nil {
				return err
			}
			e, err = s.adjudicateEntity(ctx, tx, b, e, claims, owners, evidenceID, trusted.Now)
			if err != nil {
				return err
			}
			e.Version++
			object := contextgraph.Object{ID: e.ID, TenantID: e.TenantID, ObjectType: EntityType, CanonicalKey: EntityType + ":" + e.ID, Label: e.Name, Properties: e.Properties, Status: e.Status, Sensitivity: "internal", Confidence: 1, Provenance: contextgraph.Ref{Kind: Domain, ID: e.ID}, OwnerAccessRef: e.Access, SourceStreamID: "knowledge-instance:" + e.ID, SourceVersion: e.Version, UpdatedAt: trusted.Now}
			if err := graph.SaveObject(ctx, object); err != nil {
				return err
			}
			e.ProjectionHash, err = projectionHash(ctx, tx, "context_graph_objects", b.TenantID, id, object.SourceStreamID)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_instances(tenant_id,id,access_hash,version,body_json) VALUES(?,?,?,?,?) ON CONFLICT(tenant_id,id) DO UPDATE SET version=excluded.version,body_json=excluded.body_json`, e.TenantID, e.ID, Digest(e.Access), e.Version, body(e)); err != nil {
				return err
			}
			if err := syncIdentityIndexTx(ctx, tx, e, b.ID); err != nil {
				return err
			}
		}
		if err := scheduleBoundary(ctx, tx, BoundaryJob{TenantID: b.TenantID, WatchID: b.WatchID, Version: b.Plan.Watch.Version, PlanHash: Digest(b.Plan), SourceStamp: b.Plan.Source.Stamp()}, b.Plan.Claims, trusted.Now); err != nil {
			return err
		}
		receipt = Receipt{ID: stableID("knowledge-receipt:", b.ID), BatchID: b.ID, PlanHash: Digest(b.Plan), ActionRunID: stableID("knowledge-action:", b.ID), ClaimCount: len(b.Plan.Claims), AppliedAt: trusted.Now}
		for _, choice := range b.Decision.Resolutions {
			if choice.Decision == "accept" {
				receipt.ResolutionCount++
			}
		}
		for id := range entities {
			receipt.EntityIDs = append(receipt.EntityIDs, id)
		}
		sort.Strings(receipt.EntityIDs)
		if err := completeAction(ctx, tx, b, receipt, refs, trusted); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO ontology_document_receipts(tenant_id,batch_id,plan_hash,body_json) VALUES(?,?,?,?)`, b.TenantID, b.ID, receipt.PlanHash, body(receipt)); err != nil {
			return err
		}
		b.Receipt = &receipt
		b.Status = "applied"
		_, err = tx.ExecContext(ctx, `UPDATE ontology_document_batches SET status=?,body_json=? WHERE tenant_id=? AND id=?`, b.Status, body(b), b.TenantID, b.ID)
		return err
	})
	return receipt, err
}

func claimStream(p Plan) string { return "knowledge-document:" + p.Source.TenantID + ":" + p.Watch.ID }

func (s *Store) checkEntities(ctx context.Context, tx db.Tx, p Plan) (map[string]Entity, error) {
	result := map[string]Entity{}
	for id, expected := range p.Before {
		var raw string
		err := tx.QueryRowContext(ctx, `SELECT body_json FROM knowledge_instances WHERE tenant_id=? AND id=?`, p.Source.TenantID, id).Scan(&raw)
		if expected == 0 {
			if !errors.Is(err, sql.ErrNoRows) {
				return nil, ErrConflict
			}
			var count int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM context_graph_objects WHERE tenant_id=? AND id=?`, p.Source.TenantID, id).Scan(&count); err != nil {
				return nil, err
			}
			if count != 0 {
				return nil, ErrConflict
			}
			for _, e := range p.Entities {
				if e.ID == id {
					result[id] = e
				}
			}
			if _, ok := result[id]; !ok {
				return nil, ErrInvalid
			}
			continue
		}
		if err != nil {
			return nil, ErrConflict
		}
		var e Entity
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			return nil, err
		}
		if e.Version != expected || e.Access != p.Source.Access {
			return nil, ErrConflict
		}
		current, err := projectionHash(ctx, tx, "context_graph_objects", p.Source.TenantID, id, "knowledge-instance:"+id)
		if err != nil || current != e.ProjectionHash {
			return nil, ErrConflict
		}
		result[id] = e
	}
	return result, nil
}

func claimsTx(ctx context.Context, tx db.Tx, tenant, column, id string) ([]Claim, error) {
	if column != "watch_id" && column != "entity_id" {
		return nil, ErrInvalid
	}
	rows, err := tx.QueryContext(ctx, `SELECT body_json FROM knowledge_instance_claims WHERE tenant_id=? AND `+column+`=? ORDER BY id`, tenant, id)
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

func aggregate(e Entity, claims []Claim) Entity {
	e.Properties = map[string]any{}
	// Kind is part of the persistent identity, even after all source claims
	// withdraw, so a later revision can resolve and reactivate this identity.
	e.Properties["entity_kind"] = e.Kind
	if len(e.IdentityKeys) > 0 {
		e.Properties["identity_keys"] = e.IdentityKeys
	}
	e.Conflicts = nil
	e.Status = "withdrawn"
	values := map[string]map[string]any{}
	for _, c := range claims {
		e.Status = "active"
		if c.TargetID != "" {
			continue
		}
		if values[c.Predicate] == nil {
			values[c.Predicate] = map[string]any{}
		}
		values[c.Predicate][Digest(c.Value)] = c.Value
	}
	for predicate, distinct := range values {
		if len(distinct) == 1 {
			for _, value := range distinct {
				e.Properties[predicate] = value
			}
		} else {
			e.Conflicts = append(e.Conflicts, predicate)
		}
	}
	sort.Strings(e.Conflicts)
	if len(e.Conflicts) > 0 {
		e.Properties["conflicting_properties"] = e.Conflicts
	}
	if name, ok := e.Properties["name"].(string); ok {
		e.Name = name
	}
	return e
}

// Hash every persisted projection column, including ownership and source
// version. A same-ID foreign edit cannot be silently overwritten.
func projectionHash(ctx context.Context, tx db.Tx, table, tenant, id, stream string) (string, error) {
	if table != "context_graph_objects" && table != "context_graph_facts" && table != "context_graph_links" {
		return "", ErrInvalid
	}
	rows, err := tx.QueryContext(ctx, `SELECT * FROM `+table+` WHERE tenant_id=? AND id=? AND source_stream_id=?`, tenant, id, stream)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return "", err
	}
	if !rows.Next() {
		return "", ErrConflict
	}
	values := make([]any, len(columns))
	dest := make([]any, len(columns))
	for i := range values {
		dest[i] = &values[i]
	}
	if err := rows.Scan(dest...); err != nil {
		return "", err
	}
	for i, v := range values {
		if data, ok := v.([]byte); ok {
			values[i] = string(data)
		}
	}
	return Digest(values), rows.Err()
}

func completeAction(ctx context.Context, tx db.Tx, b Batch, r Receipt, refs []contextgraph.Ref, trusted CommitContext) error {
	svc := actioncontract.NewService(actioncontract.NewPlatformRepositoryTx(tx), actioncontract.WithClock(func() time.Time { return trusted.Now }))
	objects := []actioncontract.ObjectRef{{Kind: "knowledge_document", ID: b.Plan.Source.RootID}}
	for _, id := range r.EntityIDs {
		objects = append(objects, actioncontract.ObjectRef{Kind: EntityType, ID: id})
	}
	_, err := svc.StartActionRun(ctx, actioncontract.StartRunRequest{RunID: actioncontract.ActionRunID(r.ActionRunID), TenantID: b.TenantID, ActionTypeID: actioncontract.ActionTypeID(ActionID), Actor: actioncontract.ActionSubject{Kind: actioncontract.SubjectAgent, ID: trusted.AgentID}, Runtime: actioncontract.RuntimeRef{Kind: actioncontract.RuntimeAgent, ID: Domain}, Approval: actioncontract.Ref{Kind: "ontology_document_model_decision", ID: b.ID}, Purpose: Domain, ObjectRefs: objects, ArgsHash: r.PlanHash, EvidenceRefs: []actioncontract.Ref{{Kind: "ontology_document_batch", ID: b.ID}}})
	if err != nil {
		return err
	}
	_, _, err = svc.CompleteActionRun(ctx, actioncontract.CompleteRunRequest{RunID: actioncontract.ActionRunID(r.ActionRunID), ResultRef: actioncontract.Ref{Kind: "ontology_document_batch", ID: b.ID}, EvidenceRefs: []actioncontract.Ref{{Kind: "evidence", ID: refs[0].ID}}, Writeback: &actioncontract.WritebackReceipt{ID: actioncontract.WritebackReceiptID(r.ID), TargetSystem: Domain, TargetResourceRefs: objects, BeforeHash: Digest([]any{b.Plan.Before, b.Plan.Previous, b.Plan.ClaimSetHash}), AfterHash: Digest([]any{b.Plan.Claims, b.Decision.Resolutions, b.Plan.IdentityAssignments}), ChangedFields: []string{"knowledge_instances", "source_assertions", "fact_adjudications", "identity_keys"}, IdempotencyKey: b.ID, Status: actioncontract.WritebackStatusSucceeded}})
	return err
}
