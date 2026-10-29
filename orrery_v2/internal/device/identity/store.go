package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

type SQLStore struct {
	store *db.Store
}

func NewSQLStore(ctx context.Context, store *db.Store) (*SQLStore, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: core database is required", ErrDependency)
	}
	for _, statement := range schemaStatements(store.Dialect()) {
		if _, err := store.Exec(ctx, statement); err != nil {
			return nil, fmt.Errorf("initialize device identity store: %w", err)
		}
	}
	return &SQLStore{store: store}, nil
}

func (s *SQLStore) SaveAuthorizationCode(ctx context.Context, record codeRecord) error {
	_, err := s.store.Exec(ctx, `
INSERT INTO device_oauth_codes (
 code_hash, client_id, redirect_uri, code_challenge, tenant_id, owner_id, owner_display_name,
 expires_at, created_at, consumed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`, record.CodeHash, record.ClientID, record.RedirectURI,
		record.CodeChallenge, record.TenantID, record.OwnerID, record.OwnerDisplayName, record.ExpiresAt.UnixNano(), record.CreatedAt.UnixNano())
	if err != nil {
		if isUniqueConstraint(err) {
			return ErrConflict
		}
		return fmt.Errorf("save device authorization code: %w", err)
	}
	return nil
}

func (s *SQLStore) ExchangeAuthorizationCode(ctx context.Context, record exchangeRecord) error {
	return s.store.WithTx(ctx, func(tx db.Tx) error {
		var clientID, redirectURI, challenge, tenantID, ownerID, ownerName string
		var expiresAt int64
		var consumedAt sql.NullInt64
		err := tx.QueryRowContext(ctx, `
SELECT client_id, redirect_uri, code_challenge, tenant_id, owner_id, owner_display_name, expires_at, consumed_at
FROM device_oauth_codes WHERE code_hash = ?`, record.CodeHash).Scan(&clientID, &redirectURI, &challenge, &tenantID, &ownerID, &ownerName, &expiresAt, &consumedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUnauthorized
		}
		if err != nil {
			return fmt.Errorf("load device authorization code: %w", err)
		}
		if consumedAt.Valid || record.Now.UnixNano() >= expiresAt || clientID != record.ClientID || redirectURI != record.RedirectURI || !constantTimeStringEqual(challenge, record.PresentedChallenge) {
			return ErrUnauthorized
		}
		result, err := tx.ExecContext(ctx, `UPDATE device_oauth_codes SET consumed_at = ? WHERE code_hash = ? AND consumed_at IS NULL`, record.Now.UnixNano(), record.CodeHash)
		if err != nil {
			return fmt.Errorf("consume device authorization code: %w", err)
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			return ErrUnauthorized
		}
		pairing := record.Pairing
		pairing.TenantID, pairing.OwnerID = tenantID, ownerID
		_, err = tx.ExecContext(ctx, `
INSERT INTO device_pairings (
 pairing_id, tenant_id, owner_id, refresh_hash, refresh_revision, device_id, created_at, updated_at, revoked_at
) VALUES (?, ?, ?, ?, ?, NULL, ?, ?, NULL)`, pairing.PairingID, pairing.TenantID, pairing.OwnerID, pairing.RefreshHash,
			pairing.RefreshRevision, pairing.CreatedAt.UnixNano(), record.Now.UnixNano())
		if err != nil {
			return fmt.Errorf("create device pairing: %w", err)
		}
		access := record.Access
		access.TenantID, access.OwnerID = tenantID, ownerID
		_, err = tx.ExecContext(ctx, `
INSERT INTO device_access_tokens (
 token_hash, pairing_id, tenant_id, owner_id, device_id, credential_revision, expires_at, created_at, revoked_at
) VALUES (?, ?, ?, ?, NULL, 0, ?, ?, NULL)`, access.TokenHash, access.PairingID, access.TenantID, access.OwnerID,
			access.ExpiresAt.UnixNano(), record.Now.UnixNano())
		if err != nil {
			return fmt.Errorf("create device access token: %w", err)
		}
		return nil
	})
}

func (s *SQLStore) RegisterDevice(ctx context.Context, tokenHash string, device Device, now time.Time) (Device, error) {
	var stored Device
	err := s.store.WithTx(ctx, func(tx db.Tx) error {
		var pairingID, tenantID, ownerID string
		var tokenDeviceID, pairingDeviceID sql.NullString
		var tokenExpiresAt int64
		var tokenRevokedAt, pairingRevokedAt sql.NullInt64
		err := tx.QueryRowContext(ctx, `
SELECT t.pairing_id, t.tenant_id, t.owner_id, t.device_id, t.expires_at, t.revoked_at, p.device_id, p.revoked_at
FROM device_access_tokens t
JOIN device_pairings p ON p.pairing_id = t.pairing_id
WHERE t.token_hash = ?`, tokenHash).Scan(&pairingID, &tenantID, &ownerID, &tokenDeviceID, &tokenExpiresAt, &tokenRevokedAt, &pairingDeviceID, &pairingRevokedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUnauthorized
		}
		if err != nil {
			return fmt.Errorf("load pending device pairing: %w", err)
		}
		if tokenRevokedAt.Valid || pairingRevokedAt.Valid || now.UnixNano() >= tokenExpiresAt {
			return ErrUnauthorized
		}
		if tokenDeviceID.Valid || pairingDeviceID.Valid {
			return ErrConflict
		}
		device.PairingID, device.TenantID, device.OwnerID = pairingID, tenantID, ownerID
		publicKey, err := json.Marshal(device.PublicKey)
		if err != nil {
			return ErrInvalidRequest
		}
		capabilities, err := json.Marshal(device.AllowedCapabilities)
		if err != nil {
			return ErrInvalidRequest
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO devices (
 device_id, pairing_id, tenant_id, owner_id, device_name, platform, architecture, client_version,
 protocol_min, protocol_max, public_key_json, credential_revision, allowed_capabilities_json, created_at, updated_at, revoked_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`, device.DeviceID, device.PairingID, device.TenantID,
			device.OwnerID, device.DeviceName, device.Platform, device.Architecture, device.ClientVersion, device.ProtocolMin, device.ProtocolMax,
			string(publicKey), device.CredentialRevision, string(capabilities), device.CreatedAt.UnixNano(), now.UnixNano())
		if err != nil {
			if isUniqueConstraint(err) {
				return ErrConflict
			}
			return fmt.Errorf("register device: %w", err)
		}
		result, err := tx.ExecContext(ctx, `UPDATE device_pairings SET device_id = ?, updated_at = ? WHERE pairing_id = ? AND device_id IS NULL AND revoked_at IS NULL`, device.DeviceID, now.UnixNano(), pairingID)
		if err != nil {
			return fmt.Errorf("bind device pairing: %w", err)
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			return ErrConflict
		}
		result, err = tx.ExecContext(ctx, `UPDATE device_access_tokens SET device_id = ?, credential_revision = ? WHERE token_hash = ? AND device_id IS NULL AND revoked_at IS NULL`, device.DeviceID, device.CredentialRevision, tokenHash)
		if err != nil {
			return fmt.Errorf("bind device access token: %w", err)
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			return ErrConflict
		}
		stored = device
		return nil
	})
	return stored, err
}

func (s *SQLStore) RotateRefreshCredential(ctx context.Context, deviceID, currentHash, replacementHash, accessHash string, accessExpiresAt, now time.Time) (AccessGrant, error) {
	var grant AccessGrant
	err := s.store.WithTx(ctx, func(tx db.Tx) error {
		device, pairingID, err := loadDeviceByRefreshTx(ctx, tx, deviceID, currentHash)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
UPDATE device_pairings SET refresh_hash = ?, refresh_revision = refresh_revision + 1, updated_at = ?
WHERE pairing_id = ? AND device_id = ? AND refresh_hash = ? AND revoked_at IS NULL`, replacementHash, now.UnixNano(), pairingID, deviceID, currentHash)
		if err != nil {
			return fmt.Errorf("rotate device refresh credential: %w", err)
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			return ErrUnauthorized
		}
		if _, err := tx.ExecContext(ctx, `UPDATE device_access_tokens SET revoked_at = ? WHERE pairing_id = ? AND revoked_at IS NULL`, now.UnixNano(), pairingID); err != nil {
			return fmt.Errorf("revoke superseded device access tokens: %w", err)
		}
		grant = AccessGrant{TokenHash: accessHash, PairingID: pairingID, DeviceID: device.DeviceID, TenantID: device.TenantID, OwnerID: device.OwnerID, CredentialRevision: device.CredentialRevision, ExpiresAt: accessExpiresAt}
		_, err = tx.ExecContext(ctx, `
INSERT INTO device_access_tokens (
 token_hash, pairing_id, tenant_id, owner_id, device_id, credential_revision, expires_at, created_at, revoked_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL)`, grant.TokenHash, grant.PairingID, grant.TenantID, grant.OwnerID, grant.DeviceID,
			grant.CredentialRevision, grant.ExpiresAt.UnixNano(), now.UnixNano())
		if err != nil {
			return fmt.Errorf("create rotated device access token: %w", err)
		}
		return nil
	})
	return grant, err
}

func (s *SQLStore) RevokeDevice(ctx context.Context, deviceID, refreshHash string, now time.Time) error {
	return s.store.WithTx(ctx, func(tx db.Tx) error {
		device, pairingID, err := loadDeviceByRefreshTx(ctx, tx, deviceID, refreshHash)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE device_pairings SET revoked_at = ?, updated_at = ? WHERE pairing_id = ? AND refresh_hash = ? AND revoked_at IS NULL`, now.UnixNano(), now.UnixNano(), pairingID, refreshHash)
		if err != nil {
			return fmt.Errorf("revoke device pairing: %w", err)
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			return ErrUnauthorized
		}
		if _, err := tx.ExecContext(ctx, `UPDATE devices SET credential_revision = credential_revision + 1, revoked_at = ?, updated_at = ? WHERE device_id = ? AND revoked_at IS NULL`, now.UnixNano(), now.UnixNano(), device.DeviceID); err != nil {
			return fmt.Errorf("revoke device: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE device_access_tokens SET revoked_at = ? WHERE pairing_id = ? AND revoked_at IS NULL`, now.UnixNano(), pairingID); err != nil {
			return fmt.Errorf("revoke device access tokens: %w", err)
		}
		return nil
	})
}

func (s *SQLStore) AuthenticateAccessToken(ctx context.Context, tokenHash string, now time.Time) (Device, error) {
	return scanActiveDevice(s.store.QueryRow(ctx, `
SELECT d.device_id, d.pairing_id, d.tenant_id, d.owner_id, d.device_name, d.platform, d.architecture,
 d.client_version, d.protocol_min, d.protocol_max, d.public_key_json, d.credential_revision,
 d.allowed_capabilities_json, d.created_at, d.revoked_at,
 t.expires_at, t.revoked_at, p.revoked_at
FROM device_access_tokens t
JOIN device_pairings p ON p.pairing_id = t.pairing_id
JOIN devices d ON d.device_id = t.device_id AND d.pairing_id = t.pairing_id
WHERE t.token_hash = ?`, tokenHash), now)
}

func (s *SQLStore) IssueChallenge(ctx context.Context, tokenHash, deviceID string, credentialRevision int64, challenge challengeRecord, now time.Time) (Device, error) {
	var device Device
	err := s.store.WithTx(ctx, func(tx db.Tx) error {
		var err error
		device, err = loadActiveDeviceTx(ctx, tx, tokenHash, deviceID, credentialRevision, now)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO device_connection_challenges (
 nonce_hash, token_hash, device_id, credential_revision, expires_at, created_at, consumed_at
) VALUES (?, ?, ?, ?, ?, ?, NULL)`, challenge.NonceHash, tokenHash, deviceID, credentialRevision, challenge.ExpiresAt.UnixNano(), challenge.CreatedAt.UnixNano())
		if err != nil {
			return fmt.Errorf("create device connection challenge: %w", err)
		}
		return nil
	})
	return device, err
}

func (s *SQLStore) ConnectionCandidate(ctx context.Context, tokenHash, deviceID, nonceHash string, credentialRevision int64, now time.Time) (Device, error) {
	return loadConnectionCandidate(ctx, storeQueryRower{s.store}, tokenHash, deviceID, nonceHash, credentialRevision, now)
}

func (s *SQLStore) ConsumeChallenge(ctx context.Context, tokenHash, deviceID, nonceHash string, credentialRevision int64, now time.Time) (Device, error) {
	var device Device
	err := s.store.WithTx(ctx, func(tx db.Tx) error {
		var err error
		device, err = loadConnectionCandidate(ctx, tx, tokenHash, deviceID, nonceHash, credentialRevision, now)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
UPDATE device_connection_challenges SET consumed_at = ?
WHERE nonce_hash = ? AND token_hash = ? AND device_id = ? AND credential_revision = ? AND consumed_at IS NULL AND expires_at > ?`,
			now.UnixNano(), nonceHash, tokenHash, deviceID, credentialRevision, now.UnixNano())
		if err != nil {
			return fmt.Errorf("consume device connection challenge: %w", err)
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			return ErrUnauthorized
		}
		return nil
	})
	return device, err
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type storeQueryRower struct {
	store *db.Store
}

func (query storeQueryRower) QueryRowContext(ctx context.Context, statement string, args ...any) *sql.Row {
	return query.store.QueryRow(ctx, statement, args...)
}

func loadActiveDeviceTx(ctx context.Context, query queryRower, tokenHash, deviceID string, credentialRevision int64, now time.Time) (Device, error) {
	return scanActiveDevice(query.QueryRowContext(ctx, `
SELECT d.device_id, d.pairing_id, d.tenant_id, d.owner_id, d.device_name, d.platform, d.architecture,
 d.client_version, d.protocol_min, d.protocol_max, d.public_key_json, d.credential_revision,
 d.allowed_capabilities_json, d.created_at, d.revoked_at,
 t.expires_at, t.revoked_at, p.revoked_at
FROM device_access_tokens t
JOIN device_pairings p ON p.pairing_id = t.pairing_id
JOIN devices d ON d.device_id = t.device_id AND d.pairing_id = t.pairing_id
WHERE t.token_hash = ? AND t.device_id = ? AND t.credential_revision = ?`, tokenHash, deviceID, credentialRevision), now)
}

func loadConnectionCandidate(ctx context.Context, query queryRower, tokenHash, deviceID, nonceHash string, credentialRevision int64, now time.Time) (Device, error) {
	return scanActiveDevice(query.QueryRowContext(ctx, `
SELECT d.device_id, d.pairing_id, d.tenant_id, d.owner_id, d.device_name, d.platform, d.architecture,
 d.client_version, d.protocol_min, d.protocol_max, d.public_key_json, d.credential_revision,
 d.allowed_capabilities_json, d.created_at, d.revoked_at,
 t.expires_at, t.revoked_at, p.revoked_at
FROM device_connection_challenges c
JOIN device_access_tokens t ON t.token_hash = c.token_hash
JOIN device_pairings p ON p.pairing_id = t.pairing_id
JOIN devices d ON d.device_id = c.device_id AND d.device_id = t.device_id AND d.pairing_id = t.pairing_id
WHERE c.nonce_hash = ? AND c.token_hash = ? AND c.device_id = ? AND c.credential_revision = ?
 AND c.consumed_at IS NULL AND c.expires_at > ?`, nonceHash, tokenHash, deviceID, credentialRevision, now.UnixNano()), now)
}

func scanActiveDevice(row *sql.Row, now time.Time) (Device, error) {
	var device Device
	var publicKeyJSON, capabilitiesJSON string
	var createdAt, tokenExpiresAt int64
	var deviceRevokedAt, tokenRevokedAt, pairingRevokedAt sql.NullInt64
	err := row.Scan(&device.DeviceID, &device.PairingID, &device.TenantID, &device.OwnerID, &device.DeviceName, &device.Platform,
		&device.Architecture, &device.ClientVersion, &device.ProtocolMin, &device.ProtocolMax, &publicKeyJSON, &device.CredentialRevision,
		&capabilitiesJSON, &createdAt, &deviceRevokedAt, &tokenExpiresAt, &tokenRevokedAt, &pairingRevokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, ErrUnauthorized
	}
	if err != nil {
		return Device{}, fmt.Errorf("load active device credential: %w", err)
	}
	if deviceRevokedAt.Valid || tokenRevokedAt.Valid || pairingRevokedAt.Valid || now.UnixNano() >= tokenExpiresAt {
		return Device{}, ErrUnauthorized
	}
	if err := json.Unmarshal([]byte(publicKeyJSON), &device.PublicKey); err != nil {
		return Device{}, fmt.Errorf("decode device public key: %w", err)
	}
	if err := json.Unmarshal([]byte(capabilitiesJSON), &device.AllowedCapabilities); err != nil {
		return Device{}, fmt.Errorf("decode device capabilities: %w", err)
	}
	device.CreatedAt = time.Unix(0, createdAt).UTC()
	return device, nil
}

func loadDeviceByRefreshTx(ctx context.Context, tx db.Tx, deviceID, refreshHash string) (Device, string, error) {
	var device Device
	var pairingID, publicKeyJSON, capabilitiesJSON string
	var createdAt int64
	var deviceRevokedAt, pairingRevokedAt sql.NullInt64
	err := tx.QueryRowContext(ctx, `
SELECT d.device_id, d.pairing_id, d.tenant_id, d.owner_id, d.device_name, d.platform, d.architecture,
 d.client_version, d.protocol_min, d.protocol_max, d.public_key_json, d.credential_revision,
 d.allowed_capabilities_json, d.created_at, d.revoked_at, p.pairing_id, p.revoked_at
FROM device_pairings p JOIN devices d ON d.device_id = p.device_id AND d.pairing_id = p.pairing_id
WHERE p.device_id = ? AND p.refresh_hash = ?`, deviceID, refreshHash).Scan(&device.DeviceID, &device.PairingID, &device.TenantID,
		&device.OwnerID, &device.DeviceName, &device.Platform, &device.Architecture, &device.ClientVersion, &device.ProtocolMin,
		&device.ProtocolMax, &publicKeyJSON, &device.CredentialRevision, &capabilitiesJSON, &createdAt, &deviceRevokedAt, &pairingID, &pairingRevokedAt)
	if errors.Is(err, sql.ErrNoRows) || deviceRevokedAt.Valid || pairingRevokedAt.Valid {
		return Device{}, "", ErrUnauthorized
	}
	if err != nil {
		return Device{}, "", fmt.Errorf("load device refresh credential: %w", err)
	}
	if json.Unmarshal([]byte(publicKeyJSON), &device.PublicKey) != nil || json.Unmarshal([]byte(capabilitiesJSON), &device.AllowedCapabilities) != nil {
		return Device{}, "", fmt.Errorf("decode registered device")
	}
	device.CreatedAt = time.Unix(0, createdAt).UTC()
	return device, pairingID, nil
}

func schemaStatements(dialect db.Dialect) []string {
	timeType := "INTEGER"
	if dialect == db.DialectPostgres {
		timeType = "BIGINT"
	}
	return []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS device_oauth_codes (
 code_hash TEXT PRIMARY KEY, client_id TEXT NOT NULL, redirect_uri TEXT NOT NULL, code_challenge TEXT NOT NULL,
 tenant_id TEXT NOT NULL, owner_id TEXT NOT NULL, owner_display_name TEXT NOT NULL,
 expires_at %s NOT NULL, created_at %[1]s NOT NULL, consumed_at %[1]s
)`, timeType),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS device_pairings (
 pairing_id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, owner_id TEXT NOT NULL, refresh_hash TEXT NOT NULL UNIQUE,
 refresh_revision %s NOT NULL, device_id TEXT UNIQUE, created_at %[1]s NOT NULL, updated_at %[1]s NOT NULL, revoked_at %[1]s
)`, timeType),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS devices (
 device_id TEXT PRIMARY KEY, pairing_id TEXT NOT NULL UNIQUE, tenant_id TEXT NOT NULL, owner_id TEXT NOT NULL,
 device_name TEXT NOT NULL, platform TEXT NOT NULL, architecture TEXT NOT NULL, client_version TEXT NOT NULL,
 protocol_min TEXT NOT NULL, protocol_max TEXT NOT NULL, public_key_json TEXT NOT NULL,
 credential_revision %s NOT NULL, allowed_capabilities_json TEXT NOT NULL,
 created_at %[1]s NOT NULL, updated_at %[1]s NOT NULL, revoked_at %[1]s
)`, timeType),
		`CREATE INDEX IF NOT EXISTS idx_devices_scope ON devices (tenant_id, owner_id, revoked_at)`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS device_access_tokens (
 token_hash TEXT PRIMARY KEY, pairing_id TEXT NOT NULL, tenant_id TEXT NOT NULL, owner_id TEXT NOT NULL,
 device_id TEXT, credential_revision %s NOT NULL, expires_at %[1]s NOT NULL, created_at %[1]s NOT NULL, revoked_at %[1]s
)`, timeType),
		`CREATE INDEX IF NOT EXISTS idx_device_access_pairing ON device_access_tokens (pairing_id, expires_at)`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS device_connection_challenges (
 nonce_hash TEXT PRIMARY KEY, token_hash TEXT NOT NULL, device_id TEXT NOT NULL, credential_revision %s NOT NULL,
 expires_at %[1]s NOT NULL, created_at %[1]s NOT NULL, consumed_at %[1]s
)`, timeType),
		`CREATE INDEX IF NOT EXISTS idx_device_challenges_scope ON device_connection_challenges (device_id, expires_at)`,
	}
}

func isUniqueConstraint(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
}

var _ Store = (*SQLStore)(nil)
