package contextview

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

const (
	defaultNeighborhoodDepth = 1
	defaultNeighborhoodNodes = 100
	defaultNeighborhoodEdges = 200
)

func (s *Service) Neighborhood(ctx context.Context, principal identity.Principal, query NeighborhoodQuery) (Neighborhood, error) {
	query, err := normalizeNeighborhoodQuery(query)
	if err != nil {
		return Neighborhood{}, err
	}
	authorized, err := s.authorize(ctx, principal, query.TenantID)
	if err != nil {
		return Neighborhood{}, err
	}
	release, err := s.concurrency.Acquire(ctx, ontologycontract.ConcurrencyNeighborhood, authorized.Snapshot().PrincipalKey)
	if err != nil {
		return Neighborhood{}, err
	}
	defer release()
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QueryNeighborhood)
	if err != nil {
		return Neighborhood{}, err
	}
	defer cancel()
	if err := budget.ValidateDepth(query.Depth); err != nil {
		return Neighborhood{}, err
	}
	visibility, err := s.resolveVisibility(bounded, principal, authorized, budget, datavisibility.ResourceObject, datavisibility.ResourceLink)
	if err != nil {
		return Neighborhood{}, err
	}
	storeLinkType := ""
	var storeLinkTypes []string
	if query.LinkType != "" || len(query.LinkTypes) > 0 {
		filterable, err := s.resolveFilterFields(bounded, principal, visibility, datavisibility.ResourceLink, "link_type")
		if err != nil {
			return Neighborhood{}, err
		}
		if filterable["link_type"] {
			storeLinkType = query.LinkType
			storeLinkTypes = append([]string(nil), query.LinkTypes...)
		}
	}
	if err := budget.ConsumeStoreReads(1); err != nil {
		return Neighborhood{}, err
	}
	raw, err := s.graph.BuildVisibleNeighborhood(bounded, contextgraph.NeighborhoodQuery{
		TenantID: visibility.scope.Binding().TenantID, Visibility: visibility.constraint, RootID: query.RootID,
		LinkType: storeLinkType, LinkTypes: storeLinkTypes,
		Depth: query.Depth, MaxNodes: contextgraph.MaxNeighborhoodCandidateNodes, MaxEdges: contextgraph.MaxNeighborhoodCandidateEdges,
	})
	if err != nil {
		return Neighborhood{}, queryError(bounded, budget, err)
	}
	if err := budget.ConsumeCandidates(len(raw.Objects) + len(raw.Links)); err != nil {
		return Neighborhood{}, err
	}
	nodes, err := s.visibleObjects(bounded, principal, visibility, raw.Objects, false)
	if err != nil {
		return Neighborhood{}, err
	}
	nodeByID := make(map[string]ObjectItem, len(nodes))
	for _, node := range nodes {
		nodeByID[node.ID] = node.ObjectItem
	}
	if _, ok := nodeByID[raw.RootID]; !ok {
		return Neighborhood{}, ErrUnavailable
	}
	edges, err := s.visibleNeighborhoodEdges(bounded, principal, visibility, raw.Links, raw.Objects, nodeByID)
	if err != nil {
		return Neighborhood{}, err
	}
	if query.LinkType != "" || len(query.LinkTypes) > 0 {
		allowedTypes := make(map[string]bool, len(query.LinkTypes)+1)
		allowedTypes[query.LinkType] = query.LinkType != ""
		for _, linkType := range query.LinkTypes {
			allowedTypes[linkType] = true
		}
		filtered := make([]NeighborhoodEdge, 0, len(edges))
		for _, edge := range edges {
			if allowedTypes[edge.LinkType] {
				filtered = append(filtered, edge)
			}
		}
		edges = filtered
	}
	reachable := reachableNeighborhood(raw.RootID, query.Depth, edges)
	visibleItems := make([]ObjectItem, 0, len(nodes))
	for _, rawNode := range raw.Objects {
		item, ok := nodeByID[rawNode.ID]
		if ok && reachable[rawNode.ID] {
			visibleItems = append(visibleItems, item)
		}
	}
	visibleItems, edges, truncated := limitVisibleNeighborhood(raw.RootID, query.Depth, visibleItems, edges, query.MaxNodes, query.MaxEdges)
	if raw.Truncated && !truncated {
		return Neighborhood{}, ontologycontract.ErrQueryBudgetExceeded
	}
	reachable = reachableNeighborhood(raw.RootID, query.Depth, edges)
	degree := map[string]int{}
	filteredEdges := make([]NeighborhoodEdge, 0, len(edges))
	for _, edge := range edges {
		if !reachable[edge.SourceID] || !reachable[edge.TargetID] {
			continue
		}
		filteredEdges = append(filteredEdges, edge)
		degree[edge.SourceID]++
		if edge.TargetID != edge.SourceID {
			degree[edge.TargetID]++
		}
	}
	filteredNodes := make([]NeighborhoodNode, 0, len(visibleItems))
	for _, item := range visibleItems {
		if !reachable[item.ID] {
			continue
		}
		filteredNodes = append(filteredNodes, NeighborhoodNode{ObjectItem: item, Degree: degree[item.ID]})
	}
	if err := budget.ConsumeNodes(len(filteredNodes)); err != nil {
		return Neighborhood{}, err
	}
	if err := budget.ConsumeEdges(len(filteredEdges)); err != nil {
		return Neighborhood{}, err
	}
	if err := s.populateNeighborhoodEdgeLabels(bounded, visibility.scope.Binding().TenantID, filteredEdges, budget); err != nil {
		return Neighborhood{}, err
	}
	return Neighborhood{
		RootID: raw.RootID, Nodes: filteredNodes, Edges: filteredEdges,
		VisibleNodes: len(filteredNodes), VisibleEdges: len(filteredEdges), AppliedDepth: query.Depth,
		AppliedMaxNodes: query.MaxNodes, AppliedMaxEdges: query.MaxEdges, Truncated: truncated,
	}, nil
}

func limitVisibleNeighborhood(rootID string, depth int, nodes []ObjectItem, edges []NeighborhoodEdge, maxNodes, maxEdges int) ([]ObjectItem, []NeighborhoodEdge, bool) {
	distances := neighborhoodDistances(rootID, depth, edges)
	visibleNodes := make([]ObjectItem, 0, len(nodes))
	for _, node := range nodes {
		if _, ok := distances[node.ID]; ok {
			visibleNodes = append(visibleNodes, node)
		}
	}
	sort.SliceStable(visibleNodes, func(i, j int) bool {
		return distances[visibleNodes[i].ID] < distances[visibleNodes[j].ID]
	})
	truncated := len(visibleNodes) > maxNodes
	if len(visibleNodes) > maxNodes {
		visibleNodes = visibleNodes[:maxNodes]
	}
	allowed := make(map[string]bool, len(visibleNodes))
	for _, node := range visibleNodes {
		allowed[node.ID] = true
	}
	visibleEdges := make([]NeighborhoodEdge, 0, len(edges))
	for _, edge := range edges {
		if allowed[edge.SourceID] && allowed[edge.TargetID] {
			visibleEdges = append(visibleEdges, edge)
		}
	}
	sort.SliceStable(visibleEdges, func(i, j int) bool {
		left := max(distances[visibleEdges[i].SourceID], distances[visibleEdges[i].TargetID])
		right := max(distances[visibleEdges[j].SourceID], distances[visibleEdges[j].TargetID])
		return left < right
	})
	if len(visibleEdges) > maxEdges {
		visibleEdges = visibleEdges[:maxEdges]
		truncated = true
	}
	return visibleNodes, visibleEdges, truncated
}

func neighborhoodDistances(rootID string, depth int, edges []NeighborhoodEdge) map[string]int {
	distances := map[string]int{rootID: 0}
	frontier := []string{rootID}
	for level := 0; level < depth && len(frontier) > 0; level++ {
		frontierSet := make(map[string]bool, len(frontier))
		for _, id := range frontier {
			frontierSet[id] = true
		}
		next := make([]string, 0)
		for _, edge := range edges {
			other := ""
			switch {
			case frontierSet[edge.SourceID]:
				other = edge.TargetID
			case frontierSet[edge.TargetID]:
				other = edge.SourceID
			}
			if other != "" {
				if _, seen := distances[other]; !seen {
					distances[other] = level + 1
					next = append(next, other)
				}
			}
		}
		frontier = next
	}
	return distances
}

func (s *Service) visibleNeighborhoodEdges(ctx context.Context, principal identity.Principal, state visibilityState, links []contextgraph.Link, objects []contextgraph.Object, visibleNodes map[string]ObjectItem) ([]NeighborhoodEdge, error) {
	if len(links) == 0 {
		return nil, nil
	}
	objectsByID := make(map[string]contextgraph.Object, len(objects))
	for _, object := range objects {
		objectsByID[object.ID] = object
	}
	descriptors := make([]datavisibility.ResourceDescriptor, 0, len(links))
	selected := make([]contextgraph.Link, 0, len(links))
	for _, link := range links {
		from, fromOK := objectsByID[link.FromObjectID]
		to, toOK := objectsByID[link.ToObjectID]
		_, fromVisible := visibleNodes[link.FromObjectID]
		_, toVisible := visibleNodes[link.ToObjectID]
		if !fromOK || !toOK || !fromVisible || !toVisible || !contextgraph.NormalizeAccessRef(link.SourceAccessRef).Resolved() || !contextgraph.NormalizeAccessRef(from.OwnerAccessRef).Resolved() || !contextgraph.NormalizeAccessRef(to.OwnerAccessRef).Resolved() {
			continue
		}
		descriptors = append(descriptors, datavisibility.ResourceDescriptor{
			Key: "link:" + link.ID, Kind: datavisibility.ResourceLink, TenantID: link.TenantID, Type: "link",
			FieldPaths: []string{"confidence", "link_type", "status", "updated_at", "properties.relation_code", "temporal", "temporal.mode", "temporal.business_time_unspecified", "temporal.orrery_ingested_at", "temporal.business_valid_from", "temporal.business_valid_to", "temporal.source_recorded_at", "temporal.source_superseded_at"},
			AccessRefs: visibilityAccessRefs(link.SourceAccessRef, from.OwnerAccessRef, to.OwnerAccessRef), Payload: link,
		})
		selected = append(selected, link)
	}
	if len(selected) == 0 {
		return nil, nil
	}
	decision, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{Principal: identity.NormalizePrincipal(principal), Purpose: "ontology.context.read", Resources: descriptors})
	if err != nil {
		return nil, dependencyError(err)
	}
	if err := verifyVisibilityRevision(state, datavisibility.ResourceLink, decision); err != nil {
		return nil, err
	}
	out := make([]NeighborhoodEdge, 0, len(selected))
	for _, link := range selected {
		resource, ok := decision.Resource("link:" + link.ID)
		if !ok {
			return nil, dependencyError(fmt.Errorf("neighborhood link visibility decision is incomplete"))
		}
		if resource.Hidden || resource.ExistenceDisclosure == datavisibility.ExistenceConceal {
			continue
		}
		masked := maskedFieldSet(resource)
		item := NeighborhoodEdge{
			EvidenceRefs: append([]contextgraph.Ref(nil), link.EvidenceRefs...),
			Temporal:     visibleTemporal(link.Temporal, masked),
			Redacted:     masked["link_type"] || masked["status"] || temporalMasked(masked),
			RelationCode: visibleRelationCode(link, masked),
			ID:           link.ID, LinkType: link.LinkType, SourceID: link.FromObjectID, TargetID: link.ToObjectID,
			Status: link.Status, Confidence: link.Confidence, UpdatedAt: visibleTime(link.UpdatedAt),
		}
		if masked["link_type"] {
			item.LinkType = ""
		}
		if masked["status"] {
			item.Status = ""
		}
		if masked["confidence"] {
			item.Confidence = 0
		}
		if masked["updated_at"] {
			item.UpdatedAt = nil
		}
		out = append(out, item)
	}
	return out, nil
}

func reachableNeighborhood(rootID string, depth int, edges []NeighborhoodEdge) map[string]bool {
	reachable := map[string]bool{rootID: true}
	frontier := []string{rootID}
	for level := 0; level < depth && len(frontier) > 0; level++ {
		frontierSet := make(map[string]bool, len(frontier))
		for _, id := range frontier {
			frontierSet[id] = true
		}
		next := make([]string, 0)
		for _, edge := range edges {
			other := ""
			switch {
			case frontierSet[edge.SourceID]:
				other = edge.TargetID
			case frontierSet[edge.TargetID]:
				other = edge.SourceID
			}
			if other != "" && !reachable[other] {
				reachable[other] = true
				next = append(next, other)
			}
		}
		frontier = next
	}
	return reachable
}

func normalizeNeighborhoodQuery(query NeighborhoodQuery) (NeighborhoodQuery, error) {
	query.TenantID = strings.TrimSpace(query.TenantID)
	query.RootID = strings.TrimSpace(query.RootID)
	query.LinkType = strings.ToLower(strings.TrimSpace(query.LinkType))
	query.LinkTypes = normalizeNeighborhoodLinkTypes(query.LinkTypes)
	if query.Depth == 0 {
		query.Depth = defaultNeighborhoodDepth
	}
	if query.MaxNodes == 0 {
		query.MaxNodes = defaultNeighborhoodNodes
	}
	if query.MaxEdges == 0 {
		query.MaxEdges = defaultNeighborhoodEdges
	}
	if !validID(query.TenantID) || !validID(query.RootID) || !validOptionalID(query.LinkType) || query.LinkType != "" && len(query.LinkTypes) > 0 || len(query.LinkTypes) > contextgraph.MaxNeighborhoodLinkTypes || !validNeighborhoodLinkTypes(query.LinkTypes) || query.Depth < 1 || query.Depth > 2 || query.MaxNodes < 1 || query.MaxNodes > contextgraph.MaxNeighborhoodNodes || query.MaxEdges < 1 || query.MaxEdges > contextgraph.MaxNeighborhoodEdges {
		return NeighborhoodQuery{}, ontologycontract.ErrInvalidRequest
	}
	return query, nil
}

func normalizeNeighborhoodLinkTypes(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func validNeighborhoodLinkTypes(values []string) bool {
	for _, value := range values {
		if !validID(value) {
			return false
		}
	}
	return true
}
