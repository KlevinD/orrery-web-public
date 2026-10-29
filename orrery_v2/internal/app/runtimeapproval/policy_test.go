package runtimeapproval

import "testing"

func TestDefaultPolicyUsesManualWithPersonalAssistantAuto(t *testing.T) {
	policy := DefaultPolicy()

	defaultResolved := policy.Resolve(ResolveInput{})
	if defaultResolved.HILMode != HILModeManual || defaultResolved.Source.HILMode != "platform_default" || defaultResolved.ApprovalPolicy != ApprovalPolicyOnRequest || defaultResolved.ApprovalsReviewer != ApprovalsReviewerHuman || defaultResolved.Exec.ApprovalPolicy != ApprovalPolicyNever {
		t.Fatalf("default resolved policy = %+v", defaultResolved)
	}

	personalResolved := policy.Resolve(ResolveInput{ConversationKind: " personal_assistant "})
	if personalResolved.HILMode != HILModeAuto || personalResolved.Source.HILMode != "conversation_kind:personal_assistant" || personalResolved.ApprovalPolicy != ApprovalPolicyOnRequest || personalResolved.ApprovalsReviewer != ApprovalsReviewerAI || personalResolved.Exec.ApprovalPolicy != ApprovalPolicyNever {
		t.Fatalf("personal assistant resolved policy = %+v", personalResolved)
	}
}

func TestExplicitConversationApprovalPolicySupportsNoApprovalMode(t *testing.T) {
	resolved := DefaultPolicy().Resolve(ResolveInput{
		ConversationKind:                   "direct",
		ExplicitConversationHIL:            "auto",
		ExplicitConversationApprovalPolicy: "never",
	})

	if resolved.HILMode != HILModeAuto || resolved.ApprovalPolicy != ApprovalPolicyNever || resolved.ApprovalsReviewer != ApprovalsReviewerAI || resolved.ApprovalBypass {
		t.Fatalf("resolved policy = %+v", resolved)
	}
	if resolved.Source.HILMode != "conversation_explicit" || resolved.Source.ApprovalPolicy != "conversation_explicit" || resolved.Source.ApprovalsReviewer != "conversation_explicit" {
		t.Fatalf("resolved source = %+v", resolved.Source)
	}
}

func TestWithoutHumanReviewOverridesEveryPolicyLayer(t *testing.T) {
	bypass := true
	policy := DefaultPolicy()
	policy.Exec.ApprovalPolicy = ApprovalPolicyOnRequest
	policy.ForcedHuman = ForcedHumanPolicy{ToolIDs: []string{"publish"}, ExecExecutables: []string{"rm"}}
	directExec := ExecPolicy{ApprovalPolicy: ApprovalPolicyOnRequest, Security: SecurityAllowlist}
	policy.ConversationDefaults["direct"] = PolicyOverride{HILMode: HILModeManual, ApprovalPolicy: ApprovalPolicyOnRequest, ApprovalBypass: &bypass, Exec: &directExec}
	agentExec := ExecPolicy{ApprovalPolicy: ApprovalPolicyOnRequest, Security: SecurityAllowlist}
	policy.AgentOverrides = []AgentOverride{{LogicalAgentID: "agent-1", PolicyOverride: PolicyOverride{
		HILMode: HILModeManual, ApprovalPolicy: ApprovalPolicyOnRequest, ApprovalBypass: &bypass, Exec: &agentExec,
	}}}

	disabled := WithoutHumanReview(policy)
	for _, input := range []ResolveInput{{}, {ConversationKind: "direct"}, {LogicalAgentID: "agent-1", ExplicitConversationHIL: "manual"}} {
		resolved := disabled.Resolve(input)
		if resolved.ApprovalPolicy != ApprovalPolicyNever || resolved.Exec.ApprovalPolicy != ApprovalPolicyNever || len(resolved.ForcedHuman.ToolIDs) != 0 || len(resolved.ForcedHuman.ExecExecutables) != 0 {
			t.Fatalf("resolved policy = %+v", resolved)
		}
	}
}

func TestWithoutHumanReviewIgnoresExplicitConversationManualMode(t *testing.T) {
	resolved := WithoutHumanReview(DefaultPolicy()).Resolve(ResolveInput{
		ConversationKind:        "personal_assistant",
		ExplicitConversationHIL: "manual",
	})
	if resolved.HILMode != HILModeAuto || resolved.ApprovalPolicy != ApprovalPolicyNever || resolved.ApprovalsReviewer != ApprovalsReviewerAI {
		t.Fatalf("resolved policy = %+v", resolved)
	}
	if resolved.Source.HILMode == "conversation_explicit" || resolved.Source.ApprovalPolicy == "conversation_explicit" || resolved.Source.ApprovalsReviewer == "conversation_explicit" {
		t.Fatalf("explicit manual mode overrode global no-HIL policy: %+v", resolved.Source)
	}
}

func TestDefaultPolicyAllowsPersonalAssistantContextReads(t *testing.T) {
	resolved := DefaultPolicy().Resolve(ResolveInput{ConversationKind: "personal_assistant"})
	want := map[string]string{
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
	for _, rule := range resolved.Tools.Allowlist {
		if rule.Risk != "low" || len(rule.Effects) != 1 || rule.Effects[0] != "read" || !rule.AutoApprove {
			t.Fatalf("personal assistant read rule = %+v", rule)
		}
		if want[rule.ToolID] == rule.RuleID {
			delete(want, rule.ToolID)
		}
	}
	if len(want) != 0 {
		t.Fatalf("personal assistant context read rules missing: %+v", want)
	}
}

func TestNormalizeUpgradesLegacyPersonalAssistantToolPolicy(t *testing.T) {
	legacyPolicies := map[string]ToolPolicy{
		"single original rule": {Security: SecurityAllowlist, Allowlist: []ToolRule{
			{RuleID: "pa-discussion-talent-list", ToolID: "discussion_talent_list", Effects: []string{"read"}, Risk: "low"},
		}},
		"context rules before auto approve": {Security: SecurityAllowlist, Allowlist: []ToolRule{
			{RuleID: "pa-discussion-talent-list", ToolID: "discussion_talent_list", Effects: []string{"read"}, Risk: "low"},
			{RuleID: "pa-memory-search", ToolID: "memory_search", Effects: []string{"read"}, Risk: "low"},
			{RuleID: "pa-knowledge-search", ToolID: "knowledge_search", Effects: []string{"read"}, Risk: "low"},
			{RuleID: "pa-enterprise-graph-context", ToolID: "enterprise_graph.build_context_bundle", Effects: []string{"read"}, Risk: "low"},
		}},
		"auto-approved context rules before web": {Security: SecurityAllowlist, Allowlist: []ToolRule{
			{RuleID: "pa-discussion-talent-list", ToolID: "discussion_talent_list", Effects: []string{"read"}, Risk: "low", AutoApprove: true},
			{RuleID: "pa-memory-search", ToolID: "memory_search", Effects: []string{"read"}, Risk: "low", AutoApprove: true},
			{RuleID: "pa-knowledge-search", ToolID: "knowledge_search", Effects: []string{"read"}, Risk: "low", AutoApprove: true},
			{RuleID: "pa-enterprise-graph-context", ToolID: "enterprise_graph.build_context_bundle", Effects: []string{"read"}, Risk: "low", AutoApprove: true},
		}},
		"web rules before graph search": {Security: SecurityAllowlist, Allowlist: []ToolRule{
			{RuleID: "pa-discussion-talent-list", ToolID: "discussion_talent_list", Effects: []string{"read"}, Risk: "low", AutoApprove: true},
			{RuleID: "pa-memory-search", ToolID: "memory_search", Effects: []string{"read"}, Risk: "low", AutoApprove: true},
			{RuleID: "pa-knowledge-search", ToolID: "knowledge_search", Effects: []string{"read"}, Risk: "low", AutoApprove: true},
			{RuleID: "pa-enterprise-graph-context", ToolID: "enterprise_graph.build_context_bundle", Effects: []string{"read"}, Risk: "low", AutoApprove: true},
			{RuleID: "pa-web-search", ToolID: "web_search", Effects: []string{"read"}, Risk: "low", AutoApprove: true},
			{RuleID: "pa-web-fetch", ToolID: "web_fetch", Effects: []string{"read"}, Risk: "low", AutoApprove: true},
		}},
	}
	for name, legacy := range legacyPolicies {
		t.Run(name, func(t *testing.T) {
			policy := DefaultPolicy()
			personal := policy.ConversationDefaults["personal_assistant"]
			personal.Tools = &legacy
			policy.ConversationDefaults["personal_assistant"] = personal

			resolved := Normalize(policy).Resolve(ResolveInput{ConversationKind: "personal_assistant"})
			for _, rule := range resolved.Tools.Allowlist {
				if !rule.AutoApprove {
					t.Fatalf("legacy rule was not upgraded: %+v", rule)
				}
			}
			if len(resolved.Tools.Allowlist) != 9 {
				t.Fatalf("legacy policy was not upgraded: %+v", resolved.Tools.Allowlist)
			}
		})
	}
	custom := ToolPolicy{Security: SecurityAllowlist, Allowlist: []ToolRule{{
		RuleID: "custom-memory", ToolID: "memory_search", Effects: []string{"read"}, Risk: "low",
	}}}
	policy := DefaultPolicy()
	personal := policy.ConversationDefaults["personal_assistant"]
	personal.Tools = &custom
	policy.ConversationDefaults["personal_assistant"] = personal
	resolved := Normalize(policy).Resolve(ResolveInput{ConversationKind: "personal_assistant"})
	if len(resolved.Tools.Allowlist) != 1 || resolved.Tools.Allowlist[0].RuleID != "custom-memory" || resolved.Tools.Allowlist[0].AutoApprove {
		t.Fatalf("custom policy was overwritten: %+v", resolved.Tools.Allowlist)
	}
}

func TestResolveApprovalPolicyAndReviewerBySpecificity(t *testing.T) {
	policy := DefaultPolicy()
	policy.DefaultApprovalPolicy = ApprovalPolicyOnRequest
	policy.DefaultApprovalsReviewer = ApprovalsReviewerHuman
	policy.ConversationDefaults["personal_assistant"] = PolicyOverride{
		ApprovalPolicy:    ApprovalPolicyOnRequest,
		ApprovalsReviewer: ApprovalsReviewerAI,
	}
	policy.AgentOverrides = []AgentOverride{{
		LogicalAgentID: "personal-assistant-ding",
		PolicyOverride: PolicyOverride{ApprovalPolicy: ApprovalPolicyNever},
	}}

	got := policy.Resolve(ResolveInput{
		LogicalAgentID:   "personal-assistant-ding",
		ConversationKind: "personal_assistant",
	})
	if got.ApprovalPolicy != ApprovalPolicyNever || got.ApprovalsReviewer != ApprovalsReviewerAI {
		t.Fatalf("resolved policy = %+v", got)
	}
	if got.Source.ApprovalPolicy != "logical_agent:personal-assistant-ding" || got.Source.ApprovalsReviewer != "conversation_kind:personal_assistant" {
		t.Fatalf("resolved sources = %+v", got.Source)
	}
}

func TestResolveLegacyApprovalBypassAsNever(t *testing.T) {
	bypass := true
	policy := DefaultPolicy()
	policy.AgentOverrides = []AgentOverride{{
		LogicalAgentID: "personal-assistant-ding",
		PolicyOverride: PolicyOverride{ApprovalBypass: &bypass},
	}}

	got := policy.Resolve(ResolveInput{LogicalAgentID: "personal-assistant-ding"})
	if got.ApprovalPolicy != ApprovalPolicyNever || !got.ApprovalBypass {
		t.Fatalf("resolved policy = %+v", got)
	}
	if got.Source.ApprovalPolicy != "logical_agent:personal-assistant-ding" {
		t.Fatalf("resolved sources = %+v", got.Source)
	}
}

func TestResolveExplicitApprovalPolicyWinsOverLegacyBypass(t *testing.T) {
	bypass := true
	policy := DefaultPolicy()
	policy.AgentOverrides = []AgentOverride{{
		LogicalAgentID: "personal-assistant-ding",
		PolicyOverride: PolicyOverride{
			ApprovalPolicy: ApprovalPolicyOnRequest,
			ApprovalBypass: &bypass,
		},
	}}

	got := policy.Resolve(ResolveInput{LogicalAgentID: "personal-assistant-ding"})
	if got.ApprovalPolicy != ApprovalPolicyOnRequest {
		t.Fatalf("resolved policy = %+v", got)
	}
}

func TestResolvePrefersLogicalAgentThenConversationKindThenPlatform(t *testing.T) {
	bypass := true
	policy := Normalize(Policy{
		DefaultHILMode: HILModeManual,
		Exec:           ExecPolicy{Security: SecurityAllowlist, Allowlist: []ExecRule{{RuleID: "platform-exec", Executable: "pwd"}}},
		Tools:          ToolPolicy{Security: SecurityAllowlist, Allowlist: []ToolRule{{RuleID: "platform-tool", ToolID: "discussion_talent_list"}}},
		ConversationDefaults: map[string]PolicyOverride{
			"personal_assistant": {
				HILMode: HILModeAuto,
				Exec:    &ExecPolicy{Security: SecurityDeny},
			},
		},
		AgentOverrides: []AgentOverride{{
			LogicalAgentID: "assistant:human-1",
			PolicyOverride: PolicyOverride{
				HILMode:        HILModeManual,
				ApprovalBypass: &bypass,
				Exec:           &ExecPolicy{Security: SecurityFull},
				Tools:          &ToolPolicy{Security: SecurityDeny},
			},
		}},
	})

	got := policy.Resolve(ResolveInput{
		LogicalAgentID:   "assistant:human-1",
		ConversationKind: "personal_assistant",
	})
	if got.HILMode != HILModeManual || !got.ApprovalBypass || got.Exec.Security != SecurityFull || got.Tools.Security != SecurityDeny {
		t.Fatalf("resolved policy = %+v", got)
	}
	if got.Source.HILMode != "logical_agent:assistant:human-1" || got.Source.ApprovalBypass != "logical_agent:assistant:human-1" || got.Source.Exec != "logical_agent:assistant:human-1" || got.Source.Tools != "logical_agent:assistant:human-1" {
		t.Fatalf("resolved sources = %+v", got.Source)
	}
}

func TestValidateRejectsConversationWideApprovalBypass(t *testing.T) {
	bypass := true
	policy := DefaultPolicy()
	personal := policy.ConversationDefaults["personal_assistant"]
	personal.ApprovalBypass = &bypass
	policy.ConversationDefaults["personal_assistant"] = personal

	field, _, ok := Validate(policy)
	if ok || field != "conversation_defaults.personal_assistant.approval_bypass" {
		t.Fatalf("validation = field %q ok %v", field, ok)
	}
}

func TestValidateRejectsConflictingApprovalFields(t *testing.T) {
	bypass := true
	policy := DefaultPolicy()
	policy.AgentOverrides = []AgentOverride{{
		LogicalAgentID: "personal-assistant-ding",
		PolicyOverride: PolicyOverride{
			ApprovalPolicy: ApprovalPolicyNever,
			ApprovalBypass: &bypass,
		},
	}}

	field, _, ok := Validate(policy)
	if ok || field != "agent_overrides[0].approval_policy" {
		t.Fatalf("validation = field %q ok %v", field, ok)
	}
}

func TestValidateRejectsInvalidApprovalEnums(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Policy)
		field  string
	}{
		{
			name: "platform approval policy",
			mutate: func(policy *Policy) {
				policy.DefaultApprovalPolicy = "sometimes"
			},
			field: "default_approval_policy",
		},
		{
			name: "exec approval policy",
			mutate: func(policy *Policy) {
				policy.Exec.ApprovalPolicy = "sometimes"
			},
			field: "exec.approval_policy",
		},
		{
			name: "platform reviewer",
			mutate: func(policy *Policy) {
				policy.DefaultApprovalsReviewer = "robot"
			},
			field: "default_approvals_reviewer",
		},
		{
			name: "override approval policy",
			mutate: func(policy *Policy) {
				policy.AgentOverrides = []AgentOverride{{LogicalAgentID: "assistant-1", PolicyOverride: PolicyOverride{ApprovalPolicy: "sometimes"}}}
			},
			field: "agent_overrides[0].approval_policy",
		},
		{
			name: "override reviewer",
			mutate: func(policy *Policy) {
				policy.AgentOverrides = []AgentOverride{{LogicalAgentID: "assistant-1", PolicyOverride: PolicyOverride{ApprovalsReviewer: "robot"}}}
			},
			field: "agent_overrides[0].approvals_reviewer",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := DefaultPolicy()
			test.mutate(&policy)
			field, _, ok := Validate(policy)
			if ok || field != test.field {
				t.Fatalf("validation = field %q ok %v", field, ok)
			}
		})
	}
}

func TestContainsNeverPolicyChecksOnlyExplicitStoredValues(t *testing.T) {
	base := DefaultPolicy()
	if ContainsNeverPolicy(base) {
		t.Fatal("default policy unexpectedly contains never")
	}

	platform := Clone(base)
	platform.DefaultApprovalPolicy = ApprovalPolicyNever
	if !ContainsNeverPolicy(platform) {
		t.Fatal("platform never policy was not detected")
	}

	conversation := Clone(base)
	conversation.ConversationDefaults["personal_assistant"] = PolicyOverride{ApprovalPolicy: ApprovalPolicyNever}
	if !ContainsNeverPolicy(conversation) {
		t.Fatal("conversation never policy was not detected")
	}

	agent := Clone(base)
	agent.AgentOverrides = []AgentOverride{{LogicalAgentID: "assistant-1", PolicyOverride: PolicyOverride{ApprovalPolicy: ApprovalPolicyNever}}}
	if !ContainsNeverPolicy(agent) {
		t.Fatal("agent never policy was not detected")
	}

	bypass := true
	legacy := Clone(base)
	legacy.AgentOverrides = []AgentOverride{{LogicalAgentID: "assistant-1", PolicyOverride: PolicyOverride{ApprovalBypass: &bypass}}}
	if !ContainsNeverPolicy(legacy) {
		t.Fatal("legacy approval bypass was not detected")
	}

	bypass = false
	nonBypass := Clone(base)
	nonBypass.AgentOverrides = []AgentOverride{{LogicalAgentID: "assistant-1", PolicyOverride: PolicyOverride{ApprovalBypass: &bypass}}}
	if ContainsNeverPolicy(nonBypass) {
		t.Fatal("false legacy approval bypass was detected as never")
	}
}

func TestResolveExplicitConversationManualOverridesRuntimeApprovalControl(t *testing.T) {
	policy := Normalize(Policy{
		DefaultHILMode: HILModeManual,
		AgentOverrides: []AgentOverride{{
			LogicalAgentID: "assistant:human-1",
			PolicyOverride: PolicyOverride{
				HILMode:           HILModeAuto,
				ApprovalPolicy:    ApprovalPolicyNever,
				ApprovalsReviewer: ApprovalsReviewerAI,
				Exec:              &ExecPolicy{Security: SecurityFull},
				Tools:             &ToolPolicy{Security: SecurityDeny},
			},
		}},
	})

	got := policy.Resolve(ResolveInput{LogicalAgentID: "assistant:human-1", ExplicitConversationHIL: "manual"})
	if got.HILMode != HILModeManual || got.ApprovalPolicy != ApprovalPolicyOnRequest || got.ApprovalsReviewer != ApprovalsReviewerHuman {
		t.Fatalf("resolved manual policy = %+v", got)
	}
	if got.Source.HILMode != "conversation_explicit" || got.Source.ApprovalPolicy != "conversation_explicit" || got.Source.ApprovalsReviewer != "conversation_explicit" {
		t.Fatalf("resolved manual sources = %+v", got.Source)
	}
	if got.Exec.Security != SecurityFull || got.Tools.Security != SecurityDeny {
		t.Fatalf("explicit HIL replaced agent policies: %+v", got)
	}

	auto := policy.Resolve(ResolveInput{LogicalAgentID: "assistant:human-1", ExplicitConversationHIL: "auto"})
	if auto.HILMode != HILModeAuto || auto.ApprovalPolicy != ApprovalPolicyNever || auto.ApprovalsReviewer != ApprovalsReviewerAI {
		t.Fatalf("resolved auto policy = %+v", auto)
	}
	if auto.Source.ApprovalPolicy != "logical_agent:assistant:human-1" {
		t.Fatalf("resolved auto approval policy source = %+v", auto.Source)
	}
}

func TestNormalizePolicyDeduplicatesAndTrimsRules(t *testing.T) {
	policy := Normalize(Policy{
		DefaultHILMode: " manual ",
		Reviewer:       ReviewerSettings{LLMProfileID: " reviewer ", TimeoutMS: 30000, MaxTokens: 360},
		Exec: ExecPolicy{ApprovalPolicy: " never ", Security: " allowlist ", Allowlist: []ExecRule{
			{Executable: " git ", Subcommands: []string{" status ", "status", " diff "}, SafeFlags: []string{" --stat ", "--stat"}},
			{Executable: "git", Subcommands: []string{"status", "diff"}, SafeFlags: []string{"--stat"}},
		}},
		Tools: ToolPolicy{Security: SecurityAllowlist, Allowlist: []ToolRule{
			{ToolID: " discussion_talent_list ", Effects: []string{" read ", "read"}, Risk: " low "},
			{ToolID: "discussion_talent_list", Effects: []string{"read"}, Risk: "low"},
		}},
		ForcedHuman: ForcedHumanPolicy{
			ToolIDs:         []string{" publish ", "publish"},
			ExecExecutables: []string{" rm ", "rm"},
		},
	})

	if policy.DefaultHILMode != HILModeManual || policy.Reviewer.LLMProfileID != "reviewer" || policy.Exec.ApprovalPolicy != ApprovalPolicyNever {
		t.Fatalf("normalized scalar values = %+v", policy)
	}
	if len(policy.Exec.Allowlist) != 1 || len(policy.Exec.Allowlist[0].Subcommands) != 2 || len(policy.Exec.Allowlist[0].SafeFlags) != 1 {
		t.Fatalf("normalized exec rules = %+v", policy.Exec.Allowlist)
	}
	if len(policy.Tools.Allowlist) != 1 || len(policy.Tools.Allowlist[0].Effects) != 1 {
		t.Fatalf("normalized tool rules = %+v", policy.Tools.Allowlist)
	}
	if len(policy.ForcedHuman.ToolIDs) != 1 || len(policy.ForcedHuman.ExecExecutables) != 1 {
		t.Fatalf("normalized forced-human rules = %+v", policy.ForcedHuman)
	}
}

func TestNormalizeLegacyExecPolicyDefaultsToNoApproval(t *testing.T) {
	policy := Normalize(Policy{Exec: ExecPolicy{Security: SecurityAllowlist}})
	if policy.Exec.ApprovalPolicy != ApprovalPolicyNever {
		t.Fatalf("legacy exec approval policy = %q", policy.Exec.ApprovalPolicy)
	}
	personal := policy.Resolve(ResolveInput{ConversationKind: "personal_assistant"})
	if personal.Exec.ApprovalPolicy != ApprovalPolicyNever {
		t.Fatalf("legacy personal assistant exec approval policy = %q", personal.Exec.ApprovalPolicy)
	}
}

func TestDefaultPersonalAssistantAllowlist(t *testing.T) {
	policy := DefaultPolicy()
	personal := policy.Resolve(ResolveInput{ConversationKind: "personal_assistant"})
	if personal.HILMode != HILModeAuto || personal.Exec.Security != SecurityAllowlist || personal.Tools.Security != SecurityAllowlist {
		t.Fatalf("personal assistant policy = %+v", personal)
	}
	for _, command := range []string{"pwd", "ls -la .", "rg -n approval internal", "grep -n approval README.md", "find . -maxdepth 2 -type f", "head -n 20 README.md", "tail -n 20 README.md", "wc -l README.md", "sed -n 1,20p README.md", "git status", "git diff --stat", "git log --oneline", "git show --stat HEAD"} {
		analysis := AnalyzeExec(ExecRequest{Command: command, WorkingDirectory: "/workspace/project", WorkspaceRoots: []string{"/workspace"}}, personal.Exec, personal.ForcedHuman)
		if !analysis.Safe {
			t.Fatalf("default command %q did not match: %+v", command, analysis)
		}
	}
	for _, command := range []string{"git push", "git commit -m unsafe", "find . -delete", "sed -i s/a/b/ README.md", "rm README.md", "bash -c 'pwd'"} {
		analysis := AnalyzeExec(ExecRequest{Command: command, WorkingDirectory: "/workspace/project", WorkspaceRoots: []string{"/workspace"}}, personal.Exec, personal.ForcedHuman)
		if analysis.Safe {
			t.Fatalf("dangerous command %q matched default policy: %+v", command, analysis)
		}
	}
	tool := AnalyzeTool(ToolRequest{ToolID: "discussion_talent_list"}, personal.Tools, personal.ForcedHuman)
	if !tool.Safe {
		t.Fatalf("default read-only tool did not match: %+v", tool)
	}
	for _, toolID := range []string{"talent_list", "discussion_talent_list_all", "*"} {
		analysis := AnalyzeTool(ToolRequest{ToolID: toolID}, personal.Tools, personal.ForcedHuman)
		if analysis.Safe {
			t.Fatalf("tool alias %q matched default policy: %+v", toolID, analysis)
		}
	}
}
