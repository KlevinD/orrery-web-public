package runtimeapproval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/app/actioncontract"
)

type DecisionOutcome string

const (
	DecisionOutcomeApproveOnce DecisionOutcome = "approve_once"
	DecisionOutcomeHuman       DecisionOutcome = "human"
	DecisionOutcomeDeny        DecisionOutcome = "deny"
)

type PolicyProvider interface {
	RuntimeApprovalPolicy(context.Context) (Policy, error)
}

type PolicyProviderFunc func(context.Context) (Policy, error)

func (provider PolicyProviderFunc) RuntimeApprovalPolicy(ctx context.Context) (Policy, error) {
	return provider(ctx)
}

type StaticPolicyProvider struct {
	Policy Policy
}

func (provider StaticPolicyProvider) RuntimeApprovalPolicy(context.Context) (Policy, error) {
	return Clone(provider.Policy), nil
}

type ReviewService interface {
	Review(context.Context, ReviewerSettings, ReviewInput) ReviewResult
}

type DecisionInput struct {
	Request                            runtimecore.ApprovalRequest
	LogicalAgentID                     string
	ConversationKind                   string
	ExplicitConversationHIL            string
	ExplicitConversationApprovalPolicy string
	WorkspaceRoots                     []string
}

type Decision struct {
	Outcome                    DecisionOutcome
	EffectiveHIL               HILMode
	EffectiveApprovalPolicy    ApprovalPolicy
	EffectiveApprovalsReviewer ApprovalsReviewer
	PolicySource               ResolutionSource
	PolicyDigest               string
	DecisionSource             string
	Reason                     string
	RuleID                     string
	InputDigest                string
	Review                     *ReviewResult
	Intent                     ActionIntent
}

type DecisionService struct {
	policies PolicyProvider
	reviewer ReviewService
}

func NewDecisionService(policies PolicyProvider, reviewer ReviewService) *DecisionService {
	return &DecisionService{policies: policies, reviewer: reviewer}
}

func (service *DecisionService) Decide(ctx context.Context, input DecisionInput) Decision {
	policy := DefaultPolicy()
	intent := ClassifyActionIntent(input.Request)
	if service != nil && service.policies != nil {
		loaded, err := service.policies.RuntimeApprovalPolicy(ctx)
		if err != nil {
			resolved := policy.Resolve(ResolveInput{
				LogicalAgentID:                     input.LogicalAgentID,
				ConversationKind:                   input.ConversationKind,
				ExplicitConversationHIL:            input.ExplicitConversationHIL,
				ExplicitConversationApprovalPolicy: input.ExplicitConversationApprovalPolicy,
			})
			outcome := DecisionOutcomeDeny
			decisionSource := "policy"
			if input.Request.Kind == runtimecore.ApprovalKindMCPElicitation ||
				(intent.ActionTypeID != actioncontract.ActionBusinessApprovalRequest && intentRequiresHuman(intent)) {
				outcome = DecisionOutcomeHuman
				decisionSource = "human"
			}
			return Decision{
				Outcome:                    outcome,
				EffectiveHIL:               resolved.HILMode,
				EffectiveApprovalPolicy:    resolved.ApprovalPolicy,
				EffectiveApprovalsReviewer: resolved.ApprovalsReviewer,
				PolicySource:               resolved.Source,
				PolicyDigest:               resolved.Digest,
				DecisionSource:             decisionSource,
				Reason:                     "policy_unavailable",
				InputDigest:                approvalInputDigest(input.Request),
				Intent:                     intent,
			}
		}
		policy = loaded
	}
	resolved := policy.Resolve(ResolveInput{
		LogicalAgentID:                     input.LogicalAgentID,
		ConversationKind:                   input.ConversationKind,
		ExplicitConversationHIL:            input.ExplicitConversationHIL,
		ExplicitConversationApprovalPolicy: input.ExplicitConversationApprovalPolicy,
	})
	decision := Decision{
		Outcome:                    DecisionOutcomeHuman,
		EffectiveHIL:               resolved.HILMode,
		EffectiveApprovalPolicy:    resolved.ApprovalPolicy,
		EffectiveApprovalsReviewer: resolved.ApprovalsReviewer,
		PolicySource:               resolved.Source,
		PolicyDigest:               resolved.Digest,
		DecisionSource:             "human",
		InputDigest:                approvalInputDigest(input.Request),
		Intent:                     intent,
	}
	if input.Request.Kind == runtimecore.ApprovalKindMCPElicitation {
		decision.DecisionSource = "forced_human"
		decision.Reason = "forced_mcp_elicitation"
		return decision
	}

	switch input.Request.Kind {
	case runtimecore.ApprovalKindCommandExecution:
		return service.decideExec(ctx, input, resolved, decision)
	case runtimecore.ApprovalKindFileChange, runtimecore.ApprovalKindTool:
		return service.decideTool(ctx, input, resolved, decision)
	default:
		decision.Outcome = DecisionOutcomeDeny
		decision.DecisionSource = "policy"
		decision.Reason = "approval_kind_invalid"
		return decision
	}
}

func (service *DecisionService) decideExec(ctx context.Context, input DecisionInput, policy ResolvedPolicy, decision Decision) Decision {
	if !validApprovalPolicy(policy.Exec.ApprovalPolicy) {
		decision.Outcome = DecisionOutcomeDeny
		decision.DecisionSource = "policy"
		decision.Reason = "exec_approval_policy_invalid"
		return decision
	}
	if !validSecurityMode(policy.Exec.Security) {
		decision.Outcome = DecisionOutcomeDeny
		decision.DecisionSource = "policy"
		decision.Reason = "exec_policy_invalid"
		return decision
	}
	if policy.Exec.Security == SecurityDeny {
		decision.Outcome = DecisionOutcomeDeny
		decision.DecisionSource = "policy"
		decision.Reason = "exec_policy_deny"
		return decision
	}
	command := strings.TrimSpace(input.Request.Metadata["command"])
	if command == "" {
		command = strings.TrimSpace(runtimecore.ApprovalPayloadMetadata(input.Request.Payload)["command"])
	}
	if command == "" {
		if !input.Request.CommandMaterialUnavailable {
			decision.Outcome = DecisionOutcomeDeny
			decision.DecisionSource = "policy"
			decision.Reason = "exec_command_missing"
			return decision
		}
		if len(policy.ForcedHuman.ExecExecutables) > 0 {
			decision.Outcome = DecisionOutcomeDeny
			decision.DecisionSource = "policy"
			decision.Reason = "system_tool_manual_review_not_supported"
			return decision
		}
		decision.Reason = "exec_command_unavailable"
		if policy.Exec.ApprovalPolicy == ApprovalPolicyNever {
			decision.Outcome = DecisionOutcomeApproveOnce
			decision.DecisionSource = "command_execution_no_approval"
			return decision
		}
		if policy.ApprovalPolicy == ApprovalPolicyNever {
			decision.Outcome = DecisionOutcomeApproveOnce
			decision.DecisionSource = "approval_policy_never"
			return decision
		}
		decision.Outcome = DecisionOutcomeDeny
		decision.DecisionSource = "policy"
		return decision
	}
	workingDirectory := firstNonEmpty(
		input.Request.Metadata["approval_working_directory"],
		input.Request.Metadata["working_directory"],
		input.Request.Metadata["cwd"],
		input.Request.WorkspaceRoot,
	)
	roots := append([]string(nil), input.WorkspaceRoots...)
	if strings.TrimSpace(input.Request.WorkspaceRoot) != "" {
		roots = append(roots, input.Request.WorkspaceRoot)
	}
	analysis := AnalyzeExec(ExecRequest{
		Command:          command,
		WorkingDirectory: workingDirectory,
		WorkspaceRoots:   uniqueTrimmed(roots),
		EnvironmentNames: commaSeparated(input.Request.Metadata["approval_environment_names"]),
	}, policy.Exec, policy.ForcedHuman)
	decision.Intent = refineExecActionIntent(input.Request, analysis)
	decision.Reason = analysis.Reason
	if len(analysis.Commands) == 0 {
		decision.Outcome = DecisionOutcomeDeny
		decision.DecisionSource = "policy"
		return decision
	}
	if analysis.ConfiguredForcedHuman {
		decision.Outcome = DecisionOutcomeDeny
		decision.DecisionSource = "policy"
		decision.Reason = "system_tool_manual_review_not_supported"
		return decision
	}
	if len(analysis.MatchedRuleIDs) > 0 {
		decision.RuleID = strings.Join(analysis.MatchedRuleIDs, ",")
	}
	if policy.Exec.ApprovalPolicy == ApprovalPolicyNever {
		decision.Outcome = DecisionOutcomeApproveOnce
		decision.DecisionSource = "command_execution_no_approval"
		return decision
	}
	if policy.ApprovalPolicy == ApprovalPolicyNever {
		decision.Outcome = DecisionOutcomeApproveOnce
		decision.DecisionSource = "approval_policy_never"
		decision.Reason = "approval_policy_never"
		return decision
	}
	if analysis.ForcedHuman {
		decision.Outcome = DecisionOutcomeDeny
		decision.DecisionSource = "policy"
		decision.Reason = "system_tool_manual_review_not_supported"
		return decision
	}
	if analysis.Reason == "exec_policy_deny" {
		decision.Outcome = DecisionOutcomeDeny
		decision.DecisionSource = "policy"
		return decision
	}
	if analysis.Safe {
		decision.Outcome = DecisionOutcomeApproveOnce
		decision.DecisionSource = "allowlist"
		if analysis.Reason == "exec_full" {
			decision.DecisionSource = "full"
		}
		return decision
	}
	if !analysis.Safe {
		decision.Outcome = DecisionOutcomeDeny
		decision.DecisionSource = "policy"
		return decision
	}
	return decision
}

func (service *DecisionService) decideTool(ctx context.Context, input DecisionInput, policy ResolvedPolicy, decision Decision) Decision {
	if strings.TrimSpace(input.Request.ToolName) == "" {
		decision.Outcome = DecisionOutcomeDeny
		decision.DecisionSource = "policy"
		decision.Reason = "tool_id_missing"
		return decision
	}
	if len(input.Request.Payload) > 0 && !json.Valid(input.Request.Payload) {
		decision.Outcome = DecisionOutcomeDeny
		decision.DecisionSource = "policy"
		decision.Reason = "tool_payload_invalid"
		return decision
	}
	if !validSecurityMode(policy.Tools.Security) {
		decision.Outcome = DecisionOutcomeDeny
		decision.DecisionSource = "policy"
		decision.Reason = "tool_policy_invalid"
		return decision
	}
	if policy.Tools.Security == SecurityDeny {
		decision.Outcome = DecisionOutcomeDeny
		decision.DecisionSource = "policy"
		decision.Reason = "tool_policy_deny"
		return decision
	}
	effects := commaSeparated(firstNonEmpty(input.Request.Metadata["approval_effects"], input.Request.Metadata["effects"]))
	risk := firstNonEmpty(input.Request.Metadata["approval_risk"], input.Request.Metadata["risk"])
	if input.Request.Kind == runtimecore.ApprovalKindFileChange && len(effects) == 0 {
		effects = []string{"write"}
	}
	payload := decodedPayload(input.Request.Payload)
	analysis := AnalyzeTool(ToolRequest{
		Kind:           string(input.Request.Kind),
		ToolID:         input.Request.ToolName,
		NativeToolName: input.Request.Metadata["approval_native_tool_name"],
		Effects:        effects,
		Risk:           risk,
		Metadata:       input.Request.Metadata,
		Input:          payload,
	}, policy.Tools, policy.ForcedHuman)
	decision.Intent = refineToolActionIntent(input.Request, analysis)
	decision.Reason = analysis.Reason
	decision.RuleID = analysis.MatchedRuleID
	if decision.Intent.ActionTypeID == actioncontract.ActionBusinessApprovalRequest {
		decision.Outcome = DecisionOutcomeApproveOnce
		decision.DecisionSource = "business_approval_bridge"
		decision.Reason = "business_approval_bridge"
		return decision
	}
	if intentRequiresHuman(decision.Intent) {
		decision.Outcome = DecisionOutcomeHuman
		decision.DecisionSource = "business_decision"
		decision.Reason = "business_decision_required"
		return decision
	}
	if analysis.ConfiguredForcedHuman {
		decision.Outcome = DecisionOutcomeDeny
		decision.DecisionSource = "policy"
		decision.Reason = "system_tool_manual_review_not_supported"
		return decision
	}
	if !analysis.ForcedHuman && authorizedMissionWorkspaceFileChange(input.Request) {
		decision.Outcome = DecisionOutcomeApproveOnce
		decision.DecisionSource = "mission_workspace"
		decision.Reason = "mission_workspace_file_change"
		decision.RuleID = "mission-canonical-workspace-write"
		decision.Intent = ActionIntent{
			ActionTypeID: actioncontract.ActionSystemFileChange,
			Effects:      []string{"write"}, RiskLevel: actioncontract.RiskLow,
			ResourceKind: "workspace_file", Recognized: true, Source: "mission_workspace_boundary",
		}
		return decision
	}
	if policy.ApprovalPolicy == ApprovalPolicyNever {
		decision.Outcome = DecisionOutcomeApproveOnce
		decision.DecisionSource = "approval_policy_never"
		decision.Reason = "approval_policy_never"
		return decision
	}
	if analysis.ForcedHuman {
		decision.Outcome = DecisionOutcomeDeny
		decision.DecisionSource = "policy"
		decision.Reason = "system_tool_manual_review_not_supported"
		return decision
	}
	if analysis.Reason == "tool_policy_deny" {
		decision.Outcome = DecisionOutcomeDeny
		decision.DecisionSource = "policy"
		return decision
	}
	if analysis.Safe {
		if analysis.Reason == "tool_full" || analysis.AutoApprove || policy.ApprovalsReviewer == ApprovalsReviewerHuman {
			decision.Outcome = DecisionOutcomeApproveOnce
			decision.DecisionSource = "allowlist"
			if analysis.Reason == "tool_full" {
				decision.DecisionSource = "full"
			}
			return decision
		}
	}
	if !analysis.Safe {
		decision.Outcome = DecisionOutcomeDeny
		decision.DecisionSource = "policy"
		return decision
	}
	review := service.review(ctx, policy.Reviewer, ReviewInput{
		Kind:             ReviewKindTool,
		LogicalAgentID:   input.LogicalAgentID,
		ConversationKind: input.ConversationKind,
		Reason:           analysis.Reason,
		ToolID:           strings.TrimSpace(input.Request.ToolName),
		MatchedRuleIDs:   uniqueTrimmed([]string{analysis.MatchedRuleID}),
		Deterministic:    analysis.Safe,
		Effects:          analysis.Effects,
		Risk:             analysis.Risk,
	})
	decision.Review = &review
	decision.DecisionSource = "ai_reviewer"
	if review.InputDigest != "" {
		decision.InputDigest = review.InputDigest
	}
	if review.Decision == ReviewDecisionAllow && review.Risk == "low" {
		decision.Outcome = DecisionOutcomeApproveOnce
	} else {
		decision.Outcome = DecisionOutcomeDeny
		if review.ReasonCode != "" {
			decision.Reason = review.ReasonCode
		}
	}
	return decision
}

func authorizedMissionWorkspaceFileChange(request runtimecore.ApprovalRequest) bool {
	if request.Kind != runtimecore.ApprovalKindFileChange || !request.WorkspaceWriteAuthorized || !request.WorkspaceBoundaryEnforced {
		return false
	}
	nativeTool := strings.ToLower(strings.TrimSpace(request.Metadata["approval_native_tool_name"]))
	switch nativeTool {
	case "write", "edit", "patch", "apply_patch", "file_write", "file_edit", "replace", "filechange":
	default:
		return false
	}
	paths := append([]string(nil), request.FileChangePaths...)
	if nativeTool != "filechange" {
		paths = []string{strings.TrimSpace(runtimecore.ApprovalPayloadMetadata(request.Payload)["path"])}
	}
	if len(paths) == 0 {
		return false
	}
	root, err := filepath.Abs(strings.TrimSpace(request.WorkspaceRoot))
	if err != nil || strings.TrimSpace(request.WorkspaceRoot) == "" {
		return false
	}
	workDir, err := filepath.Abs(strings.TrimSpace(request.WorkingDirectory))
	if err != nil || strings.TrimSpace(request.WorkingDirectory) == "" || !pathWithinMissionWorkspace(workDir, root) {
		return false
	}
	roots := []string{workDir}
	if nativeTool == "filechange" && len(request.WorkspaceWriteRoots) > 0 {
		roots = append([]string(nil), request.WorkspaceWriteRoots...)
	}
	for i, value := range roots {
		if strings.TrimSpace(value) == "" || !filepath.IsAbs(value) {
			return false
		}
		allowed, err := filepath.Abs(value)
		if err != nil || !pathWithinMissionWorkspace(allowed, root) {
			return false
		}
		roots[i] = allowed
	}
	for _, value := range paths {
		if strings.TrimSpace(value) == "" {
			return false
		}
		target := value
		if !filepath.IsAbs(target) {
			target = filepath.Join(workDir, target)
		}
		target, err = filepath.Abs(target)
		if err != nil {
			return false
		}
		allowed := false
		for _, writeRoot := range roots {
			if pathWithinMissionWorkspace(target, writeRoot) && missionWorkspaceRealpathAllowed(root, writeRoot, target) {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	return true
}

func missionWorkspaceRealpathAllowed(root, workDir, target string) bool {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	realWorkDir, err := filepath.EvalSymlinks(workDir)
	if err != nil || !pathWithinMissionWorkspace(realWorkDir, realRoot) {
		return false
	}
	existing := target
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return false
		}
		parent := filepath.Dir(existing)
		if parent == existing || !pathWithinMissionWorkspace(parent, workDir) {
			return false
		}
		existing = parent
	}
	realExisting, err := filepath.EvalSymlinks(existing)
	return err == nil && pathWithinMissionWorkspace(realExisting, realWorkDir)
}

func pathWithinMissionWorkspace(path, root string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func intentRequiresHuman(intent ActionIntent) bool {
	return actioncontract.IsHumanBusinessDecisionAction(intent.ActionTypeID)
}

func (service *DecisionService) review(ctx context.Context, settings ReviewerSettings, input ReviewInput) ReviewResult {
	if service == nil || service.reviewer == nil {
		return ReviewResult{
			Decision:      ReviewDecisionAsk,
			Risk:          "unknown",
			ReasonCode:    "reviewer_not_configured",
			ModelProfile:  strings.TrimSpace(settings.LLMProfileID),
			PromptVersion: ReviewerPromptVersion,
		}
	}
	return service.reviewer.Review(ctx, settings, input)
}

func approvalInputDigest(request runtimecore.ApprovalRequest) string {
	payload := map[string]any{
		"kind":                         request.Kind,
		"tool_id":                      strings.TrimSpace(request.ToolName),
		"command":                      strings.TrimSpace(request.Metadata["command"]),
		"command_material_unavailable": request.CommandMaterialUnavailable,
		"workspace_boundary_enforced":  request.WorkspaceBoundaryEnforced,
		"workspace_write_authorized":   request.WorkspaceWriteAuthorized,
		"workspace_root":               request.WorkspaceRoot,
		"workspace_write_roots":        request.WorkspaceWriteRoots,
		"file_change_paths":            request.FileChangePaths,
		"metadata":                     SanitizeReviewInput(request.Metadata),
		"input":                        SanitizeReviewInput(decodedPayload(request.Payload)),
	}
	encoded, _ := json.Marshal(payload)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func decodedPayload(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return map[string]any{"_invalid_json": true}
	}
	return payload
}

func commaSeparated(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return uniqueTrimmed(strings.Split(value, ","))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
