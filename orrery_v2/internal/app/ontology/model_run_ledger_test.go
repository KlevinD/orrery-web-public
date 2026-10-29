package ontology

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestModelRunLedgerSavesAndListsSafeTraceMetadata(t *testing.T) {
	ctx := context.Background()
	ledger := NewMemoryModelRunLedger()
	recordedAt := time.Date(2026, 6, 21, 2, 0, 0, 0, time.UTC)

	if err := ledger.SaveModelRun(ctx, OntologyModelRun{
		TenantID:              "tenant-1",
		Engine:                "ontology_designer",
		Model:                 "gpt-5",
		ModelVersion:          "2026-06-21",
		RunID:                 "run-1",
		InputHash:             "sha256:input",
		PromptHash:            "sha256:prompt",
		SourceRef:             "ontology-designer-v2",
		SourceVersion:         "2026-06-21",
		Status:                ModelRunStatusSucceeded,
		CandidateCount:        2,
		ValidCandidateCount:   1,
		SkippedCandidateCount: 1,
		ProposalCount:         1,
		AnalysisTotal:         1,
		AnalysisBlockingCount: 0,
		AnalysisWarningCount:  1,
		RecordedAt:            recordedAt,
	}); err != nil {
		t.Fatalf("SaveModelRun: %v", err)
	}

	page, err := ledger.ListModelRuns(ctx, ModelRunQuery{TenantID: "tenant-1", Engine: "ontology_designer", Limit: 10})
	if err != nil {
		t.Fatalf("ListModelRuns: %v", err)
	}
	if page.Page.Total != 1 || len(page.Runs) != 1 {
		t.Fatalf("page = %+v", page)
	}
	run := page.Runs[0]
	if run.ID == "" ||
		run.TenantID != "tenant-1" ||
		run.Engine != "ontology_designer" ||
		run.Model != "gpt-5" ||
		run.ModelVersion != "2026-06-21" ||
		run.RunID != "run-1" ||
		run.InputHash != "sha256:input" ||
		run.PromptHash != "sha256:prompt" ||
		run.Status != ModelRunStatusSucceeded ||
		run.CandidateCount != 2 ||
		run.ValidCandidateCount != 1 ||
		run.SkippedCandidateCount != 1 ||
		run.ProposalCount != 1 ||
		run.AnalysisTotal != 1 ||
		run.AnalysisWarningCount != 1 ||
		!run.RecordedAt.Equal(recordedAt) {
		t.Fatalf("run = %+v", run)
	}
}

func TestModelRunLedgerUpsertUpdatesCountersWithoutRawPayload(t *testing.T) {
	ctx := context.Background()
	ledger := NewMemoryModelRunLedger()

	base := OntologyModelRun{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		Model:        "gpt-5",
		ModelVersion: "2026-06-21",
		RunID:        "run-1",
		InputHash:    "sha256:input",
		PromptHash:   "sha256:prompt",
		Status:       ModelRunStatusStarted,
	}
	if err := ledger.SaveModelRun(ctx, base); err != nil {
		t.Fatalf("SaveModelRun started: %v", err)
	}
	base.Status = ModelRunStatusSucceeded
	base.CandidateCount = 3
	base.ValidCandidateCount = 2
	base.SkippedCandidateCount = 1
	base.ProposalCount = 2
	base.AnalysisTotal = 2
	base.AnalysisBlockingCount = 0
	base.AnalysisWarningCount = 1
	if err := ledger.SaveModelRun(ctx, base); err != nil {
		t.Fatalf("SaveModelRun succeeded: %v", err)
	}

	page, err := ledger.ListModelRuns(ctx, ModelRunQuery{TenantID: "tenant-1", RunID: "run-1", Limit: 10})
	if err != nil {
		t.Fatalf("ListModelRuns: %v", err)
	}
	if page.Page.Total != 1 || page.Runs[0].Status != ModelRunStatusSucceeded || page.Runs[0].ProposalCount != 2 {
		t.Fatalf("page = %+v", page)
	}
	if page.Runs[0].RawPrompt != "" || page.Runs[0].RawInput != "" || page.Runs[0].RawOutput != "" {
		t.Fatalf("model run leaked raw payload fields: %+v", page.Runs[0])
	}
}

func TestPlatformModelRunLedgerAggregatesEvaluationTrendsInStore(t *testing.T) {
	ctx := context.Background()
	core := newMigratedModelRunLedgerCore(t, "platform-model-run-trend-aggregation")
	ledger := NewPlatformModelRunLedger(core)
	from := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)

	for _, run := range []OntologyModelRun{{
		TenantID:              "tenant-1",
		Engine:                "ontology_designer",
		Model:                 "gpt-5",
		ModelVersion:          "2026-06-21",
		RunID:                 "run-trend-aggregate-passed",
		InputHash:             "sha256:input-passed",
		PromptHash:            "sha256:prompt-passed",
		Status:                ModelRunStatusSucceeded,
		ProposalCount:         2,
		AnalysisTotal:         2,
		AnalysisWarningCount:  1,
		AnalysisBlockingCount: 0,
		RecordedAt:            from.Add(time.Hour),
	}, {
		TenantID:              "tenant-1",
		Engine:                "ontology_designer",
		Model:                 "gpt-5",
		ModelVersion:          "2026-06-21",
		RunID:                 "run-trend-aggregate-blocked",
		InputHash:             "sha256:input-blocked",
		PromptHash:            "sha256:prompt-blocked",
		Status:                ModelRunStatusSucceeded,
		ProposalCount:         1,
		AnalysisTotal:         1,
		AnalysisBlockingCount: 1,
		RecordedAt:            from.Add(2 * time.Hour),
	}, {
		TenantID:      "tenant-1",
		Engine:        "ontology_designer",
		Model:         "gpt-5",
		ModelVersion:  "2026-06-21",
		RunID:         "run-trend-aggregate-outside-window",
		InputHash:     "sha256:input-outside",
		PromptHash:    "sha256:prompt-outside",
		Status:        ModelRunStatusSucceeded,
		ProposalCount: 1,
		RecordedAt:    to,
	}, {
		TenantID:      "tenant-2",
		Engine:        "ontology_designer",
		Model:         "gpt-5",
		ModelVersion:  "2026-06-21",
		RunID:         "run-trend-aggregate-other-tenant",
		InputHash:     "sha256:input-other",
		PromptHash:    "sha256:prompt-other",
		Status:        ModelRunStatusSucceeded,
		ProposalCount: 1,
		RecordedAt:    from.Add(time.Hour),
	}} {
		if err := ledger.SaveModelRun(ctx, run); err != nil {
			t.Fatalf("SaveModelRun(%s): %v", run.RunID, err)
		}
	}

	page, err := ledger.ListModelRunEvaluationTrendBuckets(ctx, ModelRunEvaluationTrendQuery{
		TenantID:     "tenant-1",
		Engine:       "ontology_designer",
		RecordedFrom: from,
		RecordedTo:   to,
		Limit:        10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendBuckets: %v", err)
	}
	if page.Page.Total != 1 || len(page.Buckets) != 1 {
		t.Fatalf("page = %+v", page)
	}
	bucket := page.Buckets[0]
	if bucket.RunTotal != 2 ||
		bucket.StatusCounts[ModelRunEvaluationGatePassed] != 1 ||
		bucket.StatusCounts[ModelRunEvaluationGateBlocked] != 1 ||
		bucket.ReasonCounts[ModelRunEvaluationReasonBlockingIssues] != 1 ||
		bucket.ProposalTotal != 3 ||
		bucket.BlockingTotal != 1 ||
		bucket.WarningTotal != 1 {
		t.Fatalf("bucket = %+v", bucket)
	}
}

func TestPlatformModelRunLedgerAnnotatesBaselineBeforeBucketFilters(t *testing.T) {
	ctx := context.Background()
	core := newMigratedModelRunLedgerCore(t, "platform-model-run-trend-baseline-filter")
	ledger := NewPlatformModelRunLedger(core)

	for _, run := range []OntologyModelRun{{
		TenantID:      "tenant-1",
		Engine:        "ontology_designer",
		Model:         "gpt-5",
		ModelVersion:  "2026-06-20-baseline",
		RunID:         "run-trend-baseline-filter-1",
		InputHash:     "sha256:baseline-1",
		PromptHash:    "sha256:baseline-prompt-1",
		Status:        ModelRunStatusSucceeded,
		ProposalCount: 1,
	}, {
		TenantID:      "tenant-1",
		Engine:        "ontology_designer",
		Model:         "gpt-5",
		ModelVersion:  "2026-06-21-candidate",
		RunID:         "run-trend-candidate-filter-1",
		InputHash:     "sha256:candidate-1",
		PromptHash:    "sha256:candidate-prompt-1",
		Status:        ModelRunStatusSucceeded,
		ProposalCount: 1,
	}, {
		TenantID:      "tenant-1",
		Engine:        "ontology_designer",
		Model:         "gpt-5",
		ModelVersion:  "2026-06-21-candidate",
		RunID:         "run-trend-candidate-filter-2",
		InputHash:     "sha256:candidate-2",
		PromptHash:    "sha256:candidate-prompt-2",
		Status:        ModelRunStatusSucceeded,
		ProposalCount: 1,
	}} {
		if err := ledger.SaveModelRun(ctx, run); err != nil {
			t.Fatalf("SaveModelRun(%s): %v", run.RunID, err)
		}
	}

	page, err := ledger.ListModelRunEvaluationTrendBuckets(ctx, ModelRunEvaluationTrendQuery{
		TenantID:             "tenant-1",
		Engine:               "ontology_designer",
		MinRunTotal:          2,
		BaselineModelVersion: "2026-06-20-baseline",
		Limit:                10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendBuckets: %v", err)
	}
	if page.Page.Total != 1 || len(page.Buckets) != 1 {
		t.Fatalf("page = %+v", page)
	}
	bucket := page.Buckets[0]
	if bucket.ModelVersion != "2026-06-21-candidate" ||
		bucket.BaselineModelVersion != "2026-06-20-baseline" ||
		bucket.BaselineRunTotal != 1 ||
		bucket.RunTotalDelta != 1 ||
		bucket.RunTotalDeltaPercent == nil ||
		*bucket.RunTotalDeltaPercent != 100 {
		t.Fatalf("bucket baseline comparison = %+v", bucket)
	}
}

func TestPlatformModelRunLedgerCountsPendingReasonOnBlockedPendingRuns(t *testing.T) {
	ctx := context.Background()
	core := newMigratedModelRunLedgerCore(t, "platform-model-run-trend-blocked-pending")
	ledger := NewPlatformModelRunLedger(core)

	if err := ledger.SaveModelRun(ctx, OntologyModelRun{
		TenantID:      "tenant-1",
		Engine:        "ontology_designer",
		Model:         "gpt-5",
		ModelVersion:  "2026-06-21",
		RunID:         "run-trend-blocked-pending",
		InputHash:     "sha256:blocked-pending",
		PromptHash:    "sha256:blocked-pending-prompt",
		Status:        ModelRunStatusStarted,
		ProposalCount: 0,
	}); err != nil {
		t.Fatalf("SaveModelRun: %v", err)
	}

	page, err := ledger.ListModelRunEvaluationTrendBuckets(ctx, ModelRunEvaluationTrendQuery{
		TenantID: "tenant-1",
		Engine:   "ontology_designer",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListModelRunEvaluationTrendBuckets: %v", err)
	}
	if page.Page.Total != 1 || len(page.Buckets) != 1 {
		t.Fatalf("page = %+v", page)
	}
	bucket := page.Buckets[0]
	if bucket.StatusCounts[ModelRunEvaluationGateBlocked] != 1 ||
		bucket.StatusCounts[ModelRunEvaluationGatePending] != 0 ||
		bucket.ReasonCounts[ModelRunEvaluationReasonRunPending] != 1 ||
		bucket.ReasonCounts[ModelRunEvaluationReasonNoProposals] != 1 {
		t.Fatalf("bucket = %+v", bucket)
	}
}

func newMigratedModelRunLedgerCore(t *testing.T, name string) *db.Store {
	t.Helper()
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: name},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	t.Cleanup(func() { _ = databases.Close() })
	core, err := databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core database: %v", err)
	}
	coordinator := migration.NewCoordinator(databases)
	if err := RegisterMigrations(coordinator); err != nil {
		t.Fatalf("RegisterMigrations: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	return core
}
