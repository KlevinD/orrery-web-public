package core

import (
	"fmt"
	"strings"
	"time"

	missioncore "orrery_v2/internal/mission/core"
)

type ContextPackBuildRequest struct {
	ContextPackID           ContextPackID
	RunID                   AgentRunID
	MissionID               string
	ConversationID          string
	DiscussionGroupID       DiscussionGroupID
	DiscussionGroupGoal     string
	MissionFinalGoal        string
	AgentID                 AgentID
	AgentOrganization       AgentOrgIdentity
	AgentMissionRole        string
	WorkerPersonalGoal      string
	ResponsibilityContext   *ResponsibilityContext
	Task                    TaskInput
	PersonalMemoryDigest    MemoryDigestRef
	CollectiveMemoryDigests []MemoryDigestRef
	DiscussionDigest        DiscussionDigestRef
	Tools                   []RuntimeToolBinding
	Skills                  []RuntimeSkillBinding
	PermissionConstraints   []string
	SourceRefs              []ContextSourceRef
	RawMemory               string
	RawDiscussionMessages   []string
	CreatedAt               time.Time
	ExpiresAt               time.Time
}

type ContextPackPolicyProvider interface {
	ApplyContextPolicy(req ContextPackBuildRequest) (ContextPackPolicyDecision, error)
}

type ContextPackPolicyDecision struct {
	PermissionConstraints []string
	SourceRefs            []ContextSourceRef
	OverridePolicy        OverridePolicy
}

type ContextPackBuilder struct {
	now    func() time.Time
	policy ContextPackPolicyProvider
}

func NewContextPackBuilder() ContextPackBuilder {
	return ContextPackBuilder{now: func() time.Time { return time.Now().UTC() }}
}

func NewContextPackBuilderWithPolicy(policy ContextPackPolicyProvider) ContextPackBuilder {
	builder := NewContextPackBuilder()
	builder.policy = policy
	return builder
}

func (b ContextPackBuilder) Build(req ContextPackBuildRequest) (ContextPack, error) {
	if strings.TrimSpace(req.RawMemory) != "" {
		return ContextPack{}, fmt.Errorf("raw memory is forbidden; provide memory digest refs")
	}
	if len(req.RawDiscussionMessages) > 0 {
		return ContextPack{}, fmt.Errorf("raw discussion messages are forbidden; provide discussion digest refs")
	}
	now := req.CreatedAt
	if now.IsZero() {
		now = b.now()
	}
	workerGoal := firstNonEmpty(req.WorkerPersonalGoal, req.Task.WorkerPersonalGoal)
	sourceRefs := append([]ContextSourceRef(nil), req.SourceRefs...)
	permissionConstraints := append([]string(nil), req.PermissionConstraints...)
	overridePolicy := OverridePolicyPreserveProtected
	if b.policy != nil {
		decision, err := b.policy.ApplyContextPolicy(req)
		if err != nil {
			return ContextPack{}, err
		}
		permissionConstraints = append(permissionConstraints, decision.PermissionConstraints...)
		sourceRefs = append(sourceRefs, decision.SourceRefs...)
		if decision.OverridePolicy != "" {
			overridePolicy = decision.OverridePolicy
		}
	}
	sourceRefs = appendDefaultSourceRefs(sourceRefs, req)
	pack := ContextPack{
		ID:                              firstContextPackID(req.ContextPackID, req.RunID),
		MissionID:                       missioncore.MissionID(firstNonEmpty(req.MissionID, req.Task.MissionID)),
		ConversationID:                  strings.TrimSpace(req.ConversationID),
		DiscussionGroupID:               req.DiscussionGroupID,
		AgentID:                         req.AgentID,
		TaskID:                          missioncore.TaskID(req.Task.ID),
		MissionFinalGoal:                req.MissionFinalGoal,
		DiscussionGroupGoal:             req.DiscussionGroupGoal,
		AgentOrganizationIdentity:       req.AgentOrganization,
		AgentMissionRole:                req.AgentMissionRole,
		WorkerPersonalGoal:              workerGoal,
		ResponsibilityContext:            CloneResponsibilityContext(req.ResponsibilityContext),
		TaskContractRef:                 TaskContractRef{ID: req.Task.ID},
		TaskContractSummary:             buildTaskSummary(req.Task),
		AcceptanceCriteriaRefs:          acceptanceRefs(req.Task.AcceptanceCriteria),
		AcceptanceCriteria:              append([]TaskAcceptanceInput(nil), req.Task.AcceptanceCriteria...),
		RequiredEvidenceRefs:            evidenceRefs(req.Task.EvidenceRequirements),
		RequiredEvidence:                append([]TaskEvidenceInput(nil), req.Task.EvidenceRequirements...),
		PersonalMemoryDigestRef:         req.PersonalMemoryDigest,
		PersonalMemoryDigestSummary:     req.PersonalMemoryDigest.SummaryRef,
		CollectiveMemoryDigestRefs:      append([]MemoryDigestRef(nil), req.CollectiveMemoryDigests...),
		CollectiveMemoryDigestSummaries: memorySummaries(req.CollectiveMemoryDigests),
		DiscussionDigestRef:             req.DiscussionDigest,
		DiscussionDigestSummary:         req.DiscussionDigest.SummaryRef,
		ToolSummaries:                   toolSummaries(req.Tools),
		SkillSummaries:                  skillSummaries(req.Skills),
		PermissionConstraints:           permissionConstraints,
		PermissionScope:                 strings.Join(permissionConstraints, "\n"),
		ToolScope:                       buildToolScope(req.Tools),
		SourceRefs:                      sourceRefs,
		OverridePolicy:                  overridePolicy,
		CreatedAt:                       now,
		ExpiresAt:                       req.ExpiresAt,
	}
	if errs := ValidateContextPack(pack); len(errs) > 0 {
		return ContextPack{}, fmt.Errorf("context pack invalid: %v", errs)
	}
	return pack, nil
}

func firstContextPackID(id ContextPackID, runID AgentRunID) ContextPackID {
	if id != "" {
		return id
	}
	if runID != "" {
		return ContextPackID("ctx-" + runID.String())
	}
	return "ctx-generated"
}

func appendDefaultSourceRefs(refs []ContextSourceRef, req ContextPackBuildRequest) []ContextSourceRef {
	if req.Task.ID != "" && !hasSource(refs, ContextSourceKindTaskContract, req.Task.ID) {
		refs = append(refs, ContextSourceRef{
			Kind:      ContextSourceKindTaskContract,
			Ref:       ContextSourceTypedRef{TaskContractRef: &TaskContractRef{ID: req.Task.ID}},
			Priority:  SourcePriorityContract,
			Immutable: true,
		})
	}
	for _, criterion := range req.Task.AcceptanceCriteria {
		if criterion.ID == "" || hasSource(refs, ContextSourceKindAcceptance, criterion.ID) {
			continue
		}
		refs = append(refs, ContextSourceRef{
			Kind:      ContextSourceKindAcceptance,
			Ref:       ContextSourceTypedRef{AcceptanceRef: &AcceptanceCriterionRef{ID: criterion.ID}},
			Priority:  SourcePriorityContract,
			Immutable: true,
		})
	}
	if req.PersonalMemoryDigest.ID != "" && !hasSource(refs, ContextSourceKindMemoryDigest, string(req.PersonalMemoryDigest.ID)) {
		ref := req.PersonalMemoryDigest
		refs = append(refs, ContextSourceRef{Kind: ContextSourceKindMemoryDigest, Ref: ContextSourceTypedRef{MemoryDigestRef: &ref}, Priority: SourcePriorityMemory})
	}
	for _, digest := range req.CollectiveMemoryDigests {
		if digest.ID == "" || hasSource(refs, ContextSourceKindMemoryDigest, string(digest.ID)) {
			continue
		}
		ref := digest
		refs = append(refs, ContextSourceRef{Kind: ContextSourceKindMemoryDigest, Ref: ContextSourceTypedRef{MemoryDigestRef: &ref}, Priority: SourcePriorityMemory})
	}
	if req.DiscussionDigest.ID != "" && !hasSource(refs, ContextSourceKindDiscussionDigest, string(req.DiscussionDigest.ID)) {
		ref := req.DiscussionDigest
		refs = append(refs, ContextSourceRef{Kind: ContextSourceKindDiscussionDigest, Ref: ContextSourceTypedRef{DiscussionDigestRef: &ref}, Priority: SourcePriorityContext})
	}
	if responsibility := req.ResponsibilityContext; responsibility != nil && strings.TrimSpace(responsibility.ContextID) != "" && !hasSource(refs, ContextSourceKindResponsibility, responsibility.ContextID) {
		refs = append(refs, ContextSourceRef{
			Kind: ContextSourceKindResponsibility,
			Ref: ContextSourceTypedRef{ResponsibilityRef: &ResponsibilityContextRef{ID: responsibility.ContextID}},
			Priority: SourcePriorityContext,
			Immutable: true,
		})
	}
	return refs
}

func hasSource(refs []ContextSourceRef, kind ContextSourceKind, id string) bool {
	for _, ref := range refs {
		if ref.Kind != kind {
			continue
		}
		switch kind {
		case ContextSourceKindTaskContract:
			return ref.Ref.TaskContractRef != nil && ref.Ref.TaskContractRef.ID == id
		case ContextSourceKindAcceptance:
			return ref.Ref.AcceptanceRef != nil && ref.Ref.AcceptanceRef.ID == id
		case ContextSourceKindMemoryDigest:
			return ref.Ref.MemoryDigestRef != nil && string(ref.Ref.MemoryDigestRef.ID) == id
		case ContextSourceKindDiscussionDigest:
			return ref.Ref.DiscussionDigestRef != nil && string(ref.Ref.DiscussionDigestRef.ID) == id
		case ContextSourceKindResponsibility:
			return ref.Ref.ResponsibilityRef != nil && strings.TrimSpace(ref.Ref.ResponsibilityRef.ID) == id
		}
	}
	return false
}

func buildTaskSummary(task TaskInput) string {
	var b strings.Builder
	if task.ID != "" {
		fmt.Fprintf(&b, "Task %s", task.ID)
	}
	if task.Goal != "" {
		if b.Len() > 0 {
			b.WriteString(": ")
		}
		b.WriteString(task.Goal)
	}
	if len(task.ExpectedDeliverables) > 0 {
		b.WriteString("\nExpected deliverables:")
		for _, deliverable := range task.ExpectedDeliverables {
			fmt.Fprintf(&b, "\n- %s: %s", deliverable.ID, firstNonEmpty(deliverable.Title, deliverable.Description))
		}
	}
	return b.String()
}

func acceptanceRefs(criteria []TaskAcceptanceInput) []AcceptanceCriterionRef {
	out := make([]AcceptanceCriterionRef, 0, len(criteria))
	for _, item := range criteria {
		if item.ID != "" {
			out = append(out, AcceptanceCriterionRef{ID: item.ID})
		}
	}
	return out
}

func evidenceRefs(requirements []TaskEvidenceInput) []EvidenceRequirementRef {
	out := make([]EvidenceRequirementRef, 0, len(requirements))
	for _, item := range requirements {
		if item.ID != "" {
			out = append(out, EvidenceRequirementRef{ID: item.ID})
		}
	}
	return out
}

func memorySummaries(refs []MemoryDigestRef) []MemoryDigestSummary {
	out := make([]MemoryDigestSummary, 0, len(refs))
	for _, ref := range refs {
		out = append(out, MemoryDigestSummary{ID: ref.ID, MemoryKind: ref.MemoryKind, Summary: ref.SummaryRef})
	}
	return out
}

func toolSummaries(bindings []RuntimeToolBinding) []ContextToolSummary {
	out := make([]ContextToolSummary, 0, len(bindings))
	for _, binding := range bindings {
		out = append(out, ContextToolSummary{
			ToolID:             binding.ToolID,
			Alias:              binding.Alias,
			Version:            binding.Version,
			Summary:            binding.Summary,
			GrantedPermissions: append([]string(nil), binding.GrantedPermissions...),
			SecretEnv:          cloneStringMap(binding.SecretEnv),
		})
	}
	return out
}

func skillSummaries(bindings []RuntimeSkillBinding) []ContextSkillSummary {
	out := make([]ContextSkillSummary, 0, len(bindings))
	for _, binding := range bindings {
		out = append(out, ContextSkillSummary{SkillID: SkillID(binding.SkillID), Alias: binding.Alias, Version: binding.Version, Summary: binding.Summary})
	}
	return out
}

func buildToolScope(tools []RuntimeToolBinding) string {
	var parts []string
	for _, tool := range tools {
		if tool.Summary != "" {
			parts = append(parts, fmt.Sprintf("%s: %s", tool.ToolID, tool.Summary))
		}
	}
	return strings.Join(parts, "\n")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
