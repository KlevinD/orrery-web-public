package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/gateway/channel"
	"orrery_v2/internal/platform/outbox"
)

const productionScheduledAgentDeliveryTopic = "scheduler.agent.delivery.v1"

var errScheduledAgentDeliveryDeadLetter = errors.New("scheduled delivery outbox retry budget is exhausted")

type schedulerRunConfigurationSource interface {
	GetRun(context.Context, string) (schedulerapp.ScheduledRun, error)
}

type schedulerTaskContractSource interface {
	GetTask(context.Context, string) (schedulerapp.ScheduledTask, error)
}

type schedulerRunDeliveryTracker interface {
	RecordDelivery(context.Context, string, string, string, string) error
}

type scheduledAgentDeliveryOutboxScheduler interface {
	Schedule(context.Context, runtimedaemon.AgentRun, schedulerapp.ScheduledRun) error
}

type productionScheduledAgentDeliveryPayload struct {
	TenantID         string `json:"tenant_id"`
	RuntimeRunID     string `json:"runtime_run_id"`
	ScheduledRunID   string `json:"scheduled_run_id"`
	ScheduledTaskID  string `json:"scheduled_task_id"`
	MissionID        string `json:"mission_id"`
	TaskID           string `json:"task_id"`
	AgentID          string `json:"agent_id"`
	ResponsibilityID string `json:"responsibility_id"`
}

type productionScheduledAgentDeliveryOutbox struct {
	jobs *outbox.Store
}

func registerProductionScheduledAgentDelivery(opts productionAgentRunWorkerRegistration, artifacts *productionSchedulerArtifactStore) (scheduledAgentDeliveryOutboxScheduler, error) {
	runs, ok := opts.SchedulerTasks.(schedulerRunConfigurationSource)
	if !ok {
		return nil, fmt.Errorf("scheduler repository does not support scheduled delivery recovery")
	}
	tracker, ok := opts.Scheduler.(schedulerRunDeliveryTracker)
	if !ok {
		return nil, fmt.Errorf("scheduler repository does not support scheduled delivery status")
	}
	deliveryOutbox := productionScheduledAgentDeliveryOutbox{jobs: opts.Runtime.Outbox}
	err := opts.Runtime.RegisterOutboxHandler(productionScheduledAgentDeliveryTopic, productionScheduledAgentDeliveryHandler{
		runs: runs, tracker: tracker, sender: opts.ScheduledDelivery,
		authorizer: opts.ScheduledDeliveryAuthorizer, artifacts: artifacts,
	}.HandleOutbox)
	if err != nil {
		return nil, err
	}
	return deliveryOutbox, nil
}

func (o productionScheduledAgentDeliveryOutbox) Schedule(ctx context.Context, run runtimedaemon.AgentRun, expected schedulerapp.ScheduledRun) error {
	if o.jobs == nil {
		return errors.New("scheduled delivery outbox is not configured")
	}
	runtimeRunID := strings.TrimSpace(run.RunID.String())
	if runtimeRunID == "" || runtimeRunID != strings.TrimSpace(expected.RuntimeRunID) {
		return errors.New("scheduled delivery runtime run identity does not match the completed occurrence")
	}
	if strings.TrimSpace(expected.TenantID) == "" || strings.TrimSpace(run.Request.TenantID) != strings.TrimSpace(expected.TenantID) {
		return errors.New("scheduled delivery tenant identity does not match the completed occurrence")
	}
	payload := productionScheduledAgentDeliveryPayload{
		TenantID: strings.TrimSpace(expected.TenantID), RuntimeRunID: runtimeRunID,
		ScheduledRunID: expected.ScheduledRunID, ScheduledTaskID: expected.ScheduledTaskID,
		MissionID: expected.MissionID, TaskID: expected.TaskID, AgentID: expected.AgentID,
		ResponsibilityID: expected.ResponsibilityID,
	}
	if err := validateProductionScheduledAgentDeliveryPayload(payload); err != nil {
		return err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode scheduled delivery outbox payload: %w", err)
	}
	jobID := productionScheduledAgentDeliveryJobID(payload.ScheduledRunID)
	if previousJob, found := o.jobs.Get(ctx, jobID); found && !bytes.Equal(previousJob.Payload, raw) {
		var previous productionScheduledAgentDeliveryPayload
		if err := json.Unmarshal(previousJob.Payload, &previous); err != nil || validateProductionScheduledAgentDeliveryPayload(previous) != nil {
			return errors.New("scheduled delivery outbox identity conflicts with an existing entry")
		}
		// Keep the original delivery immutable. A responsibility retry has a
		// new runtime identity within the same scheduled occurrence.
		previous.RuntimeRunID = payload.RuntimeRunID
		if previous != payload {
			return errors.New("scheduled delivery outbox identity conflicts with an existing entry")
		}
		jobID += ":" + payload.RuntimeRunID
	}
	job := outbox.Job{
		ID: jobID, Topic: productionScheduledAgentDeliveryTopic, Payload: raw,
		IdempotencyKey: jobID, MaxAttempts: 12,
	}
	if err := o.jobs.Enqueue(ctx, job); err != nil {
		return fmt.Errorf("enqueue scheduled delivery: %w", err)
	}
	persisted, ok := o.jobs.Get(ctx, jobID)
	if !ok {
		return errors.New("scheduled delivery outbox entry was not persisted")
	}
	if persisted.Topic != job.Topic || persisted.IdempotencyKey != job.IdempotencyKey || !bytes.Equal(persisted.Payload, raw) {
		return errors.New("scheduled delivery outbox identity conflicts with an existing entry")
	}
	if persisted.Status == outbox.StatusDeadLetter {
		return errScheduledAgentDeliveryDeadLetter
	}
	return nil
}

func productionScheduledAgentDeliveryJobID(scheduledRunID string) string {
	return "scheduled-agent-delivery:" + strings.TrimSpace(scheduledRunID)
}

type productionScheduledAgentDeliveryHandler struct {
	runs       schedulerRunConfigurationSource
	tracker    schedulerRunDeliveryTracker
	sender     channel.OutboundSender
	authorizer scheduledAgentDeliveryAuthorizer
	artifacts  *productionSchedulerArtifactStore
}

func (h productionScheduledAgentDeliveryHandler) HandleOutbox(ctx context.Context, job outbox.Job) error {
	if job.Topic != productionScheduledAgentDeliveryTopic {
		return fmt.Errorf("unsupported scheduled delivery topic %q", job.Topic)
	}
	if h.runs == nil || h.tracker == nil {
		return errors.New("scheduled delivery persistence is not configured")
	}
	var payload productionScheduledAgentDeliveryPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("decode scheduled delivery outbox payload: %w", err)
	}
	if err := validateProductionScheduledAgentDeliveryPayload(payload); err != nil {
		return err
	}
	stored, err := h.runs.GetRun(ctx, payload.ScheduledRunID)
	if err != nil {
		return fmt.Errorf("load scheduled run for delivery: %w", err)
	}
	expected := schedulerapp.ScheduledRun{
		ScheduledRunID: payload.ScheduledRunID, ScheduledTaskID: payload.ScheduledTaskID,
		TargetMode: schedulerapp.TargetModeAgentTask, TenantID: payload.TenantID, MissionID: payload.MissionID, TaskID: payload.TaskID,
		AgentID: payload.AgentID, ResponsibilityID: payload.ResponsibilityID, RuntimeRunID: payload.RuntimeRunID,
	}
	if err := validateScheduledAgentRunCompletionIdentity(stored, expected); err != nil {
		return err
	}
	if strings.TrimSpace(stored.RuntimeRunID) == "" || strings.TrimSpace(stored.RuntimeRunID) != strings.TrimSpace(payload.RuntimeRunID) {
		return errors.New("scheduled delivery runtime run identity does not match the durable result")
	}
	switch stored.Status {
	case schedulerapp.RunStatusQueued, schedulerapp.RunStatusRunning:
		return errors.New("scheduled result is not committed yet")
	case schedulerapp.RunStatusCompleted:
	case schedulerapp.RunStatusFailed:
		if !scheduledRunHasResult(stored.Result) {
			return nil
		}
	default:
		return nil
	}
	switch strings.TrimSpace(stored.DeliveryStatus) {
	case "delivered", "failed", "dead_letter", "not_requested", "not_recovered":
		return nil
	}
	if stored.ConfigurationSnapshotVersion <= 0 {
		return h.tracker.RecordDelivery(ctx, stored.ScheduledRunID, "not_recovered", "immutable delivery configuration is unavailable for this legacy run", "")
	}
	if !strings.EqualFold(strings.TrimSpace(stored.Delivery.Mode), "announce") {
		return h.tracker.RecordDelivery(ctx, stored.ScheduledRunID, "not_requested", "", "")
	}
	if !strings.EqualFold(strings.TrimSpace(stored.Delivery.Channel), "feishu") {
		return h.tracker.RecordDelivery(ctx, stored.ScheduledRunID, "failed", "reliable scheduled delivery is not supported for this channel", "")
	}
	runtimeRun, err := scheduledDeliveryRuntimeRun(payload, stored)
	if err != nil {
		return err
	}
	if err := h.tracker.RecordDelivery(ctx, stored.ScheduledRunID, "sending", "", ""); err != nil {
		return err
	}
	delivery := deliverScheduledAgentResultWithArtifactStore(ctx, h.sender, h.authorizer, h.artifacts, runtimeRun, stored.Result)
	if delivery.Err == nil {
		return h.tracker.RecordDelivery(ctx, stored.ScheduledRunID, delivery.Status, "", delivery.ExternalMessageID)
	}
	permanent := channel.IsPermanentDeliveryError(delivery.Err)
	terminal := permanent || (job.MaxAttempts > 0 && job.Attempts+1 >= job.MaxAttempts)
	status := "retrying"
	if terminal {
		status = "failed"
		if !channel.IsPermanentDeliveryError(delivery.Err) {
			status = "dead_letter"
		}
	}
	if err := h.tracker.RecordDelivery(ctx, stored.ScheduledRunID, status, scheduledDeliveryPublicError(delivery.Err), ""); err != nil {
		return fmt.Errorf("%s; record scheduled delivery status: %w", scheduledDeliveryPublicError(delivery.Err), err)
	}
	if permanent {
		return nil
	}
	return errors.New(scheduledDeliveryPublicError(delivery.Err))
}

func scheduledDeliveryPublicError(err error) string {
	if errors.Is(err, errScheduledDeliveryArtifactUnverified) {
		return "scheduled delivery artifact was not produced or verified for this run"
	}
	if channel.IsPermanentDeliveryError(err) {
		return "scheduled delivery was rejected by its authorization or channel policy"
	}
	return "scheduled delivery did not receive a confirmed channel receipt"
}

func scheduledDeliveryRuntimeRun(payload productionScheduledAgentDeliveryPayload, stored schedulerapp.ScheduledRun) (runtimedaemon.AgentRun, error) {
	metadata := schedulerAgentRunProviderSpecific(schedulerapp.AgentTaskRequest{
		ScheduledTaskID: stored.ScheduledTaskID,
		Title:           stored.ScheduledTaskTitle,
		Delivery:        stored.Delivery,
	}, "")
	metadata["run_source"] = schedulerAgentTaskRunSource
	metadata["scheduled_run_id"] = stored.ScheduledRunID
	contract, err := encodeSchedulerResultContract(stored.ResultContract)
	if err != nil {
		return runtimedaemon.AgentRun{}, fmt.Errorf("encode scheduled delivery result contract: %w", err)
	}
	if contract != "" {
		metadata[schedulerResultContractProviderKey] = contract
	}
	runtimeEnvelope, err := agentcore.CloneExecutionEnvelopeForRun(stored.ExecutionEnvelope, payload.RuntimeRunID, time.Now().UTC())
	if err != nil {
		return runtimedaemon.AgentRun{}, fmt.Errorf("rekey scheduled delivery execution envelope: %w", err)
	}
	return runtimedaemon.AgentRun{
		RunID: agentcore.AgentRunID(payload.RuntimeRunID), AgentID: agentcore.AgentID(stored.AgentID),
		MissionID: stored.MissionID, TaskID: stored.TaskID,
		Request: agentcore.RuntimeRequest{
			TenantID:          payload.TenantID,
			RunID:             agentcore.AgentRunID(payload.RuntimeRunID),
			AgentID:           agentcore.AgentID(stored.AgentID),
			ExecutionEnvelope: runtimeEnvelope,
			ContextPack: agentcore.ContextPack{ResponsibilityContext: &agentcore.ResponsibilityContext{
				ResponsibilityID: stored.ResponsibilityID, MissionID: stored.MissionID, TaskID: stored.TaskID, AgentProfileID: stored.AgentID,
			}},
			RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: metadata},
		},
	}, nil
}

func scheduledDeliveryRuntimeRunFromStored(stored schedulerapp.ScheduledRun) (runtimedaemon.AgentRun, error) {
	if stored.ConfigurationSnapshotVersion <= 0 || stored.TargetMode != schedulerapp.TargetModeAgentTask || strings.TrimSpace(stored.ScheduledTaskTitle) == "" {
		return runtimedaemon.AgentRun{}, errors.New("scheduled delivery immutable occurrence configuration is incomplete")
	}
	payload := productionScheduledAgentDeliveryPayload{
		TenantID: strings.TrimSpace(stored.TenantID), RuntimeRunID: strings.TrimSpace(stored.RuntimeRunID),
		ScheduledRunID: strings.TrimSpace(stored.ScheduledRunID), ScheduledTaskID: strings.TrimSpace(stored.ScheduledTaskID),
		MissionID: strings.TrimSpace(stored.MissionID), TaskID: strings.TrimSpace(stored.TaskID), AgentID: strings.TrimSpace(stored.AgentID),
		ResponsibilityID: strings.TrimSpace(stored.ResponsibilityID),
	}
	if err := validateProductionScheduledAgentDeliveryPayload(payload); err != nil {
		return runtimedaemon.AgentRun{}, err
	}
	return scheduledDeliveryRuntimeRun(payload, stored)
}

func validateProductionScheduledAgentDeliveryPayload(payload productionScheduledAgentDeliveryPayload) error {
	for name, value := range map[string]string{
		"tenant": payload.TenantID, "runtime run": payload.RuntimeRunID, "scheduled run": payload.ScheduledRunID,
		"scheduled task": payload.ScheduledTaskID, "Mission": payload.MissionID, "Mission task": payload.TaskID,
		"Agent": payload.AgentID, "responsibility": payload.ResponsibilityID,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("scheduled delivery %s identity is required", name)
		}
	}
	return nil
}
