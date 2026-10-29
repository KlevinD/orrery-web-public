package contextview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

var ErrUnavailable = ontologycontract.ErrUnavailable

const aggregateAccessRefLimit = contextgraph.MaxVisibilityAccessRefs + 1

type CursorSet struct {
	Encrypted *ontologycontract.CursorCodec
	Stateful  *ontologycontract.StateCursorStore
}

type Service struct {
	gate        *ontologyscope.ScopeAccessGate
	graph       GraphReader
	definitions DefinitionReader
	visibility  VisibilityPolicy
	cursors     CursorSet
	budgets     *ontologycontract.BudgetPolicy
	concurrency *ontologycontract.ConcurrencyLimiter
	now         func() time.Time
}

func NewService(gate *ontologyscope.ScopeAccessGate, graph GraphReader, definitions DefinitionReader, visibility VisibilityPolicy, cursors CursorSet, budgets *ontologycontract.BudgetPolicy, concurrency *ontologycontract.ConcurrencyLimiter) (*Service, error) {
	if gate == nil || graph == nil || definitions == nil || visibility == nil || cursors.Stateful == nil {
		return nil, ontologyscope.ErrDependencyUnavailable
	}
	var err error
	if budgets == nil {
		budgets, err = ontologycontract.NewBudgetPolicy(nil)
		if err != nil {
			return nil, err
		}
	}
	if concurrency == nil {
		concurrency, err = ontologycontract.NewConcurrencyLimiter(nil)
		if err != nil {
			return nil, err
		}
	}
	return &Service{gate: gate, graph: graph, definitions: definitions, visibility: visibility, cursors: cursors, budgets: budgets, concurrency: concurrency, now: time.Now}, nil
}

type visibilityState struct {
	scope          ontologyscope.AuthorizedScopeContext
	constraint     contextgraph.VisibilityConstraint
	kindRevisions  map[datavisibility.ResourceKind]string
	kindVisible    map[datavisibility.ResourceKind]bool
	revision       string
	cacheable      bool
	aggregateExact bool
}

// CompanyScopeTenantIDs returns bounded scope candidates only. The aggregate
// service resolves and authorizes every candidate before any tenant is exposed.
func (s *Service) CompanyScopeTenantIDs(ctx context.Context, limit int) (contextgraph.ContextTenantPage, error) {
	if s == nil || s.graph == nil || limit <= 0 || limit > contextgraph.MaxQueryPageSize {
		return contextgraph.ContextTenantPage{}, ontologycontract.ErrInvalidRequest
	}
	page, err := s.graph.ListContextScopeTenantIDs(ctx, limit)
	if err != nil {
		return contextgraph.ContextTenantPage{}, dependencyError(err)
	}
	return page, nil
}

var contextSummaryKinds = []datavisibility.ResourceKind{
	datavisibility.ResourceObject,
	datavisibility.ResourceLink,
	datavisibility.ResourceFact,
}

func (s *Service) Summary(ctx context.Context, principal identity.Principal, tenantID string) (ontologycontract.Summary[SummaryData], error) {
	tenantID = strings.TrimSpace(tenantID)
	if !validID(tenantID) {
		return ontologycontract.Summary[SummaryData]{}, ontologycontract.ErrInvalidRequest
	}
	authorized, err := s.authorize(ctx, principal, tenantID)
	if err != nil {
		return ontologycontract.Summary[SummaryData]{}, err
	}
	release, err := s.concurrency.Acquire(ctx, ontologycontract.ConcurrencySummary, authorized.Snapshot().PrincipalKey)
	if err != nil {
		return ontologycontract.Summary[SummaryData]{}, err
	}
	defer release()
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QueryStandard)
	if err != nil {
		return ontologycontract.Summary[SummaryData]{}, err
	}
	defer cancel()
	visibility, err := s.resolveVisibility(bounded, principal, authorized, budget, contextSummaryKinds...)
	if err != nil {
		return ontologycontract.Summary[SummaryData]{}, err
	}
	visibility, err = s.resolveAggregateVisibility(bounded, principal, authorized, visibility, budget)
	if err != nil {
		return ontologycontract.Summary[SummaryData]{}, err
	}
	if !visibility.aggregateExact {
		data := SummaryData{
			Objects: ontologycontract.UnavailableCount(), Relations: ontologycontract.UnavailableCount(),
			Facts: ontologycontract.UnavailableCount(), Evidence: ontologycontract.UnavailableCount(),
		}
		return ontologycontract.Summary[SummaryData]{Data: data, Meta: summaryMeta(s.now(), authorized, contextDataRevision(contextgraph.VisibleContextCounts{}, false))}, nil
	}
	if err := budget.ConsumeStoreReads(1); err != nil {
		return ontologycontract.Summary[SummaryData]{}, err
	}
	counts, err := s.graph.CountVisibleContext(bounded, authorized.Binding().TenantID, visibility.constraint)
	if err != nil {
		return ontologycontract.Summary[SummaryData]{}, queryError(bounded, budget, err)
	}
	data := SummaryData{
		Objects: ontologycontract.ExactCount(counts.Objects), Relations: ontologycontract.ExactCount(counts.Links),
		Facts: ontologycontract.ExactCount(counts.Facts), Evidence: ontologycontract.UnavailableCount(),
		OldestUpdatedAt: cloneTime(counts.OldestAt), NewestUpdatedAt: cloneTime(counts.NewestAt),
	}
	return ontologycontract.Summary[SummaryData]{Data: data, Meta: summaryMeta(s.now(), authorized, contextDataRevision(counts, true))}, nil
}

func (s *Service) resolveAggregateVisibility(ctx context.Context, principal identity.Principal, authorized ontologyscope.AuthorizedScopeContext, state visibilityState, budget *ontologycontract.BudgetTracker) (visibilityState, error) {
	state.aggregateExact = false
	if !state.constraint.IsCandidateScan() {
		return state, nil
	}
	binding := authorized.Binding()
	for _, kind := range contextSummaryKinds {
		probe := datavisibility.ResourceDescriptor{
			Key: "context-aggregate-policy:" + string(kind), Kind: kind, TenantID: binding.TenantID, Type: "aggregate",
			AccessRefs: []datavisibility.AccessRef{{Kind: binding.SourceObject.Type, ID: binding.SourceObject.ID, TenantID: binding.TenantID}},
			Payload:    datavisibility.AggregateProbe{},
		}
		probeDecision, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{
			Principal: identity.NormalizePrincipal(principal), Purpose: "ontology.context.read", Resources: []datavisibility.ResourceDescriptor{probe},
		})
		if err != nil {
			return visibilityState{}, dependencyError(err)
		}
		if err := verifyVisibilityRevision(state, kind, probeDecision); err != nil {
			return visibilityState{}, err
		}
		probeResource, ok := probeDecision.Resource(probe.Key)
		if !ok {
			return visibilityState{}, ontologyscope.ErrDependencyUnavailable
		}
		if probeResource.Hidden || probeResource.ExistenceDisclosure == datavisibility.ExistenceConceal {
			return state, nil
		}
	}
	if err := budget.ConsumeStoreReads(1); err != nil {
		return state, nil
	}
	page, err := s.graph.ListContextEntityAccessRefs(ctx, contextgraph.AccessRefQuery{TenantID: authorized.Binding().TenantID, Limit: aggregateAccessRefLimit})
	if err != nil {
		return visibilityState{}, queryError(ctx, budget, err)
	}
	if err := budget.ConsumeCandidates(len(page.Items)); err != nil || page.HasMore || len(page.Items) > contextgraph.MaxVisibilityAccessRefs {
		return state, nil
	}
	if len(page.Items) == 0 {
		state.constraint = contextgraph.VisibilityConstraint{}
		state.aggregateExact = true
		return state, nil
	}
	for index, ref := range page.Items {
		ref = contextgraph.NormalizeAccessRef(ref)
		if !ref.Resolved() {
			return visibilityState{}, dependencyError(contextgraph.ErrAccessRefConflict)
		}
		page.Items[index] = ref
	}
	var allowed []contextgraph.AccessRef
	for kindIndex, kind := range contextSummaryKinds {
		descriptors := make([]datavisibility.ResourceDescriptor, 0, len(page.Items))
		for index, ref := range page.Items {
			descriptors = append(descriptors, datavisibility.ResourceDescriptor{
				Key: fmt.Sprintf("context-aggregate-ref:%s:%03d", kind, index), Kind: kind,
				TenantID: authorized.Binding().TenantID, Type: ref.Kind,
				AccessRefs: []datavisibility.AccessRef{{Kind: ref.Kind, ID: ref.ID, TenantID: ref.TenantID}},
			})
		}
		decision, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{
			Principal: identity.NormalizePrincipal(principal), Purpose: "ontology.context.read", Resources: descriptors,
		})
		if err != nil {
			return visibilityState{}, dependencyError(err)
		}
		if err := verifyVisibilityRevision(state, kind, decision); err != nil {
			return visibilityState{}, err
		}
		kindAllowed := make([]contextgraph.AccessRef, 0, len(page.Items))
		for index, ref := range page.Items {
			resource, ok := decision.Resource(fmt.Sprintf("context-aggregate-ref:%s:%03d", kind, index))
			if !ok {
				return visibilityState{}, ontologyscope.ErrDependencyUnavailable
			}
			if !resource.Hidden && resource.ExistenceDisclosure != datavisibility.ExistenceConceal {
				kindAllowed = append(kindAllowed, ref)
			}
		}
		if kindIndex == 0 {
			allowed = kindAllowed
		} else if !sameAccessRefs(allowed, kindAllowed) {
			return state, nil
		}
	}
	state.constraint = contextgraph.VisibilityConstraint{AllowedAccessRefs: allowed}
	state.aggregateExact = true
	return state, nil
}

func (s *Service) authorize(ctx context.Context, principal identity.Principal, tenantID string) (ontologyscope.AuthorizedScopeContext, error) {
	if s == nil || s.gate == nil {
		return ontologyscope.AuthorizedScopeContext{}, ontologyscope.ErrDependencyUnavailable
	}
	authorized, err := s.gate.AuthorizeRead(ctx, identity.NormalizePrincipal(principal), strings.TrimSpace(tenantID))
	if ontologyscope.IsNonDisclosingReadError(err) {
		return ontologyscope.AuthorizedScopeContext{}, ErrUnavailable
	}
	return authorized, err
}

func (s *Service) resolveVisibility(ctx context.Context, principal identity.Principal, authorized ontologyscope.AuthorizedScopeContext, budget *ontologycontract.BudgetTracker, kinds ...datavisibility.ResourceKind) (visibilityState, error) {
	if budget == nil {
		return visibilityState{}, ontologycontract.ErrQueryBudgetExceeded
	}
	state := visibilityState{
		scope: authorized, kindRevisions: make(map[datavisibility.ResourceKind]string), kindVisible: make(map[datavisibility.ResourceKind]bool),
		// Candidate scans are always stateful. Their continuation represents
		// scanned source rows, not a reusable global authorization allow-list.
		cacheable: false,
	}
	binding := authorized.Binding()
	allVisible := true
	for _, kind := range normalizedContextKinds(kinds) {
		key := "context-scope-probe:" + string(kind)
		if kind == datavisibility.ResourceObject {
			key = "context-scope-probe"
		}
		probe := datavisibility.ResourceDescriptor{
			Key: key, Kind: kind, TenantID: binding.TenantID, Type: "scope",
			AccessRefs: []datavisibility.AccessRef{{Kind: binding.SourceObject.Type, ID: binding.SourceObject.ID, TenantID: binding.TenantID}},
		}
		decision, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{
			Principal: identity.NormalizePrincipal(principal), Purpose: "ontology.context.read", Resources: []datavisibility.ResourceDescriptor{probe},
		})
		if err != nil {
			return visibilityState{}, dependencyError(err)
		}
		resource, ok := decision.Resource(probe.Key)
		if !ok {
			return visibilityState{}, ontologyscope.ErrDependencyUnavailable
		}
		state.kindRevisions[kind] = strings.TrimSpace(decision.PolicyRevision)
		state.kindVisible[kind] = !resource.Hidden && resource.ExistenceDisclosure != datavisibility.ExistenceConceal
		allVisible = allVisible && state.kindVisible[kind]
	}
	state.revision = combinedPolicyRevision(authorized.Snapshot().PolicyRevision, state.kindRevisions)
	if allVisible {
		state.constraint = contextgraph.CandidateVisibilityConstraint()
	}
	return state, nil
}

func (s *Service) resolveFilterFields(ctx context.Context, principal identity.Principal, state visibilityState, kind datavisibility.ResourceKind, fields ...string) (map[string]bool, error) {
	if len(fields) == 0 {
		return nil, nil
	}
	binding := state.scope.Binding()
	key := "context-filter-probe:" + string(kind)
	decision, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{
		Principal: identity.NormalizePrincipal(principal),
		Purpose:   "ontology.context.read",
		Resources: []datavisibility.ResourceDescriptor{{
			Key: key, Kind: kind, TenantID: binding.TenantID, Type: "scope", FieldPaths: fields,
			AccessRefs: []datavisibility.AccessRef{{Kind: binding.SourceObject.Type, ID: binding.SourceObject.ID, TenantID: binding.TenantID}},
		}},
	})
	if err != nil {
		return nil, dependencyError(err)
	}
	if err := verifyVisibilityRevision(state, kind, decision); err != nil {
		return nil, err
	}
	resource, ok := decision.Resource(key)
	if !ok {
		return nil, ontologyscope.ErrDependencyUnavailable
	}
	if resource.Hidden || resource.ExistenceDisclosure == datavisibility.ExistenceConceal {
		return nil, nil
	}
	filterable := make(map[string]bool, len(resource.FilterableFields))
	for _, field := range resource.FilterableFields {
		filterable[field] = true
	}
	return filterable, nil
}

func verifyVisibilityRevision(state visibilityState, kind datavisibility.ResourceKind, decision datavisibility.Decision) error {
	want, ok := state.kindRevisions[kind]
	if !ok || strings.TrimSpace(decision.PolicyRevision) != want {
		return dependencyError(errors.New("context visibility revision changed during query"))
	}
	return nil
}

func combinedPolicyRevision(snapshotRevision string, kindRevisions map[datavisibility.ResourceKind]string) string {
	snapshotRevision = strings.TrimSpace(snapshotRevision)
	if len(kindRevisions) == 0 {
		return snapshotRevision
	}
	kinds := make([]string, 0, len(kindRevisions))
	for kind := range kindRevisions {
		kinds = append(kinds, string(kind))
	}
	sort.Strings(kinds)
	parts := []string{snapshotRevision}
	for _, kind := range kinds {
		parts = append(parts, kind, strings.TrimSpace(kindRevisions[datavisibility.ResourceKind(kind)]))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func normalizedContextKinds(kinds []datavisibility.ResourceKind) []datavisibility.ResourceKind {
	seen := map[datavisibility.ResourceKind]struct{}{}
	out := make([]datavisibility.ResourceKind, 0, len(kinds))
	for _, kind := range kinds {
		if _, duplicate := seen[kind]; duplicate {
			continue
		}
		seen[kind] = struct{}{}
		out = append(out, kind)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func sameAccessRefs(left, right []contextgraph.AccessRef) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func contextCursorBinding(state visibilityState, queryHash string) ontologycontract.CursorBinding {
	snapshot := state.scope.Snapshot()
	return ontologycontract.CursorBinding{
		PrincipalKey: snapshot.PrincipalKey, TenantID: state.scope.Binding().TenantID,
		BindingVersion: state.scope.Binding().BindingVersion, QueryHash: queryHash,
		AuthorizationRevision: snapshot.AuthorizationRevision, PolicyRevision: state.revision,
	}
}

func (s *Service) resolveSeekCursor(token string, binding ontologycontract.CursorBinding, cacheable bool) (contextgraph.ObjectSeek, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return contextgraph.ObjectSeek{}, nil
	}
	position, err := s.resolveCursorPosition(token, binding, cacheable)
	if err != nil {
		return contextgraph.ObjectSeek{}, err
	}
	if len(position.Seek) != 2 {
		return contextgraph.ObjectSeek{}, ontologycontract.ErrInvalidCursor
	}
	nanos, err := strconv.ParseInt(position.Seek[0], 10, 64)
	if err != nil || nanos <= 0 || !validID(position.Seek[1]) {
		return contextgraph.ObjectSeek{}, ontologycontract.ErrInvalidCursor
	}
	return contextgraph.ObjectSeek{UpdatedAt: time.Unix(0, nanos).UTC(), ID: position.Seek[1]}, nil
}

func (s *Service) resolveCursorPosition(token string, binding ontologycontract.CursorBinding, cacheable bool) (ontologycontract.CursorPosition, error) {
	switch {
	case strings.HasPrefix(token, "oc1.") && cacheable && s.cursors.Encrypted != nil:
		return s.cursors.Encrypted.Decode(token, binding)
	case strings.HasPrefix(token, "ocs1."):
		return s.cursors.Stateful.Resolve(token, binding)
	case strings.HasPrefix(token, "oc1."):
		return ontologycontract.CursorPosition{}, ontologycontract.ErrCursorStale
	default:
		return ontologycontract.CursorPosition{}, ontologycontract.ErrInvalidCursor
	}
}

func (s *Service) issueSeekCursor(binding ontologycontract.CursorBinding, cacheable bool, seek contextgraph.ObjectSeek) (string, error) {
	if seek.UpdatedAt.IsZero() || !validID(seek.ID) {
		return "", ontologycontract.ErrInvalidRequest
	}
	position := ontologycontract.CursorPosition{Binding: binding, Seek: []string{strconv.FormatInt(seek.UpdatedAt.UTC().UnixNano(), 10), seek.ID}}
	if cacheable && s.cursors.Encrypted != nil {
		return s.cursors.Encrypted.Encode(position)
	}
	return s.cursors.Stateful.Issue(position)
}

func summaryMeta(now time.Time, scope ontologyscope.AuthorizedScopeContext, revision string) ontologycontract.SummaryMeta {
	meta := ontologycontract.SummaryMeta{AsOf: now.UTC(), DataRevision: revision}
	if value := strings.TrimSpace(scope.Snapshot().AuthorizationRevision); value != "" {
		meta.AuthorizationRevision = &value
	}
	return meta
}

func contextDataRevision(counts contextgraph.VisibleContextCounts, exact bool) string {
	oldest, newest := int64(0), int64(0)
	if counts.OldestAt != nil {
		oldest = counts.OldestAt.UTC().UnixNano()
	}
	if counts.NewestAt != nil {
		newest = counts.NewestAt.UTC().UnixNano()
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%t:%d:%d:%d:%d:%d", exact, counts.Objects, counts.Links, counts.Facts, oldest, newest)))
	return hex.EncodeToString(sum[:])
}

func dependencyError(err error) error {
	if errors.Is(err, ontologycontract.ErrInvalidRequest) || errors.Is(err, ontologycontract.ErrInvalidCursor) || errors.Is(err, ontologycontract.ErrCursorStale) || errors.Is(err, ontologycontract.ErrQueryBudgetExceeded) {
		return err
	}
	return fmt.Errorf("%w: %v", ontologyscope.ErrDependencyUnavailable, err)
}

func queryError(ctx context.Context, budget *ontologycontract.BudgetTracker, err error) error {
	if ctx != nil && ctx.Err() != nil {
		return budget.CheckContext(ctx)
	}
	if errors.Is(err, contextgraph.ErrNotFound) {
		return ErrUnavailable
	}
	return dependencyError(err)
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := value.UTC()
	return &copy
}
