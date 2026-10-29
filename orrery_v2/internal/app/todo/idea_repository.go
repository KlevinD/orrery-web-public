package todo

import (
	"context"
	"errors"
	"strings"

	"orrery_v2/internal/platform/db"
)

func (r *Repository) SaveIdea(ctx context.Context, item IdeaItem) error {
	item = normalizeIdeaItem(item)
	current, err := r.GetIdeaForTenant(ctx, item.TenantID, item.IdeaID)
	if errors.Is(err, ErrNotFound) {
		return r.CreateIdea(ctx, item)
	}
	if err != nil {
		return err
	}
	item.Version = current.Version + 1
	return r.UpdateIdea(ctx, item, current.Version)
}

func (r *Repository) CreateIdea(ctx context.Context, item IdeaItem) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	item = normalizeIdeaItem(item)
	store, table, err := r.router.StoreForTable(ideaTable)
	if err != nil {
		return err
	}
	record, err := ideaRecord(item)
	if err != nil {
		return err
	}
	return store.WithTx(ctx, func(tx db.Tx) error {
		inserted, err := db.InsertRecordTx(ctx, tx, table, scopedRecordKey(item.TenantID, item.IdeaID), record)
		if err != nil {
			return err
		}
		if !inserted {
			return ErrConflict
		}
		return nil
	})
}

func (r *Repository) UpdateIdea(ctx context.Context, item IdeaItem, expectedVersion int64) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	item = normalizeIdeaItem(item)
	store, table, err := r.router.StoreForTable(ideaTable)
	if err != nil {
		return err
	}
	record, err := ideaRecord(item)
	if err != nil {
		return err
	}
	key := scopedRecordKey(item.TenantID, item.IdeaID)
	return store.WithTx(ctx, func(tx db.Tx) error {
		currentRecord, ok, err := db.GetRecordTx(ctx, tx, table, key)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		var current IdeaItem
		if err := decodePayload(currentRecord, &current); err != nil {
			return err
		}
		current = normalizeIdeaItem(current)
		if expectedVersion > 0 && current.Version != expectedVersion {
			return ErrConflict
		}
		return db.PutRecordTx(ctx, tx, table, key, record)
	})
}

func (r *Repository) GetIdea(ctx context.Context, ideaID string) (IdeaItem, error) {
	return r.GetIdeaForTenant(ctx, defaultTenantID, ideaID)
}

func (r *Repository) GetIdeaForTenant(ctx context.Context, tenantID, ideaID string) (IdeaItem, error) {
	if r == nil || r.router == nil {
		return IdeaItem{}, ErrServiceDependency
	}
	ideaID = strings.TrimSpace(ideaID)
	if ideaID == "" {
		return IdeaItem{}, ErrInvalidRequest
	}
	record, ok, err := r.router.GetRecord(ctx, ideaTable, scopedRecordKey(tenantID, ideaID))
	if err != nil {
		return IdeaItem{}, err
	}
	if !ok {
		return IdeaItem{}, ErrNotFound
	}
	var out IdeaItem
	if err := decodePayload(record, &out); err != nil {
		return IdeaItem{}, err
	}
	out.VoterIDs = recordStrings(record, "voter_ids")
	return normalizeIdeaItem(out), nil
}

func (r *Repository) DeleteIdea(ctx context.Context, ideaID string) error {
	return r.DeleteIdeaForTenant(ctx, defaultTenantID, ideaID, 0)
}

func (r *Repository) DeleteIdeaForTenant(ctx context.Context, tenantID, ideaID string, expectedVersion int64) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	store, table, err := r.router.StoreForTable(ideaTable)
	if err != nil {
		return err
	}
	commentStore, commentTable, err := r.router.StoreForTable(issueCommentTable)
	if err != nil {
		return err
	}
	if store != commentStore {
		return ErrServiceDependency
	}
	key := scopedRecordKey(tenantID, ideaID)
	return store.WithTx(ctx, func(tx db.Tx) error {
		record, ok, err := db.GetRecordTx(ctx, tx, table, key)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		var item IdeaItem
		if err := decodePayload(record, &item); err != nil {
			return err
		}
		item = normalizeIdeaItem(item)
		if expectedVersion > 0 && item.Version != expectedVersion {
			return ErrConflict
		}
		if err := deleteIssueCommentsTx(ctx, tx, commentTable, normalizedTenantID(tenantID), IssueKindIdea, ideaID); err != nil {
			return err
		}
		return deleteRecordTx(ctx, tx, table, key)
	})
}

func (r *Repository) ConvertIdea(ctx context.Context, idea IdeaItem, expectedVersion int64, task TodoItem) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	idea = normalizeIdeaItem(idea)
	task = normalizeTodoItem(task)
	ideaStore, ideaPhysical, err := r.router.StoreForTable(ideaTable)
	if err != nil {
		return err
	}
	taskStore, taskPhysical, err := r.router.StoreForTable(todoTable)
	if err != nil {
		return err
	}
	if ideaStore != taskStore {
		return ErrServiceDependency
	}
	ideaRecordValue, err := ideaRecord(idea)
	if err != nil {
		return err
	}
	taskRecordValue, err := todoRecord(task)
	if err != nil {
		return err
	}
	ideaKey := scopedRecordKey(idea.TenantID, idea.IdeaID)
	taskKey := scopedRecordKey(task.TenantID, task.TodoID)
	return ideaStore.WithTx(ctx, func(tx db.Tx) error {
		currentRecord, ok, err := db.GetRecordTx(ctx, tx, ideaPhysical, ideaKey)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		var current IdeaItem
		if err := decodePayload(currentRecord, &current); err != nil {
			return err
		}
		current = normalizeIdeaItem(current)
		if expectedVersion > 0 && current.Version != expectedVersion {
			return ErrConflict
		}
		inserted, err := db.InsertRecordTx(ctx, tx, taskPhysical, taskKey, taskRecordValue)
		if err != nil {
			return err
		}
		if !inserted {
			return ErrConflict
		}
		if err := enqueueTodoCreationTx(ctx, tx, task, "todo.idea_conversion"); err != nil {
			return err
		}
		return db.PutRecordTx(ctx, tx, ideaPhysical, ideaKey, ideaRecordValue)
	})
}

func ideaRecord(item IdeaItem) (db.Record, error) {
	persisted := item
	persisted.ViewerHasVoted = false
	payload, err := encodePayload(persisted)
	if err != nil {
		return nil, err
	}
	return db.Record{
		"tenant_id":         item.TenantID,
		"organization_id":   item.OrganizationID,
		"department_id":     item.DepartmentID,
		"idea_id":           item.IdeaID,
		"created_by":        item.CreatedBy,
		"status":            string(item.Status),
		"visibility":        string(item.Visibility),
		"impact":            string(item.Impact),
		"effort":            string(item.Effort),
		"owner_id":          item.OwnerID,
		"reviewer_id":       item.ReviewerID,
		"converted_todo_id": item.ConvertedTodoID,
		"search_text":       ideaSearchText(item),
		"updated_at":        formatTime(item.UpdatedAt),
		"version":           item.Version,
		"voter_ids":         append([]string(nil), item.VoterIDs...),
		payloadField:        payload,
	}, nil
}

func normalizeIdeaItem(item IdeaItem) IdeaItem {
	item.TenantID = normalizedTenantID(item.TenantID)
	item.IdeaID = strings.TrimSpace(item.IdeaID)
	item.Status = normalizeIdeaStatus(item.Status)
	if item.Status == "" {
		item.Status = IdeaStatusSubmitted
	}
	if item.Visibility == "" {
		item.Visibility = IdeaVisibilityOrganization
	}
	if item.Impact == "" {
		item.Impact = IdeaImpactMedium
	}
	if item.Effort == "" {
		item.Effort = IdeaEffortMedium
	}
	if item.Version <= 0 {
		item.Version = 1
	}
	if item.IssueNumber == "" {
		item.IssueNumber = issueNumberFromID("idea", item.IdeaID)
	}
	item.VoterIDs = normalizeTags(item.VoterIDs)
	item.VoteCount = len(item.VoterIDs)
	return item
}
