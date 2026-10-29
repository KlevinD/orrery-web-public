package lineage

import (
	"context"
	"sort"
	"strings"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type visibleEvidenceRow struct {
	raw  contextgraph.Evidence
	item EvidenceItem
}

func (s *Service) ListEvidence(ctx context.Context, principal identity.Principal, query FactPageQuery) (ontologycontract.CursorPage[EvidenceItem], error) {
	query, limit, err := normalizeFactPageQuery(query)
	if err != nil {
		return ontologycontract.CursorPage[EvidenceItem]{}, err
	}
	authorized, err := s.authorize(ctx, principal, query.TenantID)
	if err != nil {
		return ontologycontract.CursorPage[EvidenceItem]{}, err
	}
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QueryStandard)
	if err != nil {
		return ontologycontract.CursorPage[EvidenceItem]{}, err
	}
	defer cancel()
	visibility, err := s.resolveVisibility(bounded, principal, authorized, budget,
		datavisibility.ResourceFact, datavisibility.ResourceObject, datavisibility.ResourceEvidence,
	)
	if err != nil {
		return ontologycontract.CursorPage[EvidenceItem]{}, err
	}
	tenantID := visibility.scope.Binding().TenantID
	detail, factDecision, _, err := s.getVisibleFact(bounded, principal, tenantID, query.FactID, visibility, budget, []string{"evidence_refs"}, nil)
	if err != nil {
		return ontologycontract.CursorPage[EvidenceItem]{}, err
	}
	if maskedFieldSet(factDecision)["evidence_refs"] {
		return ontologycontract.CursorPage[EvidenceItem]{}, ErrUnavailable
	}
	refs, err := normalizedEvidenceRefs(detail.Fact.EvidenceRefs)
	if err != nil {
		return ontologycontract.CursorPage[EvidenceItem]{}, err
	}
	queryHash, err := ontologycontract.CanonicalQueryHash(struct {
		Kind   string             `json:"kind"`
		FactID string             `json:"fact_id"`
		Limit  int                `json:"limit"`
		Refs   []contextgraph.Ref `json:"refs"`
	}{"evidence", query.FactID, limit, refs})
	if err != nil {
		return ontologycontract.CursorPage[EvidenceItem]{}, err
	}
	binding := lineageCursorBinding(visibility, queryHash)
	after, err := s.resolveEvidenceSeek(query.Cursor, binding, visibility.cacheable)
	if err != nil {
		return ontologycontract.CursorPage[EvidenceItem]{}, err
	}
	start, err := evidenceStartIndex(refs, after)
	if err != nil {
		return ontologycontract.CursorPage[EvidenceItem]{}, err
	}
	end := start + limit
	if end > len(refs) {
		end = len(refs)
	}
	pageRefs := refs[start:end]
	hasMore := end < len(refs)
	items := make([]EvidenceItem, len(pageRefs))
	if len(pageRefs) > 0 {
		if err := budget.ConsumeStoreReads(1); err != nil {
			return ontologycontract.CursorPage[EvidenceItem]{}, err
		}
		ids := contextgraph.EvidenceRecordIDsForRefs(pageRefs)
		page, err := s.graph.ListVisibleEvidence(bounded, contextgraph.EvidenceQuery{
			TenantID: tenantID, Visibility: visibility.constraint, SourceRefs: pageRefs,
			EvidenceIDs: ids, Limit: contextgraph.MaxQueryPageSize,
		})
		if err != nil {
			return ontologycontract.CursorPage[EvidenceItem]{}, queryError(bounded, budget, err)
		}
		if page.HasMore {
			return ontologycontract.CursorPage[EvidenceItem]{}, ontologycontract.ErrQueryBudgetExceeded
		}
		if err := budget.ConsumeCandidates(len(page.Items)); err != nil {
			return ontologycontract.CursorPage[EvidenceItem]{}, err
		}
		visible, err := s.visibleEvidenceRows(bounded, principal, visibility, page.Items)
		if err != nil {
			return ontologycontract.CursorPage[EvidenceItem]{}, err
		}
		for index, ref := range pageRefs {
			items[index] = matchEvidenceRef(ref, visible)
		}
	}
	if err := budget.ConsumeResults(len(items)); err != nil {
		return ontologycontract.CursorPage[EvidenceItem]{}, err
	}
	nextCursor := ""
	if hasMore {
		last := pageRefs[len(pageRefs)-1]
		nextCursor, err = s.issueEvidenceCursor(binding, visibility.cacheable, evidenceSeek{Kind: last.Kind, ID: last.ID})
		if err != nil {
			return ontologycontract.CursorPage[EvidenceItem]{}, err
		}
	}
	return ontologycontract.NewCursorPage(items, limit, hasMore, nextCursor)
}

func normalizedEvidenceRefs(values []contextgraph.Ref) ([]contextgraph.Ref, error) {
	seen := map[contextgraph.Ref]struct{}{}
	refs := make([]contextgraph.Ref, 0, len(values))
	for _, ref := range values {
		ref.Kind = strings.ToLower(strings.TrimSpace(ref.Kind))
		ref.ID = strings.TrimSpace(ref.ID)
		if !validID(ref.Kind) || !validID(ref.ID) {
			return nil, dependencyError(contextgraph.ErrQueryInvalid)
		}
		if _, exists := seen[ref]; exists {
			continue
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool {
		return refs[i].Kind < refs[j].Kind || refs[i].Kind == refs[j].Kind && refs[i].ID < refs[j].ID
	})
	return refs, nil
}

func evidenceStartIndex(refs []contextgraph.Ref, after evidenceSeek) (int, error) {
	if after == (evidenceSeek{}) {
		return 0, nil
	}
	for index, ref := range refs {
		if ref.Kind == after.Kind && ref.ID == after.ID {
			return index + 1, nil
		}
	}
	return 0, ontologycontract.ErrCursorStale
}

func (s *Service) visibleEvidenceRows(ctx context.Context, principal identity.Principal, state visibilityState, rows []contextgraph.Evidence) ([]visibleEvidenceRow, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	resources := make([]datavisibility.ResourceDescriptor, 0, len(rows))
	for _, row := range rows {
		resources = append(resources, datavisibility.ResourceDescriptor{
			Key: "evidence:" + row.ID, Kind: datavisibility.ResourceEvidence,
			TenantID: row.TenantID, Type: row.SourceRef.Kind,
			FieldPaths: []string{"source_kind", "source_id", "summary", "confidence", "observed_at"},
			AccessRefs: visibilityAccessRefs(row.SourceAccessRef), Payload: row,
		})
	}
	decision, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{
		Principal: identity.NormalizePrincipal(principal), Purpose: "ontology.lineage.read", Resources: resources,
	})
	if err != nil {
		return nil, dependencyError(err)
	}
	if err := verifyVisibilityRevision(state, datavisibility.ResourceEvidence, decision); err != nil {
		return nil, err
	}
	out := make([]visibleEvidenceRow, 0, len(rows))
	for _, row := range rows {
		resource, ok := decision.Resource("evidence:" + row.ID)
		if !ok {
			return nil, dependencyError(datavisibility.ErrIncompleteDecision)
		}
		if hiddenDecision(resource) {
			continue
		}
		masked := maskedFieldSet(resource)
		item := EvidenceItem{
			Status: ReferenceResolved, RefKind: row.SourceRef.Kind, RefID: row.SourceRef.ID,
			EvidenceID: row.ID, Summary: row.Summary, Confidence: floatValue(row.Confidence), ObservedAt: visibleTime(row.ObservedAt),
		}
		if masked["source_kind"] {
			item.RefKind = ""
		}
		if masked["source_id"] {
			item.RefID = ""
		}
		if masked["summary"] {
			item.Summary = ""
		}
		if masked["confidence"] {
			item.Confidence = nil
		}
		if masked["observed_at"] {
			item.ObservedAt = nil
		}
		out = append(out, visibleEvidenceRow{raw: row, item: item})
	}
	return out, nil
}

func matchEvidenceRef(ref contextgraph.Ref, rows []visibleEvidenceRow) EvidenceItem {
	for _, row := range rows {
		if contextgraph.EvidenceMatchesRef(row.raw, ref) {
			item := row.item
			if strings.EqualFold(strings.TrimSpace(ref.Kind), "event") || strings.EqualFold(strings.TrimSpace(ref.Kind), "evidence") {
				if item.RefKind != "" {
					item.RefKind = ref.Kind
				}
				if item.RefID != "" {
					item.RefID = ref.ID
				}
			}
			return item
		}
	}
	return EvidenceItem{Status: ReferenceUnavailable}
}
