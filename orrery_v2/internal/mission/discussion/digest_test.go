package discussion

import (
	"context"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	missioncore "orrery_v2/internal/mission/core"
)

func TestBuildDigestCompactsDiscussionIntoContextPackSafeSummary(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestDiscussionService(t)
	group := createGroupWithMembers(t, ctx, svc)
	decision, err := svc.PostMessage(ctx, PostMessageCommand{
		MissionID:         "mission-1",
		DiscussionGroupID: group.ID,
		Author:            ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-1"},
		Content:           "Decision: keep discussion digest source-bound and omit raw logs.",
		MessageKind:       MessageKindDecision,
		SourceRefs:        []string{"event:decision-1"},
	})
	if err != nil {
		t.Fatalf("PostMessage decision: %v", err)
	}
	_, err = svc.PostMessage(ctx, PostMessageCommand{
		MissionID:         "mission-1",
		DiscussionGroupID: group.ID,
		Author:            ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-1"},
		Content:           "Blocker: projection contract needs confirmation.",
		Summary:           "Projection contract needs confirmation.\ninternal_trace: should not leak",
		MessageKind:       MessageKindBlocker,
		RelatedEvidenceID: "evidence-1",
	})
	if err != nil {
		t.Fatalf("PostMessage blocker: %v", err)
	}
	_, err = svc.PostMessage(ctx, PostMessageCommand{
		MissionID:         "mission-1",
		DiscussionGroupID: group.ID,
		Author:            ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-1"},
		Content:           "[@Worker](mention://agent/profile-agent-2) please respond with evidence.",
		MessageKind:       MessageKindRequest,
		RequiredResponse:  true,
	})
	if err != nil {
		t.Fatalf("PostMessage request: %v", err)
	}

	digest, err := svc.BuildDigest(ctx, group.ID, ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-2"})

	if err != nil {
		t.Fatalf("BuildDigest returned error: %v", err)
	}
	if digest.MissionID != "mission-1" || digest.DiscussionGroupID != group.ID || digest.DiscussionGroupGoal != group.Goal {
		t.Fatalf("digest identity/goals mismatch: %#v", digest)
	}
	if len(digest.ActiveDecisions) != 1 || !strings.Contains(digest.ActiveDecisions[0].Summary, "source-bound") {
		t.Fatalf("decision missing from digest: %#v", digest.ActiveDecisions)
	}
	if len(digest.Blockers) != 1 || strings.Contains(strings.ToLower(digest.Blockers[0].Summary), "internal_trace") {
		t.Fatalf("blocker should be sanitized in digest: %#v", digest.Blockers)
	}
	if len(digest.PendingMentions) != 1 || digest.PendingMentions[0].Recipient.Ref != "profile-agent-2" {
		t.Fatalf("pending mention missing: %#v", digest.PendingMentions)
	}
	if len(digest.ResponsibilityMap) != 2 || len(digest.WorkerGoalRefs) != 2 {
		t.Fatalf("responsibility/worker refs missing: %#v %#v", digest.ResponsibilityMap, digest.WorkerGoalRefs)
	}
	if len(digest.EvidenceIndex) != 1 || digest.EvidenceIndex[0].EvidenceID != "evidence-1" {
		t.Fatalf("evidence index missing: %#v", digest.EvidenceIndex)
	}
	if len(digest.SourceMessageRefs) != 3 || digest.Ref().ID == "" || digest.Ref().SummaryRef == "" {
		t.Fatalf("digest refs not populated: %#v", digest)
	}
	pack, err := agentcore.NewContextPackBuilder().Build(agentcore.ContextPackBuildRequest{
		ContextPackID:       "ctx-1",
		MissionID:           digest.MissionID.String(),
		DiscussionGroupID:   agentcore.DiscussionGroupID(digest.DiscussionGroupID),
		DiscussionGroupGoal: digest.DiscussionGroupGoal,
		MissionFinalGoal:    digest.MissionFinalGoal,
		AgentID:             "profile-agent-2",
		WorkerPersonalGoal:  "Respond to mention",
		DiscussionDigest:    digest.Ref(),
	})
	if err != nil {
		t.Fatalf("digest should enter ContextPack: %v", err)
	}
	if len(pack.RawDiscussionMessages) != 0 || pack.DiscussionDigestSummary == "" {
		t.Fatalf("context pack should carry digest summary only: %#v", pack)
	}
	if !containsString(digest.SourceMessageRefs, string(decision.ID)) {
		t.Fatalf("source message refs should include decision %s: %#v", decision.ID, digest.SourceMessageRefs)
	}
}

func TestMemoryCandidatesAreProposedButCannotBecomeRecordsDirectly(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestDiscussionService(t)
	group := createGroupWithMembers(t, ctx, svc)
	msg, err := svc.PostMessage(ctx, PostMessageCommand{
		MissionID:         "mission-1",
		DiscussionGroupID: group.ID,
		Author:            ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-1"},
		Content:           "Decision: keep MissionEvent as the fact source.",
		MessageKind:       MessageKindDecision,
	})
	if err != nil {
		t.Fatalf("PostMessage decision: %v", err)
	}

	candidate, ok := MemoryCandidateFromMessage(msg)

	if !ok {
		t.Fatalf("decision message should produce memory candidate")
	}
	if candidate.SourceType != memcore.SourceDiscussionMessage || candidate.SourceRef != string(msg.ID) || candidate.Status != memcore.CandidateProposed {
		t.Fatalf("unexpected memory candidate: %#v", candidate)
	}
	if candidate.CanCreateRecordDirectly() {
		t.Fatalf("discussion must not directly create MemoryRecord")
	}
}

func createGroupWithMembers(t *testing.T, ctx context.Context, svc *Service) DiscussionGroup {
	t.Helper()
	group, err := svc.CreateGroup(ctx, missioncore.MissionID("mission-1"), "Coordinate stage 9 production rollout")
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	for _, member := range []DiscussionMember{
		testAgentDiscussionMemberWithRole(group.ID, "profile-agent-1", "assignment-1", "Planner"),
		testAgentDiscussionMemberWithRole(group.ID, "profile-agent-2", "assignment-2", "Worker"),
	} {
		if err := svc.AddMember(ctx, group.ID, member); err != nil {
			t.Fatalf("AddMember: %v", err)
		}
	}
	return group
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
