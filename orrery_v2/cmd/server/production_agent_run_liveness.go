package main

import (
	"context"
	"strings"
	"time"

	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/logging"
)

type productionAgentRunLiveness interface {
	Touch(context.Context, string) error
	Forget(context.Context, string) error
}

func configuredMissionAgentRunTimeout(cfg config.Config) time.Duration {
	if timeout, ok := cfg.Duration("mission.agent.timeout"); ok && timeout > 0 && timeout <= 24*time.Hour {
		return timeout
	}
	return time.Hour
}

func startAgentRunLivenessHeartbeat(ctx context.Context, liveness productionAgentRunLiveness, runID string, interval time.Duration, logger logging.Logger) func() {
	runtimeID := "agent-run:" + strings.TrimSpace(runID)
	if liveness == nil || strings.TrimSpace(runID) == "" {
		return func() {}
	}
	if interval <= 0 {
		interval = 20 * time.Second
	}
	heartbeatCtx, cancel := context.WithCancel(ctx)
	touch := func() {
		if err := liveness.Touch(heartbeatCtx, runtimeID); err != nil && logger != nil && heartbeatCtx.Err() == nil {
			logger.Warn(ctx, "touch agent runtime liveness", logging.String("run_id", runID), logging.Any("error", err))
		}
	}
	touch()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				touch()
			}
		}
	}()
	return func() {
		cancel()
		<-done
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if err := liveness.Forget(cleanupCtx, runtimeID); err != nil && logger != nil {
			logger.Warn(ctx, "forget agent runtime liveness", logging.String("run_id", runID), logging.Any("error", err))
		}
	}
}
