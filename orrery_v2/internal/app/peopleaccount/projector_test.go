package peopleaccount

import (
	"context"
	"errors"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/migration"
	"orrery_v2/internal/platform/testkit"
)

func TestKratosProjectorReturnsInitialPasswordOnceWithoutPersistingIt(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	if err := migration.NewRunner([]migration.Migration{projectionMigration()}).Up(ctx, env.DB); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork).WithProfileSaveHook(NewProfileProjectionHook())
	profile := projectionTestProfile("human-quzheng")
	profile.UserAccount.PasswordSalt = ""
	profile.UserAccount.PasswordVerifier = ""
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save profile: %v", err)
	}
	client := &fakeKratosAccountClient{}
	oauth := &fakeOAuth2SessionRevoker{}
	projector := NewProjector(NewStore(env.DB), client).
		WithOAuth2SessionRevoker(oauth).
		WithPasswordGenerator(func() (string, error) { return "initial-password-123", nil })

	result, err := projector.ProjectLatest(ctx, profile.ID.String())
	if err != nil {
		t.Fatalf("ProjectLatest: %v", err)
	}
	if result.InitialPassword != "initial-password-123" || result.Revision != 1 || result.Status != ProjectionStatusApplied {
		t.Fatalf("projection result = %+v", result)
	}
	if client.createdCredential.HashedPassword == "" || client.createdCredential.HashedPassword == result.InitialPassword || !client.createdCredential.PasswordChangeRequired {
		t.Fatalf("created credential = %+v", client.createdCredential)
	}
	state, err := NewStore(env.DB).ProjectionState(ctx, profile.ID.String())
	if err != nil {
		t.Fatalf("ProjectionState: %v", err)
	}
	if state.Status != ProjectionStatusApplied || state.AppliedRevision != 1 || state.KratosIdentityID != "kratos-created" {
		t.Fatalf("state = %+v", state)
	}
	var statePayload, outboxPayload string
	if err := env.DB.QueryRow(ctx, `SELECT payload_json FROM people_account_projection_state WHERE person_id = ?`, profile.ID.String()).Scan(&statePayload); err != nil {
		t.Fatalf("read state payload: %v", err)
	}
	if err := env.DB.QueryRow(ctx, `SELECT payload_json FROM people_account_projection_outbox WHERE person_id = ?`, profile.ID.String()).Scan(&outboxPayload); err != nil {
		t.Fatalf("read outbox payload: %v", err)
	}
	if strings.Contains(statePayload, result.InitialPassword) || strings.Contains(outboxPayload, result.InitialPassword) {
		t.Fatalf("initial password persisted: state=%s outbox=%s", statePayload, outboxPayload)
	}

	again, err := projector.ProjectLatest(ctx, profile.ID.String())
	if err != nil {
		t.Fatalf("ProjectLatest again: %v", err)
	}
	if again.InitialPassword != "" || client.createCalls != 1 {
		t.Fatalf("second projection = %+v create_calls=%d", again, client.createCalls)
	}
	if oauth.subject != "" {
		t.Fatalf("enabled projection revoked oauth subject %q", oauth.subject)
	}
}

func TestGenerateInitialPasswordReturnsTwentyURLSafeCharacters(t *testing.T) {
	password, err := generateInitialPassword()
	if err != nil {
		t.Fatalf("generateInitialPassword: %v", err)
	}
	if len(password) != 20 {
		t.Fatalf("password length = %d, want 20", len(password))
	}
	for _, character := range password {
		if !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_", character) {
			t.Fatalf("password contains non URL-safe character %q", character)
		}
	}
}

func TestKratosProjectorRejectsIdentityOwnedByDifferentPerson(t *testing.T) {
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
	client := &fakeKratosAccountClient{identity: KratosIdentity{ID: "kratos-conflict", PersonID: "human-other"}, found: true}
	projector := NewProjector(NewStore(env.DB), client)

	_, err := projector.ProjectLatest(ctx, profile.ID.String())
	if !errors.Is(err, ErrProjectionConflict) {
		t.Fatalf("ProjectLatest error = %v, want ErrProjectionConflict", err)
	}
	state, stateErr := NewStore(env.DB).ProjectionState(ctx, profile.ID.String())
	if stateErr != nil {
		t.Fatalf("ProjectionState: %v", stateErr)
	}
	if state.Status != ProjectionStatusFailed || state.AppliedRevision != 0 {
		t.Fatalf("state = %+v", state)
	}
}

func TestKratosProjectorAdoptsIdentityWithoutPersonID(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	if err := migration.NewRunner([]migration.Migration{projectionMigration()}).Up(ctx, env.DB); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork).WithProfileSaveHook(NewProfileProjectionHook())
	profile := projectionTestProfile("human-ding")
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save profile: %v", err)
	}
	client := &fakeKratosAccountClient{identity: KratosIdentity{ID: "kratos-ding"}, found: true}
	projector := NewProjector(NewStore(env.DB), client).WithLegacyIdentityAdoption()

	result, err := projector.ProjectLatest(ctx, profile.ID.String())
	if err != nil {
		t.Fatalf("ProjectLatest: %v", err)
	}
	if result.Status != ProjectionStatusApplied || result.KratosIdentityID != "kratos-ding" {
		t.Fatalf("result = %+v", result)
	}
	if client.updated.PersonID != profile.ID.String() || client.createCalls != 0 {
		t.Fatalf("updated = %+v create_calls=%d", client.updated, client.createCalls)
	}
}

func TestKratosProjectorRejectsIdentityWithoutPersonIDByDefault(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	if err := migration.NewRunner([]migration.Migration{projectionMigration()}).Up(ctx, env.DB); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork).WithProfileSaveHook(NewProfileProjectionHook())
	profile := projectionTestProfile("human-ding")
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save profile: %v", err)
	}
	client := &fakeKratosAccountClient{identity: KratosIdentity{ID: "kratos-ding"}, found: true}

	if _, err := NewProjector(NewStore(env.DB), client).ProjectLatest(ctx, profile.ID.String()); !errors.Is(err, ErrProjectionConflict) {
		t.Fatalf("ProjectLatest error = %v, want ErrProjectionConflict", err)
	}
}

func TestKratosProjectorDisablesIdentityAndRevokesSessions(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	if err := migration.NewRunner([]migration.Migration{projectionMigration()}).Up(ctx, env.DB); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork).WithProfileSaveHook(NewProfileProjectionHook())
	profile := projectionTestProfile("human-quzheng")
	profile.Enabled = false
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save profile: %v", err)
	}
	client := &fakeKratosAccountClient{identity: KratosIdentity{ID: "kratos-quzheng", PersonID: "human-quzheng"}, found: true}
	oauth := &fakeOAuth2SessionRevoker{}
	projector := NewProjector(NewStore(env.DB), client).WithOAuth2SessionRevoker(oauth)

	if _, err := projector.ProjectLatest(ctx, profile.ID.String()); err != nil {
		t.Fatalf("ProjectLatest: %v", err)
	}
	if client.updated.Enabled || client.revokedIdentityID != "kratos-quzheng" || oauth.subject != "human-quzheng" {
		t.Fatalf("updated = %+v revoked = %q oauth_subject = %q", client.updated, client.revokedIdentityID, oauth.subject)
	}
}

func TestKratosProjectorAppliesDisabledProfileWithoutLoginAsNoop(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	if err := migration.NewRunner([]migration.Migration{projectionMigration()}).Up(ctx, env.DB); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork).WithProfileSaveHook(NewProfileProjectionHook())
	profile := projectionTestProfile("human-scheduler-audit")
	profile.Enabled = false
	profile.UserAccount = agentcore.UserAccountSpec{}
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save profile: %v", err)
	}
	client := &fakeKratosAccountClient{findErr: ErrProjectionConflict}
	store := NewStore(env.DB)

	result, err := NewProjector(store, client).ProjectLatest(ctx, profile.ID.String())
	if err != nil {
		t.Fatalf("ProjectLatest: %v", err)
	}
	if result.Status != ProjectionStatusApplied || result.KratosIdentityID != "" {
		t.Fatalf("result = %+v", result)
	}
	if client.findCalls != 0 || client.getCalls != 0 || client.createCalls != 0 || client.updated.PersonID != "" {
		t.Fatalf("Kratos calls: find=%d get=%d create=%d updated=%+v", client.findCalls, client.getCalls, client.createCalls, client.updated)
	}
	state, err := store.ProjectionState(ctx, profile.ID.String())
	if err != nil {
		t.Fatalf("ProjectionState: %v", err)
	}
	if state.Status != ProjectionStatusApplied || state.AppliedRevision != 1 || state.KratosIdentityID != "" || state.LastError != "" {
		t.Fatalf("state = %+v", state)
	}
}

func TestKratosProjectorFallsBackToUsernameWhenStoredIdentityIsMissing(t *testing.T) {
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
	client := &fakeKratosAccountClient{}
	projector := NewProjector(NewStore(env.DB), client)
	if _, err := projector.ProjectLatest(ctx, profile.ID.String()); err != nil {
		t.Fatalf("initial ProjectLatest: %v", err)
	}

	profile.DisplayName = "屈正（更新）"
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("update profile: %v", err)
	}
	client.getFound = boolPointer(false)
	client.findFound = boolPointer(true)
	client.identity = KratosIdentity{ID: "kratos-created", PersonID: profile.ID.String()}
	if _, err := projector.ProjectLatest(ctx, profile.ID.String()); err != nil {
		t.Fatalf("updated ProjectLatest: %v", err)
	}
	if client.getCalls != 1 || client.findCalls != 2 || client.createCalls != 1 {
		t.Fatalf("get=%d find=%d create=%d", client.getCalls, client.findCalls, client.createCalls)
	}
}

type fakeKratosAccountClient struct {
	identity          KratosIdentity
	found             bool
	findErr           error
	createErr         error
	updateErr         error
	createCalls       int
	createdCredential KratosCredential
	updated           Projection
	revokedIdentityID string
	getCalls          int
	findCalls         int
	getFound          *bool
	findFound         *bool
}

type fakeOAuth2SessionRevoker struct {
	subject string
}

func (r *fakeOAuth2SessionRevoker) RevokeSubjectSessions(_ context.Context, subject string) error {
	r.subject = subject
	return nil
}

func (c *fakeKratosAccountClient) FindIdentityByUsername(context.Context, string) (KratosIdentity, bool, error) {
	c.findCalls++
	found := c.found
	if c.findFound != nil {
		found = *c.findFound
	}
	return c.identity, found, c.findErr
}

func (c *fakeKratosAccountClient) GetIdentity(context.Context, string) (KratosIdentity, bool, error) {
	c.getCalls++
	found := c.found
	if c.getFound != nil {
		found = *c.getFound
	}
	return c.identity, found, c.findErr
}

func (c *fakeKratosAccountClient) CreateIdentity(_ context.Context, projection Projection, credential KratosCredential) (KratosIdentity, error) {
	c.createCalls++
	c.createdCredential = credential
	if c.createErr != nil {
		return KratosIdentity{}, c.createErr
	}
	c.identity = KratosIdentity{ID: "kratos-created", PersonID: projection.PersonID}
	c.found = true
	return c.identity, nil
}

func (c *fakeKratosAccountClient) UpdateIdentity(_ context.Context, identity KratosIdentity, projection Projection) (KratosIdentity, error) {
	c.updated = projection
	return identity, c.updateErr
}

func (c *fakeKratosAccountClient) RevokeIdentitySessions(_ context.Context, identityID string) error {
	c.revokedIdentityID = identityID
	return nil
}

func boolPointer(value bool) *bool {
	return &value
}
