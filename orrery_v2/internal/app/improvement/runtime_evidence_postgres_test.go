package improvement

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
)

func TestPostgresRuntimeEvidenceAcrossConnectionsAndRestart(t *testing.T) {
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
		t.Helper()
		core, err := db.Open(ctx, db.Config{Driver: db.DriverPostgres, URL: raw})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = core.Close() })
		return core
	}
	core1, core2 := open(), open()
	s1, s2 := NewRuntimeEvidenceStore(core1), NewRuntimeEvidenceStore(core2)
	_, e := runtimeEvidenceFixture(t, "postgres-attempt-a")
	end := runtimeEvidenceTerminal()
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, store := range []*RuntimeEvidenceStore{s1, s2} {
		wg.Add(1)
		go func(store *RuntimeEvidenceStore) { defer wg.Done(); errs <- store.Capture(ctx, e) }(store)
	}
	wg.Wait()
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	for _, store := range []*RuntimeEvidenceStore{s1, s2} {
		wg.Add(1)
		go func(store *RuntimeEvidenceStore) { defer wg.Done(); _, err := store.Complete(ctx, e, end); errs <- err }(store)
	}
	wg.Wait()
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	other := end
	other.Status = agentcore.RuntimeStatusFailed
	if _, err := s2.Complete(ctx, e, other); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting terminal: %v", err)
	}
	_ = core1.Close()
	_ = core2.Close()
	core3 := open()
	s3 := NewRuntimeEvidenceStore(core3)
	if got, err := s3.GetTerminal(ctx, e.Scope.TenantID, e.AttemptID); err != nil || got.RequestID != e.ID || got.Status != end.Status {
		t.Fatalf("restart lost terminal: %+v %v", got, err)
	}
	c, err := s3.Complete(ctx, e, end)
	if err != nil || c.State != Observed || c.Revision != 1 {
		t.Fatalf("restart idempotence: %+v %v", c, err)
	}
	if review, persisted, err := s3.GetReview(ctx, e.Scope.TenantID, e.AttemptID); err != nil || !persisted || review.BaselineReady || review.RequestID != e.ID {
		t.Fatalf("restart lost immutable review: %+v %v %v", review, persisted, err)
	}
	page, err := core3.ListRecords(ctx, runtimeReviewTable, tenantPrefix(e.Scope.TenantID), 100)
	if err != nil {
		t.Fatal(err)
	}
	reviews := 0
	for _, item := range page.Items {
		if item.Key == runtimeReviewKey(c.TenantID, e.AttemptID) {
			reviews++
		}
	}
	if reviews != 1 {
		t.Fatalf("concurrent terminal commits produced %d reviews", reviews)
	}
	for _, table := range []string{caseTable, historyTable, outboxTable} {
		page, err := core3.ListRecords(ctx, table, tenantPrefix(e.Scope.TenantID), 100)
		count := 0
		for _, item := range page.Items {
			if strings.HasPrefix(item.Key, caseKey(c.TenantID, c.ID)) {
				count++
			}
		}
		if err != nil || count != 1 {
			t.Fatalf("duplicate or missing %s after restart: %d %v", table, count, err)
		}
	}
}
