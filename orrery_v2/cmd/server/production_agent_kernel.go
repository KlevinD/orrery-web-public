package main

import (
	"context"
	"orrery_v2/internal/agent/llm"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/agentkernel"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/providers/kernelcompat"
	"orrery_v2/internal/agent/runtime/providers/profilemodel"
	"orrery_v2/internal/agent/runtime/providers/toolbridge"
	"orrery_v2/internal/agent/runtime/session"
	toolstd "orrery_v2/internal/resource/tool/std"
	httptransport "orrery_v2/internal/transport/http"
)

const agentCoreRuntimeProfileID = "runtime-orrery-agentcore"

func ensureProductionAgentCoreProfile(settings *httptransport.SettingsDTO) {
	ensureProductionClaudeProfile(settings)
	for _, profile := range settings.RuntimeProfiles {
		if profile.ProfileID == agentCoreRuntimeProfileID {
			return
		}
	}
	settings.RuntimeProfiles = append(settings.RuntimeProfiles, httptransport.SettingsRuntimeProfileDTO{
		ProfileID: agentCoreRuntimeProfileID, DisplayName: "Orrery AgentCore", RuntimeKind: "managed",
		RuntimeConfigRef: agentCoreRuntimeProfileID, RuntimeConfig: map[string]any{}, Enabled: true,
	})
}

func productionClaudeExecutable() string {
	base := strings.TrimSpace(os.Getenv("ORRERY_BASE_DIR"))
	if base == "" {
		return ""
	}
	path := filepath.Join(base, "runtime", "claude-native-2.1.220", "node_modules", ".bin", "claude")
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return path
	}
	return ""
}

func ensureProductionClaudeProfile(settings *httptransport.SettingsDTO) {
	for _, profile := range settings.RuntimeProfiles {
		if profile.RuntimeKind == "claude" {
			return
		}
	}
	if executable := productionClaudeExecutable(); executable != "" {
		settings.RuntimeProfiles = append(settings.RuntimeProfiles, httptransport.SettingsRuntimeProfileDTO{
			ProfileID: "runtime-claude-orrery-native", DisplayName: "Claude Code", RuntimeKind: "claude",
			RuntimeConfigRef: "runtime-claude-orrery-native", Enabled: true,
			RuntimeConfig: map[string]any{"mode": "local", "executable": executable, "version": "2.1.220"},
		})
	}
}

func productionAgentCoreConnectionConfig(deps productionDiscussionAgentDependencies, workspaces *session.WorkspaceBroker) *kernelcompat.Config {
	if deps.KernelJournal == nil || deps.LLMProfiles == nil || deps.ToolDispatcher == nil {
		return nil
	}
	factory := profilemodel.NewProfileModelFactory(profilemodel.ProfileModelFactoryConfig{Profiles: deps.LLMProfiles, Secrets: deps.Secrets})
	engine, err := agentkernel.NewEngine(agentkernel.Config{
		Workspaces: workspaces, Models: productionAgentCoreModels{factory: factory, deps: deps},
		Tools: deps.ToolDispatcher, ToolJournal: deps.KernelJournal, AuthorizeTool: productionAgentCoreToolPolicy,
		MaxModelCalls: 128, MaxToolCalls: 256, ModelTimeout: 2 * time.Minute, ToolTimeout: 20 * time.Minute,
		ExplicitToolScopeFence: true, MaxParallelTools: 4, ParallelToolIDs: productionAgentCoreParallelToolIDs(),
		ApprovalToolIDs: []string{string(toolstd.KnowledgeAdd)},
		Checkpoints:     deps.KernelCheckpoints, AutoCompact: deps.KernelCheckpoints != nil, PromptCache: deps.KernelCheckpoints != nil,
	})
	if err != nil {
		panic(err)
	}
	return &kernelcompat.Config{Engine: engine, MaxTurnTimeout: 90 * time.Minute}
}

// Explicit model profile selections must never silently fall back to a different
// profile. Credentials remain in the host's existing settings/secret resolver.
type productionAgentCoreModels struct {
	factory *profilemodel.ProfileModelFactory
	deps    productionDiscussionAgentDependencies
}

func (m productionAgentCoreModels) ModelForRequest(ctx context.Context, request profilemodel.ModelRequest) (llm.ChatModel, error) {
	// The shared selector resolves the live platform-default reference and
	// rejects missing or disabled explicit profiles in the same settings read.
	return m.factory.ModelForRequest(ctx, request)
}

// The gateway retains its existing ACLs and business approval workflows. Added
// business capabilities require the host-sealed execution envelope as well as
// a bound tool and its existing permission; a tool name never grants authority.
func productionAgentCoreToolPolicy(ctx context.Context, call runtimecore.RuntimeToolCallRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(call.TenantID) == "" || call.AgentID.IsZero() || call.RunID.IsZero() || runtimeToolRequiresExplicitMCPScope(call) {
		return agentkernel.ErrToolDenied
	}
	binding, ok := runtimeToolBindingForCall(call.Name, call.ToolBindings)
	if !ok {
		return agentkernel.ErrToolDenied
	}
	if err := validateRuntimeExecutionEnvelope(call, binding); err != nil {
		return agentkernel.ErrToolDenied
	}
	if envelope := call.ExecutionEnvelope; envelope != nil {
		principal := runtimeExecutionPrincipal(call)
		if (envelope.PrincipalID != "" && envelope.PrincipalID != principal.ID) ||
			(envelope.PrincipalKind != "" && envelope.PrincipalKind != principal.Kind) {
			return agentkernel.ErrToolDenied
		}
	}
	permission := ""
	// Website reception is never allowed to use generic internal knowledge,
	// memory or organization tools, even if its stored bindings are mistaken.
	if call.AgentID == agentcore.AgentID(defaultWebsiteAdvisorAgentID) &&
		binding.ToolID != toolstd.WebSearch && binding.ToolID != toolstd.WebFetch &&
		!strings.HasPrefix(string(binding.ToolID), string(defaultOrreryPublicMCPServerToolID)+".") {
		return agentkernel.ErrToolDenied
	}
	if binding.Source == "internal_plan" && toolbridge.IsPlanTool(toolbridge.RuntimeToolName(binding)) {
		if call.ExecutionEnvelope != nil && slices.Contains(call.ExecutionEnvelope.AllowedToolIDs, string(binding.ToolID)) {
			return nil
		}
		return agentkernel.ErrToolDenied
	}
	switch binding.ToolID {
	case "desktop_list_files", "desktop_read_excel", "desktop_write_excel":
		permission = desktopToolPermission(string(binding.ToolID))
		if call.ExecutionEnvelope == nil || !slices.Contains(call.ExecutionEnvelope.AllowedToolIDs, string(binding.ToolID)) || !call.ExecutionEnvelope.AllowsTool(string(binding.ToolID), []string{permission}) {
			return agentkernel.ErrToolDenied
		}
	case toolstd.KnowledgeSearch:
		permission = toolstd.PermissionKnowledgeRead
	case toolstd.KnowledgeAdd:
		permission = toolstd.PermissionKnowledgeWrite
	case toolstd.MemorySearch:
		permission = toolstd.PermissionMemoryRead
	case toolstd.MemoryDiaryRead:
		permission = toolstd.PermissionMemoryDiaryRead
	case toolstd.SessionSearch, toolstd.SessionRead:
		permission = toolstd.PermissionDiscussionRead
	case toolstd.ScheduledTaskList:
		permission = toolstd.PermissionSchedulerRead
	case toolstd.ScheduledTaskCreate, toolstd.ScheduledTaskUpdate, toolstd.ScheduledTaskCancel:
		permission = toolstd.PermissionSchedulerWrite
	case toolstd.ScheduledTaskRunNow:
		permission = toolstd.PermissionSchedulerRun
	case toolstd.SkillList, toolstd.SkillLoad, toolstd.SkillResourceList, toolstd.SkillResourceLoad:
		// The existing skill executor can read only this run's bound packages.
		return nil
	case toolstd.RSILearningPlan:
		permission = "rsi.learning.read"
	case toolstd.PersonalAssistantShellExec:
		permission = toolstd.PermissionPersonalAssistantShell
	case toolstd.WebSearch, toolstd.WebFetch:
		permission = toolstd.PermissionWebRead
	case toolstd.EnterpriseGraphBuildContextBundle, toolstd.EnterpriseGraphSearchObjects:
		permission = toolstd.PermissionEnterpriseGraphRead
	default:
		if isExternalMCPRuntimeBinding(binding) && call.ExecutionEnvelope != nil &&
			(slices.Contains(call.ExecutionEnvelope.AllowedToolIDs, string(binding.ToolID)) ||
				runtimePermissionScopesContain(call.ExecutionEnvelope.PermissionScopes, "tool.call")) &&
			string(binding.ToolID) == strings.TrimSpace(binding.ServerToolID)+"."+strings.TrimSpace(binding.DownstreamToolName) {
			// Explicit host task grants are provider-independent. The shared MCP
			// gateway still owns tenant ACLs and business approval requirements.
			return nil
		}
		if !isExternalMCPRuntimeBinding(binding) || strings.TrimSpace(binding.ServerToolID) != string(defaultOrreryPublicMCPServerToolID) {
			return agentkernel.ErrToolDenied
		}
		if string(binding.ToolID) != string(defaultOrreryPublicMCPServerToolID)+"."+strings.TrimSpace(binding.DownstreamToolName) {
			return agentkernel.ErrToolDenied
		}
		switch strings.TrimSpace(binding.DownstreamToolName) {
		case "search_documents", "get_document", "get_document_structure", "get_page_content":
			// Public MCP access still goes through the gateway's actual ACLs.
			return nil
		default:
			return agentkernel.ErrToolDenied
		}
	}
	if isSchedulerRuntimeBinding(binding) && binding.ToolID != toolstd.ScheduledTaskList {
		// Native scheduler writes require the host-owned run authorization,
		// including the operation's scope even if a binding omits its scopes.
		envelope := call.ExecutionEnvelope
		principal := runtimeExecutionPrincipal(call)
		if envelope == nil || len(envelope.PermissionScopes) == 0 ||
			!envelope.AllowsTool(string(binding.ToolID), []string{permission}) ||
			envelope.PrincipalID != principal.ID || envelope.PrincipalKind != principal.Kind {
			return agentkernel.ErrToolDenied
		}
	}
	if binding.ToolID == toolstd.PersonalAssistantShellExec {
		if call.ExecutionEnvelope == nil || !call.ExecutionEnvelope.AllowsTool(string(binding.ToolID), []string{permission}) {
			return agentkernel.ErrToolDenied
		}
	}
	if !runtimePermissionScopesContain(runtimeGatewayPermissions(call, binding), permission) {
		return agentkernel.ErrToolDenied
	}
	return nil
}
