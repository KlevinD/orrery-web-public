package task

import (
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
)

type TaskID = missioncore.TaskID
type MissionAssignmentID string
type ExpectedDeliverableID string
type ContractField string

const SourceKindResponsibilityAutoplan = "responsibility_autoplan"

type TaskContract struct {
	ID                        TaskID
	MissionID                 missioncore.MissionID
	Assignee                  TaskAssigneeRef
	Title                     string
	Goal                      string
	SourceKind                string
	SourceID                  string
	SourceStepID              string
	WorkerPersonalGoal        string
	WorkerPersonalGoalSources []WorkerPersonalGoalSourceRef
	Scope                     string
	Priority                  string
	Inputs                    []InputRef
	ExpectedDeliverables      []ExpectedDeliverable
	AcceptanceCriteria        []AcceptanceCriterion
	EvidenceRequirements      []EvidenceRequirement
	QAEnabled                 bool
	QAPlanRequired            bool
	QAPlanCompleted           bool
	QAPlanRef                 QAPlanID
	QAPlanArtifactRef         agentcore.ArtifactRef
	QAPlanIdempotencyKey      string
	QACheckRequired           bool
	Dependencies              []TaskDependencyRef
	Constraints               []Constraint
	ExecutionPermissionScopes []string
	ExecutionAllowedToolIDs   []string
	ExecutionEnvelope         *agentcore.ExecutionEnvelope
	RequireExplicitToolScopes bool
	DuePolicy                 DuePolicy
	ContextPolicy             ContextPolicy
}

// RequiresExplicitToolScopes keeps autonomous-plan tasks fail-closed when a
// planner did not grant a task-specific tool scope. The source-kind fallback
// preserves the boundary for plans materialized before the durable flag was
// introduced.
func (c TaskContract) RequiresExplicitToolScopes() bool {
	return c.RequireExplicitToolScopes || strings.EqualFold(strings.TrimSpace(c.SourceKind), SourceKindResponsibilityAutoplan)
}

type TaskAssigneeRef struct {
	AgentProfileRef      *agentcore.AgentProfileRef
	MissionAssignmentRef *MissionAssignmentRef
	ToolRef              *agentcore.ToolRef
}

func (a TaskAssigneeRef) IsAgentAssignable() bool {
	return a.agentAssigneeCount() == 1
}

func (a TaskAssigneeRef) HasToolRef() bool {
	return a.ToolRef != nil && a.ToolRef.ToolID != ""
}

func (a TaskAssigneeRef) agentAssigneeCount() int {
	count := 0
	if a.AgentProfileRef != nil && !a.AgentProfileRef.IsZero() {
		count++
	}
	if a.MissionAssignmentRef != nil && a.MissionAssignmentRef.ID != "" {
		count++
	}
	return count
}

type MissionAssignmentRef struct {
	ID MissionAssignmentID
}

type InputKind string

const (
	InputKindDocument InputKind = "document"
	InputKindArtifact InputKind = "artifact"
	InputKindURL      InputKind = "url"
)

func (k InputKind) IsValid() bool {
	switch k {
	case InputKindDocument, InputKindArtifact, InputKindURL:
		return true
	default:
		return false
	}
}

type InputRef struct {
	ID   string
	Kind InputKind
}

type ExpectedDeliverable struct {
	ID          ExpectedDeliverableID
	Title       string
	Description string
}

type ExpectedDeliverableRef struct {
	ID ExpectedDeliverableID
}

type WorkerPersonalGoalSourceKind string

const (
	WorkerPersonalGoalSourceGoal                WorkerPersonalGoalSourceKind = "goal"
	WorkerPersonalGoalSourceScope               WorkerPersonalGoalSourceKind = "scope"
	WorkerPersonalGoalSourceExpectedDeliverable WorkerPersonalGoalSourceKind = "expected_deliverable"
)

func (k WorkerPersonalGoalSourceKind) IsValid() bool {
	switch k {
	case WorkerPersonalGoalSourceGoal, WorkerPersonalGoalSourceScope, WorkerPersonalGoalSourceExpectedDeliverable:
		return true
	default:
		return false
	}
}

type ContractFieldRef struct {
	Field ContractField
}

const (
	ContractFieldGoal  ContractField = "goal"
	ContractFieldScope ContractField = "scope"
)

func (r ContractFieldRef) IsZero() bool {
	return r.Field == ""
}

type WorkerPersonalGoalSourceRef struct {
	Kind                   WorkerPersonalGoalSourceKind
	ContractFieldRef       ContractFieldRef
	ExpectedDeliverableRef ExpectedDeliverableRef
}

func (r WorkerPersonalGoalSourceRef) IsValid() bool {
	if !r.Kind.IsValid() {
		return false
	}
	switch r.Kind {
	case WorkerPersonalGoalSourceGoal:
		return r.ContractFieldRef.Field == ContractFieldGoal
	case WorkerPersonalGoalSourceScope:
		return r.ContractFieldRef.Field == ContractFieldScope
	case WorkerPersonalGoalSourceExpectedDeliverable:
		return r.ExpectedDeliverableRef.ID != ""
	default:
		return false
	}
}

type TaskDependencyRef struct {
	TaskID TaskID
}

type Constraint struct {
	Description string
}

type DuePolicy struct {
	Policy DuePolicyKind
	DueAt  time.Time
}

type DuePolicyKind string

const (
	DuePolicyBestEffort DuePolicyKind = "best_effort"
	DuePolicyFixed      DuePolicyKind = "fixed"
	DuePolicyWindow     DuePolicyKind = "window"
)

func (k DuePolicyKind) IsValid() bool {
	switch k {
	case DuePolicyBestEffort, DuePolicyFixed, DuePolicyWindow:
		return true
	default:
		return false
	}
}

type ContextPolicy struct {
	RequireMissionGoal        bool
	RequireDiscussionGoal     bool
	RequireWorkerPersonalGoal bool
}

func ValidateTaskContract(c TaskContract) []ValidationError {
	var errs []ValidationError
	if c.ID == "" {
		errs = append(errs, validationError(CodeMissingRequiredField, "task_id", "task_id is required"))
	}
	if c.MissionID.IsZero() {
		errs = append(errs, validationError(CodeMissingRequiredField, "mission_id", "mission_id is required"))
	}
	hasAgentAssignee := c.Assignee.IsAgentAssignable()
	if c.Assignee.HasToolRef() {
		errs = append(errs, validationError(CodeToolRefNotAgent, "assignee", "tool refs cannot be task assignees"))
	} else if c.Assignee.agentAssigneeCount() > 1 {
		errs = append(errs, validationError(CodeInvalidRef, "assignee", "assignee must identify exactly one responsible agent subject"))
	} else if c.Assignee.hasScopedAgentProfileRef() {
		errs = append(errs, validationError(CodeInvalidRef, "assignee.agent_profile_ref", "direct task assignee must be the stable agent profile id; scoped runtime instances belong to execution, not task ownership"))
	}
	if c.Goal == "" {
		errs = append(errs, validationError(CodeMissingRequiredField, "goal", "goal is required"))
	}
	if hasAgentAssignee && c.WorkerPersonalGoal == "" {
		errs = append(errs, validationError(CodeMissingRequiredField, "worker_personal_goal", "worker_personal_goal is required"))
	}
	if c.WorkerPersonalGoal != "" && len(c.WorkerPersonalGoalSources) == 0 {
		errs = append(errs, validationError(CodeMissingRequiredField, "worker_personal_goal_sources", "worker_personal_goal must declare derivation sources"))
	}
	deliverableIDs := expectedDeliverableIDs(c.ExpectedDeliverables)
	for i, source := range c.WorkerPersonalGoalSources {
		if !source.IsValid() {
			errs = append(errs, validationError(CodeInvalidRef, indexedField("worker_personal_goal_sources", i, "ref"), "worker_personal_goal source must reference goal, scope, or expected deliverable"))
			continue
		}
		if source.Kind == WorkerPersonalGoalSourceExpectedDeliverable && !deliverableIDs[source.ExpectedDeliverableRef.ID] {
			errs = append(errs, validationError(CodeInvalidRef, indexedField("worker_personal_goal_sources", i, "expected_deliverable_id"), "worker_personal_goal source must reference an expected deliverable from this contract"))
		}
	}
	for i, input := range c.Inputs {
		if input.ID == "" {
			errs = append(errs, validationError(CodeInvalidRef, indexedField("inputs", i, "id"), "input id is required"))
		}
		if !input.Kind.IsValid() {
			errs = append(errs, validationError(CodeInvalidRef, indexedField("inputs", i, "kind"), "input kind must be known"))
		}
	}
	if len(c.ExpectedDeliverables) == 0 {
		errs = append(errs, validationError(CodeMissingDeliverable, "expected_deliverables", "at least one expected deliverable is required"))
	}
	for i, deliverable := range c.ExpectedDeliverables {
		if deliverable.ID == "" {
			errs = append(errs, validationError(CodeInvalidRef, indexedField("expected_deliverables", i, "id"), "expected deliverable id is required"))
		}
		if deliverable.Title == "" {
			errs = append(errs, validationError(CodeMissingRequiredField, indexedField("expected_deliverables", i, "title"), "expected deliverable title is required"))
		}
	}
	for i, criterion := range c.AcceptanceCriteria {
		if criterion.ID == "" {
			errs = append(errs, validationError(CodeInvalidRef, indexedField("acceptance_criteria", i, "id"), "acceptance criterion id is required"))
		}
		if criterion.Description == "" {
			errs = append(errs, validationError(CodeMissingRequiredField, indexedField("acceptance_criteria", i, "description"), "acceptance criterion description is required"))
		}
	}
	for i, requirement := range c.EvidenceRequirements {
		if requirement.ID == "" {
			errs = append(errs, validationError(CodeInvalidRef, indexedField("evidence_requirements", i, "id"), "evidence requirement id is required"))
		}
		if requirement.Description == "" {
			errs = append(errs, validationError(CodeMissingRequiredField, indexedField("evidence_requirements", i, "description"), "evidence requirement description is required"))
		}
		if !requirement.Kind.IsValid() {
			errs = append(errs, validationError(CodeInvalidRef, indexedField("evidence_requirements", i, "kind"), "evidence requirement kind must be known"))
		}
	}
	seenPermissionScopes := make(map[string]bool, len(c.ExecutionPermissionScopes))
	for i, scope := range c.ExecutionPermissionScopes {
		scope = strings.TrimSpace(scope)
		if scope == "" || strings.ContainsAny(scope, " \t\r\n") || scope == "*" {
			errs = append(errs, validationError(CodeInvalidRef, indexedField("execution_permission_scopes", i, "scope"), "execution permission scope must be explicit and non-blank"))
			continue
		}
		if seenPermissionScopes[scope] {
			errs = append(errs, validationError(CodeInvalidRef, indexedField("execution_permission_scopes", i, "scope"), "execution permission scope must be unique"))
		}
		seenPermissionScopes[scope] = true
	}
	if c.DuePolicy.Policy != "" && !c.DuePolicy.Policy.IsValid() {
		errs = append(errs, validationError(CodeInvalidRef, "due_policy.policy", "due policy must be known"))
	}
	if (c.DuePolicy.Policy == DuePolicyFixed || c.DuePolicy.Policy == DuePolicyWindow) && c.DuePolicy.DueAt.IsZero() {
		errs = append(errs, validationError(CodeMissingRequiredField, "due_policy.due_at", "bounded due policies require due_at"))
	}
	if !c.ContextPolicy.RequireMissionGoal || !c.ContextPolicy.RequireDiscussionGoal || !c.ContextPolicy.RequireWorkerPersonalGoal {
		errs = append(errs, validationError(CodeInvalidRef, "context_policy", "context policy must require mission, discussion, and worker goals"))
	}
	return errs
}

func (a TaskAssigneeRef) hasScopedAgentProfileRef() bool {
	if a.AgentProfileRef == nil || a.AgentProfileRef.IsZero() {
		return false
	}
	return strings.Contains(strings.TrimSpace(a.AgentProfileRef.ID.String()), ":")
}

func expectedDeliverableIDs(deliverables []ExpectedDeliverable) map[ExpectedDeliverableID]bool {
	ids := make(map[ExpectedDeliverableID]bool, len(deliverables))
	for _, deliverable := range deliverables {
		if deliverable.ID != "" {
			ids[deliverable.ID] = true
		}
	}
	return ids
}
