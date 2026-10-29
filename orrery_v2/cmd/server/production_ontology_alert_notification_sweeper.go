package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/logging"
)

const productionOntologyAlertNotificationSweeperLimit = 100

type productionOntologyAlertNotificationSweeperConfig struct {
	Limit          int
	RepeatWindow   time.Duration
	RecoveryWindow time.Duration
	Log            productionOntologyAlertNotificationLogConfig
	Webhook        productionOntologyAlertNotificationWebhookConfig
}

type productionOntologyAlertNotificationLogConfig struct {
	Enabled bool
	Channel string
}

type productionOntologyAlertNotificationWebhookConfig struct {
	Enabled               bool
	Endpoint              string
	Channel               string
	Headers               map[string]string
	AllowPrivateEndpoints bool
}

func registerProductionOntologyAlertNotificationSweeper(rt *platform.Runtime, core *db.Store) error {
	if rt == nil || core == nil {
		return fmt.Errorf("ontology alert notification sweeper requires runtime and core database")
	}
	cfg := productionOntologyAlertNotificationSweeperConfigFromRuntime(rt)
	sender, err := productionOntologyAlertNotificationSender(rt, cfg)
	if err != nil {
		return err
	}
	policies := ontology.NewPlatformModelRunEvaluationTrendAlertNotificationPolicyStore(core)
	sender = ontology.NewModelRunEvaluationTrendAlertNotificationPolicySender(ontology.ModelRunEvaluationTrendAlertNotificationPolicySenderOptions{
		Policies:              policies,
		Default:               sender,
		AllowPrivateEndpoints: cfg.Webhook.AllowPrivateEndpoints,
		Secrets:               platformSecretResolver{provider: rt.Secrets},
	})
	return rt.RegisterSweeper("ontology.model_run_evaluation_alert_notification.dispatcher", ontology.NewModelRunEvaluationTrendAlertNotificationSweeper(ontology.ModelRunEvaluationTrendAlertNotificationSweeperOptions{
		Alerts:   ontology.NewPlatformModelRunEvaluationTrendAlertAuditStore(core),
		History:  ontology.NewPlatformModelRunEvaluationTrendAlertNotificationHistoryStore(core),
		Receipts: ontology.NewPlatformModelRunEvaluationTrendAlertNotificationReceiptStore(core),
		Sender:   sender,
		Clock:    rt.Clock,
		Policy: ontology.ModelRunEvaluationTrendAlertNotificationPolicy{
			RepeatWindow:   cfg.RepeatWindow,
			RecoveryWindow: cfg.RecoveryWindow,
		},
		Policies: policies,
		Query: ontology.ModelRunEvaluationTrendAlertAuditQuery{
			Limit: cfg.Limit,
		},
	}))
}

func registerProductionOntologyAlertNotificationSweeperIfEnabled(rt *platform.Runtime, core *db.Store) error {
	cfg := productionOntologyAlertNotificationSweeperConfigFromRuntime(rt)
	if !cfg.Log.Enabled && !cfg.Webhook.Enabled {
		return nil
	}
	return registerProductionOntologyAlertNotificationSweeper(rt, core)
}

func productionOntologyAlertNotificationSweeperConfigFromRuntime(rt *platform.Runtime) productionOntologyAlertNotificationSweeperConfig {
	cfg := productionOntologyAlertNotificationSweeperConfig{
		Limit: productionOntologyAlertNotificationSweeperLimit,
		Log: productionOntologyAlertNotificationLogConfig{
			Channel: "log",
		},
	}
	if rt == nil {
		return cfg
	}
	if limit, ok := rt.Config.Int("ontology.alert_notification_sweeper.limit"); ok && limit > 0 {
		cfg.Limit = limit
	}
	if repeatWindow, ok := rt.Config.Duration("ontology.alert_notification_sweeper.repeat_window"); ok && repeatWindow > 0 {
		cfg.RepeatWindow = repeatWindow
	}
	if recoveryWindow, ok := rt.Config.Duration("ontology.alert_notification_sweeper.recovery_window"); ok && recoveryWindow > 0 {
		cfg.RecoveryWindow = recoveryWindow
	}
	if enabled, ok := rt.Config.Bool("ontology.alert_notification_sweeper.log.enabled"); ok {
		cfg.Log.Enabled = enabled
	}
	if channel, ok := rt.Config.String("ontology.alert_notification_sweeper.log.channel"); ok {
		cfg.Log.Channel = strings.TrimSpace(channel)
	}
	if cfg.Log.Channel == "" {
		cfg.Log.Channel = "log"
	}
	if enabled, ok := rt.Config.Bool("ontology.alert_notification_sweeper.webhook.enabled"); ok {
		cfg.Webhook.Enabled = enabled
	}
	if endpoint, ok := rt.Config.String("ontology.alert_notification_sweeper.webhook.endpoint"); ok {
		cfg.Webhook.Endpoint = strings.TrimSpace(endpoint)
	}
	if channel, ok := rt.Config.String("ontology.alert_notification_sweeper.webhook.channel"); ok {
		cfg.Webhook.Channel = strings.TrimSpace(channel)
	}
	if cfg.Webhook.Channel == "" {
		cfg.Webhook.Channel = "webhook"
	}
	if headers, ok := stringMapConfig(rt.Config.Lookup("ontology.alert_notification_sweeper.webhook.headers")); ok {
		cfg.Webhook.Headers = headers
	}
	if allowPrivateEndpoints, ok := rt.Config.Bool("ontology.alert_notification_sweeper.webhook.allow_private_endpoints"); ok {
		cfg.Webhook.AllowPrivateEndpoints = allowPrivateEndpoints
	}
	return cfg
}

func productionOntologyAlertNotificationSender(rt *platform.Runtime, cfg productionOntologyAlertNotificationSweeperConfig) (ontology.ModelRunEvaluationTrendAlertNotificationSender, error) {
	if cfg.Webhook.Enabled {
		return ontology.NewModelRunEvaluationTrendAlertNotificationWebhookSender(ontology.ModelRunEvaluationTrendAlertNotificationWebhookSenderOptions{
			Endpoint:              cfg.Webhook.Endpoint,
			Channel:               cfg.Webhook.Channel,
			Headers:               cfg.Webhook.Headers,
			AllowPrivateEndpoints: cfg.Webhook.AllowPrivateEndpoints,
		}), nil
	}
	if cfg.Log.Enabled {
		return productionOntologyAlertNotificationLogSender{logger: rt.Logger, channel: cfg.Log.Channel}, nil
	}
	return nil, fmt.Errorf("ontology alert notification sweeper requires an enabled notification channel")
}

func stringMapConfig(value any, ok bool) (map[string]string, bool) {
	if !ok || value == nil {
		return nil, false
	}
	raw, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	out := map[string]string{}
	for key, value := range raw {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		text := strings.TrimSpace(fmt.Sprint(value))
		if text == "" {
			continue
		}
		out[key] = text
	}
	return out, true
}

type productionOntologyAlertNotificationLogSender struct {
	logger  logging.Logger
	channel string
}

func (s productionOntologyAlertNotificationLogSender) SendModelRunEvaluationTrendAlertNotification(ctx context.Context, event ontology.ModelRunEvaluationTrendAlertAuditEvent, plan ontology.ModelRunEvaluationTrendAlertNotificationPlan) (ontology.ModelRunEvaluationTrendAlertNotificationReceipt, error) {
	sentAt := time.Now().UTC()
	if s.logger != nil {
		s.logger.Warn(ctx, "ontology model run evaluation alert notification",
			logging.String("tenant_id", event.TenantID),
			logging.String("rule_id", event.RuleID),
			logging.String("status", string(event.Status)),
			logging.String("reason", string(plan.Decision.Reason)),
		)
	}
	return ontology.ModelRunEvaluationTrendAlertNotificationReceipt{
		Channel:   s.channel,
		MessageID: "ontology-alert-notification:" + event.TenantID + ":" + event.RuleID + ":" + sentAt.Format(time.RFC3339Nano),
		SentAt:    sentAt,
	}, nil
}
