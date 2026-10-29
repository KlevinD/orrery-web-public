package actioncontract

import "time"

type ActionTypeID string
type ActionRunID string
type WritebackReceiptID string

type ActionCategory string

const (
	CategoryHuman  ActionCategory = "human"
	CategoryAgent  ActionCategory = "agent"
	CategoryTool   ActionCategory = "tool"
	CategoryHybrid ActionCategory = "hybrid"
	CategorySystem ActionCategory = "system"
)

type ProviderKind string

const (
	ProviderHuman           ProviderKind = "human"
	ProviderMCPTool         ProviderKind = "mcp_tool"
	ProviderInternalService ProviderKind = "internal_service"
	ProviderConnector       ProviderKind = "connector"
	ProviderWorker          ProviderKind = "worker"
	ProviderApproval        ProviderKind = "approval"
)

type RiskLevel string

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

type ExecutionMode string

const (
	ExecutionModeApply  ExecutionMode = "apply"
	ExecutionModeDryRun ExecutionMode = "dry_run"
)

type SubjectKind string

const (
	SubjectHuman   SubjectKind = "human"
	SubjectAgent   SubjectKind = "agent"
	SubjectService SubjectKind = "service"
	SubjectWorker  SubjectKind = "worker"
)

type RuntimeKind string

const (
	RuntimeWebApp     RuntimeKind = "web_app"
	RuntimeAgent      RuntimeKind = "agent_runtime"
	RuntimeMCPGateway RuntimeKind = "mcp_gateway"
	RuntimeIRIS       RuntimeKind = "iris"
	RuntimeWorker     RuntimeKind = "worker"
	RuntimeSystem     RuntimeKind = "system"
)

type RunStatus string

const (
	RunStatusStarted   RunStatus = "started"
	RunStatusCompleted RunStatus = "completed"
	RunStatusFailed    RunStatus = "failed"
)

type WritebackStatus string

const (
	WritebackStatusSucceeded WritebackStatus = "succeeded"
	WritebackStatusFailed    WritebackStatus = "failed"
)

type ActionType struct {
	ID                ActionTypeID
	TenantID          string
	Name              string
	Category          ActionCategory
	Description       string
	ObjectTypeRefs    []string
	ProviderKind      ProviderKind
	ProviderRef       string
	InputSchema       map[string]any
	OutputSchema      map[string]any
	ResourceResolver  string
	PermissionScope   string
	FieldProfile      string
	DataPolicy        string
	PurposePolicy     string
	RiskLevel         RiskLevel
	HILPolicy         map[string]any
	DryRunSupported   bool
	WritebackTarget   map[string]any
	IdempotencyPolicy map[string]any
	AuditPolicy       map[string]any
	FailurePolicy     map[string]any
	Semantics         ActionSemanticProfile
	Enabled           bool
	Version           string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// ActionSemanticProfile describes the business meaning of an action. It is
// deliberately separate from provider and authorization fields: recognizing
// an action never grants permission to execute it.
type ActionSemanticProfile struct {
	Verbs          []string       `json:"verbs,omitempty"`
	Roles          []SemanticRole `json:"roles,omitempty"`
	Preconditions  []string       `json:"preconditions,omitempty"`
	Effects        []string       `json:"effects,omitempty"`
	DecomposesInto []ActionTypeID `json:"decomposes_into,omitempty"`
	KnowledgeTerms []string       `json:"knowledge_terms,omitempty"`
	MemoryTerms    []string       `json:"memory_terms,omitempty"`
}

type SemanticRole struct {
	Name       string `json:"name"`
	ObjectType string `json:"object_type,omitempty"`
	Required   bool   `json:"required,omitempty"`
}

type SpeechAct string

const (
	SpeechActAssertion     SpeechAct = "assertion"
	SpeechActCommand       SpeechAct = "command"
	SpeechActIntention     SpeechAct = "intention"
	SpeechActQuestion      SpeechAct = "question"
	SpeechActFailureReport SpeechAct = "failure_report"
)

type ActionState string

const (
	ActionStateObserved  ActionState = "observed"
	ActionStateCompleted ActionState = "completed"
	ActionStateRequested ActionState = "requested"
	ActionStatePlanned   ActionState = "planned"
	ActionStateFailed    ActionState = "failed"
	ActionStateUnknown   ActionState = "unknown"
)

type RetrievalMode string

const (
	RetrievalModeNone         RetrievalMode = "none"
	RetrievalModeExplain      RetrievalMode = "explain"
	RetrievalModeTroubleshoot RetrievalMode = "troubleshoot"
)

type ActionSemanticFrame struct {
	Recognized         bool           `json:"recognized"`
	ActionTypeID       ActionTypeID   `json:"action_type_id,omitempty"`
	ActionName         string         `json:"action_name,omitempty"`
	SpeechAct          SpeechAct      `json:"speech_act,omitempty"`
	State              ActionState    `json:"state,omitempty"`
	Negated            bool           `json:"negated,omitempty"`
	Actor              string         `json:"actor,omitempty"`
	MatchedVerb        string         `json:"matched_verb,omitempty"`
	Confidence         float64        `json:"confidence,omitempty"`
	ExecutionRequested bool           `json:"execution_requested"`
	RetrievalMode      RetrievalMode  `json:"retrieval_mode,omitempty"`
	Roles              []SemanticRole `json:"roles,omitempty"`
	Preconditions      []string       `json:"preconditions,omitempty"`
	Effects            []string       `json:"effects,omitempty"`
	DecomposesInto     []ActionTypeID `json:"decomposes_into,omitempty"`
	KnowledgeTerms     []string       `json:"knowledge_terms,omitempty"`
	MemoryTerms        []string       `json:"memory_terms,omitempty"`
}

type ActionSubject struct {
	Kind SubjectKind
	ID   string
	Name string
}

type RuntimeRef struct {
	Kind RuntimeKind
	ID   string
}

type ObjectRef struct {
	Kind string
	ID   string
}

type Ref struct {
	Kind string
	ID   string
}

type ActionRun struct {
	ID               ActionRunID
	TenantID         string
	WorkspaceID      string
	ActionTypeID     ActionTypeID
	ActionCategory   ActionCategory
	Actor            ActionSubject
	EffectiveUser    ActionSubject
	Delegation       Ref
	Runtime          RuntimeRef
	Session          Ref
	HumanIntent      Ref
	Approval         Ref
	Purpose          string
	ObjectRefs       []ObjectRef
	RequestedFields  []string
	ArgsHash         string
	RiskLevel        RiskLevel
	ExecutionMode    ExecutionMode
	PolicyDecisionID string
	HILTokenID       string
	Status           RunStatus
	ProviderCallRef  Ref
	ResultRef        Ref
	EvidenceRefs     []Ref
	AuditRef         Ref
	ErrorCode        string
	ErrorMessage     string
	CreatedAt        time.Time
	CompletedAt      *time.Time
}

type WritebackReceipt struct {
	ID                  WritebackReceiptID
	TenantID            string
	ActionRunID         ActionRunID
	TargetSystem        string
	TargetResourceRefs  []ObjectRef
	BeforeHash          string
	AfterHash           string
	ChangedFields       []string
	ExternalOperationID string
	IdempotencyKey      string
	Status              WritebackStatus
	EvidenceRefs        []Ref
	AuditRef            Ref
	CreatedAt           time.Time
}

type PageDTO struct {
	Limit    int `json:"limit"`
	Offset   int `json:"offset"`
	Total    int `json:"total"`
	Returned int `json:"returned"`
}

type ActionRunQuery struct {
	TenantID     string
	ActionTypeID ActionTypeID
	Status       RunStatus
	ActorID      string
	RuntimeKind  RuntimeKind
	RuntimeID    string
	Limit        int
	Offset       int
}

type ActionRunPage struct {
	Runs []ActionRun `json:"runs"`
	Page PageDTO     `json:"page"`
}

type StartRunRequest struct {
	RunID            ActionRunID
	TenantID         string
	WorkspaceID      string
	ActionTypeID     ActionTypeID
	Actor            ActionSubject
	EffectiveUser    ActionSubject
	Delegation       Ref
	Runtime          RuntimeRef
	Session          Ref
	HumanIntent      Ref
	Approval         Ref
	Purpose          string
	ObjectRefs       []ObjectRef
	RequestedFields  []string
	ArgsHash         string
	ExecutionMode    ExecutionMode
	PolicyDecisionID string
	HILTokenID       string
	EvidenceRefs     []Ref
	AuditRef         Ref
}

type CompleteRunRequest struct {
	RunID           ActionRunID
	ProviderCallRef Ref
	ResultRef       Ref
	EvidenceRefs    []Ref
	Writeback       *WritebackReceipt
}

type FailRunRequest struct {
	RunID           ActionRunID
	ProviderCallRef Ref
	ErrorCode       string
	ErrorMessage    string
	EvidenceRefs    []Ref
}
