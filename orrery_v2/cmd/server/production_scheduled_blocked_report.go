package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/outputpolicy"
	schedulerapp "orrery_v2/internal/app/scheduler"
)

func (n missionTaskAgentResultNormalizer) archiveScheduledBlockedReport(ctx context.Context, input agentcore.AgentInput, result agentcore.AgentResult, output string) (agentcore.AgentResult, error) {
	if n.scheduledArtifacts == nil {
		return agentcore.AgentResult{}, fmt.Errorf("scheduled blocked report archiver is not configured")
	}
	uploader, ok := n.scheduledArtifacts.files.(scheduledArtifactStreamUploader)
	if !ok {
		return agentcore.AgentResult{}, fmt.Errorf("scheduled blocked report streaming store is not configured")
	}
	tenantID := canonicalScheduledArtifactTenant(input.TenantID)
	scheduledRunID := strings.TrimSpace(input.RuntimeOptions.ProviderSpecific["scheduled_run_id"])
	if tenantID == "" || scheduledRunID == "" || input.RunID.IsZero() {
		return agentcore.AgentResult{}, fmt.Errorf("scheduled blocked report identity is incomplete")
	}
	// Archive only public text, never agent-selected workspace files or
	// incomplete deliverables. The blocker remains separate from acceptance.
	const fallback = "任务执行受阻，详细原因已记录到 Orrery。"
	blockerText := outputpolicy.PublicText(result.Blocker.Summary, fallback)
	bodyText := outputpolicy.PublicText(stripAgentResultManifest(output), fallback)
	parts := []string{"# Blocked run report", blockerText}
	if bodyText != blockerText {
		parts = append(parts, bodyText)
	}
	text := strings.Join(parts, "\n\n") + "\n"
	text = outputpolicy.FilterPrivateReasoning(text).Text
	text, _ = agentcore.SanitizeTraceValue(text, agentcore.TraceSanitizerOptions{}).(string)
	artifact, _, err := n.scheduledArtifacts.archiveArtifactReader(ctx, uploader, tenantID, scheduledRunID,
		schedulerapp.RunArtifact{ID: "scheduled-blocked-report:" + input.RunID.String(), Kind: "report"},
		schedulerapp.RunArtifactContent{Reader: io.NopCloser(strings.NewReader(text)), SizeBytes: int64(len(text))},
		"blocked-report.md", "text/markdown")
	if err != nil {
		return agentcore.AgentResult{}, fmt.Errorf("archive scheduled blocked report: %w", err)
	}
	result.Blocker.Report = agentcore.ArtifactRef{ID: artifact.ID, Kind: agentcore.ArtifactKindReport, URI: artifact.URI}
	return result, nil
}
