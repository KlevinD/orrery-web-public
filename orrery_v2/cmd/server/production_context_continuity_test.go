package main

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimeproviders "orrery_v2/internal/agent/runtime/providers"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/platform/testkit"
)

func TestProductionMentionRunnerCarriesContinuationRuns(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	seedProductionChatProfile(t, ctx, profiles, "worker", "秦顾问", "Worker")
	profile, err := profiles.GetProfile(ctx, "worker")
	if err != nil {
		t.Fatal(err)
	}
	profile.RuntimeSpec.Kind = agentcore.RuntimeKindPi
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	registry, err := runtimeproviders.NewSessionRegistry(runtimeproviders.SessionRegistryConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	responder := productionDiscussionAgentMentionResponder{profiles: profiles, deps: testProductionDiscussionAgentDependencies(productionDiscussionAgentDependencies{RuntimeRegistry: registry})}
	for _, mode := range []string{"direct", "mention"} {
		t.Run(mode, func(t *testing.T) {
			_, request, err := responder.mentionRunner(ctx, discussionsession.DiscussionAgentMentionRequest{
				ConversationID: "conversation", ConversationKind: discussionsession.ConversationKindGroup,
				ConversationTenantID: "tenant", ConversationOwnerID: "owner", HILMode: discussionsession.DiscussionHILModeManual,
				RunID: "new", Mode: mode, TargetAgent: discussionsession.ConversationMemberDTO{ID: "worker"},
				Runs: []discussionsession.RunRecord{{RunID: "previous", ConversationID: "conversation", AgentID: "worker", RuntimeKind: "pi", SessionID: "previous-session", Status: "completed"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(request.Runs) != 1 || request.Runs[0].SessionID != "previous-session" || request.ConversationTenantID != "tenant" || request.HILMode != discussionsession.DiscussionHILModeManual {
				t.Fatal("production dispatch lost continuation or authorization context")
			}
		})
	}
}

func TestPersonalAssistantApprovalInquiryDoesNotSwallowActionConsent(t *testing.T) {
	for _, content := range []string{
		"我批准你把公司银行开票信息和银行账户存入公司知识库", "我批准，继续执行", "请审批这项任务", "approve this request", "reject this request",
	} {
		if isProductionPersonalAssistantApprovalInquiry(content) {
			t.Errorf("action %q was classified as read-only inquiry", content)
		}
	}
	for _, content := range []string{"审批", "查看审批", "这是什么审批", "同意后会发生什么", "Explain this approval", "Should I approve this?"} {
		if !isProductionPersonalAssistantApprovalInquiry(content) {
			t.Errorf("inquiry %q was not read-only", content)
		}
	}
}
