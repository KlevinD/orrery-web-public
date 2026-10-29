package knowledgeinstances

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/db"
)

func ValidateResolutionChoices(p Plan, d Decision) error {
	if d.Decision != "approve" {
		if len(d.Resolutions) != 0 {
			return ErrInvalid
		}
		return nil
	}
	if len(d.Resolutions) != len(p.Conflicts) || (len(d.Resolutions) > 0 && p.ClaimSetHash == "") {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, choice := range d.Resolutions {
		key := choice.EntityID + ":" + choice.Predicate
		if seen[key] || strings.TrimSpace(choice.Reason) == "" || len(choice.Reason) > 2000 ||
			(choice.Decision != "accept" && choice.Decision != "defer") ||
			(choice.Decision == "defer" && choice.ClaimID != "") {
			return ErrInvalid
		}
		seen[key] = true
		index := slices.IndexFunc(p.Conflicts, func(c ConflictCase) bool { return c.EntityID == choice.EntityID && c.Predicate == choice.Predicate })
		if index < 0 {
			return ErrInvalid
		}
		if choice.Decision == "accept" && !slices.ContainsFunc(p.Conflicts[index].Assertions, func(c SourceClaim) bool { return c.Claim.ID == choice.ClaimID }) {
			return ErrInvalid
		}
	}
	return nil
}

// The claim-set fence includes hidden/omitted conflicts. Model input must be a
// complete case, and every persisted projection must still be domain-owned.
func checkConflictPreview(ctx context.Context, tx db.Tx, p Plan, old []SourceClaim) error {
	if p.ClaimSetHash != "" && p.ClaimSetHash != Digest(old) {
		return ErrConflict
	}
	for _, c := range old {
		table := "context_graph_facts"
		if c.Claim.TargetID != "" {
			table = "context_graph_links"
		}
		current, err := projectionHash(ctx, tx, table, p.Source.TenantID, c.Claim.ID, "knowledge-document:"+p.Source.TenantID+":"+c.WatchID)
		if err != nil || current != c.Claim.ProjectionHash {
			return ErrConflict
		}
	}
	actual := conflictCases(activeSourceClaimsAt(replacementClaims(p, old), p.CreatedAt))
	seen := map[string]bool{}
	for _, offered := range p.Conflicts {
		key := offered.EntityID + ":" + offered.Predicate
		index := slices.IndexFunc(actual, func(c ConflictCase) bool { return c.EntityID == offered.EntityID && c.Predicate == offered.Predicate })
		if seen[key] || index < 0 || len(offered.Assertions) != len(actual[index].Assertions) {
			return ErrConflict
		}
		seen[key] = true
		for i, a := range offered.Assertions {
			b := actual[index].Assertions[i]
			if a.WatchID != b.WatchID || a.Claim.Source == nil || a.Claim.Source.Content != "" || a.Claim.Source.TenantID != p.Source.TenantID || a.Claim.Source.Access != p.Source.Access {
				return ErrConflict
			}
			if b.Claim.Source != nil && a.Claim.Source.Stamp() != b.Claim.Source.Stamp() {
				return ErrConflict
			}
			// Legacy source metadata was recovered from its applied batch. All
			// value, identity, quote and ownership fields must still match.
			a.Claim.Source, b.Claim.Source = nil, nil
			if Digest(a) != Digest(b) {
				return ErrConflict
			}
		}
	}
	return nil
}

func (s *Store) adjudicateEntity(ctx context.Context, tx db.Tx, b Batch, e Entity, claims []Claim, owners map[string]string, evidenceID string, now time.Time) (Entity, error) {
	allClaims := claims
	priorResolutions := e.Resolutions
	claims = activeClaimsAt(claims, now)
	e = aggregate(e, claims)
	e.Resolutions = nil
	accepted := map[string]ResolutionRecord{}
	selectedValues := map[string]string{}
	for _, choice := range b.Decision.Resolutions {
		if choice.EntityID != e.ID || choice.Decision != "accept" {
			continue
		}
		index := slices.IndexFunc(claims, func(c Claim) bool { return c.ID == choice.ClaimID })
		if index < 0 {
			return e, ErrConflict
		}
		selected := claims[index]
		index = slices.IndexFunc(b.Plan.Conflicts, func(c ConflictCase) bool { return c.EntityID == e.ID && c.Predicate == choice.Predicate })
		if index < 0 {
			return e, ErrInvalid
		}
		r := ResolutionRecord{ResolutionChoice: choice, BatchID: b.ID, ModelID: b.Decision.ModelID, InputHash: Digest(b.Plan.Conflicts[index])}
		r.BusinessValidFrom, r.BusinessValidTo, _ = selected.Validity.Bounds()
		if b.Decision.ModelID == "retained-model-resolution" {
			for _, old := range priorResolutions {
				if old.Predicate == choice.Predicate {
					r.BatchID, r.ModelID = old.BatchID, old.ModelID
				}
			}
		}
		for _, c := range claims {
			if c.Predicate == choice.Predicate && c.TargetID == "" && Digest(c.Value) == Digest(selected.Value) {
				r.SupportingClaimIDs = append(r.SupportingClaimIDs, c.ID)
				// All reviewed supports are active now, so their intervals
				// overlap and their union is one connected window. A short
				// support expiring cannot end a surviving equal-value choice.
				start, end, _ := c.Validity.Bounds()
				if start.IsZero() || (!r.BusinessValidFrom.IsZero() && start.Before(r.BusinessValidFrom)) {
					r.BusinessValidFrom = start
				}
				if end == nil {
					r.BusinessValidTo = nil
				} else if r.BusinessValidTo != nil && end.After(*r.BusinessValidTo) {
					copy := *end
					r.BusinessValidTo = &copy
				}

			}
		}
		sort.Strings(r.SupportingClaimIDs)
		accepted[choice.Predicate] = r
		selectedValues[choice.Predicate] = Digest(selected.Value)
		e.Properties[choice.Predicate] = selected.Value
		e.Conflicts = slices.DeleteFunc(e.Conflicts, func(p string) bool { return p == choice.Predicate })
		e.Resolutions = append(e.Resolutions, r)
	}
	sort.Slice(e.Resolutions, func(i, j int) bool { return e.Resolutions[i].Predicate < e.Resolutions[j].Predicate })
	delete(e.Properties, "conflicting_properties")
	if len(e.Conflicts) > 0 {
		e.Properties["conflicting_properties"] = e.Conflicts
	}
	if name, ok := e.Properties["name"].(string); ok {
		e.Name = name
	}
	for _, c := range claims {
		if c.TargetID != "" {
			continue
		}
		stream := "knowledge-document:" + b.TenantID + ":" + owners[c.ID]
		f, err := ownedFact(ctx, tx, b.TenantID, c.ID, stream)
		if err != nil {
			return e, err
		}
		if f.Subject.Kind != EntityType || f.Subject.ID != e.ID || f.Predicate != c.Predicate || Digest(f.Value) != Digest(c.Value) || f.SourceAccessRef != e.Access {
			return e, ErrConflict
		}
		status := "active"
		r, resolved := accepted[c.Predicate]
		if resolved && Digest(c.Value) != selectedValues[c.Predicate] {
			status = "disputed"
		}
		oldEvidence, _ := f.Properties["adjudication_evidence_id"].(string)
		if !resolved && oldEvidence == "" && (f.Status == status || f.Status == "scheduled" || f.Status == "expired") {
			continue
		}
		f.EvidenceRefs = slices.DeleteFunc(f.EvidenceRefs, func(ref contextgraph.Ref) bool { return ref.Kind == "evidence" && ref.ID == oldEvidence })
		delete(f.Properties, "adjudication")
		delete(f.Properties, "adjudication_evidence_id")
		if resolved {
			f.Properties["adjudication"] = r
			refID := evidenceID
			if refID == "" {
				refID = oldEvidence
			}
			if refID != "" {
				f.Properties["adjudication_evidence_id"] = refID
				f.EvidenceRefs = append(f.EvidenceRefs, contextgraph.Ref{Kind: "evidence", ID: refID})
			}
		}
		f.Status, f.UpdatedAt = status, now
		if f.Temporal.Mode != "" && f.Temporal.Mode != "static" {
			f.Temporal.SourceRecordedAt = now
		}
		f.SourceVersion = max(f.SourceVersion+1, now.UnixNano())
		// The current assertion table stays one row per owned source claim;
		// immutable temporal history lives in resource_versions instead.
		if _, err := tx.ExecContext(ctx, `DELETE FROM context_graph_fact_assertions WHERE tenant_id=? AND fact_id=? AND source_stream_id=?`, b.TenantID, c.ID, stream); err != nil {
			return e, err
		}
		if err := contextgraph.NewPlatformStoreTx(tx).SaveFact(ctx, f); err != nil {
			return e, err
		}
		c.ProjectionHash, err = projectionHash(ctx, tx, "context_graph_facts", b.TenantID, c.ID, stream)
		if err != nil {
			return e, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE knowledge_instance_claims SET body_json=? WHERE tenant_id=? AND id=? AND watch_id=?`, body(c), b.TenantID, c.ID, owners[c.ID]); err != nil {
			return e, err
		}
	}
	for _, c := range allClaims {
		if err := refreshClaimClock(ctx, tx, b.TenantID, owners[c.ID], c, now); err != nil {
			return e, err
		}
	}
	return e, nil
}

// Read an already-fenced, domain-owned projection for a faithful SaveFact.
// SaveFact updates BOTH the current fact and its static assertion atomically.
func ownedFact(ctx context.Context, tx db.Tx, tenant, id, stream string) (contextgraph.Fact, error) {
	f := contextgraph.Fact{ID: id, TenantID: tenant, SourceStreamID: stream}
	var value, props, provenance, refs, temporal string
	err := tx.QueryRowContext(ctx, `SELECT subject_kind,subject_id,predicate,value_json,properties_json,confidence,status,provenance_json,evidence_refs_json,source_access_kind,source_access_id,source_access_tenant_id,source_version,temporal_json FROM context_graph_facts WHERE tenant_id=? AND id=? AND source_stream_id=? AND access_state='resolved'`, tenant, id, stream).Scan(&f.Subject.Kind, &f.Subject.ID, &f.Predicate, &value, &props, &f.Confidence, &f.Status, &provenance, &refs, &f.SourceAccessRef.Kind, &f.SourceAccessRef.ID, &f.SourceAccessRef.TenantID, &f.SourceVersion, &temporal)
	if err != nil {
		return f, ErrConflict
	}
	for _, field := range []struct {
		raw    string
		target any
	}{{value, &f.Value}, {props, &f.Properties}, {provenance, &f.Provenance}, {refs, &f.EvidenceRefs}, {temporal, &f.Temporal}} {
		if err := json.Unmarshal([]byte(field.raw), field.target); err != nil {
			return f, err
		}
	}
	if f.Properties == nil {
		f.Properties = map[string]any{}
	}
	return f, nil
}
