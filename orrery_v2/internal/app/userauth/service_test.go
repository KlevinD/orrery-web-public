package userauth

import (
	"context"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/identity"
)

func TestServiceAuthenticatesSuperadminWithPassword(t *testing.T) {
	store := newMemoryAccountStore()
	if err := EnsureBootstrapSuperadmin(context.Background(), store, SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	service := NewService(store)
	challenge, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: "superadmin"})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}

	session, err := service.CompleteLogin(context.Background(), CompleteLoginRequest{
		Username:    "superadmin",
		ChallengeID: challenge.ChallengeID,
		Password:    SuperadminPassword(),
	})

	if err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}
	if session.User.Username != "superadmin" || session.User.DisplayName != "superadmin" {
		t.Fatalf("session user = %+v", session.User)
	}
	if session.User.AgentProfileID != SuperadminAgentProfileID {
		t.Fatalf("agent profile id = %q", session.User.AgentProfileID)
	}
	if !containsScope(session.PermissionScope, "operations.manage") || !containsScope(session.PermissionScope, "discussion.write") || !containsScope(session.PermissionScope, "approvals.manage") {
		t.Fatalf("permission scope = %+v", session.PermissionScope)
	}
	if session.AccessToken == "" {
		t.Fatalf("access token should be issued")
	}
}

func TestServiceBeginLoginFailsClosedWhenRandomSourceFails(t *testing.T) {
	store := newMemoryAccountStore()
	if err := EnsureBootstrapSuperadmin(context.Background(), store, SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	service := NewService(store)
	useFailingRandomReader(t)

	_, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: "superadmin"})

	if !errors.Is(err, ErrServiceDependency) {
		t.Fatalf("BeginLogin error = %v, want ErrServiceDependency", err)
	}
	if len(store.challenges) != 0 {
		t.Fatalf("login challenge should not be saved when random source fails: %+v", store.challenges)
	}
}

func TestServiceCompleteLoginFailsClosedWhenRandomSourceFails(t *testing.T) {
	store := newMemoryAccountStore()
	if err := EnsureBootstrapSuperadmin(context.Background(), store, SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	service := NewService(store)
	challenge, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: "superadmin"})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	useFailingRandomReader(t)

	_, err = service.CompleteLogin(context.Background(), CompleteLoginRequest{
		Username:    "superadmin",
		ChallengeID: challenge.ChallengeID,
		Password:    SuperadminPassword(),
	})

	if !errors.Is(err, ErrServiceDependency) {
		t.Fatalf("CompleteLogin error = %v, want ErrServiceDependency", err)
	}
	if len(store.sessions) != 0 {
		t.Fatalf("session should not be saved when random source fails: %+v", store.sessions)
	}
}

func TestAccountSetPasswordFailsClosedWhenRandomSourceFails(t *testing.T) {
	useFailingRandomReader(t)
	account := Account{Username: "operator"}

	err := account.SetPassword("correct-horse-1")

	if !errors.Is(err, ErrServiceDependency) {
		t.Fatalf("SetPassword error = %v, want ErrServiceDependency", err)
	}
	if account.PasswordSalt != "" || account.PasswordVerifier != "" {
		t.Fatalf("password fields should remain empty on random failure: salt=%q verifier=%q", account.PasswordSalt, account.PasswordVerifier)
	}
}

func TestEnsureBootstrapSuperadminPreservesExistingPassword(t *testing.T) {
	store := newMemoryAccountStore()
	existing := Account{
		UserID:         SuperadminUsername,
		Username:       SuperadminUsername,
		DisplayName:    SuperadminUsername,
		AgentProfileID: SuperadminAgentProfileID,
		Role:           "superadmin",
		Enabled:        true,
	}
	if err := existing.SetPassword("old-password"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if err := store.SaveAccount(context.Background(), existing); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}

	if err := EnsureBootstrapSuperadmin(context.Background(), store, SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	service := NewService(store)
	challenge, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: "superadmin"})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	_, err = service.CompleteLogin(context.Background(), CompleteLoginRequest{
		Username:    "superadmin",
		ChallengeID: challenge.ChallengeID,
		Password:    "old-password",
	})
	if err != nil {
		t.Fatalf("CompleteLogin with preserved password: %v", err)
	}
}

func TestEnsureBootstrapSuperadminRejectsConflictingProfileBinding(t *testing.T) {
	store := newMemoryAccountStore()
	existing := Account{
		UserID:         SuperadminUsername,
		Username:       SuperadminUsername,
		DisplayName:    SuperadminUsername,
		AgentProfileID: "human-attacker-controlled",
		Role:           "superadmin",
		Enabled:        true,
	}
	if err := existing.SetPassword("old-password-1"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if err := store.SaveAccount(context.Background(), existing); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}

	err := EnsureBootstrapSuperadmin(context.Background(), store, SuperadminPassword())
	if !errors.Is(err, ErrAccountConflict) {
		t.Fatalf("EnsureBootstrapSuperadmin error = %v, want ErrAccountConflict", err)
	}
}

func TestEnsureBootstrapSuperadminAddsNewDefaultScopesToExistingAccount(t *testing.T) {
	store := newMemoryAccountStore()
	existing := Account{
		UserID:          SuperadminUsername,
		Username:        SuperadminUsername,
		DisplayName:     SuperadminUsername,
		AgentProfileID:  SuperadminAgentProfileID,
		Role:            "superadmin",
		Enabled:         true,
		PermissionScope: []string{"custom.scope", "todo.read"},
	}
	if err := existing.SetPassword("old-password"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if err := store.SaveAccount(context.Background(), existing); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}

	if err := EnsureBootstrapSuperadmin(context.Background(), store, SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	account, err := store.GetAccountByUsername(context.Background(), SuperadminUsername)
	if err != nil {
		t.Fatalf("GetAccountByUsername: %v", err)
	}

	for _, want := range []string{"custom.scope", "todo.read", "todo.write", "idea.read", "idea.write", "knowledge.read", "web.read", "knowledge.manage", "memory.read", "memory.review", "memory.distill", "roles.read", "permissions.manage", "approvals.manage", "tool.call"} {
		if !containsScope(account.PermissionScope, want) {
			t.Fatalf("scope %q missing from %+v", want, account.PermissionScope)
		}
	}
}

func TestServiceAcceptsPlaintextLoginPayload(t *testing.T) {
	store := newMemoryAccountStore()
	if err := EnsureBootstrapSuperadmin(context.Background(), store, SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	service := NewService(store)
	challenge, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: "superadmin"})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}

	_, err = service.CompleteLogin(context.Background(), CompleteLoginRequest{
		Username:    "superadmin",
		ChallengeID: challenge.ChallengeID,
		Password:    SuperadminPassword(),
	})

	if err != nil {
		t.Fatalf("plaintext password payload should be accepted: %v", err)
	}
}

func TestServiceBeginLoginDoesNotRevealUnknownOrDisabledAccounts(t *testing.T) {
	store := newMemoryAccountStore()
	if err := EnsureBootstrapSuperadmin(context.Background(), store, SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	disabled := Account{Username: "disabled", AgentProfileID: "human-disabled", Enabled: false}
	if err := disabled.SetPassword("correct-horse-1"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if err := store.SaveAccount(context.Background(), disabled); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	service := NewService(store)

	known, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: SuperadminUsername})
	if err != nil {
		t.Fatalf("known BeginLogin: %v", err)
	}
	knownAgain, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: SuperadminUsername})
	if err != nil {
		t.Fatalf("second known BeginLogin: %v", err)
	}
	unknown, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: "missing"})
	if err != nil {
		t.Fatalf("unknown BeginLogin: %v", err)
	}
	unknownAgain, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: "missing"})
	if err != nil {
		t.Fatalf("second unknown BeginLogin: %v", err)
	}
	disabledChallenge, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: "disabled"})
	if err != nil {
		t.Fatalf("disabled BeginLogin: %v", err)
	}
	if len(known.Salt) != len(unknown.Salt) || len(known.Salt) != len(disabledChallenge.Salt) {
		t.Fatalf("challenge salt lengths reveal account state: known=%d unknown=%d disabled=%d", len(known.Salt), len(unknown.Salt), len(disabledChallenge.Salt))
	}
	superadmin, err := store.GetAccountByUsername(context.Background(), SuperadminUsername)
	if err != nil {
		t.Fatalf("GetAccountByUsername: %v", err)
	}
	if unknown.Salt == "" || known.Salt == superadmin.PasswordSalt || unknown.Salt == disabled.PasswordSalt || disabledChallenge.Salt == disabled.PasswordSalt {
		t.Fatalf("synthetic challenge salts must be non-empty and distinct from stored account salts")
	}
	if known.Salt == knownAgain.Salt || unknown.Salt == unknownAgain.Salt {
		t.Fatalf("all challenge salts must be unpredictable per challenge")
	}
	if _, err := service.CompleteLogin(context.Background(), CompleteLoginRequest{
		Username:    "missing",
		ChallengeID: unknown.ChallengeID,
		Password:    "correct-horse-1",
	}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown CompleteLogin error = %v, want ErrInvalidCredentials", err)
	}
}

func TestServiceDoesNotLockUsernameAcrossDifferentIPAddresses(t *testing.T) {
	store := newMemoryAccountStore()
	if err := EnsureBootstrapSuperadmin(context.Background(), store, SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	service := NewService(store)

	for attempt := 1; attempt <= maxFailedLoginsPerUsername; attempt++ {
		ipAddress := fmt.Sprintf("203.0.113.%d", attempt)
		challenge, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: SuperadminUsername, IPAddress: ipAddress})
		if err != nil {
			t.Fatalf("BeginLogin attempt %d: %v", attempt, err)
		}
		_, err = service.CompleteLogin(context.Background(), CompleteLoginRequest{
			Username:    SuperadminUsername,
			ChallengeID: challenge.ChallengeID,
			Password:    "wrong-password",
			IPAddress:   ipAddress,
		})
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("CompleteLogin attempt %d error = %v, want ErrInvalidCredentials", attempt, err)
		}
	}

	if _, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: SuperadminUsername, IPAddress: "203.0.113.100"}); err != nil {
		t.Fatalf("distributed failures must not lock the target username: %v", err)
	}
	if _, ok, err := store.GetUsernameLock(context.Background(), SuperadminUsername); err != nil || ok {
		t.Fatalf("username throttle record = ok:%t err:%v, want absent", ok, err)
	}
}

func TestServiceRejectsLoginChallengeFromDifferentIPAddress(t *testing.T) {
	store := NewMemoryAccountStore()
	if err := EnsureBootstrapSuperadmin(context.Background(), store, SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	service := NewService(store)
	challenge, err := service.BeginLogin(context.Background(), BeginLoginRequest{
		Username:  SuperadminUsername,
		IPAddress: "203.0.113.10",
	})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}

	_, err = service.CompleteLogin(context.Background(), CompleteLoginRequest{
		Username:    SuperadminUsername,
		ChallengeID: challenge.ChallengeID,
		Password:    SuperadminPassword(),
		IPAddress:   "203.0.113.11",
	})
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("CompleteLogin error = %v, want ErrInvalidCredentials", err)
	}
	if sessions, err := store.ListSessionsByUsername(context.Background(), SuperadminUsername); err != nil || len(sessions) != 0 {
		t.Fatalf("sessions after mismatched challenge IP = %+v err=%v", sessions, err)
	}
}

func TestServiceRejectsLoginWhenIPAddressLocksDuringPasswordVerification(t *testing.T) {
	base := NewMemoryAccountStore()
	if err := EnsureBootstrapSuperadmin(context.Background(), base, SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	store := &lockingDuringVerificationStore{MemoryAccountStore: base}
	service := NewService(store)
	challenge, err := service.BeginLogin(context.Background(), BeginLoginRequest{
		Username:  SuperadminUsername,
		IPAddress: "203.0.113.20",
	})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}

	_, err = service.CompleteLogin(context.Background(), CompleteLoginRequest{
		Username:    SuperadminUsername,
		ChallengeID: challenge.ChallengeID,
		Password:    SuperadminPassword(),
		IPAddress:   "203.0.113.20",
	})
	if !errors.Is(err, ErrIPLocked) {
		t.Fatalf("CompleteLogin error = %v, want ErrIPLocked", err)
	}
	if sessions, listErr := base.ListSessionsByUsername(context.Background(), SuperadminUsername); listErr != nil || len(sessions) != 0 {
		t.Fatalf("sessions after concurrent IP lock = %+v err=%v", sessions, listErr)
	}
}

func TestServiceDoesNotCreateUsernameFailureState(t *testing.T) {
	store := newMemoryAccountStore()
	if err := EnsureBootstrapSuperadmin(context.Background(), store, SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	operator := Account{Username: "operator", AgentProfileID: "human-operator", Enabled: true}
	if err := operator.SetPassword("correct-horse-1"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if err := store.SaveAccount(context.Background(), operator); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	service := NewService(store)

	for attempt := 1; attempt < maxFailedLoginsPerUsername; attempt++ {
		ipAddress := fmt.Sprintf("198.51.100.%d", attempt)
		challenge, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: SuperadminUsername, IPAddress: ipAddress})
		if err != nil {
			t.Fatalf("failed BeginLogin attempt %d: %v", attempt, err)
		}
		_, err = service.CompleteLogin(context.Background(), CompleteLoginRequest{
			Username:    SuperadminUsername,
			ChallengeID: challenge.ChallengeID,
			Password:    "wrong-password",
			IPAddress:   ipAddress,
		})
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("failed CompleteLogin attempt %d error = %v", attempt, err)
		}
	}

	operatorChallenge, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: "operator", IPAddress: "192.0.2.50"})
	if err != nil {
		t.Fatalf("operator BeginLogin: %v", err)
	}
	if _, err := service.CompleteLogin(context.Background(), CompleteLoginRequest{
		Username:    "operator",
		ChallengeID: operatorChallenge.ChallengeID,
		Password:    "correct-horse-1",
		IPAddress:   "192.0.2.50",
	}); err != nil {
		t.Fatalf("operator CompleteLogin: %v", err)
	}

	finalChallenge, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: SuperadminUsername, IPAddress: "192.0.2.51"})
	if err != nil {
		t.Fatalf("final BeginLogin: %v", err)
	}
	_, err = service.CompleteLogin(context.Background(), CompleteLoginRequest{
		Username:    SuperadminUsername,
		ChallengeID: finalChallenge.ChallengeID,
		Password:    "wrong-password",
		IPAddress:   "192.0.2.51",
	})
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("final CompleteLogin error = %v, want ErrInvalidCredentials", err)
	}
	if _, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: SuperadminUsername, IPAddress: "192.0.2.52"}); err != nil {
		t.Fatalf("target username must remain usable from a clean IP: %v", err)
	}
	if _, ok, err := store.GetUsernameLock(context.Background(), SuperadminUsername); err != nil || ok {
		t.Fatalf("username throttle record = ok:%t err:%v, want absent", ok, err)
	}
}

func TestPasswordPolicyRejectsWeakAndOversizedPasswords(t *testing.T) {
	for _, password := range []string{"short", strings.Repeat("x", MaximumPasswordLength+1)} {
		account := Account{Username: "operator"}
		if err := account.SetPassword(password); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("SetPassword length %d error = %v, want ErrInvalidRequest", len([]rune(password)), err)
		}
		if account.PasswordSalt != "" || account.PasswordVerifier != "" {
			t.Fatalf("rejected password changed credential fields")
		}
	}
}

func TestServiceRejectsOversizedLoginPasswordBeforeHashing(t *testing.T) {
	store := newMemoryAccountStore()
	if err := EnsureBootstrapSuperadmin(context.Background(), store, SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	service := NewService(store)
	challenge, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: SuperadminUsername})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	if _, err := service.CompleteLogin(context.Background(), CompleteLoginRequest{
		Username:    SuperadminUsername,
		ChallengeID: challenge.ChallengeID,
		Password:    strings.Repeat("x", MaximumPasswordLength+1),
	}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("CompleteLogin error = %v, want ErrInvalidCredentials", err)
	}
}

func TestServiceUpgradesLegacyPasswordVerifierAfterPlaintextLogin(t *testing.T) {
	store := newMemoryAccountStore()
	account := Account{
		UserID:         "human-legacy",
		Username:       "legacy",
		DisplayName:    "Legacy User",
		AgentProfileID: "human-legacy",
		Enabled:        true,
		PasswordSalt:   "legacy-salt",
	}
	account.PasswordVerifier = ClientPasswordDigest("old-password", account.PasswordSalt)
	if err := store.SaveAccount(context.Background(), account); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	service := NewService(store)
	challenge, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: "legacy"})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}

	if _, err := service.CompleteLogin(context.Background(), CompleteLoginRequest{
		Username:    "legacy",
		ChallengeID: challenge.ChallengeID,
		Password:    "old-password",
	}); err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}
	upgraded, err := store.GetAccountByUsername(context.Background(), "legacy")
	if err != nil {
		t.Fatalf("GetAccountByUsername: %v", err)
	}
	if PasswordVerifierNeedsUpgrade(upgraded.PasswordVerifier) {
		t.Fatalf("password verifier was not upgraded: %q", upgraded.PasswordVerifier)
	}
	if !PasswordVerifierMatches("old-password", upgraded.PasswordSalt, upgraded.PasswordVerifier) {
		t.Fatalf("upgraded password verifier should still match the original password")
	}
}

func TestServiceRequiresLegacyWeakPasswordResetBeforeAuthorization(t *testing.T) {
	store := newMemoryAccountStore()
	account := Account{
		UserID:         "human-legacy-weak",
		Username:       "legacy-weak",
		DisplayName:    "Legacy Weak User",
		AgentProfileID: "human-legacy-weak",
		Enabled:        true,
		PasswordSalt:   "legacy-salt",
	}
	account.PasswordVerifier = ClientPasswordDigest("weak-pass", account.PasswordSalt)
	if err := store.SaveAccount(context.Background(), account); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	service := NewService(store)
	challenge, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: account.Username})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	session, err := service.CompleteLogin(context.Background(), CompleteLoginRequest{
		Username:    account.Username,
		ChallengeID: challenge.ChallengeID,
		Password:    "weak-pass",
	})
	if err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}
	if !session.MustChangePassword || len(session.PermissionScope) != 0 {
		t.Fatalf("restricted session = %+v", session)
	}
	if _, err := service.AuthenticatePrincipal(context.Background(), session.AccessToken); !errors.Is(err, ErrPasswordChangeRequired) {
		t.Fatalf("AuthenticatePrincipal error = %v, want ErrPasswordChangeRequired", err)
	}
	if _, err := service.ListSessions(context.Background(), session.AccessToken); !errors.Is(err, ErrPasswordChangeRequired) {
		t.Fatalf("ListSessions error = %v, want ErrPasswordChangeRequired", err)
	}
	if err := service.RevokeSessionByID(context.Background(), session.AccessToken, session.SessionID); !errors.Is(err, ErrPasswordChangeRequired) {
		t.Fatalf("RevokeSessionByID error = %v, want ErrPasswordChangeRequired", err)
	}
	loaded, err := store.GetAccountByUsername(context.Background(), account.Username)
	if err != nil {
		t.Fatalf("GetAccountByUsername: %v", err)
	}
	if !PasswordVerifierNeedsUpgrade(loaded.PasswordVerifier) {
		t.Fatalf("weak legacy verifier should remain pending an explicit password reset")
	}
	replacement, err := service.ChangePassword(context.Background(), session.AccessToken, "replacement-password-1", "203.0.113.8", "test-browser")
	if err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if replacement.MustChangePassword {
		t.Fatalf("replacement session remains restricted: %+v", replacement)
	}
	upgraded, err := store.GetAccountByUsername(context.Background(), account.Username)
	if err != nil || PasswordVerifierNeedsUpgrade(upgraded.PasswordVerifier) || !PasswordVerifierMatches("replacement-password-1", upgraded.PasswordSalt, upgraded.PasswordVerifier) {
		t.Fatalf("upgraded account = %+v err=%v", upgraded, err)
	}
}

func TestServiceRejectsPasswordChangeFromUnrestrictedSession(t *testing.T) {
	store := newMemoryAccountStore()
	if err := EnsureBootstrapSuperadmin(context.Background(), store, SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	service := NewService(store)
	challenge, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: SuperadminUsername})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	session, err := service.CompleteLogin(context.Background(), CompleteLoginRequest{
		Username: SuperadminUsername, ChallengeID: challenge.ChallengeID, Password: SuperadminPassword(),
	})
	if err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}
	if _, err := service.ChangePassword(context.Background(), session.AccessToken, "replacement-password-1", "", ""); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("ChangePassword error = %v, want ErrInvalidRequest", err)
	}
	account, err := store.GetAccountByUsername(context.Background(), SuperadminUsername)
	if err != nil || !PasswordVerifierMatches(SuperadminPassword(), account.PasswordSalt, account.PasswordVerifier) {
		t.Fatalf("unrestricted password change mutated account: account=%+v err=%v", account, err)
	}
}

func TestServiceInvalidatesSessionWhenCredentialVersionChanges(t *testing.T) {
	ctx := context.Background()
	store := newMemoryAccountStore()
	if err := EnsureBootstrapSuperadmin(ctx, store, SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	service := NewService(store)
	challenge, err := service.BeginLogin(ctx, BeginLoginRequest{Username: SuperadminUsername})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	session, err := service.CompleteLogin(ctx, CompleteLoginRequest{
		Username: SuperadminUsername, ChallengeID: challenge.ChallengeID, Password: SuperadminPassword(),
	})
	if err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}
	account, err := store.GetAccountByUsername(ctx, SuperadminUsername)
	if err != nil {
		t.Fatalf("GetAccountByUsername: %v", err)
	}
	if err := account.SetPassword("credential-rotated-1"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if err := store.SaveAccount(ctx, account); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	if _, err := service.AuthenticateToken(ctx, session.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("AuthenticateToken after credential change error = %v, want ErrUnauthorized", err)
	}
}

func TestServiceRejectsSessionWithoutCredentialBinding(t *testing.T) {
	ctx := context.Background()
	store := newMemoryAccountStore()
	account := Account{Username: "operator", AgentProfileID: "human-operator", Enabled: true}
	if err := account.SetPassword("correct-horse-1"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if err := store.SaveAccount(ctx, account); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	legacy := SessionRecord{
		AccessToken: "iat-unbound",
		Username:    account.Username,
		ExpiresAt:   time.Now().UTC().Add(time.Hour),
	}
	if err := store.SaveSession(ctx, legacy); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	if _, err := NewService(store).AuthenticateToken(ctx, legacy.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("AuthenticateToken error = %v, want ErrUnauthorized", err)
	}
}

func TestServiceHidesPasswordValidityWhenRequiredMFAIsNotConfiguredForAccount(t *testing.T) {
	ctx := context.Background()
	store := newMemoryAccountStore()
	account := Account{Username: "operator", AgentProfileID: "human-operator", Enabled: true}
	if err := account.SetPassword("correct-horse-1"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if err := store.SaveAccount(ctx, account); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	validator, err := NewTOTPValidator(map[string]string{SuperadminUsername: secret})
	if err != nil {
		t.Fatalf("NewTOTPValidator: %v", err)
	}
	service := NewService(store).WithMFAValidator(validator, true)
	challenge, err := service.BeginLogin(ctx, BeginLoginRequest{Username: account.Username})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	if challenge.Salt == account.PasswordSalt {
		t.Fatalf("MFA-unconfigured account exposed its real password salt")
	}
	_, err = service.CompleteLogin(ctx, CompleteLoginRequest{
		Username: account.Username, ChallengeID: challenge.ChallengeID, Password: "correct-horse-1",
	})
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("CompleteLogin error = %v, want indistinguishable ErrInvalidCredentials", err)
	}
}

func TestServiceRejectsDisabledAccount(t *testing.T) {
	store := newMemoryAccountStore()
	account := Account{
		UserID:         "human-disabled",
		Username:       "disabled",
		DisplayName:    "Disabled",
		AgentProfileID: "human-disabled",
		Enabled:        false,
	}
	if err := account.SetPassword("correct-horse-1"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if err := store.SaveAccount(context.Background(), account); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	service := NewService(store)

	challenge, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: "disabled"})
	if err != nil {
		t.Fatalf("disabled account should receive a synthetic login challenge: %v", err)
	}
	if challenge.Salt == account.PasswordSalt {
		t.Fatalf("disabled account challenge must not expose the account password salt")
	}
	if _, err := service.CompleteLogin(context.Background(), CompleteLoginRequest{
		Username:    "disabled",
		ChallengeID: challenge.ChallengeID,
		Password:    "correct-horse-1",
	}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disabled account CompleteLogin error = %v, want ErrInvalidCredentials", err)
	}
}

func TestServiceLocksIPAfterThreeFailedPasswords(t *testing.T) {
	store := newMemoryAccountStore()
	if err := EnsureBootstrapSuperadmin(context.Background(), store, SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	service := NewService(store)

	for attempt := 1; attempt <= 3; attempt++ {
		challenge, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: "superadmin", IPAddress: "203.0.113.10"})
		if err != nil {
			t.Fatalf("BeginLogin attempt %d: %v", attempt, err)
		}
		_, err = service.CompleteLogin(context.Background(), CompleteLoginRequest{
			Username:    "superadmin",
			ChallengeID: challenge.ChallengeID,
			Password:    "wrong-password",
			IPAddress:   "203.0.113.10",
		})
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d error = %v, want invalid credentials", attempt, err)
		}
	}

	if _, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: "superadmin", IPAddress: "203.0.113.10"}); !errors.Is(err, ErrIPLocked) {
		t.Fatalf("locked IP BeginLogin error = %v, want ErrIPLocked", err)
	}
	locks, err := store.ListIPLocks(context.Background(), IPBlockQuery{})
	if err != nil {
		t.Fatalf("ListIPLocks: %v", err)
	}
	if len(locks.Locks) != 1 || locks.Locks[0].IPAddress != "203.0.113.10" || locks.Locks[0].FailureCount != 3 || locks.Locks[0].Status != "active" {
		t.Fatalf("locks = %+v", locks.Locks)
	}
}

func TestServiceAutomaticallyExpiresIPLock(t *testing.T) {
	store := newMemoryAccountStore()
	if err := EnsureBootstrapSuperadmin(context.Background(), store, SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	now := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	service := NewService(store)
	service.now = func() time.Time { return now }
	if err := store.SaveIPLock(context.Background(), IPBlockRecord{
		IPAddress:    "203.0.113.40",
		Status:       "active",
		FailureCount: 3,
		LockedAt:     now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("SaveIPLock: %v", err)
	}
	if _, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: "superadmin", IPAddress: "203.0.113.40"}); !errors.Is(err, ErrIPLocked) {
		t.Fatalf("active lock error = %v, want ErrIPLocked", err)
	}

	now = now.Add(failedLoginLockTTL)
	if _, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: "superadmin", IPAddress: "203.0.113.40"}); err != nil {
		t.Fatalf("expired lock should allow login challenge: %v", err)
	}
	if _, ok, err := store.GetIPLock(context.Background(), "203.0.113.40"); err != nil || ok {
		t.Fatalf("expired lock should be removed, ok=%v err=%v", ok, err)
	}
}

func TestServiceResetsFailedLoginCountOutsideWindow(t *testing.T) {
	store := newMemoryAccountStore()
	if err := EnsureBootstrapSuperadmin(context.Background(), store, SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	now := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	service := NewService(store)
	service.now = func() time.Time { return now }
	if err := store.SaveIPLock(context.Background(), IPBlockRecord{
		IPAddress:    "203.0.113.41",
		Status:       "tracking",
		FailureCount: 2,
		UpdatedAt:    now.Add(-failedLoginWindow),
	}); err != nil {
		t.Fatalf("SaveIPLock: %v", err)
	}

	if err := service.recordFailedLogin(context.Background(), "203.0.113.41", "superadmin", "invalid_password"); err != nil {
		t.Fatalf("recordFailedLogin: %v", err)
	}
	record, ok, err := store.GetIPLock(context.Background(), "203.0.113.41")
	if err != nil || !ok {
		t.Fatalf("GetIPLock ok=%v err=%v", ok, err)
	}
	if record.FailureCount != 1 || record.Status != "tracking" {
		t.Fatalf("record = %+v", record)
	}
}

func TestServiceSuccessfulLoginClearsIPFailureCount(t *testing.T) {
	store := newMemoryAccountStore()
	if err := EnsureBootstrapSuperadmin(context.Background(), store, SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	service := NewService(store)

	challenge, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: "superadmin", IPAddress: "203.0.113.11"})
	if err != nil {
		t.Fatalf("BeginLogin failed attempt: %v", err)
	}
	_, err = service.CompleteLogin(context.Background(), CompleteLoginRequest{
		Username:    "superadmin",
		ChallengeID: challenge.ChallengeID,
		Password:    "wrong-password",
		IPAddress:   "203.0.113.11",
	})
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("failed attempt error = %v, want invalid credentials", err)
	}

	challenge, err = service.BeginLogin(context.Background(), BeginLoginRequest{Username: "superadmin", IPAddress: "203.0.113.11"})
	if err != nil {
		t.Fatalf("BeginLogin success attempt: %v", err)
	}
	if _, err := service.CompleteLogin(context.Background(), CompleteLoginRequest{
		Username:    "superadmin",
		ChallengeID: challenge.ChallengeID,
		Password:    SuperadminPassword(),
		IPAddress:   "203.0.113.11",
	}); err != nil {
		t.Fatalf("CompleteLogin success attempt: %v", err)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		challenge, err = service.BeginLogin(context.Background(), BeginLoginRequest{Username: "superadmin", IPAddress: "203.0.113.11"})
		if err != nil {
			t.Fatalf("BeginLogin second failure series %d: %v", attempt, err)
		}
		_, err = service.CompleteLogin(context.Background(), CompleteLoginRequest{
			Username:    "superadmin",
			ChallengeID: challenge.ChallengeID,
			Password:    "wrong-password",
			IPAddress:   "203.0.113.11",
		})
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("second failure series %d error = %v, want invalid credentials", attempt, err)
		}
	}

	if _, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: "superadmin", IPAddress: "203.0.113.11"}); err != nil {
		t.Fatalf("IP should not be locked after two failures following success: %v", err)
	}
}

func TestServiceAuthenticatesBearerToken(t *testing.T) {
	store := newMemoryAccountStore()
	if err := EnsureBootstrapSuperadmin(context.Background(), store, SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	service := NewService(store)
	challenge, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: "superadmin"})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	session, err := service.CompleteLogin(context.Background(), CompleteLoginRequest{
		Username:    "superadmin",
		ChallengeID: challenge.ChallengeID,
		Password:    SuperadminPassword(),
	})
	if err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}

	authenticated, err := service.AuthenticateToken(context.Background(), session.AccessToken)

	if err != nil {
		t.Fatalf("AuthenticateToken: %v", err)
	}
	if authenticated.User.Username != "superadmin" || authenticated.User.DisplayName != "superadmin" {
		t.Fatalf("authenticated session = %+v", authenticated)
	}
}

func TestServiceAuthenticatesPrincipal(t *testing.T) {
	store := newMemoryAccountStore()
	if err := EnsureBootstrapSuperadmin(context.Background(), store, SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	service := NewService(store)
	challenge, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: "superadmin"})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	session, err := service.CompleteLogin(context.Background(), CompleteLoginRequest{
		Username:    "superadmin",
		ChallengeID: challenge.ChallengeID,
		Password:    SuperadminPassword(),
	})
	if err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}

	principal, err := service.AuthenticatePrincipal(context.Background(), "Bearer "+session.AccessToken)

	if err != nil {
		t.Fatalf("AuthenticatePrincipal: %v", err)
	}
	if !principal.Authenticated() || principal.SubjectProvider != identity.ProviderLocal || principal.Subject != "superadmin" || principal.Username != "superadmin" {
		t.Fatalf("principal = %+v", principal)
	}
	if principal.AgentProfileID != SuperadminAgentProfileID {
		t.Fatalf("agent profile id = %q", principal.AgentProfileID)
	}
	if !containsScope(principal.CoarseScopes, "operations.manage") || !principal.ExpiresAt.Equal(session.ExpiresAt) {
		t.Fatalf("principal scopes/expires = %+v", principal)
	}
}

func TestServiceRefreshesPermissionScopesWhenAuthenticatingSession(t *testing.T) {
	store := newMemoryAccountStore()
	if err := EnsureBootstrapSuperadmin(context.Background(), store, SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	service := NewService(store)
	challenge, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: "superadmin"})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	session, err := service.CompleteLogin(context.Background(), CompleteLoginRequest{
		Username:    "superadmin",
		ChallengeID: challenge.ChallengeID,
		Password:    SuperadminPassword(),
	})
	if err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}
	resolver := &fixedPermissionScopeResolver{scopes: []string{"resource.read", "permissions.read"}}
	service.WithPermissionScopeResolver(resolver)

	authenticated, err := service.AuthenticateToken(context.Background(), session.AccessToken)
	if err != nil {
		t.Fatalf("AuthenticateToken: %v", err)
	}
	if len(authenticated.PermissionScope) != 2 || authenticated.PermissionScope[0] != "resource.read" || authenticated.PermissionScope[1] != "permissions.read" {
		t.Fatalf("permission scope = %+v", authenticated.PermissionScope)
	}
	if resolver.principal.AgentProfileID != SuperadminAgentProfileID || !containsScope(resolver.accountScopes, "operations.manage") {
		t.Fatalf("resolver input principal=%+v account_scopes=%+v", resolver.principal, resolver.accountScopes)
	}
}

func TestServiceUsesSeparateAccountScopesForServerAuthorization(t *testing.T) {
	store := newMemoryAccountStore()
	if err := EnsureBootstrapSuperadmin(context.Background(), store, SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	service := NewService(store).WithPermissionScopeResolver(serverAuthorizationScopeResolver{})
	challenge, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: SuperadminUsername})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	session, err := service.CompleteLogin(context.Background(), CompleteLoginRequest{
		Username:    SuperadminUsername,
		ChallengeID: challenge.ChallengeID,
		Password:    SuperadminPassword(),
	})
	if err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}
	if !containsScope(session.PermissionScope, "ui.role.permission") {
		t.Fatalf("session UI scopes = %+v", session.PermissionScope)
	}

	principal, err := service.AuthenticatePrincipal(context.Background(), session.AccessToken)
	if err != nil {
		t.Fatalf("AuthenticatePrincipal: %v", err)
	}
	if len(principal.CoarseScopes) != 1 || principal.CoarseScopes[0] != "server.account.permission" {
		t.Fatalf("server principal scopes = %+v", principal.CoarseScopes)
	}
}

func TestLocalIdentityProviderReturnsExternalIdentityTraits(t *testing.T) {
	store := newMemoryAccountStore()
	if err := EnsureBootstrapSuperadmin(context.Background(), store, SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	service := NewService(store)
	challenge, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: "superadmin"})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	session, err := service.CompleteLogin(context.Background(), CompleteLoginRequest{
		Username:    "superadmin",
		ChallengeID: challenge.ChallengeID,
		Password:    SuperadminPassword(),
	})
	if err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}

	external, err := NewLocalIdentityProvider(service).VerifyAPIToken(context.Background(), session.AccessToken)

	if err != nil {
		t.Fatalf("VerifyAPIToken: %v", err)
	}
	if external.Provider != identity.ProviderLocal || external.Subject != "superadmin" || external.Traits["agent_profile_id"] != SuperadminAgentProfileID {
		t.Fatalf("external identity = %+v", external)
	}
}

func TestServiceRevokesSessionToken(t *testing.T) {
	store := newMemoryAccountStore()
	if err := EnsureBootstrapSuperadmin(context.Background(), store, SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	service := NewService(store)
	challenge, err := service.BeginLogin(context.Background(), BeginLoginRequest{Username: "superadmin"})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	session, err := service.CompleteLogin(context.Background(), CompleteLoginRequest{
		Username:    "superadmin",
		ChallengeID: challenge.ChallengeID,
		Password:    SuperadminPassword(),
	})
	if err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}

	if err := service.RevokeSession(context.Background(), session.AccessToken); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if _, err := service.AuthenticateToken(context.Background(), session.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("AuthenticateToken after revoke error = %v, want ErrUnauthorized", err)
	}
}

func TestServiceRevokesAllSessionsForUsername(t *testing.T) {
	ctx := context.Background()
	store := newMemoryAccountStore()
	if err := EnsureBootstrapSuperadmin(ctx, store, SuperadminPassword()); err != nil {
		t.Fatalf("EnsureBootstrapSuperadmin: %v", err)
	}
	service := NewService(store)
	sessions := make([]SessionDTO, 0, 2)
	for range 2 {
		challenge, err := service.BeginLogin(ctx, BeginLoginRequest{Username: SuperadminUsername})
		if err != nil {
			t.Fatalf("BeginLogin: %v", err)
		}
		session, err := service.CompleteLogin(ctx, CompleteLoginRequest{
			Username:    SuperadminUsername,
			ChallengeID: challenge.ChallengeID,
			Password:    SuperadminPassword(),
		})
		if err != nil {
			t.Fatalf("CompleteLogin: %v", err)
		}
		sessions = append(sessions, session)
	}

	if err := service.RevokeSessionsForUsername(ctx, " SUPERADMIN "); err != nil {
		t.Fatalf("RevokeSessionsForUsername: %v", err)
	}
	for _, session := range sessions {
		if _, err := service.AuthenticateToken(ctx, session.AccessToken); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("AuthenticateToken(%q) after bulk revoke error = %v, want ErrUnauthorized", session.AccessToken, err)
		}
	}
}

func TestServiceResolvesExternalIdentityAccountByAgentProfileIDBeforeUsername(t *testing.T) {
	ctx := context.Background()
	store := newMemoryAccountStore()
	profileAccount := Account{
		UserID:          "profile-account",
		Username:        "profile-owner",
		DisplayName:     "Profile Owner",
		AgentProfileID:  "human-profile-owner",
		Enabled:         true,
		PermissionScope: []string{"org.read"},
	}
	usernameAccount := Account{
		UserID:          "username-account",
		Username:        "colliding-username",
		DisplayName:     "Username Account",
		AgentProfileID:  "human-username-account",
		Enabled:         true,
		PermissionScope: []string{"permissions.manage"},
	}
	if err := store.SaveAccount(ctx, profileAccount); err != nil {
		t.Fatalf("SaveAccount profile: %v", err)
	}
	if err := store.SaveAccount(ctx, usernameAccount); err != nil {
		t.Fatalf("SaveAccount username: %v", err)
	}
	subjects := identity.NewMemorySubjectStore()
	service := NewService(store).WithIdentitySubjectStore(subjects)
	externalSubject := identity.IdentitySubject{
		ID:             "kratos:external-subject",
		Provider:       identity.ProviderKratos,
		Subject:        "external-subject",
		Username:       "colliding-username",
		AgentProfileID: "human-profile-owner",
		Status:         identity.SubjectStatusActive,
	}
	if err := subjects.SaveSubject(ctx, externalSubject); err != nil {
		t.Fatalf("SaveSubject: %v", err)
	}

	session, err := service.CreateSessionForIdentitySubject(ctx, externalSubject)
	if err != nil {
		t.Fatalf("CreateSessionForIdentitySubject: %v", err)
	}
	if session.User.Username != "profile-owner" || session.User.AgentProfileID != "human-profile-owner" {
		t.Fatalf("external identity resolved the username collision account: %+v", session.User)
	}
	if containsScope(session.PermissionScope, "permissions.manage") {
		t.Fatalf("colliding username account permissions leaked into session: %+v", session.PermissionScope)
	}
}

func TestServiceAllowsExplicitExternalIdentityBoundToSuperadminProfile(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryAccountStore()
	if err := store.SaveAccount(ctx, Account{
		UserID:          SuperadminUsername,
		Username:        SuperadminUsername,
		AgentProfileID:  SuperadminAgentProfileID,
		Enabled:         true,
		PermissionScope: SuperadminScopes(),
	}); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	subjects := identity.NewMemorySubjectStore()
	subject := identity.IdentitySubject{
		ID:             "kratos:break-glass",
		Provider:       identity.ProviderKratos,
		Subject:        "break-glass",
		Username:       SuperadminUsername,
		AgentProfileID: SuperadminAgentProfileID,
		Status:         identity.SubjectStatusActive,
	}
	if err := subjects.SaveSubject(ctx, subject); err != nil {
		t.Fatalf("SaveSubject: %v", err)
	}
	service := NewService(store).WithIdentitySubjectStore(subjects)

	session, err := service.CreateSessionForIdentitySubject(ctx, subject)
	if err != nil {
		t.Fatalf("CreateSessionForIdentitySubject: %v", err)
	}
	if session.User.Username != SuperadminUsername || session.User.AgentProfileID != SuperadminAgentProfileID || !containsScope(session.PermissionScope, "permissions.manage") {
		t.Fatalf("external superadmin session = %+v", session)
	}
	principal, err := service.PrincipalForIdentitySubject(ctx, subject, SuperadminScopes(), time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("PrincipalForIdentitySubject: %v", err)
	}
	if principal.Username != SuperadminUsername || principal.AgentProfileID != SuperadminAgentProfileID {
		t.Fatalf("external superadmin principal = %+v", principal)
	}
	usernameOnly := subject
	usernameOnly.ID = "kratos:break-glass-username"
	usernameOnly.Subject = "break-glass-username"
	usernameOnly.Username = SuperadminUsername
	usernameOnly.AgentProfileID = ""
	if _, err := service.CreateSessionForIdentitySubject(ctx, usernameOnly); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("CreateSessionForIdentitySubject username-only error = %v, want ErrUnauthorized", err)
	}
}

func TestServiceRejectsOtherExternalIdentitiesBoundToSuperadminProfile(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryAccountStore()
	if err := store.SaveAccount(ctx, Account{
		UserID:          SuperadminUsername,
		Username:        SuperadminUsername,
		AgentProfileID:  SuperadminAgentProfileID,
		Enabled:         true,
		PermissionScope: SuperadminScopes(),
	}); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	service := NewService(store).WithIdentitySubjectStore(identity.NewMemorySubjectStore())
	for _, subject := range []identity.IdentitySubject{
		{
			Provider:       identity.ProviderKratos,
			Subject:        "kratos-operator",
			Username:       "operator",
			AgentProfileID: SuperadminAgentProfileID,
			Status:         identity.SubjectStatusActive,
		},
		{
			Provider:       identity.ProviderOIDC,
			Subject:        "oidc-superadmin",
			Username:       SuperadminUsername,
			AgentProfileID: SuperadminAgentProfileID,
			Status:         identity.SubjectStatusActive,
		},
	} {
		if _, err := service.CreateSessionForIdentitySubject(ctx, subject); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("CreateSessionForIdentitySubject(%s/%s) error = %v, want ErrUnauthorized", subject.Provider, subject.Username, err)
		}
	}
}

type fixedPermissionScopeResolver struct {
	scopes        []string
	principal     identity.Principal
	accountScopes []string
}

type serverAuthorizationScopeResolver struct{}

func (serverAuthorizationScopeResolver) ResolvePermissionScopes(context.Context, identity.Principal, []string) ([]string, error) {
	return []string{"ui.role.permission"}, nil
}

func (serverAuthorizationScopeResolver) ResolveAuthorizationAccountScopes(context.Context, identity.Principal, []string) ([]string, error) {
	return []string{"server.account.permission"}, nil
}

func (r *fixedPermissionScopeResolver) ResolvePermissionScopes(_ context.Context, principal identity.Principal, accountScopes []string) ([]string, error) {
	r.principal = principal
	r.accountScopes = append([]string(nil), accountScopes...)
	return append([]string(nil), r.scopes...), nil
}

func containsScope(scopes []string, want string) bool {
	for _, scope := range scopes {
		if scope == want {
			return true
		}
	}
	return false
}

type failingRandomReader struct{}

type lockingDuringVerificationStore struct {
	*MemoryAccountStore
	lockChecks int
}

func (s *lockingDuringVerificationStore) GetIPLock(ctx context.Context, ipAddress string) (IPBlockRecord, bool, error) {
	s.lockChecks++
	if s.lockChecks >= 3 {
		now := time.Now().UTC()
		return IPBlockRecord{IPAddress: ipAddress, Status: "active", FailureCount: maxFailedLoginsPerIP, LockedAt: now, UpdatedAt: now}, true, nil
	}
	return s.MemoryAccountStore.GetIPLock(ctx, ipAddress)
}

func (failingRandomReader) Read(_ []byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func useFailingRandomReader(t *testing.T) {
	t.Helper()
	previous := secureRandomReader
	secureRandomReader = failingRandomReader{}
	t.Cleanup(func() {
		secureRandomReader = previous
	})
}
