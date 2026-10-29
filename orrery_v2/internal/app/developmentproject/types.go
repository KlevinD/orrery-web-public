package developmentproject

import (
	"errors"
	"io"
	"time"
)

const (
	StatusActive   = "active"
	StatusArchived = "archived"

	AutomationManual  = "manual"
	AutomationSuggest = "suggest"
	AutomationManaged = "managed"

	MemberRoleOwner       = "owner"
	MemberRoleMaintainer  = "maintainer"
	MemberRoleContributor = "contributor"
	MemberRoleViewer      = "viewer"
	MemberRoleExecutor    = "executor"
	MemberRoleMerger      = "merger"

	RepositoryRolePrimary    = "primary"
	RepositoryRoleDependency = "dependency"

	AccessReadOnly  = "read_only"
	AccessReadWrite = "read_write"

	BindingStatusActive = "active"
	BindingStatusError  = "error"

	NetworkNone       = "none"
	NetworkRestricted = "restricted"
	NetworkAllowed    = "allowed"
)

var (
	ErrDependency        = errors.New("development project dependency is not configured")
	ErrInvalidRequest    = errors.New("development project request is invalid")
	ErrNotFound          = errors.New("development project not found")
	ErrConflict          = errors.New("development project version conflict")
	ErrArchived          = errors.New("development project is archived")
	ErrRepositoryPolicy  = errors.New("development project repository policy violation")
	ErrIdempotency       = errors.New("development project idempotency conflict")
	ErrWorkspaceLease    = errors.New("development workspace is already leased")
	ErrWorkspaceConflict = errors.New("development workspace identity conflict")
	ErrInvalidState      = errors.New("development execution state transition is invalid")
	ErrApprovalStale     = errors.New("development merge approval is stale")
	ErrMergeForbidden    = errors.New("development merge is not permitted for this repository source")
)

const (
	WorkspaceStatusProvisioning = "provisioning"
	WorkspaceStatusReady        = "ready"
	WorkspaceStatusLeased       = "leased"
	WorkspaceStatusReview       = "review"
	WorkspaceStatusMerged       = "merged"
	WorkspaceStatusFailed       = "failed"
	WorkspaceStatusArchived     = "archived"

	ExecutionRunStatusProvisioning = "provisioning"
	ExecutionRunStatusReady        = "ready"
	ExecutionRunStatusRunning      = "running"
	ExecutionRunStatusReview       = "review"
	ExecutionRunStatusCompleted    = "completed"
	ExecutionRunStatusFailed       = "failed"
	ExecutionRunStatusCanceled     = "canceled"

	SubmissionStatusLocalCandidate          = "local_candidate"
	SubmissionStatusPushPending             = "push_pending"
	SubmissionStatusPushed                  = "pushed"
	SubmissionStatusPROpen                  = "pr_open"
	SubmissionStatusChangesRequested        = "changes_requested"
	SubmissionStatusChecksRunning           = "checks_running"
	SubmissionStatusMergeReady              = "merge_ready"
	SubmissionStatusMergeApprovalPending    = "merge_approval_pending"
	SubmissionStatusMerging                 = "merging"
	SubmissionStatusMerged                  = "merged"
	SubmissionStatusConflicted              = "conflicted"
	SubmissionStatusMergeVerificationFailed = "merge_verification_failed"
	SubmissionStatusClosed                  = "closed"
	SubmissionStatusFailed                  = "failed"

	MergeApprovalStatusApproved = "approved"
	MergeApprovalStatusStale    = "stale"
	MergeApprovalStatusConsumed = "consumed"

	RepositorySourceManagedLocal = "managed_local_source"
	RepositorySourceManagedBare  = "managed_bare_authoritative"
)

type ViewerRef struct {
	ViewerID               string
	ViewerKind             string
	TenantID               string
	AuthorizationSubjectID string
	PermissionScope        []string
}

type ExecutionPolicy struct {
	ApprovalRequired       bool     `json:"approval_required"`
	NetworkMode            string   `json:"network_mode"`
	AllowedHosts           []string `json:"allowed_hosts,omitempty"`
	AllowedCommandPrefixes []string `json:"allowed_command_prefixes,omitempty"`
	AllowedFileScopes      []string `json:"allowed_file_scopes,omitempty"`
	AllowedSecretRefs      []string `json:"allowed_secret_refs,omitempty"`
}

type CheckEvidence struct {
	Command   []string  `json:"command"`
	ExitCode  int       `json:"exit_code"`
	Succeeded bool      `json:"succeeded"`
	Summary   string    `json:"summary,omitempty"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
}

type DevelopmentWorkspace struct {
	TenantID             string    `json:"-"`
	WorkspaceID          string    `json:"workspace_id"`
	DevelopmentProjectID string    `json:"development_project_id"`
	TodoID               string    `json:"todo_id"`
	ExecutionAttemptID   string    `json:"execution_attempt_id"`
	RepositoryBindingID  string    `json:"repository_binding_id"`
	StorageKey           string    `json:"storage_key"`
	SourceKind           string    `json:"source_kind"`
	BranchName           string    `json:"branch_name"`
	BaseCommit           string    `json:"base_commit"`
	HeadCommit           string    `json:"head_commit"`
	Status               string    `json:"status"`
	LeaseOwnerRunID      string    `json:"lease_owner_run_id,omitempty"`
	LeaseExpiresAt       time.Time `json:"lease_expires_at,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
	LastActiveAt         time.Time `json:"last_active_at"`
	CleanupAfter         time.Time `json:"cleanup_after,omitempty"`
	FailureReason        string    `json:"failure_reason,omitempty"`
	Version              int64     `json:"version"`
}

type TodoExecutionRun struct {
	TenantID               string          `json:"-"`
	ExecutionRunID         string          `json:"execution_run_id"`
	DevelopmentProjectID   string          `json:"development_project_id"`
	TodoID                 string          `json:"todo_id"`
	PlanID                 string          `json:"plan_id"`
	PlanDigest             string          `json:"plan_digest"`
	ApprovalID             string          `json:"approval_id"`
	MissionID              string          `json:"mission_id"`
	MissionTaskID          string          `json:"mission_task_id"`
	AgentRunID             string          `json:"agent_run_id,omitempty"`
	WorkspaceID            string          `json:"workspace_id"`
	AgentID                string          `json:"agent_id"`
	ResourceSnapshotDigest string          `json:"resource_snapshot_digest"`
	AllowedFileScopes      []string        `json:"allowed_file_scopes"`
	PlannedCommands        []string        `json:"planned_commands"`
	NetworkMode            string          `json:"network_mode"`
	AllowedHosts           []string        `json:"allowed_hosts,omitempty"`
	RequiredSecretRefs     []string        `json:"required_secret_refs,omitempty"`
	Status                 string          `json:"status"`
	StartedAt              time.Time       `json:"started_at,omitempty"`
	FinishedAt             time.Time       `json:"finished_at,omitempty"`
	CommitBefore           string          `json:"commit_before,omitempty"`
	CommitAfter            string          `json:"commit_after,omitempty"`
	DiffSummary            string          `json:"diff_summary,omitempty"`
	TestResults            []CheckEvidence `json:"test_results,omitempty"`
	ArtifactRefs           []string        `json:"artifact_refs,omitempty"`
	FailureReason          string          `json:"failure_reason,omitempty"`
	CreatedAt              time.Time       `json:"created_at"`
	UpdatedAt              time.Time       `json:"updated_at"`
	Version                int64           `json:"version"`
}

type TodoCommitRecord struct {
	TenantID             string    `json:"-"`
	CommitRecordID       string    `json:"commit_record_id"`
	DevelopmentProjectID string    `json:"development_project_id"`
	TodoID               string    `json:"todo_id"`
	ExecutionRunID       string    `json:"execution_run_id"`
	PlanID               string    `json:"plan_id"`
	RepositoryBindingID  string    `json:"repository_binding_id"`
	CommitSHA            string    `json:"commit_sha"`
	ParentSHAs           []string  `json:"parent_shas"`
	TreeSHA              string    `json:"tree_sha"`
	AuthorKind           string    `json:"author_kind"`
	AuthorID             string    `json:"author_id"`
	CommitterID          string    `json:"committer_id"`
	Message              string    `json:"message"`
	SignatureStatus      string    `json:"signature_status"`
	ChangedFiles         []string  `json:"changed_files"`
	Additions            int       `json:"additions"`
	Deletions            int       `json:"deletions"`
	DiffDigest           string    `json:"diff_digest"`
	CreatedAt            time.Time `json:"created_at"`
}

type MergeApproval struct {
	MergeApprovalID    string    `json:"merge_approval_id"`
	SubmissionID       string    `json:"submission_id"`
	SubmissionRevision int       `json:"submission_revision"`
	ApprovedBy         string    `json:"approved_by"`
	TargetBranch       string    `json:"target_branch"`
	ExpectedBaseHead   string    `json:"expected_base_head"`
	HeadCommit         string    `json:"head_commit"`
	TreeSHA            string    `json:"tree_sha"`
	DiffDigest         string    `json:"diff_digest"`
	ChecksDigest       string    `json:"checks_digest"`
	PlanDigest         string    `json:"plan_digest"`
	MergeStrategy      string    `json:"merge_strategy"`
	Status             string    `json:"status"`
	ApprovedAt         time.Time `json:"approved_at"`
	StaleAt            time.Time `json:"stale_at,omitempty"`
}

type DevelopmentMergeReceipt struct {
	ReceiptID          string    `json:"receipt_id"`
	IdempotencyKey     string    `json:"idempotency_key"`
	SubmissionID       string    `json:"submission_id"`
	SubmissionRevision int       `json:"submission_revision"`
	ApprovalID         string    `json:"approval_id"`
	ProviderReceiptID  string    `json:"provider_receipt_id,omitempty"`
	MergeCommitSHA     string    `json:"merge_commit_sha"`
	TreeSHA            string    `json:"tree_sha"`
	TargetHead         string    `json:"target_head"`
	MergedBy           string    `json:"merged_by"`
	MergedAt           time.Time `json:"merged_at"`
}

type TodoCodeSubmission struct {
	TenantID               string                   `json:"-"`
	SubmissionID           string                   `json:"submission_id"`
	Revision               int                      `json:"revision"`
	Version                int64                    `json:"version"`
	DevelopmentProjectID   string                   `json:"development_project_id"`
	TodoID                 string                   `json:"todo_id"`
	PlanID                 string                   `json:"plan_id"`
	PlanDigest             string                   `json:"plan_digest"`
	WorkspaceID            string                   `json:"workspace_id"`
	RepositoryBindingID    string                   `json:"repository_binding_id"`
	BranchName             string                   `json:"branch_name"`
	TargetBranch           string                   `json:"target_branch"`
	BaseCommit             string                   `json:"base_commit"`
	HeadCommit             string                   `json:"head_commit"`
	TreeSHA                string                   `json:"tree_sha"`
	CommitSHAs             []string                 `json:"commit_shas"`
	DiffDigest             string                   `json:"diff_digest"`
	ChangedFiles           []string                 `json:"changed_files"`
	Additions              int                      `json:"additions"`
	Deletions              int                      `json:"deletions"`
	Status                 string                   `json:"status"`
	RemoteBranchRef        string                   `json:"remote_branch_ref,omitempty"`
	PullRequestProvider    string                   `json:"pull_request_provider,omitempty"`
	PullRequestID          string                   `json:"pull_request_id,omitempty"`
	PullRequestURL         string                   `json:"pull_request_url,omitempty"`
	RequiredChecksSnapshot []CheckEvidence          `json:"required_checks_snapshot"`
	MergeStrategy          string                   `json:"merge_strategy"`
	ExpectedBaseHead       string                   `json:"expected_base_head,omitempty"`
	MergePreviewTreeSHA    string                   `json:"merge_preview_tree_sha,omitempty"`
	MergeApproval          *MergeApproval           `json:"merge_approval,omitempty"`
	MergeReceipt           *DevelopmentMergeReceipt `json:"merge_receipt,omitempty"`
	ReviewComment          string                   `json:"review_comment,omitempty"`
	CreatedBy              string                   `json:"created_by"`
	MergedBy               string                   `json:"merged_by,omitempty"`
	MergedAt               time.Time                `json:"merged_at,omitempty"`
	CreatedAt              time.Time                `json:"created_at"`
	UpdatedAt              time.Time                `json:"updated_at"`
	PriorRevisions         []CodeSubmissionRevision `json:"prior_revisions,omitempty"`
}

type CodeSubmissionRevision struct {
	Revision               int             `json:"revision"`
	HeadCommit             string          `json:"head_commit"`
	TreeSHA                string          `json:"tree_sha"`
	DiffDigest             string          `json:"diff_digest"`
	Status                 string          `json:"status"`
	ExpectedBaseHead       string          `json:"expected_base_head,omitempty"`
	MergePreviewTreeSHA    string          `json:"merge_preview_tree_sha,omitempty"`
	RequiredChecksSnapshot []CheckEvidence `json:"required_checks_snapshot,omitempty"`
	MergeApproval          *MergeApproval  `json:"merge_approval,omitempty"`
	RecordedAt             time.Time       `json:"recorded_at"`
}

type WorkspacePage struct {
	Items []DevelopmentWorkspace `json:"items"`
	Page  PageDTO                `json:"page"`
}
type ExecutionRunPage struct {
	Items []TodoExecutionRun `json:"items"`
	Page  PageDTO            `json:"page"`
}
type CommitPage struct {
	Items []TodoCommitRecord `json:"items"`
	Page  PageDTO            `json:"page"`
}
type CodeSubmissionPage struct {
	Items []TodoCodeSubmission `json:"items"`
	Page  PageDTO              `json:"page"`
}

type CodeExportDownload struct {
	Name        string
	ContentType string
	Size        int64
	Reader      io.ReadCloser
}

type ListExecutionArtifactsQuery struct {
	TodoID string
	Status string
	Limit  int
	Offset int
}

type ProvisionTodoWorkspaceCommand struct {
	TenantID               string
	DevelopmentProjectID   string
	TodoID                 string
	TodoTitle              string
	PlanID                 string
	PlanDigest             string
	ResourceSnapshotDigest string
	RepositoryBindingID    string
	BaseCommit             string
	AllowedFileScopes      []string
	PlannedCommands        []string
	NetworkMode            string
	AllowedHosts           []string
	RequiredSecretRefs     []string
	ApprovalID             string
	MissionID              string
	MissionTaskIDs         []string
	AgentIDs               []string
	LeaseOwnerRunID        string
}

type ProvisionTodoWorkspaceResult struct {
	Workspace DevelopmentWorkspace
	Runs      []TodoExecutionRun
}

type ResolvedManagedWorkspace struct {
	RootDir     string
	WorkDir     string
	NetworkMode string
}

type PreparedAgentRun struct {
	ExecutionRunID string
	WorkspaceID    string
	RootDir        string
	WorkDir        string
	NetworkMode    string
	AllowedHosts   []string
}

type CreateCommitCommand struct {
	IdempotencyKey     string
	ExecutionRunID     string
	ExpectedRunVersion int64
	Summary            string
	AuthorName         string
	AuthorEmail        string
}

type CreateSubmissionCommand struct {
	IdempotencyKey string
	TodoID         string
	ExecutionRunID string
	HeadCommit     string
	MergeStrategy  string
}

type SubmissionCommand struct {
	IdempotencyKey     string
	TodoID             string
	SubmissionID       string
	ExpectedVersion    int64
	ExpectedRevision   int
	ExpectedHeadCommit string
}

type RequestChangesCommand struct {
	SubmissionCommand
	Comment string
}
type MergeApprovalCommand struct {
	SubmissionCommand
	ExpectedBaseHead string
	ExpectedTreeSHA  string
	DiffDigest       string
	ChecksDigest     string
}

// ExecutionPlanSnapshot is an internal, tenant-scoped snapshot used to bind a
// Todo approval to immutable repository and execution-policy inputs. Secret
// values are never present; callers must not expose AllowedSecretRefs directly.
type ExecutionPlanSnapshot struct {
	DevelopmentProjectID string
	SnapshotDigest       string
	Repositories         []ExecutionPlanRepository
	ExecutionPolicy      ExecutionPolicy
}

type ExecutionPlanRepository struct {
	BindingID          string
	DisplayName        string
	Role               string
	AccessMode         string
	DefaultBranch      string
	SourceSubdirectory string
	BuildCommands      []string
	TestCommands       []string
	BaseCommit         string
}

type Project struct {
	TenantID              string              `json:"tenant_id"`
	DevelopmentProjectID  string              `json:"development_project_id"`
	OrganizationID        string              `json:"organization_id,omitempty"`
	Name                  string              `json:"name"`
	Description           string              `json:"description,omitempty"`
	Status                string              `json:"status"`
	DefaultAutomationMode string              `json:"default_automation_mode"`
	MaxConcurrentRuns     int                 `json:"max_concurrent_runs"`
	ExecutionPolicy       ExecutionPolicy     `json:"execution_policy"`
	CreatedBy             string              `json:"created_by"`
	CreatedAt             time.Time           `json:"created_at"`
	UpdatedAt             time.Time           `json:"updated_at"`
	Version               int64               `json:"version"`
	Members               []Member            `json:"members"`
	RepositoryBindings    []RepositoryBinding `json:"repository_bindings"`
	PendingEffects        []PendingEffect     `json:"pending_effects,omitempty"`
	MutationReceipts      []MutationReceipt   `json:"mutation_receipts,omitempty"`
	CreateIdempotencyKey  string              `json:"create_idempotency_key,omitempty"`
	CreateRequestHash     string              `json:"create_request_hash,omitempty"`
}

type PendingEffect struct {
	EffectID     string      `json:"effect_id"`
	Kind         string      `json:"kind"`
	SubjectKind  string      `json:"subject_kind,omitempty"`
	SubjectID    string      `json:"subject_id,omitempty"`
	Relation     string      `json:"relation,omitempty"`
	ConnectionID string      `json:"connection_id,omitempty"`
	Audit        *AuditEvent `json:"audit,omitempty"`
}

type MutationReceipt struct {
	IdempotencyKey          string             `json:"idempotency_key"`
	Operation               string             `json:"operation"`
	RequestHash             string             `json:"request_hash"`
	Member                  *Member            `json:"member,omitempty"`
	Repository              *RepositoryBinding `json:"repository,omitempty"`
	VerificationFailureCode string             `json:"verification_failure_code,omitempty"`
	VerificationFail        string             `json:"verification_failure,omitempty"`
}

type Member struct {
	MemberID    string    `json:"member_id"`
	SubjectKind string    `json:"subject_kind"`
	SubjectID   string    `json:"subject_id"`
	Role        string    `json:"role"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Version     int64     `json:"version"`
}

type RepositoryBinding struct {
	BindingID           string    `json:"binding_id"`
	ConnectionID        string    `json:"connection_id"`
	RepositoryLocator   string    `json:"repository_locator"`
	DisplayName         string    `json:"display_name"`
	Role                string    `json:"role"`
	AccessMode          string    `json:"access_mode"`
	DefaultBranch       string    `json:"default_branch"`
	SourceSubdirectory  string    `json:"source_subdirectory,omitempty"`
	BuildCommands       []string  `json:"build_commands,omitempty"`
	TestCommands        []string  `json:"test_commands,omitempty"`
	BranchPrefix        string    `json:"branch_prefix"`
	Status              string    `json:"status"`
	LastFetchedCommit   string    `json:"last_fetched_commit,omitempty"`
	LastVerifiedAt      time.Time `json:"last_verified_at,omitempty"`
	SourceKind          string    `json:"source_kind,omitempty"`
	WorkspaceWritable   bool      `json:"workspace_writable"`
	SupportsPush        bool      `json:"supports_push"`
	SupportsPullRequest bool      `json:"supports_pull_request"`
	SupportsMerge       bool      `json:"supports_merge"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
	Version             int64     `json:"version"`
}

type ExecutionPolicyDTO struct {
	ApprovalRequired       bool     `json:"approval_required"`
	NetworkMode            string   `json:"network_mode"`
	AllowedHosts           []string `json:"allowed_hosts,omitempty"`
	AllowedCommandPrefixes []string `json:"allowed_command_prefixes,omitempty"`
	AllowedFileScopes      []string `json:"allowed_file_scopes,omitempty"`
	AllowedSecretCount     int      `json:"allowed_secret_count"`
}

type Capabilities struct {
	CanView             bool `json:"can_view"`
	CanMonitor          bool `json:"can_monitor"`
	CanUpdate           bool `json:"can_update"`
	CanConfigure        bool `json:"can_configure"`
	CanManageMembers    bool `json:"can_manage_members"`
	CanBindRepository   bool `json:"can_bind_repository"`
	CanRequestExecution bool `json:"can_request_execution"`
	CanSubmit           bool `json:"can_submit"`
	CanApprove          bool `json:"can_approve"`
	CanMerge            bool `json:"can_merge"`
	CanArchive          bool `json:"can_archive"`
}

type ExecutionSummary struct {
	TodoID                   string    `json:"todo_id"`
	TodoTitle                string    `json:"todo_title"`
	TodoStatus               string    `json:"todo_status"`
	AutomationMode           string    `json:"automation_mode"`
	AutomationStatus         string    `json:"automation_status,omitempty"`
	AssigneeID               string    `json:"assignee_id,omitempty"`
	AssigneeName             string    `json:"assignee_name,omitempty"`
	MissionID                string    `json:"mission_id,omitempty"`
	ExecutionAttempt         int       `json:"execution_attempt,omitempty"`
	MissionStatus            string    `json:"mission_status,omitempty"`
	CurrentPhase             string    `json:"current_phase,omitempty"`
	DeliveryRate             float64   `json:"delivery_rate"`
	TotalTaskCount           int       `json:"total_task_count"`
	ActiveTaskCount          int       `json:"active_task_count"`
	DoneTaskCount            int       `json:"done_task_count"`
	BlockedTaskCount         int       `json:"blocked_task_count"`
	PendingApprovalCount     int       `json:"pending_approval_count"`
	CurrentAgentIDs          []string  `json:"current_agent_ids"`
	LastActivityAt           time.Time `json:"last_activity_at,omitempty"`
	MissionProjectionVersion int       `json:"mission_projection_version,omitempty"`
	CanOpenControl           bool      `json:"can_open_control"`
	CanOpenDiscussion        bool      `json:"can_open_discussion"`
	ConversationID           string    `json:"conversation_id,omitempty"`
}

type ExecutionPage struct {
	Items []ExecutionSummary `json:"items"`
	Page  PageDTO            `json:"page"`
}

type ListExecutionsQuery struct {
	TodoID          string
	TodoStatus      string
	MissionStatus   string
	AssigneeID      string
	Blocked         *bool
	PendingApproval *bool
	Limit           int
	Offset          int
}

type ProjectDTO struct {
	DevelopmentProjectID     string             `json:"development_project_id"`
	ExecutionRealtimeChannel string             `json:"execution_realtime_channel"`
	OrganizationID           string             `json:"organization_id,omitempty"`
	Name                     string             `json:"name"`
	Description              string             `json:"description,omitempty"`
	Status                   string             `json:"status"`
	DefaultAutomationMode    string             `json:"default_automation_mode"`
	MaxConcurrentRuns        int                `json:"max_concurrent_runs"`
	ExecutionPolicy          ExecutionPolicyDTO `json:"execution_policy"`
	CreatedBy                string             `json:"created_by"`
	CreatedAt                time.Time          `json:"created_at"`
	UpdatedAt                time.Time          `json:"updated_at"`
	Version                  int64              `json:"version"`
	MemberCount              int                `json:"member_count"`
	RepositoryCount          int                `json:"repository_count"`
	Capabilities             Capabilities       `json:"capabilities"`
}

type PageDTO struct {
	Limit    int  `json:"limit"`
	Offset   int  `json:"offset"`
	Total    int  `json:"total"`
	Returned int  `json:"returned"`
	HasMore  bool `json:"has_more"`
}

type ProjectPage struct {
	Items []ProjectDTO `json:"items"`
	Page  PageDTO      `json:"page"`
}

type ListQuery struct {
	OrganizationID string
	Status         string
	Limit          int
	Offset         int
}

type CreateCommand struct {
	IdempotencyKey        string          `json:"-"`
	DevelopmentProjectID  string          `json:"development_project_id,omitempty"`
	OrganizationID        string          `json:"organization_id,omitempty"`
	Name                  string          `json:"name"`
	Description           string          `json:"description,omitempty"`
	DefaultAutomationMode string          `json:"default_automation_mode"`
	MaxConcurrentRuns     int             `json:"max_concurrent_runs"`
	ExecutionPolicy       ExecutionPolicy `json:"execution_policy"`
}

type UpdateCommand struct {
	IdempotencyKey        string
	DevelopmentProjectID  string
	ExpectedVersion       int64
	Name                  *string
	Description           *string
	DefaultAutomationMode *string
	MaxConcurrentRuns     *int
	ExecutionPolicy       *ExecutionPolicyPatch
}

type ExecutionPolicyPatch struct {
	ApprovalRequired       *bool     `json:"approval_required"`
	NetworkMode            *string   `json:"network_mode"`
	AllowedHosts           *[]string `json:"allowed_hosts"`
	AllowedCommandPrefixes *[]string `json:"allowed_command_prefixes"`
	AllowedFileScopes      *[]string `json:"allowed_file_scopes"`
	AllowedSecretRefs      *[]string `json:"allowed_secret_refs"`
}

type ArchiveCommand struct {
	IdempotencyKey       string
	DevelopmentProjectID string
	ExpectedVersion      int64
}

type AddMemberCommand struct {
	IdempotencyKey         string
	DevelopmentProjectID   string
	ExpectedProjectVersion int64
	MemberID               string
	SubjectKind            string `json:"subject_kind"`
	SubjectID              string `json:"subject_id"`
	Role                   string `json:"role"`
}

type UpdateMemberCommand struct {
	IdempotencyKey         string
	DevelopmentProjectID   string
	MemberID               string
	ExpectedProjectVersion int64
	ExpectedMemberVersion  int64
	Role                   string `json:"role"`
}

type RemoveMemberCommand struct {
	IdempotencyKey         string
	DevelopmentProjectID   string
	MemberID               string
	ExpectedProjectVersion int64
	ExpectedMemberVersion  int64
}

type BindRepositoryCommand struct {
	IdempotencyKey         string `json:"-"`
	DevelopmentProjectID   string
	ExpectedProjectVersion int64
	BindingID              string
	ConnectionID           string   `json:"connection_id"`
	RepositoryLocator      string   `json:"repository_locator"`
	Role                   string   `json:"role"`
	AccessMode             string   `json:"access_mode"`
	DefaultBranch          string   `json:"default_branch,omitempty"`
	SourceSubdirectory     string   `json:"source_subdirectory,omitempty"`
	BuildCommands          []string `json:"build_commands,omitempty"`
	TestCommands           []string `json:"test_commands,omitempty"`
	BranchPrefix           string   `json:"branch_prefix,omitempty"`
}

type UpdateRepositoryCommand struct {
	IdempotencyKey         string
	DevelopmentProjectID   string
	BindingID              string
	ExpectedProjectVersion int64
	ExpectedBindingVersion int64
	SourceSubdirectory     *string
	BuildCommands          *[]string
	TestCommands           *[]string
	BranchPrefix           *string
}

type RemoveRepositoryCommand struct {
	IdempotencyKey         string
	DevelopmentProjectID   string
	BindingID              string
	ExpectedProjectVersion int64
	ExpectedBindingVersion int64
}

type VerifyRepositoryCommand struct {
	IdempotencyKey         string
	DevelopmentProjectID   string
	BindingID              string
	ExpectedProjectVersion int64
	ExpectedBindingVersion int64
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
