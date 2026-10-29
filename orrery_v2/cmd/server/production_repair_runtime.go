package main

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/platform/config"
)

const automaticRepairDispatcherID = "orrery-scheduled-repair"

// Only the authenticated maintenance service can select the configured coding
// runtime. Ordinary conversations keep the agent's selected runtime and tools.
// Model selection, workspace policy, MCP ACLs and approvals remain host-owned.
func automaticRepairRuntimeSpec(ctx context.Context, configRef string, request discussionsession.DiscussionAgentMentionRequest, original agentcore.RuntimeSpec) agentcore.RuntimeSpec {
	configRef = strings.TrimSpace(configRef)
	if configRef == "" || !isAutomaticRepairRequest(ctx, request) {
		return original
	}
	return agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex, ConfigRef: configRef}
}

func automaticRepairRuntimeTimeout(ctx context.Context, cfg config.Config, request discussionsession.DiscussionAgentMentionRequest, original time.Duration) time.Duration {
	if configString(cfg, "maintenance.repair_runtime_config_ref") == "" || !isAutomaticRepairRequest(ctx, request) {
		return original
	}
	if timeout, ok := cfg.Duration("maintenance.repair_timeout"); ok && timeout > 0 && timeout <= 90*time.Minute {
		return timeout
	}
	return original
}

func isAutomaticRepairRequest(ctx context.Context, request discussionsession.DiscussionAgentMentionRequest) bool {
	if !identity.TrustedInternalServiceFromContext(ctx) {
		return false
	}
	principal, ok := identity.PrincipalFromContext(ctx)
	if !ok || principal.ViewerKind != identity.ViewerKindSystem ||
		principal.UserID != automaticRepairDispatcherID || principal.Subject != automaticRepairDispatcherID ||
		principal.TenantID == "" || principal.TenantID != request.ConversationTenantID ||
		request.SourceMessage.AuthorID != automaticRepairDispatcherID ||
		request.ConversationOwnerID != automaticRepairDispatcherID ||
		request.TargetAgent.ID != defaultOntologyStewardAgentID ||
		request.ConversationKind != "direct" || request.Workspace.AgentID != defaultOntologyStewardAgentID ||
		!filepath.IsAbs(request.Workspace.RootDir) || !filepath.IsAbs(request.Workspace.AgentWorkDir) {
		return false
	}
	relative, err := filepath.Rel(request.Workspace.RootDir, request.Workspace.AgentWorkDir)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}
