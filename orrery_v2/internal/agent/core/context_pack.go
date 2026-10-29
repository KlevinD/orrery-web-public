package core

import (
	"strings"
	"time"
)

import missioncore "orrery_v2/internal/mission/core"

type DiscussionGroupID string
type UserInstructionID string
type MissionEventID string
type RelevantDecisionRef struct{ ID string }
type RelevantBlockerRef struct{ ID string }
type TaskContractRef struct{ ID string }
type AcceptanceCriterionRef struct{ ID string }
type EvidenceRequirementRef struct{ ID string }
type UserInstructionRef struct{ ID UserInstructionID }
type MissionEventRef struct{ ID MissionEventID }
type ResponsibilityContextRef struct{ ID string }

type ContextSourceRef struct {
	Kind      ContextSourceKind
	Ref       ContextSourceTypedRef
	Priority  SourcePriority
	Immutable bool
}

type ContextSourceTypedRef struct {
	UserInstructionRef  *UserInstructionRef
	TaskContractRef     *TaskContractRef
	AcceptanceRef       *AcceptanceCriterionRef
	MissionEventRef     *MissionEventRef
	MemoryDigestRef     *MemoryDigestRef
	DiscussionDigestRef *DiscussionDigestRef
	DecisionRef         *RelevantDecisionRef
	BlockerRef          *RelevantBlockerRef
	ResponsibilityRef   *ResponsibilityContextRef
}

func (r ContextSourceTypedRef) Matches(kind ContextSourceKind) bool {
	if r.countRefs() != 1 {
		return false
	}
	switch kind {
	case ContextSourceKindUserInstruction:
		return r.UserInstructionRef != nil && r.UserInstructionRef.ID != ""
	case ContextSourceKindTaskContract:
		return r.TaskContractRef != nil && r.TaskContractRef.ID != ""
	case ContextSourceKindAcceptance:
		return r.AcceptanceRef != nil && r.AcceptanceRef.ID != ""
	case ContextSourceKindMissionEvent:
		return r.MissionEventRef != nil && r.MissionEventRef.ID != ""
	case ContextSourceKindMemoryDigest:
		return r.MemoryDigestRef != nil && r.MemoryDigestRef.ID != ""
	case ContextSourceKindDiscussionDigest:
		return r.DiscussionDigestRef != nil && r.DiscussionDigestRef.ID != ""
	case ContextSourceKindDecision:
		return r.DecisionRef != nil && r.DecisionRef.ID != ""
	case ContextSourceKindBlocker:
		return r.BlockerRef != nil && r.BlockerRef.ID != ""
	case ContextSourceKindResponsibility:
		return r.ResponsibilityRef != nil && strings.TrimSpace(r.ResponsibilityRef.ID) != ""
	default:
		return false
	}
}

func (r ContextSourceTypedRef) countRefs() int {
	count := 0
	if r.UserInstructionRef != nil && r.UserInstructionRef.ID != "" {
		count++
	}
	if r.TaskContractRef != nil && r.TaskContractRef.ID != "" {
		count++
	}
	if r.AcceptanceRef != nil && r.AcceptanceRef.ID != "" {
		count++
	}
	if r.MissionEventRef != nil && r.MissionEventRef.ID != "" {
		count++
	}
	if r.MemoryDigestRef != nil && r.MemoryDigestRef.ID != "" {
		count++
	}
	if r.DiscussionDigestRef != nil && r.DiscussionDigestRef.ID != "" {
		count++
	}
	if r.DecisionRef != nil && r.DecisionRef.ID != "" {
		count++
	}
	if r.BlockerRef != nil && r.BlockerRef.ID != "" {
		count++
	}
	if r.ResponsibilityRef != nil && strings.TrimSpace(r.ResponsibilityRef.ID) != "" {
		count++
	}
	return count
}

type ContextSourceKind string

const (
	ContextSourceKindUserInstruction  ContextSourceKind = "user_instruction"
	ContextSourceKindTaskContract     ContextSourceKind = "task_contract"
	ContextSourceKindAcceptance       ContextSourceKind = "acceptance_criterion"
	ContextSourceKindMissionEvent     ContextSourceKind = "mission_event"
	ContextSourceKindMemoryDigest     ContextSourceKind = "memory_digest"
	ContextSourceKindDiscussionDigest ContextSourceKind = "discussion_digest"
	ContextSourceKindDecision         ContextSourceKind = "decision"
	ContextSourceKindBlocker          ContextSourceKind = "blocker"
	ContextSourceKindResponsibility   ContextSourceKind = "responsibility_context"
)

type SourcePriority int

const (
	SourcePriorityBackground      SourcePriority = 10
	SourcePriorityMemory          SourcePriority = 20
	SourcePriorityContext         SourcePriority = 30
	SourcePriorityContract        SourcePriority = 80
	SourcePriorityUserInstruction SourcePriority = 100
)

type OverridePolicy string

const (
	OverridePolicyPreserveProtected    OverridePolicy = "preserve_protected"
	OverridePolicyAllowContextOverride OverridePolicy = "allow_context_override"
)

// ResponsibilityContext is the immutable responsibility snapshot attached to
// one runtime. It is intentionally a bounded projection rather than a copy of
// the People service's entire organization model.
type ResponsibilityContext struct {
	ContextID                 string    `json:"context_id"`
	ResponsibilityID          string    `json:"responsibility_id"`
	ResponsibilityVersion     int64     `json:"responsibility_version"`
	PolicyID                  string    `json:"policy_id,omitempty"`
	PolicyVersion             int64     `json:"policy_version,omitempty"`
	Source                    string    `json:"source"`
	ActorMemberID             string    `json:"actor_member_id"`
	AgentProfileID            string    `json:"agent_profile_id"`
	MissionID                 string    `json:"mission_id"`
	TaskID                    string    `json:"task_id"`
	OrganizationID            string    `json:"organization_id"`
	OrganizationName          string    `json:"organization_name"`
	PositionID                string    `json:"position_id"`
	PositionName              string    `json:"position_name"`
	Summary                   string    `json:"summary"`
	Goals                     []string  `json:"goals,omitempty"`
	Boundaries                []string  `json:"boundaries,omitempty"`
	AllowedTools              []string  `json:"allowed_tools,omitempty"`
	AllowedDataScopes         []string  `json:"allowed_data_scopes,omitempty"`
	ForbiddenActions          []string  `json:"forbidden_actions,omitempty"`
	HumanInLoopTriggers       []string  `json:"human_in_loop_triggers,omitempty"`
	PolicyToolScopes          []string  `json:"policy_tool_scopes,omitempty"`
	PolicyDataScopes          []string  `json:"policy_data_scopes,omitempty"`
	TaskPermissionScopes      []string  `json:"task_permission_scopes,omitempty"`
	RequireExplicitToolScopes bool      `json:"require_explicit_tool_scopes,omitempty"`
	EvidenceRequirements      []string  `json:"evidence_requirements,omitempty"`
	SourceRefs                []string  `json:"source_refs,omitempty"`
	CapturedAt                time.Time `json:"captured_at,omitempty"`
	EffectiveAt               time.Time `json:"effective_at,omitempty"`
	ExpiresAt                 time.Time `json:"expires_at,omitempty"`
}

func CloneResponsibilityContext(value *ResponsibilityContext) *ResponsibilityContext {
	if value == nil {
		return nil
	}
	clone := *value
	clone.Goals = append([]string(nil), value.Goals...)
	clone.Boundaries = append([]string(nil), value.Boundaries...)
	clone.AllowedTools = append([]string(nil), value.AllowedTools...)
	clone.AllowedDataScopes = append([]string(nil), value.AllowedDataScopes...)
	clone.ForbiddenActions = append([]string(nil), value.ForbiddenActions...)
	clone.HumanInLoopTriggers = append([]string(nil), value.HumanInLoopTriggers...)
	clone.PolicyToolScopes = append([]string(nil), value.PolicyToolScopes...)
	clone.PolicyDataScopes = append([]string(nil), value.PolicyDataScopes...)
	clone.TaskPermissionScopes = append([]string(nil), value.TaskPermissionScopes...)
	clone.EvidenceRequirements = append([]string(nil), value.EvidenceRequirements...)
	clone.SourceRefs = append([]string(nil), value.SourceRefs...)
	return &clone
}

type ContextPack struct {
	ID                              ContextPackID
	MissionID                       missioncore.MissionID
	ConversationID                  string
	DiscussionGroupID               DiscussionGroupID
	AgentID                         AgentID
	MemoryOwnerAgentID              AgentID
	TaskID                          missioncore.TaskID
	MissionFinalGoal                string
	DiscussionGroupGoal             string
	AgentOrganizationIdentity       AgentOrgIdentity
	AgentMissionRole                string
	WorkerPersonalGoal              string
	ResponsibilityContext           *ResponsibilityContext
	TaskContractRef                 TaskContractRef
	TaskContractSummary             string
	AcceptanceCriteriaRefs          []AcceptanceCriterionRef
	AcceptanceCriteria              []TaskAcceptanceInput
	RequiredEvidenceRefs            []EvidenceRequirementRef
	RequiredEvidence                []TaskEvidenceInput
	PersonalMemoryDigestRef         MemoryDigestRef
	PersonalMemoryDigestSummary     string
	CollectiveMemoryDigestRefs      []MemoryDigestRef
	CollectiveMemoryDigestSummaries []MemoryDigestSummary
	DiscussionDigestRef             DiscussionDigestRef
	DiscussionDigestSummary         string
	ToolSummaries                   []ContextToolSummary
	SkillSummaries                  []ContextSkillSummary
	PermissionConstraints           []string
	RelevantDecisionRefs            []RelevantDecisionRef
	RelevantBlockerRefs             []RelevantBlockerRef
	PermissionScope                 string
	ToolScope                       string
	SourceRefs                      []ContextSourceRef
	OverridePolicy                  OverridePolicy
	RawMemory                       string
	RawDiscussionMessages           []string
	CreatedAt                       time.Time
	ExpiresAt                       time.Time
}

type MemoryDigestSummary struct {
	ID         MemoryDigestID
	MemoryKind MemoryKind
	Summary    string
}

type ContextToolSummary struct {
	ToolID             ToolID
	Alias              string
	Version            string
	Summary            string
	GrantedPermissions []string
	SecretEnv          map[string]string
}

type ContextSkillSummary struct {
	SkillID SkillID
	Alias   string
	Version string
	Summary string
}

func ValidateContextPack(p ContextPack) []ValidationError {
	var errs []ValidationError
	if p.MissionFinalGoal == "" {
		errs = append(errs, validationError(CodeMissingRequiredField, "mission_final_goal", "mission_final_goal is required"))
	}
	if p.DiscussionGroupGoal == "" {
		errs = append(errs, validationError(CodeMissingRequiredField, "discussion_group_goal", "discussion_group_goal is required"))
	}
	if p.TaskID != "" && p.WorkerPersonalGoal == "" {
		errs = append(errs, validationError(CodeMissingRequiredField, "worker_personal_goal", "worker_personal_goal is required for worker tasks"))
	}
	if len(p.SourceRefs) == 0 {
		errs = append(errs, validationError(CodeContextSourceRequired, "source_refs", "source_refs are required for audit"))
	}
	if responsibility := p.ResponsibilityContext; responsibility != nil {
		if strings.TrimSpace(responsibility.ContextID) == "" || strings.TrimSpace(responsibility.ResponsibilityID) == "" || responsibility.ResponsibilityVersion < 1 {
			errs = append(errs, validationError(CodeInvalidRef, "responsibility_context", "responsibility context requires context_id, responsibility_id, and a positive responsibility_version"))
		}
		if strings.TrimSpace(responsibility.PolicyID) == "" || responsibility.PolicyVersion < 1 {
			errs = append(errs, validationError(CodeInvalidRef, "responsibility_context.policy", "responsibility policy id and positive version are required"))
		}
		if strings.TrimSpace(responsibility.AgentProfileID) == "" {
			errs = append(errs, validationError(CodeInvalidRef, "responsibility_context.agent_profile_id", "responsibility context requires agent_profile_id"))
		}
	}
	for i, ref := range p.SourceRefs {
		if !ref.Ref.Matches(ref.Kind) {
			errs = append(errs, validationError(CodeInvalidRef, indexedField("source_refs", i, "ref"), "source ref must match its source kind"))
		}
	}
	if p.OverridePolicy == OverridePolicyAllowContextOverride && hasProtectedSource(p.SourceRefs) {
		errs = append(errs, validationError(CodeInvalidRef, "override_policy", "context cannot override protected user instructions or contracts"))
	}
	if !hasRequiredProtectedPrecedence(p.SourceRefs) {
		errs = append(errs, validationError(CodeInvalidRef, "source_refs", "protected context sources require immutable contract/user precedence"))
	}
	if p.RawMemory != "" {
		errs = append(errs, validationError(CodeMemoryDigestRequired, "raw_memory", "memory must enter context as digest refs"))
	}
	if len(p.RawDiscussionMessages) > 0 {
		errs = append(errs, validationError(CodeMemoryDigestRequired, "raw_discussion_messages", "discussion history must enter context as digest refs"))
	}
	return errs
}

func hasProtectedSource(refs []ContextSourceRef) bool {
	for _, ref := range refs {
		if ref.Kind.IsProtected() {
			return true
		}
	}
	return false
}

func hasRequiredProtectedPrecedence(refs []ContextSourceRef) bool {
	for _, ref := range refs {
		if !ref.Kind.IsProtected() {
			continue
		}
		if !ref.Immutable {
			return false
		}
		switch ref.Kind {
		case ContextSourceKindUserInstruction:
			if ref.Priority < SourcePriorityUserInstruction {
				return false
			}
		case ContextSourceKindTaskContract, ContextSourceKindAcceptance, ContextSourceKindMissionEvent:
			if ref.Priority < SourcePriorityContract {
				return false
			}
		}
	}
	return true
}

func (k ContextSourceKind) IsProtected() bool {
	switch k {
	case ContextSourceKindUserInstruction, ContextSourceKindTaskContract, ContextSourceKindAcceptance, ContextSourceKindMissionEvent:
		return true
	default:
		return false
	}
}
