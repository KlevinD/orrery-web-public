package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type SubjectAdminStore interface {
	SubjectStore
	GetSubjectByID(ctx context.Context, id string) (IdentitySubject, error)
	ListSubjects(ctx context.Context, query SubjectQuery) (SubjectPage, error)
}

type SubjectAdminService struct {
	store   SubjectAdminStore
	revoker SubjectSessionRevoker
}

type SubjectSessionRevoker interface {
	RevokeSessionsForIdentitySubject(ctx context.Context, provider string, subject string) error
}

func NewSubjectAdminService(store SubjectAdminStore) *SubjectAdminService {
	return &SubjectAdminService{store: store}
}

func (s *SubjectAdminService) WithSessionRevoker(revoker SubjectSessionRevoker) *SubjectAdminService {
	if s != nil {
		s.revoker = revoker
	}
	return s
}

func (s *SubjectAdminService) ListSubjects(ctx context.Context, query SubjectQuery) (SubjectPage, error) {
	if s == nil || s.store == nil {
		return SubjectPage{}, fmt.Errorf("identity subject admin store is required")
	}
	query.Provider = normalizeLower(query.Provider)
	query.Subject = strings.TrimSpace(query.Subject)
	query.AgentProfileID = strings.TrimSpace(query.AgentProfileID)
	if strings.TrimSpace(query.Status) != "" {
		status, err := requestedSubjectStatus(query.Status)
		if err != nil {
			return SubjectPage{}, err
		}
		query.Status = status
	}
	return s.store.ListSubjects(ctx, query)
}

func (s *SubjectAdminService) LinkSubject(ctx context.Context, req LinkSubjectRequest) (IdentitySubject, error) {
	if s == nil || s.store == nil {
		return IdentitySubject{}, fmt.Errorf("identity subject admin store is required")
	}
	external := NormalizeExternalIdentity(ExternalIdentity{
		Provider:    req.Provider,
		Subject:     req.Subject,
		Username:    req.Username,
		Email:       req.Email,
		DisplayName: req.DisplayName,
	})
	if external.Provider == "" || external.Subject == "" || strings.TrimSpace(req.AgentProfileID) == "" {
		return IdentitySubject{}, fmt.Errorf("%w: provider, subject and agent_profile_id are required", ErrInvalidRequest)
	}
	if external.Provider == ProviderLocal {
		return IdentitySubject{}, fmt.Errorf("%w: local subjects are managed by the local provider", ErrInvalidRequest)
	}
	status, err := requestedSubjectStatus(req.Status)
	if err != nil {
		return IdentitySubject{}, err
	}
	subject := IdentitySubject{
		Provider:       external.Provider,
		Subject:        external.Subject,
		UserID:         firstNonEmpty(external.Username, external.Subject),
		Username:       firstNonEmpty(external.Username, external.Subject),
		Email:          external.Email,
		DisplayName:    firstNonEmpty(external.DisplayName, external.Username, external.Subject),
		AgentProfileID: strings.TrimSpace(req.AgentProfileID),
		Status:         status,
	}
	if existing, err := s.store.GetSubject(ctx, subject.Provider, subject.Subject); err == nil {
		previous := existing
		if external.Username != "" {
			existing.UserID = subject.UserID
			existing.Username = subject.Username
		}
		if external.Email != "" {
			existing.Email = subject.Email
		}
		if external.DisplayName != "" {
			existing.DisplayName = subject.DisplayName
		}
		existing.AgentProfileID = subject.AgentProfileID
		if strings.TrimSpace(req.Status) != "" {
			existing.Status = status
		}
		subject = existing
		if !canTargetProtectedBreakGlassProfile(subject.Provider, subject.Username, subject.AgentProfileID) {
			return IdentitySubject{}, fmt.Errorf("%w: external identity cannot target the protected superadmin profile", ErrInvalidRequest)
		}
		securityBindingChanged := identitySubjectSecurityBindingChanged(previous, subject)
		if err := s.revokeSubjectSessions(ctx, previous, securityBindingChanged); err != nil {
			return IdentitySubject{}, err
		}
		if err := s.store.SaveSubject(ctx, subject); err != nil {
			return IdentitySubject{}, err
		}
		if err := s.revokeSubjectSessions(ctx, previous, securityBindingChanged); err != nil {
			return IdentitySubject{}, errors.Join(err, s.disableSubjectAfterRevocationFailure(ctx, subject))
		}
	} else if err != nil && !errors.Is(err, ErrSubjectNotFound) {
		return IdentitySubject{}, err
	} else {
		if !canTargetProtectedBreakGlassProfile(subject.Provider, subject.Username, subject.AgentProfileID) {
			return IdentitySubject{}, fmt.Errorf("%w: external identity cannot target the protected superadmin profile", ErrInvalidRequest)
		}
		if err := s.store.SaveSubject(ctx, subject); err != nil {
			return IdentitySubject{}, err
		}
	}
	return s.store.GetSubject(ctx, subject.Provider, subject.Subject)
}

func (s *SubjectAdminService) DisableSubject(ctx context.Context, id string) (IdentitySubject, error) {
	return s.setSubjectStatus(ctx, id, SubjectStatusDisabled)
}

func (s *SubjectAdminService) EnableSubject(ctx context.Context, id string) (IdentitySubject, error) {
	return s.setSubjectStatus(ctx, id, SubjectStatusActive)
}

func (s *SubjectAdminService) setSubjectStatus(ctx context.Context, id string, status string) (IdentitySubject, error) {
	if s == nil || s.store == nil {
		return IdentitySubject{}, fmt.Errorf("identity subject admin store is required")
	}
	subject, err := s.store.GetSubjectByID(ctx, strings.TrimSpace(id))
	if err != nil {
		return IdentitySubject{}, err
	}
	nextStatus := normalizeStatus(status)
	if nextStatus == SubjectStatusActive && !canTargetProtectedBreakGlassProfile(subject.Provider, subject.Username, subject.AgentProfileID) {
		return IdentitySubject{}, fmt.Errorf("%w: external identity cannot target the protected superadmin profile", ErrInvalidRequest)
	}
	statusChanged := subject.Status != nextStatus
	if err := s.revokeSubjectSessions(ctx, subject, statusChanged); err != nil {
		return IdentitySubject{}, err
	}
	subject.Status = nextStatus
	if err := s.store.SaveSubject(ctx, subject); err != nil {
		return IdentitySubject{}, err
	}
	if err := s.revokeSubjectSessions(ctx, subject, statusChanged); err != nil {
		return IdentitySubject{}, errors.Join(err, s.disableSubjectAfterRevocationFailure(ctx, subject))
	}
	return s.store.GetSubject(ctx, subject.Provider, subject.Subject)
}

func (s *SubjectAdminService) disableSubjectAfterRevocationFailure(ctx context.Context, subject IdentitySubject) error {
	if s == nil || s.store == nil {
		return ErrServiceDependency
	}
	subject = NormalizeIdentitySubject(subject)
	subject.Status = SubjectStatusDisabled
	return s.store.SaveSubject(ctx, subject)
}

func (s *SubjectAdminService) revokeSubjectSessions(ctx context.Context, subject IdentitySubject, required bool) error {
	if !required {
		return nil
	}
	if s == nil || s.revoker == nil {
		return ErrServiceDependency
	}
	subject = NormalizeIdentitySubject(subject)
	if subject.Provider == "" || subject.Subject == "" {
		return fmt.Errorf("%w: identity subject is incomplete", ErrInvalidRequest)
	}
	return s.revoker.RevokeSessionsForIdentitySubject(ctx, subject.Provider, subject.Subject)
}

func identitySubjectSecurityBindingChanged(previous IdentitySubject, next IdentitySubject) bool {
	previous = NormalizeIdentitySubject(previous)
	next = NormalizeIdentitySubject(next)
	return previous.AgentProfileID != next.AgentProfileID || previous.Username != next.Username || previous.Status != next.Status
}

func requestedSubjectStatus(value string) (string, error) {
	switch normalizeLower(value) {
	case "", SubjectStatusActive:
		return SubjectStatusActive, nil
	case SubjectStatusDisabled:
		return SubjectStatusDisabled, nil
	case SubjectStatusPendingLink:
		return SubjectStatusPendingLink, nil
	default:
		return "", fmt.Errorf("%w: unsupported identity subject status", ErrInvalidRequest)
	}
}
