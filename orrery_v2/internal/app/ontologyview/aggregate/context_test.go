package aggregate

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	contextview "orrery_v2/internal/app/ontologyview/context"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

type fakeContextReader struct {
	listCalls       []contextview.ObjectListQuery
	getCalls        []string
	companyTenants  []string
	overviewByScope map[string]ontologycontract.Summary[contextview.CompanyOverviewData]
}

type flakyCompanyOverviewReader struct {
	*fakeContextReader
	failures int
	calls    int
}

func (f *flakyCompanyOverviewReader) Overview(ctx context.Context, principal identity.Principal, tenantID string) (ontologycontract.Summary[contextview.CompanyOverviewData], error) {
	f.calls++
	if f.failures > 0 {
		f.failures--
		return ontologycontract.Summary[contextview.CompanyOverviewData]{}, ontologyscope.ErrDependencyUnavailable
	}
	return f.fakeContextReader.Overview(ctx, principal, tenantID)
}

func (f *fakeContextReader) CompanyScopeTenantIDs(context.Context, int) (contextgraph.ContextTenantPage, error) {
	return contextgraph.ContextTenantPage{TenantIDs: append([]string(nil), f.companyTenants...)}, nil
}

func (f *fakeContextReader) Summary(context.Context, identity.Principal, string) (ontologycontract.Summary[contextview.SummaryData], error) {
	return ontologycontract.Summary[contextview.SummaryData]{}, nil
}

func (f *fakeContextReader) Overview(_ context.Context, _ identity.Principal, tenantID string) (ontologycontract.Summary[contextview.CompanyOverviewData], error) {
	return f.overviewByScope[tenantID], nil
}

func (f *fakeContextReader) ListObjects(_ context.Context, _ identity.Principal, query contextview.ObjectListQuery) (ontologycontract.CursorPage[contextview.ObjectItem], error) {
	f.listCalls = append(f.listCalls, query)
	switch {
	case query.TenantID == "org-1" && query.Cursor == "":
		return fakePage([]contextview.ObjectItem{{ID: "same"}, {ID: "org-2"}}, query.Limit, true, "org-next"), nil
	case query.TenantID == "org-1" && query.Cursor == "org-next":
		return fakePage([]contextview.ObjectItem{}, query.Limit, false, ""), nil
	case query.TenantID == "system":
		return fakePage([]contextview.ObjectItem{{ID: "same"}}, query.Limit, false, ""), nil
	default:
		return ontologycontract.CursorPage[contextview.ObjectItem]{}, errors.New("unexpected object query")
	}
}

func (f *fakeContextReader) GetObject(_ context.Context, _ identity.Principal, tenantID, objectID string) (contextview.ObjectDetail, error) {
	f.getCalls = append(f.getCalls, tenantID+"/"+objectID)
	return contextview.ObjectDetail{ObjectItem: contextview.ObjectItem{ID: objectID}}, nil
}

func (*fakeContextReader) ListFacts(context.Context, identity.Principal, contextview.ChildQuery) (ontologycontract.CursorPage[contextview.FactItem], error) {
	return ontologycontract.CursorPage[contextview.FactItem]{}, nil
}

func (*fakeContextReader) ListRelations(context.Context, identity.Principal, contextview.RelationListQuery) (ontologycontract.CursorPage[contextview.RelationItem], error) {
	return ontologycontract.CursorPage[contextview.RelationItem]{}, nil
}

func (*fakeContextReader) ListEvidence(context.Context, identity.Principal, contextview.ChildQuery) (ontologycontract.CursorPage[contextview.EvidenceItem], error) {
	return ontologycontract.CursorPage[contextview.EvidenceItem]{}, nil
}

func (*fakeContextReader) Neighborhood(context.Context, identity.Principal, contextview.NeighborhoodQuery) (contextview.Neighborhood, error) {
	return contextview.Neighborhood{}, nil
}

func TestContextServiceAggregatesObjectsWithTenantProvenanceAndCursor(t *testing.T) {
	catalog := &fakeScopeCatalog{pages: map[string]ontologycontract.CursorPage[ontologyscope.ScopeCatalogItem]{"": scopePage([]string{"org-1", "system"}, false, "")}}
	reader := &fakeContextReader{}
	service, err := NewContextService(newTestScopeEnumerator(t, catalog), reader)
	if err != nil {
		t.Fatalf("NewContextService: %v", err)
	}
	principal := testPrincipal("user-1")

	first, err := service.ListObjects(context.Background(), principal, contextview.ObjectListQuery{Limit: 2})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first.Items) != 2 || first.Items[0].TenantID != "org-1" || first.Items[1].TenantID != "org-1" || !first.Page.HasMore || first.Page.NextCursor == "" {
		t.Fatalf("first page = %+v", first)
	}

	second, err := service.ListObjects(context.Background(), principal, contextview.ObjectListQuery{Limit: 2, Cursor: first.Page.NextCursor})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].ID != "same" || second.Items[0].TenantID != "system" || second.Page.HasMore {
		t.Fatalf("second page = %+v", second)
	}
	gotCalls := make([]string, len(reader.listCalls))
	for index, call := range reader.listCalls {
		gotCalls[index] = call.TenantID + "/" + call.Cursor
	}
	if !slices.Equal(gotCalls, []string{"org-1/", "org-1/org-next", "system/"}) {
		t.Fatalf("list calls = %v", gotCalls)
	}
}

func TestContextServiceRequiresConcreteTenantForDetails(t *testing.T) {
	reader := &fakeContextReader{}
	service, err := NewContextService(newTestScopeEnumerator(t, &fakeScopeCatalog{}), reader)
	if err != nil {
		t.Fatalf("NewContextService: %v", err)
	}
	principal := testPrincipal("user-1")
	if _, err := service.GetObject(context.Background(), principal, "", "object-1"); !errors.Is(err, ontologycontract.ErrInvalidRequest) {
		t.Fatalf("empty tenant error = %v", err)
	}
	if _, err := service.GetObject(context.Background(), principal, "org-1", "object-1"); err != nil {
		t.Fatalf("concrete detail: %v", err)
	}
	if !slices.Equal(reader.getCalls, []string{"org-1/object-1"}) {
		t.Fatalf("get calls = %v", reader.getCalls)
	}
}

func TestContextServiceOverviewRequiresAndForwardsConcreteTenant(t *testing.T) {
	observed := time.Date(2026, 8, 14, 1, 0, 0, 0, time.UTC)
	reader := &fakeContextReader{overviewByScope: map[string]ontologycontract.Summary[contextview.CompanyOverviewData]{
		"org-1": companyOverviewScopeSummary("org-1", observed, 3, nil),
	}}
	service, err := NewContextService(newTestScopeEnumerator(t, &fakeScopeCatalog{}), reader)
	if err != nil {
		t.Fatalf("NewContextService: %v", err)
	}
	if _, err := service.Overview(context.Background(), testPrincipal("user-1"), ""); !errors.Is(err, ontologycontract.ErrInvalidRequest) {
		t.Fatalf("empty tenant error = %v", err)
	}
	overview, err := service.Overview(context.Background(), testPrincipal("user-1"), "org-1")
	if err != nil || overview.Data.Objects.Value == nil || *overview.Data.Objects.Value != 3 {
		t.Fatalf("concrete overview = %+v, err=%v", overview, err)
	}
}

func TestContextServiceBuildsCompanyOverviewWithoutCompatibilitySystemScope(t *testing.T) {
	first := time.Date(2026, 8, 8, 1, 0, 0, 0, time.UTC)
	second := first.Add(time.Hour)
	reader := &fakeContextReader{companyTenants: []string{"org-1", "system"}, overviewByScope: map[string]ontologycontract.Summary[contextview.CompanyOverviewData]{
		"org-1": companyOverviewScopeSummary("org-1", first, 3, []contextview.ObjectItem{
			{ID: "org-new", Label: "Org new", UpdatedAt: timePointer(first.Add(5 * time.Minute))},
			{ID: "org-old", Label: "Org old", UpdatedAt: timePointer(first)},
		}),
		"system": companyOverviewScopeSummary("system", second, 2, []contextview.ObjectItem{
			{ID: "system-new", Label: "System new", UpdatedAt: timePointer(second.Add(5 * time.Minute))},
			{ID: "system-two", Label: "System two", UpdatedAt: timePointer(second)},
			{ID: "system-three", Label: "System three", UpdatedAt: timePointer(first.Add(30 * time.Minute))},
			{ID: "system-old", Label: "System old", UpdatedAt: timePointer(first.Add(-time.Minute))},
		}),
	}}
	catalog := &fakeScopeCatalog{pages: map[string]ontologycontract.CursorPage[ontologyscope.ScopeCatalogItem]{"": scopePage([]string{"org-1", "system"}, false, "")}}
	service, err := NewContextService(newTestScopeEnumerator(t, catalog), reader)
	if err != nil {
		t.Fatalf("NewContextService: %v", err)
	}

	overview, err := service.CompanyOverview(context.Background(), testPrincipal("user-1"))
	if err != nil {
		t.Fatalf("CompanyOverview: %v", err)
	}
	catalog.mu.Lock()
	listCalls := append([]ontologyscope.CatalogQuery(nil), catalog.calls...)
	resolveCalls := append([]string(nil), catalog.resolveCalls...)
	catalog.mu.Unlock()
	slices.Sort(resolveCalls)
	if len(listCalls) != 0 || !slices.Equal(resolveCalls, []string{"org-1", "system"}) {
		t.Fatalf("company overview scope queries: lists=%+v resolves=%+v", listCalls, resolveCalls)
	}
	if overview.Data.Objects.Value == nil || *overview.Data.Objects.Value != 3 || overview.Data.Facts.Value == nil || *overview.Data.Facts.Value != 4 || overview.Data.Relations.Value == nil || *overview.Data.Relations.Value != 5 || overview.Data.Evidence.Value == nil || *overview.Data.Evidence.Value != 6 {
		t.Fatalf("company metrics = %+v", overview.Data)
	}
	if overview.Data.DomainCount.Value == nil || *overview.Data.DomainCount.Value != 2 {
		t.Fatalf("company domains = %+v", overview.Data.Domains)
	}
	if len(overview.Data.RecentObjects) != 2 {
		t.Fatalf("company recent objects = %+v", overview.Data.RecentObjects)
	}
	want := []string{"org-1/org-new", "org-1/org-old"}
	for index, item := range overview.Data.RecentObjects {
		if item.TenantID+"/"+item.ID != want[index] {
			t.Fatalf("company recent object %d = %+v", index, item)
		}
	}
}

func TestContextServiceRetriesCompanyOverviewOnceAfterRevisionChange(t *testing.T) {
	observed := time.Date(2026, 8, 13, 1, 0, 0, 0, time.UTC)
	reader := &flakyCompanyOverviewReader{fakeContextReader: &fakeContextReader{
		companyTenants: []string{"system"},
		overviewByScope: map[string]ontologycontract.Summary[contextview.CompanyOverviewData]{
			"system": companyOverviewScopeSummary("system", observed, 3, nil),
		},
	}, failures: 1}
	catalog := &fakeScopeCatalog{}
	service, err := NewContextService(newTestScopeEnumerator(t, catalog), reader)
	if err != nil {
		t.Fatalf("NewContextService: %v", err)
	}

	overview, err := service.CompanyOverview(context.Background(), testPrincipal("user-1"))
	if err != nil {
		t.Fatalf("CompanyOverview: %v", err)
	}
	if reader.calls != 2 || overview.Data.Objects.Value == nil || *overview.Data.Objects.Value != 3 {
		t.Fatalf("overview calls=%d data=%+v", reader.calls, overview.Data)
	}
}

func TestContextServiceDoesNotRetryCompanyOverviewMoreThanOnce(t *testing.T) {
	reader := &flakyCompanyOverviewReader{fakeContextReader: &fakeContextReader{companyTenants: []string{"system"}}, failures: 2}
	service, err := NewContextService(newTestScopeEnumerator(t, &fakeScopeCatalog{}), reader)
	if err != nil {
		t.Fatalf("NewContextService: %v", err)
	}

	if _, err := service.CompanyOverview(context.Background(), testPrincipal("user-1")); !errors.Is(err, ontologyscope.ErrDependencyUnavailable) {
		t.Fatalf("CompanyOverview error = %v", err)
	}
	if reader.calls != 2 {
		t.Fatalf("overview calls = %d, want 2", reader.calls)
	}
}

func companyOverviewScopeSummary(tenantID string, observed time.Time, objects int64, recent []contextview.ObjectItem) ontologycontract.Summary[contextview.CompanyOverviewData] {
	for index := range recent {
		recent[index].TenantID = ""
	}
	return ontologycontract.Summary[contextview.CompanyOverviewData]{
		Data: contextview.CompanyOverviewData{
			Objects: ontologycontract.ExactCount(objects), Facts: ontologycontract.ExactCount(objects + 1),
			Relations: ontologycontract.ExactCount(objects + 2), Evidence: ontologycontract.ExactCount(objects + 3),
			DomainCount: ontologycontract.ExactCount(2),
			Domains: []contextview.OverviewDomain{
				{ID: "organization", Objects: ontologycontract.ExactCount(objects - 1)},
				{ID: "automation", Objects: ontologycontract.ExactCount(1)},
			},
			RecentObjects: recent, NewestUpdatedAt: timePointer(observed),
		},
		Meta: ontologycontract.SummaryMeta{AsOf: observed, DataRevision: tenantID, AuthorizationRevision: stringPointer("auth-" + tenantID)},
	}
}

func timePointer(value time.Time) *time.Time { return &value }
func stringPointer(value string) *string     { return &value }

func fakePage[T any](items []T, limit int, hasMore bool, next string) ontologycontract.CursorPage[T] {
	return ontologycontract.CursorPage[T]{Items: items, Page: ontologycontract.Page{Limit: limit, HasMore: hasMore, NextCursor: next}}
}
