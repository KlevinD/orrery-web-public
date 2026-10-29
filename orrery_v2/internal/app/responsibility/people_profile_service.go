package responsibility

import (
	"context"
	"sort"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type PeopleResponsibilityProfileDTO struct {
	ScopeType           string                                    `json:"scope_type"`
	ScopeID             string                                    `json:"scope_id"`
	Source              string                                    `json:"source"`
	Portfolios          []PeopleResponsibilityPortfolioProfileDTO `json:"portfolios"`
	ResponsibilityCount int                                       `json:"responsibility_count"`
	VersionCount        int                                       `json:"version_count"`
	UpdatedAt           time.Time                                 `json:"updated_at"`
}

type PeopleResponsibilityPortfolioProfileDTO struct {
	BindingID             string                                    `json:"binding_id"`
	PeopleTenantID        string                                    `json:"people_tenant_id"`
	SubjectID             string                                    `json:"subject_id"`
	AgentProfileID        string                                    `json:"agent_profile_id"`
	AgentDisplayName      string                                    `json:"agent_display_name"`
	OrreryOrganizationID  string                                    `json:"orrery_organization_id,omitempty"`
	OrreryDepartmentID    string                                    `json:"orrery_department_id,omitempty"`
	OrreryPositionID      string                                    `json:"orrery_position_id,omitempty"`
	Status                string                                    `json:"status"`
	CurrentVersionID      string                                    `json:"current_version_id"`
	CurrentSourceRevision string                                    `json:"current_source_revision"`
	CurrentSourceDigest   string                                    `json:"current_source_digest"`
	Responsibilities      []PeopleResolvedResponsibility            `json:"responsibilities"`
	Versions              []PeopleResponsibilityPortfolioVersionDTO `json:"versions"`
	PulseScheduledTaskID  string                                    `json:"pulse_scheduled_task_id,omitempty"`
	Gap                   *PeopleResponsibilityGap                  `json:"gap,omitempty"`
	Audit                 []AuditDTO                                `json:"audit"`
	SourceAsOf            time.Time                                 `json:"source_as_of"`
	SyncedAt              time.Time                                 `json:"synced_at"`
}

type PeopleResponsibilityPortfolioVersionDTO struct {
	VersionID        string                         `json:"version_id"`
	SourceRevision   string                         `json:"source_revision"`
	SourceDigest     string                         `json:"source_digest"`
	Responsibilities []PeopleResolvedResponsibility `json:"responsibilities"`
	SourceAsOf       time.Time                      `json:"source_as_of"`
	RecordedAt       time.Time                      `json:"recorded_at"`
}

func (s *OperationsService) GetPeopleAgentResponsibilityProfile(ctx context.Context, tenantID, agentProfileID string, versionLimit int) (PeopleResponsibilityProfileDTO, error) {
	tenantID, agentProfileID = strings.TrimSpace(tenantID), strings.TrimSpace(agentProfileID)
	if s == nil || s.repository == nil || s.profiles == nil || tenantID == "" || agentProfileID == "" {
		return PeopleResponsibilityProfileDTO{}, ErrInvalidContract
	}
	profile, err := s.profiles.GetProfile(ctx, agentcore.AgentProfileID(agentProfileID))
	if err != nil {
		return PeopleResponsibilityProfileDTO{}, err
	}
	if profile.IsPersonalAssistant() {
		return s.buildPeopleResponsibilityProfile(ctx, "agent", agentProfileID, nil, versionLimit, nil)
	}
	portfolios, err := s.repository.ListPeopleResponsibilityPortfolios(ctx, tenantID)
	if err != nil {
		return PeopleResponsibilityProfileDTO{}, err
	}
	filtered := make([]PeopleResponsibilityPortfolio, 0, len(portfolios))
	keys := make(map[string]struct{}, len(portfolios))
	for _, portfolio := range portfolios {
		if portfolio.AgentProfileID == agentProfileID {
			filtered = append(filtered, portfolio)
			keys[peopleResponsibilityPortfolioSourceKey(portfolio)] = struct{}{}
		}
	}
	if tenantID != "system" {
		if strings.TrimSpace(string(profile.OrgIdentity.OrganizationRef.ID)) == tenantID {
			legacy, legacyErr := s.repository.ListPeopleResponsibilityPortfolios(ctx, "system")
			if legacyErr != nil {
				return PeopleResponsibilityProfileDTO{}, legacyErr
			}
			for _, portfolio := range legacy {
				if portfolio.AgentProfileID != agentProfileID {
					continue
				}
				key := peopleResponsibilityPortfolioSourceKey(portfolio)
				if _, exists := keys[key]; exists {
					continue
				}
				filtered = append(filtered, portfolio)
				keys[key] = struct{}{}
			}
		}
	}
	profiles := map[agentcore.AgentProfileID]agentcore.AgentProfile{profile.ID: profile}
	return s.buildPeopleResponsibilityProfile(ctx, "agent", agentProfileID, filtered, versionLimit, profiles)
}

func (s *OperationsService) GetPeopleOrganizationResponsibilityProfile(ctx context.Context, tenantID, organizationScopeID string, versionLimit int) (PeopleResponsibilityProfileDTO, error) {
	tenantID, organizationScopeID = strings.TrimSpace(tenantID), strings.TrimSpace(organizationScopeID)
	if s == nil || s.repository == nil || s.profiles == nil || tenantID == "" || organizationScopeID == "" {
		return PeopleResponsibilityProfileDTO{}, ErrInvalidContract
	}
	portfolios, err := s.repository.ListPeopleResponsibilityPortfolios(ctx, tenantID)
	if err != nil {
		return PeopleResponsibilityProfileDTO{}, err
	}
	var legacy []PeopleResponsibilityPortfolio

	// People responsibility reconciliation historically stored portfolios in the
	// system tenant. Authenticated workforce viewers now use their organization as
	// tenant, so retain a bounded fallback until those legacy portfolios are
	// naturally reconciled into organization tenants.
	if tenantID != "system" {
		var legacyErr error
		legacy, legacyErr = s.repository.ListPeopleResponsibilityPortfolios(ctx, "system")
		if legacyErr != nil {
			return PeopleResponsibilityProfileDTO{}, legacyErr
		}
	}
	allPortfolios := append(append(make([]PeopleResponsibilityPortfolio, 0, len(portfolios)+len(legacy)), portfolios...), legacy...)
	profiles, err := s.peopleResponsibilityProfiles(ctx, allPortfolios)
	if err != nil {
		return PeopleResponsibilityProfileDTO{}, err
	}
	filtered, keys, err := filterPeopleResponsibilityPortfolios(portfolios, organizationScopeID, "", profiles)
	if err != nil {
		return PeopleResponsibilityProfileDTO{}, err
	}
	if tenantID != "system" {
		legacy, _, err = filterPeopleResponsibilityPortfolios(legacy, organizationScopeID, tenantID, profiles)
		if err != nil {
			return PeopleResponsibilityProfileDTO{}, err
		}
		for _, portfolio := range legacy {
			key := peopleResponsibilityPortfolioSourceKey(portfolio)
			if _, exists := keys[key]; exists {
				continue
			}
			filtered = append(filtered, portfolio)
			keys[key] = struct{}{}
		}
	}
	return s.buildPeopleResponsibilityProfile(ctx, "organization", organizationScopeID, filtered, versionLimit, profiles)
}

func (s *OperationsService) peopleResponsibilityProfiles(ctx context.Context, portfolios []PeopleResponsibilityPortfolio) (map[agentcore.AgentProfileID]agentcore.AgentProfile, error) {
	ids := make([]agentcore.AgentProfileID, 0, len(portfolios))
	seen := make(map[agentcore.AgentProfileID]struct{}, len(portfolios))
	for _, portfolio := range portfolios {
		id := agentcore.AgentProfileID(strings.TrimSpace(portfolio.AgentProfileID))
		if id.IsZero() {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	profiles := make(map[agentcore.AgentProfileID]agentcore.AgentProfile, len(ids))
	if batch, ok := s.profiles.(AgentProfileBatchPort); ok {
		loaded, err := batch.GetProfiles(ctx, ids)
		if err != nil {
			return nil, err
		}
		for id, profile := range loaded {
			profiles[id] = profile
		}
	}
	for _, id := range ids {
		if _, ok := profiles[id]; ok {
			continue
		}
		profile, err := s.profiles.GetProfile(ctx, id)
		if err != nil {
			return nil, err
		}
		profiles[id] = profile
	}
	return profiles, nil
}

func filterPeopleResponsibilityPortfolios(portfolios []PeopleResponsibilityPortfolio, scopeID, requiredOrganizationID string, profiles map[agentcore.AgentProfileID]agentcore.AgentProfile) ([]PeopleResponsibilityPortfolio, map[string]struct{}, error) {
	filtered := make([]PeopleResponsibilityPortfolio, 0, len(portfolios))
	keys := make(map[string]struct{}, len(portfolios))
	for _, portfolio := range portfolios {
		profile, ok := profiles[agentcore.AgentProfileID(portfolio.AgentProfileID)]
		if !ok {
			return nil, nil, agentcore.ErrNotFound
		}
		if profile.IsPersonalAssistant() {
			continue
		}
		organizationID := strings.TrimSpace(string(profile.OrgIdentity.OrganizationRef.ID))
		departmentID := strings.TrimSpace(string(profile.OrgIdentity.DepartmentRef.ID))
		if requiredOrganizationID != "" && organizationID != requiredOrganizationID {
			continue
		}
		if scopeID != organizationID && scopeID != departmentID {
			continue
		}
		filtered = append(filtered, portfolio)
		keys[peopleResponsibilityPortfolioSourceKey(portfolio)] = struct{}{}
	}
	return filtered, keys, nil
}

func peopleResponsibilityPortfolioSourceKey(portfolio PeopleResponsibilityPortfolio) string {
	return strings.Join([]string{portfolio.PeopleTenantID, portfolio.SubjectID, portfolio.AgentProfileID}, "\x00")
}

func (s *OperationsService) buildPeopleResponsibilityProfile(ctx context.Context, scopeType, scopeID string, portfolios []PeopleResponsibilityPortfolio, versionLimit int, profiles map[agentcore.AgentProfileID]agentcore.AgentProfile) (PeopleResponsibilityProfileDTO, error) {
	result := PeopleResponsibilityProfileDTO{ScopeType: scopeType, ScopeID: scopeID, Source: "people", Portfolios: make([]PeopleResponsibilityPortfolioProfileDTO, 0, len(portfolios))}
	portfolioIDs := make([]string, 0, len(portfolios))
	for _, portfolio := range portfolios {
		portfolioIDs = append(portfolioIDs, portfolio.ID)
	}
	includeHistory := versionLimit != 0
	versions := make(map[string][]PeopleResponsibilityPortfolioVersion)
	if includeHistory {
		var err error
		versions, err = s.repository.ListPeopleResponsibilityPortfolioVersionsForPortfolios(ctx, portfolioIDs, versionLimit)
		if err != nil {
			return PeopleResponsibilityProfileDTO{}, err
		}
	}
	audits := make(map[string][]AuditRecord)
	if includeHistory {
		var err error
		audits, err = s.peopleResponsibilityAudits(ctx, portfolios)
		if err != nil {
			return PeopleResponsibilityProfileDTO{}, err
		}
	}
	for _, portfolio := range portfolios {
		profile, ok := profiles[agentcore.AgentProfileID(portfolio.AgentProfileID)]
		if !ok {
			return PeopleResponsibilityProfileDTO{}, agentcore.ErrNotFound
		}
		view, err := s.buildPeoplePortfolioProfile(ctx, portfolio, profile, versions[portfolio.ID], includeHistory, audits[peopleResponsibilityAuditKey(portfolio.TenantID, portfolio.BindingID)])
		if err != nil {
			return PeopleResponsibilityProfileDTO{}, err
		}
		result.Portfolios = append(result.Portfolios, view)
		result.ResponsibilityCount += len(view.Responsibilities)
		result.VersionCount += len(view.Versions)
		if portfolio.UpdatedAt.After(result.UpdatedAt) {
			result.UpdatedAt = portfolio.UpdatedAt
		}
	}
	sort.Slice(result.Portfolios, func(i, j int) bool { return result.Portfolios[i].AgentProfileID < result.Portfolios[j].AgentProfileID })
	return result, nil
}

func (s *OperationsService) peopleResponsibilityAudits(ctx context.Context, portfolios []PeopleResponsibilityPortfolio) (map[string][]AuditRecord, error) {
	bindingsByTenant := make(map[string][]string)
	for _, portfolio := range portfolios {
		bindingsByTenant[portfolio.TenantID] = append(bindingsByTenant[portfolio.TenantID], portfolio.BindingID)
	}
	result := make(map[string][]AuditRecord, len(portfolios))
	for tenantID, bindingIDs := range bindingsByTenant {
		audits, err := s.repository.ListAuditsForResponsibilities(ctx, tenantID, bindingIDs, 100)
		if err != nil {
			return nil, err
		}
		for bindingID, records := range audits {
			result[peopleResponsibilityAuditKey(tenantID, bindingID)] = records
		}
	}
	return result, nil
}

func peopleResponsibilityAuditKey(tenantID, bindingID string) string {
	return strings.Join([]string{strings.TrimSpace(tenantID), strings.TrimSpace(bindingID)}, "\x00")
}

func (s *OperationsService) buildPeoplePortfolioProfile(ctx context.Context, portfolio PeopleResponsibilityPortfolio, profile agentcore.AgentProfile, versions []PeopleResponsibilityPortfolioVersion, includeHistory bool, audits []AuditRecord) (PeopleResponsibilityPortfolioProfileDTO, error) {
	view := PeopleResponsibilityPortfolioProfileDTO{
		BindingID: portfolio.BindingID, PeopleTenantID: portfolio.PeopleTenantID, SubjectID: portfolio.SubjectID,
		AgentProfileID: portfolio.AgentProfileID, AgentDisplayName: profile.DisplayName,
		OrreryOrganizationID: string(profile.OrgIdentity.OrganizationRef.ID), OrreryDepartmentID: string(profile.OrgIdentity.DepartmentRef.ID),
		OrreryPositionID: string(profile.OrgIdentity.PositionRef.ID), Status: portfolio.Status,
		CurrentVersionID:      PeopleResponsibilityPortfolioVersionID(portfolio.BindingID, portfolio.SourceDigest),
		CurrentSourceRevision: portfolio.SourceRevision, CurrentSourceDigest: portfolio.SourceDigest,
		Responsibilities: portfolio.Responsibilities, Versions: make([]PeopleResponsibilityPortfolioVersionDTO, 0, len(versions)), Audit: make([]AuditDTO, 0),
		PulseScheduledTaskID: portfolio.PulseScheduledTaskID, SourceAsOf: portfolio.SourceAsOf, SyncedAt: portfolio.SyncedAt,
	}
	for _, version := range versions {
		view.Versions = append(view.Versions, PeopleResponsibilityPortfolioVersionDTO{
			VersionID: version.ID, SourceRevision: version.SourceRevision, SourceDigest: version.SourceDigest,
			Responsibilities: version.Responsibilities, SourceAsOf: version.SourceAsOf, RecordedAt: version.RecordedAt,
		})
	}
	if gap, found, err := s.repository.GetPeopleResponsibilityGap(ctx, PeopleResponsibilityGapID(portfolio.BindingID)); err != nil {
		return PeopleResponsibilityPortfolioProfileDTO{}, err
	} else if found && gap.Open {
		view.Gap = &gap
	}
	if !includeHistory {
		return view, nil
	}
	view.Audit = mapAudits(audits)
	return view, nil
}
