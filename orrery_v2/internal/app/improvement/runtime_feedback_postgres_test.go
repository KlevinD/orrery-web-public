package improvement

import (
	"context"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"orrery_v2/internal/platform/db"
)

func TestPostgresRuntimeFeedbackRecoveryAcrossWorkersAndRestart(t *testing.T) {
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
	_, req := runtimeEvidenceFixture(t, "postgres-recovery-attempt")
	entry := feedbackFixture(t, req)
	first := open()
	if err := NewRuntimeEvidenceStore(first).Capture(ctx, req); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := feedbackJournal(t, root).Save(ctx, entry); err != nil {
		t.Fatal(err)
	}
	_ = first.Close()
	// Both new connections and independent directory cursors may see the journal.
	cores := []*db.Store{open(), open()}
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, core := range cores {
		wg.Add(1)
		journal := feedbackJournal(t, root)
		go func(core *db.Store, journal *FileRuntimeFeedbackJournal) {
			defer wg.Done()
			_, err := NewRuntimeEvidenceStore(core).RecoverFeedback(ctx, journal, 100)
			errs <- err
		}(core, journal)
	}
	wg.Wait()
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	for _, core := range cores {
		_ = core.Close()
	}
	core := open()
	store := NewRuntimeEvidenceStore(core)
	end, err := store.GetTerminal(ctx, req.Scope.TenantID, req.AttemptID)
	if err != nil || Digest(end) != Digest(entry.Terminal) {
		t.Fatalf("wrong recovered terminal after restart: %+v %v", end, err)
	}
	id := caseID(Observation{TenantID: req.Scope.TenantID, ID: "captured-runtime-result:" + Digest(end)})
	c, err := NewStore(core).Get(ctx, req.Scope.TenantID, id)
	if err != nil || c.Revision != 1 || c.State != Observed {
		t.Fatalf("missing recovered case: %+v %v", c, err)
	}
	for _, table := range []string{caseTable, historyTable, outboxTable} {
		page, err := core.ListRecords(ctx, table, tenantPrefix(c.TenantID), 100)
		count := 0
		for _, item := range page.Items {
			if strings.HasPrefix(item.Key, caseKey(c.TenantID, c.ID)) {
				count++
			}
		}
		if err != nil || count != 1 {
			t.Fatalf("non-atomic or duplicate recovered %s: %d %v", table, count, err)
		}
	}
	if files, err := os.ReadDir(root); err != nil || len(files) != 0 {
		t.Fatalf("journal not acknowledged: %d %v", len(files), err)
	}
}
