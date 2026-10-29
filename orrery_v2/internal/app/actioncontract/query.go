package actioncontract

import (
	"context"
	"fmt"
	"strings"
)

type ActionTypePage struct {
	Items   []ActionType
	HasMore bool
}

func (r *PlatformRepository) ListActionTypePage(ctx context.Context, tenantID, afterID string, limit int) (ActionTypePage, error) {
	if r == nil || r.store == nil {
		return ActionTypePage{}, fmt.Errorf("%w: store is required", ErrInvalidRequest)
	}
	tenantID = strings.TrimSpace(tenantID)
	afterID = strings.TrimSpace(afterID)
	if tenantID == "" || len(tenantID) > 256 || len(afterID) > 256 || limit <= 0 || limit > 1000 {
		return ActionTypePage{}, fmt.Errorf("%w: action type page query is invalid", ErrInvalidRequest)
	}
	rows, err := r.store.Query(ctx, actionTypeSelectSQL()+` WHERE id > ? AND (tenant_id = '' OR tenant_id = 'system' OR tenant_id = ?) ORDER BY id ASC LIMIT ?`, afterID, tenantID, limit+1)
	if err != nil {
		return ActionTypePage{}, fmt.Errorf("list action type page: %w", err)
	}
	defer rows.Close()
	items := make([]ActionType, 0, limit+1)
	for rows.Next() {
		item, err := scanActionType(rows)
		if err != nil {
			return ActionTypePage{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ActionTypePage{}, err
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	return ActionTypePage{Items: items, HasMore: hasMore}, nil
}
