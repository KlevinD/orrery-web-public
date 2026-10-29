package contextgraph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/app/rebac"
)

type Store interface {
	Snapshot(ctx context.Context, tenantID string) ([]Object, []Link, []Fact, []Evidence, error)
}

type TemporalStore interface {
	SnapshotAsOf(ctx context.Context, tenantID string, businessAsOf, knownAt time.Time) ([]Object, []Link, []Fact, []Evidence, error)
}

type CurrentSubgraphStore interface {
	SnapshotCurrentSubgraph(ctx context.Context, tenantID string, subjectRefs []Ref, maxDepth int, limits CurrentSubgraphLimits) ([]Object, []Link, []Fact, []Evidence, error)
}

type TemporalSubgraphStore interface {
	SnapshotSubgraphAsOf(ctx context.Context, tenantID string, subjectRefs []Ref, maxDepth int, limits CurrentSubgraphLimits, businessAsOf, knownAt time.Time) ([]Object, []Link, []Fact, []Evidence, error)
}

type SchemaProvider interface {
	SchemaDictionary(ctx context.Context, tenantID string) (SchemaDictionary, error)
}

type PolicyProvider interface {
	DecideContext(ctx context.Context, req BuildContextBundleRequest, objects []Object) (PermissionDecision, error)
}

type ReBACStorePort interface {
	CheckRelation(ctx context.Context, check rebac.RelationCheck) (bool, rebac.RelationTuple, error)
}

type ObjectVisibilityProvider interface {
	VisibleObjectIDs(context.Context, BuildContextBundleRequest, []Object) (map[string]bool, error)
}

type Service struct {
	store      Store
	schema     SchemaProvider
	policy     PolicyProvider
	rebac      ReBACStorePort
	visibility ObjectVisibilityProvider
	now        func() time.Time
}

type Option func(*Service)

func WithPolicyProvider(policy PolicyProvider) Option {
	return func(service *Service) {
		service.policy = policy
	}
}

func WithSchemaProvider(schema SchemaProvider) Option {
	return func(service *Service) {
		service.schema = schema
	}
}

func WithReBACStore(store ReBACStorePort) Option {
	return func(service *Service) {
		service.rebac = store
	}
}

func WithObjectVisibilityProvider(provider ObjectVisibilityProvider) Option {
	return func(service *Service) {
		service.visibility = provider
	}
}

func WithClock(now func() time.Time) Option {
	return func(service *Service) {
		if now != nil {
			service.now = now
		}
	}
}

func NewService(store Store, options ...Option) *Service {
	service := &Service{
		store: store,
		now:   func() time.Time { return time.Now().UTC() },
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *Service) BuildContextBundle(ctx context.Context, req BuildContextBundleRequest) (ContextBundle, error) {
	if s == nil || s.store == nil {
		return ContextBundle{}, fmt.Errorf("context graph store is required")
	}
	req = normalizeBuildRequest(req)
	if req.TenantID == "" || req.Consumer == "" || req.Purpose == "" || req.ActorRef.ID == "" || len(req.SubjectRefs) == 0 || req.Query != "" || req.MaxDepth > 2 {
		return ContextBundle{}, fmt.Errorf("%w: tenant_id, consumer, purpose, actor_ref, subject_refs, and depth 0..2 are required", ErrQueryInvalid)
	}
	temporalRequested := !req.BusinessAsOf.IsZero() || !req.KnownAt.IsZero()
	now := s.now().UTC()
	if req.BusinessAsOf.IsZero() {
		req.BusinessAsOf = now
	}
	if req.KnownAt.IsZero() {
		req.KnownAt = now
	}
	var objects []Object
	var links []Link
	var facts []Fact
	var evidence []Evidence
	var err error
	if temporalRequested {
		store, ok := s.store.(TemporalSubgraphStore)
		if !ok {
			return ContextBundle{}, fmt.Errorf("%w: bounded temporal subgraph store is required", ErrQueryInvalid)
		}
		objects, links, facts, evidence, err = store.SnapshotSubgraphAsOf(ctx, req.TenantID, req.SubjectRefs, req.MaxDepth, DefaultCurrentSubgraphLimits(), req.BusinessAsOf, req.KnownAt)
	} else {
		store, ok := s.store.(CurrentSubgraphStore)
		if !ok {
			return ContextBundle{}, fmt.Errorf("%w: bounded current subgraph store is required", ErrQueryInvalid)
		}
		objects, links, facts, evidence, err = store.SnapshotCurrentSubgraph(ctx, req.TenantID, req.SubjectRefs, req.MaxDepth, DefaultCurrentSubgraphLimits())
	}
	if err != nil {
		return ContextBundle{}, err
	}
	selectedIDs := selectObjectIDs(req, objects, links)
	selectedObjects := filterObjects(objects, selectedIDs, req.IncludeKinds)
	visibilityHidden := make([]string, 0)
	if s.visibility != nil {
		visibleIDs, err := s.visibility.VisibleObjectIDs(ctx, req, selectedObjects)
		if err != nil {
			return ContextBundle{}, err
		}
		for _, object := range selectedObjects {
			if !visibleIDs[object.ID] {
				visibilityHidden = append(visibilityHidden, object.ID)
			}
		}
	}
	rebacHidden, err := s.rebacHiddenObjectIDs(ctx, req, selectedObjects)
	if err != nil {
		return ContextBundle{}, err
	}
	decision := PermissionDecision{}
	if s.policy != nil {
		decision, err = s.policy.DecideContext(ctx, req, selectedObjects)
		if err != nil {
			return ContextBundle{}, err
		}
	}
	decision.HiddenObjectIDs = mergeStrings(mergeStrings(decision.HiddenObjectIDs, visibilityHidden), rebacHidden)
	hidden := stringSet(decision.HiddenObjectIDs)
	visibleObjects, maskedFields := applyObjectPolicy(selectedObjects, hidden, decision.MaskedFields)
	visibleIDs := map[string]struct{}{}
	for _, object := range visibleObjects {
		visibleIDs[object.ID] = struct{}{}
	}
	visibleLinks := filterLinks(links, selectedIDs, visibleIDs)
	visibleFacts := filterFacts(facts, visibleIDs, decision.MaskedFields)
	visibleEvidence := filterEvidence(evidence, visibleObjects, visibleLinks, visibleFacts)
	schema, err := s.schemaDictionary(ctx, req.TenantID)
	if err != nil {
		return ContextBundle{}, err
	}
	schema = relevantSchemaDictionary(schema, visibleObjects, visibleLinks, visibleFacts)
	bundleID, err := contextBundleID(req, schema.VersionHash)
	if err != nil {
		return ContextBundle{}, err
	}
	bundle := ContextBundle{
		BundleID:              bundleID,
		TenantID:              req.TenantID,
		Consumer:              req.Consumer,
		Purpose:               req.Purpose,
		ActorRef:              req.ActorRef,
		Schema:                schema,
		Objects:               visibleObjects,
		Links:                 visibleLinks,
		Facts:                 visibleFacts,
		Evidence:              visibleEvidence,
		PermissionConstraints: visiblePermissionConstraints(decision.Constraints, visibleIDs),
		Freshness: Freshness{
			AsOf: req.KnownAt, BusinessAsOf: req.BusinessAsOf, KnownAt: req.KnownAt,
		},
		Confidence: bundleConfidence(visibleObjects, visibleFacts, visibleEvidence),
		BuildTrace: BuildTrace{
			SeedObjectIDs:  seedObjectIDs(req.SubjectRefs),
			IncludedKinds:  req.IncludeKinds,
			MaskedFields:   maskedFields,
			KnowledgeQuery: req.Query,
		},
	}
	if req.TokenBudget > 0 {
		if err := applyContextBundleTokenBudget(&bundle, req.TokenBudget, req.SubjectRefs); err != nil {
			return ContextBundle{}, err
		}
	}
	return bundle, nil
}

func visiblePermissionConstraints(constraints []PermissionConstraint, visibleIDs map[string]struct{}) []PermissionConstraint {
	out := make([]PermissionConstraint, 0, len(constraints))
	for _, constraint := range constraints {
		objectID := strings.TrimSpace(constraint.ObjectRef.ID)
		if objectID != "" {
			if _, ok := visibleIDs[objectID]; !ok {
				continue
			}
		}
		constraint.MaskedFields = append([]string(nil), constraint.MaskedFields...)
		out = append(out, constraint)
	}
	return out
}

func relevantSchemaDictionary(schema SchemaDictionary, objects []Object, links []Link, facts []Fact) SchemaDictionary {
	objectTypes := make(map[string]bool)
	for _, object := range objects {
		objectTypes[object.ObjectType] = true
	}
	linkTypes := make(map[string]bool)
	for _, link := range links {
		linkTypes[link.LinkType] = true
	}
	propertyKeys := make(map[string]bool)
	for _, object := range objects {
		for property := range object.Properties {
			propertyKeys[object.ObjectType+"\x00"+property] = true
		}
	}
	for _, fact := range facts {
		propertyKeys[fact.Subject.Kind+"\x00"+fact.Predicate] = true
	}
	out := SchemaDictionary{VersionHash: schema.VersionHash}
	for _, definition := range schema.ObjectTypes {
		if objectTypes[definition.ID] {
			out.ObjectTypes = append(out.ObjectTypes, definition)
		}
	}
	for _, definition := range schema.LinkTypes {
		if linkTypes[definition.ID] {
			out.LinkTypes = append(out.LinkTypes, definition)
		}
	}
	for _, definition := range schema.PropertyTypes {
		if propertyKeys[definition.ObjectTypeID+"\x00"+definition.PropertyID] {
			out.PropertyTypes = append(out.PropertyTypes, definition)
		}
	}
	for _, definition := range schema.ActionTypes {
		for _, objectType := range definition.ObjectTypeRefs {
			if objectTypes[objectType] {
				out.ActionTypes = append(out.ActionTypes, definition)
				break
			}
		}
	}
	return out
}

func contextBundleID(req BuildContextBundleRequest, schemaHash string) (string, error) {
	subjects := append([]Ref(nil), req.SubjectRefs...)
	sort.Slice(subjects, func(i, j int) bool {
		return subjects[i].Kind+"\x00"+subjects[i].ID < subjects[j].Kind+"\x00"+subjects[j].ID
	})
	kinds := append([]string(nil), req.IncludeKinds...)
	sort.Strings(kinds)
	raw, err := json.Marshal(struct {
		Consumer     string    `json:"consumer"`
		Purpose      string    `json:"purpose"`
		Actor        Ref       `json:"actor"`
		Subjects     []Ref     `json:"subjects"`
		Kinds        []string  `json:"kinds"`
		Depth        int       `json:"depth"`
		BusinessAsOf time.Time `json:"business_as_of"`
		KnownAt      time.Time `json:"known_at"`
		SchemaHash   string    `json:"schema_hash"`
	}{req.Consumer, req.Purpose, req.ActorRef, subjects, kinds, req.MaxDepth, req.BusinessAsOf, req.KnownAt, schemaHash})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "bundle:" + req.TenantID + ":" + hex.EncodeToString(sum[:]), nil
}

func applyContextBundleTokenBudget(bundle *ContextBundle, tokenBudget int, subjects []Ref) error {
	if bundle == nil || tokenBudget <= 0 {
		return nil
	}
	seedIDs := make(map[string]bool, len(subjects))
	for _, ref := range subjects {
		seedIDs[ref.ID] = true
	}
	for contextBundleEstimatedTokens(*bundle) > tokenBudget {
		bundle.Partial = true
		bundle.PartialReason = "token_budget_exceeded"
		switch {
		case len(bundle.Evidence) > 0:
			bundle.Evidence = bundle.Evidence[:len(bundle.Evidence)-1]
			bundle.Truncated.Evidence++
		case len(bundle.Facts) > 0:
			bundle.Facts = bundle.Facts[:len(bundle.Facts)-1]
			bundle.Truncated.Facts++
		case len(bundle.Links) > 0:
			bundle.Links = bundle.Links[:len(bundle.Links)-1]
			bundle.Truncated.Links++
		default:
			removed := false
			for index := len(bundle.Objects) - 1; index >= 0; index-- {
				if seedIDs[bundle.Objects[index].ID] {
					continue
				}
				bundle.Objects = append(bundle.Objects[:index], bundle.Objects[index+1:]...)
				bundle.Truncated.Objects++
				removed = true
				break
			}
			if !removed {
				return fmt.Errorf("%w: requested seed context exceeds token budget", ErrQueryBudgetExceeded)
			}
		}
	}
	return nil
}

func contextBundleEstimatedTokens(bundle ContextBundle) int {
	raw, _ := json.Marshal(bundle)
	return (len(raw) + 3) / 4
}

func (s *Service) schemaDictionary(ctx context.Context, tenantID string) (SchemaDictionary, error) {
	if s == nil || s.schema == nil {
		return SchemaDictionary{}, nil
	}
	return s.schema.SchemaDictionary(ctx, tenantID)
}

func (s *Service) rebacHiddenObjectIDs(ctx context.Context, req BuildContextBundleRequest, objects []Object) ([]string, error) {
	if s == nil || s.rebac == nil {
		return nil, nil
	}
	hidden := make([]string, 0)
	for _, object := range objects {
		allowed, _, err := s.rebac.CheckRelation(ctx, rebac.RelationCheck{
			TenantID: req.TenantID,
			Subject: rebac.Ref{
				Kind: req.ActorRef.Kind,
				ID:   req.ActorRef.ID,
			},
			Relation: "viewer",
			Object: rebac.Ref{
				Kind: object.ObjectType,
				ID:   object.ID,
			},
			At: req.BusinessAsOf,
		})
		if err != nil {
			return nil, err
		}
		if !allowed {
			hidden = append(hidden, object.ID)
		}
	}
	sort.Strings(hidden)
	return hidden, nil
}

func normalizeBuildRequest(req BuildContextBundleRequest) BuildContextBundleRequest {
	req.TenantID = strings.TrimSpace(req.TenantID)
	req.Consumer = strings.TrimSpace(req.Consumer)
	req.Purpose = strings.TrimSpace(req.Purpose)
	req.Query = strings.TrimSpace(req.Query)
	req.ActorRef.Kind = strings.ToLower(strings.TrimSpace(req.ActorRef.Kind))
	req.ActorRef.ID = strings.TrimSpace(req.ActorRef.ID)
	for i := range req.SubjectRefs {
		req.SubjectRefs[i].Kind = strings.ToLower(strings.TrimSpace(req.SubjectRefs[i].Kind))
		req.SubjectRefs[i].ID = strings.TrimSpace(req.SubjectRefs[i].ID)
	}
	for i := range req.IncludeKinds {
		req.IncludeKinds[i] = strings.ToLower(strings.TrimSpace(req.IncludeKinds[i]))
	}
	if req.MaxDepth < 0 {
		req.MaxDepth = 0
	}
	if !req.BusinessAsOf.IsZero() {
		req.BusinessAsOf = req.BusinessAsOf.UTC()
	}
	if !req.KnownAt.IsZero() {
		req.KnownAt = req.KnownAt.UTC()
	}
	return req
}

func selectObjectIDs(req BuildContextBundleRequest, objects []Object, links []Link) map[string]struct{} {
	ids := map[string]struct{}{}
	for _, ref := range req.SubjectRefs {
		if ref.ID != "" {
			ids[ref.ID] = struct{}{}
		}
	}
	for depth := 0; depth < req.MaxDepth; depth++ {
		next := map[string]struct{}{}
		for _, link := range links {
			if _, ok := ids[link.FromObjectID]; ok {
				next[link.ToObjectID] = struct{}{}
			}
			if _, ok := ids[link.ToObjectID]; ok {
				next[link.FromObjectID] = struct{}{}
			}
		}
		for id := range next {
			ids[id] = struct{}{}
		}
	}
	valid := map[string]struct{}{}
	for _, object := range objects {
		if _, ok := ids[object.ID]; ok {
			valid[object.ID] = struct{}{}
		}
	}
	return valid
}

func filterObjects(objects []Object, selectedIDs map[string]struct{}, includeKinds []string) []Object {
	kinds := stringSet(includeKinds)
	out := make([]Object, 0, len(objects))
	for _, object := range objects {
		if _, ok := selectedIDs[object.ID]; !ok {
			continue
		}
		if len(kinds) > 0 {
			if _, ok := kinds[strings.ToLower(strings.TrimSpace(object.ObjectType))]; !ok {
				continue
			}
		}
		out = append(out, cloneObject(object))
	}
	return out
}

func applyObjectPolicy(objects []Object, hidden map[string]struct{}, masked map[string][]string) ([]Object, []string) {
	out := make([]Object, 0, len(objects))
	trace := []string{}
	for _, object := range objects {
		if _, ok := hidden[object.ID]; ok {
			continue
		}
		object = cloneObject(object)
		for _, field := range masked[object.ID] {
			field = strings.TrimSpace(field)
			if field == "" {
				continue
			}
			delete(object.Properties, field)
			trace = append(trace, object.ID+"."+field)
		}
		out = append(out, object)
	}
	sort.Strings(trace)
	return out, trace
}

func filterLinks(links []Link, selectedIDs map[string]struct{}, visibleIDs map[string]struct{}) []Link {
	out := make([]Link, 0, len(links))
	for _, link := range links {
		if strings.EqualFold(strings.TrimSpace(link.Status), "suggested") {
			continue
		}
		if _, ok := selectedIDs[link.FromObjectID]; !ok {
			continue
		}
		if _, ok := selectedIDs[link.ToObjectID]; !ok {
			continue
		}
		if _, ok := visibleIDs[link.FromObjectID]; !ok {
			continue
		}
		if _, ok := visibleIDs[link.ToObjectID]; !ok {
			continue
		}
		out = append(out, cloneLink(link))
	}
	return out
}

func filterFacts(facts []Fact, visibleIDs map[string]struct{}, masked map[string][]string) []Fact {
	out := make([]Fact, 0, len(facts))
	for _, fact := range facts {
		if _, ok := visibleIDs[fact.Subject.ID]; !ok {
			continue
		}
		hidden := false
		for _, field := range masked[fact.Subject.ID] {
			if field = strings.TrimSpace(field); field != "" && field == strings.TrimSpace(fact.Predicate) {
				hidden = true
				break
			}
		}
		if !hidden {
			out = append(out, cloneFact(fact))
		}
	}
	return out
}

func filterEvidence(evidence []Evidence, objects []Object, links []Link, facts []Fact) []Evidence {
	needed := map[string]struct{}{}
	for _, object := range objects {
		for _, ref := range object.EvidenceRefs {
			needed[ref.ID] = struct{}{}
		}
	}
	for _, link := range links {
		for _, ref := range link.EvidenceRefs {
			needed[ref.ID] = struct{}{}
		}
	}
	for _, fact := range facts {
		for _, ref := range fact.EvidenceRefs {
			needed[ref.ID] = struct{}{}
		}
	}
	out := make([]Evidence, 0, len(evidence))
	for _, item := range evidence {
		if _, ok := needed[item.ID]; ok {
			out = append(out, item)
		}
	}
	return out
}

func bundleConfidence(objects []Object, facts []Fact, evidence []Evidence) float64 {
	total := 0.0
	count := 0
	for _, object := range objects {
		if object.Confidence > 0 {
			total += object.Confidence
			count++
		}
	}
	for _, fact := range facts {
		if fact.Confidence > 0 {
			total += fact.Confidence
			count++
		}
	}
	for _, item := range evidence {
		if item.Confidence > 0 {
			total += item.Confidence
			count++
		}
	}
	if count == 0 {
		return 1
	}
	return total / float64(count)
}

func seedObjectIDs(refs []Ref) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if strings.TrimSpace(ref.ID) != "" {
			out = append(out, strings.TrimSpace(ref.ID))
		}
	}
	return out
}

func stringSet(values []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out[value] = struct{}{}
		}
	}
	return out
}

func mergeStrings(left []string, right []string) []string {
	seen := stringSet(left)
	out := append([]string(nil), left...)
	for _, value := range right {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		out = append(out, value)
		seen[value] = struct{}{}
	}
	sort.Strings(out)
	return out
}
