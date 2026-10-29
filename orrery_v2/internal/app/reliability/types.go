package reliability

import "time"

type FailureCategory string

const (
	FailureCategoryTransient     FailureCategory = "transient"
	FailureCategoryConfiguration FailureCategory = "configuration"
	FailureCategoryCode          FailureCategory = "code"
	FailureCategoryPermission    FailureCategory = "permission"
	FailureCategoryData          FailureCategory = "data"
	FailureCategorySemantic      FailureCategory = "semantic"
	FailureCategoryUnknown       FailureCategory = "unknown"
)

type FailureSeverity string

const (
	FailureSeverityWarning  FailureSeverity = "warning"
	FailureSeverityError    FailureSeverity = "error"
	FailureSeverityCritical FailureSeverity = "critical"
)

type IncidentStatus string

const (
	IncidentStatusOpen        IncidentStatus = "open"
	IncidentStatusContained   IncidentStatus = "contained"
	IncidentStatusDiagnosed   IncidentStatus = "diagnosed"
	IncidentStatusRepairing   IncidentStatus = "repairing"
	IncidentStatusMonitoring  IncidentStatus = "monitoring"
	IncidentStatusResolved    IncidentStatus = "resolved"
	IncidentStatusEscalated   IncidentStatus = "escalated"
	IncidentStatusQuarantined IncidentStatus = "quarantined"
)

type DiagnosisCaseStatus string

const (
	DiagnosisCaseStatusProposed  DiagnosisCaseStatus = "proposed"
	DiagnosisCaseStatusConfirmed DiagnosisCaseStatus = "confirmed"
	DiagnosisCaseStatusRejected  DiagnosisCaseStatus = "rejected"
)

type DiagnosisDecision string

const (
	DiagnosisDecisionConfirm DiagnosisDecision = "confirm"
	DiagnosisDecisionReject  DiagnosisDecision = "reject"
)

type RepairStrategy string

const (
	RepairStrategyRetry            RepairStrategy = "retry"
	RepairStrategyRestart          RepairStrategy = "restart"
	RepairStrategyFallback         RepairStrategy = "fallback"
	RepairStrategyReconfigure      RepairStrategy = "reconfigure"
	RepairStrategyPatch            RepairStrategy = "patch"
	RepairStrategyPermissionChange RepairStrategy = "permission_change"
	RepairStrategyDataMutation     RepairStrategy = "data_mutation"
)

type RepairRuleStatus string

const (
	RepairRuleStatusProposed    RepairRuleStatus = "proposed"
	RepairRuleStatusActive      RepairRuleStatus = "active"
	RepairRuleStatusQuarantined RepairRuleStatus = "quarantined"
	RepairRuleStatusRetired     RepairRuleStatus = "retired"
)

type RiskLevel string

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

type ActorRef struct {
	Kind string
	ID   string
}

type RepairTargetKind string

const (
	RepairTargetMissionAgentRun RepairTargetKind = "mission_agent_run"
)

// RepairTarget is the immutable operational object captured with one failure
// observation. AuthorizationTenantID deliberately remains separate from the
// Ontology-scoped tenant used by the incident.
type RepairTarget struct {
	TenantID              string
	ObservationID         string
	IncidentID            string
	Kind                  RepairTargetKind
	AuthorizationTenantID string
	MissionID             string
	RunID                 string
	OccurredAt            time.Time
}

type FailureObservation struct {
	ID            string
	TenantID      string
	Source        string
	Component     string
	ErrorCode     string
	Message       string
	Category      FailureCategory
	Severity      FailureSeverity
	Retryable     *bool
	AgentRunID    string
	ActionRunID   string
	EvidenceID    string
	CorrelationID string
	CausationID   string
	Actor         ActorRef
	OccurredAt    time.Time
	RepairTarget  *RepairTarget
}

type FailureIncident struct {
	ID              string
	TenantID        string
	Fingerprint     string
	Source          string
	Component       string
	Category        FailureCategory
	Severity        FailureSeverity
	Status          IncidentStatus
	ErrorCode       string
	RedactedSummary string
	Retryable       bool
	FirstSeenAt     time.Time
	LastSeenAt      time.Time
	OccurrenceCount int64
	Version         int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// DiagnosisCase stores a bounded, evidence-backed conclusion. Raw logs and
// model reasoning remain in their source systems and are referenced by ID.
type DiagnosisCase struct {
	ID                string
	TenantID          string
	IncidentID        string
	IncidentVersion   int64
	RootCauseCategory FailureCategory
	RedactedSummary   string
	Confidence        float64
	Status            DiagnosisCaseStatus
	EvidenceIDs       []string
	ProposedBy        ActorRef
	ReviewedBy        ActorRef
	ReviewReason      string
	Version           int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type DiagnosisCaseCommand struct {
	ID        string
	Diagnosis DiagnosisCase
	Actor     ActorRef
}

type DiagnosisDecisionCommand struct {
	ID              string
	TenantID        string
	DiagnosisCaseID string
	Decision        DiagnosisDecision
	Reason          string
	Actor           ActorRef
}

type RepairRule struct {
	ID                   string
	TenantID             string
	SelectorFingerprint  string
	Strategy             RepairStrategy
	ActionTypeID         string
	RiskLevel            RiskLevel
	Status               RepairRuleStatus
	Confidence           float64
	SupportCount         int64
	SuccessCount         int64
	FailureCount         int64
	Reversible           bool
	Idempotent           bool
	AutoApply            bool
	Version              int64
	SourceIncidentID     string
	DiagnosisCaseID      string
	ActivationDecisionID string
	ActivatedBy          ActorRef
	ActivatedAt          *time.Time
	LastVerifiedAt       *time.Time
	ExpiresAt            *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type RepairRuleActivationAuthorization struct {
	DecisionID          string
	ReceiptID           string
	ArgsHash            string
	Purpose             string
	PreflightEvidenceID string
}

type RepairRuleCommand struct {
	ID         string
	Rule       RepairRule
	Actor      ActorRef
	Activation *RepairRuleActivationAuthorization
}

type RepairOutcome struct {
	ID         string
	TenantID   string
	RuleID     string
	IncidentID string
	Success    bool
	EvidenceID string
	Actor      ActorRef
	OccurredAt time.Time
}

type RepairPreflight struct {
	EvidenceID          string
	TenantID            string
	ActionRunID         string
	IncidentID          string
	IncidentVersion     int64
	RuleID              string
	RuleVersion         int64
	Strategy            RepairStrategy
	ArgsHash            string
	TargetObservationID string
	TargetKind          RepairTargetKind
	TargetRunID         string
	Passed              bool
	CheckedAt           time.Time
	ExpiresAt           time.Time
}

type RepairExecutionStatus string

const (
	RepairExecutionDispatching RepairExecutionStatus = "dispatching"
	RepairExecutionMonitoring  RepairExecutionStatus = "monitoring"
	RepairExecutionSucceeded   RepairExecutionStatus = "succeeded"
	RepairExecutionFailed      RepairExecutionStatus = "failed"
)

type RepairExecution struct {
	TenantID            string
	ActionRunID         string
	IncidentID          string
	RuleID              string
	PreflightEvidenceID string
	Strategy            RepairStrategy
	Target              RepairTarget
	RetryRunID          string
	Status              RepairExecutionStatus
	FailureCode         string
	Version             int64
	StartedAt           time.Time
	DeadlineAt          time.Time
	UpdatedAt           time.Time
	CompletedAt         *time.Time
}

type RepairExecutionUpdate struct {
	TenantID        string
	ActionRunID     string
	ExpectedVersion int64
	Status          RepairExecutionStatus
	RetryRunID      string
	FailureCode     string
	UpdatedAt       time.Time
	CompletedAt     *time.Time
}

type AutoApplyDecision struct {
	Allowed bool
	Reasons []string
}

type Event struct {
	ID            string
	TenantID      string
	Type          string
	StreamID      string
	Version       int64
	CorrelationID string
	CausationID   string
	OccurredAt    time.Time
	Actor         ActorRef
	Payload       map[string]any
}

const (
	EventFailureObserved       = "reliability.failure.observed"
	EventFailureStatusChanged  = "reliability.failure.status_changed"
	EventDiagnosisCaseUpserted = "reliability.diagnosis_case.upserted"
	EventRepairRuleUpserted    = "reliability.repair_rule.upserted"
)
