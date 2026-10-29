package directory

import (
	"context"
	"errors"
	"time"
)

const (
	HostingModeManaged    = "orrery_managed"
	HostingModeOwnedAgent = "owned_agent"

	// HostingModeCloud and HostingModeLocal are the product-level names. The
	// persisted values remain compatible with existing desktop installations.
	HostingModeCloud = HostingModeManaged
	HostingModeLocal = HostingModeOwnedAgent
)

const (
	// A typed local executor is a tool target, not a whole-Agent host.
	RuntimeKindOrreryTools = "orrery_tools"
	RuntimeKindOpenClaw    = "openclaw"
	RuntimeKindHermes      = "hermes"
	RuntimeKindCodex       = "codex"
	RuntimeKindPI          = "pi"
	RuntimeKindAionRS      = "aionrs"
	RuntimeKindACP         = "acp"
	RuntimeKindOther       = "other"
)

const (
	AgentStatusOnline      = "online"
	AgentStatusOffline     = "offline"
	AgentStatusUnavailable = "unavailable"
	AgentStatusRevoked     = "revoked"
)

var (
	ErrInvalidRequest = errors.New("owned agent directory request is invalid")
	ErrNotFound       = errors.New("owned agent was not found")
	ErrConflict       = errors.New("personal assistant configuration revision conflicts")
	ErrDependency     = errors.New("owned agent directory dependency is unavailable")
)

type OwnerScope struct {
	TenantID string
	OwnerID  string
}

type AgentProfileDescriptor struct {
	ProfileID       string                     `json:"profile_id"`
	DisplayName     string                     `json:"display_name"`
	RuntimeKind     string                     `json:"runtime_kind"`
	Capabilities    []string                   `json:"capabilities"`
	Available       bool                       `json:"available"`
	WorkspaceGrants []WorkspaceGrantDescriptor `json:"workspace_grants"`
}

type WorkspaceGrantDescriptor struct {
	GrantID      string   `json:"grant_id"`
	DisplayName  string   `json:"display_name"`
	Capabilities []string `json:"capabilities"`
	Available    bool     `json:"available"`
}

type PublishCatalogInput struct {
	CatalogDigest string                   `json:"catalog_digest"`
	ObservedAt    time.Time                `json:"observed_at"`
	Profiles      []AgentProfileDescriptor `json:"profiles"`
}

type OwnedAgent struct {
	TenantID        string
	OwnerID         string
	OwnedAgentID    string
	DeviceID        string
	DeviceName      string
	LocalProfileID  string
	DisplayName     string
	RuntimeKind     string
	Capabilities    []string
	WorkspaceGrants []WorkspaceGrantDescriptor
	Available       bool
	CatalogDigest   string
	ObservedAt      time.Time
	UpdatedAt       time.Time
	RevokedAt       time.Time
}

type OwnedAgentView struct {
	OwnedAgentID    string                     `json:"owned_agent_id"`
	DeviceID        string                     `json:"device_id"`
	DeviceName      string                     `json:"device_name"`
	DisplayName     string                     `json:"display_name"`
	RuntimeKind     string                     `json:"runtime_kind"`
	Capabilities    []string                   `json:"capabilities"`
	WorkspaceGrants []WorkspaceGrantDescriptor `json:"workspace_grants"`
	Status          string                     `json:"status"`
	LastSeenAt      string                     `json:"last_seen_at"`
}

type HostingRecord struct {
	TenantID          string
	OwnerID           string
	AssistantEnabled  bool
	AssignedAgentID   string
	HostingMode       string
	OwnedAgentID      string
	WorkspaceGrantID  string
	FallbackToManaged bool
	Revision          int64
	UpdatedAt         time.Time
}

type PersonalAssistantView struct {
	AssistantEnabled  bool             `json:"assistant_enabled"`
	AgentProfileID    string           `json:"agent_profile_id,omitempty"`
	DisplayName       string           `json:"display_name,omitempty"`
	SystemPrompt      string           `json:"system_prompt,omitempty"`
	AssignedAgentID   string           `json:"assigned_agent_id,omitempty"`
	HostingMode       string           `json:"hosting_mode"`
	OwnedAgentID      string           `json:"owned_agent_id,omitempty"`
	WorkspaceGrantID  string           `json:"workspace_grant_id,omitempty"`
	FallbackToManaged bool             `json:"fallback_to_managed"`
	Revision          int64            `json:"revision"`
	UpdatedAt         string           `json:"updated_at,omitempty"`
	AvatarURL         string           `json:"avatar_url,omitempty"`
	AvatarETag        string           `json:"avatar_etag,omitempty"`
	ExecutionIdentity string           `json:"execution_identity"`
	CloudFallback     bool             `json:"cloud_fallback"`
	OwnedAgents       []OwnedAgentView `json:"owned_agents"`
}

type UpdatePersonalAssistantInput struct {
	AssistantEnabled  bool   `json:"assistant_enabled"`
	AssignedAgentID   string `json:"assigned_agent_id,omitempty"`
	HostingMode       string `json:"hosting_mode"`
	OwnedAgentID      string `json:"owned_agent_id,omitempty"`
	WorkspaceGrantID  string `json:"workspace_grant_id,omitempty"`
	FallbackToManaged bool   `json:"fallback_to_managed"`
	Revision          int64  `json:"revision"`
}

type PersonalAssistantNotificationPreferences struct {
	ApprovalNotificationsEnabled bool   `json:"approval_notifications_enabled"`
	Channel                      string `json:"channel"`
	Revision                     int64  `json:"revision"`
	UpdatedAt                    string `json:"updated_at,omitempty"`
}

type PersonalAssistantNotificationRecord struct {
	TenantID                     string
	OwnerID                      string
	ApprovalNotificationsEnabled bool
	Revision                     int64
	UpdatedAt                    time.Time
}

type UpdatePersonalAssistantNotificationsInput struct {
	ApprovalNotificationsEnabled bool  `json:"approval_notifications_enabled"`
	Revision                     int64 `json:"revision"`
}

type PersonalAssistantAvatarInfo struct {
	TenantID    string
	OwnerID     string
	ContentType string
	Size        int64
	ETag        string
	UpdatedAt   time.Time
}

type PersonalAssistantAvatar struct {
	Info    PersonalAssistantAvatarInfo
	Content []byte
}

type PersonalAssistantAvatarStore interface {
	LoadPersonalAssistantAvatarInfo(context.Context, OwnerScope) (PersonalAssistantAvatarInfo, error)
	LoadPersonalAssistantAvatar(context.Context, OwnerScope) (PersonalAssistantAvatar, error)
	SavePersonalAssistantAvatar(context.Context, OwnerScope, string, []byte, string, time.Time) (PersonalAssistantAvatarInfo, error)
	DeletePersonalAssistantAvatar(context.Context, OwnerScope) error
}

type PersonalAssistantNotificationStore interface {
	LoadPersonalAssistantNotifications(context.Context, OwnerScope) (PersonalAssistantNotificationRecord, bool, error)
	SavePersonalAssistantNotifications(context.Context, PersonalAssistantNotificationRecord, int64) (PersonalAssistantNotificationRecord, error)
}

type Store interface {
	ReplaceOwnedAgents(context.Context, OwnerScope, string, string, time.Time, []OwnedAgent, time.Time) error
	ListOwnedAgents(context.Context, OwnerScope) ([]OwnedAgent, error)
	LoadHosting(context.Context, OwnerScope) (HostingRecord, bool, error)
	SaveHosting(context.Context, HostingRecord, int64) (HostingRecord, error)
}
