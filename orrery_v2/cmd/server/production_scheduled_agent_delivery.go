package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	schedulerapp "orrery_v2/internal/app/scheduler"
	gwbindings "orrery_v2/internal/gateway/binding"
	"orrery_v2/internal/gateway/channel"
)

const (
	scheduledDeliveryContentMaxBytes int64 = 64 << 10
	scheduledFeishuServiceAccountID        = "orrery"
)

var errScheduledDeliveryArtifactUnverified = errors.New("configured delivery artifact was not verified for this run")
var errScheduledDeliverySummaryUnavailable = errors.New("scheduled Agent has no public result text")

type scheduledAgentDeliveryResult struct {
	Status            string
	ExternalMessageID string
	Err               error
}

type scheduledAgentDeliveryAuthorizer interface {
	AuthorizeAgentChannel(ctx context.Context, tenantID, channelKey, accountID, agentProfileID string) error
}

type gatewayScheduledAgentDeliveryAuthorizer struct {
	bindings *gwbindings.Store
}

func (a gatewayScheduledAgentDeliveryAuthorizer) AuthorizeAgentChannel(ctx context.Context, tenantID, channelKey, accountID, agentProfileID string) error {
	tenantID = strings.TrimSpace(tenantID)
	channelKey = strings.ToLower(strings.TrimSpace(channelKey))
	accountID = strings.TrimSpace(accountID)
	agentProfileID = strings.TrimSpace(agentProfileID)
	if a.bindings == nil || tenantID == "" || channelKey != "feishu" || accountID == "" || agentProfileID == "" {
		return channel.PermanentDeliveryError(errors.New("scheduled Feishu delivery ownership is invalid"))
	}
	if accountID == scheduledFeishuServiceAccountID {
		if tenantID != "system" {
			return channel.PermanentDeliveryError(errors.New("scheduled Feishu service account requires system tenant"))
		}
		return nil
	}
	owner, err := a.bindings.ResolveAgentChannelBinding(ctx, channelKey, accountID)
	if err != nil {
		if errors.Is(err, gwbindings.ErrNotFound) || errors.Is(err, gwbindings.ErrInvalidInput) {
			return channel.PermanentDeliveryError(fmt.Errorf("resolve scheduled Feishu delivery owner: %w", err))
		}
		return fmt.Errorf("resolve scheduled Feishu delivery owner: %w", err)
	}
	if owner.AgentProfileID != agentProfileID || (owner.TenantID != tenantID && tenantID != "system") {
		return channel.PermanentDeliveryError(errors.New("scheduled Feishu delivery account owner mismatch"))
	}
	return nil
}

func deliverScheduledAgentResult(ctx context.Context, sender channel.OutboundSender, authorizer scheduledAgentDeliveryAuthorizer, run runtimedaemon.AgentRun, runResult schedulerapp.RunResult) scheduledAgentDeliveryResult {
	return deliverScheduledAgentResultWithArtifactStore(ctx, sender, authorizer, nil, run, runResult)
}

func deliverScheduledAgentResultWithArtifactStore(ctx context.Context, sender channel.OutboundSender, authorizer scheduledAgentDeliveryAuthorizer, artifacts *productionSchedulerArtifactStore, run runtimedaemon.AgentRun, runResult schedulerapp.RunResult) scheduledAgentDeliveryResult {
	metadata := run.Request.RuntimeOptions.ProviderSpecific
	if strings.TrimSpace(metadata["delivery_mode"]) != "announce" {
		return scheduledAgentDeliveryResult{Status: "not_requested"}
	}
	channelKey := strings.TrimSpace(metadata["delivery_channel"])
	accountID := strings.TrimSpace(metadata["delivery_account_id"])
	if channelKey == "feishu" {
		if authorizer == nil {
			return scheduledAgentDeliveryResult{Status: "failed", Err: channel.PermanentDeliveryError(errors.New("scheduled Feishu delivery ownership is not configured"))}
		}
		if err := authorizer.AuthorizeAgentChannel(ctx, run.Request.TenantID, channelKey, accountID, run.AgentID.String()); err != nil {
			return scheduledAgentDeliveryResult{Status: "failed", Err: err}
		}
	}
	if sender == nil {
		return scheduledAgentDeliveryResult{Status: "failed", Err: errors.New("scheduled result delivery is not configured")}
	}
	message, format, err := scheduledAgentDeliveryContentWithArtifactStore(ctx, artifacts, run, runResult)
	if err != nil {
		return scheduledAgentDeliveryResult{Status: "failed", Err: err}
	}
	message = scheduledAgentPublicText(message)
	if message == "" {
		return scheduledAgentDeliveryResult{Status: "failed", Err: channel.PermanentDeliveryError(errScheduledDeliverySummaryUnavailable)}
	}
	recipient := strings.TrimPrefix(strings.TrimSpace(metadata["delivery_recipient_id"]), "user:")
	result, err := sender.Send(ctx, channel.Delivery{
		ID:             "scheduled-result:" + run.RunID.String(),
		ChannelKey:     channelKey,
		AccountID:      accountID,
		RecipientID:    recipient,
		Title:          strings.TrimSpace(metadata["scheduled_task_name"]),
		Text:           message,
		Format:         format,
		IdempotencyKey: "scheduled-result:" + run.RunID.String(),
	})
	if err != nil {
		return scheduledAgentDeliveryResult{Status: "failed", Err: err}
	}
	externalMessageID := strings.TrimSpace(result.ExternalMessageID)
	if externalMessageID == "" {
		return scheduledAgentDeliveryResult{Status: "failed", Err: errors.New("scheduled result delivery returned no external receipt")}
	}
	return scheduledAgentDeliveryResult{Status: "delivered", ExternalMessageID: externalMessageID}
}

func scheduledAgentDeliveryContent(run runtimedaemon.AgentRun, runResult schedulerapp.RunResult) (string, string, error) {
	return scheduledAgentDeliveryContentWithArtifactStore(context.Background(), nil, run, runResult)
}

func scheduledAgentDeliveryContentWithArtifactStore(ctx context.Context, artifacts *productionSchedulerArtifactStore, run runtimedaemon.AgentRun, runResult schedulerapp.RunResult) (string, string, error) {
	metadata := run.Request.RuntimeOptions.ProviderSpecific
	format := strings.ToLower(strings.TrimSpace(metadata["delivery_format"]))
	if format == "" {
		format = "text"
	}
	artifactID := strings.TrimSpace(metadata["delivery_content_artifact_id"])
	if artifactID == "" {
		message := strings.TrimSpace(runResult.Summary)
		if message == "" {
			return "", "", channel.PermanentDeliveryError(errScheduledDeliverySummaryUnavailable)
		}
		return message, format, nil
	}
	expectedID := "scheduled-artifact:" + run.RunID.String() + ":" + artifactID
	var selected schedulerapp.RunArtifact
	for _, artifact := range runResult.Artifacts {
		if artifact.ID == expectedID {
			selected = artifact
			break
		}
	}
	if selected.ID == "" {
		return "", "", channel.PermanentDeliveryError(fmt.Errorf("%w: %q", errScheduledDeliveryArtifactUnverified, artifactID))
	}
	fileName := firstNonBlank(selected.FileName, selected.URI)
	switch strings.ToLower(filepath.Ext(fileName)) {
	case ".md", ".txt":
	default:
		return "", "", fmt.Errorf("configured delivery artifact %q is not a supported text file", artifactID)
	}
	if _, ok := scheduledArtifactKeyFromTypedURI(selected.URI); ok {
		if artifacts == nil {
			return "", "", fmt.Errorf("configured delivery artifact %q snapshot store is not configured", artifactID)
		}
		content, err := artifacts.OpenFinalizedArtifact(ctx, run.Request.TenantID, scheduledRunIDFromRun(run), selected)
		if err != nil {
			return "", "", fmt.Errorf("open configured delivery artifact %q: %w", artifactID, err)
		}
		defer content.Reader.Close()
		return readScheduledDeliveryContent(content.Reader, content.SizeBytes, artifactID, format)
	}
	path := filepath.Clean(strings.TrimSpace(selected.URI))
	if !filepath.IsAbs(path) {
		return "", "", fmt.Errorf("configured delivery artifact %q does not have an absolute path", artifactID)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", "", fmt.Errorf("open configured delivery artifact %q: %w", artifactID, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", "", fmt.Errorf("inspect configured delivery artifact %q: %w", artifactID, err)
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("configured delivery artifact %q is not a regular file", artifactID)
	}
	if info.Size() > scheduledDeliveryContentMaxBytes {
		return "", "", fmt.Errorf("configured delivery artifact %q exceeds 64 KiB", artifactID)
	}
	return readScheduledDeliveryContent(file, info.Size(), artifactID, format)
}

func readScheduledDeliveryContent(reader io.Reader, sizeBytes int64, artifactID, format string) (string, string, error) {
	if sizeBytes > scheduledDeliveryContentMaxBytes {
		return "", "", fmt.Errorf("configured delivery artifact %q exceeds 64 KiB", artifactID)
	}
	body, err := io.ReadAll(io.LimitReader(reader, scheduledDeliveryContentMaxBytes+1))
	if err != nil {
		return "", "", fmt.Errorf("read configured delivery artifact %q: %w", artifactID, err)
	}
	if int64(len(body)) > scheduledDeliveryContentMaxBytes {
		return "", "", fmt.Errorf("configured delivery artifact %q exceeds 64 KiB", artifactID)
	}
	message := strings.TrimSpace(string(body))
	if message == "" {
		return "", "", fmt.Errorf("configured delivery artifact %q is empty", artifactID)
	}
	return message, format, nil
}

type scheduledAgentDeliveryRelay struct {
	mu     sync.RWMutex
	sender channel.OutboundSender
}

func (r *scheduledAgentDeliveryRelay) SetSender(sender channel.OutboundSender) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.sender = sender
	r.mu.Unlock()
}

func (r *scheduledAgentDeliveryRelay) Send(ctx context.Context, delivery channel.Delivery) (channel.DeliveryResult, error) {
	if r == nil {
		return channel.DeliveryResult{}, errors.New("scheduled result delivery relay is not configured")
	}
	r.mu.RLock()
	sender := r.sender
	r.mu.RUnlock()
	if sender == nil {
		return channel.DeliveryResult{}, errors.New("scheduled result delivery channel is not ready")
	}
	return sender.Send(ctx, delivery)
}

func scheduledAgentRunTimeout(run runtimedaemon.AgentRun, fallback time.Duration) time.Duration {
	if !isSchedulerAgentRun(run) {
		return fallback
	}
	seconds, err := strconv.Atoi(strings.TrimSpace(run.Request.RuntimeOptions.ProviderSpecific["timeout_seconds"]))
	if err != nil || seconds <= 0 || seconds > 24*60*60 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}
