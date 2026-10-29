package identity

import (
	"context"
	"testing"
)

func TestSelfServiceBindingServiceLinksAndRevokesCurrentUsersFeishuIdentity(t *testing.T) {
	ctx := context.Background()
	store := NewMemorySubjectStore()
	revoker := &recordingSubjectRevoker{}
	provider := &fakeBindingProvider{external: ExternalIdentity{
		Provider: ProviderFeishu, Subject: "tenant:on_1", Username: "alice@example.test", Email: "alice@example.test", DisplayName: "Alice",
		Traits: map[string]string{"binding_agent_profile_id": "human-alice"},
	}}
	service := NewSelfServiceBindingService(provider, store, revoker)

	binding, err := service.CompleteBinding(ctx, "human-alice", CompleteLoginRequest{Code: "code"})
	if err != nil {
		t.Fatalf("CompleteBinding: %v", err)
	}
	if !binding.Configured || !binding.Bound || binding.DisplayName != "Alice" || binding.Email != "alice@example.test" {
		t.Fatalf("binding = %+v", binding)
	}
	unbound, err := service.Unbind(ctx, "human-alice")
	if err != nil {
		t.Fatalf("Unbind: %v", err)
	}
	if unbound.Bound || len(revoker.subjects) != 2 {
		t.Fatalf("unbound = %+v revocations=%+v", unbound, revoker.subjects)
	}
	subject, err := store.GetSubject(ctx, ProviderFeishu, "tenant:on_1")
	if err != nil || subject.Status != SubjectStatusDisabled {
		t.Fatalf("stored subject = %+v err=%v", subject, err)
	}
}

func TestSelfServiceBindingServiceRejectsCrossAccountBinding(t *testing.T) {
	ctx := context.Background()
	store := NewMemorySubjectStore()
	if err := store.SaveSubject(ctx, IdentitySubject{
		Provider: ProviderFeishu, Subject: "tenant:on_1", AgentProfileID: "human-bob", Status: SubjectStatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	provider := &fakeBindingProvider{external: ExternalIdentity{
		Provider: ProviderFeishu, Subject: "tenant:on_1", Traits: map[string]string{"binding_agent_profile_id": "human-alice"},
	}}
	service := NewSelfServiceBindingService(provider, store, &recordingSubjectRevoker{})
	if _, err := service.CompleteBinding(ctx, "human-alice", CompleteLoginRequest{}); err != ErrConflict {
		t.Fatalf("error = %v, want conflict", err)
	}
}

type fakeBindingProvider struct {
	external ExternalIdentity
}

func (p *fakeBindingProvider) Name() string { return ProviderFeishu }

func (p *fakeBindingProvider) BeginBrowserLogin(context.Context, BeginLoginRequest) (LoginRedirect, error) {
	return LoginRedirect{}, nil
}

func (p *fakeBindingProvider) CompleteBrowserLogin(context.Context, CompleteLoginRequest) (ExternalIdentity, error) {
	return p.external, nil
}

func (p *fakeBindingProvider) BeginBrowserBinding(context.Context, BeginBindingRequest) (LoginRedirect, error) {
	return LoginRedirect{URL: "https://example.test"}, nil
}

func (p *fakeBindingProvider) CompleteBrowserBinding(context.Context, CompleteLoginRequest) (ExternalIdentity, error) {
	return p.external, nil
}

type recordingSubjectRevoker struct {
	subjects []string
}

func (r *recordingSubjectRevoker) RevokeSessionsForIdentitySubject(_ context.Context, provider, subject string) error {
	r.subjects = append(r.subjects, provider+":"+subject)
	return nil
}
