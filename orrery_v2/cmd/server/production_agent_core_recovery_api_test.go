package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"orrery_v2/internal/agent/runtime/agentkernel"
	"orrery_v2/internal/app/agentrunrecovery"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/platform/db"
)

type recoveryStoreStub struct {
	view       agentkernel.TaskInspection
	refs       []agentkernel.TaskRef
	reviews    int
	actor, key string
	err        error
}

func (s *recoveryStoreStub) CheckTaskRestart(context.Context, string, string, string) error {
	return s.err
}
func (s *recoveryStoreStub) InspectTask(_ context.Context, ref agentkernel.TaskRef) (agentkernel.TaskInspection, error) {
	s.refs = append(s.refs, ref)
	return s.view, s.err
}
func (s *recoveryStoreStub) ReviewTask(_ context.Context, ref agentkernel.TaskRef, actor, key string, input agentkernel.TaskReviewInput) (agentkernel.TaskReview, error) {
	s.refs = append(s.refs, ref)
	s.reviews++
	s.actor, s.key = actor, key
	return agentkernel.TaskReview{TaskReviewInput: input, ActorID: actor}, s.err
}

func TestProductionAgentCoreRecoveryAPIEnforcesObjectAndOperationPermissions(t *testing.T) {
	ctx := context.Background()
	query := &productionDiscussionMissionToolTestQuery{ops: missioncontrol.MissionOpsViewDTO{RuntimeSessions: []missioncontrol.RuntimeSessionDTO{{RunID: "run-1", AgentID: "agent-1", TaskID: "task-1", RuntimeKind: "managed", Status: "failed"}}}}
	store := &recoveryStoreStub{view: agentkernel.TaskInspection{Available: true, Revision: "9007199254740993", Phase: "tools_pending", RestartBlocked: true}}
	api := productionAPI{missionQuery: query, agentCoreRecovery: store, authorizationService: authorization.NewService(fixedProductionAuthorizationAdapter{decision: authorization.Decision{Allowed: true}})}
	viewer := missioncontrol.ViewerRef{ViewerID: "operator-1", ViewerKind: missioncontrol.ViewerKindUser, TenantID: "tenant-1", PermissionScope: []string{"operations.manage"}}
	view, err := api.GetAgentRunRecovery(ctx, viewer, "mission-1", "run-1")
	if err != nil || !view.CanReview || view.CanRestart {
		t.Fatalf("unsafe operator actions: %+v %v", view, err)
	}
	if len(store.refs) != 1 || store.refs[0] != (agentkernel.TaskRef{TenantID: "tenant-1", MissionID: "mission-1", TaskID: "task-1", AgentID: "agent-1", RunID: "run-1"}) {
		t.Fatal("untrusted task identity")
	}
	input := agentrunrecovery.ReviewInput{Revision: view.Revision, Conclusion: "uncertain", Note: "verified", EvidenceRefs: []string{"receipt:42"}}
	if _, err := api.ReviewAgentRunRecovery(ctx, viewer, "mission-1", "run-1", "review-key", input); err != nil || store.actor != "operator-1" || store.key != "review-key" {
		t.Fatalf("trusted actor not recorded: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*missioncontrol.ViewerRef)
	}{
		{"read-only", func(v *missioncontrol.ViewerRef) { v.PermissionScope = []string{"operations.read"} }},
		{"agent", func(v *missioncontrol.ViewerRef) { v.ViewerKind = missioncontrol.ViewerKindAgent }},
		{"system", func(v *missioncontrol.ViewerRef) { v.ViewerKind = missioncontrol.ViewerKindSystem }},
		{"mission-editor-without-operations", func(v *missioncontrol.ViewerRef) { v.PermissionScope = []string{"mission.write"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := viewer
			tc.mutate(&v)
			before := store.reviews
			if _, err := api.ReviewAgentRunRecovery(ctx, v, "mission-1", "run-1", "key", input); !errors.Is(err, operations.ErrPermissionDenied) || store.reviews != before {
				t.Fatalf("review authorization bypass: %v", err)
			}
		})
	}
	readOnly := viewer
	readOnly.PermissionScope = []string{"operations.read"}
	if view, err := api.GetAgentRunRecovery(ctx, readOnly, "mission-1", "run-1"); err != nil || view.CanReview || view.CanRestart {
		t.Fatal("read-only operator received mutation actions")
	}
	before := len(store.refs)
	if _, err := api.GetAgentRunRecovery(ctx, viewer, "mission-1", "another-run"); !errors.Is(err, missioncontrol.ErrProjectionNotFound) || len(store.refs) != before {
		t.Fatal("unknown run inspected private checkpoint")
	}
	query.ops.RuntimeSessions[0].RuntimeKind = "codex"
	if _, err := api.GetAgentRunRecovery(ctx, viewer, "mission-1", "run-1"); !errors.Is(err, missioncontrol.ErrProjectionNotFound) {
		t.Fatal("native runtime treated as AgentCore")
	}
	query.ops.RuntimeSessions[0].RuntimeKind = "managed"
	query.ops.RuntimeSessions[0].IsAlive = true
	if _, err := api.ReviewAgentRunRecovery(ctx, viewer, "mission-1", "run-1", "key", input); !errors.Is(err, missioncontrol.ErrConflict) {
		t.Fatal("live execution accepted review")
	}
	query.ops.RuntimeSessions[0].IsAlive = false
	query.ops.RuntimeSessions[0].Status = "timed_out"
	if view, err := api.GetAgentRunRecovery(ctx, viewer, "mission-1", "run-1"); err != nil || !view.CanReview {
		t.Fatal("timed-out run lost its review entry")
	}
	store.view.RestartBlocked = false
	store.view.Phase = "ready"
	if view, err := api.GetAgentRunRecovery(ctx, viewer, "mission-1", "run-1"); err != nil || !view.CanRestart || view.CanReview {
		t.Fatal("safe failed run has incorrect actions")
	}
	// Mission authorization is checked before the runtime store is touched.
	api.authorizationService = authorization.NewService(fixedProductionAuthorizationAdapter{decision: authorization.Decision{Allowed: false}})
	before = len(store.refs)
	if _, err := api.GetAgentRunRecovery(ctx, viewer, "mission-1", "run-1"); !errors.Is(err, missioncontrol.ErrProjectionNotFound) || len(store.refs) != before {
		t.Fatal("invisible mission exposed checkpoint")
	}
	if _, err := api.ReviewAgentRunRecovery(ctx, viewer, "mission-1", "run-1", "key", input); !errors.Is(err, missioncontrol.ErrProjectionNotFound) || len(store.refs) != before {
		t.Fatal("invisible mission accepted review")
	}
}

func TestProductionAgentCoreRestartGuardFailsClosed(t *testing.T) {
	for _, cause := range []error{agentkernel.ErrUnsafeResume, agentkernel.ErrCheckpoint} {
		guard := productionAgentCoreRestartGuard{store: &recoveryStoreStub{err: cause}}
		if err := guard.CheckTaskRestart(context.Background(), "tenant", "mission", "task"); !errors.Is(err, missioncontrol.ErrConflict) {
			t.Fatalf("unsafe restart allowed: %v", err)
		}
	}
}

func TestProductionAgentCoreRecoveryWiringSharesStoreAndCommand(t *testing.T) {
	database, err := db.Open(context.Background(), db.Config{Driver: db.DriverFile, URL: "file://" + filepath.Join(t.TempDir(), "runtime.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	checkpoints, err := agentkernel.NewSQLCheckpointStore(database)
	if err != nil {
		t.Fatal(err)
	}
	command := missioncontrol.NewCommandService(nil)
	committer := &discussionAgentConversationResultCommitter{}
	deps := productionDiscussionAgentDependencies{KernelCheckpoints: checkpoints, Committer: committer}
	registry, recovery := configureProductionRuntimeTaskCommands(&deps, command)
	defer registry.Close()
	if recovery != checkpoints || registry != deps.RuntimeRegistry || committer.commands != command {
		t.Fatal("production recovery state or task commands were disconnected from the runtime")
	}
}
