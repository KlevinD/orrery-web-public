package ontologyscope

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

const (
	catalogDefaultLimit = 30
	catalogScanBatch    = 99
	catalogTenantID     = "ontology-scope-catalog"
	catalogAuthWorkers  = 8
)

var catalogKindOrder = []Kind{
	KindOrganization,
	KindProject,
	KindMission,
	KindResource,
	KindSystem,
	KindCustom,
}

type CatalogQuery struct {
	Search      string
	Kind        Kind
	Cursor      string
	Limit       int
	BudgetClass ontologycontract.QueryClass
}

type CatalogCandidate struct {
	TenantID       string
	Kind           Kind
	Seek           string
	Label          string
	ParentTenantID string
	ActivityAt     time.Time
	// Binding is trusted source metadata used to batch catalog authorization.
	// Sources that cannot construct a canonical binding may leave it empty and
	// retain the resolver-backed authorization path.
	Binding ScopeBinding
}

type CandidatePage struct {
	Items     []CatalogCandidate
	NextAfter string
	Scanned   int
	HasMore   bool
}

type CatalogSource interface {
	Kind() Kind
	ListCandidates(ctx context.Context, after string, limit int) (CandidatePage, error)
	GetCandidate(ctx context.Context, binding ScopeBinding) (CatalogCandidate, bool, error)
}

// CatalogCandidateResolver resolves only candidates already known to contain
// ontology data. Implementations should reject unrelated tenant ID shapes
// without touching their backing store.
type CatalogCandidateResolver interface {
	ResolveCatalogCandidate(ctx context.Context, tenantID string) (ScopeBinding, CatalogCandidate, bool, error)
}

type ScopeCapability string

const (
	CapabilityRead   ScopeCapability = "ontology.read"
	CapabilityManage ScopeCapability = "ontology.manage"
)

type ScopeCatalogItem struct {
	TenantID       string            `json:"tenant_id"`
	Kind           Kind              `json:"kind"`
	Label          string            `json:"label"`
	ParentTenantID string            `json:"parent_tenant_id,omitempty"`
	ActivityAt     *time.Time        `json:"activity_at,omitempty"`
	Capabilities   []ScopeCapability `json:"capabilities"`
}

type ResolvedScope struct {
	ScopeCatalogItem
	BindingVersion int64 `json:"binding_version"`
}

type CatalogCursorSet struct {
	Encrypted *ontologycontract.CursorCodec
	Stateful  *ontologycontract.StateCursorStore
}

type Catalog struct {
	gate    *ScopeAccessGate
	sources map[Kind]CatalogSource
	cursors CatalogCursorSet
	budgets *ontologycontract.BudgetPolicy
}

func NewCatalog(gate *ScopeAccessGate, cursors CatalogCursorSet, budgets *ontologycontract.BudgetPolicy, sources ...CatalogSource) (*Catalog, error) {
	if gate == nil || cursors.Stateful == nil {
		return nil, fmt.Errorf("%w: scope catalog dependencies are required", ErrDependencyUnavailable)
	}
	if budgets == nil {
		var err error
		budgets, err = ontologycontract.NewBudgetPolicy(nil)
		if err != nil {
			return nil, err
		}
	}
	indexed := make(map[Kind]CatalogSource, len(sources))
	for _, source := range sources {
		if source == nil || !validKind(source.Kind()) || source.Kind() == KindSystem {
			return nil, fmt.Errorf("%w: invalid or reserved catalog source", ErrDependencyUnavailable)
		}
		if _, duplicate := indexed[source.Kind()]; duplicate {
			return nil, fmt.Errorf("%w: duplicate catalog source for %s", ErrDependencyUnavailable, source.Kind())
		}
		indexed[source.Kind()] = source
	}
	return &Catalog{gate: gate, sources: indexed, cursors: cursors, budgets: budgets}, nil
}

func (c *Catalog) List(ctx context.Context, principal identity.Principal, query CatalogQuery) (ontologycontract.CursorPage[ScopeCatalogItem], error) {
	if c == nil || c.gate == nil || c.budgets == nil {
		return ontologycontract.CursorPage[ScopeCatalogItem]{}, ErrDependencyUnavailable
	}
	query.Search = strings.TrimSpace(query.Search)
	query.Kind = Kind(strings.ToLower(strings.TrimSpace(string(query.Kind))))
	if !utf8.ValidString(query.Search) || utf8.RuneCountInString(query.Search) > 128 {
		return ontologycontract.CursorPage[ScopeCatalogItem]{}, fmt.Errorf("%w: scope search must not exceed 128 characters", ontologycontract.ErrInvalidRequest)
	}
	if query.Kind != "" && !validKind(query.Kind) {
		return ontologycontract.CursorPage[ScopeCatalogItem]{}, fmt.Errorf("%w: unsupported scope kind", ontologycontract.ErrInvalidRequest)
	}
	budgetClass := query.BudgetClass
	if budgetClass == "" {
		budgetClass = ontologycontract.QueryStandard
	}
	if budgetClass != ontologycontract.QueryStandard && budgetClass != ontologycontract.QueryCompanyOverview {
		return ontologycontract.CursorPage[ScopeCatalogItem]{}, fmt.Errorf("%w: unsupported scope catalog budget", ontologycontract.ErrInvalidRequest)
	}
	limit, err := ontologycontract.NormalizeLimit(query.Limit, catalogDefaultLimit, ontologycontract.MaxPageLimit)
	if err != nil {
		return ontologycontract.CursorPage[ScopeCatalogItem]{}, err
	}
	principal = identity.NormalizePrincipal(principal)
	snapshot, err := c.gate.AuthorizeFeatureRead(ctx, principal)
	if err != nil {
		return ontologycontract.CursorPage[ScopeCatalogItem]{}, err
	}
	includeManageCapability := false
	if budgetClass != ontologycontract.QueryCompanyOverview {
		manageSnapshot, manageErr := c.gate.AuthorizeFeatureManage(ctx, principal)
		if manageErr == nil {
			if revisionsChanged(snapshot, manageSnapshot) {
				return ontologycontract.CursorPage[ScopeCatalogItem]{}, ErrDependencyUnavailable
			}
			includeManageCapability = true
		} else if !errors.Is(manageErr, ErrAccessDenied) {
			return ontologycontract.CursorPage[ScopeCatalogItem]{}, manageErr
		}
	}
	queryHash, err := ontologycontract.CanonicalQueryHash(struct {
		Search string `json:"search"`
		Kind   Kind   `json:"kind,omitempty"`
		Limit  int    `json:"limit"`
	}{Search: strings.ToLower(query.Search), Kind: query.Kind, Limit: limit})
	if err != nil {
		return ontologycontract.CursorPage[ScopeCatalogItem]{}, err
	}
	cursorBinding := catalogCursorBinding(snapshot, queryHash)
	groupIndex, after, err := c.resolveCursor(query.Cursor, cursorBinding, snapshot.Cacheable())
	if err != nil {
		return ontologycontract.CursorPage[ScopeCatalogItem]{}, err
	}
	bounded, budget, cancel, err := c.budgets.Start(ctx, budgetClass)
	if err != nil {
		return ontologycontract.CursorPage[ScopeCatalogItem]{}, err
	}
	defer cancel()

	visible := make([]catalogVisibleItem, 0, limit+1)
	search := strings.ToLower(query.Search)
	for groupIndex < len(catalogKindOrder) && len(visible) <= limit {
		kind := catalogKindOrder[groupIndex]
		if query.Kind != "" && kind != query.Kind {
			groupIndex++
			after = ""
			continue
		}
		source := c.source(kind)
		if source == nil {
			groupIndex++
			after = ""
			continue
		}
		if err := budget.ConsumeStoreReads(1); err != nil {
			return ontologycontract.CursorPage[ScopeCatalogItem]{}, err
		}
		page, err := source.ListCandidates(bounded, after, catalogScanBatch)
		if err != nil {
			if bounded.Err() != nil {
				return ontologycontract.CursorPage[ScopeCatalogItem]{}, fmt.Errorf("%w: catalog deadline reached", ontologycontract.ErrQueryBudgetExceeded)
			}
			return ontologycontract.CursorPage[ScopeCatalogItem]{}, fmt.Errorf("%w: list %s scope candidates: %v", ErrDependencyUnavailable, kind, err)
		}
		if err := validateCandidatePage(kind, after, page); err != nil {
			return ontologycontract.CursorPage[ScopeCatalogItem]{}, err
		}
		if err := budget.ConsumeCandidates(page.Scanned); err != nil {
			return ontologycontract.CursorPage[ScopeCatalogItem]{}, err
		}
		authorized, err := c.authorizeCandidatePage(bounded, principal, page.Items, snapshot, includeManageCapability)
		if err != nil {
			if bounded.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return ontologycontract.CursorPage[ScopeCatalogItem]{}, fmt.Errorf("%w: catalog deadline reached", ontologycontract.ErrQueryBudgetExceeded)
			}
			return ontologycontract.CursorPage[ScopeCatalogItem]{}, err
		}
		for _, result := range authorized {
			candidate, item := result.candidate, result.item
			if search != "" && !strings.Contains(strings.ToLower(item.Label+"\x00"+item.TenantID), search) {
				continue
			}
			visible = append(visible, catalogVisibleItem{item: item, group: groupIndex, seek: candidate.Seek})
			if len(visible) > limit {
				break
			}
		}
		if len(visible) > limit {
			break
		}
		if page.HasMore {
			after = page.NextAfter
			continue
		}
		groupIndex++
		after = ""
	}
	if err := budget.CheckContext(bounded); err != nil {
		return ontologycontract.CursorPage[ScopeCatalogItem]{}, err
	}
	hasMore := len(visible) > limit
	if hasMore {
		visible = visible[:limit]
	}
	items := make([]ScopeCatalogItem, len(visible))
	for index := range visible {
		items[index] = visible[index].item
	}
	nextCursor := ""
	if hasMore {
		last := visible[len(visible)-1]
		nextCursor, err = c.issueCursor(cursorBinding, snapshot.Cacheable(), last.group, last.seek)
		if err != nil {
			return ontologycontract.CursorPage[ScopeCatalogItem]{}, err
		}
	}
	return ontologycontract.NewCursorPage(items, limit, hasMore, nextCursor)
}

func (c *Catalog) Resolve(ctx context.Context, principal identity.Principal, tenantID string) (ResolvedScope, error) {
	if c == nil || c.gate == nil {
		return ResolvedScope{}, ErrDependencyUnavailable
	}
	principal = identity.NormalizePrincipal(principal)
	authorized, err := c.gate.AuthorizeRead(ctx, principal, strings.TrimSpace(tenantID))
	if err != nil {
		if isNonDisclosingScopeError(err) {
			return ResolvedScope{}, ErrBindingNotFound
		}
		return ResolvedScope{}, err
	}
	binding := authorized.Binding()
	source := c.source(binding.Kind)
	if source == nil {
		return ResolvedScope{}, ErrBindingNotFound
	}
	candidate, found, err := source.GetCandidate(ctx, binding)
	if err != nil {
		return ResolvedScope{}, fmt.Errorf("%w: resolve scope metadata: %v", ErrDependencyUnavailable, err)
	}
	if !found || candidate.TenantID != binding.TenantID || candidate.Kind != binding.Kind {
		return ResolvedScope{}, ErrBindingNotFound
	}
	item, allowed, err := c.authorizeCandidate(ctx, principal, candidate, authorized.Snapshot(), true)
	if err != nil {
		return ResolvedScope{}, err
	}
	if !allowed {
		return ResolvedScope{}, ErrBindingNotFound
	}
	return ResolvedScope{ScopeCatalogItem: item, BindingVersion: binding.BindingVersion}, nil
}

func (c *Catalog) ResolveCandidates(ctx context.Context, principal identity.Principal, tenantIDs []string) ([]ScopeCatalogItem, error) {
	if c == nil || c.gate == nil || c.budgets == nil || len(tenantIDs) > ontologycontract.MaxPageLimit {
		return nil, ErrDependencyUnavailable
	}
	principal = identity.NormalizePrincipal(principal)
	bounded, budget, cancel, err := c.budgets.Start(ctx, ontologycontract.QueryCompanyOverview)
	if err != nil {
		return nil, err
	}
	defer cancel()
	if err := budget.ConsumeCandidates(len(tenantIDs)); err != nil {
		return nil, err
	}
	expected, err := c.gate.AuthorizeFeatureRead(bounded, principal)
	if err != nil {
		return nil, err
	}

	type candidateResult struct {
		binding   ScopeBinding
		candidate CatalogCandidate
		found     bool
		err       error
	}
	results := make([]candidateResult, len(tenantIDs))
	var group sync.WaitGroup
	jobs := make(chan int)
	workerCount := min(8, len(tenantIDs))
	for range workerCount {
		group.Add(1)
		go func() {
			defer group.Done()
			for index := range jobs {
				results[index].binding, results[index].candidate, results[index].found, results[index].err = c.resolveCatalogCandidate(bounded, tenantIDs[index])
			}
		}()
	}
	for index := range tenantIDs {
		select {
		case jobs <- index:
		case <-bounded.Done():
			close(jobs)
			group.Wait()
			return nil, fmt.Errorf("%w: catalog deadline reached", ontologycontract.ErrQueryBudgetExceeded)
		}
	}
	close(jobs)
	group.Wait()
	if err := budget.CheckContext(bounded); err != nil {
		return nil, err
	}

	type preparedCandidate struct {
		item    ScopeCatalogItem
		request ObjectAuthorizationRequest
	}
	prepared := make([]preparedCandidate, 0, len(results))
	for _, result := range results {
		if result.err != nil {
			if IsNonDisclosingReadError(result.err) {
				continue
			}
			return nil, result.err
		}
		if !result.found {
			continue
		}
		item, request, err := prepareResolvedCandidate(result.binding, result.candidate)
		if err != nil {
			return nil, err
		}
		prepared = append(prepared, preparedCandidate{item: item, request: request})
	}

	items := make([]ScopeCatalogItem, 0, len(prepared))
	if batch, ok := c.gate.authorizer.(BatchAccessAuthorizer); ok {
		requests := make([]ObjectAuthorizationRequest, len(prepared))
		for index := range prepared {
			requests[index] = prepared[index].request
		}
		allowed, err := batch.AuthorizeObjects(bounded, principal, requests)
		if err != nil {
			if bounded.Err() != nil {
				return nil, bounded.Err()
			}
			mapped := scopeAuthorizationError(err, "source object authorization failed")
			if errors.Is(mapped, ErrAccessDenied) {
				allowed = make([]bool, len(prepared))
			} else {
				return nil, mapped
			}
		}
		if len(allowed) != len(prepared) {
			return nil, ErrDependencyUnavailable
		}
		for index, visible := range allowed {
			if visible {
				items = append(items, prepared[index].item)
			}
		}
	} else {
		for _, candidate := range prepared {
			allowed, err := authorizePreparedCandidate(bounded, c.gate.authorizer, principal, candidate.request)
			if err != nil {
				return nil, err
			}
			if allowed {
				items = append(items, candidate.item)
			}
		}
	}
	after, err := c.gate.captureSnapshot(bounded, principal)
	if err != nil {
		return nil, err
	}
	if revisionsChanged(expected, after) {
		return nil, fmt.Errorf("%w: authorization revision changed during candidate resolution", ErrDependencyUnavailable)
	}
	return items, nil
}

func (c *Catalog) resolveCatalogCandidate(ctx context.Context, tenantID string) (ScopeBinding, CatalogCandidate, bool, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "system" {
		binding := SystemBinding()
		candidate, found, err := c.source(KindSystem).GetCandidate(ctx, binding)
		return binding, candidate, found, err
	}
	matched := false
	complete := true
	var resolvedBinding ScopeBinding
	var resolvedCandidate CatalogCandidate
	for _, kind := range catalogKindOrder {
		if kind == KindSystem {
			continue
		}
		source := c.source(kind)
		resolver, ok := source.(CatalogCandidateResolver)
		if !ok {
			complete = false
			continue
		}
		binding, candidate, found, err := resolver.ResolveCatalogCandidate(ctx, tenantID)
		if err != nil {
			return ScopeBinding{}, CatalogCandidate{}, false, err
		}
		if !found {
			continue
		}
		if matched || binding.TenantID != tenantID || candidate.TenantID != tenantID || binding.Kind != kind || candidate.Kind != kind {
			return ScopeBinding{}, CatalogCandidate{}, false, ErrBindingConflict
		}
		matched = true
		resolvedBinding, resolvedCandidate = binding, candidate
	}
	if matched {
		return resolvedBinding, resolvedCandidate, true, nil
	}
	if complete {
		return ScopeBinding{}, CatalogCandidate{}, false, nil
	}
	// Non-production sources can retain the exact resolver path used by the
	// public resolve endpoint.
	binding, err := c.gate.resolver.Resolve(ctx, tenantID)
	if err != nil {
		return ScopeBinding{}, CatalogCandidate{}, false, err
	}
	candidate, found, err := c.source(binding.Kind).GetCandidate(ctx, binding)
	return binding, candidate, found, err
}

func (c *Catalog) authorizeResolvedCandidate(ctx context.Context, principal identity.Principal, binding ScopeBinding, candidate CatalogCandidate) (ScopeCatalogItem, bool, error) {
	item, request, err := prepareResolvedCandidate(binding, candidate)
	if err != nil {
		return ScopeCatalogItem{}, false, err
	}
	allowed, err := authorizePreparedCandidate(ctx, c.gate.authorizer, principal, request)
	return item, allowed, err
}

func prepareResolvedCandidate(binding ScopeBinding, candidate CatalogCandidate) (ScopeCatalogItem, ObjectAuthorizationRequest, error) {
	binding = NormalizeBinding(binding)
	if err := ValidateBinding(binding); err != nil || candidate.TenantID != binding.TenantID || candidate.Kind != binding.Kind {
		return ScopeCatalogItem{}, ObjectAuthorizationRequest{}, ErrDependencyUnavailable
	}
	object := binding.SourceObject
	object.TenantID = binding.AuthorizationTenantID
	request := ObjectAuthorizationRequest{Action: binding.ReadAction, Object: object, AuthorizationTenantID: binding.AuthorizationTenantID}
	item := ScopeCatalogItem{
		TenantID:     binding.TenantID,
		Kind:         binding.Kind,
		Label:        strings.TrimSpace(candidate.Label),
		Capabilities: []ScopeCapability{CapabilityRead},
	}
	if item.Label == "" {
		item.Label = item.TenantID
	}
	if !candidate.ActivityAt.IsZero() {
		activity := candidate.ActivityAt.UTC()
		item.ActivityAt = &activity
	}
	return item, request, nil
}

func authorizePreparedCandidate(ctx context.Context, authorizer AccessAuthorizer, principal identity.Principal, request ObjectAuthorizationRequest) (bool, error) {
	if err := authorizer.AuthorizeObject(ctx, principal, request.Action, request.Object, request.AuthorizationTenantID); err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		mapped := scopeAuthorizationError(err, "source object authorization failed")
		if errors.Is(mapped, ErrAccessDenied) {
			return false, nil
		}
		return false, mapped
	}
	return true, nil
}

type catalogVisibleItem struct {
	item  ScopeCatalogItem
	group int
	seek  string
}

type candidateAuthorizationResult struct {
	item    ScopeCatalogItem
	allowed bool
	err     error
}

func (c *Catalog) authorizeCandidateWindow(ctx context.Context, principal identity.Principal, candidates []CatalogCandidate, expected authorization.AuthorizationSnapshot, includeManageCapability bool) ([]candidateAuthorizationResult, error) {
	results := make([]candidateAuthorizationResult, len(candidates))
	var wg sync.WaitGroup
	for index := range candidates {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			results[index].item, results[index].allowed, results[index].err = c.authorizeCandidateAtSnapshot(ctx, principal, candidates[index], expected, includeManageCapability)
		}(index)
	}
	wg.Wait()
	if err := c.gate.validateSnapshot(ctx, principal, expected); err != nil {
		return nil, err
	}
	return results, nil
}

type authorizedCatalogCandidate struct {
	candidate CatalogCandidate
	item      ScopeCatalogItem
}

type catalogAuthorizationPlan struct {
	candidate   CatalogCandidate
	item        ScopeCatalogItem
	readIndex   int
	manageIndex int
	parentIndex int
}

func (c *Catalog) authorizeCandidatePage(ctx context.Context, principal identity.Principal, candidates []CatalogCandidate, expected authorization.AuthorizationSnapshot, includeManageCapability bool) ([]authorizedCatalogCandidate, error) {
	batch, batchSupported := c.gate.authorizer.(BatchAccessAuthorizer)
	bindingsProvided := true
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.Binding.TenantID) == "" {
			bindingsProvided = false
			break
		}
	}
	if !batchSupported || !bindingsProvided {
		out := make([]authorizedCatalogCandidate, 0, len(candidates))
		for start := 0; start < len(candidates); start += catalogAuthWorkers {
			end := min(start+catalogAuthWorkers, len(candidates))
			results, err := c.authorizeCandidateWindow(ctx, principal, candidates[start:end], expected, includeManageCapability)
			if err != nil {
				return nil, err
			}
			for offset, result := range results {
				if result.err != nil {
					return nil, result.err
				}
				if result.allowed {
					out = append(out, authorizedCatalogCandidate{candidate: candidates[start+offset], item: result.item})
				}
			}
		}
		return out, nil
	}

	manageAllowed := includeManageCapability

	plans := make([]catalogAuthorizationPlan, 0, len(candidates))
	requests := make([]ObjectAuthorizationRequest, 0, len(candidates)*2)
	parentRequests := make(map[string]int)
	parentBindings := make(map[string]ScopeBinding)
	for _, candidate := range candidates {
		binding := NormalizeBinding(candidate.Binding)
		if err := ValidateBinding(binding); err != nil || binding.TenantID != candidate.TenantID || binding.Kind != candidate.Kind {
			return nil, ErrDependencyUnavailable
		}
		item := ScopeCatalogItem{
			TenantID:     binding.TenantID,
			Kind:         binding.Kind,
			Label:        strings.TrimSpace(candidate.Label),
			Capabilities: []ScopeCapability{CapabilityRead},
		}
		if item.Label == "" {
			item.Label = item.TenantID
		}
		if !candidate.ActivityAt.IsZero() {
			activity := candidate.ActivityAt.UTC()
			item.ActivityAt = &activity
		}
		plan := catalogAuthorizationPlan{candidate: candidate, item: item, readIndex: -1, manageIndex: -1, parentIndex: -1}
		plan.readIndex = len(requests)
		requests = append(requests, bindingAuthorizationRequest(binding, false))
		if manageAllowed {
			plan.manageIndex = len(requests)
			requests = append(requests, bindingAuthorizationRequest(binding, true))
		}
		parentTenantID := strings.TrimSpace(candidate.ParentTenantID)
		if parentTenantID != "" {
			parentBinding, found := parentBindings[parentTenantID]
			if !found {
				var err error
				parentBinding, err = c.gate.resolver.Resolve(ctx, parentTenantID)
				if err != nil {
					if !isNonDisclosingScopeError(err) {
						return nil, err
					}
					parentBinding = ScopeBinding{}
				}
				parentBindings[parentTenantID] = parentBinding
			}
			if parentBinding.TenantID != "" {
				requestKey := parentBinding.TenantID + "\x00" + parentBinding.ReadAction
				parentIndex, exists := parentRequests[requestKey]
				if !exists {
					parentIndex = len(requests)
					parentRequests[requestKey] = parentIndex
					requests = append(requests, bindingAuthorizationRequest(parentBinding, false))
				}
				plan.parentIndex = parentIndex
				plan.item.ParentTenantID = parentTenantID
			}
		}
		plans = append(plans, plan)
	}

	allowed, err := batch.AuthorizeObjects(ctx, principal, requests)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		mapped := scopeAuthorizationError(err, "source object authorization failed")
		if errors.Is(mapped, ErrAccessDenied) {
			allowed = make([]bool, len(requests))
		} else {
			return nil, mapped
		}
	}
	if len(allowed) != len(requests) {
		return nil, ErrDependencyUnavailable
	}
	after, err := c.gate.captureSnapshot(ctx, principal)
	if err != nil {
		return nil, err
	}
	if revisionsChanged(expected, after) {
		return nil, ErrDependencyUnavailable
	}

	out := make([]authorizedCatalogCandidate, 0, len(plans))
	for _, plan := range plans {
		if plan.readIndex < 0 || !allowed[plan.readIndex] {
			continue
		}
		if plan.manageIndex >= 0 && allowed[plan.manageIndex] {
			plan.item.Capabilities = append(plan.item.Capabilities, CapabilityManage)
		}
		if plan.parentIndex >= 0 && !allowed[plan.parentIndex] {
			plan.item.ParentTenantID = ""
		}
		out = append(out, authorizedCatalogCandidate{candidate: plan.candidate, item: plan.item})
	}
	return out, nil
}

func bindingAuthorizationRequest(binding ScopeBinding, manage bool) ObjectAuthorizationRequest {
	binding = NormalizeBinding(binding)
	action := binding.ReadAction
	if manage {
		action = binding.ManageAction
	}
	object := binding.SourceObject
	object.TenantID = binding.AuthorizationTenantID
	return ObjectAuthorizationRequest{Action: action, Object: object, AuthorizationTenantID: binding.AuthorizationTenantID}
}

func (c *Catalog) authorizeCandidate(ctx context.Context, principal identity.Principal, candidate CatalogCandidate, expected authorization.AuthorizationSnapshot, includeManageCapability bool) (ScopeCatalogItem, bool, error) {
	return c.authorizeCandidateUsing(ctx, principal, candidate, expected, includeManageCapability, func(ctx context.Context, tenantID string, manage bool) (AuthorizedScopeContext, error) {
		if manage {
			return c.gate.AuthorizeManage(ctx, principal, tenantID)
		}
		return c.gate.AuthorizeRead(ctx, principal, tenantID)
	})
}

func (c *Catalog) authorizeCandidateAtSnapshot(ctx context.Context, principal identity.Principal, candidate CatalogCandidate, expected authorization.AuthorizationSnapshot, includeManageCapability bool) (ScopeCatalogItem, bool, error) {
	return c.authorizeCandidateUsing(ctx, principal, candidate, expected, includeManageCapability, func(ctx context.Context, tenantID string, manage bool) (AuthorizedScopeContext, error) {
		return c.gate.authorizeAtSnapshot(ctx, principal, tenantID, expected, manage)
	})
}

func (c *Catalog) authorizeCandidateUsing(ctx context.Context, principal identity.Principal, candidate CatalogCandidate, expected authorization.AuthorizationSnapshot, includeManageCapability bool, authorize func(context.Context, string, bool) (AuthorizedScopeContext, error)) (ScopeCatalogItem, bool, error) {
	read, err := authorize(ctx, candidate.TenantID, false)
	if err != nil {
		if isNonDisclosingScopeError(err) {
			return ScopeCatalogItem{}, false, nil
		}
		return ScopeCatalogItem{}, false, err
	}
	if revisionsChanged(expected, read.Snapshot()) || read.Binding().Kind != candidate.Kind {
		return ScopeCatalogItem{}, false, ErrDependencyUnavailable
	}
	capabilities := []ScopeCapability{CapabilityRead}
	if includeManageCapability {
		manage, err := authorize(ctx, candidate.TenantID, true)
		if err == nil {
			if revisionsChanged(expected, manage.Snapshot()) {
				return ScopeCatalogItem{}, false, ErrDependencyUnavailable
			}
			capabilities = append(capabilities, CapabilityManage)
		} else if !errors.Is(err, ErrAccessDenied) {
			return ScopeCatalogItem{}, false, err
		}
	}
	parent := strings.TrimSpace(candidate.ParentTenantID)
	if parent != "" {
		parentRead, parentErr := authorize(ctx, parent, false)
		if parentErr != nil {
			if isNonDisclosingScopeError(parentErr) {
				parent = ""
			} else {
				return ScopeCatalogItem{}, false, parentErr
			}
		} else if revisionsChanged(expected, parentRead.Snapshot()) {
			return ScopeCatalogItem{}, false, ErrDependencyUnavailable
		}
	}
	item := ScopeCatalogItem{
		TenantID:       candidate.TenantID,
		Kind:           candidate.Kind,
		Label:          strings.TrimSpace(candidate.Label),
		ParentTenantID: parent,
		Capabilities:   capabilities,
	}
	if item.Label == "" {
		item.Label = item.TenantID
	}
	if !candidate.ActivityAt.IsZero() {
		activity := candidate.ActivityAt.UTC()
		item.ActivityAt = &activity
	}
	return item, true, nil
}

func (c *Catalog) source(kind Kind) CatalogSource {
	if kind == KindSystem {
		return systemCatalogSource{}
	}
	return c.sources[kind]
}

func (c *Catalog) resolveCursor(token string, binding ontologycontract.CursorBinding, cacheable bool) (int, string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return 0, "", nil
	}
	var position ontologycontract.CursorPosition
	var err error
	if strings.HasPrefix(token, "oc1.") && cacheable && c.cursors.Encrypted != nil {
		position, err = c.cursors.Encrypted.Decode(token, binding)
	} else if strings.HasPrefix(token, "ocs1.") && c.cursors.Stateful != nil {
		position, err = c.cursors.Stateful.Resolve(token, binding)
	} else if strings.HasPrefix(token, "oc1.") || strings.HasPrefix(token, "ocs1.") {
		err = ontologycontract.ErrCursorStale
	} else {
		err = ontologycontract.ErrInvalidCursor
	}
	if err != nil {
		return 0, "", err
	}
	if len(position.Seek) != 2 {
		return 0, "", ontologycontract.ErrInvalidCursor
	}
	group, err := strconv.Atoi(position.Seek[0])
	if err != nil || group < 0 || group >= len(catalogKindOrder) {
		return 0, "", ontologycontract.ErrInvalidCursor
	}
	return group, position.Seek[1], nil
}

func (c *Catalog) issueCursor(binding ontologycontract.CursorBinding, cacheable bool, group int, after string) (string, error) {
	position := ontologycontract.CursorPosition{Binding: binding, Seek: []string{strconv.Itoa(group), after}}
	if cacheable && c.cursors.Encrypted != nil {
		return c.cursors.Encrypted.Encode(position)
	}
	if c.cursors.Stateful == nil {
		return "", ErrDependencyUnavailable
	}
	return c.cursors.Stateful.Issue(position)
}

func catalogCursorBinding(snapshot authorization.AuthorizationSnapshot, queryHash string) ontologycontract.CursorBinding {
	return ontologycontract.CursorBinding{
		PrincipalKey:          snapshot.PrincipalKey,
		TenantID:              catalogTenantID,
		BindingVersion:        1,
		QueryHash:             queryHash,
		AuthorizationRevision: snapshot.AuthorizationRevision,
		PolicyRevision:        snapshot.PolicyRevision,
	}
}

func validateCandidatePage(kind Kind, after string, page CandidatePage) error {
	if page.Scanned < len(page.Items) || page.Scanned < 0 || page.Scanned > catalogScanBatch+1 {
		return fmt.Errorf("%w: invalid %s catalog scan accounting", ErrDependencyUnavailable, kind)
	}
	previous := after
	for _, candidate := range page.Items {
		tenantID := strings.TrimSpace(candidate.TenantID)
		seek := strings.TrimSpace(candidate.Seek)
		if candidate.TenantID != tenantID || candidate.Seek != seek || candidate.Kind != kind || tenantID == "" || seek == "" || seek <= previous {
			return fmt.Errorf("%w: invalid or unstable %s catalog candidate", ErrDependencyUnavailable, kind)
		}
		previous = seek
	}
	page.NextAfter = strings.TrimSpace(page.NextAfter)
	if page.HasMore && (page.NextAfter == "" || page.NextAfter <= after) {
		return fmt.Errorf("%w: %s catalog did not advance its seek", ErrDependencyUnavailable, kind)
	}
	return nil
}

func isNonDisclosingScopeError(err error) bool {
	return IsNonDisclosingReadError(err)
}

type systemCatalogSource struct{}

func (systemCatalogSource) Kind() Kind { return KindSystem }

func (systemCatalogSource) ListCandidates(_ context.Context, after string, _ int) (CandidatePage, error) {
	if strings.TrimSpace(after) != "" {
		return CandidatePage{}, nil
	}
	return CandidatePage{Items: []CatalogCandidate{{TenantID: "system", Kind: KindSystem, Seek: "system", Label: "System", Binding: SystemBinding()}}, NextAfter: "system", Scanned: 1}, nil
}

func (systemCatalogSource) GetCandidate(_ context.Context, binding ScopeBinding) (CatalogCandidate, bool, error) {
	if binding.TenantID != "system" || binding.Kind != KindSystem {
		return CatalogCandidate{}, false, nil
	}
	return CatalogCandidate{TenantID: "system", Kind: KindSystem, Seek: "system", Label: "System", Binding: SystemBinding()}, true, nil
}
