package resourceadmin

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
)

func TestSkillPromotionFromSeparateServicesRejectsConcurrentWinner(t *testing.T) {
	kinds := []string{"memory", "separate-database-connections"}
	if os.Getenv("ORRERY_TEST_RSI_POSTGRES_URL") != "" {
		kinds = append(kinds, "postgres")
	}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			one, two, core := commitTestRegistries(t, kind)
			base := skillres.SkillDefinition{ID: "skill.commit", Name: "Commit", Version: "1", Enabled: true}
			first, second := base.Clone(), base.Clone()
			first.Version, second.Version = "2", "3"
			if err := one.Register(ctx, base); err != nil {
				t.Fatal(err)
			}
			for _, candidate := range []skillres.SkillDefinition{first, second} {
				if err := one.RegisterVersion(ctx, candidate); err != nil {
					t.Fatal(err)
				}
			}
			evaluations := NewMemorySkillVersionEvaluationStore()
			for _, candidate := range []skillres.SkillDefinition{first, second} {
				if err := evaluations.SaveSkillVersionEvaluation(ctx, base.ID, passingSkillVersionEvaluation(candidate.Version, base, candidate)); err != nil {
					t.Fatal(err)
				}
			}
			entered := make(chan struct{}, 2)
			releaseOne, releaseTwo := make(chan struct{}), make(chan struct{})
			commandOne, commandTwo := NewCommandService(nil, one), NewCommandService(nil, two)
			commandOne.SetSkillVersionEvaluationStore(gatedSkillEvaluations{SkillVersionEvaluationStore: evaluations, entered: entered, release: releaseOne})
			commandTwo.SetSkillVersionEvaluationStore(gatedSkillEvaluations{SkillVersionEvaluationStore: evaluations, entered: entered, release: releaseTwo})
			viewer := ViewerRef{ViewerID: "rsi-test", PermissionScope: []string{PermissionResourceManage}}
			resultOne, resultTwo := make(chan error, 1), make(chan error, 1)
			go func() {
				_, err := commandOne.PromoteSkillVersion(ctx, viewer, string(base.ID), first.Version, PromoteSkillVersionRequest{EvaluationRef: first.Version})
				resultOne <- err
			}()
			go func() {
				_, err := commandTwo.PromoteSkillVersion(ctx, viewer, string(base.ID), second.Version, PromoteSkillVersionRequest{EvaluationRef: second.Version})
				resultTwo <- err
			}()
			for i := 0; i < 2; i++ {
				select {
				case <-entered:
				case <-ctx.Done():
					t.Fatal("both services did not reach the evaluated commit boundary")
				}
			}
			close(releaseOne)
			if err := <-resultOne; err != nil {
				t.Fatalf("first commit: %v", err)
			}
			close(releaseTwo)
			if err := <-resultTwo; !errors.Is(err, ErrConflict) {
				t.Fatalf("stale second commit = %v, want conflict", err)
			}
			active, _ := two.Get(ctx, base.ID)
			wasActive, _ := two.WasActiveVersion(ctx, base.ID, second.Version)
			if active.Version != first.Version || wasActive {
				t.Fatalf("loser changed state: active=%s, loser history=%v", active.Version, wasActive)
			}
			if core != nil {
				page, err := core.ListRecords(ctx, skillVersionAuditTable, "", 100)
				if err != nil || len(page.Items) != 1 {
					t.Fatalf("committed audit events = %d, error = %v", len(page.Items), err)
				}
			}
		})
	}
}

func TestSkillPromotionRechecksSameVersionContentAfterEvaluationRead(t *testing.T) {
	ctx := context.Background()
	registry := newRSITestSkillRegistry(t)
	base, _ := registry.Get(ctx, "skill.review")
	candidate := base.Clone()
	candidate.Version = "2.0.0"
	if err := registry.RegisterVersion(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	evaluations := NewMemorySkillVersionEvaluationStore()
	if err := evaluations.SaveSkillVersionEvaluation(ctx, base.ID, passingSkillVersionEvaluation("eval", base, candidate)); err != nil {
		t.Fatal(err)
	}
	changed := base.Clone()
	changed.Description = "new runtime policy under the same label"
	command := NewCommandService(nil, registry)
	command.SetSkillVersionEvaluationStore(mutatingSkillEvaluations{SkillVersionEvaluationStore: evaluations, mutate: func() error { return registry.UpdateDefinition(ctx, changed) }})
	_, err := command.PromoteSkillVersion(ctx, ViewerRef{ViewerID: "rsi", PermissionScope: []string{PermissionResourceManage}}, string(base.ID), candidate.Version, PromoteSkillVersionRequest{EvaluationRef: "eval"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("same-label stale evaluation commit = %v", err)
	}
	active, _ := registry.Get(ctx, base.ID)
	if skillres.DefinitionHash(active) != skillres.DefinitionHash(changed) {
		t.Fatal("stale evaluation overwrote newer content")
	}
}

func TestSkillAuditFailureDoesNotActivateCandidate(t *testing.T) {
	ctx := context.Background()
	registry := newRSITestSkillRegistry(t)
	base, _ := registry.Get(ctx, "skill.review")
	candidate := base.Clone()
	candidate.Version = "2.0.0"
	if err := registry.RegisterVersion(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	evaluations := NewMemorySkillVersionEvaluationStore()
	if err := evaluations.SaveSkillVersionEvaluation(ctx, base.ID, passingSkillVersionEvaluation("eval", base, candidate)); err != nil {
		t.Fatal(err)
	}
	command := NewCommandService(toolres.NewMemoryRegistry(), registry)
	command.SetSkillVersionEvaluationStore(evaluations)
	failure := errors.New("audit unavailable")
	command.SetAuditRecorder(commitAuditFailure{failure})
	_, err := command.PromoteSkillVersion(ctx, ViewerRef{ViewerID: "rsi", PermissionScope: []string{PermissionResourceManage}}, string(base.ID), candidate.Version, PromoteSkillVersionRequest{EvaluationRef: "eval"})
	if !errors.Is(err, failure) {
		t.Fatalf("audit failure = %v", err)
	}
	active, _ := registry.Get(ctx, base.ID)
	wasActive, _ := registry.WasActiveVersion(ctx, base.ID, candidate.Version)
	if active.Version != base.Version || wasActive {
		t.Fatalf("failed audit left candidate activated: active=%s, history=%v", active.Version, wasActive)
	}
}

type gatedSkillEvaluations struct {
	SkillVersionEvaluationStore
	entered chan<- struct{}
	release <-chan struct{}
}

func (s gatedSkillEvaluations) GetSkillVersionEvaluation(ctx context.Context, id agentcore.SkillID, ref string) (SkillVersionEvaluation, error) {
	evaluation, err := s.SkillVersionEvaluationStore.GetSkillVersionEvaluation(ctx, id, ref)
	if err != nil {
		return evaluation, err
	}
	s.entered <- struct{}{}
	select {
	case <-s.release:
		return evaluation, nil
	case <-ctx.Done():
		return SkillVersionEvaluation{}, ctx.Err()
	}
}

type mutatingSkillEvaluations struct {
	SkillVersionEvaluationStore
	mutate func() error
}

func (s mutatingSkillEvaluations) GetSkillVersionEvaluation(ctx context.Context, id agentcore.SkillID, ref string) (SkillVersionEvaluation, error) {
	evaluation, err := s.SkillVersionEvaluationStore.GetSkillVersionEvaluation(ctx, id, ref)
	if err == nil {
		err = s.mutate()
	}
	return evaluation, err
}

type commitAuditFailure struct{ err error }

func (r commitAuditFailure) RecordAuditEvent(context.Context, AuditEvent) error { return r.err }

func commitTestRegistries(t *testing.T, kind string) (skillres.VersionedRegistry, skillres.VersionedRegistry, *db.Store) {
	t.Helper()
	if kind == "memory" {
		registry := skillres.NewMemoryRegistry()
		return registry, registry, nil
	}
	config := db.Config{Driver: db.DriverFile, URL: "file://" + filepath.Join(t.TempDir(), "commit.db")}
	if kind == "postgres" {
		config = rsiPostgresTestConfig(t)
	}
	first, err := db.Open(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := db.Open(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	return skillres.NewPlatformRegistry(first), skillres.NewPlatformRegistry(second), first
}

func rsiPostgresTestConfig(t *testing.T) db.Config {
	t.Helper()
	raw := os.Getenv("ORRERY_TEST_RSI_POSTGRES_URL")
	if raw == "" {
		t.Skip("ORRERY_TEST_RSI_POSTGRES_URL is not set")
	}
	parsed, err := url.Parse(raw)
	if err != nil || !strings.HasPrefix(parsed.Query().Get("search_path"), "rsi_validation_") {
		t.Fatal("PostgreSQL RSI tests require a dedicated rsi_validation_ schema")
	}
	return db.Config{Driver: db.DriverPostgres, URL: raw}
}

func TestPostgresSkillJournalFailureRollsBackPublication(t *testing.T) {
	ctx := context.Background()
	core, err := db.Open(ctx, rsiPostgresTestConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	registry := skillres.NewPlatformRegistry(core)
	base := skillres.SkillDefinition{ID: "skill.postgres-journal-failure", Name: "Postgres atomic journal", Version: "1", Enabled: true}
	candidate := base.Clone()
	candidate.Version = "2"
	if err := registry.Register(ctx, base); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterVersion(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	evaluations := NewMemorySkillVersionEvaluationStore()
	if err := evaluations.SaveSkillVersionEvaluation(ctx, base.ID, passingSkillVersionEvaluation("postgres-failure", base, candidate)); err != nil {
		t.Fatal(err)
	}
	if _, err := core.Exec(ctx, `CREATE FUNCTION rsi_validation_reject_journal() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.table_name = 'resource_skill_version_audit_events' THEN RAISE EXCEPTION 'RSI test journal unavailable'; END IF; RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = core.Exec(ctx, `DROP FUNCTION rsi_validation_reject_journal() CASCADE`) })
	if _, err := core.Exec(ctx, `CREATE TRIGGER rsi_validation_reject_journal BEFORE INSERT ON platform_records FOR EACH ROW EXECUTE FUNCTION rsi_validation_reject_journal()`); err != nil {
		t.Fatal(err)
	}
	command := NewCommandService(nil, registry)
	command.SetSkillVersionEvaluationStore(evaluations)
	_, err = command.PromoteSkillVersion(ctx, ViewerRef{ViewerID: "postgres-validation", PermissionScope: []string{PermissionResourceManage}}, string(base.ID), candidate.Version, PromoteSkillVersionRequest{EvaluationRef: "postgres-failure"})
	if err == nil {
		t.Fatal("publication succeeded despite the failed journal insert")
	}
	active, err := registry.Get(ctx, base.ID)
	if err != nil || active.Version != base.Version {
		t.Fatalf("failed transaction changed active version: %s, %v", active.Version, err)
	}
	wasActive, err := registry.WasActiveVersion(ctx, base.ID, candidate.Version)
	if err != nil || wasActive {
		t.Fatalf("failed transaction retained candidate activation: %v, %v", wasActive, err)
	}
}
