package memoryontology

import (
	"strings"

	"orrery_v2/internal/app/contextgraph"
)

func ProjectMentions(tenantID string, extraction Extraction, objects []contextgraph.Object) []MentionProjection {
	projections := make([]MentionProjection, 0, len(extraction.Mentions))
	for index, mention := range extraction.Mentions {
		projection := MentionProjection{Index: index, Mention: mention}
		linkType, supported := LinkTypeForObjectType(mention.ObjectType)
		if !supported {
			projection.Resolution = ResolutionInvalidType
			projections = append(projections, projection)
			continue
		}
		projection.LinkType = linkType
		if mention.Confidence < MinimumLinkConfidence {
			projection.Resolution = ResolutionLowConfidence
			projections = append(projections, projection)
			continue
		}
		projection.ResolvedObjectID, projection.Resolution = resolveMention(tenantID, mention, objects)
		projections = append(projections, projection)
	}
	return projections
}

func resolveMention(tenantID string, mention Mention, objects []contextgraph.Object) (string, Resolution) {
	tenantID = strings.TrimSpace(tenantID)
	candidates := make([]contextgraph.Object, 0)
	for _, object := range objects {
		if object.TenantID == tenantID && strings.EqualFold(strings.TrimSpace(object.ObjectType), mention.ObjectType) {
			candidates = append(candidates, object)
		}
	}
	values := uniqueNonEmpty([]string{mention.ObjectHint, mention.Surface})
	for _, matcher := range []func(contextgraph.Object, string) bool{directObjectMatch, objectAliasMatch, objectLabelMatch} {
		matches := make([]string, 0, 1)
		for _, object := range candidates {
			for _, value := range values {
				if matcher(object, value) {
					matches = appendUnique(matches, object.ID)
					break
				}
			}
		}
		if len(matches) == 1 {
			return matches[0], ResolutionResolved
		}
		if len(matches) > 1 {
			return "", ResolutionAmbiguous
		}
	}
	return "", ResolutionUnresolved
}

func directObjectMatch(object contextgraph.Object, value string) bool {
	value = normalizedMatchValue(value)
	if value == "" {
		return false
	}
	if normalizedMatchValue(object.ID) == value || normalizedMatchValue(object.CanonicalKey) == value {
		return true
	}
	if normalizedMatchValue(object.ObjectType+":"+strings.TrimSpace(value)) == normalizedMatchValue(object.CanonicalKey) {
		return true
	}
	externalID, _ := object.Properties["external_id"].(string)
	return normalizedMatchValue(externalID) == value
}

func objectLabelMatch(object contextgraph.Object, value string) bool {
	return normalizedMatchValue(object.Label) != "" && normalizedMatchValue(object.Label) == normalizedMatchValue(value)
}

func objectAliasMatch(object contextgraph.Object, value string) bool {
	want := normalizedMatchValue(value)
	if want == "" {
		return false
	}
	switch aliases := object.Properties["aliases"].(type) {
	case []any:
		for _, candidate := range aliases {
			if text, ok := candidate.(string); ok && normalizedMatchValue(text) == want {
				return true
			}
		}
	case []string:
		for _, candidate := range aliases {
			if normalizedMatchValue(candidate) == want {
				return true
			}
		}
	case string:
		return normalizedMatchValue(aliases) == want
	}
	return false
}

func normalizedMatchValue(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func uniqueNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = appendUnique(out, value)
		}
	}
	return out
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if strings.EqualFold(existing, value) {
			return values
		}
	}
	return append(values, value)
}
