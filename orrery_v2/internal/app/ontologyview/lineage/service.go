package lineage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

var ErrUnavailable = ontologycontract.ErrUnavailable

const (
	lineageScanBatch            = contextgraph.MaxQueryPageSize
	lineageCandidateScanBatches = 8
	maxMappingCandidates        = 20
	maxSummaryFacts             = 200
)

type CursorSet struct {
	Encrypted *ontologycontract.CursorCodec
	Stateful  *ontologycontract.StateCursorStore
}

type Service struct {
	gate        *ontologyscope.ScopeAccessGate
	graph       GraphReader
	events      SafeEventReader
	rules       MappingRuleReader
	visibility  VisibilityPolicy
	cursors     CursorSet
	budgets     *ontologycontract.BudgetPolicy
	concurrency *ontologycontract.ConcurrencyLimiter
	now         func() time.Time
}

func NewService(
	gate *ontologyscope.ScopeAccessGate,
	graph GraphReader,
	events SafeEventReader,
	rules MappingRuleReader,
	visibility VisibilityPolicy,
	cursors CursorSet,
	budgets *ontologycontract.BudgetPolicy,
	concurrency *ontologycontract.ConcurrencyLimiter,
) (*Service, error) {
	if gate == nil || graph == nil || events == nil || rules == nil || visibility == nil || cursors.Stateful == nil {
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
	return &Service{
		gate: gate, graph: graph, events: events, rules: rules, visibility: visibility,
		cursors: cursors, budgets: budgets, concurrency: concurrency, now: time.Now,
	}, nil
}

type visibilityState struct {
	scope         ontologyscope.AuthorizedScopeContext
	constraint    contextgraph.VisibilityConstraint
	kindRevisions map[datavisibility.ResourceKind]string
	revision      string
	cacheable     bool
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
		scope: authorized, kindRevisions: make(map[datavisibility.ResourceKind]string), cacheable: false,
	}
	binding := authorized.Binding()
	allVisible := true
	for _, kind := range normalizedLineageKinds(kinds) {
		key := "lineage-scope-probe:" + string(kind)
		if kind == datavisibility.ResourceFact {
			key = "lineage-scope-probe"
		}
		typeName := "scope"
		if kind == datavisibility.ResourceDefinition {
			typeName = "mapping"
		}
		probe := datavisibility.ResourceDescriptor{
			Key: key, Kind: kind, TenantID: binding.TenantID, Type: typeName,
			AccessRefs: []datavisibility.AccessRef{{Kind: binding.SourceObject.Type, ID: binding.SourceObject.ID, TenantID: binding.TenantID}},
		}
		decision, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{
			Principal: identity.NormalizePrincipal(principal), Purpose: "ontology.lineage.read", Resources: []datavisibility.ResourceDescriptor{probe},
		})
		if err != nil {
			return visibilityState{}, dependencyError(err)
		}
		resource, ok := decision.Resource(probe.Key)
		if !ok {
			return visibilityState{}, ontologyscope.ErrDependencyUnavailable
		}
		state.kindRevisions[kind] = strings.TrimSpace(decision.PolicyRevision)
		allVisible = allVisible && !hiddenDecision(resource)
	}
	state.revision = combinedPolicyRevision(authorized.Snapshot().PolicyRevision, state.kindRevisions)
	if allVisible {
		state.constraint = contextgraph.CandidateVisibilityConstraint()
	}
	return state, nil
}

func verifyVisibilityRevision(state visibilityState, kind datavisibility.ResourceKind, decision datavisibility.Decision) error {
	want, ok := state.kindRevisions[kind]
	if !ok || strings.TrimSpace(decision.PolicyRevision) != want {
		return dependencyError(errors.New("lineage visibility revision changed during query"))
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

func normalizedLineageKinds(kinds []datavisibility.ResourceKind) []datavisibility.ResourceKind {
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

func (s *Service) getVisibleFact(ctx context.Context, principal identity.Principal, tenantID, factID string, state visibilityState, budget *ontologycontract.BudgetTracker, factFields, subjectFields []string) (contextgraph.FactDetail, datavisibility.ResourceDecision, datavisibility.ResourceDecision, error) {
	if err := budget.ConsumeStoreReads(1); err != nil {
		return contextgraph.FactDetail{}, datavisibility.ResourceDecision{}, datavisibility.ResourceDecision{}, err
	}
	detail, err := s.graph.GetVisibleFact(ctx, tenantID, factID, state.constraint)
	if err != nil {
		return contextgraph.FactDetail{}, datavisibility.ResourceDecision{}, datavisibility.ResourceDecision{}, queryError(ctx, budget, err)
	}
	if err := budget.ConsumeCandidates(1); err != nil {
		return contextgraph.FactDetail{}, datavisibility.ResourceDecision{}, datavisibility.ResourceDecision{}, err
	}
	factKey := "fact:" + detail.Fact.ID
	subjectKey := "subject:" + detail.Subject.ObjectType + ":" + detail.Subject.ID
	factDescriptor := datavisibility.ResourceDescriptor{
		Key: factKey, Kind: datavisibility.ResourceFact, TenantID: detail.Fact.TenantID,
		Type: detail.Fact.Predicate, FieldPaths: append([]string(nil), factFields...),
		AccessRefs: visibilityAccessRefs(detail.Fact.SourceAccessRef), Payload: detail.Fact,
	}
	factSet, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{
		Principal: identity.NormalizePrincipal(principal), Purpose: "ontology.lineage.read", Resources: []datavisibility.ResourceDescriptor{factDescriptor},
	})
	if err != nil {
		return contextgraph.FactDetail{}, datavisibility.ResourceDecision{}, datavisibility.ResourceDecision{}, dependencyError(err)
	}
	if err := verifyVisibilityRevision(state, datavisibility.ResourceFact, factSet); err != nil {
		return contextgraph.FactDetail{}, datavisibility.ResourceDecision{}, datavisibility.ResourceDecision{}, err
	}
	subjectDescriptor := datavisibility.ResourceDescriptor{
		Key: subjectKey, Kind: datavisibility.ResourceObject, TenantID: detail.Subject.TenantID,
		Type: detail.Subject.ObjectType, FieldPaths: append([]string(nil), subjectFields...),
		AccessRefs: visibilityAccessRefs(detail.Subject.OwnerAccessRef), Payload: detail.Subject,
	}
	subjectSet, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{
		Principal: identity.NormalizePrincipal(principal), Purpose: "ontology.lineage.read", Resources: []datavisibility.ResourceDescriptor{subjectDescriptor},
	})
	if err != nil {
		return contextgraph.FactDetail{}, datavisibility.ResourceDecision{}, datavisibility.ResourceDecision{}, dependencyError(err)
	}
	if err := verifyVisibilityRevision(state, datavisibility.ResourceObject, subjectSet); err != nil {
		return contextgraph.FactDetail{}, datavisibility.ResourceDecision{}, datavisibility.ResourceDecision{}, err
	}
	factDecision, factOK := factSet.Resource(factKey)
	subjectDecision, subjectOK := subjectSet.Resource(subjectKey)
	if !factOK || !subjectOK {
		return contextgraph.FactDetail{}, datavisibility.ResourceDecision{}, datavisibility.ResourceDecision{}, dependencyError(datavisibility.ErrIncompleteDecision)
	}
	if hiddenDecision(factDecision) || hiddenDecision(subjectDecision) {
		return contextgraph.FactDetail{}, datavisibility.ResourceDecision{}, datavisibility.ResourceDecision{}, ErrUnavailable
	}
	return detail, factDecision, subjectDecision, nil
}

func lineageCursorBinding(state visibilityState, queryHash string) ontologycontract.CursorBinding {
	snapshot := state.scope.Snapshot()
	return ontologycontract.CursorBinding{
		PrincipalKey: snapshot.PrincipalKey, TenantID: state.scope.Binding().TenantID,
		BindingVersion: state.scope.Binding().BindingVersion, QueryHash: queryHash,
		AuthorizationRevision: snapshot.AuthorizationRevision, PolicyRevision: state.revision,
	}
}

func summaryMeta(now time.Time, scope ontologyscope.AuthorizedScopeContext, revision string) ontologycontract.SummaryMeta {
	meta := ontologycontract.SummaryMeta{AsOf: now.UTC(), DataRevision: revision}
	if value := strings.TrimSpace(scope.Snapshot().AuthorizationRevision); value != "" {
		meta.AuthorizationRevision = &value
	}
	return meta
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

func validID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 256
}

func hiddenDecision(decision datavisibility.ResourceDecision) bool {
	return decision.Hidden || decision.ExistenceDisclosure == datavisibility.ExistenceConceal
}

func maskedFieldSet(decision datavisibility.ResourceDecision) map[string]bool {
	out := make(map[string]bool, len(decision.Fields.Masked))
	for _, field := range decision.Fields.Masked {
		out[field] = true
	}
	return out
}

func visibilityAccessRefs(refs ...contextgraph.AccessRef) []datavisibility.AccessRef {
	out := make([]datavisibility.AccessRef, 0, len(refs))
	seen := map[contextgraph.AccessRef]struct{}{}
	for _, ref := range refs {
		ref = contextgraph.NormalizeAccessRef(ref)
		if !ref.Resolved() {
			continue
		}
		if _, exists := seen[ref]; exists {
			continue
		}
		seen[ref] = struct{}{}
		out = append(out, datavisibility.AccessRef{Kind: ref.Kind, ID: ref.ID, TenantID: ref.TenantID})
	}
	return out
}

func visibleTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value.UTC()
	return &copy
}

func floatValue(value float64) *float64 {
	copy := value
	return &copy
}
