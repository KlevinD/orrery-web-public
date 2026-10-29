package main

import (
	"context"
	"encoding/base64"
	"strings"
	"time"

	todoapp "orrery_v2/internal/app/todo"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/graphprotocol"
)

func (s *productionTodoOntologyService) publishTodoCreationEvent(ctx context.Context, event todoapp.CreationEvent) error {
	return s.publishTodoCreated(ctx, todoapp.TodoItem{
		TenantID: event.TenantID, TodoID: event.TodoID, CreatedBy: event.CreatedBy,
		CreatedByKind: event.CreatedByKind, CreatedBySubjectID: event.CreatedBySubjectID,
		CreatedAt: event.CreatedAt,
	}, event.Source)
}

func newProductionTodoOntologyRuntime(rt *platform.Runtime, inner *todoapp.Service, api *productionAPI, disabled bool) (*productionTodoOntologyService, error) {
	service := newProductionTodoOntologyService(inner, api)
	if !disabled {
		if err := registerProductionTodoCreationSweeper(rt, service); err != nil {
			return nil, err
		}
	}
	return service, nil
}

func registerProductionTodoCreationSweeper(rt *platform.Runtime, service *productionTodoOntologyService) error {
	limit := rt.Config.Sweeper.BatchSize
	if limit < 1 || limit > 100 {
		limit = 100
	}
	return rt.RegisterSweeper("todo.creation_events", platform.SweeperFunc(func(ctx context.Context) error {
		return service.inner.ReconcileCreationEvents(ctx, limit, service.publishTodoCreationEvent)
	}))
}

// Creation contributes immutable origin facts to the existing Todo object.
// It must not replay the task's initial title, status or assignee over later work.
func (s *productionTodoOntologyService) publishTodoCreated(ctx context.Context, item todoapp.TodoItem, source string) error {
	if err := s.requireOntologyPublisher(); err != nil {
		return err
	}
	tenantID := strings.TrimSpace(item.TenantID)
	if tenantID == "" || strings.TrimSpace(item.TodoID) == "" || strings.TrimSpace(item.CreatedBy) == "" || strings.TrimSpace(string(item.CreatedByKind)) == "" || item.CreatedAt.IsZero() || strings.TrimSpace(source) == "" {
		return todoapp.ErrInvalidRequest
	}
	return s.api.applyOntologyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "todo-created:" + base64.RawURLEncoding.EncodeToString([]byte(tenantID)) + ":" + item.TodoID,
		TenantID: tenantID,
		Type:     "todo.created",
		StreamID: item.TodoID,
		Version:  1,
		Payload: map[string]any{
			"id":              item.TodoID,
			"created_at":      item.CreatedAt.UTC().Format(time.RFC3339Nano),
			"created_by":      item.CreatedBy,
			"creation_source": source,
		},
		PublishedBy:     graphprotocol.ActorRef{Kind: string(item.CreatedByKind), ID: item.CreatedBy},
		OccurredAt:      item.CreatedAt,
		SourceAccessRef: trustedOntologySourceRef("todo", item.TodoID, tenantID),
	})
}
