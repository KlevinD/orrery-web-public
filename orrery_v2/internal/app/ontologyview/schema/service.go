package schema

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

var ErrUnavailable = ontologycontract.ErrUnavailable

const schemaScanBatch = 99
const schemaTargetBatch = 250

const (
	referenceCursorPhaseOutgoing = "out"
	referenceCursorPhaseIncoming = "in"
	referenceCursorEmpty         = "\x01"
)

type referenceCursorPosition struct {
	phase           string
	offset          int
	group           int
	after           string
	sourceID        string
	referenceOffset int
}

var definitionKindOrder = []DefinitionKind{KindObject, KindLink, KindProperty, KindMapping, KindAction}

type CursorSet struct {
	Encrypted *ontologycontract.CursorCodec
	Stateful  *ontologycontract.StateCursorStore
}

type Service struct {
	gate        *ontologyscope.ScopeAccessGate
	registry    RegistryReader
	actions     ActionReader
	visibility  VisibilityPolicy
	cursors     CursorSet
	budgets     *ontologycontract.BudgetPolicy
	concurrency *ontologycontract.ConcurrencyLimiter
	now         func() time.Time
}

func NewService(gate *ontologyscope.ScopeAccessGate, registry RegistryReader, actions ActionReader, visibility VisibilityPolicy, cursors CursorSet, budgets *ontologycontract.BudgetPolicy, concurrency *ontologycontract.ConcurrencyLimiter) (*Service, error) {
	if gate == nil || registry == nil || actions == nil || visibility == nil || cursors.Stateful == nil {
		return nil, ontologyscope.ErrDependencyUnavailable
	}
	if budgets == nil {
		var err error
		budgets, err = ontologycontract.NewBudgetPolicy(nil)
		if err != nil {
			return nil, err
		}
	}
	if concurrency == nil {
		var err error
		concurrency, err = ontologycontract.NewConcurrencyLimiter(nil)
		if err != nil {
			return nil, err
		}
	}
	return &Service{gate: gate, registry: registry, actions: actions, visibility: visibility, cursors: cursors, budgets: budgets, concurrency: concurrency, now: time.Now}, nil
}

func (s *Service) ListDefinitions(ctx context.Context, principal identity.Principal, query DefinitionListQuery) (ontologycontract.CursorPage[DefinitionItem], error) {
	query, limit, err := normalizeDefinitionQuery(query)
	if err != nil {
		return ontologycontract.CursorPage[DefinitionItem]{}, err
	}
	authorized, err := s.authorize(ctx, principal, query.TenantID)
	if err != nil {
		return ontologycontract.CursorPage[DefinitionItem]{}, err
	}
	policyState, err := s.resolvePolicyState(ctx, principal, authorized)
	if err != nil {
		return ontologycontract.CursorPage[DefinitionItem]{}, err
	}
	kinds := selectedKinds(query.Kind)
	queryHash, err := ontologycontract.CanonicalQueryHash(struct {
		Kind   DefinitionKind   `json:"kind"`
		Origin DefinitionOrigin `json:"origin"`
		Status DefinitionStatus `json:"status"`
		Search string           `json:"search"`
		Limit  int              `json:"limit"`
	}{query.Kind, query.Origin, query.Status, strings.ToLower(query.Search), limit})
	if err != nil {
		return ontologycontract.CursorPage[DefinitionItem]{}, err
	}
	binding := schemaCursorBinding(authorized, policyState, queryHash)
	group, after, err := s.resolveCursor(query.Cursor, binding, policyState.cacheable, len(kinds))
	if err != nil {
		return ontologycontract.CursorPage[DefinitionItem]{}, err
	}
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QueryStandard)
	if err != nil {
		return ontologycontract.CursorPage[DefinitionItem]{}, err
	}
	defer cancel()
	type positioned struct {
		item  DefinitionItem
		group int
		seek  string
	}
	visible := make([]positioned, 0, limit+1)
	for group < len(kinds) && len(visible) <= limit {
		if err := budget.ConsumeStoreReads(1); err != nil {
			return ontologycontract.CursorPage[DefinitionItem]{}, err
		}
		page, err := s.readPage(bounded, query.TenantID, kinds[group], after, schemaScanBatch)
		if err != nil {
			return ontologycontract.CursorPage[DefinitionItem]{}, queryError(bounded, budget, err)
		}
		if err := budget.ConsumeCandidates(page.scanned); err != nil {
			return ontologycontract.CursorPage[DefinitionItem]{}, err
		}
		rows, err := s.visibleRows(bounded, principal, query.TenantID, policyState, page.items)
		if err != nil {
			return ontologycontract.CursorPage[DefinitionItem]{}, queryError(bounded, budget, err)
		}
		for _, row := range rows {
			item := row.listItem()
			if !definitionMatches(item, query) {
				continue
			}
			visible = append(visible, positioned{item: item, group: group, seek: row.id})
			if len(visible) > limit {
				break
			}
		}
		if len(visible) > limit {
			break
		}
		if page.hasMore {
			after = page.nextAfter
			continue
		}
		group++
		after = ""
	}
	hasMore := len(visible) > limit
	if hasMore {
		visible = visible[:limit]
	}
	items := make([]DefinitionItem, len(visible))
	for index := range visible {
		items[index] = visible[index].item
	}
	if err := budget.ConsumeResults(len(items)); err != nil {
		return ontologycontract.CursorPage[DefinitionItem]{}, err
	}
	next := ""
	if hasMore {
		last := visible[len(visible)-1]
		next, err = s.issueCursor(binding, policyState.cacheable, last.group, last.seek)
		if err != nil {
			return ontologycontract.CursorPage[DefinitionItem]{}, err
		}
	}
	return ontologycontract.NewCursorPage(items, limit, hasMore, next)
}

func (s *Service) GetDefinition(ctx context.Context, principal identity.Principal, tenantID string, kind DefinitionKind, id string) (DefinitionDetail, error) {
	authorized, err := s.authorize(ctx, principal, tenantID)
	if err != nil {
		return DefinitionDetail{}, err
	}
	if !validKind(kind) || kind == KindEvent || !validID(id) {
		return DefinitionDetail{}, ontologycontract.ErrInvalidRequest
	}
	policyState, err := s.resolvePolicyState(ctx, principal, authorized)
	if err != nil {
		return DefinitionDetail{}, err
	}
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QueryStandard)
	if err != nil {
		return DefinitionDetail{}, err
	}
	defer cancel()
	if err := budget.ConsumeStoreReads(1); err != nil {
		return DefinitionDetail{}, err
	}
	row, err := s.readOne(bounded, tenantID, kind, id)
	if err != nil {
		if exactReadUnavailable(err) {
			return DefinitionDetail{}, ErrUnavailable
		}
		return DefinitionDetail{}, queryError(bounded, budget, err)
	}
	if err := budget.ConsumeCandidates(1); err != nil {
		return DefinitionDetail{}, err
	}
	visible, err := s.visibleRows(bounded, principal, tenantID, policyState, []rawDefinition{row})
	if err != nil {
		return DefinitionDetail{}, queryError(bounded, budget, err)
	}
	if len(visible) != 1 || !definitionTenantVisible(row.tenantID, authorized.Binding().TenantID) {
		return DefinitionDetail{}, ErrUnavailable
	}
	if err := budget.ConsumeResults(1); err != nil {
		return DefinitionDetail{}, err
	}
	return visible[0].detail(), nil
}

func (s *Service) Summary(ctx context.Context, principal identity.Principal, tenantID string) (ontologycontract.Summary[SummaryData], error) {
	return s.summary(ctx, principal, tenantID, "")
}

// SummaryOrigin preserves Summary's authorization and bounded query contract.
func (s *Service) SummaryOrigin(ctx context.Context, principal identity.Principal, tenantID string, origin DefinitionOrigin) (ontologycontract.Summary[SummaryData], error) {
	if origin != OriginSystem && origin != OriginTenant {
		return ontologycontract.Summary[SummaryData]{}, ontologycontract.ErrInvalidRequest
	}
	return s.summary(ctx, principal, tenantID, origin)
}

func (s *Service) summary(ctx context.Context, principal identity.Principal, tenantID string, origin DefinitionOrigin) (ontologycontract.Summary[SummaryData], error) {
	authorized, err := s.authorize(ctx, principal, tenantID)
	if err != nil {
		return ontologycontract.Summary[SummaryData]{}, err
	}
	release, err := s.concurrency.Acquire(ctx, ontologycontract.ConcurrencySummary, authorized.Snapshot().PrincipalKey)
	if err != nil {
		return ontologycontract.Summary[SummaryData]{}, err
	}
	defer release()
	policyState, err := s.resolvePolicyState(ctx, principal, authorized)
	if err != nil {
		return ontologycontract.Summary[SummaryData]{}, err
	}
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QuerySchemaSummary)
	if err != nil {
		return ontologycontract.Summary[SummaryData]{}, err
	}
	defer cancel()
	counts := map[DefinitionKind]int64{}
	var latest time.Time
	exact := true
	for _, kind := range definitionKindOrder {
		after := ""
		for {
			if err := budget.ConsumeStoreReads(1); err != nil {
				exact = false
				break
			}
			page, err := s.readPage(bounded, tenantID, kind, after, schemaScanBatch)
			if err != nil {
				err = queryError(bounded, budget, err)
				if errors.Is(err, ontologycontract.ErrQueryBudgetExceeded) && ctx.Err() == nil {
					exact = false
					break
				}
				return ontologycontract.Summary[SummaryData]{}, err
			}
			if err := budget.ConsumeCandidates(page.scanned); err != nil {
				exact = false
				break
			}
			visible, err := s.visibleRows(bounded, principal, tenantID, policyState, page.items)
			if err != nil {
				err = queryError(bounded, budget, err)
				if errors.Is(err, ontologycontract.ErrQueryBudgetExceeded) && ctx.Err() == nil {
					exact = false
					break
				}
				return ontologycontract.Summary[SummaryData]{}, err
			}
			for _, row := range visible {
				if origin != "" && row.origin() != origin {
					continue
				}
				counts[kind]++
				if row.updatedAt.After(latest) {
					latest = row.updatedAt
				}
			}
			if !page.hasMore {
				break
			}
			after = page.nextAfter
		}
		if !exact {
			break
		}
	}
	data := SummaryData{}
	if exact {
		data.ObjectTypes = ontologycontract.ExactCount(counts[KindObject])
		data.LinkTypes = ontologycontract.ExactCount(counts[KindLink])
		data.PropertyTypes = ontologycontract.ExactCount(counts[KindProperty])
		data.MappingRules = ontologycontract.ExactCount(counts[KindMapping])
		data.ActionTypes = ontologycontract.ExactCount(counts[KindAction])
		if !latest.IsZero() {
			value := latest.UTC()
			data.LatestUpdatedAt = &value
		}
	} else {
		data.ObjectTypes = ontologycontract.UnavailableCount()
		data.LinkTypes = ontologycontract.UnavailableCount()
		data.PropertyTypes = ontologycontract.UnavailableCount()
		data.MappingRules = ontologycontract.UnavailableCount()
		data.ActionTypes = ontologycontract.UnavailableCount()
	}
	meta := ontologycontract.SummaryMeta{AsOf: s.now().UTC(), DataRevision: schemaDataRevision(counts, latest, exact)}
	if revision := strings.TrimSpace(authorized.Snapshot().AuthorizationRevision); revision != "" {
		meta.AuthorizationRevision = &revision
	}
	return ontologycontract.Summary[SummaryData]{Data: data, Meta: meta}, nil
}

func (s *Service) ListReferences(ctx context.Context, principal identity.Principal, query ReferenceQuery) (ontologycontract.CursorPage[DefinitionReference], error) {
	query.TenantID = strings.TrimSpace(query.TenantID)
	query.DefinitionID = strings.TrimSpace(query.DefinitionID)
	query.Direction = strings.ToLower(strings.TrimSpace(query.Direction))
	if query.Direction == "" {
		query.Direction = "both"
	}
	if !validKind(query.Kind) || query.Kind == KindEvent || !validID(query.DefinitionID) || (query.Direction != "in" && query.Direction != "out" && query.Direction != "both") {
		return ontologycontract.CursorPage[DefinitionReference]{}, ontologycontract.ErrInvalidRequest
	}
	limit, err := ontologycontract.NormalizeLimit(query.Limit, ontologycontract.DefaultChildListLimit, ontologycontract.MaxPageLimit)
	if err != nil {
		return ontologycontract.CursorPage[DefinitionReference]{}, err
	}
	authorized, err := s.authorize(ctx, principal, query.TenantID)
	if err != nil {
		return ontologycontract.CursorPage[DefinitionReference]{}, err
	}
	policyState, err := s.resolvePolicyState(ctx, principal, authorized)
	if err != nil {
		return ontologycontract.CursorPage[DefinitionReference]{}, err
	}
	bounded, budget, cancel, err := s.budgets.Start(ctx, ontologycontract.QueryStandard)
	if err != nil {
		return ontologycontract.CursorPage[DefinitionReference]{}, err
	}
	defer cancel()
	if err := budget.ConsumeStoreReads(1); err != nil {
		return ontologycontract.CursorPage[DefinitionReference]{}, err
	}
	target, err := s.readOne(bounded, query.TenantID, query.Kind, query.DefinitionID)
	if err != nil {
		if exactReadUnavailable(err) {
			return ontologycontract.CursorPage[DefinitionReference]{}, ErrUnavailable
		}
		return ontologycontract.CursorPage[DefinitionReference]{}, queryError(bounded, budget, err)
	}
	if err := budget.ConsumeCandidates(1); err != nil {
		return ontologycontract.CursorPage[DefinitionReference]{}, err
	}
	visibleTarget, err := s.visibleRows(bounded, principal, query.TenantID, policyState, []rawDefinition{target})
	if err != nil || len(visibleTarget) != 1 {
		if err != nil {
			return ontologycontract.CursorPage[DefinitionReference]{}, queryError(bounded, budget, err)
		}
		return ontologycontract.CursorPage[DefinitionReference]{}, ErrUnavailable
	}
	queryHash, err := ontologycontract.CanonicalQueryHash(struct {
		Kind      DefinitionKind `json:"kind"`
		ID        string         `json:"id"`
		Direction string         `json:"direction"`
		Limit     int            `json:"limit"`
		Version   int            `json:"version"`
	}{query.Kind, query.DefinitionID, query.Direction, limit, 2})
	if err != nil {
		return ontologycontract.CursorPage[DefinitionReference]{}, err
	}
	binding := schemaCursorBinding(authorized, policyState, queryHash)
	position, err := s.resolveReferenceCursor(query.Cursor, binding, policyState.cacheable, query.Direction)
	if err != nil {
		return ontologycontract.CursorPage[DefinitionReference]{}, err
	}

	rows := make([]referenceRow, 0, limit)
	if position.phase == referenceCursorPhaseOutgoing {
		if query.Direction == "in" {
			return ontologycontract.CursorPage[DefinitionReference]{}, ontologycontract.ErrInvalidCursor
		}
		outgoing, err := s.listVisibleOutgoingReferences(bounded, budget, principal, query.TenantID, policyState, visibleTarget[0])
		if err != nil {
			return ontologycontract.CursorPage[DefinitionReference]{}, err
		}
		if position.offset > len(outgoing) {
			return ontologycontract.CursorPage[DefinitionReference]{}, ontologycontract.ErrInvalidCursor
		}
		end := min(position.offset+limit, len(outgoing))
		rows = append(rows, outgoing[position.offset:end]...)
		if end < len(outgoing) {
			position.offset = end
			return s.newReferencePage(rows, limit, true, position, binding, policyState.cacheable, budget)
		}
		if query.Direction == "out" {
			return s.newReferencePage(rows, limit, false, referenceCursorPosition{}, binding, policyState.cacheable, budget)
		}
		position = referenceCursorPosition{phase: referenceCursorPhaseIncoming}
		if len(rows) == limit {
			return s.newReferencePage(rows, limit, true, position, binding, policyState.cacheable, budget)
		}
	}
	if position.phase != referenceCursorPhaseIncoming || query.Direction == "out" {
		return ontologycontract.CursorPage[DefinitionReference]{}, ontologycontract.ErrInvalidCursor
	}
	rows, hasMore, nextPosition, err := s.scanIncomingReferences(bounded, budget, principal, query.TenantID, policyState, visibleTarget[0].key(), position, limit, rows)
	if err != nil {
		return ontologycontract.CursorPage[DefinitionReference]{}, err
	}
	return s.newReferencePage(rows, limit, hasMore, nextPosition, binding, policyState.cacheable, budget)
}

func (s *Service) listVisibleOutgoingReferences(ctx context.Context, budget *ontologycontract.BudgetTracker, principal identity.Principal, tenantID string, state schemaPolicyState, source rawDefinition) ([]referenceRow, error) {
	references := uniqueRawReferences(source.references())
	targets, err := s.visibleReferenceTargets(ctx, budget, principal, tenantID, state, references)
	if err != nil {
		return nil, err
	}
	rows := make([]referenceRow, 0, len(references))
	seen := map[string]struct{}{}
	for _, ref := range references {
		target, ok := targets[ref.key]
		if !ok {
			continue
		}
		rows = appendUniqueReference(rows, seen, referenceRow{
			item:      DefinitionReference{Kind: target.kind, ID: target.id, Label: target.label, Relation: ref.relation, Direction: "out"},
			sourceKey: source.key(), relation: ref.relation, targetKey: ref.key, direction: "out",
		})
	}
	sort.Slice(rows, func(i, j int) bool { return referenceSeek(rows[i]) < referenceSeek(rows[j]) })
	return rows, nil
}

func (s *Service) visibleReferenceTargets(ctx context.Context, budget *ontologycontract.BudgetTracker, principal identity.Principal, tenantID string, state schemaPolicyState, references []rawReference) (map[string]rawDefinition, error) {
	idsByKind := map[DefinitionKind][]string{}
	seen := map[string]struct{}{}
	for _, ref := range references {
		if _, exists := seen[ref.key]; exists {
			continue
		}
		seen[ref.key] = struct{}{}
		idsByKind[ref.kind] = append(idsByKind[ref.kind], ref.id)
	}
	targets := make([]rawDefinition, 0, len(seen))
	for _, kind := range definitionKindOrder {
		if kind == KindAction || len(idsByKind[kind]) == 0 {
			continue
		}
		if err := budget.ConsumeStoreReads(1); err != nil {
			return nil, err
		}
		rows, err := s.readMany(ctx, tenantID, kind, idsByKind[kind])
		if err != nil {
			return nil, queryError(ctx, budget, err)
		}
		targets = append(targets, rows...)
	}
	for _, id := range idsByKind[KindAction] {
		if err := budget.ConsumeStoreReads(1); err != nil {
			return nil, err
		}
		row, err := s.readOne(ctx, tenantID, KindAction, id)
		if exactReadUnavailable(err) {
			continue
		}
		if err != nil {
			return nil, queryError(ctx, budget, err)
		}
		targets = append(targets, row)
	}
	if err := budget.ConsumeCandidates(len(targets)); err != nil {
		return nil, err
	}
	visible, err := s.visibleRows(ctx, principal, tenantID, state, targets)
	if err != nil {
		return nil, queryError(ctx, budget, err)
	}
	byKey := make(map[string]rawDefinition, len(visible))
	for _, target := range visible {
		byKey[target.key()] = target
	}
	return byKey, nil
}

func (s *Service) scanIncomingReferences(ctx context.Context, budget *ontologycontract.BudgetTracker, principal identity.Principal, tenantID string, state schemaPolicyState, targetKey string, position referenceCursorPosition, limit int, rows []referenceRow) ([]referenceRow, bool, referenceCursorPosition, error) {
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		seen[referenceSeek(row)] = struct{}{}
	}
	for position.group < len(definitionKindOrder) {
		if len(rows) >= limit {
			return rows, true, position, nil
		}
		if err := budget.ConsumeStoreReads(1); err != nil {
			if errors.Is(err, ontologycontract.ErrQueryBudgetExceeded) {
				return rows, true, position, nil
			}
			return nil, false, referenceCursorPosition{}, err
		}
		page, err := s.readPage(ctx, tenantID, definitionKindOrder[position.group], position.after, schemaScanBatch)
		if err != nil {
			return nil, false, referenceCursorPosition{}, queryError(ctx, budget, err)
		}
		if err := budget.ConsumeCandidates(page.scanned); err != nil {
			if errors.Is(err, ontologycontract.ErrQueryBudgetExceeded) {
				return rows, true, position, nil
			}
			return nil, false, referenceCursorPosition{}, err
		}
		visible, err := s.visibleRows(ctx, principal, tenantID, state, page.items)
		if err != nil {
			return nil, false, referenceCursorPosition{}, queryError(ctx, budget, err)
		}
		visibleByKey := make(map[string]rawDefinition, len(visible))
		for _, source := range visible {
			visibleByKey[source.key()] = source
		}
		lastAfter := position.after
		for _, candidate := range page.items {
			source, visible := visibleByKey[candidate.key()]
			if !visible {
				lastAfter = candidate.id
				continue
			}
			if position.sourceID != "" && source.id != position.sourceID {
				position.sourceID = ""
				position.referenceOffset = 0
			}
			refs := source.references()
			if err := budget.ConsumeCandidates(len(refs)); err != nil {
				if errors.Is(err, ontologycontract.ErrQueryBudgetExceeded) {
					return rows, true, referenceCursorPosition{phase: referenceCursorPhaseIncoming, group: position.group, after: lastAfter}, nil
				}
				return nil, false, referenceCursorPosition{}, err
			}
			incoming := incomingReferenceRows(source, targetKey)
			start := 0
			if source.id == position.sourceID {
				start = position.referenceOffset
			}
			if start > len(incoming) {
				return nil, false, referenceCursorPosition{}, ontologycontract.ErrInvalidCursor
			}
			available := limit - len(rows)
			if available == 0 {
				return rows, true, referenceCursorPosition{phase: referenceCursorPhaseIncoming, group: position.group, after: lastAfter, sourceID: source.id, referenceOffset: start}, nil
			}
			end := min(start+available, len(incoming))
			for _, row := range incoming[start:end] {
				rows = appendUniqueReference(rows, seen, row)
			}
			if end < len(incoming) {
				return rows, true, referenceCursorPosition{phase: referenceCursorPhaseIncoming, group: position.group, after: lastAfter, sourceID: source.id, referenceOffset: end}, nil
			}
			lastAfter = source.id
			position.sourceID = ""
			position.referenceOffset = 0
		}
		if page.hasMore {
			position = referenceCursorPosition{phase: referenceCursorPhaseIncoming, group: position.group, after: page.nextAfter}
			continue
		}
		position = referenceCursorPosition{phase: referenceCursorPhaseIncoming, group: position.group + 1}
	}
	return rows, false, referenceCursorPosition{}, nil
}

func incomingReferenceRows(source rawDefinition, targetKey string) []referenceRow {
	rows := make([]referenceRow, 0)
	seen := map[string]struct{}{}
	for _, ref := range source.references() {
		if ref.key != targetKey {
			continue
		}
		rows = appendUniqueReference(rows, seen, referenceRow{
			item:      DefinitionReference{Kind: source.kind, ID: source.id, Label: source.label, Relation: ref.relation, Direction: "in"},
			sourceKey: source.key(), relation: ref.relation, targetKey: ref.key, direction: "in",
		})
	}
	sort.Slice(rows, func(i, j int) bool { return referenceSeek(rows[i]) < referenceSeek(rows[j]) })
	return rows
}

func uniqueRawReferences(references []rawReference) []rawReference {
	rows := append([]rawReference(nil), references...)
	sort.Slice(rows, func(i, j int) bool {
		left := rows[i].key + "\x00" + rows[i].relation
		right := rows[j].key + "\x00" + rows[j].relation
		return left < right
	})
	result := make([]rawReference, 0, len(rows))
	seen := map[string]struct{}{}
	for _, row := range rows {
		key := row.key + "\x00" + row.relation
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, row)
	}
	return result
}

func (s *Service) newReferencePage(rows []referenceRow, limit int, hasMore bool, position referenceCursorPosition, binding ontologycontract.CursorBinding, cacheable bool, budget *ontologycontract.BudgetTracker) (ontologycontract.CursorPage[DefinitionReference], error) {
	items := make([]DefinitionReference, len(rows))
	for index := range rows {
		items[index] = rows[index].item
	}
	if err := budget.ConsumeResults(len(items)); err != nil {
		return ontologycontract.CursorPage[DefinitionReference]{}, err
	}
	next := ""
	if hasMore {
		var err error
		next, err = s.issueReferenceCursor(binding, cacheable, position)
		if err != nil {
			return ontologycontract.CursorPage[DefinitionReference]{}, err
		}
	}
	return ontologycontract.NewCursorPage(items, limit, hasMore, next)
}

func definitionMatches(item DefinitionItem, q DefinitionListQuery) bool {
	if q.Origin != "" && item.Origin != q.Origin {
		return false
	}
	if q.Status != "" && item.Status != q.Status {
		return false
	}
	search := strings.ToLower(q.Search)
	return search == "" || strings.Contains(strings.ToLower(item.ID+"\x00"+item.Label+"\x00"+item.OwnerDomain), search)
}
func normalizeDefinitionQuery(q DefinitionListQuery) (DefinitionListQuery, int, error) {
	q.TenantID = strings.TrimSpace(q.TenantID)
	q.Search = strings.TrimSpace(q.Search)
	q.Kind = DefinitionKind(strings.ToLower(strings.TrimSpace(string(q.Kind))))
	q.Origin = DefinitionOrigin(strings.ToLower(strings.TrimSpace(string(q.Origin))))
	q.Status = DefinitionStatus(strings.ToLower(strings.TrimSpace(string(q.Status))))
	if q.TenantID == "" || len(q.TenantID) > 256 || !validSearch(q.Search) || (q.Kind != "" && !validKind(q.Kind)) || (q.Origin != "" && q.Origin != OriginSystem && q.Origin != OriginTenant) || (q.Status != "" && q.Status != StatusActive && q.Status != StatusDisabled) {
		return q, 0, ontologycontract.ErrInvalidRequest
	}
	limit, err := ontologycontract.NormalizeLimit(q.Limit, ontologycontract.DefaultListLimit, ontologycontract.MaxPageLimit)
	return q, limit, err
}
func validSearch(value string) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= 128
}
func validKind(kind DefinitionKind) bool {
	switch kind {
	case KindObject, KindLink, KindProperty, KindMapping, KindAction:
		return true
	}
	return false
}
func validID(id string) bool {
	id = strings.TrimSpace(id)
	return id != "" && len(id) <= 256
}
func selectedKinds(kind DefinitionKind) []DefinitionKind {
	if kind != "" {
		return []DefinitionKind{kind}
	}
	return append([]DefinitionKind(nil), definitionKindOrder...)
}
func (s *Service) authorize(ctx context.Context, p identity.Principal, tenant string) (ontologyscope.AuthorizedScopeContext, error) {
	if s == nil || s.gate == nil {
		return ontologyscope.AuthorizedScopeContext{}, ontologyscope.ErrDependencyUnavailable
	}
	authorized, err := s.gate.AuthorizeRead(ctx, identity.NormalizePrincipal(p), strings.TrimSpace(tenant))
	if ontologyscope.IsNonDisclosingReadError(err) {
		return ontologyscope.AuthorizedScopeContext{}, ErrUnavailable
	}
	return authorized, err
}
func (s *Service) resolvePolicyState(ctx context.Context, principal identity.Principal, scope ontologyscope.AuthorizedScopeContext) (schemaPolicyState, error) {
	binding := scope.Binding()
	descriptor := datavisibility.ResourceDescriptor{
		Key: "schema-policy-probe", Kind: datavisibility.ResourceDefinition,
		TenantID: definitionPolicyTenant(binding.TenantID), Type: string(KindObject),
	}
	decision, err := s.visibility.Evaluate(ctx, datavisibility.EvaluateRequest{
		Principal: identity.NormalizePrincipal(principal), Purpose: "ontology.schema.read",
		Resources: []datavisibility.ResourceDescriptor{descriptor},
	})
	if err != nil {
		return schemaPolicyState{}, dependencyError(err)
	}
	if _, ok := decision.Resource(descriptor.Key); !ok {
		return schemaPolicyState{}, dependencyError(errors.New("schema policy probe is incomplete"))
	}
	decisionRevision := strings.TrimSpace(decision.PolicyRevision)
	return schemaPolicyState{
		decisionRevision: decisionRevision,
		cursorRevision:   schemaPolicyRevision(scope.Snapshot().PolicyRevision, decisionRevision),
		cacheable:        scope.Snapshot().Cacheable() && decision.Cacheable(),
	}, nil
}

func schemaPolicyRevision(snapshotRevision, decisionRevision string) string {
	snapshotRevision = strings.TrimSpace(snapshotRevision)
	decisionRevision = strings.TrimSpace(decisionRevision)
	if snapshotRevision == "" && decisionRevision == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(snapshotRevision + "\x00" + decisionRevision))
	return hex.EncodeToString(sum[:])
}

func schemaCursorBinding(scope ontologyscope.AuthorizedScopeContext, state schemaPolicyState, hash string) ontologycontract.CursorBinding {
	snap := scope.Snapshot()
	return ontologycontract.CursorBinding{PrincipalKey: snap.PrincipalKey, TenantID: scope.Binding().TenantID, BindingVersion: scope.Binding().BindingVersion, QueryHash: hash, AuthorizationRevision: snap.AuthorizationRevision, PolicyRevision: state.cursorRevision}
}
func (s *Service) resolveCursor(token string, b ontologycontract.CursorBinding, cacheable bool, groups int) (int, string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return 0, "", nil
	}
	var p ontologycontract.CursorPosition
	var err error
	if strings.HasPrefix(token, "oc1.") && cacheable && s.cursors.Encrypted != nil {
		p, err = s.cursors.Encrypted.Decode(token, b)
	} else if strings.HasPrefix(token, "ocs1.") {
		p, err = s.cursors.Stateful.Resolve(token, b)
	} else if strings.HasPrefix(token, "oc1.") {
		err = ontologycontract.ErrCursorStale
	} else {
		err = ontologycontract.ErrInvalidCursor
	}
	if err != nil {
		return 0, "", err
	}
	if len(p.Seek) != 2 {
		return 0, "", ontologycontract.ErrInvalidCursor
	}
	g, e := strconv.Atoi(p.Seek[0])
	if e != nil || g < 0 || g >= groups {
		return 0, "", ontologycontract.ErrInvalidCursor
	}
	return g, p.Seek[1], nil
}
func (s *Service) issueCursor(b ontologycontract.CursorBinding, cacheable bool, g int, after string) (string, error) {
	p := ontologycontract.CursorPosition{Binding: b, Seek: []string{strconv.Itoa(g), after}}
	if cacheable && s.cursors.Encrypted != nil {
		return s.cursors.Encrypted.Encode(p)
	}
	return s.cursors.Stateful.Issue(p)
}

func referenceSeek(row referenceRow) string {
	return strings.Join([]string{row.sourceKey, row.relation, row.targetKey, row.direction}, "\x00")
}

func appendUniqueReference(rows []referenceRow, seen map[string]struct{}, row referenceRow) []referenceRow {
	seek := referenceSeek(row)
	if _, exists := seen[seek]; exists {
		return rows
	}
	seen[seek] = struct{}{}
	return append(rows, row)
}

func (s *Service) resolveReferenceCursor(token string, binding ontologycontract.CursorBinding, cacheable bool, direction string) (referenceCursorPosition, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		if direction == "in" {
			return referenceCursorPosition{phase: referenceCursorPhaseIncoming}, nil
		}
		return referenceCursorPosition{phase: referenceCursorPhaseOutgoing}, nil
	}
	var position ontologycontract.CursorPosition
	var err error
	if strings.HasPrefix(token, "oc1.") && cacheable && s.cursors.Encrypted != nil {
		position, err = s.cursors.Encrypted.Decode(token, binding)
	} else if strings.HasPrefix(token, "ocs1.") {
		position, err = s.cursors.Stateful.Resolve(token, binding)
	} else if strings.HasPrefix(token, "oc1.") {
		err = ontologycontract.ErrCursorStale
	} else {
		err = ontologycontract.ErrInvalidCursor
	}
	if err != nil {
		return referenceCursorPosition{}, err
	}
	if len(position.Seek) != 7 || position.Seek[0] != "references-v2" {
		return referenceCursorPosition{}, ontologycontract.ErrInvalidCursor
	}
	offset, offsetErr := strconv.Atoi(position.Seek[2])
	group, groupErr := strconv.Atoi(position.Seek[3])
	referenceOffset, referenceOffsetErr := strconv.Atoi(position.Seek[6])
	if offsetErr != nil || groupErr != nil || referenceOffsetErr != nil || offset < 0 || group < 0 || referenceOffset < 0 || (position.Seek[1] != referenceCursorPhaseOutgoing && position.Seek[1] != referenceCursorPhaseIncoming) {
		return referenceCursorPosition{}, ontologycontract.ErrInvalidCursor
	}
	if position.Seek[1] == referenceCursorPhaseIncoming && group >= len(definitionKindOrder) {
		return referenceCursorPosition{}, ontologycontract.ErrInvalidCursor
	}
	if position.Seek[1] == referenceCursorPhaseOutgoing && direction == "in" {
		return referenceCursorPosition{}, ontologycontract.ErrInvalidCursor
	}
	return referenceCursorPosition{phase: position.Seek[1], offset: offset, group: group, after: referenceCursorValue(position.Seek[4]), sourceID: referenceCursorValue(position.Seek[5]), referenceOffset: referenceOffset}, nil
}

func (s *Service) issueReferenceCursor(binding ontologycontract.CursorBinding, cacheable bool, reference referenceCursorPosition) (string, error) {
	position := ontologycontract.CursorPosition{Binding: binding, Seek: []string{
		"references-v2", reference.phase, strconv.Itoa(reference.offset), strconv.Itoa(reference.group), referenceCursorSeekValue(reference.after), referenceCursorSeekValue(reference.sourceID), strconv.Itoa(reference.referenceOffset),
	}}
	if cacheable && s.cursors.Encrypted != nil {
		return s.cursors.Encrypted.Encode(position)
	}
	return s.cursors.Stateful.Issue(position)
}

func referenceCursorSeekValue(value string) string {
	if value == "" {
		return referenceCursorEmpty
	}
	return value
}

func referenceCursorValue(value string) string {
	if value == referenceCursorEmpty {
		return ""
	}
	return value
}

func dependencyError(err error) error {
	if errors.Is(err, ontologycontract.ErrInvalidRequest) || errors.Is(err, ontologycontract.ErrInvalidCursor) || errors.Is(err, ontologycontract.ErrCursorStale) || errors.Is(err, ontologycontract.ErrQueryBudgetExceeded) {
		return err
	}
	return fmt.Errorf("%w: %v", ontologyscope.ErrDependencyUnavailable, err)
}

func queryError(ctx context.Context, budget *ontologycontract.BudgetTracker, err error) error {
	if ctx != nil && ctx.Err() != nil {
		if budget != nil {
			return budget.CheckContext(ctx)
		}
		return fmt.Errorf("%w: %v", ontologycontract.ErrQueryBudgetExceeded, ctx.Err())
	}
	return dependencyError(err)
}

func exactReadUnavailable(err error) bool {
	return errors.Is(err, ErrUnavailable) ||
		errors.Is(err, ontology.ErrNotFound) ||
		errors.Is(err, actioncontract.ErrNotFound)
}
func schemaDataRevision(counts map[DefinitionKind]int64, latest time.Time, exact bool) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%t:%d:%d:%d:%d:%d:%d", exact, counts[KindObject], counts[KindLink], counts[KindProperty], counts[KindMapping], counts[KindAction], latest.UnixNano())))
	return hex.EncodeToString(sum[:])
}
