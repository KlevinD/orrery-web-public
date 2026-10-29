package core

import "testing"

func TestResponsibilityMCPHeadersCarriesImmutableAuthority(t *testing.T) {
	context := &ResponsibilityContext{
		ContextID:                 "context-1",
		ResponsibilityID:          "responsibility-1",
		ResponsibilityVersion:     3,
		PolicyID:                  "policy-1",
		PolicyVersion:             5,
		AgentProfileID:            "profile-1",
		AllowedTools:              []string{"repo.read", "repo.read", "invalid,scope"},
		AllowedDataScopes:         []string{"mission"},
		PolicyToolScopes:          []string{"repo.*"},
		PolicyDataScopes:          []string{"mission"},
		TaskPermissionScopes:      []string{"repo.read"},
		RequireExplicitToolScopes: true,
	}

	headers := ResponsibilityMCPHeaders(context)

	want := map[string]string{
		RuntimeResponsibilityContextIDHeader:                 "context-1",
		RuntimeResponsibilityIDHeader:                        "responsibility-1",
		RuntimeResponsibilityVersionHeader:                   "3",
		RuntimeResponsibilityPolicyIDHeader:                  "policy-1",
		RuntimeResponsibilityPolicyVersionHeader:             "5",
		RuntimeResponsibilityAgentProfileHeader:              "profile-1",
		RuntimeResponsibilityAllowedToolsHeader:              "repo.read",
		RuntimeResponsibilityAllowedDataHeader:               "mission",
		RuntimeResponsibilityPolicyToolScopesHeader:          "repo.*",
		RuntimeResponsibilityPolicyDataScopesHeader:          "mission",
		RuntimeResponsibilityTaskScopesHeader:                "repo.read",
		RuntimeResponsibilityRequireExplicitToolScopesHeader: "true",
	}
	if len(headers) != len(want) {
		t.Fatalf("headers = %+v", headers)
	}
	for key, value := range want {
		if headers[key] != value {
			t.Fatalf("header %q = %q, want %q", key, headers[key], value)
		}
	}
}

func TestCloneResponsibilityContextClonesAuthoritySlices(t *testing.T) {
	original := &ResponsibilityContext{
		AllowedTools:              []string{"repo.read"},
		AllowedDataScopes:         []string{"mission"},
		ForbiddenActions:          []string{"deploy"},
		HumanInLoopTriggers:       []string{"commitment"},
		PolicyToolScopes:          []string{"repo.*"},
		PolicyDataScopes:          []string{"mission"},
		TaskPermissionScopes:      []string{"repo.read"},
		RequireExplicitToolScopes: true,
	}
	clone := CloneResponsibilityContext(original)
	clone.AllowedTools[0] = "repo.write"
	clone.AllowedDataScopes[0] = "company"
	clone.ForbiddenActions[0] = "delete"
	clone.HumanInLoopTriggers[0] = "payment"
	clone.PolicyToolScopes[0] = "*"
	clone.PolicyDataScopes[0] = "*"
	clone.TaskPermissionScopes[0] = "repo.write"

	if original.AllowedTools[0] != "repo.read" || original.AllowedDataScopes[0] != "mission" || original.ForbiddenActions[0] != "deploy" ||
		original.HumanInLoopTriggers[0] != "commitment" || original.PolicyToolScopes[0] != "repo.*" || original.PolicyDataScopes[0] != "mission" || original.TaskPermissionScopes[0] != "repo.read" || !original.RequireExplicitToolScopes || !clone.RequireExplicitToolScopes {
		t.Fatalf("clone mutated original: %+v", original)
	}
}
