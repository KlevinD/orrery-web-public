package aggregate

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

const MaxVisibleScopes = 100

const aggregateCursorTenantID = "ontology-all-visible"

type ScopeCatalog interface {
	List(context.Context, identity.Principal, ontologyscope.CatalogQuery) (ontologycontract.CursorPage[ontologyscope.ScopeCatalogItem], error)
	Resolve(context.Context, identity.Principal, string) (ontologyscope.ResolvedScope, error)
	ResolveCandidates(context.Context, identity.Principal, []string) ([]ontologyscope.ScopeCatalogItem, error)
}

type ScopeSet struct {
	items []ontologyscope.ScopeCatalogItem
	hash  string
}

func (s ScopeSet) Items() []ontologyscope.ScopeCatalogItem {
	items := make([]ontologyscope.ScopeCatalogItem, len(s.items))
	copy(items, s.items)
	return items
}

func (s ScopeSet) TenantIDs() []string {
	ids := make([]string, len(s.items))
	for index, item := range s.items {
		ids[index] = item.TenantID
	}
	return ids
}

func (s ScopeSet) Hash() string {
	return s.hash
}

type ScopeEnumerator struct {
	catalog     ScopeCatalog
	cursors     *ontologycontract.StateCursorStore
	discoveryMu sync.Mutex
	discoveries map[string]*scopeDiscoveryFlight
}

type scopeDiscoveryFlight struct {
	done chan struct{}
	set  ScopeSet
	err  error
}

type PagePosition struct {
	ScopeIndex int
	Inner      string
}

func NewScopeEnumerator(catalog ScopeCatalog, cursors *ontologycontract.StateCursorStore) (*ScopeEnumerator, error) {
	if catalog == nil || cursors == nil {
		return nil, ontologyscope.ErrDependencyUnavailable
	}
	return &ScopeEnumerator{catalog: catalog, cursors: cursors, discoveries: map[string]*scopeDiscoveryFlight{}}, nil
}

func (e *ScopeEnumerator) Discover(ctx context.Context, principal identity.Principal) (ScopeSet, error) {
	return e.discoverWithBudget(ctx, principal, ontologycontract.QueryStandard)
}

func (e *ScopeEnumerator) DiscoverCompanyOverview(ctx context.Context, principal identity.Principal, tenantIDs []string) (ScopeSet, error) {
	if e == nil || e.catalog == nil {
		return ScopeSet{}, ontologyscope.ErrDependencyUnavailable
	}
	if len(tenantIDs) > MaxVisibleScopes {
		return ScopeSet{}, ontologycontract.ErrQueryBudgetExceeded
	}
	principal = identity.NormalizePrincipal(principal)
	unique := make([]string, 0, len(tenantIDs))
	seen := make(map[string]bool, len(tenantIDs))
	for _, tenantID := range tenantIDs {
		tenantID = strings.TrimSpace(tenantID)
		if tenantID == "" || len(tenantID) > 256 {
			return ScopeSet{}, ontologyscope.ErrDependencyUnavailable
		}
		if !seen[tenantID] {
			seen[tenantID] = true
			unique = append(unique, tenantID)
		}
	}
	slices.Sort(unique)
	items, err := e.catalog.ResolveCandidates(ctx, principal, unique)
	if err != nil {
		return ScopeSet{}, scopeDiscoveryError(ctx, err)
	}
	return newScopeSet(companyOverviewScopes(items))
}

func companyOverviewScopes(items []ontologyscope.ScopeCatalogItem) []ontologyscope.ScopeCatalogItem {
	hasBusinessScope := false
	for _, item := range items {
		if item.Kind != ontologyscope.KindSystem {
			hasBusinessScope = true
			break
		}
	}
	if !hasBusinessScope {
		return items
	}

	// The system scope is a compatibility partition. Once a concrete business
	// scope exists, including it can duplicate projections and let legacy rows
	// make an otherwise healthy company aggregate unavailable.
	filtered := make([]ontologyscope.ScopeCatalogItem, 0, len(items)-1)
	for _, item := range items {
		if item.Kind != ontologyscope.KindSystem {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func (e *ScopeEnumerator) discoverWithBudget(ctx context.Context, principal identity.Principal, budgetClass ontologycontract.QueryClass) (ScopeSet, error) {
	if e == nil || e.catalog == nil {
		return ScopeSet{}, ontologyscope.ErrDependencyUnavailable
	}
	principal = identity.NormalizePrincipal(principal)
	key, err := scopeDiscoveryKey(principal)
	if err != nil {
		return ScopeSet{}, err
	}
	key += "\x00" + string(budgetClass)
	e.discoveryMu.Lock()
	if flight := e.discoveries[key]; flight != nil {
		e.discoveryMu.Unlock()
		select {
		case <-flight.done:
			return flight.set, flight.err
		case <-ctx.Done():
			return ScopeSet{}, scopeDiscoveryError(ctx, ctx.Err())
		}
	}
	flight := &scopeDiscoveryFlight{done: make(chan struct{})}
	e.discoveries[key] = flight
	e.discoveryMu.Unlock()

	flight.set, flight.err = e.discover(ctx, principal, budgetClass)
	flight.err = scopeDiscoveryError(ctx, flight.err)
	e.discoveryMu.Lock()
	close(flight.done)
	delete(e.discoveries, key)
	e.discoveryMu.Unlock()
	return flight.set, flight.err
}

func (e *ScopeEnumerator) discover(ctx context.Context, principal identity.Principal, budgetClass ontologycontract.QueryClass) (ScopeSet, error) {
	items := make([]ontologyscope.ScopeCatalogItem, 0, MaxVisibleScopes)
	cursor := ""
	seenCursors := map[string]bool{}
	for {
		page, err := e.catalog.List(ctx, principal, ontologyscope.CatalogQuery{Cursor: cursor, Limit: MaxVisibleScopes, BudgetClass: budgetClass})
		if err != nil {
			return ScopeSet{}, err
		}
		if len(page.Items) > MaxVisibleScopes-len(items) || len(items)+len(page.Items) == MaxVisibleScopes && page.Page.HasMore {
			return ScopeSet{}, ontologycontract.ErrQueryBudgetExceeded
		}
		items = append(items, page.Items...)
		if !page.Page.HasMore {
			break
		}
		next := strings.TrimSpace(page.Page.NextCursor)
		if next == "" || next == cursor || seenCursors[next] {
			return ScopeSet{}, ontologyscope.ErrDependencyUnavailable
		}
		seenCursors[next] = true
		cursor = next
	}
	return newScopeSet(items)
}

func scopeDiscoveryKey(principal identity.Principal) (string, error) {
	raw, err := json.Marshal(struct {
		PrincipalKey              string   `json:"principal_key"`
		TenantID                  string   `json:"tenant_id"`
		CoarseScopes              []string `json:"coarse_scopes"`
		CredentialScopes          []string `json:"credential_scopes"`
		CredentialScopeRestricted bool     `json:"credential_scope_restricted"`
	}{
		PrincipalKey:              authorization.PrincipalCacheKey(principal),
		TenantID:                  principal.TenantID,
		CoarseScopes:              principal.CoarseScopes,
		CredentialScopes:          principal.CredentialScopes,
		CredentialScopeRestricted: principal.CredentialScopeRestricted,
	})
	if err != nil {
		return "", fmt.Errorf("%w: authorization context cannot be canonicalized", ontologycontract.ErrInvalidRequest)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func scopeDiscoveryError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		cause := err
		if ctx.Err() != nil {
			cause = ctx.Err()
		}
		return fmt.Errorf("%w: %v", ontologycontract.ErrQueryBudgetExceeded, cause)
	}
	return err
}

func newScopeSet(items []ontologyscope.ScopeCatalogItem) (ScopeSet, error) {
	normalized := make([]ontologyscope.ScopeCatalogItem, len(items))
	seen := make(map[string]bool, len(items))
	type hashItem struct {
		TenantID     string                          `json:"tenant_id"`
		Kind         ontologyscope.Kind              `json:"kind"`
		Capabilities []ontologyscope.ScopeCapability `json:"capabilities"`
	}
	hashItems := make([]hashItem, 0, len(items))
	for index, item := range items {
		item.TenantID = strings.TrimSpace(item.TenantID)
		item.Label = strings.TrimSpace(item.Label)
		item.ParentTenantID = strings.TrimSpace(item.ParentTenantID)
		if item.TenantID == "" || seen[item.TenantID] || !aggregateScopeKindValid(item.Kind) {
			return ScopeSet{}, ontologyscope.ErrDependencyUnavailable
		}
		seen[item.TenantID] = true
		capabilities, ok := normalizeCapabilities(item.Capabilities)
		if !ok {
			return ScopeSet{}, ontologyscope.ErrDependencyUnavailable
		}
		item.Capabilities = capabilities
		normalized[index] = item
		hashItems = append(hashItems, hashItem{TenantID: item.TenantID, Kind: item.Kind, Capabilities: capabilities})
	}
	hash, err := ontologycontract.CanonicalQueryHash(hashItems)
	if err != nil {
		return ScopeSet{}, err
	}
	return ScopeSet{items: normalized, hash: hash}, nil
}

func normalizeCapabilities(values []ontologyscope.ScopeCapability) ([]ontologyscope.ScopeCapability, bool) {
	seen := map[ontologyscope.ScopeCapability]bool{}
	capabilities := make([]ontologyscope.ScopeCapability, 0, len(values))
	for _, capability := range values {
		switch capability {
		case ontologyscope.CapabilityRead, ontologyscope.CapabilityManage:
		default:
			return nil, false
		}
		if !seen[capability] {
			seen[capability] = true
			capabilities = append(capabilities, capability)
		}
	}
	if !seen[ontologyscope.CapabilityRead] {
		return nil, false
	}
	slices.Sort(capabilities)
	return capabilities, true
}

func aggregateScopeKindValid(kind ontologyscope.Kind) bool {
	switch kind {
	case ontologyscope.KindOrganization, ontologyscope.KindProject, ontologyscope.KindMission, ontologyscope.KindResource, ontologyscope.KindSystem, ontologyscope.KindCustom:
		return true
	default:
		return false
	}
}

func (e *ScopeEnumerator) ResolvePageCursor(token, queryKey string, principal identity.Principal, scopes ScopeSet) (PagePosition, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return PagePosition{}, nil
	}
	binding, err := aggregateCursorBinding(queryKey, principal, scopes)
	if err != nil {
		return PagePosition{}, err
	}
	position, err := e.cursors.Resolve(token, binding)
	if err != nil {
		return PagePosition{}, err
	}
	if len(position.Seek) != 2 {
		return PagePosition{}, ontologycontract.ErrInvalidCursor
	}
	scopeIndex, err := strconv.Atoi(position.Seek[0])
	if err != nil || scopeIndex < 0 || scopeIndex > MaxVisibleScopes {
		return PagePosition{}, ontologycontract.ErrInvalidCursor
	}
	innerEncoded := strings.TrimPrefix(position.Seek[1], "inner:")
	if innerEncoded == position.Seek[1] {
		return PagePosition{}, ontologycontract.ErrInvalidCursor
	}
	innerBytes, err := base64.RawURLEncoding.DecodeString(innerEncoded)
	if err != nil {
		return PagePosition{}, ontologycontract.ErrInvalidCursor
	}
	return PagePosition{ScopeIndex: scopeIndex, Inner: string(innerBytes)}, nil
}

func (e *ScopeEnumerator) IssuePageCursor(position PagePosition, queryKey string, principal identity.Principal, scopes ScopeSet) (string, error) {
	if e == nil || e.cursors == nil {
		return "", ontologyscope.ErrDependencyUnavailable
	}
	if position.ScopeIndex < 0 || position.ScopeIndex > MaxVisibleScopes {
		return "", ontologycontract.ErrInvalidRequest
	}
	binding, err := aggregateCursorBinding(queryKey, principal, scopes)
	if err != nil {
		return "", err
	}
	return e.cursors.Issue(ontologycontract.CursorPosition{
		Binding: binding,
		Seek: []string{
			strconv.Itoa(position.ScopeIndex),
			"inner:" + base64.RawURLEncoding.EncodeToString([]byte(position.Inner)),
		},
	})
}

func aggregateCursorBinding(queryKey string, principal identity.Principal, scopes ScopeSet) (ontologycontract.CursorBinding, error) {
	queryKey = strings.TrimSpace(queryKey)
	if queryKey == "" || scopes.hash == "" {
		return ontologycontract.CursorBinding{}, ontologycontract.ErrInvalidRequest
	}
	queryHash, err := ontologycontract.CanonicalQueryHash(struct {
		QueryKey string `json:"query_key"`
		ScopeSet string `json:"scope_set"`
	}{QueryKey: queryKey, ScopeSet: scopes.hash})
	if err != nil {
		return ontologycontract.CursorBinding{}, err
	}
	principalKey := authorization.PrincipalCacheKey(identity.NormalizePrincipal(principal))
	if principalKey == "" {
		return ontologycontract.CursorBinding{}, fmt.Errorf("%w: principal is required", ontologycontract.ErrInvalidRequest)
	}
	return ontologycontract.CursorBinding{
		PrincipalKey:   principalKey,
		TenantID:       aggregateCursorTenantID,
		BindingVersion: 1,
		QueryHash:      queryHash,
	}, nil
}
