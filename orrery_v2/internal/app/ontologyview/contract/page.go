package contract

import (
	"fmt"
	"strings"
)

const DefaultListLimit = 50
const DefaultChildListLimit = 25
const MaxPageLimit = 100

type Page struct {
	Limit      int    `json:"limit"`
	HasMore    bool   `json:"has_more"`
	NextCursor string `json:"next_cursor,omitempty"`
}

type CursorPage[T any] struct {
	Items []T  `json:"items"`
	Page  Page `json:"page"`
}

func NormalizeLimit(requested, defaultLimit, hardLimit int) (int, error) {
	if defaultLimit <= 0 || hardLimit <= 0 || defaultLimit > hardLimit || hardLimit > MaxPageLimit {
		return 0, fmt.Errorf("%w: invalid server page limits", ErrInvalidRequest)
	}
	if requested == 0 {
		return defaultLimit, nil
	}
	if requested < 0 || requested > hardLimit {
		return 0, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidRequest, hardLimit)
	}
	return requested, nil
}

func NewCursorPage[T any](items []T, limit int, hasMore bool, nextCursor string) (CursorPage[T], error) {
	if limit <= 0 || limit > MaxPageLimit || len(items) > limit {
		return CursorPage[T]{}, fmt.Errorf("%w: cursor page exceeds its limit", ErrInvalidRequest)
	}
	nextCursor = strings.TrimSpace(nextCursor)
	if (hasMore && nextCursor == "") || (!hasMore && nextCursor != "") {
		return CursorPage[T]{}, fmt.Errorf("%w: next cursor must match has_more", ErrInvalidRequest)
	}
	itemsCopy := make([]T, len(items))
	copy(itemsCopy, items)
	return CursorPage[T]{Items: itemsCopy, Page: Page{Limit: limit, HasMore: hasMore, NextCursor: nextCursor}}, nil
}
