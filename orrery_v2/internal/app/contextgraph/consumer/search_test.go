package consumer

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
)

func TestSearchObjectsValidatesBoundsAndAppliesVisibility(t *testing.T) {
	store := &recordingObjectSearchStore{page: contextgraph.ObjectPage{Items: []contextgraph.Object{
		{ID: "mission-1", TenantID: "tenant-1", ObjectType: "mission", Label: "Visible", UpdatedAt: time.Unix(2, 0).UTC()},
		{ID: "mission-secret", TenantID: "tenant-1", ObjectType: "mission", Label: "Secret", UpdatedAt: time.Unix(1, 0).UTC()},
	}, HasMore: true}}
	visibility := &recordingSearchVisibility{visible: map[string]bool{"mission-1": true}}
	cursors := &recordingSearchCursor{next: "opaque-next"}
	service, err := NewSearchService(store, visibility, cursors)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.SearchObjects(context.Background(), SearchObjectsInput{
		TenantID: "tenant-1", ActorRef: contextgraph.Ref{Kind: "agent", ID: "agent-1"},
		Query: "mission", IncludeKinds: []string{"mission"}, Limit: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.calls != 1 || store.query.TenantID != "tenant-1" || store.query.Search != "mission" || store.query.ObjectType != "mission" || store.query.Limit != 21 {
		t.Fatalf("store calls=%d query=%+v", store.calls, store.query)
	}
	if visibility.calls != 1 || len(result.Items) != 1 || result.Items[0].Ref.ID != "mission-1" || result.NextCursor != "opaque-next" {
		t.Fatalf("visibility=%d result=%+v", visibility.calls, result)
	}
}

func TestSearchObjectsRejectsInvalidInputBeforeStore(t *testing.T) {
	for _, input := range []SearchObjectsInput{
		{TenantID: "tenant-1", ActorRef: contextgraph.Ref{Kind: "agent", ID: "agent-1"}, Limit: 20},
		{TenantID: "tenant-1", ActorRef: contextgraph.Ref{Kind: "agent", ID: "agent-1"}, Query: "mission", Limit: 51},
		{TenantID: "", ActorRef: contextgraph.Ref{Kind: "agent", ID: "agent-1"}, Query: "mission", Limit: 20},
		{TenantID: "tenant-1", ActorRef: contextgraph.Ref{Kind: "agent", ID: "agent-1"}, Query: strings.Repeat("q", 129), Limit: 20},
		{TenantID: "tenant-1", ActorRef: contextgraph.Ref{Kind: "agent", ID: "agent-1"}, Query: "mission", IncludeKinds: []string{strings.Repeat("k", 257)}, Limit: 20},
	} {
		store := &recordingObjectSearchStore{}
		service, err := NewSearchService(store, &recordingSearchVisibility{}, &recordingSearchCursor{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.SearchObjects(context.Background(), input); !errors.Is(err, contextgraph.ErrQueryInvalid) {
			t.Fatalf("SearchObjects error = %v", err)
		}
		if store.calls != 0 {
			t.Fatalf("store calls = %d", store.calls)
		}
	}
}

func TestSearchObjectsReturnsCursorWhenCurrentCandidatesAreAllConcealed(t *testing.T) {
	lastCandidate := contextgraph.Object{
		ID: "mission-secret-2", TenantID: "tenant-1", ObjectType: "mission",
		UpdatedAt: time.Unix(1, 0).UTC(),
	}
	store := &recordingObjectSearchStore{page: contextgraph.ObjectPage{
		Items: []contextgraph.Object{
			{ID: "mission-secret-1", TenantID: "tenant-1", ObjectType: "mission", UpdatedAt: time.Unix(2, 0).UTC()},
			lastCandidate,
		},
		HasMore: true,
	}}
	cursors := &recordingSearchCursor{next: "opaque-next"}
	service, err := NewSearchService(store, &recordingSearchVisibility{visible: map[string]bool{}}, cursors)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.SearchObjects(context.Background(), SearchObjectsInput{
		TenantID: "tenant-1", ActorRef: contextgraph.Ref{Kind: "agent", ID: "agent-1"},
		Query: "mission", IncludeKinds: []string{"mission"}, Limit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 0 || result.NextCursor != "opaque-next" {
		t.Fatalf("result = %+v", result)
	}
	if cursors.encodedAfter != (contextgraph.ObjectSeek{UpdatedAt: lastCandidate.UpdatedAt, ID: lastCandidate.ID}) {
		t.Fatalf("encoded cursor seek = %+v", cursors.encodedAfter)
	}
}

type recordingSearchVisibility struct {
	calls   int
	objects []contextgraph.Object
	visible map[string]bool
}

func (v *recordingSearchVisibility) VisibleObjectIDs(_ context.Context, _ contextgraph.BuildContextBundleRequest, objects []contextgraph.Object) (map[string]bool, error) {
	v.calls++
	v.objects = append([]contextgraph.Object(nil), objects...)
	return v.visible, nil
}

type recordingObjectSearchStore struct {
	calls int
	query contextgraph.ObjectQuery
	page  contextgraph.ObjectPage
}

func (s *recordingObjectSearchStore) ListVisibleObjects(_ context.Context, query contextgraph.ObjectQuery) (contextgraph.ObjectPage, error) {
	s.calls++
	s.query = query
	return s.page, nil
}

type recordingSearchCursor struct {
	after        contextgraph.ObjectSeek
	encodedAfter contextgraph.ObjectSeek
	next         string
}

func (c *recordingSearchCursor) DecodeSearchCursor(SearchObjectsInput) (contextgraph.ObjectSeek, error) {
	return c.after, nil
}

func (c *recordingSearchCursor) EncodeSearchCursor(_ SearchObjectsInput, after contextgraph.ObjectSeek) (string, error) {
	c.encodedAfter = after
	return c.next, nil
}
