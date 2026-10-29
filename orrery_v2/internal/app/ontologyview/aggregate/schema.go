package aggregate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	schemaview "orrery_v2/internal/app/ontologyview/schema"
)

const maxAggregateDefinitionPages = 1000

type SchemaReader interface {
	Summary(context.Context, identity.Principal, string) (ontologycontract.Summary[schemaview.SummaryData], error)
	ListDefinitions(context.Context, identity.Principal, schemaview.DefinitionListQuery) (ontologycontract.CursorPage[schemaview.DefinitionItem], error)
	GetDefinition(context.Context, identity.Principal, string, schemaview.DefinitionKind, string) (schemaview.DefinitionDetail, error)
	ListReferences(context.Context, identity.Principal, schemaview.ReferenceQuery) (ontologycontract.CursorPage[schemaview.DefinitionReference], error)
	Graph(context.Context, identity.Principal, schemaview.GraphQuery) (schemaview.Graph, error)
}

type SchemaService struct {
	scopes   *ScopeEnumerator
	delegate SchemaReader
}

type originSummaryReader interface {
	SummaryOrigin(context.Context, identity.Principal, string, schemaview.DefinitionOrigin) (ontologycontract.Summary[schemaview.SummaryData], error)
}

type definitionSource struct {
	tenantID string
	origin   schemaview.DefinitionOrigin
}

type aggregateDefinitionRevision struct {
	TenantID string                      `json:"tenant_id"`
	Kind     schemaview.DefinitionKind   `json:"kind"`
	ID       string                      `json:"id"`
	Version  string                      `json:"version"`
	Status   schemaview.DefinitionStatus `json:"status"`
}

func NewSchemaService(scopes *ScopeEnumerator, delegate SchemaReader) (*SchemaService, error) {
	if scopes == nil || delegate == nil {
		return nil, ontologyscope.ErrDependencyUnavailable
	}
	return &SchemaService{scopes: scopes, delegate: delegate}, nil
}

func (s *SchemaService) Summary(ctx context.Context, principal identity.Principal, tenantID string) (ontologycontract.Summary[schemaview.SummaryData], error) {
	if strings.TrimSpace(tenantID) != "" {
		return s.delegate.Summary(ctx, principal, tenantID)
	}
	scopes, err := s.scopes.Discover(ctx, principal)
	if err != nil {
		return ontologycontract.Summary[schemaview.SummaryData]{}, err
	}
	if reader, ok := s.delegate.(originSummaryReader); ok {
		return s.summaryOrigins(ctx, principal, definitionSources(scopes, ""), reader)
	}
	items, err := s.readAllDefinitions(ctx, principal, definitionSources(scopes, ""))
	if err != nil {
		return ontologycontract.Summary[schemaview.SummaryData]{}, err
	}
	counts := map[schemaview.DefinitionKind]int64{}
	var latestUpdatedAt *time.Time
	revisions := make([]aggregateDefinitionRevision, 0, len(items))
	for _, item := range items {
		counts[item.Kind]++
		latestUpdatedAt = latest(latestUpdatedAt, item.UpdatedAt)
		revisions = append(revisions, aggregateDefinitionRevision{item.TenantID, item.Kind, item.ID, item.Version, item.Status})
	}
	revision, err := aggregateSchemaRevision(revisions)
	if err != nil {
		return ontologycontract.Summary[schemaview.SummaryData]{}, err
	}
	return ontologycontract.Summary[schemaview.SummaryData]{
		Data: schemaview.SummaryData{
			ObjectTypes: ontologycontract.ExactCount(counts[schemaview.KindObject]), LinkTypes: ontologycontract.ExactCount(counts[schemaview.KindLink]),
			PropertyTypes: ontologycontract.ExactCount(counts[schemaview.KindProperty]), MappingRules: ontologycontract.ExactCount(counts[schemaview.KindMapping]),
			ActionTypes: ontologycontract.ExactCount(counts[schemaview.KindAction]), LatestUpdatedAt: latestUpdatedAt,
		},
		Meta: ontologycontract.SummaryMeta{AsOf: time.Now().UTC(), DataRevision: revision},
	}, nil
}

func aggregateSchemaRevision(revisions []aggregateDefinitionRevision) (string, error) {
	raw, err := json.Marshal(revisions)
	if err != nil {
		return "", fmt.Errorf("%w: schema revision cannot be canonicalized", ontologycontract.ErrInvalidRequest)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (s *SchemaService) summaryOrigins(ctx context.Context, principal identity.Principal, sources []definitionSource, reader originSummaryReader) (ontologycontract.Summary[schemaview.SummaryData], error) {
	data := schemaview.SummaryData{ObjectTypes: ontologycontract.ExactCount(0), LinkTypes: ontologycontract.ExactCount(0), PropertyTypes: ontologycontract.ExactCount(0), MappingRules: ontologycontract.ExactCount(0), ActionTypes: ontologycontract.ExactCount(0)}
	metas := make([]ontologycontract.SummaryMeta, 0, len(sources))
	scopeKeys := make([]string, 0, len(sources)*2)
	for _, source := range sources {
		value, err := reader.SummaryOrigin(ctx, principal, source.tenantID, source.origin)
		if err != nil {
			return ontologycontract.Summary[schemaview.SummaryData]{}, err
		}
		data.ObjectTypes = mergeCounts(data.ObjectTypes, value.Data.ObjectTypes)
		data.LinkTypes = mergeCounts(data.LinkTypes, value.Data.LinkTypes)
		data.PropertyTypes = mergeCounts(data.PropertyTypes, value.Data.PropertyTypes)
		data.MappingRules = mergeCounts(data.MappingRules, value.Data.MappingRules)
		data.ActionTypes = mergeCounts(data.ActionTypes, value.Data.ActionTypes)
		data.LatestUpdatedAt = latest(data.LatestUpdatedAt, value.Data.LatestUpdatedAt)
		metas = append(metas, value.Meta)
		scopeKeys = append(scopeKeys, source.tenantID, string(source.origin))
	}
	meta := mergeSummaryMeta(metas...)
	meta.DataRevision, _ = ontologycontract.CanonicalQueryHash([]any{scopeKeys, meta.DataRevision})
	return ontologycontract.Summary[schemaview.SummaryData]{Data: data, Meta: meta}, nil
}

func (s *SchemaService) ListDefinitions(ctx context.Context, principal identity.Principal, query schemaview.DefinitionListQuery) (ontologycontract.CursorPage[schemaview.DefinitionItem], error) {
	if strings.TrimSpace(query.TenantID) != "" {
		return s.delegate.ListDefinitions(ctx, principal, query)
	}
	query.Kind = schemaview.DefinitionKind(strings.ToLower(strings.TrimSpace(string(query.Kind))))
	query.Origin = schemaview.DefinitionOrigin(strings.ToLower(strings.TrimSpace(string(query.Origin))))
	query.Status = schemaview.DefinitionStatus(strings.ToLower(strings.TrimSpace(string(query.Status))))
	query.Search = strings.TrimSpace(query.Search)
	query.Cursor = strings.TrimSpace(query.Cursor)
	limit, err := ontologycontract.NormalizeLimit(query.Limit, ontologycontract.DefaultListLimit, ontologycontract.MaxPageLimit)
	if err != nil || !validDefinitionKind(query.Kind) || !validDefinitionOrigin(query.Origin) || !validDefinitionStatus(query.Status) || !utf8.ValidString(query.Search) || utf8.RuneCountInString(query.Search) > 128 || len(query.Cursor) > 4096 {
		return ontologycontract.CursorPage[schemaview.DefinitionItem]{}, ontologycontract.ErrInvalidRequest
	}
	key, err := aggregateQueryKey("schema-definitions", struct {
		Kind   schemaview.DefinitionKind   `json:"kind"`
		Origin schemaview.DefinitionOrigin `json:"origin"`
		Status schemaview.DefinitionStatus `json:"status"`
		Search string                      `json:"search"`
		Limit  int                         `json:"limit"`
	}{query.Kind, query.Origin, query.Status, strings.ToLower(query.Search), limit})
	if err != nil {
		return ontologycontract.CursorPage[schemaview.DefinitionItem]{}, err
	}
	scopes, err := s.scopes.Discover(ctx, principal)
	if err != nil {
		return ontologycontract.CursorPage[schemaview.DefinitionItem]{}, err
	}
	return s.listDefinitions(ctx, principal, scopes, definitionSources(scopes, query.Origin), key, query, limit)
}

func definitionSources(scopes ScopeSet, origin schemaview.DefinitionOrigin) []definitionSource {
	sources := make([]definitionSource, 0, len(scopes.items)+1)
	if origin != schemaview.OriginTenant && len(scopes.items) > 0 {
		global := scopes.items[0]
		for _, scope := range scopes.items {
			if scope.Kind == ontologyscope.KindSystem {
				global = scope
				break
			}
		}
		sources = append(sources, definitionSource{tenantID: global.TenantID, origin: schemaview.OriginSystem})
	}
	if origin != schemaview.OriginSystem {
		for _, scope := range scopes.items {
			if scope.Kind != ontologyscope.KindSystem {
				sources = append(sources, definitionSource{tenantID: scope.TenantID, origin: schemaview.OriginTenant})
			}
		}
	}
	return sources
}

func (s *SchemaService) listDefinitions(ctx context.Context, principal identity.Principal, scopes ScopeSet, sources []definitionSource, key string, query schemaview.DefinitionListQuery, limit int) (ontologycontract.CursorPage[schemaview.DefinitionItem], error) {
	position, err := s.scopes.ResolvePageCursor(query.Cursor, key, principal, scopes)
	if err != nil {
		return ontologycontract.CursorPage[schemaview.DefinitionItem]{}, err
	}
	if position.ScopeIndex < 0 || position.ScopeIndex > len(sources) || position.ScopeIndex == len(sources) && position.Inner != "" {
		return ontologycontract.CursorPage[schemaview.DefinitionItem]{}, ontologycontract.ErrInvalidCursor
	}
	items := make([]schemaview.DefinitionItem, 0, limit)
	nextPosition := PagePosition{}
	hasMore := false
	for index := position.ScopeIndex; index < len(sources); index++ {
		cursor := ""
		if index == position.ScopeIndex {
			cursor = position.Inner
		}
		for len(items) < limit {
			remaining := limit - len(items)
			inner := query
			inner.TenantID, inner.Origin, inner.Limit, inner.Cursor = sources[index].tenantID, sources[index].origin, remaining, cursor
			page, err := s.delegate.ListDefinitions(ctx, principal, inner)
			if err != nil {
				return ontologycontract.CursorPage[schemaview.DefinitionItem]{}, err
			}
			if err := validateDelegatePage(page, remaining, cursor); err != nil {
				return ontologycontract.CursorPage[schemaview.DefinitionItem]{}, err
			}
			for _, item := range page.Items {
				item.TenantID = sources[index].tenantID
				items = append(items, item)
			}
			if page.Page.HasMore {
				cursor = page.Page.NextCursor
				if len(items) == limit {
					nextPosition, hasMore = PagePosition{ScopeIndex: index, Inner: cursor}, true
					break
				}
				continue
			}
			break
		}
		if hasMore {
			break
		}
		if len(items) == limit {
			if index+1 < len(sources) {
				nextPosition, hasMore = PagePosition{ScopeIndex: index + 1}, true
			}
			break
		}
	}
	nextCursor := ""
	if hasMore {
		nextCursor, err = s.scopes.IssuePageCursor(nextPosition, key, principal, scopes)
		if err != nil {
			return ontologycontract.CursorPage[schemaview.DefinitionItem]{}, err
		}
	}
	return ontologycontract.NewCursorPage(items, limit, hasMore, nextCursor)
}

func (s *SchemaService) readAllDefinitions(ctx context.Context, principal identity.Principal, sources []definitionSource) ([]schemaview.DefinitionItem, error) {
	items := make([]schemaview.DefinitionItem, 0)
	pages := 0
	for _, source := range sources {
		cursor := ""
		for {
			pages++
			if pages > maxAggregateDefinitionPages {
				return nil, ontologycontract.ErrQueryBudgetExceeded
			}
			page, err := s.delegate.ListDefinitions(ctx, principal, schemaview.DefinitionListQuery{TenantID: source.tenantID, Origin: source.origin, Limit: ontologycontract.MaxPageLimit, Cursor: cursor})
			if err != nil {
				return nil, err
			}
			if err := validateDelegatePage(page, ontologycontract.MaxPageLimit, cursor); err != nil {
				return nil, err
			}
			for _, item := range page.Items {
				item.TenantID = source.tenantID
				items = append(items, item)
			}
			if !page.Page.HasMore {
				break
			}
			cursor = page.Page.NextCursor
		}
	}
	return items, nil
}

func (s *SchemaService) GetDefinition(ctx context.Context, principal identity.Principal, tenantID string, kind schemaview.DefinitionKind, definitionID string) (schemaview.DefinitionDetail, error) {
	tenantID, err := requireConcreteTenant(tenantID)
	if err != nil {
		return schemaview.DefinitionDetail{}, err
	}
	return s.delegate.GetDefinition(ctx, principal, tenantID, kind, definitionID)
}

func (s *SchemaService) ListReferences(ctx context.Context, principal identity.Principal, query schemaview.ReferenceQuery) (ontologycontract.CursorPage[schemaview.DefinitionReference], error) {
	tenantID, err := requireConcreteTenant(query.TenantID)
	if err != nil {
		return ontologycontract.CursorPage[schemaview.DefinitionReference]{}, err
	}
	query.TenantID = tenantID
	return s.delegate.ListReferences(ctx, principal, query)
}

func (s *SchemaService) Graph(ctx context.Context, principal identity.Principal, query schemaview.GraphQuery) (schemaview.Graph, error) {
	if strings.TrimSpace(query.TenantID) != "" {
		return s.delegate.Graph(ctx, principal, query)
	}
	query.Search = strings.TrimSpace(query.Search)
	if query.View == "" {
		query.View = schemaview.GraphSemantic
	}
	if query.MaxNodes == 0 {
		query.MaxNodes = 200
	}
	if query.View != schemaview.GraphSemantic && query.View != schemaview.GraphProjection || query.MaxNodes < 1 || query.MaxNodes > 500 || !utf8.ValidString(query.Search) || utf8.RuneCountInString(query.Search) > 128 {
		return schemaview.Graph{}, ontologycontract.ErrInvalidRequest
	}
	scopes, err := s.scopes.Discover(ctx, principal)
	if err != nil {
		return schemaview.Graph{}, err
	}
	graphs := make([]schemaview.Graph, len(scopes.items))
	for index, scope := range scopes.items {
		inner := query
		inner.TenantID = scope.TenantID
		graphs[index], err = s.delegate.Graph(ctx, principal, inner)
		if err != nil {
			return schemaview.Graph{}, err
		}
	}
	return mergeGraphs(query, scopes, graphs), nil
}

func mergeGraphs(query schemaview.GraphQuery, scopes ScopeSet, graphs []schemaview.Graph) schemaview.Graph {
	result := schemaview.Graph{View: query.View, AppliedMaxNodes: query.MaxNodes}
	nodeIDs := make([]map[string]string, len(graphs))
	canonical := map[string]string{}
	for graphIndex, graph := range graphs {
		result.Truncated = result.Truncated || graph.Truncated
		tenantID := scopes.items[graphIndex].TenantID
		nodeIDs[graphIndex] = make(map[string]string, len(graph.Nodes))
		for _, node := range graph.Nodes {
			originalID := node.ID
			key := tenantID + "\x00" + string(node.Kind) + "\x00" + originalID
			if node.Origin == schemaview.OriginSystem {
				key = "system\x00" + string(node.Kind) + "\x00" + originalID
			}
			qualified, exists := canonical[key]
			if !exists {
				if len(result.Nodes) >= query.MaxNodes {
					result.Truncated = true
					continue
				}
				qualified = qualifyID(tenantID, originalID)
				canonical[key] = qualified
				node.ID, node.TenantID, node.DefinitionID = qualified, tenantID, graphDefinitionID(node.Kind, originalID)
				result.Nodes = append(result.Nodes, node)
			}
			nodeIDs[graphIndex][originalID] = qualified
		}
	}
	seenEdges := map[string]bool{}
	for graphIndex, graph := range graphs {
		tenantID := scopes.items[graphIndex].TenantID
		for _, edge := range graph.Edges {
			sourceID, sourceOK := nodeIDs[graphIndex][edge.SourceID]
			targetID, targetOK := nodeIDs[graphIndex][edge.TargetID]
			if !sourceOK || !targetOK {
				continue
			}
			key := sourceID + "\x00" + edge.Relation + "\x00" + targetID
			if seenEdges[key] {
				continue
			}
			if len(result.Edges) >= 1000 {
				result.Truncated = true
				break
			}
			seenEdges[key] = true
			edge.ID, edge.SourceID, edge.TargetID = qualifyID(tenantID, edge.ID), sourceID, targetID
			result.Edges = append(result.Edges, edge)
		}
	}
	result.VisibleNodes, result.VisibleEdges = len(result.Nodes), len(result.Edges)
	return result
}

func graphDefinitionID(kind schemaview.DefinitionKind, graphID string) string {
	prefix := string(kind) + ":"
	return strings.TrimPrefix(graphID, prefix)
}

func validDefinitionKind(kind schemaview.DefinitionKind) bool {
	switch kind {
	case "", schemaview.KindObject, schemaview.KindLink, schemaview.KindProperty, schemaview.KindMapping, schemaview.KindAction:
		return true
	default:
		return false
	}
}

func validDefinitionOrigin(origin schemaview.DefinitionOrigin) bool {
	return origin == "" || origin == schemaview.OriginSystem || origin == schemaview.OriginTenant
}

func validDefinitionStatus(status schemaview.DefinitionStatus) bool {
	return status == "" || status == schemaview.StatusActive || status == schemaview.StatusDisabled
}
