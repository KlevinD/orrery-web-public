package actioncontract

import (
	"context"
	"errors"
	"testing"
)

func TestDefaultReliabilityRepairActionRequiresGovernedHILExecution(t *testing.T) {
	actionType, ok := DefaultActionType("reliability.repair.execute")
	if !ok {
		t.Fatal("default reliability repair action is missing")
	}
	if actionType.RiskLevel != RiskHigh || actionType.ProviderKind != ProviderInternalService || !actionType.DryRunSupported {
		t.Fatalf("repair action governance = %+v", actionType)
	}
	if actionType.HILPolicy["human_intent_required"] != true || actionType.FailurePolicy["automatic_retry"] != false {
		t.Fatalf("repair action policies = HIL:%+v failure:%+v", actionType.HILPolicy, actionType.FailurePolicy)
	}
	service := NewService(NewMemoryRepository())
	if err := service.RegisterActionType(context.Background(), actionType); err != nil {
		t.Fatal(err)
	}
	_, err := service.StartActionRun(context.Background(), StartRunRequest{
		RunID: "repair-run-1", TenantID: "tenant-a", ActionTypeID: actionType.ID,
		Actor: ActionSubject{Kind: SubjectAgent, ID: "reliability-agent"}, Runtime: RuntimeRef{Kind: RuntimeSystem, ID: "reliability"},
		ObjectRefs: []ObjectRef{{Kind: "failure_incident", ID: "failure-1"}, {Kind: "repair_rule", ID: "rule-1"}},
		ArgsHash:   "sha256:repair",
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("repair without approval error = %v, want ErrInvalidRequest", err)
	}
}

func TestDefaultReliabilityRepairActionRequiresBoundTargetsAndNewTerminalEvidence(t *testing.T) {
	ctx := context.Background()
	actionType, ok := DefaultActionType("reliability.repair.execute")
	if !ok {
		t.Fatal("default reliability repair action is missing")
	}
	repository := NewMemoryRepository()
	service := NewService(repository, WithAuthorizationVerifier(allowTestActionAuthorization{}))
	if err := service.RegisterActionType(ctx, actionType); err != nil {
		t.Fatal(err)
	}
	request := StartRunRequest{
		RunID: "repair-run-1", TenantID: "tenant-a", ActionTypeID: actionType.ID,
		Actor: ActionSubject{Kind: SubjectHuman, ID: "operator-1"}, Runtime: RuntimeRef{Kind: RuntimeSystem, ID: "reliability"},
		ObjectRefs: []ObjectRef{{Kind: "failure_incident", ID: "failure-1"}}, ArgsHash: "sha256:repair",
		ExecutionMode: ExecutionModeDryRun,
		Approval:      Ref{Kind: "approval", ID: "approval-1"}, PolicyDecisionID: "policy-1", HILTokenID: "hil-1",
		EvidenceRefs: []Ref{{Kind: "evidence_item", ID: "diagnosis-1"}},
	}
	if _, err := service.StartActionRun(ctx, request); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("repair without rule target error = %v, want ErrInvalidRequest", err)
	}
	request.ObjectRefs = append(request.ObjectRefs, ObjectRef{Kind: "repair_rule", ID: "rule-1"})
	run, err := service.StartActionRun(ctx, request)
	if err != nil {
		t.Fatalf("StartActionRun: %v", err)
	}
	if _, _, err := service.CompleteActionRun(ctx, CompleteRunRequest{
		RunID: run.ID, EvidenceRefs: []Ref{{Kind: "evidence_item", ID: "diagnosis-1"}},
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("completion with reused evidence error = %v, want ErrInvalidRequest", err)
	}
	completed, _, err := service.CompleteActionRun(ctx, CompleteRunRequest{
		RunID: run.ID, EvidenceRefs: []Ref{{Kind: "evidence_item", ID: "verification-1"}},
	})
	if err != nil {
		t.Fatalf("CompleteActionRun: %v", err)
	}
	if completed.Status != RunStatusCompleted || completed.ExecutionMode != ExecutionModeDryRun || len(completed.EvidenceRefs) != 2 || completed.EvidenceRefs[1].ID != "verification-1" {
		t.Fatalf("completed repair run = %+v", completed)
	}

	request.RunID = "repair-run-2"
	failedRun, err := service.StartActionRun(ctx, request)
	if err != nil {
		t.Fatalf("StartActionRun failed repair: %v", err)
	}
	failed, err := service.FailActionRun(ctx, FailRunRequest{RunID: failedRun.ID, ErrorCode: "repair_failed"})
	if err != nil {
		t.Fatalf("FailActionRun: %v", err)
	}
	if failed.Status != RunStatusFailed || len(failed.EvidenceRefs) != 2 || failed.EvidenceRefs[1].ID != "action-run-failure-evidence:repair-run-2" {
		t.Fatalf("failed repair run evidence = %+v", failed)
	}
}

func TestRegisterActionTypeRejectsInvalidReliabilityAuditPolicy(t *testing.T) {
	ctx := context.Background()
	for name, mutate := range map[string]func(*ActionType){
		"undeclared required target": func(actionType *ActionType) {
			actionType.AuditPolicy["required_object_types"] = []string{"failure_incident", "unknown"}
		},
		"malformed boolean": func(actionType *ActionType) {
			actionType.AuditPolicy["terminal_evidence_required"] = "yes"
		},
		"empty evidence kind": func(actionType *ActionType) {
			actionType.AuditPolicy["terminal_evidence_kind"] = " "
		},
	} {
		t.Run(name, func(t *testing.T) {
			actionType, ok := DefaultActionType("reliability.repair.execute")
			if !ok {
				t.Fatal("default reliability repair action is missing")
			}
			mutate(&actionType)
			err := NewService(NewMemoryRepository()).RegisterActionType(ctx, actionType)
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("RegisterActionType error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}
