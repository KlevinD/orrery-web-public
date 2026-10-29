package main

import (
	"context"

	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/notification"
	"orrery_v2/internal/app/operations"
	gwconfig "orrery_v2/internal/gateway/config"
	gwoutbound "orrery_v2/internal/gateway/outbound"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/logging"
)

type productionGatewayLifecycle struct {
	ctx               context.Context
	cancel            context.CancelFunc
	manager           *productionMessageGatewayManager
	deliveries        *gwoutbound.Service
	notifications     []*productionPersonalAssistantHeartbeat
	taskNotifications *notification.Service
}

func startProductionGatewayLifecycle(
	ctx context.Context,
	rt *platform.Runtime,
	discussions *discussionsession.Service,
	profiles productionGatewayAgentDirectory,
	settings *gwconfig.Store,
	startChannels bool,
	audit *operations.AuditStore,
	accounts productionGatewayAccountLookup,
	scopes productionGatewayPermissionScopeResolver,
	approvalDecisions productionPersonalAssistantApprovalDecisionService,
) (productionGatewayLifecycle, error) {
	lifecycleCtx, cancel := context.WithCancel(ctx)
	manager := newProductionMessageGatewayManager(rt, discussions, profiles, settings)
	manager.runner.SetAuditRecorder(audit)
	manager.runner.SetViewerAuthorization(accounts, scopes)
	stop := func() {
		cancel()
		manager.Stop(context.Background())
	}
	jobs, err := rt.Databases.Store(db.RoleJobs)
	if err != nil {
		stop()
		return productionGatewayLifecycle{}, err
	}
	deliveries := gwoutbound.NewService(jobs, manager.BindingStore(), manager)
	if err := deliveries.EnsureSchema(lifecycleCtx); err != nil {
		stop()
		return productionGatewayLifecycle{}, err
	}
	core, err := rt.Databases.Store(db.RoleCore)
	if err != nil {
		stop()
		return productionGatewayLifecycle{}, err
	}
	replyStore := newProductionPersonalAssistantApprovalReplyStore(core, jobs)
	if err := replyStore.EnsureSchema(lifecycleCtx); err != nil {
		stop()
		return productionGatewayLifecycle{}, err
	}
	manager.runner.SetPersonalAssistantApprovalReplyHandler(productionPersonalAssistantApprovalReplyService{
		claims: replyStore, decisions: approvalDecisions,
	})
	manager.runner.SetPersonalAssistantApprovalContextProvider(replyStore)
	if err := rt.RegisterOutboxHandler(gwoutbound.Topic, deliveries.HandleOutbox); err != nil {
		stop()
		return productionGatewayLifecycle{}, err
	}
	if startChannels {
		startProductionGatewayChannels(lifecycleCtx, manager)
	}
	return productionGatewayLifecycle{ctx: lifecycleCtx, cancel: cancel, manager: manager, deliveries: deliveries}, nil
}

type productionGatewayStarter interface {
	Start(context.Context)
}

func startProductionGatewayChannels(ctx context.Context, starter productionGatewayStarter) {
	if starter == nil {
		return
	}
	// External channel handshakes must not hold up the HTTP listener.
	go starter.Start(ctx)
}

func (l productionGatewayLifecycle) Stop() {
	if l.cancel != nil {
		l.cancel()
	}
	for _, worker := range l.notifications {
		worker.Stop()
	}
	if l.manager != nil {
		l.manager.Stop(context.Background())
	}
}

func productionHandlerCleanup(rt *platform.Runtime, gateway productionGatewayLifecycle, realtimeCleanup func(), runtimeRegistry interface{ Close() error }) func() {
	return func() {
		gateway.Stop()
		realtimeCleanup()
		if err := runtimeRegistry.Close(); err != nil {
			rt.Logger.Warn(context.Background(), "close discussion runtime registry", logging.Any("error", err))
		}
	}
}
