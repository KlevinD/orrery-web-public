package memoryontology

import (
	"context"
	"fmt"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestPlatformStoreClaimsRunOnceAndPersistsMentions(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "memory-ontology-store"})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	if err := migration.NewRunner([]migration.Migration{bridgeMigration()}).Up(ctx, database); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	store := NewPlatformStore(database)
	now := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
	key := RunKey{TenantID: "tenant-1", MemoryID: "memory-1", ContentHash: "hash-1", ExtractorVersion: ExtractorVersion}
	run, claimed, err := store.ClaimRun(ctx, key, "gpt-5.6-luna", now)
	if err != nil || !claimed || run.Attempts != 1 || run.Status != RunRunning {
		t.Fatalf("first claim = %+v, %t, %v", run, claimed, err)
	}
	if _, claimed, err = store.ClaimRun(ctx, key, "gpt-5.6-luna", now.Add(time.Minute)); err != nil || claimed {
		t.Fatalf("duplicate claim = %t, %v", claimed, err)
	}
	projections := []MentionProjection{{
		Index:      0,
		Mention:    Mention{Surface: "Orrery", ObjectType: "project", Confidence: 0.99},
		Resolution: ResolutionResolved, ResolvedObjectID: "project-1",
		LinkType: "memory_mentions_project", EvidenceID: "evidence-1", LinkID: "link-1",
	}}
	if err := store.ReplaceMentions(ctx, key, projections, now); err != nil {
		t.Fatalf("ReplaceMentions: %v", err)
	}
	run.Status, run.CompletedAt, run.UpdatedAt = RunSucceeded, now, now
	if err := store.SaveRun(ctx, run); err != nil {
		t.Fatalf("SaveRun: %v", err)
	}
	if got, exists, err := store.GetRun(ctx, key); err != nil || !exists || got.Status != RunSucceeded || got.ModelID != "gpt-5.6-luna" {
		t.Fatalf("GetRun = %+v, %t, %v", got, exists, err)
	}
}

func TestPlatformStoreRecoversCurrentAndSupersededSourceProjectionFailures(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	if err := migration.NewRunner([]migration.Migration{bridgeMigration()}).Up(ctx, database); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	store := NewPlatformStore(database)
	now := time.Date(2026, 8, 22, 13, 0, 0, 0, time.UTC)
	sourceKey := RunKey{TenantID: "tenant-1", MemoryID: "memory-source", ContentHash: "hash-source", ExtractorVersion: ExtractorVersion}
	obsoleteKey := RunKey{TenantID: "tenant-1", MemoryID: "memory-obsolete", ContentHash: "hash-obsolete", ExtractorVersion: ExtractorVersion}
	llmKey := RunKey{TenantID: "tenant-1", MemoryID: "memory-llm", ContentHash: "hash-llm", ExtractorVersion: ExtractorVersion}
	if err := store.SaveRun(ctx, Run{
		RunKey: sourceKey, Status: RunFailed, Attempts: MaximumAttempts,
		LastError: "memory ontology source object memory-source is not projected", UpdatedAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRun(ctx, Run{
		RunKey: obsoleteKey, Status: RunPending, Attempts: 0, UpdatedAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRun(ctx, Run{
		RunKey: llmKey, Status: RunFailed, Attempts: MaximumAttempts,
		LastError: "llm returned status 524", UpdatedAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	report, err := store.RecoverSourceProjectionFailures(ctx, []RunKey{sourceKey}, now)
	if err != nil || report.Reset != 1 || report.Superseded != 1 {
		t.Fatalf("RecoverSourceProjectionFailures = %+v, %v", report, err)
	}
	sourceRun, _, err := store.GetRun(ctx, sourceKey)
	if err != nil || sourceRun.Status != RunPending || sourceRun.Attempts != 0 || sourceRun.LastError != "" {
		t.Fatalf("source run = %+v, %v", sourceRun, err)
	}
	obsoleteRun, _, err := store.GetRun(ctx, obsoleteKey)
	if err != nil || obsoleteRun.Status != RunSuperseded || obsoleteRun.LastError != "" {
		t.Fatalf("obsolete run = %+v, %v", obsoleteRun, err)
	}
	llmRun, _, err := store.GetRun(ctx, llmKey)
	if err != nil || llmRun.Status != RunFailed || llmRun.Attempts != MaximumAttempts || llmRun.LastError != "llm returned status 524" {
		t.Fatalf("llm run = %+v, %v", llmRun, err)
	}
}

func TestFailExhaustedStaleRunsDoesNotRetry(t *testing.T) {
	now := time.Date(2026, 8, 24, 1, 0, 0, 0, time.UTC)
	store, closeStore := newMemoryOntologyPlatformStore(t)
	defer closeStore()
	exhaustedKey := RunKey{TenantID: "tenant-1", MemoryID: "exhausted", ContentHash: "hash-1", ExtractorVersion: ExtractorVersion}
	retryableKey := RunKey{TenantID: "tenant-1", MemoryID: "retryable", ContentHash: "hash-2", ExtractorVersion: ExtractorVersion}
	for _, run := range []Run{
		{RunKey: exhaustedKey, Status: RunRunning, Attempts: MaximumAttempts, UpdatedAt: now.Add(-time.Hour)},
		{RunKey: retryableKey, Status: RunRunning, Attempts: 1, UpdatedAt: now.Add(-time.Hour)},
	} {
		if err := store.SaveRun(context.Background(), run); err != nil {
			t.Fatal(err)
		}
	}

	updated, err := store.FailExhaustedStaleRuns(context.Background(), now, 10*time.Minute, 100)
	if err != nil || updated != 1 {
		t.Fatalf("updated = %d, err = %v", updated, err)
	}
	exhausted, _, err := store.GetRun(context.Background(), exhaustedKey)
	if err != nil || exhausted.Status != RunFailed || exhausted.Attempts != MaximumAttempts ||
		!exhausted.NextRetryAt.IsZero() || !exhausted.CompletedAt.Equal(now) ||
		exhausted.LastError != "stale extraction run exhausted after maximum attempts" {
		t.Fatalf("exhausted run = %+v, err = %v", exhausted, err)
	}
	retryable, _, err := store.GetRun(context.Background(), retryableKey)
	if err != nil || retryable.Status != RunRunning || retryable.Attempts != 1 {
		t.Fatalf("retryable run = %+v, err = %v", retryable, err)
	}
}

func TestFailExhaustedStaleRunsHonorsLimit(t *testing.T) {
	now := time.Date(2026, 8, 24, 1, 0, 0, 0, time.UTC)
	store, closeStore := newMemoryOntologyPlatformStore(t)
	defer closeStore()
	keys := make([]RunKey, 0, 101)
	for index := 0; index < 101; index++ {
		key := RunKey{
			TenantID: "tenant-1", MemoryID: fmt.Sprintf("memory-%03d", index),
			ContentHash: fmt.Sprintf("hash-%03d", index), ExtractorVersion: ExtractorVersion,
		}
		keys = append(keys, key)
		if err := store.SaveRun(context.Background(), Run{
			RunKey: key, Status: RunRunning, Attempts: MaximumAttempts, UpdatedAt: now.Add(-time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}

	updated, err := store.FailExhaustedStaleRuns(context.Background(), now, 10*time.Minute, 100)
	if err != nil || updated != 100 {
		t.Fatalf("updated = %d, err = %v", updated, err)
	}
	failed := 0
	running := 0
	for _, key := range keys {
		run, _, err := store.GetRun(context.Background(), key)
		if err != nil {
			t.Fatal(err)
		}
		switch run.Status {
		case RunFailed:
			failed++
		case RunRunning:
			running++
		}
	}
	if failed != 100 || running != 1 {
		t.Fatalf("failed = %d, running = %d", failed, running)
	}
}

func newMemoryOntologyPlatformStore(t *testing.T) (*PlatformStore, func()) {
	t.Helper()
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	if err := migration.NewRunner([]migration.Migration{bridgeMigration()}).Up(ctx, database); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	return NewPlatformStore(database), func() { _ = database.Close() }
}

func TestPlatformStoreReassignTenantMergesRunsAndMentions(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	if err := migration.NewRunner([]migration.Migration{bridgeMigration()}).Up(ctx, database); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	store := NewPlatformStore(database)
	now := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)
	legacyKey := RunKey{TenantID: "legacy-org", MemoryID: "memory-1", ContentHash: "hash-1", ExtractorVersion: ExtractorVersion}
	canonicalKey := legacyKey
	canonicalKey.TenantID = "minvia"
	legacyRun := Run{RunKey: legacyKey, Status: RunSucceeded, Attempts: 2, ModelID: "legacy-model", UpdatedAt: now}
	canonicalRun := Run{RunKey: canonicalKey, Status: RunFailed, Attempts: 3, ModelID: "canonical-model", UpdatedAt: now.Add(time.Minute)}
	if err := store.SaveRun(ctx, legacyRun); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRun(ctx, canonicalRun); err != nil {
		t.Fatal(err)
	}
	legacyMentions := []MentionProjection{
		{Index: 0, Mention: Mention{Surface: "Legacy conflict", ObjectType: "project", Confidence: 0.8}, Resolution: ResolutionUnresolved},
		{Index: 1, Mention: Mention{Surface: "Legacy copied", ObjectType: "organization", Confidence: 0.9}, Resolution: ResolutionUnresolved},
	}
	canonicalMentions := []MentionProjection{
		{Index: 0, Mention: Mention{Surface: "Canonical", ObjectType: "project", Confidence: 0.95}, Resolution: ResolutionUnresolved},
	}
	if err := store.ReplaceMentions(ctx, legacyKey, legacyMentions, now); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceMentions(ctx, canonicalKey, canonicalMentions, now); err != nil {
		t.Fatal(err)
	}

	migrated, err := store.ReassignTenant(ctx, legacyKey.TenantID, canonicalKey.TenantID)
	if err != nil || migrated != 3 {
		t.Fatalf("ReassignTenant = %d, %v", migrated, err)
	}
	got, exists, err := store.GetRun(ctx, canonicalKey)
	if err != nil || !exists || got.ModelID != canonicalRun.ModelID || got.Status != canonicalRun.Status {
		t.Fatalf("canonical run = %+v, exists=%v, err=%v", got, exists, err)
	}
	if _, exists, err := store.GetRun(ctx, legacyKey); err != nil || exists {
		t.Fatalf("legacy run exists=%v, err=%v", exists, err)
	}
	rows, err := database.Query(ctx, `
SELECT mention_index, surface FROM memory_ontology_mentions
WHERE tenant_id = ? ORDER BY mention_index`, canonicalKey.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var mentions []string
	for rows.Next() {
		var index int
		var surface string
		if err := rows.Scan(&index, &surface); err != nil {
			t.Fatal(err)
		}
		mentions = append(mentions, fmt.Sprintf("%d:%s", index, surface))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(mentions) != 2 || mentions[0] != "0:Canonical" || mentions[1] != "1:Legacy copied" {
		t.Fatalf("canonical mentions = %v", mentions)
	}
	if repeated, err := store.ReassignTenant(ctx, legacyKey.TenantID, canonicalKey.TenantID); err != nil || repeated != 0 {
		t.Fatalf("repeated ReassignTenant = %d, %v", repeated, err)
	}
}

func TestPlatformStoreDeletesOnlySelectedMemoryDerivedState(t *testing.T) {
	ctx := context.Background()
	store, closeStore := newMemoryOntologyPlatformStore(t)
	defer closeStore()
	now := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	privateKey := RunKey{TenantID: "agent:private", MemoryID: "memory-private", ContentHash: "private-hash", ExtractorVersion: ExtractorVersion}
	publicKey := RunKey{TenantID: "minvia", MemoryID: "memory-public", ContentHash: "public-hash", ExtractorVersion: ExtractorVersion}
	for _, key := range []RunKey{privateKey, publicKey} {
		if err := store.SaveRun(ctx, Run{RunKey: key, Status: RunSucceeded, Attempts: 1, CompletedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if err := store.ReplaceMentions(ctx, key, []MentionProjection{{
			Index: 0, Mention: Mention{Surface: key.MemoryID, ObjectType: "person", Confidence: 0.99}, Resolution: ResolutionUnresolved,
		}}, now); err != nil {
			t.Fatal(err)
		}
	}

	deleted, err := store.DeleteDerivedStateForMemories(ctx, []string{privateKey.MemoryID, privateKey.MemoryID, ""})
	if err != nil || deleted != 2 {
		t.Fatalf("DeleteDerivedStateForMemories = %d, %v", deleted, err)
	}
	if _, exists, err := store.GetRun(ctx, privateKey); err != nil || exists {
		t.Fatalf("private run exists = %t, err = %v", exists, err)
	}
	if _, exists, err := store.GetRun(ctx, publicKey); err != nil || !exists {
		t.Fatalf("public run exists = %t, err = %v", exists, err)
	}
}
