package hil

import (
	"context"
	"errors"
	"testing"
)

type fakeWorkflowAssigneeResolver struct {
	results map[string][]ActorRef
	err     error
}

func (r fakeWorkflowAssigneeResolver) ResolveWorkflowAssignees(_ context.Context, request WorkflowAssigneeRequest) ([]ActorRef, error) {
	if r.err != nil {
		return nil, r.err
	}
	return append([]ActorRef(nil), r.results[string(request.Selector.Kind)+":"+request.Selector.ID]...), nil
}

func TestResolveWorkflowActorSelectorsFreezesEligibleHumans(t *testing.T) {
	request := WorkflowAssigneeRequest{
		TenantID: "tenant-1", Requester: ActorRef{Kind: ActorHuman, ID: "requester"}, Resource: ObjectRef{Kind: "article", ID: "article-1"},
	}
	resolver := fakeWorkflowAssigneeResolver{results: map[string][]ActorRef{
		"explicit_human:human-a":       {{Kind: ActorHuman, ID: "human-a"}},
		"people_role:marketing-review": {{Kind: ActorHuman, ID: "human-b"}, {Kind: ActorHuman, ID: "requester"}},
		"organization_position:lead":   {{Kind: ActorHuman, ID: "human-b"}},
		"requester_manager:":           {{Kind: ActorHuman, ID: "human-c"}},
		"resource_owner:":              {{Kind: ActorHuman, ID: "human-d"}},
	}}
	selectors := []WorkflowActorSelector{
		{Kind: WorkflowSelectorHuman, ID: "human-a"},
		{Kind: WorkflowSelectorPeopleRole, ID: "marketing-review"},
		{Kind: WorkflowSelectorOrganizationPosition, ID: "lead"},
		{Kind: WorkflowSelectorRequesterManager},
		{Kind: WorkflowSelectorResourceOwner},
	}
	actors, err := resolveWorkflowActorSelectors(context.Background(), resolver, request, selectors, workflowAssigneeResolutionOptions{
		FormalApproval: true, ExcludeRequester: true,
	})
	if err != nil {
		t.Fatalf("resolveWorkflowActorSelectors: %v", err)
	}
	want := []ActorRef{{Kind: ActorHuman, ID: "human-a"}, {Kind: ActorHuman, ID: "human-b"}, {Kind: ActorHuman, ID: "human-c"}, {Kind: ActorHuman, ID: "human-d"}}
	if len(actors) != len(want) {
		t.Fatalf("actors = %+v, want %+v", actors, want)
	}
	for index := range want {
		if actors[index] != want[index] {
			t.Fatalf("actors = %+v, want %+v", actors, want)
		}
	}
}

func TestResolveWorkflowActorSelectorsAllowsCouncilRecommendations(t *testing.T) {
	resolver := fakeWorkflowAssigneeResolver{results: map[string][]ActorRef{
		"ontology_recommended:": {{Kind: ActorAgent, ID: "agent-1"}, {Kind: ActorHuman, ID: "human-a"}},
	}}
	actors, err := resolveWorkflowActorSelectors(context.Background(), resolver, WorkflowAssigneeRequest{TenantID: "tenant-1"}, []WorkflowActorSelector{{Kind: WorkflowSelectorOntologyRecommended}}, workflowAssigneeResolutionOptions{})
	if err != nil || len(actors) != 2 || actors[0].ID != "agent-1" || actors[1].ID != "human-a" {
		t.Fatalf("council actors = %+v err=%v", actors, err)
	}
}

func TestResolveWorkflowActorSelectorsFailsClosed(t *testing.T) {
	request := WorkflowAssigneeRequest{TenantID: "tenant-1", Requester: ActorRef{Kind: ActorHuman, ID: "requester"}}
	for _, test := range []struct {
		name     string
		resolver WorkflowAssigneeResolver
		selector WorkflowActorSelector
		options  workflowAssigneeResolutionOptions
	}{
		{name: "unresolved", resolver: fakeWorkflowAssigneeResolver{results: map[string][]ActorRef{}}, selector: WorkflowActorSelector{Kind: WorkflowSelectorPeopleRole, ID: "missing"}, options: workflowAssigneeResolutionOptions{FormalApproval: true}},
		{name: "only requester", resolver: fakeWorkflowAssigneeResolver{results: map[string][]ActorRef{"explicit_human:requester": {{Kind: ActorHuman, ID: "requester"}}}}, selector: WorkflowActorSelector{Kind: WorkflowSelectorHuman, ID: "requester"}, options: workflowAssigneeResolutionOptions{FormalApproval: true, ExcludeRequester: true}},
		{name: "agent formal approver", resolver: fakeWorkflowAssigneeResolver{results: map[string][]ActorRef{"people_role:review": {{Kind: ActorAgent, ID: "agent-1"}}}}, selector: WorkflowActorSelector{Kind: WorkflowSelectorPeopleRole, ID: "review"}, options: workflowAssigneeResolutionOptions{FormalApproval: true}},
		{name: "multiple escalation actors", resolver: fakeWorkflowAssigneeResolver{results: map[string][]ActorRef{"people_role:leaders": {{Kind: ActorHuman, ID: "human-a"}, {Kind: ActorHuman, ID: "human-b"}}}}, selector: WorkflowActorSelector{Kind: WorkflowSelectorPeopleRole, ID: "leaders"}, options: workflowAssigneeResolutionOptions{FormalApproval: true, ExactlyOne: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := resolveWorkflowActorSelectors(context.Background(), test.resolver, request, []WorkflowActorSelector{test.selector}, test.options)
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}
