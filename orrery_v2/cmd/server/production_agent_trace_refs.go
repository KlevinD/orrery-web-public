package main

import (
	"context"
	"errors"
	"strings"

	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/platform/logging"
)

type schedulerRunTraceTracker interface {
	RecordTrace(context.Context, string, string, string) error
}

type responsibilityRunTraceLifecycleTracker interface {
	CompletedWithTrace(context.Context, string, string, string, string) error
	FailedWithTrace(context.Context, string, string, string, runtimedaemon.RuntimeLifecycleKind, error, string) error
}

type responsibilityRunUsageLifecycleTracker interface {
	CompletedWithTraceAndUsage(context.Context, string, string, string, string, int64) error
	FailedWithTraceAndUsage(context.Context, string, string, string, runtimedaemon.RuntimeLifecycleKind, error, string, int64) error
}

func (w productionAgentRunWorker) recordTraceRef(ctx context.Context, run *runtimedaemon.AgentRun, traceRef string, scheduled bool) {
	traceRef = strings.TrimSpace(traceRef)
	if run == nil || traceRef == "" {
		return
	}
	if run.Request.RuntimeOptions.ProviderSpecific == nil {
		run.Request.RuntimeOptions.ProviderSpecific = map[string]string{}
	}
	run.Request.RuntimeOptions.ProviderSpecific["trace_ref"] = traceRef
	if scheduled {
		if tracker, ok := w.scheduler.(schedulerRunTraceTracker); ok {
			_ = tracker.RecordTrace(ctx, scheduledRunIDFromRun(*run), traceRef, "hot")
		}
	}
}

func (w productionAgentRunWorker) recordTerminalResponsibilityFailure(ctx context.Context, run runtimedaemon.AgentRun, cause, lifecycleErr error, usageTokens int64) (bool, error) {
	if runtimeLifecycleTerminalAlreadyWon(lifecycleErr) {
		return false, nil
	}
	if w.responsibility == nil {
		return false, nil
	}
	kind := runtimeLifecycleKindFromFailureCode(runtimeFailureCodeFromError(cause, "agent_runtime_failed"))
	var trackingErr error
	if tracker, ok := w.responsibility.(responsibilityRunUsageLifecycleTracker); ok {
		trackingErr = tracker.FailedWithTraceAndUsage(ctx, run.Request.TenantID, run.TaskID, run.RunID.String(), kind, cause, run.Request.RuntimeOptions.ProviderSpecific["trace_ref"], usageTokens)
	} else if tracker, ok := w.responsibility.(responsibilityRunTraceLifecycleTracker); ok {
		trackingErr = tracker.FailedWithTrace(ctx, run.Request.TenantID, run.TaskID, run.RunID.String(), kind, cause, run.Request.RuntimeOptions.ProviderSpecific["trace_ref"])
	} else {
		trackingErr = w.responsibility.Failed(ctx, run.Request.TenantID, run.TaskID, run.RunID.String(), kind, cause)
	}
	if trackingErr == nil {
		if lifecycleErr != nil && w.logger != nil {
			w.logger.Error(ctx, "publish failed runtime lifecycle", logging.String("run_id", run.RunID.String()), logging.String("task_id", run.TaskID), logging.Any("error", lifecycleErr))
		}
		return true, nil
	}
	if lifecycleErr == nil {
		if w.logger != nil {
			w.logger.Error(ctx, "record failed responsibility attempt", logging.String("run_id", run.RunID.String()), logging.String("task_id", run.TaskID), logging.Any("error", trackingErr))
		}
		return false, nil
	}
	return false, errors.Join(lifecycleErr, trackingErr)
}

func runtimeLifecycleTerminalAlreadyWon(err error) bool {
	return errors.Is(err, missioncontrol.ErrAgentRunTerminal)
}
