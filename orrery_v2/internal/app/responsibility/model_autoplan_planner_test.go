package responsibility

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"orrery_v2/internal/agent/llm"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestModelAutoplanPlannerAcceptsBoundedProposalWithoutTools(t *testing.T) {
	request := modelAutoplanPlannerTestRequest()
	proposal := modelAutoplanPlannerTestProposal(request)
	var received []*llm.Message
	planner := modelAutoplanPlannerForTest(t, &modelAutoplanPlannerTestChatModel{
		generate: func(_ context.Context, messages []*llm.Message, options ...llm.Option) (*llm.Message, error) {
			received = messages
			common := llm.Apply(options...)
			if len(common.Tools) != 0 {
				return nil, fmt.Errorf("planner supplied tools to the model")
			}
			return modelAutoplanPlannerTestMessage(t, proposal), nil
		},
	})

	got, err := planner.PlanAutoplan(context.Background(), request)
	if err != nil {
		t.Fatalf("PlanAutoplan: %v", err)
	}
	if got.Summary != proposal.Summary || len(got.Steps) != 1 || got.Steps[0].AssigneeAgentID != request.PlanRequest.AgentProfileID {
		t.Fatalf("unexpected proposal: %+v", got)
	}
	if len(received) != 2 || received[0].Role != llm.System || received[1].Role != llm.User {
		t.Fatalf("expected system and user prompt, got %+v", received)
	}
	if !strings.Contains(received[0].Content, "Do not call tools") || !strings.Contains(received[1].Content, request.PlanRequest.AgentProfileID) {
		t.Fatalf("planner prompt lost safety boundaries: %+v", received)
	}
	if !strings.Contains(received[0].Content, "RiskSummary") || !strings.Contains(received[0].Content, "keep array syntax even for one item") {
		t.Fatalf("planner prompt does not declare strict array fields: %q", received[0].Content)
	}
}

func TestModelAutoplanPlannerRejectsOutsideResponsibilityBoundary(t *testing.T) {
	request := modelAutoplanPlannerTestRequest()
	tests := []struct {
		name   string
		mutate func(*AutoplanProposal)
	}{
		{
			name: "source reference",
			mutate: func(proposal *AutoplanProposal) {
				proposal.SourceRefs = []string{"external:untrusted-record"}
			},
		},
		{
			name: "assignee",
			mutate: func(proposal *AutoplanProposal) {
				proposal.Steps[0].AssigneeAgentID = "agent-outside-responsibility"
			},
		},
		{
			name: "parent goal reference",
			mutate: func(proposal *AutoplanProposal) {
				proposal.Steps[0].ParentGoalRef = "people:position_goal:other"
			},
		},
		{
			name: "parent goal text",
			mutate: func(proposal *AutoplanProposal) {
				proposal.Steps[0].ParentGoal = "A goal written by the planner"
			},
		},
		{
			name: "policy step count",
			mutate: func(proposal *AutoplanProposal) {
				copy := proposal.Steps[0]
				copy.StepID = "step-2"
				copy.Title = "Second bounded step"
				copy.Goal = "Produce another bounded assessment"
				copy.AcceptanceCriteria = []string{"A second traceable assessment is recorded"}
				copy.EvidenceRequirements = []AutoplanEvidenceRequirement{{
					EvidenceRequirementID: "evidence-step-2", AcceptanceCriterion: copy.AcceptanceCriteria[0], Kind: "tool_log", Required: true,
				}}
				proposal.Steps = append(proposal.Steps, copy)
			},
		},
		{
			name: "summary length",
			mutate: func(proposal *AutoplanProposal) {
				proposal.Summary = strings.Repeat("x", modelAutoplanSummaryMaxRunes+1)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			proposal := modelAutoplanPlannerTestProposal(request)
			test.mutate(&proposal)
			planner := modelAutoplanPlannerForTest(t, &modelAutoplanPlannerTestChatModel{
				generate: func(context.Context, []*llm.Message, ...llm.Option) (*llm.Message, error) {
					return modelAutoplanPlannerTestMessage(t, proposal), nil
				},
			})
			_, err := planner.PlanAutoplan(context.Background(), request)
			if !errors.Is(err, ErrModelAutoplanOutput) {
				t.Fatalf("PlanAutoplan error = %v, want ErrModelAutoplanOutput", err)
			}
		})
	}
}

func TestModelAutoplanPlannerRejectsInvalidJSON(t *testing.T) {
	planner := modelAutoplanPlannerForTest(t, &modelAutoplanPlannerTestChatModel{
		generate: func(context.Context, []*llm.Message, ...llm.Option) (*llm.Message, error) {
			return llm.AssistantMessage("```json\n{\"Version\":1}\n```", nil), nil
		},
	})
	_, err := planner.PlanAutoplan(context.Background(), modelAutoplanPlannerTestRequest())
	if !errors.Is(err, ErrModelAutoplanOutput) {
		t.Fatalf("PlanAutoplan error = %v, want strict JSON error", err)
	}
}

func TestModelAutoplanPlannerDoesNotInventWorkWithoutExistingGoal(t *testing.T) {
	request := modelAutoplanPlannerTestRequest()
	request.GoalContext.Goals = nil
	request.GoalContext.SourceRefs = nil
	digest, err := AutoplanGoalContextDigest(request.GoalContext)
	if err != nil {
		t.Fatal(err)
	}
	request.PlanRequest.GoalContextDigest = digest
	modelCalled := false
	planner := modelAutoplanPlannerForTest(t, &modelAutoplanPlannerTestChatModel{
		generate: func(context.Context, []*llm.Message, ...llm.Option) (*llm.Message, error) {
			modelCalled = true
			return nil, errors.New("model must not be called without an existing goal")
		},
	})

	proposal, err := planner.PlanAutoplan(context.Background(), request)
	if err != nil {
		t.Fatalf("PlanAutoplan: %v", err)
	}
	if modelCalled || len(proposal.Steps) != 0 || !strings.Contains(proposal.NoActionReason, "no explicit existing goal") {
		t.Fatalf("proposal=%+v model_called=%v", proposal, modelCalled)
	}
}

func TestModelAutoplanPlannerTimesOutModelCall(t *testing.T) {
	planner := modelAutoplanPlannerForTest(t, &modelAutoplanPlannerTestChatModel{
		generate: func(ctx context.Context, _ []*llm.Message, _ ...llm.Option) (*llm.Message, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}, ModelAutoplanPlannerOptions{Timeout: 15 * time.Millisecond})
	_, err := planner.PlanAutoplan(context.Background(), modelAutoplanPlannerTestRequest())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("PlanAutoplan error = %v, want context deadline exceeded", err)
	}
}

func TestModelAutoplanPlannerLimitsConcurrentModelCalls(t *testing.T) {
	request := modelAutoplanPlannerTestRequest()
	proposal := modelAutoplanPlannerTestProposal(request)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	planner := modelAutoplanPlannerForTest(t, &modelAutoplanPlannerTestChatModel{
		generate: func(ctx context.Context, _ []*llm.Message, _ ...llm.Option) (*llm.Message, error) {
			calls.Add(1)
			select {
			case entered <- struct{}{}:
			default:
			}
			select {
			case <-release:
				return modelAutoplanPlannerTestMessage(t, proposal), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}, ModelAutoplanPlannerOptions{Timeout: time.Second, MaxConcurrent: 1})

	first := make(chan error, 1)
	go func() {
		_, err := planner.PlanAutoplan(context.Background(), request)
		first <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first model call did not start")
	}
	second := make(chan error, 1)
	go func() {
		_, err := planner.PlanAutoplan(context.Background(), request)
		second <- err
	}()
	select {
	case err := <-second:
		t.Fatalf("second call escaped local concurrency limit early: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("Generate calls = %d, want exactly one while first call holds slot", got)
	}
	close(release)
	for _, result := range []<-chan error{first, second} {
		select {
		case err := <-result:
			if err != nil {
				t.Fatalf("PlanAutoplan: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("model call did not finish")
		}
	}
}

func modelAutoplanPlannerForTest(t *testing.T, chatModel llm.ChatModel, overrides ...ModelAutoplanPlannerOptions) *ModelAutoplanPlanner {
	t.Helper()
	options := ModelAutoplanPlannerOptions{
		ModelFactory: AutoplanChatModelFactoryFunc(func(context.Context) (llm.ChatModel, error) {
			return chatModel, nil
		}),
	}
	if len(overrides) > 0 {
		options.Timeout = overrides[0].Timeout
		options.MaxConcurrent = overrides[0].MaxConcurrent
		options.MaxResponseBytes = overrides[0].MaxResponseBytes
	}
	planner, err := NewModelAutoplanPlanner(options)
	if err != nil {
		t.Fatalf("NewModelAutoplanPlanner: %v", err)
	}
	return planner
}

func modelAutoplanPlannerTestRequest() AutoplanPlanningRequest {
	portfolio := PeopleResponsibilityPortfolio{
		ID: PeopleResponsibilityPortfolioID("binding-1"), TenantID: "tenant-1", BindingID: "binding-1", SubjectID: "subject-1",
		AgentProfileID: "agent-1", SourceRevision: "1", SourceDigest: "sha256:source", Status: PeoplePortfolioStatusActive,
	}
	responsibility := PeopleResolvedResponsibility{
		ID: "responsibility-1", Code: "DELIVERY", Title: "Deliver the current outcome", Description: "Create a traceable, current deliverable.", Revision: 1,
	}
	policy := DefaultAutoplanPolicy(portfolio.AgentProfileID)
	policy.Enabled = true
	policy.AutonomyLevel = AutonomyContinuous
	policy.MaxSteps = 1
	policy.AllowedAssigneeIDs = []string{portfolio.AgentProfileID}
	goalContext := autoplanServiceTestGoalContext()
	goalDigest, err := AutoplanGoalContextDigest(goalContext)
	if err != nil {
		panic(err)
	}
	return AutoplanPlanningRequest{
		PlanRequest: AutoplanRequest{
			TenantID: portfolio.TenantID, PortfolioID: portfolio.ID, PortfolioBindingID: portfolio.BindingID,
			PortfolioVersionID: PeopleResponsibilityPortfolioVersionID(portfolio.BindingID, portfolio.SourceDigest),
			SourceRevision:     portfolio.SourceRevision, SourceDigest: portfolio.SourceDigest, AgentProfileID: portfolio.AgentProfileID,
			GoalContextID: goalContext.ContextID, GoalContextDigest: goalDigest,
			PolicyID: "policy-1", PolicyVersion: 1, PlanningCycle: 1, TriggerKind: "responsibility_pulse",
			TriggerRef: responsibilityAutoplanTriggerRef(responsibility), TriggeredAt: time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC),
		},
		Portfolio: portfolio, Responsibility: responsibility, GoalContext: goalContext, Policy: policy,
	}
}

func modelAutoplanPlannerTestProposal(request AutoplanPlanningRequest) AutoplanProposal {
	criterion := "A traceable read-only assessment is recorded"
	return AutoplanProposal{
		Version: AutoplanProposalVersion, Summary: "Assess the current responsibility", Rationale: "The responsibility has no active concrete task.",
		SourceRefs: []string{responsibilityAutoplanSourceRefs(request.Portfolio, request.Responsibility)[2], request.GoalContext.Goals[0].Ref},
		Steps: []AutoplanStep{{
			StepID: "step-1", Title: "Assess responsibility", Description: "Produce a bounded, read-only assessment.", Goal: "Record the next-step assessment",
			ParentGoalRef: request.GoalContext.Goals[0].Ref, ParentGoal: request.GoalContext.Goals[0].Goal,
			AssigneeAgentID: request.PlanRequest.AgentProfileID, Priority: 50, RiskLevel: AutoplanRiskLow,
			AcceptanceCriteria: []string{criterion},
			EvidenceRequirements: []AutoplanEvidenceRequirement{{
				EvidenceRequirementID: "evidence-step-1", AcceptanceCriterion: criterion, Kind: "tool_log", Required: true,
			}},
		}},
	}
}

func modelAutoplanPlannerTestMessage(t *testing.T, proposal AutoplanProposal) *llm.Message {
	t.Helper()
	raw, err := json.Marshal(proposal)
	if err != nil {
		t.Fatalf("marshal test proposal: %v", err)
	}
	return llm.AssistantMessage(string(raw), nil)
}

type modelAutoplanPlannerTestChatModel struct {
	generate func(context.Context, []*llm.Message, ...llm.Option) (*llm.Message, error)
}

func (m *modelAutoplanPlannerTestChatModel) Generate(ctx context.Context, input []*llm.Message, options ...llm.Option) (*llm.Message, error) {
	if m == nil || m.generate == nil {
		return nil, errors.New("test chat model is not configured")
	}
	return m.generate(ctx, input, options...)
}

func (m *modelAutoplanPlannerTestChatModel) Stream(ctx context.Context, input []*llm.Message, options ...llm.Option) (*llm.MessageStream, error) {
	message, err := m.Generate(ctx, input, options...)
	if err != nil {
		return nil, err
	}
	return llm.NewMessageStream([]*llm.Message{message}), nil
}
