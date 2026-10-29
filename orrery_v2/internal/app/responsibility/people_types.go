package responsibility

import (
	"context"
	"strings"
	"time"
)

const (
	peopleAgentBindingTable     = "responsibility_people_agent_bindings"
	peoplePortfolioTable        = "responsibility_people_portfolios"
	peoplePortfolioVersionTable = "responsibility_people_portfolio_versions"
	peopleGapTable              = "responsibility_people_gaps"
	peopleReconcileStateTable   = "responsibility_people_reconcile_state"
	peopleBindingStatusActive   = "active"
	peopleBindingStatusDetached = "detached"
	peoplePortfolioStatusActive = "active"
	peoplePortfolioStatusPaused = "paused"
)

const (
	PeoplePortfolioStatusActive = peoplePortfolioStatusActive
	PeoplePortfolioStatusPaused = peoplePortfolioStatusPaused
)

type PeopleAgentBindingRecord struct {
	ID             string    `json:"id"`
	TenantID       string    `json:"tenant_id"`
	PeopleTenantID string    `json:"people_tenant_id"`
	SubjectID      string    `json:"subject_id"`
	AgentProfileID string    `json:"agent_profile_id"`
	Source         string    `json:"source"`
	Status         string    `json:"status"`
	Version        int64     `json:"version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type PeopleResponsibilityBinding struct {
	BindingID        string     `json:"binding_id"`
	ID               string     `json:"id"`
	ResponsibilityID string     `json:"responsibility_id"`
	TargetType       string     `json:"target_type"`
	TargetID         string     `json:"target_id"`
	ValidFrom        time.Time  `json:"valid_from"`
	ValidTo          *time.Time `json:"valid_to"`
	Revision         int64      `json:"revision"`
}

type PeopleResolvedResponsibility struct {
	ID          string                        `json:"id"`
	Code        string                        `json:"code"`
	Title       string                        `json:"title"`
	Description string                        `json:"description"`
	ValidFrom   time.Time                     `json:"valid_from"`
	ValidTo     *time.Time                    `json:"valid_to"`
	Revision    int64                         `json:"revision"`
	Sources     []PeopleResponsibilityBinding `json:"sources"`
}

type PeopleResponsibilityResolution struct {
	SubjectID      string                         `json:"subject_id"`
	Items          []PeopleResolvedResponsibility `json:"items"`
	AsOf           time.Time                      `json:"as_of"`
	SourceRevision string                         `json:"source_revision"`
}

type PeopleResponsibilityPortfolio struct {
	ID                   string                         `json:"id"`
	TenantID             string                         `json:"tenant_id"`
	BindingID            string                         `json:"binding_id"`
	PeopleTenantID       string                         `json:"people_tenant_id"`
	SubjectID            string                         `json:"subject_id"`
	AgentProfileID       string                         `json:"agent_profile_id"`
	SourceRevision       string                         `json:"source_revision"`
	SourceDigest         string                         `json:"source_digest"`
	Responsibilities     []PeopleResolvedResponsibility `json:"responsibilities"`
	Status               string                         `json:"status"`
	PulseScheduledTaskID string                         `json:"pulse_scheduled_task_id,omitempty"`
	Version              int64                          `json:"version"`
	SourceAsOf           time.Time                      `json:"source_as_of"`
	SyncedAt             time.Time                      `json:"synced_at"`
	CreatedAt            time.Time                      `json:"created_at"`
	UpdatedAt            time.Time                      `json:"updated_at"`
}

type PeopleResponsibilityPortfolioVersion struct {
	ID               string                         `json:"id"`
	PortfolioID      string                         `json:"portfolio_id"`
	TenantID         string                         `json:"tenant_id"`
	BindingID        string                         `json:"binding_id"`
	PeopleTenantID   string                         `json:"people_tenant_id"`
	SubjectID        string                         `json:"subject_id"`
	AgentProfileID   string                         `json:"agent_profile_id"`
	SourceRevision   string                         `json:"source_revision"`
	SourceDigest     string                         `json:"source_digest"`
	Responsibilities []PeopleResolvedResponsibility `json:"responsibilities"`
	SourceAsOf       time.Time                      `json:"source_as_of"`
	RecordedAt       time.Time                      `json:"recorded_at"`
}

type PeopleResponsibilityGap struct {
	ID             string    `json:"id"`
	TenantID       string    `json:"tenant_id"`
	BindingID      string    `json:"binding_id"`
	SubjectID      string    `json:"subject_id"`
	AgentProfileID string    `json:"agent_profile_id"`
	Code           string    `json:"code"`
	Message        string    `json:"message"`
	Open           bool      `json:"open"`
	FirstSeenAt    time.Time `json:"first_seen_at"`
	LastSeenAt     time.Time `json:"last_seen_at"`
	ResolvedAt     time.Time `json:"resolved_at,omitempty"`
}

type PeopleResponsibilityReconcileState struct {
	ID             string    `json:"id"`
	TenantID       string    `json:"tenant_id"`
	PeopleTenantID string    `json:"people_tenant_id"`
	LastAttemptAt  time.Time `json:"last_attempt_at"`
	LastSuccessAt  time.Time `json:"last_success_at,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
	Version        int64     `json:"version"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type PeopleResponsibilityReconcileStats struct {
	SourceAttempted     bool
	SourceThrottled     bool
	BindingsScanned     int
	BindingsChanged     int
	PortfoliosChanged   int
	RoleProfilesChanged int
	PulsesEnsured       int
	PulsesDisabled      int
	GapsOpen            int
	GapsResolved        int
	Failed              int
}

type PeopleResponsibilityRoleSyncInput struct {
	TenantID       string
	PeopleTenantID string
	SubjectID      string
	AgentProfileID string
	SourceRevision string
	ManagedRoleIDs []string
	DesiredRoleIDs []string
}

type PeopleResponsibilityRoleSyncPort interface {
	SyncPeopleResponsibilityRoles(context.Context, PeopleResponsibilityRoleSyncInput) (bool, error)
}

func PeopleAgentBindingID(tenantID, peopleTenantID, subjectID string) string {
	return "people-agent-binding-" + stableHex(strings.TrimSpace(tenantID), strings.TrimSpace(peopleTenantID), strings.TrimSpace(subjectID))[:24]
}

func PeopleResponsibilityPortfolioID(bindingID string) string {
	return "people-responsibility-portfolio-" + stableHex(strings.TrimSpace(bindingID))[:24]
}

func PeopleResponsibilityPortfolioVersionID(bindingID, sourceDigest string) string {
	return "people-responsibility-portfolio-version-" + stableHex(strings.TrimSpace(bindingID), strings.TrimSpace(sourceDigest))[:24]
}

func PeopleResponsibilityGapID(bindingID string) string {
	return "people-responsibility-gap-" + stableHex(strings.TrimSpace(bindingID))[:24]
}

func peopleResponsibilityReconcileStateID(tenantID, peopleTenantID string) string {
	return "people-responsibility-reconcile-" + stableHex(strings.TrimSpace(tenantID), strings.TrimSpace(peopleTenantID))[:24]
}
