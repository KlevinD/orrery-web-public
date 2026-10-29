package main

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/app/knowledgeprovider"
)

func TestProductionKnowledgeProviderSyncSweeperSyncsEnabledBindingsAndReportsFailures(t *testing.T) {
	service := &recordingKnowledgeProviderSyncService{
		bindings: []knowledgeprovider.Binding{
			{ID: "active", Status: knowledgeprovider.StatusActive},
			{ID: "retry", Status: knowledgeprovider.StatusError},
			{ID: "disabled", Status: knowledgeprovider.StatusDisabled},
		},
		errors: map[string]error{"retry": errors.New("provider unavailable")},
	}
	err := (&productionKnowledgeProviderSyncSweeper{service: service, ownerID: defaultBusinessOrganizationID}).Sweep(context.Background())
	if err == nil || len(service.requests) != 2 {
		t.Fatalf("sweep calls=%+v err=%v", service.requests, err)
	}
	if _, called := service.requests["disabled"]; called {
		t.Fatal("disabled provider binding was synced")
	}
	for id, request := range service.requests {
		if request.OwnerID != defaultBusinessOrganizationID || request.PrincipalID != "system:knowledge-provider-sync" {
			t.Fatalf("sync request %s = %+v", id, request)
		}
	}
}

type recordingKnowledgeProviderSyncService struct {
	bindings []knowledgeprovider.Binding
	errors   map[string]error
	requests map[string]knowledgeprovider.SyncRequest
}

func (s *recordingKnowledgeProviderSyncService) ListBindings(context.Context) ([]knowledgeprovider.Binding, error) {
	return append([]knowledgeprovider.Binding(nil), s.bindings...), nil
}

func (s *recordingKnowledgeProviderSyncService) Sync(_ context.Context, id string, request knowledgeprovider.SyncRequest) (knowledgeprovider.SyncResult, error) {
	if s.requests == nil {
		s.requests = map[string]knowledgeprovider.SyncRequest{}
	}
	s.requests[id] = request
	return knowledgeprovider.SyncResult{}, s.errors[id]
}
