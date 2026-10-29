package schema

import (
	"context"
	"sort"
	"strings"

	"orrery_v2/internal/app/identity"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

func (s *Service) Graph(ctx context.Context, principal identity.Principal, query GraphQuery) (Graph, error) {
	query.TenantID = strings.TrimSpace(query.TenantID)
	query.Search = strings.TrimSpace(query.Search)
	if query.View == "" {
		query.View = GraphSemantic
	}
	if query.View != GraphSemantic && query.View != GraphProjection || !validSearch(query.Search) {
		return Graph{}, ontologycontract.ErrInvalidRequest
	}
	if query.MaxNodes == 0 {
		query.MaxNodes = 200
	}
	if query.MaxNodes < 1 || query.MaxNodes > 500 {
		return Graph{}, ontologycontract.ErrInvalidRequest
	}
	authorized, err := s.authorize(ctx, principal, query.TenantID)
	if err != nil {
		return Graph{}, err
	}
	release, err := s.concurrency.Acquire(ctx, ontologycontract.ConcurrencySchemaGraph, authorized.Snapshot().PrincipalKey)
	if err != nil {
		return Graph{}, err
	}
	defer release()
	policyState, err := s.resolvePolicyState(ctx, principal, authorized)
	if err != nil {
		return Graph{}, err
	}
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QuerySchemaGraph)
	if err != nil {
		return Graph{}, err
	}
	defer cancel()
	list, truncated, err := s.collectVisible(bounded, principal, query.TenantID, policyState, query.View, query.Search, query.MaxNodes, budget)
	if err != nil {
		return Graph{}, err
	}
	nodes := make([]GraphNode, 0, len(list))
	nodeSet := map[string]bool{}
	for _, row := range list {
		included, err := appendGraphNode(&nodes, nodeSet, GraphNode{ID: row.key(), Kind: row.kind, Label: row.label, Status: row.status, Origin: row.origin(), Matched: query.Search == "" || graphDefinitionMatches(row, query.Search)}, query.MaxNodes, budget)
		if err != nil {
			return Graph{}, err
		}
		if !included {
			truncated = true
		}
		if query.View == GraphProjection && row.kind == KindMapping && row.eventType != "" {
			included, err = appendGraphNode(&nodes, nodeSet, GraphNode{ID: "event:" + row.eventType, Kind: KindEvent, Label: row.eventType, Status: StatusActive, Origin: row.origin(), Matched: query.Search == "" || textMatches(row.eventType, query.Search)}, query.MaxNodes, budget)
			if err != nil {
				return Graph{}, err
			}
			if !included {
				truncated = true
			}
		}
	}
	edges := make([]GraphEdge, 0)
	seenEdges := map[string]bool{}
	edgeLimitReached := false

edgeRows:
	for _, row := range list {
		if query.View == GraphProjection && row.kind == KindMapping {
			limitReached, err := appendGraphEdge(&edges, seenEdges, "event:"+row.eventType, row.key(), "projects_through", nodeSet, budget)
			if err != nil {
				return Graph{}, err
			}
			truncated = limitReached || truncated
			if limitReached {
				edgeLimitReached = true
				break edgeRows
			}
		}
		for _, ref := range row.references() {
			if query.View == GraphSemantic && row.kind == KindMapping {
				continue
			}
			if query.View == GraphProjection && row.kind != KindMapping {
				continue
			}
			limitReached, err := appendGraphEdge(&edges, seenEdges, row.key(), ref.key, ref.relation, nodeSet, budget)
			if err != nil {
				return Graph{}, err
			}
			truncated = limitReached || truncated
			if limitReached {
				edgeLimitReached = true
				break edgeRows
			}
		}
	}
	truncated = edgeLimitReached || truncated
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
	return Graph{View: query.View, Nodes: nodes, Edges: edges, VisibleNodes: len(nodes), VisibleEdges: len(edges), AppliedMaxNodes: query.MaxNodes, Truncated: truncated}, nil
}

func appendGraphNode(nodes *[]GraphNode, seen map[string]bool, node GraphNode, maxNodes int, budget *ontologycontract.BudgetTracker) (bool, error) {
	if seen[node.ID] {
		return true, nil
	}
	if len(*nodes) >= maxNodes {
		return false, nil
	}
	if err := budget.ConsumeNodes(1); err != nil {
		return false, err
	}
	seen[node.ID] = true
	*nodes = append(*nodes, node)
	return true, nil
}

func appendGraphEdge(edges *[]GraphEdge, seen map[string]bool, source, target, relation string, nodes map[string]bool, budget *ontologycontract.BudgetTracker) (bool, error) {
	if !nodes[source] || !nodes[target] {
		return false, nil
	}
	id := source + "|" + relation + "|" + target
	if seen[id] {
		return false, nil
	}
	if len(*edges) >= 1000 {
		return true, nil
	}
	if err := budget.ConsumeEdges(1); err != nil {
		return false, err
	}
	seen[id] = true
	*edges = append(*edges, GraphEdge{ID: id, SourceID: source, TargetID: target, Relation: relation})
	return false, nil
}

func (s *Service) collectVisible(ctx context.Context, principal identity.Principal, tenant string, state schemaPolicyState, view GraphView, search string, maxNodes int, budget *ontologycontract.BudgetTracker) ([]rawDefinition, bool, error) {
	if view == GraphProjection {
		return s.collectProjectionVisible(ctx, principal, tenant, state, search, maxNodes, budget)
	}
	result := make([]rawDefinition, 0, maxNodes+1)
	allVisible := make([]rawDefinition, 0, maxNodes+1)
	candidateNodes := map[string]struct{}{}
	for _, kind := range graphKindOrder(GraphSemantic) {
		after := ""
		for {
			if err := budget.ConsumeStoreReads(1); err != nil {
				return nil, false, err
			}
			page, err := s.readPage(ctx, tenant, kind, after, schemaScanBatch)
			if err != nil {
				return nil, false, queryError(ctx, budget, err)
			}
			if err := budget.ConsumeCandidates(page.scanned); err != nil {
				return nil, false, err
			}
			visible, err := s.visibleRows(ctx, principal, tenant, state, page.items)
			if err != nil {
				return nil, false, queryError(ctx, budget, err)
			}
			for _, row := range visible {
				if search != "" {
					allVisible = append(allVisible, row)
					continue
				}
				result = append(result, row)
				for _, key := range graphCandidateNodeKeys(GraphSemantic, row) {
					candidateNodes[key] = struct{}{}
				}
				if len(candidateNodes) > maxNodes {
					return result, true, nil
				}
			}
			if !page.hasMore {
				break
			}
			after = page.nextAfter
		}
	}
	if search != "" {
		connected, truncated := connectedSemanticNeighborhood(allVisible, search, maxNodes)
		return connected, truncated, nil
	}
	return result, false, nil
}

func connectedSemanticNeighborhood(rows []rawDefinition, search string, maxNodes int) ([]rawDefinition, bool) {
	rowsByKey := make(map[string]rawDefinition, len(rows))
	adjacency := make(map[string][]string, len(rows))
	for _, row := range rows {
		rowsByKey[row.key()] = row
	}
	for _, row := range rows {
		for _, ref := range row.references() {
			if _, ok := rowsByKey[ref.key]; !ok {
				continue
			}
			adjacency[row.key()] = appendUniqueKey(adjacency[row.key()], ref.key)
			adjacency[ref.key] = appendUniqueKey(adjacency[ref.key], row.key())
		}
	}

	type queuedNode struct {
		key   string
		depth int
	}
	selected := make(map[string]bool, min(len(rows), maxNodes))
	result := make([]rawDefinition, 0, min(len(rows), maxNodes))
	queue := make([]queuedNode, 0, min(len(rows), maxNodes))
	for _, row := range rows {
		if !graphDefinitionMatches(row, search) || selected[row.key()] {
			continue
		}
		if len(result) >= maxNodes {
			return result, true
		}
		selected[row.key()] = true
		result = append(result, row)
		queue = append(queue, queuedNode{key: row.key()})
	}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current.depth >= 2 {
			continue
		}
		for _, neighborKey := range adjacency[current.key] {
			if selected[neighborKey] {
				continue
			}
			if len(result) >= maxNodes {
				return result, true
			}
			selected[neighborKey] = true
			result = append(result, rowsByKey[neighborKey])
			queue = append(queue, queuedNode{key: neighborKey, depth: current.depth + 1})
		}
	}
	return result, false
}

func appendUniqueKey(keys []string, key string) []string {
	for _, existing := range keys {
		if existing == key {
			return keys
		}
	}
	return append(keys, key)
}

func (s *Service) collectProjectionVisible(ctx context.Context, principal identity.Principal, tenant string, state schemaPolicyState, search string, maxNodes int, budget *ontologycontract.BudgetTracker) ([]rawDefinition, bool, error) {
	result := make([]rawDefinition, 0, maxNodes+1)
	candidateNodes := map[string]struct{}{}
	after := ""
	pageLimit := schemaScanBatch
	if search == "" && maxNodes+1 < pageLimit {
		pageLimit = maxNodes + 1
	}
	for {
		if err := budget.ConsumeStoreReads(1); err != nil {
			return nil, false, err
		}
		page, err := s.readPage(ctx, tenant, KindMapping, after, pageLimit)
		if err != nil {
			return nil, false, queryError(ctx, budget, err)
		}
		if err := budget.ConsumeCandidates(page.scanned); err != nil {
			return nil, false, err
		}
		mappings, err := s.visibleRows(ctx, principal, tenant, state, page.items)
		if err != nil {
			return nil, false, queryError(ctx, budget, err)
		}
		targets, err := s.loadProjectionTargets(ctx, principal, tenant, state, mappings, search, budget)
		if err != nil {
			return nil, false, err
		}
		targetByKey := make(map[string]rawDefinition, len(targets))
		for _, target := range targets {
			targetByKey[target.key()] = target
		}
		for _, mapping := range mappings {
			mappingMatches := graphDefinitionMatches(mapping, search)
			groupTargets := projectionGroupTargets(mapping, targetByKey, search)
			targetMatches := false
			for _, target := range groupTargets {
				targetMatches = targetMatches || graphDefinitionMatches(target, search)
			}
			if search != "" && !mappingMatches && !targetMatches {
				continue
			}
			result = append(result, mapping)
			result = append(result, groupTargets...)
			addProjectionMappingKeys(candidateNodes, mapping)
			for _, target := range groupTargets {
				candidateNodes[target.key()] = struct{}{}
			}
			if len(candidateNodes) > maxNodes {
				return result, true, nil
			}
		}
		if !page.hasMore {
			return result, false, nil
		}
		after = page.nextAfter
	}
}

func (s *Service) loadProjectionTargets(ctx context.Context, principal identity.Principal, tenant string, state schemaPolicyState, mappings []rawDefinition, search string, budget *ontologycontract.BudgetTracker) ([]rawDefinition, error) {
	refs := prioritizedProjectionReferences(mappings, search)
	if err := budget.ConsumeCandidates(len(refs)); err != nil {
		return nil, err
	}
	idsByKind := map[DefinitionKind][]string{KindObject: {}, KindLink: {}}
	for _, ref := range refs {
		idsByKind[ref.kind] = append(idsByKind[ref.kind], ref.id)
	}
	targets := make([]rawDefinition, 0, len(refs))
	for _, kind := range []DefinitionKind{KindObject, KindLink} {
		ids := idsByKind[kind]
		for start := 0; start < len(ids); start += schemaTargetBatch {
			end := min(start+schemaTargetBatch, len(ids))
			if err := budget.ConsumeStoreReads(1); err != nil {
				return nil, err
			}
			rows, err := s.readMany(ctx, tenant, kind, ids[start:end])
			if err != nil {
				return nil, queryError(ctx, budget, err)
			}
			targets = append(targets, rows...)
		}
	}
	visible, err := s.visibleRows(ctx, principal, tenant, state, targets)
	if err != nil {
		return nil, queryError(ctx, budget, err)
	}
	return visible, nil
}

func projectionGroupTargets(mapping rawDefinition, targets map[string]rawDefinition, search string) []rawDefinition {
	search = strings.TrimSpace(search)
	out := make([]rawDefinition, 0, len(mapping.referenceList))
	appendMatches := func(matching bool) {
		for _, ref := range mapping.references() {
			target, ok := targets[ref.key]
			if !ok || search != "" && graphDefinitionMatches(target, search) != matching {
				continue
			}
			out = append(out, target)
		}
	}
	if search != "" {
		appendMatches(true)
		appendMatches(false)
		return out
	}
	appendMatches(false)
	return out
}

func prioritizedProjectionReferences(mappings []rawDefinition, search string) []rawReference {
	search = strings.ToLower(strings.TrimSpace(search))
	refs := make([]rawReference, 0)
	seen := map[string]bool{}
	appendMatches := func(matching bool) {
		for _, mapping := range mappings {
			for _, ref := range mapping.references() {
				if ref.kind != KindObject && ref.kind != KindLink || seen[ref.key] {
					continue
				}
				matches := search != "" && strings.Contains(strings.ToLower(ref.id), search)
				if matches != matching {
					continue
				}
				seen[ref.key] = true
				refs = append(refs, ref)
			}
		}
	}
	if search != "" {
		appendMatches(true)
	}
	appendMatches(false)
	return refs
}

func addProjectionMappingKeys(keys map[string]struct{}, mapping rawDefinition) {
	for _, key := range graphCandidateNodeKeys(GraphProjection, mapping) {
		keys[key] = struct{}{}
	}
}

func graphKindOrder(view GraphView) []DefinitionKind {
	if view == GraphProjection {
		return []DefinitionKind{KindMapping, KindObject, KindLink}
	}
	return []DefinitionKind{KindObject, KindLink, KindProperty, KindAction}
}

func graphCandidateNodeKeys(view GraphView, row rawDefinition) []string {
	keys := []string{row.key()}
	if view == GraphProjection && row.kind == KindMapping && row.eventType != "" {
		keys = append(keys, "event:"+row.eventType)
	}
	return keys
}

func graphDefinitionMatches(row rawDefinition, search string) bool {
	search = strings.ToLower(strings.TrimSpace(search))
	if search == "" {
		return true
	}
	haystack := strings.Join([]string{row.id, row.label, row.ownerDomain, row.eventType}, "\x00")
	return strings.Contains(strings.ToLower(haystack), search)
}

func textMatches(value, search string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(strings.TrimSpace(search)))
}
