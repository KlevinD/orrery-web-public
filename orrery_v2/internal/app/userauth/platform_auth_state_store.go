package userauth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

type PlatformAuthStateStore struct {
	store *db.Store
	now   func() time.Time
}

func NewPlatformAuthStateStore(store *db.Store) *PlatformAuthStateStore {
	return &PlatformAuthStateStore{store: store, now: func() time.Time { return time.Now().UTC() }}
}

func (s *PlatformAuthStateStore) ConsumeTOTPCounter(ctx context.Context, username string, counter int64) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if s == nil || s.store == nil {
		return false, ErrServiceDependency
	}
	username = normalizeUsername(username)
	if username == "" || counter < 0 {
		return false, ErrInvalidRequest
	}
	result, err := s.store.Exec(ctx, `
INSERT INTO auth_mfa_replay_counters (username, last_accepted_counter, updated_at)
VALUES (?, ?, ?)
ON CONFLICT(username) DO UPDATE SET
	last_accepted_counter = excluded.last_accepted_counter,
	updated_at = excluded.updated_at
WHERE auth_mfa_replay_counters.last_accepted_counter < excluded.last_accepted_counter`,
		username,
		counter,
		authStateTimeToUnixNano(s.now()),
	)
	if err != nil {
		return false, fmt.Errorf("consume TOTP replay counter: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("consume TOTP replay counter result: %w", err)
	}
	return affected == 1, nil
}

func (s *PlatformAuthStateStore) SaveLoginChallenge(ctx context.Context, challenge LoginChallenge) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.store == nil {
		return ErrServiceDependency
	}
	challenge.ChallengeID = strings.TrimSpace(challenge.ChallengeID)
	challenge.Username = normalizeUsername(challenge.Username)
	challenge.IPAddress = normalizeIPAddress(challenge.IPAddress)
	challenge.Salt = strings.TrimSpace(challenge.Salt)
	if challenge.ChallengeID == "" || challenge.Username == "" || challenge.Salt == "" {
		return ErrInvalidRequest
	}
	now := s.now()
	_, _ = s.store.Exec(ctx, `DELETE FROM auth_login_challenges WHERE expires_at <= ? OR (username = ? AND ip_address = ?)`, authStateTimeToUnixNano(now), challenge.Username, challenge.IPAddress)
	_, _ = s.store.Exec(ctx, `
DELETE FROM auth_login_account_throttles
WHERE (status = 'tracking' AND updated_at <= ?)
	OR (status = 'active' AND CASE WHEN locked_at = 0 THEN updated_at ELSE locked_at END <= ?)`,
		authStateTimeToUnixNano(now.Add(-failedLoginWindow)),
		authStateTimeToUnixNano(now.Add(-failedLoginLockTTL)),
	)
	_, err := s.store.Exec(ctx, `
INSERT INTO auth_login_challenges (
	challenge_id, username, ip_address, salt, expires_at, created_at
) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(challenge_id) DO UPDATE SET
	username = excluded.username,
	ip_address = excluded.ip_address,
	salt = excluded.salt,
	expires_at = excluded.expires_at,
	created_at = excluded.created_at`,
		challenge.ChallengeID,
		challenge.Username,
		challenge.IPAddress,
		challenge.Salt,
		authStateTimeToUnixNano(challenge.ExpiresAt),
		authStateTimeToUnixNano(s.now()),
	)
	if err != nil {
		return fmt.Errorf("save login challenge: %w", err)
	}
	return nil
}

func (s *PlatformAuthStateStore) ConsumeLoginChallenge(ctx context.Context, challengeID string) (LoginChallenge, error) {
	if err := ctx.Err(); err != nil {
		return LoginChallenge{}, err
	}
	if s == nil || s.store == nil {
		return LoginChallenge{}, ErrServiceDependency
	}
	challengeID = strings.TrimSpace(challengeID)
	if challengeID == "" {
		return LoginChallenge{}, ErrInvalidCredentials
	}
	var challenge LoginChallenge
	err := s.store.WithTx(ctx, func(tx db.Tx) error {
		var expiresAt int64
		err := tx.QueryRowContext(ctx, `
SELECT challenge_id, username, ip_address, salt, expires_at
FROM auth_login_challenges
WHERE challenge_id = ?`, challengeID).Scan(
			&challenge.ChallengeID,
			&challenge.Username,
			&challenge.IPAddress,
			&challenge.Salt,
			&expiresAt,
		)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidCredentials
		}
		if err != nil {
			return fmt.Errorf("get login challenge: %w", err)
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM auth_login_challenges WHERE challenge_id = ?`, challengeID)
		if err != nil {
			return fmt.Errorf("consume login challenge: %w", err)
		}
		deleted, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("consume login challenge result: %w", err)
		}
		if deleted != 1 {
			return ErrInvalidCredentials
		}
		challenge.ExpiresAt = authStateUnixNanoToTime(expiresAt)
		return nil
	})
	if err != nil {
		return LoginChallenge{}, err
	}
	return challenge, nil
}

func (s *PlatformAuthStateStore) SaveSession(ctx context.Context, session SessionRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.store == nil {
		return ErrServiceDependency
	}
	tokenHash := appSessionTokenHash(session.AccessToken)
	session.Username = normalizeUsername(session.Username)
	session.SubjectProvider = strings.ToLower(strings.TrimSpace(session.SubjectProvider))
	session.Subject = strings.TrimSpace(session.Subject)
	session.IdentitySubjectID = strings.TrimSpace(session.IdentitySubjectID)
	session.CredentialVersion = strings.TrimSpace(session.CredentialVersion)
	session.SessionID = strings.TrimSpace(session.SessionID)
	session.IPAddress = normalizeIPAddress(session.IPAddress)
	session.UserAgent = strings.TrimSpace(session.UserAgent)
	if tokenHash == "" || session.Username == "" {
		return ErrInvalidRequest
	}
	now := s.now()
	if session.SessionID == "" {
		session.SessionID = "legacy-" + tokenHash[:24]
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	if session.LastSeenAt.IsZero() {
		session.LastSeenAt = session.CreatedAt
	}
	err := s.store.WithTx(ctx, func(tx db.Tx) error {
		_, _ = tx.ExecContext(ctx, `DELETE FROM auth_app_session_metadata WHERE token_hash IN (SELECT token_hash FROM auth_app_sessions WHERE expires_at <= ?)`, authStateTimeToUnixNano(now))
		_, _ = tx.ExecContext(ctx, `DELETE FROM auth_app_sessions WHERE expires_at <= ?`, authStateTimeToUnixNano(now))
		_, err := tx.ExecContext(ctx, `
INSERT INTO auth_app_sessions (
	token_hash, username, subject_provider, subject, identity_subject_id, expires_at, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(token_hash) DO UPDATE SET
	username = excluded.username,
	subject_provider = excluded.subject_provider,
	subject = excluded.subject,
	identity_subject_id = excluded.identity_subject_id,
	expires_at = excluded.expires_at`,
			tokenHash,
			session.Username,
			session.SubjectProvider,
			session.Subject,
			session.IdentitySubjectID,
			authStateTimeToUnixNano(session.ExpiresAt),
			authStateTimeToUnixNano(session.CreatedAt),
		)
		if err != nil {
			return fmt.Errorf("save app session: %w", err)
		}
		mustChangePassword := 0
		if session.MustChangePassword {
			mustChangePassword = 1
		}
		_, err = tx.ExecContext(ctx, `
	INSERT INTO auth_app_session_metadata (
		token_hash, session_id, created_at, last_seen_at, must_change_password, ip_address, user_agent, credential_version
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(token_hash) DO UPDATE SET
		session_id = excluded.session_id,
		created_at = excluded.created_at,
		last_seen_at = excluded.last_seen_at,
		must_change_password = excluded.must_change_password,
		ip_address = excluded.ip_address,
		user_agent = excluded.user_agent,
		credential_version = excluded.credential_version`,
			tokenHash,
			session.SessionID,
			authStateTimeToUnixNano(session.CreatedAt),
			authStateTimeToUnixNano(session.LastSeenAt),
			mustChangePassword,
			session.IPAddress,
			session.UserAgent,
			session.CredentialVersion,
		)
		if err != nil {
			return fmt.Errorf("save app session metadata: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

func (s *PlatformAuthStateStore) UpdateSession(ctx context.Context, session SessionRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.store == nil {
		return ErrServiceDependency
	}
	tokenHash := appSessionTokenHash(session.AccessToken)
	session.Username = normalizeUsername(session.Username)
	session.CredentialVersion = strings.TrimSpace(session.CredentialVersion)
	session.SessionID = strings.TrimSpace(session.SessionID)
	session.IPAddress = normalizeIPAddress(session.IPAddress)
	session.UserAgent = strings.TrimSpace(session.UserAgent)
	if tokenHash == "" || session.Username == "" || session.SessionID == "" {
		return ErrInvalidRequest
	}
	mustChangePassword := 0
	if session.MustChangePassword {
		mustChangePassword = 1
	}
	result, err := s.store.Exec(ctx, `
	UPDATE auth_app_session_metadata
	SET session_id = ?, created_at = ?, last_seen_at = ?, must_change_password = ?,
		ip_address = ?, user_agent = ?, credential_version = ?
	WHERE token_hash = ?
		AND EXISTS (
			SELECT 1 FROM auth_app_sessions s
			WHERE s.token_hash = auth_app_session_metadata.token_hash AND s.username = ?
		)`,
		session.SessionID,
		authStateTimeToUnixNano(session.CreatedAt),
		authStateTimeToUnixNano(session.LastSeenAt),
		mustChangePassword,
		session.IPAddress,
		session.UserAgent,
		session.CredentialVersion,
		tokenHash,
		session.Username,
	)
	if err != nil {
		return fmt.Errorf("update app session metadata: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update app session metadata result: %w", err)
	}
	if updated != 1 {
		return ErrUnauthorized
	}
	return nil
}

func (s *PlatformAuthStateStore) GetSession(ctx context.Context, accessToken string) (SessionRecord, error) {
	if err := ctx.Err(); err != nil {
		return SessionRecord{}, err
	}
	if s == nil || s.store == nil {
		return SessionRecord{}, ErrServiceDependency
	}
	accessToken = strings.TrimSpace(accessToken)
	tokenHash := appSessionTokenHash(accessToken)
	if tokenHash == "" {
		return SessionRecord{}, ErrUnauthorized
	}
	var session SessionRecord
	var expiresAt, createdAt, lastSeenAt int64
	var mustChangePassword int
	err := s.store.QueryRow(ctx, `
	SELECT s.username, s.subject_provider, s.subject, s.identity_subject_id, s.expires_at,
		m.session_id, m.created_at, m.last_seen_at, m.must_change_password, m.ip_address, m.user_agent, m.credential_version
FROM auth_app_sessions s
JOIN auth_app_session_metadata m ON m.token_hash = s.token_hash
WHERE s.token_hash = ?`, tokenHash).Scan(
		&session.Username,
		&session.SubjectProvider,
		&session.Subject,
		&session.IdentitySubjectID,
		&expiresAt,
		&session.SessionID,
		&createdAt,
		&lastSeenAt,
		&mustChangePassword,
		&session.IPAddress,
		&session.UserAgent,
		&session.CredentialVersion,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionRecord{}, ErrUnauthorized
	}
	if err != nil {
		return SessionRecord{}, fmt.Errorf("get app session: %w", err)
	}
	session.AccessToken = accessToken
	session.ExpiresAt = authStateUnixNanoToTime(expiresAt)
	session.CreatedAt = authStateUnixNanoToTime(createdAt)
	session.LastSeenAt = authStateUnixNanoToTime(lastSeenAt)
	session.MustChangePassword = mustChangePassword != 0
	if !session.ExpiresAt.IsZero() && !session.ExpiresAt.After(s.now()) {
		_ = s.DeleteSession(ctx, accessToken)
		return SessionRecord{}, ErrUnauthorized
	}
	return session, nil
}

func (s *PlatformAuthStateStore) DeleteSession(ctx context.Context, accessToken string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.store == nil {
		return ErrServiceDependency
	}
	tokenHash := appSessionTokenHash(accessToken)
	if tokenHash == "" {
		return nil
	}
	return s.store.WithTx(ctx, func(tx db.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM auth_app_session_metadata WHERE token_hash = ?`, tokenHash); err != nil {
			return fmt.Errorf("delete app session metadata: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM auth_app_sessions WHERE token_hash = ?`, tokenHash); err != nil {
			return fmt.Errorf("delete app session: %w", err)
		}
		return nil
	})
}

func (s *PlatformAuthStateStore) ReplaceSession(ctx context.Context, currentAccessToken string, replacement SessionRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.store == nil {
		return ErrServiceDependency
	}
	currentTokenHash := appSessionTokenHash(currentAccessToken)
	replacementTokenHash := appSessionTokenHash(replacement.AccessToken)
	replacement.Username = normalizeUsername(replacement.Username)
	replacement.SubjectProvider = strings.ToLower(strings.TrimSpace(replacement.SubjectProvider))
	replacement.Subject = strings.TrimSpace(replacement.Subject)
	replacement.IdentitySubjectID = strings.TrimSpace(replacement.IdentitySubjectID)
	replacement.CredentialVersion = strings.TrimSpace(replacement.CredentialVersion)
	replacement.SessionID = strings.TrimSpace(replacement.SessionID)
	replacement.IPAddress = normalizeIPAddress(replacement.IPAddress)
	replacement.UserAgent = strings.TrimSpace(replacement.UserAgent)
	if currentTokenHash == "" || replacementTokenHash == "" || currentTokenHash == replacementTokenHash || replacement.Username == "" {
		return ErrInvalidRequest
	}
	now := s.now()
	if replacement.SessionID == "" {
		replacement.SessionID = "legacy-" + replacementTokenHash[:24]
	}
	if replacement.CreatedAt.IsZero() {
		replacement.CreatedAt = now
	}
	if replacement.LastSeenAt.IsZero() {
		replacement.LastSeenAt = replacement.CreatedAt
	}
	mustChangePassword := 0
	if replacement.MustChangePassword {
		mustChangePassword = 1
	}
	return s.store.WithTx(ctx, func(tx db.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM auth_app_session_metadata WHERE token_hash = ?`, currentTokenHash); err != nil {
			return fmt.Errorf("delete rotated app session metadata: %w", err)
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM auth_app_sessions WHERE token_hash = ?`, currentTokenHash)
		if err != nil {
			return fmt.Errorf("delete rotated app session: %w", err)
		}
		deleted, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("delete rotated app session result: %w", err)
		}
		if deleted != 1 {
			return ErrUnauthorized
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO auth_app_sessions (
	token_hash, username, subject_provider, subject, identity_subject_id, expires_at, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			replacementTokenHash,
			replacement.Username,
			replacement.SubjectProvider,
			replacement.Subject,
			replacement.IdentitySubjectID,
			authStateTimeToUnixNano(replacement.ExpiresAt),
			authStateTimeToUnixNano(replacement.CreatedAt),
		); err != nil {
			return fmt.Errorf("save rotated app session: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO auth_app_session_metadata (
	token_hash, session_id, created_at, last_seen_at, must_change_password, ip_address, user_agent, credential_version
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			replacementTokenHash,
			replacement.SessionID,
			authStateTimeToUnixNano(replacement.CreatedAt),
			authStateTimeToUnixNano(replacement.LastSeenAt),
			mustChangePassword,
			replacement.IPAddress,
			replacement.UserAgent,
			replacement.CredentialVersion,
		); err != nil {
			return fmt.Errorf("save rotated app session metadata: %w", err)
		}
		return nil
	})
}

func (s *PlatformAuthStateStore) ConsumeSession(ctx context.Context, accessToken string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.store == nil {
		return ErrServiceDependency
	}
	tokenHash := appSessionTokenHash(accessToken)
	if tokenHash == "" {
		return ErrInvalidRequest
	}
	return s.store.WithTx(ctx, func(tx db.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM auth_app_session_metadata WHERE token_hash = ?`, tokenHash); err != nil {
			return fmt.Errorf("consume app session metadata: %w", err)
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM auth_app_sessions WHERE token_hash = ?`, tokenHash)
		if err != nil {
			return fmt.Errorf("consume app session: %w", err)
		}
		deleted, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("consume app session result: %w", err)
		}
		if deleted != 1 {
			return ErrUnauthorized
		}
		return nil
	})
}

func (s *PlatformAuthStateStore) GetUsernameLock(ctx context.Context, username string) (UsernameBlockRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return UsernameBlockRecord{}, false, err
	}
	if s == nil || s.store == nil {
		return UsernameBlockRecord{}, false, ErrServiceDependency
	}
	username = normalizeUsername(username)
	if username == "" {
		return UsernameBlockRecord{}, false, nil
	}
	var record UsernameBlockRecord
	var lockedAt, updatedAt int64
	err := s.store.QueryRow(ctx, `SELECT username, status, failure_count, last_reason, locked_at, updated_at FROM auth_login_account_throttles WHERE username = ?`, username).Scan(
		&record.Username, &record.Status, &record.FailureCount, &record.LastReason, &lockedAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return UsernameBlockRecord{}, false, nil
	}
	if err != nil {
		return UsernameBlockRecord{}, false, fmt.Errorf("get account login throttle: %w", err)
	}
	record.LockedAt = authStateUnixNanoToTime(lockedAt)
	record.UpdatedAt = authStateUnixNanoToTime(updatedAt)
	return record, true, nil
}

func (s *PlatformAuthStateStore) SaveUsernameLock(ctx context.Context, record UsernameBlockRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.store == nil {
		return ErrServiceDependency
	}
	record.Username = normalizeUsername(record.Username)
	if record.Username == "" {
		return ErrInvalidRequest
	}
	_, err := s.store.Exec(ctx, `
		INSERT INTO auth_login_account_throttles (username, status, failure_count, last_reason, locked_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(username) DO UPDATE SET status = excluded.status, failure_count = excluded.failure_count,
			last_reason = excluded.last_reason, locked_at = excluded.locked_at, updated_at = excluded.updated_at`,
		record.Username, record.Status, record.FailureCount, strings.TrimSpace(record.LastReason), authStateTimeToUnixNano(record.LockedAt), authStateTimeToUnixNano(record.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("save account login throttle: %w", err)
	}
	return nil
}

func (s *PlatformAuthStateStore) DeleteUsernameLock(ctx context.Context, username string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.store == nil {
		return ErrServiceDependency
	}
	username = normalizeUsername(username)
	if username == "" {
		return nil
	}
	if _, err := s.store.Exec(ctx, `DELETE FROM auth_login_account_throttles WHERE username = ?`, username); err != nil {
		return fmt.Errorf("delete account login throttle: %w", err)
	}
	return nil
}

func (s *PlatformAuthStateStore) RecordUsernameFailure(ctx context.Context, username string, reason string, now time.Time, window time.Duration, threshold int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.store == nil {
		return ErrServiceDependency
	}
	username = normalizeUsername(username)
	if username == "" || threshold <= 0 {
		return ErrInvalidRequest
	}
	now = now.UTC()
	return s.store.WithTx(ctx, func(tx db.Tx) error {
		query := `SELECT status, failure_count, last_reason, locked_at, updated_at FROM auth_login_account_throttles WHERE username = ?`
		if dialecter, ok := tx.(interface{ Dialect() db.Dialect }); ok && dialecter.Dialect() == db.DialectPostgres {
			query += ` FOR UPDATE`
		}
		var record UsernameBlockRecord
		var lockedAt, updatedAt int64
		err := tx.QueryRowContext(ctx, query, username).Scan(&record.Status, &record.FailureCount, &record.LastReason, &lockedAt, &updatedAt)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("get account login throttle for update: %w", err)
		}
		record.Username = username
		record.LockedAt = authStateUnixNanoToTime(lockedAt)
		record.UpdatedAt = authStateUnixNanoToTime(updatedAt)
		if err == nil && normalizeIPLockStatus(record.Status) == "active" {
			return nil
		}
		if errors.Is(err, sql.ErrNoRows) || normalizeIPLockStatus(record.Status) != "tracking" || (!record.UpdatedAt.IsZero() && !now.Before(record.UpdatedAt.Add(window))) {
			record = UsernameBlockRecord{Username: username, Status: "tracking"}
		}
		record.FailureCount++
		record.LastReason = strings.TrimSpace(reason)
		record.UpdatedAt = now
		if record.FailureCount >= threshold {
			record.Status = "active"
			if record.LockedAt.IsZero() {
				record.LockedAt = now
			}
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO auth_login_account_throttles (username, status, failure_count, last_reason, locked_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(username) DO UPDATE SET
	status = excluded.status,
	failure_count = excluded.failure_count,
	last_reason = excluded.last_reason,
	locked_at = excluded.locked_at,
	updated_at = excluded.updated_at`,
			record.Username,
			record.Status,
			record.FailureCount,
			record.LastReason,
			authStateTimeToUnixNano(record.LockedAt),
			authStateTimeToUnixNano(record.UpdatedAt),
		)
		if err != nil {
			return fmt.Errorf("save account login throttle: %w", err)
		}
		return nil
	})
}

func (s *PlatformAuthStateStore) DeleteSessionsByUsername(ctx context.Context, username string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.store == nil {
		return ErrServiceDependency
	}
	username = normalizeUsername(username)
	if username == "" {
		return nil
	}
	return s.store.WithTx(ctx, func(tx db.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM auth_app_session_metadata WHERE token_hash IN (SELECT token_hash FROM auth_app_sessions WHERE username = ?)`, username); err != nil {
			return fmt.Errorf("delete app session metadata by username: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM auth_app_sessions WHERE username = ?`, username); err != nil {
			return fmt.Errorf("delete app sessions by username: %w", err)
		}
		return nil
	})
}

func (s *PlatformAuthStateStore) DeleteSessionsByIdentitySubject(ctx context.Context, provider string, subject string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.store == nil {
		return ErrServiceDependency
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	subject = strings.TrimSpace(subject)
	if provider == "" || subject == "" {
		return nil
	}
	return s.store.WithTx(ctx, func(tx db.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM auth_app_session_metadata WHERE token_hash IN (SELECT token_hash FROM auth_app_sessions WHERE subject_provider = ? AND subject = ?)`, provider, subject); err != nil {
			return fmt.Errorf("delete app session metadata by identity subject: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM auth_app_sessions WHERE subject_provider = ? AND subject = ?`, provider, subject); err != nil {
			return fmt.Errorf("delete app sessions by identity subject: %w", err)
		}
		return nil
	})
}

func (s *PlatformAuthStateStore) ListSessionsByUsername(ctx context.Context, username string) ([]SessionRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil {
		return nil, ErrServiceDependency
	}
	username = normalizeUsername(username)
	rows, err := s.store.Query(ctx, `
	SELECT s.username, s.subject_provider, s.subject, s.identity_subject_id, s.expires_at,
		m.session_id, m.created_at, m.last_seen_at, m.must_change_password, m.ip_address, m.user_agent, m.credential_version
FROM auth_app_sessions s
JOIN auth_app_session_metadata m ON m.token_hash = s.token_hash
WHERE s.username = ? AND s.expires_at > ?
ORDER BY m.last_seen_at DESC`, username, authStateTimeToUnixNano(s.now()))
	if err != nil {
		return nil, fmt.Errorf("list app sessions: %w", err)
	}
	defer rows.Close()
	out := []SessionRecord{}
	for rows.Next() {
		var session SessionRecord
		var expiresAt, createdAt, lastSeenAt int64
		var mustChangePassword int
		if err := rows.Scan(
			&session.Username,
			&session.SubjectProvider,
			&session.Subject,
			&session.IdentitySubjectID,
			&expiresAt,
			&session.SessionID,
			&createdAt,
			&lastSeenAt,
			&mustChangePassword,
			&session.IPAddress,
			&session.UserAgent,
			&session.CredentialVersion,
		); err != nil {
			return nil, fmt.Errorf("scan app session: %w", err)
		}
		session.ExpiresAt = authStateUnixNanoToTime(expiresAt)
		session.CreatedAt = authStateUnixNanoToTime(createdAt)
		session.LastSeenAt = authStateUnixNanoToTime(lastSeenAt)
		session.MustChangePassword = mustChangePassword != 0
		out = append(out, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list app sessions rows: %w", err)
	}
	return out, nil
}

func (s *PlatformAuthStateStore) DeleteSessionByID(ctx context.Context, username string, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.store == nil {
		return ErrServiceDependency
	}
	username = normalizeUsername(username)
	sessionID = strings.TrimSpace(sessionID)
	if username == "" || sessionID == "" {
		return ErrInvalidRequest
	}
	return s.store.WithTx(ctx, func(tx db.Tx) error {
		var tokenHash string
		err := tx.QueryRowContext(ctx, `
SELECT m.token_hash
FROM auth_app_session_metadata m
JOIN auth_app_sessions s ON s.token_hash = m.token_hash
WHERE s.username = ? AND m.session_id = ?`, username, sessionID).Scan(&tokenHash)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("find app session by id: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM auth_app_session_metadata WHERE token_hash = ?`, tokenHash); err != nil {
			return fmt.Errorf("delete app session metadata by id: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM auth_app_sessions WHERE token_hash = ?`, tokenHash); err != nil {
			return fmt.Errorf("delete app session by id: %w", err)
		}
		return nil
	})
}

func appSessionTokenHash(accessToken string) string {
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(accessToken))
	return hex.EncodeToString(digest[:])
}

func authStateTimeToUnixNano(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixNano()
}

func authStateUnixNanoToTime(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(0, value).UTC()
}

var _ AuthStateStore = (*PlatformAuthStateStore)(nil)
var _ SessionBulkStore = (*PlatformAuthStateStore)(nil)
var _ SessionCatalogStore = (*PlatformAuthStateStore)(nil)
var _ SessionConsumeStore = (*PlatformAuthStateStore)(nil)
var _ SessionRotationStore = (*PlatformAuthStateStore)(nil)
var _ SessionUpdateStore = (*PlatformAuthStateStore)(nil)
var _ UsernameFailureStore = (*PlatformAuthStateStore)(nil)
var _ TOTPReplayStore = (*PlatformAuthStateStore)(nil)
