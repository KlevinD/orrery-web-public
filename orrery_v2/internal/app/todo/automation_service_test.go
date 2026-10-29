package todo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/testkit"
)

func TestTodoAutomationSweepCreatesOneDurableProposalAndApproval(t *testing.T) {
	service, repo, discussion, closeEnv := newTodoAutomationServiceForTest(t)
	defer closeEnv()
	ctx := context.Background()
	owner := testViewer("tenant-a", "owner-a")
	task := mustCreateTask(t, service, ctx, owner, automationTodoRequest(CreateTodoRequest{Title: "Prepare launch brief", Status: TodoStatusPlanned}))

	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}

	loaded, err := service.GetTodo(ctx, owner, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Automation == nil || loaded.Automation.Status != TodoAutomationStatusPendingApproval || loaded.Automation.ConversationID != "discussion-plan" {
		t.Fatalf("automation = %+v", loaded.Automation)
	}
	if loaded.Automation.ApprovalID == "" || !strings.Contains(loaded.Automation.Proposal, "TODO_AUTOMATION_PROPOSAL") {
		t.Fatalf("proposal = %+v", loaded.Automation)
	}
	if len(discussion.commands) != 1 {
		t.Fatalf("discussion commands = %d, want duplicate sweep suppressed", len(discussion.commands))
	}
	comments, err := repo.ListIssueCommentsForTenant(ctx, owner.TenantID, IssueKindTodo, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 || !strings.Contains(comments[0].Body, "/discussions/discussion-plan") {
		t.Fatalf("automation comments = %+v", comments)
	}
	approvals, err := service.ListTodoAutomationApprovals(ctx, owner, ListTodoAutomationApprovalsQuery{Status: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	if len(approvals) != 1 || approvals[0].TodoID != task.TodoID {
		t.Fatalf("approvals = %+v", approvals)
	}
}

func TestTodoAutomationSkipsHILWhenHumanReviewDisabled(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	hilService := &todoHILDecisionStub{decisionID: "must-not-be-created"}
	service := NewService(ServiceOptions{
		Clock: clock.NewFake(todoTestTime), IDGenerator: idgen.NewDeterministic("automation-no-hil"), Repository: repo,
		AutomationDiscussion: &todoAutomationDiscussionStub{}, MissionExecution: todoMissionExecutionStub{}, HILDecisionService: hilService,
		AutomationAgentIDs: []string{"lead-agent", "coordinator-agent"},
		HumanReviewEnabled: func(context.Context) (bool, error) { return false, nil },
	})
	owner := testViewer("tenant-a", "owner-a")
	owner.AuthorizationSubjectID = "profile-owner-a"
	task := mustCreateTask(t, service, ctx, owner, automationTodoRequest(CreateTodoRequest{Title: "No HIL plan", Status: TodoStatusPlanned}))

	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	loaded, err := service.GetTodo(ctx, owner, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Automation == nil || loaded.Automation.Status != TodoAutomationStatusApproved || loaded.Automation.ApprovedPlanDigest != loaded.Automation.PlanDigest {
		t.Fatalf("automation = %+v", loaded.Automation)
	}
	if len(hilService.inputs) != 0 || loaded.Automation.HILDecisionID != "" {
		t.Fatalf("HIL inputs=%d automation=%+v", len(hilService.inputs), loaded.Automation)
	}
	approvals, err := service.ListTodoAutomationApprovals(ctx, owner, ListTodoAutomationApprovalsQuery{Status: "pending"})
	if err != nil || len(approvals) != 0 {
		t.Fatalf("approvals=%+v err=%v", approvals, err)
	}
}

func TestTodoAutomationCreatesLinkedFixedHILApprovalAndAppliesResolution(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	discussion := &todoAutomationDiscussionStub{}
	hilService := &todoHILDecisionStub{decisionID: "hil-todo-approval-1"}
	service := NewService(ServiceOptions{
		Clock: clock.NewFake(todoTestTime), IDGenerator: idgen.NewDeterministic("automation-hil"), Repository: repo,
		AutomationDiscussion: discussion, MissionExecution: todoMissionExecutionStub{}, HILDecisionService: hilService,
		AutomationAgentIDs: []string{"lead-agent", "coordinator-agent"},
	})
	approver := testViewer("tenant-a", "account-approver")
	approver.AuthorizationSubjectID = "profile-approver"
	task := mustCreateTask(t, service, ctx, approver, automationTodoRequest(CreateTodoRequest{Title: "HIL-backed plan"}))

	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	planned, err := service.GetTodo(ctx, approver, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	if planned.Automation == nil || planned.Automation.HILDecisionID != "hil-todo-approval-1" {
		t.Fatalf("automation = %+v", planned.Automation)
	}
	if len(hilService.inputs) != 1 {
		t.Fatalf("HIL inputs = %+v", hilService.inputs)
	}
	input := hilService.inputs[0]
	if input.PreferredMode != hil.ModeApproval || !input.ApprovalRequired || input.IdempotencyKey != todoHILIdempotencyKey(*planned.Automation) {
		t.Fatalf("HIL input = %+v", input)
	}
	if input.PayloadPreview["source"] != "todo_automation" || input.PayloadPreview["approval_assigned_to"] != approver.AuthorizationSubjectID || input.PayloadPreview["todo_approval_id"] != planned.Automation.ApprovalID {
		t.Fatalf("HIL payload = %+v", input.PayloadPreview)
	}
	display, ok := input.PayloadPreview[hil.ApprovalDisplayPayloadKey].(map[string]any)
	if !ok {
		t.Fatalf("HIL display payload = %#v", input.PayloadPreview[hil.ApprovalDisplayPayloadKey])
	}
	for key, expected := range map[string]string{
		"title":            "HIL-backed plan",
		"approval_summary": "是否同意按下列方案执行 Todo「HIL-backed plan」？",
		"summary":          "Verify inputs and produce the deliverable",
		"impact_scope":     "同意后系统将按方案执行 1 个步骤；拒绝后不会开始执行。",
	} {
		if actual, _ := display[key].(string); actual != expected {
			t.Fatalf("HIL payload %s = %q, want %q; payload=%+v", key, actual, expected, input.PayloadPreview)
		}
	}
	steps, ok := display["steps"].([]map[string]any)
	if !ok || len(steps) != 1 || steps[0]["title"] != "Produce deliverable" || steps[0]["description"] != "Verify inputs and complete the Todo" {
		t.Fatalf("HIL readable steps = %#v", display["steps"])
	}
	if _, exposed := input.PayloadPreview["planned_commands"]; exposed {
		t.Fatalf("HIL payload exposed planned commands: %+v", input.PayloadPreview)
	}
	approvals, err := service.ListTodoAutomationApprovals(ctx, approver, ListTodoAutomationApprovalsQuery{Status: "pending"})
	if err != nil || len(approvals) != 1 || approvals[0].HILDecisionID != planned.Automation.HILDecisionID {
		t.Fatalf("approvals = %+v err=%v", approvals, err)
	}

	service.DecisionResolved(ctx, hil.DecisionResolution{
		Decision: hil.DecisionRequest{ID: planned.Automation.HILDecisionID, TenantID: approver.TenantID, Status: hil.DecisionResolved, PayloadPreview: input.PayloadPreview},
		Receipt:  hil.DecisionReceipt{Effect: hil.EffectAuthorize},
		Actor:    hil.ActorRef{Kind: hil.ActorHuman, ID: "unassigned-profile"},
		Reason:   "not the assigned approver",
	})
	pending, err := service.GetTodo(ctx, approver, task.TodoID)
	if err != nil || pending.Automation == nil || pending.Automation.Status != TodoAutomationStatusPendingApproval {
		t.Fatalf("unassigned actor changed automation = %+v err=%v", pending.Automation, err)
	}

	service.DecisionResolved(ctx, hil.DecisionResolution{
		Decision: hil.DecisionRequest{ID: planned.Automation.HILDecisionID, TenantID: approver.TenantID, Status: hil.DecisionResolved, PayloadPreview: input.PayloadPreview},
		Receipt:  hil.DecisionReceipt{Effect: hil.EffectAuthorize},
		Actor:    hil.ActorRef{Kind: hil.ActorHuman, ID: approver.AuthorizationSubjectID},
		Reason:   "reviewed",
	})
	updated, err := service.GetTodo(ctx, approver, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Automation == nil || updated.Automation.Status != TodoAutomationStatusApproved || updated.Automation.DecisionBy != approver.ViewerID || updated.Automation.decisionAuthorizationSubjectID != approver.AuthorizationSubjectID {
		t.Fatalf("resolved automation = %+v", updated.Automation)
	}

	service.DecisionResolved(ctx, hil.DecisionResolution{
		Decision: hil.DecisionRequest{ID: planned.Automation.HILDecisionID, TenantID: approver.TenantID, Status: hil.DecisionDenied, PayloadPreview: input.PayloadPreview},
		Receipt:  hil.DecisionReceipt{Effect: hil.EffectDeny},
		Actor:    hil.ActorRef{Kind: hil.ActorHuman, ID: approver.AuthorizationSubjectID},
		Reason:   "late conflicting answer",
	})
	updated, err = service.GetTodo(ctx, approver, task.TodoID)
	if err != nil || updated.Automation.Status != TodoAutomationStatusApproved {
		t.Fatalf("conflicting resolution changed state = %+v err=%v", updated.Automation, err)
	}
}

func TestTodoAutomationHILExpiryRejectsOnlyThroughTrustedExpiryActor(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	hilService := &todoHILDecisionStub{decisionID: "hil-expiry-1"}
	service := NewService(ServiceOptions{
		Clock: clock.NewFake(todoTestTime), IDGenerator: idgen.NewDeterministic("automation-hil-expiry"), Repository: NewRepository(env.DBRouter),
		AutomationDiscussion: &todoAutomationDiscussionStub{}, MissionExecution: todoMissionExecutionStub{}, HILDecisionService: hilService,
		AutomationAgentIDs: []string{"lead-agent", "coordinator-agent"},
	})
	approver := testViewer("tenant-a", "expiry-approver")
	approver.AuthorizationSubjectID = "expiry-profile"
	task := mustCreateTask(t, service, ctx, approver, automationTodoRequest(CreateTodoRequest{Title: "Expire HIL-backed plan"}))
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	planned, err := service.GetTodo(ctx, approver, task.TodoID)
	if err != nil || planned.Automation == nil || len(hilService.inputs) != 1 {
		t.Fatalf("planned Todo = %+v err=%v HIL inputs=%+v", planned.Automation, err, hilService.inputs)
	}
	payload := hilService.inputs[0].PayloadPreview
	expired := hil.DecisionResolution{
		Decision: hil.DecisionRequest{ID: planned.Automation.HILDecisionID, TenantID: approver.TenantID, Status: hil.DecisionDenied, PayloadPreview: payload},
		Receipt:  hil.DecisionReceipt{Effect: hil.EffectDeny},
		Reason:   "decision expired",
	}

	expired.Actor = hil.ActorRef{Kind: hil.ActorService, ID: "another-service"}
	service.DecisionResolved(ctx, expired)
	pending, err := service.GetTodo(ctx, approver, task.TodoID)
	if err != nil || pending.Automation == nil || pending.Automation.Status != TodoAutomationStatusPendingApproval {
		t.Fatalf("untrusted service changed automation = %+v err=%v", pending.Automation, err)
	}

	expired.Actor = hil.ActorRef{Kind: hil.ActorService, ID: todoHILExpiryActorID}
	service.DecisionResolved(ctx, expired)
	updated, err := service.GetTodo(ctx, approver, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Automation == nil || updated.Automation.Status != TodoAutomationStatusRejected ||
		updated.Automation.DecisionBy != approver.ViewerID || updated.Automation.decisionAuthorizationSubjectID != approver.AuthorizationSubjectID {
		t.Fatalf("expired automation = %+v", updated.Automation)
	}
}

func TestTodoAutomationScanReplaysLinkedTerminalHILResolution(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	hilService := &todoHILResolutionStub{todoHILDecisionStub: todoHILDecisionStub{decisionID: "hil-replay-1"}}
	service := NewService(ServiceOptions{
		Clock: clock.NewFake(todoTestTime), IDGenerator: idgen.NewDeterministic("automation-hil-replay"), Repository: NewRepository(env.DBRouter),
		AutomationDiscussion: &todoAutomationDiscussionStub{}, MissionExecution: todoMissionExecutionStub{}, HILDecisionService: hilService,
		AutomationAgentIDs: []string{"lead-agent", "coordinator-agent"},
	})
	approver := testViewer("tenant-a", "replay-approver")
	approver.AuthorizationSubjectID = "replay-profile"
	task := mustCreateTask(t, service, ctx, approver, automationTodoRequest(CreateTodoRequest{Title: "Replay HIL-backed plan"}))
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	planned, err := service.GetTodo(ctx, approver, task.TodoID)
	if err != nil || planned.Automation == nil || len(hilService.inputs) != 1 {
		t.Fatalf("planned Todo = %+v err=%v HIL inputs=%+v", planned.Automation, err, hilService.inputs)
	}
	payload := hilService.inputs[0].PayloadPreview
	hilService.resolution = hil.DecisionResolution{
		Decision: hil.DecisionRequest{ID: planned.Automation.HILDecisionID, TenantID: approver.TenantID, Status: hil.DecisionResolved, PayloadPreview: payload},
		Receipt:  hil.DecisionReceipt{Effect: hil.EffectAuthorize},
		Actor:    hil.ActorRef{Kind: hil.ActorService, ID: todoHILReplayActorID},
		Reason:   "replayed committed HIL decision",
	}

	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	updated, err := service.GetTodo(ctx, approver, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Automation == nil || updated.Automation.Status != TodoAutomationStatusApproved ||
		updated.Automation.DecisionBy != approver.ViewerID || updated.Automation.decisionAuthorizationSubjectID != approver.AuthorizationSubjectID {
		t.Fatalf("replayed automation = %+v", updated.Automation)
	}
	if hilService.resolutionCalls != 1 || hilService.lastTenantID != approver.TenantID || hilService.lastDecisionID != planned.Automation.HILDecisionID {
		t.Fatalf("replay lookup = calls=%d tenant=%q decision=%q", hilService.resolutionCalls, hilService.lastTenantID, hilService.lastDecisionID)
	}
}

func TestTodoAutomationScanDoesNotApplyUntrustedOrPendingHILResolution(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	hilService := &todoHILResolutionStub{todoHILDecisionStub: todoHILDecisionStub{decisionID: "hil-replay-guard"}}
	service := NewService(ServiceOptions{
		Clock: clock.NewFake(todoTestTime), IDGenerator: idgen.NewDeterministic("automation-hil-replay-guard"), Repository: NewRepository(env.DBRouter),
		AutomationDiscussion: &todoAutomationDiscussionStub{}, MissionExecution: todoMissionExecutionStub{}, HILDecisionService: hilService,
		AutomationAgentIDs: []string{"lead-agent", "coordinator-agent"},
	})
	approver := testViewer("tenant-a", "replay-guard-approver")
	approver.AuthorizationSubjectID = "replay-guard-profile"
	task := mustCreateTask(t, service, ctx, approver, automationTodoRequest(CreateTodoRequest{Title: "Guard replay HIL-backed plan"}))
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	planned, err := service.GetTodo(ctx, approver, task.TodoID)
	if err != nil || planned.Automation == nil || len(hilService.inputs) != 1 {
		t.Fatalf("planned Todo = %+v err=%v HIL inputs=%+v", planned.Automation, err, hilService.inputs)
	}
	payload := hilService.inputs[0].PayloadPreview
	hilService.resolution = hil.DecisionResolution{
		Decision: hil.DecisionRequest{ID: planned.Automation.HILDecisionID, TenantID: approver.TenantID, Status: hil.DecisionResolved, PayloadPreview: payload},
		Receipt:  hil.DecisionReceipt{Effect: hil.EffectAuthorize},
		Actor:    hil.ActorRef{Kind: hil.ActorService, ID: "untrusted-replay"},
	}
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err := service.GetTodo(ctx, approver, task.TodoID)
	if err != nil || pending.Automation == nil || pending.Automation.Status != TodoAutomationStatusPendingApproval {
		t.Fatalf("untrusted replay changed automation = %+v err=%v", pending.Automation, err)
	}

	hilService.resolution = hil.DecisionResolution{
		Decision: hil.DecisionRequest{ID: planned.Automation.HILDecisionID, TenantID: approver.TenantID, Status: hil.DecisionPending, PayloadPreview: payload},
		Actor:    hil.ActorRef{Kind: hil.ActorService, ID: todoHILReplayActorID},
	}
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err = service.GetTodo(ctx, approver, task.TodoID)
	if err != nil || pending.Automation == nil || pending.Automation.Status != TodoAutomationStatusPendingApproval {
		t.Fatalf("pending HIL resolution changed automation = %+v err=%v", pending.Automation, err)
	}
}

func TestTodoAutomationHILLinkRetriesWithoutChangingLegacyPendingState(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	hilService := &todoHILDecisionStub{decisionID: "hil-retry-1", err: errors.New("HIL temporarily unavailable")}
	service := NewService(ServiceOptions{
		Clock: clock.NewFake(todoTestTime), IDGenerator: idgen.NewDeterministic("automation-hil-retry"), Repository: NewRepository(env.DBRouter),
		AutomationDiscussion: &todoAutomationDiscussionStub{}, MissionExecution: todoMissionExecutionStub{}, HILDecisionService: hilService,
		AutomationAgentIDs: []string{"lead-agent", "coordinator-agent"},
	})
	owner := testViewer("tenant-a", "owner-retry")
	task := mustCreateTask(t, service, ctx, owner, automationTodoRequest(CreateTodoRequest{Title: "Retry HIL link"}))
	if err := service.RunAutomationScan(ctx); err == nil {
		t.Fatal("first scan error = nil, want temporary HIL failure")
	}
	pending, err := service.GetTodo(ctx, owner, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Automation == nil || pending.Automation.Status != TodoAutomationStatusPendingApproval || pending.Automation.HILDecisionID != "" {
		t.Fatalf("legacy pending state after HIL failure = %+v", pending.Automation)
	}
	hilService.err = nil
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	linked, err := service.GetTodo(ctx, owner, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	if linked.Automation == nil || linked.Automation.Status != TodoAutomationStatusPendingApproval || linked.Automation.HILDecisionID != "hil-retry-1" {
		t.Fatalf("retried HIL link state = %+v", linked.Automation)
	}
	if len(hilService.inputs) != 2 || hilService.inputs[0].IdempotencyKey != hilService.inputs[1].IdempotencyKey {
		t.Fatalf("HIL retry inputs = %+v", hilService.inputs)
	}
}

func TestTodoAutomationEmitsRequiredPlanAndExecutionAuditActions(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	audit := &todoAutomationAuditStub{}
	service := NewService(ServiceOptions{
		Clock: clock.NewFake(todoTestTime), IDGenerator: idgen.NewDeterministic("automation-audit"), Repository: NewRepository(env.DBRouter),
		AutomationDiscussion: &todoAutomationDiscussionStub{}, AutomationAudit: audit, MissionExecution: todoMissionExecutionStub{},
		AutomationAgentIDs: []string{"lead-agent", "coordinator-agent"},
	})
	owner := testViewer("tenant-a", "owner-a")
	request := automationTodoRequest(CreateTodoRequest{Title: "Audit required lifecycle"})
	request.AutomationMode = TodoAutomationModeManaged
	task := mustCreateTask(t, service, ctx, owner, request)
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	planned, err := service.GetTodo(ctx, owner, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.DecideTodoAutomationApproval(ctx, owner, DecideTodoAutomationApprovalRequest{
		ApprovalID: planned.Automation.ApprovalID, ActionID: "approve", PlanDigest: planned.Automation.PlanDigest, IdempotencyKey: "approve-audited-lifecycle",
	}); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{
		"todo.automation.claimed",
		"todo.execution_plan.created",
		"todo.execution_plan.approved",
		"todo.mission.binding_created",
		"todo.mission.bound",
		"todo.execution.started",
	} {
		if !audit.hasAction(action) {
			t.Fatalf("missing audit action %q in %+v", action, audit.events)
		}
	}
}

func TestTodoAutomationAuditOutageLeavesDurableIntentsAndScheduledScanRecoversThem(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	auditFailure := errors.New("audit unavailable")
	audit := &todoAutomationAuditStub{err: auditFailure}
	service := NewService(ServiceOptions{
		Clock: clock.NewFake(todoTestTime), IDGenerator: idgen.NewDeterministic("automation-audit-recovery"), Repository: repo,
		AutomationDiscussion: &todoAutomationDiscussionStub{}, AutomationAudit: audit,
		AutomationAgentIDs: []string{"lead-agent", "coordinator-agent"},
	})
	owner := testViewer("tenant-a", "owner-a")
	task := mustCreateTask(t, service, ctx, owner, automationTodoRequest(CreateTodoRequest{Title: "Recover audit delivery"}))
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatalf("planning during audit outage: %v", err)
	}
	state, err := repo.GetTodoAutomation(ctx, owner.TenantID, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.pendingAuditEvents) < 3 {
		t.Fatalf("pending audit events = %+v, want claimed, plan-created, and proposal events", state.pendingAuditEvents)
	}
	seenIDs := map[string]bool{}
	for _, event := range state.pendingAuditEvents {
		if event.AuditID == "" || event.TenantID != owner.TenantID || seenIDs[event.AuditID] {
			t.Fatalf("invalid durable audit event = %+v", event)
		}
		seenIDs[event.AuditID] = true
	}

	audit.err = nil
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatalf("recover audit intents: %v", err)
	}
	state, err = repo.GetTodoAutomation(ctx, owner.TenantID, task.TodoID)
	if err != nil || len(state.pendingAuditEvents) != 0 {
		t.Fatalf("recovered state pending audits = %+v, err=%v", state.pendingAuditEvents, err)
	}
	if len(audit.events) != len(seenIDs) {
		t.Fatalf("recovered audit events = %+v, want %d", audit.events, len(seenIDs))
	}
	recoveredCount := len(audit.events)
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	if len(audit.events) != recoveredCount {
		t.Fatalf("audit replay created duplicates: before=%d after=%d", recoveredCount, len(audit.events))
	}
}

func TestTodoAutomationReopenedTodoEmitsNewAttemptAudit(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	audit := &todoAutomationAuditStub{}
	service := NewService(ServiceOptions{
		Clock: clock.NewFake(todoTestTime), IDGenerator: idgen.NewDeterministic("automation-reopen-audit"), Repository: repo,
		AutomationDiscussion: &todoAutomationDiscussionStub{}, AutomationAudit: audit, MissionExecution: attemptAwareTodoMissionExecutionStub{},
		AutomationAgentIDs: []string{"lead-agent", "coordinator-agent"},
	})
	owner := testViewer("tenant-a", "owner-a")
	request := automationTodoRequest(CreateTodoRequest{Title: "First execution"})
	request.AutomationMode = TodoAutomationModeManaged
	task := mustCreateTask(t, service, ctx, owner, request)
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	first, err := service.GetTodo(ctx, owner, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.DecideTodoAutomationApproval(ctx, owner, DecideTodoAutomationApprovalRequest{
		ApprovalID: first.Automation.ApprovalID, ActionID: "approve", PlanDigest: first.Automation.PlanDigest, IdempotencyKey: "approve-first-attempt",
	}); err != nil {
		t.Fatal(err)
	}
	binding, err := repo.GetTodoMissionBinding(ctx, owner.TenantID, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	binding.Status = TodoMissionBindingStatusExecuted
	binding.UpdatedAt = todoTestTime.Add(time.Minute)
	if err := repo.SaveTodoMissionBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	updatedTitle := "Second execution"
	expectedVersion := first.Version
	if _, err := service.UpdateTodo(ctx, owner, UpdateTodoRequest{TodoID: task.TodoID, Title: &updatedTitle, Version: &expectedVersion}); err != nil {
		t.Fatal(err)
	}
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := service.GetTodo(ctx, owner, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Automation == nil || second.Automation.Plan.ProposalRevision != 2 {
		t.Fatalf("reopened Todo plan = %+v", second.Automation)
	}
	if _, err := service.DecideTodoAutomationApproval(ctx, owner, DecideTodoAutomationApprovalRequest{
		ApprovalID: second.Automation.ApprovalID, ActionID: "approve", PlanDigest: second.Automation.PlanDigest, IdempotencyKey: "approve-second-attempt",
	}); err != nil {
		t.Fatal(err)
	}
	current, err := repo.GetTodoMissionBinding(ctx, owner.TenantID, task.TodoID)
	if err != nil || current.ExecutionAttempt != 2 || current.MissionID != "mission-attempt-2" {
		t.Fatalf("reopened Todo binding = %+v err=%v", current, err)
	}
	if !audit.hasAction("todo.mission.attempt_reopened") {
		t.Fatalf("reopened attempt audit events = %+v", audit.events)
	}
}

func TestCheckTodoAutomationPlansOnlyTheAuthorizedRequestedTodo(t *testing.T) {
	service, _, discussion, closeEnv := newTodoAutomationServiceForTest(t)
	defer closeEnv()
	ctx := context.Background()
	owner := testViewer("tenant-a", "owner-a")
	requested := mustCreateTask(t, service, ctx, owner, automationTodoRequest(CreateTodoRequest{Title: "Check this Todo"}))
	unrelated := mustCreateTask(t, service, ctx, owner, automationTodoRequest(CreateTodoRequest{Title: "Do not scan this Todo"}))

	result, err := service.CheckTodoAutomation(ctx, owner, CheckTodoAutomationRequest{TodoID: requested.TodoID, IdempotencyKey: "manual-check-1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.ScannedCount != 1 || result.EligibleCount != 1 || result.ClaimedCount != 1 || result.PlannedCount != 1 || result.SkippedCount != 0 {
		t.Fatalf("manual check result = %+v", result)
	}
	if len(discussion.commands) != 1 {
		t.Fatalf("discussion commands = %d, want one", len(discussion.commands))
	}
	loadedUnrelated, err := service.GetTodo(ctx, owner, unrelated.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	if loadedUnrelated.Automation != nil || loadedUnrelated.DiscussionBinding != nil {
		t.Fatalf("manual check touched unrelated Todo: %+v", loadedUnrelated)
	}

	duplicate, err := service.CheckTodoAutomation(ctx, owner, CheckTodoAutomationRequest{TodoID: requested.TodoID, IdempotencyKey: "manual-check-1"})
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.ScannedCount != 1 || duplicate.SkippedCount != 1 || duplicate.ClaimedCount != 0 || len(discussion.commands) != 1 {
		t.Fatalf("duplicate manual check result=%+v commands=%d", duplicate, len(discussion.commands))
	}
}

func TestNormalizeTodoExecutionPlanStepsRejectsDependencyCycles(t *testing.T) {
	plan := TodoExecutionPlan{Steps: []TodoExecutionPlanStep{
		{StepID: "step-a", Title: "A", AssigneeAgentID: "lead-agent", AcceptanceCriteria: []string{"A done"}, DependsOn: []string{"step-b"}},
		{StepID: "step-b", Title: "B", AssigneeAgentID: "lead-agent", AcceptanceCriteria: []string{"B done"}, DependsOn: []string{"step-c"}},
		{StepID: "step-c", Title: "C", AssigneeAgentID: "lead-agent", AcceptanceCriteria: []string{"C done"}, DependsOn: []string{"step-a"}},
	}}
	err := normalizeTodoExecutionPlanSteps(&plan, TodoItem{AcceptanceCriteria: "Done"}, []string{"lead-agent"})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("cycle error = %v, want ErrInvalidRequest", err)
	}
}

func TestExecutionCommandPolicyRejectsShellAndArgumentInjection(t *testing.T) {
	configured := map[string]bool{"go test ./...": true}
	if !executionCommandAllowed("go test ./...", []string{"go test"}, configured) {
		t.Fatal("configured command was rejected")
	}
	for _, command := range []string{
		"go test ./...; Remove-Item -Recurse .",
		"go test ./... && curl example.invalid",
		"go test ./... $(whoami)",
		"go test -exec=malicious ./...",
		"go test ./... --script-shell=powershell",
	} {
		if executionCommandAllowed(command, []string{"go test"}, configured) {
			t.Fatalf("unsafe command was accepted: %q", command)
		}
	}
}

func TestTodoAutomationDiscussionIncludesProposerAssigneeAndReviewer(t *testing.T) {
	service, _, discussion, closeEnv := newTodoAutomationServiceForTest(t)
	defer closeEnv()
	ctx := context.Background()
	owner := testViewer("tenant-a", "owner-a")
	mustCreateTask(t, service, ctx, owner, automationTodoRequest(CreateTodoRequest{
		Title:        "Coordinate implementation",
		AssigneeID:   "assignee-a",
		AssigneeName: "Assignee A",
		AssigneeKind: AssigneeKindHuman,
		ReviewerID:   "reviewer-a",
		ReviewerName: "Reviewer A",
	}))

	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	if len(discussion.commands) != 1 || len(discussion.viewers) != 1 || len(discussion.ensured) != 1 || len(discussion.ensureViewers) != 1 {
		t.Fatalf("commands=%d viewers=%d ensured=%d ensure_viewers=%d", len(discussion.commands), len(discussion.viewers), len(discussion.ensured), len(discussion.ensureViewers))
	}
	if discussion.ensureViewers[0].ViewerID != owner.ViewerID {
		t.Fatalf("discussion creator = %q, want proposer %q", discussion.ensureViewers[0].ViewerID, owner.ViewerID)
	}
	if discussion.viewers[0].ViewerID != todoAutomationActorID || discussion.viewers[0].ViewerKind != discussionsession.ViewerKindSystem {
		t.Fatalf("planning prompt actor = %+v, want Todo automation system identity", discussion.viewers[0])
	}
	wantMembers := []string{"lead-agent", "coordinator-agent", "assignee-a", "reviewer-a"}
	for _, memberID := range wantMembers {
		if !containsFold(discussion.ensured[0].InitialMemberIDs, memberID) {
			t.Fatalf("initial members = %v, missing %q", discussion.ensured[0].InitialMemberIDs, memberID)
		}
	}
}

func TestTodoAutomationDiscussionUsesHumanAuthorizationSubjects(t *testing.T) {
	service, _, discussion, closeEnv := newTodoAutomationServiceForTest(t)
	defer closeEnv()
	ctx := context.Background()
	owner := testViewer("tenant-a", "owner-login")
	owner.AuthorizationSubjectID = "human-owner"
	task := mustCreateTask(t, service, ctx, owner, automationTodoRequest(CreateTodoRequest{
		Title:        "Coordinate production identities",
		AssigneeID:   "human-assignee",
		AssigneeKind: AssigneeKindHuman,
		ReviewerID:   "human-reviewer",
	}))
	if task.CreatedBySubjectID != owner.AuthorizationSubjectID {
		t.Fatalf("created Todo subject = %q, want %q", task.CreatedBySubjectID, owner.AuthorizationSubjectID)
	}

	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	if len(discussion.ensureViewers) != 1 || discussion.ensureViewers[0].ViewerID != owner.AuthorizationSubjectID || discussion.ensureViewers[0].AgentProfileID != owner.AuthorizationSubjectID {
		t.Fatalf("discussion creator = %+v, want authorization subject %q", discussion.ensureViewers, owner.AuthorizationSubjectID)
	}
	for _, memberID := range []string{"human-assignee", "human-reviewer"} {
		if !containsFold(discussion.ensured[0].InitialMemberIDs, memberID) {
			t.Fatalf("initial members = %v, missing %q", discussion.ensured[0].InitialMemberIDs, memberID)
		}
	}
	if containsFold(discussion.ensured[0].InitialMemberIDs, owner.ViewerID) {
		t.Fatalf("initial members contain account username instead of its directory profile: %v", discussion.ensured[0].InitialMemberIDs)
	}

	item, err := service.GetTodo(ctx, owner, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	state := *item.Automation
	state.DecisionBy = owner.ViewerID
	state.decisionAuthorizationSubjectID = owner.AuthorizationSubjectID
	members := todoAutomationDiscussionMemberIDs(item, state, service.automationAgentIDs, service.executionSupportIDs, "")
	if !containsFold(members, owner.AuthorizationSubjectID) || containsFold(members, owner.ViewerID) {
		t.Fatalf("post-decision members = %v", members)
	}
}

func TestTodoAutomationReusesDiscussionAfterTodoChanges(t *testing.T) {
	service, _, discussion, closeEnv := newTodoAutomationServiceForTest(t)
	defer closeEnv()
	ctx := context.Background()
	owner := testViewer("tenant-a", "owner-a")
	task := mustCreateTask(t, service, ctx, owner, automationTodoRequest(CreateTodoRequest{Title: "Implement shared chat", AssigneeID: "assignee-a", AssigneeName: "Assignee A"}))
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}

	changedTitle := "Implement shared chat with revised scope"
	updated, err := service.UpdateTodo(ctx, owner, UpdateTodoRequest{TodoID: task.TodoID, Title: &changedTitle, Version: &task.Version})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}

	if len(discussion.commands) != 2 {
		t.Fatalf("discussion commands = %d, want one new turn in the existing chat", len(discussion.commands))
	}
	if discussion.commands[1].ConversationID != "discussion-plan" {
		t.Fatalf("second discussion id = %q", discussion.commands[1].ConversationID)
	}
	if len(discussion.memberCommands) != 0 {
		t.Fatalf("unchanged members should not be synchronized again: %+v", discussion.memberCommands)
	}
	loaded, err := service.GetTodo(ctx, owner, updated.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Automation == nil || loaded.Automation.ConversationID != "discussion-plan" {
		t.Fatalf("automation = %+v", loaded.Automation)
	}
}

func TestTodoAutomationPlanningDoesNotOverwriteMutationInvalidation(t *testing.T) {
	service, repo, discussion, closeEnv := newTodoAutomationServiceForTest(t)
	defer closeEnv()
	ctx := context.Background()
	owner := testViewer("tenant-a", "owner-a")
	task := mustCreateTask(t, service, ctx, owner, automationTodoRequest(CreateTodoRequest{Title: "Original scope"}))
	discussion.sendHook = func() error {
		discussion.sendHook = nil
		changed := "Changed while the plan was being discussed"
		_, err := service.UpdateTodo(ctx, owner, UpdateTodoRequest{TodoID: task.TodoID, Title: &changed, Version: &task.Version})
		return err
	}

	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := repo.GetTodoAutomation(ctx, owner.TenantID, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != TodoAutomationStatusStale || state.TodoVersion != task.Version {
		t.Fatalf("planning completion overwrote mutation invalidation: %+v", state)
	}
	if state.PlanDigest != "" || state.ApprovalID != "" {
		t.Fatalf("stale planning claim retained an approvable plan: %+v", state)
	}
}

func TestTodoAutomationPlanningDoesNotOverwriteNewerProposal(t *testing.T) {
	service, repo, discussion, closeEnv := newTodoAutomationServiceForTest(t)
	defer closeEnv()
	ctx := context.Background()
	owner := testViewer("tenant-a", "owner-a")
	task := mustCreateTask(t, service, ctx, owner, automationTodoRequest(CreateTodoRequest{Title: "Original scope"}))
	discussion.sendHook = func() error {
		discussion.sendHook = nil
		changed := "New proposal scope"
		updated, err := service.UpdateTodo(ctx, owner, UpdateTodoRequest{TodoID: task.TodoID, Title: &changed, Version: &task.Version})
		if err != nil {
			return err
		}
		claimed, err := repo.ClaimTodoAutomation(ctx, updated, TodoAutomationState{
			TenantID: updated.TenantID, TodoID: updated.TodoID, TodoVersion: updated.Version,
			ProposalID: "newer-proposal", Status: TodoAutomationStatusDiscussing,
			RequestedBy: todoAutomationActorID, CreatedAt: todoTestTime.Add(time.Minute), UpdatedAt: todoTestTime.Add(time.Minute),
		}, todoTestTime.Add(time.Minute))
		if err != nil {
			return err
		}
		if !claimed {
			return errors.New("newer proposal was not claimed")
		}
		return nil
	}

	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := repo.GetTodoAutomation(ctx, owner.TenantID, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	if state.ProposalID != "newer-proposal" || state.TodoVersion != task.Version+1 || state.Status != TodoAutomationStatusDiscussing {
		t.Fatalf("old planning completion overwrote newer proposal: %+v", state)
	}
}

func TestTodoAutomationPeriodicRecheckReusesDiscussion(t *testing.T) {
	service, repo, discussion, closeEnv := newTodoAutomationServiceForTest(t)
	defer closeEnv()
	ctx := context.Background()
	owner := testViewer("tenant-a", "owner-a")
	task := mustCreateTask(t, service, ctx, owner, automationTodoRequest(CreateTodoRequest{Title: "Wait for dependency"}))
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := repo.GetTodoAutomation(ctx, owner.TenantID, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	state.Status = TodoAutomationStatusNotReady
	state.NextCheckAt = todoTestTime
	if err := repo.SaveTodoAutomation(ctx, state); err != nil {
		t.Fatal(err)
	}

	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	if len(discussion.commands) != 2 || discussion.commands[1].ConversationID != "discussion-plan" {
		t.Fatalf("periodic recheck commands = %+v", discussion.commands)
	}
}

func TestTodoAutomationBackfillsLegacyDiscussionMembersWithoutStartingNewTurn(t *testing.T) {
	service, repo, discussion, closeEnv := newTodoAutomationServiceForTest(t)
	defer closeEnv()
	ctx := context.Background()
	owner := testViewer("tenant-a", "owner-a")
	task := mustCreateTask(t, service, ctx, owner, automationTodoRequest(CreateTodoRequest{Title: "Existing automation", AssigneeID: "assignee-a", AssigneeName: "Assignee A"}))
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := repo.GetTodoAutomation(ctx, owner.TenantID, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	recordKey := scopedRecordKey(state.TenantID, state.TodoID)
	record, ok, err := repo.router.GetRecord(ctx, todoAutomationTable, recordKey)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("automation record is missing")
	}
	delete(record, todoAutomationDiscussionMembersFingerprintField)
	if err := repo.router.PutRecord(ctx, todoAutomationTable, recordKey, record); err != nil {
		t.Fatal(err)
	}

	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	if len(discussion.commands) != 1 {
		t.Fatalf("discussion commands = %d, membership backfill must not start a new turn", len(discussion.commands))
	}
	if len(discussion.memberCommands) != 1 || discussion.memberCommands[0].ConversationID != "discussion-plan" ||
		!containsFold(discussion.memberCommands[0].TargetIDs, "assignee-a") ||
		!containsFold(discussion.memberCommands[0].TargetIDs, "qa-plan-agent") ||
		!containsFold(discussion.memberCommands[0].TargetIDs, "qa-check-agent") {
		t.Fatalf("member backfill = %+v", discussion.memberCommands)
	}
	updated, err := repo.GetTodoAutomation(ctx, owner.TenantID, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.discussionMembersFingerprint == "" {
		t.Fatal("discussion member fingerprint was not persisted")
	}
}

func TestTodoAutomationAddsChangedReviewerBeforeReusingDiscussion(t *testing.T) {
	service, _, discussion, closeEnv := newTodoAutomationServiceForTest(t)
	defer closeEnv()
	ctx := context.Background()
	agent := testViewer("tenant-a", "agent-a")
	agent.ViewerKind = ViewerKindAgent
	task := mustCreateTask(t, service, ctx, agent, automationTodoRequest(CreateTodoRequest{Title: "Agent-created work", ReviewerID: "reviewer-old", ReviewerName: "Old Reviewer"}))
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}

	newReviewerID := "reviewer-new"
	newReviewerName := "New Reviewer"
	if _, err := service.UpdateTodo(ctx, agent, UpdateTodoRequest{
		TodoID:       task.TodoID,
		ReviewerID:   &newReviewerID,
		ReviewerName: &newReviewerName,
		Version:      &task.Version,
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}

	if len(discussion.memberCommands) != 1 || len(discussion.memberViewers) != 1 {
		t.Fatalf("member commands=%d viewers=%d", len(discussion.memberCommands), len(discussion.memberViewers))
	}
	if discussion.memberViewers[0].ViewerID != todoAutomationActorID || discussion.memberViewers[0].ViewerKind != discussionsession.ViewerKindSystem {
		t.Fatalf("membership caller = %+v, want scoped system identity", discussion.memberViewers[0])
	}
	if !containsFold(discussion.memberCommands[0].TargetIDs, newReviewerID) {
		t.Fatalf("member targets = %v, missing new reviewer", discussion.memberCommands[0].TargetIDs)
	}
	if containsFold(discussion.memberCommands[0].TargetIDs, "reviewer-old") {
		t.Fatalf("member targets = %v, stale reviewer must be revoked", discussion.memberCommands[0].TargetIDs)
	}
	if len(discussion.commands) != 2 || discussion.commands[1].ConversationID != "discussion-plan" || len(discussion.ensureViewers) != 2 || discussion.ensureViewers[1].ViewerID != newReviewerID || discussion.viewers[1].ViewerKind != discussionsession.ViewerKindSystem {
		t.Fatalf("reused command=%+v creator=%+v prompt_actor=%+v", discussion.commands, discussion.ensureViewers, discussion.viewers)
	}
}

func TestTodoExecutionPlanRejectsUnconfiguredAssignee(t *testing.T) {
	proposal := `{"version":3,"summary":"Implement safely","steps":[{"step_id":"step-1","title":"Implement","assignee_agent_id":"invented-agent","acceptance_criteria":["done"],"evidence_requirements":[{"evidence_requirement_id":"evidence-step-1-1","acceptance_criterion":"done","kind":"artifact","required":true}]}]}`
	item := TodoItem{TenantID: "tenant-a", TodoID: "todo-1", Version: 1, AcceptanceCriteria: "done"}
	state := TodoAutomationState{TenantID: item.TenantID, TodoID: item.TodoID, TodoVersion: item.Version, ProposalID: "proposal-1", ConversationID: "discussion-1", CreatedAt: todoTestTime, UpdatedAt: todoTestTime}
	_, err := todoExecutionPlanFromProposal(proposal, item, state, 1, TodoDevelopmentExecutionPlanContext{AllowedNetworkMode: "none"}, []string{"lead-agent", "coordinator-agent"})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("plan error = %v, want ErrInvalidRequest", err)
	}
}

func TestTodoExecutionPlanRequiresExplicitEvidenceForEveryAcceptanceCriterion(t *testing.T) {
	proposal := `{"version":3,"summary":"Update the external record","steps":[{"step_id":"step-1","title":"Update record","assignee_agent_id":"lead-agent","acceptance_criteria":["The external record is updated"],"evidence_requirements":[]}]}`
	item := TodoItem{TenantID: "tenant-a", TodoID: "todo-1", Version: 1, AcceptanceCriteria: "The external record is updated"}
	state := TodoAutomationState{TenantID: item.TenantID, TodoID: item.TodoID, TodoVersion: item.Version, ProposalID: "proposal-1", ConversationID: "discussion-1", CreatedAt: todoTestTime, UpdatedAt: todoTestTime}
	_, err := todoExecutionPlanFromProposal(proposal, item, state, 1, TodoDevelopmentExecutionPlanContext{AllowedNetworkMode: "none"}, []string{"lead-agent"})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing evidence error = %v, want ErrInvalidRequest", err)
	}
}

func TestTodoExecutionPlanPreservesApprovedExternalEvidenceKind(t *testing.T) {
	proposal := `{"version":3,"summary":"Update the external record","steps":[{"step_id":"step-1","title":"Update record","assignee_agent_id":"lead-agent","acceptance_criteria":["The external record is updated"],"evidence_requirements":[{"evidence_requirement_id":"evidence-step-1-1","acceptance_criterion":"The external record is updated","kind":"tool_log","required":true}]}]}`
	item := TodoItem{TenantID: "tenant-a", TodoID: "todo-1", Version: 1, AcceptanceCriteria: "The external record is updated"}
	state := TodoAutomationState{TenantID: item.TenantID, TodoID: item.TodoID, TodoVersion: item.Version, ProposalID: "proposal-1", ConversationID: "discussion-1", CreatedAt: todoTestTime, UpdatedAt: todoTestTime}
	plan, err := todoExecutionPlanFromProposal(proposal, item, state, 1, TodoDevelopmentExecutionPlanContext{AllowedNetworkMode: "none"}, []string{"lead-agent"})
	if err != nil {
		t.Fatalf("todoExecutionPlanFromProposal: %v", err)
	}
	if len(plan.Steps[0].EvidenceRequirements) != 1 || plan.Steps[0].EvidenceRequirements[0].Kind != "tool_log" || !plan.Steps[0].EvidenceRequirements[0].Required {
		t.Fatalf("evidence requirements = %+v", plan.Steps[0].EvidenceRequirements)
	}
}

func TestTodoExecutionPlanCreatesRootStepWhenProposalHasNoExplicitSteps(t *testing.T) {
	proposal := `{"version":3,"summary":"Coordinate the Todo","steps":[]}`
	item := TodoItem{TenantID: "tenant-a", TodoID: "todo-1", Title: "Prepare release notes", Version: 1, AcceptanceCriteria: "Release notes are approved"}
	state := TodoAutomationState{TenantID: item.TenantID, TodoID: item.TodoID, TodoVersion: item.Version, ProposalID: "proposal-1", ConversationID: "discussion-1", CreatedAt: todoTestTime, UpdatedAt: todoTestTime}
	plan, err := todoExecutionPlanFromProposal(proposal, item, state, 1, TodoDevelopmentExecutionPlanContext{AllowedNetworkMode: "none"}, []string{"lead-agent"})
	if err != nil {
		t.Fatalf("todoExecutionPlanFromProposal: %v", err)
	}
	if len(plan.Steps) != 1 || plan.Steps[0].StepID != "root" || plan.Steps[0].Title != "完成 Todo：Prepare release notes" || plan.Steps[0].AssigneeAgentID != "lead-agent" {
		t.Fatalf("root execution step = %+v", plan.Steps)
	}
	if len(plan.Steps[0].EvidenceRequirements) != 1 || plan.Steps[0].EvidenceRequirements[0].AcceptanceCriterion != "Release notes are approved" {
		t.Fatalf("root evidence requirements = %+v", plan.Steps[0].EvidenceRequirements)
	}
}

func TestTodoProposalUsesOnlyLatestDiscussionTurn(t *testing.T) {
	proposal := todoProposalFromConversation(discussionsession.ConversationDTO{Messages: []discussionsession.MessageDTO{
		{Role: "user", Content: "first review"},
		{Role: "assistant", AuthorName: "Lead", Content: "TODO_AUTOMATION_NOT_READY\nmissing input"},
		{Role: "user", Content: "review again"},
		{Role: "assistant", AuthorName: "Lead", Content: "TODO_AUTOMATION_PROPOSAL\nexecute the revised plan"},
	}})
	if strings.Contains(proposal, "missing input") || !strings.Contains(proposal, "execute the revised plan") {
		t.Fatalf("proposal = %q", proposal)
	}
}

func TestTodoAutomationCandidateBatchSkipsAlreadyClaimedTodos(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := NewService(ServiceOptions{
		Clock:       clock.NewFake(todoTestTime),
		IDGenerator: idgen.NewDeterministic("todo"),
		Repository:  repo,
	})
	ctx := context.Background()
	owner := testViewer("tenant-a", "owner-a")
	first := mustCreateTask(t, service, ctx, owner, automationTodoRequest(CreateTodoRequest{Title: "Already claimed"}))
	second := mustCreateTask(t, service, ctx, owner, automationTodoRequest(CreateTodoRequest{Title: "Still eligible"}))
	if err := repo.SaveTodoAutomation(ctx, TodoAutomationState{
		TenantID: first.TenantID, ProposalID: "proposal-existing", ApprovalID: "approval-existing", TodoID: first.TodoID,
		TodoVersion: first.Version, Status: TodoAutomationStatusPendingApproval, RequestedBy: todoAutomationActorID,
		ApproverID: owner.ViewerID, CreatedAt: todoTestTime, UpdatedAt: todoTestTime,
	}); err != nil {
		t.Fatal(err)
	}

	candidates, err := repo.ListTodoAutomationCandidates(ctx, todoTestTime, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].TodoID != second.TodoID {
		t.Fatalf("candidates = %+v", candidates)
	}
}

func TestTodoAutomationCandidateBatchIsTenantFair(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := NewService(ServiceOptions{Clock: clock.NewFake(todoTestTime), IDGenerator: idgen.NewDeterministic("todo"), Repository: repo})
	ctx := context.Background()
	for index := 0; index < 3; index++ {
		mustCreateTask(t, service, ctx, testViewer("tenant-a", "owner-a"), automationTodoRequest(CreateTodoRequest{Title: fmt.Sprintf("Tenant A %d", index)}))
	}
	mustCreateTask(t, service, ctx, testViewer("tenant-b", "owner-b"), automationTodoRequest(CreateTodoRequest{Title: "Tenant B"}))

	candidates, err := repo.ListTodoAutomationCandidates(ctx, todoTestTime, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].TenantID == candidates[1].TenantID {
		t.Fatalf("candidates = %+v, want one candidate per tenant", candidates)
	}
}

func TestTodoAutomationApprovalsAreTenantAndApproverScoped(t *testing.T) {
	service, _, _, closeEnv := newTodoAutomationServiceForTest(t)
	defer closeEnv()
	ctx := context.Background()
	owner := testViewer("tenant-a", "owner-a")
	mustCreateTask(t, service, ctx, owner, automationTodoRequest(CreateTodoRequest{Title: "Scoped work", ReviewerID: "reviewer-a"}))
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}

	for name, viewer := range map[string]ViewerRef{
		"other tenant":   testViewer("tenant-b", "reviewer-a"),
		"wrong reviewer": testViewer("tenant-a", "reviewer-a"),
	} {
		t.Run(name, func(t *testing.T) {
			items, err := service.ListTodoAutomationApprovals(ctx, viewer, ListTodoAutomationApprovalsQuery{})
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 0 {
				t.Fatalf("items = %+v", items)
			}
		})
	}
	items, err := service.ListTodoAutomationApprovals(ctx, owner, ListTodoAutomationApprovalsQuery{})
	if err != nil || len(items) != 1 {
		t.Fatalf("reviewer items = %+v err=%v", items, err)
	}
}

func TestTodoAutomationApprovalAcceptsBoundHumanProfileIdentity(t *testing.T) {
	service, repo, _, closeEnv := newTodoAutomationServiceForTest(t)
	defer closeEnv()
	ctx := context.Background()
	owner := testViewer("tenant-a", "owner-login")
	owner.AuthorizationSubjectID = "subject-owner"
	task := mustCreateTask(t, service, ctx, owner, automationTodoRequest(CreateTodoRequest{Title: "Profile-scoped approval"}))
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := repo.GetTodoAutomation(ctx, owner.TenantID, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	state.ApproverID = owner.AuthorizationSubjectID
	if err := repo.SaveTodoAutomation(ctx, state); err != nil {
		t.Fatal(err)
	}

	approvals, err := service.ListTodoAutomationApprovals(ctx, owner, ListTodoAutomationApprovalsQuery{Status: "pending"})
	if err != nil || len(approvals) != 1 || approvals[0].ApproverID != owner.AuthorizationSubjectID {
		t.Fatalf("profile-scoped approvals = %+v err=%v", approvals, err)
	}
	decided, err := service.DecideTodoAutomationApproval(ctx, owner, DecideTodoAutomationApprovalRequest{
		ApprovalID: state.ApprovalID, ActionID: "reject", Message: "Needs revision", PlanDigest: state.PlanDigest, IdempotencyKey: "profile-reject-1",
	})
	if err != nil || decided.Status != TodoAutomationStatusRejected || decided.DecisionBy != owner.ViewerID || decided.decisionAuthorizationSubjectID != owner.AuthorizationSubjectID {
		t.Fatalf("profile-scoped decision = %+v err=%v", decided, err)
	}
}

func TestTodoAutomationRequiresHumanApproverForAgentCreatedTodos(t *testing.T) {
	service, _, discussion, closeEnv := newTodoAutomationServiceForTest(t)
	defer closeEnv()
	ctx := context.Background()
	agent := testViewer("tenant-a", "agent-a")
	agent.ViewerKind = ViewerKindAgent
	withoutReviewer := mustCreateTask(t, service, ctx, agent, automationTodoRequest(CreateTodoRequest{Title: "Agent work without reviewer"}))
	withReviewer := mustCreateTask(t, service, ctx, agent, automationTodoRequest(CreateTodoRequest{Title: "Agent work with reviewer", ReviewerID: "reviewer-a"}))

	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	if len(discussion.commands) != 1 {
		t.Fatalf("discussion commands = %d, want only the human-reviewed Todo", len(discussion.commands))
	}
	skipped, err := service.GetTodo(ctx, agent, withoutReviewer.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	if skipped.Automation != nil {
		t.Fatalf("agent-created Todo without a human reviewer was automated: %+v", skipped.Automation)
	}
	planned, err := service.GetTodo(ctx, agent, withReviewer.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	if planned.Automation == nil || planned.Automation.ApproverID != "reviewer-a" {
		t.Fatalf("automation = %+v, want reviewer-a approval", planned.Automation)
	}
}

func TestTodoAutomationRejectsStaleTodoVersionBeforeExecution(t *testing.T) {
	service, _, discussion, closeEnv := newTodoAutomationServiceForTest(t)
	defer closeEnv()
	ctx := context.Background()
	owner := testViewer("tenant-a", "owner-a")
	task := mustCreateTask(t, service, ctx, owner, automationTodoRequest(CreateTodoRequest{Title: "Versioned work"}))
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	planned, err := service.GetTodo(ctx, owner, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	newTitle := "Changed after planning"
	if _, err := service.UpdateTodo(ctx, owner, UpdateTodoRequest{TodoID: task.TodoID, Title: &newTitle, Version: &planned.Version}); err != nil {
		t.Fatal(err)
	}
	_, err = service.DecideTodoAutomationApproval(ctx, owner, DecideTodoAutomationApprovalRequest{
		ApprovalID: planned.Automation.ApprovalID, ActionID: "approve", PlanDigest: planned.Automation.PlanDigest, IdempotencyKey: "approve-stale",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("approve stale error = %v, want ErrConflict", err)
	}
	state, err := service.repo.GetTodoAutomation(ctx, owner.TenantID, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != TodoAutomationStatusStale || len(discussion.commands) != 1 {
		t.Fatalf("state = %+v commands=%d", state, len(discussion.commands))
	}
}

func TestTodoAutomationApprovalContinuesExecutionInOriginalGroupChat(t *testing.T) {
	service, repo, discussion, closeEnv := newTodoAutomationServiceForTest(t)
	defer closeEnv()
	ctx := context.Background()
	owner := testViewer("tenant-a", "owner-a")
	request := automationTodoRequest(CreateTodoRequest{Title: "Execute approved work"})
	request.AutomationMode = TodoAutomationModeManaged
	task := mustCreateTask(t, service, ctx, owner, request)
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	planned, err := service.GetTodo(ctx, owner, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := service.DecideTodoAutomationApproval(ctx, owner, DecideTodoAutomationApprovalRequest{
		ApprovalID: planned.Automation.ApprovalID, ActionID: "approve", Message: "Proceed with the reviewed plan", PlanDigest: planned.Automation.PlanDigest, IdempotencyKey: "approve-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != TodoAutomationStatusExecuting || !state.ExecutedAt.IsZero() {
		t.Fatalf("state = %+v", state)
	}
	binding, err := repo.GetTodoMissionBinding(ctx, owner.TenantID, task.TodoID)
	if err != nil || binding.Status != TodoMissionBindingStatusExecuting || binding.MissionID == "" || binding.ExecutionAttempt != 1 {
		t.Fatalf("active Mission binding = %+v err=%v", binding, err)
	}
	if len(discussion.commands) != 1 || len(discussion.notices) != 1 || discussion.notices[0].ConversationID != "discussion-plan" || !strings.Contains(discussion.notices[0].Content, "Proceed with the reviewed plan") {
		t.Fatalf("planning commands=%+v execution notices=%+v", discussion.commands, discussion.notices)
	}
	comments, err := repo.ListIssueCommentsForTenant(ctx, owner.TenantID, IssueKindTodo, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 2 || !strings.Contains(comments[1].Body, "已批准") {
		t.Fatalf("comments = %+v", comments)
	}
	if _, err := service.DecideTodoAutomationApproval(ctx, owner, DecideTodoAutomationApprovalRequest{
		ApprovalID: planned.Automation.ApprovalID, ActionID: "approve", PlanDigest: planned.Automation.PlanDigest, IdempotencyKey: "approve-1",
	}); err != nil {
		t.Fatal(err)
	}
	if len(discussion.commands) != 1 || len(discussion.notices) != 1 {
		t.Fatalf("idempotent approval dispatched commands=%d notices=%d", len(discussion.commands), len(discussion.notices))
	}
}

func TestTodoAutomationExecutionDoesNotOverwriteConcurrentTodoMutation(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	discussion := &todoAutomationDiscussionStub{}
	mission := &recordingTodoMissionExecutionStub{}
	service := NewService(ServiceOptions{
		Clock: clock.NewFake(todoTestTime), IDGenerator: idgen.NewDeterministic("automation-race"), Repository: repo,
		AutomationDiscussion: discussion, MissionExecution: mission,
		AutomationAgentIDs: []string{"lead-agent", "coordinator-agent"},
	})
	owner := testViewer("tenant-a", "owner-a")
	request := automationTodoRequest(CreateTodoRequest{Title: "Execute version one"})
	request.AutomationMode = TodoAutomationModeManaged
	task := mustCreateTask(t, service, ctx, owner, request)
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	planned, err := service.GetTodo(ctx, owner, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	mission.beforeReturn = func() error {
		mission.beforeReturn = nil
		changed := "Changed during Mission materialization"
		_, err := service.UpdateTodo(ctx, owner, UpdateTodoRequest{TodoID: task.TodoID, Title: &changed, Version: &planned.Version})
		return err
	}

	_, err = service.DecideTodoAutomationApproval(ctx, owner, DecideTodoAutomationApprovalRequest{
		ApprovalID: planned.Automation.ApprovalID, ActionID: "approve", PlanDigest: planned.Automation.PlanDigest, IdempotencyKey: "approve-race",
	})
	if err == nil {
		t.Fatal("expected stale Mission materialization to stop")
	}
	state, getErr := repo.GetTodoAutomation(ctx, owner.TenantID, task.TodoID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if state.Status != TodoAutomationStatusStale || state.TodoVersion != planned.Version {
		t.Fatalf("old execution overwrote mutation invalidation: %+v", state)
	}
	binding, getErr := repo.GetTodoMissionBinding(ctx, owner.TenantID, task.TodoID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if binding.Status != TodoMissionBindingStatusPaused && binding.Status != TodoMissionBindingStatusPauseRequested {
		t.Fatalf("concurrent mutation did not stop the old Mission binding: %+v", binding)
	}
}

func TestDevelopmentTodoWaitsForManagedWorkspaceBeforeAgentDispatch(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	discussion := &todoAutomationDiscussionStub{}
	mission := &recordingTodoMissionExecutionStub{requiresWorkspace: true}
	guard := &todoDevelopmentGuardStub{}
	service := NewService(ServiceOptions{
		Clock: clock.NewFake(todoTestTime), IDGenerator: idgen.NewDeterministic("automation"), Repository: repo,
		AutomationDiscussion: discussion, MissionExecution: mission, DevelopmentExecution: guard,
		AutomationAgentIDs:     []string{"lead-agent", "coordinator-agent"},
		AllowOwnerSelfApproval: true, AutomationAudit: &todoAutomationAuditStub{},
	})
	owner := testViewer("tenant-a", "owner-a")
	request := automationTodoRequest(CreateTodoRequest{
		Title: "Implement in repository", WorkKind: TodoWorkKindDevelopment,
		DevelopmentProjectID: "project-1", ReviewerID: owner.ViewerID, ReviewerName: "Owner A",
	})
	request.AutomationMode = TodoAutomationModeManaged
	task := mustCreateTask(t, service, ctx, owner, request)
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	planned, err := service.GetTodo(ctx, owner, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := service.DecideTodoAutomationApproval(ctx, owner, DecideTodoAutomationApprovalRequest{
		ApprovalID: planned.Automation.ApprovalID, ActionID: "approve", PlanDigest: planned.Automation.PlanDigest, IdempotencyKey: "approve-development",
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != TodoAutomationStatusProvisioningWorkspace || !state.ExecutedAt.IsZero() {
		t.Fatalf("development automation state = %+v", state)
	}
	binding, err := repo.GetTodoMissionBinding(ctx, owner.TenantID, task.TodoID)
	if err != nil || binding.Status != TodoMissionBindingStatusProvisioning || binding.MissionID == "" {
		t.Fatalf("development Mission binding = %+v err=%v", binding, err)
	}
	if len(discussion.notices) != 1 || discussion.notices[0].Metadata["kind"] != "todo_workspace_provisioning_required" {
		t.Fatalf("development notices = %+v", discussion.notices)
	}
	if len(guard.approvalRequests) != 1 || len(guard.executionRequests) != 1 || len(mission.requests) != 1 {
		t.Fatalf("guard approval=%+v execution=%+v Mission requests=%+v", guard.approvalRequests, guard.executionRequests, mission.requests)
	}
	repeated, err := service.DecideTodoAutomationApproval(ctx, owner, DecideTodoAutomationApprovalRequest{
		ApprovalID: planned.Automation.ApprovalID, ActionID: "approve", PlanDigest: planned.Automation.PlanDigest, IdempotencyKey: "approve-development",
	})
	if err != nil || repeated.Status != TodoAutomationStatusProvisioningWorkspace {
		t.Fatalf("repeat development approval = %+v err=%v", repeated, err)
	}
	approvals, err := service.ListTodoAutomationApprovals(ctx, owner, ListTodoAutomationApprovalsQuery{Status: "approved"})
	if err != nil || len(approvals) != 1 || approvals[0].Status != "approved" {
		t.Fatalf("development approvals = %+v err=%v", approvals, err)
	}
	if len(mission.requests) != 1 || len(discussion.notices) != 1 {
		t.Fatalf("repeat approval created Mission requests=%d notices=%d", len(mission.requests), len(discussion.notices))
	}
}

func TestSuggestDevelopmentTodoStartIsIdempotentWhileWorkspaceProvisioning(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	discussion := &todoAutomationDiscussionStub{}
	mission := &recordingTodoMissionExecutionStub{requiresWorkspace: true}
	service := NewService(ServiceOptions{
		Clock: clock.NewFake(todoTestTime), IDGenerator: idgen.NewDeterministic("automation"), Repository: repo,
		AutomationDiscussion: discussion, MissionExecution: mission, DevelopmentExecution: &todoDevelopmentGuardStub{},
		AutomationAgentIDs:     []string{"lead-agent", "coordinator-agent"},
		AllowOwnerSelfApproval: true, AutomationAudit: &todoAutomationAuditStub{},
	})
	owner := testViewer("tenant-a", "owner-a")
	task := mustCreateTask(t, service, ctx, owner, automationTodoRequest(CreateTodoRequest{
		Title: "Implement after explicit start", WorkKind: TodoWorkKindDevelopment, DevelopmentProjectID: "project-1",
		ReviewerID: owner.ViewerID, ReviewerName: "Owner A",
	}))
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	planned, err := service.GetTodo(ctx, owner, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := service.DecideTodoAutomationApproval(ctx, owner, DecideTodoAutomationApprovalRequest{
		ApprovalID: planned.Automation.ApprovalID, ActionID: "approve", PlanDigest: planned.Automation.PlanDigest, IdempotencyKey: "approve-suggest-development",
	})
	if err != nil || approved.Status != TodoAutomationStatusApproved {
		t.Fatalf("approve suggest development Todo = %+v err=%v", approved, err)
	}
	start := StartTodoAutomationExecutionRequest{TodoID: task.TodoID, PlanID: planned.Automation.ProposalID, PlanDigest: planned.Automation.PlanDigest, TodoVersion: planned.Automation.TodoVersion, IdempotencyKey: "start-suggest-development"}
	started, err := service.StartTodoAutomationExecution(ctx, owner, start)
	if err != nil || started.Status != TodoAutomationStatusProvisioningWorkspace {
		t.Fatalf("start suggest development Todo = %+v err=%v", started, err)
	}
	repeated, err := service.StartTodoAutomationExecution(ctx, owner, start)
	if err != nil || repeated.Status != TodoAutomationStatusProvisioningWorkspace {
		t.Fatalf("repeat suggest development start = %+v err=%v", repeated, err)
	}
	if len(mission.requests) != 1 || len(discussion.notices) != 1 {
		t.Fatalf("repeat start created Mission requests=%d notices=%d", len(mission.requests), len(discussion.notices))
	}
}

func TestSuggestTodoPreservesApproverWhenDifferentUserStartsAndRecoveryRuns(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	discussion := &todoAutomationDiscussionStub{}
	audit := &todoAutomationAuditStub{}
	fakeClock := clock.NewFake(todoTestTime)
	initialMission := &recordingTodoMissionExecutionStub{err: errors.New("mission materialization unavailable")}
	service := NewService(ServiceOptions{
		Clock: fakeClock, IDGenerator: idgen.NewDeterministic("automation-principals"), Repository: repo,
		AutomationDiscussion: discussion, AutomationAudit: audit, MissionExecution: initialMission,
		AutomationAgentIDs: []string{"lead-agent", "coordinator-agent"},
	})
	approver := testViewer("tenant-a", "approver-login")
	approver.AuthorizationSubjectID = "subject-approver"
	approver.DisplayName = "Plan Approver"
	starter := testViewer("tenant-a", "starter-login")
	starter.AuthorizationSubjectID = "subject-starter"
	starter.DisplayName = "Execution Starter"
	starter.PermissionScope = []string{"todo.write", "current-execution-scope"}
	task := mustCreateTask(t, service, ctx, approver, automationTodoRequest(CreateTodoRequest{Title: "Start approved work separately"}))
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	planned, err := service.GetTodo(ctx, approver, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := service.DecideTodoAutomationApproval(ctx, approver, DecideTodoAutomationApprovalRequest{
		ApprovalID: planned.Automation.ApprovalID, ActionID: "approve", PlanDigest: planned.Automation.PlanDigest, IdempotencyKey: "approve-separate-starter",
	})
	if err != nil || approved.Status != TodoAutomationStatusApproved {
		t.Fatalf("approve = %+v err=%v", approved, err)
	}
	_, err = service.StartTodoAutomationExecution(ctx, starter, StartTodoAutomationExecutionRequest{
		TodoID: task.TodoID, PlanID: approved.ProposalID, PlanDigest: approved.PlanDigest, TodoVersion: approved.TodoVersion, IdempotencyKey: "start-separate-starter",
	})
	if err == nil {
		t.Fatal("expected initial Mission materialization failure")
	}
	if !audit.hasAction("todo.mission.binding_failed") || !audit.hasAction("todo.execution.failed") {
		t.Fatalf("failed execution audit events = %+v", audit.events)
	}
	persisted, err := repo.GetTodoAutomation(ctx, approver.TenantID, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.DecisionBy != approver.ViewerID || persisted.decisionAuthorizationSubjectID != approver.AuthorizationSubjectID || persisted.ExecutionRequestedBy != starter.ViewerID || persisted.executionRequesterAuthorizationSubjectID != starter.AuthorizationSubjectID {
		t.Fatalf("persisted approval/execution principals = %+v", persisted)
	}

	fakeClock.Advance(todoAutomationFailureRetry + time.Minute)
	recoveredMission := &recordingTodoMissionExecutionStub{}
	resolver := &todoDecisionPrincipalResolverStub{viewer: starter}
	restarted := NewService(ServiceOptions{
		Clock: fakeClock, IDGenerator: idgen.NewDeterministic("automation-principals-recovery"), Repository: repo,
		AutomationDiscussion: discussion, MissionExecution: recoveredMission, DecisionPrincipal: resolver,
		AutomationAgentIDs: []string{"lead-agent", "coordinator-agent"},
	})
	result, err := restarted.RunScheduledAutomationScan(ctx, TodoAutomationScanRequest{ScheduledRunID: "recover-separate-starter"})
	if err != nil || result.RecoveredCount != 1 || len(recoveredMission.requests) != 1 {
		t.Fatalf("recovery result=%+v requests=%+v err=%v", result, recoveredMission.requests, err)
	}
	request := recoveredMission.requests[0]
	if request.ApprovedBy != approver.ViewerID || request.ApproverAuthorizationSubjectID != approver.AuthorizationSubjectID || request.ApproverDisplayName != approver.DisplayName {
		t.Fatalf("Mission approval attribution = %+v", request)
	}
	binding, err := repo.GetTodoMissionBinding(ctx, approver.TenantID, task.TodoID)
	if err != nil || binding.CreatedBy != starter.ViewerID {
		t.Fatalf("execution binding = %+v err=%v", binding, err)
	}
	if len(resolver.persisted) != 1 || resolver.persisted[0].ViewerID != starter.ViewerID || len(resolver.persisted[0].PermissionScope) != 0 {
		t.Fatalf("recovery resolver principals = %+v", resolver.persisted)
	}
}

func TestDevelopmentTodoSelfApprovalRequiresAuditedLocalOverride(t *testing.T) {
	for _, tc := range []struct {
		name      string
		allow     bool
		wantError error
	}{
		{name: "default denied", wantError: ErrForbidden},
		{name: "explicit local override", allow: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			ctx := context.Background()
			audit := &todoAutomationAuditStub{}
			service := NewService(ServiceOptions{
				Clock: clock.NewFake(todoTestTime), IDGenerator: idgen.NewDeterministic("automation-self-approval"), Repository: NewRepository(env.DBRouter),
				AutomationDiscussion: &todoAutomationDiscussionStub{}, AutomationAudit: audit,
				MissionExecution: &recordingTodoMissionExecutionStub{requiresWorkspace: true}, DevelopmentExecution: &todoDevelopmentGuardStub{},
				AutomationAgentIDs: []string{"lead-agent", "coordinator-agent"}, AllowOwnerSelfApproval: tc.allow,
			})
			owner := testViewer("tenant-a", "owner-a")
			request := automationTodoRequest(CreateTodoRequest{
				Title: "Review own development change", WorkKind: TodoWorkKindDevelopment, DevelopmentProjectID: "project-1", ReviewerID: owner.ViewerID,
			})
			request.AutomationMode = TodoAutomationModeSuggest
			task := mustCreateTask(t, service, ctx, owner, request)
			if err := service.RunAutomationScan(ctx); err != nil {
				t.Fatal(err)
			}
			planned, err := service.GetTodo(ctx, owner, task.TodoID)
			if err != nil {
				t.Fatal(err)
			}
			state, err := service.DecideTodoAutomationApproval(ctx, owner, DecideTodoAutomationApprovalRequest{
				ApprovalID: planned.Automation.ApprovalID, ActionID: "approve", PlanDigest: planned.Automation.PlanDigest, IdempotencyKey: "self-approve",
			})
			if tc.wantError != nil {
				if !errors.Is(err, tc.wantError) {
					t.Fatalf("approval error = %v, want %v", err, tc.wantError)
				}
				if !audit.hasAction("todo.automation.self_approval_denied") {
					t.Fatalf("denial audit events = %+v", audit.events)
				}
				return
			}
			if err != nil || state.Status != TodoAutomationStatusApproved {
				t.Fatalf("override approval = %+v err=%v", state, err)
			}
			if !audit.hasAction("todo.automation.owner_self_approval_override") {
				t.Fatalf("override audit events = %+v", audit.events)
			}
			for _, event := range audit.events {
				if event.Action == "todo.automation.owner_self_approval_override" && event.Metadata["allow_owner_self_approval"] != "true" {
					t.Fatalf("override audit metadata = %+v", event.Metadata)
				}
			}
		})
	}
}

func TestDevelopmentTodoPlanBindsExactResourceSnapshotAndRejectsStaleApproval(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	discussion := &todoAutomationDiscussionStub{}
	guard := &todoDevelopmentGuardStub{}
	audit := &todoAutomationAuditStub{}
	service := NewService(ServiceOptions{
		Clock: clock.NewFake(todoTestTime), IDGenerator: idgen.NewDeterministic("automation"), Repository: repo,
		AutomationDiscussion: discussion, MissionExecution: &recordingTodoMissionExecutionStub{requiresWorkspace: true}, DevelopmentExecution: guard,
		AutomationAgentIDs:     []string{"lead-agent", "coordinator-agent"},
		AllowOwnerSelfApproval: true, AutomationAudit: audit,
	})
	owner := testViewer("tenant-a", "owner-a")
	request := automationTodoRequest(CreateTodoRequest{
		Title: "Implement exact approved scope", WorkKind: TodoWorkKindDevelopment, DevelopmentProjectID: "project-1",
		ReviewerID: owner.ViewerID, ReviewerName: "Owner A", AcceptanceCriteria: "All focused tests pass",
	})
	request.AutomationMode = TodoAutomationModeManaged
	task := mustCreateTask(t, service, ctx, owner, request)
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	planned, err := service.GetTodo(ctx, owner, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	plan := planned.Automation.Plan
	if plan.Version != todoExecutionPlanVersion || plan.PlanID != planned.Automation.ProposalID || plan.TodoID != task.TodoID || plan.TodoVersion != task.Version || plan.ProposalRevision != 1 || plan.ConversationID != "discussion-plan" {
		t.Fatalf("plan identity = %+v", plan)
	}
	if plan.DevelopmentProjectID != "project-1" || plan.ResourceSnapshotDigest == "" || len(plan.RepositoryScopes) != 1 || len(plan.BaseCommits) != 1 || plan.BaseCommits[0].CommitSHA == "" {
		t.Fatalf("plan repository snapshot = %+v", plan)
	}
	if len(plan.AllowedFileScopes) != 1 || plan.AllowedFileScopes[0] != "orrery_v2" || len(plan.PlannedCommands) != 1 || plan.PlannedCommands[0] != "go test ./..." || plan.NetworkPolicy.Mode != "none" {
		t.Fatalf("plan execution boundary = %+v", plan)
	}
	originalDigest := planned.Automation.PlanDigest
	guard.planContext = TodoDevelopmentExecutionPlanContext{
		DevelopmentProjectID: "project-1", ResourceSnapshotDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Repositories: []TodoDevelopmentPlanRepository{{
			BindingID: "binding-primary", DisplayName: "Primary", Role: "primary", AccessMode: "read_write", DefaultBranch: "main",
			SourceSubdirectory: "orrery_v2", TestCommands: []string{"go test ./..."}, BaseCommit: "fedcba9876543210fedcba9876543210fedcba98",
		}},
		AllowedFileScopes: []string{"orrery_v2"}, AllowedNetworkMode: "none",
	}
	_, err = service.DecideTodoAutomationApproval(ctx, owner, DecideTodoAutomationApprovalRequest{
		ApprovalID: planned.Automation.ApprovalID, ActionID: "approve", PlanDigest: originalDigest, IdempotencyKey: "approve-stale-resource",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale resource approval error = %v, want ErrConflict", err)
	}
	state, err := repo.GetTodoAutomation(ctx, owner.TenantID, task.TodoID)
	if err != nil || state.Status != TodoAutomationStatusStale || state.ApprovedPlanDigest != "" {
		t.Fatalf("stale automation state = %+v err=%v", state, err)
	}
	if !audit.hasAction("todo.execution_plan.stale") {
		t.Fatalf("stale execution plan audit events = %+v", audit.events)
	}
}

func TestDevelopmentTodoApprovalRequiresProjectApproverAuthorization(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	guard := &todoDevelopmentGuardStub{approvalErr: ErrForbidden}
	service := NewService(ServiceOptions{
		Clock: clock.NewFake(todoTestTime), IDGenerator: idgen.NewDeterministic("automation"), Repository: NewRepository(env.DBRouter),
		AutomationDiscussion: &todoAutomationDiscussionStub{}, MissionExecution: &recordingTodoMissionExecutionStub{requiresWorkspace: true}, DevelopmentExecution: guard,
		AutomationAgentIDs:     []string{"lead-agent", "coordinator-agent"},
		AllowOwnerSelfApproval: true, AutomationAudit: &todoAutomationAuditStub{},
	})
	owner := testViewer("tenant-a", "owner-a")
	request := automationTodoRequest(CreateTodoRequest{
		Title: "Protected development approval", WorkKind: TodoWorkKindDevelopment, DevelopmentProjectID: "project-1", ReviewerID: owner.ViewerID,
	})
	request.AutomationMode = TodoAutomationModeManaged
	task := mustCreateTask(t, service, ctx, owner, request)
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	planned, err := service.GetTodo(ctx, owner, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	approvals, err := service.ListTodoAutomationApprovals(ctx, owner, ListTodoAutomationApprovalsQuery{})
	if err != nil || len(approvals) != 0 {
		t.Fatalf("unauthorized approval list = %+v err=%v", approvals, err)
	}
	_, err = service.DecideTodoAutomationApproval(ctx, owner, DecideTodoAutomationApprovalRequest{
		ApprovalID: planned.Automation.ApprovalID, ActionID: "approve", PlanDigest: planned.Automation.PlanDigest, IdempotencyKey: "approve-forbidden",
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("approval error = %v, want ErrForbidden", err)
	}
}

func TestTodoAutomationRejectionRequiresReasonAndDoesNotExecute(t *testing.T) {
	service, _, discussion, closeEnv := newTodoAutomationServiceForTest(t)
	defer closeEnv()
	ctx := context.Background()
	owner := testViewer("tenant-a", "owner-a")
	task := mustCreateTask(t, service, ctx, owner, automationTodoRequest(CreateTodoRequest{Title: "Rejectable work"}))
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	planned, _ := service.GetTodo(ctx, owner, task.TodoID)
	request := DecideTodoAutomationApprovalRequest{ApprovalID: planned.Automation.ApprovalID, ActionID: "reject", PlanDigest: planned.Automation.PlanDigest, IdempotencyKey: "reject-1"}
	if _, err := service.DecideTodoAutomationApproval(ctx, owner, request); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("reject without reason = %v", err)
	}
	request.Message = "Missing customer confirmation"
	state, err := service.DecideTodoAutomationApproval(ctx, owner, request)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != TodoAutomationStatusRejected || len(discussion.commands) != 1 {
		t.Fatalf("state = %+v commands=%d", state, len(discussion.commands))
	}
}

func newTodoAutomationServiceForTest(t *testing.T) (*Service, *Repository, *todoAutomationDiscussionStub, func()) {
	t.Helper()
	env := testkit.New(t)
	repo := NewRepository(env.DBRouter)
	discussion := &todoAutomationDiscussionStub{}
	service := NewService(ServiceOptions{
		Clock:                clock.NewFake(todoTestTime),
		IDGenerator:          idgen.NewDeterministic("automation"),
		IdeaIDGenerator:      idgen.NewDeterministic("idea"),
		Repository:           repo,
		AutomationDiscussion: discussion,
		MissionExecution:     todoMissionExecutionStub{},
		AutomationAgentIDs:   []string{"lead-agent", "coordinator-agent"},
		ExecutionSupportIDs:  []string{"qa-plan-agent", "qa-check-agent"},
	})
	return service, repo, discussion, env.Close
}

type todoAutomationDiscussionStub struct {
	commands       []discussionsession.SendTodoAutomationMessageCommand
	notices        []discussionsession.AppendNoticeCommand
	noticeViewers  []discussionsession.ViewerRef
	viewers        []discussionsession.ViewerRef
	ensured        []discussionsession.EnsureTodoConversationCommand
	ensureViewers  []discussionsession.ViewerRef
	memberCommands []discussionsession.ReconcileConversationMembersCommand
	memberViewers  []discussionsession.ViewerRef
	sendHook       func() error
}

type todoHILDecisionStub struct {
	decisionID string
	inputs     []hil.CreateDecisionInput
	err        error
}

func (s *todoHILDecisionStub) CreateDecision(_ context.Context, input hil.CreateDecisionInput) (hil.CreateDecisionResult, error) {
	s.inputs = append(s.inputs, input)
	if s.err != nil {
		return hil.CreateDecisionResult{}, s.err
	}
	return hil.CreateDecisionResult{Decision: hil.DecisionRequest{ID: s.decisionID, TenantID: input.TenantID, Route: hil.RouteDecision{Mode: hil.ModeApproval}, Status: hil.DecisionPending}}, nil
}

type todoHILResolutionStub struct {
	todoHILDecisionStub
	resolution      hil.DecisionResolution
	resolutionErr   error
	resolutionCalls int
	lastTenantID    string
	lastDecisionID  string
}

func (s *todoHILResolutionStub) GetDecisionResolution(_ context.Context, tenantID, decisionID string) (hil.DecisionResolution, error) {
	s.resolutionCalls++
	s.lastTenantID = tenantID
	s.lastDecisionID = decisionID
	return s.resolution, s.resolutionErr
}

func TestTodoAutomationApprovalAndExecutionRequireHumanUserViewer(t *testing.T) {
	service, _, _, closeEnv := newTodoAutomationServiceForTest(t)
	defer closeEnv()
	ctx := context.Background()
	owner := testViewer("tenant-a", "owner-a")
	task := mustCreateTask(t, service, ctx, owner, automationTodoRequest(CreateTodoRequest{Title: "Human approval only"}))
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	planned, err := service.GetTodo(ctx, owner, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []ViewerKind{ViewerKindAgent, ViewerKindSystem} {
		viewer := owner
		viewer.ViewerKind = kind
		if approvals, err := service.ListTodoAutomationApprovals(ctx, viewer, ListTodoAutomationApprovalsQuery{}); err != nil || len(approvals) != 0 {
			t.Fatalf("%s approvals=%+v err=%v", kind, approvals, err)
		}
		if _, err := service.DecideTodoAutomationApproval(ctx, viewer, DecideTodoAutomationApprovalRequest{
			ApprovalID: planned.Automation.ApprovalID, ActionID: "approve", PlanDigest: planned.Automation.PlanDigest, IdempotencyKey: "approve-" + string(kind),
		}); !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s approval error = %v, want ErrForbidden", kind, err)
		}
	}
	if _, err := service.DecideTodoAutomationApproval(ctx, owner, DecideTodoAutomationApprovalRequest{
		ApprovalID: planned.Automation.ApprovalID, ActionID: "approve", PlanDigest: planned.Automation.PlanDigest, IdempotencyKey: "approve-user",
	}); err != nil {
		t.Fatal(err)
	}
	agent := owner
	agent.ViewerKind = ViewerKindAgent
	if _, err := service.StartTodoAutomationExecution(ctx, agent, StartTodoAutomationExecutionRequest{
		TodoID: task.TodoID, PlanID: planned.Automation.ProposalID, PlanDigest: planned.Automation.PlanDigest, TodoVersion: planned.Automation.TodoVersion, IdempotencyKey: "start-agent",
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Agent start error = %v, want ErrForbidden", err)
	}
}

func TestTodoAutomationRecoveryReloadsCurrentCanonicalApprovalPrincipal(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	discussion := &todoAutomationDiscussionStub{}
	fakeClock := clock.NewFake(todoTestTime)
	failing := &recordingTodoMissionExecutionStub{err: errors.New("mission materialization unavailable")}
	service := NewService(ServiceOptions{
		Clock: fakeClock, IDGenerator: idgen.NewDeterministic("automation"), Repository: repo,
		AutomationDiscussion: discussion, MissionExecution: failing,
		AutomationAgentIDs: []string{"lead-agent", "coordinator-agent"},
	})
	owner := testViewer("tenant-a", "owner-login")
	owner.AuthorizationSubjectID = "subject-owner"
	owner.PermissionScope = []string{"todo.read", "todo.write", "development.execute"}
	request := automationTodoRequest(CreateTodoRequest{Title: "Recover exact principal"})
	request.AutomationMode = TodoAutomationModeManaged
	task := mustCreateTask(t, service, ctx, owner, request)
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	planned, err := service.GetTodo(ctx, owner, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.DecideTodoAutomationApproval(ctx, owner, DecideTodoAutomationApprovalRequest{
		ApprovalID: planned.Automation.ApprovalID, ActionID: "approve", PlanDigest: planned.Automation.PlanDigest, IdempotencyKey: "approve-recovery",
	}); err == nil {
		t.Fatal("expected initial Mission materialization failure")
	}
	persisted, err := repo.GetTodoAutomation(ctx, owner.TenantID, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.decisionViewerKind != ViewerKindUser || persisted.DecisionBy != "owner-login" || persisted.decisionAuthorizationSubjectID != "subject-owner" || !containsFold(persisted.decisionPermissionScope, "development.execute") {
		t.Fatalf("persisted decision principal = %+v kind=%q subject=%q scopes=%v", persisted, persisted.decisionViewerKind, persisted.decisionAuthorizationSubjectID, persisted.decisionPermissionScope)
	}

	fakeClock.Advance(todoAutomationFailureRetry + time.Minute)
	recoveredMission := &recordingTodoMissionExecutionStub{}
	currentOwner := owner
	currentOwner.PermissionScope = []string{"todo.read", "todo.write", "development.execute", "current-only"}
	principalResolver := &todoDecisionPrincipalResolverStub{viewer: currentOwner}
	restarted := NewService(ServiceOptions{
		Clock: fakeClock, IDGenerator: idgen.NewDeterministic("automation-restarted"), Repository: repo,
		AutomationDiscussion: discussion, MissionExecution: recoveredMission,
		DecisionPrincipal:  principalResolver,
		AutomationAgentIDs: []string{"lead-agent", "coordinator-agent"},
	})
	restarted.SetAuthorization(authorization.NewService(authorization.NewScopeAdapter(map[string]string{"todo.edit": "todo.write"})), nil)
	result, err := restarted.RunScheduledAutomationScan(ctx, TodoAutomationScanRequest{ScheduledRunID: "run-recovery"})
	if err != nil {
		t.Fatalf("RunScheduledAutomationScan: %v", err)
	}
	if result.RecoveredCount != 1 || len(recoveredMission.requests) != 1 {
		t.Fatalf("recovery result=%+v requests=%+v", result, recoveredMission.requests)
	}
	recovered := recoveredMission.requests[0]
	if recovered.ApprovedBy != "owner-login" || recovered.ApproverAuthorizationSubjectID != "subject-owner" {
		t.Fatalf("recovered approval principal = %+v", recovered)
	}
	if len(discussion.noticeViewers) != 1 || discussion.noticeViewers[0].ViewerID != "subject-owner" {
		t.Fatalf("recovered execution notice viewers = %+v", discussion.noticeViewers)
	}
	if len(principalResolver.persisted) != 1 || len(principalResolver.persisted[0].PermissionScope) != 0 {
		t.Fatalf("resolver received historical permissions: %+v", principalResolver.persisted)
	}
}

func TestTodoAutomationRecoversCrashBeforeMissionBindingCreationAfterLeaseExpiry(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	discussion := &todoAutomationDiscussionStub{}
	fakeClock := clock.NewFake(todoTestTime)
	owner := testViewer("tenant-a", "owner-login")
	owner.AuthorizationSubjectID = "subject-owner"
	service := NewService(ServiceOptions{
		Clock: fakeClock, IDGenerator: idgen.NewDeterministic("automation-crash"), Repository: repo,
		AutomationDiscussion: discussion, MissionExecution: &recordingTodoMissionExecutionStub{},
		AutomationAgentIDs: []string{"lead-agent", "coordinator-agent"},
	})
	request := automationTodoRequest(CreateTodoRequest{Title: "Recover crash before Mission binding"})
	request.AutomationMode = TodoAutomationModeManaged
	task := mustCreateTask(t, service, ctx, owner, request)
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	planned, err := repo.GetTodoAutomation(ctx, owner.TenantID, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	recordTodoAutomationDecisionViewer(&planned, owner)
	planned.ApprovedPlanDigest = planned.PlanDigest
	planned.Status = TodoAutomationStatusExecuting
	planned.DecidedAt = fakeClock.Now()
	planned.UpdatedAt = fakeClock.Now()
	if err := repo.SaveTodoAutomation(ctx, planned); err != nil {
		t.Fatal(err)
	}

	fakeClock.Advance(todoMissionExecutionLease)
	mission := &recordingTodoMissionExecutionStub{}
	restarted := NewService(ServiceOptions{
		Clock: fakeClock, IDGenerator: idgen.NewDeterministic("automation-crash-restarted"), Repository: repo,
		AutomationDiscussion: discussion, MissionExecution: mission,
		DecisionPrincipal:  &todoDecisionPrincipalResolverStub{viewer: owner},
		AutomationAgentIDs: []string{"lead-agent", "coordinator-agent"},
	})
	result, err := restarted.RunScheduledAutomationScan(ctx, TodoAutomationScanRequest{ScheduledRunID: "run-crash-recovery"})
	if err != nil {
		t.Fatalf("RunScheduledAutomationScan: %v", err)
	}
	if result.RecoveredCount != 1 || len(mission.requests) != 1 {
		t.Fatalf("crash recovery result=%+v requests=%+v", result, mission.requests)
	}
	binding, err := repo.GetTodoMissionBinding(ctx, owner.TenantID, task.TodoID)
	if err != nil || binding.MissionID != "mission-recovered" || binding.Status != TodoMissionBindingStatusExecuting {
		t.Fatalf("recovered binding=%+v err=%v", binding, err)
	}
}

func TestTodoAutomationPersistsPartialMissionReceiptAndRecoversSameMission(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	discussion := &todoAutomationDiscussionStub{}
	fakeClock := clock.NewFake(todoTestTime)
	owner := testViewer("tenant-a", "owner-login")
	owner.AuthorizationSubjectID = "subject-owner"
	missionFailure := errors.New("Mission post-create publication failed")
	mission := &recordingTodoMissionExecutionStub{
		missionID: "mission-partial", err: missionFailure,
		partialReceipt: TodoMissionExecutionReceipt{
			MissionID: "mission-partial", ConversationID: "discussion-plan",
			Tasks: []TodoMissionTaskExecution{{MissionID: "mission-partial", TaskID: "task-step-1", AgentID: "lead-agent"}},
		},
	}
	service := NewService(ServiceOptions{
		Clock: fakeClock, IDGenerator: idgen.NewDeterministic("automation-partial"), Repository: repo,
		AutomationDiscussion: discussion, MissionExecution: mission,
		DecisionPrincipal:  &todoDecisionPrincipalResolverStub{viewer: owner},
		AutomationAgentIDs: []string{"lead-agent", "coordinator-agent"},
	})
	request := automationTodoRequest(CreateTodoRequest{Title: "Recover partially created Mission"})
	request.AutomationMode = TodoAutomationModeManaged
	task := mustCreateTask(t, service, ctx, owner, request)
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	planned, err := service.GetTodo(ctx, owner, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.DecideTodoAutomationApproval(ctx, owner, DecideTodoAutomationApprovalRequest{
		ApprovalID: planned.Automation.ApprovalID, ActionID: "approve", PlanDigest: planned.Automation.PlanDigest, IdempotencyKey: "approve-partial",
	}); !errors.Is(err, missionFailure) {
		t.Fatalf("initial execution error = %v, want %v", err, missionFailure)
	}
	binding, err := repo.GetTodoMissionBinding(ctx, owner.TenantID, task.TodoID)
	if err != nil || binding.Status != TodoMissionBindingStatusFailed || binding.MissionID != "mission-partial" || len(binding.MissionTaskIDs) != 1 || binding.MissionTaskIDs[0] != "task-step-1" {
		t.Fatalf("failed partial Mission binding = %+v err=%v", binding, err)
	}

	mission.err = nil
	mission.partialReceipt = TodoMissionExecutionReceipt{}
	fakeClock.Advance(todoAutomationFailureRetry + time.Minute)
	result, err := service.RunScheduledAutomationScan(ctx, TodoAutomationScanRequest{ScheduledRunID: "run-partial-recovery"})
	if err != nil {
		t.Fatalf("RunScheduledAutomationScan: %v", err)
	}
	if result.RecoveredCount != 1 || len(mission.requests) != 2 {
		t.Fatalf("partial recovery result=%+v requests=%+v", result, mission.requests)
	}
	binding, err = repo.GetTodoMissionBinding(ctx, owner.TenantID, task.TodoID)
	if err != nil || binding.Status != TodoMissionBindingStatusExecuting || binding.MissionID != "mission-partial" {
		t.Fatalf("recovered partial Mission binding = %+v err=%v", binding, err)
	}
}

func TestTodoAutomationRecoveryFailsClosedAfterApprovalPermissionRevocation(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	discussion := &todoAutomationDiscussionStub{}
	fakeClock := clock.NewFake(todoTestTime)
	failing := &recordingTodoMissionExecutionStub{err: errors.New("mission materialization unavailable")}
	service := NewService(ServiceOptions{
		Clock: fakeClock, IDGenerator: idgen.NewDeterministic("automation"), Repository: repo,
		AutomationDiscussion: discussion, MissionExecution: failing,
		AutomationAgentIDs: []string{"lead-agent", "coordinator-agent"},
	})
	owner := testViewer("tenant-a", "owner-login")
	owner.AuthorizationSubjectID = "subject-owner"
	owner.PermissionScope = []string{"todo.read", "todo.write"}
	request := automationTodoRequest(CreateTodoRequest{Title: "Fail closed after permission revocation"})
	request.AutomationMode = TodoAutomationModeManaged
	task := mustCreateTask(t, service, ctx, owner, request)
	if err := service.RunAutomationScan(ctx); err != nil {
		t.Fatal(err)
	}
	planned, err := service.GetTodo(ctx, owner, task.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.DecideTodoAutomationApproval(ctx, owner, DecideTodoAutomationApprovalRequest{
		ApprovalID: planned.Automation.ApprovalID, ActionID: "approve", PlanDigest: planned.Automation.PlanDigest, IdempotencyKey: "approve-revocation",
	}); err == nil {
		t.Fatal("expected initial Mission materialization failure")
	}

	fakeClock.Advance(todoAutomationFailureRetry + time.Minute)
	currentOwner := owner
	currentOwner.PermissionScope = []string{"todo.read"}
	principalResolver := &todoDecisionPrincipalResolverStub{viewer: currentOwner}
	recoveredMission := &recordingTodoMissionExecutionStub{}
	restarted := NewService(ServiceOptions{
		Clock: fakeClock, IDGenerator: idgen.NewDeterministic("automation-revoked"), Repository: repo,
		AutomationDiscussion: discussion, MissionExecution: recoveredMission,
		DecisionPrincipal:  principalResolver,
		AutomationAgentIDs: []string{"lead-agent", "coordinator-agent"},
	})
	restarted.SetAuthorization(authorization.NewService(authorization.NewScopeAdapter(map[string]string{"todo.edit": "todo.write"})), nil)
	result, err := restarted.RunScheduledAutomationScan(ctx, TodoAutomationScanRequest{ScheduledRunID: "run-revoked"})
	if err == nil {
		t.Fatal("expected recovery to fail after todo.write was revoked")
	}
	if result.RecoveredCount != 0 || result.FailedCount != 1 || len(recoveredMission.requests) != 0 {
		t.Fatalf("recovery result=%+v requests=%+v", result, recoveredMission.requests)
	}
	persisted, getErr := repo.GetTodoAutomation(ctx, owner.TenantID, task.TodoID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if persisted.Status != TodoAutomationStatusExecutionFailed {
		t.Fatalf("automation status = %q, want execution_failed", persisted.Status)
	}
}

type todoDecisionPrincipalResolverStub struct {
	viewer    ViewerRef
	err       error
	persisted []ViewerRef
}

type todoAutomationAuditStub struct {
	events []operations.AuditEventDTO
	err    error
}

func (s *todoAutomationAuditStub) RecordAuditEvent(_ context.Context, event operations.AuditEventDTO) error {
	if s.err != nil {
		return s.err
	}
	s.events = append(s.events, event)
	return nil
}

func (s *todoAutomationAuditStub) hasAction(action string) bool {
	for _, event := range s.events {
		if event.Action == action {
			return true
		}
	}
	return false
}

func (r *todoDecisionPrincipalResolverStub) ResolveTodoDecisionPrincipal(_ context.Context, persisted ViewerRef) (ViewerRef, error) {
	r.persisted = append(r.persisted, persisted)
	return r.viewer, r.err
}

func (s *todoAutomationDiscussionStub) AppendNotice(_ context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.AppendNoticeCommand) (discussionsession.ConversationDTO, error) {
	s.notices = append(s.notices, cmd)
	s.noticeViewers = append(s.noticeViewers, viewer)
	return discussionsession.ConversationDTO{ConversationID: cmd.ConversationID, Status: "completed"}, nil
}

func (s *todoAutomationDiscussionStub) SendTodoAutomationMessage(_ context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.SendTodoAutomationMessageCommand) (discussionsession.ConversationDTO, error) {
	s.commands = append(s.commands, cmd)
	s.viewers = append(s.viewers, viewer)
	if s.sendHook != nil {
		if err := s.sendHook(); err != nil {
			return discussionsession.ConversationDTO{}, err
		}
	}
	if !strings.Contains(cmd.Content, "Todo 自动评估群聊") {
		return discussionsession.ConversationDTO{ConversationID: cmd.ConversationID, Status: "completed"}, nil
	}
	return discussionsession.ConversationDTO{
		ConversationID: "discussion-plan",
		Status:         "completed",
		Messages: []discussionsession.MessageDTO{
			{Role: "system", AuthorID: "orrery-todo-automation", AuthorName: "Orrery", Content: cmd.Content},
			{Role: "assistant", AuthorID: "lead-agent", AuthorName: "Lead Agent", Content: "TODO_AUTOMATION_PROPOSAL\n\n```json\n{\"version\":3,\"summary\":\"Verify inputs and produce the deliverable\",\"steps\":[{\"step_id\":\"step-1\",\"title\":\"Produce deliverable\",\"description\":\"Verify inputs and complete the Todo\",\"assignee_agent_id\":\"lead-agent\",\"acceptance_criteria\":[\"The requested deliverable is complete\"],\"evidence_requirements\":[{\"evidence_requirement_id\":\"evidence-step-1-1\",\"acceptance_criterion\":\"The requested deliverable is complete\",\"kind\":\"artifact\",\"required\":true}],\"depends_on\":[]}],\"risk_summary\":[\"Inputs may be incomplete\"],\"required_approvals\":[\"Todo owner approval\"]}\n```"},
			{Role: "assistant", AuthorID: "coordinator-agent", AuthorName: "Coordinator Agent", Content: "Risks and acceptance checks reviewed."},
		},
	}, nil
}

func (s *todoAutomationDiscussionStub) AddConversationMembers(_ context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.AddConversationMembersCommand) (discussionsession.ConversationDTO, error) {
	s.memberViewers = append(s.memberViewers, viewer)
	return discussionsession.ConversationDTO{ConversationID: cmd.ConversationID}, nil
}

func (s *todoAutomationDiscussionStub) EnsureTodoConversation(_ context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.EnsureTodoConversationCommand) (discussionsession.ConversationDTO, error) {
	s.ensured = append(s.ensured, cmd)
	s.ensureViewers = append(s.ensureViewers, viewer)
	return discussionsession.ConversationDTO{ConversationID: "discussion-plan", PurposeKind: "todo", PurposeID: cmd.TodoID}, nil
}

func (s *todoAutomationDiscussionStub) ReconcileConversationMembers(_ context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.ReconcileConversationMembersCommand) (discussionsession.ConversationDTO, error) {
	s.memberCommands = append(s.memberCommands, cmd)
	s.memberViewers = append(s.memberViewers, viewer)
	return discussionsession.ConversationDTO{ConversationID: cmd.ConversationID}, nil
}

type todoMissionExecutionStub struct{}

func (todoMissionExecutionStub) CreateMissionFromTodo(_ context.Context, req TodoMissionExecutionRequest) (TodoMissionExecutionReceipt, error) {
	tasks := make([]TodoMissionTaskExecution, 0, len(req.Plan.Steps))
	for _, step := range req.Plan.Steps {
		tasks = append(tasks, TodoMissionTaskExecution{MissionID: "mission-todo", TaskID: "task-" + step.StepID, AgentID: step.AssigneeAgentID, Goal: step.Title})
	}
	return TodoMissionExecutionReceipt{MissionID: "mission-todo", ConversationID: req.ConversationID, Tasks: tasks}, nil
}

type attemptAwareTodoMissionExecutionStub struct{}

func (attemptAwareTodoMissionExecutionStub) CreateMissionFromTodo(_ context.Context, req TodoMissionExecutionRequest) (TodoMissionExecutionReceipt, error) {
	missionID := fmt.Sprintf("mission-attempt-%d", req.ExecutionAttempt)
	tasks := make([]TodoMissionTaskExecution, 0, len(req.Plan.Steps))
	for _, step := range req.Plan.Steps {
		tasks = append(tasks, TodoMissionTaskExecution{MissionID: missionID, TaskID: fmt.Sprintf("task-attempt-%d-%s", req.ExecutionAttempt, step.StepID), AgentID: step.AssigneeAgentID, Goal: step.Title})
	}
	return TodoMissionExecutionReceipt{MissionID: missionID, ConversationID: req.ConversationID, Tasks: tasks}, nil
}

type recordingTodoMissionExecutionStub struct {
	requests          []TodoMissionExecutionRequest
	err               error
	missionID         string
	partialReceipt    TodoMissionExecutionReceipt
	requiresWorkspace bool
	beforeReturn      func() error
}

func (s *recordingTodoMissionExecutionStub) CreateMissionFromTodo(_ context.Context, req TodoMissionExecutionRequest) (TodoMissionExecutionReceipt, error) {
	s.requests = append(s.requests, req)
	if s.beforeReturn != nil {
		if err := s.beforeReturn(); err != nil {
			return TodoMissionExecutionReceipt{}, err
		}
	}
	if s.err != nil {
		return s.partialReceipt, s.err
	}
	missionID := firstNonEmptyText(s.missionID, "mission-recovered")
	tasks := make([]TodoMissionTaskExecution, 0, len(req.Plan.Steps))
	for _, step := range req.Plan.Steps {
		tasks = append(tasks, TodoMissionTaskExecution{MissionID: missionID, TaskID: "task-" + step.StepID, AgentID: step.AssigneeAgentID, Goal: step.Title})
	}
	return TodoMissionExecutionReceipt{MissionID: missionID, ConversationID: req.ConversationID, Tasks: tasks, RequiresWorkspace: s.requiresWorkspace}, nil
}

func automationTodoRequest(req CreateTodoRequest) CreateTodoRequest {
	req.AutomationMode = TodoAutomationModeSuggest
	return req
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}
