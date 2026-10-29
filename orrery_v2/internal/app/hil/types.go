package hil

import "time"

type DecisionMode string

const (
	ModeAuto     DecisionMode = "auto"
	ModeDeny     DecisionMode = "deny"
	ModeHIL      DecisionMode = "hil"
	ModeApproval DecisionMode = "approval"
	ModeCouncil  DecisionMode = "council"
)

type DecisionEffect string

const (
	EffectNone             DecisionEffect = ""
	EffectAuthorize        DecisionEffect = "authorize"
	EffectRequiresApproval DecisionEffect = "requires_approval"
	EffectDeny             DecisionEffect = "deny"
)

type RiskLevel string

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

type ActorKind string

const (
	ActorHuman   ActorKind = "human"
	ActorAgent   ActorKind = "agent"
	ActorService ActorKind = "service"
)

type ActorRef struct {
	Kind ActorKind `json:"kind"`
	ID   string    `json:"id"`
}

type ObjectRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type DecisionStatus string

const (
	DecisionPending  DecisionStatus = "pending"
	DecisionResolved DecisionStatus = "resolved"
	DecisionDenied   DecisionStatus = "denied"
)

type OntologyContextSnapshot struct {
	BundleID                string               `json:"bundle_id"`
	SchemaHash              string               `json:"schema_hash,omitempty"`
	Confidence              float64              `json:"confidence,omitempty"`
	Complete                bool                 `json:"complete"`
	EvidenceRefs            []string             `json:"evidence_refs,omitempty"`
	Conflict                bool                 `json:"conflict,omitempty"`
	RecommendedParticipants []CouncilParticipant `json:"recommended_participants,omitempty"`
}

type DecisionRequest struct {
	ID                 string                  `json:"id"`
	TenantID           string                  `json:"tenant_id"`
	Requester          ActorRef                `json:"requester"`
	Action             string                  `json:"action"`
	ActionTypeID       string                  `json:"action_type_id,omitempty"`
	Resource           ObjectRef               `json:"resource"`
	ObjectRefs         []ObjectRef             `json:"object_refs,omitempty"`
	ArgsHash           string                  `json:"args_hash"`
	Purpose            string                  `json:"purpose,omitempty"`
	RiskLevel          RiskLevel               `json:"risk_level"`
	PreferredMode      DecisionMode            `json:"preferred_mode,omitempty"`
	Route              RouteDecision           `json:"route"`
	Context            OntologyContextSnapshot `json:"context"`
	PayloadPreview     map[string]any          `json:"payload_preview,omitempty"`
	IdempotencyKey     string                  `json:"-"`
	RequestFingerprint string                  `json:"-"`
	Status             DecisionStatus          `json:"status"`
	CouncilID          string                  `json:"council_id,omitempty"`
	CreatedAt          time.Time               `json:"created_at"`
	ExpiresAt          time.Time               `json:"expires_at,omitempty"`
}

type DecisionListQuery struct {
	TenantID            string
	AdditionalTenantIDs []string
	DecisionID          string
	Mode                DecisionMode
	Status              DecisionStatus
	RequestedBy         ActorRef
	Mine                bool
	ActionableBy        ActorRef
	Limit               int
	Offset              int
}

type DecisionPage struct {
	Items []DecisionRequest `json:"items"`
	Total int               `json:"total"`
}

type DecisionReceipt struct {
	ID                 string         `json:"id"`
	TenantID           string         `json:"tenant_id"`
	RequestID          string         `json:"request_id"`
	CouncilID          string         `json:"council_id,omitempty"`
	WorkflowInstanceID string         `json:"workflow_instance_id,omitempty"`
	ArgsHash           string         `json:"args_hash"`
	MaterialHash       string         `json:"material_hash,omitempty"`
	Effect             DecisionEffect `json:"effect"`
	ResolverActorKind  ActorKind      `json:"resolver_actor_kind,omitempty"`
	ResolverActorID    string         `json:"resolver_actor_id,omitempty"`
	ResolverReason     string         `json:"resolver_reason,omitempty"`
	ResolverSummary    map[string]any `json:"resolver_summary,omitempty"`
	SingleUse          bool           `json:"single_use"`
	CreatedAt          time.Time      `json:"created_at"`
	ExpiresAt          time.Time      `json:"expires_at,omitempty"`
	UsedAt             time.Time      `json:"used_at,omitempty"`
}

// ResolveDecisionInput is the human (or trusted service) response to a
// pending HIL/approval decision. Decision accepts approve/approved/allow or
// reject/rejected/deny/denied for compatibility with existing clients.
type ResolveDecisionInput struct {
	Decision       string `json:"decision"`
	Reason         string `json:"reason,omitempty"`
	IdempotencyKey string `json:"-"`
}

// DecisionResolution is the durable result handed to runtime consumers after
// a decision is committed.
type DecisionResolution struct {
	Decision DecisionRequest `json:"decision"`
	Receipt  DecisionReceipt `json:"receipt"`
	Actor    ActorRef        `json:"actor"`
	Reason   string          `json:"reason,omitempty"`
}

type ResolveDecisionResult = DecisionResolution

// GatewayReceiptConsumeInput binds a retrying tool call to the exact HIL
// request that authorized it. The receipt itself is never accepted from the
// client; it is looked up by the server-side idempotency key.
type GatewayReceiptConsumeInput struct {
	TenantID       string
	IdempotencyKey string
	Actor          ActorRef
	Resource       ObjectRef
	ArgsHash       string
}

// ActionReceiptConsumeInput binds an approved HIL decision to one exact
// governed action. DecisionID and ReceiptID are both server-verified; the
// receipt is consumed only after every immutable action field matches.
type ActionReceiptConsumeInput struct {
	TenantID     string
	DecisionID   string
	ReceiptID    string
	Actor        ActorRef
	Action       string
	ActionTypeID string
	ObjectRefs   []ObjectRef
	ArgsHash     string
	Purpose      string
	RiskLevel    RiskLevel
}

type CouncilStatus string

const (
	CouncilOpen     CouncilStatus = "open"
	CouncilPassed   CouncilStatus = "passed"
	CouncilRejected CouncilStatus = "rejected"
	CouncilExpired  CouncilStatus = "expired"
)

type VoteChoice string

const (
	VoteApprove VoteChoice = "approve"
	VoteReject  VoteChoice = "reject"
	VoteAbstain VoteChoice = "abstain"
)

type CouncilParticipant struct {
	Actor       ActorRef `json:"actor"`
	DisplayName string   `json:"display_name,omitempty"`
	Role        string   `json:"role,omitempty"`
	Required    bool     `json:"required"`
}

type CouncilContribution struct {
	Actor          ActorRef  `json:"actor"`
	Position       string    `json:"position"`
	Reasoning      string    `json:"reasoning"`
	EvidenceRefs   []string  `json:"evidence_refs,omitempty"`
	IdempotencyKey string    `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type CouncilVote struct {
	Actor     ActorRef   `json:"actor"`
	Choice    VoteChoice `json:"choice"`
	Reason    string     `json:"reason,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type CouncilSession struct {
	ID                string                `json:"id"`
	TenantID          string                `json:"tenant_id"`
	RequestID         string                `json:"request_id"`
	ArgsHash          string                `json:"args_hash"`
	RiskLevel         RiskLevel             `json:"risk_level"`
	Status            CouncilStatus         `json:"status"`
	Effect            DecisionEffect        `json:"effect,omitempty"`
	Quorum            int                   `json:"quorum"`
	ApprovalThreshold int                   `json:"approval_threshold"`
	Participants      []CouncilParticipant  `json:"participants"`
	Contributions     []CouncilContribution `json:"contributions,omitempty"`
	Votes             []CouncilVote         `json:"votes,omitempty"`
	Dissent           []ActorRef            `json:"dissent,omitempty"`
	CreatedAt         time.Time             `json:"created_at"`
	UpdatedAt         time.Time             `json:"updated_at"`
	ExpiresAt         time.Time             `json:"expires_at"`
	ResolvedAt        time.Time             `json:"resolved_at,omitempty"`
}

type CouncilResolution struct {
	CouncilID   string         `json:"council_id"`
	RequestID   string         `json:"request_id"`
	ArgsHash    string         `json:"args_hash"`
	Status      CouncilStatus  `json:"status"`
	Effect      DecisionEffect `json:"effect,omitempty"`
	Approvals   int            `json:"approvals"`
	Rejections  int            `json:"rejections"`
	Abstentions int            `json:"abstentions"`
	Dissent     []ActorRef     `json:"dissent,omitempty"`
	ResolvedAt  time.Time      `json:"resolved_at,omitempty"`
}
