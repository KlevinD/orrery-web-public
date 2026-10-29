package identity

import (
	"context"
	"errors"
	"testing"
)

func TestSubjectAdminServiceLinksListsAndDisablesIdentitySubject(t *testing.T) {
	ctx := context.Background()
	store := NewMemorySubjectStore()
	service := NewSubjectAdminService(store).WithSessionRevoker(&recordingSubjectSessionRevoker{})

	subject, err := service.LinkSubject(ctx, LinkSubjectRequest{
		Provider:       ProviderKratos,
		Subject:        "kratos-id-1",
		AgentProfileID: "human-operator",
		Username:       "Operator",
		Email:          "operator@example.com",
		DisplayName:    "Operator One",
	})
	if err != nil {
		t.Fatalf("LinkSubject: %v", err)
	}
	if subject.ID != "kratos:kratos-id-1" || subject.Username != "operator" || subject.Status != SubjectStatusActive {
		t.Fatalf("linked subject = %+v", subject)
	}

	page, err := service.ListSubjects(ctx, SubjectQuery{Provider: ProviderKratos, AgentProfileID: "human-operator"})
	if err != nil {
		t.Fatalf("ListSubjects: %v", err)
	}
	if page.Total != 1 || page.Subjects[0].Subject != "kratos-id-1" {
		t.Fatalf("page = %+v", page)
	}

	disabled, err := service.DisableSubject(ctx, subject.ID)
	if err != nil {
		t.Fatalf("DisableSubject: %v", err)
	}
	if disabled.Status != SubjectStatusDisabled {
		t.Fatalf("disabled subject = %+v", disabled)
	}

	enabled, err := service.EnableSubject(ctx, subject.ID)
	if err != nil {
		t.Fatalf("EnableSubject: %v", err)
	}
	if enabled.Status != SubjectStatusActive {
		t.Fatalf("enabled subject = %+v", enabled)
	}
}

func TestSubjectAdminServiceRejectsLocalProviderLink(t *testing.T) {
	service := NewSubjectAdminService(NewMemorySubjectStore())

	_, err := service.LinkSubject(context.Background(), LinkSubjectRequest{
		Provider:       ProviderLocal,
		Subject:        "superadmin",
		AgentProfileID: "human-superadmin",
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("LinkSubject local error = %v, want ErrInvalidRequest", err)
	}
}

func TestSubjectAdminServiceAllowsExplicitExternalSuperadminLink(t *testing.T) {
	service := NewSubjectAdminService(NewMemorySubjectStore())

	subject, err := service.LinkSubject(context.Background(), LinkSubjectRequest{
		Provider:       ProviderKratos,
		Subject:        "kratos-break-glass",
		AgentProfileID: "human-superadmin",
		Username:       "superadmin",
	})
	if err != nil {
		t.Fatalf("LinkSubject external superadmin: %v", err)
	}
	if subject.Provider != ProviderKratos || subject.Username != "superadmin" || subject.AgentProfileID != "human-superadmin" {
		t.Fatalf("external superadmin subject = %+v", subject)
	}
}

func TestSubjectAdminServiceRejectsOtherExternalIdentitiesTargetingSuperadminProfile(t *testing.T) {
	for _, req := range []LinkSubjectRequest{
		{
			Provider:       ProviderKratos,
			Subject:        "kratos-operator",
			AgentProfileID: "human-superadmin",
			Username:       "operator",
		},
		{
			Provider:       ProviderOIDC,
			Subject:        "oidc-superadmin",
			AgentProfileID: "human-superadmin",
			Username:       "superadmin",
		},
	} {
		service := NewSubjectAdminService(NewMemorySubjectStore())
		if _, err := service.LinkSubject(context.Background(), req); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("LinkSubject(%s/%s) error = %v, want ErrInvalidRequest", req.Provider, req.Username, err)
		}
	}
}

func TestSubjectAdminServiceRelinksExistingSubjectWithoutOverwritingUsernameWhenOmitted(t *testing.T) {
	ctx := context.Background()
	service := NewSubjectAdminService(NewMemorySubjectStore()).WithSessionRevoker(&recordingSubjectSessionRevoker{})
	subject, err := service.LinkSubject(ctx, LinkSubjectRequest{
		Provider:       ProviderKratos,
		Subject:        "kratos-id-1",
		AgentProfileID: "human-operator",
		Username:       "operator",
	})
	if err != nil {
		t.Fatalf("LinkSubject: %v", err)
	}

	updated, err := service.LinkSubject(ctx, LinkSubjectRequest{
		Provider:       ProviderKratos,
		Subject:        "kratos-id-1",
		AgentProfileID: "human-operator-2",
		DisplayName:    "Operator Two",
	})
	if err != nil {
		t.Fatalf("LinkSubject existing: %v", err)
	}
	if updated.ID != subject.ID || updated.Username != "operator" || updated.AgentProfileID != "human-operator-2" || updated.DisplayName != "Operator Two" {
		t.Fatalf("updated subject = %+v", updated)
	}
}

func TestSubjectAdminServiceRevokesSessionsWhenIdentityBindingChanges(t *testing.T) {
	ctx := context.Background()
	revoker := &recordingSubjectSessionRevoker{}
	service := NewSubjectAdminService(NewMemorySubjectStore()).WithSessionRevoker(revoker)
	if _, err := service.LinkSubject(ctx, LinkSubjectRequest{
		Provider:       ProviderKratos,
		Subject:        "kratos-id-1",
		AgentProfileID: "human-operator",
		Username:       "operator",
	}); err != nil {
		t.Fatalf("LinkSubject: %v", err)
	}
	if len(revoker.calls) != 0 {
		t.Fatalf("initial link should not revoke sessions: %+v", revoker.calls)
	}

	if _, err := service.LinkSubject(ctx, LinkSubjectRequest{
		Provider:       ProviderKratos,
		Subject:        "kratos-id-1",
		AgentProfileID: "human-reviewer",
		Username:       "reviewer",
	}); err != nil {
		t.Fatalf("relink subject: %v", err)
	}
	if len(revoker.calls) != 2 || revoker.calls[0] != "kratos/kratos-id-1" || revoker.calls[1] != "kratos/kratos-id-1" {
		t.Fatalf("session revocations = %+v", revoker.calls)
	}
}

func TestSubjectAdminServiceOmittedStatusDoesNotReactivateDisabledSubject(t *testing.T) {
	ctx := context.Background()
	revoker := &recordingSubjectSessionRevoker{}
	service := NewSubjectAdminService(NewMemorySubjectStore()).WithSessionRevoker(revoker)
	subject, err := service.LinkSubject(ctx, LinkSubjectRequest{
		Provider:       ProviderKratos,
		Subject:        "kratos-id-1",
		AgentProfileID: "human-operator",
		Username:       "operator",
	})
	if err != nil {
		t.Fatalf("LinkSubject: %v", err)
	}
	if _, err := service.DisableSubject(ctx, subject.ID); err != nil {
		t.Fatalf("DisableSubject: %v", err)
	}

	updated, err := service.LinkSubject(ctx, LinkSubjectRequest{
		Provider:       ProviderKratos,
		Subject:        "kratos-id-1",
		AgentProfileID: "human-operator",
		DisplayName:    "Disabled Operator",
	})
	if err != nil {
		t.Fatalf("LinkSubject disabled subject: %v", err)
	}
	if updated.Status != SubjectStatusDisabled {
		t.Fatalf("omitted status reactivated subject: %+v", updated)
	}
	if len(revoker.calls) != 2 || revoker.calls[0] != "kratos/kratos-id-1" || revoker.calls[1] != "kratos/kratos-id-1" {
		t.Fatalf("disable should revoke mapped sessions before and after the status change: %+v", revoker.calls)
	}
}

func TestSubjectAdminServiceRevokesStaleSessionsBeforeReenablingSubject(t *testing.T) {
	ctx := context.Background()
	revoker := &recordingSubjectSessionRevoker{}
	service := NewSubjectAdminService(NewMemorySubjectStore()).WithSessionRevoker(revoker)
	subject, err := service.LinkSubject(ctx, LinkSubjectRequest{
		Provider:       ProviderKratos,
		Subject:        "kratos-id-1",
		AgentProfileID: "human-operator",
		Username:       "operator",
	})
	if err != nil {
		t.Fatalf("LinkSubject: %v", err)
	}
	if _, err := service.DisableSubject(ctx, subject.ID); err != nil {
		t.Fatalf("DisableSubject: %v", err)
	}
	if _, err := service.EnableSubject(ctx, subject.ID); err != nil {
		t.Fatalf("EnableSubject: %v", err)
	}
	if len(revoker.calls) != 4 || revoker.calls[0] != "kratos/kratos-id-1" || revoker.calls[1] != "kratos/kratos-id-1" || revoker.calls[2] != "kratos/kratos-id-1" || revoker.calls[3] != "kratos/kratos-id-1" {
		t.Fatalf("disable/enable session revocations = %+v", revoker.calls)
	}
}

func TestSubjectAdminServiceFailsClosedWithoutSessionRevoker(t *testing.T) {
	ctx := context.Background()
	store := NewMemorySubjectStore()
	service := NewSubjectAdminService(store)
	subject, err := service.LinkSubject(ctx, LinkSubjectRequest{
		Provider:       ProviderKratos,
		Subject:        "kratos-id-1",
		AgentProfileID: "human-operator",
		Username:       "operator",
	})
	if err != nil {
		t.Fatalf("LinkSubject: %v", err)
	}

	if _, err := service.DisableSubject(ctx, subject.ID); !errors.Is(err, ErrServiceDependency) {
		t.Fatalf("DisableSubject error = %v, want ErrServiceDependency", err)
	}
	stored, err := store.GetSubject(ctx, ProviderKratos, "kratos-id-1")
	if err != nil {
		t.Fatalf("GetSubject: %v", err)
	}
	if stored.Status != SubjectStatusActive {
		t.Fatalf("subject status changed without session revoker: %+v", stored)
	}
}

func TestSubjectAdminServiceDisablesSubjectWhenPostChangeRevocationFails(t *testing.T) {
	ctx := context.Background()
	store := NewMemorySubjectStore()
	revoker := &recordingSubjectSessionRevoker{failAt: 2, err: errors.New("session store unavailable")}
	service := NewSubjectAdminService(store).WithSessionRevoker(revoker)
	subject, err := service.LinkSubject(ctx, LinkSubjectRequest{
		Provider:       ProviderKratos,
		Subject:        "kratos-id-1",
		AgentProfileID: "human-operator",
		Username:       "operator",
	})
	if err != nil {
		t.Fatalf("LinkSubject: %v", err)
	}

	_, err = service.LinkSubject(ctx, LinkSubjectRequest{
		Provider:       ProviderKratos,
		Subject:        "kratos-id-1",
		AgentProfileID: "human-reviewer",
		Username:       "reviewer",
	})
	if !errors.Is(err, revoker.err) {
		t.Fatalf("LinkSubject relink error = %v, want revocation error", err)
	}
	stored, getErr := store.GetSubject(ctx, ProviderKratos, "kratos-id-1")
	if getErr != nil {
		t.Fatalf("GetSubject: %v", getErr)
	}
	if stored.Status != SubjectStatusDisabled {
		t.Fatalf("subject remained active after post-change revocation failure: %+v", stored)
	}
	if stored.AgentProfileID != "human-reviewer" {
		t.Fatalf("failed relink binding = %+v", stored)
	}
	if subject.ID != stored.ID {
		t.Fatalf("subject identity changed: before=%q after=%q", subject.ID, stored.ID)
	}
}

func TestSubjectAdminServiceRejectsUnknownStatus(t *testing.T) {
	store := NewMemorySubjectStore()
	service := NewSubjectAdminService(store)
	_, err := service.LinkSubject(context.Background(), LinkSubjectRequest{
		Provider:       ProviderKratos,
		Subject:        "kratos-id-1",
		AgentProfileID: "human-operator",
		Username:       "operator",
		Status:         "actve",
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("LinkSubject error = %v, want ErrInvalidRequest", err)
	}
	if _, err := store.GetSubject(context.Background(), ProviderKratos, "kratos-id-1"); !errors.Is(err, ErrSubjectNotFound) {
		t.Fatalf("unknown status persisted subject: %v", err)
	}
	if got := NormalizeIdentitySubject(IdentitySubject{Status: "corrupt"}).Status; got != SubjectStatusPendingLink {
		t.Fatalf("corrupt stored status normalized to %q, want fail-closed pending_link", got)
	}
}

type recordingSubjectSessionRevoker struct {
	calls  []string
	failAt int
	err    error
}

func (r *recordingSubjectSessionRevoker) RevokeSessionsForIdentitySubject(_ context.Context, provider string, subject string) error {
	r.calls = append(r.calls, provider+"/"+subject)
	if r.failAt > 0 && len(r.calls) == r.failAt {
		return r.err
	}
	return nil
}
