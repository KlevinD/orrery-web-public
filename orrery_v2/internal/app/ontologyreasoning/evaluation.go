package ontologyreasoning

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
)

// EvaluationRecord is persisted inside the existing run request JSON. Derived
// candidates are scoped to one requester and one bounded, policy-filtered input.
type EvaluationRecord struct {
	Historical         bool                    `json:"historical,omitempty"`
	HistoryLimitations []string                `json:"history_limitations,omitempty"`
	NextBoundary       *time.Time              `json:"next_boundary,omitempty"`
	FactConflicts      []FactConflict          `json:"fact_conflicts,omitempty"`
	RuleSetID          string                  `json:"rule_set_id,omitempty"`
	RuleSetVersion     int64                   `json:"rule_set_version,omitempty"`
	Dependencies       map[string][]Dependency `json:"dependencies,omitempty"`
	RootID             string                  `json:"root_id,omitempty"`
	InputStamp         string                  `json:"input_stamp,omitempty"`
	RevisedIDs         []string                `json:"revised_ids,omitempty"`
	SupportHashes      map[string]string       `json:"support_hashes,omitempty"`
	ActorHash          string                  `json:"actor_hash"`
	QueryHash          string                  `json:"query_hash"`
	BusinessAsOf       time.Time               `json:"business_as_of"`
	KnownAt            time.Time               `json:"known_at"`
	Rules              []Rule                  `json:"rules"`
	Closure            Closure                 `json:"closure"`
	PreviousRunID      string                  `json:"previous_run_id,omitempty"`
	WithdrawnIDs       []string                `json:"withdrawn_ids,omitempty"`
	RetainedIDs        []string                `json:"retained_ids,omitempty"`
	Documents          []DocumentReceipt       `json:"documents,omitempty"`
}

type EvaluateRequest struct {
	BusinessAsOf   *time.Time      `json:"business_as_of,omitempty"`
	KnownAt        *time.Time      `json:"known_at,omitempty"`
	RuleSetID      string          `json:"rule_set_id,omitempty"`
	RuleSetVersion int64           `json:"rule_set_version,omitempty"`
	TenantID       string          `json:"tenant_id"`
	RootID         string          `json:"root_id"`
	Rules          []Rule          `json:"rules"`
	Documents      []DocumentInput `json:"documents,omitempty"`
	PreviousRunID  string          `json:"previous_run_id,omitempty"`
}

// Evaluate creates proposals without mutating business objects or the Registry.
// The caller must obtain pack through an authenticated, policy-filtered reader.
func (s *Service) Evaluate(ctx context.Context, pack ContextPack, record EvaluationRecord, documents []DocumentInput) (ReasoningRun, error) {
	return s.evaluate(ctx, pack, record, documents, true)
}

// persist=false shares validation and proof semantics without creating runs,
// proposals, lifecycle candidates, model decisions or writeback receipts.
func (s *Service) evaluate(ctx context.Context, pack ContextPack, record EvaluationRecord, documents []DocumentInput, persist bool) (ReasoningRun, error) {
	if s == nil || (persist && s.store == nil) || pack.TenantID == "" || record.ActorHash == "" || record.QueryHash == "" || record.BusinessAsOf.IsZero() || record.KnownAt.IsZero() {
		return ReasoningRun{}, fmt.Errorf("%w: authenticated evaluation context is required", ErrInvalidRequest)
	}
	if len(pack.CandidateObjects) > 200 || len(pack.NeighborLinks) > 400 || len(pack.Facts) > 1000 {
		return ReasoningRun{}, fmt.Errorf("%w: context budget exceeded", ErrInvalidRequest)
	}
	pack, record.NextBoundary = temporalEvaluationPack(pack, record)
	if record.Historical {
		if !contextgraph.TemporalCoordinateValid(record.BusinessAsOf) || !contextgraph.TemporalCoordinateValid(record.KnownAt) || len(documents) != 0 || record.PreviousRunID != "" {
			return ReasoningRun{}, ErrInvalidRequest
		}
		record.NextBoundary = nil
	}
	pack = canonicalEvaluationPack(pack)
	_, record.FactConflicts = conflictedFacts(pack)
	index, err := indexFromContextPack(pack)
	if err != nil {
		return ReasoningRun{}, err
	}
	premises, evidenceByPremise, err := premisesFromPack(pack)
	if err != nil {
		return ReasoningRun{}, err
	}
	closure, err := EvaluateRules(ctx, pack.TenantID, record.Rules, premises)
	if err != nil {
		return ReasoningRun{}, err
	}
	record.Closure = closure
	record.WithdrawnIDs, record.RetainedIDs, record.RevisedIDs = nil, nil, nil
	candidates := make([]Candidate, 0, len(closure.Conclusions))
	for _, c := range closure.Conclusions {
		candidate := Candidate{ID: c.ID, Operation: OperationSuggest, Reason: "deterministic rule inference; source certainty is not inferred; review required"}
		if c.Atom.Kind == "fact" {
			object, ok := index.objects[c.Atom.Subject]
			if !ok {
				return ReasoningRun{}, fmt.Errorf("%w: conclusion subject is outside context", ErrValidationFailed)
			}
			candidate.Kind, candidate.Subject, candidate.Predicate, candidate.Value = CandidateFact, &ObjectRef{Type: object.ObjectType, ID: object.ID}, c.Atom.Predicate, c.Atom.Value
		} else {
			from, fromOK := index.objects[c.Atom.Subject]
			to, toOK := index.objects[c.Atom.Object]
			if !fromOK || !toOK {
				return ReasoningRun{}, fmt.Errorf("%w: conclusion endpoints are outside context", ErrValidationFailed)
			}
			candidate.Kind, candidate.LinkType = CandidateLink, c.Atom.Type
			candidate.From, candidate.To = &ObjectRef{Type: from.ObjectType, ID: from.ID}, &ObjectRef{Type: to.ObjectType, ID: to.ID}
		}
		candidate.EvidenceRefs = conclusionEvidence(c.ID, closure, evidenceByPremise)
		candidates = append(candidates, candidate)
	}
	documentCandidates, receipts, err := ExtractDocumentCandidates(pack, documents)
	if err != nil {
		return ReasoningRun{}, err
	}
	record.Documents = receipts
	for _, candidate := range documentCandidates {
		for _, ref := range candidate.EvidenceRefs {
			index.evidence[refKey(ref)] = true
		}
	}
	candidates = append(candidates, documentCandidates...)
	if len(candidates) > maxReasoningCandidates {
		return ReasoningRun{}, fmt.Errorf("%w: candidate budget exceeded", ErrValidationFailed)
	}
	pack.Hash = hashContextPack(pack)
	inputHash := stableID("input:", struct {
		Context, Rules, Semantics string
		Documents                 []DocumentReceipt
	}{pack.Hash, closure.RuleHash, InferenceSemanticsVersion, receipts})
	request := CreateRunRequest{TenantID: pack.TenantID, Engine: InferenceSemanticsVersion, ModelVersion: closure.RuleHash, InputHash: inputHash, ContextPackHash: pack.Hash, ContextPack: pack, Candidates: candidates, Evaluation: &record}
	run := ReasoningRun{TenantID: pack.TenantID, InputHash: inputHash, ContextPack: pack, Candidates: candidates, Request: request}
	if persist {
		run, err = s.CreateRun(ctx, request)
		if err != nil {
			return ReasoningRun{}, err
		}
	}
	for _, candidate := range run.Candidates {
		issues := s.validateCandidate(candidate.ID, candidate, index)
		issues = append(issues, entityPropertyIssues(candidate, index)...)
		if len(issues) == 0 {
			issues = append(issues, newIssue(candidate.ID, "governed_candidate", issueSeverityReviewRequired, "candidate requires review before business promotion"))
		}
		status := validationStatusForIssues(issues)
		run.Validation = append(run.Validation, ValidationResult{CandidateID: candidate.ID, Status: status, Issues: issues})
		if status == ValidationRejected {
			run.RejectedCandidateCount++
		} else {
			run.ReviewRequiredCount++
		}
	}
	validateProofPaths(&run)
	for i := range run.Candidates {
		for _, conclusion := range run.Request.Evaluation.Closure.Conclusions {
			if conclusion.ID == run.Candidates[i].ID {
				run.Candidates[i].EvidenceRefs = conclusionEvidence(conclusion.ID, run.Request.Evaluation.Closure, evidenceByPremise)
				break
			}
		}
	}
	setEvaluationSupports(&run)
	if !persist {
		return run, nil
	}
	if err := s.compareEvaluation(ctx, &run); err != nil {
		return ReasoningRun{}, err
	}
	run.Status = RunStatusValidated
	run, err = s.saveUpdatedRun(ctx, run)
	if err != nil {
		return ReasoningRun{}, err
	}
	if record.Historical {
		return run, nil
	}
	return s.CreateProposals(ctx, pack.TenantID, run.ID)
}

func indexFromContextPack(pack ContextPack) (validationIndex, error) {
	i := validationIndex{objectTypes: map[string]ontology.ObjectType{}, linkTypes: map[string]ontology.LinkType{}, propertyTypes: map[string]ontology.PropertyType{}, objects: map[string]contextgraph.Object{}, actionTypes: map[string]actioncontract.ActionType{}, evidence: map[string]bool{}}
	for _, o := range pack.AllowedObjectTypes {
		if definitionVisibleToTenant(o.TenantID, pack.TenantID) {
			i.objectTypes[o.ID] = o
		}
	}
	for _, l := range pack.AllowedLinkTypes {
		if definitionVisibleToTenant(l.TenantID, pack.TenantID) {
			i.linkTypes[l.ID] = l
		}
	}
	for _, p := range pack.AllowedPropertyTypes {
		if definitionVisibleToTenant(p.TenantID, pack.TenantID) {
			i.propertyTypes[p.ObjectTypeID+"."+p.PropertyID] = p
		}
	}
	for _, o := range i.objectTypes {
		for _, p := range o.Properties {
			key := o.ID + "." + p.PropertyID
			if _, ok := i.propertyTypes[key]; !ok {
				i.propertyTypes[key] = ontology.PropertyType{ID: key, ObjectTypeID: o.ID, PropertyID: p.PropertyID, ValueType: p.ValueType}
			}
		}
	}
	for _, ref := range pack.EvidenceRefs {
		i.evidence[refKey(ref)] = true
	}
	for _, o := range pack.CandidateObjects {
		if o.TenantID != pack.TenantID || o.ID == "" || i.objects[o.ID].ID != "" {
			return i, fmt.Errorf("%w: invalid context object identity", ErrInvalidRequest)
		}
		if _, ok := i.objectTypes[o.ObjectType]; !ok {
			return i, fmt.Errorf("%w: context object type is not registered", ErrValidationFailed)
		}
		i.objects[o.ID] = o
	}
	for _, l := range pack.NeighborLinks {
		if l.TenantID != pack.TenantID || i.objects[l.FromObjectID].ID == "" || i.objects[l.ToObjectID].ID == "" {
			return i, fmt.Errorf("%w: invalid context link", ErrValidationFailed)
		}
		definition, ok := i.linkTypes[l.LinkType]
		if !ok || i.objects[l.FromObjectID].ObjectType != definition.FromObjectType || i.objects[l.ToObjectID].ObjectType != definition.ToObjectType {
			return i, fmt.Errorf("%w: context link does not conform to Registry", ErrValidationFailed)
		}
		i.links = append(i.links, l)
	}
	for _, f := range pack.Facts {
		if f.TenantID != pack.TenantID || i.objects[f.Subject.ID].ID == "" {
			return i, fmt.Errorf("%w: invalid context fact", ErrValidationFailed)
		}
		if assertionEligible(f.Status) {
			object := i.objects[f.Subject.ID]
			property, ok := i.propertyTypes[object.ObjectType+"."+f.Predicate]
			if !ok || !valueMatchesType(f.Value, property.ValueType) || (f.Subject.Kind != "" && f.Subject.Kind != object.ObjectType) {
				return i, fmt.Errorf("%w: context fact does not conform to Registry", ErrValidationFailed)
			}
		}
		i.facts = append(i.facts, f)
	}
	return i, nil
}

func premisesFromPack(pack ContextPack) ([]Premise, map[string][]contextgraph.Ref, error) {
	blocked, _ := conflictedFacts(pack)
	var premises []Premise
	evidence := map[string][]contextgraph.Ref{}
	add := func(ref string, a Atom, refs []contextgraph.Ref) {
		id := stableID("premise:", struct {
			Tenant, Ref string
			Atom        Atom
		}{pack.TenantID, ref, a})
		premises = append(premises, Premise{ID: id, AssertionRef: ref, Atom: a})
		evidence[id] = cloneRefs(refs)
	}
	for _, o := range pack.CandidateObjects {
		if o.TenantID != pack.TenantID {
			return nil, nil, fmt.Errorf("%w: foreign object in context", ErrInvalidRequest)
		}
		if o.ObjectType == "knowledge_entity" && !assertionEligible(o.Status) {
			continue
		}
		add("object:"+o.ID, Atom{Kind: "object", Type: o.ObjectType, Subject: o.ID}, o.EvidenceRefs)
	}
	for _, l := range pack.NeighborLinks {
		if l.TenantID != pack.TenantID {
			return nil, nil, fmt.Errorf("%w: foreign link in context", ErrInvalidRequest)
		}
		if !assertionEligible(l.Status) {
			continue
		}
		add("link:"+l.ID, Atom{Kind: "link", Type: l.LinkType, Subject: l.FromObjectID, Object: l.ToObjectID, RelationCode: relationCode(l)}, l.EvidenceRefs)
	}
	for _, f := range pack.Facts {
		if f.TenantID != pack.TenantID {
			return nil, nil, fmt.Errorf("%w: foreign fact in context", ErrInvalidRequest)
		}
		if !assertionEligible(f.Status) {
			continue
		}
		if blocked[factSlot{f.Subject.ID, f.Predicate}] {
			continue
		}
		add("fact:"+f.ID, Atom{Kind: "fact", Subject: f.Subject.ID, Predicate: f.Predicate, Value: f.Value}, f.EvidenceRefs)
	}
	return premises, evidence, nil
}

func conclusionEvidence(id string, closure Closure, base map[string][]contextgraph.Ref) []contextgraph.Ref {
	conclusions := map[string]Conclusion{}
	for _, c := range closure.Conclusions {
		conclusions[c.ID] = c
	}
	seen, refs := map[string]bool{}, map[string]contextgraph.Ref{}
	var visit func(string)
	visit = func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		for _, ref := range base[id] {
			refs[refKey(ref)] = ref
		}
		for _, d := range conclusions[id].Derivations {
			for _, p := range d.PremiseIDs {
				visit(p)
			}
		}
	}
	visit(id)
	keys := make([]string, 0, len(refs))
	for k := range refs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	result := make([]contextgraph.Ref, 0, len(keys))
	for _, k := range keys {
		result = append(result, refs[k])
	}
	return result
}

func randomRunSuffix() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("secure run identity unavailable")
	}
	return hex.EncodeToString(b[:])
}

func assertionEligible(status string) bool {
	switch status {
	case "", "confirmed", "active", "asserted", "current":
		return true
	default:
		return false
	}
}

func canonicalEvaluationPack(pack ContextPack) ContextPack {
	pack = cloneContextPack(pack)
	sort.Slice(pack.CandidateObjects, func(i, j int) bool { return pack.CandidateObjects[i].ID < pack.CandidateObjects[j].ID })
	sort.Slice(pack.NeighborLinks, func(i, j int) bool { return pack.NeighborLinks[i].ID < pack.NeighborLinks[j].ID })
	sort.Slice(pack.Facts, func(i, j int) bool { return pack.Facts[i].ID < pack.Facts[j].ID })
	sort.Slice(pack.Evidence, func(i, j int) bool { return pack.Evidence[i].ID < pack.Evidence[j].ID })
	sort.Slice(pack.AllowedObjectTypes, func(i, j int) bool { return pack.AllowedObjectTypes[i].ID < pack.AllowedObjectTypes[j].ID })
	sort.Slice(pack.AllowedLinkTypes, func(i, j int) bool { return pack.AllowedLinkTypes[i].ID < pack.AllowedLinkTypes[j].ID })
	sort.Slice(pack.AllowedPropertyTypes, func(i, j int) bool { return pack.AllowedPropertyTypes[i].ID < pack.AllowedPropertyTypes[j].ID })
	refs := map[string]contextgraph.Ref{}
	for _, ref := range pack.EvidenceRefs {
		refs[refKey(ref)] = ref
	}
	keys := make([]string, 0, len(refs))
	for key := range refs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pack.EvidenceRefs = nil
	for _, key := range keys {
		pack.EvidenceRefs = append(pack.EvidenceRefs, refs[key])
	}
	return pack
}
