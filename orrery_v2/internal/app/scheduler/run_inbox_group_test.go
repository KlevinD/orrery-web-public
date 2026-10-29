package scheduler

import (
	"context"
	"fmt"
	"orrery_v2/internal/platform/testkit"
	"testing"
	"time"
)

func TestRunInboxGroupsBeforeFiltersAndOmitsArtifactChecks(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	service := newServiceForTestWithRepository(repo)
	artifacts := &availabilitySchedulerArtifactStore{}
	service.artifactStore = artifacts
	viewer := ViewerRef{TenantID: "tenant-a", ViewerID: "viewer-a", ViewerKind: ViewerKindHuman}
	task := schedulerTestTask("daily", schedulerTestTime)
	task.TenantID = viewer.TenantID
	if err := repo.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 120; i++ {
		run := schedulerTestRun(fmt.Sprintf("old-%03d", i), "daily", RunStatusFailed, schedulerTestTime.Add(time.Duration(i-121)*time.Minute))
		run.TenantID = viewer.TenantID
		if err := repo.SaveRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	latest := schedulerTestRun("latest", "daily", RunStatusCompleted, schedulerTestTime)
	latest.TenantID = viewer.TenantID
	latest.Result = RunResult{Summary: "Recovered", Artifacts: []RunArtifact{{ID: "report", Kind: "markdown", URI: "file:///report.md"}}}
	latest.ConfigurationSnapshot = &task
	other := schedulerTestRun("other", "weekly", RunStatusFailed, schedulerTestTime.Add(-time.Minute))
	other.TenantID = viewer.TenantID
	private := schedulerTestRun("private", "private", RunStatusFailed, schedulerTestTime.Add(time.Minute))
	private.TenantID = "tenant-b"
	internal := schedulerTestRun("internal", "maintenance", RunStatusCompleted, schedulerTestTime.Add(time.Minute))
	internal.TenantID = viewer.TenantID
	internal.TargetMode = TargetModeResponsibilityReconcile
	for _, run := range []ScheduledRun{latest, other, private, internal} {
		if err := repo.SaveRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.SetRunReadState(ctx, viewer, SetRunReadStateRequest{ScheduledRunID: latest.ScheduledRunID, Read: true}); err != nil {
		t.Fatal(err)
	}
	page, err := service.ListRunInbox(ctx, viewer, ListRunInboxQuery{GroupByTask: true, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || page.UnreadTotal != 1 || page.AttentionTotal != 1 || len(page.Items) != 1 {
		t.Fatalf("group totals = %+v", page)
	}
	item := page.Items[0]
	if item.Run.ScheduledRunID != "latest" || !item.IsRead || item.Run.Result.Summary != "Recovered" || len(item.Run.Result.Artifacts) != 0 || item.Run.ConfigurationSnapshot != nil || len(artifacts.accesses) != 0 {
		t.Fatalf("not a lightweight latest summary: %+v; accesses=%d", item, len(artifacts.accesses))
	}
	page, err = service.ListRunInbox(ctx, viewer, ListRunInboxQuery{GroupByTask: true, Status: RunStatusFailed})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].Run.ScheduledRunID != "other" {
		t.Fatalf("historical failure resurfaced: %+v, %v", page, err)
	}
	page, err = service.ListRunInbox(ctx, viewer, ListRunInboxQuery{GroupByTask: true, Limit: 1, Offset: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].Run.ScheduledRunID != "other" {
		t.Fatalf("page two: %+v, %v", page, err)
	}
	viewer.ViewerID = "viewer-b"
	page, err = service.ListRunInbox(ctx, viewer, ListRunInboxQuery{GroupByTask: true})
	if err != nil || page.UnreadTotal != 2 {
		t.Fatalf("viewer isolation: %+v, %v", page, err)
	}
	page, err = service.ListRunInbox(ctx, viewer, ListRunInboxQuery{ScheduledTaskID: "daily", Limit: 20})
	if err != nil || page.Total != 121 || len(page.Items[0].Run.Result.Artifacts) != 1 || len(artifacts.accesses) != 1 {
		t.Fatalf("full history contract changed: %+v, %v", page, err)
	}
}
