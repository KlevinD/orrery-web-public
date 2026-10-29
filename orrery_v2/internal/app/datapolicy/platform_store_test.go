package datapolicy

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

type policyRevisionObserverFunc func(int64)

func (f policyRevisionObserverFunc) PolicyRevisionAdvanced(revision int64) {
	f(revision)
}

func TestPlatformStorePersistsPoliciesAndDecidesContext(t *testing.T) {
	ctx := context.Background()
	sqlStore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "datapolicy-platform-store"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer sqlStore.Close()
	if err := migration.NewRunner([]migration.Migration{dataPolicyMigration(), dataPolicyRevisionMigration()}).Up(ctx, sqlStore); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	now := time.Unix(600, 0).UTC()
	observedRevisions := make([]int64, 0, 2)
	store := NewPlatformStore(sqlStore, WithPolicyRevisionObserver(policyRevisionObserverFunc(func(revision int64) {
		observedRevisions = append(observedRevisions, revision)
	})))
	if revision, err := store.PolicyRevision(ctx); err != nil || revision != 0 {
		t.Fatalf("initial policy revision = %d, err=%v", revision, err)
	}
	if err := store.SavePurposePolicy(ctx, PurposePolicy{
		ID:         "purpose-hide-contact",
		TenantID:   "tenant-1",
		Consumer:   "iris",
		Purpose:    "mission_assignment",
		ObjectType: "contact",
		Effect:     EffectDeny,
		Reason:     "contact is outside purpose",
		Enabled:    true,
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("SavePurposePolicy: %v", err)
	}
	if err := store.SaveFieldPolicy(ctx, FieldPolicy{
		ID:         "field-mask-email",
		TenantID:   "tenant-1",
		Consumer:   "iris",
		Purpose:    "mission_assignment",
		ObjectType: "person",
		FieldPath:  "email",
		Effect:     EffectMask,
		Reason:     "email is not needed",
		Enabled:    true,
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("SaveFieldPolicy: %v", err)
	}
	if revision, err := store.PolicyRevision(ctx); err != nil || revision != 2 {
		t.Fatalf("policy revision after saves = %d, err=%v", revision, err)
	}
	if len(observedRevisions) != 2 || observedRevisions[0] != 1 || observedRevisions[1] != 2 {
		t.Fatalf("observed policy revisions = %+v", observedRevisions)
	}

	reopened := NewPlatformStore(sqlStore)
	decision, err := reopened.DecideContext(ctx, contextgraph.BuildContextBundleRequest{
		TenantID: "tenant-1",
		Consumer: "iris",
		Purpose:  "mission_assignment",
	}, []contextgraph.Object{
		{ID: "person-1", ObjectType: "person"},
		{ID: "contact-1", ObjectType: "contact"},
	})
	if err != nil {
		t.Fatalf("DecideContext: %v", err)
	}
	if !containsString(decision.HiddenObjectIDs, "contact-1") {
		t.Fatalf("hidden objects = %+v, want contact-1", decision.HiddenObjectIDs)
	}
	if got := decision.MaskedFields["person-1"]; len(got) != 1 || got[0] != "email" {
		t.Fatalf("masked fields = %+v", decision.MaskedFields)
	}
}

func TestPlatformStoreRollsBackPolicyWhenRevisionCannotAdvance(t *testing.T) {
	ctx := context.Background()
	sqlStore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "data-policy-revision-rollback"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer sqlStore.Close()
	if err := migration.NewRunner([]migration.Migration{dataPolicyMigration()}).Up(ctx, sqlStore); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	err = NewPlatformStore(sqlStore).SaveFieldPolicy(ctx, FieldPolicy{
		ID: "field-1", TenantID: "tenant-1", Consumer: "ontology", Purpose: "read", ObjectType: "mission", FieldPath: "secret", Effect: EffectMask, Enabled: true,
	})
	if err == nil {
		t.Fatal("save without revision state error = nil")
	}
	var count int
	if err := sqlStore.QueryRow(ctx, `SELECT COUNT(*) FROM field_policies`).Scan(&count); err != nil {
		t.Fatalf("count policies: %v", err)
	}
	if count != 0 {
		t.Fatalf("policy committed without revision: count=%d", count)
	}
}

func TestPlatformStoreHasActivePurposeDenyUsesExactEnabledScope(t *testing.T) {
	ctx := context.Background()
	sqlStore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "data-policy-active-purpose-deny"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer sqlStore.Close()
	if err := migration.NewRunner([]migration.Migration{dataPolicyMigration(), dataPolicyRevisionMigration()}).Up(ctx, sqlStore); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	store := NewPlatformStore(sqlStore)
	policies := []PurposePolicy{
		{ID: "matching-deny", TenantID: "tenant-1", Consumer: "ontology_workbench", Purpose: "context_read", ObjectType: "mission", Effect: EffectDeny, Enabled: true},
		{ID: "disabled-deny", TenantID: "tenant-2", Consumer: "ontology_workbench", Purpose: "context_read", ObjectType: "mission", Effect: EffectDeny, Enabled: false},
		{ID: "different-purpose", TenantID: "tenant-3", Consumer: "ontology_workbench", Purpose: "lineage_read", ObjectType: "mission", Effect: EffectDeny, Enabled: true},
	}
	for _, policy := range policies {
		if err := store.SavePurposePolicy(ctx, policy); err != nil {
			t.Fatalf("SavePurposePolicy(%s): %v", policy.ID, err)
		}
	}

	tests := []struct {
		name     string
		tenantID string
		consumer string
		purpose  string
		want     bool
	}{
		{name: "matching active deny", tenantID: "tenant-1", consumer: "ontology_workbench", purpose: "context_read", want: true},
		{name: "different tenant", tenantID: "tenant-2", consumer: "ontology_workbench", purpose: "context_read"},
		{name: "different purpose", tenantID: "tenant-3", consumer: "ontology_workbench", purpose: "context_read"},
		{name: "different consumer", tenantID: "tenant-1", consumer: "other", purpose: "context_read"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := store.HasActivePurposeDeny(ctx, tt.tenantID, tt.consumer, tt.purpose)
			if err != nil {
				t.Fatalf("HasActivePurposeDeny: %v", err)
			}
			if got != tt.want {
				t.Fatalf("HasActivePurposeDeny = %v, want %v", got, tt.want)
			}
		})
	}
}
