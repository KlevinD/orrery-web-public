package aggregate

import (
	"context"
	"fmt"
	"strings"

	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

func collectPages[T any](
	ctx context.Context,
	scopes ScopeSet,
	position PagePosition,
	limit int,
	read func(context.Context, string, int, string) (ontologycontract.CursorPage[T], error),
	stamp func(string, T) T,
) ([]T, PagePosition, bool, error) {
	if limit < 1 || limit > ontologycontract.MaxPageLimit || position.ScopeIndex < 0 || position.ScopeIndex > len(scopes.items) || read == nil || stamp == nil {
		return nil, PagePosition{}, false, ontologycontract.ErrInvalidRequest
	}
	if position.ScopeIndex == len(scopes.items) {
		if position.Inner != "" {
			return nil, PagePosition{}, false, ontologycontract.ErrInvalidCursor
		}
		return []T{}, PagePosition{}, false, nil
	}

	items := make([]T, 0, limit)
	for scopeIndex := position.ScopeIndex; scopeIndex < len(scopes.items); scopeIndex++ {
		inner := ""
		if scopeIndex == position.ScopeIndex {
			inner = position.Inner
		}
		for len(items) < limit {
			remaining := limit - len(items)
			page, err := read(ctx, scopes.items[scopeIndex].TenantID, remaining, inner)
			if err != nil {
				return nil, PagePosition{}, false, err
			}
			if err := validateDelegatePage(page, remaining, inner); err != nil {
				return nil, PagePosition{}, false, err
			}
			for _, item := range page.Items {
				items = append(items, stamp(scopes.items[scopeIndex].TenantID, item))
			}
			if page.Page.HasMore {
				inner = page.Page.NextCursor
				if len(items) == limit {
					return items, PagePosition{ScopeIndex: scopeIndex, Inner: inner}, true, nil
				}
				continue
			}
			break
		}
		if len(items) == limit {
			if scopeIndex+1 < len(scopes.items) {
				return items, PagePosition{ScopeIndex: scopeIndex + 1}, true, nil
			}
			return items, PagePosition{}, false, nil
		}
	}
	return items, PagePosition{}, false, nil
}

func validateDelegatePage[T any](page ontologycontract.CursorPage[T], requested int, previousCursor string) error {
	next := strings.TrimSpace(page.Page.NextCursor)
	if len(page.Items) > requested || page.Page.Limit < len(page.Items) || page.Page.Limit < 1 || page.Page.Limit > ontologycontract.MaxPageLimit ||
		page.Page.HasMore && (len(page.Items) == 0 || next == "" || next == previousCursor) || !page.Page.HasMore && next != "" {
		return fmt.Errorf("%w: malformed delegated ontology page", ontologyscope.ErrDependencyUnavailable)
	}
	return nil
}
