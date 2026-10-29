package discussionsession

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
)

func TestPostgresRepositorySerializesConcurrentConversationCreation(t *testing.T) {
	rawURL := os.Getenv("ORRERY_TEST_POSTGRES_URL")
	if rawURL == "" {
		t.Skip("ORRERY_TEST_POSTGRES_URL is not set")
	}
	ctx := context.Background()
	conversationID := fmt.Sprintf("conv-concurrent-%d", time.Now().UnixNano())
	registries := make([]*db.Registry, 2)
	repositories := make([]*Repository, 2)
	for index := range registries {
		registry, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
			db.RoleCore: {Driver: db.DriverPostgres, URL: rawURL, Name: fmt.Sprintf("discussion-concurrency-%d", index)},
		}})
		if err != nil {
			t.Fatalf("open postgres registry %d: %v", index, err)
		}
		registries[index] = registry
		repositories[index] = NewRepository(db.NewRouter(registry))
	}
	t.Cleanup(func() {
		_ = repositories[0].DeleteConversation(context.Background(), conversationID)
		for _, registry := range registries {
			_ = registry.Close()
		}
	})

	const workers = 16
	start := make(chan struct{})
	results := make(chan error, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			now := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
			conversation := ConversationRecord{
				ConversationID: conversationID, TenantID: "tenant-concurrent", PurposeKind: "todo", PurposeID: "todo-concurrent",
				Status: "draft", CreatedBy: fmt.Sprintf("user-%d", index), CreatedAt: now, UpdatedAt: now,
			}
			_, err := repositories[index%len(repositories)].CreateConversationWithMembers(ctx, conversation, []ConversationMemberRecord{{
				ConversationID: conversationID, MemberKind: "user", MemberID: fmt.Sprintf("user-%d", index), CreatedAt: now,
			}})
			results <- err
		}(index)
	}
	close(start)
	wait.Wait()
	close(results)

	succeeded := 0
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrRevisionConflict):
		default:
			t.Fatalf("concurrent create error: %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("successful conversation creates = %d, want exactly 1", succeeded)
	}
	loaded, err := repositories[0].GetConversation(ctx, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Revision != 1 || len(loaded.Members) != 1 || loaded.Members[0].MemberID != loaded.CreatedBy {
		t.Fatalf("canonical conversation aggregate = %+v", loaded)
	}
}
