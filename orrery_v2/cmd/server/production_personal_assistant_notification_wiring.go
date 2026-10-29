package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	agentcore "orrery_v2/internal/agent/core"

	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/operations"
	schedulerapp "orrery_v2/internal/app/scheduler"
	todoapp "orrery_v2/internal/app/todo"
	gwconfig "orrery_v2/internal/gateway/config"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
)

type productionPersonalAssistantNotificationWiring struct {
	runtime        *platform.Runtime
	core           *db.Store
	profiles       productionHILProfileDirectory
	accounts       productionGatewayAccountLookup
	scopes         productionGatewayPermissionScopeResolver
	todos          *todoapp.Service
	discussions    productionPersonalAssistantNoticeSink
	runner         discussionsession.DiscussionAgentRunner
	hil            *hil.Service
	workflow       *hil.PlatformRepository
	settings       productionSettingsStore
	attention      productionPersonalAssistantAttentionSweeper
	delegations    *productionPersonalAssistantDelegationEngine
	scheduled      *productionPersonalAssistantScheduledResultSweeper
	replies        productionAgentNotificationReplyWriter
	reportRegistry agentcore.SessionRuntimeRegistry
	reportRecorder agentcore.TraceRecorder
	failures       productionNotificationFailureCapture
	systemUpdates  *productionPersonalAssistantSystemUpdates
	systemAccess   personalAssistantSystemAccess
}

func (w *productionPersonalAssistantNotificationWiring) setFailureCapture(diagnostics *productionClientDiagnostics) {
	if diagnostics != nil {
		w.failures = diagnostics.inbox
	}
}

func (w *productionPersonalAssistantNotificationWiring) StartGateway(
	ctx context.Context, discussions *discussionsession.Service, profiles productionGatewayAgentDirectory,
	settings *gwconfig.Store, audit *operations.AuditStore, approvals productionHILAutoplanWiring, backgroundDisabled bool,
) (productionGatewayLifecycle, error) {
	if err := w.Prepare(ctx, backgroundDisabled); err != nil {
		return productionGatewayLifecycle{}, err
	}
	if !backgroundDisabled {
		if err := w.runtime.RegisterSweeper("todo.source_notices", platform.SweeperFunc(w.todos.ReconcileSourceNotices)); err != nil {
			return productionGatewayLifecycle{}, err
		}
	}
	gateway, err := startProductionGatewayLifecycle(ctx, w.runtime, discussions, profiles, settings, !backgroundDisabled, audit, w.accounts, w.scopes, w.hil)
	if err != nil {
		return productionGatewayLifecycle{}, err
	}
	if w.failures != nil {
		gateway.deliveries.SetFailureObserver(productionGatewayNotificationFailureObserver(w.failures))
	}
	if err := registerProductionHILWorkflowSweeper(w.runtime, w.core, approvals, w.profiles, gateway.deliveries, w.failures); err != nil {
		gateway.Stop()
		return productionGatewayLifecycle{}, err
	}
	if !backgroundDisabled {
		w.Start(&gateway)
	}
	return gateway, nil
}

func (w *productionPersonalAssistantNotificationWiring) Prepare(ctx context.Context, backgroundDisabled bool) error {
	recipients, err := productionPersonalAssistantAttentionRecipients(os.Getenv("ORRERY_PERSONAL_ASSISTANT_ATTENTION_RECIPIENTS"))
	if err != nil {
		return err
	}
	grants, err := parsePersonalAssistantDelegationGrants(os.Getenv("ORRERY_PERSONAL_ASSISTANT_APPROVAL_DELEGATIONS"))
	if err != nil {
		return err
	}
	rt := w.runtime
	w.attention = productionPersonalAssistantAttentionSweeper{
		recipients: recipients, profiles: w.profiles, accounts: w.accounts, scopes: w.scopes,
		targets: productionPersonalAssistantApprovalNotificationStore{store: w.core}, todos: w.todos,
		decisions: w.workflow, publicOrigin: rt.Config.DeviceDelivery.PublicOrigin, now: rt.Clock.Now,
	}
	scheduledConfig, err := parsePersonalAssistantScheduledResultConfig(
		os.Getenv("ORRERY_PERSONAL_ASSISTANT_SCHEDULED_RESULT_RECIPIENTS"),
		os.Getenv("ORRERY_PERSONAL_ASSISTANT_SCHEDULED_RESULTS_SINCE"),
		os.Getenv("ORRERY_PERSONAL_ASSISTANT_SCHEDULED_RESULTS_REPLAY_RUN_IDS"),
	)
	if err != nil {
		return err
	}
	jobs, err := rt.Databases.Store(db.RoleJobs)
	if err != nil {
		return err
	}
	w.replies = &productionAgentNotificationReplies{store: jobs, runner: w.runner}
	w.attention.replies = w.replies
	systemRoutes, err := parsePersonalAssistantSystemRoutes(os.Getenv("ORRERY_PERSONAL_ASSISTANT_SYSTEM_UPDATE_RECIPIENTS"))
	if err != nil {
		return err
	}
	if len(systemRoutes) > 0 {
		if w.systemAccess == nil {
			return errors.New("personal assistant system update authorizer required")
		}
		source := productionPersonalAssistantSystemSource{core: w.core}
		if repairs, ok := w.failures.(personalAssistantAutomaticRepairReader); ok {
			source.repairs = repairs
		}
		w.systemUpdates = &productionPersonalAssistantSystemUpdates{routes: systemRoutes, jobs: jobs, source: source, attention: w.attention, access: w.systemAccess, replies: w.replies, now: rt.Clock.Now}
		if !backgroundDisabled {
			for _, route := range systemRoutes {
				if _, err := w.systemUpdates.fence(ctx, route); err != nil {
					return err
				}
			}
		}
	}
	scheduledAttention := w.attention
	scheduledAttention.targets = personalAssistantScheduledResultTargetStore{core: w.core}
	w.scheduled = &productionPersonalAssistantScheduledResultSweeper{
		config: scheduledConfig, repository: schedulerapp.NewRepository(rt.DBRouter),
		resolve: scheduledAttention.recipient, jobs: jobs, now: rt.Clock.Now,
		replies: newProductionScheduledResultReplies(jobs),
	}
	if !backgroundDisabled && len(scheduledConfig.recipients) > 0 {
		if traces, ok := w.reportRecorder.(agentcore.DurableTraceRecorder); ok && w.reportRegistry != nil {
			reports := &productionScheduledReports{store: jobs, owner: w.scheduled,
				recovery: &productionScheduledReportRecovery{jobs: rt.ExecutionQueue, files: rt.FileStore, registry: w.reportRegistry, traces: traces, profiles: w.profiles, workspaceRoot: filepath.Join(rt.Config.Runtime.ExecEnvRoot, "report-only")}}
			if err := reports.activate(ctx); err != nil {
				return err
			}
			w.scheduled.replies.reports = reports
		}
	}
	if len(grants) > 0 && !backgroundDisabled {
		authority := &productionPersonalAssistantDelegationAuthority{grants: grants, accounts: w.accounts, scopes: w.scopes, targets: w.attention.targets, profiles: w.profiles, now: rt.Clock.Now}
		w.delegations = &productionPersonalAssistantDelegationEngine{
			core: w.core, authority: authority, decisions: w.hil, settings: w.settings,
			completion: newProductionPersonalAssistantApprovalCompletion(w.settings, platformSecretResolver{provider: rt.Secrets}),
			notices:    w.discussions, now: rt.Clock.Now,
			replies: w.replies,
		}
		if err := w.delegations.ensureSchema(ctx); err != nil {
			return err
		}
		w.hil.SetDelegationAuthority(authority)
	}
	if runner, ok := w.runner.(*productionPersonalAssistantDiscussionAgentRunner); ok {
		jobs, err := rt.Databases.Store(db.RoleJobs)
		if err != nil {
			return err
		}
		runner.setNotificationContext(&productionPersonalAssistantNotificationContext{
			jobs: jobs, attention: w.attention, approvals: &productionPersonalAssistantApprovalReplyStore{core: w.core, now: rt.Clock.Now}, delegations: w.delegations, scheduled: w.scheduled, systemUpdates: w.systemUpdates,
		})
	}
	return nil
}

func (w *productionPersonalAssistantNotificationWiring) Start(gateway *productionGatewayLifecycle) {
	rt := w.runtime
	recovery := &productionPersonalAssistantNotificationRecovery{
		recipients: w.attention.recipients, accounts: w.accounts, scopes: w.scopes, notices: w.discussions, deliveries: gateway.deliveries,
		preserveContent: true,
		failures:        w.failures,
	}
	approvals := productionPersonalAssistantApprovalNotificationSweeper{
		delegation: w.delegations, store: productionPersonalAssistantApprovalNotificationStore{store: w.core}, profiles: w.profiles,
		replies:    w.replies,
		deliveries: gateway.deliveries, logger: rt.Logger, recovery: recovery, publicOrigin: rt.Config.DeviceDelivery.PublicOrigin, batchSize: rt.Config.Sweeper.BatchSize,
	}
	w.attention.deliveries, w.attention.recovery = gateway.deliveries, recovery
	w.scheduled.deliveries = gateway.deliveries
	w.scheduled.recovery = &productionPersonalAssistantNotificationRecovery{
		recipients: w.scheduled.config.recipients, accounts: w.accounts, scopes: w.scopes, notices: w.discussions, deliveries: gateway.deliveries,
		preserveContent: true,
		failures:        w.failures,
	}
	gateway.deliveries.SetDeliveryValidator(productionPersonalAssistantNotificationGuard{
		attention: w.attention, approvals: &productionPersonalAssistantApprovalReplyStore{core: w.core, now: rt.Clock.Now}, scheduled: w.scheduled,
		replies: w.replies, systemUpdates: w.systemUpdates,
	}.Validate)
	approvalHeartbeat, attentionHeartbeat := newProductionPersonalAssistantHeartbeat(), newProductionPersonalAssistantHeartbeat()
	w.hil.SetNotificationWake(func() { approvalHeartbeat.Wake(); attentionHeartbeat.Wake() })
	w.todos.SetNotificationWake(attentionHeartbeat.Wake)
	gateway.notifications = append(gateway.notifications, approvalHeartbeat, attentionHeartbeat)
	approvalHeartbeat.Start(gateway.ctx, 30*time.Second, 25*time.Second, approvals.Sweep, rt.Logger)
	attentionHeartbeat.Start(gateway.ctx, 30*time.Second, 25*time.Second, w.attention.Sweep, rt.Logger)
	scheduledHeartbeat := newProductionPersonalAssistantHeartbeat()
	gateway.notifications = append(gateway.notifications, scheduledHeartbeat)
	scheduledHeartbeat.Start(gateway.ctx, 30*time.Second, 25*time.Second, w.scheduled.Sweep, rt.Logger)
	if w.systemUpdates != nil {
		recipients := make([]productionPersonalAssistantAttentionRecipient, 0, len(w.systemUpdates.routes))
		for _, route := range w.systemUpdates.routes {
			recipients = append(recipients, productionPersonalAssistantAttentionRecipient{TenantID: route.TenantID, HumanProfileID: route.HumanProfileID})
		}
		w.systemUpdates.deliveries = gateway.deliveries
		w.systemUpdates.recovery = &productionPersonalAssistantNotificationRecovery{recipients: recipients, accounts: w.accounts, scopes: w.scopes, notices: w.discussions, deliveries: gateway.deliveries, preserveContent: true, failures: w.failures}
		heartbeat := newProductionPersonalAssistantHeartbeat()
		gateway.notifications = append(gateway.notifications, heartbeat)
		heartbeat.Start(gateway.ctx, 30*time.Second, 25*time.Second, w.systemUpdates.Sweep, rt.Logger)
	}
	if reports := w.scheduled.replies.reports; reports != nil {
		reportHeartbeat := newProductionPersonalAssistantHeartbeat()
		gateway.notifications = append(gateway.notifications, reportHeartbeat)
		reportHeartbeat.Start(gateway.ctx, 30*time.Second, 25*time.Second, reports.Sweep, rt.Logger)
	}
}
