package contextview

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

const overviewRecentLimit = 5

var overviewResourceKinds = []datavisibility.ResourceKind{
	datavisibility.ResourceObject,
	datavisibility.ResourceFact,
	datavisibility.ResourceLink,
	datavisibility.ResourceEvidence,
}

var overviewDomainOrder = []string{
	"organization", "workforce", "execution", "automation", "resources",
	"knowledge", "memory", "evidence", "other",
}

var overviewObjectDomains = map[string]string{
	"organization":       "organization",
	"department":         "organization",
	"position":           "organization",
	"person":             "organization",
	"role_assignment":    "organization",
	"agent":              "workforce",
	"agent_version":      "workforce",
	"mission":            "execution",
	"task":               "execution",
	"agent_task":         "execution",
	"agent_run":          "execution",
	"execution_trace":    "execution",
	"context_snapshot":   "execution",
	"scheduled_task":     "automation",
	"scheduled_run":      "automation",
	"tool":               "resources",
	"skill":              "resources",
	"mcp_server":         "resources",
	"knowledge_base":     "knowledge",
	"knowledge_document": "knowledge",
	"memory_profile":     "memory",
	"memory_record":      "memory",
	"evidence_item":      "evidence",
	"artifact":           "evidence",
}

func (s *Service) Overview(ctx context.Context, principal identity.Principal, tenantID string) (ontologycontract.Summary[CompanyOverviewData], error) {
	tenantID = strings.TrimSpace(tenantID)
	if !validID(tenantID) {
		return ontologycontract.Summary[CompanyOverviewData]{}, ontologycontract.ErrInvalidRequest
	}
	authorized, err := s.authorize(ctx, principal, tenantID)
	if err != nil {
		return ontologycontract.Summary[CompanyOverviewData]{}, err
	}
	release, err := s.concurrency.Acquire(ctx, ontologycontract.ConcurrencySummary, authorized.Snapshot().PrincipalKey)
	if err != nil {
		return ontologycontract.Summary[CompanyOverviewData]{}, err
	}
	defer release()
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QueryCompanyOverview)
	if err != nil {
		return ontologycontract.Summary[CompanyOverviewData]{}, err
	}
	defer cancel()
	ownsRequestMemo := datavisibility.RequestMemoFromContext(bounded) == nil
	bounded = datavisibility.WithRequestMemo(bounded)
	visibility, err := s.resolveVisibility(bounded, principal, authorized, budget, overviewResourceKinds...)
	if err != nil {
		return ontologycontract.Summary[CompanyOverviewData]{}, queryError(bounded, budget, err)
	}
	if err := budget.ConsumeStoreReads(4); err != nil {
		return ontologycontract.Summary[CompanyOverviewData]{}, err
	}
	groups, err := s.graph.ReadContextOverviewGroups(bounded, authorized.Binding().TenantID)
	if err != nil {
		return ontologycontract.Summary[CompanyOverviewData]{}, queryError(bounded, budget, err)
	}
	if len(groups) > contextgraph.MaxContextOverviewGroups {
		return ontologycontract.Summary[CompanyOverviewData]{}, ontologycontract.ErrQueryBudgetExceeded
	}
	refCount, err := validateOverviewGroups(groups)
	if err != nil {
		return ontologycontract.Summary[CompanyOverviewData]{}, dependencyError(err)
	}
	if err := budget.ConsumeCandidates(refCount); err != nil {
		return ontologycontract.Summary[CompanyOverviewData]{}, err
	}

	allowed := make(map[int]bool, len(groups))
	exactKinds := make(map[datavisibility.ResourceKind]bool, len(overviewResourceKinds))
	for _, kind := range overviewResourceKinds {
		visible, exact, err := s.visibleOverviewGroups(bounded, principal, visibility, kind, groups)
		if err != nil {
			return ontologycontract.Summary[CompanyOverviewData]{}, queryError(bounded, budget, err)
		}
		exactKinds[kind] = exact
		for index := range visible {
			allowed[index] = true
		}
	}

	data := buildCompanyOverviewData(groups, allowed, exactKinds)
	recent, err := s.recentOverviewObjects(bounded, principal, visibility, budget)
	if err != nil {
		return ontologycontract.Summary[CompanyOverviewData]{}, queryError(bounded, budget, err)
	}
	if err := budget.ConsumeResults(len(recent)); err != nil {
		return ontologycontract.Summary[CompanyOverviewData]{}, err
	}
	data.RecentObjects = recent
	for _, object := range recent {
		data.NewestUpdatedAt = latestOverviewTime(data.NewestUpdatedAt, object.UpdatedAt)
	}
	revision, err := overviewDataRevision(data)
	if err != nil {
		return ontologycontract.Summary[CompanyOverviewData]{}, err
	}
	result := ontologycontract.Summary[CompanyOverviewData]{
		Data: data,
		Meta: summaryMeta(s.now(), authorized, revision),
	}
	if ownsRequestMemo {
		if err := datavisibility.RequestMemoFromContext(bounded).Validate(bounded); err != nil {
			return ontologycontract.Summary[CompanyOverviewData]{}, queryError(bounded, budget, dependencyError(err))
		}
	}
	return result, nil
}

func validateOverviewGroups(groups []contextgraph.ContextOverviewGroup) (int, error) {
	refs := make(map[contextgraph.AccessRef]bool)
	for _, group := range groups {
		if group.Count < 1 || overviewResourceKind(group.Kind) == "" ||
			(group.Kind == contextgraph.ContextOverviewObject && strings.TrimSpace(group.ObjectType) == "") {
			return 0, contextgraph.ErrQueryInvalid
		}
		if len(group.AccessRefs) == 0 || len(group.AccessRefs) > 3 {
			return 0, contextgraph.ErrAccessRefConflict
		}
		for _, ref := range group.AccessRefs {
			ref = contextgraph.NormalizeAccessRef(ref)
			if !ref.Resolved() {
				continue
			}
			refs[ref] = true
		}
	}
	return len(refs), nil
}

func (s *Service) visibleOverviewGroups(ctx context.Context, principal identity.Principal, state visibilityState, kind datavisibility.ResourceKind, groups []contextgraph.ContextOverviewGroup) (map[int]bool, bool, error) {
	if !state.kindVisible[kind] {
		return nil, false, nil
	}
	binding := state.scope.Binding()
	probeKey := "context-overview-aggregate:" + string(kind)
	groupKind := overviewGroupKind(kind)
	indices := make([]int, 0)
	exact := true
	for index, group := range groups {
		if group.Kind == groupKind {
			if visibilityAccessRefsValid(group.AccessRefs...) {
				indices = append(indices, index)
			} else {
				exact = false
			}
		}
	}
	visible := make(map[int]bool, len(indices))
	probePending := true
	for probePending || len(indices) > 0 {
		descriptors := make([]datavisibility.ResourceDescriptor, 0, len(indices)+1)
		initialRefs := []contextgraph.AccessRef(nil)
		if probePending {
			descriptors = append(descriptors, datavisibility.ResourceDescriptor{
				Key: probeKey, Kind: kind, TenantID: binding.TenantID, Type: "aggregate",
				AccessRefs: []datavisibility.AccessRef{{Kind: binding.SourceObject.Type, ID: binding.SourceObject.ID, TenantID: binding.TenantID}},
				Payload:    datavisibility.AggregateProbe{},
			})
			initialRefs = append(initialRefs, contextgraph.AccessRef{Kind: binding.SourceObject.Type, ID: binding.SourceObject.ID, TenantID: binding.TenantID})
		}
		groupLimit := datavisibility.MaxEvaluationResources - len(descriptors)
		batchEnd := overviewBatchEnd(indices, groups, groupLimit, initialRefs...)
		batch := indices[:batchEnd]
		for _, index := range batch {
			group := groups[index]
			descriptors = append(descriptors, datavisibility.ResourceDescriptor{
				Key: fmt.Sprintf("context-overview-group:%s:%04d", kind, index), Kind: kind,
				TenantID: binding.TenantID, Type: overviewGroupType(group), AccessRefs: visibilityAccessRefs(group.AccessRefs...),
			})
		}
		decision, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{
			Principal: identity.NormalizePrincipal(principal), Purpose: "ontology.context.read", Resources: descriptors,
		})
		if err != nil {
			return nil, false, dependencyError(err)
		}
		if err := verifyVisibilityRevision(state, kind, decision); err != nil {
			return nil, false, err
		}
		if probePending {
			probe, ok := decision.Resource(probeKey)
			if !ok {
				return nil, false, dependencyError(datavisibility.ErrIncompleteDecision)
			}
			probePending = false
			if probe.Hidden || probe.ExistenceDisclosure == datavisibility.ExistenceConceal {
				return nil, false, nil
			}
		}
		for _, index := range batch {
			key := fmt.Sprintf("context-overview-group:%s:%04d", kind, index)
			resource, ok := decision.Resource(key)
			if !ok {
				return nil, false, dependencyError(datavisibility.ErrIncompleteDecision)
			}
			if !resource.Hidden && resource.ExistenceDisclosure != datavisibility.ExistenceConceal {
				visible[index] = true
			}
		}
		indices = indices[batchEnd:]
	}
	return visible, exact, nil
}

func overviewBatchEnd(indices []int, groups []contextgraph.ContextOverviewGroup, groupLimit int, initial ...contextgraph.AccessRef) int {
	refs := make(map[contextgraph.AccessRef]bool, contextgraph.MaxVisibilityAccessRefs)
	for _, ref := range initial {
		ref = contextgraph.NormalizeAccessRef(ref)
		if ref.Resolved() {
			refs[ref] = true
		}
	}
	for position, index := range indices {
		if position == groupLimit {
			return position
		}
		newRefs := 0
		for _, ref := range groups[index].AccessRefs {
			ref = contextgraph.NormalizeAccessRef(ref)
			if !refs[ref] {
				newRefs++
			}
		}
		if len(refs)+newRefs > contextgraph.MaxVisibilityAccessRefs {
			return position
		}
		for _, ref := range groups[index].AccessRefs {
			refs[contextgraph.NormalizeAccessRef(ref)] = true
		}
	}
	return len(indices)
}

func buildCompanyOverviewData(groups []contextgraph.ContextOverviewGroup, allowed map[int]bool, exactKinds map[datavisibility.ResourceKind]bool) CompanyOverviewData {
	counts := make(map[datavisibility.ResourceKind]int64, len(overviewResourceKinds))
	domainCounts := make(map[string]int64, len(overviewDomainOrder))
	newest := make(map[datavisibility.ResourceKind]*time.Time, len(overviewResourceKinds))
	for index, group := range groups {
		kind := overviewResourceKind(group.Kind)
		if !allowed[index] || !exactKinds[kind] {
			continue
		}
		counts[kind] += group.Count
		newest[kind] = latestOverviewTime(newest[kind], group.NewestAt)
		if group.Kind == contextgraph.ContextOverviewObject {
			domainCounts[overviewDomain(group.ObjectType)] += group.Count
		}
	}
	data := CompanyOverviewData{
		Objects:       metricForOverviewKind(exactKinds, counts, datavisibility.ResourceObject),
		Facts:         metricForOverviewKind(exactKinds, counts, datavisibility.ResourceFact),
		Relations:     metricForOverviewKind(exactKinds, counts, datavisibility.ResourceLink),
		Evidence:      metricForOverviewKind(exactKinds, counts, datavisibility.ResourceEvidence),
		RecentObjects: []ObjectItem{},
	}
	for _, kind := range overviewResourceKinds {
		if exactKinds[kind] {
			data.NewestUpdatedAt = latestOverviewTime(data.NewestUpdatedAt, newest[kind])
		}
	}
	data.Domains = make([]OverviewDomain, 0, len(overviewDomainOrder))
	if !exactKinds[datavisibility.ResourceObject] {
		data.DomainCount = ontologycontract.UnavailableCount()
		for _, id := range overviewDomainOrder {
			data.Domains = append(data.Domains, OverviewDomain{ID: id, Objects: ontologycontract.UnavailableCount()})
		}
		return data
	}
	var represented int64
	for _, id := range overviewDomainOrder {
		count := domainCounts[id]
		if count > 0 {
			represented++
		}
		data.Domains = append(data.Domains, OverviewDomain{ID: id, Objects: ontologycontract.ExactCount(count)})
	}
	data.DomainCount = ontologycontract.ExactCount(represented)
	return data
}

func (s *Service) recentOverviewObjects(ctx context.Context, principal identity.Principal, state visibilityState, budget *ontologycontract.BudgetTracker) ([]ObjectItem, error) {
	if !state.kindVisible[datavisibility.ResourceObject] {
		return []ObjectItem{}, nil
	}
	items := make([]ObjectItem, 0, overviewRecentLimit)
	after := contextgraph.ObjectSeek{}
	for batch := 0; batch < contextCandidateScanBatches && len(items) < overviewRecentLimit; batch++ {
		if err := budget.ConsumeStoreReads(1); err != nil {
			return items, nil
		}
		page, err := s.graph.ListVisibleObjects(ctx, contextgraph.ObjectQuery{
			TenantID: state.scope.Binding().TenantID, Visibility: contextgraph.CandidateVisibilityConstraint(), Limit: contextScanBatch, After: after,
		})
		if err != nil {
			mapped := queryError(ctx, budget, err)
			if errors.Is(mapped, ontologycontract.ErrQueryBudgetExceeded) {
				return items, nil
			}
			return nil, mapped
		}
		if err := budget.ConsumeCandidates(len(page.Items)); err != nil {
			return items, nil
		}
		rows, err := s.visibleObjectRows(ctx, principal, state, page.Items, false)
		if err != nil {
			if errors.Is(err, ontologycontract.ErrQueryBudgetExceeded) {
				return items, nil
			}
			return nil, err
		}
		for _, row := range rows {
			row.detail.TenantID = state.scope.Binding().TenantID
			items = append(items, row.detail.ObjectItem)
			if len(items) == overviewRecentLimit {
				break
			}
		}
		if len(items) == overviewRecentLimit || !page.HasMore {
			return items, nil
		}
		if len(page.Items) == 0 {
			return nil, dependencyError(contextgraph.ErrQueryInvalid)
		}
		last := page.Items[len(page.Items)-1]
		after = contextgraph.ObjectSeek{UpdatedAt: last.UpdatedAt, ID: last.ID}
	}
	return items, nil
}

func overviewResourceKind(kind contextgraph.ContextOverviewGroupKind) datavisibility.ResourceKind {
	switch kind {
	case contextgraph.ContextOverviewObject:
		return datavisibility.ResourceObject
	case contextgraph.ContextOverviewFact:
		return datavisibility.ResourceFact
	case contextgraph.ContextOverviewLink:
		return datavisibility.ResourceLink
	case contextgraph.ContextOverviewEvidence:
		return datavisibility.ResourceEvidence
	default:
		return ""
	}
}

func overviewGroupKind(kind datavisibility.ResourceKind) contextgraph.ContextOverviewGroupKind {
	switch kind {
	case datavisibility.ResourceObject:
		return contextgraph.ContextOverviewObject
	case datavisibility.ResourceFact:
		return contextgraph.ContextOverviewFact
	case datavisibility.ResourceLink:
		return contextgraph.ContextOverviewLink
	case datavisibility.ResourceEvidence:
		return contextgraph.ContextOverviewEvidence
	default:
		return ""
	}
}

func overviewGroupType(group contextgraph.ContextOverviewGroup) string {
	if value := strings.TrimSpace(group.ObjectType); value != "" {
		return value
	}
	return string(group.Kind)
}

func overviewDomain(objectType string) string {
	if domain := overviewObjectDomains[strings.ToLower(strings.TrimSpace(objectType))]; domain != "" {
		return domain
	}
	return "other"
}

func metricForOverviewKind(exact map[datavisibility.ResourceKind]bool, counts map[datavisibility.ResourceKind]int64, kind datavisibility.ResourceKind) ontologycontract.CountMetric {
	if !exact[kind] {
		return ontologycontract.UnavailableCount()
	}
	return ontologycontract.ExactCount(counts[kind])
}

func latestOverviewTime(left, right *time.Time) *time.Time {
	if left == nil {
		return cloneTime(right)
	}
	if right == nil || !right.After(*left) {
		return cloneTime(left)
	}
	return cloneTime(right)
}

func overviewDataRevision(data CompanyOverviewData) (string, error) {
	type recentRevision struct {
		TenantID string     `json:"tenant_id"`
		ID       string     `json:"id"`
		Updated  *time.Time `json:"updated_at,omitempty"`
	}
	recent := make([]recentRevision, len(data.RecentObjects))
	for index, item := range data.RecentObjects {
		recent[index] = recentRevision{TenantID: item.TenantID, ID: item.ID, Updated: item.UpdatedAt}
	}
	payload := struct {
		Objects     ontologycontract.CountMetric `json:"objects"`
		Facts       ontologycontract.CountMetric `json:"facts"`
		Relations   ontologycontract.CountMetric `json:"relations"`
		Evidence    ontologycontract.CountMetric `json:"evidence"`
		DomainCount ontologycontract.CountMetric `json:"domain_count"`
		Domains     []OverviewDomain             `json:"domains"`
		Recent      []recentRevision             `json:"recent"`
		Newest      *time.Time                   `json:"newest_updated_at,omitempty"`
	}{
		Objects: data.Objects, Facts: data.Facts, Relations: data.Relations, Evidence: data.Evidence,
		DomainCount: data.DomainCount, Domains: data.Domains, Recent: recent, Newest: data.NewestUpdatedAt,
	}
	return ontologycontract.CanonicalQueryHash(payload)
}
