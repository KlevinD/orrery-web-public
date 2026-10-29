package missioncontrol

import (
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestMentionAgentPromptIncludesDiscussionMembersAsMarkdown(t *testing.T) {
	view := mentionView()
	view.Team.Members = []TeamMemberDTO{
		{AgentID: mentionTestLeadAgentID, AgentProfileID: mentionTestLeadAgentID, AgentInstanceID: mentionTestLeadAgentID, AgentKind: "ai", DisplayName: "Lead", RoleName: "lead", MissionRole: "Lead", Status: "active"},
		{AgentID: mentionTestStaffingAgentID, AgentProfileID: mentionTestStaffingAgentID, AgentInstanceID: mentionTestStaffingAgentID, AgentKind: "ai", DisplayName: "Staffing", RoleName: "staffing", MissionRole: "Staffing", Status: "active"},
	}
	parent := MessageDTO{
		MessageID:         "message-human",
		DiscussionGroupID: "discussion-1",
		AuthorID:          "local-user",
		AuthorDisplayName: "本地用户",
		AuthorType:        "user",
		Body:              "<status>&谁在群里？",
	}
	view.Discussion.Messages = append(view.Discussion.Messages, parent)

	prompt := mentionAgentPrompt(mentionProfile(mentionTestLeadAgentID), view, parent)

	for _, want := range []string{
		"## 当前提及任务",
		"你在讨论组中被 @ 提及",
		"## 当前讨论组",
		"当前讨论组已绑定 Mission",
		"已绑定 Mission：",
		"当前讨论组成员：",
		"- @Lead：agent agent-lead，身份 Lead，状态 active，来源 mission_team，通知链接 [@Lead](mention://agent/agent-lead)",
		"- @Staffing：agent agent-staffing，身份 Staffing，状态 active，来源 mission_team，通知链接 [@Staffing](mention://agent/agent-staffing)",
		"- @本地用户：user local-user，来源 discussion_author，通知链接 [@本地用户](mention://user/local-user)",
		"[@Lead](mention://agent/agent-lead)",
		"[@本地用户](mention://user/local-user)",
		"注意：普通提及时只写 @显示名",
		"## 讨论消息",
		"<status>&谁在群里？",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, unexpected := range []string{
		"<system>",
		"<current_discussion_group>",
		"<linked_missions>",
		"<discussion_members",
		"<notification_mentions>",
		"<display>",
		"<discussion_messages>",
		"<mission_discussion_prompt>",
		"<persona>",
		"<profile_prompt>",
		"<resources>",
	} {
		if strings.Contains(prompt, unexpected) {
			t.Fatalf("prompt should not duplicate role/resource section %q:\n%s", unexpected, prompt)
		}
	}
}

func TestMentionAgentPromptMapsHumanTeamMembersToUserMentions(t *testing.T) {
	view := mentionView()
	view.Team.Members = []TeamMemberDTO{{
		AgentID:         "profile-local-user",
		AgentProfileID:  "profile-local-user",
		AgentInstanceID: "run-local-user",
		AgentKind:       "human",
		DisplayName:     "本地用户",
		RoleName:        "Contributor",
		MissionRole:     "Contributor",
		Status:          "active",
	}}
	parent := MessageDTO{
		MessageID:         "message-human",
		DiscussionGroupID: "discussion-1",
		AuthorID:          "local-user",
		AuthorDisplayName: "本地用户",
		AuthorType:        "user",
		Body:              "[@Lead](mention://agent/agent-lead) 请看一下",
	}
	view.Discussion.Messages = []MessageDTO{parent}

	prompt := mentionAgentPrompt(mentionProfile(mentionTestLeadAgentID), view, parent)

	for _, want := range []string{
		"当前讨论组成员：",
		"- @本地用户：user local-user，身份 Contributor，状态 active，来源 mission_team，通知链接 [@本地用户](mention://user/local-user)",
		"[@本地用户](mention://user/local-user)",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "mention://agent/human-local-user") {
		t.Fatalf("human team member should not be rendered as an agent mention:\n%s", prompt)
	}
}

func TestMentionAgentPromptDoesNotDuplicateBoundResourcesInUserMessage(t *testing.T) {
	bindings := agentcore.AgentBindings{
		Tools: []agentcore.ToolBinding{{
			ToolID:             "repo.write",
			Summary:            "修改工作区文件",
			GrantedPermissions: []string{"repo.write"},
		}},
		Skills: []agentcore.SkillBinding{{
			SkillID: "mission-planning",
			Summary: "拆解 Mission 和 Task",
		}},
		Memory: agentcore.MemoryBinding{
			PersonalDigest: agentcore.MemoryDigestRef{ID: "mem-personal", MemoryKind: agentcore.MemoryKindPersonal, SummaryRef: "用户偏好中文总结"},
			CollectiveDigests: []agentcore.MemoryDigestRef{{
				ID:         "mem-team",
				MemoryKind: agentcore.MemoryKindCollective,
				SummaryRef: "团队约定错误必须显式暴露",
			}},
		},
	}

	prompt := mentionAgentPromptWithResources(mentionProfile(mentionTestLeadAgentID), bindings, mentionView(), MessageDTO{
		MessageID:         "message-1",
		DiscussionGroupID: "discussion-1",
		AuthorID:          "local-user",
		AuthorType:        "user",
		Body:              "请推进",
	})

	for _, want := range []string{
		"## 当前提及任务",
		"## 当前讨论组",
		"## 讨论消息",
		"请推进",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, unexpected := range []string{
		"<available_tools>",
		"repo.write",
		"<loaded_skills>",
		"mission-planning",
		"<long_term_memory>",
		"用户偏好中文总结",
		"团队约定错误必须显式暴露",
		"<external_knowledge>",
	} {
		if strings.Contains(prompt, unexpected) {
			t.Fatalf("user prompt should not duplicate bound resource %q:\n%s", unexpected, prompt)
		}
	}
}
