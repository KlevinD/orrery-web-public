package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/peopledirectory"
)

type feishuProvisioningProfileStore interface {
	GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error)
	QueryDirectory(ctx context.Context, query agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error)
}

type feishuProvisioningSubjectLinker interface {
	LinkSubject(ctx context.Context, req identity.LinkSubjectRequest) (identity.IdentitySubject, error)
}

type feishuIdentityProvisioner struct {
	profiles feishuProvisioningProfileStore
	subjects feishuProvisioningSubjectLinker
}

func (p feishuIdentityProvisioner) ProvisionIdentity(ctx context.Context, external identity.ExternalIdentity) (identity.IdentitySubject, error) {
	external = identity.NormalizeExternalIdentity(external)
	if p.profiles == nil || p.subjects == nil {
		return identity.IdentitySubject{}, identity.ErrServiceDependency
	}
	if external.Provider != identity.ProviderFeishu || external.Subject == "" {
		return identity.IdentitySubject{}, identity.ErrUnauthorized
	}
	profile, err := p.approvedPeopleProfile(ctx, external)
	if err != nil {
		return identity.IdentitySubject{}, err
	}
	return p.subjects.LinkSubject(ctx, identity.LinkSubjectRequest{
		Provider:       external.Provider,
		Subject:        external.Subject,
		AgentProfileID: profile.ID.String(),
		Username:       profile.UserAccount.Username,
		Email:          external.Email,
		DisplayName:    external.DisplayName,
		Status:         identity.SubjectStatusActive,
	})
}

func (p feishuIdentityProvisioner) approvedPeopleProfile(ctx context.Context, external identity.ExternalIdentity) (agentcore.AgentProfile, error) {
	tenantID, sourceIDs := feishuExternalSourceFacts(external)
	if len(sourceIDs) == 0 {
		return agentcore.AgentProfile{}, identity.ErrUnauthorized
	}
	page, err := p.profiles.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{IncludeHumans: true})
	if err != nil {
		return agentcore.AgentProfile{}, err
	}
	var matched agentcore.AgentProfile
	for _, entry := range page.Entries {
		profile, err := p.profiles.GetProfile(ctx, entry.AgentProfileID)
		if errors.Is(err, agentcore.ErrNotFound) {
			continue
		}
		if err != nil {
			return agentcore.AgentProfile{}, err
		}
		facts := profile.PersonSource
		if !strings.EqualFold(strings.TrimSpace(facts.Source), peopledirectory.SourceFeishu) ||
			!sourceIDs[strings.TrimSpace(facts.SourceRecordID)] ||
			(tenantID != "" && !strings.EqualFold(strings.TrimSpace(facts.SourceTenantID), tenantID)) {
			continue
		}
		if !matched.ID.IsZero() && matched.ID != profile.ID {
			return agentcore.AgentProfile{}, fmt.Errorf("%w: Feishu identity matches multiple People records", identity.ErrConflict)
		}
		matched = profile
	}
	if matched.ID.IsZero() || matched.AgentKind != agentcore.AgentKindHuman || !matched.Enabled ||
		!matched.UserAccount.AccountEnabled || strings.TrimSpace(matched.UserAccount.Username) == "" {
		return agentcore.AgentProfile{}, identity.ErrUnauthorized
	}
	return matched, nil
}

func feishuExternalSourceFacts(external identity.ExternalIdentity) (string, map[string]bool) {
	tenantID := strings.TrimSpace(external.Traits["tenant_key"])
	subjectID := strings.TrimSpace(external.Subject)
	if prefix, suffix, ok := strings.Cut(subjectID, ":"); ok {
		if tenantID == "" {
			tenantID = strings.TrimSpace(prefix)
		}
		subjectID = strings.TrimSpace(suffix)
	}
	ids := map[string]bool{}
	for _, value := range []string{
		subjectID,
		external.Traits["open_id"],
		external.Traits["union_id"],
		external.Traits["user_id"],
	} {
		if value = strings.TrimSpace(value); value != "" {
			ids[value] = true
		}
	}
	return tenantID, ids
}
