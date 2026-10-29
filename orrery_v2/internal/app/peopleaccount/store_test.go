package peopleaccount

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/migration"
	"orrery_v2/internal/platform/testkit"
)

func TestProfileProjectionHookIncrementsRevisionAndSanitizesPayload(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	if err := migration.NewRunner([]migration.Migration{projectionMigration()}).Up(ctx, env.DB); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	store := NewStore(env.DB)
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork).WithProfileSaveHook(NewProfileProjectionHook())
	profile := projectionTestProfile("human-quzheng")
	profile.PersonSource = agentcore.PersonSourceFacts{
		EmployeeNumber: "E-001", Source: "people.feishu", SourceRecordID: "ou_quzheng", SourceTenantID: "tenant-1",
	}

	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save initial profile: %v validation=%+v", err, agentcore.ValidateAgentProfile(profile))
	}
	first, err := store.LatestProjection(ctx, profile.ID.String())
	if err != nil {
		t.Fatalf("LatestProjection initial: %v", err)
	}
	if first.Revision != 1 || first.PersonID != "human-quzheng" || first.Username != "quzheng" || first.FeishuSubject != "ou_quzheng" || !first.Enabled {
		t.Fatalf("first projection = %+v", first)
	}
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save unchanged profile: %v", err)
	}
	profile.DisplayName = "屈正（产品）"
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save changed profile: %v", err)
	}
	latest, err := store.LatestProjection(ctx, profile.ID.String())
	if err != nil {
		t.Fatalf("LatestProjection changed: %v", err)
	}
	if latest.Revision != 2 || latest.DisplayName != profile.DisplayName {
		t.Fatalf("latest projection = %+v", latest)
	}

	var count int
	if err := env.DB.QueryRow(ctx, `SELECT COUNT(*) FROM people_account_projection_outbox WHERE person_id = ?`, profile.ID.String()).Scan(&count); err != nil {
		t.Fatalf("count projection outbox: %v", err)
	}
	if count != 2 {
		t.Fatalf("outbox count = %d, want 2", count)
	}
	rows, err := env.DB.Query(ctx, `SELECT payload_json FROM people_account_projection_outbox WHERE person_id = ?`, profile.ID.String())
	if err != nil {
		t.Fatalf("query projection payloads: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			t.Fatalf("scan projection payload: %v", err)
		}
		for _, forbidden := range []string{"salt-secret", "verifier-secret", "password_salt", "password_verifier", "legacy_password"} {
			if strings.Contains(strings.ToLower(payload), forbidden) {
				t.Fatalf("projection payload contains %q: %s", forbidden, payload)
			}
		}
	}
}

func TestProfileProjectionHookFailureRollsBackProfile(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork).WithProfileSaveHook(NewProfileProjectionHook())
	profile := projectionTestProfile("human-missing-migration")

	if err := profiles.SaveProfile(ctx, profile); err == nil {
		t.Fatal("SaveProfile error = nil, want missing projection table error")
	}
	if _, err := profiles.GetProfile(ctx, profile.ID); !errors.Is(err, agentcore.ErrNotFound) {
		t.Fatalf("GetProfile error = %v, want ErrNotFound", err)
	}
}

func TestStoreEnsureProfileProjectionSeedsLegacyProfileOnce(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	if err := migration.NewRunner([]migration.Migration{projectionMigration()}).Up(ctx, env.DB); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	profile := projectionTestProfile("human-legacy")
	profile.UserAccount.PasswordVerifier = "$2a$legacy-hash"
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save legacy profile: %v", err)
	}
	store := NewStore(env.DB)

	created, err := store.EnsureProfileProjection(ctx, profile)
	if err != nil {
		t.Fatalf("EnsureProfileProjection first: %v", err)
	}
	if !created {
		t.Fatal("EnsureProfileProjection first created = false")
	}
	created, err = store.EnsureProfileProjection(ctx, profile)
	if err != nil {
		t.Fatalf("EnsureProfileProjection second: %v", err)
	}
	if created {
		t.Fatal("EnsureProfileProjection second created = true")
	}
	state, err := store.ProjectionState(ctx, profile.ID.String())
	if err != nil {
		t.Fatalf("ProjectionState: %v", err)
	}
	if state.Projection.PersonID != profile.ID.String() || state.Projection.Revision != 1 || state.Projection.LegacyPassword != "" {
		t.Fatalf("projection state = %+v", state)
	}
	var count int
	if err := env.DB.QueryRow(ctx, `SELECT COUNT(*) FROM people_account_projection_outbox WHERE person_id = ?`, profile.ID.String()).Scan(&count); err != nil {
		t.Fatalf("count projection outbox: %v", err)
	}
	if count != 1 {
		t.Fatalf("projection outbox count = %d, want 1", count)
	}
}

func TestProjectionStoreReclaimsStaleProcessingClaim(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	if err := migration.NewRunner([]migration.Migration{projectionMigration()}).Up(ctx, env.DB); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork).WithProfileSaveHook(NewProfileProjectionHook())
	profile := projectionTestProfile("human-quzheng")
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save profile: %v", err)
	}
	store := NewStore(env.DB)
	if claimed, err := store.ClaimLatest(ctx, profile.ID.String(), 1); err != nil || !claimed {
		t.Fatalf("first claim = %t, err=%v", claimed, err)
	}
	if claimed, err := store.ClaimLatest(ctx, profile.ID.String(), 1); err != nil || claimed {
		t.Fatalf("active claim = %t, err=%v", claimed, err)
	}
	staleAt := time.Now().UTC().Add(-projectionClaimTimeout - time.Second).UnixMilli()
	if _, err := env.DB.Exec(ctx, `UPDATE people_account_projection_state SET updated_at = ? WHERE person_id = ?`, staleAt, profile.ID.String()); err != nil {
		t.Fatalf("age state claim: %v", err)
	}
	if _, err := env.DB.Exec(ctx, `UPDATE people_account_projection_outbox SET updated_at = ? WHERE person_id = ?`, staleAt, profile.ID.String()); err != nil {
		t.Fatalf("age outbox claim: %v", err)
	}
	pending, err := store.PendingPersonIDs(ctx, 10)
	if err != nil {
		t.Fatalf("PendingPersonIDs: %v", err)
	}
	if len(pending) != 1 || pending[0] != profile.ID.String() {
		t.Fatalf("pending = %v", pending)
	}
	if claimed, err := store.ClaimLatest(ctx, profile.ID.String(), 1); err != nil || !claimed {
		t.Fatalf("stale claim = %t, err=%v", claimed, err)
	}
}

func projectionTestProfile(id agentcore.AgentProfileID) agentcore.AgentProfile {
	return agentcore.AgentProfile{
		ID:          id,
		RealName:    "屈正",
		DisplayName: "屈正",
		Email:       "quzheng@example.com",
		AgentKind:   agentcore.AgentKindHuman,
		UserAccount: agentcore.UserAccountSpec{
			Username:         "quzheng",
			Email:            "quzheng@example.com",
			AccountEnabled:   true,
			PermissionScope:  []string{"basic.user", "member"},
			PasswordSalt:     "salt-secret",
			PasswordVerifier: "verifier-secret",
		},
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef:   agentcore.OrganizationRef{ID: "minvia"},
			DepartmentRef:     agentcore.DepartmentRef{ID: "product"},
			PositionRef:       agentcore.PositionRef{ID: "product-manager"},
			EmploymentType:    agentcore.EmploymentTypeHuman,
			SupervisionPolicy: agentcore.SupervisionPolicyRoot,
		},
		Persona: agentcore.AgentPersona{ResponsibilityBoundary: "负责产品工作。"},
		BaseAgent: agentcore.BaseAgentSpec{
			InheritsBaseAgent: true,
			AuditRequired:     true,
			SystemPrompt:      "人员账号",
			LLMAPIKeyRef:      "secret://system/llm-api-key",
			SkillScope:        "profile_bound",
			ToolScope:         "profile_bound",
			MCPScope:          "profile_bound",
		},
		PersonalMemoryProfileRef: agentcore.MemoryProfileRef{
			MemoryProfileID: agentcore.MemoryProfileID("memory-" + id.String()),
			OwnerAgentID:    agentcore.AgentID(id),
			MemoryKind:      agentcore.MemoryKindPersonal,
		},
		SkillRefs: []agentcore.SkillRef{},
		ToolRefs:  []agentcore.ToolRef{},
		Enabled:   true,
	}
}
