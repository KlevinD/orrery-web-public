package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/identity"
	contextview "orrery_v2/internal/app/ontologyview/context"
	"orrery_v2/internal/app/ontologyview/contract"
	"orrery_v2/internal/app/reliability"
	"orrery_v2/internal/platform/idgen"
	httptransport "orrery_v2/internal/transport/http"
)

type recordingOntologyActionAuthorizer struct {
	action string
	object identity.ObjectRef
	err    error
}

type recordingOntologyRepairExecutor struct {
	validateInput repairInvocation
	validateCalls int
	executeInput  actioncontract.ActionRun
	executeCalls  int
	validateErr   error
	executeErr    error
}

func (e *recordingOntologyRepairExecutor) Validate(_ context.Context, input repairInvocation) error {
	e.validateCalls++
	e.validateInput = input
	return e.validateErr
}

func (e *recordingOntologyRepairExecutor) Execute(_ context.Context, run actioncontract.ActionRun) (actioncontract.ActionRun, error) {
	e.executeCalls++
	e.executeInput = run
	return run, e.executeErr
}

func (a *recordingOntologyActionAuthorizer) Can(_ context.Context, _ identity.Principal, action string, object identity.ObjectRef) error {
	a.action = action
	a.object = object
	return a.err
}

func TestProductionOntologyActionAPIDerivesIdentityAuthorizationAndPolicy(t *testing.T) {
	ctx := context.Background()
	repository := actioncontract.NewMemoryRepository()
	service := actioncontract.NewService(repository, actioncontract.WithClock(func() time.Time {
		return time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	}))
	if err := actioncontract.InstallDefaultActionTypes(ctx, service); err != nil {
		t.Fatalf("install default action types: %v", err)
	}
	authorizer := &recordingOntologyActionAuthorizer{}
	executions := newProductionOntologyExecutionAPI(&fakeOntologyExecutionContext{detail: contextview.ObjectDetail{ObjectItem: contextview.ObjectItem{ObjectType: "task"}}})
	api := newProductionOntologyActionAPI(repository, service, authorizer, idgen.NewDeterministic("ontology-action"), executions, nil)
	principal := identity.Principal{
		SubjectProvider: "kratos", Subject: "identity-1", UserID: "human-1", DisplayName: "Human One", ViewerKind: identity.ViewerKindUser,
	}

	result, err := api.InvokeAction(ctx, principal, "task.complete", httptransport.OntologyActionInvokeRequest{
		TenantID: "tenant-1", RuntimeKind: "web_app", RuntimeID: "orrery-web", ArgsHash: "sha256:args",
		ObjectRefs: []httptransport.OntologyActionRefDTO{{Kind: "task", ID: "task-1"}},
	})
	if err != nil {
		t.Fatalf("invoke action: %v", err)
	}
	if result.ID != "action-run:ontology-action-000001" || result.PolicyDecisionID != "policy-decision:ontology-action-000001" {
		t.Fatalf("result identities = %+v", result)
	}
	if authorizer.action != "task.write" || authorizer.object != (identity.ObjectRef{Type: "task", ID: "task-1", TenantID: "tenant-1"}) {
		t.Fatalf("authorization = action %q object %+v", authorizer.action, authorizer.object)
	}
	run, err := repository.GetActionRun(ctx, actioncontract.ActionRunID(result.ID))
	if err != nil {
		t.Fatalf("get saved action run: %v", err)
	}
	if run.Actor.ID != "human-1" || run.EffectiveUser.ID != "human-1" || run.Actor.Kind != actioncontract.SubjectHuman {
		t.Fatalf("saved actor = %+v effective user = %+v", run.Actor, run.EffectiveUser)
	}
}

func TestProductionOntologyActionAPIRejectsUnverifiedHighRiskAction(t *testing.T) {
	ctx := context.Background()
	repository := actioncontract.NewMemoryRepository()
	service := actioncontract.NewService(repository)
	if err := actioncontract.InstallDefaultActionTypes(ctx, service); err != nil {
		t.Fatalf("install default action types: %v", err)
	}
	executions := newProductionOntologyExecutionAPI(&fakeOntologyExecutionContext{detail: contextview.ObjectDetail{ObjectItem: contextview.ObjectItem{ObjectType: "mission"}}})
	api := newProductionOntologyActionAPI(repository, service, &recordingOntologyActionAuthorizer{}, idgen.NewDeterministic("ontology-action"), executions, nil)
	principal := identity.Principal{SubjectProvider: "kratos", Subject: "identity-1", UserID: "human-1", ViewerKind: identity.ViewerKindUser}

	_, err := api.InvokeAction(ctx, principal, "iris.accept_recommendation", httptransport.OntologyActionInvokeRequest{
		TenantID: "tenant-1", RuntimeKind: "web_app", RuntimeID: "orrery-web", ArgsHash: "sha256:args",
		ApprovalID: "approval-1", HILTokenID: "hil-1", ObjectRefs: []httptransport.OntologyActionRefDTO{{Kind: "mission", ID: "mission-1"}},
	})
	if !errors.Is(err, contract.ErrInvalidRequest) {
		t.Fatalf("invoke high-risk action error = %v, want invalid request", err)
	}
	page, listErr := repository.ListActionRuns(ctx, actioncontract.ActionRunQuery{TenantID: "tenant-1"})
	if listErr != nil {
		t.Fatalf("list action runs: %v", listErr)
	}
	if page.Page.Total != 0 {
		t.Fatalf("saved high-risk action runs = %d, want 0", page.Page.Total)
	}
}

func TestProductionOntologyActionAPIRejectsUnverifiedApprovalClaimsForLowRiskAction(t *testing.T) {
	ctx := context.Background()
	repository := actioncontract.NewMemoryRepository()
	service := actioncontract.NewService(repository)
	if err := actioncontract.InstallDefaultActionTypes(ctx, service); err != nil {
		t.Fatalf("install default action types: %v", err)
	}
	executions := newProductionOntologyExecutionAPI(&fakeOntologyExecutionContext{detail: contextview.ObjectDetail{ObjectItem: contextview.ObjectItem{ObjectType: "task"}}})
	api := newProductionOntologyActionAPI(repository, service, &recordingOntologyActionAuthorizer{}, idgen.NewDeterministic("ontology-action"), executions, nil)
	principal := identity.Principal{SubjectProvider: "kratos", Subject: "identity-1", UserID: "human-1", ViewerKind: identity.ViewerKindUser}

	_, err := api.InvokeAction(ctx, principal, "task.complete", httptransport.OntologyActionInvokeRequest{
		TenantID: "tenant-1", RuntimeKind: "web_app", RuntimeID: "orrery-web", ArgsHash: "sha256:args",
		ApprovalID: "unverified-approval", ObjectRefs: []httptransport.OntologyActionRefDTO{{Kind: "task", ID: "task-1"}},
	})
	if !errors.Is(err, contract.ErrInvalidRequest) {
		t.Fatalf("invoke low-risk action with approval claim error = %v, want invalid request", err)
	}
}

func TestProductionOntologyActionAPIRejectsUnsupportedRuntimeKind(t *testing.T) {
	ctx := context.Background()
	repository := actioncontract.NewMemoryRepository()
	service := actioncontract.NewService(repository)
	if err := actioncontract.InstallDefaultActionTypes(ctx, service); err != nil {
		t.Fatalf("install default action types: %v", err)
	}
	executions := newProductionOntologyExecutionAPI(&fakeOntologyExecutionContext{detail: contextview.ObjectDetail{ObjectItem: contextview.ObjectItem{ObjectType: "task"}}})
	api := newProductionOntologyActionAPI(repository, service, &recordingOntologyActionAuthorizer{}, idgen.NewDeterministic("ontology-action"), executions, nil)
	principal := identity.Principal{SubjectProvider: "kratos", Subject: "identity-1", UserID: "human-1", ViewerKind: identity.ViewerKindUser}

	_, err := api.InvokeAction(ctx, principal, "task.complete", httptransport.OntologyActionInvokeRequest{
		TenantID: "tenant-1", RuntimeKind: "untrusted_runtime", RuntimeID: "runtime-1", ArgsHash: "sha256:args",
		ObjectRefs: []httptransport.OntologyActionRefDTO{{Kind: "task", ID: "task-1"}},
	})
	if !errors.Is(err, contract.ErrInvalidRequest) {
		t.Fatalf("invoke unsupported runtime error = %v, want invalid request", err)
	}
}

func TestProductionOntologyActionAPIRecordsAgentPrincipalAsAgentActor(t *testing.T) {
	ctx := context.Background()
	repository := actioncontract.NewMemoryRepository()
	service := actioncontract.NewService(repository)
	if err := actioncontract.InstallDefaultActionTypes(ctx, service); err != nil {
		t.Fatalf("install default action types: %v", err)
	}
	executions := newProductionOntologyExecutionAPI(&fakeOntologyExecutionContext{detail: contextview.ObjectDetail{ObjectItem: contextview.ObjectItem{ObjectType: "task"}}})
	api := newProductionOntologyActionAPI(repository, service, &recordingOntologyActionAuthorizer{}, idgen.NewDeterministic("ontology-agent-action"), executions, nil)
	principal := identity.Principal{
		SubjectProvider: "internal", Subject: "agent-1", UserID: "agent-credential-1", AgentProfileID: "agent-1", ViewerKind: identity.ViewerKindAgent,
	}

	result, err := api.InvokeAction(ctx, principal, "task.complete", httptransport.OntologyActionInvokeRequest{
		TenantID: "tenant-1", RuntimeKind: "web_app", RuntimeID: "orrery-web", ArgsHash: "sha256:args",
		ObjectRefs: []httptransport.OntologyActionRefDTO{{Kind: "task", ID: "task-1"}},
	})
	if err != nil {
		t.Fatalf("invoke agent action: %v", err)
	}
	run, err := repository.GetActionRun(ctx, actioncontract.ActionRunID(result.ID))
	if err != nil {
		t.Fatalf("get saved action run: %v", err)
	}
	if run.Actor != (actioncontract.ActionSubject{Kind: actioncontract.SubjectAgent, ID: "agent-1"}) {
		t.Fatalf("saved agent actor = %+v", run.Actor)
	}
	if run.EffectiveUser != (actioncontract.ActionSubject{}) {
		t.Fatalf("agent action should not invent an effective human: %+v", run.EffectiveUser)
	}
}

func TestProductionOntologyActionAPIRejectsInvalidRepairApplyBeforeConsumingHILReceipt(t *testing.T) {
	ctx := context.Background()
	repository := actioncontract.NewMemoryRepository()
	decisions := &recordingProductionActionDecisionConsumer{}
	service := actioncontract.NewService(repository, actioncontract.WithAuthorizationVerifier(productionActionHILAuthorizer{decisions: decisions}))
	if err := actioncontract.InstallDefaultActionTypes(ctx, service); err != nil {
		t.Fatal(err)
	}
	executor := &recordingOntologyRepairExecutor{validateErr: reliability.ErrInvalidRequest}
	executions := newProductionOntologyExecutionAPI(&fakeOntologyExecutionContext{details: map[string]contextview.ObjectDetail{
		"incident-1": {ObjectItem: contextview.ObjectItem{ObjectType: "failure_incident"}},
		"rule-1":     {ObjectItem: contextview.ObjectItem{ObjectType: "repair_rule"}},
	}})
	api := newProductionOntologyActionAPI(repository, service, &recordingOntologyActionAuthorizer{}, idgen.NewDeterministic("repair-apply"), executions, executor)
	principal := identity.Principal{SubjectProvider: "kratos", Subject: "identity-1", UserID: "operator-1", ViewerKind: identity.ViewerKindUser}

	_, err := api.InvokeAction(ctx, principal, string(productionRepairActionTypeID), httptransport.OntologyActionInvokeRequest{
		TenantID: "tenant-a", ExecutionMode: "apply", RuntimeKind: "system", RuntimeID: "reliability-control-plane",
		ArgsHash: "sha256:repair", ApprovalID: "decision-1", HILTokenID: "receipt-1",
		ObjectRefs: []httptransport.OntologyActionRefDTO{{Kind: "failure_incident", ID: "incident-1"}, {Kind: "repair_rule", ID: "rule-1"}},
	})
	if !errors.Is(err, contract.ErrInvalidRequest) {
		t.Fatalf("repair apply error = %v, want invalid request", err)
	}
	if decisions.calls != 0 {
		t.Fatalf("HIL receipt consumed %d times", decisions.calls)
	}
	if executor.validateCalls != 1 || executor.executeCalls != 0 || executor.validateInput.ExecutionMode != actioncontract.ExecutionModeApply {
		t.Fatalf("repair executor calls: validate=%d execute=%d input=%+v", executor.validateCalls, executor.executeCalls, executor.validateInput)
	}
	page, listErr := repository.ListActionRuns(ctx, actioncontract.ActionRunQuery{TenantID: "tenant-a"})
	if listErr != nil {
		t.Fatal(listErr)
	}
	if page.Page.Total != 0 {
		t.Fatalf("repair apply created %d runs", page.Page.Total)
	}
}

func TestProductionOntologyActionAPIReturnsStartedForApprovedRepairApply(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 15, 15, 0, 0, time.UTC)
	repository := actioncontract.NewMemoryRepository()
	decisions := &recordingProductionActionDecisionConsumer{}
	service := actioncontract.NewService(repository,
		actioncontract.WithClock(func() time.Time { return now }),
		actioncontract.WithAuthorizationVerifier(productionActionHILAuthorizer{decisions: decisions}),
	)
	if err := actioncontract.InstallDefaultActionTypes(ctx, service); err != nil {
		t.Fatal(err)
	}
	executor := &recordingOntologyRepairExecutor{}
	executions := newProductionOntologyExecutionAPI(&fakeOntologyExecutionContext{details: map[string]contextview.ObjectDetail{
		"incident-1":  {ObjectItem: contextview.ObjectItem{ObjectType: "failure_incident"}},
		"rule-1":      {ObjectItem: contextview.ObjectItem{ObjectType: "repair_rule"}},
		"preflight-1": {ObjectItem: contextview.ObjectItem{ObjectType: "evidence_item"}},
	}})
	api := newProductionOntologyActionAPI(repository, service, &recordingOntologyActionAuthorizer{}, idgen.NewDeterministic("repair-apply"), executions, executor)
	principal := identity.Principal{SubjectProvider: "kratos", Subject: "identity-1", UserID: "operator-1", ViewerKind: identity.ViewerKindUser}

	result, err := api.InvokeAction(ctx, principal, string(productionRepairActionTypeID), httptransport.OntologyActionInvokeRequest{
		TenantID: "tenant-a", ExecutionMode: "apply", RuntimeKind: "system", RuntimeID: "reliability-control-plane",
		Purpose: "retry the exact failed run", ArgsHash: "sha256:repair", ApprovalID: "decision-1", HILTokenID: "receipt-1",
		ObjectRefs:   []httptransport.OntologyActionRefDTO{{Kind: "failure_incident", ID: "incident-1"}, {Kind: "repair_rule", ID: "rule-1"}},
		EvidenceRefs: []httptransport.OntologyActionRefDTO{{Kind: "evidence_item", ID: "preflight-1"}},
	})
	if err != nil {
		t.Fatalf("InvokeAction apply: %v", err)
	}
	if result.Status != string(actioncontract.RunStatusStarted) || result.ExecutionMode != string(actioncontract.ExecutionModeApply) || result.PolicyDecisionID != "decision-1" {
		t.Fatalf("repair apply result = %+v", result)
	}
	if decisions.calls != 1 || decisions.input.Action != "ontology.action.apply" || decisions.input.ReceiptID != "receipt-1" {
		t.Fatalf("consumed HIL material = %+v calls=%d", decisions.input, decisions.calls)
	}
	if executor.validateCalls != 1 || executor.executeCalls != 1 || executor.executeInput.Status != actioncontract.RunStatusStarted || len(executor.validateInput.EvidenceRefs) != 1 {
		t.Fatalf("repair executor = %+v", executor)
	}
}

func TestProductionOntologyActionAPIRunsApprovedRepairDryRun(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 15, 30, 0, 0, time.UTC)
	reliabilityRepository, incident, rule := testRepairDryRunReliabilityState(t, ctx, now, reliability.RepairStrategyRetry)
	target, err := reliabilityRepository.GetLatestRepairTarget(ctx, incident.TenantID, incident.ID)
	if err != nil {
		t.Fatal(err)
	}
	argsHash := repairInvocationArgsHash(incident, rule, &target)
	actionRepository := actioncontract.NewMemoryRepository()
	decisions := &recordingProductionActionDecisionConsumer{}
	actionService := actioncontract.NewService(actionRepository,
		actioncontract.WithClock(func() time.Time { return now }),
		actioncontract.WithAuthorizationVerifier(productionActionHILAuthorizer{decisions: decisions}),
	)
	if err := actioncontract.InstallDefaultActionTypes(ctx, actionService); err != nil {
		t.Fatal(err)
	}
	evidence := &recordingRepairDryRunEvidenceRecorder{}
	executor := newProductionRepairDryRunExecutor(reliabilityRepository, reliability.NewDefaultRepairProviderRegistry(), actionService, evidence, func() time.Time { return now })
	executions := newProductionOntologyExecutionAPI(&fakeOntologyExecutionContext{details: map[string]contextview.ObjectDetail{
		incident.ID: {ObjectItem: contextview.ObjectItem{ObjectType: "failure_incident"}},
		rule.ID:     {ObjectItem: contextview.ObjectItem{ObjectType: "repair_rule"}},
	}})
	api := newProductionOntologyActionAPI(actionRepository, actionService, &recordingOntologyActionAuthorizer{}, idgen.NewDeterministic("repair-dry-run"), executions, executor)
	principal := identity.Principal{SubjectProvider: "kratos", Subject: "identity-1", UserID: "operator-1", ViewerKind: identity.ViewerKindUser}

	result, err := api.InvokeAction(ctx, principal, string(productionRepairActionTypeID), httptransport.OntologyActionInvokeRequest{
		TenantID: "tenant-a", ExecutionMode: "dry_run", RuntimeKind: "system", RuntimeID: "reliability-control-plane",
		Purpose: "validate service recovery", ArgsHash: argsHash, ApprovalID: "decision-1", HILTokenID: "receipt-1",
		ObjectRefs: []httptransport.OntologyActionRefDTO{{Kind: "failure_incident", ID: incident.ID}, {Kind: "repair_rule", ID: rule.ID}},
	})
	if err != nil {
		t.Fatalf("InvokeAction: %v", err)
	}
	if result.Status != string(actioncontract.RunStatusCompleted) || result.ExecutionMode != "dry_run" ||
		result.PolicyDecisionID != "decision-1" || result.ApprovalID != "decision-1" {
		t.Fatalf("repair dry-run result = %+v", result)
	}
	if decisions.calls != 1 || decisions.input.Action != "ontology.action.dry_run" || decisions.input.DecisionID != "decision-1" ||
		decisions.input.ReceiptID != "receipt-1" || decisions.input.ArgsHash != argsHash || len(decisions.input.ObjectRefs) != 2 {
		t.Fatalf("consumed HIL material = %+v calls=%d", decisions.input, decisions.calls)
	}
	saved, err := actionRepository.GetActionRun(ctx, actioncontract.ActionRunID(result.ID))
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != actioncontract.RunStatusCompleted || len(saved.EvidenceRefs) != 1 {
		t.Fatalf("saved repair dry-run = %+v", saved)
	}
	if evidence.calls != 1 || evidence.record.EvidenceID != saved.EvidenceRefs[0].ID {
		t.Fatalf("repair dry-run evidence = %+v calls=%d", evidence.record, evidence.calls)
	}
	unchanged, err := reliabilityRepository.GetRepairRule(ctx, "tenant-a", rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.SuccessCount != 0 || unchanged.FailureCount != 0 || unchanged.Version != rule.Version {
		t.Fatalf("approved dry-run changed learned rule = %+v", unchanged)
	}
}
