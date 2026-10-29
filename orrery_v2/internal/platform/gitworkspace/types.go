package gitworkspace

import (
	"errors"
	"io"
	"time"
)

const (
	SourceManagedLocalReadOnly = "managed_local_source"
	SourceManagedBare          = "managed_bare_authoritative"

	MergeStrategySquash      = "squash"
	MergeStrategyMergeCommit = "merge_commit"
)

var (
	ErrInvalidRequest      = errors.New("git workspace request is invalid")
	ErrGitUnavailable      = errors.New("git executable is unavailable")
	ErrRepository          = errors.New("git repository operation failed")
	ErrWorkspaceConflict   = errors.New("git workspace conflicts with its persisted identity")
	ErrWorkspaceDirty      = errors.New("git workspace contains uncommitted changes outside the approved commit")
	ErrPolicyViolation     = errors.New("git workspace policy violation")
	ErrSigningRequired     = errors.New("git commit signing is required but unavailable")
	ErrMergeConflict       = errors.New("git merge preview has conflicts")
	ErrTargetMoved         = errors.New("git merge target changed")
	ErrVerificationFailed  = errors.New("git result verification failed")
	ErrAuthoritativeSource = errors.New("git source is not authoritative for merge")
)

type Config struct {
	Root             string
	GitPath          string
	CommitterName    string
	CommitterEmail   string
	SigningKey       string
	SigningFormat    string
	RequireSigning   bool
	MaxFileBytes     int64
	CommandTimeout   time.Duration
	FetchLockTimeout time.Duration
}

type ProvisionRequest struct {
	TenantID             string
	DevelopmentProjectID string
	TodoID               string
	ExecutionAttemptID   string
	RepositoryBindingID  string
	SourcePath           string
	SourceKind           string
	DefaultBranch        string
	BranchPrefix         string
	BranchSlug           string
	BaseCommit           string
}

type Workspace struct {
	StorageKey   string
	BranchName   string
	BaseCommit   string
	HeadCommit   string
	WorktreePath string
	MirrorPath   string
}

type CheckResult struct {
	Command   []string
	ExitCode  int
	Succeeded bool
	Summary   string
	StartedAt time.Time
	EndedAt   time.Time
}

type CommitRequest struct {
	ProvisionRequest
	PlanID             string
	PlanDigest         string
	ExecutionRunID     string
	AgentID            string
	AuthorName         string
	AuthorEmail        string
	Summary            string
	AllowedFileScopes  []string
	SourceSubdirectory string
	CheckCommands      []string
}

type CommitResult struct {
	CommitSHA       string
	ParentSHAs      []string
	TreeSHA         string
	Message         string
	SignatureStatus string
	ChangedFiles    []string
	Additions       int
	Deletions       int
	DiffDigest      string
	Checks          []CheckResult
	HeadBefore      string
	CreatedAt       time.Time
}

type SubmissionSnapshotRequest struct {
	ProvisionRequest
	HeadCommit string
}

type SubmissionSnapshot struct {
	BaseCommit   string
	HeadCommit   string
	TreeSHA      string
	CommitSHAs   []string
	ChangedFiles []string
	Additions    int
	Deletions    int
	DiffDigest   string
}

type MergePreviewRequest struct {
	ProvisionRequest
	HeadCommit         string
	ExpectedBaseHead   string
	MergeStrategy      string
	SourceSubdirectory string
	CheckCommands      []string
}

type MergePreview struct {
	ExpectedBaseHead string
	HeadCommit       string
	TreeSHA          string
	MergeStrategy    string
	Checks           []CheckResult
}

type MergeRequest struct {
	ProvisionRequest
	HeadCommit       string
	ExpectedBaseHead string
	ExpectedTreeSHA  string
	MergeStrategy    string
	Message          string
	ActorName        string
	ActorEmail       string
}

type MergeReceipt struct {
	MergeCommitSHA string
	TreeSHA        string
	TargetHead     string
	MergedAt       time.Time
}

type ExportResult struct {
	PatchPath  string
	BundlePath string
}

type ExportFile struct {
	Name        string
	ContentType string
	Size        int64
	Reader      io.ReadCloser
}
