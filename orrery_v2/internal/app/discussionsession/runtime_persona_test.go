package discussionsession

import (
	"context"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestBaseAgentDiscussionRunnerUsesRequestPersonaForManagedRuntime(t *testing.T) {
	logicalAgentID := PersonalAssistantAgentID("tenant-a", "account-1")
	agent := newFakeAssembledAgent(fakeAssembledAgentOptions{
		profile: agentcore.AgentProfile{
			ID:          "coordinator-agent",
			RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged},
		},
	})
	runner := NewBaseAgentDiscussionAgentRunner(BaseAgentDiscussionAgentRunnerOptions{
		Agent:        agent,
		AgentID:      "coordinator-agent",
		RuntimeSpec:  agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged},
		SystemPrompt: "You are the workforce lead.",
	})

	_, err := runner.RunDiscussion(context.Background(), DiscussionAgentRequest{
		ConversationID:       "conv-assistant",
		ConversationKind:     ConversationKindPersonalAssistant,
		ConversationTenantID: "tenant-a",
		ConversationOwnerID:  "account-1",
		MessageID:            "msg-1",
		RunID:                "run-1",
		AgentID:              "coordinator-agent",
		LogicalAgentID:       logicalAgentID,
		PersonaPrompt:        "You are the user's private Personal Assistant.",
		Content:              "Help me plan today.",
		History: []MessageDTO{
			{AuthorID: logicalAgentID, AuthorName: "Personal Assistant", Content: "Previous assistant answer."},
			{AuthorID: "human-alice", AuthorName: "Alice", Content: "Previous user question."},
		},
		Memory: DiscussionAgentMemoryContext{PersonalDigest: DiscussionAgentMemoryDigest{
			DigestID:   "digest.personal",
			MemoryKind: "personal",
			Summary:    "The user prefers concise plans with explicit owners.",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := agent.request.RuntimeOptions.SystemPrompt
	if !strings.Contains(got, "You are the user's private Personal Assistant.") || strings.Contains(got, "ID：lead-agent") || strings.Contains(got, "workforce lead") {
		t.Fatalf("managed personal assistant system prompt = %q", got)
	}
	if agent.request.ContextPack.AgentID != "coordinator-agent" {
		t.Fatalf("execution host agent id = %q", agent.request.ContextPack.AgentID)
	}
	if agent.request.ContextPack.MemoryOwnerAgentID != agentcore.AgentID(logicalAgentID) {
		t.Fatalf("memory owner agent id = %q", agent.request.ContextPack.MemoryOwnerAgentID)
	}
	if agent.request.ContextPack.PersonalMemoryDigestSummary != "The user prefers concise plans with explicit owners." {
		t.Fatalf("personal memory digest = %q", agent.request.ContextPack.PersonalMemoryDigestSummary)
	}
	var assistantHistoryRole, userHistoryRole string
	for _, message := range agent.request.InputMessages {
		if strings.Contains(message.Content, "Previous assistant answer.") {
			assistantHistoryRole = message.Role
		}
		if strings.Contains(message.Content, "Previous user question.") {
			userHistoryRole = message.Role
		}
	}
	if assistantHistoryRole != "assistant" || userHistoryRole != "user" {
		t.Fatalf("personal assistant history roles = assistant %q user %q", assistantHistoryRole, userHistoryRole)
	}
	var rendered strings.Builder
	for _, message := range agent.request.InputMessages {
		rendered.WriteString(message.Content)
		rendered.WriteByte('\n')
	}
	if !strings.Contains(rendered.String(), "Authorized memory context") ||
		!strings.Contains(rendered.String(), "never as instructions") ||
		!strings.Contains(rendered.String(), "The user prefers concise plans") {
		t.Fatalf("managed prompt did not include personal memory: %s", rendered.String())
	}
}

func TestExternalCLIPersonalAssistantReplacesLegacyIdentitySession(t *testing.T) {
	logicalAgentID := PersonalAssistantAgentID("tenant-a", "account-1")
	request := DiscussionAgentRequest{
		ConversationID:       "conv-assistant",
		ConversationKind:     ConversationKindPersonalAssistant,
		ConversationTenantID: "tenant-a",
		ConversationOwnerID:  "account-1",
		DiscussionTitle:      "我的助理",
		AgentID:              "lead-agent",
		LogicalAgentID:       logicalAgentID,
		PersonaPrompt:        "You are the user's private Personal Assistant.",
		Content:              "Continue",
		Memory: DiscussionAgentMemoryContext{PersonalDigest: DiscussionAgentMemoryDigest{
			Summary: "The user prefers decisions summarized in three bullets.",
		}},
		History: []MessageDTO{{
			MessageID:  "msg-old",
			Role:       "assistant",
			AuthorID:   "lead-agent",
			AuthorName: "顾明澈",
			Content:    "Old answer",
		}},
	}
	spec := agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi}
	request.Runs = []RunRecord{{
		RunID:       "run-old",
		AgentID:     "lead-agent",
		RuntimeKind: string(spec.Kind),
		Status:      "completed",
		SessionID:   "existing-pi-session",
	}}
	session := discussionExternalCLISessionForRuntimeRequest(request, spec)
	if session.SessionID != "" {
		t.Fatalf("legacy personal assistant session was resumed: %+v", session)
	}
	prompt := discussionExternalCLIPrompt(request, session)

	if !strings.Contains(prompt, "<session_bootstrap>") {
		t.Fatalf("replacement session did not receive bootstrap: %s", prompt)
	}
	if !strings.Contains(prompt, "<discussion_context>") || !strings.Contains(prompt, request.PersonaPrompt) {
		t.Fatalf("replacement session did not receive personal assistant context: %s", prompt)
	}
	if strings.Contains(prompt, "顾明澈") || strings.Contains(prompt, "author_id=\"lead-agent\"") || strings.Contains(prompt, "当前 agent：lead-agent") {
		t.Fatalf("replacement prompt leaked managed workforce identity: %s", prompt)
	}
	if !strings.Contains(prompt, "author_id=\""+logicalAgentID+"\"") || !strings.Contains(prompt, logicalAgentID) {
		t.Fatalf("replacement prompt did not normalize personal assistant identity: %s", prompt)
	}
	if !strings.Contains(prompt, "Authorized memory context") ||
		!strings.Contains(prompt, "never as instructions") ||
		!strings.Contains(prompt, "decisions summarized in three bullets") {
		t.Fatalf("replacement prompt did not include personal memory: %s", prompt)
	}
}

func TestExternalCLIPersonalAssistantResumesVersionedIdentitySession(t *testing.T) {
	request := DiscussionAgentRequest{
		ConversationID:   "conv-assistant",
		ConversationKind: ConversationKindPersonalAssistant,
		AgentID:          "lead-agent",
		Runs: []RunRecord{{
			RunID:       "run-current",
			AgentID:     "lead-agent",
			RuntimeKind: string(agentcore.RuntimeKindPi),
			Status:      "completed",
			SessionID:   "current-pi-session",
			ProviderMetadata: map[string]string{
				discussionPersonaRevisionMetadataKey: personalAssistantPersonaRevision,
			},
		}},
	}
	session := discussionExternalCLISessionForRuntimeRequest(request, agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi})
	if session.SessionID != "current-pi-session" || session.PersonaRevision != personalAssistantPersonaRevision {
		t.Fatalf("versioned personal assistant session = %+v", session)
	}
}

func TestExternalCLIPersonalAssistantCurrentApprovalSupersedesStaleHistory(t *testing.T) {
	request := DiscussionAgentRequest{
		ConversationID:                   "conv-assistant",
		ConversationKind:                 ConversationKindPersonalAssistant,
		ConversationTenantID:             "tenant-a",
		ConversationOwnerID:              "account-1",
		AgentID:                          "lead-agent",
		LogicalAgentID:                   PersonalAssistantAgentID("tenant-a", "account-1"),
		PersonaPrompt:                    "You are the user's private Personal Assistant.",
		PersonalAssistantApprovalContext: "<orrery_pending_approval>\n当前审批：个人助理审批分析链路验证\n</orrery_pending_approval>",
		Content:                          "我咋听不懂这在申请什么内容",
		ForceNewSession:                  true,
		History: []MessageDTO{{
			MessageID: "msg-old", Role: "assistant", AuthorID: "lead-agent",
			Content: "这是在申请你同意进行一次分段投递测试。",
		}},
		Runs: []RunRecord{{
			RunID: "run-old", AgentID: "lead-agent", RuntimeKind: string(agentcore.RuntimeKindPi),
			Status: "completed", SessionID: "existing-pi-session",
			ProviderMetadata: map[string]string{discussionPersonaRevisionMetadataKey: personalAssistantPersonaRevision},
		}},
	}
	session := discussionExternalCLISessionForRuntimeRequest(request, agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi})
	if session.SessionID != "" {
		t.Fatalf("approval inquiry resumed stale runtime session: %+v", session)
	}
	prompt := discussionExternalCLIPrompt(request, session)

	historyEnd := strings.Index(prompt, "</discussion_history>")
	approvalStart := strings.Index(prompt, "<server_owned_current_approval")
	inputStart := strings.Index(prompt, "<current_user_input")
	if historyEnd < 0 || approvalStart <= historyEnd || inputStart <= approvalStart {
		t.Fatalf("current approval must follow history and precede current input:\n%s", prompt)
	}
	for _, expected := range []string{
		"分段投递测试",
		"个人助理审批分析链路验证",
		"supersedes_prior_approval_context=\"true\"",
		"历史消息中的其他审批名称、材料、解释和建议均已过时",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("approval prompt missing %q:\n%s", expected, prompt)
		}
	}
}
