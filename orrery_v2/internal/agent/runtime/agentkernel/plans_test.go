package agentkernel

import (
	agentcore "orrery_v2/internal/agent/core"
	"testing"
)

func TestPlansRequireExplicitHostGrantAndTaskAllowlist(t *testing.T) {
	envelope := &agentcore.ExecutionEnvelope{TenantID: "tenant", RunID: "run", AgentID: "agent", AgentProfileID: "agent", AllowedToolIDs: []string{"plan.create", "plan.update", "plan.complete"}}
	if err := envelope.Seal(); err != nil {
		t.Fatal(err)
	}
	req := agentcore.RuntimeRequest{ExecutionEnvelope: envelope, RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{agentcore.RuntimeAllowedToolIDsProviderKey: `["plan.create"]`}}}
	got := appendAuthorizedPlanTools(req)
	if len(got) != 1 || got[0].ToolID != "plan.create" {
		t.Fatalf("wrong plan grants: %+v", got)
	}
	req.ToolBindings = got
	if len(appendAuthorizedPlanTools(req)) != 1 {
		t.Fatal("plan binding duplicated")
	}
	req.ExecutionEnvelope = nil
	req.ToolBindings = nil
	if len(appendAuthorizedPlanTools(req)) != 0 {
		t.Fatal("implicit local tool grant")
	}
}
