package main

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/responsibility"
)

type recordingProductionResponsibilityContextProvider struct {
	request     responsibility.ContextRequest
	goalRequest responsibility.ContextRequest
}

func (p *recordingProductionResponsibilityContextProvider) ResolveAutoplanGoalContext(_ context.Context, request responsibility.ContextRequest) (responsibility.AutoplanGoalContext, error) {
	p.goalRequest = request
	return responsibility.AutoplanGoalContext{
		ContextID: "people-goal-context", ResponsibilityID: request.ResponsibilityID,
		ResponsibilityVersion: request.AssignmentVersion, AgentProfileID: request.AgentProfileID,
	}, nil
}

func (p *recordingProductionResponsibilityContextProvider) ResolveResponsibilityContext(_ context.Context, request responsibility.ContextRequest) (agentcore.ResponsibilityContext, error) {
	p.request = request
	return agentcore.ResponsibilityContext{
		ContextID: "people-context", ActorMemberID: request.ActorMemberID, AgentProfileID: request.AgentProfileID,
	}, nil
}

func TestPeopleBoundResponsibilityContextProviderUsesPeopleSubjectAndPreservesOrreryMember(t *testing.T) {
	inner := &recordingProductionResponsibilityContextProvider{}
	provider := newPeopleBoundResponsibilityContextProvider(inner, map[string]string{"member-openclaw-tiedan": "A000008"})

	result, err := provider.ResolveResponsibilityContext(context.Background(), responsibility.ContextRequest{
		ActorMemberID: "orrery-member-1", AgentProfileID: "A000008",
	})
	if err != nil {
		t.Fatal(err)
	}
	if inner.request.ActorMemberID != "member-openclaw-tiedan" || inner.request.AgentProfileID != "A000008" {
		t.Fatalf("People request = %+v", inner.request)
	}
	if result.ActorMemberID != "orrery-member-1" || !containsString(result.SourceRefs, "people:subject:member-openclaw-tiedan") {
		t.Fatalf("runtime context = %+v", result)
	}
	goalProvider, ok := provider.(responsibility.AutoplanGoalContextProvider)
	if !ok {
		t.Fatal("People-bound provider does not expose autonomous planning goals")
	}
	if _, err := goalProvider.ResolveAutoplanGoalContext(context.Background(), responsibility.ContextRequest{
		ActorMemberID: "orrery-member-1", AgentProfileID: "A000008", ResponsibilityID: "responsibility-1", AssignmentVersion: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if inner.goalRequest.ActorMemberID != "member-openclaw-tiedan" || inner.goalRequest.AgentProfileID != "A000008" {
		t.Fatalf("People goal request = %+v", inner.goalRequest)
	}
}
