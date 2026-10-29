package userauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
	"orrery_v2/internal/app/identity"
)

type Service struct {
	store                   AccountStore
	identitySubjects        identity.SubjectStore
	scopeResolver           PermissionScopeResolver
	externalPasswordChanger ExternalPasswordChanger
	passwordLimiter         *passwordVerificationLimiter
	mfaValidator            MFAValidator
	requireLocalMFA         bool
	now                     func() time.Time
}

type ExternalPasswordChanger interface {
	ChangeInitialPassword(ctx context.Context, subject, newPassword string) error
}

type PermissionScopeResolver interface {
	ResolvePermissionScopes(ctx context.Context, principal identity.Principal, accountScopes []string) ([]string, error)
}

type AuthorizationAccountScopeResolver interface {
	ResolveAuthorizationAccountScopes(ctx context.Context, principal identity.Principal, accountScopes []string) ([]string, error)
}

const (
	maxFailedLoginsPerIP       = 3
	maxFailedLoginsPerUsername = 5 // retained for legacy throttle data migration and diagnostics only
	failedLoginWindow          = 15 * time.Minute
	failedLoginLockTTL         = 15 * time.Minute
)

func NewService(store AccountStore) *Service {
	return &Service{
		store:           store,
		passwordLimiter: newPasswordVerificationLimiter(defaultPasswordVerificationConcurrency, defaultPasswordVerificationConcurrencyPerIP),
		now:             func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) WithIdentitySubjectStore(store identity.SubjectStore) *Service {
	if s != nil {
		s.identitySubjects = store
	}
	return s
}

func (s *Service) WithPermissionScopeResolver(resolver PermissionScopeResolver) *Service {
	if s != nil {
		s.scopeResolver = resolver
	}
	return s
}

func (s *Service) WithExternalPasswordChanger(changer ExternalPasswordChanger) *Service {
	if s != nil {
		s.externalPasswordChanger = changer
	}
	return s
}

func (s *Service) WithPasswordVerificationConcurrency(globalLimit, perIPLimit int) *Service {
	if s != nil {
		s.passwordLimiter = newPasswordVerificationLimiter(globalLimit, perIPLimit)
	}
	return s
}

func (s *Service) WithMFAValidator(validator MFAValidator, required bool) *Service {
	if s != nil {
		s.mfaValidator = validator
		s.requireLocalMFA = required
	}
	return s
}

func SuperadminPassword() string {
	return "orrery-dev-password"
}

func SuperadminScopes() []string {
	return []string{
		"mission.read",
		"mission.write",
		"resource.read",
		"resource.manage",
		"operations.read",
		"operations.manage",
		"people.manage",
		"org.read",
		"org.manage",
		"workforce.read",
		"workforce.manage",
		"roles.read",
		"roles.manage",
		"permissions.read",
		"permissions.manage",
		"audit.read",
		"audit.manage",
		"agent.run",
		"agent.trace.read",
		"agent.trace.manage",
		"tool.call",
		"approvals.read",
		"approvals.decide",
		"approvals.manage",
		"legal.manage",
		"legal.mcp",
		"project.read",
		"project.create",
		"project.write",
		"project.manage",
		"development.read",
		"development.monitor",
		"development.create",
		"development.write",
		"development.execute",
		"development.submit",
		"development.approve",
		"development.manage",
		"development.merge",
		"connection.read",
		"connection.use",
		"connection.manage",
		"value.manage",
		"settings.read",
		"settings.manage",
		"gateway.delivery.send",
		"discussion.read",
		"discussion.write",
		"knowledge.read",
		"web.read",
		"knowledge.write",
		"knowledge.manage",
		"knowledge.website_gap.propose",
		"email.send",
		"memory.read",
		"memory.write",
		"memory.review",
		"memory.world.manage",
		"memory.distill",
		"memory.audit.read",
		"memory.diary.read",
		"memory.diary.write",
		"scheduler.read",
		"scheduler.write",
		"scheduler.run",
		"todo.read",
		"todo.write",
		"idea.read",
		"idea.write",
		"evaluation.review",
		"enterprise_graph.read",
		"ontology.read",
		"ontology.manage",
		"skill.read",
		"personal_assistant.shell",
	}
}

func EnsureBootstrapSuperadmin(ctx context.Context, store AccountStore, initialPassword string) error {
	if store == nil {
		return ErrServiceDependency
	}
	account, err := store.GetAccountByUsername(ctx, SuperadminUsername)
	if err != nil {
		if !errors.Is(err, ErrAccountNotFound) {
			return err
		}
		account = Account{
			UserID:          SuperadminUsername,
			Username:        SuperadminUsername,
			DisplayName:     SuperadminUsername,
			Nickname:        SuperadminUsername,
			AgentProfileID:  SuperadminAgentProfileID,
			Role:            "superadmin",
			Enabled:         true,
			PermissionScope: SuperadminScopes(),
		}
		if err := account.SetPassword(initialPassword); err != nil {
			return err
		}
		return store.SaveAccount(ctx, account)
	}
	account = account.normalized()
	if account.AgentProfileID != "" && account.AgentProfileID != SuperadminAgentProfileID {
		return fmt.Errorf("%w: %s must remain bound to agent profile %s", ErrAccountConflict, SuperadminUsername, SuperadminAgentProfileID)
	}
	account.UserID = firstNonEmpty(account.UserID, SuperadminUsername)
	account.Username = SuperadminUsername
	account.DisplayName = firstNonEmpty(account.DisplayName, account.Nickname, SuperadminUsername)
	account.Nickname = firstNonEmpty(account.Nickname, account.DisplayName, SuperadminUsername)
	account.AgentProfileID = SuperadminAgentProfileID
	account.Role = firstNonEmpty(account.Role, "superadmin")
	account.Enabled = true
	account.PermissionScope = mergeScopes(account.PermissionScope, SuperadminScopes())
	if strings.TrimSpace(account.PasswordSalt) == "" || strings.TrimSpace(account.PasswordVerifier) == "" {
		if err := account.SetPassword(initialPassword); err != nil {
			return err
		}
	}
	return store.SaveAccount(ctx, account)
}

func (s *Service) BeginLogin(ctx context.Context, req BeginLoginRequest) (LoginChallengeDTO, error) {
	if s == nil || s.store == nil {
		return LoginChallengeDTO{}, ErrServiceDependency
	}
	username := normalizeUsername(req.Username)
	if username == "" {
		return LoginChallengeDTO{}, fmt.Errorf("%w: username is required", ErrInvalidRequest)
	}
	ipAddress := normalizeIPAddress(req.IPAddress)
	if err := s.ensureLoginNotLocked(ctx, ipAddress, username); err != nil {
		return LoginChallengeDTO{}, err
	}
	_, err := s.store.GetAccountByUsername(ctx, username)
	if err != nil && !errors.Is(err, ErrAccountNotFound) && !errors.Is(err, ErrAccountConflict) {
		return LoginChallengeDTO{}, err
	}
	challengeSalt, err := newSyntheticLoginSalt()
	if err != nil {
		return LoginChallengeDTO{}, err
	}
	now := s.now()
	challengeToken, err := randomToken(18)
	if err != nil {
		return LoginChallengeDTO{}, err
	}
	challenge := LoginChallenge{
		ChallengeID: "login-" + challengeToken,
		Username:    username,
		IPAddress:   ipAddress,
		Salt:        challengeSalt,
		ExpiresAt:   now.Add(5 * time.Minute),
	}
	if err := s.store.SaveLoginChallenge(ctx, challenge); err != nil {
		return LoginChallengeDTO{}, err
	}
	return LoginChallengeDTO{
		ChallengeID: challenge.ChallengeID,
		Username:    username,
		Salt:        challenge.Salt,
		ExpiresAt:   challenge.ExpiresAt,
	}, nil
}

func (s *Service) CompleteLogin(ctx context.Context, req CompleteLoginRequest) (SessionDTO, error) {
	if s == nil || s.store == nil {
		return SessionDTO{}, ErrServiceDependency
	}
	username := normalizeUsername(req.Username)
	challengeID := strings.TrimSpace(req.ChallengeID)
	password := req.Password
	passwordDigest := strings.TrimSpace(req.PasswordDigest)
	if username == "" || challengeID == "" || (password == "" && passwordDigest == "") {
		return SessionDTO{}, fmt.Errorf("%w: username, challenge_id and password or password_digest are required", ErrInvalidRequest)
	}
	ipAddress := normalizeIPAddress(req.IPAddress)
	if err := s.ensureLoginNotLocked(ctx, ipAddress, username); err != nil {
		return SessionDTO{}, err
	}
	releaseVerification, err := s.passwordLimiter.acquire(ctx, ipAddress)
	if err != nil {
		return SessionDTO{}, err
	}
	defer releaseVerification()
	challenge, err := s.store.ConsumeLoginChallenge(ctx, challengeID)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			return SessionDTO{}, s.failedLoginError(ctx, ipAddress, username, "invalid_challenge")
		}
		return SessionDTO{}, err
	}
	challengeIPAddress := normalizeIPAddress(challenge.IPAddress)
	if ipAddress == "" {
		ipAddress = challengeIPAddress
		if err := s.ensureLoginNotLocked(ctx, ipAddress, username); err != nil {
			return SessionDTO{}, err
		}
	} else if challengeIPAddress != "" && challengeIPAddress != ipAddress {
		return SessionDTO{}, s.failedLoginError(ctx, ipAddress, username, "challenge_ip_mismatch")
	}
	now := s.now()
	if !challenge.ExpiresAt.IsZero() && now.After(challenge.ExpiresAt) {
		return SessionDTO{}, s.failedLoginError(ctx, ipAddress, username, "expired_challenge")
	}
	if normalizeUsername(challenge.Username) != username {
		return SessionDTO{}, s.failedLoginError(ctx, ipAddress, username, "challenge_username_mismatch")
	}
	if password != "" && len([]rune(password)) > MaximumPasswordLength {
		return SessionDTO{}, s.failedLoginError(ctx, ipAddress, username, "password_too_long")
	}
	if passwordDigest != "" && len(passwordDigest) != sha256.Size*2 {
		return SessionDTO{}, s.failedLoginError(ctx, ipAddress, username, "invalid_password_digest")
	}
	if password == "" {
		performSyntheticPasswordCheck(challenge.Salt, password, passwordDigest, challenge.ChallengeID)
		return SessionDTO{}, s.failedLoginError(ctx, ipAddress, username, "password_digest_not_supported")
	}
	account, err := s.store.GetAccountByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, ErrAccountNotFound) || errors.Is(err, ErrAccountConflict) {
			performSyntheticPasswordCheck(challenge.Salt, password, passwordDigest, challenge.ChallengeID)
			return SessionDTO{}, s.failedLoginError(ctx, ipAddress, username, "account_not_found")
		}
		return SessionDTO{}, err
	}
	account = account.normalized()
	if !account.Enabled {
		performSyntheticPasswordCheck(challenge.Salt, password, passwordDigest, challenge.ChallengeID)
		return SessionDTO{}, s.failedLoginError(ctx, ipAddress, username, "account_disabled")
	}
	if s.localMFAUnavailable(username) {
		performSyntheticPasswordCheck(challenge.Salt, password, passwordDigest, challenge.ChallengeID)
		return SessionDTO{}, s.failedLoginError(ctx, ipAddress, username, "mfa_not_configured")
	}
	legacyVerifier := PasswordVerifierNeedsUpgrade(account.PasswordVerifier)
	matched, canUpgrade := s.passwordMatches(ctx, account, challenge.ChallengeID, password, passwordDigest)
	if !matched {
		return SessionDTO{}, s.failedLoginError(ctx, ipAddress, username, "invalid_password")
	}
	if s.requireLocalMFA || s.mfaValidator != nil && s.mfaValidator.Required(username) {
		if s.mfaValidator == nil || !s.mfaValidator.Required(username) {
			return SessionDTO{}, ErrMFARequired
		}
		if strings.TrimSpace(req.MFACode) == "" {
			return SessionDTO{}, ErrMFARequired
		}
		if err := s.mfaValidator.Validate(ctx, username, req.MFACode, now); err != nil {
			if !errors.Is(err, ErrMFARequired) && !errors.Is(err, ErrMFAInvalid) {
				return SessionDTO{}, err
			}
			return SessionDTO{}, s.failedLoginError(ctx, ipAddress, username, "invalid_mfa")
		}
	}
	if err := s.ensureLoginNotLocked(ctx, ipAddress, username); err != nil {
		return SessionDTO{}, err
	}
	mustChangePassword := legacyVerifier && (password == "" || ValidatePassword(password) != nil)
	if canUpgrade {
		if err := account.SetPassword(password); err != nil {
			return SessionDTO{}, err
		}
		if err := s.store.SaveAccount(ctx, account); err != nil {
			return SessionDTO{}, err
		}
	}
	if err := s.clearIPFailures(ctx, ipAddress); err != nil {
		return SessionDTO{}, err
	}
	if err := s.clearUsernameFailures(ctx, username); err != nil {
		return SessionDTO{}, err
	}
	session, err := newSessionRecord(account, identity.ProviderLocal, firstNonEmpty(account.UserID, account.Username), "", now, mustChangePassword, ipAddress, req.UserAgent)
	if err != nil {
		return SessionDTO{}, err
	}
	if err := s.store.SaveSession(ctx, session); err != nil {
		return SessionDTO{}, err
	}
	return s.sessionDTO(ctx, account, session)
}

func (s *Service) passwordMatches(ctx context.Context, account Account, challengeID, password, passwordDigest string) (bool, bool) {
	_ = ctx
	if password != "" {
		matched := PasswordVerifierMatches(password, account.PasswordSalt, account.PasswordVerifier)
		legacyVerifier := PasswordVerifierNeedsUpgrade(account.PasswordVerifier)
		if legacyVerifier {
			_ = PasswordVerifierMatches(password, account.PasswordSalt, syntheticLoginVerifier(account.PasswordSalt))
		}
		canUpgrade := matched && legacyVerifier
		if canUpgrade && ValidatePassword(password) != nil {
			canUpgrade = false
		}
		return matched, canUpgrade
	}
	if PasswordVerifierNeedsUpgrade(account.PasswordVerifier) && passwordProofMatches(account.PasswordVerifier, challengeID, passwordDigest) {
		return true, false
	}
	return false, false
}

func (s *Service) ensureIPNotLocked(ctx context.Context, ipAddress string) error {
	ipAddress = normalizeIPAddress(ipAddress)
	if ipAddress == "" {
		return nil
	}
	record, ok, err := s.store.GetIPLock(ctx, ipAddress)
	if err != nil {
		return err
	}
	if ok && normalizeIPLockStatus(record.Status) == "active" {
		lockedAt := record.LockedAt
		if lockedAt.IsZero() {
			lockedAt = record.UpdatedAt
		}
		if !lockedAt.IsZero() && !s.now().Before(lockedAt.Add(failedLoginLockTTL)) {
			return s.store.DeleteIPLock(ctx, ipAddress)
		}
		return ErrIPLocked
	}
	return nil
}

func (s *Service) ensureLoginNotLocked(ctx context.Context, ipAddress string, username string) error {
	_ = username
	return s.ensureIPNotLocked(ctx, ipAddress)
}

func (s *Service) ensureUsernameNotLocked(ctx context.Context, username string) error {
	username = normalizeUsername(username)
	if username == "" {
		return nil
	}
	record, ok, err := s.store.GetUsernameLock(ctx, username)
	if err != nil {
		return err
	}
	if ok && normalizeIPLockStatus(record.Status) == "active" {
		lockedAt := record.LockedAt
		if lockedAt.IsZero() {
			lockedAt = record.UpdatedAt
		}
		if !lockedAt.IsZero() && !s.now().Before(lockedAt.Add(failedLoginLockTTL)) {
			return s.store.DeleteUsernameLock(ctx, username)
		}
		return ErrUsernameLocked
	}
	return nil
}

func (s *Service) recordFailedLogin(ctx context.Context, ipAddress, username, reason string) error {
	ipAddress = normalizeIPAddress(ipAddress)
	username = normalizeUsername(username)
	now := s.now()
	if ipAddress != "" {
		if recorder, ok := s.store.(IPFailureStore); ok {
			if err := recorder.RecordIPFailure(ctx, ipAddress, username, reason, now, failedLoginWindow, maxFailedLoginsPerIP); err != nil {
				return err
			}
		} else {
			record, ok, err := s.store.GetIPLock(ctx, ipAddress)
			if err != nil {
				return err
			}
			if !ok || normalizeIPLockStatus(record.Status) != "tracking" || !record.UpdatedAt.IsZero() && !now.Before(record.UpdatedAt.Add(failedLoginWindow)) {
				record = IPBlockRecord{IPAddress: ipAddress, Status: "tracking"}
			}
			record.IPAddress = ipAddress
			record.FailureCount++
			record.LastUsername = username
			record.LastReason = strings.TrimSpace(reason)
			record.UpdatedAt = now
			if record.FailureCount >= maxFailedLoginsPerIP {
				record.Status = "active"
				if record.LockedAt.IsZero() {
					record.LockedAt = now
				}
			}
			if err := s.store.SaveIPLock(ctx, record); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) failedLoginError(ctx context.Context, ipAddress, username, reason string) error {
	if err := s.recordFailedLogin(ctx, ipAddress, username, reason); err != nil {
		return fmt.Errorf("%w: record failed login: %v", ErrServiceDependency, err)
	}
	return ErrInvalidCredentials
}

func (s *Service) clearIPFailures(ctx context.Context, ipAddress string) error {
	ipAddress = normalizeIPAddress(ipAddress)
	if ipAddress == "" {
		return nil
	}
	record, ok, err := s.store.GetIPLock(ctx, ipAddress)
	if err != nil {
		return err
	}
	if !ok || normalizeIPLockStatus(record.Status) == "active" {
		return nil
	}
	return s.store.DeleteIPLock(ctx, ipAddress)
}

func (s *Service) clearUsernameFailures(ctx context.Context, username string) error {
	username = normalizeUsername(username)
	if username == "" {
		return nil
	}
	record, ok, err := s.store.GetUsernameLock(ctx, username)
	if err != nil {
		return err
	}
	if !ok || normalizeIPLockStatus(record.Status) == "active" {
		return nil
	}
	return s.store.DeleteUsernameLock(ctx, username)
}

func (s *Service) localMFAUnavailable(username string) bool {
	return s != nil && s.requireLocalMFA && (s.mfaValidator == nil || !s.mfaValidator.Required(username))
}

func newSyntheticLoginSalt() (string, error) {
	salt, err := randomBytes(passwordArgon2IDSaltBytes)
	if err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(salt), nil
}

func performSyntheticPasswordCheck(salt string, password string, passwordDigest string, challengeID string) {
	if password != "" {
		_ = PasswordVerifierMatches(password, salt, syntheticLoginVerifier(salt))
		return
	}
	_ = passwordProofMatches(strings.Repeat("0", sha256.Size*2), challengeID, passwordDigest)
}

func syntheticLoginVerifier(salt string) string {
	return fmt.Sprintf("%sv=%d$m=%d,t=%d,p=%d$%s$%s",
		passwordVerifierArgon2IDPrefix,
		argon2.Version,
		passwordArgon2IDMemoryKB,
		passwordArgon2IDIterations,
		passwordArgon2IDParallelism,
		strings.TrimSpace(salt),
		base64.RawStdEncoding.EncodeToString(make([]byte, passwordArgon2IDKeyBytes)),
	)
}

func (s *Service) AuthenticateToken(ctx context.Context, accessToken string) (SessionDTO, error) {
	account, record, err := s.authenticateSessionRecord(ctx, accessToken)
	if err != nil {
		return SessionDTO{}, err
	}
	return s.sessionDTO(ctx, account, record)
}

func (s *Service) authenticateSessionRecord(ctx context.Context, accessToken string) (Account, SessionRecord, error) {
	if s == nil || s.store == nil {
		return Account{}, SessionRecord{}, ErrServiceDependency
	}
	accessToken = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(accessToken), "Bearer "))
	if accessToken == "" {
		return Account{}, SessionRecord{}, ErrUnauthorized
	}
	record, err := s.store.GetSession(ctx, accessToken)
	if err != nil {
		return Account{}, SessionRecord{}, err
	}
	now := s.now()
	if !record.ExpiresAt.IsZero() && now.After(record.ExpiresAt) {
		_ = s.store.DeleteSession(ctx, accessToken)
		return Account{}, SessionRecord{}, ErrUnauthorized
	}
	changed := false
	if record.SessionID == "" {
		sessionID, tokenErr := randomToken(18)
		if tokenErr != nil {
			return Account{}, SessionRecord{}, tokenErr
		}
		record.SessionID = "session-" + sessionID
		changed = true
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
		changed = true
	}
	if record.LastSeenAt.IsZero() {
		record.LastSeenAt = record.CreatedAt
		changed = true
	}
	if !record.LastSeenAt.IsZero() && now.Sub(record.LastSeenAt) > defaultSessionIdleTTL {
		_ = s.store.DeleteSession(ctx, accessToken)
		return Account{}, SessionRecord{}, ErrUnauthorized
	}
	if now.Sub(record.LastSeenAt) >= sessionTouchInterval {
		record.LastSeenAt = now
		changed = true
	}
	account, err := s.store.GetAccountByUsername(ctx, record.Username)
	if err != nil {
		_ = s.store.DeleteSession(ctx, accessToken)
		return Account{}, SessionRecord{}, ErrUnauthorized
	}
	account = account.normalized()
	if !account.Enabled {
		_ = s.store.DeleteSession(ctx, accessToken)
		return Account{}, SessionRecord{}, ErrUnauthorized
	}
	credentialVersion := accountCredentialVersion(account)
	if record.CredentialVersion == "" || !credentialVersionMatches(record.CredentialVersion, credentialVersion) {
		_ = s.store.DeleteSession(ctx, accessToken)
		return Account{}, SessionRecord{}, ErrUnauthorized
	}
	if err := s.ensureSessionSubjectActive(ctx, record, account); err != nil {
		_ = s.store.DeleteSession(ctx, accessToken)
		return Account{}, SessionRecord{}, err
	}
	if changed {
		updater, ok := s.store.(SessionUpdateStore)
		if !ok {
			return Account{}, SessionRecord{}, ErrServiceDependency
		}
		if err := updater.UpdateSession(ctx, record); err != nil {
			return Account{}, SessionRecord{}, err
		}
	}
	return account, record, nil
}

func (s *Service) ensureSessionSubjectActive(ctx context.Context, record SessionRecord, account Account) error {
	provider := strings.TrimSpace(record.SubjectProvider)
	subjectID := strings.TrimSpace(record.Subject)
	if provider == "" || provider == identity.ProviderLocal || subjectID == "" {
		return nil
	}
	if s == nil || s.identitySubjects == nil {
		return ErrServiceDependency
	}
	subject, err := s.identitySubjects.GetSubject(ctx, provider, subjectID)
	if err != nil {
		return ErrUnauthorized
	}
	subject = identity.NormalizeIdentitySubject(subject)
	if subject.Status != identity.SubjectStatusActive || strings.TrimSpace(record.IdentitySubjectID) == "" || subject.ID != strings.TrimSpace(record.IdentitySubjectID) {
		return ErrUnauthorized
	}
	mapped, err := s.accountForIdentitySubject(ctx, subject)
	if err != nil {
		return ErrUnauthorized
	}
	mapped = mapped.normalized()
	account = account.normalized()
	if mapped.Username != account.Username || mapped.AgentProfileID != account.AgentProfileID || normalizeUsername(record.Username) != mapped.Username {
		return ErrUnauthorized
	}
	return nil
}

func (s *Service) AuthenticatePrincipal(ctx context.Context, accessToken string) (identity.Principal, error) {
	account, record, err := s.authenticateSessionRecord(ctx, accessToken)
	if err != nil {
		return identity.Principal{}, err
	}
	if record.MustChangePassword {
		return identity.Principal{}, ErrPasswordChangeRequired
	}
	principal := PrincipalFromSession(sessionDTO(account, record))
	principal.CoarseScopes, err = s.resolveAuthorizationAccountScopes(ctx, principal, principal.CoarseScopes)
	if err != nil {
		return identity.Principal{}, err
	}
	return identity.NormalizePrincipal(principal), nil
}

func (s *Service) CreateSessionForIdentitySubject(ctx context.Context, subject identity.IdentitySubject) (SessionDTO, error) {
	return s.CreateSessionForIdentitySubjectUntil(ctx, subject, time.Time{})
}

func (s *Service) CreateSessionForIdentitySubjectUntil(ctx context.Context, subject identity.IdentitySubject, expiresAt time.Time) (SessionDTO, error) {
	return s.createSessionForIdentitySubjectUntil(ctx, subject, expiresAt, false)
}

func (s *Service) CreatePasswordChangeSessionForIdentitySubjectUntil(ctx context.Context, subject identity.IdentitySubject, expiresAt time.Time) (SessionDTO, error) {
	return s.createSessionForIdentitySubjectUntil(ctx, subject, expiresAt, true)
}

func (s *Service) createSessionForIdentitySubjectUntil(ctx context.Context, subject identity.IdentitySubject, expiresAt time.Time, mustChangePassword bool) (SessionDTO, error) {
	if s == nil || s.store == nil {
		return SessionDTO{}, ErrServiceDependency
	}
	subject = identity.NormalizeIdentitySubject(subject)
	if subject.Status != identity.SubjectStatusActive {
		return SessionDTO{}, ErrUnauthorized
	}
	if subject.Provider != "" && subject.Provider != identity.ProviderLocal && s.identitySubjects == nil {
		return SessionDTO{}, ErrServiceDependency
	}
	account, err := s.accountForIdentitySubject(ctx, subject)
	if err != nil {
		return SessionDTO{}, ErrUnauthorized
	}
	account = account.normalized()
	if !account.Enabled {
		return SessionDTO{}, ErrUnauthorized
	}
	now := s.now()
	session, err := newSessionRecord(account, subject.Provider, subject.Subject, subject.ID, now, mustChangePassword, "", "")
	if err != nil {
		return SessionDTO{}, err
	}
	if !expiresAt.IsZero() {
		expiresAt = expiresAt.UTC()
		if !expiresAt.After(now) {
			return SessionDTO{}, ErrUnauthorized
		}
		if expiresAt.Before(session.ExpiresAt) {
			session.ExpiresAt = expiresAt
		}
	}
	if err := s.store.SaveSession(ctx, session); err != nil {
		return SessionDTO{}, err
	}
	return s.sessionDTO(ctx, account, session)
}

func (s *Service) PrincipalForIdentitySubject(ctx context.Context, subject identity.IdentitySubject, tokenScopes []string, expiresAt time.Time) (identity.Principal, error) {
	return s.principalForIdentitySubject(ctx, subject, tokenScopes, expiresAt, false)
}

func (s *Service) principalForIdentitySubject(ctx context.Context, subject identity.IdentitySubject, tokenScopes []string, expiresAt time.Time, desktopSession bool) (identity.Principal, error) {
	if s == nil || s.store == nil {
		return identity.Principal{}, ErrServiceDependency
	}
	subject = identity.NormalizeIdentitySubject(subject)
	if subject.Status != identity.SubjectStatusActive {
		return identity.Principal{}, ErrUnauthorized
	}
	account, err := s.accountForIdentitySubject(ctx, subject)
	if err != nil {
		return identity.Principal{}, ErrUnauthorized
	}
	account = account.normalized()
	if !account.Enabled {
		return identity.Principal{}, ErrUnauthorized
	}
	principal := identity.PrincipalFromSubject(subject, account.PermissionScope, expiresAt)
	principal.UserID = firstNonEmpty(account.UserID, principal.UserID)
	principal.Username = firstNonEmpty(account.Username, principal.Username)
	principal.DisplayName = firstNonEmpty(account.DisplayName, account.Nickname, principal.DisplayName)
	principal.AgentProfileID = firstNonEmpty(account.AgentProfileID, principal.AgentProfileID)
	authorizationScopes, err := s.resolveAuthorizationAccountScopes(ctx, principal, account.PermissionScope)
	if err != nil {
		return identity.Principal{}, err
	}
	principal.CoarseScopes = intersectScopes(authorizationScopes, tokenScopes)
	principal.CredentialScopes = cleanScopes(tokenScopes)
	principal.CredentialScopeRestricted = true
	if desktopSession {
		// A verified first-party desktop identity session follows the same live
		// account authorization as Web. Other OAuth clients keep token limits.
		principal.CoarseScopes = authorizationScopes
		principal.CredentialScopes = nil
		principal.CredentialScopeRestricted = false
	}
	return identity.NormalizePrincipal(principal), nil
}

func (s *Service) accountForIdentitySubject(ctx context.Context, subject identity.IdentitySubject) (Account, error) {
	subject = identity.NormalizeIdentitySubject(subject)
	if subject.Provider != "" && subject.Provider != identity.ProviderLocal {
		if subject.AgentProfileID == "" || !externalSubjectCanUseAgentProfile(subject) {
			return Account{}, ErrUnauthorized
		}
		store, ok := s.store.(interface {
			GetAccountByAgentProfileID(context.Context, string) (Account, error)
		})
		if !ok {
			return Account{}, ErrServiceDependency
		}
		return store.GetAccountByAgentProfileID(ctx, subject.AgentProfileID)
	}
	if subject.AgentProfileID != "" {
		if store, ok := s.store.(interface {
			GetAccountByAgentProfileID(context.Context, string) (Account, error)
		}); ok {
			return store.GetAccountByAgentProfileID(ctx, subject.AgentProfileID)
		}
	}
	if subject.Username != "" {
		account, err := s.store.GetAccountByUsername(ctx, subject.Username)
		if err == nil {
			return account, nil
		}
	}
	return Account{}, ErrAccountNotFound
}

func externalSubjectCanUseAgentProfile(subject identity.IdentitySubject) bool {
	if !strings.EqualFold(subject.AgentProfileID, SuperadminAgentProfileID) {
		return true
	}
	return subject.Provider == identity.ProviderKratos && strings.EqualFold(subject.Username, SuperadminUsername)
}

func (s *Service) RevokeSession(ctx context.Context, accessToken string) error {
	if s == nil || s.store == nil {
		return ErrServiceDependency
	}
	accessToken = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(accessToken), "Bearer "))
	if accessToken == "" {
		return nil
	}
	return s.store.DeleteSession(ctx, accessToken)
}

func (s *Service) ChangePassword(ctx context.Context, accessToken string, newPassword string, ipAddress string, userAgent string) (SessionDTO, error) {
	account, record, err := s.authenticateSessionRecord(ctx, accessToken)
	if err != nil {
		return SessionDTO{}, err
	}
	if !record.MustChangePassword {
		return SessionDTO{}, fmt.Errorf("%w: password change requires a restricted reset session", ErrInvalidRequest)
	}
	if record.SubjectProvider != "" && record.SubjectProvider != identity.ProviderLocal {
		return s.changeExternalPassword(ctx, account, record, newPassword, ipAddress, userAgent)
	}
	if err := account.SetPassword(newPassword); err != nil {
		return SessionDTO{}, err
	}
	consumer, ok := s.store.(SessionConsumeStore)
	if !ok {
		return SessionDTO{}, ErrServiceDependency
	}
	if err := consumer.ConsumeSession(ctx, record.AccessToken); err != nil {
		return SessionDTO{}, err
	}
	if err := s.RevokeSessionsForUsername(ctx, account.Username); err != nil {
		return SessionDTO{}, err
	}
	if err := s.store.SaveAccount(ctx, account); err != nil {
		return SessionDTO{}, err
	}
	if err := s.RevokeSessionsForUsername(ctx, account.Username); err != nil {
		return SessionDTO{}, err
	}
	now := s.now()
	replacement, err := newSessionRecord(account, record.SubjectProvider, record.Subject, record.IdentitySubjectID, now, false, ipAddress, userAgent)
	if err != nil {
		return SessionDTO{}, err
	}
	if err := s.store.SaveSession(ctx, replacement); err != nil {
		return SessionDTO{}, err
	}
	return s.sessionDTO(ctx, account, replacement)
}

func (s *Service) changeExternalPassword(ctx context.Context, account Account, record SessionRecord, newPassword, ipAddress, userAgent string) (SessionDTO, error) {
	if s.externalPasswordChanger == nil {
		return SessionDTO{}, ErrServiceDependency
	}
	if err := ValidatePassword(newPassword); err != nil {
		return SessionDTO{}, err
	}
	if err := s.externalPasswordChanger.ChangeInitialPassword(ctx, record.Subject, newPassword); err != nil {
		return SessionDTO{}, err
	}
	consumer, ok := s.store.(SessionConsumeStore)
	if !ok {
		return SessionDTO{}, ErrServiceDependency
	}
	if err := consumer.ConsumeSession(ctx, record.AccessToken); err != nil {
		return SessionDTO{}, err
	}
	if err := s.RevokeSessionsForIdentitySubject(ctx, record.SubjectProvider, record.Subject); err != nil {
		return SessionDTO{}, err
	}
	replacement, err := newSessionRecord(account, record.SubjectProvider, record.Subject, record.IdentitySubjectID, s.now(), false, ipAddress, userAgent)
	if err != nil {
		return SessionDTO{}, err
	}
	if !record.ExpiresAt.IsZero() && record.ExpiresAt.Before(replacement.ExpiresAt) {
		replacement.ExpiresAt = record.ExpiresAt
	}
	if err := s.store.SaveSession(ctx, replacement); err != nil {
		return SessionDTO{}, err
	}
	return s.sessionDTO(ctx, account, replacement)
}

func (s *Service) RotateSession(ctx context.Context, accessToken string, ipAddress string, userAgent string) (SessionDTO, error) {
	account, record, err := s.authenticateSessionRecord(ctx, accessToken)
	if err != nil {
		return SessionDTO{}, err
	}
	if record.MustChangePassword {
		return SessionDTO{}, ErrPasswordChangeRequired
	}
	replacement, err := newSessionRecord(account, record.SubjectProvider, record.Subject, record.IdentitySubjectID, s.now(), false, ipAddress, userAgent)
	if err != nil {
		return SessionDTO{}, err
	}
	replacement.ExpiresAt = record.ExpiresAt
	rotator, ok := s.store.(SessionRotationStore)
	if !ok {
		return SessionDTO{}, ErrServiceDependency
	}
	if err := rotator.ReplaceSession(ctx, record.AccessToken, replacement); err != nil {
		return SessionDTO{}, err
	}
	return s.sessionDTO(ctx, account, replacement)
}

func (s *Service) ListSessions(ctx context.Context, accessToken string) (SessionPageDTO, error) {
	account, current, err := s.authenticateSessionRecord(ctx, accessToken)
	if err != nil {
		return SessionPageDTO{}, err
	}
	if current.MustChangePassword {
		return SessionPageDTO{}, ErrPasswordChangeRequired
	}
	catalog, ok := s.store.(SessionCatalogStore)
	if !ok {
		return SessionPageDTO{}, ErrServiceDependency
	}
	records, err := catalog.ListSessionsByUsername(ctx, account.Username)
	if err != nil {
		return SessionPageDTO{}, err
	}
	out := SessionPageDTO{Sessions: make([]SessionSummaryDTO, 0, len(records))}
	for _, record := range records {
		out.Sessions = append(out.Sessions, SessionSummaryDTO{
			SessionID:  record.SessionID,
			Current:    record.SessionID == current.SessionID,
			IPAddress:  record.IPAddress,
			UserAgent:  record.UserAgent,
			CreatedAt:  record.CreatedAt,
			LastSeenAt: record.LastSeenAt,
			ExpiresAt:  record.ExpiresAt,
		})
	}
	return out, nil
}

func (s *Service) RevokeSessionByID(ctx context.Context, accessToken string, sessionID string) error {
	account, current, err := s.authenticateSessionRecord(ctx, accessToken)
	if err != nil {
		return err
	}
	if current.MustChangePassword {
		return ErrPasswordChangeRequired
	}
	catalog, ok := s.store.(SessionCatalogStore)
	if !ok {
		return ErrServiceDependency
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ErrInvalidRequest
	}
	return catalog.DeleteSessionByID(ctx, account.Username, sessionID)
}

func (s *Service) RevokeSessionsForUsername(ctx context.Context, username string) error {
	if s == nil || s.store == nil {
		return ErrServiceDependency
	}
	bulk, ok := s.store.(SessionBulkStore)
	if !ok {
		return ErrServiceDependency
	}
	return bulk.DeleteSessionsByUsername(ctx, normalizeUsername(username))
}

func (s *Service) RevokeSessionsForIdentitySubject(ctx context.Context, provider string, subject string) error {
	if s == nil || s.store == nil {
		return ErrServiceDependency
	}
	bulk, ok := s.store.(SessionBulkStore)
	if !ok {
		return ErrServiceDependency
	}
	return bulk.DeleteSessionsByIdentitySubject(ctx, provider, subject)
}

func (s *Service) ListIPLocks(ctx context.Context, query IPBlockQuery) (IPBlockPageDTO, error) {
	if s == nil || s.store == nil {
		return IPBlockPageDTO{}, ErrServiceDependency
	}
	return s.store.ListIPLocks(ctx, query)
}

func (s *Service) UnlockIP(ctx context.Context, ipAddress string, actorID string, reason string) error {
	if s == nil || s.store == nil {
		return ErrServiceDependency
	}
	if unlocker, ok := s.store.(interface {
		UnlockIP(context.Context, string, string, string) error
	}); ok {
		return unlocker.UnlockIP(ctx, normalizeIPAddress(ipAddress), actorID, reason)
	}
	return s.store.DeleteIPLock(ctx, normalizeIPAddress(ipAddress))
}

func passwordProofMatches(wantVerifier, challengeID, clientProof string) bool {
	wantVerifier = strings.TrimSpace(wantVerifier)
	gotProof := ChallengePasswordProof(wantVerifier, challengeID)
	if len(clientProof) != len(gotProof) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(clientProof), []byte(gotProof)) == 1
}

func sessionDTO(account Account, record SessionRecord) SessionDTO {
	account = account.normalized()
	scopes := cleanScopes(account.PermissionScope)
	if record.MustChangePassword {
		scopes = nil
	}
	subjectProvider := strings.TrimSpace(record.SubjectProvider)
	if subjectProvider == "" {
		subjectProvider = identity.ProviderLocal
	}
	subject := strings.TrimSpace(record.Subject)
	if subject == "" {
		subject = firstNonEmpty(account.UserID, account.Username)
	}
	return SessionDTO{
		AccessToken:       record.AccessToken,
		SubjectProvider:   subjectProvider,
		Subject:           subject,
		IdentitySubjectID: strings.TrimSpace(record.IdentitySubjectID),
		User: UserDTO{
			UserID:         account.UserID,
			Username:       account.Username,
			DisplayName:    firstNonEmpty(account.DisplayName, account.Nickname, account.Username),
			Nickname:       account.Nickname,
			Email:          account.Email,
			AgentProfileID: account.AgentProfileID,
			Role:           account.Role,
		},
		PermissionScope:    scopes,
		ExpiresAt:          record.ExpiresAt,
		SessionID:          record.SessionID,
		CreatedAt:          record.CreatedAt,
		LastSeenAt:         record.LastSeenAt,
		MustChangePassword: record.MustChangePassword,
		RotationRequired:   false,
	}
}

func newSessionRecord(account Account, provider string, subject string, identitySubjectID string, now time.Time, mustChangePassword bool, ipAddress string, userAgent string) (SessionRecord, error) {
	accessToken, err := randomToken(32)
	if err != nil {
		return SessionRecord{}, err
	}
	sessionID, err := randomToken(18)
	if err != nil {
		return SessionRecord{}, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	ttl := defaultSessionTTL
	if mustChangePassword {
		ttl = passwordChangeSessionTTL
	}
	return SessionRecord{
		AccessToken:        "iat-" + accessToken,
		Username:           account.Username,
		SubjectProvider:    provider,
		Subject:            subject,
		IdentitySubjectID:  identitySubjectID,
		CredentialVersion:  accountCredentialVersion(account),
		ExpiresAt:          now.Add(ttl),
		SessionID:          "session-" + sessionID,
		CreatedAt:          now,
		LastSeenAt:         now,
		MustChangePassword: mustChangePassword,
		IPAddress:          normalizeIPAddress(ipAddress),
		UserAgent:          strings.TrimSpace(userAgent),
	}, nil
}

func accountCredentialVersion(account Account) string {
	account = account.normalized()
	digest := sha256.Sum256([]byte(account.UserID + "\n" + account.Username + "\n" + account.AgentProfileID + "\n" + account.PasswordSalt + "\n" + account.PasswordVerifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func credentialVersionMatches(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	return len(left) == len(right) && subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func (s *Service) sessionDTO(ctx context.Context, account Account, record SessionRecord) (SessionDTO, error) {
	dto := sessionDTO(account, record)
	principal := PrincipalFromSession(dto)
	scopes, err := s.resolvePermissionScopes(ctx, principal, dto.PermissionScope)
	if err != nil {
		return SessionDTO{}, err
	}
	dto.PermissionScope = scopes
	dto.RotationRequired = !record.MustChangePassword && !record.CreatedAt.IsZero() && s.now().Sub(record.CreatedAt) >= defaultSessionRotationAge
	return dto, nil
}

func (s *Service) resolvePermissionScopes(ctx context.Context, principal identity.Principal, accountScopes []string) ([]string, error) {
	accountScopes = cleanScopes(accountScopes)
	if s == nil || s.scopeResolver == nil {
		return accountScopes, nil
	}
	resolved, err := s.scopeResolver.ResolvePermissionScopes(ctx, identity.NormalizePrincipal(principal), accountScopes)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve permission scopes: %v", ErrServiceDependency, err)
	}
	return cleanScopes(resolved), nil
}

func (s *Service) resolveAuthorizationAccountScopes(ctx context.Context, principal identity.Principal, accountScopes []string) ([]string, error) {
	accountScopes = cleanScopes(accountScopes)
	if s == nil || s.scopeResolver == nil {
		return accountScopes, nil
	}
	resolver, ok := s.scopeResolver.(AuthorizationAccountScopeResolver)
	if !ok {
		return accountScopes, nil
	}
	resolved, err := resolver.ResolveAuthorizationAccountScopes(ctx, identity.NormalizePrincipal(principal), accountScopes)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve authorization account scopes: %v", ErrServiceDependency, err)
	}
	return cleanScopes(resolved), nil
}

func PrincipalFromSession(session SessionDTO) identity.Principal {
	return identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: firstNonEmpty(session.SubjectProvider, identity.ProviderLocal),
		Subject:         firstNonEmpty(session.Subject, session.User.UserID, session.User.Username),
		UserID:          firstNonEmpty(session.User.UserID, session.User.Username),
		Username:        session.User.Username,
		DisplayName:     firstNonEmpty(session.User.DisplayName, session.User.Nickname, session.User.Username),
		AgentProfileID:  session.User.AgentProfileID,
		ViewerKind:      identity.ViewerKindUser,
		CoarseScopes:    session.PermissionScope,
		ExpiresAt:       session.ExpiresAt,
	})
}

func intersectScopes(accountScopes []string, tokenScopes []string) []string {
	accountScopes = cleanScopes(accountScopes)
	tokenScopes = cleanScopes(tokenScopes)
	if len(tokenScopes) == 0 {
		return []string{}
	}
	granted := map[string]struct{}{}
	for _, scope := range accountScopes {
		granted[strings.ToLower(strings.TrimSpace(scope))] = struct{}{}
	}
	out := make([]string, 0, len(tokenScopes))
	seen := map[string]struct{}{}
	for _, scope := range tokenScopes {
		normalized := strings.ToLower(strings.TrimSpace(scope))
		if normalized == "" {
			continue
		}
		_, accountGranted := granted[normalized]
		if !accountGranted && normalized != "website-advisor" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}
