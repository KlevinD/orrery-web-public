package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/app/knowledgeprovider"
	knowledgestore "orrery_v2/internal/knowledge/store"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
)

type knowledgeProviderSyncService interface {
	ListBindings(context.Context) ([]knowledgeprovider.Binding, error)
	Sync(context.Context, string, knowledgeprovider.SyncRequest) (knowledgeprovider.SyncResult, error)
}

type productionKnowledgeProviderSyncSweeper struct {
	service knowledgeProviderSyncService
	ownerID string
}

func newProductionKnowledgeProviderServices(
	rt *platform.Runtime,
	core *db.Store,
	registry knowledgeProviderToolRegistry,
	runtime knowledgeProviderMCPRuntime,
	repo knowledgestore.Repository,
	disableBackgroundWorkers bool,
) (*knowledgeprovider.ExternalProvider, *knowledgeprovider.Service, error) {
	bindings := knowledgeprovider.NewStore(core)
	port := productionKnowledgeProviderMCPPort{registry: registry, runtime: runtime}
	adapter := knowledgeprovider.NewMCPAdapter(port)
	external := knowledgeprovider.NewExternalProvider(bindings, adapter)
	service := knowledgeprovider.NewService(bindings, adapter, repo, rt.Clock.Now)
	if disableBackgroundWorkers {
		return external, service, nil
	}
	err := rt.RegisterSweeper("knowledge.provider_sync", newProductionCadencedSweeper(
		&productionKnowledgeProviderSyncSweeper{service: service, ownerID: defaultBusinessOrganizationID},
		rt.Clock.Now, time.Hour,
	))
	return external, service, err
}

func (s *productionKnowledgeProviderSyncSweeper) Sweep(ctx context.Context) error {
	if s == nil || s.service == nil || strings.TrimSpace(s.ownerID) == "" {
		return fmt.Errorf("knowledge provider sync sweeper is not configured")
	}
	bindings, err := s.service.ListBindings(ctx)
	if err != nil {
		return err
	}
	var sweepErr error
	for _, binding := range bindings {
		if binding.Status == knowledgeprovider.StatusDisabled {
			continue
		}
		if _, err := s.service.Sync(ctx, binding.ID, knowledgeprovider.SyncRequest{
			PrincipalID: "system:knowledge-provider-sync",
			OwnerID:     strings.TrimSpace(s.ownerID),
		}); err != nil {
			sweepErr = errors.Join(sweepErr, fmt.Errorf("sync knowledge provider %s: %w", binding.ID, err))
		}
	}
	return sweepErr
}
