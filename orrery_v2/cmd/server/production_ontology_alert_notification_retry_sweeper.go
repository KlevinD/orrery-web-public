package main

import (
	"fmt"
	"time"

	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
)

const productionOntologyAlertNotificationRetrySweeperLimit = 100
const productionOntologyAlertNotificationRetrySweeperMaxAttempts = 3
const productionOntologyAlertNotificationRetrySweeperBackoff = time.Minute

type productionOntologyAlertNotificationRetrySweeperConfig struct {
	Enabled     bool
	Limit       int
	MaxAttempts int
	Backoff     time.Duration
}

func registerProductionOntologyAlertNotificationRetrySweeper(rt *platform.Runtime, core *db.Store) error {
	if rt == nil || core == nil {
		return fmt.Errorf("ontology alert notification retry sweeper requires runtime and core database")
	}
	cfg := productionOntologyAlertNotificationRetrySweeperConfigFromRuntime(rt)
	notificationCfg := productionOntologyAlertNotificationSweeperConfigFromRuntime(rt)
	var sender ontology.ModelRunEvaluationTrendAlertNotificationSender
	if notificationCfg.Log.Enabled || notificationCfg.Webhook.Enabled {
		var err error
		sender, err = productionOntologyAlertNotificationSender(rt, notificationCfg)
		if err != nil {
			return err
		}
		policies := ontology.NewPlatformModelRunEvaluationTrendAlertNotificationPolicyStore(core)
		sender = ontology.NewModelRunEvaluationTrendAlertNotificationPolicySender(ontology.ModelRunEvaluationTrendAlertNotificationPolicySenderOptions{
			Policies:              policies,
			Default:               sender,
			AllowPrivateEndpoints: notificationCfg.Webhook.AllowPrivateEndpoints,
			Secrets:               platformSecretResolver{provider: rt.Secrets},
		})
	}
	return rt.RegisterSweeper("ontology.model_run_evaluation_alert_notification.retry", ontology.NewModelRunEvaluationTrendAlertNotificationRetrySweeper(ontology.ModelRunEvaluationTrendAlertNotificationRetrySweeperOptions{
		Receipts:     ontology.NewPlatformModelRunEvaluationTrendAlertNotificationReceiptStore(core),
		History:      ontology.NewPlatformModelRunEvaluationTrendAlertNotificationHistoryStore(core),
		Sender:       sender,
		Clock:        rt.Clock,
		MaxAttempts:  cfg.MaxAttempts,
		RetryBackoff: cfg.Backoff,
		Query: ontology.ModelRunEvaluationTrendAlertNotificationReceiptQuery{
			Limit: cfg.Limit,
		},
	}))
}

func registerProductionOntologyAlertNotificationRetrySweeperIfEnabled(rt *platform.Runtime, core *db.Store) error {
	cfg := productionOntologyAlertNotificationRetrySweeperConfigFromRuntime(rt)
	if !cfg.Enabled {
		return nil
	}
	return registerProductionOntologyAlertNotificationRetrySweeper(rt, core)
}

func productionOntologyAlertNotificationRetrySweeperConfigFromRuntime(rt *platform.Runtime) productionOntologyAlertNotificationRetrySweeperConfig {
	cfg := productionOntologyAlertNotificationRetrySweeperConfig{
		Limit:       productionOntologyAlertNotificationRetrySweeperLimit,
		MaxAttempts: productionOntologyAlertNotificationRetrySweeperMaxAttempts,
		Backoff:     productionOntologyAlertNotificationRetrySweeperBackoff,
	}
	if rt == nil {
		return cfg
	}
	if enabled, ok := rt.Config.Bool("ontology.alert_notification_retry_sweeper.enabled"); ok {
		cfg.Enabled = enabled
	}
	if limit, ok := rt.Config.Int("ontology.alert_notification_retry_sweeper.limit"); ok && limit > 0 {
		cfg.Limit = limit
	}
	if maxAttempts, ok := rt.Config.Int("ontology.alert_notification_retry_sweeper.max_attempts"); ok && maxAttempts > 0 {
		cfg.MaxAttempts = maxAttempts
	}
	if backoff, ok := rt.Config.Duration("ontology.alert_notification_retry_sweeper.backoff"); ok && backoff >= 0 {
		cfg.Backoff = backoff
	}
	return cfg
}
