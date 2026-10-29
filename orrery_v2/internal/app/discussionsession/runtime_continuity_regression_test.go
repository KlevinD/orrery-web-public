package discussionsession

import (
	"context"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimesession "orrery_v2/internal/agent/runtime/session"
)

func TestDiscussionContinuitySurvivesProjectionPersistenceAndNewRunner(t *testing.T) {
	for _, mode := range []string{"personal_assistant", "group", "direct", "mention"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			repo := newMemoryRepository(t)
			view := ConversationRecordView{ConversationRecord: ConversationRecord{
				ConversationID: "conv", ConversationKind: mode, OwnerID: "owner", TenantID: "tenant", CreatedAt: testTime,
			}}
			if mode == "mention" {
				view.ConversationKind = ConversationKindGroup
			}
			if err := repo.SaveConversation(ctx, view.ConversationRecord); err != nil {
				t.Fatal(err)
			}
			viewer := ViewerRef{ViewerID: "human", OwnerID: "owner", TenantID: "tenant", ViewerKind: ViewerKindUser}
			spec := agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi}
			var runtimeRequests []agentcore.RuntimeRequest
			for turn := 1; turn <= 2; turn++ {
				view, _ = repo.GetConversation(ctx, "conv")
				id := "first"
				if turn == 2 {
					id = "second"
				}
				message := MessageRecord{MessageID: id, ConversationID: "conv", AuthorID: "human", Role: "user", Content: id + " current instruction", CreatedAt: testTime.Add(time.Duration(turn) * time.Minute)}
				run := RunRecord{RunID: id, ConversationID: "conv", AgentID: "personal-assistant", CreatedAt: message.CreatedAt}
				request := discussionAgentRequestFromConversation(view, run, viewer, message, mapMessagesWithAttachments(view.Messages, nil))
				if mode == "direct" || mode == "mention" {
					target := ConversationMemberRecord{MemberID: run.AgentID, MemberKind: "agent"}
					request = discussionAgentRequestFromMentionRequest(discussionTargetRequestFromRun(mode, view, message, target, run))
				}
				agent := newFakeAssembledAgent(fakeAssembledAgentOptions{
					profile: agentcore.AgentProfile{ID: "personal-assistant", RuntimeSpec: spec},
					runtimeFactory: func(agent *fakeAssembledAgent) agentcore.SessionRuntime {
						return &continuityProjectionRuntime{fakeAssembledRuntime: fakeAssembledRuntime{agent: agent}, at: message.CreatedAt}
					},
				})
				runner := NewBaseAgentDiscussionAgentRunner(BaseAgentDiscussionAgentRunnerOptions{Agent: agent, AgentID: run.AgentID, RuntimeSpec: spec})
				response, err := runner.RunDiscussion(ctx, request)
				if err != nil {
					t.Fatal(err)
				}
				runtimeRequests = append(runtimeRequests, agent.request)
				run.LogicalAgentID = request.LogicalAgentID
				run.ProviderMetadata, run.SessionID = response.ProviderMetadata, response.SessionID
				run.RuntimeKind, run.Status, run.CompletedAt = "pi", "completed", message.CreatedAt.Add(time.Second)
				if err := repo.SaveRun(ctx, run); err != nil {
					t.Fatal(err)
				}
				if err := repo.SaveMessage(ctx, message); err != nil {
					t.Fatal(err)
				}
			}
			if runtimeRequests[0].RuntimeOptions.ResumeSessionID != "" || runtimeRequests[1].RuntimeOptions.ResumeSessionID != "native-session" {
				t.Fatal("second turn did not resume the persisted native session")
			}
			metadata := runtimeRequests[1].RuntimeOptions.ProviderSpecific
			if metadata[discussionHistoryModeProviderKey] != "delta" || metadata[discussionHistorySinceRunIDProviderKey] != "first" {
				t.Fatal("history checkpoint was lost")
			}
			prompt := agentcore.RuntimePromptMessagesFallbackPrompt(runtimeRequests[1].InputMessages)
			if strings.Contains(prompt, "<session_bootstrap>") || !strings.Contains(prompt, "second current instruction") {
				t.Fatal("wrong second-turn input")
			}
		})
	}
}

type continuityProjectionRuntime struct {
	fakeAssembledRuntime
	at       time.Time
	response agentcore.RuntimeResponse
}

func (r *continuityProjectionRuntime) RunSession(ctx context.Context, request agentcore.RuntimeRequest, observer agentcore.SessionEventObserver) (agentcore.RuntimeResponse, error) {
	if _, err := r.fakeAssembledRuntime.RunSession(ctx, request, observer); err != nil {
		return agentcore.RuntimeResponse{}, err
	}
	projection := runtimesession.NewResponseProjection(request, agentcore.RuntimeKindPi, r.at)
	for _, event := range []agentcore.SessionEvent{
		{Kind: agentcore.EventBackendBound, BackendBound: &agentcore.BackendBoundEvent{BackendSessionID: "native-session"}},
		{Kind: agentcore.EventMessageFinalized, MessageFinalized: &agentcore.MessageFinalizedEvent{Message: agentcore.FinalizedMessage{ItemID: "reply", Sequence: 1, Kind: agentcore.ItemText, Content: "assembled response"}}},
		{Kind: agentcore.EventTurnResult, TurnResult: &agentcore.TurnResultEvent{Epoch: 1, Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeEndTurn}}},
	} {
		if err := projection.Fold(agentcore.SessionEnvelope{Timestamp: r.at.Add(time.Second), Event: event}); err != nil {
			return agentcore.RuntimeResponse{}, err
		}
	}
	r.response = projection.Response(r.at.Add(time.Second))
	return r.response, nil
}

func (r *continuityProjectionRuntime) RuntimeRequest(runID agentcore.AgentRunID) (agentcore.RuntimeRequest, bool) {
	return r.agent.request, r.agent.request.RunID == runID
}

func (r *continuityProjectionRuntime) RuntimeResponse(runID agentcore.AgentRunID) (agentcore.RuntimeResponse, bool) {
	return r.response, r.response.RunID == runID
}

func (*continuityProjectionRuntime) RuntimeEvents(agentcore.AgentRunID) []agentcore.SessionEnvelope {
	return nil
}
func (*continuityProjectionRuntime) SubscribeRuntimeEvents(agentcore.AgentRunID) (<-chan agentcore.SessionEnvelope, func()) {
	return nil, func() {}
}
func (*continuityProjectionRuntime) DeleteRuntimeRecord(context.Context, agentcore.AgentRunID) error {
	return nil
}

var _ agentcore.SessionRuntimeRecordReader = (*continuityProjectionRuntime)(nil)

func TestDiscussionStructuredContextRetainsRecentFactsUnderLargeCatalog(t *testing.T) {
	agent := newLightweightTurnTestAgent()
	runner := NewBaseAgentDiscussionAgentRunner(BaseAgentDiscussionAgentRunnerOptions{Agent: agent, AgentID: "personal-assistant", RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi}})
	request := lightweightTurnTestRequest("Store the previously supplied company information. Do not publish it.")
	for i := 0; i < 80; i++ {
		request.History = append(request.History, MessageDTO{MessageID: "old", Content: strings.Repeat("old discussion ", 100)})
	}
	request.History = append(request.History, MessageDTO{MessageID: "latest", Content: "Company account alias is TEST-ALPHA, for internal reference only."})
	if _, err := runner.RunDiscussion(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	prompt := agentcore.RuntimePromptMessagesFallbackPrompt(agent.request.InputMessages)
	for _, expected := range []string{"TEST-ALPHA", "Do not publish it.", "</discussion_history>", "</current_user_input>"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("lost %q", expected)
		}
	}
	if strings.Contains(prompt, "[... context truncated ...]") || agent.request.RuntimeOptions.ProviderSpecific[agentcore.RuntimeContextTruncatedProviderKey] != "true" {
		t.Fatal("structured trimming did not run")
	}
}

func TestDiscussionContextOverflowDoesNotInvokeRuntime(t *testing.T) {
	agent := newLightweightTurnTestAgent()
	runner := NewBaseAgentDiscussionAgentRunner(BaseAgentDiscussionAgentRunnerOptions{Agent: agent, AgentID: "personal-assistant", RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi}})
	request := lightweightTurnTestRequest(strings.Repeat("mandatory current instruction ", 3000))
	if _, err := runner.RunDiscussion(context.Background(), request); err == nil || agent.calls != 0 {
		t.Fatal("runtime executed an over-budget partial instruction")
	}
}

func TestDiscussionContinuationRejectsOtherIdentityAndForcedNewSession(t *testing.T) {
	spec := agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi, ConfigRef: "config-a"}
	base := DiscussionAgentRequest{ConversationID: "conv", AgentID: "worker", RunID: "new", Runs: []RunRecord{{RunID: "old", ConversationID: "conv", AgentID: "worker", RuntimeKind: "pi", SessionID: "session", Status: "completed"}}}
	for _, reason := range []string{"conversation", "agent", "runtime", "logical_assistant", "cancelled", "retry"} {
		t.Run(reason, func(t *testing.T) {
			request := base
			request.Runs = append([]RunRecord(nil), base.Runs...)
			switch reason {
			case "conversation":
				request.Runs[0].ConversationID = "another-private-conversation"
			case "agent":
				request.Runs[0].AgentID = "another-agent"
			case "runtime":
				request.Runs[0].RuntimeKind = "codex"
			case "logical_assistant":
				request.Runs[0].LogicalAgentID = "private-assistant"
			case "cancelled":
				request.Runs[0].Status = "cancelled"
			case "retry":
				request.ForceNewSession = true
			}
			if discussionExternalCLISessionForRuntimeRequest(request, spec).SessionID != "" {
				t.Fatal("invalid prior session was selected")
			}
		})
	}
}
