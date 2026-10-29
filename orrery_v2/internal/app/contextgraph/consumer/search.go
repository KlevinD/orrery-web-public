package consumer

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"orrery_v2/internal/app/contextgraph"
)

const (
	defaultObjectSearchLimit = 20
	maxObjectSearchLimit     = 50
	maxObjectSearchKinds     = 8
	maxObjectSearchQueryLen  = 512
	maxObjectSearchQueryRune = 128
	maxObjectSearchCursorLen = 4096
)

type SearchObjectsInput struct {
	TenantID              string
	ActorRef              contextgraph.Ref
	Query                 string
	IncludeKinds          []string
	Limit                 int
	Cursor                string
	PermissionScopes      []string
	PrincipalKey          string
	AuthorizationRevision string
	PolicyRevision        string
}

type SearchObjectItem struct {
	Ref   contextgraph.Ref `json:"ref"`
	Label string           `json:"label"`
}

type SearchObjectsResult struct {
	Items      []SearchObjectItem `json:"items"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

type ObjectSearchStore interface {
	ListVisibleObjects(context.Context, contextgraph.ObjectQuery) (contextgraph.ObjectPage, error)
}

type SearchCursorCodec interface {
	DecodeSearchCursor(SearchObjectsInput) (contextgraph.ObjectSeek, error)
	EncodeSearchCursor(SearchObjectsInput, contextgraph.ObjectSeek) (string, error)
}

type SearchService struct {
	store      ObjectSearchStore
	visibility contextgraph.ObjectVisibilityProvider
	cursors    SearchCursorCodec
}

func NewSearchService(store ObjectSearchStore, visibility contextgraph.ObjectVisibilityProvider, cursors SearchCursorCodec) (*SearchService, error) {
	if store == nil || visibility == nil || cursors == nil {
		return nil, fmt.Errorf("%w: object search dependencies are required", contextgraph.ErrQueryInvalid)
	}
	return &SearchService{store: store, visibility: visibility, cursors: cursors}, nil
}

func (s *SearchService) SearchObjects(ctx context.Context, input SearchObjectsInput) (SearchObjectsResult, error) {
	input, err := NormalizeSearchObjectsInput(input)
	if err != nil {
		return SearchObjectsResult{}, err
	}
	var after contextgraph.ObjectSeek
	if input.Cursor != "" {
		after, err = s.cursors.DecodeSearchCursor(input)
		if err != nil {
			return SearchObjectsResult{}, err
		}
	}
	kinds := input.IncludeKinds
	if len(kinds) == 0 {
		kinds = []string{""}
	}
	byID := make(map[string]contextgraph.Object)
	hasMore := false
	for _, kind := range kinds {
		page, err := s.store.ListVisibleObjects(ctx, contextgraph.ObjectQuery{
			TenantID: input.TenantID, Visibility: contextgraph.CandidateVisibilityConstraint(),
			ObjectType: kind, Search: input.Query, Limit: input.Limit + 1, After: after,
		})
		if err != nil {
			return SearchObjectsResult{}, err
		}
		hasMore = hasMore || page.HasMore
		for _, object := range page.Items {
			byID[object.ID] = object
		}
	}
	candidates := make([]contextgraph.Object, 0, len(byID))
	for _, object := range byID {
		candidates = append(candidates, object)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if !candidates[i].UpdatedAt.Equal(candidates[j].UpdatedAt) {
			return candidates[i].UpdatedAt.After(candidates[j].UpdatedAt)
		}
		return candidates[i].ID < candidates[j].ID
	})
	visible, err := s.visibility.VisibleObjectIDs(ctx, contextgraph.BuildContextBundleRequest{
		TenantID: input.TenantID, Consumer: "agent-runtime", Purpose: "object-search",
		ActorRef: input.ActorRef, PermissionScopes: append([]string(nil), input.PermissionScopes...),
	}, candidates)
	if err != nil {
		return SearchObjectsResult{}, err
	}
	items := make([]SearchObjectItem, 0, input.Limit)
	var lastConsumed contextgraph.Object
	for _, object := range candidates {
		if !visible[object.ID] {
			lastConsumed = object
			continue
		}
		if len(items) == input.Limit {
			hasMore = true
			break
		}
		items = append(items, SearchObjectItem{Ref: contextgraph.Ref{Kind: object.ObjectType, ID: object.ID}, Label: object.Label})
		lastConsumed = object
	}
	result := SearchObjectsResult{Items: items}
	if hasMore && strings.TrimSpace(lastConsumed.ID) != "" {
		result.NextCursor, err = s.cursors.EncodeSearchCursor(input, contextgraph.ObjectSeek{UpdatedAt: lastConsumed.UpdatedAt, ID: lastConsumed.ID})
		if err != nil {
			return SearchObjectsResult{}, err
		}
	}
	return result, nil
}

func NormalizeSearchObjectsInput(input SearchObjectsInput) (SearchObjectsInput, error) {
	input.TenantID = strings.TrimSpace(input.TenantID)
	input.ActorRef.Kind = strings.ToLower(strings.TrimSpace(input.ActorRef.Kind))
	input.ActorRef.ID = strings.TrimSpace(input.ActorRef.ID)
	input.Query = strings.TrimSpace(input.Query)
	input.Cursor = strings.TrimSpace(input.Cursor)
	if input.Limit == 0 {
		input.Limit = defaultObjectSearchLimit
	}
	if input.TenantID == "" || input.ActorRef.Kind == "" || input.ActorRef.ID == "" || input.Query == "" || len(input.Query) > maxObjectSearchQueryLen || !utf8.ValidString(input.Query) || utf8.RuneCountInString(input.Query) > maxObjectSearchQueryRune || input.Limit < 1 || input.Limit > maxObjectSearchLimit || len(input.Cursor) > maxObjectSearchCursorLen || len(input.IncludeKinds) > maxObjectSearchKinds {
		return SearchObjectsInput{}, fmt.Errorf("%w: bounded object search input is invalid", contextgraph.ErrQueryInvalid)
	}
	seen := make(map[string]bool, len(input.IncludeKinds))
	kinds := make([]string, 0, len(input.IncludeKinds))
	for _, kind := range input.IncludeKinds {
		kind = strings.ToLower(strings.TrimSpace(kind))
		if kind == "" || seen[kind] {
			continue
		}
		if len(kind) > MaxGraphKindLength {
			return SearchObjectsInput{}, fmt.Errorf("%w: object search kind exceeds %d bytes", contextgraph.ErrQueryInvalid, MaxGraphKindLength)
		}
		seen[kind] = true
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	input.IncludeKinds = kinds
	return input, nil
}
