package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datapolicy"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/app/memoryadmin"
	"orrery_v2/internal/app/ontologyscope"
	contextview "orrery_v2/internal/app/ontologyview/context"
	knowledgecore "orrery_v2/internal/knowledge/core"
	knowledgestore "orrery_v2/internal/knowledge/store"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform/db"
)

type productionContextAccessPolicy struct {
	scopes       *productionOntologyScopeState
	authorizer   *authorization.Service
	knowledge    *knowledgestore.PlatformStore
	memory       *memstore.PlatformStore
	knowledgeACL *knowledgeadmin.AccessPolicy
	records      *db.Router
	actions      *actioncontract.PlatformRepository
	snapshots    authorization.SnapshotProvider
}

type productionContextDataPolicy struct {
	provider  *datapolicy.PlatformStore
	revisions datavisibility.PolicyRevisionSource
}

func newProductionContextAccessPolicy(
	core *db.Store,
	records *db.Router,
	scopes *productionOntologyScopeState,
	authorizer *authorization.Service,
	knowledge *knowledgestore.PlatformStore,
	memory *memstore.PlatformStore,
) productionContextAccessPolicy {
	return productionContextAccessPolicy{
		scopes: scopes, authorizer: authorizer, knowledge: knowledge, memory: memory,
		knowledgeACL: knowledgeadmin.NewAccessPolicy(knowledge), records: records,
		actions: actioncontract.NewPlatformRepository(core), snapshots: scopes.Snapshots,
	}
}

type contextAccessRefState struct {
	allowed bool
	check   int
	set     bool
}

func newProductionOntologyContextAndExecutionAPIs(core *db.Store, records *db.Router, security *productionOntologySecurityState, authorizer *authorization.Service, knowledge *knowledgestore.PlatformStore, memory *memstore.PlatformStore) (*contextview.Service, *productionOntologyExecutionAPI, error) {
	if security == nil {
		return nil, nil, fmt.Errorf("ontology security state is required")
	}
	contextAPI, err := newProductionOntologyContextAPI(core, records, security.Scopes, security.Cursors, security.Concurrency, security.PolicyStore, authorizer, knowledge, memory)
	if err != nil {
		return nil, nil, err
	}
	return contextAPI, newProductionOntologyExecutionAPI(contextAPI), nil
}

func (p productionContextAccessPolicy) Evaluate(ctx context.Context, req datavisibility.AdapterRequest) (datavisibility.AdapterDecision, error) {
	if p.scopes == nil || p.scopes.Resolver == nil || p.scopes.Gate == nil || p.authorizer == nil || p.knowledge == nil || p.memory == nil || p.knowledgeACL == nil || p.records == nil || p.actions == nil || p.snapshots == nil {
		return datavisibility.AdapterDecision{}, datavisibility.ErrDependencyUnavailable
	}
	ctx = datavisibility.WithRequestMemo(ctx)
	principal := identity.NormalizePrincipal(req.Principal)
	beforeRevision, err := p.authorizationRevision(ctx, principal)
	if err != nil {
		return datavisibility.AdapterDecision{}, err
	}
	refs := make(map[datavisibility.AccessRef]*contextAccessRefState)
	ordered := make([]datavisibility.AccessRef, 0)
	memo := datavisibility.RequestMemoFromContext(ctx)
	for _, resource := range req.Resources {
		for _, ref := range resource.AccessRefs {
			ref.Kind = strings.ToLower(strings.TrimSpace(ref.Kind))
			ref.ID = strings.TrimSpace(ref.ID)
			ref.TenantID = strings.TrimSpace(ref.TenantID)
			if _, exists := refs[ref]; !exists {
				refs[ref] = &contextAccessRefState{check: -1}
				ordered = append(ordered, ref)
			}
		}
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Kind != ordered[j].Kind {
			return ordered[i].Kind < ordered[j].Kind
		}
		if ordered[i].ID != ordered[j].ID {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].TenantID < ordered[j].TenantID
	})
	checks := make([]authorization.CheckRequest, 0, len(ordered))
	pendingActionRuns := make([]actioncontract.ActionRunID, 0)
	pendingKnowledgeRefs := make([]datavisibility.AccessRef, 0)
	pendingScopeBindings := make([]string, 0)
	for _, ref := range ordered {
		if cached, ok := cachedContextAccessRef(memo, principal, beforeRevision, ref); ok {
			*refs[ref] = cached
			continue
		}
		if ref.Kind == "action_run" && ontologyReadScopeGranted(principal) {
			pendingActionRuns = append(pendingActionRuns, actioncontract.ActionRunID(ref.ID))
		}
		if contextKnowledgeAccessRef(ref.Kind) && ref.ID != "" && ref.TenantID != "" {
			pendingKnowledgeRefs = append(pendingKnowledgeRefs, ref)
		}
		if ref.Kind == "resource" && ref.TenantID == "system" && ref.ID != "" {
			pendingScopeBindings = append(pendingScopeBindings, "resource:"+ref.ID)
		}
	}
	if err := p.prefetchScopeBindings(ctx, pendingScopeBindings); err != nil {
		return datavisibility.AdapterDecision{}, err
	}
	actionRunTenants, err := p.actions.GetActionRunTenants(ctx, pendingActionRuns)
	if err != nil {
		return datavisibility.AdapterDecision{}, err
	}
	knowledgeDecisions, err := p.authorizeKnowledgeRefs(ctx, principal, pendingKnowledgeRefs)
	if err != nil {
		return datavisibility.AdapterDecision{}, err
	}
	for _, ref := range ordered {
		state := refs[ref]
		if state.set {
			continue
		}
		if contextKnowledgeAccessRef(ref.Kind) {
			allowed, target, matched, err := p.authorizeScopeBindingRef(ctx, principal, ref)
			if err != nil {
				return datavisibility.AdapterDecision{}, err
			}
			if matched {
				state.allowed, state.set = allowed, true
				if target != nil {
					state.check = len(checks)
					checks = append(checks, *target)
				}
				continue
			}
		}
		if allowed, evaluated := knowledgeDecisions[ref]; evaluated {
			state.allowed, state.set = allowed, true
			continue
		}
		if ref.Kind == "action_run" {
			tenantID, found := actionRunTenants[actioncontract.ActionRunID(ref.ID)]
			state.allowed, state.set = found && tenantID == ref.TenantID, true
			continue
		}
		allowed, target, err := p.authorizeRef(ctx, principal, ref)
		if err != nil {
			return datavisibility.AdapterDecision{}, err
		}
		state.allowed, state.set = allowed, true
		if target != nil {
			state.check = len(checks)
			checks = append(checks, *target)
		}
	}
	if len(checks) > 0 {
		decisions, err := p.authorizer.BatchCan(ctx, checks)
		if err != nil {
			return datavisibility.AdapterDecision{}, err
		}
		if len(decisions) != len(checks) {
			return datavisibility.AdapterDecision{}, fmt.Errorf("%w: source authorization returned an incomplete batch", datavisibility.ErrIncompleteDecision)
		}
		for _, ref := range ordered {
			state := refs[ref]
			if state.check < 0 {
				continue
			}
			check := checks[state.check]
			decision := decisions[state.check]
			decisionErr := p.scopes.Authorizer.authorizeObjectDecision(ctx, principal, check.Action, check.Object, decision)
			switch {
			case decisionErr == nil:
				state.allowed = true
			case errors.Is(decisionErr, authorization.ErrDenied), errors.Is(decisionErr, authorization.ErrUnsupported):
				state.allowed = false
			default:
				return datavisibility.AdapterDecision{}, decisionErr
			}
		}
	}
	afterRevision, err := p.authorizationRevision(ctx, principal)
	if err != nil {
		return datavisibility.AdapterDecision{}, err
	}
	if beforeRevision != afterRevision {
		return datavisibility.AdapterDecision{}, fmt.Errorf("%w: authorization revision changed during visibility evaluation", datavisibility.ErrDependencyUnavailable)
	}
	for _, ref := range ordered {
		storeContextAccessRef(memo, principal, afterRevision, ref, *refs[ref])
	}
	out := datavisibility.AdapterDecision{Coverage: datavisibility.CoverageEntity, PolicyRevision: afterRevision, Resources: make([]datavisibility.AdapterResourceDecision, 0, len(req.Resources))}
	for _, resource := range req.Resources {
		hidden := len(resource.AccessRefs) == 0
		for _, ref := range resource.AccessRefs {
			ref.Kind = strings.ToLower(strings.TrimSpace(ref.Kind))
			ref.ID = strings.TrimSpace(ref.ID)
			ref.TenantID = strings.TrimSpace(ref.TenantID)
			state, exists := refs[ref]
			hidden = hidden || !exists || !state.set || !state.allowed
			if contextAccessRefRequiresTenantMatch(ref.Kind) && strings.TrimSpace(resource.TenantID) != ref.TenantID {
				hidden = true
			}
		}
		disclosure := datavisibility.ExistenceVisible
		if hidden {
			disclosure = datavisibility.ExistenceConceal
		}
		out.Resources = append(out.Resources, datavisibility.AdapterResourceDecision{Key: resource.Key, Hidden: hidden, ExistenceDisclosure: disclosure})
	}
	return out, nil
}

func (p productionContextAccessPolicy) authorizationRevision(ctx context.Context, principal identity.Principal) (string, error) {
	principal = identity.NormalizePrincipal(principal)
	principalKey := authorization.PrincipalCacheKey(principal)
	memo := datavisibility.RequestMemoFromContext(ctx)
	cacheKey := "context-authorization-revision\x00" + principalKey
	if value, ok := memo.Load(cacheKey); ok {
		if revision, valid := value.(string); valid && strings.TrimSpace(revision) != "" {
			return revision, nil
		}
	}
	snapshot, err := p.snapshots.Capture(ctx, principal)
	if err != nil {
		return "", err
	}
	revision := strings.TrimSpace(snapshot.AuthorizationRevision)
	if snapshot.PrincipalKey != principalKey {
		return "", datavisibility.ErrDependencyUnavailable
	}
	if memo != nil && revision != "" {
		memo.Store(cacheKey, revision)
		memo.RegisterValidator(cacheKey, func(validateCtx context.Context) error {
			current, validateErr := p.snapshots.Capture(validateCtx, principal)
			if validateErr != nil {
				return validateErr
			}
			if current.PrincipalKey != principalKey || strings.TrimSpace(current.AuthorizationRevision) != revision {
				return fmt.Errorf("%w: authorization revision changed during visibility request", datavisibility.ErrDependencyUnavailable)
			}
			return nil
		})
	}
	return revision, nil
}

func (p productionContextAccessPolicy) authorizeRef(ctx context.Context, principal identity.Principal, ref datavisibility.AccessRef) (bool, *authorization.CheckRequest, error) {
	if strings.TrimSpace(ref.Kind) == "" || strings.TrimSpace(ref.ID) == "" || strings.TrimSpace(ref.TenantID) == "" {
		return false, nil, nil
	}
	if allowed, check, matched, err := p.authorizeScopeBindingRef(ctx, principal, ref); matched || err != nil {
		return allowed, check, err
	}
	switch ref.Kind {
	case "mission", "project", "organization", "resource":
		bindingTenantID := ref.TenantID
		if ref.Kind == "resource" && ref.TenantID == "system" {
			bindingTenantID = "resource:" + ref.ID
		}
		binding, found, err := p.resolveScopeBinding(ctx, bindingTenantID)
		if err != nil {
			return false, nil, err
		}
		if !found {
			return false, nil, nil
		}
		if !contextBindingMatchesRef(binding, ref) {
			return false, nil, nil
		}
		object := binding.SourceObject
		object.TenantID = binding.AuthorizationTenantID
		check := productionOntologyObjectAuthorizationCheck(principal, binding.ReadAction, object, binding.AuthorizationTenantID)
		return false, &check, nil
	case "memory_record":
		record, err := p.memory.GetRecord(ctx, ref.ID)
		if errors.Is(err, memcore.ErrNotFound) {
			return false, nil, nil
		}
		if err != nil {
			return false, nil, err
		}
		if contextMemoryTenantID(record) != ref.TenantID {
			return false, nil, nil
		}
		viewer := contextMemoryViewer(principal)
		if memoryadmin.CanReadRecord(viewer, record) {
			return true, nil, nil
		}
		allowed, err := p.authorizeSourceScope(ctx, principal, ref.TenantID)
		if err != nil || !allowed {
			return false, nil, err
		}
		addPrincipalTenantScopes(ref.TenantID, &viewer)
		return memoryadmin.CanReadRecord(viewer, record), nil, nil
	case "knowledge_base", "knowledge_document", "knowledge_chunk":
		hasKnowledgeRead := principalHasAnyScope(principal, knowledgeadmin.PermissionKnowledgeRead, knowledgeadmin.PermissionKnowledgeManage)
		if !hasKnowledgeRead && !ontologyReadScopeGranted(principal) {
			return false, nil, nil
		}
		base, found, err := p.knowledgeBaseForRef(ctx, ref)
		if err != nil || !found {
			return false, nil, err
		}
		if contextKnowledgeTenantID(base) != ref.TenantID {
			return false, nil, nil
		}
		if contextPublicKnowledgeGraphReadAllowed(principal, ref.TenantID, base) {
			return true, nil, nil
		}
		if !hasKnowledgeRead {
			return false, nil, nil
		}
		subject := contextKnowledgeSubject(principal)
		if p.knowledgeACL.CanRead(ctx, subject, base) {
			return true, nil, nil
		}
		if base.OwnerType != knowledgecore.OwnerOrganization || strings.TrimSpace(base.OwnerID) == "" {
			return false, nil, nil
		}
		allowed, err := p.authorizeSourceScope(ctx, principal, ref.TenantID)
		if err != nil || !allowed {
			return false, nil, err
		}
		subject.OrganizationIDs = append(subject.OrganizationIDs, strings.TrimSpace(base.OwnerID))
		return p.knowledgeACL.CanRead(ctx, subject, base), nil, nil
	case "idea":
		if !principalHasAnyScope(principal, "idea.read") {
			return false, nil, nil
		}
		return p.canReadIdea(ctx, principal, ref)
	case "todo":
		if !principalHasAnyScope(principal, "todo.read") {
			return false, nil, nil
		}
		return p.sourceRecordExists(ctx, "core.todos", ref)
	case "issue_comment":
		return p.canReadIssueComment(ctx, principal, ref)
	case "action_run":
		if !ontologyReadScopeGranted(principal) {
			return false, nil, nil
		}
		run, err := p.actions.GetActionRun(ctx, actioncontract.ActionRunID(ref.ID))
		if errors.Is(err, actioncontract.ErrNotFound) {
			return false, nil, nil
		}
		if err != nil {
			return false, nil, err
		}
		return strings.TrimSpace(run.TenantID) == ref.TenantID, nil, nil
	case "scheduled_task", "agent_run", "action_writeback":
		allowed, err := p.authorizeTrustedProjectionScope(ctx, principal, ref.TenantID)
		return allowed, nil, err
	default:
		return false, nil, nil
	}
}

func (p productionContextAccessPolicy) authorizeTrustedProjectionScope(ctx context.Context, principal identity.Principal, tenantID string) (bool, error) {
	key := strings.Join([]string{"context-trusted-projection-scope", authorization.PrincipalCacheKey(principal), strings.TrimSpace(tenantID)}, "\x00")
	memo := datavisibility.RequestMemoFromContext(ctx)
	if value, ok := memo.Load(key); ok {
		allowed, valid := value.(bool)
		if valid {
			return allowed, nil
		}
	}
	allowed, err := p.authorizeSourceScope(ctx, principal, tenantID)
	if err == nil {
		memo.Store(key, allowed)
	}
	return allowed, err
}

func (p productionContextAccessPolicy) authorizeSourceScope(ctx context.Context, principal identity.Principal, tenantID string) (bool, error) {
	_, err := p.scopes.Gate.AuthorizeRead(ctx, identity.NormalizePrincipal(principal), strings.TrimSpace(tenantID))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ontologyscope.ErrAccessDenied) || errors.Is(err, ontologyscope.ErrBindingNotFound) || errors.Is(err, ontologyscope.ErrInvalidBinding) {
		return false, nil
	}
	return false, err
}

func (p productionContextAccessPolicy) sourceRecord(ctx context.Context, logicalTable string, ref datavisibility.AccessRef) (db.Record, bool, error) {
	record, found, err := p.records.GetRecord(ctx, logicalTable, ref.TenantID+"::"+ref.ID)
	if err != nil || !found {
		return nil, found, err
	}
	if contextRecordString(record, "tenant_id") != ref.TenantID {
		return nil, false, nil
	}
	return record, true, nil
}

func (p productionContextAccessPolicy) sourceRecordExists(ctx context.Context, logicalTable string, ref datavisibility.AccessRef) (bool, *authorization.CheckRequest, error) {
	_, found, err := p.sourceRecord(ctx, logicalTable, ref)
	return found, nil, err
}

func (p productionContextAccessPolicy) canReadIdea(ctx context.Context, principal identity.Principal, ref datavisibility.AccessRef) (bool, *authorization.CheckRequest, error) {
	record, found, err := p.sourceRecord(ctx, "core.ideas", ref)
	if err != nil || !found {
		return false, nil, err
	}
	viewerID := contextPrincipalID(principal)
	var payload struct {
		CreatedBy string `json:"created_by"`
	}
	if raw := contextRecordString(record, "payload"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			return false, nil, fmt.Errorf("decode idea visibility payload: %w", err)
		}
	}
	if viewerID != "" && (viewerID == contextRecordString(record, "owner_id") || viewerID == contextRecordString(record, "reviewer_id") || viewerID == strings.TrimSpace(payload.CreatedBy)) {
		return true, nil, nil
	}
	switch strings.ToLower(contextRecordString(record, "visibility")) {
	case "", "organization":
		organizationID := contextRecordString(record, "organization_id")
		if organizationID == "" {
			return true, nil, nil
		}
		check := productionOntologyObjectAuthorizationCheck(
			principal,
			"organization.read",
			identity.ObjectRef{Type: "organization", ID: organizationID, TenantID: organizationID},
			organizationID,
		)
		return false, &check, nil
	case "department":
		departmentID := contextRecordString(record, "department_id")
		if departmentID == "" {
			return false, nil, nil
		}
		authorizationTenantID := contextRecordString(record, "organization_id")
		if authorizationTenantID == "" {
			authorizationTenantID = ref.TenantID
		}
		check := productionOntologyObjectAuthorizationCheck(
			principal,
			"department.read",
			identity.ObjectRef{Type: "department", ID: departmentID, TenantID: authorizationTenantID},
			authorizationTenantID,
		)
		return false, &check, nil
	case "private":
		return false, nil, nil
	default:
		return false, nil, nil
	}
}

func (p productionContextAccessPolicy) canReadIssueComment(ctx context.Context, principal identity.Principal, ref datavisibility.AccessRef) (bool, *authorization.CheckRequest, error) {
	record, found, err := p.sourceRecord(ctx, "core.issue_comments", ref)
	if err != nil || !found {
		return false, nil, err
	}
	issueID := contextRecordString(record, "issue_id")
	switch contextRecordString(record, "issue_kind") {
	case "idea":
		return p.authorizeRef(ctx, principal, datavisibility.AccessRef{Kind: "idea", ID: issueID, TenantID: ref.TenantID})
	case "todo":
		return p.authorizeRef(ctx, principal, datavisibility.AccessRef{Kind: "todo", ID: issueID, TenantID: ref.TenantID})
	default:
		return false, nil, nil
	}
}

func contextRecordString(record db.Record, key string) string {
	value, _ := record[key].(string)
	return strings.TrimSpace(value)
}

func (p productionContextAccessPolicy) knowledgeBaseForRef(ctx context.Context, ref datavisibility.AccessRef) (knowledgecore.KnowledgeBase, bool, error) {
	var baseID knowledgecore.KnowledgeBaseID
	switch ref.Kind {
	case "knowledge_base":
		baseID = knowledgecore.KnowledgeBaseID(ref.ID)
	case "knowledge_document":
		document, err := p.knowledge.GetDocument(ctx, knowledgecore.KnowledgeDocumentID(ref.ID))
		if errors.Is(err, knowledgecore.ErrNotFound) {
			return knowledgecore.KnowledgeBase{}, false, nil
		}
		if err != nil {
			return knowledgecore.KnowledgeBase{}, false, err
		}
		baseID = document.KnowledgeBaseID
	case "knowledge_chunk":
		chunk, err := p.knowledge.GetChunk(ctx, knowledgecore.KnowledgeChunkID(ref.ID))
		if errors.Is(err, knowledgecore.ErrNotFound) {
			return knowledgecore.KnowledgeBase{}, false, nil
		}
		if err != nil {
			return knowledgecore.KnowledgeBase{}, false, err
		}
		baseID = chunk.KnowledgeBaseID
	}
	base, err := p.knowledge.GetKnowledgeBase(ctx, baseID)
	if errors.Is(err, knowledgecore.ErrNotFound) {
		return knowledgecore.KnowledgeBase{}, false, nil
	}
	return base, err == nil, err
}

func contextBindingMatchesRef(binding ontologyscope.ScopeBinding, ref datavisibility.AccessRef) bool {
	expectedKind := map[string]ontologyscope.Kind{
		"mission": ontologyscope.KindMission, "project": ontologyscope.KindProject,
		"organization": ontologyscope.KindOrganization, "resource": ontologyscope.KindResource,
	}[ref.Kind]
	return expectedKind != "" && binding.Kind == expectedKind && strings.EqualFold(binding.SourceObject.Type, ref.Kind) && binding.SourceObject.ID == ref.ID && binding.State == ontologyscope.BindingStateActive
}

func contextMemoryTenantID(record memcore.MemoryRecord) string {
	scopeID := strings.TrimSpace(record.Scope.ID)
	switch record.Scope.Kind {
	case memcore.ScopeOrganization:
		return scopeID
	case memcore.ScopeDepartment, memcore.ScopeProject, memcore.ScopeMission, memcore.ScopeDiscussion, memcore.ScopeAgent:
		if scopeID != "" {
			return string(record.Scope.Kind) + ":" + scopeID
		}
	}
	return scopeID
}

func contextKnowledgeTenantID(base knowledgecore.KnowledgeBase) string {
	if ownerID := strings.TrimSpace(base.OwnerID); ownerID != "" {
		return ownerID
	}
	return strings.TrimSpace(string(base.ID))
}

func contextPublicKnowledgeGraphReadAllowed(principal identity.Principal, tenantID string, base knowledgecore.KnowledgeBase) bool {
	principal = identity.NormalizePrincipal(principal)
	tenantID = strings.TrimSpace(tenantID)
	return ontologyReadScopeGranted(principal) &&
		contextPrincipalID(principal) != "" &&
		tenantID != "" &&
		strings.TrimSpace(principal.TenantID) == tenantID &&
		contextKnowledgeTenantID(base) == tenantID &&
		base.Status == knowledgecore.KnowledgeActive &&
		base.Visibility == knowledgecore.VisibilityPublic
}

func (p productionContextDataPolicy) Evaluate(ctx context.Context, req datavisibility.AdapterRequest) (datavisibility.AdapterDecision, error) {
	if p.provider == nil || p.revisions == nil {
		return datavisibility.AdapterDecision{}, datavisibility.ErrDependencyUnavailable
	}
	principal := identity.NormalizePrincipal(req.Principal)
	beforeRevision, err := p.policyRevision(ctx, principal)
	if err != nil {
		return datavisibility.AdapterDecision{}, err
	}
	beforeRevision = strings.TrimSpace(beforeRevision)
	type tenantBatch struct {
		objects   []contextgraph.Object
		resources []datavisibility.ResourceDescriptor
	}
	byTenant := map[string]*tenantBatch{}
	aggregateProbes := map[string][]datavisibility.ResourceDescriptor{}
	decisions := map[string]datavisibility.AdapterResourceDecision{}
	for _, resource := range req.Resources {
		if _, ok := resource.Payload.(datavisibility.AggregateProbe); ok {
			aggregateProbes[resource.TenantID] = append(aggregateProbes[resource.TenantID], resource)
			continue
		}
		if resource.Payload == nil && len(resource.FieldPaths) == 0 {
			decisions[resource.Key] = datavisibility.AdapterResourceDecision{Key: resource.Key, ExistenceDisclosure: datavisibility.ExistenceVisible}
			continue
		}
		object := contextgraph.Object{ID: resource.Key, TenantID: resource.TenantID, ObjectType: resource.Type}
		if payload, ok := resource.Payload.(contextgraph.Object); ok {
			object = payload
			object.ID = resource.Key
		}
		batch := byTenant[resource.TenantID]
		if batch == nil {
			batch = &tenantBatch{}
			byTenant[resource.TenantID] = batch
		}
		batch.objects = append(batch.objects, object)
		batch.resources = append(batch.resources, resource)
	}
	tenants := make([]string, 0, len(byTenant))
	for tenantID := range byTenant {
		tenants = append(tenants, tenantID)
	}
	for tenantID := range aggregateProbes {
		if byTenant[tenantID] == nil {
			tenants = append(tenants, tenantID)
		}
	}
	sort.Strings(tenants)
	for _, tenantID := range tenants {
		batch := byTenant[tenantID]
		if batch != nil {
			policy, err := p.provider.DecideContext(ctx, contextgraph.BuildContextBundleRequest{
				TenantID: tenantID, Consumer: "ontology_workbench", Purpose: req.Purpose, ActorRef: schemaPolicyActor(req.Principal),
			}, batch.objects)
			if err != nil {
				return datavisibility.AdapterDecision{}, err
			}
			hidden := stringSet(policy.HiddenObjectIDs)
			for _, resource := range batch.resources {
				_, conceal := hidden[resource.Key]
				masked := contextRequestedMaskedFields(resource.FieldPaths, policy.MaskedFields[resource.Key])
				item := datavisibility.AdapterResourceDecision{
					Key: resource.Key, Hidden: conceal, ExistenceDisclosure: datavisibility.ExistenceVisible,
					AllowedFieldPaths: subtractSchemaFields(resource.FieldPaths, masked), MaskedFieldPaths: masked,
				}
				if conceal {
					item.ExistenceDisclosure, item.AllowedFieldPaths, item.MaskedFieldPaths = datavisibility.ExistenceConceal, nil, nil
				}
				decisions[resource.Key] = item
			}
		}
		if probes := aggregateProbes[tenantID]; len(probes) > 0 {
			hasDeny, err := p.provider.HasActivePurposeDeny(ctx, tenantID, "ontology_workbench", req.Purpose)
			if err != nil {
				return datavisibility.AdapterDecision{}, err
			}
			for _, resource := range probes {
				item := datavisibility.AdapterResourceDecision{Key: resource.Key, Hidden: hasDeny, ExistenceDisclosure: datavisibility.ExistenceVisible}
				if hasDeny {
					item.ExistenceDisclosure = datavisibility.ExistenceConceal
				}
				decisions[resource.Key] = item
			}
		}
	}
	afterRevision, err := p.policyRevision(ctx, principal)
	if err != nil {
		return datavisibility.AdapterDecision{}, err
	}
	if beforeRevision == "" || beforeRevision != strings.TrimSpace(afterRevision) {
		return datavisibility.AdapterDecision{}, fmt.Errorf("%w: data policy revision changed during visibility evaluation", datavisibility.ErrDependencyUnavailable)
	}
	out := datavisibility.AdapterDecision{Coverage: datavisibility.CoverageFilter | datavisibility.CoverageFields, PolicyRevision: beforeRevision}
	for _, resource := range req.Resources {
		out.Resources = append(out.Resources, decisions[resource.Key])
	}
	return out, nil
}

func (p productionContextDataPolicy) policyRevision(ctx context.Context, principal identity.Principal) (string, error) {
	principal = identity.NormalizePrincipal(principal)
	principalKey := authorization.PrincipalCacheKey(principal)
	memo := datavisibility.RequestMemoFromContext(ctx)
	cacheKey := "context-data-policy-revision\x00" + principalKey
	if value, ok := memo.Load(cacheKey); ok {
		if revision, valid := value.(string); valid && strings.TrimSpace(revision) != "" {
			return revision, nil
		}
	}
	revision, err := p.revisions.Revision(ctx, principal)
	if err != nil {
		return "", err
	}
	revision = strings.TrimSpace(revision)
	if revision == "" {
		return "", datavisibility.ErrDependencyUnavailable
	}
	if memo != nil {
		memo.Store(cacheKey, revision)
		memo.RegisterValidator(cacheKey, func(validateCtx context.Context) error {
			current, validateErr := p.revisions.Revision(validateCtx, principal)
			if validateErr != nil {
				return validateErr
			}
			if strings.TrimSpace(current) != revision {
				return fmt.Errorf("%w: data policy revision changed during visibility request", datavisibility.ErrDependencyUnavailable)
			}
			return nil
		})
	}
	return revision, nil
}

func contextRequestedMaskedFields(requested, masked []string) []string {
	maskedSet := stringSet(masked)
	out := make([]string, 0)
	for _, field := range requested {
		property := strings.TrimPrefix(field, "properties.")
		_, exact := maskedSet[field]
		_, legacyProperty := maskedSet[property]
		_, allProperties := maskedSet["properties"]
		if exact || strings.HasPrefix(field, "properties.") && (legacyProperty || allProperties) {
			out = append(out, field)
		}
	}
	sort.Strings(out)
	return out
}

func contextMemoryViewer(principal identity.Principal) memoryadmin.ViewerRef {
	principal = identity.NormalizePrincipal(principal)
	viewer := memoryadmin.ViewerRef{ViewerID: contextPrincipalID(principal), ViewerKind: principal.ViewerKind, PermissionScope: append([]string(nil), principal.CoarseScopes...)}
	if principal.ViewerKind == identity.ViewerKindAgent {
		viewer.AgentID = firstNonBlank(principal.AgentProfileID, principal.Subject)
	}
	addPrincipalTenantScopes(principal.TenantID, &viewer)
	return viewer
}

func contextKnowledgeSubject(principal identity.Principal) knowledgeadmin.AccessSubject {
	principal = identity.NormalizePrincipal(principal)
	subject := knowledgeadmin.AccessSubject{ViewerID: contextPrincipalID(principal)}
	if principal.ViewerKind == identity.ViewerKindAgent {
		subject.AgentIDs = []string{firstNonBlank(principal.AgentProfileID, principal.Subject)}
	} else if id := firstNonBlank(principal.UserID, principal.Subject); id != "" {
		subject.HumanIDs = []string{id}
	}
	if tenantID := strings.TrimSpace(principal.TenantID); tenantID != "" && !strings.Contains(tenantID, ":") {
		subject.OrganizationIDs = []string{tenantID}
	}
	return subject
}

func contextPrincipalID(principal identity.Principal) string {
	return firstNonBlank(principal.UserID, principal.AgentProfileID, principal.Subject)
}

func addPrincipalTenantScopes(tenantID string, viewer *memoryadmin.ViewerRef) {
	if viewer == nil {
		return
	}
	tenantID = strings.TrimSpace(tenantID)
	switch {
	case strings.HasPrefix(tenantID, "mission:"):
		viewer.MissionIDs = []string{strings.TrimPrefix(tenantID, "mission:")}
	case strings.HasPrefix(tenantID, "project:"):
		viewer.ProjectIDs = []string{strings.TrimPrefix(tenantID, "project:")}
	case tenantID != "" && !strings.Contains(tenantID, ":"):
		viewer.OrganizationIDs = []string{tenantID}
	}
}

func principalHasAnyScope(principal identity.Principal, scopes ...string) bool {
	granted := map[string]struct{}{}
	for _, scope := range principal.CoarseScopes {
		granted[strings.TrimPrefix(strings.ToLower(strings.TrimSpace(scope)), "orrery:")] = struct{}{}
	}
	for _, scope := range scopes {
		if _, ok := granted[strings.TrimPrefix(strings.ToLower(strings.TrimSpace(scope)), "orrery:")]; ok {
			return true
		}
	}
	return false
}
