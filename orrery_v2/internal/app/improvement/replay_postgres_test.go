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

func TestPostgresReplayPersistsArtifactsAndResumesOnNewConnection(t *testing.T) {
	raw := os.Getenv("ORRERY_TEST_IMPROVEMENT_POSTGRES_URL")
	if raw == "" {
		t.Skip("isolated improvement PostgreSQL URL is not set")
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.HasPrefix(u.Query().Get("search_path"), "orrery_test_") {
		t.Fatal("an isolated orrery_test_ schema is required")
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
	key := []byte(strings.Repeat("postgres-test-seal", 3))
	vault1, _ := NewArtifactVault(core1, key)
	vault2, _ := NewArtifactVault(core2, key)
	refs := make(chan ArtifactRef, 2)
	failures := make(chan error, 2)
	var wg sync.WaitGroup
	for _, vault := range []*ArtifactVault{vault1, vault2} {
		wg.Add(1)
		go func(v *ArtifactVault) {
			defer wg.Done()
			ref, err := v.put(ctx, "tenant-a", "immutable-material", "same bytes", true)
			refs <- ref
			failures <- err
		}(vault)
	}
	wg.Wait()
	close(refs)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first ArtifactRef
	for ref := range refs {
		if first.ID != "" && first != ref {
			t.Fatal("concurrent immutable writes differ")
		}
		first = ref
	}
	spec := replayTestSpec()
	spec.Observation.ID = "postgres-code-replay"
	model := &replayTestModel{identity: spec.Generator}
	runner := &replayTestRunner{}
	w := &ReplayCoordinator{Store: NewStore(core1), Vault: vault1, Model: model, Runner: runner}
	c, err := w.Run(ctx, spec)
	if err != nil || c.State != Evaluated {
		t.Fatalf("PostgreSQL experiment: %+v %v", c, err)
	}
	restartedModel := &replayTestModel{identity: spec.Generator}
	restartedRunner := &replayTestRunner{}
	restarted := &ReplayCoordinator{Store: NewStore(core2), Vault: vault2, Model: restartedModel, Runner: restartedRunner}
	got, err := restarted.Run(ctx, spec)
	if err != nil || got.ID != c.ID || got.Revision != c.Revision || restartedModel.calls != 0 || restartedRunner.calls != 0 {
		t.Fatalf("restart repeated external operations: %+v %v", got, err)
	}
	if err := NewReplayVerifier(vault2).Verify(ctx, got, Transition{To: Evaluated, Evaluation: got.Evaluation}); err != nil {
		t.Fatalf("persisted proof failed on independent connection: %v", err)
	}
}
