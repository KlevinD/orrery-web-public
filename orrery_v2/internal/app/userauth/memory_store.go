package userauth

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

type AccountStore interface {
	SaveAccount(ctx context.Context, account Account) error
	GetAccountByUsername(ctx context.Context, username string) (Account, error)
	SaveLoginChallenge(ctx context.Context, challenge LoginChallenge) error
	ConsumeLoginChallenge(ctx context.Context, challengeID string) (LoginChallenge, error)
	SaveSession(ctx context.Context, session SessionRecord) error
	GetSession(ctx context.Context, accessToken string) (SessionRecord, error)
	DeleteSession(ctx context.Context, accessToken string) error
	GetIPLock(ctx context.Context, ipAddress string) (IPBlockRecord, bool, error)
	SaveIPLock(ctx context.Context, record IPBlockRecord) error
	DeleteIPLock(ctx context.Context, ipAddress string) error
	ListIPLocks(ctx context.Context, query IPBlockQuery) (IPBlockPageDTO, error)
	GetUsernameLock(ctx context.Context, username string) (UsernameBlockRecord, bool, error)
	SaveUsernameLock(ctx context.Context, record UsernameBlockRecord) error
	DeleteUsernameLock(ctx context.Context, username string) error
}

type IPBlockStore interface {
	GetIPLock(ctx context.Context, ipAddress string) (IPBlockRecord, bool, error)
	SaveIPLock(ctx context.Context, record IPBlockRecord) error
	DeleteIPLock(ctx context.Context, ipAddress string) error
	ListIPLocks(ctx context.Context, query IPBlockQuery) (IPBlockPageDTO, error)
}

type AuthStateStore interface {
	SaveLoginChallenge(ctx context.Context, challenge LoginChallenge) error
	ConsumeLoginChallenge(ctx context.Context, challengeID string) (LoginChallenge, error)
	SaveSession(ctx context.Context, session SessionRecord) error
	GetSession(ctx context.Context, accessToken string) (SessionRecord, error)
	DeleteSession(ctx context.Context, accessToken string) error
	GetUsernameLock(ctx context.Context, username string) (UsernameBlockRecord, bool, error)
	SaveUsernameLock(ctx context.Context, record UsernameBlockRecord) error
	DeleteUsernameLock(ctx context.Context, username string) error
}

type SessionBulkStore interface {
	DeleteSessionsByUsername(ctx context.Context, username string) error
	DeleteSessionsByIdentitySubject(ctx context.Context, provider string, subject string) error
}

type SessionCatalogStore interface {
	ListSessionsByUsername(ctx context.Context, username string) ([]SessionRecord, error)
	DeleteSessionByID(ctx context.Context, username string, sessionID string) error
}

type SessionRotationStore interface {
	ReplaceSession(ctx context.Context, currentAccessToken string, replacement SessionRecord) error
}

type SessionUpdateStore interface {
	UpdateSession(ctx context.Context, session SessionRecord) error
}

type SessionConsumeStore interface {
	ConsumeSession(ctx context.Context, accessToken string) error
}

type UsernameFailureStore interface {
	RecordUsernameFailure(ctx context.Context, username string, reason string, now time.Time, window time.Duration, threshold int) error
}

type IPFailureStore interface {
	RecordIPFailure(ctx context.Context, ipAddress string, username string, reason string, now time.Time, window time.Duration, threshold int) error
}

type SplitAccountStore struct {
	accounts  AccountStore
	authState AuthStateStore
	ipLocks   IPBlockStore
}

func NewSplitAccountStore(accounts AccountStore, ipLocks IPBlockStore) *SplitAccountStore {
	return &SplitAccountStore{accounts: accounts, ipLocks: ipLocks}
}

func (s *SplitAccountStore) WithAuthStateStore(store AuthStateStore) *SplitAccountStore {
	if s != nil {
		s.authState = store
	}
	return s
}

type MemoryAccountStore struct {
	mu         sync.RWMutex
	accounts   map[string]Account
	challenges map[string]LoginChallenge
	sessions   map[string]SessionRecord
	ipLocks    map[string]IPBlockRecord
	userLocks  map[string]UsernameBlockRecord
}

func NewMemoryAccountStore() *MemoryAccountStore {
	return newMemoryAccountStore()
}

func newMemoryAccountStore() *MemoryAccountStore {
	return &MemoryAccountStore{
		accounts:   map[string]Account{},
		challenges: map[string]LoginChallenge{},
		sessions:   map[string]SessionRecord{},
		ipLocks:    map[string]IPBlockRecord{},
		userLocks:  map[string]UsernameBlockRecord{},
	}
}

func (s *SplitAccountStore) SaveAccount(ctx context.Context, account Account) error {
	if s == nil || s.accounts == nil {
		return ErrServiceDependency
	}
	return s.accounts.SaveAccount(ctx, account)
}

func (s *SplitAccountStore) GetAccountByUsername(ctx context.Context, username string) (Account, error) {
	if s == nil || s.accounts == nil {
		return Account{}, ErrServiceDependency
	}
	return s.accounts.GetAccountByUsername(ctx, username)
}

func (s *SplitAccountStore) GetAccountByAgentProfileID(ctx context.Context, agentProfileID string) (Account, error) {
	if s == nil || s.accounts == nil {
		return Account{}, ErrServiceDependency
	}
	if store, ok := s.accounts.(interface {
		GetAccountByAgentProfileID(context.Context, string) (Account, error)
	}); ok {
		return store.GetAccountByAgentProfileID(ctx, agentProfileID)
	}
	return Account{}, ErrAccountNotFound
}

func (s *SplitAccountStore) SaveLoginChallenge(ctx context.Context, challenge LoginChallenge) error {
	store, err := s.authStateStore()
	if err != nil {
		return err
	}
	return store.SaveLoginChallenge(ctx, challenge)
}

func (s *SplitAccountStore) ConsumeLoginChallenge(ctx context.Context, challengeID string) (LoginChallenge, error) {
	store, err := s.authStateStore()
	if err != nil {
		return LoginChallenge{}, err
	}
	return store.ConsumeLoginChallenge(ctx, challengeID)
}

func (s *SplitAccountStore) SaveSession(ctx context.Context, session SessionRecord) error {
	store, err := s.authStateStore()
	if err != nil {
		return err
	}
	return store.SaveSession(ctx, session)
}

func (s *SplitAccountStore) UpdateSession(ctx context.Context, session SessionRecord) error {
	store, err := s.authStateStore()
	if err != nil {
		return err
	}
	updater, ok := store.(SessionUpdateStore)
	if !ok {
		return ErrServiceDependency
	}
	return updater.UpdateSession(ctx, session)
}

func (s *SplitAccountStore) GetSession(ctx context.Context, accessToken string) (SessionRecord, error) {
	store, err := s.authStateStore()
	if err != nil {
		return SessionRecord{}, err
	}
	return store.GetSession(ctx, accessToken)
}

func (s *SplitAccountStore) DeleteSession(ctx context.Context, accessToken string) error {
	store, err := s.authStateStore()
	if err != nil {
		return err
	}
	return store.DeleteSession(ctx, accessToken)
}

func (s *SplitAccountStore) GetUsernameLock(ctx context.Context, username string) (UsernameBlockRecord, bool, error) {
	store, err := s.authStateStore()
	if err != nil {
		return UsernameBlockRecord{}, false, err
	}
	return store.GetUsernameLock(ctx, username)
}

func (s *SplitAccountStore) SaveUsernameLock(ctx context.Context, record UsernameBlockRecord) error {
	store, err := s.authStateStore()
	if err != nil {
		return err
	}
	return store.SaveUsernameLock(ctx, record)
}

func (s *SplitAccountStore) DeleteUsernameLock(ctx context.Context, username string) error {
	store, err := s.authStateStore()
	if err != nil {
		return err
	}
	return store.DeleteUsernameLock(ctx, username)
}

func (s *SplitAccountStore) RecordUsernameFailure(ctx context.Context, username string, reason string, now time.Time, window time.Duration, threshold int) error {
	store, err := s.authStateStore()
	if err != nil {
		return err
	}
	recorder, ok := store.(UsernameFailureStore)
	if ok {
		return recorder.RecordUsernameFailure(ctx, username, reason, now, window, threshold)
	}
	record, found, err := store.GetUsernameLock(ctx, username)
	if err != nil {
		return err
	}
	if found && normalizeIPLockStatus(record.Status) == "active" {
		return nil
	}
	if !found || normalizeIPLockStatus(record.Status) != "tracking" || (!record.UpdatedAt.IsZero() && !now.Before(record.UpdatedAt.Add(window))) {
		record = UsernameBlockRecord{Username: normalizeUsername(username), Status: "tracking"}
	}
	record.FailureCount++
	record.LastReason = strings.TrimSpace(reason)
	record.UpdatedAt = now.UTC()
	if record.FailureCount >= threshold {
		record.Status = "active"
		if record.LockedAt.IsZero() {
			record.LockedAt = now.UTC()
		}
	}
	return store.SaveUsernameLock(ctx, record)
}

func (s *SplitAccountStore) DeleteSessionsByUsername(ctx context.Context, username string) error {
	store, err := s.authStateStore()
	if err != nil {
		return err
	}
	bulk, ok := store.(SessionBulkStore)
	if !ok {
		return ErrServiceDependency
	}
	return bulk.DeleteSessionsByUsername(ctx, username)
}

func (s *SplitAccountStore) DeleteSessionsByIdentitySubject(ctx context.Context, provider string, subject string) error {
	store, err := s.authStateStore()
	if err != nil {
		return err
	}
	bulk, ok := store.(SessionBulkStore)
	if !ok {
		return ErrServiceDependency
	}
	return bulk.DeleteSessionsByIdentitySubject(ctx, provider, subject)
}

func (s *SplitAccountStore) ListSessionsByUsername(ctx context.Context, username string) ([]SessionRecord, error) {
	store, err := s.authStateStore()
	if err != nil {
		return nil, err
	}
	catalog, ok := store.(SessionCatalogStore)
	if !ok {
		return nil, ErrServiceDependency
	}
	return catalog.ListSessionsByUsername(ctx, username)
}

func (s *SplitAccountStore) DeleteSessionByID(ctx context.Context, username string, sessionID string) error {
	store, err := s.authStateStore()
	if err != nil {
		return err
	}
	catalog, ok := store.(SessionCatalogStore)
	if !ok {
		return ErrServiceDependency
	}
	return catalog.DeleteSessionByID(ctx, username, sessionID)
}

func (s *SplitAccountStore) ReplaceSession(ctx context.Context, currentAccessToken string, replacement SessionRecord) error {
	store, err := s.authStateStore()
	if err != nil {
		return err
	}
	rotator, ok := store.(SessionRotationStore)
	if !ok {
		return ErrServiceDependency
	}
	return rotator.ReplaceSession(ctx, currentAccessToken, replacement)
}

func (s *SplitAccountStore) ConsumeSession(ctx context.Context, accessToken string) error {
	store, err := s.authStateStore()
	if err != nil {
		return err
	}
	consumer, ok := store.(SessionConsumeStore)
	if !ok {
		return ErrServiceDependency
	}
	return consumer.ConsumeSession(ctx, accessToken)
}

func (s *SplitAccountStore) authStateStore() (AuthStateStore, error) {
	if s == nil {
		return nil, ErrServiceDependency
	}
	if s.authState != nil {
		return s.authState, nil
	}
	if s.accounts != nil {
		return s.accounts, nil
	}
	return nil, ErrServiceDependency
}

func (s *SplitAccountStore) GetIPLock(ctx context.Context, ipAddress string) (IPBlockRecord, bool, error) {
	store, err := s.ipBlockStore()
	if err != nil {
		return IPBlockRecord{}, false, err
	}
	return store.GetIPLock(ctx, ipAddress)
}

func (s *SplitAccountStore) SaveIPLock(ctx context.Context, record IPBlockRecord) error {
	store, err := s.ipBlockStore()
	if err != nil {
		return err
	}
	return store.SaveIPLock(ctx, record)
}

func (s *SplitAccountStore) DeleteIPLock(ctx context.Context, ipAddress string) error {
	store, err := s.ipBlockStore()
	if err != nil {
		return err
	}
	return store.DeleteIPLock(ctx, ipAddress)
}

func (s *SplitAccountStore) ListIPLocks(ctx context.Context, query IPBlockQuery) (IPBlockPageDTO, error) {
	store, err := s.ipBlockStore()
	if err != nil {
		return IPBlockPageDTO{}, err
	}
	return store.ListIPLocks(ctx, query)
}

func (s *SplitAccountStore) RecordIPFailure(ctx context.Context, ipAddress string, username string, reason string, now time.Time, window time.Duration, threshold int) error {
	store, err := s.ipBlockStore()
	if err != nil {
		return err
	}
	if recorder, ok := store.(IPFailureStore); ok {
		return recorder.RecordIPFailure(ctx, ipAddress, username, reason, now, window, threshold)
	}
	record, found, err := store.GetIPLock(ctx, ipAddress)
	if err != nil {
		return err
	}
	if found && normalizeIPLockStatus(record.Status) == "active" {
		return nil
	}
	if !found || normalizeIPLockStatus(record.Status) != "tracking" || (!record.UpdatedAt.IsZero() && !now.Before(record.UpdatedAt.Add(window))) {
		record = IPBlockRecord{IPAddress: normalizeIPAddress(ipAddress), Status: "tracking"}
	}
	record.FailureCount++
	record.LastUsername = normalizeUsername(username)
	record.LastReason = strings.TrimSpace(reason)
	record.UpdatedAt = now.UTC()
	if record.FailureCount >= threshold {
		record.Status = "active"
		if record.LockedAt.IsZero() {
			record.LockedAt = now.UTC()
		}
	}
	return store.SaveIPLock(ctx, record)
}

func (s *SplitAccountStore) UnlockIP(ctx context.Context, ipAddress string, actorID string, reason string) error {
	store, err := s.ipBlockStore()
	if err != nil {
		return err
	}
	if unlocker, ok := store.(interface {
		UnlockIP(context.Context, string, string, string) error
	}); ok {
		return unlocker.UnlockIP(ctx, ipAddress, actorID, reason)
	}
	return store.DeleteIPLock(ctx, ipAddress)
}

func (s *SplitAccountStore) ipBlockStore() (IPBlockStore, error) {
	if s == nil {
		return nil, ErrServiceDependency
	}
	if s.ipLocks != nil {
		return s.ipLocks, nil
	}
	if s.accounts != nil {
		return s.accounts, nil
	}
	return nil, ErrServiceDependency
}

func (s *MemoryAccountStore) SaveAccount(ctx context.Context, account Account) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return ErrServiceDependency
	}
	account = account.normalized()
	if account.Username == "" {
		return ErrInvalidRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.accounts[account.Username]; ok && strings.TrimSpace(existing.AgentProfileID) != "" && strings.TrimSpace(existing.AgentProfileID) != account.AgentProfileID {
		return ErrAccountConflict
	}
	for username, existing := range s.accounts {
		if username != account.Username && strings.TrimSpace(existing.AgentProfileID) == account.AgentProfileID {
			delete(s.accounts, username)
		}
	}
	s.accounts[account.Username] = account
	return nil
}

func (s *MemoryAccountStore) GetAccountByUsername(ctx context.Context, username string) (Account, error) {
	if err := ctx.Err(); err != nil {
		return Account{}, err
	}
	if s == nil {
		return Account{}, ErrServiceDependency
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	account, ok := s.accounts[normalizeUsername(username)]
	if !ok {
		return Account{}, ErrAccountNotFound
	}
	return account, nil
}

func (s *MemoryAccountStore) GetAccountByAgentProfileID(ctx context.Context, agentProfileID string) (Account, error) {
	if err := ctx.Err(); err != nil {
		return Account{}, err
	}
	if s == nil {
		return Account{}, ErrServiceDependency
	}
	agentProfileID = strings.TrimSpace(agentProfileID)
	if agentProfileID == "" {
		return Account{}, ErrAccountNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, account := range s.accounts {
		if strings.TrimSpace(account.AgentProfileID) == agentProfileID {
			return account, nil
		}
	}
	return Account{}, ErrAccountNotFound
}

func (s *MemoryAccountStore) SaveLoginChallenge(ctx context.Context, challenge LoginChallenge) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return ErrServiceDependency
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	for id, existing := range s.challenges {
		if (!existing.ExpiresAt.IsZero() && !existing.ExpiresAt.After(now)) ||
			(normalizeUsername(existing.Username) == normalizeUsername(challenge.Username) && normalizeIPAddress(existing.IPAddress) == normalizeIPAddress(challenge.IPAddress)) {
			delete(s.challenges, id)
		}
	}
	for username, record := range s.userLocks {
		expiresAt := record.UpdatedAt.Add(failedLoginWindow)
		if normalizeIPLockStatus(record.Status) == "active" {
			lockedAt := record.LockedAt
			if lockedAt.IsZero() {
				lockedAt = record.UpdatedAt
			}
			expiresAt = lockedAt.Add(failedLoginLockTTL)
		}
		if !expiresAt.IsZero() && !expiresAt.After(now) {
			delete(s.userLocks, username)
		}
	}
	s.challenges[challenge.ChallengeID] = challenge
	return nil
}

func (s *MemoryAccountStore) ConsumeLoginChallenge(ctx context.Context, challengeID string) (LoginChallenge, error) {
	if err := ctx.Err(); err != nil {
		return LoginChallenge{}, err
	}
	if s == nil {
		return LoginChallenge{}, ErrServiceDependency
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	challenge, ok := s.challenges[challengeID]
	if !ok {
		return LoginChallenge{}, ErrInvalidCredentials
	}
	delete(s.challenges, challengeID)
	return challenge, nil
}

func (s *MemoryAccountStore) SaveSession(ctx context.Context, session SessionRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return ErrServiceDependency
	}
	session.AccessToken = strings.TrimSpace(session.AccessToken)
	session.Username = normalizeUsername(session.Username)
	session.SubjectProvider = strings.ToLower(strings.TrimSpace(session.SubjectProvider))
	session.Subject = strings.TrimSpace(session.Subject)
	session.IdentitySubjectID = strings.TrimSpace(session.IdentitySubjectID)
	session.CredentialVersion = strings.TrimSpace(session.CredentialVersion)
	session.SessionID = strings.TrimSpace(session.SessionID)
	session.IPAddress = normalizeIPAddress(session.IPAddress)
	session.UserAgent = strings.TrimSpace(session.UserAgent)
	if session.AccessToken == "" || session.Username == "" {
		return ErrInvalidRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	for token, existing := range s.sessions {
		if !existing.ExpiresAt.IsZero() && !existing.ExpiresAt.After(now) {
			delete(s.sessions, token)
		}
	}
	s.sessions[session.AccessToken] = session
	return nil
}

func (s *MemoryAccountStore) UpdateSession(ctx context.Context, session SessionRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return ErrServiceDependency
	}
	session.AccessToken = strings.TrimSpace(session.AccessToken)
	session.Username = normalizeUsername(session.Username)
	session.SubjectProvider = strings.ToLower(strings.TrimSpace(session.SubjectProvider))
	session.Subject = strings.TrimSpace(session.Subject)
	session.IdentitySubjectID = strings.TrimSpace(session.IdentitySubjectID)
	session.CredentialVersion = strings.TrimSpace(session.CredentialVersion)
	session.SessionID = strings.TrimSpace(session.SessionID)
	session.IPAddress = normalizeIPAddress(session.IPAddress)
	session.UserAgent = strings.TrimSpace(session.UserAgent)
	if session.AccessToken == "" || session.Username == "" {
		return ErrInvalidRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[session.AccessToken]; !ok {
		return ErrUnauthorized
	}
	s.sessions[session.AccessToken] = session
	return nil
}

func (s *MemoryAccountStore) GetSession(ctx context.Context, accessToken string) (SessionRecord, error) {
	if err := ctx.Err(); err != nil {
		return SessionRecord{}, err
	}
	if s == nil {
		return SessionRecord{}, ErrServiceDependency
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[accessToken]
	if !ok {
		return SessionRecord{}, ErrUnauthorized
	}
	if !session.ExpiresAt.IsZero() && !session.ExpiresAt.After(time.Now().UTC()) {
		delete(s.sessions, accessToken)
		return SessionRecord{}, ErrUnauthorized
	}
	return session, nil
}

func (s *MemoryAccountStore) DeleteSession(ctx context.Context, accessToken string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return ErrServiceDependency
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, strings.TrimSpace(accessToken))
	return nil
}

func (s *MemoryAccountStore) ReplaceSession(ctx context.Context, currentAccessToken string, replacement SessionRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return ErrServiceDependency
	}
	currentAccessToken = strings.TrimSpace(currentAccessToken)
	replacement.AccessToken = strings.TrimSpace(replacement.AccessToken)
	replacement.Username = normalizeUsername(replacement.Username)
	replacement.SubjectProvider = strings.ToLower(strings.TrimSpace(replacement.SubjectProvider))
	replacement.Subject = strings.TrimSpace(replacement.Subject)
	replacement.IdentitySubjectID = strings.TrimSpace(replacement.IdentitySubjectID)
	replacement.CredentialVersion = strings.TrimSpace(replacement.CredentialVersion)
	replacement.SessionID = strings.TrimSpace(replacement.SessionID)
	replacement.IPAddress = normalizeIPAddress(replacement.IPAddress)
	replacement.UserAgent = strings.TrimSpace(replacement.UserAgent)
	if currentAccessToken == "" || replacement.AccessToken == "" || currentAccessToken == replacement.AccessToken || replacement.Username == "" {
		return ErrInvalidRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.sessions[currentAccessToken]
	if !ok || !current.ExpiresAt.IsZero() && !current.ExpiresAt.After(time.Now().UTC()) {
		delete(s.sessions, currentAccessToken)
		return ErrUnauthorized
	}
	if _, exists := s.sessions[replacement.AccessToken]; exists {
		return ErrInvalidRequest
	}
	delete(s.sessions, currentAccessToken)
	s.sessions[replacement.AccessToken] = replacement
	return nil
}

func (s *MemoryAccountStore) ConsumeSession(ctx context.Context, accessToken string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return ErrServiceDependency
	}
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return ErrInvalidRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[accessToken]; !ok {
		return ErrUnauthorized
	}
	delete(s.sessions, accessToken)
	return nil
}

func (s *MemoryAccountStore) GetUsernameLock(ctx context.Context, username string) (UsernameBlockRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return UsernameBlockRecord{}, false, err
	}
	if s == nil {
		return UsernameBlockRecord{}, false, ErrServiceDependency
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.userLocks[normalizeUsername(username)]
	return record, ok, nil
}

func (s *MemoryAccountStore) SaveUsernameLock(ctx context.Context, record UsernameBlockRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return ErrServiceDependency
	}
	record.Username = normalizeUsername(record.Username)
	if record.Username == "" {
		return ErrInvalidRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userLocks[record.Username] = record
	return nil
}

func (s *MemoryAccountStore) DeleteUsernameLock(ctx context.Context, username string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return ErrServiceDependency
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.userLocks, normalizeUsername(username))
	return nil
}

func (s *MemoryAccountStore) RecordUsernameFailure(ctx context.Context, username string, reason string, now time.Time, window time.Duration, threshold int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return ErrServiceDependency
	}
	username = normalizeUsername(username)
	if username == "" || threshold <= 0 {
		return ErrInvalidRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.userLocks[username]
	if ok && normalizeIPLockStatus(record.Status) == "active" {
		return nil
	}
	if !ok || normalizeIPLockStatus(record.Status) != "tracking" || (!record.UpdatedAt.IsZero() && !now.Before(record.UpdatedAt.Add(window))) {
		record = UsernameBlockRecord{Username: username, Status: "tracking"}
	}
	record.FailureCount++
	record.LastReason = strings.TrimSpace(reason)
	record.UpdatedAt = now.UTC()
	if record.FailureCount >= threshold {
		record.Status = "active"
		if record.LockedAt.IsZero() {
			record.LockedAt = now.UTC()
		}
	}
	s.userLocks[username] = record
	return nil
}

func (s *MemoryAccountStore) DeleteSessionsByUsername(ctx context.Context, username string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return ErrServiceDependency
	}
	username = normalizeUsername(username)
	if username == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for token, session := range s.sessions {
		if normalizeUsername(session.Username) == username {
			delete(s.sessions, token)
		}
	}
	return nil
}

func (s *MemoryAccountStore) DeleteSessionsByIdentitySubject(ctx context.Context, provider string, subject string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return ErrServiceDependency
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	subject = strings.TrimSpace(subject)
	if provider == "" || subject == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for token, session := range s.sessions {
		if strings.EqualFold(strings.TrimSpace(session.SubjectProvider), provider) && strings.TrimSpace(session.Subject) == subject {
			delete(s.sessions, token)
		}
	}
	return nil
}

func (s *MemoryAccountStore) ListSessionsByUsername(ctx context.Context, username string) ([]SessionRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, ErrServiceDependency
	}
	username = normalizeUsername(username)
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []SessionRecord{}
	for _, session := range s.sessions {
		if normalizeUsername(session.Username) == username {
			out = append(out, session)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeenAt.After(out[j].LastSeenAt) })
	return out, nil
}

func (s *MemoryAccountStore) DeleteSessionByID(ctx context.Context, username string, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return ErrServiceDependency
	}
	username = normalizeUsername(username)
	sessionID = strings.TrimSpace(sessionID)
	s.mu.Lock()
	defer s.mu.Unlock()
	for token, session := range s.sessions {
		if normalizeUsername(session.Username) == username && strings.TrimSpace(session.SessionID) == sessionID {
			delete(s.sessions, token)
		}
	}
	return nil
}

func (s *MemoryAccountStore) GetIPLock(ctx context.Context, ipAddress string) (IPBlockRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return IPBlockRecord{}, false, err
	}
	if s == nil {
		return IPBlockRecord{}, false, ErrServiceDependency
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.ipLocks[normalizeIPAddress(ipAddress)]
	return record, ok, nil
}

func (s *MemoryAccountStore) SaveIPLock(ctx context.Context, record IPBlockRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return ErrServiceDependency
	}
	record = normalizeIPBlockRecord(record)
	if record.IPAddress == "" {
		return ErrInvalidRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ipLocks[record.IPAddress] = record
	return nil
}

func (s *MemoryAccountStore) RecordIPFailure(ctx context.Context, ipAddress string, username string, reason string, now time.Time, window time.Duration, threshold int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return ErrServiceDependency
	}
	ipAddress = normalizeIPAddress(ipAddress)
	if ipAddress == "" || threshold <= 0 {
		return ErrInvalidRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.ipLocks[ipAddress]
	if ok && normalizeIPLockStatus(record.Status) == "active" {
		return nil
	}
	if !ok || normalizeIPLockStatus(record.Status) != "tracking" || (!record.UpdatedAt.IsZero() && !now.Before(record.UpdatedAt.Add(window))) {
		record = IPBlockRecord{IPAddress: ipAddress, Status: "tracking"}
	}
	record.FailureCount++
	record.LastUsername = normalizeUsername(username)
	record.LastReason = strings.TrimSpace(reason)
	record.UpdatedAt = now.UTC()
	if record.FailureCount >= threshold {
		record.Status = "active"
		if record.LockedAt.IsZero() {
			record.LockedAt = now.UTC()
		}
	}
	s.ipLocks[ipAddress] = record
	return nil
}

func (s *MemoryAccountStore) DeleteIPLock(ctx context.Context, ipAddress string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return ErrServiceDependency
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.ipLocks, normalizeIPAddress(ipAddress))
	return nil
}

func (s *MemoryAccountStore) ListIPLocks(ctx context.Context, query IPBlockQuery) (IPBlockPageDTO, error) {
	if err := ctx.Err(); err != nil {
		return IPBlockPageDTO{}, err
	}
	if s == nil {
		return IPBlockPageDTO{}, ErrServiceDependency
	}
	status := normalizeIPLockStatus(query.Status)
	s.mu.RLock()
	defer s.mu.RUnlock()
	locks := make([]IPBlockRecord, 0, len(s.ipLocks))
	for _, record := range s.ipLocks {
		record = normalizeIPBlockRecord(record)
		if status != "" && record.Status != status {
			continue
		}
		locks = append(locks, record)
	}
	sort.Slice(locks, func(i, j int) bool {
		left := locks[i].LockedAt
		if left.IsZero() {
			left = locks[i].UpdatedAt
		}
		right := locks[j].LockedAt
		if right.IsZero() {
			right = locks[j].UpdatedAt
		}
		if left.Equal(right) {
			return locks[i].IPAddress < locks[j].IPAddress
		}
		return left.After(right)
	})
	total := len(locks)
	offset := query.Offset
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	limit := query.Limit
	if limit <= 0 || offset+limit > total {
		limit = total - offset
	}
	pageLocks := locks[offset : offset+limit]
	return IPBlockPageDTO{
		Locks: pageLocks,
		Page:  PageDTO{Limit: query.Limit, Offset: query.Offset, Total: total, Returned: len(pageLocks), HasMore: offset+limit < total},
	}, nil
}

func normalizeIPBlockRecord(record IPBlockRecord) IPBlockRecord {
	record.IPAddress = normalizeIPAddress(record.IPAddress)
	record.Status = normalizeIPLockStatus(record.Status)
	if record.Status == "" {
		record.Status = "active"
	}
	if record.FailureCount < 0 {
		record.FailureCount = 0
	}
	record.LastUsername = normalizeUsername(record.LastUsername)
	return record
}

func normalizeIPLockStatus(status string) string {
	return normalizeUsername(status)
}
