package main

import (
	"context"
	"errors"
	"strings"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/app/ontologyscope"
	knowledgecore "orrery_v2/internal/knowledge/core"
)

type contextScopeBindingState struct {
	binding ontologyscope.ScopeBinding
	found   bool
}

func contextAccessRefRequiresTenantMatch(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "idea", "todo", "issue_comment", "action_run", "scheduled_task", "agent_run", "action_writeback":
		return true
	default:
		return false
	}
}

func contextKnowledgeAccessRef(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "knowledge_base", "knowledge_document", "knowledge_chunk":
		return true
	default:
		return false
	}
}

func (p productionContextAccessPolicy) authorizeKnowledgeRefs(ctx context.Context, principal identity.Principal, refs []datavisibility.AccessRef) (map[datavisibility.AccessRef]bool, error) {
	decisions := make(map[datavisibility.AccessRef]bool, len(refs))
	for _, ref := range refs {
		decisions[ref] = false
	}
	if len(refs) == 0 {
		return decisions, nil
	}
	hasKnowledgeRead := principalHasAnyScope(principal, knowledgeadmin.PermissionKnowledgeRead, knowledgeadmin.PermissionKnowledgeManage)
	if !hasKnowledgeRead && !ontologyReadScopeGranted(principal) {
		return decisions, nil
	}

	documentIDs := make([]knowledgecore.KnowledgeDocumentID, 0)
	chunkIDs := make([]knowledgecore.KnowledgeChunkID, 0)
	baseForRef := make(map[datavisibility.AccessRef]knowledgecore.KnowledgeBaseID, len(refs))
	for _, ref := range refs {
		switch ref.Kind {
		case "knowledge_base":
			baseForRef[ref] = knowledgecore.KnowledgeBaseID(ref.ID)
		case "knowledge_document":
			documentIDs = append(documentIDs, knowledgecore.KnowledgeDocumentID(ref.ID))
		case "knowledge_chunk":
			chunkIDs = append(chunkIDs, knowledgecore.KnowledgeChunkID(ref.ID))
		}
	}
	documents, err := p.knowledge.GetDocuments(ctx, documentIDs)
	if err != nil {
		return nil, err
	}
	chunks, err := p.knowledge.GetChunks(ctx, chunkIDs)
	if err != nil {
		return nil, err
	}
	for _, ref := range refs {
		switch ref.Kind {
		case "knowledge_document":
			if document, found := documents[knowledgecore.KnowledgeDocumentID(ref.ID)]; found {
				baseForRef[ref] = document.KnowledgeBaseID
			}
		case "knowledge_chunk":
			if chunk, found := chunks[knowledgecore.KnowledgeChunkID(ref.ID)]; found {
				baseForRef[ref] = chunk.KnowledgeBaseID
			}
		}
	}

	baseIDs := make([]knowledgecore.KnowledgeBaseID, 0, len(baseForRef))
	seenBaseIDs := make(map[knowledgecore.KnowledgeBaseID]struct{}, len(baseForRef))
	for _, baseID := range baseForRef {
		if _, exists := seenBaseIDs[baseID]; exists {
			continue
		}
		seenBaseIDs[baseID] = struct{}{}
		baseIDs = append(baseIDs, baseID)
	}
	basesByID, err := p.knowledge.GetKnowledgeBases(ctx, baseIDs)
	if err != nil {
		return nil, err
	}
	bases := make([]knowledgecore.KnowledgeBase, 0, len(basesByID))
	for _, base := range basesByID {
		bases = append(bases, base)
	}
	readable := map[knowledgecore.KnowledgeBaseID]bool{}
	if hasKnowledgeRead {
		subject := contextKnowledgeSubject(principal)
		readable = p.knowledgeACL.CanReadBases(ctx, subject, bases)
	}
	for _, ref := range refs {
		baseID, found := baseForRef[ref]
		if !found {
			continue
		}
		base, found := basesByID[baseID]
		if !found || contextKnowledgeTenantID(base) != ref.TenantID {
			continue
		}
		if contextPublicKnowledgeGraphReadAllowed(principal, ref.TenantID, base) {
			decisions[ref] = true
			continue
		}
		if !hasKnowledgeRead {
			continue
		}
		if readable[baseID] {
			decisions[ref] = true
			continue
		}
		ownerID := strings.TrimSpace(base.OwnerID)
		if base.OwnerType != knowledgecore.OwnerOrganization || ownerID == "" {
			continue
		}
		allowed, err := p.authorizeTrustedProjectionScope(ctx, principal, ref.TenantID)
		if err != nil {
			return nil, err
		}
		if allowed {
			// The single-ref path adds this base's owner organization to the
			// subject, which makes an active base owner-readable.
			decisions[ref] = base.Status == knowledgecore.KnowledgeActive
		}
	}
	return decisions, nil
}

func contextAccessRefCacheKey(principal identity.Principal, revision string, ref datavisibility.AccessRef) string {
	return strings.Join([]string{"context-access", authorization.PrincipalCacheKey(principal), strings.TrimSpace(revision), ref.Kind, ref.ID, ref.TenantID}, "\x00")
}

func cachedContextAccessRef(memo *datavisibility.RequestMemo, principal identity.Principal, revision string, ref datavisibility.AccessRef) (contextAccessRefState, bool) {
	value, ok := memo.Load(contextAccessRefCacheKey(principal, revision, ref))
	if !ok {
		return contextAccessRefState{}, false
	}
	state, ok := value.(contextAccessRefState)
	return state, ok && state.set
}

func storeContextAccessRef(memo *datavisibility.RequestMemo, principal identity.Principal, revision string, ref datavisibility.AccessRef, state contextAccessRefState) {
	if state.set {
		state.check = -1
		memo.Store(contextAccessRefCacheKey(principal, revision, ref), state)
	}
}

func (p productionContextAccessPolicy) authorizeScopeBindingRef(ctx context.Context, principal identity.Principal, ref datavisibility.AccessRef) (bool, *authorization.CheckRequest, bool, error) {
	binding, found, err := p.resolveScopeBinding(ctx, ref.TenantID)
	if err != nil {
		return false, nil, true, err
	}
	if !found {
		return false, nil, false, nil
	}
	binding = ontologyscope.NormalizeBinding(binding)
	if binding.State != ontologyscope.BindingStateActive || binding.SourceObject.Type != ref.Kind || binding.SourceObject.ID != ref.ID {
		return false, nil, false, nil
	}
	object := binding.SourceObject
	object.TenantID = binding.AuthorizationTenantID
	check := productionOntologyObjectAuthorizationCheck(principal, binding.ReadAction, object, binding.AuthorizationTenantID)
	return false, &check, true, nil
}

func (p productionContextAccessPolicy) resolveScopeBinding(ctx context.Context, tenantID string) (ontologyscope.ScopeBinding, bool, error) {
	tenantID = strings.TrimSpace(tenantID)
	key := "context-scope-binding\x00" + tenantID
	memo := datavisibility.RequestMemoFromContext(ctx)
	if value, ok := memo.Load(key); ok {
		if state, valid := value.(contextScopeBindingState); valid {
			return state.binding, state.found, nil
		}
	}
	binding, err := p.scopes.Resolver.Resolve(ctx, tenantID)
	if errors.Is(err, ontologyscope.ErrBindingNotFound) || errors.Is(err, ontologyscope.ErrInvalidBinding) {
		memo.Store(key, contextScopeBindingState{})
		return ontologyscope.ScopeBinding{}, false, nil
	}
	if err != nil {
		return ontologyscope.ScopeBinding{}, false, err
	}
	binding = ontologyscope.NormalizeBinding(binding)
	memo.Store(key, contextScopeBindingState{binding: binding, found: true})
	return binding, true, nil
}

func (p productionContextAccessPolicy) prefetchScopeBindings(ctx context.Context, tenantIDs []string) error {
	unique := make([]string, 0, len(tenantIDs))
	seen := make(map[string]struct{}, len(tenantIDs))
	for _, tenantID := range tenantIDs {
		tenantID = strings.TrimSpace(tenantID)
		if tenantID == "" {
			continue
		}
		if _, exists := seen[tenantID]; exists {
			continue
		}
		seen[tenantID] = struct{}{}
		unique = append(unique, tenantID)
	}
	if len(unique) == 0 {
		return nil
	}

	bindings, err := p.scopes.Resolver.ResolveMany(ctx, unique)
	if err != nil {
		return err
	}
	memo := datavisibility.RequestMemoFromContext(ctx)
	for _, tenantID := range unique {
		binding, found := bindings[tenantID]
		memo.Store("context-scope-binding\x00"+tenantID, contextScopeBindingState{binding: binding, found: found})
	}
	return nil
}
