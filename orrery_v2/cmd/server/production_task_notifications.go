package main

import (
	"context"
	"os"

	"orrery_v2/internal/app/notification"
	"orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/gateway/binding"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
)

type notificationBindings struct{ store *binding.Store }

func (b notificationBindings) Channels(ctx context.Context, tenant, human string) ([]string, error) {
	if b.store == nil {
		return []string{}, nil
	}
	all, err := b.store.ListByHuman(ctx, tenant, human)
	if err != nil {
		return nil, err
	}
	out := []string{}
	seen := map[string]bool{}
	for _, item := range all {
		if (item.ChannelKey == "weixin" || item.ChannelKey == "feishu") && !seen[item.ChannelKey] {
			out = append(out, item.ChannelKey)
			seen[item.ChannelKey] = true
		}
	}
	return out, nil
}
func startProductionTaskNotifications(ctx context.Context, rt *platform.Runtime, core *db.Store, tasks *scheduler.Repository, profiles notification.Profiles, gateway productionGatewayLifecycle, artifacts *scheduler.Service, disabled bool) (*notification.Service, error) {
	if tasks == nil {
		tasks = scheduler.NewRepository(rt.DBRouter)
	}
	var artifactReader notification.ArtifactReader
	if artifacts != nil {
		artifactReader = artifacts
	}
	service := notification.New(core, tasks, profiles, gateway.deliveries, artifactReader, notificationBindings{gateway.manager.BindingStore()}, rt.Config.DeviceDelivery.PublicOrigin)
	jobs, err := rt.Databases.Store(db.RoleJobs)
	if err != nil {
		return nil, err
	}
	// Only read committed reports. Organization publication cannot activate
	// recovery, enroll historical runs or dispatch a model/business session.
	service.SetReportReader(&productionScheduledReports{store: jobs})
	config, err := parsePersonalAssistantScheduledResultConfig(os.Getenv("ORRERY_PERSONAL_ASSISTANT_SCHEDULED_RESULT_RECIPIENTS"), os.Getenv("ORRERY_PERSONAL_ASSISTANT_SCHEDULED_RESULTS_SINCE"), os.Getenv("ORRERY_PERSONAL_ASSISTANT_SCHEDULED_RESULTS_REPLAY_RUN_IDS"))
	if err != nil {
		return nil, err
	}
	service.SetExternalReportRelay(func(event notification.Event, human, channel string) bool {
		if disabled || channel != "weixin" || (event.CreatedAt.Before(config.since) && !config.replay[event.RunID]) {
			return false
		}
		for _, recipient := range config.recipients {
			if recipient.TenantID == personalAssistantScheduledTenant(event.OriginTenant) && recipient.HumanProfileID == human {
				return true
			}
		}
		return false
	})
	service.SetClock(rt.Clock.Now)
	if err := service.EnsureSchema(ctx); err != nil {
		return nil, err
	}
	gateway.deliveries.AddDeliveryValidator(service.ValidateDelivery)
	if !disabled {
		if err := rt.RegisterSweeper("task.notifications", platform.SweeperFunc(service.Sweep)); err != nil {
			return nil, err
		}
	}
	return service, nil
}
