package scheduler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"orrery_v2/internal/platform/testkit"
)

func TestServiceOpenRunArtifactAuthorizesTaskRunAndArtifact(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	store := &recordingSchedulerArtifactStore{content: RunArtifactContent{
		Reader: io.NopCloser(strings.NewReader("report")), FileName: "report.md", ContentType: "text/markdown", SizeBytes: 6,
	}}
	service := newServiceForTestWithRepository(repo)
	service.artifactStore = store
	ctx := context.Background()
	task := schedulerTestTask("sched-artifact", schedulerTestTime)
	task.TenantID = "tenant-a"
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	artifact := RunArtifact{ID: "artifact-1", Kind: "report", URI: "/workspace/report.md", SizeBytes: 6}
	run := schedulerTestRun("run-artifact", task.ScheduledTaskID, RunStatusCompleted, schedulerTestTime)
	run.Result.Artifacts = []RunArtifact{artifact}
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}

	content, err := service.OpenRunArtifact(ctx, OpenRunArtifactQuery{
		TenantID: "tenant-a", ScheduledTaskID: task.ScheduledTaskID, ScheduledRunID: run.ScheduledRunID,
		ArtifactID: PublicRunArtifactID(run.ScheduledRunID, 0, artifact),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer content.Reader.Close()
	if store.opened.Artifact.ID != artifact.ID || store.opened.Task.ScheduledTaskID != task.ScheduledTaskID || store.opened.Run.ScheduledRunID != run.ScheduledRunID || content.FileName != "report.md" {
		t.Fatalf("opened=%+v content=%+v", store.opened, content)
	}
	if _, err := service.OpenRunArtifact(ctx, OpenRunArtifactQuery{
		TenantID: "tenant-a", ScheduledTaskID: task.ScheduledTaskID, ScheduledRunID: run.ScheduledRunID, ArtifactID: artifact.ID,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("raw internal artifact ID error = %v, want ErrNotFound", err)
	}
}

func TestServiceListRunsUsesStoreArtifactAvailability(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	store := &availabilitySchedulerArtifactStore{available: map[string]bool{"artifact-ready": true}}
	service := newServiceForTestWithRepository(repo)
	service.artifactStore = store
	ctx := context.Background()
	task := schedulerTestTask("sched-availability", schedulerTestTime)
	task.TenantID = "tenant-a"
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	run := schedulerTestRun("run-availability", task.ScheduledTaskID, RunStatusCompleted, schedulerTestTime)
	run.Result.Artifacts = []RunArtifact{
		{ID: "artifact-ready", URI: "artifact://scheduled-runs/v1/ready"},
		{ID: "artifact-missing", URI: "artifact://scheduled-runs/v1/missing", ContentAvailable: true},
	}
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}

	page, err := service.ListRuns(ctx, ListRunsQuery{TenantID: "tenant-a", ScheduledTaskID: task.ScheduledTaskID})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Runs) != 1 || len(page.Runs[0].Result.Artifacts) != 2 {
		t.Fatalf("page = %+v", page)
	}
	artifacts := page.Runs[0].Result.Artifacts
	if !artifacts[0].ContentAvailable || artifacts[1].ContentAvailable || !artifacts[0].ContentAvailabilityChecked || !artifacts[1].ContentAvailabilityChecked {
		t.Fatalf("availability = %+v", artifacts)
	}
	if len(store.accesses) != 2 || store.accesses[0].Task.ScheduledTaskID != task.ScheduledTaskID || store.accesses[0].Run.ScheduledRunID != run.ScheduledRunID {
		t.Fatalf("availability accesses = %+v", store.accesses)
	}
}

func TestServiceListRunsBoundsArtifactAvailabilityChecks(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	store := &availabilitySchedulerArtifactStore{available: map[string]bool{}}
	service := newServiceForTestWithRepository(repo)
	service.artifactStore = store
	ctx := context.Background()
	task := schedulerTestTask("sched-availability-budget", schedulerTestTime)
	task.TenantID = "tenant-a"
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	run := schedulerTestRun("run-availability-budget", task.ScheduledTaskID, RunStatusCompleted, schedulerTestTime)
	for index := 0; index < maxScheduledArtifactAvailabilityChecks+1; index++ {
		id := fmt.Sprintf("artifact-%03d", index)
		store.available[id] = true
		run.Result.Artifacts = append(run.Result.Artifacts, RunArtifact{
			ID: id, URI: "artifact://scheduled-runs/v1/" + id, ContentAvailable: true,
		})
	}
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}

	page, err := service.ListRuns(ctx, ListRunsQuery{TenantID: "tenant-a", ScheduledTaskID: task.ScheduledTaskID})
	if err != nil {
		t.Fatal(err)
	}
	artifacts := page.Runs[0].Result.Artifacts
	if len(store.accesses) != maxScheduledArtifactAvailabilityChecks {
		t.Fatalf("availability checks = %d, want %d", len(store.accesses), maxScheduledArtifactAvailabilityChecks)
	}
	if !artifacts[maxScheduledArtifactAvailabilityChecks-1].ContentAvailable || !artifacts[maxScheduledArtifactAvailabilityChecks-1].ContentAvailabilityChecked ||
		artifacts[maxScheduledArtifactAvailabilityChecks].ContentAvailable || artifacts[maxScheduledArtifactAvailabilityChecks].ContentAvailabilityChecked {
		t.Fatalf("availability budget boundary = %+v", artifacts[maxScheduledArtifactAvailabilityChecks-1:])
	}
}

func TestServiceOpenRunArtifactArchivesHistoricalArtifactAndPersistsReplacement(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	ctx := context.Background()
	task := schedulerTestTask("sched-artifact-migration", schedulerTestTime)
	task.TenantID = "system"
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	historical := RunArtifact{
		ID: "scheduled-artifact:runtime-1:report", Kind: "report", URI: "/workspace/report.md",
		FileName: "report.md", SizeBytes: 6, EvidenceID: "evidence-1",
	}
	archived := historical
	archived.URI = "artifact://scheduled-runs/v1/tenant/run/artifact/6-digest/report.md"
	archived.ContentType = "text/markdown; charset=utf-8"
	archived.SHA256 = strings.Repeat("a", 64)
	store := &migratingSchedulerArtifactStore{historical: historical, archived: archived}
	service.artifactStore = store
	run := schedulerTestRun("run-artifact-migration", task.ScheduledTaskID, RunStatusCompleted, schedulerTestTime)
	run.Result.Artifacts = []RunArtifact{historical}
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	artifactID := PublicRunArtifactID(run.ScheduledRunID, 0, historical)

	content, err := service.OpenRunArtifact(ctx, OpenRunArtifactQuery{
		TenantID: "system", ScheduledTaskID: task.ScheduledTaskID, ScheduledRunID: run.ScheduledRunID, ArtifactID: artifactID,
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(content.Reader)
	content.Reader.Close()
	if err != nil || string(body) != "report" {
		t.Fatalf("body=%q error=%v", body, err)
	}
	persisted, err := repo.GetRun(ctx, run.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Result.Artifacts) != 1 || persisted.Result.Artifacts[0] != archived {
		t.Fatalf("persisted artifact = %+v", persisted.Result.Artifacts)
	}
	if store.archiveCalls != 1 || store.openCalls != 2 {
		t.Fatalf("archive calls=%d open calls=%d", store.archiveCalls, store.openCalls)
	}

	content, err = service.OpenRunArtifact(ctx, OpenRunArtifactQuery{
		TenantID: "system", ScheduledTaskID: task.ScheduledTaskID, ScheduledRunID: run.ScheduledRunID, ArtifactID: artifactID,
	})
	if err != nil {
		t.Fatal(err)
	}
	content.Reader.Close()
	if store.archiveCalls != 1 || store.openCalls != 3 {
		t.Fatalf("repeat archive calls=%d open calls=%d", store.archiveCalls, store.openCalls)
	}
}

func TestServiceOpenRunArtifactRejectsInvalidHistoricalReplacement(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	ctx := context.Background()
	task := schedulerTestTask("sched-artifact-invalid-migration", schedulerTestTime)
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	historical := RunArtifact{ID: "artifact-1", Kind: "report", URI: "/workspace/report.md", FileName: "report.md", SizeBytes: 6}
	invalid := historical
	invalid.ID = "artifact-other"
	invalid.URI = "artifact://scheduled-runs/v1/other"
	invalid.SHA256 = strings.Repeat("b", 64)
	service.artifactStore = &migratingSchedulerArtifactStore{historical: historical, archived: invalid}
	run := schedulerTestRun("run-artifact-invalid-migration", task.ScheduledTaskID, RunStatusCompleted, schedulerTestTime)
	run.Result.Artifacts = []RunArtifact{historical}
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}

	_, err := service.OpenRunArtifact(ctx, OpenRunArtifactQuery{
		TenantID: task.TenantID, ScheduledTaskID: task.ScheduledTaskID, ScheduledRunID: run.ScheduledRunID,
		ArtifactID: PublicRunArtifactID(run.ScheduledRunID, 0, historical),
	})
	if !errors.Is(err, ErrServiceDependency) {
		t.Fatalf("invalid replacement error = %v, want ErrServiceDependency", err)
	}
	persisted, getErr := repo.GetRun(ctx, run.ScheduledRunID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if persisted.Result.Artifacts[0] != historical {
		t.Fatalf("historical artifact was mutated: %+v", persisted.Result.Artifacts[0])
	}
}

func TestServiceOpenRunArtifactRejectsConcurrentHistoricalMetadataChange(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	ctx := context.Background()
	task := schedulerTestTask("sched-artifact-concurrent-migration", schedulerTestTime)
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	historical := RunArtifact{ID: "artifact-1", Kind: "report", URI: "/workspace/report.md", FileName: "report.md", SizeBytes: 6}
	archived := historical
	archived.URI = "artifact://scheduled-runs/v1/tenant/run/artifact/6-digest/report.md"
	archived.SHA256 = strings.Repeat("c", 64)
	run := schedulerTestRun("run-artifact-concurrent-migration", task.ScheduledTaskID, RunStatusCompleted, schedulerTestTime)
	run.Result.Artifacts = []RunArtifact{historical}
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	store := &migratingSchedulerArtifactStore{historical: historical, archived: archived}
	store.onArchive = func() {
		_, err := repo.MutateRun(ctx, run.ScheduledRunID, func(current *ScheduledRun) error {
			current.Result.Artifacts[0].URI = "/workspace/changed.md"
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	service.artifactStore = store

	_, err := service.OpenRunArtifact(ctx, OpenRunArtifactQuery{
		TenantID: task.TenantID, ScheduledTaskID: task.ScheduledTaskID, ScheduledRunID: run.ScheduledRunID,
		ArtifactID: PublicRunArtifactID(run.ScheduledRunID, 0, historical),
	})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("concurrent migration error = %v, want ErrRevisionConflict", err)
	}
	persisted, getErr := repo.GetRun(ctx, run.ScheduledRunID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if persisted.Result.Artifacts[0].URI != "/workspace/changed.md" {
		t.Fatalf("concurrent metadata was overwritten: %+v", persisted.Result.Artifacts[0])
	}
}

func TestServiceOpenRunArtifactRejectsMismatchedOwnership(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	store := &recordingSchedulerArtifactStore{}
	service := newServiceForTestWithRepository(repo)
	service.artifactStore = store
	ctx := context.Background()
	taskA := schedulerTestTask("sched-a", schedulerTestTime)
	taskA.TenantID = "tenant-a"
	taskB := schedulerTestTask("sched-b", schedulerTestTime)
	taskB.TenantID = "tenant-a"
	for _, task := range []ScheduledTask{taskA, taskB} {
		if err := repo.SaveTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	run := schedulerTestRun("run-a", taskA.ScheduledTaskID, RunStatusCompleted, schedulerTestTime)
	run.Result.Artifacts = []RunArtifact{{ID: "artifact-a", URI: "/workspace/a.md"}}
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}

	tests := []OpenRunArtifactQuery{
		{TenantID: "tenant-b", ScheduledTaskID: taskA.ScheduledTaskID, ScheduledRunID: run.ScheduledRunID, ArtifactID: PublicRunArtifactID(run.ScheduledRunID, 0, run.Result.Artifacts[0])},
		{TenantID: "tenant-a", ScheduledTaskID: taskB.ScheduledTaskID, ScheduledRunID: run.ScheduledRunID, ArtifactID: PublicRunArtifactID(run.ScheduledRunID, 0, run.Result.Artifacts[0])},
		{TenantID: "tenant-a", ScheduledTaskID: taskA.ScheduledTaskID, ScheduledRunID: run.ScheduledRunID, ArtifactID: "artifact-missing"},
		{TenantID: "tenant-a", ScheduledTaskID: taskA.ScheduledTaskID, ScheduledRunID: "run-missing", ArtifactID: PublicRunArtifactID(run.ScheduledRunID, 0, run.Result.Artifacts[0])},
	}
	for _, query := range tests {
		if _, err := service.OpenRunArtifact(ctx, query); !errors.Is(err, ErrNotFound) {
			t.Fatalf("query=%+v error=%v, want ErrNotFound", query, err)
		}
	}
	if store.calls != 0 {
		t.Fatalf("artifact store calls = %d, want 0", store.calls)
	}
}

type recordingSchedulerArtifactStore struct {
	opened  RunArtifactAccess
	content RunArtifactContent
	err     error
	calls   int
}

type availabilitySchedulerArtifactStore struct {
	available map[string]bool
	accesses  []RunArtifactAccess
}

func (*availabilitySchedulerArtifactStore) OpenArtifact(context.Context, RunArtifactAccess) (RunArtifactContent, error) {
	return RunArtifactContent{}, ErrNotFound
}

func (s *availabilitySchedulerArtifactStore) ArtifactAvailable(_ context.Context, access RunArtifactAccess) bool {
	s.accesses = append(s.accesses, access)
	return s.available[access.Artifact.ID]
}

type migratingSchedulerArtifactStore struct {
	historical   RunArtifact
	archived     RunArtifact
	openCalls    int
	archiveCalls int
	onArchive    func()
}

func (s *migratingSchedulerArtifactStore) OpenArtifact(_ context.Context, access RunArtifactAccess) (RunArtifactContent, error) {
	s.openCalls++
	if access.Artifact == s.historical {
		return RunArtifactContent{}, ErrNotFound
	}
	if access.Artifact != s.archived {
		return RunArtifactContent{}, ErrNotFound
	}
	return RunArtifactContent{
		Reader: io.NopCloser(strings.NewReader("report")), FileName: "report.md",
		ContentType: "text/markdown; charset=utf-8", SizeBytes: 6, SHA256: s.archived.SHA256,
	}, nil
}

func (s *migratingSchedulerArtifactStore) ArchiveHistoricalArtifact(_ context.Context, access RunArtifactAccess) (RunArtifact, error) {
	s.archiveCalls++
	if access.Artifact != s.historical {
		return RunArtifact{}, ErrNotFound
	}
	if s.onArchive != nil {
		s.onArchive()
	}
	return s.archived, nil
}

func (s *recordingSchedulerArtifactStore) OpenArtifact(_ context.Context, artifact RunArtifactAccess) (RunArtifactContent, error) {
	s.calls++
	s.opened = artifact
	if s.err != nil {
		return RunArtifactContent{}, s.err
	}
	return s.content, nil
}
