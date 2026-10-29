package aggregate

import (
	"context"
	"fmt"
	"slices"
	"testing"

	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

func TestCollectPagesRetainsInnerCursorAndAdvancesScopes(t *testing.T) {
	scopes := newTestScopeSet(t, "org-1", "system")
	pages := map[string]map[string]ontologycontract.CursorPage[string]{
		"org-1": {
			"":         mustTestPage(t, []string{"one"}, 2, true, "org-next"),
			"org-next": mustTestPage(t, []string{"two"}, 1, false, ""),
		},
		"system": {"": mustTestPage(t, []string{"global"}, 1, false, "")},
	}
	var calls []string
	read := func(_ context.Context, tenantID string, limit int, cursor string) (ontologycontract.CursorPage[string], error) {
		calls = append(calls, fmt.Sprintf("%s:%d:%s", tenantID, limit, cursor))
		return pages[tenantID][cursor], nil
	}
	stamp := func(tenantID, value string) string { return tenantID + "/" + value }

	items, next, hasMore, err := collectPages(context.Background(), scopes, PagePosition{}, 2, read, stamp)
	if err != nil {
		t.Fatalf("collect first page: %v", err)
	}
	if !slices.Equal(items, []string{"org-1/one", "org-1/two"}) || !hasMore || next != (PagePosition{ScopeIndex: 1}) {
		t.Fatalf("first page items=%v next=%+v hasMore=%v", items, next, hasMore)
	}
	if !slices.Equal(calls, []string{"org-1:2:", "org-1:1:org-next"}) {
		t.Fatalf("first page calls=%v", calls)
	}

	calls = nil
	items, next, hasMore, err = collectPages(context.Background(), scopes, next, 2, read, stamp)
	if err != nil {
		t.Fatalf("collect second page: %v", err)
	}
	if !slices.Equal(items, []string{"system/global"}) || hasMore || next != (PagePosition{}) {
		t.Fatalf("second page items=%v next=%+v hasMore=%v", items, next, hasMore)
	}
	if !slices.Equal(calls, []string{"system:2:"}) {
		t.Fatalf("second page calls=%v", calls)
	}
}

func TestCollectPagesRejectsMalformedDelegatePage(t *testing.T) {
	_, _, _, err := collectPages(context.Background(), newTestScopeSet(t, "org-1"), PagePosition{}, 2,
		func(context.Context, string, int, string) (ontologycontract.CursorPage[string], error) {
			return ontologycontract.CursorPage[string]{Items: []string{"one"}, Page: ontologycontract.Page{Limit: 2, HasMore: true}}, nil
		},
		func(_ string, value string) string { return value },
	)
	if err == nil {
		t.Fatal("expected malformed page error")
	}
}

func mustTestPage[T any](t *testing.T, items []T, limit int, hasMore bool, cursor string) ontologycontract.CursorPage[T] {
	t.Helper()
	page, err := ontologycontract.NewCursorPage(items, limit, hasMore, cursor)
	if err != nil {
		t.Fatalf("new cursor page: %v", err)
	}
	return page
}
