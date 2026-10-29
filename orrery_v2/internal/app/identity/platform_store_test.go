package identity

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestPlatformSubjectStorePersistsAndUpdatesIdentitySubjects(t *testing.T) {
	ctx := context.Background()
	sqlStore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "identity-subject-platform-store"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer sqlStore.Close()
	if err := migration.NewRunner([]migration.Migration{identitySubjectsMigration()}).Up(ctx, sqlStore); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	store := NewPlatformSubjectStore(sqlStore)
	createdAt := time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)
	loginAt := createdAt.Add(time.Hour)
	store.now = func() time.Time { return createdAt.Add(2 * time.Hour) }
	if err := store.SaveSubject(ctx, IdentitySubject{
		ID:             "kratos:subject-1",
		Provider:       ProviderKratos,
		Subject:        "subject-1",
		UserID:         "human-1",
		Username:       "operator",
		AgentProfileID: "human-operator",
		Status:         SubjectStatusActive,
		LastLoginAt:    loginAt,
		CreatedAt:      createdAt,
	}); err != nil {
		t.Fatalf("SaveSubject: %v", err)
	}

	loaded, err := store.GetSubject(ctx, ProviderKratos, "subject-1")
	if err != nil {
		t.Fatalf("GetSubject: %v", err)
	}
	if loaded.ID != "kratos:subject-1" || loaded.Username != "operator" || loaded.AgentProfileID != "human-operator" || !loaded.LastLoginAt.Equal(loginAt) {
		t.Fatalf("loaded subject = %+v", loaded)
	}
	loadedByID, err := store.GetSubjectByID(ctx, "kratos:subject-1")
	if err != nil {
		t.Fatalf("GetSubjectByID: %v", err)
	}
	if loadedByID.Subject != "subject-1" {
		t.Fatalf("loaded by id = %+v", loadedByID)
	}

	loadedByAgent, err := store.GetSubjectByAgentProfileID(ctx, ProviderKratos, "human-operator")
	if err != nil {
		t.Fatalf("GetSubjectByAgentProfileID: %v", err)
	}
	if loadedByAgent.Subject != "subject-1" {
		t.Fatalf("loaded by agent = %+v", loadedByAgent)
	}

	loaded.Email = "operator@example.com"
	store.now = func() time.Time { return createdAt.Add(3 * time.Hour) }
	if err := store.SaveSubject(ctx, loaded); err != nil {
		t.Fatalf("SaveSubject update: %v", err)
	}
	updated, err := store.GetSubject(ctx, ProviderKratos, "subject-1")
	if err != nil {
		t.Fatalf("GetSubject updated: %v", err)
	}
	if updated.Email != "operator@example.com" || updated.CreatedAt.IsZero() || !updated.UpdatedAt.After(updated.CreatedAt) {
		t.Fatalf("updated subject = %+v", updated)
	}
	page, err := store.ListSubjects(ctx, SubjectQuery{Provider: ProviderKratos, AgentProfileID: "human-operator"})
	if err != nil {
		t.Fatalf("ListSubjects: %v", err)
	}
	if page.Total != 1 || page.Subjects[0].Email != "operator@example.com" {
		t.Fatalf("subject page = %+v", page)
	}

	if _, err := store.GetSubject(ctx, ProviderKratos, "missing"); !errors.Is(err, ErrSubjectNotFound) {
		t.Fatalf("missing subject error = %v, want ErrSubjectNotFound", err)
	}
}
