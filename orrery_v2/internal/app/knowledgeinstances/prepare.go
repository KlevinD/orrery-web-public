package knowledgeinstances

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
)

// AttributeTypes is deliberately limited to this domain's typed attributes.
// External domains opt in through their own commands, never graph UPSERTs.
var AttributeTypes = map[string]string{
	"name": "string", "entity_kind": "string", "aliases": "array", "description": "string",
	"country": "string", "registration_id": "string", "effective_date": "date",
	"expiration_date": "date", "amount": "number", "currency": "string", "role": "string",
	"identity_keys": "array",
}

func ValidateSource(s Source) error {
	if s.TenantID == "" || s.RootID == "" || s.KnowledgeBaseID == "" || s.DocumentID == "" || s.Revision < 1 ||
		s.ContentHash == "" || len(s.Content) > 96*1024 || s.Access.Kind == "" || s.Access.ID == "" || s.Access.TenantID == "" {
		return fmt.Errorf("%w: trusted source revision is required", ErrInvalid)
	}
	return nil
}

func canonical(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
func quoted(s Source, q string) bool {
	return len(q) >= 2 && len(q) <= 2000 && strings.Contains(s.Content, q)
}

func validateProperty(s Source, p Property) error {
	if err := validateValidity(s, p.Validity); err != nil {
		return err
	}
	t, ok := AttributeTypes[p.Predicate]
	if !ok || p.Predicate == "name" || p.Predicate == "entity_kind" || p.Predicate == "identity_keys" || !quoted(s, p.Quote) {
		return ErrInvalid
	}
	switch t {
	case "string", "date":
		v, ok := p.Value.(string)
		if !ok || strings.TrimSpace(v) == "" || len(v) > 2000 {
			return ErrInvalid
		}
		if t == "date" {
			if _, err := time.Parse("2006-01-02", v); err != nil {
				return ErrInvalid
			}
		}
	case "number":
		v, ok := p.Value.(float64)
		if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
			return ErrInvalid
		}
		// Require the number itself in its evidence, in addition to model review.
		if !strings.Contains(strings.ReplaceAll(p.Quote, ",", ""), strconv.FormatFloat(v, 'f', -1, 64)) {
			return ErrInvalid
		}
	case "array":
		v, ok := p.Value.([]any)
		if !ok || len(v) > 10 || len(v) == 0 {
			return ErrInvalid
		}
		for _, item := range v {
			text, ok := item.(string)
			if !ok || text == "" || !strings.Contains(p.Quote, text) {
				return ErrInvalid
			}
		}
	}
	return nil
}

// Prepare rejects invented citations, unoffered existing IDs and duplicate
// identities before any persistence or execution is possible.
func Prepare(batchID string, watch Watch, source Source, schema, authorization string, extraction Extraction, candidates []Entity, previous []Claim, now time.Time) (Plan, error) {
	p := Plan{TemporalProtocol: TemporalProtocol, ExtractionReason: extraction.Reason, BatchID: batchID, Watch: watch, Source: source, SchemaHash: schema, AuthorizationHash: authorization, Before: map[string]int64{}, Previous: previous, CreatedAt: now.UTC()}
	if err := ValidateSource(source); err != nil {
		return p, err
	}
	if batchID == "" || !watch.Active || watch.TenantID != source.TenantID || watch.RootID != source.RootID ||
		extraction.Defer || strings.TrimSpace(extraction.Reason) == "" || len(extraction.Reason) > 2000 || len(extraction.Entities) > 40 || len(extraction.Relations) > 80 {
		return p, ErrInvalid
	}
	existing := map[string]Entity{}
	for _, e := range candidates {
		if e.TenantID == source.TenantID && e.Access == source.Access {
			existing[e.ID] = e
		}
	}
	refs := map[string]string{}
	used := map[string]bool{}
	for _, input := range extraction.Entities {
		if input.Ref == "" || len(input.Ref) > 128 || refs[input.Ref] != "" || len(input.Name) > 256 || strings.TrimSpace(input.Name) == "" ||
			!quoted(source, input.Quote) || !strings.Contains(strings.ToLower(input.Quote), strings.ToLower(input.Name)) ||
			!slices.Contains([]string{"company", "person", "contract", "product", "project", "other"}, input.Kind) || len(input.Properties) > 20 {
			return p, ErrInvalid
		}
		e, assignment, err := prepareIdentity(source, input, existing)
		if err != nil {
			return p, err
		}
		p.IdentityAssignments = append(p.IdentityAssignments, assignment)
		if used[e.ID] {
			return p, ErrInvalid
		}
		used[e.ID] = true
		refs[input.Ref] = e.ID
		p.Entities = append(p.Entities, e)
		p.Before[e.ID] = e.Version
		properties := append([]Property{{Predicate: "name", Value: input.Name, Quote: input.Quote}, {Predicate: "entity_kind", Value: input.Kind, Quote: input.Quote}}, input.Properties...)
		seen := map[string]bool{}
		for i, prop := range properties {
			key := prop.Predicate + ":" + validityKey(prop.Validity)
			if seen[key] {
				return p, ErrInvalid
			}
			seen[key] = true
			if i >= 2 {
				if err := validateProperty(source, prop); err != nil {
					return p, fmt.Errorf("%w: attribute %s", err, prop.Predicate)
				}
			}
			claim := Claim{EntityID: e.ID, Predicate: prop.Predicate, Value: prop.Value, Quote: prop.Quote, Validity: prop.Validity}
			claim.ID = stableID("knowledge-claim:", []any{source.TenantID, watch.ID, e.ID, prop.Predicate})
			if prop.Validity != nil {
				claim.ID = stableID("knowledge-claim:", []any{source.TenantID, watch.ID, e.ID, prop.Predicate, validityKey(prop.Validity)})
			}
			p.Claims = append(p.Claims, claim)
		}
	}
	for _, input := range extraction.Relations {
		if err := validateValidity(source, input.Validity); err != nil {
			return p, err
		}
		if input.RelationCode != "" && !ontology.ValidKnowledgeRelationCode(input.RelationCode) {
			return p, fmt.Errorf("%w: unsupported relation code", ErrInvalid)
		}
		from, to := refs[input.From], refs[input.To]
		if from == "" || to == "" || from == to || !quoted(source, input.Quote) || len(input.Relation) > 128 || strings.TrimSpace(input.Relation) == "" {
			return p, ErrInvalid
		}
		claim := Claim{EntityID: from, Predicate: RelatedLink, Value: input.Relation, TargetID: to, Quote: input.Quote, RelationCode: input.RelationCode, Validity: input.Validity}
		claim.ID = stableID("knowledge-claim:", []any{source.TenantID, watch.ID, from, to, claim.Value})
		if input.Validity != nil {
			claim.ID = stableID("knowledge-claim:", []any{source.TenantID, watch.ID, from, to, claim.Value, validityKey(input.Validity)})
		}
		if used[claim.ID] {
			return p, ErrInvalid
		}
		used[claim.ID] = true
		p.Claims = append(p.Claims, claim)
	}
	for _, old := range previous {
		for _, id := range []string{old.EntityID, old.TargetID} {
			if id == "" {
				continue
			}
			if _, ok := p.Before[id]; !ok {
				e, exists := existing[id]
				if !exists {
					return p, ErrConflict
				}
				p.Before[id] = e.Version
			}
		}
	}
	stamp := source
	stamp.Content = ""
	for i := range p.Claims {
		p.Claims[i].Source = &stamp
	}
	p.ID = stableID("knowledge-plan:", p)
	return p, nil
}

func EntityAccess(s Source) contextgraph.AccessRef { return s.Access }
