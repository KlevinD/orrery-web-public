package runtimeapproval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

type HILMode string

const (
	HILModeManual HILMode = "manual"
	HILModeAuto   HILMode = "auto"
)

type ApprovalPolicy string

const (
	ApprovalPolicyOnRequest ApprovalPolicy = "on-request"
	ApprovalPolicyNever     ApprovalPolicy = "never"
)

type ApprovalsReviewer string

const (
	ApprovalsReviewerHuman ApprovalsReviewer = "human"
	ApprovalsReviewerAI    ApprovalsReviewer = "ai"
)

type SecurityMode string

const (
	SecurityDeny      SecurityMode = "deny"
	SecurityAllowlist SecurityMode = "allowlist"
	SecurityFull      SecurityMode = "full"
)

type ReviewerSettings struct {
	LLMProfileID string `json:"llm_profile_id,omitempty"`
	TimeoutMS    int    `json:"timeout_ms"`
	MaxTokens    int    `json:"max_tokens"`
}

type ExecRule struct {
	RuleID        string   `json:"rule_id,omitempty"`
	Executable    string   `json:"executable"`
	Subcommands   []string `json:"subcommands,omitempty"`
	SafeFlags     []string `json:"safe_flags,omitempty"`
	WorkspaceOnly bool     `json:"workspace_only,omitempty"`
}

type ExecPolicy struct {
	ApprovalPolicy ApprovalPolicy `json:"approval_policy"`
	Security       SecurityMode   `json:"security"`
	Allowlist      []ExecRule     `json:"allowlist"`
}

type ToolRule struct {
	RuleID      string   `json:"rule_id,omitempty"`
	ToolID      string   `json:"tool_id"`
	Effects     []string `json:"effects,omitempty"`
	Risk        string   `json:"risk,omitempty"`
	AutoApprove bool     `json:"auto_approve,omitempty"`
}

type ToolPolicy struct {
	Security  SecurityMode `json:"security"`
	Allowlist []ToolRule   `json:"allowlist"`
}

type ForcedHumanPolicy struct {
	ToolIDs         []string `json:"tool_ids"`
	ExecExecutables []string `json:"exec_executables"`
}

type PolicyOverride struct {
	HILMode           HILMode           `json:"hil_mode,omitempty"`
	ApprovalPolicy    ApprovalPolicy    `json:"approval_policy,omitempty"`
	ApprovalsReviewer ApprovalsReviewer `json:"approvals_reviewer,omitempty"`
	ApprovalBypass    *bool             `json:"approval_bypass,omitempty"`
	Exec              *ExecPolicy       `json:"exec,omitempty"`
	Tools             *ToolPolicy       `json:"tools,omitempty"`
}

type AgentOverride struct {
	LogicalAgentID string `json:"logical_agent_id"`
	PolicyOverride
}

type Policy struct {
	DefaultHILMode           HILMode                   `json:"default_hil_mode"`
	DefaultApprovalPolicy    ApprovalPolicy            `json:"default_approval_policy"`
	DefaultApprovalsReviewer ApprovalsReviewer         `json:"default_approvals_reviewer"`
	Reviewer                 ReviewerSettings          `json:"reviewer"`
	Exec                     ExecPolicy                `json:"exec"`
	Tools                    ToolPolicy                `json:"tools"`
	ForcedHuman              ForcedHumanPolicy         `json:"forced_human"`
	ConversationDefaults     map[string]PolicyOverride `json:"conversation_defaults"`
	AgentOverrides           []AgentOverride           `json:"agent_overrides"`
	humanReviewDisabled      bool
}

type ResolveInput struct {
	LogicalAgentID                     string
	ConversationKind                   string
	ExplicitConversationHIL            string
	ExplicitConversationApprovalPolicy string
}

type ResolutionSource struct {
	HILMode           string `json:"hil_mode"`
	ApprovalPolicy    string `json:"approval_policy"`
	ApprovalsReviewer string `json:"approvals_reviewer"`
	ApprovalBypass    string `json:"approval_bypass,omitempty"`
	Exec              string `json:"exec"`
	Tools             string `json:"tools"`
}

type ResolvedPolicy struct {
	HILMode           HILMode
	ApprovalPolicy    ApprovalPolicy
	ApprovalsReviewer ApprovalsReviewer
	ApprovalBypass    bool
	Reviewer          ReviewerSettings
	Exec              ExecPolicy
	Tools             ToolPolicy
	ForcedHuman       ForcedHumanPolicy
	Source            ResolutionSource
	Digest            string
}

func DefaultPolicy() Policy {
	personalExec := defaultPersonalAssistantExecPolicy()
	personalTools := defaultPersonalAssistantToolPolicy()
	return Policy{
		DefaultHILMode:           HILModeManual,
		DefaultApprovalPolicy:    ApprovalPolicyOnRequest,
		DefaultApprovalsReviewer: ApprovalsReviewerHuman,
		Reviewer: ReviewerSettings{
			TimeoutMS: 30000,
			MaxTokens: 360,
		},
		Exec: ExecPolicy{
			ApprovalPolicy: ApprovalPolicyNever,
			Security:       SecurityAllowlist,
			Allowlist:      []ExecRule{},
		},
		Tools: ToolPolicy{Security: SecurityAllowlist, Allowlist: []ToolRule{}},
		ForcedHuman: ForcedHumanPolicy{
			ToolIDs:         []string{},
			ExecExecutables: []string{},
		},
		ConversationDefaults: map[string]PolicyOverride{
			"personal_assistant": {
				HILMode:           HILModeAuto,
				ApprovalsReviewer: ApprovalsReviewerAI,
				Exec:              &personalExec,
				Tools:             &personalTools,
			},
		},
		AgentOverrides: []AgentOverride{},
	}
}

// WithoutHumanReview returns a policy that never pauses a valid runtime action
// for human approval. Authentication, tool binding, sandbox, and explicit deny
// checks remain owned by their existing enforcement layers.
func WithoutHumanReview(policy Policy) Policy {
	policy = Normalize(policy)
	policy.humanReviewDisabled = true
	policy.DefaultHILMode = HILModeAuto
	policy.DefaultApprovalPolicy = ApprovalPolicyNever
	policy.Exec.ApprovalPolicy = ApprovalPolicyNever
	policy.ForcedHuman = ForcedHumanPolicy{ToolIDs: []string{}, ExecExecutables: []string{}}
	for kind, override := range policy.ConversationDefaults {
		override.HILMode = HILModeAuto
		override.ApprovalPolicy = ApprovalPolicyNever
		override.ApprovalBypass = nil
		if override.Exec != nil {
			override.Exec.ApprovalPolicy = ApprovalPolicyNever
		}
		policy.ConversationDefaults[kind] = override
	}
	for index := range policy.AgentOverrides {
		policy.AgentOverrides[index].HILMode = HILModeAuto
		policy.AgentOverrides[index].ApprovalPolicy = ApprovalPolicyNever
		policy.AgentOverrides[index].ApprovalBypass = nil
		if policy.AgentOverrides[index].Exec != nil {
			policy.AgentOverrides[index].Exec.ApprovalPolicy = ApprovalPolicyNever
		}
	}
	return policy
}

func Normalize(policy Policy) Policy {
	defaults := DefaultPolicy()
	policy.DefaultHILMode = HILMode(strings.TrimSpace(string(policy.DefaultHILMode)))
	if policy.DefaultHILMode == "" {
		policy.DefaultHILMode = defaults.DefaultHILMode
	}
	policy.DefaultApprovalPolicy = ApprovalPolicy(strings.TrimSpace(string(policy.DefaultApprovalPolicy)))
	if policy.DefaultApprovalPolicy == "" {
		policy.DefaultApprovalPolicy = ApprovalPolicyOnRequest
	}
	policy.DefaultApprovalsReviewer = ApprovalsReviewer(strings.TrimSpace(string(policy.DefaultApprovalsReviewer)))
	if policy.DefaultApprovalsReviewer == "" {
		policy.DefaultApprovalsReviewer = reviewerFromHIL(policy.DefaultHILMode)
	}
	policy.Reviewer.LLMProfileID = strings.TrimSpace(policy.Reviewer.LLMProfileID)
	if policy.Reviewer.TimeoutMS == 0 {
		policy.Reviewer.TimeoutMS = defaults.Reviewer.TimeoutMS
	}
	if policy.Reviewer.MaxTokens == 0 {
		policy.Reviewer.MaxTokens = defaults.Reviewer.MaxTokens
	}
	policy.Exec = normalizeExecPolicy(policy.Exec)
	policy.Tools = normalizeToolPolicy(policy.Tools)
	policy.ForcedHuman.ToolIDs = uniqueTrimmed(policy.ForcedHuman.ToolIDs)
	policy.ForcedHuman.ExecExecutables = uniqueTrimmed(policy.ForcedHuman.ExecExecutables)

	conversationDefaults := make(map[string]PolicyOverride, len(policy.ConversationDefaults)+1)
	for kind, override := range policy.ConversationDefaults {
		kind = strings.TrimSpace(kind)
		if kind == "" {
			continue
		}
		conversationDefaults[kind] = normalizeOverride(override)
	}
	personal := conversationDefaults["personal_assistant"]
	if personal.HILMode == "" {
		personal.HILMode = HILModeAuto
	}
	if personal.ApprovalsReviewer == "" {
		personal.ApprovalsReviewer = reviewerFromHIL(personal.HILMode)
	}
	defaultPersonal := defaults.ConversationDefaults["personal_assistant"]
	if personal.Exec == nil {
		exec := cloneExecPolicy(*defaultPersonal.Exec)
		personal.Exec = &exec
	}
	if personal.Tools == nil {
		tools := cloneToolPolicy(*defaultPersonal.Tools)
		personal.Tools = &tools
	} else if isLegacyPersonalAssistantToolPolicy(*personal.Tools) {
		tools := cloneToolPolicy(*defaultPersonal.Tools)
		personal.Tools = &tools
	}
	conversationDefaults["personal_assistant"] = personal
	policy.ConversationDefaults = conversationDefaults

	for i := range policy.AgentOverrides {
		policy.AgentOverrides[i].LogicalAgentID = strings.TrimSpace(policy.AgentOverrides[i].LogicalAgentID)
		policy.AgentOverrides[i].PolicyOverride = normalizeOverride(policy.AgentOverrides[i].PolicyOverride)
	}
	if policy.AgentOverrides == nil {
		policy.AgentOverrides = []AgentOverride{}
	}
	return policy
}

func defaultPersonalAssistantExecPolicy() ExecPolicy {
	return normalizeExecPolicy(ExecPolicy{Security: SecurityAllowlist, Allowlist: []ExecRule{
		{RuleID: "pa-pwd", Executable: "pwd", WorkspaceOnly: true},
		{RuleID: "pa-ls", Executable: "ls", SafeFlags: []string{"-a", "-l", "-h", "-al", "-la", "-lh", "-ahl", "-alh", "-hal", "-hla", "-lah", "-lha"}, WorkspaceOnly: true},
		{RuleID: "pa-rg", Executable: "rg", SafeFlags: []string{"-n", "--line-number", "-i", "--ignore-case", "-S", "--smart-case", "-l", "--files-with-matches", "--files", "-g", "--glob", "--hidden", "--no-ignore", "-c", "--count", "--stats", "--json", "-A", "-B", "-C", "--after-context", "--before-context", "--context", "-t", "--type"}, WorkspaceOnly: true},
		{RuleID: "pa-grep", Executable: "grep", SafeFlags: []string{"-n", "-i", "-r", "-R", "-l", "-E", "-F", "-A", "-B", "-C", "--after-context", "--before-context", "--context"}, WorkspaceOnly: true},
		{RuleID: "pa-find", Executable: "find", SafeFlags: []string{"-maxdepth", "-mindepth", "-type", "-name", "-iname", "-path", "-ipath", "-print", "-print0", "-size", "-mtime", "-newer", "-a", "-o", "-not"}, WorkspaceOnly: true},
		{RuleID: "pa-head", Executable: "head", SafeFlags: []string{"-n", "-c", "--lines", "--bytes"}, WorkspaceOnly: true},
		{RuleID: "pa-tail", Executable: "tail", SafeFlags: []string{"-n", "-c", "--lines", "--bytes"}, WorkspaceOnly: true},
		{RuleID: "pa-wc", Executable: "wc", SafeFlags: []string{"-l", "-w", "-c", "-m", "--lines", "--words", "--bytes", "--chars"}, WorkspaceOnly: true},
		{RuleID: "pa-sed", Executable: "sed", SafeFlags: []string{"-n", "-E", "-e", "--quiet", "--silent", "--regexp-extended", "--expression"}, WorkspaceOnly: true},
		{RuleID: "pa-git-read", Executable: "git", Subcommands: []string{"status", "diff", "log", "show"}, SafeFlags: []string{"--no-pager", "--short", "--porcelain", "--stat", "--name-only", "--name-status", "--oneline", "--decorate", "--graph", "-s", "-p", "-U", "--unified"}, WorkspaceOnly: true},
	}})
}

func defaultPersonalAssistantToolPolicy() ToolPolicy {
	return normalizeToolPolicy(ToolPolicy{Security: SecurityAllowlist, Allowlist: []ToolRule{
		{RuleID: "pa-discussion-talent-list", ToolID: "discussion_talent_list", Effects: []string{"read"}, Risk: "low", AutoApprove: true},
		{RuleID: "pa-memory-search", ToolID: "memory_search", Effects: []string{"read"}, Risk: "low", AutoApprove: true},
		{RuleID: "pa-knowledge-search", ToolID: "knowledge_search", Effects: []string{"read"}, Risk: "low", AutoApprove: true},
		{RuleID: "pa-enterprise-graph-context", ToolID: "enterprise_graph.build_context_bundle", Effects: []string{"read"}, Risk: "low", AutoApprove: true},
		{RuleID: "pa-enterprise-graph-search", ToolID: "enterprise_graph.search_objects", Effects: []string{"read"}, Risk: "low", AutoApprove: true},
		{RuleID: "pa-web-search", ToolID: "web_search", Effects: []string{"read"}, Risk: "low", AutoApprove: true},
		{RuleID: "pa-web-fetch", ToolID: "web_fetch", Effects: []string{"read"}, Risk: "low", AutoApprove: true},
		{RuleID: "pa-server-browser-navigate", ToolID: "mcp_server_browser_navigate_page", Effects: []string{"read"}, Risk: "low", AutoApprove: true},
		{RuleID: "pa-server-browser-screenshot", ToolID: "mcp_server_browser_take_screenshot", Effects: []string{"read"}, Risk: "low", AutoApprove: true},
	}})
}

func isLegacyPersonalAssistantToolPolicy(policy ToolPolicy) bool {
	policy = normalizeToolPolicy(policy)
	if policy.Security != SecurityAllowlist {
		return false
	}
	expected := map[string]string{
		"discussion_talent_list":                "pa-discussion-talent-list",
		"memory_search":                         "pa-memory-search",
		"knowledge_search":                      "pa-knowledge-search",
		"enterprise_graph.build_context_bundle": "pa-enterprise-graph-context",
		"enterprise_graph.search_objects":       "pa-enterprise-graph-search",
		"web_search":                            "pa-web-search",
		"web_fetch":                             "pa-web-fetch",
		"mcp_server_browser_navigate_page":      "pa-server-browser-navigate",
		"mcp_server_browser_take_screenshot":    "pa-server-browser-screenshot",
	}
	legacyRuleCounts := map[int]bool{1: true, 4: true, 6: true, 7: true, len(expected): true}
	if !legacyRuleCounts[len(policy.Allowlist)] {
		return false
	}
	seen := map[string]bool{}
	allAutoApprove := true
	for _, rule := range policy.Allowlist {
		if expected[rule.ToolID] != rule.RuleID || len(rule.Effects) != 1 || rule.Effects[0] != "read" || rule.Risk != "low" || seen[rule.ToolID] {
			return false
		}
		seen[rule.ToolID] = true
		allAutoApprove = allAutoApprove && rule.AutoApprove
	}
	required := []string{"discussion_talent_list"}
	switch len(policy.Allowlist) {
	case 4:
		required = append(required, "memory_search", "knowledge_search", "enterprise_graph.build_context_bundle")
	case 6:
		required = append(required, "memory_search", "knowledge_search", "enterprise_graph.build_context_bundle", "web_search", "web_fetch")
	case 7:
		required = append(required, "memory_search", "knowledge_search", "enterprise_graph.build_context_bundle", "web_search", "web_fetch", "enterprise_graph.search_objects")
	case len(expected):
		required = append(required,
			"memory_search", "knowledge_search", "enterprise_graph.build_context_bundle", "web_search", "web_fetch", "enterprise_graph.search_objects",
			"mcp_server_browser_navigate_page", "mcp_server_browser_take_screenshot",
		)
	}
	for _, toolID := range required {
		if !seen[toolID] {
			return false
		}
	}
	return len(policy.Allowlist) != len(expected) || !allAutoApprove
}

func Clone(policy Policy) Policy {
	out := policy
	out.Exec = cloneExecPolicy(policy.Exec)
	out.Tools = cloneToolPolicy(policy.Tools)
	out.ForcedHuman.ToolIDs = append([]string(nil), policy.ForcedHuman.ToolIDs...)
	out.ForcedHuman.ExecExecutables = append([]string(nil), policy.ForcedHuman.ExecExecutables...)
	out.ConversationDefaults = make(map[string]PolicyOverride, len(policy.ConversationDefaults))
	for key, override := range policy.ConversationDefaults {
		out.ConversationDefaults[key] = cloneOverride(override)
	}
	out.AgentOverrides = make([]AgentOverride, len(policy.AgentOverrides))
	for i, override := range policy.AgentOverrides {
		out.AgentOverrides[i] = AgentOverride{
			LogicalAgentID: override.LogicalAgentID,
			PolicyOverride: cloneOverride(override.PolicyOverride),
		}
	}
	return out
}

func (policy Policy) Resolve(input ResolveInput) ResolvedPolicy {
	policy = Normalize(policy)
	resolved := ResolvedPolicy{
		HILMode:           policy.DefaultHILMode,
		ApprovalPolicy:    policy.DefaultApprovalPolicy,
		ApprovalsReviewer: policy.DefaultApprovalsReviewer,
		Reviewer:          policy.Reviewer,
		Exec:              cloneExecPolicy(policy.Exec),
		Tools:             cloneToolPolicy(policy.Tools),
		ForcedHuman:       policy.ForcedHuman,
		Source: ResolutionSource{
			HILMode:           "platform_default",
			ApprovalPolicy:    "platform_default",
			ApprovalsReviewer: "platform_default",
			Exec:              "platform_default",
			Tools:             "platform_default",
		},
	}
	conversationKind := strings.TrimSpace(input.ConversationKind)
	if override, ok := policy.ConversationDefaults[conversationKind]; ok {
		applyOverride(&resolved, override, "conversation_kind:"+conversationKind)
	}
	logicalAgentID := strings.TrimSpace(input.LogicalAgentID)
	for _, override := range policy.AgentOverrides {
		if override.LogicalAgentID == logicalAgentID && logicalAgentID != "" {
			applyOverride(&resolved, override.PolicyOverride, "logical_agent:"+logicalAgentID)
			break
		}
	}
	if !policy.humanReviewDisabled {
		explicitHIL := HILMode(strings.TrimSpace(input.ExplicitConversationHIL))
		if explicitHIL == HILModeManual || explicitHIL == HILModeAuto {
			resolved.HILMode = explicitHIL
			resolved.ApprovalsReviewer = reviewerFromHIL(explicitHIL)
			resolved.Source.HILMode = "conversation_explicit"
			resolved.Source.ApprovalsReviewer = "conversation_explicit"
			if explicitHIL == HILModeManual {
				resolved.ApprovalPolicy = ApprovalPolicyOnRequest
				resolved.ApprovalBypass = false
				resolved.Source.ApprovalPolicy = "conversation_explicit"
			}
		}
		explicitApprovalPolicy := ApprovalPolicy(strings.TrimSpace(input.ExplicitConversationApprovalPolicy))
		if validApprovalPolicy(explicitApprovalPolicy) {
			resolved.ApprovalPolicy = explicitApprovalPolicy
			resolved.Source.ApprovalPolicy = "conversation_explicit"
		}
	}
	resolved.Digest = policyDigest(resolved)
	return resolved
}

func Validate(policy Policy) (string, string, bool) {
	if policy.DefaultHILMode != HILModeManual && policy.DefaultHILMode != HILModeAuto {
		return "default_hil_mode", "default HIL mode must be manual or auto", false
	}
	if !validApprovalPolicy(policy.DefaultApprovalPolicy) {
		return "default_approval_policy", "default approval policy must be on-request or never", false
	}
	if !validApprovalsReviewer(policy.DefaultApprovalsReviewer) {
		return "default_approvals_reviewer", "default approvals reviewer must be human or ai", false
	}
	if policy.Reviewer.TimeoutMS < 1000 || policy.Reviewer.TimeoutMS > 120000 {
		return "reviewer.timeout_ms", "reviewer timeout must be between 1000 and 120000 milliseconds", false
	}
	if policy.Reviewer.MaxTokens < 64 || policy.Reviewer.MaxTokens > 1024 {
		return "reviewer.max_tokens", "reviewer max tokens must be between 64 and 1024", false
	}
	if field, message, ok := validateExecPolicy("exec", policy.Exec); !ok {
		return field, message, false
	}
	if field, message, ok := validateToolPolicy("tools", policy.Tools); !ok {
		return field, message, false
	}
	for i, toolID := range policy.ForcedHuman.ToolIDs {
		if !validStableID(toolID) {
			return indexedField("forced_human.tool_ids", i), "forced-human tool id must be an exact stable id", false
		}
	}
	for i, executable := range policy.ForcedHuman.ExecExecutables {
		if !validExecutable(executable) {
			return indexedField("forced_human.exec_executables", i), "forced-human executable must be an exact executable name", false
		}
	}
	conversationKinds := make([]string, 0, len(policy.ConversationDefaults))
	for kind := range policy.ConversationDefaults {
		conversationKinds = append(conversationKinds, kind)
	}
	sort.Strings(conversationKinds)
	for _, kind := range conversationKinds {
		if policy.ConversationDefaults[kind].ApprovalBypass != nil {
			return "conversation_defaults." + kind + ".approval_bypass", "approval bypass is only allowed for a logical agent override", false
		}
		if field, message, ok := validateOverride("conversation_defaults."+kind, policy.ConversationDefaults[kind]); !ok {
			return field, message, false
		}
	}
	seenAgents := map[string]bool{}
	for i, override := range policy.AgentOverrides {
		prefix := indexedField("agent_overrides", i)
		if !validStableID(override.LogicalAgentID) {
			return prefix + ".logical_agent_id", "logical agent id must be an exact stable id", false
		}
		if seenAgents[override.LogicalAgentID] {
			return prefix + ".logical_agent_id", "logical agent id must be unique", false
		}
		seenAgents[override.LogicalAgentID] = true
		if field, message, ok := validateOverride(prefix, override.PolicyOverride); !ok {
			return field, message, false
		}
	}
	return "", "", true
}

func ContainsNeverPolicy(policy Policy) bool {
	if policy.DefaultApprovalPolicy == ApprovalPolicyNever {
		return true
	}
	for _, override := range policy.ConversationDefaults {
		if overrideContainsNeverPolicy(override) {
			return true
		}
	}
	for _, override := range policy.AgentOverrides {
		if overrideContainsNeverPolicy(override.PolicyOverride) {
			return true
		}
	}
	return false
}

func overrideContainsNeverPolicy(override PolicyOverride) bool {
	return override.ApprovalPolicy == ApprovalPolicyNever ||
		(override.ApprovalBypass != nil && *override.ApprovalBypass)
}

func normalizeOverride(override PolicyOverride) PolicyOverride {
	override.HILMode = HILMode(strings.TrimSpace(string(override.HILMode)))
	override.ApprovalPolicy = ApprovalPolicy(strings.TrimSpace(string(override.ApprovalPolicy)))
	override.ApprovalsReviewer = ApprovalsReviewer(strings.TrimSpace(string(override.ApprovalsReviewer)))
	if override.ApprovalsReviewer == "" && override.HILMode != "" {
		override.ApprovalsReviewer = reviewerFromHIL(override.HILMode)
	}
	if override.Exec != nil {
		exec := normalizeExecPolicy(*override.Exec)
		override.Exec = &exec
	}
	if override.Tools != nil {
		tools := normalizeToolPolicy(*override.Tools)
		override.Tools = &tools
	}
	return override
}

func normalizeExecPolicy(policy ExecPolicy) ExecPolicy {
	policy.ApprovalPolicy = ApprovalPolicy(strings.TrimSpace(string(policy.ApprovalPolicy)))
	if policy.ApprovalPolicy == "" {
		policy.ApprovalPolicy = ApprovalPolicyNever
	}
	policy.Security = SecurityMode(strings.TrimSpace(string(policy.Security)))
	if policy.Security == "" {
		policy.Security = SecurityAllowlist
	}
	out := make([]ExecRule, 0, len(policy.Allowlist))
	seen := map[string]bool{}
	for _, rule := range policy.Allowlist {
		rule.RuleID = strings.TrimSpace(rule.RuleID)
		rule.Executable = strings.TrimSpace(rule.Executable)
		rule.Subcommands = uniqueTrimmed(rule.Subcommands)
		rule.SafeFlags = uniqueTrimmed(rule.SafeFlags)
		keyBytes, _ := json.Marshal(struct {
			Executable    string
			Subcommands   []string
			SafeFlags     []string
			WorkspaceOnly bool
		}{rule.Executable, rule.Subcommands, rule.SafeFlags, rule.WorkspaceOnly})
		key := string(keyBytes)
		if seen[key] {
			continue
		}
		seen[key] = true
		if rule.RuleID == "" && rule.Executable != "" {
			rule.RuleID = "exec:" + shortDigest(keyBytes)
		}
		out = append(out, rule)
	}
	policy.Allowlist = out
	if policy.Allowlist == nil {
		policy.Allowlist = []ExecRule{}
	}
	return policy
}

func normalizeToolPolicy(policy ToolPolicy) ToolPolicy {
	policy.Security = SecurityMode(strings.TrimSpace(string(policy.Security)))
	if policy.Security == "" {
		policy.Security = SecurityAllowlist
	}
	out := make([]ToolRule, 0, len(policy.Allowlist))
	seen := map[string]bool{}
	for _, rule := range policy.Allowlist {
		rule.RuleID = strings.TrimSpace(rule.RuleID)
		rule.ToolID = strings.TrimSpace(rule.ToolID)
		rule.Effects = uniqueTrimmed(rule.Effects)
		rule.Risk = strings.TrimSpace(rule.Risk)
		keyBytes, _ := json.Marshal(struct {
			ToolID  string
			Effects []string
			Risk    string
		}{rule.ToolID, rule.Effects, rule.Risk})
		key := string(keyBytes)
		if seen[key] {
			continue
		}
		seen[key] = true
		if rule.RuleID == "" && rule.ToolID != "" {
			rule.RuleID = "tool:" + shortDigest(keyBytes)
		}
		out = append(out, rule)
	}
	policy.Allowlist = out
	if policy.Allowlist == nil {
		policy.Allowlist = []ToolRule{}
	}
	return policy
}

func applyOverride(resolved *ResolvedPolicy, override PolicyOverride, source string) {
	if override.HILMode != "" {
		resolved.HILMode = override.HILMode
		resolved.Source.HILMode = source
	}
	if override.ApprovalBypass != nil {
		resolved.ApprovalBypass = *override.ApprovalBypass
		resolved.Source.ApprovalBypass = source
		if *override.ApprovalBypass && override.ApprovalPolicy == "" {
			resolved.ApprovalPolicy = ApprovalPolicyNever
			resolved.Source.ApprovalPolicy = source
		}
	}
	if override.ApprovalPolicy != "" {
		resolved.ApprovalPolicy = override.ApprovalPolicy
		resolved.Source.ApprovalPolicy = source
	}
	if override.ApprovalsReviewer != "" {
		resolved.ApprovalsReviewer = override.ApprovalsReviewer
		resolved.Source.ApprovalsReviewer = source
	}
	if override.Exec != nil {
		resolved.Exec = cloneExecPolicy(*override.Exec)
		resolved.Source.Exec = source
	}
	if override.Tools != nil {
		resolved.Tools = cloneToolPolicy(*override.Tools)
		resolved.Source.Tools = source
	}
}

func policyDigest(policy ResolvedPolicy) string {
	payload, _ := json.Marshal(struct {
		HILMode           HILMode
		ApprovalPolicy    ApprovalPolicy
		ApprovalsReviewer ApprovalsReviewer
		ApprovalBypass    bool
		Reviewer          ReviewerSettings
		Exec              ExecPolicy
		Tools             ToolPolicy
		ForcedHuman       ForcedHumanPolicy
	}{policy.HILMode, policy.ApprovalPolicy, policy.ApprovalsReviewer, policy.ApprovalBypass, policy.Reviewer, policy.Exec, policy.Tools, policy.ForcedHuman})
	return shortDigest(payload)
}

func shortDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func cloneExecPolicy(policy ExecPolicy) ExecPolicy {
	out := policy
	out.Allowlist = make([]ExecRule, len(policy.Allowlist))
	for i, rule := range policy.Allowlist {
		rule.Subcommands = append([]string(nil), rule.Subcommands...)
		rule.SafeFlags = append([]string(nil), rule.SafeFlags...)
		out.Allowlist[i] = rule
	}
	return out
}

func cloneToolPolicy(policy ToolPolicy) ToolPolicy {
	out := policy
	out.Allowlist = make([]ToolRule, len(policy.Allowlist))
	for i, rule := range policy.Allowlist {
		rule.Effects = append([]string(nil), rule.Effects...)
		out.Allowlist[i] = rule
	}
	return out
}

func cloneOverride(override PolicyOverride) PolicyOverride {
	out := override
	if override.ApprovalBypass != nil {
		value := *override.ApprovalBypass
		out.ApprovalBypass = &value
	}
	if override.Exec != nil {
		exec := cloneExecPolicy(*override.Exec)
		out.Exec = &exec
	}
	if override.Tools != nil {
		tools := cloneToolPolicy(*override.Tools)
		out.Tools = &tools
	}
	return out
}

func validateOverride(prefix string, override PolicyOverride) (string, string, bool) {
	if override.HILMode != "" && override.HILMode != HILModeManual && override.HILMode != HILModeAuto {
		return prefix + ".hil_mode", "HIL mode must be manual or auto", false
	}
	if override.ApprovalPolicy != "" && override.ApprovalBypass != nil {
		return prefix + ".approval_policy", "approval policy conflicts with legacy approval bypass", false
	}
	if override.ApprovalPolicy != "" && !validApprovalPolicy(override.ApprovalPolicy) {
		return prefix + ".approval_policy", "approval policy must be on-request or never", false
	}
	if override.ApprovalsReviewer != "" && !validApprovalsReviewer(override.ApprovalsReviewer) {
		return prefix + ".approvals_reviewer", "approvals reviewer must be human or ai", false
	}
	if override.Exec != nil {
		if field, message, ok := validateExecPolicy(prefix+".exec", *override.Exec); !ok {
			return field, message, false
		}
	}
	if override.Tools != nil {
		if field, message, ok := validateToolPolicy(prefix+".tools", *override.Tools); !ok {
			return field, message, false
		}
	}
	return "", "", true
}

func validateExecPolicy(prefix string, policy ExecPolicy) (string, string, bool) {
	if !validApprovalPolicy(policy.ApprovalPolicy) {
		return prefix + ".approval_policy", "approval policy must be on-request or never", false
	}
	if !validSecurityMode(policy.Security) {
		return prefix + ".security", "security must be deny, allowlist, or full", false
	}
	for i, rule := range policy.Allowlist {
		rulePrefix := indexedField(prefix+".allowlist", i)
		if !validExecutable(rule.Executable) {
			return rulePrefix + ".executable", "executable must be an exact executable name", false
		}
		for j, flag := range rule.SafeFlags {
			if !strings.HasPrefix(flag, "-") || strings.ContainsAny(flag, " \t\r\n") {
				return indexedField(rulePrefix+".safe_flags", j), "safe flag must be one exact flag", false
			}
		}
	}
	return "", "", true
}

func validateToolPolicy(prefix string, policy ToolPolicy) (string, string, bool) {
	if !validSecurityMode(policy.Security) {
		return prefix + ".security", "security must be deny, allowlist, or full", false
	}
	validEffects := map[string]bool{"read": true, "write": true, "network": true, "delete": true, "publish": true, "permission": true, "identity": true, "credential": true, "financial": true, "secret": true, "unknown": true}
	validRisk := map[string]bool{"": true, "low": true, "medium": true, "high": true, "critical": true, "unknown": true}
	for i, rule := range policy.Allowlist {
		rulePrefix := indexedField(prefix+".allowlist", i)
		if !validStableID(rule.ToolID) {
			return rulePrefix + ".tool_id", "tool id must be an exact stable id", false
		}
		for j, effect := range rule.Effects {
			if !validEffects[effect] {
				return indexedField(rulePrefix+".effects", j), "tool effect is not supported", false
			}
		}
		if !validRisk[rule.Risk] {
			return rulePrefix + ".risk", "tool risk is not supported", false
		}
	}
	return "", "", true
}

func validSecurityMode(mode SecurityMode) bool {
	return mode == SecurityDeny || mode == SecurityAllowlist || mode == SecurityFull
}

func validApprovalPolicy(policy ApprovalPolicy) bool {
	return policy == ApprovalPolicyOnRequest || policy == ApprovalPolicyNever
}

func validApprovalsReviewer(reviewer ApprovalsReviewer) bool {
	return reviewer == ApprovalsReviewerHuman || reviewer == ApprovalsReviewerAI
}

func reviewerFromHIL(mode HILMode) ApprovalsReviewer {
	if mode == HILModeAuto {
		return ApprovalsReviewerAI
	}
	return ApprovalsReviewerHuman
}

var exactIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*$`)
var executablePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)

func validStableID(value string) bool {
	return exactIDPattern.MatchString(value)
}

func validExecutable(value string) bool {
	return executablePattern.MatchString(value)
}

func uniqueTrimmed(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	if out == nil {
		return []string{}
	}
	return out
}

func indexedField(prefix string, index int) string {
	const digits = "0123456789"
	if index < 10 {
		return prefix + "[" + string(digits[index]) + "]"
	}
	value, _ := json.Marshal(index)
	return prefix + "[" + string(value) + "]"
}
