package userauth

import (
	"context"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/app/identity"
)

func TestPasswordVerificationLimiterRejectsConcurrentWork(t *testing.T) {
	limiter := newPasswordVerificationLimiter(1, 1)
	release, err := limiter.acquire(context.Background(), "203.0.113.10")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer release()
	if _, err := limiter.acquire(context.Background(), "203.0.113.11"); !errors.Is(err, ErrLoginThrottled) {
		t.Fatalf("second acquire error = %v, want ErrLoginThrottled", err)
	}
}

func TestArgon2VerifierRejectsOversizedParametersAndDigest(t *testing.T) {
	salt := base64.RawStdEncoding.EncodeToString(make([]byte, passwordArgon2IDSaltBytes))
	digest := base64.RawStdEncoding.EncodeToString(make([]byte, passwordArgon2IDKeyBytes))
	overflow := "$argon2id$v=19$m=4294967297,t=3,p=2$" + salt + "$" + digest
	if PasswordVerifierMatches("password-value", salt, overflow) {
		t.Fatal("argon2 verifier accepted overflowing memory parameter")
	}
	oversizedDigest := "$argon2id$v=19$m=65536,t=3,p=2$" + salt + "$" + base64.RawStdEncoding.EncodeToString(make([]byte, passwordArgon2IDKeyBytes+1))
	if PasswordVerifierMatches("password-value", salt, oversizedDigest) {
		t.Fatal("argon2 verifier accepted oversized digest")
	}
}

func TestTOTPValidatorRejectsReplayWithinAcceptanceWindow(t *testing.T) {
	secret := []byte("12345678901234567890")
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
	validator, err := NewTOTPValidator(map[string]string{"operator": encoded})
	if err != nil {
		t.Fatalf("NewTOTPValidator: %v", err)
	}
	now := time.Now().UTC()
	code := totpCode(secret, uint64(now.Unix()/30))
	if err := validator.Validate(context.Background(), "operator", code, now); err != nil {
		t.Fatalf("first Validate: %v", err)
	}
	if err := validator.Validate(context.Background(), "operator", code, now.Add(10*time.Second)); !errors.Is(err, ErrMFAInvalid) {
		t.Fatalf("replayed Validate error = %v, want ErrMFAInvalid", err)
	}
}

func TestTOTPValidatorRejectsReplayAcrossValidatorInstances(t *testing.T) {
	secret := []byte("12345678901234567890")
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
	replayStore := &memoryTOTPReplayStore{counters: map[string]int64{}}
	first, err := NewTOTPValidator(map[string]string{"operator": encoded})
	if err != nil {
		t.Fatalf("NewTOTPValidator first: %v", err)
	}
	second, err := NewTOTPValidator(map[string]string{"operator": encoded})
	if err != nil {
		t.Fatalf("NewTOTPValidator second: %v", err)
	}
	first.WithReplayStore(replayStore)
	second.WithReplayStore(replayStore)
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	code := totpCode(secret, uint64(now.Unix()/30))
	if err := first.Validate(context.Background(), "operator", code, now); err != nil {
		t.Fatalf("first Validate: %v", err)
	}
	if err := second.Validate(context.Background(), "operator", code, now); !errors.Is(err, ErrMFAInvalid) {
		t.Fatalf("cross-instance replay error = %v, want ErrMFAInvalid", err)
	}
}

type memoryTOTPReplayStore struct {
	mu       sync.Mutex
	counters map[string]int64
}

func (s *memoryTOTPReplayStore) ConsumeTOTPCounter(_ context.Context, username string, counter int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if previous, ok := s.counters[username]; ok && counter <= previous {
		return false, nil
	}
	s.counters[username] = counter
	return true, nil
}

func TestLocalLoginRequiresConfiguredTOTP(t *testing.T) {
	secret := []byte("12345678901234567890")
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
	validator, err := NewTOTPValidator(map[string]string{"operator": encoded})
	if err != nil {
		t.Fatalf("NewTOTPValidator: %v", err)
	}
	store := newMemoryAccountStore()
	account := Account{Username: "operator", AgentProfileID: "human-operator", Enabled: true}
	if err := account.SetPassword("correct-horse-1"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if err := store.SaveAccount(context.Background(), account); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	now := time.Now().UTC()
	service := NewService(store).WithMFAValidator(validator, false)
	service.now = func() time.Time { return now }
	challenge, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: account.Username, IPAddress: "203.0.113.12"})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	code := totpCode(secret, uint64(now.Unix()/30))
	if _, err := service.CompleteLogin(context.Background(), CompleteLoginRequest{
		Username: account.Username, ChallengeID: challenge.ChallengeID, Password: "correct-horse-1", IPAddress: "203.0.113.12",
	}); !errors.Is(err, ErrMFARequired) {
		t.Fatalf("CompleteLogin without TOTP error = %v, want ErrMFARequired", err)
	}
	challenge, err = service.BeginLogin(context.Background(), BeginLoginRequest{Username: account.Username, IPAddress: "203.0.113.12"})
	if err != nil {
		t.Fatalf("BeginLogin with TOTP: %v", err)
	}
	if _, err := service.CompleteLogin(context.Background(), CompleteLoginRequest{
		Username: account.Username, ChallengeID: challenge.ChallengeID, Password: "correct-horse-1", MFACode: code, IPAddress: "203.0.113.12",
	}); err != nil {
		t.Fatalf("CompleteLogin with TOTP: %v", err)
	}
}

func TestLocalLoginOnlyRequiresTOTPForConfiguredAccount(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	validator, err := NewTOTPValidator(map[string]string{SuperadminUsername: secret})
	if err != nil {
		t.Fatalf("NewTOTPValidator: %v", err)
	}
	store := newMemoryAccountStore()
	account := Account{Username: "operator", AgentProfileID: "human-operator", Enabled: true}
	if err := account.SetPassword("correct-horse-1"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if err := store.SaveAccount(context.Background(), account); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	service := NewService(store).WithMFAValidator(validator, false)
	challenge, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: account.Username})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	if _, err := service.CompleteLogin(context.Background(), CompleteLoginRequest{
		Username: account.Username, ChallengeID: challenge.ChallengeID, Password: "correct-horse-1",
	}); err != nil {
		t.Fatalf("CompleteLogin for account without TOTP: %v", err)
	}
}

func TestLocalLoginPreservesTOTPReplayStoreFailure(t *testing.T) {
	secret := []byte("12345678901234567890")
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
	storeErr := errors.New("replay store unavailable")
	validator, err := NewTOTPValidator(map[string]string{"operator": encoded})
	if err != nil {
		t.Fatalf("NewTOTPValidator: %v", err)
	}
	validator.WithReplayStore(failingTOTPReplayStore{err: storeErr})
	store := newMemoryAccountStore()
	account := Account{Username: "operator", AgentProfileID: "human-operator", Enabled: true}
	if err := account.SetPassword("correct-horse-1"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if err := store.SaveAccount(context.Background(), account); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	service := NewService(store).WithMFAValidator(validator, true)
	service.now = func() time.Time { return now }
	challenge, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: account.Username, IPAddress: "203.0.113.12"})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	code := totpCode(secret, uint64(now.Unix()/30))
	_, err = service.CompleteLogin(context.Background(), CompleteLoginRequest{
		Username: account.Username, ChallengeID: challenge.ChallengeID, Password: "correct-horse-1", MFACode: code, IPAddress: "203.0.113.12",
	})
	if !errors.Is(err, ErrServiceDependency) || !strings.Contains(err.Error(), storeErr.Error()) {
		t.Fatalf("CompleteLogin error = %v, want replay store dependency error", err)
	}
}

type failingTOTPReplayStore struct {
	err error
}

func (s failingTOTPReplayStore) ConsumeTOTPCounter(context.Context, string, int64) (bool, error) {
	return false, s.err
}

func TestSessionTouchCannotResurrectConcurrentlyRevokedSession(t *testing.T) {
	ctx := context.Background()
	base := newMemoryAccountStore()
	store := &revokingSessionUpdateStore{MemoryAccountStore: base}
	account := Account{Username: "operator", AgentProfileID: "human-operator", Enabled: true}
	if err := account.SetPassword("correct-horse-1"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if err := store.SaveAccount(ctx, account); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	createdAt := time.Now().UTC()
	record, err := newSessionRecord(account, identity.ProviderLocal, account.Username, "", createdAt, false, "203.0.113.12", "security-test")
	if err != nil {
		t.Fatalf("newSessionRecord: %v", err)
	}
	if err := store.SaveSession(ctx, record); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	service := NewService(store)
	service.now = func() time.Time { return createdAt.Add(2 * time.Minute) }

	if _, err := service.AuthenticateToken(ctx, record.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("AuthenticateToken error = %v, want concurrent revocation denial", err)
	}
	if _, err := base.GetSession(ctx, record.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked session was recreated: %v", err)
	}
}

type revokingSessionUpdateStore struct {
	*MemoryAccountStore
}

func (s *revokingSessionUpdateStore) UpdateSession(ctx context.Context, record SessionRecord) error {
	if err := s.MemoryAccountStore.DeleteSession(ctx, record.AccessToken); err != nil {
		return err
	}
	return s.MemoryAccountStore.UpdateSession(ctx, record)
}

func TestSessionIdleTimeoutRotationAndDeviceRevocation(t *testing.T) {
	ctx := context.Background()
	store := newMemoryAccountStore()
	account := Account{Username: "operator", AgentProfileID: "human-operator", Enabled: true}
	if err := account.SetPassword("correct-horse-1"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if err := store.SaveAccount(ctx, account); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	now := time.Now().UTC()
	service := NewService(store)
	service.now = func() time.Time { return now }
	stale := SessionRecord{
		AccessToken: "iat-stale", Username: account.Username, SubjectProvider: "local", Subject: account.Username,
		CredentialVersion: accountCredentialVersion(account),
		SessionID:         "session-stale", CreatedAt: now.Add(-time.Hour), LastSeenAt: now.Add(-defaultSessionIdleTTL - time.Second), ExpiresAt: now.Add(time.Hour),
	}
	if err := store.SaveSession(ctx, stale); err != nil {
		t.Fatalf("SaveSession stale: %v", err)
	}
	if _, err := service.AuthenticatePrincipal(ctx, stale.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("stale AuthenticatePrincipal error = %v, want ErrUnauthorized", err)
	}
	active := SessionRecord{
		AccessToken: "iat-active", Username: account.Username, SubjectProvider: "local", Subject: account.Username,
		CredentialVersion: accountCredentialVersion(account),
		SessionID:         "session-active", CreatedAt: now.Add(-defaultSessionRotationAge), LastSeenAt: now, ExpiresAt: now.Add(time.Hour), IPAddress: "203.0.113.20", UserAgent: "browser-a",
	}
	if err := store.SaveSession(ctx, active); err != nil {
		t.Fatalf("SaveSession active: %v", err)
	}
	if stored, err := store.GetSession(ctx, active.AccessToken); err != nil {
		t.Fatalf("GetSession active after save: %v", err)
	} else if stored.CredentialVersion != accountCredentialVersion(account) {
		t.Fatalf("active credential version = %q, want %q", stored.CredentialVersion, accountCredentialVersion(account))
	}
	dto, err := service.AuthenticateToken(ctx, active.AccessToken)
	if err != nil || !dto.RotationRequired {
		t.Fatalf("AuthenticateToken dto=%+v err=%v", dto, err)
	}
	rotated, err := service.RotateSession(ctx, active.AccessToken, "203.0.113.21", "browser-b")
	if err != nil {
		t.Fatalf("RotateSession: %v", err)
	}
	page, err := service.ListSessions(ctx, rotated.AccessToken)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(page.Sessions) != 1 || !page.Sessions[0].Current || page.Sessions[0].IPAddress != "203.0.113.21" {
		t.Fatalf("sessions = %+v", page.Sessions)
	}
	if err := service.RevokeSessionByID(ctx, rotated.AccessToken, rotated.SessionID); err != nil {
		t.Fatalf("RevokeSessionByID: %v", err)
	}
	if _, err := service.AuthenticateToken(ctx, rotated.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked AuthenticateToken error = %v, want ErrUnauthorized", err)
	}
}

func TestConcurrentSessionRotationIssuesOnlyOneReplacement(t *testing.T) {
	ctx := context.Background()
	store := newMemoryAccountStore()
	account := Account{Username: "operator", AgentProfileID: "human-operator", Enabled: true}
	if err := account.SetPassword("correct-horse-1"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if err := store.SaveAccount(ctx, account); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	now := time.Now().UTC()
	service := NewService(store)
	service.now = func() time.Time { return now }
	current := SessionRecord{
		AccessToken: "iat-current", Username: account.Username, SubjectProvider: "local", Subject: account.Username,
		CredentialVersion: accountCredentialVersion(account), SessionID: "session-current", CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := store.SaveSession(ctx, current); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if _, err := store.GetSession(ctx, current.AccessToken); err != nil {
		t.Fatalf("GetSession current after save: %v", err)
	}

	results := make(chan SessionDTO, 2)
	errorsSeen := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			session, err := service.RotateSession(ctx, current.AccessToken, "203.0.113.20", "browser")
			results <- session
			errorsSeen <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errorsSeen)
	succeeded := 0
	rejected := 0
	var replacement SessionDTO
	for err := range errorsSeen {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrUnauthorized):
			rejected++
		default:
			t.Fatalf("RotateSession error = %v", err)
		}
	}
	for session := range results {
		if session.AccessToken != "" {
			replacement = session
		}
	}
	if succeeded != 1 || rejected != 1 || replacement.AccessToken == "" {
		t.Fatalf("succeeded=%d rejected=%d replacement=%+v", succeeded, rejected, replacement)
	}
	page, err := service.ListSessions(ctx, replacement.AccessToken)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(page.Sessions) != 1 || !page.Sessions[0].Current {
		t.Fatalf("sessions after concurrent rotation = %+v", page.Sessions)
	}
}

func TestRestrictedPasswordSessionCanBeConsumedOnlyOnce(t *testing.T) {
	ctx := context.Background()
	store := newMemoryAccountStore()
	account := Account{Username: "operator", AgentProfileID: "human-operator", Enabled: true}
	if err := account.SetPassword("old-password-1"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if err := store.SaveAccount(ctx, account); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	now := time.Now().UTC()
	service := NewService(store)
	service.now = func() time.Time { return now }
	current := SessionRecord{
		AccessToken: "iat-password-reset", Username: account.Username, SubjectProvider: "local", Subject: account.Username,
		CredentialVersion: accountCredentialVersion(account), SessionID: "session-password-reset", CreatedAt: now, LastSeenAt: now,
		ExpiresAt: now.Add(passwordChangeSessionTTL), MustChangePassword: true,
	}
	if err := store.SaveSession(ctx, current); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	type passwordResult struct {
		password string
		session  SessionDTO
		err      error
	}
	results := make(chan passwordResult, 2)
	var wg sync.WaitGroup
	for _, password := range []string{"new-password-a1", "new-password-b2"} {
		wg.Add(1)
		go func(password string) {
			defer wg.Done()
			session, err := service.ChangePassword(ctx, current.AccessToken, password, "203.0.113.20", "browser")
			results <- passwordResult{password: password, session: session, err: err}
		}(password)
	}
	wg.Wait()
	close(results)
	succeeded := 0
	rejected := 0
	winningPassword := ""
	var replacement SessionDTO
	for result := range results {
		switch {
		case result.err == nil:
			succeeded++
			winningPassword = result.password
			replacement = result.session
		case errors.Is(result.err, ErrUnauthorized):
			rejected++
		default:
			t.Fatalf("ChangePassword error = %v", result.err)
		}
	}
	if succeeded != 1 || rejected != 1 || replacement.AccessToken == "" {
		t.Fatalf("succeeded=%d rejected=%d replacement=%+v", succeeded, rejected, replacement)
	}
	stored, err := store.GetAccountByUsername(ctx, account.Username)
	if err != nil {
		t.Fatalf("GetAccountByUsername: %v", err)
	}
	if !PasswordVerifierMatches(winningPassword, stored.PasswordSalt, stored.PasswordVerifier) {
		t.Fatal("stored password does not match the successful password change")
	}
	if _, err := service.AuthenticateToken(ctx, replacement.AccessToken); err != nil {
		t.Fatalf("replacement session: %v", err)
	}
}
