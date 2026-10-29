package aggregate

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type blockingScopeCatalog struct {
	calls    atomic.Int32
	resolves atomic.Int32
	entered  chan struct{}
	release  chan struct{}
	once     sync.Once
	page     ontologycontract.CursorPage[ontologyscope.ScopeCatalogItem]
}

func (f *blockingScopeCatalog) Resolve(_ context.Context, _ identity.Principal, tenantID string) (ontologyscope.ResolvedScope, error) {
	f.resolves.Add(1)
	return resolvedTestScope(tenantID), nil
}

func (f *blockingScopeCatalog) List(ctx context.Context, _ identity.Principal, _ ontologyscope.CatalogQuery) (ontologycontract.CursorPage[ontologyscope.ScopeCatalogItem], error) {
	f.calls.Add(1)
	f.once.Do(func() { close(f.entered) })
	select {
	case <-f.release:
		return f.page, nil
	case <-ctx.Done():
		return ontologycontract.CursorPage[ontologyscope.ScopeCatalogItem]{}, ctx.Err()
	}
}

type contextErrorScopeCatalog struct{}

func (contextErrorScopeCatalog) List(ctx context.Context, _ identity.Principal, _ ontologyscope.CatalogQuery) (ontologycontract.CursorPage[ontologyscope.ScopeCatalogItem], error) {
	return ontologycontract.CursorPage[ontologyscope.ScopeCatalogItem]{}, ctx.Err()
}

func (contextErrorScopeCatalog) Resolve(ctx context.Context, _ identity.Principal, _ string) (ontologyscope.ResolvedScope, error) {
	return ontologyscope.ResolvedScope{}, ctx.Err()
}

type fakeScopeCatalog struct {
	mu            sync.Mutex
	pages         map[string]ontologycontract.CursorPage[ontologyscope.ScopeCatalogItem]
	calls         []ontologyscope.CatalogQuery
	resolveCalls  []string
	resolveErrors map[string]error
}

func (f *fakeScopeCatalog) List(_ context.Context, _ identity.Principal, query ontologyscope.CatalogQuery) (ontologycontract.CursorPage[ontologyscope.ScopeCatalogItem], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, query)
	return f.pages[query.Cursor], nil
}

func (f *fakeScopeCatalog) Resolve(_ context.Context, _ identity.Principal, tenantID string) (ontologyscope.ResolvedScope, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolveCalls = append(f.resolveCalls, tenantID)
	if err := f.resolveErrors[tenantID]; err != nil {
		return ontologyscope.ResolvedScope{}, err
	}
	return resolvedTestScope(tenantID), nil
}

func (f *fakeScopeCatalog) ResolveCandidates(_ context.Context, _ identity.Principal, tenantIDs []string) ([]ontologyscope.ScopeCatalogItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := make([]ontologyscope.ScopeCatalogItem, 0, len(tenantIDs))
	for _, tenantID := range tenantIDs {
		f.resolveCalls = append(f.resolveCalls, tenantID)
		if err := f.resolveErrors[tenantID]; err != nil {
			if ontologyscope.IsNonDisclosingReadError(err) {
				continue
			}
			return nil, err
		}
		items = append(items, resolvedTestScope(tenantID).ScopeCatalogItem)
	}
	return items, nil
}

func (f *blockingScopeCatalog) ResolveCandidates(_ context.Context, _ identity.Principal, tenantIDs []string) ([]ontologyscope.ScopeCatalogItem, error) {
	f.resolves.Add(int32(len(tenantIDs)))
	items := make([]ontologyscope.ScopeCatalogItem, 0, len(tenantIDs))
	for _, tenantID := range tenantIDs {
		items = append(items, resolvedTestScope(tenantID).ScopeCatalogItem)
	}
	return items, nil
}

func (contextErrorScopeCatalog) ResolveCandidates(ctx context.Context, _ identity.Principal, _ []string) ([]ontologyscope.ScopeCatalogItem, error) {
	return nil, ctx.Err()
}

func TestScopeSetListsEveryAuthorizedScopeInCatalogOrder(t *testing.T) {
	catalog := &fakeScopeCatalog{pages: map[string]ontologycontract.CursorPage[ontologyscope.ScopeCatalogItem]{
		"":     scopePage([]string{"org-1", "project:org-1/p-1"}, true, "next"),
		"next": scopePage([]string{"system"}, false, ""),
	}}
	enumerator := newTestScopeEnumerator(t, catalog)

	set, err := enumerator.Discover(context.Background(), testPrincipal("user-1"))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if got, want := set.TenantIDs(), []string{"org-1", "project:org-1/p-1", "system"}; !slices.Equal(got, want) {
		t.Fatalf("tenant ids = %v, want %v", got, want)
	}
	if len(catalog.calls) != 2 || catalog.calls[0].Limit != MaxVisibleScopes || catalog.calls[1].Cursor != "next" {
		t.Fatalf("catalog calls = %+v", catalog.calls)
	}
}

func TestScopeSetAcceptsAnEmptyAuthorizedCatalog(t *testing.T) {
	enumerator := newTestScopeEnumerator(t, &fakeScopeCatalog{pages: map[string]ontologycontract.CursorPage[ontologyscope.ScopeCatalogItem]{
		"": scopePage(nil, false, ""),
	}})

	set, err := enumerator.Discover(context.Background(), testPrincipal("user-1"))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(set.TenantIDs()) != 0 || set.Hash() == "" {
		t.Fatalf("empty scope set = %+v hash=%q", set.TenantIDs(), set.Hash())
	}
}

func TestScopeEnumeratorSharesConcurrentDiscoveryForTheSameAuthorizationContext(t *testing.T) {
	catalog := &blockingScopeCatalog{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		page:    scopePage([]string{"org-1", "system"}, false, ""),
	}
	enumerator := newTestScopeEnumerator(t, catalog)
	principal := testPrincipal("user-1")
	principal.CoarseScopes = []string{"operations.read"}
	results := make(chan ScopeSet, 2)
	errors := make(chan error, 2)

	go func() {
		set, err := enumerator.Discover(context.Background(), principal)
		results <- set
		errors <- err
	}()
	<-catalog.entered
	go func() {
		set, err := enumerator.Discover(context.Background(), principal)
		results <- set
		errors <- err
	}()
	time.Sleep(20 * time.Millisecond)
	close(catalog.release)

	for range 2 {
		if err := <-errors; err != nil {
			t.Fatalf("Discover: %v", err)
		}
		if got := (<-results).TenantIDs(); !slices.Equal(got, []string{"org-1", "system"}) {
			t.Fatalf("tenant ids = %v", got)
		}
	}
	if got := catalog.calls.Load(); got != 1 {
		t.Fatalf("catalog calls = %d, want 1", got)
	}
}

func TestScopeEnumeratorDoesNotShareDiscoveryAcrossAuthorizationScopes(t *testing.T) {
	catalog := &blockingScopeCatalog{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		page:    scopePage([]string{"system"}, false, ""),
	}
	enumerator := newTestScopeEnumerator(t, catalog)
	readPrincipal := testPrincipal("user-1")
	readPrincipal.CoarseScopes = []string{"operations.read"}
	managePrincipal := testPrincipal("user-1")
	managePrincipal.CoarseScopes = []string{"operations.read", "operations.manage"}
	errors := make(chan error, 2)

	go func() {
		_, err := enumerator.Discover(context.Background(), readPrincipal)
		errors <- err
	}()
	<-catalog.entered
	go func() {
		_, err := enumerator.Discover(context.Background(), managePrincipal)
		errors <- err
	}()
	deadline := time.Now().Add(time.Second)
	for catalog.calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(catalog.release)

	for range 2 {
		if err := <-errors; err != nil {
			t.Fatalf("Discover: %v", err)
		}
	}
	if got := catalog.calls.Load(); got != 2 {
		t.Fatalf("catalog calls = %d, want 2", got)
	}
}

func TestScopeEnumeratorCompanyDiscoveryDoesNotWaitForCatalogScan(t *testing.T) {
	catalog := &blockingScopeCatalog{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		page:    scopePage([]string{"system"}, false, ""),
	}
	enumerator := newTestScopeEnumerator(t, catalog)
	principal := testPrincipal("user-1")
	errors := make(chan error, 1)

	go func() {
		_, err := enumerator.Discover(context.Background(), principal)
		errors <- err
	}()
	<-catalog.entered
	company, err := enumerator.DiscoverCompanyOverview(context.Background(), principal, []string{"system"})
	if err != nil {
		t.Fatalf("DiscoverCompanyOverview: %v", err)
	}
	if got := company.TenantIDs(); !slices.Equal(got, []string{"system"}) {
		t.Fatalf("company tenant ids = %v", got)
	}
	close(catalog.release)
	if err := <-errors; err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if got := catalog.calls.Load(); got != 1 || catalog.resolves.Load() != 1 {
		t.Fatalf("catalog list/resolve calls = %d/%d, want 1/1", got, catalog.resolves.Load())
	}
}

func TestScopeEnumeratorCompanyDiscoveryFiltersUnavailableGraphTenants(t *testing.T) {
	catalog := &fakeScopeCatalog{resolveErrors: map[string]error{
		"legacy-runtime": ontologyscope.ErrBindingNotFound,
	}}
	enumerator := newTestScopeEnumerator(t, catalog)

	set, err := enumerator.DiscoverCompanyOverview(context.Background(), testPrincipal("user-1"), []string{
		"system", "legacy-runtime", "org-1", "system",
	})
	if err != nil {
		t.Fatalf("DiscoverCompanyOverview: %v", err)
	}
	if got, want := set.TenantIDs(), []string{"org-1"}; !slices.Equal(got, want) {
		t.Fatalf("tenant ids = %v, want %v", got, want)
	}
	catalog.mu.Lock()
	resolved := append([]string(nil), catalog.resolveCalls...)
	catalog.mu.Unlock()
	slices.Sort(resolved)
	if !slices.Equal(resolved, []string{"legacy-runtime", "org-1", "system"}) {
		t.Fatalf("resolved tenants = %v", resolved)
	}
}

func TestScopeEnumeratorCompanyDiscoveryKeepsSystemOnlyFallback(t *testing.T) {
	enumerator := newTestScopeEnumerator(t, &fakeScopeCatalog{})

	set, err := enumerator.DiscoverCompanyOverview(context.Background(), testPrincipal("user-1"), []string{"system"})
	if err != nil {
		t.Fatalf("DiscoverCompanyOverview: %v", err)
	}
	if got, want := set.TenantIDs(), []string{"system"}; !slices.Equal(got, want) {
		t.Fatalf("tenant ids = %v, want %v", got, want)
	}
}

func resolvedTestScope(tenantID string) ontologyscope.ResolvedScope {
	kind := ontologyscope.KindOrganization
	if tenantID == "system" {
		kind = ontologyscope.KindSystem
	}
	return ontologyscope.ResolvedScope{ScopeCatalogItem: ontologyscope.ScopeCatalogItem{
		TenantID: tenantID, Kind: kind, Label: tenantID,
		Capabilities: []ontologyscope.ScopeCapability{ontologyscope.CapabilityRead},
	}, BindingVersion: 1}
}

func TestScopeEnumeratorMapsCatalogContextErrorsToQueryBudget(t *testing.T) {
	enumerator := newTestScopeEnumerator(t, contextErrorScopeCatalog{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := enumerator.Discover(ctx, testPrincipal("user-1"))
	if !errors.Is(err, ontologycontract.ErrQueryBudgetExceeded) {
		t.Fatalf("error = %v, want query budget exceeded", err)
	}
}

func TestScopeSetRejectsDuplicatesAndMoreThanTheBound(t *testing.T) {
	t.Run("duplicate", func(t *testing.T) {
		enumerator := newTestScopeEnumerator(t, &fakeScopeCatalog{pages: map[string]ontologycontract.CursorPage[ontologyscope.ScopeCatalogItem]{
			"": scopePage([]string{"org-1", "org-1"}, false, ""),
		}})
		if _, err := enumerator.Discover(context.Background(), testPrincipal("user-1")); !errors.Is(err, ontologyscope.ErrDependencyUnavailable) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("more than one hundred", func(t *testing.T) {
		ids := make([]string, MaxVisibleScopes)
		for index := range ids {
			ids[index] = "tenant-" + string(rune('A'+index%26)) + "-" + string(rune('a'+index/26))
		}
		enumerator := newTestScopeEnumerator(t, &fakeScopeCatalog{pages: map[string]ontologycontract.CursorPage[ontologyscope.ScopeCatalogItem]{
			"": scopePage(ids, true, "more"),
		}})
		if _, err := enumerator.Discover(context.Background(), testPrincipal("user-1")); !errors.Is(err, ontologycontract.ErrQueryBudgetExceeded) {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestScopeCursorIsBoundToPrincipalQueryAndVisibleScopeSet(t *testing.T) {
	enumerator := newTestScopeEnumerator(t, &fakeScopeCatalog{})
	principal := testPrincipal("user-1")
	scopes := newTestScopeSet(t, "org-1", "system")
	token, err := enumerator.IssuePageCursor(PagePosition{ScopeIndex: 1, Inner: "inner-cursor"}, "definitions", principal, scopes)
	if err != nil {
		t.Fatalf("IssuePageCursor: %v", err)
	}
	position, err := enumerator.ResolvePageCursor(token, "definitions", principal, scopes)
	if err != nil {
		t.Fatalf("ResolvePageCursor: %v", err)
	}
	if position.ScopeIndex != 1 || position.Inner != "inner-cursor" {
		t.Fatalf("position = %+v", position)
	}
	if _, err := enumerator.ResolvePageCursor(token, "definitions", testPrincipal("user-2"), scopes); !errors.Is(err, ontologycontract.ErrCursorStale) {
		t.Fatalf("cross-principal error = %v", err)
	}
	if _, err := enumerator.ResolvePageCursor(token, "definitions", principal, newTestScopeSet(t, "org-1", "project:org-1/p-1", "system")); !errors.Is(err, ontologycontract.ErrCursorStale) {
		t.Fatalf("changed-scope error = %v", err)
	}
	if _, err := enumerator.ResolvePageCursor(token, "objects", principal, scopes); !errors.Is(err, ontologycontract.ErrCursorStale) {
		t.Fatalf("changed-query error = %v", err)
	}
}

func newTestScopeEnumerator(t *testing.T, catalog ScopeCatalog) *ScopeEnumerator {
	t.Helper()
	cursors, err := ontologycontract.NewStateCursorStore(ontologycontract.StateCursorOptions{MaxEntries: 100})
	if err != nil {
		t.Fatalf("NewStateCursorStore: %v", err)
	}
	enumerator, err := NewScopeEnumerator(catalog, cursors)
	if err != nil {
		t.Fatalf("NewScopeEnumerator: %v", err)
	}
	return enumerator
}

func newTestScopeSet(t *testing.T, tenantIDs ...string) ScopeSet {
	t.Helper()
	items := make([]ontologyscope.ScopeCatalogItem, 0, len(tenantIDs))
	for _, tenantID := range tenantIDs {
		kind := ontologyscope.KindOrganization
		if tenantID == "system" {
			kind = ontologyscope.KindSystem
		}
		items = append(items, ontologyscope.ScopeCatalogItem{TenantID: tenantID, Kind: kind, Label: tenantID, Capabilities: []ontologyscope.ScopeCapability{ontologyscope.CapabilityRead}})
	}
	set, err := newScopeSet(items)
	if err != nil {
		t.Fatalf("newScopeSet: %v", err)
	}
	return set
}

func scopePage(tenantIDs []string, hasMore bool, next string) ontologycontract.CursorPage[ontologyscope.ScopeCatalogItem] {
	items := make([]ontologyscope.ScopeCatalogItem, 0, len(tenantIDs))
	for _, tenantID := range tenantIDs {
		kind := ontologyscope.KindOrganization
		if tenantID == "system" {
			kind = ontologyscope.KindSystem
		}
		items = append(items, ontologyscope.ScopeCatalogItem{TenantID: tenantID, Kind: kind, Label: tenantID, Capabilities: []ontologyscope.ScopeCapability{ontologyscope.CapabilityRead}})
	}
	return ontologycontract.CursorPage[ontologyscope.ScopeCatalogItem]{Items: items, Page: ontologycontract.Page{Limit: MaxVisibleScopes, HasMore: hasMore, NextCursor: next}}
}

func testPrincipal(subject string) identity.Principal {
	return identity.Principal{SubjectProvider: identity.ProviderLocal, Subject: subject, UserID: subject, Username: subject, ViewerKind: identity.ViewerKindUser}
}
