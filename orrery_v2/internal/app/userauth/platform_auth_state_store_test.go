package userauth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestPlatformAuthStateStorePersistsChallengesAndHashedSessions(t *testing.T) {
	ctx := context.Background()
	sqlStore := openAuthStateTestStore(t, ctx)
	defer sqlStore.Close()
	store := NewPlatformAuthStateStore(sqlStore)
	now := time.Date(2026, 7, 11, 11, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }

	challenge := LoginChallenge{
		ChallengeID: "login-persistent",
		Username:    "operator",
		IPAddress:   "203.0.113.50",
		Salt:        "salt-1",
		ExpiresAt:   now.Add(5 * time.Minute),
	}
	if err := store.SaveLoginChallenge(ctx, challenge); err != nil {
		t.Fatalf("SaveLoginChallenge: %v", err)
	}
	consumed, err := NewPlatformAuthStateStore(sqlStore).ConsumeLoginChallenge(ctx, challenge.ChallengeID)
	if err != nil {
		t.Fatalf("ConsumeLoginChallenge: %v", err)
	}
	if consumed.Username != challenge.Username || consumed.IPAddress != challenge.IPAddress || !consumed.ExpiresAt.Equal(challenge.ExpiresAt) {
		t.Fatalf("consumed challenge = %+v", consumed)
	}
	if _, err := store.ConsumeLoginChallenge(ctx, challenge.ChallengeID); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("second consume error = %v, want ErrInvalidCredentials", err)
	}

	session := SessionRecord{
		AccessToken:       "iat-persistent-secret",
		Username:          "operator",
		SubjectProvider:   "kratos",
		Subject:           "subject-1",
		IdentitySubjectID: "kratos:subject-1",
		CredentialVersion: "credential-version-1",
		ExpiresAt:         now.Add(24 * time.Hour),
		SessionID:         "session-persistent",
		CreatedAt:         now,
		LastSeenAt:        now,
		IPAddress:         "203.0.113.50",
		UserAgent:         "test-browser",
	}
	if err := store.SaveSession(ctx, session); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	var persistedHash string
	if err := sqlStore.QueryRow(ctx, `SELECT token_hash FROM auth_app_sessions`).Scan(&persistedHash); err != nil {
		t.Fatalf("read persisted token hash: %v", err)
	}
	if persistedHash == session.AccessToken || persistedHash != appSessionTokenHash(session.AccessToken) {
		t.Fatalf("persisted token value = %q", persistedHash)
	}
	loadedStore := NewPlatformAuthStateStore(sqlStore)
	loadedStore.now = func() time.Time { return now }
	loaded, err := loadedStore.GetSession(ctx, session.AccessToken)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if loaded != session {
		t.Fatalf("loaded session = %+v, want %+v", loaded, session)
	}
	if err := store.DeleteSession(ctx, session.AccessToken); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := store.GetSession(ctx, session.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("GetSession after delete error = %v, want ErrUnauthorized", err)
	}
	session.LastSeenAt = now.Add(time.Minute)
	if err := store.UpdateSession(ctx, session); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("UpdateSession after delete error = %v, want ErrUnauthorized", err)
	}
	var sessionCount int
	if err := sqlStore.QueryRow(ctx, `SELECT COUNT(*) FROM auth_app_sessions`).Scan(&sessionCount); err != nil {
		t.Fatalf("count sessions after rejected update: %v", err)
	}
	if sessionCount != 0 {
		t.Fatalf("rejected session update recreated %d session rows", sessionCount)
	}
}

func TestPlatformAuthStateStoreConsumesChallengeOnlyOnceConcurrently(t *testing.T) {
	ctx := context.Background()
	sqlStore := openAuthStateTestStore(t, ctx)
	defer sqlStore.Close()
	store := NewPlatformAuthStateStore(sqlStore)
	if err := store.SaveLoginChallenge(ctx, LoginChallenge{
		ChallengeID: "login-single-use",
		Username:    "operator",
		Salt:        "salt-1",
		ExpiresAt:   time.Now().UTC().Add(time.Minute),
	}); err != nil {
		t.Fatalf("SaveLoginChallenge: %v", err)
	}

	var wg sync.WaitGroup
	errorsSeen := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.ConsumeLoginChallenge(ctx, "login-single-use")
			errorsSeen <- err
		}()
	}
	wg.Wait()
	close(errorsSeen)
	succeeded := 0
	rejected := 0
	for err := range errorsSeen {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrInvalidCredentials):
			rejected++
		default:
			t.Fatalf("consume error = %v", err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("succeeded=%d rejected=%d", succeeded, rejected)
	}
}

func TestPlatformAuthStateStoreReplacesSessionOnlyOnceConcurrently(t *testing.T) {
	ctx := context.Background()
	sqlStore := openAuthStateTestStore(t, ctx)
	defer sqlStore.Close()
	store := NewPlatformAuthStateStore(sqlStore)
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	current := SessionRecord{
		AccessToken: "iat-current", Username: "operator", CredentialVersion: "v1", ExpiresAt: now.Add(time.Hour),
		SessionID: "session-current", CreatedAt: now, LastSeenAt: now,
	}
	if err := store.SaveSession(ctx, current); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	replacements := []SessionRecord{
		{AccessToken: "iat-replacement-1", Username: "operator", CredentialVersion: "v1", ExpiresAt: current.ExpiresAt, SessionID: "session-replacement-1", CreatedAt: now, LastSeenAt: now},
		{AccessToken: "iat-replacement-2", Username: "operator", CredentialVersion: "v1", ExpiresAt: current.ExpiresAt, SessionID: "session-replacement-2", CreatedAt: now, LastSeenAt: now},
	}
	errorsSeen := make(chan error, len(replacements))
	var wg sync.WaitGroup
	for _, replacement := range replacements {
		wg.Add(1)
		go func(replacement SessionRecord) {
			defer wg.Done()
			errorsSeen <- NewPlatformAuthStateStore(sqlStore).ReplaceSession(ctx, current.AccessToken, replacement)
		}(replacement)
	}
	wg.Wait()
	close(errorsSeen)
	succeeded := 0
	rejected := 0
	for err := range errorsSeen {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrUnauthorized):
			rejected++
		default:
			t.Fatalf("ReplaceSession error = %v", err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("succeeded=%d rejected=%d", succeeded, rejected)
	}
	if _, err := store.GetSession(ctx, current.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("current session error = %v, want ErrUnauthorized", err)
	}
	activeReplacements := 0
	for _, replacement := range replacements {
		if _, err := store.GetSession(ctx, replacement.AccessToken); err == nil {
			activeReplacements++
		} else if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("GetSession(%q): %v", replacement.AccessToken, err)
		}
	}
	if activeReplacements != 1 {
		t.Fatalf("active replacement count = %d, want 1", activeReplacements)
	}
}

func TestPlatformAuthStateStoreDeletesSessionsByUsername(t *testing.T) {
	ctx := context.Background()
	sqlStore := openAuthStateTestStore(t, ctx)
	defer sqlStore.Close()
	store := NewPlatformAuthStateStore(sqlStore)
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	sessions := []SessionRecord{
		{AccessToken: "iat-operator-1", Username: "operator", ExpiresAt: now.Add(time.Hour)},
		{AccessToken: "iat-operator-2", Username: "Operator", ExpiresAt: now.Add(time.Hour)},
		{AccessToken: "iat-reviewer-1", Username: "reviewer", ExpiresAt: now.Add(time.Hour)},
	}
	for _, session := range sessions {
		if err := store.SaveSession(ctx, session); err != nil {
			t.Fatalf("SaveSession(%q): %v", session.AccessToken, err)
		}
	}

	if err := store.DeleteSessionsByUsername(ctx, " OPERATOR "); err != nil {
		t.Fatalf("DeleteSessionsByUsername: %v", err)
	}
	for _, token := range []string{"iat-operator-1", "iat-operator-2"} {
		if _, err := store.GetSession(ctx, token); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("GetSession(%q) error = %v, want ErrUnauthorized", token, err)
		}
	}
	if _, err := store.GetSession(ctx, "iat-reviewer-1"); err != nil {
		t.Fatalf("unrelated reviewer session was revoked: %v", err)
	}
}

func TestPlatformAuthStateStorePersistsUsernameThrottle(t *testing.T) {
	ctx := context.Background()
	sqlStore := openAuthStateTestStore(t, ctx)
	defer sqlStore.Close()
	store := NewPlatformAuthStateStore(sqlStore)
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	for attempt := 1; attempt <= maxFailedLoginsPerUsername; attempt++ {
		if err := store.RecordUsernameFailure(ctx, " Operator ", "invalid_password", now.Add(time.Duration(attempt)*time.Second), failedLoginWindow, maxFailedLoginsPerUsername); err != nil {
			t.Fatalf("RecordUsernameFailure attempt %d: %v", attempt, err)
		}
	}

	record, ok, err := NewPlatformAuthStateStore(sqlStore).GetUsernameLock(ctx, "operator")
	if err != nil {
		t.Fatalf("GetUsernameLock: %v", err)
	}
	if !ok || record.Status != "active" || record.FailureCount != maxFailedLoginsPerUsername || record.LockedAt.IsZero() {
		t.Fatalf("persisted username throttle = %+v, ok=%v", record, ok)
	}
}

func TestPlatformAuthStateStoreConsumesTOTPCounterOnce(t *testing.T) {
	ctx := context.Background()
	sqlStore := openAuthStateTestStore(t, ctx)
	defer sqlStore.Close()
	first := NewPlatformAuthStateStore(sqlStore)
	second := NewPlatformAuthStateStore(sqlStore)

	accepted, err := first.ConsumeTOTPCounter(ctx, " Operator ", 100)
	if err != nil || !accepted {
		t.Fatalf("first ConsumeTOTPCounter accepted=%v err=%v", accepted, err)
	}
	for _, counter := range []int64{100, 99} {
		accepted, err = second.ConsumeTOTPCounter(ctx, "operator", counter)
		if err != nil || accepted {
			t.Fatalf("replay counter %d accepted=%v err=%v", counter, accepted, err)
		}
	}
	accepted, err = second.ConsumeTOTPCounter(ctx, "operator", 101)
	if err != nil || !accepted {
		t.Fatalf("newer counter accepted=%v err=%v", accepted, err)
	}
}

func TestPlatformAuthStateStoreConsumesTOTPCounterAtomically(t *testing.T) {
	ctx := context.Background()
	sqlStore := openAuthStateTestStore(t, ctx)
	defer sqlStore.Close()
	stores := []*PlatformAuthStateStore{NewPlatformAuthStateStore(sqlStore), NewPlatformAuthStateStore(sqlStore)}
	results := make(chan bool, len(stores))
	errorsSeen := make(chan error, len(stores))
	var wg sync.WaitGroup
	for _, store := range stores {
		wg.Add(1)
		go func(store *PlatformAuthStateStore) {
			defer wg.Done()
			accepted, err := store.ConsumeTOTPCounter(ctx, "operator", 200)
			results <- accepted
			errorsSeen <- err
		}(store)
	}
	wg.Wait()
	close(results)
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("ConsumeTOTPCounter: %v", err)
		}
	}
	acceptedCount := 0
	for accepted := range results {
		if accepted {
			acceptedCount++
		}
	}
	if acceptedCount != 1 {
		t.Fatalf("accepted count = %d, want 1", acceptedCount)
	}
}

func TestPlatformAuthStateStoreCleansExpiredUsernameThrottles(t *testing.T) {
	ctx := context.Background()
	sqlStore := openAuthStateTestStore(t, ctx)
	defer sqlStore.Close()
	store := NewPlatformAuthStateStore(sqlStore)
	now := time.Date(2026, 7, 13, 13, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	if err := store.SaveUsernameLock(ctx, UsernameBlockRecord{
		Username:     "stale-user",
		Status:       "tracking",
		FailureCount: 1,
		UpdatedAt:    now.Add(-failedLoginWindow),
	}); err != nil {
		t.Fatalf("SaveUsernameLock: %v", err)
	}
	if err := store.SaveLoginChallenge(ctx, LoginChallenge{
		ChallengeID: "fresh-challenge",
		Username:    "fresh-user",
		Salt:        "salt",
		ExpiresAt:   now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("SaveLoginChallenge: %v", err)
	}
	if _, ok, err := store.GetUsernameLock(ctx, "stale-user"); err != nil || ok {
		t.Fatalf("stale username throttle was not cleaned, ok=%v err=%v", ok, err)
	}
}

func TestAuthStateStoresDeleteExpiredSessionsWhenRead(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	memory := NewMemoryAccountStore()
	if err := memory.SaveSession(ctx, SessionRecord{AccessToken: "iat-expired-memory", Username: "operator", ExpiresAt: now.Add(-time.Minute)}); err != nil {
		t.Fatalf("memory SaveSession: %v", err)
	}
	if _, err := memory.GetSession(ctx, "iat-expired-memory"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("memory GetSession error = %v, want ErrUnauthorized", err)
	}
	if len(memory.sessions) != 0 {
		t.Fatalf("expired memory session was not deleted")
	}

	sqlStore := openAuthStateTestStore(t, ctx)
	defer sqlStore.Close()
	platform := NewPlatformAuthStateStore(sqlStore)
	platform.now = func() time.Time { return now.Add(-2 * time.Minute) }
	if err := platform.SaveSession(ctx, SessionRecord{AccessToken: "iat-expired-platform", Username: "operator", ExpiresAt: now.Add(-time.Minute)}); err != nil {
		t.Fatalf("platform SaveSession: %v", err)
	}
	platform.now = func() time.Time { return now }
	if _, err := platform.GetSession(ctx, "iat-expired-platform"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("platform GetSession error = %v, want ErrUnauthorized", err)
	}
	var count int
	if err := sqlStore.QueryRow(ctx, `SELECT COUNT(*) FROM auth_app_sessions`).Scan(&count); err != nil {
		t.Fatalf("count app sessions: %v", err)
	}
	if count != 0 {
		t.Fatalf("expired platform session row count = %d", count)
	}
}

func TestServiceLoginAndSessionSurviveServiceRecreation(t *testing.T) {
	ctx := context.Background()
	sqlStore := openAuthStateTestStore(t, ctx)
	defer sqlStore.Close()
	accounts := NewMemoryAccountStore()
	if err := EnsureBootstrapSuperadmin(ctx, accounts, SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	firstStore := NewSplitAccountStore(accounts, accounts).WithAuthStateStore(NewPlatformAuthStateStore(sqlStore))
	firstService := NewService(firstStore)
	challenge, err := firstService.BeginLogin(ctx, BeginLoginRequest{Username: "superadmin", IPAddress: "203.0.113.60"})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}

	secondStore := NewSplitAccountStore(accounts, accounts).WithAuthStateStore(NewPlatformAuthStateStore(sqlStore))
	secondService := NewService(secondStore)
	session, err := secondService.CompleteLogin(ctx, CompleteLoginRequest{
		Username:    "superadmin",
		ChallengeID: challenge.ChallengeID,
		Password:    SuperadminPassword(),
		IPAddress:   "203.0.113.60",
	})
	if err != nil {
		t.Fatalf("CompleteLogin after recreation: %v", err)
	}

	thirdService := NewService(NewSplitAccountStore(accounts, accounts).WithAuthStateStore(NewPlatformAuthStateStore(sqlStore)))
	authenticated, err := thirdService.AuthenticateToken(ctx, session.AccessToken)
	if err != nil {
		t.Fatalf("AuthenticateToken after recreation: %v", err)
	}
	if authenticated.User.Username != "superadmin" || authenticated.AccessToken != session.AccessToken {
		t.Fatalf("authenticated session = %+v", authenticated)
	}
}

func openAuthStateTestStore(t *testing.T, ctx context.Context) *db.Store {
	t.Helper()
	store, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := migration.NewRunner([]migration.Migration{authStateMigration(), authThrottleMigration(), authSessionMetadataMigration(), authSessionCredentialVersionMigration(), authMFAReplayCounterMigration()}).Up(ctx, store); err != nil {
		store.Close()
		t.Fatalf("migration up: %v", err)
	}
	return store
}
