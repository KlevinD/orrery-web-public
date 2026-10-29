package team

import (
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestIsWorkerInternalQAAgentMatchesProfileIDAndRoleRefs(t *testing.T) {
	tests := []struct {
		name      string
		profileID agentcore.AgentProfileID
		roleRefs  []agentcore.RoleRef
		want      bool
	}{
		{name: "qa plan profile id", profileID: "qa-plan-agent", want: true},
		{name: "qa check role ref", profileID: "profile-qa-check", roleRefs: []agentcore.RoleRef{{ID: "QACheck"}}, want: true},
		{name: "normal worker", profileID: "agent-frontend", roleRefs: []agentcore.RoleRef{{ID: "FrontendEngineer"}}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsWorkerInternalQAAgent(tt.profileID, tt.roleRefs); got != tt.want {
				t.Fatalf("IsWorkerInternalQAAgent(%q, %+v) = %v, want %v", tt.profileID, tt.roleRefs, got, tt.want)
			}
		})
	}
}

func TestIsWorkerInternalQARoleIdentifierTrimsAndNormalizes(t *testing.T) {
	for _, value := range []string{" QAPlan ", "qacheck", "qa_plan_agent", "qa-check-agent"} {
		if !IsWorkerInternalQARoleIdentifier(value) {
			t.Fatalf("IsWorkerInternalQARoleIdentifier(%q) = false, want true", value)
		}
	}
	if IsWorkerInternalQARoleIdentifier("agent-worker") {
		t.Fatal("normal worker should not be classified as worker-internal QA")
	}
}
