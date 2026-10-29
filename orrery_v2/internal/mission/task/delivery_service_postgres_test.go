package task

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/uow"
)

func TestPostgresDeliverySerializesConcurrentIdempotentSubmission(t *testing.T) {
	rawURL := os.Getenv("ORRERY_TEST_POSTGRES_URL")
	if rawURL == "" {
		t.Skip("ORRERY_TEST_POSTGRES_URL is not set")
	}
	ctx := context.Background()
	registries := make([]*db.Registry, 2)
	units := make([]*uow.UnitOfWork, 2)
	repositories := make([]*PlatformRepository, 2)
	services := make([]*DeliveryService, 2)
	for index := range registries {
		registry, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
			db.RoleCore: {Driver: db.DriverPostgres, URL: rawURL, Name: fmt.Sprintf("task-delivery-concurrency-%d", index)},
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
		repositories[index] = NewPlatformRepository()
		services[index] = NewDeliveryService(units[index], repositories[index], nil, DeliveryServiceOptions{
			IDGenerator: idgen.NewRandom(fmt.Sprintf("submission-%d", index)),
		})
	}
	t.Cleanup(func() {
		for _, registry := range registries {
			_ = registry.Close()
		}
	})

	unique := fmt.Sprintf("%d", time.Now().UnixNano())
	taskID := TaskID("task-concurrent-submission-" + unique)
	record := validTaskRecord(TaskStatusInProgress)
	record.Contract.ID = taskID
	record.Contract.MissionID = missioncore.MissionID("mission-concurrent-submission-" + unique)
	if err := units[0].Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		return repositories[0].SaveTask(ctx, tx, record)
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = units[0].Do(context.Background(), func(ctx context.Context, tx uow.Tx) error {
			return repositories[0].DeleteTask(ctx, tx, taskID)
		})
	})

	result := validAgentResultForContract()
	result.TaskID = taskID
	result.MissionID = record.Contract.MissionID
	start := make(chan struct{})
	results := make(chan TaskSubmission, len(services))
	errs := make(chan error, len(services))
	var wait sync.WaitGroup
	for index := range services {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			submission, err := services[index].SubmitAgentResult(ctx, taskID, result, SubmitOptions{IdempotencyKey: "same-submission"})
			results <- submission
			errs <- err
		}(index)
	}
	close(start)
	wait.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent submission: %v", err)
		}
	}
	var first TaskSubmissionID
	for submission := range results {
		if first == "" {
			first = submission.ID
			continue
		}
		if submission.ID != first {
			t.Fatalf("idempotent concurrent submissions returned different ids: %s and %s", first, submission.ID)
		}
	}
}

func TestPostgresQASerializesConcurrentIdempotentDecision(t *testing.T) {
	rawURL := os.Getenv("ORRERY_TEST_POSTGRES_URL")
	if rawURL == "" {
		t.Skip("ORRERY_TEST_POSTGRES_URL is not set")
	}
	ctx := context.Background()
	registries := make([]*db.Registry, 2)
	units := make([]*uow.UnitOfWork, 2)
	repositories := make([]*PlatformRepository, 2)
	services := make([]*QAService, 2)
	for index := range registries {
		registry, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
			db.RoleCore: {Driver: db.DriverPostgres, URL: rawURL, Name: fmt.Sprintf("task-qa-concurrency-%d", index)},
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
		repositories[index] = NewPlatformRepository()
		services[index] = NewQAService(units[index], repositories[index], nil, QAServiceOptions{
			IDGenerator: idgen.NewRandom(fmt.Sprintf("qa-result-%d", index)),
			Policy:      QAPolicy{EvidenceStrictness: EvidenceStrictnessRequired, RequireAcceptanceReport: true},
		})
	}
	t.Cleanup(func() {
		for _, registry := range registries {
			_ = registry.Close()
		}
	})

	unique := fmt.Sprintf("%d", time.Now().UnixNano())
	taskID := TaskID("task-concurrent-qa-" + unique)
	record := validTaskRecord(TaskStatusInReview)
	record.Contract.ID = taskID
	record.Contract.MissionID = missioncore.MissionID("mission-concurrent-qa-" + unique)
	submission := validSubmission()
	submission.ID = TaskSubmissionID("submission-concurrent-qa-" + unique)
	submission.TaskID = taskID
	evidence := validEvidenceSubmission()
	evidence.ID = EvidenceSubmissionID("evidence-concurrent-qa-" + unique)
	evidence.TaskID = taskID
	evidence.SubmissionID = submission.ID
	if err := units[0].Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if err := repositories[0].SaveTask(ctx, tx, record); err != nil {
			return err
		}
		return repositories[0].SaveSubmission(ctx, tx, submission, nil, []EvidenceSubmission{evidence})
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = units[0].Do(context.Background(), func(ctx context.Context, tx uow.Tx) error {
			return repositories[0].DeleteTask(ctx, tx, taskID)
		})
	})

	decision := QAResultRecord{
		TaskID: taskID, SubmissionID: string(submission.ID), CheckedBy: "qa-agent", Status: QAStatusApproved,
		CheckedCriteria: []CheckedCriterion{{CriterionID: "acceptance-001", Status: CheckStatusPassed}},
		EvidenceResults: []EvidenceCheckResult{{RequirementID: "evidence-required-001", EvidenceID: evidence.Evidence.ID, Status: CheckStatusPassed}},
	}
	start := make(chan struct{})
	results := make(chan QAResultRecord, len(services))
	errs := make(chan error, len(services))
	var wait sync.WaitGroup
	for index := range services {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			result, err := services[index].SubmitQAResult(ctx, taskID, decision, QASubmitOptions{IdempotencyKey: "same-qa-decision"})
			results <- result
			errs <- err
		}(index)
	}
	close(start)
	wait.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent QA decision: %v", err)
		}
	}
	var first QAResultID
	for result := range results {
		if first == "" {
			first = result.ID
			continue
		}
		if result.ID != first {
			t.Fatalf("idempotent concurrent QA decisions returned different ids: %s and %s", first, result.ID)
		}
	}
}
