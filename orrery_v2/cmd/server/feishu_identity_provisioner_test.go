package main

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/peopledirectory"
)

func TestFeishuIdentityProvisionerLinksApprovedPeopleProfileWithoutCreatingWorkforceData(t *testing.T) {
	profiles := &fakeFeishuProvisioningProfiles{items: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"human-alice": {
			ID: "human-alice", AgentKind: agentcore.AgentKindHuman, Enabled: true,
			PersonSource: agentcore.PersonSourceFacts{Source: peopledirectory.SourceFeishu, SourceRecordID: "on_1", SourceTenantID: "tenant-1"},
			UserAccount:  agentcore.UserAccountSpec{Username: "alice", AccountEnabled: true},
		},
	}}
	subjects := &fakeFeishuProvisioningSubjects{}
	provisioner := feishuIdentityProvisioner{profiles: profiles, subjects: subjects}

	subject, err := provisioner.ProvisionIdentity(context.Background(), identity.ExternalIdentity{
		Provider: identity.ProviderFeishu, Subject: "tenant-1:on_1", Username: "alice@example.test", Email: "alice@example.test", DisplayName: "Alice",
	})
	if err != nil {
		t.Fatalf("ProvisionIdentity: %v", err)
	}
	if subject.AgentProfileID != "human-alice" || subjects.request.AgentProfileID != subject.AgentProfileID || subjects.request.Provider != identity.ProviderFeishu || subjects.request.Username != "alice" {
		t.Fatalf("subject = %+v request=%+v", subject, subjects.request)
	}
	if profiles.saveCalls != 0 {
		t.Fatalf("provisioner wrote %d workforce profiles", profiles.saveCalls)
	}
}

func TestFeishuIdentityProvisionerRejectsUnknownPersonWithoutCreatingData(t *testing.T) {
	profiles := &fakeFeishuProvisioningProfiles{items: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	subjects := &fakeFeishuProvisioningSubjects{}
	provisioner := feishuIdentityProvisioner{profiles: profiles, subjects: subjects}

	_, err := provisioner.ProvisionIdentity(context.Background(), identity.ExternalIdentity{
		Provider: identity.ProviderFeishu, Subject: "tenant-1:on_unknown", DisplayName: "Unknown",
	})
	if !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatalf("ProvisionIdentity error = %v, want ErrUnauthorized", err)
	}
	if len(profiles.items) != 0 || profiles.saveCalls != 0 || subjects.request.AgentProfileID != "" {
		t.Fatalf("unknown identity created data: profiles=%+v save_calls=%d request=%+v", profiles.items, profiles.saveCalls, subjects.request)
	}
}

type fakeFeishuProvisioningProfiles struct {
	items     map[agentcore.AgentProfileID]agentcore.AgentProfile
	saveCalls int
}

func (s *fakeFeishuProvisioningProfiles) GetProfile(_ context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	profile, ok := s.items[id]
	if !ok {
		return agentcore.AgentProfile{}, agentcore.ErrNotFound
	}
	return profile, nil
}

func (s *fakeFeishuProvisioningProfiles) QueryDirectory(_ context.Context, _ agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	page := agentcore.AgentDirectoryPage{}
	for id, profile := range s.items {
		page.Entries = append(page.Entries, agentcore.AgentDirectoryEntry{AgentProfileID: id, AgentKind: profile.AgentKind, Enabled: profile.Enabled})
	}
	page.Total = len(page.Entries)
	return page, nil
}

type fakeFeishuProvisioningSubjects struct {
	request identity.LinkSubjectRequest
}

func (s *fakeFeishuProvisioningSubjects) LinkSubject(_ context.Context, req identity.LinkSubjectRequest) (identity.IdentitySubject, error) {
	s.request = req
	return identity.IdentitySubject{
		Provider: req.Provider, Subject: req.Subject, AgentProfileID: req.AgentProfileID, Username: req.Username, Email: req.Email, DisplayName: req.DisplayName, Status: req.Status,
	}, nil
}
