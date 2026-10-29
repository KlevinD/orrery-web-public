package notification

import (
	"context"
	"errors"
	"io"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/gateway/outbound"
	"orrery_v2/internal/platform/db"
)

var (
	ErrForbidden = errors.New("notification forbidden")
	ErrNotFound  = errors.New("notification not found")
	ErrInvalid   = errors.New("invalid notification request")
	ErrConflict  = errors.New("notification configuration changed")
)

type Viewer struct {
	TenantID, HumanID string
	Manage, System    bool
}
type Source struct {
	ID              string        `json:"id"`
	TenantID        string        `json:"tenant_id"`
	OriginTenant    string        `json:"origin_tenant"`
	TaskID          string        `json:"task_id"`
	Title           string        `json:"title"`
	AgentID         string        `json:"agent_id"`
	Enabled         bool          `json:"enabled"`
	Audience        string        `json:"audience"`
	People          []string      `json:"people"`
	IncludeFailures bool          `json:"include_failures"`
	Revision        int64         `json:"revision"`
	ActivatedAt     time.Time     `json:"activated_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
	Subscription    *Subscription `json:"subscription,omitempty"`
}
type Subscription struct {
	Enabled         bool      `json:"enabled"`
	Channels        []string  `json:"channels"`
	IncludeFailures bool      `json:"include_failures"`
	Since           time.Time `json:"since"`
}
type Preferences struct {
	Channels []string `json:"channels"`
}
type Person struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Channels []string `json:"channels"`
}
type Preview struct {
	People []Person `json:"people"`
	Total  int      `json:"total"`
}
type Artifact struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size_bytes"`
}
type Event struct {
	ID           string                 `json:"id"`
	SourceID     string                 `json:"source_id"`
	TaskID       string                 `json:"task_id"`
	RunID        string                 `json:"run_id"`
	TenantID     string                 `json:"tenant_id"`
	OriginTenant string                 `json:"origin_tenant,omitempty"`
	Title        string                 `json:"title"`
	AgentID      string                 `json:"agent_id"`
	Status       string                 `json:"status"`
	Summary      string                 `json:"summary"`
	AgentReport  *scheduler.AgentReport `json:"agent_report,omitempty"`
	Artifacts    []Artifact             `json:"artifacts"`
	CreatedAt    time.Time              `json:"created_at"`
}
type Push struct {
	Channel string `json:"channel"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
}
type Item struct {
	ID     string `json:"id"`
	Event  Event  `json:"event"`
	Read   bool   `json:"read"`
	Reason string `json:"reason"`
	Pushes []Push `json:"pushes"`
}
type Page struct {
	Items  []Item `json:"items"`
	Total  int    `json:"total"`
	Unread int    `json:"unread"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}
type Receipt struct {
	HumanID string `json:"human_id"`
	Name    string `json:"name"`
	Read    bool   `json:"read"`
	Pushes  []Push `json:"pushes"`
}
type Scheduler interface {
	GetTask(context.Context, string) (scheduler.ScheduledTask, error)
	GetRun(context.Context, string) (scheduler.ScheduledRun, error)
	ListTasks(context.Context, bool) ([]scheduler.ScheduledTask, error)
	ListNotificationRuns(context.Context, string, string, time.Time) ([]scheduler.ScheduledRun, error)
}

// ReportReader reads a verified, persisted report for the supplied original
// execution. It must never generate text, resume a session or execute work.
type ReportReader interface {
	Report(context.Context, scheduler.ScheduledRun) (scheduler.AgentReport, bool, error)
}
type Profiles interface {
	GetProfile(context.Context, agentcore.AgentProfileID) (agentcore.AgentProfile, error)
	QueryDirectory(context.Context, agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error)
}
type Gateway interface {
	Enqueue(context.Context, outbound.EnqueueCommand) ([]outbound.Delivery, error)
	Get(context.Context, string, string) (outbound.Delivery, error)
}
type ArtifactReader interface {
	OpenRunArtifact(context.Context, scheduler.OpenRunArtifactQuery) (scheduler.RunArtifactContent, error)
}
type BindingReader interface {
	Channels(context.Context, string, string) ([]string, error)
}
type Service struct {
	store               *db.Store
	scheduler           Scheduler
	profiles            Profiles
	gateway             Gateway
	artifacts           ArtifactReader
	bindings            BindingReader
	now                 func() time.Time
	publicOrigin        string
	externalReportRelay func(Event, string, string) bool
	reports             ReportReader
}

func New(store *db.Store, tasks Scheduler, profiles Profiles, gateway Gateway, artifacts ArtifactReader, bindings BindingReader, origin string) *Service {
	return &Service{store: store, scheduler: tasks, profiles: profiles, gateway: gateway, artifacts: artifacts, bindings: bindings, now: func() time.Time { return time.Now().UTC() }, publicOrigin: origin}
}

// SetClock configures the host clock before background reconciliation starts.
func (s *Service) SetClock(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

type Content struct {
	Reader            io.ReadCloser
	Name, ContentType string
	Size              int64
}
