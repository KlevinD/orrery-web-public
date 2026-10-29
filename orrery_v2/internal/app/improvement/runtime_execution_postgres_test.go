package improvement

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"orrery_v2/internal/platform/db"
)

func TestPostgresRuntimeExecutionCannotRaceTerminalHistory(t *testing.T) {
	raw := os.Getenv("ORRERY_TEST_IMPROVEMENT_POSTGRES_URL")
	if raw == "" {
		t.Skip("isolated improvement PostgreSQL URL is not set")
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.HasPrefix(u.Query().Get("search_path"), "orrery_test_") {
		t.Fatal("isolated orrery_test_ schema required")
	}
	ctx := context.Background()
	open := func() *db.Store {
		core, err := db.Open(ctx, db.Config{Driver: db.DriverPostgres, URL: raw})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = core.Close() })
		return core
	}
	a, b := NewRuntimeEvidenceStore(open()), NewRuntimeEvidenceStore(open())
	for i := range 12 {
		_, request := runtimeEvidenceFixture(t, fmt.Sprintf("postgres-execution-race-%d", i))
		execution := executionFixture(t, request)
		if err := a.Capture(ctx, request); err != nil {
			t.Fatal(err)
		}
		var captureErr, completeErr error
		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Add(2)
		go func() { defer wg.Done(); <-start; captureErr = a.CaptureExecution(ctx, execution) }()
		go func() { defer wg.Done(); <-start; _, completeErr = b.Complete(ctx, request, runtimeEvidenceTerminal()) }()
		close(start)
		wg.Wait()
		if completeErr != nil || (captureErr != nil && !errors.Is(captureErr, ErrConflict)) {
			t.Fatalf("race failed capture=%v complete=%v", captureErr, completeErr)
		}
		reopened := NewRuntimeEvidenceStore(open())
		r, persisted, err := reopened.GetReview(ctx, request.Scope.TenantID, request.AttemptID)
		if err != nil || !persisted {
			t.Fatal("mixed immutable history", err)
		}
		if captureErr == nil {
			if r.Protocol != runtimeReviewProtocolV2 || r.ExecutionID != execution.ID {
				t.Fatal("lost execution before terminal")
			}
		} else if r.Protocol != runtimeReviewProtocol || r.ExecutionID != "" {
			t.Fatal("late evidence rewrote V1")
		}
	}
}
