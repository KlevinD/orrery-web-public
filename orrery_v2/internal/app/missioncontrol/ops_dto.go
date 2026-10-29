package missioncontrol

import "time"

type MissionOpsViewDTO struct {
	MissionID       string              `json:"mission_id"`
	Title           string              `json:"title"`
	Status          string              `json:"status"`
	Phase           string              `json:"phase"`
	CurrentFocus    []OpsFocusDTO       `json:"current_focus"`
	AgentLanes      []AgentLaneDTO      `json:"agent_lanes"`
	RuntimeSessions []RuntimeSessionDTO `json:"runtime_sessions"`
	ActionCards     []ActionCardDTO     `json:"action_cards"`
	EvidenceGates   []EvidenceGateDTO   `json:"evidence_gates"`
	ApprovalGates   []ApprovalGateDTO   `json:"approval_gates"`
	AutonomyPolicy  AutonomyPolicyDTO   `json:"autonomy_policy"`
	UpdatedAt       time.Time           `json:"updated_at"`
	Version         int                 `json:"version"`
}

type OpsFocusDTO struct {
	FocusID    string    `json:"focus_id"`
	Kind       string    `json:"kind"`
	Priority   string    `json:"priority"`
	Title      string    `json:"title"`
	Summary    string    `json:"summary,omitempty"`
	MissionID  string    `json:"mission_id"`
	TaskID     string    `json:"task_id,omitempty"`
	AgentID    string    `json:"agent_id,omitempty"`
	SourceType string    `json:"source_type,omitempty"`
	SourceID   string    `json:"source_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type AgentLaneDTO struct {
	AgentID          string              `json:"agent_id"`
	AgentProfileID   string              `json:"agent_profile_id,omitempty"`
	AgentInstanceID  string              `json:"agent_instance_id,omitempty"`
	AgentKind        string              `json:"agent_kind,omitempty"`
	DisplayName      string              `json:"display_name"`
	MissionRole      string              `json:"mission_role,omitempty"`
	Status           string              `json:"status"`
	ActiveTaskIDs    []string            `json:"active_task_ids"`
	WaitingTaskIDs   []string            `json:"waiting_task_ids"`
	InboxOpenCount   int                 `json:"inbox_open_count"`
	RuntimeSessions  []RuntimeSessionDTO `json:"runtime_sessions"`
	ResourceBindings []ResourceRefDTO    `json:"resource_bindings"`
	NextActions      []CommandLinkDTO    `json:"next_actions"`
}

type RuntimeSessionDTO struct {
	RunID                         string    `json:"run_id"`
	AgentID                       string    `json:"agent_id"`
	TaskID                        string    `json:"task_id,omitempty"`
	RuntimeKind                   string    `json:"runtime_kind,omitempty"`
	QueueID                       string    `json:"queue_id,omitempty"`
	Status                        string    `json:"status"`
	LastEvent                     string    `json:"last_event,omitempty"`
	ErrorSummary                  string    `json:"error_summary,omitempty"`
	LastHeartbeatAt               time.Time `json:"last_heartbeat_at,omitempty"`
	LeaseExpiresAt                time.Time `json:"lease_expires_at,omitempty"`
	IsAlive                       bool      `json:"is_alive"`
	IsStale                       bool      `json:"is_stale"`
	LaneHealth                    string    `json:"lane_health,omitempty"`
	StartedAt                     time.Time `json:"started_at,omitempty"`
	UpdatedAt                     time.Time `json:"updated_at,omitempty"`
	ResponsibilityContextID       string    `json:"responsibility_context_id,omitempty"`
	ResponsibilityID              string    `json:"responsibility_id,omitempty"`
	ResponsibilityActorMemberID   string    `json:"responsibility_actor_member_id,omitempty"`
	ResponsibilityVersion         int64     `json:"responsibility_version,omitempty"`
}

type ResourceRefDTO struct {
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Alias        string `json:"alias,omitempty"`
	Source       string `json:"source,omitempty"`
}

type ActionCardDTO struct {
	ActionID   string           `json:"action_id"`
	Kind       string           `json:"kind"`
	Priority   string           `json:"priority"`
	Title      string           `json:"title"`
	Summary    string           `json:"summary,omitempty"`
	MissionID  string           `json:"mission_id"`
	TaskID     string           `json:"task_id,omitempty"`
	AgentID    string           `json:"agent_id,omitempty"`
	SourceType string           `json:"source_type,omitempty"`
	SourceID   string           `json:"source_id,omitempty"`
	Status     string           `json:"status"`
	Commands   []CommandLinkDTO `json:"commands"`
	CreatedAt  time.Time        `json:"created_at"`
}

type CommandLinkDTO struct {
	ActionID           string `json:"action_id"`
	Label              string `json:"label"`
	Method             string `json:"method"`
	Path               string `json:"path"`
	RequiredPermission string `json:"required_permission,omitempty"`
}

type EvidenceGateDTO struct {
	TaskID                string    `json:"task_id"`
	Status                string    `json:"status"`
	RequiredCount         int       `json:"required_count"`
	SubmittedCount        int       `json:"submitted_count"`
	MissingRequirementIDs []string  `json:"missing_requirement_ids"`
	SubmittedEvidenceIDs  []string  `json:"submitted_evidence_ids"`
	LastSubmittedAt       time.Time `json:"last_submitted_at,omitempty"`
}

type ApprovalGateDTO struct {
	ApprovalID   string           `json:"approval_id"`
	ApprovalType string           `json:"approval_type"`
	Status       string           `json:"status"`
	Priority     string           `json:"priority"`
	MissionID    string           `json:"mission_id"`
	TaskID       string           `json:"task_id,omitempty"`
	SourceType   string           `json:"source_type,omitempty"`
	SourceID     string           `json:"source_id,omitempty"`
	Title        string           `json:"title"`
	Summary      string           `json:"summary,omitempty"`
	RequestedBy  string           `json:"requested_by,omitempty"`
	AssignedTo   string           `json:"assigned_to,omitempty"`
	EvidenceRefs []string         `json:"evidence_refs,omitempty"`
	Commands     []CommandLinkDTO `json:"commands"`
	CreatedAt    time.Time        `json:"created_at"`
	DecidedAt    time.Time        `json:"decided_at,omitempty"`
}

type AutonomyPolicyDTO struct {
	Mode                   string   `json:"mode"`
	AutoStartAllowed       bool     `json:"auto_start_allowed"`
	MaxRiskWithoutApproval string   `json:"max_risk_without_approval"`
	RequiredApprovals      []string `json:"required_approvals"`
}
