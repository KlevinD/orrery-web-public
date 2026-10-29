package main

import (
	"context"
	"fmt"
	"time"

	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/hil"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/responsibility"
	todoapp "orrery_v2/internal/app/todo"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/logging"
	platformrealtime "orrery_v2/internal/platform/realtime"
)

const productionMarketingApprovalReviewWindow = 24 * time.Hour

type productionRealtimeMissionRepository interface {
	SetRealtimeStore(*platformrealtime.Store)
}

type productionHILAutoplanWiring struct {
	realtimePublisher          productionDiscussionRealtimePublisher
	runtimeApprovalCoordinator *discussionsession.RuntimeApprovalCoordinator
	hilService                 *hil.Service
	hilObservers               *productionHILResolutionObservers
	workflowService            *hil.WorkflowService
	workflowRepository         *hil.PlatformRepository
	autoplanAdapter            *productionResponsibilityAutoplanAdapter
	autoplanMaterializer       *productionResponsibilityAutoplanMissionMaterializer
	runtimeApprovalReconcile   func(error)
}

func newProductionHILAutoplanWiring(
	ctx context.Context,
	core *db.Store,
	rt *platform.Runtime,
	missionRepo productionRealtimeMissionRepository,
	discussionRepository *discussionsession.Repository,
	realtimeStore *platformrealtime.Store,
	settingsStore productionSettingsStore,
	profiles productionHILProfileDirectory,
	installWorkflowDefaults bool,
	responsibilityRepository *responsibility.Repository,
	missionCommand *missioncontrol.CommandService,
	contextProvider responsibility.ContextProvider,
) (productionHILAutoplanWiring, error) {
	if missionRepo != nil {
		missionRepo.SetRealtimeStore(realtimeStore)
	}
	realtimePublisher := productionDiscussionRealtimePublisher{store: realtimeStore}
	runtimeApprovalCoordinator := discussionsession.NewRuntimeApprovalCoordinator(discussionsession.RuntimeApprovalCoordinatorOptions{
		Repository:          discussionRepository,
		RealtimePublisher:   realtimePublisher,
		Clock:               rt.Clock,
		ApprovalHoldTimeout: productionHILRuntimeApprovalHoldTimeout(ctx, settingsStore),
		HumanReviewEnabled:  settingsStore.HumanReviewEnabled,
		Decisions:           newProductionRuntimeApprovalDecisionService(settingsStore, platformSecretResolver{provider: rt.Secrets}),
	})
	reportRuntimeMissionResume := func(resumeErr error) {
		if resumeErr != nil {
			rt.Logger.Warn(context.Background(), "detached runtime HIL mission continuation failed", logging.Any("error", resumeErr))
			return
		}
		rt.Logger.Info(context.Background(), "detached runtime HIL mission continuation queued")
	}
	runtimeApprovalCoordinator.SetHILDetachedApprovalResolver(productionRuntimeHILMissionResolver{missions: missionCommand, report: reportRuntimeMissionResume})
	asyncRuntimeObserver := newProductionAsyncHILResolutionObserver(runtimeApprovalCoordinator, func(recovered any) {
		rt.Logger.Warn(
			context.Background(),
			"runtime HIL resolution observer panicked",
			logging.Any("panic", recovered),
		)
	})
	hilObservers := newProductionHILResolutionObservers(asyncRuntimeObserver)
	hilService, workflowService, workflowRepository, err := newProductionHILService(ctx, core, rt, profiles, installWorkflowDefaults, hilObservers)
	if err != nil {
		return productionHILAutoplanWiring{}, fmt.Errorf("configure production HIL workflows: %w", err)
	}
	runtimeApprovalCoordinator.SetHILDecisionService(hilService)
	autoplanPlanner, err := newProductionResponsibilityAutoplanPlanner(settingsStore, platformSecretResolver{provider: rt.Secrets})
	if err != nil {
		return productionHILAutoplanWiring{}, fmt.Errorf("configure responsibility autonomous planner: %w", err)
	}
	autoplanMaterializer := newProductionResponsibilityAutoplanMissionMaterializer(missionCommand, rt.Clock.Now)
	var goalContext responsibility.AutoplanGoalContextProvider
	if contextProvider != nil {
		var ok bool
		goalContext, ok = contextProvider.(responsibility.AutoplanGoalContextProvider)
		if !ok {
			return productionHILAutoplanWiring{}, fmt.Errorf("configure responsibility autonomous planning: goal context provider is unavailable")
		}
	}
	autoplanService, err := responsibility.NewAutoplanService(responsibility.AutoplanServiceOptions{
		Repository:         responsibilityRepository,
		Planner:            autoplanPlanner,
		GoalContext:        goalContext,
		Materializer:       autoplanMaterializer,
		ExecutionGrants:    responsibilityRepository,
		HIL:                hilService,
		Now:                rt.Clock.Now,
		HumanReviewEnabled: settingsStore.HumanReviewEnabled,
	})
	if err != nil {
		return productionHILAutoplanWiring{}, fmt.Errorf("configure responsibility autonomous planning: %w", err)
	}
	autoplanAdapter := newProductionResponsibilityAutoplanAdapter(autoplanService)
	hilObservers.Add(newProductionResponsibilityAutoplanHILObserver(autoplanAdapter, func(observerErr error) {
		rt.Logger.Warn(context.Background(), "responsibility autonomous-plan HIL resolution failed", logging.Any("error", observerErr))
	}))
	return productionHILAutoplanWiring{
		realtimePublisher:          realtimePublisher,
		runtimeApprovalCoordinator: runtimeApprovalCoordinator,
		hilService:                 hilService,
		hilObservers:               hilObservers,
		workflowService:            workflowService,
		workflowRepository:         workflowRepository,
		autoplanAdapter:            autoplanAdapter,
		autoplanMaterializer:       autoplanMaterializer,
		runtimeApprovalReconcile: func(reconcileErr error) {
			if reconcileErr != nil {
				rt.Logger.Warn(context.Background(), "runtime HIL reconciliation failed", logging.Any("error", reconcileErr))
			}
		},
	}, nil
}

func (w productionHILAutoplanWiring) AttachAutoplanMissionCommands(commands responsibilityAutoplanMissionCommandPort) {
	if w.autoplanMaterializer != nil && commands != nil {
		w.autoplanMaterializer.missions = commands
	}
}

func productionHILRuntimeApprovalHoldTimeout(ctx context.Context, settings productionSettingsStore) time.Duration {
	timeout := runtimeApprovalHoldTimeoutFromSettings(ctx, settings)
	if timeout < productionMarketingApprovalReviewWindow {
		return productionMarketingApprovalReviewWindow
	}
	return timeout
}

func (w productionHILAutoplanWiring) AttachDiscussionService(service *discussionsession.Service) {
	if w.runtimeApprovalCoordinator != nil && service != nil {
		w.runtimeApprovalCoordinator.SetHILRuntimeApprovalResolver(service)
		go func() {
			err := w.runtimeApprovalCoordinator.ReconcileHILRuntimeApprovals(context.Background())
			if w.runtimeApprovalReconcile != nil {
				w.runtimeApprovalReconcile(err)
			}
		}()
	}
}

func (w productionHILAutoplanWiring) AttachTodoService(service *todoapp.Service) {
	if service == nil {
		return
	}
	service.SetHILDecisionService(w.hilService)
	if w.hilObservers != nil {
		w.hilObservers.Add(service)
	}
}

func (w productionHILAutoplanWiring) AttachSchedulerPort(port *productionSchedulerResponsibilityPort) {
	if port != nil {
		port.autoplan = w.autoplanAdapter
	}
}
