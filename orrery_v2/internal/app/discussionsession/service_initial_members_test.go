package discussionsession

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func TestServiceSendMessageCreatesConversationWithLeadStaffingAndCreatorMembers(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{
		AssistantMessage: "当前讨论组有顾明澈、秦若兰和你。",
		Status:           "completed",
		RuntimeKind:      "codex",
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})
	viewer := ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}

	dto, err := service.SendMessage(context.Background(), viewer, SendMessageCommand{Content: "现在这个讨论组都有谁", IdempotencyKey: "send-members"})
	if err != nil {
		t.Fatal(err)
	}

	want := []ConversationMemberDTO{
		{Kind: "agent", ID: "lead-agent", DisplayName: "顾明澈", Role: "Lead"},
		{Kind: "agent", ID: "staffing-agent", DisplayName: "秦若兰", Role: "Staffing"},
		{Kind: "user", ID: "local-user", DisplayName: "本地用户", Role: "话题发起人"},
	}
	if got := runner.request.Members; !sameConversationMembers(got, want) {
		t.Fatalf("discussion agent request members = %+v, want %+v", got, want)
	}
	if got := dto.Members; !sameConversationMembers(got, want) {
		t.Fatalf("dto members = %+v, want %+v", got, want)
	}
	stored, err := repo.GetConversation(context.Background(), dto.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if got := mapConversationMembers(stored.Members); !sameConversationMembers(got, want) {
		t.Fatalf("stored members = %+v, want %+v", got, want)
	}
	if len(dto.Messages) != 2 || dto.Messages[0].AuthorName != "本地用户" || dto.Messages[1].AuthorName != "顾明澈" {
		t.Fatalf("message author names = %+v, want persisted real names", dto.Messages)
	}
}

func TestServiceSendMessageCreatesConversationWithExplicitInitialMembersAndMemberAddPolicy(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "已开始处理。",
		Status:           "completed",
		RuntimeKind:      "codex",
	}}
	allowMemberAdditions := false
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})
	viewer := ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}

	dto, err := service.SendMessage(context.Background(), viewer, SendMessageCommand{
		Content:              "准备日报",
		InitialMemberIDs:     []string{"lead-agent", "coordinator-agent"},
		AllowMemberAdditions: &allowMemberAdditions,
		IdempotencyKey:       "send-explicit-members",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []ConversationMemberDTO{
		{Kind: "agent", ID: "lead-agent", DisplayName: "顾明澈", Role: "Lead"},
		{Kind: "agent", ID: "coordinator-agent", DisplayName: "林知远", Role: "Coordinator"},
		{Kind: "user", ID: "local-user", DisplayName: "本地用户", Role: "话题发起人"},
	}
	if dto.MemberAdditionsAllowed {
		t.Fatalf("member additions should be disabled: %+v", dto)
	}
	if got := dto.Members; !sameConversationMembers(got, want) {
		t.Fatalf("dto members = %+v, want %+v", got, want)
	}
	if runner.request.Members[1].ID != "coordinator-agent" {
		t.Fatalf("runner members = %+v", runner.request.Members)
	}
	if containsDiscussionTool(runner.request.BoundTools, "discussion_member_add") {
		t.Fatalf("member add tool should not be bound when policy disables it: %+v", runner.request.BoundTools)
	}
	stored, err := repo.GetConversation(context.Background(), dto.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if conversationMemberAdditionsAllowed(stored.ConversationRecord) {
		t.Fatalf("stored conversation should disable member additions: %+v", stored.ConversationRecord)
	}
	if conversationMemberName(stored.Members, "agent", "staffing-agent") != "" {
		t.Fatalf("explicit initial members should not fallback to staffing member: %+v", stored.Members)
	}
}

func TestServiceSendMessageCreatesDirectConversationWithOnlyTargetAgentAndCreator(t *testing.T) {
	repo := newMemoryRepository(t)
	lead := &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "lead should not run"}}
	direct := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		AssistantMessage: "我来单独处理。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  lead,
		MentionResponder: direct,
		Clock:            fixedClock{now: testTime},
		IDGenerator:      fixedIDGen("conv-1", "msg-user", "run-direct", "msg-direct"),
	})
	viewer := ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}

	dto, err := service.SendMessage(context.Background(), viewer, SendMessageCommand{
		Content:        "这个实现你单独看下",
		DirectAgentID:  "staffing-agent",
		IdempotencyKey: "direct-staffing",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []ConversationMemberDTO{
		{Kind: "agent", ID: "staffing-agent", DisplayName: "秦若兰", Role: "私聊对象"},
		{Kind: "user", ID: "local-user", DisplayName: "本地用户", Role: "话题发起人"},
	}
	if dto.ConversationKind != "direct" || dto.DirectAgentID != "staffing-agent" {
		t.Fatalf("direct conversation metadata = kind %q target %q", dto.ConversationKind, dto.DirectAgentID)
	}
	if dto.DirectTargetKind != "agent" || dto.DirectTargetID != "staffing-agent" {
		t.Fatalf("direct target metadata = kind %q target %q", dto.DirectTargetKind, dto.DirectTargetID)
	}
	if got := dto.Title; got != "秦若兰" {
		t.Fatalf("direct conversation title = %q, want target display name", got)
	}
	if got := dto.Members; !sameConversationMembers(got, want) {
		t.Fatalf("dto members = %+v, want %+v", got, want)
	}
	if lead.called {
		t.Fatal("direct conversation should not run the default lead discussion agent")
	}
	if !direct.called || direct.request.TargetAgent.ID != "staffing-agent" {
		t.Fatalf("direct mention request = %+v", direct.request)
	}
	if len(dto.Runs) != 1 || dto.Runs[0].AgentID != "staffing-agent" {
		t.Fatalf("direct runs = %+v, want target agent run", dto.Runs)
	}
	if len(dto.Messages) != 2 || dto.Messages[1].AuthorID != "staffing-agent" || dto.Messages[1].Content != "我来单独处理。" {
		t.Fatalf("direct messages = %+v", dto.Messages)
	}
	stored, err := repo.GetConversation(context.Background(), dto.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ConversationKind != "direct" || stored.DirectAgentID != "staffing-agent" || stored.DirectTargetKind != "agent" || stored.DirectTargetID != "staffing-agent" {
		t.Fatalf("stored direct metadata = %+v", stored.ConversationRecord)
	}
	if got := mapConversationMembers(stored.Members); !sameConversationMembers(got, want) {
		t.Fatalf("stored members = %+v, want %+v", got, want)
	}
}

func TestServiceSendMessageCreatesDirectConversationWithHumanTargetWithoutAgentRun(t *testing.T) {
	repo := newMemoryRepository(t)
	lead := &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "lead should not run"}}
	direct := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{AssistantMessage: "agent should not run"}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  lead,
		MentionResponder: direct,
		AgentDirectory: &testAgentDirectory{entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID: "human-reviewer",
			DisplayName:    "陆清岚",
			RealName:       "陆清岚",
			RoleName:       "Reviewer",
			AgentKind:      agentcore.AgentKindHuman,
			Enabled:        true,
		}}},
		Clock:       fixedClock{now: testTime},
		IDGenerator: fixedIDGen("conv-human", "msg-user"),
	})
	viewer := ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}

	dto, err := service.SendMessage(context.Background(), viewer, SendMessageCommand{
		Content:        "这个你也看一下",
		DirectTargetID: "human-reviewer",
		IdempotencyKey: "direct-human",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []ConversationMemberDTO{
		{Kind: "user", ID: "human-reviewer", DisplayName: "陆清岚", Role: "私聊对象"},
		{Kind: "user", ID: "local-user", DisplayName: "本地用户", Role: "话题发起人"},
	}
	if dto.ConversationID != "conv-human" || dto.ConversationKind != "direct" {
		t.Fatalf("direct human conversation metadata = id %q kind %q", dto.ConversationID, dto.ConversationKind)
	}
	if dto.DirectTargetKind != "user" || dto.DirectTargetID != "human-reviewer" || dto.DirectAgentID != "" {
		t.Fatalf("direct human target metadata = kind %q target %q agent %q", dto.DirectTargetKind, dto.DirectTargetID, dto.DirectAgentID)
	}
	if got := dto.Title; got != "陆清岚" {
		t.Fatalf("direct human title = %q, want target display name", got)
	}
	if got := dto.Members; !sameConversationMembers(got, want) {
		t.Fatalf("dto members = %+v, want %+v", got, want)
	}
	if lead.called {
		t.Fatal("direct human conversation should not run the default lead discussion agent")
	}
	if direct.called {
		t.Fatal("direct human conversation should not call the agent mention responder")
	}
	if len(dto.Runs) != 0 {
		t.Fatalf("direct human runs = %+v, want none", dto.Runs)
	}
	if len(dto.Messages) != 1 || dto.Messages[0].AuthorID != "local-user" || dto.Messages[0].Content != "这个你也看一下" {
		t.Fatalf("direct human messages = %+v", dto.Messages)
	}
	stored, err := repo.GetConversation(context.Background(), dto.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ConversationKind != "direct" || stored.DirectTargetKind != "user" || stored.DirectTargetID != "human-reviewer" || stored.DirectAgentID != "" {
		t.Fatalf("stored direct human metadata = %+v", stored.ConversationRecord)
	}
	if got := mapConversationMembers(stored.Members); !sameConversationMembers(got, want) {
		t.Fatalf("stored members = %+v, want %+v", got, want)
	}
}

func TestServiceSendMessageRejectsDirectConversationWithCurrentHumanProfile(t *testing.T) {
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{
		Repository: repo,
		DiscussionAgent: &capturingDiscussionAgentRunner{
			agentID:  "lead-agent",
			response: DiscussionAgentResponse{AssistantMessage: "lead should not run"},
		},
		MentionResponder: &capturingDiscussionAgentMentionResponder{
			response: DiscussionAgentResponse{AssistantMessage: "agent should not run"},
		},
		AgentDirectory: &testAgentDirectory{entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID: "human-operator",
			DisplayName:    "Operator",
			RealName:       "Operator",
			RoleName:       "Reviewer",
			AgentKind:      agentcore.AgentKindHuman,
			Enabled:        true,
		}}},
		HumanProfiles: &testHumanProfiles{profile: agentcore.AgentProfile{
			ID:          "human-operator",
			DisplayName: "Operator",
			RealName:    "Operator",
			AgentKind:   agentcore.AgentKindHuman,
			Enabled:     true,
		}},
		Clock:       fixedClock{now: testTime},
		IDGenerator: fixedIDGen("conv-self", "msg-user"),
	})

	_, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "operator", AgentProfileID: "human-operator", ViewerKind: ViewerKindUser, DisplayName: "Operator"}, SendMessageCommand{
		Content:        "发给我自己",
		DirectTargetID: "human-operator",
		IdempotencyKey: "direct-self-human",
	})

	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("SendMessage error = %v, want ErrInvalidRequest", err)
	}
	if _, getErr := repo.GetConversation(context.Background(), "conv-self"); !errors.Is(getErr, ErrNotFound) {
		t.Fatalf("self direct conversation should not be created, get error = %v", getErr)
	}
}

func TestServiceSendMessageRejectsExistingSelfDirectConversationReuse(t *testing.T) {
	repo := newMemoryRepository(t)
	existing := ConversationRecord{
		ConversationID:   "conv-self-existing",
		ConversationKind: "direct",
		DirectTargetKind: "user",
		DirectTargetID:   "human-operator",
		Title:            "Operator",
		Status:           "completed",
		CreatedBy:        "operator",
		CreatedByName:    "Operator",
		CreatedAt:        testTime,
		UpdatedAt:        testTime,
	}
	if err := repo.SaveConversation(context.Background(), existing); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(context.Background(), ConversationMemberRecord{ConversationID: "conv-self-existing", MemberKind: "user", MemberID: "human-operator", DisplayName: "Operator", Role: "私聊对象", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(context.Background(), ConversationMemberRecord{ConversationID: "conv-self-existing", MemberKind: "user", MemberID: "operator", DisplayName: "Operator", Role: "话题发起人", CreatedAt: testTime.Add(time.Nanosecond)}); err != nil {
		t.Fatal(err)
	}
	service := newTestService(ServiceOptions{
		Repository: repo,
		DiscussionAgent: &capturingDiscussionAgentRunner{
			agentID:  "lead-agent",
			response: DiscussionAgentResponse{AssistantMessage: "lead should not run"},
		},
		MentionResponder: &capturingDiscussionAgentMentionResponder{
			response: DiscussionAgentResponse{AssistantMessage: "agent should not run"},
		},
		AgentDirectory: &testAgentDirectory{entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID: "human-operator",
			DisplayName:    "Operator",
			RealName:       "Operator",
			RoleName:       "Reviewer",
			AgentKind:      agentcore.AgentKindHuman,
			Enabled:        true,
		}}},
		HumanProfiles: &testHumanProfiles{profile: agentcore.AgentProfile{
			ID:          "human-operator",
			DisplayName: "Operator",
			RealName:    "Operator",
			AgentKind:   agentcore.AgentKindHuman,
			Enabled:     true,
		}},
		Clock:       fixedClock{now: testTime.Add(time.Minute)},
		IDGenerator: fixedIDGen("msg-user"),
	})

	_, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "operator", AgentProfileID: "human-operator", ViewerKind: ViewerKindUser, DisplayName: "Operator"}, SendMessageCommand{
		Content:        "继续发给我自己",
		DirectTargetID: "human-operator",
		IdempotencyKey: "direct-self-human-existing",
	})

	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("SendMessage error = %v, want ErrInvalidRequest", err)
	}
	stored, getErr := repo.GetConversation(context.Background(), "conv-self-existing")
	if getErr != nil {
		t.Fatal(getErr)
	}
	if len(stored.Messages) != 0 {
		t.Fatalf("self direct conversation messages = %+v, want none added", stored.Messages)
	}
}

func TestServiceSendMessageInfersAgentDirectConversationFromTargetID(t *testing.T) {
	repo := newMemoryRepository(t)
	lead := &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "lead should not run"}}
	direct := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		AssistantMessage: "我来单独处理。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  lead,
		MentionResponder: direct,
		Clock:            fixedClock{now: testTime},
		IDGenerator:      fixedIDGen("conv-agent-target", "msg-user", "run-direct", "msg-direct"),
	})
	viewer := ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}

	dto, err := service.SendMessage(context.Background(), viewer, SendMessageCommand{
		Content:        "这个实现你单独看下",
		DirectTargetID: "staffing-agent",
		IdempotencyKey: "direct-target-staffing",
	})
	if err != nil {
		t.Fatal(err)
	}

	if dto.ConversationKind != "direct" || dto.DirectTargetKind != "agent" || dto.DirectTargetID != "staffing-agent" || dto.DirectAgentID != "staffing-agent" {
		t.Fatalf("direct target metadata = kind %q target %q agent %q", dto.DirectTargetKind, dto.DirectTargetID, dto.DirectAgentID)
	}
	if lead.called {
		t.Fatal("inferred direct agent conversation should not run the default lead discussion agent")
	}
	if !direct.called || direct.request.TargetAgent.ID != "staffing-agent" {
		t.Fatalf("direct responder request = %+v", direct.request)
	}
	if len(dto.Runs) != 1 || dto.Runs[0].AgentID != "staffing-agent" {
		t.Fatalf("direct runs = %+v, want target agent run", dto.Runs)
	}
}

func TestServiceSendMessageReusesExistingDirectConversationForSameCreatorAndAgent(t *testing.T) {
	repo := newMemoryRepository(t)
	existing := ConversationRecord{
		ConversationID:   "conv-direct-existing",
		ConversationKind: "direct",
		DirectAgentID:    "staffing-agent",
		Title:            "秦若兰",
		Status:           "completed",
		CreatedBy:        "local-user",
		CreatedByName:    "本地用户",
		CreatedAt:        testTime,
		UpdatedAt:        testTime,
	}
	if err := repo.SaveConversation(context.Background(), existing); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(context.Background(), ConversationMemberRecord{ConversationID: "conv-direct-existing", MemberKind: "agent", MemberID: "staffing-agent", DisplayName: "秦若兰", Role: "私聊对象", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(context.Background(), ConversationMemberRecord{ConversationID: "conv-direct-existing", MemberKind: "user", MemberID: "local-user", DisplayName: "本地用户", Role: "话题发起人", CreatedAt: testTime.Add(time.Nanosecond)}); err != nil {
		t.Fatal(err)
	}
	lead := &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "lead should not run"}}
	direct := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{
		AssistantMessage: "继续私聊。",
		Status:           "completed",
		RuntimeKind:      "managed",
	}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  lead,
		MentionResponder: direct,
		Clock:            fixedClock{now: testTime.Add(time.Minute)},
		IDGenerator:      fixedIDGen("conv-new-should-not-exist", "msg-user", "run-direct", "msg-direct"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{
		Content:        "继续看这个问题",
		DirectAgentID:  "staffing-agent",
		IdempotencyKey: "direct-staffing-repeat",
	})
	if err != nil {
		t.Fatal(err)
	}

	if dto.ConversationID != "conv-direct-existing" {
		t.Fatalf("conversation id = %q, want existing direct conversation", dto.ConversationID)
	}
	if lead.called {
		t.Fatal("reused direct conversation should not run the default lead discussion agent")
	}
	if !direct.called || direct.request.ConversationID != "conv-direct-existing" {
		t.Fatalf("direct responder request = %+v", direct.request)
	}
	if len(dto.Messages) != 2 || dto.Messages[0].Content != "继续看这个问题" || dto.Messages[1].Content != "继续私聊。" {
		t.Fatalf("direct messages = %+v", dto.Messages)
	}
	if _, err := repo.GetConversation(context.Background(), "conv-new-should-not-exist"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("new direct conversation should not be created, get error = %v", err)
	}
}

func TestServiceSendMessageReusesExistingDirectConversationForSameCreatorAndHumanTarget(t *testing.T) {
	repo := newMemoryRepository(t)
	existing := ConversationRecord{
		ConversationID:   "conv-direct-human-existing",
		ConversationKind: "direct",
		DirectTargetKind: "user",
		DirectTargetID:   "human-reviewer",
		Title:            "陆清岚",
		Status:           "completed",
		CreatedBy:        "local-user",
		CreatedByName:    "本地用户",
		CreatedAt:        testTime,
		UpdatedAt:        testTime,
	}
	if err := repo.SaveConversation(context.Background(), existing); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(context.Background(), ConversationMemberRecord{ConversationID: "conv-direct-human-existing", MemberKind: "user", MemberID: "human-reviewer", DisplayName: "陆清岚", Role: "私聊对象", CreatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMember(context.Background(), ConversationMemberRecord{ConversationID: "conv-direct-human-existing", MemberKind: "user", MemberID: "local-user", DisplayName: "本地用户", Role: "话题发起人", CreatedAt: testTime.Add(time.Nanosecond)}); err != nil {
		t.Fatal(err)
	}
	lead := &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "lead should not run"}}
	direct := &capturingDiscussionAgentMentionResponder{response: DiscussionAgentResponse{AssistantMessage: "agent should not run"}}
	service := newTestService(ServiceOptions{
		Repository:       repo,
		DiscussionAgent:  lead,
		MentionResponder: direct,
		AgentDirectory: &testAgentDirectory{entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID: "human-reviewer",
			DisplayName:    "陆清岚",
			RealName:       "陆清岚",
			RoleName:       "Reviewer",
			AgentKind:      agentcore.AgentKindHuman,
			Enabled:        true,
		}}},
		Clock:       fixedClock{now: testTime.Add(time.Minute)},
		IDGenerator: fixedIDGen("conv-new-should-not-exist", "msg-user"),
	})

	dto, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{
		Content:        "继续私聊这个点",
		DirectTargetID: "human-reviewer",
		IdempotencyKey: "direct-human-repeat",
	})
	if err != nil {
		t.Fatal(err)
	}

	if dto.ConversationID != "conv-direct-human-existing" {
		t.Fatalf("conversation id = %q, want existing direct human conversation", dto.ConversationID)
	}
	if dto.DirectTargetKind != "user" || dto.DirectTargetID != "human-reviewer" || dto.DirectAgentID != "" {
		t.Fatalf("direct human target metadata = kind %q target %q agent %q", dto.DirectTargetKind, dto.DirectTargetID, dto.DirectAgentID)
	}
	if lead.called {
		t.Fatal("reused direct human conversation should not run the default lead discussion agent")
	}
	if direct.called {
		t.Fatal("reused direct human conversation should not call the agent mention responder")
	}
	if len(dto.Runs) != 0 {
		t.Fatalf("direct human runs = %+v, want none", dto.Runs)
	}
	if len(dto.Messages) != 1 || dto.Messages[0].Content != "继续私聊这个点" {
		t.Fatalf("direct human messages = %+v", dto.Messages)
	}
	if _, err := repo.GetConversation(context.Background(), "conv-new-should-not-exist"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("new direct human conversation should not be created, get error = %v", err)
	}
}

func TestServiceInitialConversationAgentsComeFromConfiguredMembers(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{agentID: "coordinator-agent", response: DiscussionAgentResponse{
		AssistantMessage: "收到",
		Status:           "completed",
		RuntimeKind:      "codex",
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		InitialAgentMembers: []InitialAgentMemberSeed{{
			AgentID: "coordinator-agent",
			Role:    "Coordinator",
		}},
		Clock:       fixedClock{now: testTime},
		IDGenerator: fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})
	viewer := ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}

	dto, err := service.SendMessage(context.Background(), viewer, SendMessageCommand{Content: "启动项目", IdempotencyKey: "send-configured-members"})
	if err != nil {
		t.Fatal(err)
	}

	want := []ConversationMemberDTO{
		{Kind: "agent", ID: "coordinator-agent", DisplayName: "林知远", Role: "Coordinator"},
		{Kind: "user", ID: "local-user", DisplayName: "本地用户", Role: "话题发起人"},
	}
	if got := runner.request.Members; !sameConversationMembers(got, want) {
		t.Fatalf("discussion agent request members = %+v, want %+v", got, want)
	}
	if got := dto.Members; !sameConversationMembers(got, want) {
		t.Fatalf("dto members = %+v, want %+v", got, want)
	}
	if got := runner.request.AgentID; got != "coordinator-agent" {
		t.Fatalf("run agent id = %q, want runner identity", got)
	}
}

func TestServiceSendMessageAllowsUsernameAsDisplayName(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{
		AssistantMessage: "收到",
		Status:           "completed",
		RuntimeKind:      "codex",
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})
	viewer := ViewerRef{ViewerID: "superadmin", ViewerKind: ViewerKindUser, DisplayName: "superadmin"}

	dto, err := service.SendMessage(context.Background(), viewer, SendMessageCommand{Content: "启动项目", IdempotencyKey: "send-username-display"})
	if err != nil {
		t.Fatal(err)
	}

	want := ConversationMemberDTO{Kind: "user", ID: "superadmin", DisplayName: "superadmin", Role: "话题发起人"}
	if len(dto.Members) == 0 || dto.Members[len(dto.Members)-1] != want {
		t.Fatalf("creator member = %+v, want %+v", dto.Members, want)
	}
	if runner.request.Viewer.DisplayName != "superadmin" {
		t.Fatalf("runner viewer display name = %+v", runner.request.Viewer)
	}
}

func TestServiceRejectsWorkerInternalQAAsInitialConversationMember(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "should not run"}}
	service := NewService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		InitialAgentMembers: []InitialAgentMemberSeed{{
			AgentID: "qa-plan-agent",
			Role:    "QAPlan",
		}},
		Clock:       fixedClock{now: testTime},
		IDGenerator: fixedIDGen("conv-1"),
		AgentDirectory: &testAgentDirectory{entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID:  "qa-plan-agent",
			AgentInstanceID: "qa-plan-agent",
			DisplayName:     "验收计划",
			RealName:        "验收计划",
			RoleName:        "QAPlan",
			AgentKind:       agentcore.AgentKindAI,
			OrgIdentity:     agentcore.AgentOrgIdentity{RoleRefs: []agentcore.RoleRef{{ID: "QAPlan"}}},
			Enabled:         true,
		}}},
		HumanProfiles: &testHumanProfiles{},
	})

	_, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "启动项目", IdempotencyKey: "send-qa-initial-member"})
	if !errors.Is(err, ErrConversationMember) {
		t.Fatalf("SendMessage error = %v, want ErrConversationMember", err)
	}
	if !strings.Contains(err.Error(), "worker-internal QA") {
		t.Fatalf("SendMessage error = %v, want worker-internal QA detail", err)
	}
	if runner.called {
		t.Fatal("discussion agent runner should not run when worker-internal QA is configured as a direct member")
	}
	if _, getErr := repo.GetConversation(context.Background(), "conv-1"); !errors.Is(getErr, ErrNotFound) {
		t.Fatalf("conversation should not be saved on worker-internal QA member failure, get error = %v", getErr)
	}
}

func TestServiceRunAgentRequiresRunnerIdentityInsteadOfConfiguredFirstMember(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &anonymousDiscussionAgentRunner{response: DiscussionAgentResponse{
		AssistantMessage: "收到",
		Status:           "completed",
		RuntimeKind:      "codex",
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		InitialAgentMembers: []InitialAgentMemberSeed{{
			AgentID: "coordinator-agent",
			Role:    "Coordinator",
		}},
		Clock:       fixedClock{now: testTime},
		IDGenerator: fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	_, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "启动项目", IdempotencyKey: "send-configured-run-agent"})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("SendMessage error = %v, want ErrInvalidRequest", err)
	}
	if runner.called {
		t.Fatal("runner should not execute when it does not expose a stable agent identity")
	}
	if _, getErr := repo.GetConversation(context.Background(), "conv-1"); !errors.Is(getErr, ErrNotFound) {
		t.Fatalf("conversation should not be saved without runner identity, get error = %v", getErr)
	}
}

func TestServiceSendMessageRequiresConfiguredRunAgent(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &anonymousDiscussionAgentRunner{response: DiscussionAgentResponse{AssistantMessage: "should not run"}}
	service := newTestService(ServiceOptions{
		Repository:          repo,
		DiscussionAgent:     runner,
		InitialAgentMembers: []InitialAgentMemberSeed{},
		Clock:               fixedClock{now: testTime},
		IDGenerator:         fixedIDGen("conv-1"),
	})

	_, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "启动项目", IdempotencyKey: "send-missing-run-agent"})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("SendMessage error = %v, want ErrInvalidRequest", err)
	}
	if runner.called {
		t.Fatal("runner should not execute without a configured run agent")
	}
	if _, getErr := repo.GetConversation(context.Background(), "conv-1"); !errors.Is(getErr, ErrNotFound) {
		t.Fatalf("conversation should not be saved without configured run agent, get error = %v", getErr)
	}
}

func TestServiceSendMessageFailsNewConversationWhenDefaultMembersMissing(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "should not run"}}
	service := NewService(ServiceOptions{
		Repository:          repo,
		DiscussionAgent:     runner,
		InitialAgentMembers: defaultTestConversationAgentMembers(),
		Clock:               fixedClock{now: testTime},
		IDGenerator:         fixedIDGen("conv-1"),
		AgentDirectory: &testAgentDirectory{entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID:  "lead-agent",
			AgentInstanceID: "lead-agent",
			DisplayName:     "顾明澈",
			RealName:        "顾明澈",
			AgentKind:       agentcore.AgentKindAI,
			Enabled:         true,
		}}},
		HumanProfiles: &testHumanProfiles{},
	})

	_, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "现在这个讨论组都有谁", IdempotencyKey: "send-members"})
	if !errors.Is(err, ErrConversationMember) {
		t.Fatalf("SendMessage error = %v, want ErrConversationMember", err)
	}
	if !strings.Contains(err.Error(), "no available agent for discussion role Staffing in personnel directory") {
		t.Fatalf("SendMessage error = %v, want unavailable discussion member message", err)
	}
	if runner.called {
		t.Fatal("discussion agent runner should not run when default members cannot be resolved")
	}
	if _, getErr := repo.GetConversation(context.Background(), "conv-1"); !errors.Is(getErr, ErrNotFound) {
		t.Fatalf("conversation should not be saved on member resolution failure, get error = %v", getErr)
	}
}

func TestServiceSendMessageFailsNewConversationWhenDefaultAgentLacksProfileIdentity(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "should not run"}}
	service := NewService(ServiceOptions{
		Repository:          repo,
		DiscussionAgent:     runner,
		InitialAgentMembers: []InitialAgentMemberSeed{{AgentID: "lead-instance", Role: "Lead"}},
		Clock:               fixedClock{now: testTime},
		IDGenerator:         fixedIDGen("conv-1", "msg-user", "run-1", "msg-agent"),
		AgentDirectory: &testAgentDirectory{entries: []agentcore.AgentDirectoryEntry{{
			AgentInstanceID: "lead-instance",
			DisplayName:     "顾明澈",
			RealName:        "顾明澈",
			AgentKind:       agentcore.AgentKindAI,
			Enabled:         true,
		}}},
		HumanProfiles: &testHumanProfiles{},
	})

	_, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "现在这个讨论组都有谁", IdempotencyKey: "send-members"})
	if !errors.Is(err, ErrConversationMember) {
		t.Fatalf("SendMessage error = %v, want ErrConversationMember", err)
	}
	if runner.called {
		t.Fatal("runner should not run when default member lacks profile identity")
	}
	if _, getErr := repo.GetConversation(context.Background(), "conv-1"); !errors.Is(getErr, ErrNotFound) {
		t.Fatalf("conversation should not be saved on member identity failure, get error = %v", getErr)
	}
}

func TestServiceSendMessageFailsNewConversationWhenDiscussionAgentUnavailable(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &capturingDiscussionAgentRunner{agentID: "lead-agent", response: DiscussionAgentResponse{AssistantMessage: "should not run"}}
	service := NewService(ServiceOptions{
		Repository:          repo,
		DiscussionAgent:     runner,
		InitialAgentMembers: defaultTestConversationAgentMembers(),
		Clock:               fixedClock{now: testTime},
		IDGenerator:         fixedIDGen("conv-1"),
		AgentDirectory:      &testAgentDirectory{entries: []agentcore.AgentDirectoryEntry{}},
		HumanProfiles:       &testHumanProfiles{},
	})

	_, err := service.SendMessage(context.Background(), ViewerRef{ViewerID: "local-user", ViewerKind: ViewerKindUser, DisplayName: "本地用户"}, SendMessageCommand{Content: "现在这个讨论组都有谁", IdempotencyKey: "send-members"})
	if !errors.Is(err, ErrConversationMember) {
		t.Fatalf("SendMessage error = %v, want ErrConversationMember", err)
	}
	if !strings.Contains(err.Error(), "no available agent for discussion role Lead in personnel directory") {
		t.Fatalf("SendMessage error = %v, want unavailable discussion member message", err)
	}
	if runner.called {
		t.Fatal("discussion runner should not run when configured member cannot be resolved")
	}
	if _, getErr := repo.GetConversation(context.Background(), "conv-1"); !errors.Is(getErr, ErrNotFound) {
		t.Fatalf("conversation should not be saved on member resolution failure, get error = %v", getErr)
	}
}
