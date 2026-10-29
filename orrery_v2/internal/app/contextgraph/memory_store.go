package contextgraph

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type WriterStore interface {
	SaveObject(ctx context.Context, object Object) error
	SaveLink(ctx context.Context, link Link) error
	SaveFact(ctx context.Context, fact Fact) error
	SaveEvidence(ctx context.Context, evidence Evidence) error
	Snapshot(ctx context.Context, tenantID string) ([]Object, []Link, []Fact, []Evidence, error)
}

type MemoryStore struct {
	mu       sync.RWMutex
	objects  map[string]Object
	links    map[string]Link
	facts    map[string]Fact
	evidence map[string]Evidence
	history  []temporalResourceVersion
}

var _ Store = (*MemoryStore)(nil)
var _ WriterStore = (*MemoryStore)(nil)

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		objects:  map[string]Object{},
		links:    map[string]Link{},
		facts:    map[string]Fact{},
		evidence: map[string]Evidence{},
	}
}

func (s *MemoryStore) SaveObject(ctx context.Context, object Object) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(object.TenantID) == "" || strings.TrimSpace(object.ID) == "" {
		return fmt.Errorf("object tenant id and id are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := memoryResourceKey(object.TenantID, object.ID)
	ref, state, err := memoryAccessRef(s.objects[key].OwnerAccessRef, s.objects[key].AccessState, object.OwnerAccessRef)
	if err != nil {
		return err
	}
	object.OwnerAccessRef, object.AccessState = ref, state
	object.Temporal = canonicalTemporalMetadata(object.Temporal, object.UpdatedAt)
	if keepsTemporalHistory(object.Temporal) {
		if err := s.appendTemporalVersion(objectTemporalVersion(object), object.OwnerAccessRef, object.AccessState); err != nil {
			return err
		}
	}
	s.objects[key] = cloneObject(object)
	return nil
}

func (s *MemoryStore) EnsureObjectReference(ctx context.Context, object Object) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(object.TenantID) == "" || strings.TrimSpace(object.ID) == "" || strings.TrimSpace(object.ObjectType) == "" {
		return fmt.Errorf("object reference tenant id, id, and type are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := memoryResourceKey(object.TenantID, object.ID)
	if _, exists := s.objects[key]; exists {
		return nil
	}
	object.OwnerAccessRef = normalizedAccessRefOrEmpty(object.OwnerAccessRef)
	object.AccessState = accessStateFor(object.OwnerAccessRef)
	s.objects[key] = cloneObject(object)
	return nil
}

func (s *MemoryStore) SaveLink(ctx context.Context, link Link) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(link.TenantID) == "" || strings.TrimSpace(link.ID) == "" {
		return fmt.Errorf("link tenant id and id are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := memoryResourceKey(link.TenantID, link.ID)
	ref, state, err := memoryAccessRef(s.links[key].SourceAccessRef, s.links[key].AccessState, link.SourceAccessRef)
	if err != nil {
		return err
	}
	link.SourceAccessRef, link.AccessState = ref, state
	link.Temporal = canonicalTemporalMetadata(link.Temporal, link.UpdatedAt)
	if keepsTemporalHistory(link.Temporal) {
		if err := s.appendTemporalVersion(linkTemporalVersion(link), link.SourceAccessRef, link.AccessState); err != nil {
			return err
		}
	}
	s.links[key] = cloneLink(link)
	return nil
}

func (s *MemoryStore) DeleteLink(ctx context.Context, tenantID, linkID, sourceStreamID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tenantID, linkID, sourceStreamID = strings.TrimSpace(tenantID), strings.TrimSpace(linkID), strings.TrimSpace(sourceStreamID)
	if tenantID == "" || linkID == "" || sourceStreamID == "" {
		return fmt.Errorf("link tenant id, id, and source stream id are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := memoryResourceKey(tenantID, linkID)
	if link, exists := s.links[key]; exists && link.SourceStreamID == sourceStreamID {
		delete(s.links, key)
	}
	return nil
}

func (s *MemoryStore) SaveFact(ctx context.Context, fact Fact) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(fact.TenantID) == "" || strings.TrimSpace(fact.ID) == "" {
		return fmt.Errorf("fact tenant id and id are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := memoryResourceKey(fact.TenantID, fact.ID)
	ref, state, err := memoryAccessRef(s.facts[key].SourceAccessRef, s.facts[key].AccessState, fact.SourceAccessRef)
	if err != nil {
		return err
	}
	fact.SourceAccessRef, fact.AccessState = ref, state
	fact.Temporal = canonicalTemporalMetadata(fact.Temporal, fact.UpdatedAt)
	if keepsTemporalHistory(fact.Temporal) {
		if err := s.appendTemporalVersion(factTemporalVersion(fact), fact.SourceAccessRef, fact.AccessState); err != nil {
			return err
		}
	}
	s.facts[key] = cloneFact(fact)
	return nil
}

func (s *MemoryStore) appendTemporalVersion(version temporalResourceVersion, accessRef AccessRef, accessState AccessState) error {
	assertionID, _, err := temporalResourceVersionIdentity(version, accessRef, accessState)
	if err != nil {
		return err
	}
	for _, existing := range s.history {
		if existing.AssertionID == assertionID {
			return nil
		}
	}
	version.AssertionID = assertionID
	s.history = append(s.history, version)
	return nil
}

func (s *MemoryStore) SaveEvidence(ctx context.Context, evidence Evidence) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(evidence.TenantID) == "" || strings.TrimSpace(evidence.ID) == "" {
		return fmt.Errorf("evidence tenant id and id are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := memoryResourceKey(evidence.TenantID, evidence.ID)
	ref, state, err := memoryAccessRef(s.evidence[key].SourceAccessRef, s.evidence[key].AccessState, evidence.SourceAccessRef)
	if err != nil {
		return err
	}
	evidence.SourceAccessRef, evidence.AccessState = ref, state
	s.evidence[key] = evidence
	return nil
}

func (s *MemoryStore) DeleteEvidence(ctx context.Context, tenantID, evidenceID string, sourceRef Ref) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tenantID, evidenceID = strings.TrimSpace(tenantID), strings.TrimSpace(evidenceID)
	sourceRef.Kind, sourceRef.ID = strings.TrimSpace(sourceRef.Kind), strings.TrimSpace(sourceRef.ID)
	if tenantID == "" || evidenceID == "" || sourceRef.Kind == "" || sourceRef.ID == "" {
		return fmt.Errorf("evidence tenant id, id, and source ref are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := memoryResourceKey(tenantID, evidenceID)
	if evidence, exists := s.evidence[key]; exists && evidence.SourceRef == sourceRef {
		delete(s.evidence, key)
	}
	return nil
}

func memoryResourceKey(tenantID, resourceID string) string {
	return strings.TrimSpace(tenantID) + "\x1f" + strings.TrimSpace(resourceID)
}

func memoryAccessRef(existing AccessRef, existingState AccessState, incoming AccessRef) (AccessRef, AccessState, error) {
	incoming = normalizedAccessRefOrEmpty(incoming)
	if existingState == "" {
		return incoming, accessStateFor(incoming), nil
	}
	existing = normalizedAccessRefOrEmpty(existing)
	if existingState == AccessStateResolved {
		if !existing.Resolved() {
			return AccessRef{}, AccessStateUnresolved, fmt.Errorf("%w: resolved row has an incomplete reference", ErrAccessRefConflict)
		}
		if incoming.Resolved() && !sameAccessRef(existing, incoming) {
			return AccessRef{}, AccessStateUnresolved, ErrAccessRefConflict
		}
		return existing, AccessStateResolved, nil
	}
	if existingState != AccessStateUnresolved {
		return AccessRef{}, AccessStateUnresolved, fmt.Errorf("%w: unsupported access state %q", ErrAccessRefConflict, existingState)
	}
	return incoming, accessStateFor(incoming), nil
}

func (s *MemoryStore) Snapshot(ctx context.Context, tenantID string) ([]Object, []Link, []Fact, []Evidence, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, nil, err
	}
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return nil, nil, nil, nil, fmt.Errorf("tenant id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotLocked(tenantID)
}

func (s *MemoryStore) SnapshotCurrentSubgraph(ctx context.Context, tenantID string, subjectRefs []Ref, maxDepth int, limits CurrentSubgraphLimits) ([]Object, []Link, []Fact, []Evidence, error) {
	if !limits.valid() || len(subjectRefs) == 0 || maxDepth < 0 || maxDepth > 2 {
		return nil, nil, nil, nil, ErrQueryInvalid
	}
	objects, links, facts, evidence, err := s.Snapshot(ctx, tenantID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return boundedMemorySubgraph(objects, links, facts, evidence, subjectRefs, maxDepth, limits)
}

func (s *MemoryStore) SnapshotSubgraphAsOf(ctx context.Context, tenantID string, subjectRefs []Ref, maxDepth int, limits CurrentSubgraphLimits, businessAsOf, knownAt time.Time) ([]Object, []Link, []Fact, []Evidence, error) {
	if !limits.valid() || len(subjectRefs) == 0 || maxDepth < 0 || maxDepth > 2 {
		return nil, nil, nil, nil, ErrQueryInvalid
	}
	objects, links, facts, evidence, err := s.SnapshotAsOf(ctx, tenantID, businessAsOf, knownAt)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return boundedMemorySubgraph(objects, links, facts, evidence, subjectRefs, maxDepth, limits)
}

func boundedMemorySubgraph(objects []Object, links []Link, facts []Fact, evidence []Evidence, subjectRefs []Ref, maxDepth int, limits CurrentSubgraphLimits) ([]Object, []Link, []Fact, []Evidence, error) {
	selected := make(map[string]bool, len(subjectRefs))
	for _, ref := range subjectRefs {
		if id := strings.TrimSpace(ref.ID); id != "" {
			selected[id] = true
		}
	}
	if len(selected) > limits.Objects {
		return nil, nil, nil, nil, ErrQueryBudgetExceeded
	}
	for range maxDepth {
		before := len(selected)
		frontier := make(map[string]bool, len(selected))
		for id := range selected {
			frontier[id] = true
		}
		edges := 0
		for _, link := range links {
			if frontier[link.FromObjectID] || frontier[link.ToObjectID] {
				edges++
				if edges > limits.Links {
					return nil, nil, nil, nil, ErrQueryBudgetExceeded
				}
				selected[link.FromObjectID] = true
				selected[link.ToObjectID] = true
				if len(selected) > limits.Objects {
					return nil, nil, nil, nil, ErrQueryBudgetExceeded
				}
			}
		}
		if len(selected) == before {
			break
		}
	}
	selectedObjects := make([]Object, 0, len(selected))
	for _, object := range objects {
		if selected[object.ID] {
			selectedObjects = append(selectedObjects, object)
		}
	}
	selectedLinks := make([]Link, 0)
	for _, link := range links {
		if selected[link.FromObjectID] && selected[link.ToObjectID] {
			selectedLinks = append(selectedLinks, link)
		}
	}
	selectedFacts := make([]Fact, 0)
	for _, fact := range facts {
		if selected[fact.Subject.ID] {
			selectedFacts = append(selectedFacts, fact)
		}
	}
	if len(selectedObjects) > limits.Objects || len(selectedLinks) > limits.Links || len(selectedFacts) > limits.Facts {
		return nil, nil, nil, nil, ErrQueryBudgetExceeded
	}
	neededEvidence := make(map[string]bool)
	for _, object := range selectedObjects {
		for _, ref := range object.EvidenceRefs {
			neededEvidence[ref.ID] = true
		}
	}
	for _, link := range selectedLinks {
		for _, ref := range link.EvidenceRefs {
			neededEvidence[ref.ID] = true
		}
	}
	for _, fact := range selectedFacts {
		for _, ref := range fact.EvidenceRefs {
			neededEvidence[ref.ID] = true
		}
	}
	selectedEvidence := make([]Evidence, 0, len(neededEvidence))
	for _, item := range evidence {
		if neededEvidence[item.ID] {
			selectedEvidence = append(selectedEvidence, item)
		}
	}
	if len(selectedEvidence) > limits.Evidence {
		return nil, nil, nil, nil, ErrQueryBudgetExceeded
	}
	return selectedObjects, selectedLinks, selectedFacts, selectedEvidence, nil
}

func (s *MemoryStore) SnapshotAsOf(ctx context.Context, tenantID string, businessAsOf, knownAt time.Time) ([]Object, []Link, []Fact, []Evidence, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, nil, err
	}
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return nil, nil, nil, nil, fmt.Errorf("tenant id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	objects, links, facts, evidence, err := s.snapshotLocked(tenantID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	versions := make([]temporalResourceVersion, 0, len(s.history))
	for _, version := range s.history {
		switch {
		case version.Object != nil && version.Object.TenantID == tenantID:
			candidate := objectTemporalVersion(*version.Object)
			candidate.AssertionID = version.AssertionID
			versions = append(versions, candidate)
		case version.Link != nil && version.Link.TenantID == tenantID:
			candidate := linkTemporalVersion(*version.Link)
			candidate.AssertionID = version.AssertionID
			versions = append(versions, candidate)
		case version.Fact != nil && version.Fact.TenantID == tenantID:
			candidate := factTemporalVersion(*version.Fact)
			candidate.AssertionID = version.AssertionID
			versions = append(versions, candidate)
		}
	}
	return temporalSnapshot(objects, links, facts, evidence, versions, businessAsOf, knownAt)
}

func (s *MemoryStore) snapshotLocked(tenantID string) ([]Object, []Link, []Fact, []Evidence, error) {
	objects := make([]Object, 0, len(s.objects))
	for _, object := range s.objects {
		if object.TenantID != tenantID {
			continue
		}
		objects = append(objects, cloneObject(object))
	}
	links := make([]Link, 0, len(s.links))
	for _, link := range s.links {
		if link.TenantID != tenantID {
			continue
		}
		links = append(links, cloneLink(link))
	}
	facts := make([]Fact, 0, len(s.facts))
	for _, fact := range s.facts {
		if fact.TenantID != tenantID {
			continue
		}
		facts = append(facts, cloneFact(fact))
	}
	evidence := make([]Evidence, 0, len(s.evidence))
	for _, item := range s.evidence {
		if item.TenantID != tenantID {
			continue
		}
		evidence = append(evidence, item)
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].ID < objects[j].ID })
	sort.Slice(links, func(i, j int) bool { return links[i].ID < links[j].ID })
	sort.Slice(facts, func(i, j int) bool { return facts[i].ID < facts[j].ID })
	sort.Slice(evidence, func(i, j int) bool { return evidence[i].ID < evidence[j].ID })
	return objects, links, facts, evidence, nil
}

func cloneObject(object Object) Object {
	object.Properties = cloneMap(object.Properties)
	object.EvidenceRefs = append([]Ref(nil), object.EvidenceRefs...)
	return object
}

func cloneLink(link Link) Link {
	link.Properties = cloneMap(link.Properties)
	link.EvidenceRefs = append([]Ref(nil), link.EvidenceRefs...)
	return link
}

func cloneFact(fact Fact) Fact {
	fact.Properties = cloneMap(fact.Properties)
	fact.EvidenceRefs = append([]Ref(nil), fact.EvidenceRefs...)
	return fact
}

func cloneMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
