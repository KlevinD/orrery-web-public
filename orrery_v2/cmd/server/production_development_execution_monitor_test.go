package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"orrery_v2/internal/app/developmentproject"
	todoapp "orrery_v2/internal/app/todo"
	"orrery_v2/internal/platform/testkit"
)

func TestProductionDevelopmentExecutionMonitorReturnsRedactedProjectSummary(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := todoapp.NewRepository(env.DBRouter)
	if err := repo.CreateTodo(ctx, todoapp.TodoItem{
		TenantID: "tenant-a", TodoID: "todo-project", DevelopmentProjectID: "project-a", WorkKind: todoapp.TodoWorkKindDevelopment,
		Title: "Visible execution title", Description: "private-description-marker", AcceptanceCriteria: "private-acceptance-marker",
		Status: todoapp.TodoStatusPlanned, AutomationMode: todoapp.TodoAutomationModeSuggest,
		CreatedBy: "owner-login", CreatedBySubjectID: "subject-owner", AssigneeID: "subject-assignee", ReviewerID: "subject-reviewer",
		CreatedAt: env.Clock.Now(), UpdatedAt: env.Clock.Now(), Version: 1,
	}); err != nil {
		t.Fatal(err)
	}
	monitor := productionDevelopmentExecutionMonitor{api: &productionAPI{}, todos: repo}
	project := developmentproject.Project{TenantID: "tenant-a", DevelopmentProjectID: "project-a"}

	for _, role := range []string{developmentproject.MemberRoleViewer, developmentproject.MemberRoleContributor} {
		viewer := developmentproject.ViewerRef{TenantID: "tenant-a", ViewerID: role + "-login", AuthorizationSubjectID: "subject-" + role, ViewerKind: "user"}
		page, err := monitor.ListProjectExecutions(ctx, viewer, project, false, developmentproject.ListExecutionsQuery{})
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("%s summaries = %+v err=%v", role, page, err)
		}
		item := page.Items[0]
		if item.TodoID != "todo-project" || item.TodoTitle != "Visible execution title" || item.CanOpenControl || item.CanOpenDiscussion || item.ConversationID != "" {
			t.Fatalf("%s summary = %+v", role, item)
		}
		payload, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		text := string(payload)
		for _, private := range []string{"private-description-marker", "private-acceptance-marker", "created_by", "reviewer_id"} {
			if strings.Contains(text, private) {
				t.Fatalf("%s summary leaked %q: %s", role, private, text)
			}
		}
	}
}
