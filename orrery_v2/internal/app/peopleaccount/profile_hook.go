package peopleaccount

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/userauth"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/uow"
)

type ProfileProjectionHook struct {
	now func() time.Time
}

func NewProfileProjectionHook() *ProfileProjectionHook {
	return &ProfileProjectionHook{now: func() time.Time { return time.Now().UTC() }}
}

func (h *ProfileProjectionHook) BeforeProfileSave(ctx context.Context, tx uow.Tx, previous *agentcore.AgentProfile, current agentcore.AgentProfile) error {
	if !projectsPeopleAccount(current) {
		return nil
	}
	if previous != nil && previous.AgentKind == agentcore.AgentKindHuman && sameAccountFacts(*previous, current) {
		return nil
	}
	_, err := saveProfileProjection(ctx, tx, current, false, h.now())
	return err
}

func (s *Store) EnsureProfileProjection(ctx context.Context, profile agentcore.AgentProfile) (bool, error) {
	if s == nil || s.database == nil {
		return false, fmt.Errorf("people account projection store is not configured")
	}
	if !projectsPeopleAccount(profile) {
		return false, nil
	}
	created := false
	err := s.database.WithTx(ctx, func(tx db.Tx) error {
		var err error
		created, err = saveProfileProjection(ctx, tx, profile, true, time.Now().UTC())
		return err
	})
	return created, err
}

func saveProfileProjection(ctx context.Context, tx db.Tx, current agentcore.AgentProfile, onlyIfMissing bool, now time.Time) (bool, error) {
	personID := strings.TrimSpace(current.ID.String())
	if personID == "" {
		return false, fmt.Errorf("People projection person_id is required")
	}
	if err := db.LockRecordKeyTx(ctx, tx, projectionStateTable, personID); err != nil {
		return false, err
	}
	var revision int64
	err := tx.QueryRowContext(ctx, `SELECT revision FROM people_account_projection_state WHERE person_id = ?`, personID).Scan(&revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("load People projection revision: %w", err)
	}
	if onlyIfMissing && err == nil {
		return false, nil
	}
	projection := projectionFromProfile(current)
	projection.Revision = revision + 1
	payload, err := json.Marshal(projection)
	if err != nil {
		return false, fmt.Errorf("encode People account projection: %w", err)
	}
	nowMillis := now.UTC().UnixMilli()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO people_account_projection_state (
	person_id, revision, payload_json, status, applied_revision, kratos_identity_id, last_error, updated_at
) VALUES (?, ?, ?, ?, 0, '', '', ?)
ON CONFLICT(person_id) DO UPDATE SET
	revision = excluded.revision,
	payload_json = excluded.payload_json,
	status = excluded.status,
	last_error = '',
	updated_at = excluded.updated_at`, personID, projection.Revision, string(payload), ProjectionStatusPending, nowMillis); err != nil {
		return false, fmt.Errorf("save People account projection state: %w", err)
	}
	id := fmt.Sprintf("%s:%d", personID, projection.Revision)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO people_account_projection_outbox (
	id, person_id, revision, payload_json, status, attempts, last_error, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, 0, '', ?, ?)
ON CONFLICT(person_id, revision) DO NOTHING`, id, personID, projection.Revision, string(payload), ProjectionStatusPending, nowMillis, nowMillis); err != nil {
		return false, fmt.Errorf("enqueue People account projection: %w", err)
	}
	return true, nil
}

func projectsPeopleAccount(profile agentcore.AgentProfile) bool {
	return profile.AgentKind == agentcore.AgentKindHuman && !strings.EqualFold(strings.TrimSpace(profile.ID.String()), userauth.SuperadminAgentProfileID)
}

func projectionFromProfile(profile agentcore.AgentProfile) Projection {
	email := strings.TrimSpace(profile.UserAccount.Email)
	if email == "" {
		email = strings.TrimSpace(profile.Email)
	}
	displayName := strings.TrimSpace(profile.DisplayName)
	if displayName == "" {
		displayName = strings.TrimSpace(profile.RealName)
	}
	projection := Projection{
		PersonID:        strings.TrimSpace(profile.ID.String()),
		Username:        strings.TrimSpace(profile.UserAccount.Username),
		DisplayName:     displayName,
		EnterpriseEmail: email,
		Enabled:         profile.Enabled && profile.UserAccount.AccountEnabled,
		LegacyPassword:  profile.UserAccount.PasswordVerifier,
	}
	if strings.EqualFold(strings.TrimSpace(profile.PersonSource.Source), "people.feishu") {
		projection.FeishuSubject = strings.TrimSpace(profile.PersonSource.SourceRecordID)
	}
	return projection
}

func sameAccountFacts(previous, current agentcore.AgentProfile) bool {
	return projectionFromProfile(previous) == projectionFromProfile(current) &&
		previous.UserAccount.PasswordSalt == current.UserAccount.PasswordSalt
}

var _ agentcore.ProfileSaveHook = (*ProfileProjectionHook)(nil)
