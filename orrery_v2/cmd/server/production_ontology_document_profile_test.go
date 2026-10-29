package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/knowledgeinstances"
	"orrery_v2/internal/app/ontologyscope"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func documentProfileFenceFixture(t *testing.T, core *db.Store) (*productionOntologyDocumentInference, agentcore.AgentProfile) {
	t.Helper()
	p := agentcore.AgentProfile{ID: "profile-fence-agent", AgentKind: agentcore.AgentKindAI, Enabled: true}
	for _, id := range []agentcore.ToolID{toolstd.OntologyInfer, toolstd.OntologyInferenceWorkflow, toolstd.OntologyInferenceRules, toolstd.OntologyDocumentInference} {
		p.ToolRefs = append(p.ToolRefs, agentcore.ToolRef{ToolID: id, PermissionScope: "ontology.manage"})
	}
	p.ToolRefs = append(p.ToolRefs, agentcore.ToolRef{ToolID: toolstd.KnowledgeSearch, PermissionScope: "knowledge.read"})
	encoded, _ := json.Marshal(p)
	if err := core.PutRecord(context.Background(), "agent_profiles", string(p.ID), db.Record{"payload": string(encoded)}); err != nil {
		t.Fatal(err)
	}
	return &productionOntologyDocumentInference{store: &knowledgeinstances.Store{Core: core}, api: &productionOntologyInferenceAPI{profiles: agentcore.NewPlatformProfileRepository(uow.New(core))}}, p
}

func TestProductionOntologyDocumentFencesPersistedProfileGrants(t *testing.T) {
	_, core, _, _ := productionInferenceWorkflowFixture(t)
	w, profile := documentProfileFenceFixture(t, core)
	hash, err := documentProfileHash(profile, string(profile.ID))
	if err != nil {
		t.Fatal(err)
	}
	fence := func() error {
		return core.WithTx(context.Background(), func(tx db.Tx) error { return w.fenceProfile(context.Background(), tx, string(profile.ID), hash) })
	}
	if err := fence(); err != nil {
		t.Fatal(err)
	}
	profile.ToolRefs = nil
	encoded, _ := json.Marshal(profile)
	if err := core.PutRecord(context.Background(), "agent_profiles", string(profile.ID), db.Record{"payload": string(encoded)}); err != nil {
		t.Fatal(err)
	}
	if err := fence(); !errors.Is(err, ontologyscope.ErrAccessDenied) {
		t.Fatalf("persisted revocation accepted: %v", err)
	}
}

func TestPostgresOntologyDocumentProfileFence(t *testing.T) {
	raw := os.Getenv("ORRERY_TEST_POSTGRES_URL")
	if raw == "" {
		t.Skip("isolated PostgreSQL URL is not configured")
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.HasPrefix(u.Query().Get("search_path"), "orrery_inference_test_") {
		t.Fatal("isolated schema is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	open := func(raw string) *db.Registry {
		t.Helper()
		r, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{db.RoleCore: {Driver: db.DriverPostgres, URL: raw}}})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	admin := open(raw)
	defer admin.Close()
	adminCore, _ := admin.Store(db.RoleCore)
	schema := fmt.Sprintf("orrery_inference_test_profile_%d", time.Now().UnixNano())
	if _, err := adminCore.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatal(err)
	}
	defer adminCore.Exec(context.Background(), `DROP SCHEMA "`+schema+`" CASCADE`)
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	r := open(u.String())
	defer r.Close()
	core, _ := r.Store(db.RoleCore)
	w, profile := documentProfileFenceFixture(t, core)
	hash, _ := documentProfileHash(profile, string(profile.ID))
	locked := make(chan struct{})
	release := make(chan struct{})
	committed := make(chan error, 1)
	go func() {
		committed <- core.WithTx(ctx, func(tx db.Tx) error {
			if err := w.fenceProfile(ctx, tx, string(profile.ID), hash); err != nil {
				return err
			}
			close(locked)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-locked:
	case err := <-committed:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	profile.ToolRefs = nil
	encoded, _ := json.Marshal(profile)
	revoked := make(chan error, 1)
	go func() {
		revoked <- core.PutRecord(ctx, "agent_profiles", string(profile.ID), db.Record{"payload": string(encoded)})
	}()
	select {
	case err := <-revoked:
		close(release)
		t.Fatalf("revocation crossed the committing grant fence: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	if err := <-committed; err != nil {
		t.Fatal(err)
	}
	if err := <-revoked; err != nil {
		t.Fatal(err)
	}
	if err := core.WithTx(ctx, func(tx db.Tx) error { return w.fenceProfile(ctx, tx, string(profile.ID), hash) }); !errors.Is(err, ontologyscope.ErrAccessDenied) {
		t.Fatalf("revoked profile passed: %v", err)
	}
}
