package scmconnection

import (
	"errors"
	"time"
)

const (
	ScopePersonal     = "personal"
	ScopeOrganization = "organization"
	ScopePlatform     = "platform"

	ProviderGitHub       = "github"
	ProviderGitLab       = "gitlab"
	ProviderGitea        = "gitea"
	ProviderManagedLocal = "managed_local"
	ProviderGenericGit   = "generic_git"

	StatusActive  = "active"
	StatusExpired = "expired"
	StatusRevoked = "revoked"
	StatusError   = "error"
)

var (
	ErrDependency         = errors.New("scm connection dependency is not configured")
	ErrInvalidRequest     = errors.New("scm connection request is invalid")
	ErrNotFound           = errors.New("scm connection not found")
	ErrConflict           = errors.New("scm connection version conflict")
	ErrVerificationFailed = errors.New("scm connection verification failed")
	ErrIdempotency        = errors.New("scm connection idempotency conflict")
)

type ViewerRef struct {
	ViewerID               string
	ViewerKind             string
	TenantID               string
	AuthorizationSubjectID string
	PermissionScope        []string
}

type Connection struct {
	TenantID                  string
	ConnectionID              string
	OrganizationID            string
	OwnerID                   string
	Scope                     string
	Provider                  string
	BaseURL                   string
	InstallationOrAccountID   string
	SecretRef                 string
	AllowedRepositoryPatterns []string
	Status                    string
	LastVerifiedAt            time.Time
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
	Version                   int64
	PendingEffects            []PendingEffect
	MutationReceipts          []MutationReceipt
	CreateIdempotencyKey      string
	CreateRequestHash         string
}

type PendingEffect struct {
	EffectID    string      `json:"effect_id"`
	Kind        string      `json:"kind"`
	SubjectKind string      `json:"subject_kind,omitempty"`
	SubjectID   string      `json:"subject_id,omitempty"`
	Audit       *AuditEvent `json:"audit,omitempty"`
}

type MutationReceipt struct {
	IdempotencyKey string        `json:"idempotency_key"`
	Operation      string        `json:"operation"`
	RequestHash    string        `json:"request_hash"`
	Verification   *Verification `json:"verification,omitempty"`
	Failure        string        `json:"failure,omitempty"`
}

type ConnectionDTO struct {
	ConnectionID              string                 `json:"connection_id"`
	OrganizationID            string                 `json:"organization_id,omitempty"`
	OwnerID                   string                 `json:"owner_id,omitempty"`
	Scope                     string                 `json:"scope"`
	Provider                  string                 `json:"provider"`
	BaseURL                   string                 `json:"base_url,omitempty"`
	InstallationOrAccountID   string                 `json:"installation_or_account_id,omitempty"`
	AllowedRepositoryPatterns []string               `json:"allowed_repository_patterns,omitempty"`
	Status                    string                 `json:"status"`
	LastVerifiedAt            time.Time              `json:"last_verified_at,omitempty"`
	CreatedAt                 time.Time              `json:"created_at"`
	UpdatedAt                 time.Time              `json:"updated_at"`
	Version                   int64                  `json:"version"`
	Capabilities              ConnectionCapabilities `json:"capabilities"`
}

type ConnectionCapabilities struct {
	CanView   bool `json:"can_view"`
	CanUse    bool `json:"can_use"`
	CanManage bool `json:"can_manage"`
	CanVerify bool `json:"can_verify"`
	CanRotate bool `json:"can_rotate"`
	CanRevoke bool `json:"can_revoke"`
}

type ConnectionPage struct {
	Items []ConnectionDTO `json:"items"`
	Page  PageDTO         `json:"page"`
}

type PageDTO struct {
	Limit    int  `json:"limit"`
	Offset   int  `json:"offset"`
	Total    int  `json:"total"`
	Returned int  `json:"returned"`
	HasMore  bool `json:"has_more"`
}

type RepositoryMetadata struct {
	RepositoryLocator   string `json:"repository_locator"`
	DisplayName         string `json:"display_name"`
	DefaultBranch       string `json:"default_branch"`
	HeadCommit          string `json:"head_commit"`
	ReadOnly            bool   `json:"read_only"`
	SourceKind          string `json:"source_kind,omitempty"`
	WorkspaceWritable   bool   `json:"workspace_writable"`
	SupportsPush        bool   `json:"supports_push"`
	SupportsPullRequest bool   `json:"supports_pull_request"`
	SupportsMerge       bool   `json:"supports_merge"`
}

type Verification struct {
	VerifiedAt time.Time          `json:"verified_at"`
	Repository RepositoryMetadata `json:"repository"`
}

type CreateCommand struct {
	IdempotencyKey            string   `json:"-"`
	ConnectionID              string   `json:"connection_id,omitempty"`
	OrganizationID            string   `json:"organization_id,omitempty"`
	Scope                     string   `json:"scope"`
	Provider                  string   `json:"provider"`
	BaseURL                   string   `json:"base_url,omitempty"`
	InstallationOrAccountID   string   `json:"installation_or_account_id,omitempty"`
	SecretRef                 string   `json:"secret_ref,omitempty"`
	AllowedRepositoryPatterns []string `json:"allowed_repository_patterns,omitempty"`
}

type UpdateCommand struct {
	IdempotencyKey            string
	ConnectionID              string
	ExpectedVersion           int64
	BaseURL                   *string
	InstallationOrAccountID   *string
	AllowedRepositoryPatterns *[]string
}

type RotateCommand struct {
	IdempotencyKey  string `json:"-"`
	ConnectionID    string `json:"-"`
	SecretRef       string `json:"secret_ref"`
	ExpectedVersion int64  `json:"expected_version,omitempty"`
}

type ActionCommand struct {
	IdempotencyKey  string
	ConnectionID    string
	ExpectedVersion int64
}

type ListQuery struct {
	OrganizationID string
	Provider       string
	Status         string
	Limit          int
	Offset         int
}

type AuditEvent struct {
	AuditID    string
	TenantID   string
	ActorID    string
	EntityID   string
	Action     string
	Result     string
	Summary    string
	Metadata   map[string]string
	OccurredAt time.Time
}
