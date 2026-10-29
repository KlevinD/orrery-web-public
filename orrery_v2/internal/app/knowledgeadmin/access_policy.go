package knowledgeadmin

import (
	"context"

	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/store"
)

type AccessSubject struct {
	ViewerID        string
	AgentIDs        []string
	HumanIDs        []string
	OrganizationIDs []string
	PositionIDs     []string
	RoleIDs         []string
}

type AccessPolicy struct {
	repo store.Repository
}

func NewAccessPolicy(repo store.Repository) *AccessPolicy {
	return &AccessPolicy{repo: repo}
}

func (p *AccessPolicy) CanRead(ctx context.Context, subject AccessSubject, base core.KnowledgeBase) bool {
	if base.Status != core.KnowledgeActive {
		return false
	}
	if ownsBase(subject, base) {
		return true
	}
	if p.hasBinding(ctx, subject, base.ID, core.AccessRead) {
		return true
	}
	switch base.Visibility {
	case core.VisibilityPublic:
		return hasAuthenticatedIdentity(subject)
	case core.VisibilityScoped:
		return inOwnerScope(subject, base)
	default:
		return false
	}
}

// CanReadBases evaluates multiple knowledge bases after loading bindings once.
// Repository failures remain fail-closed for binding-derived access while
// preserving owner, public, and scoped visibility behavior.
func (p *AccessPolicy) CanReadBases(ctx context.Context, subject AccessSubject, bases []core.KnowledgeBase) map[core.KnowledgeBaseID]bool {
	if len(bases) == 0 {
		return map[core.KnowledgeBaseID]bool{}
	}
	bindingsByBase := map[core.KnowledgeBaseID][]core.KnowledgeBinding{}
	if p != nil && p.repo != nil {
		bindings, err := p.repo.ListBindings(ctx, core.BindingQuery{})
		if err == nil {
			for _, binding := range bindings {
				bindingsByBase[binding.KnowledgeBaseID] = append(bindingsByBase[binding.KnowledgeBaseID], binding)
			}
		}
	}

	allowed := make(map[core.KnowledgeBaseID]bool, len(bases))
	for _, base := range bases {
		if base.Status != core.KnowledgeActive {
			continue
		}
		if ownsBase(subject, base) {
			allowed[base.ID] = true
			continue
		}
		for _, binding := range bindingsByBase[base.ID] {
			if subjectMatchesBinding(subject, binding) && bindingAllows(binding.Access, core.AccessRead) {
				allowed[base.ID] = true
				break
			}
		}
		if allowed[base.ID] {
			continue
		}
		switch base.Visibility {
		case core.VisibilityPublic:
			allowed[base.ID] = hasAuthenticatedIdentity(subject)
		case core.VisibilityScoped:
			allowed[base.ID] = inOwnerScope(subject, base)
		}
	}
	return allowed
}

func (p *AccessPolicy) CanWrite(ctx context.Context, subject AccessSubject, base core.KnowledgeBase) bool {
	if base.Status != core.KnowledgeActive {
		return false
	}
	if ownsBase(subject, base) {
		return true
	}
	if base.OwnerType == core.OwnerGlobal && base.Visibility == core.VisibilityPublic && hasAuthenticatedIdentity(subject) {
		return true
	}
	return p.hasBinding(ctx, subject, base.ID, core.AccessWrite)
}

func (p *AccessPolicy) CanManage(ctx context.Context, subject AccessSubject, base core.KnowledgeBase) bool {
	if ownsBase(subject, base) {
		return true
	}
	if base.OwnerType == core.OwnerGlobal && containsIdentity(subject.RoleIDs, "superadmin") {
		return true
	}
	return p.hasBinding(ctx, subject, base.ID, core.AccessManage)
}

func (p *AccessPolicy) hasBinding(ctx context.Context, subject AccessSubject, baseID core.KnowledgeBaseID, required core.AccessLevel) bool {
	if p == nil || p.repo == nil {
		return false
	}
	bindings, err := p.repo.ListBindings(ctx, core.BindingQuery{KnowledgeBaseID: baseID})
	if err != nil {
		return false
	}
	for _, binding := range bindings {
		if subjectMatchesBinding(subject, binding) && bindingAllows(binding.Access, required) {
			return true
		}
	}
	return false
}

func ownsBase(subject AccessSubject, base core.KnowledgeBase) bool {
	switch base.OwnerType {
	case core.OwnerAgent:
		return containsIdentity(subject.AgentIDs, base.OwnerID)
	case core.OwnerHuman:
		return containsIdentity(subject.HumanIDs, base.OwnerID)
	case core.OwnerOrganization:
		return containsIdentity(subject.OrganizationIDs, base.OwnerID)
	case core.OwnerPosition:
		return containsIdentity(subject.PositionIDs, base.OwnerID)
	default:
		return false
	}
}

func inOwnerScope(subject AccessSubject, base core.KnowledgeBase) bool {
	if base.OwnerType != core.OwnerOrganization {
		return ownsBase(subject, base)
	}
	return containsIdentity(subject.OrganizationIDs, base.OwnerID)
}

func subjectMatchesBinding(subject AccessSubject, binding core.KnowledgeBinding) bool {
	switch binding.BoundToType {
	case core.BindingSubjectAgent:
		return containsIdentity(subject.AgentIDs, binding.BoundToID)
	case core.BindingSubjectHuman:
		return containsIdentity(subject.HumanIDs, binding.BoundToID)
	case core.BindingSubjectOrganization:
		return containsIdentity(subject.OrganizationIDs, binding.BoundToID)
	case core.BindingSubjectPosition:
		return containsIdentity(subject.PositionIDs, binding.BoundToID)
	case core.BindingSubjectRole:
		return containsIdentity(subject.RoleIDs, binding.BoundToID)
	default:
		return false
	}
}

func hasAuthenticatedIdentity(subject AccessSubject) bool {
	return subject.ViewerID != "" ||
		len(subject.AgentIDs) > 0 ||
		len(subject.HumanIDs) > 0 ||
		len(subject.OrganizationIDs) > 0 ||
		len(subject.PositionIDs) > 0 ||
		len(subject.RoleIDs) > 0
}

func bindingAllows(actual core.AccessLevel, required core.AccessLevel) bool {
	if actual == core.AccessManage {
		return true
	}
	if actual == core.AccessWrite {
		return required == core.AccessWrite || required == core.AccessRead
	}
	return actual == core.AccessRead && required == core.AccessRead
}

func containsIdentity(values []string, want string) bool {
	if want == "" {
		return false
	}
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
