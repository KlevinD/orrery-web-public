package actioncontract

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestPlatformRepositoryPersistsActionTypeRunAndWriteback(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "actioncontract-platform-repo"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner([]migration.Migration{actionContractMigration(), actionTypeSemanticsMigration(), actionRunExecutionModeMigration()}).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	repo := NewPlatformRepository(store)
	service := NewService(repo, WithClock(fixedClock()))

	actionType, ok := DefaultActionType("mcp.tool.call")
	if !ok {
		t.Fatalf("missing default mcp action")
	}
	actionType.Semantics = ActionSemanticProfile{Verbs: []string{"调用工具"}, Effects: []string{"tool_result_created"}}
	if err := service.RegisterActionType(ctx, actionType); err != nil {
		t.Fatalf("RegisterActionType: %v", err)
	}
	loaded, err := repo.GetActionType(ctx, "mcp.tool.call")
	if err != nil {
		t.Fatalf("GetActionType: %v", err)
	}
	if loaded.ProviderKind != ProviderMCPTool || loaded.IdempotencyPolicy["args_hash_required"] != true || len(loaded.Semantics.Verbs) != 1 || loaded.Semantics.Verbs[0] != "调用工具" {
		t.Fatalf("loaded action type = %#v", loaded)
	}

	run, err := service.StartActionRun(ctx, StartRunRequest{
		RunID:         "run-1",
		TenantID:      "tenant-1",
		ActionTypeID:  "mcp.tool.call",
		Actor:         ActionSubject{Kind: SubjectAgent, ID: "agent-1"},
		EffectiveUser: ActionSubject{Kind: SubjectHuman, ID: "human-1"},
		Runtime:       RuntimeRef{Kind: RuntimeMCPGateway, ID: "gateway"},
		ObjectRefs:    []ObjectRef{{Kind: "tool", ID: "github.search"}},
		ArgsHash:      "sha256:abc",
		ExecutionMode: ExecutionModeApply,
	})
	if err != nil {
		t.Fatalf("StartActionRun: %v", err)
	}
	reopened := NewPlatformRepository(store)
	loadedRun, err := reopened.GetActionRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetActionRun: %v", err)
	}
	if loadedRun.ActionTypeID != "mcp.tool.call" || loadedRun.ObjectRefs[0].ID != "github.search" || loadedRun.ExecutionMode != ExecutionModeApply {
		t.Fatalf("loaded run = %#v", loadedRun)
	}
	loadedRuns, err := reopened.GetActionRuns(ctx, []ActionRunID{run.ID, run.ID, "missing-run"})
	if err != nil {
		t.Fatalf("GetActionRuns: %v", err)
	}
	if len(loadedRuns) != 1 || loadedRuns[run.ID].TenantID != "tenant-1" {
		t.Fatalf("loaded run batch = %#v", loadedRuns)
	}
	runTenants, err := reopened.GetActionRunTenants(ctx, []ActionRunID{run.ID, run.ID, "missing-run"})
	if err != nil {
		t.Fatalf("GetActionRunTenants: %v", err)
	}
	if len(runTenants) != 1 || runTenants[run.ID] != "tenant-1" {
		t.Fatalf("action run tenant batch = %#v", runTenants)
	}

	if _, receipt, err := service.CompleteActionRun(ctx, CompleteRunRequest{
		RunID:           run.ID,
		ProviderCallRef: Ref{Kind: "mcp_tool_call", ID: "call-1"},
		EvidenceRefs:    []Ref{{Kind: "evidence_item", ID: "verification-1"}},
		Writeback: &WritebackReceipt{
			ID:                 "receipt-1",
			TargetSystem:       "github",
			TargetResourceRefs: []ObjectRef{{Kind: "issue", ID: "42"}},
			Status:             WritebackStatusSucceeded,
		},
	}); err != nil {
		t.Fatalf("CompleteActionRun: receipt=%#v err=%v", receipt, err)
	}
	receipts, err := reopened.ListWritebackReceipts(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListWritebackReceipts: %v", err)
	}
	if len(receipts) != 1 || receipts[0].TargetResourceRefs[0].ID != "42" {
		t.Fatalf("receipts = %#v", receipts)
	}
	completed, err := reopened.GetActionRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetActionRun completed: %v", err)
	}
	if completed.ProviderCallRef.ID != "call-1" || completed.Status != RunStatusCompleted || len(completed.EvidenceRefs) != 1 || completed.EvidenceRefs[0].ID != "verification-1" {
		t.Fatalf("completed run = %#v", completed)
	}
}

func TestPlatformRepositoryRejectsCrossTenantRunIDCollision(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "actioncontract-platform-tenant-collision"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner([]migration.Migration{actionContractMigration(), actionTypeSemanticsMigration(), actionRunExecutionModeMigration()}).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	service := NewService(NewPlatformRepository(store), WithClock(fixedClock()))
	actionType, _ := DefaultActionType("mcp.tool.call")
	if err := service.RegisterActionType(ctx, actionType); err != nil {
		t.Fatalf("RegisterActionType: %v", err)
	}
	request := StartRunRequest{
		RunID: "shared-run", TenantID: "tenant-1", ActionTypeID: "mcp.tool.call",
		Actor: ActionSubject{Kind: SubjectAgent, ID: "agent-1"}, Runtime: RuntimeRef{Kind: RuntimeMCPGateway, ID: "gateway"},
		ObjectRefs: []ObjectRef{{Kind: "tool", ID: "github.search"}}, ArgsHash: "sha256:one",
	}
	if _, err := service.StartActionRun(ctx, request); err != nil {
		t.Fatalf("StartActionRun tenant-1: %v", err)
	}
	request.TenantID = "tenant-2"
	request.ArgsHash = "sha256:two"
	if _, err := service.StartActionRun(ctx, request); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("cross-tenant StartActionRun error = %v, want ErrInvalidRequest", err)
	}
}

func TestPlatformRepositoryListsActionRunsWithFilters(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "actioncontract-platform-runs"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner([]migration.Migration{actionContractMigration(), actionTypeSemanticsMigration(), actionRunExecutionModeMigration()}).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	repo := NewPlatformRepository(store)
	service := NewService(repo, WithClock(fixedClock()))

	for _, id := range []ActionTypeID{"task.complete", "mcp.tool.call"} {
		actionType, ok := DefaultActionType(id)
		if !ok {
			t.Fatalf("missing default action %s", id)
		}
		if err := service.RegisterActionType(ctx, actionType); err != nil {
			t.Fatalf("RegisterActionType(%s): %v", id, err)
		}
	}
	if _, err := service.StartActionRun(ctx, StartRunRequest{
		RunID:        "run-human",
		TenantID:     "tenant-1",
		ActionTypeID: "task.complete",
		Actor:        ActionSubject{Kind: SubjectHuman, ID: "operator-1"},
		Runtime:      RuntimeRef{Kind: RuntimeWebApp, ID: "web"},
		ObjectRefs:   []ObjectRef{{Kind: "task", ID: "task-1"}},
		ArgsHash:     "sha256:human",
	}); err != nil {
		t.Fatalf("StartActionRun human: %v", err)
	}
	toolRun, err := service.StartActionRun(ctx, StartRunRequest{
		RunID:        "run-tool",
		TenantID:     "tenant-1",
		ActionTypeID: "mcp.tool.call",
		Actor:        ActionSubject{Kind: SubjectAgent, ID: "agent-1"},
		Runtime:      RuntimeRef{Kind: RuntimeMCPGateway, ID: "gateway"},
		ObjectRefs:   []ObjectRef{{Kind: "tool", ID: "github.search"}},
		ArgsHash:     "sha256:tool",
	})
	if err != nil {
		t.Fatalf("StartActionRun tool: %v", err)
	}
	if _, _, err := service.CompleteActionRun(ctx, CompleteRunRequest{RunID: toolRun.ID}); err != nil {
		t.Fatalf("CompleteActionRun tool: %v", err)
	}

	page, err := repo.ListActionRuns(ctx, ActionRunQuery{
		TenantID:     "tenant-1",
		ActionTypeID: "mcp.tool.call",
		Status:       RunStatusCompleted,
		ActorID:      "agent-1",
		RuntimeKind:  RuntimeMCPGateway,
		Limit:        10,
	})
	if err != nil {
		t.Fatalf("ListActionRuns: %v", err)
	}
	if page.Page.Total != 1 || page.Page.Returned != 1 {
		t.Fatalf("page = %+v", page.Page)
	}
	if len(page.Runs) != 1 || page.Runs[0].ID != "run-tool" || page.Runs[0].Status != RunStatusCompleted {
		t.Fatalf("runs = %+v", page.Runs)
	}
}
