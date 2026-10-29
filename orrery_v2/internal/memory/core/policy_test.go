package core

import (
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestPersonalMemoryDefaultVisibilityOnlyAllowsOwner(t *testing.T) {
	profile := MemoryProfile{
		ID:         agentcore.MemoryProfileID("mem.personal.agent-a"),
		OwnerRef:   OwnerRef{AgentID: agentcore.AgentID("agent-a")},
		MemoryKind: agentcore.MemoryKindPersonal,
		DefaultScope: MemoryScope{
			Kind: ScopeAgent,
			ID:   "agent-a",
		},
		VisibilityPolicy: VisibilityPolicy{Mode: VisibilityOwnerOnly},
		Enabled:          true,
	}

	if !profile.CanRead(AccessSubject{AgentID: agentcore.AgentID("agent-a")}) {
		t.Fatalf("owner should read personal memory")
	}
	if profile.CanRead(AccessSubject{AgentID: agentcore.AgentID("agent-b")}) {
		t.Fatalf("non-owner should not read personal memory")
	}
}

func TestCollectiveMemoryScopeAllowsMembers(t *testing.T) {
	profile := MemoryProfile{
		ID:         agentcore.MemoryProfileID("mem.project.alpha"),
		OwnerRef:   OwnerRef{OrganizationID: "org-1"},
		MemoryKind: agentcore.MemoryKindProject,
		DefaultScope: MemoryScope{
			Kind: ScopeProject,
			ID:   "project-alpha",
		},
		VisibilityPolicy: VisibilityPolicy{
			Mode:      VisibilityScopedMembers,
			ScopeKind: ScopeProject,
			ScopeID:   "project-alpha",
		},
		Enabled: true,
	}

	if !profile.CanRead(AccessSubject{ProjectIDs: []string{"project-alpha"}}) {
		t.Fatalf("project member should read collective memory")
	}
	if profile.CanRead(AccessSubject{ProjectIDs: []string{"project-beta"}}) {
		t.Fatalf("other project member should not read memory")
	}
}

func TestMemoryCandidateCannotBypassAcceptance(t *testing.T) {
	candidate := MemoryCandidate{
		ID:            "candidate-1",
		SourceType:    SourceDiscussionMessage,
		SourceRef:     "message-1",
		ProposedScope: MemoryScope{Kind: ScopeDiscussion, ID: "discussion-1"},
		ProposedKind:  agentcore.MemoryKindDiscussion,
		Summary:       "Decision candidate",
		EvidenceRefs:  []string{"message-1"},
		CreatedBy:     agentcore.AgentID("agent-a"),
		Status:        CandidateProposed,
	}
	if candidate.CanCreateRecordDirectly() {
		t.Fatalf("proposed discussion candidate must not create memory record directly")
	}
	candidate.Status = CandidateAccepted
	if !candidate.CanCreateRecordDirectly() {
		t.Fatalf("accepted candidate should be eligible for record creation")
	}
}
