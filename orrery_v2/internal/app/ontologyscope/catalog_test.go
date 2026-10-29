package ontologyscope

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type catalogTestResolver struct {
	bindings map[string]ScopeBinding
}

func (r catalogTestResolver) Resolve(_ context.Context, tenantID string) (ScopeBinding, error) {
	if tenantID == "system" {
		return SystemBinding(), nil
	}
	binding, ok := r.bindings[tenantID]
	if !ok {
		return ScopeBinding{}, ErrBindingNotFound
	}
	return binding, nil
}

type catalogTestAuthorizer struct {
	readable       map[string]bool
	manageable     map[string]bool
	errFor         map[string]error
	waitForContext bool
	delay          time.Duration
	entered        chan<- string
	release        <-chan struct{}
	featureCalls   *atomic.Int32
}

type catalogBatchAuthorizer struct {
	catalogTestAuthorizer
	batchCalls    int
	batchRequests []ObjectAuthorizationRequest
}

func (a *catalogBatchAuthorizer) AuthorizeObjects(ctx context.Context, principal identity.Principal, requests []ObjectAuthorizationRequest) ([]bool, error) {
	a.batchCalls++
	a.batchRequests = append(a.batchRequests, requests...)
	allowed := make([]bool, len(requests))
	for index, request := range requests {
		err := a.catalogTestAuthorizer.AuthorizeObject(ctx, principal, request.Action, request.Object, request.AuthorizationTenantID)
		if errors.Is(err, ErrAccessDenied) {
			continue
		}
		if err != nil {
			return nil, err
		}
		allowed[index] = true
	}
	return allowed, nil
}

func (a catalogTestAuthorizer) AuthorizeFeature(context.Context, identity.Principal, string) error {
	if a.featureCalls != nil {
		a.featureCalls.Add(1)
	}
	return nil
}

func (a catalogTestAuthorizer) AuthorizeObject(ctx context.Context, _ identity.Principal, action string, object identity.ObjectRef, _ string) error {
	if a.waitForContext {
		<-ctx.Done()
		return ctx.Err()
	}
	if a.entered != nil {
		select {
		case a.entered <- object.ID:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if a.release != nil {
		select {
		case <-a.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if a.delay > 0 {
		timer := time.NewTimer(a.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err := a.errFor[object.ID]; err != nil {
		return err
	}
	if stringsHasSuffix(action, ".manage") || action == FeatureManageAction {
		if !a.manageable[object.ID] {
			return ErrAccessDenied
		}
		return nil
	}
	if !a.readable[object.ID] {
		return ErrAccessDenied
	}
	return nil
}

func TestCatalogAuthorizesCandidateWindowConcurrentlyInCatalogOrder(t *testing.T) {
	principal := testGatePrincipal()
	bindings := map[string]ScopeBinding{
		"project:1": catalogTestBinding("project:1", KindProject, "project", "project-1"),
		"project:2": catalogTestBinding("project:2", KindProject, "project", "project-2"),
	}
	entered := make(chan string, 3)
	release := make(chan struct{})
	catalog := newCatalogForTest(t, principal, bindings, catalogTestAuthorizer{
		readable:   map[string]bool{"project-1": true, "project-2": true, "global": true},
		manageable: map[string]bool{}, errFor: map[string]error{}, entered: entered, release: release,
	}, catalogTestSource{kind: KindProject, candidates: []CatalogCandidate{
		{TenantID: "project:1", Kind: KindProject, Seek: "project-1", Label: "One"},
		{TenantID: "project:2", Kind: KindProject, Seek: "project-2", Label: "Two"},
	}})
	type listResult struct {
		page ontologycontract.CursorPage[ScopeCatalogItem]
		err  error
	}
	done := make(chan listResult, 1)
	go func() {
		page, err := catalog.List(context.Background(), principal, CatalogQuery{Limit: 10, BudgetClass: ontologycontract.QueryCompanyOverview})
		done <- listResult{page: page, err: err}
	}()

	first := <-entered
	var second string
	select {
	case second = <-entered:
	case <-time.After(time.Second):
		close(release)
		<-done
		t.Fatalf("only %q authorization entered before release", first)
	}
	close(release)
	result := <-done
	if result.err != nil {
		t.Fatalf("List: %v", result.err)
	}
	enteredIDs := map[string]bool{first: true, second: true}
	if !enteredIDs["project-1"] || !enteredIDs["project-2"] || len(enteredIDs) != 2 {
		t.Fatalf("authorization entries = [%q %q]", first, second)
	}
	if len(result.page.Items) != 3 || result.page.Items[0].TenantID != "project:1" || result.page.Items[1].TenantID != "project:2" || result.page.Items[2].TenantID != "system" {
		t.Fatalf("catalog items = %+v", result.page.Items)
	}
}

type catalogTestSnapshots struct {
	calls *atomic.Int32
}

func (s catalogTestSnapshots) Capture(_ context.Context, principal identity.Principal) (authorization.AuthorizationSnapshot, error) {
	if s.calls != nil {
		s.calls.Add(1)
	}
	return authorization.AuthorizationSnapshot{
		PrincipalKey:          authorization.PrincipalCacheKey(principal),
		AuthorizationRevision: "auth-1",
		PolicyRevision:        "policy-1",
	}, nil
}

func TestCatalogReusesFeatureAuthorizationAndSnapshotAcrossCandidateWindow(t *testing.T) {
	principal := testGatePrincipal()
	bindings := map[string]ScopeBinding{
		"project:1": catalogTestBinding("project:1", KindProject, "project", "project-1"),
		"project:2": catalogTestBinding("project:2", KindProject, "project", "project-2"),
	}
	featureCalls := &atomic.Int32{}
	snapshotCalls := &atomic.Int32{}
	gate := NewScopeAccessGate(catalogTestResolver{bindings: bindings}, catalogTestAuthorizer{
		readable:     map[string]bool{"project-1": true, "project-2": true},
		errFor:       map[string]error{},
		featureCalls: featureCalls,
	}, catalogTestSnapshots{calls: snapshotCalls})
	stateful, err := ontologycontract.NewStateCursorStore(ontologycontract.StateCursorOptions{MaxEntries: 100})
	if err != nil {
		t.Fatalf("NewStateCursorStore: %v", err)
	}
	catalog, err := NewCatalog(gate, CatalogCursorSet{Stateful: stateful}, nil, catalogTestSource{
		kind: KindProject,
		candidates: []CatalogCandidate{
			{TenantID: "project:1", Kind: KindProject, Seek: "project-1", Label: "One"},
			{TenantID: "project:2", Kind: KindProject, Seek: "project-2", Label: "Two"},
		},
	})
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}

	page, err := catalog.List(context.Background(), principal, CatalogQuery{
		Kind: KindProject, Limit: 10, BudgetClass: ontologycontract.QueryCompanyOverview,
	})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("List = %+v, err=%v", page, err)
	}
	if got := featureCalls.Load(); got != 1 {
		t.Fatalf("feature authorization calls = %d, want 1", got)
	}
	if got := snapshotCalls.Load(); got != 3 {
		t.Fatalf("snapshot capture calls = %d, want 3", got)
	}
}

type catalogTestUncacheableSnapshots struct{}

func (catalogTestUncacheableSnapshots) Capture(_ context.Context, principal identity.Principal) (authorization.AuthorizationSnapshot, error) {
	return authorization.AuthorizationSnapshot{PrincipalKey: authorization.PrincipalCacheKey(principal)}, nil
}

type catalogChangingSnapshots struct {
	calls atomic.Int32
}

func (s *catalogChangingSnapshots) Capture(_ context.Context, principal identity.Principal) (authorization.AuthorizationSnapshot, error) {
	revision := "auth-1"
	if s.calls.Add(1) > 2 {
		revision = "auth-2"
	}
	return authorization.AuthorizationSnapshot{
		PrincipalKey:          authorization.PrincipalCacheKey(principal),
		AuthorizationRevision: revision,
		PolicyRevision:        "policy-1",
	}, nil
}

func TestCatalogFailsClosedWhenRevisionChangesDuringCandidateWindow(t *testing.T) {
	principal := testGatePrincipal()
	binding := catalogTestBinding("project:1", KindProject, "project", "project-1")
	gate := NewScopeAccessGate(catalogTestResolver{bindings: map[string]ScopeBinding{"project:1": binding}}, catalogTestAuthorizer{
		readable: map[string]bool{"project-1": true}, errFor: map[string]error{},
	}, &catalogChangingSnapshots{})
	stateful, err := ontologycontract.NewStateCursorStore(ontologycontract.StateCursorOptions{MaxEntries: 100})
	if err != nil {
		t.Fatalf("NewStateCursorStore: %v", err)
	}
	catalog, err := NewCatalog(gate, CatalogCursorSet{Stateful: stateful}, nil, catalogTestSource{
		kind:       KindProject,
		candidates: []CatalogCandidate{{TenantID: "project:1", Kind: KindProject, Seek: "project-1", Label: "One"}},
	})
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}

	_, err = catalog.List(context.Background(), principal, CatalogQuery{
		Kind: KindProject, Limit: 10, BudgetClass: ontologycontract.QueryCompanyOverview,
	})
	if !errors.Is(err, ErrDependencyUnavailable) {
		t.Fatalf("List revision change error = %v", err)
	}
}

type catalogTestSource struct {
	kind       Kind
	candidates []CatalogCandidate
}

func (s catalogTestSource) Kind() Kind { return s.kind }

func (s catalogTestSource) ListCandidates(_ context.Context, after string, limit int) (CandidatePage, error) {
	page := CandidatePage{}
	for _, candidate := range s.candidates {
		if candidate.Seek <= after {
			continue
		}
		page.Scanned++
		if len(page.Items) < limit {
			page.Items = append(page.Items, candidate)
			page.NextAfter = candidate.Seek
			continue
		}
		page.HasMore = true
		break
	}
	return page, nil
}

func (s catalogTestSource) GetCandidate(_ context.Context, binding ScopeBinding) (CatalogCandidate, bool, error) {
	for _, candidate := range s.candidates {
		if candidate.TenantID == binding.TenantID {
			return candidate, true, nil
		}
	}
	return CatalogCandidate{}, false, nil
}

func TestCatalogFiltersEveryCandidateAndPaginatesOnlyVisibleScopes(t *testing.T) {
	principal := testGatePrincipal()
	bindings := map[string]ScopeBinding{
		"org-1":     catalogTestBinding("org-1", KindOrganization, "organization", "org-1"),
		"project:1": catalogTestBinding("project:1", KindProject, "project", "project-1"),
		"project:2": catalogTestBinding("project:2", KindProject, "project", "project-2"),
		"project:3": catalogTestBinding("project:3", KindProject, "project", "project-3"),
	}
	authorizer := catalogTestAuthorizer{
		readable:   map[string]bool{"org-1": true, "project-1": true, "project-3": true, "global": true},
		manageable: map[string]bool{"project-1": true},
		errFor:     map[string]error{},
	}
	catalog := newCatalogForTest(t, principal, bindings, authorizer,
		catalogTestSource{kind: KindOrganization, candidates: []CatalogCandidate{{TenantID: "org-1", Kind: KindOrganization, Seek: "org-1", Label: "Visible Org"}}},
		catalogTestSource{kind: KindProject, candidates: []CatalogCandidate{
			{TenantID: "project:1", Kind: KindProject, Seek: "project-1", Label: "Alpha", ParentTenantID: "org-1"},
			{TenantID: "project:2", Kind: KindProject, Seek: "project-2", Label: "Hidden"},
			{TenantID: "project:3", Kind: KindProject, Seek: "project-3", Label: "Gamma"},
		}},
	)

	first, err := catalog.List(context.Background(), principal, CatalogQuery{Limit: 2})
	if err != nil {
		t.Fatalf("List first page: %v", err)
	}
	if !first.Page.HasMore || first.Page.NextCursor == "" || len(first.Items) != 2 {
		t.Fatalf("first page = %+v", first)
	}
	if first.Items[0].TenantID != "org-1" || first.Items[1].TenantID != "project:1" {
		t.Fatalf("first items = %+v", first.Items)
	}
	if len(first.Items[1].Capabilities) != 2 || first.Items[1].ParentTenantID != "org-1" {
		t.Fatalf("authorized metadata = %+v", first.Items[1])
	}
	second, err := catalog.List(context.Background(), principal, CatalogQuery{Limit: 2, Cursor: first.Page.NextCursor})
	if err != nil {
		t.Fatalf("List second page: %v", err)
	}
	if second.Page.HasMore || len(second.Items) != 2 || second.Items[0].TenantID != "project:3" || second.Items[1].TenantID != "system" {
		t.Fatalf("second page = %+v", second)
	}
}

func TestCatalogUsesCompanyOverviewBudgetOnlyWhenRequested(t *testing.T) {
	principal := testGatePrincipal()
	binding := catalogTestBinding("org-1", KindOrganization, "organization", "org-1")
	catalog := newCatalogForTest(t, principal, map[string]ScopeBinding{"org-1": binding}, catalogTestAuthorizer{
		readable: map[string]bool{"org-1": true, "global": true}, manageable: map[string]bool{"org-1": true, "global": true}, errFor: map[string]error{}, delay: 10 * time.Millisecond,
	}, catalogTestSource{kind: KindOrganization, candidates: []CatalogCandidate{{TenantID: "org-1", Kind: KindOrganization, Seek: "org-1", Label: "Org"}}})
	budgets, err := ontologycontract.NewBudgetPolicy(map[ontologycontract.QueryClass]ontologycontract.QueryBudget{
		ontologycontract.QueryStandard:        {Deadline: 5 * time.Millisecond},
		ontologycontract.QueryCompanyOverview: {Deadline: 100 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("NewBudgetPolicy: %v", err)
	}
	catalog.budgets = budgets

	if _, err := catalog.List(context.Background(), principal, CatalogQuery{Limit: 10}); !errors.Is(err, ontologycontract.ErrQueryBudgetExceeded) {
		t.Fatalf("standard budget error = %v", err)
	}
	page, err := catalog.List(context.Background(), principal, CatalogQuery{Limit: 10, BudgetClass: ontologycontract.QueryCompanyOverview})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("company overview catalog page = %+v, err=%v", page, err)
	}
	for _, item := range page.Items {
		if len(item.Capabilities) != 1 || item.Capabilities[0] != CapabilityRead {
			t.Fatalf("company overview capabilities = %+v, want read only", item.Capabilities)
		}
	}
}

func TestCatalogResolveCandidatesUsesBatchAuthorization(t *testing.T) {
	principal := testGatePrincipal()
	bindings := map[string]ScopeBinding{
		"project:1": catalogTestBinding("project:1", KindProject, "project", "project-1"),
		"project:2": catalogTestBinding("project:2", KindProject, "project", "project-2"),
	}
	authorizer := &catalogBatchAuthorizer{catalogTestAuthorizer: catalogTestAuthorizer{
		readable: map[string]bool{"project-1": true, "global": true}, manageable: map[string]bool{}, errFor: map[string]error{},
	}}
	catalog := newCatalogForTest(t, principal, bindings, authorizer, catalogTestSource{kind: KindProject, candidates: []CatalogCandidate{
		{TenantID: "project:1", Kind: KindProject, Seek: "project-1", Label: "One"},
		{TenantID: "project:2", Kind: KindProject, Seek: "project-2", Label: "Two"},
	}})

	items, err := catalog.ResolveCandidates(context.Background(), principal, []string{"project:1", "project:2"})
	if err != nil {
		t.Fatalf("ResolveCandidates: %v", err)
	}
	if len(items) != 1 || items[0].TenantID != "project:1" {
		t.Fatalf("items = %+v", items)
	}
	if authorizer.batchCalls != 1 || len(authorizer.batchRequests) != 2 {
		t.Fatalf("batch calls = %d, requests = %+v", authorizer.batchCalls, authorizer.batchRequests)
	}
}

func TestCatalogSearchCursorAndResolveArePrincipalBoundAndNonDisclosing(t *testing.T) {
	principal := testGatePrincipal()
	bindings := map[string]ScopeBinding{
		"project:1": catalogTestBinding("project:1", KindProject, "project", "project-1"),
		"project:2": catalogTestBinding("project:2", KindProject, "project", "project-2"),
	}
	authorizer := catalogTestAuthorizer{
		readable:   map[string]bool{"project-1": true, "project-2": true},
		manageable: map[string]bool{},
		errFor:     map[string]error{},
	}
	catalog := newCatalogForTest(t, principal, bindings, authorizer, catalogTestSource{kind: KindProject, candidates: []CatalogCandidate{
		{TenantID: "project:1", Kind: KindProject, Seek: "project-1", Label: "Alpha plan"},
		{TenantID: "project:2", Kind: KindProject, Seek: "project-2", Label: "Alpha result"},
	}})
	page, err := catalog.List(context.Background(), principal, CatalogQuery{Search: "alpha", Limit: 1})
	if err != nil || !page.Page.HasMore {
		t.Fatalf("List search page = %+v, err=%v", page, err)
	}
	if _, err := catalog.List(context.Background(), principal, CatalogQuery{Search: "different", Limit: 1, Cursor: page.Page.NextCursor}); !errors.Is(err, ontologycontract.ErrCursorStale) {
		t.Fatalf("query-bound cursor error = %v", err)
	}
	other := principal
	other.Subject = "other"
	if _, err := catalog.List(context.Background(), other, CatalogQuery{Search: "alpha", Limit: 1, Cursor: page.Page.NextCursor}); !errors.Is(err, ontologycontract.ErrCursorStale) {
		t.Fatalf("principal-bound cursor error = %v", err)
	}
	resolved, err := catalog.Resolve(context.Background(), principal, "project:1")
	if err != nil || resolved.BindingVersion != 1 || resolved.Label != "Alpha plan" {
		t.Fatalf("Resolve = %+v, err=%v", resolved, err)
	}
	authorizer.readable["project-1"] = false
	catalog = newCatalogForTest(t, principal, bindings, authorizer, catalogTestSource{kind: KindProject, candidates: []CatalogCandidate{{TenantID: "project:1", Kind: KindProject, Seek: "project-1", Label: "Secret"}}})
	if _, err := catalog.Resolve(context.Background(), principal, "project:1"); !errors.Is(err, ErrBindingNotFound) {
		t.Fatalf("hidden resolve error = %v", err)
	}
	if _, err := catalog.Resolve(context.Background(), principal, "project:missing"); !errors.Is(err, ErrBindingNotFound) {
		t.Fatalf("missing resolve error = %v", err)
	}
}

func TestCatalogKindFilterSkipsOtherSourcesAndBindsCursor(t *testing.T) {
	principal := testGatePrincipal()
	bindings := map[string]ScopeBinding{
		"org-1":     catalogTestBinding("org-1", KindOrganization, "organization", "org-1"),
		"project:1": catalogTestBinding("project:1", KindProject, "project", "project-1"),
		"project:2": catalogTestBinding("project:2", KindProject, "project", "project-2"),
	}
	authorizer := catalogTestAuthorizer{
		readable: map[string]bool{"org-1": true, "project-1": true, "project-2": true}, manageable: map[string]bool{}, errFor: map[string]error{},
	}
	catalog := newCatalogForTest(t, principal, bindings, authorizer,
		catalogTestSource{kind: KindOrganization, candidates: []CatalogCandidate{{TenantID: "org-1", Kind: KindOrganization, Seek: "org-1", Label: "Org"}}},
		catalogTestSource{kind: KindProject, candidates: []CatalogCandidate{
			{TenantID: "project:1", Kind: KindProject, Seek: "project-1", Label: "One"},
			{TenantID: "project:2", Kind: KindProject, Seek: "project-2", Label: "Two"},
		}},
	)
	first, err := catalog.List(context.Background(), principal, CatalogQuery{Kind: KindProject, Limit: 1})
	if err != nil || len(first.Items) != 1 || first.Items[0].Kind != KindProject || !first.Page.HasMore {
		t.Fatalf("filtered first page = %+v, err=%v", first, err)
	}
	if _, err := catalog.List(context.Background(), principal, CatalogQuery{Kind: KindOrganization, Limit: 1, Cursor: first.Page.NextCursor}); !errors.Is(err, ontologycontract.ErrCursorStale) {
		t.Fatalf("kind-bound cursor error = %v", err)
	}
	if _, err := catalog.List(context.Background(), principal, CatalogQuery{Kind: Kind("unknown"), Limit: 1}); !errors.Is(err, ontologycontract.ErrInvalidRequest) {
		t.Fatalf("invalid kind error = %v", err)
	}
}

func TestCatalogFailsClosedWhenCandidateAuthorizationDependencyFails(t *testing.T) {
	principal := testGatePrincipal()
	binding := catalogTestBinding("project:1", KindProject, "project", "project-1")
	catalog := newCatalogForTest(t, principal, map[string]ScopeBinding{"project:1": binding}, catalogTestAuthorizer{
		readable: map[string]bool{}, manageable: map[string]bool{}, errFor: map[string]error{"project-1": errors.New("rebac unavailable")},
	}, catalogTestSource{kind: KindProject, candidates: []CatalogCandidate{{TenantID: "project:1", Kind: KindProject, Seek: "project-1", Label: "Secret"}}})
	if _, err := catalog.List(context.Background(), principal, CatalogQuery{Search: "does-not-match-secret-label", Limit: 10}); !errors.Is(err, ErrDependencyUnavailable) {
		t.Fatalf("List dependency error = %v", err)
	}
}

func TestCatalogMapsCandidateAuthorizationDeadlineToQueryBudget(t *testing.T) {
	principal := testGatePrincipal()
	binding := catalogTestBinding("project:1", KindProject, "project", "project-1")
	stateful, err := ontologycontract.NewStateCursorStore(ontologycontract.StateCursorOptions{MaxEntries: 100})
	if err != nil {
		t.Fatalf("NewStateCursorStore: %v", err)
	}
	budgets, err := ontologycontract.NewBudgetPolicy(map[ontologycontract.QueryClass]ontologycontract.QueryBudget{
		ontologycontract.QueryStandard: {Deadline: 10 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("NewBudgetPolicy: %v", err)
	}
	gate := NewScopeAccessGate(
		catalogTestResolver{bindings: map[string]ScopeBinding{"project:1": binding}},
		catalogTestAuthorizer{waitForContext: true},
		catalogTestSnapshots{},
	)
	catalog, err := NewCatalog(gate, CatalogCursorSet{Stateful: stateful}, budgets, catalogTestSource{
		kind:       KindProject,
		candidates: []CatalogCandidate{{TenantID: "project:1", Kind: KindProject, Seek: "project-1", Label: "Project"}},
	})
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}

	_, err = catalog.List(context.Background(), principal, CatalogQuery{Limit: 10})
	if !errors.Is(err, ontologycontract.ErrQueryBudgetExceeded) {
		t.Fatalf("error = %v, want query budget exceeded", err)
	}
}

func TestCatalogUsesStateCursorWithoutTrustedRevisions(t *testing.T) {
	principal := testGatePrincipal()
	bindings := map[string]ScopeBinding{
		"project:1": catalogTestBinding("project:1", KindProject, "project", "project-1"),
		"project:2": catalogTestBinding("project:2", KindProject, "project", "project-2"),
	}
	authorizer := catalogTestAuthorizer{
		readable: map[string]bool{"project-1": true, "project-2": true, "global": true}, manageable: map[string]bool{}, errFor: map[string]error{},
	}
	stateful, err := ontologycontract.NewStateCursorStore(ontologycontract.StateCursorOptions{MaxEntries: 100})
	if err != nil {
		t.Fatalf("NewStateCursorStore: %v", err)
	}
	gate := NewScopeAccessGate(catalogTestResolver{bindings: bindings}, authorizer, catalogTestUncacheableSnapshots{})
	catalog, err := NewCatalog(gate, CatalogCursorSet{Stateful: stateful}, nil, catalogTestSource{kind: KindProject, candidates: []CatalogCandidate{
		{TenantID: "project:1", Kind: KindProject, Seek: "project-1", Label: "One"},
		{TenantID: "project:2", Kind: KindProject, Seek: "project-2", Label: "Two"},
	}})
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	first, err := catalog.List(context.Background(), principal, CatalogQuery{Limit: 1})
	if err != nil {
		t.Fatalf("List first page: %v", err)
	}
	if !stringsHasPrefix(first.Page.NextCursor, "ocs1.") {
		t.Fatalf("state cursor = %q", first.Page.NextCursor)
	}
	second, err := catalog.List(context.Background(), principal, CatalogQuery{Limit: 1, Cursor: first.Page.NextCursor})
	if err != nil || len(second.Items) != 1 || second.Items[0].TenantID != "project:2" {
		t.Fatalf("List second page = %+v, err=%v", second, err)
	}
}

func newCatalogForTest(t *testing.T, principal identity.Principal, bindings map[string]ScopeBinding, authorizer AccessAuthorizer, sources ...CatalogSource) *Catalog {
	t.Helper()
	codec, err := ontologycontract.NewCursorCodec(ontologycontract.CursorCodecOptions{
		PrimaryID: "test",
		Keys:      []ontologycontract.CursorKey{{ID: "test", Key: bytes.Repeat([]byte{7}, 32)}},
	})
	if err != nil {
		t.Fatalf("NewCursorCodec: %v", err)
	}
	stateful, err := ontologycontract.NewStateCursorStore(ontologycontract.StateCursorOptions{MaxEntries: 100})
	if err != nil {
		t.Fatalf("NewStateCursorStore: %v", err)
	}
	gate := NewScopeAccessGate(catalogTestResolver{bindings: bindings}, authorizer, catalogTestSnapshots{})
	catalog, err := NewCatalog(gate, CatalogCursorSet{Encrypted: codec, Stateful: stateful}, nil, sources...)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	return catalog
}

func catalogTestBinding(tenantID string, kind Kind, objectType, objectID string) ScopeBinding {
	return ScopeBinding{
		TenantID: tenantID, Kind: kind,
		SourceObject:          identity.ObjectRef{Type: objectType, ID: objectID},
		AuthorizationTenantID: "auth-tenant",
		ReadAction:            objectType + ".view", ManageAction: objectType + ".manage",
		BindingVersion: 1, State: BindingStateActive,
	}
}

func stringsHasSuffix(value, suffix string) bool {
	return len(value) >= len(suffix) && value[len(value)-len(suffix):] == suffix
}

func stringsHasPrefix(value, prefix string) bool {
	return len(value) >= len(prefix) && value[:len(prefix)] == prefix
}
