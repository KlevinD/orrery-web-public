package main

import (
	"context"

	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/platform/logging"
)

type productionRuntimeLifecyclePublisher struct {
	mission  runtimedaemon.RuntimeLifecyclePublisher
	ontology runtimedaemon.RuntimeLifecyclePublisher
	logger   logging.Logger
}

func (p productionRuntimeLifecyclePublisher) PublishRuntimeLifecycle(ctx context.Context, event runtimedaemon.RuntimeLifecycleEvent) error {
	missionPublished := false
	if p.mission != nil && event.MissionID != "" && event.TaskID != "" {
		if err := p.mission.PublishRuntimeLifecycle(ctx, event); err != nil {
			return err
		}
		missionPublished = true
	}
	if p.ontology != nil {
		if err := p.ontology.PublishRuntimeLifecycle(ctx, event); err != nil {
			if !missionPublished {
				return err
			}
			if p.logger != nil {
				p.logger.Warn(ctx, "publish runtime lifecycle ontology projection",
					logging.String("run_id", event.RunID.String()),
					logging.String("task_id", event.TaskID.String()),
					logging.Any("error", err),
				)
			}
		}
	}
	return nil
}

func (p productionRuntimeLifecyclePublisher) PublishRuntimeFailure(ctx context.Context, event runtimedaemon.RuntimeFailureEvent) error {
	missionPublished := false
	if p.mission != nil && event.MissionID != "" && event.TaskID != "" {
		if err := p.mission.PublishRuntimeFailure(ctx, event); err != nil {
			return err
		}
		missionPublished = true
	}
	if p.ontology != nil {
		if err := p.ontology.PublishRuntimeFailure(ctx, event); err != nil {
			if !missionPublished {
				return err
			}
			if p.logger != nil {
				p.logger.Warn(ctx, "publish runtime failure ontology projection",
					logging.String("run_id", event.RunID.String()),
					logging.String("task_id", event.TaskID.String()),
					logging.Any("error", err),
				)
			}
		}
	}
	return nil
}
