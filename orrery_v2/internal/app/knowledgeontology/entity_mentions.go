package knowledgeontology

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"orrery_v2/internal/app/contextgraph"
)

var mentionableObjectTypes = map[string]struct{}{
	"person": {}, "agent": {}, "organization": {}, "team": {}, "department": {},
	"mission": {}, "project": {}, "task": {}, "tool": {}, "idea": {}, "todo": {},
	"requirement": {}, "decision": {}, "design_artifact": {}, "repository": {},
	"issue": {}, "release": {}, "contract": {}, "policy": {},
}

func LinkTypeForMentionedObject(objectType string) (string, bool) {
	objectType = strings.ToLower(strings.TrimSpace(objectType))
	if _, ok := mentionableObjectTypes[objectType]; !ok {
		return "", false
	}
	return "knowledge_document_mentions_" + objectType, true
}

// extractEntityMentions performs deterministic entity linking against the
// tenant's ontology dictionary. A confirmed mention is a retrieval edge, not a
// business relationship between the mentioned entities.
func extractEntityMentions(req ProjectRequest, existing []SubjectProjection) []SubjectProjection {
	if strings.TrimSpace(req.Source) == "" {
		return nil
	}
	seen := make(map[string]struct{}, len(existing))
	for _, subject := range existing {
		if subject.ObjectID != "" {
			seen[subject.ObjectID] = struct{}{}
		}
	}
	objects := append([]contextgraph.Object(nil), req.Objects...)
	sort.Slice(objects, func(i, j int) bool { return objects[i].ID < objects[j].ID })
	lines := strings.Split(strings.ReplaceAll(req.Source, "\r\n", "\n"), "\n")
	out := make([]SubjectProjection, 0)
	for _, object := range objects {
		objectType := strings.ToLower(strings.TrimSpace(object.ObjectType))
		if object.TenantID != req.TenantID || object.ID == "" {
			continue
		}
		if _, ok := mentionableObjectTypes[objectType]; !ok {
			continue
		}
		if _, ok := seen[object.ID]; ok {
			continue
		}
		surface, line := firstObjectMention(lines, object)
		if surface == "" {
			continue
		}
		seen[object.ID] = struct{}{}
		out = append(out, SubjectProjection{
			Reference: ObjectRef{Kind: objectType, ID: object.ID}, ObjectID: object.ID,
			Resolution: ResolutionResolved, Status: AssertionConfirmed,
			Surface: surface, Confidence: 0.85,
			Evidence: evidence(req, fmt.Sprintf("markdown:line:%d", line), "deterministic_exact_entity_match"),
		})
	}
	return out
}

func firstObjectMention(lines []string, object contextgraph.Object) (string, int) {
	terms := objectMentionTerms(object)
	for lineIndex, line := range lines {
		for _, term := range terms {
			if containsEntityTerm(line, term) {
				return term, lineIndex + 1
			}
		}
	}
	return "", 0
}

func objectMentionTerms(object contextgraph.Object) []string {
	terms := []string{strings.TrimSpace(object.Label)}
	if value, ok := object.Properties["aliases"]; ok {
		switch aliases := value.(type) {
		case []any:
			for _, alias := range aliases {
				if text, ok := alias.(string); ok {
					terms = append(terms, strings.TrimSpace(text))
				}
			}
		case []string:
			terms = append(terms, aliases...)
		case string:
			terms = append(terms, aliases)
		}
	}
	unique := make(map[string]struct{})
	out := make([]string, 0, len(terms))
	for _, term := range terms {
		term = strings.TrimSpace(term)
		if !validEntityTerm(term) {
			continue
		}
		key := strings.ToLower(term)
		if _, ok := unique[key]; ok {
			continue
		}
		unique[key] = struct{}{}
		out = append(out, term)
	}
	sort.SliceStable(out, func(i, j int) bool { return utf8.RuneCountInString(out[i]) > utf8.RuneCountInString(out[j]) })
	return out
}

func validEntityTerm(term string) bool {
	count := utf8.RuneCountInString(term)
	if count < 2 {
		return false
	}
	if count >= 3 {
		return true
	}
	for _, r := range term {
		if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) {
			return true
		}
	}
	return false
}

func containsEntityTerm(line, term string) bool {
	lineRunes := []rune(strings.ToLower(line))
	termRunes := []rune(strings.ToLower(term))
	for start := 0; start+len(termRunes) <= len(lineRunes); start++ {
		if string(lineRunes[start:start+len(termRunes)]) != string(termRunes) {
			continue
		}
		beforeOK := start == 0 || !isEntityWordRune(lineRunes[start-1]) || !isEntityWordRune(termRunes[0])
		end := start + len(termRunes)
		afterOK := end == len(lineRunes) || !isEntityWordRune(lineRunes[end]) || !isEntityWordRune(termRunes[len(termRunes)-1])
		if beforeOK && afterOK {
			return true
		}
	}
	return false
}

func isEntityWordRune(r rune) bool {
	return unicode.Is(unicode.Latin, r) || unicode.IsDigit(r) || r == '_'
}

func knowledgeEntityDictionaryVersion(objects []contextgraph.Object) string {
	type entity struct {
		ID           string   `json:"id"`
		ObjectType   string   `json:"object_type"`
		CanonicalKey string   `json:"canonical_key"`
		Label        string   `json:"label"`
		Terms        []string `json:"terms"`
	}
	entities := make([]entity, 0, len(objects))
	for _, object := range objects {
		objectType := strings.ToLower(strings.TrimSpace(object.ObjectType))
		if _, ok := mentionableObjectTypes[objectType]; !ok {
			continue
		}
		terms := objectMentionTerms(object)
		sort.Strings(terms)
		entities = append(entities, entity{
			ID: strings.TrimSpace(object.ID), ObjectType: objectType,
			CanonicalKey: strings.TrimSpace(object.CanonicalKey), Label: strings.TrimSpace(object.Label), Terms: terms,
		})
	}
	sort.Slice(entities, func(i, j int) bool {
		if entities[i].ObjectType == entities[j].ObjectType {
			return entities[i].ID < entities[j].ID
		}
		return entities[i].ObjectType < entities[j].ObjectType
	})
	payload, _ := json.Marshal(entities)
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("pageindex-ontology-links-v4:%x", digest[:8])
}
