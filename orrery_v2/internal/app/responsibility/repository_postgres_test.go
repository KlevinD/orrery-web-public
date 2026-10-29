package responsibility

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
)

func TestPostgresRepositorySerializesConcurrentReactivation(t *testing.T) {
	rawURL := os.Getenv("ORRERY_TEST_POSTGRES_URL")
	if rawURL == "" {
		t.Skip("ORRERY_TEST_POSTGRES_URL is not set")
	}
	ctx := context.Background()
	registries := make([]*db.Registry, 2)
	units := make([]*uow.UnitOfWork, 2)
	repositories := make([]*Repository, 2)
	for index := range registries {
		registry, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
			db.RoleCore: {Driver: db.DriverPostgres, URL: rawURL, Name: fmt.Sprintf("responsibility-concurrency-%d", index)},
		}})
		if err != nil {
			t.Fatalf("open postgres registry %d: %v", index, err)
		}
		store, err := registry.Store(db.RoleCore)
		if err != nil {
			_ = registry.Close()
			t.Fatalf("open core store %d: %v", index, err)
		}
		registries[index] = registry
		units[index] = uow.New(store)
		repositories[index] = NewRepository(db.NewRouter(registry), units[index])
	}
	t.Cleanup(func() {
		for _, registry := range registries {
			_ = registry.Close()
		}
	})

	unique := fmt.Sprintf("%d", time.Now().UnixNano())
	tenantID := "tenant-reactivation-" + unique
	taskID := "task-reactivation-" + unique
	assignmentID := AssignmentID(tenantID, WorkKindMissionTask, taskID)
	t.Cleanup(func() {
		_ = repositories[0].router.DeleteRecord(context.Background(), "core."+assignmentTable, assignmentID)
		for _, candidate := range []struct {
			cause   ActivationCause
			version int64
		}{
			{cause: ActivationCauseAssigned, version: 1},
			{cause: ActivationCauseDependencyResolved, version: 2},
			{cause: ActivationCauseDependencyResolved, version: 3},
			{cause: ActivationCauseBlockerResolved, version: 2},
			{cause: ActivationCauseBlockerResolved, version: 3},
		} {
			dedupeKey := ActivationDedupeKey(tenantID, assignmentID, candidate.cause, candidate.version)
			_ = repositories[0].router.DeleteRecord(context.Background(), "core."+activationTable, ActivationID(dedupeKey))
		}
	})
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	if err := units[0].Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		_, _, err := repositories[0].AssignMissionTaskTx(ctx, tx, MissionTaskAssignmentInput{
			TenantID: tenantID, MissionID: "mission-" + unique, TaskID: taskID, AgentProfileID: "agent-1",
			Priority: 50, AcceptBy: now.Add(time.Hour), ActivationCause: ActivationCauseAssigned,
		}, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	causes := []ActivationCause{ActivationCauseDependencyResolved, ActivationCauseBlockerResolved}
	start := make(chan struct{})
	errs := make(chan error, len(causes))
	var wait sync.WaitGroup
	for index, cause := range causes {
		wait.Add(1)
		go func(repo *Repository, cause ActivationCause) {
			defer wait.Done()
			<-start
			_, err := repo.Reactivate(ctx, assignmentID, cause, now.Add(time.Minute), now.Add(time.Minute))
			errs <- err
		}(repositories[index], cause)
	}
	close(start)
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent reactivation: %v", err)
		}
	}
	assignment, err := repositories[0].GetAssignment(ctx, assignmentID)
	if err != nil {
		t.Fatal(err)
	}
	if assignment.Version != 3 {
		t.Fatalf("assignment version = %d, want 3", assignment.Version)
	}
	ready, err := repositories[0].ListReadyActivations(ctx, now.Add(2*time.Minute), 20)
	if err != nil {
		t.Fatal(err)
	}
	current := 0
	for _, activation := range ready {
		if activation.ResponsibilityID == assignmentID && activation.AssignmentVersion == assignment.Version {
			current++
		}
	}
	if current != 1 {
		t.Fatalf("current ready activations = %d, want 1", current)
	}
}
