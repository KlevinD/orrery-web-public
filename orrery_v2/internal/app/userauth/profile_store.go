package userauth

import (
	"context"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type ProfileAccountRepository interface {
	GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error)
	QueryDirectory(ctx context.Context, q agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error)
	SaveProfile(ctx context.Context, profile agentcore.AgentProfile) error
}

type profileAccountBulkRepository interface {
	GetProfiles(ctx context.Context, ids []agentcore.AgentProfileID) (map[agentcore.AgentProfileID]agentcore.AgentProfile, error)
}

type ProfileAccountStore struct {
	profiles  ProfileAccountRepository
	transient *MemoryAccountStore
}

func NewProfileAccountStore(profiles ProfileAccountRepository) *ProfileAccountStore {
	return &ProfileAccountStore{profiles: profiles, transient: NewMemoryAccountStore()}
}

func (s *ProfileAccountStore) SaveAccount(ctx context.Context, account Account) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.profiles == nil {
		return ErrServiceDependency
	}
	account = account.normalized()
	if account.Username == "" || account.AgentProfileID == "" {
		return ErrInvalidRequest
	}
	if account.Username == SuperadminUsername || IsSystemPrincipalID(account.AgentProfileID) {
		if account.Username != SuperadminUsername || !IsSystemPrincipalID(account.AgentProfileID) {
			return ErrAccountConflict
		}
		return s.memory().SaveAccount(ctx, account)
	}
	if IsReservedWorkforceUsername(account.Username) || IsReservedWorkforceProfileID(account.AgentProfileID) {
		return ErrAccountConflict
	}
	if err := s.ensureUsernameAvailable(ctx, account.Username, account.AgentProfileID); err != nil {
		return err
	}
	profile, err := s.profiles.GetProfile(ctx, agentcore.AgentProfileID(account.AgentProfileID))
	if err != nil {
		return err
	}
	if profile.AgentKind != agentcore.AgentKindHuman {
		return ErrInvalidRequest
	}
	profile.DisplayName = firstNonEmpty(account.DisplayName, account.Nickname, profile.DisplayName, profile.RealName, account.Username)
	profile.UserAccount = agentcore.UserAccountSpec{
		Username:         account.Username,
		Nickname:         firstNonEmpty(account.Nickname, profile.DisplayName, account.Username),
		Email:            strings.TrimSpace(account.Email),
		Role:             strings.TrimSpace(account.Role),
		AccountEnabled:   account.Enabled,
		PermissionScope:  cleanScopes(account.PermissionScope),
		PasswordSalt:     strings.TrimSpace(account.PasswordSalt),
		PasswordVerifier: strings.TrimSpace(account.PasswordVerifier),
	}
	return s.profiles.SaveProfile(ctx, profile)
}

func (s *ProfileAccountStore) GetAccountByUsername(ctx context.Context, username string) (Account, error) {
	if err := ctx.Err(); err != nil {
		return Account{}, err
	}
	if s == nil || s.profiles == nil {
		return Account{}, ErrServiceDependency
	}
	username = normalizeUsername(username)
	if username == "" {
		return Account{}, ErrAccountNotFound
	}
	if username == SuperadminUsername {
		return s.memory().GetAccountByUsername(ctx, username)
	}
	page, err := s.profiles.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{IncludeHumans: true})
	if err != nil {
		return Account{}, err
	}
	profiles, err := s.directoryProfiles(ctx, page.Entries)
	if err != nil {
		return Account{}, err
	}
	var matched Account
	matches := 0
	for _, profile := range profiles {
		if normalizeUsername(profile.UserAccount.Username) != username {
			continue
		}
		matched = accountFromProfile(profile)
		matches++
	}
	if matches > 1 {
		return Account{}, ErrAccountConflict
	}
	if matches == 1 {
		return matched, nil
	}
	return Account{}, ErrAccountNotFound
}

func (s *ProfileAccountStore) ensureUsernameAvailable(ctx context.Context, username string, agentProfileID string) error {
	username = normalizeUsername(username)
	agentProfileID = strings.TrimSpace(agentProfileID)
	if IsReservedWorkforceUsername(username) || IsReservedWorkforceProfileID(agentProfileID) {
		return ErrAccountConflict
	}
	page, err := s.profiles.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{IncludeHumans: true})
	if err != nil {
		return err
	}
	profiles, err := s.directoryProfiles(ctx, page.Entries)
	if err != nil {
		return err
	}
	for _, profile := range profiles {
		if profile.ID.String() == agentProfileID {
			continue
		}
		if normalizeUsername(profile.UserAccount.Username) == username {
			return ErrAccountConflict
		}
	}
	return nil
}

func (s *ProfileAccountStore) directoryProfiles(ctx context.Context, entries []agentcore.AgentDirectoryEntry) ([]agentcore.AgentProfile, error) {
	profiles := make([]agentcore.AgentProfile, 0, len(entries))
	bulk, supportsBulk := s.profiles.(profileAccountBulkRepository)
	if !supportsBulk {
		for _, entry := range entries {
			profile, err := s.profiles.GetProfile(ctx, entry.AgentProfileID)
			if err != nil {
				return nil, err
			}
			profiles = append(profiles, profile)
		}
		return profiles, nil
	}

	ids := make([]agentcore.AgentProfileID, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.AgentProfileID)
	}
	byID, err := bulk.GetProfiles(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		profile, ok := byID[entry.AgentProfileID]
		if !ok {
			profile, err = s.profiles.GetProfile(ctx, entry.AgentProfileID)
			if err != nil {
				return nil, err
			}
		}
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

func (s *ProfileAccountStore) GetAccountByAgentProfileID(ctx context.Context, agentProfileID string) (Account, error) {
	if err := ctx.Err(); err != nil {
		return Account{}, err
	}
	if s == nil || s.profiles == nil {
		return Account{}, ErrServiceDependency
	}
	agentProfileID = strings.TrimSpace(agentProfileID)
	if agentProfileID == "" {
		return Account{}, ErrAccountNotFound
	}
	if IsSystemPrincipalID(agentProfileID) {
		return s.memory().GetAccountByAgentProfileID(ctx, agentProfileID)
	}
	if IsReservedWorkforceProfileID(agentProfileID) {
		return Account{}, ErrAccountNotFound
	}
	profile, err := s.profiles.GetProfile(ctx, agentcore.AgentProfileID(agentProfileID))
	if err != nil {
		return Account{}, err
	}
	if profile.AgentKind != agentcore.AgentKindHuman {
		return Account{}, ErrAccountNotFound
	}
	return accountFromProfile(profile), nil
}

func (s *ProfileAccountStore) SaveLoginChallenge(ctx context.Context, challenge LoginChallenge) error {
	return s.memory().SaveLoginChallenge(ctx, challenge)
}

func (s *ProfileAccountStore) ConsumeLoginChallenge(ctx context.Context, challengeID string) (LoginChallenge, error) {
	return s.memory().ConsumeLoginChallenge(ctx, challengeID)
}

func (s *ProfileAccountStore) SaveSession(ctx context.Context, session SessionRecord) error {
	return s.memory().SaveSession(ctx, session)
}

func (s *ProfileAccountStore) UpdateSession(ctx context.Context, session SessionRecord) error {
	return s.memory().UpdateSession(ctx, session)
}

func (s *ProfileAccountStore) GetSession(ctx context.Context, accessToken string) (SessionRecord, error) {
	return s.memory().GetSession(ctx, accessToken)
}

func (s *ProfileAccountStore) DeleteSession(ctx context.Context, accessToken string) error {
	return s.memory().DeleteSession(ctx, accessToken)
}

func (s *ProfileAccountStore) ReplaceSession(ctx context.Context, currentAccessToken string, replacement SessionRecord) error {
	return s.memory().ReplaceSession(ctx, currentAccessToken, replacement)
}

func (s *ProfileAccountStore) ConsumeSession(ctx context.Context, accessToken string) error {
	return s.memory().ConsumeSession(ctx, accessToken)
}

func (s *ProfileAccountStore) DeleteSessionsByUsername(ctx context.Context, username string) error {
	return s.memory().DeleteSessionsByUsername(ctx, username)
}

func (s *ProfileAccountStore) DeleteSessionsByIdentitySubject(ctx context.Context, provider string, subject string) error {
	return s.memory().DeleteSessionsByIdentitySubject(ctx, provider, subject)
}

func (s *ProfileAccountStore) ListSessionsByUsername(ctx context.Context, username string) ([]SessionRecord, error) {
	return s.memory().ListSessionsByUsername(ctx, username)
}

func (s *ProfileAccountStore) DeleteSessionByID(ctx context.Context, username string, sessionID string) error {
	return s.memory().DeleteSessionByID(ctx, username, sessionID)
}

func (s *ProfileAccountStore) GetUsernameLock(ctx context.Context, username string) (UsernameBlockRecord, bool, error) {
	return s.memory().GetUsernameLock(ctx, username)
}

func (s *ProfileAccountStore) SaveUsernameLock(ctx context.Context, record UsernameBlockRecord) error {
	return s.memory().SaveUsernameLock(ctx, record)
}

func (s *ProfileAccountStore) DeleteUsernameLock(ctx context.Context, username string) error {
	return s.memory().DeleteUsernameLock(ctx, username)
}

func (s *ProfileAccountStore) RecordUsernameFailure(ctx context.Context, username string, reason string, now time.Time, window time.Duration, threshold int) error {
	return s.memory().RecordUsernameFailure(ctx, username, reason, now, window, threshold)
}

func (s *ProfileAccountStore) GetIPLock(ctx context.Context, ipAddress string) (IPBlockRecord, bool, error) {
	return s.memory().GetIPLock(ctx, ipAddress)
}

func (s *ProfileAccountStore) SaveIPLock(ctx context.Context, record IPBlockRecord) error {
	return s.memory().SaveIPLock(ctx, record)
}

func (s *ProfileAccountStore) RecordIPFailure(ctx context.Context, ipAddress string, username string, reason string, now time.Time, window time.Duration, threshold int) error {
	return s.memory().RecordIPFailure(ctx, ipAddress, username, reason, now, window, threshold)
}

func (s *ProfileAccountStore) DeleteIPLock(ctx context.Context, ipAddress string) error {
	return s.memory().DeleteIPLock(ctx, ipAddress)
}

func (s *ProfileAccountStore) ListIPLocks(ctx context.Context, query IPBlockQuery) (IPBlockPageDTO, error) {
	return s.memory().ListIPLocks(ctx, query)
}

func (s *ProfileAccountStore) memory() *MemoryAccountStore {
	if s.transient == nil {
		s.transient = NewMemoryAccountStore()
	}
	return s.transient
}

func accountFromProfile(profile agentcore.AgentProfile) Account {
	spec := profile.UserAccount
	return Account{
		UserID:           firstNonEmpty(spec.Username, profile.ID.String()),
		Username:         spec.Username,
		DisplayName:      firstNonEmpty(spec.Nickname, profile.DisplayName, profile.RealName, spec.Username),
		Nickname:         firstNonEmpty(spec.Nickname, profile.DisplayName, spec.Username),
		Email:            spec.Email,
		AgentProfileID:   profile.ID.String(),
		Role:             spec.Role,
		Enabled:          spec.AccountEnabled && profile.Enabled && profile.Placement.IsInternal(profile.OrgIdentity),
		PasswordSalt:     spec.PasswordSalt,
		PasswordVerifier: spec.PasswordVerifier,
		PermissionScope:  cleanScopes(spec.PermissionScope),
	}
}
