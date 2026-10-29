package main

import (
	"context"
	"strings"
	"testing"

	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/runtimeapproval"
	"orrery_v2/internal/platform/idgen"
)

type assistantDelegationAssignees struct{}

func (assistantDelegationAssignees) ResolveWorkflowAssignees(_ context.Context, request hil.WorkflowAssigneeRequest) ([]hil.ActorRef, error) {
	return []hil.ActorRef{{Kind: hil.ActorHuman, ID: request.Selector.ID}}, nil
}

func assistantDelegationWorkflow(t *testing.T, e *productionPersonalAssistantDelegationEngine, decision hil.DecisionRequest) (*hil.WorkflowService, hil.DecisionRequest, hil.PreparedWorkflow) {
	t.Helper()
	ctx := context.Background()
	repo := hil.NewPlatformRepository(e.core)
	workflow := hil.NewWorkflowService(hil.WorkflowServiceOptions{Repository: repo, Assignees: assistantDelegationAssignees{}, IDs: idgen.NewDeterministic("delegated-workflow"), Clock: e.now})
	definition := hil.WorkflowDefinition{RiskLevels: []hil.RiskLevel{hil.RiskLow}, Stages: []hil.WorkflowStageDefinition{{
		Key: "review", Name: "Review", Order: 1, Type: hil.WorkflowStageApproval,
		Approval: &hil.ApprovalStagePolicy{Strategy: hil.ApprovalAll, DeadlineMinutes: 60, Assignees: []hil.WorkflowActorSelector{{Kind: hil.WorkflowSelectorHuman, ID: "human-a"}, {Kind: hil.WorkflowSelectorHuman, ID: "human-b"}}},
	}}}
	admin := hil.ActorRef{Kind: hil.ActorHuman, ID: "admin"}
	template, err := workflow.SaveTemplateDraft(ctx, admin, hil.SaveTemplateDraftInput{ID: "template", TenantID: decision.TenantID, Name: "Read-only review", Definition: definition, IdempotencyKey: "save"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workflow.PublishTemplate(ctx, admin, hil.PublishTemplateInput{Template: template, IdempotencyKey: "publish", Bindings: []hil.WorkflowTemplateBinding{{ID: "binding", TenantID: decision.TenantID, ActionTypeID: decision.ActionTypeID, RiskLevel: hil.RiskLow, TemplateID: template.ID, Active: true}}}); err != nil {
		t.Fatal(err)
	}
	decision.ID, decision.IdempotencyKey, decision.RequestFingerprint = "workflow-decision", "create-workflow", "workflow-fingerprint"
	decision.Route.Mode = hil.ModeApproval
	decision.ObjectRefs = []hil.ObjectRef{decision.Resource}
	decision.PayloadPreview["material_hash"] = "sha256:workflow-material"
	prepared, found, err := workflow.PrepareDecisionWorkflow(ctx, decision)
	if err != nil || !found {
		t.Fatalf("prepare workflow: %v %v", found, err)
	}
	if err := repo.CreateDecisionWorkflow(ctx, decision, prepared.Case, prepared.Instance, prepared.Stages, prepared.Tasks, prepared.Audit, prepared.Notifications); err != nil {
		t.Fatal(err)
	}
	hil.WithWorkflowService(workflow)(e.decisions)
	return workflow, decision, prepared
}

func TestPersonalAssistantDelegationKeepsReviewedWorkflowVersion(t *testing.T) {
	for _, scenario := range []string{"unchanged", "other_seat_decided"} {
		t.Run(scenario, func(t *testing.T) {
			e, d, candidate, target, _ := delegatedApprovalFixture(t)
			workflow, d, prepared := assistantDelegationWorkflow(t, e, d)
			candidate.DecisionID, candidate.WorkflowID, candidate.RouteMode = d.ID, prepared.Instance.ID, hil.ModeApproval
			var otherTask string
			for _, task := range prepared.Tasks {
				if task.CurrentAssignee.ID == candidate.HumanProfileID {
					candidate.TaskID = task.ID
				} else {
					otherTask = task.ID
				}
			}
			if candidate.TaskID == "" || otherTask == "" {
				t.Fatal("workflow seats missing")
			}
			calls := 0
			e.completion = assistantDelegationCompletionFunc(func(ctx context.Context, _ runtimeapproval.CompletionRequest) (string, error) {
				calls++
				if scenario == "other_seat_decided" {
					if _, err := workflow.DecideTask(ctx, d.TenantID, otherTask, hil.ActorRef{Kind: hil.ActorHuman, ID: "human-b"}, hil.DecideWorkflowTaskInput{Decision: hil.WorkflowTaskApprove, Reason: "Concurrent review", IdempotencyKey: "human-first", ExpectedInstanceVersion: prepared.Instance.Version}); err != nil {
						t.Fatal(err)
					}
				}
				return `{"decision":"approve","risk":"low","material_complete":true,"reason":"Checked","missing_information":""}`, nil
			})
			handled, err := e.Handle(context.Background(), candidate, target)
			if err != nil || handled != (scenario == "unchanged") {
				t.Fatalf("stale workflow review committed: handled=%v err=%v", handled, err)
			}
			detail, err := hil.NewPlatformRepository(e.core).GetWorkflowInstanceByDecisionID(context.Background(), d.TenantID, d.ID)
			if err != nil {
				t.Fatal(err)
			}
			if detail.Instance.Status != hil.WorkflowRunning || len(detail.Receipts) != 0 {
				t.Fatal("review completed a changed workflow")
			}
			for _, task := range detail.Tasks {
				if task.ID == candidate.TaskID && scenario == "other_seat_decided" && task.Status != hil.TaskPending {
					t.Fatal("owner seat changed after concurrent human review")
				}
			}
			if scenario == "other_seat_decided" {
				summary, reviewing, err := e.humanReviewSummary(context.Background(), candidate)
				if err != nil || reviewing || !strings.Contains(summary, "审批流程已变化") {
					t.Fatalf("missing stale-review escalation: %q %v %v", summary, reviewing, err)
				}
				if handled, err := e.Handle(context.Background(), candidate, target); err != nil || handled || calls != 1 {
					t.Fatalf("stale review retried: %v %v calls=%d", handled, err, calls)
				}
			}
		})
	}
}
