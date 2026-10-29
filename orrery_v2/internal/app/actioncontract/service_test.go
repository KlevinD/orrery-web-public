package actioncontract

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestServiceRegistersActionTypesForHumanAgentToolAndHybridWork(t *testing.T) {
	ctx := context.Background()
	service := NewService(NewMemoryRepository(), WithClock(fixedClock()), WithAuthorizationVerifier(allowTestActionAuthorization{}))

	definitions := []ActionType{
		{
			ID:               "task.complete",
			Name:             "Complete task",
			Category:         CategoryHuman,
			ProviderKind:     ProviderHuman,
			ResourceResolver: "task",
			PermissionScope:  "task.write",
			RiskLevel:        RiskLow,
			Enabled:          true,
			Version:          "v1",
		},
		{
			ID:               "mission.plan",
			Name:             "Plan mission",
			Category:         CategoryAgent,
			ProviderKind:     ProviderInternalService,
			ResourceResolver: "mission",
			PermissionScope:  "mission.plan",
			RiskLevel:        RiskMedium,
			Enabled:          true,
			Version:          "v1",
		},
		{
			ID:               "email.send",
			Name:             "Send email",
			Category:         CategoryTool,
			ProviderKind:     ProviderMCPTool,
			ProviderRef:      "mcp:lark_mail.send",
			ResourceResolver: "email",
			PermissionScope:  "email.send",
			RiskLevel:        RiskHigh,
			Enabled:          true,
			Version:          "v1",
		},
		{
			ID:               "iris.accept_recommendation",
			Name:             "Accept Iris recommendation",
			Category:         CategoryHybrid,
			ProviderKind:     ProviderInternalService,
			ResourceResolver: "mission",
			PermissionScope:  "mission.write",
			RiskLevel:        RiskHigh,
			Enabled:          true,
			Version:          "v1",
		},
	}

	for _, definition := range definitions {
		if err := service.RegisterActionType(ctx, definition); err != nil {
			t.Fatalf("RegisterActionType(%s) error = %v", definition.ID, err)
		}
	}

	registered, err := service.ListActionTypes(ctx)
	if err != nil {
		t.Fatalf("ListActionTypes error = %v", err)
	}
	if got, want := len(registered), len(definitions); got != want {
		t.Fatalf("registered action types = %d, want %d", got, want)
	}
	if registered[0].ID != "email.send" || registered[3].ID != "task.complete" {
		t.Fatalf("action types are not sorted by id: %#v", registered)
	}
}

func TestServiceStartsRunsForHumanAgentToolAndHybridActors(t *testing.T) {
	ctx := context.Background()
	service := NewService(NewMemoryRepository(), WithClock(fixedClock()), WithAuthorizationVerifier(allowTestActionAuthorization{}))
	registerMinimalActionTypes(t, ctx, service)

	cases := []struct {
		name         string
		request      StartRunRequest
		wantActor    string
		wantRunID    ActionRunID
		wantCategory ActionCategory
	}{
		{
			name: "human page action",
			request: StartRunRequest{
				RunID:        "run-human",
				TenantID:     "tenant-1",
				ActionTypeID: "task.complete",
				Actor:        ActionSubject{Kind: SubjectHuman, ID: "klevin"},
				Runtime:      RuntimeRef{Kind: RuntimeWebApp, ID: "web"},
				ObjectRefs:   []ObjectRef{{Kind: "task", ID: "task-1"}},
				ArgsHash:     "sha256:human",
			},
			wantActor:    "klevin",
			wantRunID:    "run-human",
			wantCategory: CategoryHuman,
		},
		{
			name: "agent on behalf of human",
			request: StartRunRequest{
				RunID:         "run-agent",
				TenantID:      "tenant-1",
				ActionTypeID:  "mission.plan",
				Actor:         ActionSubject{Kind: SubjectAgent, ID: "planner-agent"},
				EffectiveUser: ActionSubject{Kind: SubjectHuman, ID: "klevin"},
				Delegation:    Ref{Kind: "delegation", ID: "delegate-1"},
				Runtime:       RuntimeRef{Kind: RuntimeAgent, ID: "agent-run-1"},
				ObjectRefs:    []ObjectRef{{Kind: "mission", ID: "mission-1"}},
				ArgsHash:      "sha256:agent",
			},
			wantActor:    "planner-agent",
			wantRunID:    "run-agent",
			wantCategory: CategoryAgent,
		},
		{
			name: "mcp tool action",
			request: StartRunRequest{
				RunID:         "run-tool",
				TenantID:      "tenant-1",
				ActionTypeID:  "email.send",
				Actor:         ActionSubject{Kind: SubjectAgent, ID: "marketing-agent"},
				EffectiveUser: ActionSubject{Kind: SubjectHuman, ID: "klevin"},
				Runtime:       RuntimeRef{Kind: RuntimeMCPGateway, ID: "gateway"},
				ObjectRefs:    []ObjectRef{{Kind: "customer", ID: "customer-1"}},
				ArgsHash:      "sha256:tool",
			},
			wantActor:    "marketing-agent",
			wantRunID:    "run-tool",
			wantCategory: CategoryTool,
		},
		{
			name: "hybrid iris action",
			request: StartRunRequest{
				RunID:        "run-hybrid",
				TenantID:     "tenant-1",
				ActionTypeID: "iris.accept_recommendation",
				Actor:        ActionSubject{Kind: SubjectHuman, ID: "klevin"},
				Runtime:      RuntimeRef{Kind: RuntimeIRIS, ID: "decision-run-1"},
				ObjectRefs:   []ObjectRef{{Kind: "mission", ID: "mission-1"}},
				ArgsHash:     "sha256:hybrid",
				HumanIntent:  Ref{Kind: "decision_acceptance", ID: "accept-1"},
			},
			wantActor:    "klevin",
			wantRunID:    "run-hybrid",
			wantCategory: CategoryHybrid,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.wantCategory == CategoryTool || tc.wantCategory == CategoryHybrid {
				tc.request.Approval = Ref{Kind: "approval", ID: "approval-1"}
				tc.request.PolicyDecisionID = "policy-decision-1"
				tc.request.HILTokenID = "hil-token-1"
			}
			run, err := service.StartActionRun(ctx, tc.request)
			if err != nil {
				t.Fatalf("StartActionRun error = %v", err)
			}
			if run.ID != tc.wantRunID {
				t.Fatalf("run id = %q, want %q", run.ID, tc.wantRunID)
			}
			if run.Actor.ID != tc.wantActor {
				t.Fatalf("actor id = %q, want %q", run.Actor.ID, tc.wantActor)
			}
			if run.ActionCategory != tc.wantCategory {
				t.Fatalf("category = %q, want %q", run.ActionCategory, tc.wantCategory)
			}
			if run.Status != RunStatusStarted {
				t.Fatalf("status = %q, want %q", run.Status, RunStatusStarted)
			}
			if run.CreatedAt.IsZero() {
				t.Fatalf("CreatedAt was not set")
			}
		})
	}
}

func TestServiceCompletesRunWithWritebackReceipt(t *testing.T) {
	ctx := context.Background()
	service := NewService(NewMemoryRepository(), WithClock(fixedClock()), WithAuthorizationVerifier(allowTestActionAuthorization{}))
	registerMinimalActionTypes(t, ctx, service)
	run, err := service.StartActionRun(ctx, StartRunRequest{
		RunID:            "run-tool",
		TenantID:         "tenant-1",
		ActionTypeID:     "email.send",
		Actor:            ActionSubject{Kind: SubjectAgent, ID: "marketing-agent"},
		EffectiveUser:    ActionSubject{Kind: SubjectHuman, ID: "klevin"},
		Runtime:          RuntimeRef{Kind: RuntimeMCPGateway, ID: "gateway"},
		ObjectRefs:       []ObjectRef{{Kind: "customer", ID: "customer-1"}},
		ArgsHash:         "sha256:tool",
		Approval:         Ref{Kind: "approval", ID: "approval-1"},
		PolicyDecisionID: "policy-decision-1",
		HILTokenID:       "hil-token-1",
	})
	if err != nil {
		t.Fatalf("StartActionRun error = %v", err)
	}

	completed, receipt, err := service.CompleteActionRun(ctx, CompleteRunRequest{
		RunID:           run.ID,
		ProviderCallRef: Ref{Kind: "mcp_tool_call", ID: "call-1"},
		ResultRef:       Ref{Kind: "mcp_result", ID: "result-1"},
		Writeback: &WritebackReceipt{
			ID:                  "receipt-1",
			TargetSystem:        "lark_mail",
			TargetResourceRefs:  []ObjectRef{{Kind: "email", ID: "message-1"}},
			ExternalOperationID: "message-1",
			IdempotencyKey:      "idem-1",
			Status:              WritebackStatusSucceeded,
		},
	})
	if err != nil {
		t.Fatalf("CompleteActionRun error = %v", err)
	}

	if completed.Status != RunStatusCompleted {
		t.Fatalf("completed status = %q, want %q", completed.Status, RunStatusCompleted)
	}
	if completed.CompletedAt == nil || completed.CompletedAt.IsZero() {
		t.Fatalf("CompletedAt was not set")
	}
	if completed.ProviderCallRef.ID != "call-1" {
		t.Fatalf("provider call ref = %+v", completed.ProviderCallRef)
	}
	if receipt == nil {
		t.Fatalf("receipt is nil")
	}
	if receipt.ActionRunID != run.ID || receipt.TenantID != "tenant-1" {
		t.Fatalf("receipt did not inherit run id and tenant: %#v", receipt)
	}

	receipts, err := service.ListWritebackReceipts(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListWritebackReceipts error = %v", err)
	}
	if len(receipts) != 1 || receipts[0].ID != "receipt-1" {
		t.Fatalf("receipts = %#v, want receipt-1", receipts)
	}
}

func TestServiceFailsStartedActionRun(t *testing.T) {
	ctx := context.Background()
	service := NewService(NewMemoryRepository(), WithClock(fixedClock()))
	if err := InstallDefaultActionTypes(ctx, service); err != nil {
		t.Fatal(err)
	}
	run, err := service.StartActionRun(ctx, StartRunRequest{
		RunID: "run-failed", TenantID: "tenant-1", ActionTypeID: "mcp.tool.call",
		Actor: ActionSubject{Kind: SubjectAgent, ID: "agent-1"}, Runtime: RuntimeRef{Kind: RuntimeMCPGateway, ID: "gateway"},
		ObjectRefs: []ObjectRef{{Kind: "tool", ID: "mcp.github.search"}}, ArgsHash: "sha256:args",
	})
	if err != nil {
		t.Fatal(err)
	}

	failed, err := service.FailActionRun(ctx, FailRunRequest{
		RunID: run.ID, ProviderCallRef: Ref{Kind: "mcp_tool_call", ID: "call-failed"},
		ErrorCode: "dependency_unavailable", ErrorMessage: "MCP tool dependency was unavailable",
	})
	if err != nil {
		t.Fatalf("FailActionRun: %v", err)
	}
	if failed.Status != RunStatusFailed || failed.CompletedAt == nil || failed.ErrorCode != "dependency_unavailable" || failed.ProviderCallRef.ID != "call-failed" {
		t.Fatalf("failed run = %+v", failed)
	}
	if _, err := service.FailActionRun(ctx, FailRunRequest{RunID: run.ID, ErrorCode: "again"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("second FailActionRun error = %v, want ErrInvalidRequest", err)
	}
}

func TestServiceValidatesWritebackBeforeCompletingRun(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	service := NewService(repository, WithClock(fixedClock()))
	if err := InstallDefaultActionTypes(ctx, service); err != nil {
		t.Fatal(err)
	}
	run, err := service.StartActionRun(ctx, StartRunRequest{
		RunID: "run-invalid-receipt", TenantID: "tenant-1", ActionTypeID: "mcp.tool.call",
		Actor: ActionSubject{Kind: SubjectService, ID: "gateway"}, Runtime: RuntimeRef{Kind: RuntimeMCPGateway, ID: "gateway"},
		ObjectRefs: []ObjectRef{{Kind: "tool", ID: "tool-1"}}, ArgsHash: "sha256:args",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.CompleteActionRun(ctx, CompleteRunRequest{
		RunID:     run.ID,
		Writeback: &WritebackReceipt{ID: "receipt-invalid", TargetSystem: "mcp", Status: "unknown"},
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("CompleteActionRun error = %v, want ErrInvalidRequest", err)
	}
	stored, err := repository.GetActionRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != RunStatusStarted {
		t.Fatalf("run status = %q, want started", stored.Status)
	}
}

func TestServiceRejectsInvalidRunRequests(t *testing.T) {
	ctx := context.Background()
	service := NewService(NewMemoryRepository(), WithClock(fixedClock()))
	registerMinimalActionTypes(t, ctx, service)

	_, err := service.StartActionRun(ctx, StartRunRequest{
		RunID:        "run-missing-actor",
		TenantID:     "tenant-1",
		ActionTypeID: "task.complete",
		Runtime:      RuntimeRef{Kind: RuntimeWebApp, ID: "web"},
		ObjectRefs:   []ObjectRef{{Kind: "task", ID: "task-1"}},
		ArgsHash:     "sha256:missing-actor",
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing actor error = %v, want ErrInvalidRequest", err)
	}

	_, err = service.StartActionRun(ctx, StartRunRequest{
		RunID:        "run-missing-hash",
		TenantID:     "tenant-1",
		ActionTypeID: "task.complete",
		Actor:        ActionSubject{Kind: SubjectHuman, ID: "klevin"},
		Runtime:      RuntimeRef{Kind: RuntimeWebApp, ID: "web"},
		ObjectRefs:   []ObjectRef{{Kind: "task", ID: "task-1"}},
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing args hash error = %v, want ErrInvalidRequest", err)
	}
}

func TestServiceRejectsActionRunObjectRefsOutsideActionContract(t *testing.T) {
	ctx := context.Background()
	service := NewService(NewMemoryRepository(), WithClock(fixedClock()))
	registerMinimalActionTypes(t, ctx, service)

	_, err := service.StartActionRun(ctx, StartRunRequest{
		RunID:        "run-wrong-object-kind",
		TenantID:     "tenant-1",
		ActionTypeID: "task.complete",
		Actor:        ActionSubject{Kind: SubjectHuman, ID: "klevin"},
		Runtime:      RuntimeRef{Kind: RuntimeWebApp, ID: "web"},
		ObjectRefs:   []ObjectRef{{Kind: "contract", ID: "contract-1"}},
		ArgsHash:     "sha256:wrong-object-kind",
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("wrong object kind error = %v, want ErrInvalidRequest", err)
	}
}

func registerMinimalActionTypes(t *testing.T, ctx context.Context, service *Service) {
	t.Helper()
	for _, actionType := range []ActionType{
		{ID: "task.complete", Name: "Complete task", Category: CategoryHuman, ObjectTypeRefs: []string{"task"}, ProviderKind: ProviderHuman, ResourceResolver: "task", PermissionScope: "task.write", RiskLevel: RiskLow, Enabled: true, Version: "v1"},
		{ID: "mission.plan", Name: "Plan mission", Category: CategoryAgent, ObjectTypeRefs: []string{"mission"}, ProviderKind: ProviderInternalService, ResourceResolver: "mission", PermissionScope: "mission.plan", RiskLevel: RiskMedium, Enabled: true, Version: "v1"},
		{ID: "email.send", Name: "Send email", Category: CategoryTool, ObjectTypeRefs: []string{"customer"}, ProviderKind: ProviderMCPTool, ProviderRef: "mcp:lark_mail.send", ResourceResolver: "email", PermissionScope: "email.send", RiskLevel: RiskHigh, Enabled: true, Version: "v1"},
		{ID: "iris.accept_recommendation", Name: "Accept Iris recommendation", Category: CategoryHybrid, ObjectTypeRefs: []string{"mission"}, ProviderKind: ProviderInternalService, ResourceResolver: "mission", PermissionScope: "mission.write", RiskLevel: RiskHigh, Enabled: true, Version: "v1"},
	} {
		if err := service.RegisterActionType(ctx, actionType); err != nil {
			t.Fatalf("RegisterActionType(%s) error = %v", actionType.ID, err)
		}
	}
}

func fixedClock() func() time.Time {
	return func() time.Time {
		return time.Date(2026, 6, 19, 9, 30, 0, 0, time.UTC)
	}
}

type allowTestActionAuthorization struct{}

func (allowTestActionAuthorization) VerifyActionAuthorization(context.Context, StartRunRequest, ActionType) error {
	return nil
}
