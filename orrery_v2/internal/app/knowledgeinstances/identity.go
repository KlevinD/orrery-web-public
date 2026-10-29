package knowledgeinstances

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Identifier binds a registration to its jurisdiction and issuing authority.
// These are identity metadata, not an independent active business assertion.
type Identifier struct {
	Scheme       string `json:"scheme"`
	Jurisdiction string `json:"jurisdiction"`
	Authority    string `json:"authority"`
	Value        string `json:"value"`
}

type IdentifierInput struct {
	Identifier
	Quote string `json:"quote"`
}

type IdentityAssignment struct {
	EntityID    string            `json:"entity_id"`
	New         bool              `json:"new"`
	Name        string            `json:"name"`
	Quote       string            `json:"quote"`
	Identifiers []IdentifierInput `json:"identifiers,omitempty"`
}

func identifierNamespace(k Identifier) string {
	return Digest([]string{k.Scheme, k.Jurisdiction, k.Authority})
}

func normalizeIdentifier(k Identifier) (Identifier, error) {
	k = Identifier{canonical(k.Scheme), canonical(k.Jurisdiction), canonical(k.Authority), canonical(k.Value)}
	if !slices.Contains([]string{"company_registration", "tax_registration"}, k.Scheme) ||
		k.Jurisdiction == "" || len(k.Jurisdiction) > 128 || k.Authority == "" || len(k.Authority) > 256 || k.Value == "" || len(k.Value) > 128 {
		return k, ErrInvalid
	}
	return k, nil
}

func validateIdentifiers(source Source, input EntityInput) ([]Identifier, error) {
	if len(input.Identifiers) > 4 || (len(input.Identifiers) > 0 && input.Kind != "company") {
		return nil, ErrInvalid
	}
	var result []Identifier
	for _, offered := range input.Identifiers {
		k, err := normalizeIdentifier(offered.Identifier)
		quote := canonical(offered.Quote)
		if err != nil || !quoted(source, offered.Quote) || !containsIdentifierValue(quote, k.Value) ||
			!containsIdentifierValue(quote, k.Jurisdiction) || !containsIdentifierValue(quote, k.Authority) {
			return nil, fmt.Errorf("%w: qualified identifier must cite its value, jurisdiction and authority", ErrInvalid)
		}
		result = append(result, k)
	}
	return mergeIdentityKeys(nil, result)
}

func containsIdentifierValue(quote, value string) bool {
	alphaNumeric := func(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') }
	for offset := 0; offset < len(quote); {
		index := strings.Index(quote[offset:], value)
		if index < 0 {
			return false
		}
		index += offset
		end := index + len(value)
		if (index == 0 || !alphaNumeric(quote[index-1])) && (end == len(quote) || !alphaNumeric(quote[end])) {
			return true
		}
		offset = index + 1
	}
	return false
}

func mergeIdentityKeys(old, added []Identifier) ([]Identifier, error) {
	byNamespace := map[string]Identifier{}
	for _, k := range append(slices.Clone(old), added...) {
		value, err := normalizeIdentifier(k)
		if err != nil {
			return nil, err
		}
		namespace := identifierNamespace(value)
		if previous, ok := byNamespace[namespace]; ok && previous.Value != value.Value {
			return nil, fmt.Errorf("%w: contradictory registrations in one identity namespace", ErrConflict)
		}
		byNamespace[namespace] = value
	}
	var result []Identifier
	for _, k := range byNamespace {
		result = append(result, k)
	}
	sort.Slice(result, func(i, j int) bool { return Digest(result[i]) < Digest(result[j]) })
	return result, nil
}

func prepareIdentity(source Source, input EntityInput, existing map[string]Entity) (Entity, IdentityAssignment, error) {
	keys, err := validateIdentifiers(source, input)
	if err != nil {
		return Entity{}, IdentityAssignment{}, err
	}
	e := Entity{TenantID: source.TenantID, Kind: input.Kind, Name: input.Name, Key: canonical(input.Name), Access: source.Access, Properties: map[string]any{}}
	if len(keys) > 0 {
		e.ID = stableID("knowledge-entity:", []any{e.TenantID, e.Access, e.Kind, "qualified-identity-v1", keys[0]})
	} else {
		// An unqualified name cannot be globally unique. Source and local mention
		// distinguish homonyms; the model explicitly reuses offered historical IDs.
		e.ID = stableID("knowledge-entity:", []any{e.TenantID, e.Access, e.Kind, "document-identity-v1", source.KnowledgeBaseID, source.DocumentID, e.Key, input.Ref})
	}
	if input.ExistingID != "" {
		var ok bool
		e, ok = existing[input.ExistingID]
		if !ok || e.Kind != input.Kind {
			return e, IdentityAssignment{}, fmt.Errorf("%w: identity was not an authorized candidate", ErrInvalid)
		}
	} else {
		if _, ok := existing[e.ID]; ok {
			return e, IdentityAssignment{}, fmt.Errorf("%w: existing identity requires explicit resolution", ErrConflict)
		}
		for _, candidate := range existing {
			for _, k := range keys {
				if slices.Contains(candidate.IdentityKeys, k) {
					return e, IdentityAssignment{}, fmt.Errorf("%w: offered registration requires explicit identity resolution", ErrConflict)
				}
			}
		}
	}
	e.IdentityKeys, err = mergeIdentityKeys(e.IdentityKeys, keys)
	return e, IdentityAssignment{EntityID: e.ID, New: input.ExistingID == "", Name: input.Name, Quote: input.Quote, Identifiers: slices.Clone(input.Identifiers)}, err
}

// ValidateMentions checks recall hints before using model text in indexed queries.
// It grants no identity or write authority; the final Prepare validates all output.
func ValidateMentions(source Source, extraction Extraction) error {
	if len(extraction.Entities) > 40 || len(extraction.Relations) > 80 || strings.TrimSpace(extraction.Reason) == "" || len(extraction.Reason) > 2000 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, input := range extraction.Entities {
		if input.Ref == "" || len(input.Ref) > 128 || seen[input.Ref] || input.ExistingID != "" || !slices.Contains([]string{"company", "person", "contract", "product", "project", "other"}, input.Kind) || strings.TrimSpace(input.Name) == "" || len(input.Name) > 256 ||
			!quoted(source, input.Quote) || !strings.Contains(canonical(input.Quote), canonical(input.Name)) || len(input.Properties) > 20 {
			return ErrInvalid
		}
		seen[input.Ref] = true
		if _, err := validateIdentifiers(source, input); err != nil {
			return err
		}
		for _, property := range input.Properties {
			if err := validateProperty(source, property); err != nil {
				return err
			}
		}
	}
	return nil
}

// Policy-filtered graph metadata is the only registration context offered to
// models. Raw entity metadata must never bypass field-level read policies.
func VisibleIdentityKeys(properties map[string]any) []Identifier {
	value, ok := properties["identity_keys"]
	if !ok {
		return nil
	}
	raw, _ := json.Marshal(value)
	var result []Identifier
	if json.Unmarshal(raw, &result) != nil {
		return nil
	}
	keys, err := mergeIdentityKeys(nil, result)
	if err != nil {
		return nil
	}
	return keys
}
