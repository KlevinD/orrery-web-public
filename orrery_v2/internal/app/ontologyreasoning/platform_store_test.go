package ontologyreasoning

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestPlatformStorePersistsReasoningRun(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "ontology-reasoning-platform-store"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner([]migration.Migration{ontologyReasoningRunsMigration()}).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	now := time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC)
	run := ReasoningRun{
		ID:                     "run-1",
		TenantID:               "tenant-1",
		Engine:                 "test",
		Model:                  "structured-candidates",
		ModelVersion:           "v1",
		IntentHash:             "sha256:intent",
		InputHash:              "sha256:input",
		PromptHash:             "sha256:prompt",
		ContextPackHash:        "sha256:context",
		Status:                 RunStatusProposed,
		CandidateCount:         1,
		ValidCandidateCount:    1,
		RejectedCandidateCount: 0,
		ReviewRequiredCount:    0,
		Candidates: []Candidate{{
			ID:        "candidate-1",
			Kind:      CandidateLink,
			Operation: OperationUpsert,
			LinkType:  "person_member_of_team",
		}},
		Validation: []ValidationResult{{CandidateID: "candidate-1", Status: ValidationAccepted}},
		Proposals: []ReasoningProposal{{
			ID:               "proposal-1",
			TenantID:         "tenant-1",
			RunID:            "run-1",
			CandidateID:      "candidate-1",
			Kind:             CandidateLink,
			Status:           ProposalStatusProposed,
			ValidationStatus: ValidationAccepted,
			CreatedAt:        now,
		}},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := NewPlatformStore(store).SaveRun(ctx, run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}

	reopened := NewPlatformStore(store)
	got, err := reopened.GetRun(ctx, "tenant-1", "run-1")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.ID != run.ID || got.TenantID != "tenant-1" || got.Status != RunStatusProposed || len(got.Candidates) != 1 || len(got.Validation) != 1 || len(got.Proposals) != 1 {
		t.Fatalf("restored run mismatch: %+v", got)
	}
	if !got.CreatedAt.Equal(now) || !got.UpdatedAt.Equal(now) {
		t.Fatalf("timestamps mismatch: created=%s updated=%s", got.CreatedAt, got.UpdatedAt)
	}
	if _, err := reopened.GetRun(ctx, "tenant-2", "run-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for cross-tenant get, got %v", err)
	}
}

func TestPlatformStoreListsRunByTenant(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "ontology-reasoning-platform-list"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := migration.NewRunner([]migration.Migration{ontologyReasoningRunsMigration()}).Up(ctx, store); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	platformStore := NewPlatformStore(store)
	now := time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC)
	for _, run := range []ReasoningRun{
		{ID: "run-1", TenantID: "tenant-1", Status: RunStatusDraft, CreatedAt: now, UpdatedAt: now},
		{ID: "run-2", TenantID: "tenant-2", Status: RunStatusDraft, CreatedAt: now, UpdatedAt: now},
	} {
		if err := platformStore.SaveRun(ctx, run); err != nil {
			t.Fatalf("SaveRun(%s): %v", run.ID, err)
		}
	}

	runs, err := platformStore.ListRuns(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != "run-1" {
		t.Fatalf("expected tenant-1 run only, got %+v", runs)
	}
}
