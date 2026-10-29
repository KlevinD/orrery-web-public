package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

type PlatformSubjectStore struct {
	store *db.Store
	now   func() time.Time
}

func NewPlatformSubjectStore(store *db.Store) *PlatformSubjectStore {
	return &PlatformSubjectStore{
		store: store,
		now:   func() time.Time { return time.Now().UTC() },
	}
}

func (s *PlatformSubjectStore) SaveSubject(ctx context.Context, subject IdentitySubject) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.store == nil {
		return fmt.Errorf("identity subject platform store is required")
	}
	subject = NormalizeIdentitySubject(subject)
	if subject.Provider == "" || subject.Subject == "" {
		return fmt.Errorf("provider and subject are required")
	}
	now := s.now()
	if subject.ID == "" {
		subject.ID = subject.Provider + ":" + subject.Subject
	}
	if subject.CreatedAt.IsZero() {
		subject.CreatedAt = now
	}
	subject.UpdatedAt = now
	_, err := s.store.Exec(ctx, `
INSERT INTO identity_subjects (
	id, provider, subject, user_id, username, email, display_name,
	agent_profile_id, status, last_login_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(provider, subject) DO UPDATE SET
	id = excluded.id,
	user_id = excluded.user_id,
	username = excluded.username,
	email = excluded.email,
	display_name = excluded.display_name,
	agent_profile_id = excluded.agent_profile_id,
	status = excluded.status,
	last_login_at = excluded.last_login_at,
	updated_at = excluded.updated_at`,
		subject.ID,
		subject.Provider,
		subject.Subject,
		subject.UserID,
		subject.Username,
		subject.Email,
		subject.DisplayName,
		subject.AgentProfileID,
		subject.Status,
		timeToUnixNano(subject.LastLoginAt),
		timeToUnixNano(subject.CreatedAt),
		timeToUnixNano(subject.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("save identity subject: %w", err)
	}
	return nil
}

func (s *PlatformSubjectStore) GetSubject(ctx context.Context, provider string, subject string) (IdentitySubject, error) {
	if err := ctx.Err(); err != nil {
		return IdentitySubject{}, err
	}
	if s == nil || s.store == nil {
		return IdentitySubject{}, fmt.Errorf("identity subject platform store is required")
	}
	external := NormalizeExternalIdentity(ExternalIdentity{Provider: provider, Subject: subject})
	if external.Provider == "" || external.Subject == "" {
		return IdentitySubject{}, ErrSubjectNotFound
	}
	row := s.store.QueryRow(ctx, `SELECT `+identitySubjectColumns()+` FROM identity_subjects WHERE provider = ? AND subject = ?`, external.Provider, external.Subject)
	record, err := scanIdentitySubject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return IdentitySubject{}, ErrSubjectNotFound
	}
	if err != nil {
		return IdentitySubject{}, fmt.Errorf("get identity subject: %w", err)
	}
	return record, nil
}

func (s *PlatformSubjectStore) GetSubjectByID(ctx context.Context, id string) (IdentitySubject, error) {
	if err := ctx.Err(); err != nil {
		return IdentitySubject{}, err
	}
	if s == nil || s.store == nil {
		return IdentitySubject{}, fmt.Errorf("identity subject platform store is required")
	}
	id = firstNonEmpty(id)
	if id == "" {
		return IdentitySubject{}, ErrSubjectNotFound
	}
	row := s.store.QueryRow(ctx, `SELECT `+identitySubjectColumns()+` FROM identity_subjects WHERE id = ?`, id)
	record, err := scanIdentitySubject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return IdentitySubject{}, ErrSubjectNotFound
	}
	if err != nil {
		return IdentitySubject{}, fmt.Errorf("get identity subject by id: %w", err)
	}
	return record, nil
}

func (s *PlatformSubjectStore) GetSubjectByAgentProfileID(ctx context.Context, provider string, agentProfileID string) (IdentitySubject, error) {
	if err := ctx.Err(); err != nil {
		return IdentitySubject{}, err
	}
	if s == nil || s.store == nil {
		return IdentitySubject{}, fmt.Errorf("identity subject platform store is required")
	}
	provider = normalizeLower(provider)
	agentProfileID = firstNonEmpty(agentProfileID)
	if provider == "" || agentProfileID == "" {
		return IdentitySubject{}, ErrSubjectNotFound
	}
	row := s.store.QueryRow(ctx, `SELECT `+identitySubjectColumns()+` FROM identity_subjects WHERE provider = ? AND agent_profile_id = ? ORDER BY updated_at DESC, id ASC LIMIT 1`, provider, agentProfileID)
	record, err := scanIdentitySubject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return IdentitySubject{}, ErrSubjectNotFound
	}
	if err != nil {
		return IdentitySubject{}, fmt.Errorf("get identity subject by agent profile id: %w", err)
	}
	return record, nil
}

func (s *PlatformSubjectStore) ListSubjects(ctx context.Context, query SubjectQuery) (SubjectPage, error) {
	if err := ctx.Err(); err != nil {
		return SubjectPage{}, err
	}
	if s == nil || s.store == nil {
		return SubjectPage{}, fmt.Errorf("identity subject platform store is required")
	}
	where := []string{}
	args := []any{}
	if provider := normalizeLower(query.Provider); provider != "" {
		where = append(where, "provider = ?")
		args = append(args, provider)
	}
	if subject := strings.TrimSpace(query.Subject); subject != "" {
		where = append(where, "subject = ?")
		args = append(args, subject)
	}
	if agentProfileID := strings.TrimSpace(query.AgentProfileID); agentProfileID != "" {
		where = append(where, "agent_profile_id = ?")
		args = append(args, agentProfileID)
	}
	if status := normalizeLower(query.Status); status != "" {
		where = append(where, "status = ?")
		args = append(args, status)
	}
	statement := `SELECT ` + identitySubjectColumns() + ` FROM identity_subjects`
	if len(where) > 0 {
		statement += ` WHERE ` + strings.Join(where, ` AND `)
	}
	statement += ` ORDER BY updated_at DESC, id ASC`
	rows, err := s.store.Query(ctx, statement, args...)
	if err != nil {
		return SubjectPage{}, fmt.Errorf("list identity subjects: %w", err)
	}
	defer rows.Close()
	items := []IdentitySubject{}
	for rows.Next() {
		record, err := scanIdentitySubject(rows)
		if err != nil {
			return SubjectPage{}, fmt.Errorf("scan identity subject: %w", err)
		}
		items = append(items, record)
	}
	if err := rows.Err(); err != nil {
		return SubjectPage{}, fmt.Errorf("list identity subjects: %w", err)
	}
	return subjectPage(items, query.Limit, query.Offset), nil
}

type identitySubjectScanner interface {
	Scan(dest ...any) error
}

func identitySubjectColumns() string {
	return `id, provider, subject, user_id, username, email, display_name,
agent_profile_id, status, last_login_at, created_at, updated_at`
}

func scanIdentitySubject(scanner identitySubjectScanner) (IdentitySubject, error) {
	var subject IdentitySubject
	var lastLoginAt, createdAt, updatedAt int64
	if err := scanner.Scan(
		&subject.ID,
		&subject.Provider,
		&subject.Subject,
		&subject.UserID,
		&subject.Username,
		&subject.Email,
		&subject.DisplayName,
		&subject.AgentProfileID,
		&subject.Status,
		&lastLoginAt,
		&createdAt,
		&updatedAt,
	); err != nil {
		return IdentitySubject{}, err
	}
	subject.LastLoginAt = unixNanoToTime(lastLoginAt)
	subject.CreatedAt = unixNanoToTime(createdAt)
	subject.UpdatedAt = unixNanoToTime(updatedAt)
	return NormalizeIdentitySubject(subject), nil
}

func timeToUnixNano(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().UnixNano()
}

func unixNanoToTime(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(0, value).UTC()
}

var _ SubjectAdminStore = (*PlatformSubjectStore)(nil)
