package feishu

import (
	"context"
	"errors"
	"strings"
	"time"
)

const (
	ProviderFeishu = "feishu"

	PublishSourceChat = "source_chat"
	PublishOrreryOnly = "orrery_only"
)

var (
	ErrInvalidRequest     = errors.New("invalid meeting automation request")
	ErrAutomationDisabled = errors.New("meeting automation is disabled")
	ErrForbidden          = errors.New("meeting automation is not allowed")
	ErrNotFound           = errors.New("meeting automation resource not found")
	ErrConflict           = errors.New("meeting automation state conflict")
	ErrDependency         = errors.New("meeting automation dependency is unavailable")
)

type MeetingStatus string

const (
	StatusCreating          MeetingStatus = "creating"
	StatusReserved          MeetingStatus = "reserved"
	StatusInProgress        MeetingStatus = "in_progress"
	StatusAwaitingRecording MeetingStatus = "awaiting_recording"
	StatusRecordingReady    MeetingStatus = "recording_ready"
	StatusDownloading       MeetingStatus = "downloading"
	StatusTranscribing      MeetingStatus = "transcribing"
	StatusSummarizing       MeetingStatus = "summarizing"
	StatusDraftReady        MeetingStatus = "draft_ready"
	StatusPublished         MeetingStatus = "published"
	StatusReservationFailed MeetingStatus = "reservation_failed"
	StatusFailedTerminal    MeetingStatus = "failed_terminal"
	StatusDeleted           MeetingStatus = "deleted"
)

type MeetingPolicy struct {
	TenantID               string
	Provider               string
	AccountID              string
	Enabled                bool
	AllowedChatIDs         []string
	AllowedActorIDs        []string
	SummaryAgentID         string
	PublishTarget          string
	RawMediaRetention      time.Duration
	MaxMeetingDuration     time.Duration
	DailyTranscriptionTime time.Duration
	Version                int64
	UpdatedAt              time.Time
}

type MeetingPolicySnapshot struct {
	SummaryAgentID         string
	PublishTarget          string
	RawMediaRetention      time.Duration
	MaxMeetingDuration     time.Duration
	DailyTranscriptionTime time.Duration
	PolicyVersion          int64
}

type MeetingSession struct {
	ID                string
	TenantID          string
	Provider          string
	AccountID         string
	ProviderTenantKey string
	SourceChatID      string
	RequestedBy       string
	OwnerExternalID   string
	Topic             string
	ReservationID     string
	MeetingID         string
	MeetingNo         string
	MeetingURL        string
	MinuteToken       string
	RecordingDuration time.Duration
	Status            MeetingStatus
	Policy            MeetingPolicySnapshot
	SourceContextKind string
	SourceContextID   string
	IdempotencyKey    string
	LastErrorCode     string
	ExpiresAt         time.Time
	StartedAt         time.Time
	EndedAt           time.Time
	RecordingReadyAt  time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
	Version           int64
}

type CreateMeetingRequest struct {
	TenantID          string
	Provider          string
	AccountID         string
	ProviderTenantKey string
	SourceChatID      string
	RequestedBy       string
	OwnerExternalID   string
	Topic             string
	ExpiresAt         time.Time
	SourceContextKind string
	SourceContextID   string
	IdempotencyKey    string
}

type MeetingReservationRequest struct {
	AccountID       string
	OwnerExternalID string
	Topic           string
	ExpiresAt       time.Time
	AutoRecord      bool
}

type MeetingReservation struct {
	ID         string
	MeetingNo  string
	MeetingURL string
}

type ActiveMeeting struct {
	ID        string
	Topic     string
	StartedAt time.Time
}

type MeetingEventType string

const (
	ProviderEventStarted        MeetingEventType = "meeting_started"
	ProviderEventEnded          MeetingEventType = "meeting_ended"
	ProviderEventRecordingReady MeetingEventType = "recording_ready"
)

type MeetingEvent struct {
	Provider          string
	AccountID         string
	ProviderTenantKey string
	EventID           string
	Type              MeetingEventType
	MeetingID         string
	MeetingNo         string
	Topic             string
	MinuteToken       string
	OccurredAt        time.Time
	RecordingDuration time.Duration
}

type MeetingRecordingJob struct {
	ID                string
	TenantID          string
	SessionID         string
	Provider          string
	AccountID         string
	MinuteToken       string
	SummaryAgentID    string
	PublishTarget     string
	SourceChatID      string
	RawMediaRetention time.Duration
}

type MeetingProvider interface {
	CreateReservation(context.Context, MeetingReservationRequest) (MeetingReservation, error)
	LookupActiveMeeting(context.Context, string, string) (ActiveMeeting, bool, error)
}

type MeetingProviderRegistry interface {
	ResolveProvider(provider, accountID string) (MeetingProvider, bool)
}

type MeetingRepositoryStore interface {
	GetPolicy(ctx context.Context, tenantID, provider, accountID string) (MeetingPolicy, bool, error)
	CreateSession(ctx context.Context, session MeetingSession) (MeetingSession, bool, error)
	GetSession(ctx context.Context, tenantID, sessionID string) (MeetingSession, bool, error)
	FindSessionByMeeting(ctx context.Context, provider, accountID, providerTenantKey, meetingID string) (MeetingSession, bool, error)
	FindSessionByMeetingNo(ctx context.Context, provider, accountID, providerTenantKey, meetingNo string) (MeetingSession, bool, error)
	ListUnboundSessions(ctx context.Context, provider, accountID, providerTenantKey string, limit int) ([]MeetingSession, error)
	CompareAndSaveSession(ctx context.Context, session MeetingSession, expectedVersion int64) (bool, error)
}

type MeetingRecordingQueue interface {
	EnqueueRecording(context.Context, MeetingRecordingJob) (bool, error)
}

type MeetingAutomation interface {
	HandleProviderEvent(context.Context, MeetingEvent) (MeetingSession, bool, error)
}

func (p MeetingPolicy) allowsChat(chatID string) bool {
	return containsIdentity(p.AllowedChatIDs, chatID)
}

func (p MeetingPolicy) allowsActor(actorID string) bool {
	return containsIdentity(p.AllowedActorIDs, actorID)
}

func containsIdentity(allowed []string, value string) bool {
	value = strings.TrimSpace(value)
	for _, candidate := range allowed {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || (candidate != "" && candidate == value) {
			return true
		}
	}
	return false
}
