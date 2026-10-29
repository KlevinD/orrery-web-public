package discussionsession

import (
	"errors"
	"testing"
	"time"
)

func TestNormalizeMessageMentionsUsesConversationMembershipAsAuthority(t *testing.T) {
	members := []ConversationMemberRecord{{MemberKind: "agent", MemberID: "staffing-agent", DisplayName: "Canonical Name"}}
	mentions, err := normalizeMessageMentions([]MessageMentionDTO{{TargetKind: "agent", TargetID: "staffing-agent", DisplayName: "Untrusted Name"}}, members)
	if err != nil {
		t.Fatal(err)
	}
	if len(mentions) != 1 || mentions[0].TargetID != "staffing-agent" || mentions[0].DisplayName != "Canonical Name" {
		t.Fatalf("normalized mentions = %+v", mentions)
	}
	_, err = normalizeMessageMentions([]MessageMentionDTO{{TargetKind: "agent", TargetID: "not-a-member"}}, members)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("unknown target error = %v, want ErrForbidden", err)
	}
}

func TestResolveDiscussionTurnTargetsDirectAgent(t *testing.T) {
	view := ConversationRecordView{
		ConversationRecord: ConversationRecord{ConversationID: "conv-direct", ConversationKind: "direct", CreatedBy: "user-1"},
		Members: []ConversationMemberRecord{
			{ConversationID: "conv-direct", MemberKind: "agent", MemberID: "staffing-agent", DisplayName: "智员a"},
			{ConversationID: "conv-direct", MemberKind: "user", MemberID: "user-1", DisplayName: "本地用户"},
		},
	}

	plan := resolveDiscussionTurnTargets(view, MessageRecord{MessageID: "msg-user", Role: "user", AuthorID: "user-1", Content: "你会啥"}, "lead-agent")

	if plan.Mode != discussionTurnTargetDirectAgent || len(plan.Members) != 1 || plan.Members[0].MemberID != "staffing-agent" {
		t.Fatalf("direct target plan = %+v", plan)
	}
	if plan.requiresDiscussionAgent() || !plan.requiresMentionResponder() {
		t.Fatalf("direct target requirements = discussionAgent:%v mentionResponder:%v", plan.requiresDiscussionAgent(), plan.requiresMentionResponder())
	}
}

func TestResolveDiscussionTurnTargetsMention(t *testing.T) {
	view := ConversationRecordView{
		ConversationRecord: ConversationRecord{ConversationID: "conv-mention", ConversationKind: "group", CreatedBy: "user-1"},
		Members: []ConversationMemberRecord{
			{ConversationID: "conv-mention", MemberKind: "agent", MemberID: "lead-agent", DisplayName: "顾明澈"},
			{ConversationID: "conv-mention", MemberKind: "agent", MemberID: "staffing-agent", DisplayName: "秦若兰"},
			{ConversationID: "conv-mention", MemberKind: "user", MemberID: "user-1", DisplayName: "本地用户"},
		},
	}

	plan := resolveDiscussionTurnTargets(view, MessageRecord{
		MessageID: "msg-user", Role: "user", AuthorID: "user-1", Content: "@秦若兰 你看下",
		Mentions: []MessageMentionRecord{{TargetKind: "agent", TargetID: "staffing-agent", DisplayName: "秦若兰"}},
	}, "lead-agent")

	if plan.Mode != discussionTurnTargetMention || len(plan.Members) != 1 || plan.Members[0].MemberID != "staffing-agent" {
		t.Fatalf("mention target plan = %+v", plan)
	}
	if plan.requiresDiscussionAgent() || !plan.requiresMentionResponder() {
		t.Fatalf("mention target requirements = discussionAgent:%v mentionResponder:%v", plan.requiresDiscussionAgent(), plan.requiresMentionResponder())
	}
}

func TestResolveDiscussionTurnTargetsGroup(t *testing.T) {
	view := ConversationRecordView{
		ConversationRecord: ConversationRecord{ConversationID: "conv-group", ConversationKind: "group", CreatedBy: "user-1"},
		Members: []ConversationMemberRecord{
			{ConversationID: "conv-group", MemberKind: "agent", MemberID: "lead-agent", DisplayName: "顾明澈", CreatedAt: testTime},
			{ConversationID: "conv-group", MemberKind: "user", MemberID: "user-1", DisplayName: "本地用户", CreatedAt: testTime.Add(time.Nanosecond)},
		},
	}

	plan := resolveDiscussionTurnTargets(view, MessageRecord{MessageID: "msg-user", Role: "user", AuthorID: "user-1", Content: "大家看下"}, "lead-agent")

	if plan.Mode != discussionTurnTargetGroup || len(plan.Members) != 1 || plan.Members[0].MemberID != "lead-agent" {
		t.Fatalf("group target plan = %+v", plan)
	}
	if !plan.requiresDiscussionAgent() || plan.requiresMentionResponder() {
		t.Fatalf("group target requirements = discussionAgent:%v mentionResponder:%v", plan.requiresDiscussionAgent(), plan.requiresMentionResponder())
	}
}

func TestResolveDiscussionTurnTargetsDoesNotParseNewUserDisplayText(t *testing.T) {
	view := ConversationRecordView{
		ConversationRecord: ConversationRecord{ConversationID: "conv-group", ConversationKind: "group", CreatedBy: "user-1"},
		Members: []ConversationMemberRecord{
			{ConversationID: "conv-group", MemberKind: "agent", MemberID: "lead-agent", DisplayName: "Lead"},
			{ConversationID: "conv-group", MemberKind: "agent", MemberID: "staffing-agent", DisplayName: "Staffing"},
		},
	}
	message := MessageRecord{
		MessageID: "msg-user", Role: "user", AuthorID: "user-1", RoutingSchemaVersion: 1,
		Content: "[@Staffing](mention://agent/staffing-agent) this is display text, not routing data",
	}
	plan := resolveDiscussionTurnTargets(view, message, "lead-agent")
	if plan.Mode != discussionTurnTargetGroup || len(plan.Members) != 1 || plan.Members[0].MemberID != "lead-agent" {
		t.Fatalf("display text affected routing: %+v", plan)
	}
}

func TestResolveDiscussionTurnTargetsDeduplicatesMentionOrder(t *testing.T) {
	view := ConversationRecordView{
		ConversationRecord: ConversationRecord{ConversationID: "conv-mention", ConversationKind: "group", CreatedBy: "user-1"},
		Members: []ConversationMemberRecord{
			{ConversationID: "conv-mention", MemberKind: "agent", MemberID: "alpha-agent", DisplayName: "Alpha"},
			{ConversationID: "conv-mention", MemberKind: "agent", MemberID: "beta-agent", DisplayName: "Beta"},
			{ConversationID: "conv-mention", MemberKind: "user", MemberID: "user-1", DisplayName: "本地用户"},
		},
	}
	plan := resolveDiscussionTurnTargets(view, MessageRecord{
		MessageID: "msg-user", Role: "user", AuthorID: "user-1", Content: "@Beta @Alpha @Beta",
		Mentions: []MessageMentionRecord{
			{TargetKind: "agent", TargetID: "beta-agent", DisplayName: "Beta"},
			{TargetKind: "agent", TargetID: "alpha-agent", DisplayName: "Alpha"},
			{TargetKind: "agent", TargetID: "beta-agent", DisplayName: "Beta"},
		},
	}, "lead-agent")

	if plan.Mode != discussionTurnTargetMention || len(plan.Members) != 2 || plan.Members[0].MemberID != "beta-agent" || plan.Members[1].MemberID != "alpha-agent" {
		t.Fatalf("mention target order = %+v", plan)
	}
}

func TestResolveRuntimeApprovalTurnTargets(t *testing.T) {
	view := ConversationRecordView{
		ConversationRecord: ConversationRecord{ConversationID: "conv-approval", ConversationKind: "direct", CreatedBy: "user-1"},
		Members: []ConversationMemberRecord{
			{ConversationID: "conv-approval", MemberKind: "agent", MemberID: "lead-agent", DisplayName: "顾明澈"},
			{ConversationID: "conv-approval", MemberKind: "agent", MemberID: "staffing-agent", DisplayName: "秦若兰"},
			{ConversationID: "conv-approval", MemberKind: "user", MemberID: "user-1", DisplayName: "本地用户"},
		},
	}

	plan := resolveRuntimeApprovalTurnTargets(view, RunRecord{RunID: "run-blocked", ConversationID: "conv-approval", AgentID: "staffing-agent", Status: "blocked"})

	if plan.Mode != discussionTurnTargetApprovalContinuation || len(plan.Members) != 1 || plan.Members[0].MemberID != "staffing-agent" {
		t.Fatalf("approval target plan = %+v", plan)
	}
	if plan.requiresDiscussionAgent() || !plan.requiresMentionResponder() {
		t.Fatalf("approval target requirements = discussionAgent:%v mentionResponder:%v", plan.requiresDiscussionAgent(), plan.requiresMentionResponder())
	}
}
