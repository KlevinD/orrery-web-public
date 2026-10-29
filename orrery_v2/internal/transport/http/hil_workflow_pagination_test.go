package httptransport

import (
	"context"
	"fmt"
	"testing"

	"orrery_v2/internal/app/hil"
)

type workflowPaginationStub struct {
	HILWorkflowService
	items   []hil.WorkflowInstanceDetail
	offsets []int
}

func (s *workflowPaginationStub) ListWorkflowInstances(_ context.Context, q hil.WorkflowListQuery) (hil.WorkflowInstancePage, error) {
	s.offsets = append(s.offsets, q.Offset)
	end := min(q.Offset+q.Limit, len(s.items))
	return hil.WorkflowInstancePage{Items: s.items[min(q.Offset, len(s.items)):end], Total: len(s.items)}, nil
}

func TestVisibleWorkflowPaginationFiltersBeforePaging(t *testing.T) {
	viewer := hil.ActorRef{Kind: hil.ActorHuman, ID: "reviewer"}
	stub := &workflowPaginationStub{}
	for i := range 107 {
		requester := hil.ActorRef{Kind: hil.ActorHuman, ID: "other"}
		if i >= 100 {
			requester = viewer
		}
		stub.items = append(stub.items, hil.WorkflowInstanceDetail{Case: hil.WorkflowCase{Requester: requester}, Instance: hil.WorkflowInstance{ID: fmt.Sprint(i)}})
	}
	page, err := listVisibleWorkflowInstances(context.Background(), stub, hil.WorkflowListQuery{TenantID: "tenant", Limit: 2, Offset: 1}, viewer, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 7 || len(page.Items) != 2 || page.Items[0].Instance.ID != "101" || page.Items[1].Instance.ID != "102" {
		t.Fatalf("wrong visible page: %+v", page)
	}
	if len(stub.offsets) != 2 || stub.offsets[1] != 100 {
		t.Fatalf("did not load later page: %v", stub.offsets)
	}
	page, err = listVisibleWorkflowInstances(context.Background(), stub, hil.WorkflowListQuery{TenantID: "tenant", Limit: 2, Offset: 7}, viewer, false, false)
	if err != nil || page.Total != 7 || len(page.Items) != 0 {
		t.Fatalf("wrong last page: %+v %v", page, err)
	}
}

func TestVisibleWorkflowPaginationStartedByMeDoesNotIncludeAssignedWork(t *testing.T) {
	viewer := hil.ActorRef{Kind: hil.ActorHuman, ID: "reviewer"}
	stub := &workflowPaginationStub{items: []hil.WorkflowInstanceDetail{
		{Case: hil.WorkflowCase{Requester: hil.ActorRef{Kind: hil.ActorAgent, ID: "worker"}}, Tasks: []hil.WorkflowTask{{CurrentAssignee: viewer}}},
		{Case: hil.WorkflowCase{Requester: viewer}},
	}}
	page, err := listVisibleWorkflowInstances(context.Background(), stub, hil.WorkflowListQuery{TenantID: "tenant"}, viewer, true, true)
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].Case.Requester != viewer {
		t.Fatalf("wrong requester visibility: %+v %v", page, err)
	}
}
