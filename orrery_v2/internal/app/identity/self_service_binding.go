package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type SelfServiceBindingDTO struct {
	Provider    string    `json:"provider"`
	Configured  bool      `json:"configured"`
	Bound       bool      `json:"bound"`
	DisplayName string    `json:"display_name,omitempty"`
	Email       string    `json:"email,omitempty"`
	BoundAt     time.Time `json:"bound_at,omitempty"`
}

type SelfServiceBindingService struct {
	provider BrowserIdentityBindingProvider
	store    SubjectAdminStore
	admin    *SubjectAdminService
}

func NewSelfServiceBindingService(provider BrowserIdentityBindingProvider, store SubjectAdminStore, revoker SubjectSessionRevoker) *SelfServiceBindingService {
	return &SelfServiceBindingService{
		provider: provider,
		store:    store,
		admin:    NewSubjectAdminService(store).WithSessionRevoker(revoker),
	}
}

func (s *SelfServiceBindingService) GetBinding(ctx context.Context, agentProfileID string) (SelfServiceBindingDTO, error) {
	dto := SelfServiceBindingDTO{Provider: ProviderFeishu, Configured: s != nil && s.provider != nil}
	if !dto.Configured {
		return dto, nil
	}
	if s.store == nil {
		return SelfServiceBindingDTO{}, ErrServiceDependency
	}
	profileID := strings.TrimSpace(agentProfileID)
	if profileID == "" {
		return SelfServiceBindingDTO{}, fmt.Errorf("%w: agent profile is required", ErrInvalidRequest)
	}
	subject, found, err := s.activeBinding(ctx, profileID)
	if err != nil {
		return SelfServiceBindingDTO{}, err
	}
	if !found {
		return dto, nil
	}
	dto.Bound = true
	dto.DisplayName = subject.DisplayName
	dto.Email = subject.Email
	dto.BoundAt = subject.UpdatedAt
	return dto, nil
}

func (s *SelfServiceBindingService) BeginBinding(ctx context.Context, agentProfileID, returnTo string) (LoginRedirect, error) {
	if s == nil || s.provider == nil {
		return LoginRedirect{}, ErrServiceDependency
	}
	if strings.TrimSpace(agentProfileID) == "" {
		return LoginRedirect{}, fmt.Errorf("%w: agent profile is required", ErrInvalidRequest)
	}
	return s.provider.BeginBrowserBinding(ctx, BeginBindingRequest{AgentProfileID: agentProfileID, ReturnTo: returnTo})
}

func (s *SelfServiceBindingService) CompleteBinding(ctx context.Context, agentProfileID string, req CompleteLoginRequest) (SelfServiceBindingDTO, error) {
	if s == nil || s.provider == nil || s.store == nil || s.admin == nil {
		return SelfServiceBindingDTO{}, ErrServiceDependency
	}
	profileID := strings.TrimSpace(agentProfileID)
	if profileID == "" {
		return SelfServiceBindingDTO{}, fmt.Errorf("%w: agent profile is required", ErrInvalidRequest)
	}
	external, err := s.provider.CompleteBrowserBinding(ctx, req)
	if err != nil {
		return SelfServiceBindingDTO{}, err
	}
	external = NormalizeExternalIdentity(external)
	if external.Provider != s.provider.Name() || external.Subject == "" || external.Traits["binding_agent_profile_id"] != profileID {
		return SelfServiceBindingDTO{}, ErrUnauthorized
	}
	if existing, getErr := s.store.GetSubject(ctx, external.Provider, external.Subject); getErr == nil {
		if existing.Status == SubjectStatusActive && existing.AgentProfileID != profileID {
			return SelfServiceBindingDTO{}, ErrConflict
		}
	} else if !errors.Is(getErr, ErrSubjectNotFound) {
		return SelfServiceBindingDTO{}, getErr
	}
	if current, found, findErr := s.activeBinding(ctx, profileID); findErr != nil {
		return SelfServiceBindingDTO{}, findErr
	} else if found && current.Subject != external.Subject {
		return SelfServiceBindingDTO{}, ErrConflict
	}
	if _, err := s.admin.LinkSubject(ctx, LinkSubjectRequest{
		Provider:       external.Provider,
		Subject:        external.Subject,
		AgentProfileID: profileID,
		Username:       external.Username,
		Email:          external.Email,
		DisplayName:    external.DisplayName,
		Status:         SubjectStatusActive,
	}); err != nil {
		return SelfServiceBindingDTO{}, err
	}
	return s.GetBinding(ctx, profileID)
}

func (s *SelfServiceBindingService) Unbind(ctx context.Context, agentProfileID string) (SelfServiceBindingDTO, error) {
	if s == nil || s.provider == nil || s.store == nil || s.admin == nil {
		return SelfServiceBindingDTO{}, ErrServiceDependency
	}
	profileID := strings.TrimSpace(agentProfileID)
	if profileID == "" {
		return SelfServiceBindingDTO{}, fmt.Errorf("%w: agent profile is required", ErrInvalidRequest)
	}
	subject, found, err := s.activeBinding(ctx, profileID)
	if err != nil {
		return SelfServiceBindingDTO{}, err
	}
	if found {
		if _, err := s.admin.DisableSubject(ctx, subject.ID); err != nil {
			return SelfServiceBindingDTO{}, err
		}
	}
	return s.GetBinding(ctx, profileID)
}

func (s *SelfServiceBindingService) BindingReturnTo(state string) string {
	if s == nil || s.provider == nil {
		return "/profile"
	}
	if provider, ok := s.provider.(interface{ BindingReturnTo(string) string }); ok {
		return provider.BindingReturnTo(state)
	}
	return "/profile"
}

func (s *SelfServiceBindingService) activeBinding(ctx context.Context, agentProfileID string) (IdentitySubject, bool, error) {
	if s == nil || s.provider == nil || s.store == nil {
		return IdentitySubject{}, false, ErrServiceDependency
	}
	page, err := s.store.ListSubjects(ctx, SubjectQuery{
		Provider:       s.provider.Name(),
		AgentProfileID: strings.TrimSpace(agentProfileID),
		Status:         SubjectStatusActive,
	})
	if err != nil {
		return IdentitySubject{}, false, err
	}
	if len(page.Subjects) == 0 {
		return IdentitySubject{}, false, nil
	}
	return page.Subjects[0], true, nil
}
