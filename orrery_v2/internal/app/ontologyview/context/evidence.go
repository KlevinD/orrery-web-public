package contextview

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type positionedEvidence struct {
	raw  contextgraph.Evidence
	item EvidenceItem
}

func (s *Service) ListEvidence(ctx context.Context, principal identity.Principal, query ChildQuery) (ontologycontract.CursorPage[EvidenceItem], error) {
	query, limit, err := normalizeChildQuery(query)
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
	visibility, err := s.resolveVisibility(bounded, principal, authorized, budget, datavisibility.ResourceObject, datavisibility.ResourceEvidence)
	if err != nil {
		return ontologycontract.CursorPage[EvidenceItem]{}, err
	}
	tenantID := visibility.scope.Binding().TenantID
	parent, err := s.getVisibleParent(bounded, principal, tenantID, query.ObjectID, visibility, budget)
	if err != nil {
		return ontologycontract.CursorPage[EvidenceItem]{}, err
	}
	refs, err := s.visibleEvidenceRefs(bounded, principal, visibility, parent)
	if err != nil {
		return ontologycontract.CursorPage[EvidenceItem]{}, err
	}
	if len(refs) > contextgraph.MaxQueryPageSize {
		return ontologycontract.CursorPage[EvidenceItem]{}, ontologycontract.ErrQueryBudgetExceeded
	}
	queryHash, err := ontologycontract.CanonicalQueryHash(struct {
		Kind     string `json:"kind"`
		ObjectID string `json:"object_id"`
		Limit    int    `json:"limit"`
	}{"evidence", query.ObjectID, limit})
	if err != nil {
		return ontologycontract.CursorPage[EvidenceItem]{}, err
	}
	binding := contextCursorBinding(visibility, queryHash)
	after, err := s.resolveSeekCursor(query.Cursor, binding, visibility.cacheable)
	if err != nil {
		return ontologycontract.CursorPage[EvidenceItem]{}, err
	}
	if len(refs) == 0 {
		return ontologycontract.NewCursorPage([]EvidenceItem(nil), limit, false, "")
	}
	evidenceIDs := contextgraph.EvidenceRecordIDsForRefs(refs)
	visible := make([]positionedEvidence, 0, limit+1)
	scanBatches := 0
	for len(visible) <= limit {
		if scanBatches >= contextCandidateScanBatches {
			return ontologycontract.CursorPage[EvidenceItem]{}, ontologycontract.ErrQueryBudgetExceeded
		}
		if err := budget.ConsumeStoreReads(1); err != nil {
			return ontologycontract.CursorPage[EvidenceItem]{}, err
		}
		scanBatches++
		page, err := s.graph.ListVisibleEvidence(bounded, contextgraph.EvidenceQuery{
			TenantID: tenantID, Visibility: visibility.constraint, SourceRefs: refs, EvidenceIDs: evidenceIDs, Limit: contextScanBatch, After: after,
		})
		if err != nil {
			return ontologycontract.CursorPage[EvidenceItem]{}, queryError(bounded, budget, err)
		}
		if err := budget.ConsumeCandidates(len(page.Items)); err != nil {
			return ontologycontract.CursorPage[EvidenceItem]{}, err
		}
		mapped, err := s.visibleEvidence(bounded, principal, visibility, page.Items)
		if err != nil {
			return ontologycontract.CursorPage[EvidenceItem]{}, err
		}
		mappedByID := make(map[string]EvidenceItem, len(mapped))
		for _, item := range mapped {
			mappedByID[item.ID] = item
		}
		for _, raw := range page.Items {
			if !evidenceMatchesAnyRef(raw, refs) {
				continue
			}
			item, ok := mappedByID[raw.ID]
			if !ok {
				continue
			}
			visible = append(visible, positionedEvidence{raw: raw, item: item})
			if len(visible) > limit {
				break
			}
		}
		if len(visible) > limit || !page.HasMore {
			break
		}
		if len(page.Items) == 0 {
			return ontologycontract.CursorPage[EvidenceItem]{}, dependencyError(contextgraph.ErrQueryInvalid)
		}
		last := page.Items[len(page.Items)-1]
		after = contextgraph.ObjectSeek{UpdatedAt: last.ObservedAt, ID: last.ID}
	}
	hasMore := len(visible) > limit
	if hasMore {
		visible = visible[:limit]
	}
	items := make([]EvidenceItem, len(visible))
	for index := range visible {
		items[index] = visible[index].item
	}
	if err := budget.ConsumeResults(len(items)); err != nil {
		return ontologycontract.CursorPage[EvidenceItem]{}, err
	}
	nextCursor := ""
	if hasMore {
		last := visible[len(visible)-1].raw
		nextSeek := contextgraph.ObjectSeek{UpdatedAt: last.ObservedAt, ID: last.ID}
		nextCursor, err = s.issueSeekCursor(binding, visibility.cacheable, nextSeek)
		if err != nil {
			return ontologycontract.CursorPage[EvidenceItem]{}, err
		}
	}
	return ontologycontract.NewCursorPage(items, limit, hasMore, nextCursor)
}

func evidenceMatchesAnyRef(evidence contextgraph.Evidence, refs []contextgraph.Ref) bool {
	for _, ref := range refs {
		if contextgraph.EvidenceMatchesRef(evidence, ref) {
			return true
		}
	}
	return false
}

func (s *Service) visibleEvidenceRefs(ctx context.Context, principal identity.Principal, state visibilityState, parent contextgraph.Object) ([]contextgraph.Ref, error) {
	descriptor := datavisibility.ResourceDescriptor{
		Key: "object-evidence-refs:" + parent.ID, Kind: datavisibility.ResourceObject, TenantID: parent.TenantID,
		Type: parent.ObjectType, FieldPaths: []string{"evidence_refs"}, AccessRefs: visibilityAccessRefs(parent.OwnerAccessRef), Payload: parent,
	}
	decision, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{Principal: identity.NormalizePrincipal(principal), Purpose: "ontology.context.read", Resources: []datavisibility.ResourceDescriptor{descriptor}})
	if err != nil {
		return nil, dependencyError(err)
	}
	if err := verifyVisibilityRevision(state, datavisibility.ResourceObject, decision); err != nil {
		return nil, err
	}
	resource, ok := decision.Resource(descriptor.Key)
	if !ok {
		return nil, dependencyError(fmt.Errorf("object evidence-reference decision is incomplete"))
	}
	if resource.Hidden || resource.ExistenceDisclosure == datavisibility.ExistenceConceal {
		return nil, ErrUnavailable
	}
	if maskedFieldSet(resource)["evidence_refs"] {
		return nil, nil
	}
	seen := map[contextgraph.Ref]struct{}{}
	refs := make([]contextgraph.Ref, 0, len(parent.EvidenceRefs))
	for _, ref := range parent.EvidenceRefs {
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
