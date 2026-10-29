package aggregate

import (
	"context"
	"strings"

	"orrery_v2/internal/app/identity"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

func aggregateQueryKey(kind string, query any) (string, error) {
	hash, err := ontologycontract.CanonicalQueryHash(query)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(kind) + ":" + hash, nil
}

func aggregateCursorPage[T any](
	ctx context.Context,
	enumerator *ScopeEnumerator,
	principal identity.Principal,
	scopes ScopeSet,
	queryKey string,
	cursor string,
	limit int,
	read func(context.Context, string, int, string) (ontologycontract.CursorPage[T], error),
	stamp func(string, T) T,
) (ontologycontract.CursorPage[T], error) {
	position, err := enumerator.ResolvePageCursor(cursor, queryKey, principal, scopes)
	if err != nil {
		return ontologycontract.CursorPage[T]{}, err
	}
	items, nextPosition, hasMore, err := collectPages(ctx, scopes, position, limit, read, stamp)
	if err != nil {
		return ontologycontract.CursorPage[T]{}, err
	}
	nextCursor := ""
	if hasMore {
		nextCursor, err = enumerator.IssuePageCursor(nextPosition, queryKey, principal, scopes)
		if err != nil {
			return ontologycontract.CursorPage[T]{}, err
		}
	}
	return ontologycontract.NewCursorPage(items, limit, hasMore, nextCursor)
}

func requireConcreteTenant(tenantID string) (string, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" || len(tenantID) > 256 {
		return "", ontologycontract.ErrInvalidRequest
	}
	return tenantID, nil
}
