package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	contextgraph "orrery_v2/internal/app/contextgraph"
	contextconsumer "orrery_v2/internal/app/contextgraph/consumer"
	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/permissionscope"
	schedulerapp "orrery_v2/internal/app/scheduler"
	todoapp "orrery_v2/internal/app/todo"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
	toolstd "orrery_v2/internal/resource/tool/std"
)

type ToolRouterOptions struct {
	PersonalBrowserExecutor        PersonalBrowserExecutor
	Registry                       toolres.Registry
	SchedulerExecutor              SchedulerExecutor
	TodoExecutor                   TodoExecutor
	BusinessApprovalExecutor       BusinessApprovalExecutor
	EvaluationExecutor             EvaluationToolExecutor
	MemorySearchExecutor           MemorySearchToolExecutor
	SessionHistoryExecutor         SessionHistoryToolExecutor
	RSILearningExecutor            RSILearningToolExecutor
	MemoryDiaryExecutor            MemoryDiaryToolExecutor
	KnowledgeExecutor              KnowledgeToolExecutor
	WebExecutor                    WebToolExecutor
	EmailExecutor                  EmailToolExecutor
	PersonalAssistantShellExecutor PersonalAssistantShellExecutor
	EnterpriseGraphExecutor        EnterpriseGraphExecutor
	OntologyExecutor               OntologyToolExecutor
	SkillExecutor                  SkillToolExecutor
	WebsiteKnowledgeExecutor       WebsiteKnowledgeToolExecutor
	ExternalMCP                    ExternalMCPClient
	AuditRecorder                  AuditRecorder
	Now                            func() time.Time
}

type ToolRouter struct {
	personalBrowserExecutor        PersonalBrowserExecutor
	registry                       toolres.Registry
	schedulerExecutor              SchedulerExecutor
	todoExecutor                   TodoExecutor
	businessApprovalExecutor       BusinessApprovalExecutor
	evaluationExecutor             EvaluationToolExecutor
	memorySearchExecutor           MemorySearchToolExecutor
	sessionHistoryExecutor         SessionHistoryToolExecutor
	rsiLearningExecutor            RSILearningToolExecutor
	memoryDiaryExecutor            MemoryDiaryToolExecutor
	knowledgeExecutor              KnowledgeToolExecutor
	webExecutor                    WebToolExecutor
	emailExecutor                  EmailToolExecutor
	personalAssistantShellExecutor PersonalAssistantShellExecutor
	enterpriseGraphExecutor        EnterpriseGraphExecutor
	ontologyExecutor               OntologyToolExecutor
	skillExecutor                  SkillToolExecutor
	websiteKnowledgeExecutor       WebsiteKnowledgeToolExecutor
	externalMCP                    ExternalMCPClient
	audit                          AuditRecorder
	now                            func() time.Time
}

type SchedulerExecutor interface {
	Execute(ctx context.Context, req schedulerapp.ToolExecutionRequest) (schedulerapp.ToolExecutionResult, error)
}

type TodoExecutor interface {
	Execute(ctx context.Context, req todoapp.ToolExecutionRequest) (todoapp.ToolExecutionResult, error)
}

type BusinessApprovalExecutor interface {
	Execute(context.Context, hil.BusinessApprovalToolRequest) (hil.BusinessApprovalToolResult, error)
}

type EvaluationToolExecutor interface {
	ExecuteEvaluationTool(ctx context.Context, toolID string, req EvaluationToolRequest) (EvaluationToolResult, error)
}

type EvaluationToolRequest struct {
	PrincipalID        string
	AgentID            string
	Mode               string
	TargetAgentID      string
	EvaluatorAgentID   string
	Summary            string
	EstimatedHumanDays float64
	WindowDays         int
	IdempotencyKey     string
	ReviewInputs       []EvaluationToolReviewInput
}

type EvaluationToolReviewInput struct {
	AgentID            string
	Summary            string
	EstimatedHumanDays float64
}

type EvaluationToolResult struct {
	Output map[string]any
}

type MemoryDiaryToolExecutor interface {
	ExecuteMemoryDiaryTool(ctx context.Context, toolID string, req MemoryDiaryToolRequest) (MemoryDiaryToolResult, error)
}

type MemorySearchToolExecutor interface {
	ExecuteMemorySearchTool(ctx context.Context, req MemorySearchToolRequest) (MemorySearchToolResult, error)
}

type MemorySearchToolRequest struct {
	TenantID       string
	PrincipalID    string
	PrincipalKind  string
	AgentID        string
	LogicalAgentID string
	Query          string
}

type MemorySearchToolResult struct {
	Output map[string]any
}

type MemoryDiaryToolRequest struct {
	TenantID          string
	PrincipalID       string
	AgentID           string
	Mode              string
	TargetAgentID     string
	Date              string
	Force             bool
	Limit             int
	IdempotencyKey    string
	Path              string
	Content           string
	SessionID         string
	ContextRevision   string
	Trigger           string
	MissionID         string
	TaskID            string
	RunID             string
	Sensitivity       string
	SourceSensitivity string
	EvidenceRefs      []string
	LineageRefs       []string
}

type MemoryDiaryToolResult struct {
	Output map[string]any
}

type SkillToolExecutor interface {
	ExecuteSkillTool(ctx context.Context, toolID string, req skillres.RuntimeSkillRequest) (skillres.RuntimeToolResult, error)
}

type KnowledgeToolExecutor interface {
	ExecuteKnowledgeTool(ctx context.Context, toolID string, req KnowledgeToolRequest) (KnowledgeToolResult, error)
}

type KnowledgeToolRequest struct {
	TenantID         string
	PrincipalID      string
	PrincipalKind    string
	AgentID          string
	Query            string
	RetrievalMode    string
	KnowledgeBaseIDs []string
	KnowledgeBaseID  string
	OwnerType        string
	OwnerID          string
	Limit            int
	Title            string
	Content          string
	Filename         string
	ContentType      string
}

type KnowledgeToolResult struct {
	Output map[string]any
}

type WebToolExecutor interface {
	ExecuteWebTool(ctx context.Context, toolID string, req WebToolRequest) (WebToolResult, error)
}

type WebToolRequest struct {
	Query         string
	URL           string
	Limit         int
	MaxCharacters int
}

type WebToolResult struct {
	Output map[string]any
}

type WebsiteKnowledgeToolExecutor interface {
	ExecuteWebsiteKnowledgeTool(context.Context, string, WebsiteKnowledgeToolRequest) (WebsiteKnowledgeToolResult, error)
}

type WebsiteKnowledgeToolRequest struct {
	PrincipalID        string
	MinimumCount       int
	ProposalKind       string
	GapID              string
	TargetKnowledgeKey string
	Draft              string
	SourceRefs         []string
	Rationale          string
	FAQRevision        map[string]any
}

type WebsiteKnowledgeToolResult struct {
	Output map[string]any
}

type EmailToolExecutor interface {
	ExecuteEmailTool(ctx context.Context, toolID string, req EmailToolRequest) (EmailToolResult, error)
}

type EmailToolRequest struct {
	TenantID       string
	PrincipalID    string
	AgentID        string
	ProfileID      string
	To             []string
	Cc             []string
	Subject        string
	Text           string
	HTML           string
	IdempotencyKey string
}

type EmailToolResult struct {
	Output map[string]any
}

type PersonalAssistantShellExecutor interface {
	ExecutePersonalAssistantShell(ctx context.Context, toolID string, req PersonalAssistantShellRequest) (PersonalAssistantShellResult, error)
}

type PersonalAssistantShellRequest struct {
	PrincipalID       string
	AgentID           string
	Command           string
	Cwd               string
	WorkspaceRoot     string
	WorkingDirectory  string
	TimeoutMs         int
	MaximumTimeoutMs  int
	MaxOutputLen      int
	EnvironmentPolicy string
}

type PersonalAssistantShellResult struct {
	Output map[string]any
}

type EnterpriseGraphExecutor interface {
	BuildContextBundle(ctx context.Context, req contextconsumer.BuildContextBundleInput) (contextgraph.ContextBundle, error)
	SearchObjects(ctx context.Context, req contextconsumer.SearchObjectsInput) (contextconsumer.SearchObjectsResult, error)
}

type OntologyToolExecutor interface {
	ExecuteOntologyTool(ctx context.Context, toolID string, req OntologyToolRequest) (OntologyToolResult, error)
}

type OntologyEvidenceRef struct {
	Kind     string
	ID       string
	Metadata map[string]string
}

type OntologyProposalCandidate struct {
	TargetKind    string
	TargetID      string
	MetadataKey   string
	ProposedValue string
	Confidence    float64
	Reason        string
	EvidenceRefs  []OntologyEvidenceRef
}

type OntologyToolRequest struct {
	InferenceJSON    json.RawMessage
	TenantID         string
	PrincipalID      string
	AgentID          string
	Permissions      []string
	Engine           string
	Model            string
	ModelVersion     string
	RunID            string
	InputHash        string
	PromptHash       string
	SourceRef        string
	SourceVersion    string
	Candidates       []OntologyProposalCandidate
	ProposalID       string
	ExpectedRevision int64
	Decision         string
	Reason           string
	EvidenceRefs     []OntologyEvidenceRef
	PlanID           string
	ReceiptID        string
	IdempotencyKey   string
	Confirmed        bool
}

type OntologyToolResult struct {
	Output map[string]any
}

type CallToolRequest struct {
	TenantID                 string   `json:"tenant_id,omitempty"`
	RunID                    string   `json:"run_id,omitempty"`
	ToolID                   string   `json:"tool_id"`
	PrincipalID              string   `json:"principal_id,omitempty"`
	PrincipalKind            string   `json:"-"`
	Permissions              []string `json:"permissions,omitempty"`
	PermissionsAuthoritative bool     `json:"-"`
	AgentID                  string   `json:"agent_id,omitempty"`
	LogicalAgentID           string   `json:"-"`
	ProfileID                string   `json:"profile_id,omitempty"`
	// ExecutionEnvelope is trusted in-process authority and is excluded from
	// transport JSON so clients cannot submit their own permission snapshot.
	ExecutionEnvelope            *agentcore.ExecutionEnvelope     `json:"-"`
	ExecutionEnvelopeIDClaim     string                           `json:"-"`
	ExecutionEnvelopeDigestClaim string                           `json:"-"`
	SkillBindings                []agentcore.RuntimeSkillBinding  `json:"skill_bindings,omitempty"`
	ResponsibilityContext        *agentcore.ResponsibilityContext `json:"responsibility_context,omitempty"`
	MCPAuthorizations            []agentcore.MCPAuthorization     `json:"-"`
	Arguments                    map[string]any                   `json:"arguments,omitempty"`
	RequestMeta                  map[string]any                   `json:"-"`
	Maintenance                  bool                             `json:"-"`
}

type CallToolResult struct {
	Output map[string]any `json:"output,omitempty"`
}

type AuditOutcome string

const (
	AuditOutcomeAllowed AuditOutcome = "allowed"
	AuditOutcomeDenied  AuditOutcome = "denied"
	AuditOutcomeFailed  AuditOutcome = "failed"
)

type AuditEvent struct {
	TenantID                    string
	RunID                       string
	ToolID                      string
	PrincipalID                 string
	CorrelationID               string
	Source                      ToolSource
	ServerToolID                string
	DownstreamToolName          string
	ResponsibilityContextID     string
	ResponsibilityID            string
	ResponsibilityPolicyID      string
	ResponsibilityPolicyVersion int64
	ExecutionEnvelopeID         string
	ExecutionEnvelopeDigest     string
	Outcome                     AuditOutcome
	Reason                      string
	OccurredAt                  time.Time
}

type AuditRecorder interface {
	RecordMCPToolAudit(ctx context.Context, event AuditEvent) error
}

func NewToolRouter(opts ToolRouterOptions) *ToolRouter {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &ToolRouter{
		personalBrowserExecutor:        opts.PersonalBrowserExecutor,
		registry:                       opts.Registry,
		schedulerExecutor:              opts.SchedulerExecutor,
		todoExecutor:                   opts.TodoExecutor,
		businessApprovalExecutor:       opts.BusinessApprovalExecutor,
		evaluationExecutor:             opts.EvaluationExecutor,
		memorySearchExecutor:           opts.MemorySearchExecutor,
		sessionHistoryExecutor:         opts.SessionHistoryExecutor,
		rsiLearningExecutor:            opts.RSILearningExecutor,
		memoryDiaryExecutor:            opts.MemoryDiaryExecutor,
		knowledgeExecutor:              opts.KnowledgeExecutor,
		webExecutor:                    opts.WebExecutor,
		emailExecutor:                  opts.EmailExecutor,
		personalAssistantShellExecutor: opts.PersonalAssistantShellExecutor,
		enterpriseGraphExecutor:        opts.EnterpriseGraphExecutor,
		ontologyExecutor:               opts.OntologyExecutor,
		skillExecutor:                  opts.SkillExecutor,
		websiteKnowledgeExecutor:       opts.WebsiteKnowledgeExecutor,
		externalMCP:                    opts.ExternalMCP,
		audit:                          opts.AuditRecorder,
		now:                            now,
	}
}

func (r *ToolRouter) CallTool(ctx context.Context, req CallToolRequest) (CallToolResult, error) {
	if err := ctx.Err(); err != nil {
		return CallToolResult{}, err
	}
	if r == nil || r.registry == nil {
		return CallToolResult{}, ErrServiceDependency
	}
	toolID := strings.TrimSpace(req.ToolID)
	if toolID == "" {
		return CallToolResult{}, ErrInvalidRequest
	}
	if err := validateResponsibilityExecutionIdentity(req.RunID, req.ResponsibilityContext); err != nil {
		return CallToolResult{}, err
	}
	if err := validateExecutionEnvelopeForRequest(req); err != nil {
		r.record(ctx, auditEventForRequest(req, "", toolres.ToolDefinition{}, "", AuditOutcomeDenied, "execution_envelope_invalid"))
		return CallToolResult{}, err
	}
	if req.ExecutionEnvelope != nil && req.ExecutionEnvelope.ResponsibilityContext != nil {
		req.ResponsibilityContext = agentcore.CloneResponsibilityContext(req.ExecutionEnvelope.ResponsibilityContext)
	}
	definition, downstreamToolName, source, err := r.resolveTool(ctx, toolID)
	if err != nil {
		r.record(ctx, auditEventForRequest(req, source, definition, downstreamToolName, AuditOutcomeDenied, reasonForError(err)))
		return CallToolResult{}, err
	}
	if !responsibilityAuthorityAllowsTool(req.ResponsibilityContext, toolID, string(definition.ID), permissionDeclarationScopes(definition.Permissions)) {
		r.record(ctx, auditEventForRequest(req, source, definition, downstreamToolName, AuditOutcomeDenied, "responsibility_permission_denied"))
		return CallToolResult{}, ErrPermissionDenied
	}
	if req.ExecutionEnvelope != nil && executionEnvelopeEnforcesTool(req.ExecutionEnvelope) &&
		!req.ExecutionEnvelope.AllowsTool(toolID, permissionDeclarationScopes(definition.Permissions)) {
		r.record(ctx, auditEventForRequest(req, source, definition, downstreamToolName, AuditOutcomeDenied, "execution_envelope_permission_denied"))
		return CallToolResult{}, ErrPermissionDenied
	}
	if responsibility := req.ResponsibilityContext; responsibility != nil && strings.TrimSpace(responsibility.AgentProfileID) != "" &&
		strings.TrimSpace(req.ProfileID) != "" && !strings.EqualFold(strings.TrimSpace(responsibility.AgentProfileID), strings.TrimSpace(req.ProfileID)) {
		r.record(ctx, auditEventForRequest(req, source, definition, downstreamToolName, AuditOutcomeDenied, "responsibility_profile_mismatch"))
		return CallToolResult{}, ErrPermissionDenied
	}
	if isMemoryDiaryMaintenanceToolID(toolID) && (!req.Maintenance || !validDiaryMaintenanceIdentity(req)) {
		r.record(ctx, auditEventForRequest(req, source, definition, downstreamToolName, AuditOutcomeDenied, "permission_denied"))
		return CallToolResult{}, ErrPermissionDenied
	}
	if err := checkGatewayPermissions(definition.Permissions, req.Permissions); err != nil {
		r.record(ctx, auditEventForRequest(req, source, definition, downstreamToolName, AuditOutcomeDenied, "permission_denied"))
		return CallToolResult{}, err
	}
	result, err := r.dispatch(ctx, req, definition, downstreamToolName, source)
	if err != nil {
		r.record(ctx, auditEventForRequest(req, source, definition, downstreamToolName, AuditOutcomeFailed, err.Error()))
		return CallToolResult{}, err
	}
	r.record(ctx, auditEventForRequest(req, source, definition, downstreamToolName, AuditOutcomeAllowed, ""))
	return result, nil
}

func validDiaryMaintenanceIdentity(req CallToolRequest) bool {
	tenantID := strings.TrimSpace(req.TenantID)
	agentID := strings.TrimSpace(req.AgentID)
	principalID := strings.TrimSpace(req.PrincipalID)
	return tenantID != "" && agentID != "" && principalID != "" && principalID == agentID
}

func (r *ToolRouter) dispatch(ctx context.Context, req CallToolRequest, definition toolres.ToolDefinition, downstreamToolName string, source ToolSource) (CallToolResult, error) {
	switch source {
	case ToolSourceInternal:
		if definition.ID == toolstd.SessionSearch || definition.ID == toolstd.SessionRead {
			if r.sessionHistoryExecutor == nil {
				return CallToolResult{}, ErrServiceDependency
			}
			output, err := r.sessionHistoryExecutor.ExecuteHistoryTool(ctx, sessionHistoryToolRequest(req, string(definition.ID)))
			return CallToolResult{Output: output}, err
		}
		if isWebsiteKnowledgeToolID(string(definition.ID)) {
			if r.websiteKnowledgeExecutor == nil {
				return CallToolResult{}, ErrServiceDependency
			}
			result, err := r.websiteKnowledgeExecutor.ExecuteWebsiteKnowledgeTool(ctx, string(definition.ID), websiteKnowledgeToolRequest(req))
			if err != nil {
				return CallToolResult{}, err
			}
			return CallToolResult{Output: result.Output}, nil
		}
		if isSkillToolID(string(definition.ID)) {
			if r.skillExecutor == nil {
				return CallToolResult{}, ErrServiceDependency
			}
			result, err := r.skillExecutor.ExecuteSkillTool(ctx, string(definition.ID), skillRuntimeRequest(req))
			if err != nil {
				return CallToolResult{}, err
			}
			return CallToolResult{Output: result.Output}, nil
		}
		if isSchedulerToolID(string(definition.ID)) {
			if r.schedulerExecutor == nil {
				return CallToolResult{}, ErrServiceDependency
			}
			viewerKind := schedulerapp.ViewerKindAgent
			creatorProfileID := strings.TrimSpace(firstNonBlank(req.ProfileID, req.AgentID))
			if req.ExecutionEnvelope != nil && req.ExecutionEnvelope.PrincipalKind == agentcore.ExecutionPrincipalUser {
				// The envelope has already been checked against the host's run.
				// Persist the delegated human as creator, not the executing Agent.
				viewerKind = schedulerapp.ViewerKindUser
				creatorProfileID = strings.TrimSpace(req.ExecutionEnvelope.PrincipalID)
			}
			result, err := r.schedulerExecutor.Execute(ctx, schedulerapp.ToolExecutionRequest{
				ToolID: string(definition.ID),
				Viewer: schedulerapp.ViewerRef{
					TenantID:        strings.TrimSpace(firstNonBlank(req.TenantID, "system")),
					ViewerID:        strings.TrimSpace(req.PrincipalID),
					ViewerKind:      viewerKind,
					DisplayName:     strings.TrimSpace(req.PrincipalID),
					ProfileID:       creatorProfileID,
					PermissionScope: append([]string(nil), req.Permissions...),
				},
				Arguments: cloneArguments(req.Arguments),
			})
			if err != nil {
				return CallToolResult{}, err
			}
			return CallToolResult{Output: result.Output}, nil
		}
		if isTodoToolID(string(definition.ID)) {
			if r.todoExecutor == nil {
				return CallToolResult{}, ErrServiceDependency
			}
			result, err := r.todoExecutor.Execute(ctx, todoapp.ToolExecutionRequest{
				ToolID: string(definition.ID),
				Viewer: todoapp.ViewerRef{
					TenantID:        strings.TrimSpace(firstNonBlank(req.TenantID, "system")),
					ViewerID:        strings.TrimSpace(req.PrincipalID),
					ViewerKind:      todoapp.ViewerKindAgent,
					DisplayName:     strings.TrimSpace(req.PrincipalID),
					PermissionScope: append([]string(nil), req.Permissions...),
				},
				Arguments: cloneArguments(req.Arguments),
			})
			if err != nil {
				return CallToolResult{}, err
			}
			return CallToolResult{Output: result.Output}, nil
		}
		if isEvaluationToolID(string(definition.ID)) {
			if r.evaluationExecutor == nil {
				return CallToolResult{}, ErrServiceDependency
			}
			result, err := r.evaluationExecutor.ExecuteEvaluationTool(ctx, string(definition.ID), evaluationToolRequest(req))
			if err != nil {
				return CallToolResult{}, err
			}
			return CallToolResult{Output: result.Output}, nil
		}
		if isBusinessApprovalToolID(string(definition.ID)) {
			if r.businessApprovalExecutor == nil {
				return CallToolResult{}, ErrServiceDependency
			}
			result, err := r.businessApprovalExecutor.Execute(ctx, hil.BusinessApprovalToolRequest{
				TenantID: strings.TrimSpace(firstNonBlank(req.TenantID, "system")), PrincipalID: strings.TrimSpace(req.PrincipalID),
				AgentID: strings.TrimSpace(req.AgentID), Arguments: cloneArguments(req.Arguments),
			})
			if err != nil {
				return CallToolResult{}, err
			}
			return CallToolResult{Output: map[string]any{
				"decision_id": result.DecisionID, "workflow_instance_id": result.WorkflowInstanceID,
				"status": string(result.Status), "route_mode": string(result.RouteMode),
				"material_hash": result.MaterialHash, "revision": result.Revision,
			}}, nil
		}
		if isMemoryDiaryToolID(string(definition.ID)) {
			if r.memoryDiaryExecutor == nil {
				return CallToolResult{}, ErrServiceDependency
			}
			result, err := r.memoryDiaryExecutor.ExecuteMemoryDiaryTool(ctx, string(definition.ID), memoryDiaryToolRequest(req))
			if err != nil {
				return CallToolResult{}, err
			}
			return CallToolResult{Output: result.Output}, nil
		}
		if isMemorySearchToolID(string(definition.ID)) {
			if r.memorySearchExecutor == nil {
				return CallToolResult{}, ErrServiceDependency
			}
			result, err := r.memorySearchExecutor.ExecuteMemorySearchTool(ctx, memorySearchToolRequest(req))
			if err != nil {
				return CallToolResult{}, err
			}
			return CallToolResult{Output: result.Output}, nil
		}
		if definition.ID == toolstd.RSILearningPlan {
			if r.rsiLearningExecutor == nil {
				return CallToolResult{}, ErrServiceDependency
			}
			output, err := r.rsiLearningExecutor.ExecuteRSILearningTool(ctx, RSILearningToolRequest{TenantID: req.TenantID, AgentID: req.AgentID, Arguments: cloneArguments(req.Arguments)})
			if err != nil {
				return CallToolResult{}, err
			}
			return CallToolResult{Output: output}, nil
		}
		if isKnowledgeToolID(string(definition.ID)) {
			if r.knowledgeExecutor == nil {
				return CallToolResult{}, ErrServiceDependency
			}
			result, err := r.knowledgeExecutor.ExecuteKnowledgeTool(ctx, string(definition.ID), knowledgeToolRequest(req))
			if err != nil {
				return CallToolResult{}, err
			}
			return CallToolResult{Output: result.Output}, nil
		}
		if isWebToolID(string(definition.ID)) {
			if r.webExecutor == nil {
				return CallToolResult{}, ErrServiceDependency
			}
			result, err := r.webExecutor.ExecuteWebTool(ctx, string(definition.ID), webToolRequest(req))
			if err != nil {
				return CallToolResult{}, err
			}
			return CallToolResult{Output: result.Output}, nil
		}
		if isEmailToolID(string(definition.ID)) {
			if r.emailExecutor == nil {
				return CallToolResult{}, ErrServiceDependency
			}
			result, err := r.emailExecutor.ExecuteEmailTool(ctx, string(definition.ID), emailToolRequest(req))
			if err != nil {
				return CallToolResult{}, err
			}
			return CallToolResult{Output: result.Output}, nil
		}
		if isPersonalAssistantShellToolID(string(definition.ID)) {
			if r.personalAssistantShellExecutor == nil {
				return CallToolResult{}, ErrServiceDependency
			}
			result, err := r.personalAssistantShellExecutor.ExecutePersonalAssistantShell(ctx, string(definition.ID), personalAssistantShellRequest(req))
			if err != nil {
				return CallToolResult{}, err
			}
			return CallToolResult{Output: result.Output}, nil
		}
		if toolstd.IsPersonalBrowserTool(string(definition.ID)) {
			if r.personalBrowserExecutor == nil {
				return CallToolResult{}, ErrServiceDependency
			}
			return r.personalBrowserExecutor.ExecutePersonalBrowser(ctx, req)
		}
		if isEnterpriseGraphToolID(string(definition.ID)) {
			if r.enterpriseGraphExecutor == nil {
				return CallToolResult{}, ErrServiceDependency
			}
			if err := validateEnterpriseGraphArguments(string(definition.ID), req.Arguments); err != nil {
				return CallToolResult{}, err
			}
			if strings.TrimSpace(string(definition.ID)) == string(toolstd.EnterpriseGraphSearchObjects) {
				result, err := r.enterpriseGraphExecutor.SearchObjects(ctx, enterpriseGraphSearchObjectsRequest(req))
				if err != nil {
					return CallToolResult{}, err
				}
				return CallToolResult{Output: searchObjectsOutput(result)}, nil
			}
			bundle, err := r.enterpriseGraphExecutor.BuildContextBundle(ctx, enterpriseGraphBuildContextBundleRequest(req))
			if err != nil {
				return CallToolResult{}, err
			}
			output, err := contextBundleOutput(bundle)
			if err != nil {
				return CallToolResult{}, err
			}
			return CallToolResult{Output: output}, nil
		}
		if isOntologyToolID(string(definition.ID)) {
			if r.ontologyExecutor == nil {
				return CallToolResult{}, ErrServiceDependency
			}
			result, err := r.ontologyExecutor.ExecuteOntologyTool(ctx, string(definition.ID), ontologyToolRequest(req))
			if err != nil {
				return CallToolResult{}, err
			}
			return CallToolResult{Output: result.Output}, nil
		}
		return CallToolResult{}, fmt.Errorf("%w: internal tool executor is not configured for %s", ErrServiceDependency, definition.ID)
	case ToolSourceExternalMCP:
		if r.externalMCP == nil {
			return CallToolResult{}, ErrServiceDependency
		}
		definition = definition.Clone()
		applyMCPAuthorizations(&definition, req.MCPAuthorizations)
		return r.externalMCP.CallTool(ctx, ExternalMCPCallRequest{
			Server:             definition,
			DownstreamToolName: downstreamToolName,
			PrincipalID:        strings.TrimSpace(req.PrincipalID),
			Permissions:        append([]string(nil), req.Permissions...),
			Arguments:          cloneArguments(req.Arguments),
			RequestMeta:        cloneArguments(req.RequestMeta),
		})
	default:
		return CallToolResult{}, ErrToolNotFound
	}
}

func applyMCPAuthorizations(definition *toolres.ToolDefinition, authorizations []agentcore.MCPAuthorization) {
	if definition == nil || definition.MCP == nil || len(authorizations) == 0 {
		return
	}
	for _, auth := range authorizations {
		if !strings.EqualFold(string(auth.ServerToolID), string(definition.ID)) {
			continue
		}
		value := strings.TrimSpace(auth.CredentialValue)
		if value == "" {
			continue
		}
		headerName := strings.TrimSpace(auth.HeaderName)
		if headerName == "" {
			headerName = strings.TrimSpace(definition.MCP.Auth.HeaderName)
		}
		if headerName == "" {
			headerName = "Authorization"
		}
		if definition.MCP.Headers == nil {
			definition.MCP.Headers = map[string]string{}
		}
		definition.MCP.Headers[headerName] = value
	}
}

func (r *ToolRouter) resolveTool(ctx context.Context, toolID string) (toolres.ToolDefinition, string, ToolSource, error) {
	definition, err := r.registry.Get(ctx, agentcore.ToolID(toolID))
	if err == nil {
		if definition.Kind != toolres.KindInternalServiceTool || !definition.Enabled {
			return toolres.ToolDefinition{}, "", "", ErrToolNotFound
		}
		return definition, "", ToolSourceInternal, nil
	}
	if !errors.Is(err, toolres.ErrNotFound) {
		return toolres.ToolDefinition{}, "", "", err
	}
	mcpServers, err := r.registry.ListByKind(ctx, toolres.KindMCPServer)
	if err != nil {
		return toolres.ToolDefinition{}, "", "", err
	}
	for _, server := range mcpServers {
		if !server.Enabled || server.MCP == nil {
			continue
		}
		prefix := string(server.ID) + "."
		if !strings.HasPrefix(toolID, prefix) {
			continue
		}
		downstream := strings.TrimPrefix(toolID, prefix)
		if hasExposedTool(server.MCP.ExposedTools, downstream) || hasExposedToolDetail(server.MCP.ExposedToolDetails, downstream) {
			return server, downstream, ToolSourceExternalMCP, nil
		}
	}
	return toolres.ToolDefinition{}, "", "", ErrToolNotFound
}

func checkGatewayPermissions(required []toolres.PermissionDeclaration, granted []string) error {
	for _, permission := range required {
		scope := permissionscope.Normalize(permission.Scope)
		if scope == "" {
			continue
		}
		if !permissionscope.AnyAllows(granted, scope) {
			return fmt.Errorf("%w: missing %s", ErrPermissionDenied, scope)
		}
	}
	return nil
}

func validateResponsibilityExecutionIdentity(runID string, responsibility *agentcore.ResponsibilityContext) error {
	if responsibility == nil {
		return nil
	}
	if strings.TrimSpace(runID) == "" || strings.TrimSpace(responsibility.ContextID) == "" ||
		strings.TrimSpace(responsibility.ResponsibilityID) == "" || strings.TrimSpace(responsibility.PolicyID) == "" ||
		strings.TrimSpace(responsibility.AgentProfileID) == "" ||
		responsibility.ResponsibilityVersion < 1 || responsibility.PolicyVersion < 1 {
		return ErrInvalidRequest
	}
	return nil
}

func validateExecutionEnvelopeForRequest(req CallToolRequest) error {
	envelope := req.ExecutionEnvelope
	if envelope == nil {
		if strings.TrimSpace(req.ExecutionEnvelopeIDClaim) != "" || strings.TrimSpace(req.ExecutionEnvelopeDigestClaim) != "" {
			return ErrPermissionDenied
		}
		return nil
	}
	if err := envelope.Verify(); err != nil {
		return ErrPermissionDenied
	}
	if !envelope.ExpiresAt.IsZero() && !envelope.ExpiresAt.After(time.Now().UTC()) {
		return ErrPermissionDenied
	}
	if claim := strings.TrimSpace(req.ExecutionEnvelopeIDClaim); claim != "" && claim != strings.TrimSpace(envelope.EnvelopeID) {
		return ErrPermissionDenied
	}
	if claim := strings.TrimSpace(req.ExecutionEnvelopeDigestClaim); claim != "" && claim != strings.TrimSpace(envelope.EnvelopeDigest) {
		return ErrPermissionDenied
	}
	if runID := strings.TrimSpace(req.RunID); runID != "" && runID != strings.TrimSpace(envelope.RunID) {
		return ErrPermissionDenied
	}
	if tenantID := strings.TrimSpace(req.TenantID); tenantID != "" && tenantID != strings.TrimSpace(envelope.TenantID) {
		return ErrPermissionDenied
	}
	if agentID := strings.TrimSpace(req.AgentID); agentID != "" && agentID != strings.TrimSpace(envelope.AgentID) {
		return ErrPermissionDenied
	}
	if profileID := strings.TrimSpace(req.ProfileID); profileID != "" && profileID != strings.TrimSpace(envelope.AgentProfileID) {
		return ErrPermissionDenied
	}
	if responsibility := req.ResponsibilityContext; responsibility != nil {
		if envelope.ResponsibilityContext == nil ||
			strings.TrimSpace(responsibility.ContextID) != strings.TrimSpace(envelope.ResponsibilityContext.ContextID) ||
			responsibility.ResponsibilityVersion != envelope.ResponsibilityContext.ResponsibilityVersion ||
			strings.TrimSpace(responsibility.PolicyID) != strings.TrimSpace(envelope.ResponsibilityContext.PolicyID) ||
			responsibility.PolicyVersion != envelope.ResponsibilityContext.PolicyVersion {
			return ErrPermissionDenied
		}
	} else if envelope.ResponsibilityContext != nil && envelope.ResponsibilityContext.RequireExplicitToolScopes {
		return ErrPermissionDenied
	}
	return nil
}

func executionEnvelopeEnforcesTool(value *agentcore.ExecutionEnvelope) bool {
	return value != nil && (value.ResponsibilityContext != nil || len(value.PermissionScopes) > 0 || len(value.AllowedToolIDs) > 0 || len(value.ForbiddenActions) > 0)
}

func responsibilityAuthorityAllowsTool(responsibility *agentcore.ResponsibilityContext, toolID, serverToolID string, requiredScopes []string) bool {
	if responsibility == nil {
		return true
	}
	if responsibility.RequireExplicitToolScopes && !hasResponsibilityTaskPermissionScopes(responsibility.TaskPermissionScopes) {
		return false
	}
	if responsibilityScopeAllows(responsibility.ForbiddenActions, toolID) || responsibilityScopeAllows(responsibility.ForbiddenActions, serverToolID) {
		return false
	}
	for _, required := range requiredScopes {
		if responsibilityScopeAllows(responsibility.ForbiddenActions, required) {
			return false
		}
	}
	if len(responsibility.AllowedTools) > 0 && !responsibilityToolSelectorsAllow(responsibility.AllowedTools, toolID, serverToolID, requiredScopes) {
		return false
	}
	for _, required := range requiredScopes {
		if len(responsibility.PolicyToolScopes) > 0 && !responsibilityScopeAllows(responsibility.PolicyToolScopes, required) {
			return false
		}
		if len(responsibility.TaskPermissionScopes) > 0 && !responsibilityScopeAllows(responsibility.TaskPermissionScopes, required) {
			return false
		}
	}
	return true
}

func hasResponsibilityTaskPermissionScopes(scopes []string) bool {
	for _, scope := range scopes {
		if strings.TrimSpace(scope) != "" {
			return true
		}
	}
	return false
}

func responsibilityToolSelectorsAllow(selectors []string, toolID, serverToolID string, requiredScopes []string) bool {
	if responsibilityScopeAllows(selectors, toolID) || responsibilityScopeAllows(selectors, serverToolID) {
		return true
	}
	if len(requiredScopes) == 0 {
		return false
	}
	for _, required := range requiredScopes {
		if !responsibilityScopeAllows(selectors, required) {
			return false
		}
	}
	return true
}

func responsibilityScopeAllows(granted []string, required string) bool {
	required = strings.TrimSpace(required)
	if required == "" {
		return false
	}
	for _, candidate := range granted {
		candidate = strings.TrimSpace(candidate)
		switch {
		case candidate == "*" || candidate == required:
			return true
		case permissionscope.Allows(candidate, required):
			return true
		case strings.HasSuffix(candidate, "*") && strings.HasPrefix(required, strings.TrimSuffix(candidate, "*")):
			return true
		}
	}
	return false
}

func permissionDeclarationScopes(values []toolres.PermissionDeclaration) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if scope := strings.TrimSpace(value.Scope); scope != "" {
			out = append(out, scope)
		}
	}
	return out
}

func hasExposedTool(values []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}

func hasExposedToolDetail(values []toolres.MCPExposedToolDefinition, target string) bool {
	target = strings.TrimSpace(target)
	for _, value := range values {
		if strings.TrimSpace(value.Name) == target {
			return true
		}
	}
	return false
}

func isSchedulerToolID(toolID string) bool {
	switch strings.TrimSpace(toolID) {
	case string(toolstd.ScheduledTaskCreate),
		string(toolstd.ScheduledTaskUpdate),
		string(toolstd.ScheduledTaskCancel),
		string(toolstd.ScheduledTaskList),
		string(toolstd.ScheduledTaskRunNow):
		return true
	default:
		return false
	}
}

func isWebsiteKnowledgeToolID(toolID string) bool {
	switch strings.TrimSpace(toolID) {
	case "website_knowledge_gap_list", "website_distillation_candidates_list", "website_faq_catalog_list", "website_faq_metrics_list", "website_knowledge_proposal_create":
		return true
	default:
		return false
	}
}

func websiteKnowledgeToolRequest(req CallToolRequest) WebsiteKnowledgeToolRequest {
	return WebsiteKnowledgeToolRequest{
		PrincipalID:        strings.TrimSpace(req.PrincipalID),
		MinimumCount:       int(floatArg(req.Arguments, "minimum_count", "minimumCount")),
		ProposalKind:       firstStringArg(req.Arguments, "proposal_kind", "proposalKind"),
		GapID:              firstStringArg(req.Arguments, "gap_id", "gapId"),
		TargetKnowledgeKey: firstStringArg(req.Arguments, "target_knowledge_key", "targetKnowledgeKey"),
		Draft:              firstStringArg(req.Arguments, "draft"),
		SourceRefs:         stringSliceArg(req.Arguments, "source_refs", "sourceRefs"),
		Rationale:          firstStringArg(req.Arguments, "rationale"),
		FAQRevision:        firstMapArg(req.Arguments, "faq_revision", "faqRevision"),
	}
}

func firstMapArg(arguments map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if value, ok := arguments[key].(map[string]any); ok {
			out := make(map[string]any, len(value))
			for name, field := range value {
				out[name] = field
			}
			return out
		}
	}
	return nil
}

func isEvaluationToolID(toolID string) bool {
	return strings.TrimSpace(toolID) == string(toolstd.EvaluationReview)
}

func isBusinessApprovalToolID(toolID string) bool {
	return strings.TrimSpace(toolID) == string(toolstd.RequestBusinessApproval)
}

func isMemoryDiaryToolID(toolID string) bool {
	switch strings.TrimSpace(toolID) {
	case string(toolstd.MemoryDiaryWrite), string(toolstd.MemoryDiaryRead), string(toolstd.MemoryDiaryAppend):
		return true
	default:
		return false
	}
}

func isMemoryDiaryMaintenanceToolID(toolID string) bool {
	switch strings.TrimSpace(toolID) {
	case string(toolstd.MemoryDiaryRead), string(toolstd.MemoryDiaryAppend):
		return true
	default:
		return false
	}
}

func isMemorySearchToolID(toolID string) bool {
	return strings.TrimSpace(toolID) == string(toolstd.MemorySearch)
}

func isTodoToolID(toolID string) bool {
	switch strings.TrimSpace(toolID) {
	case string(toolstd.TodoCreate),
		string(toolstd.TodoUpdate),
		string(toolstd.TodoDelete),
		string(toolstd.TodoClaim),
		string(toolstd.TodoMove),
		string(toolstd.TodoComplete),
		string(toolstd.TodoCancel),
		string(toolstd.TodoReopen),
		string(toolstd.TodoList),
		string(toolstd.IdeaCreate),
		string(toolstd.IdeaUpdate),
		string(toolstd.IdeaDelete),
		string(toolstd.IdeaArchive),
		string(toolstd.IdeaRestore),
		string(toolstd.IdeaTransition),
		string(toolstd.IdeaVote),
		string(toolstd.IdeaConvertToTodo),
		string(toolstd.IdeaList):
		return true
	default:
		return false
	}
}

func isKnowledgeToolID(toolID string) bool {
	switch strings.TrimSpace(toolID) {
	case string(toolstd.KnowledgeSearch), string(toolstd.KnowledgeAdd):
		return true
	default:
		return false
	}
}

func isWebToolID(toolID string) bool {
	switch strings.TrimSpace(toolID) {
	case string(toolstd.WebSearch), string(toolstd.WebFetch):
		return true
	default:
		return false
	}
}

func webToolRequest(req CallToolRequest) WebToolRequest {
	return WebToolRequest{
		Query:         firstStringArg(req.Arguments, "query"),
		URL:           firstStringArg(req.Arguments, "url"),
		Limit:         int(floatArg(req.Arguments, "limit")),
		MaxCharacters: int(floatArg(req.Arguments, "max_characters", "maxCharacters")),
	}
}

func isEmailToolID(toolID string) bool {
	return strings.TrimSpace(toolID) == string(toolstd.EmailSend)
}

func isPersonalAssistantShellToolID(toolID string) bool {
	return strings.TrimSpace(toolID) == string(toolstd.PersonalAssistantShellExec)
}

func isEnterpriseGraphToolID(toolID string) bool {
	switch strings.TrimSpace(toolID) {
	case string(toolstd.EnterpriseGraphBuildContextBundle), string(toolstd.EnterpriseGraphSearchObjects):
		return true
	default:
		return false
	}
}

func isOntologyToolID(toolID string) bool {
	switch strings.TrimSpace(toolID) {
	case string(toolstd.OntologyGroundedContext), string(toolstd.OntologyDocumentInference), string(toolstd.OntologyInfer), string(toolstd.OntologyInferenceWorkflow), string(toolstd.OntologyInferenceRules), string(toolstd.OntologyProposalCreate),
		string(toolstd.OntologyProposalDecide),
		string(toolstd.OntologyPromotionPlanCreate),
		string(toolstd.OntologyPromotionApply),
		string(toolstd.OntologyProjectionRefreshRetry):
		return true
	default:
		return false
	}
}

func ontologyToolRequest(req CallToolRequest) OntologyToolRequest {
	inference, _ := json.Marshal(req.Arguments)
	return OntologyToolRequest{
		InferenceJSON:    inference,
		TenantID:         firstNonBlank(req.TenantID, firstStringArg(req.Arguments, "tenant_id", "tenantId")),
		PrincipalID:      strings.TrimSpace(req.PrincipalID),
		AgentID:          strings.TrimSpace(firstNonBlank(req.AgentID, req.ProfileID)),
		Permissions:      append([]string(nil), req.Permissions...),
		Engine:           firstStringArg(req.Arguments, "engine"),
		Model:            firstStringArg(req.Arguments, "model"),
		ModelVersion:     firstStringArg(req.Arguments, "model_version", "modelVersion"),
		RunID:            firstStringArg(req.Arguments, "run_id", "runId"),
		InputHash:        firstStringArg(req.Arguments, "input_hash", "inputHash"),
		PromptHash:       firstStringArg(req.Arguments, "prompt_hash", "promptHash"),
		SourceRef:        firstStringArg(req.Arguments, "source_ref", "sourceRef"),
		SourceVersion:    firstStringArg(req.Arguments, "source_version", "sourceVersion"),
		Candidates:       ontologyProposalCandidatesArg(req.Arguments, "candidates"),
		ProposalID:       firstStringArg(req.Arguments, "proposal_id", "proposalId"),
		ExpectedRevision: int64(intArg(req.Arguments, "expected_revision")),
		Decision:         firstStringArg(req.Arguments, "decision"),
		Reason:           firstStringArg(req.Arguments, "reason"),
		EvidenceRefs:     ontologyEvidenceRefsArg(req.Arguments, "evidence_refs"),
		PlanID:           firstStringArg(req.Arguments, "plan_id", "planId"),
		ReceiptID:        firstStringArg(req.Arguments, "receipt_id", "receiptId"),
		IdempotencyKey:   firstStringArg(req.Arguments, "idempotency_key", "idempotencyKey"),
		Confirmed:        boolArg(req.Arguments, "confirmed"),
	}
}

func ontologyProposalCandidatesArg(arguments map[string]any, name string) []OntologyProposalCandidate {
	raw, ok := arguments[name].([]any)
	if !ok {
		return nil
	}
	out := make([]OntologyProposalCandidate, 0, len(raw))
	for _, item := range raw {
		fields, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, OntologyProposalCandidate{
			TargetKind: firstStringArg(fields, "target_kind", "targetKind"), TargetID: firstStringArg(fields, "target_id", "targetId"),
			MetadataKey: firstStringArg(fields, "metadata_key", "metadataKey"), ProposedValue: stringArgPreserve(fields, "proposed_value"),
			Confidence: floatArg(fields, "confidence"), Reason: firstStringArg(fields, "reason"),
			EvidenceRefs: ontologyEvidenceRefsArg(fields, "evidence_refs"),
		})
	}
	return out
}

func ontologyEvidenceRefsArg(arguments map[string]any, name string) []OntologyEvidenceRef {
	raw, ok := arguments[name].([]any)
	if !ok {
		return nil
	}
	out := make([]OntologyEvidenceRef, 0, len(raw))
	for _, item := range raw {
		fields, ok := item.(map[string]any)
		if !ok {
			continue
		}
		metadata := map[string]string{}
		if values, ok := fields["metadata"].(map[string]any); ok {
			for key, value := range values {
				if text, ok := value.(string); ok {
					metadata[strings.TrimSpace(key)] = strings.TrimSpace(text)
				}
			}
		}
		out = append(out, OntologyEvidenceRef{Kind: firstStringArg(fields, "kind"), ID: firstStringArg(fields, "id"), Metadata: metadata})
	}
	return out
}

func isSkillToolID(toolID string) bool {
	switch strings.TrimSpace(toolID) {
	case string(toolstd.SkillList),
		string(toolstd.SkillLoad),
		string(toolstd.SkillResourceList),
		string(toolstd.SkillResourceLoad):
		return true
	default:
		return false
	}
}

func enterpriseGraphBuildContextBundleRequest(req CallToolRequest) contextconsumer.BuildContextBundleInput {
	return contextconsumer.BuildContextBundleInput{
		TenantID:         firstNonBlank(req.TenantID, firstStringArg(req.Arguments, "tenant_id", "tenantId")),
		Consumer:         firstStringArg(req.Arguments, "consumer"),
		Purpose:          firstStringArg(req.Arguments, "purpose"),
		ActorRef:         refArg(req.Arguments, "actor_ref", "actorRef"),
		SubjectRefs:      refSliceArg(req.Arguments, "subject_refs", "subjectRefs"),
		Query:            firstStringArg(req.Arguments, "query", "q"),
		IncludeKinds:     stringSliceArg(req.Arguments, "include_kinds", "includeKinds"),
		MaxDepth:         intArg(req.Arguments, "max_depth"),
		TokenBudget:      intArg(req.Arguments, "token_budget"),
		PermissionScopes: append([]string(nil), req.Permissions...),
	}
}

func enterpriseGraphSearchObjectsRequest(req CallToolRequest) contextconsumer.SearchObjectsInput {
	return contextconsumer.SearchObjectsInput{
		TenantID:         firstNonBlank(req.TenantID, firstStringArg(req.Arguments, "tenant_id", "tenantId")),
		ActorRef:         refArg(req.Arguments, "actor_ref", "actorRef"),
		Query:            firstStringArg(req.Arguments, "query", "q"),
		IncludeKinds:     stringSliceArg(req.Arguments, "include_kinds", "includeKinds"),
		Limit:            intArg(req.Arguments, "limit"),
		Cursor:           firstStringArg(req.Arguments, "cursor"),
		PermissionScopes: append([]string(nil), req.Permissions...),
		PrincipalKey:     firstNonBlank(req.ProfileID, req.AgentID, req.PrincipalID),
	}
}

func validateEnterpriseGraphArguments(toolID string, arguments map[string]any) error {
	allowed := map[string]bool{}
	switch strings.TrimSpace(toolID) {
	case string(toolstd.EnterpriseGraphBuildContextBundle):
		for _, field := range []string{"tenant_id", "consumer", "purpose", "actor_ref", "subject_refs", "include_kinds", "max_depth", "token_budget"} {
			allowed[field] = true
		}
	case string(toolstd.EnterpriseGraphSearchObjects):
		for _, field := range []string{"tenant_id", "actor_ref", "query", "include_kinds", "limit", "cursor"} {
			allowed[field] = true
		}
	default:
		return ErrToolNotFound
	}
	for field := range arguments {
		if !allowed[field] {
			return fmt.Errorf("%w: unsupported enterprise graph argument %q", ErrInvalidRequest, field)
		}
	}
	return nil
}

func searchObjectsOutput(result contextconsumer.SearchObjectsResult) map[string]any {
	items := make([]any, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, map[string]any{
			"ref":   map[string]any{"kind": item.Ref.Kind, "id": item.Ref.ID},
			"label": item.Label,
		})
	}
	return map[string]any{"items": items, "next_cursor": result.NextCursor}
}

func knowledgeToolRequest(req CallToolRequest) KnowledgeToolRequest {
	return KnowledgeToolRequest{
		TenantID:         strings.TrimSpace(req.TenantID),
		PrincipalID:      strings.TrimSpace(req.PrincipalID),
		PrincipalKind:    strings.TrimSpace(req.PrincipalKind),
		AgentID:          strings.TrimSpace(firstNonBlank(req.AgentID, req.ProfileID)),
		Query:            firstStringArg(req.Arguments, "query", "q"),
		RetrievalMode:    firstStringArg(req.Arguments, "retrieval_mode", "retrievalMode"),
		KnowledgeBaseIDs: stringSliceArg(req.Arguments, "knowledge_base_ids", "knowledgeBaseIds"),
		KnowledgeBaseID:  firstStringArg(req.Arguments, "knowledge_base_id", "knowledgeBaseId"),
		OwnerType:        firstStringArg(req.Arguments, "owner_type", "ownerType"),
		OwnerID:          firstStringArg(req.Arguments, "owner_id", "ownerId"),
		Limit:            intArg(req.Arguments, "limit"),
		Title:            firstStringArg(req.Arguments, "title"),
		Content:          firstStringArg(req.Arguments, "content", "text"),
		Filename:         firstStringArg(req.Arguments, "filename", "file_name", "fileName"),
		ContentType:      firstStringArg(req.Arguments, "content_type", "contentType"),
	}
}

func memorySearchToolRequest(req CallToolRequest) MemorySearchToolRequest {
	return MemorySearchToolRequest{
		TenantID:       strings.TrimSpace(req.TenantID),
		PrincipalID:    strings.TrimSpace(req.PrincipalID),
		PrincipalKind:  strings.TrimSpace(req.PrincipalKind),
		AgentID:        strings.TrimSpace(firstNonBlank(req.AgentID, req.ProfileID)),
		LogicalAgentID: strings.TrimSpace(req.LogicalAgentID),
		Query:          firstStringArg(req.Arguments, "query", "q"),
	}
}

func emailToolRequest(req CallToolRequest) EmailToolRequest {
	return EmailToolRequest{
		TenantID:       strings.TrimSpace(req.TenantID),
		PrincipalID:    strings.TrimSpace(req.PrincipalID),
		AgentID:        strings.TrimSpace(req.AgentID),
		ProfileID:      strings.TrimSpace(req.ProfileID),
		To:             stringSliceArg(req.Arguments, "to"),
		Cc:             stringSliceArg(req.Arguments, "cc"),
		Subject:        firstStringArg(req.Arguments, "subject"),
		Text:           stringArgPreserve(req.Arguments, "text"),
		HTML:           stringArgPreserve(req.Arguments, "html"),
		IdempotencyKey: firstStringArg(req.Arguments, "idempotency_key", "idempotencyKey"),
	}
}

func evaluationToolRequest(req CallToolRequest) EvaluationToolRequest {
	return EvaluationToolRequest{
		PrincipalID:        strings.TrimSpace(req.PrincipalID),
		AgentID:            strings.TrimSpace(firstNonBlank(req.AgentID, req.ProfileID)),
		Mode:               firstStringArg(req.Arguments, "mode"),
		TargetAgentID:      firstStringArg(req.Arguments, "agent_id", "target_agent_id", "targetAgentId"),
		EvaluatorAgentID:   firstStringArg(req.Arguments, "evaluator_agent_id", "evaluatorAgentId"),
		Summary:            firstStringArg(req.Arguments, "summary", "conclusion"),
		EstimatedHumanDays: floatArg(req.Arguments, "estimated_human_days", "estimatedHumanDays"),
		WindowDays:         intArg(req.Arguments, "window_days"),
		IdempotencyKey:     firstStringArg(req.Arguments, "idempotency_key", "idempotencyKey"),
		ReviewInputs:       evaluationReviewInputArgs(req.Arguments),
	}
}

func memoryDiaryToolRequest(req CallToolRequest) MemoryDiaryToolRequest {
	return MemoryDiaryToolRequest{
		TenantID:          strings.TrimSpace(req.TenantID),
		PrincipalID:       strings.TrimSpace(req.PrincipalID),
		AgentID:           strings.TrimSpace(firstNonBlank(req.AgentID, req.ProfileID)),
		Mode:              firstStringArg(req.Arguments, "mode"),
		TargetAgentID:     firstStringArg(req.Arguments, "agent_id", "target_agent_id", "targetAgentId"),
		Date:              firstStringArg(req.Arguments, "date", "diary_date", "diaryDate"),
		Force:             boolArg(req.Arguments, "force"),
		Limit:             intArg(req.Arguments, "limit"),
		IdempotencyKey:    firstStringArg(req.Arguments, "idempotency_key", "idempotencyKey"),
		Path:              firstStringArg(req.Arguments, "path", "diary_path", "diaryPath"),
		Content:           firstStringArg(req.Arguments, "content", "text"),
		SessionID:         firstStringArg(req.Arguments, "session_id", "sessionId"),
		ContextRevision:   firstStringArg(req.Arguments, "context_revision", "contextRevision"),
		Trigger:           firstStringArg(req.Arguments, "trigger"),
		MissionID:         firstStringArg(req.Arguments, "mission_id", "missionId"),
		TaskID:            firstStringArg(req.Arguments, "task_id", "taskId"),
		RunID:             firstStringArg(req.Arguments, "run_id", "runId"),
		Sensitivity:       firstStringArg(req.Arguments, "sensitivity"),
		SourceSensitivity: firstStringArg(req.Arguments, "source_sensitivity", "sourceSensitivity"),
		EvidenceRefs:      stringSliceArg(req.Arguments, "evidence_refs", "evidenceRefs"),
		LineageRefs:       stringSliceArg(req.Arguments, "lineage_refs", "lineageRefs"),
	}
}

func personalAssistantShellRequest(req CallToolRequest) PersonalAssistantShellRequest {
	return PersonalAssistantShellRequest{
		PrincipalID:       strings.TrimSpace(req.PrincipalID),
		AgentID:           strings.TrimSpace(firstNonBlank(req.AgentID, req.ProfileID)),
		Command:           firstStringArg(req.Arguments, "command", "cmd"),
		Cwd:               firstStringArg(req.Arguments, "cwd", "working_directory"),
		WorkspaceRoot:     firstStringArg(req.Arguments, "workspace_root", "workspaceRoot"),
		WorkingDirectory:  firstStringArg(req.Arguments, "runtime_working_directory", "workingDirectory"),
		TimeoutMs:         intArg(req.Arguments, "timeout_ms"),
		MaxOutputLen:      intArg(req.Arguments, "max_output_len"),
		EnvironmentPolicy: firstStringArg(req.Arguments, "environment_policy", "environmentPolicy"),
	}
}

func evaluationReviewInputArgs(args map[string]any) []EvaluationToolReviewInput {
	value, ok := args["reviews"]
	if !ok {
		value, ok = args["review_inputs"]
	}
	if !ok {
		return nil
	}
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]EvaluationToolReviewInput, 0, len(raw))
	for _, item := range raw {
		fields, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, EvaluationToolReviewInput{
			AgentID:            firstStringArg(fields, "agent_id", "target_agent_id", "targetAgentId"),
			Summary:            firstStringArg(fields, "summary", "conclusion"),
			EstimatedHumanDays: floatArg(fields, "estimated_human_days", "estimatedHumanDays"),
		})
	}
	return out
}

func skillRuntimeRequest(req CallToolRequest) skillres.RuntimeSkillRequest {
	return skillres.RuntimeSkillRequest{
		SkillBindings: append([]agentcore.RuntimeSkillBinding(nil), req.SkillBindings...),
		SkillID:       firstStringArg(req.Arguments, "skill_id", "id", "name"),
		Path:          firstStringArg(req.Arguments, "path", "resource_path"),
		Query:         firstStringArg(req.Arguments, "query"),
	}
}

func contextBundleOutput(bundle contextgraph.ContextBundle) (map[string]any, error) {
	raw, err := json.Marshal(bundle)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func refArg(args map[string]any, names ...string) contextgraph.Ref {
	for _, name := range names {
		value, ok := args[name]
		if !ok {
			continue
		}
		return refFromAny(value)
	}
	return contextgraph.Ref{}
}

func refSliceArg(args map[string]any, names ...string) []contextgraph.Ref {
	for _, name := range names {
		value, ok := args[name]
		if !ok {
			continue
		}
		switch v := value.(type) {
		case []contextgraph.Ref:
			return append([]contextgraph.Ref(nil), v...)
		case []any:
			out := make([]contextgraph.Ref, 0, len(v))
			for _, item := range v {
				if ref := refFromAny(item); ref.Kind != "" || ref.ID != "" {
					out = append(out, ref)
				}
			}
			return out
		default:
			ref := refFromAny(v)
			if ref.Kind == "" && ref.ID == "" {
				return nil
			}
			return []contextgraph.Ref{ref}
		}
	}
	return nil
}

func refFromAny(value any) contextgraph.Ref {
	switch v := value.(type) {
	case contextgraph.Ref:
		return v
	case map[string]any:
		return contextgraph.Ref{
			Kind: strings.TrimSpace(fmt.Sprint(v["kind"])),
			ID:   strings.TrimSpace(fmt.Sprint(v["id"])),
		}
	case map[string]string:
		return contextgraph.Ref{
			Kind: strings.TrimSpace(v["kind"]),
			ID:   strings.TrimSpace(v["id"]),
		}
	default:
		var ref contextgraph.Ref
		raw, err := json.Marshal(value)
		if err != nil {
			return contextgraph.Ref{}
		}
		if err := json.Unmarshal(raw, &ref); err != nil {
			return contextgraph.Ref{}
		}
		ref.Kind = strings.TrimSpace(ref.Kind)
		ref.ID = strings.TrimSpace(ref.ID)
		return ref
	}
}

func floatArg(args map[string]any, names ...string) float64 {
	for _, name := range names {
		value, ok := args[name]
		if !ok {
			continue
		}
		switch v := value.(type) {
		case float64:
			return v
		case float32:
			return float64(v)
		case int:
			return float64(v)
		case int64:
			return float64(v)
		case json.Number:
			parsed, _ := v.Float64()
			return parsed
		default:
			var out float64
			_, _ = fmt.Sscan(fmt.Sprint(value), &out)
			return out
		}
	}
	return 0
}

func stringSliceArg(args map[string]any, names ...string) []string {
	for _, name := range names {
		value, ok := args[name]
		if !ok {
			continue
		}
		switch v := value.(type) {
		case []string:
			return append([]string(nil), v...)
		case []any:
			out := make([]string, 0, len(v))
			for _, item := range v {
				if text := strings.TrimSpace(fmt.Sprint(item)); text != "" {
					out = append(out, text)
				}
			}
			return out
		case string:
			if strings.TrimSpace(v) == "" {
				return nil
			}
			parts := strings.Split(v, ",")
			out := make([]string, 0, len(parts))
			for _, part := range parts {
				if trimmed := strings.TrimSpace(part); trimmed != "" {
					out = append(out, trimmed)
				}
			}
			return out
		}
	}
	return nil
}

func intArg(args map[string]any, name string) int {
	value, ok := args[name]
	if !ok {
		return 0
	}
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	case json.Number:
		parsed, _ := v.Int64()
		return int(parsed)
	default:
		var out int
		_, _ = fmt.Sscan(fmt.Sprint(value), &out)
		return out
	}
}

func boolArg(args map[string]any, name string) bool {
	value, ok := args[name]
	if !ok {
		return false
	}
	switch v := value.(type) {
	case bool:
		return v
	case string:
		text := strings.TrimSpace(v)
		return strings.EqualFold(text, "true") || strings.EqualFold(text, "yes") || text == "1"
	case int:
		return v != 0
	case int64:
		return v != 0
	case float64:
		return v != 0
	case float32:
		return v != 0
	case json.Number:
		parsed, _ := v.Int64()
		return parsed != 0
	default:
		text := strings.TrimSpace(fmt.Sprint(value))
		return strings.EqualFold(text, "true") || text == "1"
	}
}

func firstStringArg(args map[string]any, names ...string) string {
	for _, name := range names {
		value, ok := args[name]
		if !ok {
			continue
		}
		switch v := value.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		case fmt.Stringer:
			if text := strings.TrimSpace(v.String()); text != "" {
				return text
			}
		default:
			if value != nil {
				text := strings.TrimSpace(fmt.Sprint(value))
				if text != "" {
					return text
				}
			}
		}
	}
	return ""
}

func stringArgPreserve(args map[string]any, name string) string {
	value, ok := args[name]
	if !ok || value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func auditEventForRequest(req CallToolRequest, source ToolSource, definition toolres.ToolDefinition, downstreamToolName string, outcome AuditOutcome, reason string) AuditEvent {
	event := AuditEvent{
		TenantID:           strings.TrimSpace(req.TenantID),
		RunID:              strings.TrimSpace(req.RunID),
		ToolID:             strings.TrimSpace(req.ToolID),
		PrincipalID:        strings.TrimSpace(req.PrincipalID),
		CorrelationID:      auditCorrelationID(req.Arguments),
		Source:             source,
		Outcome:            outcome,
		Reason:             reason,
		DownstreamToolName: downstreamToolName,
	}
	if responsibility := req.ResponsibilityContext; responsibility != nil {
		event.ResponsibilityContextID = strings.TrimSpace(responsibility.ContextID)
		event.ResponsibilityID = strings.TrimSpace(responsibility.ResponsibilityID)
		event.ResponsibilityPolicyID = strings.TrimSpace(responsibility.PolicyID)
		event.ResponsibilityPolicyVersion = responsibility.PolicyVersion
	}
	if envelope := req.ExecutionEnvelope; envelope != nil {
		event.ExecutionEnvelopeID = strings.TrimSpace(envelope.EnvelopeID)
		event.ExecutionEnvelopeDigest = strings.TrimSpace(envelope.EnvelopeDigest)
	}
	if definition.ID != "" {
		event.ServerToolID = string(definition.ID)
	}
	return event
}

func auditCorrelationID(arguments map[string]any) string {
	value, ok := arguments["correlation_id"].(string)
	if !ok {
		return ""
	}
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return ""
	}
	for index := 0; index < len(value); index++ {
		char := value[index]
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') {
			continue
		}
		switch char {
		case '-', '_', '.', ':', '/':
			continue
		default:
			return ""
		}
	}
	return value
}

func (r *ToolRouter) record(ctx context.Context, event AuditEvent) {
	if r == nil || r.audit == nil {
		return
	}
	event.OccurredAt = r.now().UTC()
	_ = r.audit.RecordMCPToolAudit(ctx, event)
}

func reasonForError(err error) string {
	switch {
	case errors.Is(err, ErrToolNotFound):
		return "tool_not_found"
	case errors.Is(err, ErrPermissionDenied):
		return "permission_denied"
	default:
		return err.Error()
	}
}

func cloneArguments(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
