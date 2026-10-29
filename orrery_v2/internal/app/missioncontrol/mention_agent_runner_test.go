package missioncontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentassembly "orrery_v2/internal/agent/assembly"
	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	missionprojection "orrery_v2/internal/mission/projection"
	"orrery_v2/internal/platform/execution"
)

const (
	mentionTestLeadAgentID     = "agent-lead"
	mentionTestStaffingAgentID = "agent-staffing"
)

func TestMentionAgentRunnerRepliesToDiscussionMention(t *testing.T) {
	commands := &capturingMentionCommands{}
	assembler := &capturingMentionAssembler{reply: "收到，我来处理。"}
	query := staticMentionQuery{view: MissionControlViewDTO{
		Mission: MissionDTO{MissionID: "mission-1", FinalGoal: "Build a snake game"},
		Team:    TeamDTO{Assignments: []AssignmentDTO{{AgentID: mentionTestLeadAgentID, AgentProfileID: mentionTestLeadAgentID, AgentInstanceID: mentionTestLeadAgentID, AgentKind: "ai", MissionRole: "Lead"}}},
		Discussion: DiscussionDTO{
			DiscussionGroupID: "discussion-1",
			Messages: []MessageDTO{{
				MessageID:         "message-1",
				DiscussionGroupID: "discussion-1",
				AuthorID:          "user-1",
				AuthorType:        "user",
				Body:              "[@Lead](mention://agent/agent-lead) 请处理",
			}},
		},
	}}
	runner := NewMentionAgentRunner(MentionAgentRunnerOptions{
		Assembler: assembler,
		Profiles:  staticMentionProfiles{profile: mentionProfile(mentionTestLeadAgentID)},
		Commands:  commands,
		Query:     query,
		Clock:     staticMentionClock{now: time.Unix(100, 0).UTC()},
	})

	err := runner.HandleExecution(context.Background(), execution.Job{
		ID:    "job-1",
		Topic: MentionDispatchTopic,
		Payload: mustMentionPayload(t, MentionDispatchPayload{
			InboxItemID:       "inbox-1",
			MissionID:         "mission-1",
			DiscussionGroupID: "discussion-1",
			MessageID:         "message-1",
			AgentID:           mentionTestLeadAgentID,
			RequiredResponse:  true,
		}),
	})

	if err != nil {
		t.Fatalf("HandleExecution returned error: %v", err)
	}
	if len(commands.replies) != 1 {
		t.Fatalf("expected one discussion reply, got %#v", commands.replies)
	}
	reply := commands.replies[0]
	if reply.Content != "收到，我来处理。" || reply.InboxItemID != "inbox-1" || reply.MessageID != "message-1" {
		t.Fatalf("unexpected reply command: %#v", reply)
	}
	if assembler.agentID != mentionTestLeadAgentID || assembler.instanceID != mentionTestLeadAgentID {
		t.Fatalf("assembler should load stable agent identity, agent=%s instance=%s", assembler.agentID, assembler.instanceID)
	}
	if len(assembler.permissions) != 0 {
		t.Fatalf("mention runner must not inject mission permissions into agent assembly: %+v", assembler.permissions)
	}
}

func TestMentionAgentRunnerSendsStructuredMentionPromptAsRuntimeUserMessage(t *testing.T) {
	assembler := &capturingMentionAssembler{reply: "收到，我来处理。"}
	runner := NewMentionAgentRunner(MentionAgentRunnerOptions{
		Assembler: assembler,
		Profiles:  staticMentionProfiles{profile: mentionProfile(mentionTestLeadAgentID)},
		Commands:  &capturingMentionCommands{},
		Query:     staticMentionQuery{view: mentionViewWithTeamBinding()},
		Clock:     staticMentionClock{now: time.Unix(100, 0).UTC()},
		Timeout:   7 * time.Minute,
	})

	err := runner.HandleExecution(context.Background(), execution.Job{
		ID:    "job-1",
		Topic: MentionDispatchTopic,
		Payload: mustMentionPayload(t, MentionDispatchPayload{
			InboxItemID:       "inbox-1",
			MissionID:         "mission-1",
			DiscussionGroupID: "discussion-1",
			MessageID:         "message-1",
			AgentID:           mentionTestLeadAgentID,
			RequiredResponse:  true,
		}),
	})

	if err != nil {
		t.Fatalf("HandleExecution returned error: %v", err)
	}
	req := assembler.request
	if req.RuntimeOptions.Timeout != 7*time.Minute {
		t.Fatalf("runtime timeout = %v, want 7m", req.RuntimeOptions.Timeout)
	}
	for _, value := range []string{req.ContextPack.MissionFinalGoal, req.ContextPack.WorkerPersonalGoal} {
		if !strings.Contains(value, "你在讨论组中被 @ 提及") ||
			!strings.Contains(value, "## 当前讨论组") ||
			!strings.Contains(value, "当前讨论组成员：") ||
			!strings.Contains(value, "请处理") {
			t.Fatalf("runtime prompt missing mention discussion context:\n%s", value)
		}
		for _, unexpected := range []string{"<system>", "<current_discussion_group>", "<discussion_messages>", "<notification_mentions>"} {
			if strings.Contains(value, unexpected) {
				t.Fatalf("runtime prompt should use markdown discussion context, found %q:\n%s", unexpected, value)
			}
		}
	}
	if strings.TrimSpace(req.Task.Goal) == "Build a snake game" || strings.TrimSpace(req.ContextPack.MissionFinalGoal) == "Build a snake game" {
		t.Fatalf("runtime user prompt should not collapse to the plain task goal: %+v", req.ContextPack)
	}
	if !hasMentionRunSource(req.ContextPack.SourceRefs) {
		t.Fatalf("runtime context must identify the synthetic mention run: %+v", req.ContextPack.SourceRefs)
	}
}

func hasMentionRunSource(refs []agentcore.ContextSourceRef) bool {
	for _, ref := range refs {
		if ref.Kind == agentcore.ContextSourceKindUserInstruction && ref.Ref.UserInstructionRef != nil &&
			ref.Ref.UserInstructionRef.ID == agentcore.UserInstructionID(MentionAgentRunSource) {
			return true
		}
	}
	return false
}

func TestMentionAgentRunnerCreatesCanonicalPerRunWorkspace(t *testing.T) {
	root := filepath.Join(t.TempDir(), "mention-runtime")
	assembler := &capturingMentionAssembler{reply: "done"}
	runner := NewMentionAgentRunner(MentionAgentRunnerOptions{
		Assembler:     assembler,
		Profiles:      staticMentionProfiles{profile: mentionProfile(mentionTestLeadAgentID)},
		Commands:      &capturingMentionCommands{},
		Query:         staticMentionQuery{view: mentionViewWithTeamBinding()},
		Clock:         staticMentionClock{now: time.Unix(100, 0).UTC()},
		WorkspaceRoot: root,
	})

	err := runner.HandleExecution(context.Background(), execution.Job{
		ID: "job-workspace", Topic: MentionDispatchTopic,
		Payload: mustMentionPayload(t, MentionDispatchPayload{
			InboxItemID: "inbox-1", MissionID: "mission-1", DiscussionGroupID: "discussion-1",
			MessageID: "message-1", AgentID: mentionTestLeadAgentID, RequiredResponse: true,
		}),
	})
	if err != nil {
		t.Fatalf("HandleExecution: %v", err)
	}
	options := assembler.request.RuntimeOptions
	if options.WorkingDirectory == "" || options.WorkingDirectory != options.WorkspacePolicy.WorkDir || !filepath.IsAbs(options.WorkingDirectory) {
		t.Fatalf("mention runtime workspace = %+v", options)
	}
	if options.WorkspacePolicy.Root != filepath.Clean(root) || !options.WorkspacePolicy.EnforceBoundary || options.SandboxProfile.WorkDir != options.WorkingDirectory {
		t.Fatalf("mention runtime workspace policy = %+v", options)
	}
	if info, err := os.Stat(options.WorkingDirectory); err != nil || !info.IsDir() {
		t.Fatalf("mention runtime workspace directory missing: info=%v err=%v", info, err)
	}
}

func TestMentionAgentRunnerFallbackViewUsesReadOnlyMentionViewer(t *testing.T) {
	commands := &capturingMentionCommands{}
	reader := &capturingMentionEvents{events: []missionevent.Envelope{{
		EventID:       "event-message",
		MissionID:     "mission-1",
		EventType:     missionevent.EventMessagePosted,
		StreamVersion: 1,
		OccurredAt:    time.Unix(100, 0).UTC(),
		Payload: missionevent.MessagePostedPayload{
			MessageID:         "message-1",
			DiscussionGroupID: "discussion-1",
			AuthorID:          "user-1",
			AuthorType:        "user",
			Body:              "[@Lead](mention://agent/agent-lead) 请处理",
			Mentions:          []agentcore.AgentID{mentionTestLeadAgentID},
			Visibility:        []string{"mission"},
		},
	}}}
	runner := NewMentionAgentRunner(MentionAgentRunnerOptions{
		Assembler:   &capturingMentionAssembler{reply: "收到，我来处理。"},
		Profiles:    staticMentionProfiles{profile: mentionProfile(mentionTestLeadAgentID)},
		Commands:    commands,
		Query:       staticMentionQuery{view: mentionViewWithTeamBinding()},
		EventReader: reader,
		Clock:       staticMentionClock{now: time.Unix(100, 0).UTC()},
	})

	err := runner.HandleExecution(context.Background(), execution.Job{
		ID:    "job-1",
		Topic: MentionDispatchTopic,
		Payload: mustMentionPayload(t, MentionDispatchPayload{
			InboxItemID:       "inbox-1",
			MissionID:         "mission-1",
			DiscussionGroupID: "discussion-1",
			MessageID:         "message-1",
			AgentID:           mentionTestLeadAgentID,
			RequiredResponse:  true,
		}),
	})

	if err != nil {
		t.Fatalf("HandleExecution returned error: %v", err)
	}
	if len(commands.replies) != 1 {
		t.Fatalf("expected one discussion reply, got %#v", commands.replies)
	}
	if got := runner.mentionViewer("global", mentionTestLeadAgentID).PermissionScope; strings.Join(got, ",") != "mission.read,discussion.write" {
		t.Fatalf("mention viewer permission scope = %+v, want mission read and discussion write scopes", got)
	}
}

func TestMentionAgentRunnerResolvesProfileFromTeamBinding(t *testing.T) {
	commands := &capturingMentionCommands{}
	assembler := &capturingMentionAssembler{reply: "收到，我来处理。"}
	profiles := &capturingMentionProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		mentionTestLeadAgentID: mentionProfile(mentionTestLeadAgentID),
	}}
	query := staticMentionQuery{view: MissionControlViewDTO{
		Mission: MissionDTO{MissionID: "mission-1", FinalGoal: "Build a snake game"},
		Team: TeamDTO{
			Members: []TeamMemberDTO{{AgentID: mentionTestLeadAgentID, AgentProfileID: mentionTestLeadAgentID, AgentInstanceID: mentionTestLeadAgentID, AgentKind: "ai", DisplayName: "Lead", MissionRole: "Lead"}},
			Assignments: []AssignmentDTO{{
				AssignmentID: "assignment-lead", AgentID: mentionTestLeadAgentID, AgentProfileID: mentionTestLeadAgentID, AgentInstanceID: mentionTestLeadAgentID, AgentKind: "ai", MissionRole: "Lead",
			}},
		},
		Discussion: DiscussionDTO{
			DiscussionGroupID: "discussion-1",
			Messages: []MessageDTO{{
				MessageID:         "message-1",
				DiscussionGroupID: "discussion-1",
				AuthorID:          "user-1",
				AuthorType:        "user",
				Body:              "[@Lead](mention://agent/agent-lead) 请处理",
			}},
		},
	}}
	runner := NewMentionAgentRunner(MentionAgentRunnerOptions{
		Assembler: assembler,
		Profiles:  profiles,
		Commands:  commands,
		Query:     query,
		Clock:     staticMentionClock{now: time.Unix(100, 0).UTC()},
	})

	err := runner.HandleExecution(context.Background(), execution.Job{
		ID:    "job-1",
		Topic: MentionDispatchTopic,
		Payload: mustMentionPayload(t, MentionDispatchPayload{
			InboxItemID:       "inbox-1",
			MissionID:         "mission-1",
			DiscussionGroupID: "discussion-1",
			MessageID:         "message-1",
			AgentID:           mentionTestLeadAgentID,
			RequiredResponse:  true,
		}),
	})

	if err != nil {
		t.Fatalf("HandleExecution returned error: %v", err)
	}
	if profiles.requested != mentionTestLeadAgentID || assembler.agentID != mentionTestLeadAgentID || assembler.instanceID != mentionTestLeadAgentID {
		t.Fatalf("mention runner should load stable profile identity from team binding, requested=%s agent=%s instance=%s", profiles.requested, assembler.agentID, assembler.instanceID)
	}
	if len(commands.replies) != 1 {
		t.Fatalf("expected one discussion reply, got %#v", commands.replies)
	}
}

func TestMentionAgentRunnerRejectsRuntimeInstanceTeamBinding(t *testing.T) {
	commands := &capturingMentionCommands{}
	assembler := &capturingMentionAssembler{reply: "收到，我来处理。"}
	profiles := &capturingMentionProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		mentionTestLeadAgentID: mentionProfile(mentionTestLeadAgentID),
	}}
	query := staticMentionQuery{view: MissionControlViewDTO{
		Mission: MissionDTO{MissionID: "mission-1", FinalGoal: "Build a snake game"},
		Team: TeamDTO{
			Members: []TeamMemberDTO{{AgentID: mentionTestLeadAgentID, AgentProfileID: mentionTestLeadAgentID, AgentInstanceID: "run-42", AgentKind: "ai", DisplayName: "Lead", MissionRole: "Lead"}},
			Assignments: []AssignmentDTO{{
				AssignmentID: "assignment-lead", AgentID: mentionTestLeadAgentID, AgentProfileID: mentionTestLeadAgentID, AgentInstanceID: "run-42", AgentKind: "ai", MissionRole: "Lead",
			}},
		},
		Discussion: DiscussionDTO{
			DiscussionGroupID: "discussion-1",
			Messages: []MessageDTO{{
				MessageID:         "message-1",
				DiscussionGroupID: "discussion-1",
				AuthorID:          "user-1",
				AuthorType:        "user",
				Body:              "[@Lead](mention://agent/agent-lead) 请处理",
			}},
		},
	}}
	runner := NewMentionAgentRunner(MentionAgentRunnerOptions{
		Assembler: assembler,
		Profiles:  profiles,
		Commands:  commands,
		Query:     query,
		Clock:     staticMentionClock{now: time.Unix(100, 0).UTC()},
	})

	err := runner.HandleExecution(context.Background(), execution.Job{
		ID:    "job-1",
		Topic: MentionDispatchTopic,
		Payload: mustMentionPayload(t, MentionDispatchPayload{
			InboxItemID:       "inbox-1",
			MissionID:         "mission-1",
			DiscussionGroupID: "discussion-1",
			MessageID:         "message-1",
			AgentID:           mentionTestLeadAgentID,
			RequiredResponse:  true,
		}),
	})

	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("HandleExecution error = %v, want ErrInvalidRequest", err)
	}
	if profiles.requested != "" || assembler.agentID != "" || assembler.instanceID != "" || len(commands.replies) != 0 {
		t.Fatalf("runtime instance team binding should be rejected before profile load or reply, requested=%s agent=%s instance=%s replies=%#v", profiles.requested, assembler.agentID, assembler.instanceID, commands.replies)
	}
}

func TestMentionAgentRunnerRejectsRuntimeInstancePayloadAgentID(t *testing.T) {
	runner := NewMentionAgentRunner(MentionAgentRunnerOptions{
		Assembler: &capturingMentionAssembler{reply: "收到，我来处理。"},
		Profiles: &capturingMentionProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			mentionTestLeadAgentID: mentionProfile(mentionTestLeadAgentID),
		}},
		Commands: &capturingMentionCommands{},
		Query: staticMentionQuery{view: MissionControlViewDTO{
			Mission: MissionDTO{MissionID: "mission-1", FinalGoal: "Build a snake game"},
			Team: TeamDTO{
				Members: []TeamMemberDTO{{AgentID: mentionTestLeadAgentID, AgentProfileID: mentionTestLeadAgentID, AgentInstanceID: "run-42", AgentKind: "ai", MissionRole: "Lead"}},
				Assignments: []AssignmentDTO{{
					AssignmentID: "assignment-lead", AgentID: mentionTestLeadAgentID, AgentProfileID: mentionTestLeadAgentID, AgentInstanceID: "run-42", AgentKind: "ai", MissionRole: "Lead",
				}},
			},
			Discussion: DiscussionDTO{
				DiscussionGroupID: "discussion-1",
				Messages: []MessageDTO{{
					MessageID:         "message-1",
					DiscussionGroupID: "discussion-1",
					AuthorID:          "user-1",
					AuthorType:        "user",
					Body:              "[@Lead](mention://agent/agent-lead) 请处理",
				}},
			},
		}},
		Clock: staticMentionClock{now: time.Unix(100, 0).UTC()},
	})

	err := runner.HandleExecution(context.Background(), execution.Job{
		ID:    "job-1",
		Topic: MentionDispatchTopic,
		Payload: mustMentionPayload(t, MentionDispatchPayload{
			InboxItemID:       "inbox-1",
			MissionID:         "mission-1",
			DiscussionGroupID: "discussion-1",
			MessageID:         "message-1",
			AgentID:           "run-42",
			RequiredResponse:  true,
		}),
	})

	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("HandleExecution error = %v, want ErrInvalidRequest", err)
	}
}

func TestMentionAgentIdentityRejectsBindingStableIDDifferentFromProfile(t *testing.T) {
	_, err := mentionAgentIdentityForBinding("alias-agent", "profile-agent", "profile-agent")

	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("mentionAgentIdentityForBinding error = %v, want ErrInvalidRequest", err)
	}
}

func TestMentionAgentIdentityRejectsRuntimeInstanceBinding(t *testing.T) {
	_, err := mentionAgentIdentityForBinding("profile-agent", "profile-agent", "profile-agent:mission-1")

	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("mentionAgentIdentityForBinding error = %v, want ErrInvalidRequest", err)
	}
}

func TestMentionAgentRunnerRecordsFailedEventWithProjectionDetails(t *testing.T) {
	expected := errors.New("managed llm unavailable")
	events := &capturingMentionEvents{}
	repo := &capturingMentionProjectionRepo{model: missionprojection.NewReadModel(missioncore.MissionID("mission-1"))}
	runner := NewMentionAgentRunner(MentionAgentRunnerOptions{
		Assembler:   failingMentionAssembler{err: expected},
		Profiles:    staticMentionProfiles{profile: mentionProfile(mentionTestLeadAgentID)},
		Commands:    &capturingMentionCommands{},
		Query:       staticMentionQuery{view: mentionViewWithTeamBinding()},
		Repo:        repo,
		Events:      events,
		Clock:       staticMentionClock{now: time.Unix(100, 0).UTC()},
		IDGenerator: staticMentionIDGenerator{id: "evt-1"},
	})

	err := runner.HandleExecution(context.Background(), execution.Job{
		ID:          "job-1",
		Topic:       MentionDispatchTopic,
		Attempts:    1,
		MaxAttempts: 3,
		Payload: mustMentionPayload(t, MentionDispatchPayload{
			InboxItemID:       "inbox-1",
			MissionID:         "mission-1",
			DiscussionGroupID: "discussion-1",
			MessageID:         "message-1",
			AgentID:           mentionTestLeadAgentID,
			RequiredResponse:  true,
		}),
	})

	if !errors.Is(err, expected) {
		t.Fatalf("HandleExecution error = %v, want %v", err, expected)
	}
	if len(events.events) != 1 {
		t.Fatalf("expected one failure event, got %#v", events.events)
	}
	event := events.events[0]
	if event.EventType != missionevent.EventAgentRunFailed || event.StreamVersion != 1 {
		t.Fatalf("unexpected failure event: %#v", event)
	}
	if len(repo.saved.ProgressGraph.Nodes) != 1 {
		t.Fatalf("failure event should refresh projection progress, got %#v", repo.saved.ProgressGraph.Nodes)
	}
	node := repo.saved.ProgressGraph.Nodes[0]
	if node.Status != "failed" || node.Details != "managed llm unavailable" {
		t.Fatalf("failure progress node missing details: %#v", node)
	}
}

func TestMentionAgentRunnerKeepsTenantOnQueryRuntimeAndReply(t *testing.T) {
	assembler := &capturingMentionAssembler{reply: "tenant reply"}
	query := &capturingTenantMentionQuery{view: mentionViewWithTeamBinding()}
	commands := &capturingMentionCommands{}
	runner := NewMentionAgentRunner(MentionAgentRunnerOptions{
		Assembler: assembler,
		Profiles:  staticMentionProfiles{profile: mentionProfile(mentionTestLeadAgentID)},
		Commands:  commands,
		Query:     query,
		Clock:     staticMentionClock{now: time.Unix(100, 0).UTC()},
	})

	err := runner.HandleExecution(context.Background(), execution.Job{
		ID:    "job-tenant-a",
		Topic: MentionDispatchTopic,
		Payload: mustMentionPayload(t, MentionDispatchPayload{
			TenantID:          "tenant-a",
			InboxItemID:       "inbox-tenant-a",
			MissionID:         "mission-1",
			DiscussionGroupID: "discussion-1",
			MessageID:         "message-1",
			AgentID:           mentionTestLeadAgentID,
			RequiredResponse:  true,
		}),
	})
	if err != nil {
		t.Fatalf("HandleExecution: %v", err)
	}
	if query.viewer.Tenant() != "tenant-a" {
		t.Fatalf("query viewer tenant = %q", query.viewer.Tenant())
	}
	if assembler.request.TenantID != "tenant-a" {
		t.Fatalf("runtime input tenant = %q", assembler.request.TenantID)
	}
	if len(commands.viewers) != 1 || commands.viewers[0].Tenant() != "tenant-a" {
		t.Fatalf("reply viewers = %+v", commands.viewers)
	}
}

func TestMentionAgentRunnerFailureRefreshesOnlyPayloadTenant(t *testing.T) {
	expected := errors.New("managed llm unavailable")
	ctx := context.Background()
	repo := NewMemoryProjectionRepository()
	for _, tenantID := range []string{"tenant-a", "tenant-b"} {
		model := missionprojection.NewReadModel("mission-1")
		model.TenantID = tenantID
		model.Mission.TenantID = tenantID
		if err := repo.SaveForTenant(ctx, tenantID, *model); err != nil {
			t.Fatalf("SaveForTenant(%s): %v", tenantID, err)
		}
	}
	events := &capturingMentionEvents{}
	runner := NewMentionAgentRunner(MentionAgentRunnerOptions{
		Assembler:   failingMentionAssembler{err: expected},
		Profiles:    staticMentionProfiles{profile: mentionProfile(mentionTestLeadAgentID)},
		Commands:    &capturingMentionCommands{},
		Query:       staticMentionQuery{view: mentionViewWithTeamBinding()},
		Repo:        repo,
		Events:      events,
		Clock:       staticMentionClock{now: time.Unix(100, 0).UTC()},
		IDGenerator: staticMentionIDGenerator{id: "evt-tenant-a"},
	})

	err := runner.HandleExecution(ctx, execution.Job{
		ID:    "job-tenant-a",
		Topic: MentionDispatchTopic,
		Payload: mustMentionPayload(t, MentionDispatchPayload{
			TenantID: "tenant-a", InboxItemID: "inbox-1", MissionID: "mission-1",
			DiscussionGroupID: "discussion-1", MessageID: "message-1", AgentID: mentionTestLeadAgentID, RequiredResponse: true,
		}),
	})
	if !errors.Is(err, expected) {
		t.Fatalf("HandleExecution error = %v, want %v", err, expected)
	}
	if len(events.events) != 1 || events.events[0].Tenant() != "tenant-a" || events.events[0].StreamID != "tenant-a/mission-1" {
		t.Fatalf("failure events = %+v", events.events)
	}
	updatedA, err := repo.GetMissionControlForTenant(ctx, "tenant-a", "mission-1")
	if err != nil || len(updatedA.ProgressGraph.Nodes) != 1 {
		t.Fatalf("tenant-a projection = %+v, err=%v", updatedA, err)
	}
	updatedB, err := repo.GetMissionControlForTenant(ctx, "tenant-b", "mission-1")
	if err != nil || len(updatedB.ProgressGraph.Nodes) != 0 {
		t.Fatalf("tenant-b projection = %+v, err=%v", updatedB, err)
	}
}

func mentionView() MissionControlViewDTO {
	return MissionControlViewDTO{
		Mission: MissionDTO{MissionID: "mission-1", FinalGoal: "Build a snake game"},
		Discussion: DiscussionDTO{
			DiscussionGroupID: "discussion-1",
			Messages: []MessageDTO{{
				MessageID:         "message-1",
				DiscussionGroupID: "discussion-1",
				Body:              "[@Lead](mention://agent/agent-lead) 请处理",
			}},
		},
	}
}

func mentionViewWithTeamBinding() MissionControlViewDTO {
	view := mentionView()
	view.Team = TeamDTO{
		Members: []TeamMemberDTO{{AgentID: mentionTestLeadAgentID, AgentProfileID: mentionTestLeadAgentID, AgentInstanceID: mentionTestLeadAgentID, AgentKind: "ai", MissionRole: "Lead"}},
		Assignments: []AssignmentDTO{{
			AssignmentID: "assignment-lead", AgentID: mentionTestLeadAgentID, AgentProfileID: mentionTestLeadAgentID, AgentInstanceID: mentionTestLeadAgentID, AgentKind: "ai", MissionRole: "Lead",
		}},
	}
	return view
}

func mentionProfile(id string) agentcore.AgentProfile {
	return agentcore.AgentProfile{
		ID:          agentcore.AgentProfileID(id),
		DisplayName: "Lead",
		RealName:    "Lead",
		RoleName:    "lead",
		AgentKind:   agentcore.AgentKindAI,
		BaseAgent:   agentcore.BaseAgentSpec{SystemPrompt: "你是 Lead Agent"},
	}
}

func mustMentionPayload(t *testing.T, payload MentionDispatchPayload) []byte {
	t.Helper()
	data, err := encodeMentionPayload(payload)
	if err != nil {
		t.Fatalf("encode payload: %v", err)
	}
	return data
}

func encodeMentionPayload(payload MentionDispatchPayload) ([]byte, error) {
	return json.Marshal(payload)
}

type staticMentionAssembler struct {
	reply string
}

func (a staticMentionAssembler) Build(_ context.Context, profile agentcore.AgentProfile, opts agentassembly.BuildOptions) (agentcore.AgentInstance, error) {
	base := agentcore.NewBaseAgent(agentcore.BaseAgentConfig{
		Identity: agentcore.AgentIdentity{AgentID: opts.AgentID, AgentProfileID: profile.ID, InstanceRef: agentcore.AgentInstanceRef{ID: opts.InstanceID}},
		Profile:  profile,
		Runtime:  staticMentionRuntime{reply: a.reply},
		Delivery: noopMentionDelivery{},
		Trace:    agentcore.NoopTraceRecorder{},
		Notifier: agentcore.NoopNotifier{},
	})
	return base, nil
}

type capturingMentionAssembler struct {
	reply       string
	agentID     agentcore.AgentID
	instanceID  agentcore.AgentInstanceID
	permissions []string
	request     agentcore.AgentInput
}

func (a *capturingMentionAssembler) Build(ctx context.Context, profile agentcore.AgentProfile, opts agentassembly.BuildOptions) (agentcore.AgentInstance, error) {
	a.agentID = opts.AgentID
	a.instanceID = opts.InstanceID
	a.permissions = append([]string(nil), opts.Permissions...)
	base := agentcore.NewBaseAgent(agentcore.BaseAgentConfig{
		Identity: agentcore.AgentIdentity{AgentID: opts.AgentID, AgentProfileID: profile.ID, InstanceRef: agentcore.AgentInstanceRef{ID: opts.InstanceID}},
		Profile:  profile,
		Runtime:  capturingMentionRuntime{reply: a.reply, request: &a.request},
		Delivery: noopMentionDelivery{},
		Trace:    agentcore.NoopTraceRecorder{},
		Notifier: agentcore.NoopNotifier{},
	})
	return base, nil
}

type failingMentionAssembler struct {
	err error
}

func (a failingMentionAssembler) Build(context.Context, agentcore.AgentProfile, agentassembly.BuildOptions) (agentcore.AgentInstance, error) {
	return nil, a.err
}

type staticMentionProfiles struct {
	profile agentcore.AgentProfile
}

func (p staticMentionProfiles) GetProfile(context.Context, agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	return p.profile, nil
}

type capturingMentionProfiles struct {
	profiles  map[agentcore.AgentProfileID]agentcore.AgentProfile
	requested agentcore.AgentProfileID
}

func (p *capturingMentionProfiles) GetProfile(_ context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	p.requested = id
	profile, ok := p.profiles[id]
	if !ok {
		return agentcore.AgentProfile{}, fmt.Errorf("profile %s not found", id)
	}
	return profile, nil
}

type staticMentionQuery struct {
	view MissionControlViewDTO
}

func (q staticMentionQuery) GetMissionControlView(context.Context, ViewerRef, missioncore.MissionID) (MissionControlViewDTO, error) {
	return q.view, nil
}

type capturingTenantMentionQuery struct {
	view   MissionControlViewDTO
	viewer ViewerRef
}

func (q *capturingTenantMentionQuery) GetMissionControlView(_ context.Context, viewer ViewerRef, _ missioncore.MissionID) (MissionControlViewDTO, error) {
	q.viewer = viewer
	return q.view, nil
}

type capturingMentionCommands struct {
	replies []ReplyDiscussionMessageCommand
	viewers []ViewerRef
}

func (c *capturingMentionCommands) ReplyDiscussionMessage(_ context.Context, viewer ViewerRef, cmd ReplyDiscussionMessageCommand) (CommandReceiptDTO, error) {
	c.replies = append(c.replies, cmd)
	c.viewers = append(c.viewers, viewer)
	return CommandReceiptDTO{CommandID: "reply-1", Status: "accepted", MissionID: cmd.MissionID.String(), AcceptedAt: time.Unix(100, 0).UTC()}, nil
}

type staticMentionRuntime struct {
	mentionSessionControls
	reply string
}

func (r staticMentionRuntime) RunSession(context.Context, agentcore.RuntimeRequest, agentcore.SessionEventObserver) (agentcore.RuntimeResponse, error) {
	return agentcore.RuntimeResponse{
		Status:   agentcore.RuntimeStatusCompleted,
		Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Content: r.reply}},
	}, nil
}

type capturingMentionRuntime struct {
	mentionSessionControls
	reply   string
	request *agentcore.AgentInput
}

func (r capturingMentionRuntime) RunSession(_ context.Context, req agentcore.RuntimeRequest, _ agentcore.SessionEventObserver) (agentcore.RuntimeResponse, error) {
	if r.request != nil {
		*r.request = agentcore.AgentInput{
			RunID:          req.RunID,
			TenantID:       req.TenantID,
			ContextPack:    req.ContextPack,
			Task:           agentcore.TaskInput{ID: req.TaskID, MissionID: req.MissionID, Goal: req.ContextPack.MissionFinalGoal, WorkerPersonalGoal: req.ContextPack.WorkerPersonalGoal},
			RuntimeOptions: req.RuntimeOptions,
		}
	}
	return agentcore.RuntimeResponse{
		Status:   agentcore.RuntimeStatusCompleted,
		Messages: []agentcore.RuntimeMessage{{Type: agentcore.RuntimeMessageText, Content: r.reply}},
	}, nil
}

type mentionSessionControls struct{}

func (mentionSessionControls) CancelSession(context.Context, agentcore.AgentRunID, string) error {
	return nil
}

func (mentionSessionControls) SteerSession(context.Context, agentcore.AgentRunID, string) error {
	return nil
}

func (mentionSessionControls) PrewarmSession(context.Context, agentcore.RuntimeRequest) error {
	return nil
}

func (mentionSessionControls) ReleaseRuntimeWorkspace(context.Context, string) error { return nil }

func (mentionSessionControls) Capabilities(context.Context) agentcore.Capabilities {
	return agentcore.Capabilities{
		Signals:   agentcore.SignalSet{TerminalResult: true},
		Transport: agentcore.TransportEvidence{Provider: "mention-test", Protocol: "canonical-test"},
	}
}

type noopMentionDelivery struct{}

func (noopMentionDelivery) SubmitAgentResult(context.Context, agentcore.AgentResult, agentcore.CommitOptions) error {
	return nil
}

type capturingMentionEvents struct {
	events []missionevent.Envelope
}

func (e *capturingMentionEvents) Append(_ context.Context, event missionevent.Envelope) error {
	e.events = append(e.events, event)
	return nil
}

func (e *capturingMentionEvents) LoadMission(_ context.Context, _ missioncore.MissionID) ([]missionevent.Envelope, error) {
	return append([]missionevent.Envelope(nil), e.events...), nil
}

type capturingMentionProjectionRepo struct {
	model *missionprojection.ReadModel
	saved missionprojection.ReadModel
}

func (r *capturingMentionProjectionRepo) GetMissionControl(context.Context, missioncore.MissionID) (missionprojection.ReadModel, error) {
	if r.model == nil {
		return missionprojection.ReadModel{}, ErrProjectionNotFound
	}
	return *r.model, nil
}

func (r *capturingMentionProjectionRepo) Save(_ context.Context, model missionprojection.ReadModel) error {
	r.saved = model
	return nil
}

type staticMentionClock struct {
	now time.Time
}

func (c staticMentionClock) Now() time.Time {
	return c.now
}

type staticMentionIDGenerator struct {
	id string
}

func (g staticMentionIDGenerator) NewIDContext() (string, error) {
	return g.id, nil
}
