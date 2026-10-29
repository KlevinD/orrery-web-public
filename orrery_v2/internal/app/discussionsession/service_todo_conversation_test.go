package discussionsession

import (
	"context"
	"errors"
	"sync"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestTodoConversationReconciliationRevokesReplacedCreator(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{
		Repository: repo,
		AgentDirectory: &testAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
			{AgentProfileID: "lead-agent", AgentInstanceID: "lead-agent", DisplayName: "Lead", AgentKind: agentcore.AgentKindAI, Enabled: true},
			{AgentProfileID: "reviewer-new", AgentInstanceID: "reviewer-new", DisplayName: "New Reviewer", AgentKind: agentcore.AgentKindHuman, Enabled: true},
		}},
		Clock: fixedClock{now: testTime},
	})
	oldReviewer := ViewerRef{ViewerID: "reviewer-old", ViewerKind: ViewerKindUser, DisplayName: "Old Reviewer", TenantID: "tenant-a"}
	conversation, err := service.EnsureTodoConversation(ctx, oldReviewer, EnsureTodoConversationCommand{
		TenantID: "tenant-a", TodoID: "todo-1", Title: "Todo", InitialMemberIDs: []string{"lead-agent"}, ManagedSource: "todo:tenant-a/todo-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if conversation.CreatedBy != oldReviewer.ViewerID {
		t.Fatalf("created_by = %q", conversation.CreatedBy)
	}

	system := ViewerRef{ViewerID: "todo-automation-agent", ViewerKind: ViewerKindSystem, TenantID: "tenant-a"}
	updated, err := service.ReconcileConversationMembers(ctx, system, ReconcileConversationMembersCommand{
		ConversationID: conversation.ConversationID, TargetIDs: []string{"lead-agent", "reviewer-new"}, ManagedSource: "todo:tenant-a/todo-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range updated.Members {
		if member.ID == oldReviewer.ViewerID {
			t.Fatalf("stale creator remained a member: %+v", updated.Members)
		}
	}
	if _, err := service.GetConversation(ctx, oldReviewer, conversation.ConversationID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("old reviewer access error = %v, want ErrForbidden", err)
	}
	if _, err := service.GetConversation(ctx, ViewerRef{ViewerID: "reviewer-new", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}, conversation.ConversationID); err != nil {
		t.Fatalf("new reviewer cannot access canonical conversation: %v", err)
	}
}

func TestTodoConversationEnsureIsIdempotentAcrossServiceWorkers(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	services := []*Service{
		newTestService(ServiceOptions{Repository: repo, Clock: fixedClock{now: testTime}}),
		newTestService(ServiceOptions{Repository: repo, Clock: fixedClock{now: testTime}}),
	}
	viewer := ViewerRef{ViewerID: "todo-owner", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}
	command := EnsureTodoConversationCommand{
		TenantID: "tenant-a", TodoID: "todo-concurrent", Title: "Concurrent Todo", ManagedSource: "todo:tenant-a/todo-concurrent",
	}
	start := make(chan struct{})
	results := make(chan ConversationDTO, len(services))
	errorsFound := make(chan error, len(services))
	var wait sync.WaitGroup
	for _, service := range services {
		wait.Add(1)
		go func(service *Service) {
			defer wait.Done()
			<-start
			conversation, err := service.EnsureTodoConversation(ctx, viewer, command)
			if err != nil {
				errorsFound <- err
				return
			}
			results <- conversation
		}(service)
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatalf("concurrent EnsureTodoConversation: %v", err)
	}
	conversationID := ""
	count := 0
	for conversation := range results {
		count++
		if conversationID == "" {
			conversationID = conversation.ConversationID
		}
		if conversation.ConversationID != conversationID {
			t.Fatalf("concurrent conversations differ: %q != %q", conversation.ConversationID, conversationID)
		}
	}
	if count != len(services) || conversationID == "" {
		t.Fatalf("successful ensures = %d, conversation_id=%q", count, conversationID)
	}
}

func TestTodoConversationCannotBeDeletedOrCascadeItsMissionLinks(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	deleter := &capturingExecutionDeleter{}
	service := newTestService(ServiceOptions{Repository: repo, ExecutionDeleter: deleter, Clock: fixedClock{now: testTime}})
	owner := ViewerRef{ViewerID: "todo-owner", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}
	conversation, err := service.EnsureTodoConversation(ctx, owner, EnsureTodoConversationCommand{
		TenantID: "tenant-a", TodoID: "todo-delete-protected", Title: "Protected Todo",
		ManagedSource: "todo:tenant-a/todo-delete-protected",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveMissionLink(ctx, LinkedMissionRecord{
		ConversationID: conversation.ConversationID, MissionID: "mission-history", Status: "completed", CreatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteConversation(ctx, owner, conversation.ConversationID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("DeleteConversation error = %v, want ErrForbidden", err)
	}
	retained, err := service.GetConversation(ctx, owner, conversation.ConversationID)
	if err != nil {
		t.Fatalf("GetConversation after rejected delete: %v", err)
	}
	if retained.PurposeKind != todoConversationPurposeKind || len(retained.LinkedMissions) != 1 || retained.LinkedMissions[0].MissionID != "mission-history" {
		t.Fatalf("retained Todo conversation = %+v", retained)
	}
	if len(deleter.deletedMissionIDs) != 0 || len(deleter.deletedTasks) != 0 {
		t.Fatalf("Todo conversation delete cascaded executions: missions=%+v tasks=%+v", deleter.deletedMissionIDs, deleter.deletedTasks)
	}
}

func TestTodoConversationSystemAccessIsLimitedToManagedReconciliation(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{Repository: repo, Clock: fixedClock{now: testTime}})
	owner := ViewerRef{ViewerID: "todo-owner", ViewerKind: ViewerKindUser, TenantID: "tenant-a"}
	conversation, err := service.EnsureTodoConversation(ctx, owner, EnsureTodoConversationCommand{
		TenantID: "tenant-a", TodoID: "todo-system-access", ManagedSource: "todo:tenant-a/todo-system-access",
	})
	if err != nil {
		t.Fatal(err)
	}
	system := ViewerRef{ViewerID: "todo-automation-agent", ViewerKind: ViewerKindSystem, TenantID: "tenant-a"}
	if _, err := service.GetConversation(ctx, system, conversation.ConversationID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("system GetConversation error = %v, want ErrForbidden", err)
	}
	if _, err := service.ReconcileConversationMembers(ctx, system, ReconcileConversationMembersCommand{
		ConversationID: conversation.ConversationID, ManagedSource: "todo:tenant-a/todo-system-access",
	}); err != nil {
		t.Fatalf("managed reconciliation failed: %v", err)
	}
	for _, command := range []struct {
		viewer ViewerRef
		source string
	}{
		{viewer: ViewerRef{ViewerID: "todo-automation-agent", ViewerKind: ViewerKindSystem, TenantID: "tenant-b"}, source: "todo:tenant-a/todo-system-access"},
		{viewer: system, source: "todo:tenant-a/another-todo"},
	} {
		if _, err := service.ReconcileConversationMembers(ctx, command.viewer, ReconcileConversationMembersCommand{
			ConversationID: conversation.ConversationID, ManagedSource: command.source,
		}); !errors.Is(err, ErrForbidden) {
			t.Fatalf("unauthorized reconciliation error = %v, want ErrForbidden", err)
		}
	}
}
