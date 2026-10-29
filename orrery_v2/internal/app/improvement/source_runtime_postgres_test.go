package improvement

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
)

func TestPostgresSourceRuntimeAdoptionAcrossConnectionsAndRestart(t *testing.T) {
	raw := os.Getenv("ORRERY_TEST_IMPROVEMENT_POSTGRES_URL")
	if raw == "" {
		t.Skip("isolated improvement PostgreSQL URL is not set")
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.HasPrefix(u.Query().Get("search_path"), "orrery_test_") {
		t.Fatal("isolated orrery_test_ schema required")
	}
	ctx := context.Background()
	core1, err := db.Open(ctx, db.Config{Driver: db.DriverPostgres, URL: raw})
	if err != nil {
		t.Fatal(err)
	}
	defer core1.Close()
	core2, err := db.Open(ctx, db.Config{Driver: db.DriverPostgres, URL: raw})
	if err != nil {
		t.Fatal(err)
	}
	defer core2.Close()
	seal := []byte(strings.Repeat("source-runtime-evidence", 2))
	v1, _ := NewArtifactVault(core1, seal)
	v2, _ := NewArtifactVault(core2, seal)
	s1, s2 := NewStore(core1), NewStore(core2)
	spec := replayTestSpec()
	spec.Observation.ID = "postgres-source-runtime-adoption"
	runner := &replayTestRunner{}
	w := ReplayCoordinator{Store: s1, Vault: v1, Model: &replayTestModel{identity: spec.Generator}, Runner: runner}
	c, err := w.Run(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(11 * time.Minute)
	s1.now = func() time.Time { return now }
	s2.now = s1.now
	authority, _ := NewRuntimeAuthority([]byte(strings.Repeat("runtime-operator", 3)))
	r1 := &SourceRuntime{Store: s1, Vault: v1, Authority: authority, Runner: runner, ExecutorSHA256: Digest("runtime-executor")}
	r2 := &SourceRuntime{Store: s2, Vault: v2, Authority: authority, Runner: runner, ExecutorSHA256: r1.ExecutorSHA256}
	req := RuntimeRequest{TenantID: c.TenantID, CaseID: c.ID, RuntimeID: "postgres-source-runtime", ExecutorSHA256: r1.ExecutorSHA256, AllowBootstrap: true, ObservationBudget: 6, Tasks: privateCopy(spec.Tasks)}
	for i := range req.Tasks {
		req.Tasks[i].Input = json.RawMessage(`{"new_input":` + string(rune('0'+i)) + `}`)
	}
	plan, err := r1.Prepare(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	grant, _ := authority.Authorize(plan, "test-operator", now, time.Hour)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, runtime := range []*SourceRuntime{r1, r2} {
		wg.Add(1)
		go func(runtime *SourceRuntime) {
			defer wg.Done()
			_, err := runtime.Activate(ctx, plan, grant)
			results <- err
		}(runtime)
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
	if winners == 0 {
		t.Fatal("no activation committed")
	}
	active, err := r2.Active(ctx, c.TenantID, req.RuntimeID)
	if err != nil || active.Revision != 2 || len(active.Resources) != 2 || !strings.HasPrefix(active.Resources[0].Version, "candidate:") || !strings.HasPrefix(active.Resources[1].Version, "candidate:") {
		t.Fatalf("independent connection saw partial adoption: %+v %v", active, err)
	}
	result, err := r2.Cycle(ctx, plan, grant)
	if err != nil || result.Case.State != Verified || runner.calls != 12 {
		t.Fatalf("restarted runtime failed: %+v %v calls=%d", result.Case, err, runner.calls)
	}
	if _, err := r1.Cycle(ctx, plan, grant); err != nil || runner.calls != 12 {
		t.Fatalf("restart repeated external runs: %v calls=%d", err, runner.calls)
	}
}
