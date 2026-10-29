package improvement

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
)

func TestPostgresImprovementConcurrentFencingAndTransitions(t *testing.T) {
	raw := os.Getenv("ORRERY_TEST_IMPROVEMENT_POSTGRES_URL")
	if raw == "" {
		t.Skip("isolated improvement PostgreSQL URL is not set")
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.HasPrefix(u.Query().Get("search_path"), "orrery_test_") {
		t.Fatal("an isolated orrery_test_ schema is required")
	}
	ctx := context.Background()
	cfg := db.Config{Driver: db.DriverPostgres, URL: raw}
	core1, err := db.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer core1.Close()
	core2, err := db.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer core2.Close()
	stores := []*Store{NewStore(core1), NewStore(core2)}
	c, err := stores[0].Observe(ctx, testObservation("postgres-fencing"))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i, store := range stores {
		wg.Add(1)
		go func(i int, store *Store) {
			defer wg.Done()
			_, err := store.Claim(ctx, c.TenantID, c.ID, []string{"worker-a", "worker-b"}[i], c.Revision, time.Minute)
			results <- err
		}(i, store)
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrConflict) && !errors.Is(err, ErrLease) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("PostgreSQL claim winners: %d", winners)
	}
	c, err = stores[1].Get(ctx, c.TenantID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	results = make(chan error, 2)
	for i, store := range stores {
		wg.Add(1)
		go func(i int, store *Store) {
			defer wg.Done()
			command := testCommand(c, Rejected)
			command.ID = []string{"reject-a", "reject-b"}[i]
			command.Reason = "no eligible candidate"
			_, err := store.Advance(ctx, command, nil)
			results <- err
		}(i, store)
	}
	wg.Wait()
	close(results)
	winners = 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("PostgreSQL transition winners: %d", winners)
	}
	got, err := stores[0].Get(ctx, c.TenantID, c.ID)
	if err != nil || got.State != Rejected || got.Revision != c.Revision+1 {
		t.Fatalf("committed state: %+v %v", got, err)
	}
	if _, err := stores[0].Get(ctx, "tenant-b", c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign tenant read: %v", err)
	}
}
