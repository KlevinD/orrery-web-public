package runtimeapproval

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestToolAnalyzerRequiresExactStableToolID(t *testing.T) {
	policy := ToolPolicy{Security: SecurityAllowlist, Allowlist: []ToolRule{{
		RuleID: "talent-read", ToolID: "discussion_talent_list", Effects: []string{"read"}, Risk: "low",
	}}}
	match := AnalyzeTool(ToolRequest{ToolID: "discussion_talent_list", NativeToolName: "Talent List"}, policy, ForcedHumanPolicy{})
	if !match.Safe || match.Reason != "tool_allowlist_match" || match.MatchedRuleID != "talent-read" {
		t.Fatalf("exact tool analysis = %+v", match)
	}
	alias := AnalyzeTool(ToolRequest{ToolID: "Talent List", NativeToolName: "discussion_talent_list"}, policy, ForcedHumanPolicy{})
	if alias.Safe || alias.Reason != "tool_allowlist_miss" {
		t.Fatalf("alias tool analysis = %+v", alias)
	}
}

func TestToolAnalyzerTreatsMissingEffectsAsUnknown(t *testing.T) {
	policy := ToolPolicy{Security: SecurityAllowlist, Allowlist: []ToolRule{{ToolID: "custom_lookup"}}}
	got := AnalyzeTool(ToolRequest{ToolID: "custom_lookup"}, policy, ForcedHumanPolicy{})
	if got.Safe || got.Reason != "tool_effect_unknown" || len(got.Effects) != 1 || got.Effects[0] != "unknown" || got.Risk != "unknown" {
		t.Fatalf("unknown tool analysis = %+v", got)
	}
}

func TestForcedHumanBoundaryCannotBeOverridden(t *testing.T) {
	tests := []struct {
		name    string
		request ToolRequest
		reason  string
	}{
		{name: "mcp elicitation", request: ToolRequest{Kind: "mcp_elicitation", ToolID: "mcp_elicitation"}, reason: "forced_mcp_elicitation"},
		{name: "explicit", request: ToolRequest{ToolID: "custom", Metadata: map[string]string{"approval_force_manual": "true"}}, reason: "forced_explicit"},
		{name: "configured tool", request: ToolRequest{ToolID: "legal_contract_send", Effects: []string{"network"}, Risk: "low"}, reason: "forced_configured_tool"},
	}
	forced := ForcedHumanPolicy{ToolIDs: []string{"legal_contract_send"}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AnalyzeTool(tt.request, ToolPolicy{Security: SecurityFull}, forced)
			if got.Safe || !got.ForcedHuman || got.Reason != tt.reason {
				t.Fatalf("tool analysis = %+v, want forced reason %q", got, tt.reason)
			}
		})
	}
}

func TestToolAnalyzerDoesNotInferHumanApprovalFromEffectsOrRisk(t *testing.T) {
	for _, request := range []ToolRequest{
		{ToolID: "publish", Effects: []string{"publish"}, Risk: "low"},
		{ToolID: "charge", Effects: []string{"financial"}, Risk: "low"},
		{ToolID: "delete", Effects: []string{"delete"}, Risk: "medium"},
		{ToolID: "grant", Effects: []string{"permission"}, Risk: "medium"},
		{ToolID: "credentials", Effects: []string{"secret"}, Risk: "medium"},
		{ToolID: "unknown", Effects: []string{"write"}, Risk: "high"},
		{ToolID: "unknown", Effects: []string{"write"}, Risk: "critical"},
	} {
		got := AnalyzeTool(request, ToolPolicy{Security: SecurityFull}, ForcedHumanPolicy{})
		if got.ForcedHuman || !got.Safe || got.Reason != "tool_full" {
			t.Fatalf("tool analysis = %+v", got)
		}
	}
}

func TestMarketingWechatManualContractRemainsForced(t *testing.T) {
	got := AnalyzeTool(ToolRequest{ToolID: "marketing_wechat_draft_create"}, ToolPolicy{Security: SecurityFull}, ForcedHumanPolicy{})
	if got.Safe || !got.ForcedHuman || got.Reason != "forced_marketing_wechat" {
		t.Fatalf("marketing tool analysis = %+v", got)
	}
}

func TestSanitizeReviewInputRemovesSecretsAndBoundsPayload(t *testing.T) {
	input := map[string]any{
		"query":         "talent",
		"api_token":     "token-should-not-appear",
		"Authorization": "Bearer secret-should-not-appear",
		"nested": map[string]any{
			"private_key": "private-key-should-not-appear",
			"safe":        strings.Repeat("x", 2000),
		},
	}
	encoded, err := json.Marshal(SanitizeReviewInput(input))
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, secret := range []string{"token-should-not-appear", "secret-should-not-appear", "private-key-should-not-appear"} {
		if strings.Contains(text, secret) {
			t.Fatalf("sanitized input contains secret %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, `"api_token":"[REDACTED]"`) || len(text) > 8192 {
		t.Fatalf("sanitized input = %s", text)
	}
}
