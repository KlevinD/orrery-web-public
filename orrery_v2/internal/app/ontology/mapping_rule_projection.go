package ontology

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"orrery_v2/internal/platform/graphprotocol"
)

type compiledMappingProjection struct {
	spec             graphprotocol.ProjectionSpec
	objectTemplates  []compiledObjectTemplate
	factTemplates    []compiledFactTemplate
	linkTemplates    []compiledLinkTemplate
	temporalTemplate *compiledTemporalTemplate
	sourceSystemName string
}

type compiledObjectTemplate struct {
	objectType   string
	id           string
	canonicalKey string
	label        string
}

type compiledFactTemplate struct {
	objectType string
	objectID   string
	predicate  string
	value      any
	optional   bool
}

type compiledLinkTemplate struct {
	identity string
	linkType string
	fromType string
	fromID   string
	toType   string
	toID     string
	optional bool
}

type compiledTemporalTemplate struct {
	mode               TemporalMode
	businessValidFrom  string
	businessValidTo    string
	sourceRecordedAt   string
	sourceSupersededAt string
}

var errMissingMappingPayloadField = errors.New("missing mapping payload field")

func CompileMappingRuleProjection(rule MappingRule) (graphprotocol.GraphProjection, error) {
	normalized, err := normalizeMappingRule(rule)
	if err != nil {
		return nil, err
	}
	if !normalized.Enabled {
		return nil, fmt.Errorf("%w: mapping rule %s is disabled", ErrInvalidDefinition, normalized.ID)
	}
	projection := &compiledMappingProjection{
		spec: graphprotocol.ProjectionSpec{
			Name:                normalized.ID,
			Version:             fmt.Sprintf("v%d", normalized.Version),
			TenantID:            normalized.TenantID,
			SourceType:          normalized.EventType,
			SourceSchemaVersion: normalized.EventSchemaVersion,
			ObjectTypes:         append([]string(nil), normalized.ObjectTypes...),
			FactPredicates:      factPredicatesFromRule(normalized),
		},
		sourceSystemName: "ontology.mapping_rule",
	}
	if projection.spec.Version == "v0" {
		projection.spec.Version = "v1"
	}
	if err := projection.compileTemplates(normalized.Rule); err != nil {
		return nil, err
	}
	projection.spec.LinkTypes = requiredLinkTypesFromTemplates(projection.linkTemplates)
	return projection, nil
}

func (p *compiledMappingProjection) Spec() graphprotocol.ProjectionSpec {
	return p.spec
}

func (p *compiledMappingProjection) Project(ctx context.Context, event graphprotocol.DomainEvent) (graphprotocol.ProjectionResult, error) {
	if err := ctx.Err(); err != nil {
		return graphprotocol.ProjectionResult{}, err
	}
	if event.Type != "" && event.Type != p.spec.SourceType {
		return graphprotocol.ProjectionResult{}, fmt.Errorf("%w: event type %s does not match mapping rule source %s", ErrInvalidDefinition, event.Type, p.spec.SourceType)
	}
	temporal, err := p.resolveTemporalContext(event)
	if err != nil {
		return graphprotocol.ProjectionResult{}, err
	}
	sourceEventID := event.SourceEventID
	if sourceEventID == "" {
		sourceEventID = event.ID
	}
	provenance := graphprotocol.ProvenanceRef{
		SourceEventID:   sourceEventID,
		SourceEventType: event.Type,
		SourceStreamID:  event.StreamID,
		SourceVersion:   event.Version,
		SourceAccessRef: event.SourceAccessRef,
		SourceSystem:    p.sourceSystemName,
		Actor:           event.PublishedBy,
		ObservedAt:      event.OccurredAt,
		Temporal:        temporal,
	}
	assertions := make([]graphprotocol.Assertion, 0, len(p.objectTemplates)+len(p.factTemplates)+len(p.linkTemplates))
	for _, template := range p.objectTemplates {
		objectID, err := resolveMappingString(template.id, event.Payload)
		if err != nil {
			return graphprotocol.ProjectionResult{}, err
		}
		canonicalKey, err := resolveMappingString(template.canonicalKey, event.Payload)
		if err != nil {
			return graphprotocol.ProjectionResult{}, err
		}
		label, err := resolveMappingString(template.label, event.Payload)
		if err != nil {
			return graphprotocol.ProjectionResult{}, err
		}
		assertions = append(assertions, graphprotocol.ObjectAssertion{
			Object:       graphprotocol.ObjectRef{TenantID: event.TenantID, Type: template.objectType, ID: objectID},
			CanonicalKey: canonicalKey,
			Label:        label,
			Provenance:   provenance,
		})
	}
	for _, template := range p.factTemplates {
		objectID, err := resolveMappingString(template.objectID, event.Payload)
		if err != nil {
			return graphprotocol.ProjectionResult{}, err
		}
		value, err := resolveMappingValue(template.value, event.Payload)
		if err != nil {
			if template.optional && isMissingMappingPayloadField(err) {
				continue
			}
			return graphprotocol.ProjectionResult{}, err
		}
		assertions = append(assertions, graphprotocol.FactAssertion{
			Subject:    graphprotocol.ObjectRef{TenantID: event.TenantID, Type: template.objectType, ID: objectID},
			Predicate:  template.predicate,
			Value:      value,
			Provenance: provenance,
		})
	}
	for _, template := range p.linkTemplates {
		identity, err := resolveMappingString(template.identity, event.Payload)
		if err != nil {
			return graphprotocol.ProjectionResult{}, err
		}
		fromID, fromOK, err := resolveLinkEndpointID(template.fromID, event.Payload, template.optional)
		if err != nil {
			return graphprotocol.ProjectionResult{}, err
		}
		toID, toOK, err := resolveLinkEndpointID(template.toID, event.Payload, template.optional)
		if err != nil {
			return graphprotocol.ProjectionResult{}, err
		}
		if template.optional && (!fromOK || !toOK) {
			continue
		}
		assertions = append(assertions, graphprotocol.LinkAssertion{
			Identity:   identity,
			LinkType:   template.linkType,
			From:       graphprotocol.ObjectRef{TenantID: event.TenantID, Type: template.fromType, ID: fromID},
			To:         graphprotocol.ObjectRef{TenantID: event.TenantID, Type: template.toType, ID: toID},
			Provenance: provenance,
		})
	}
	return graphprotocol.ProjectionResult{
		Cursor:     graphprotocol.ProjectionCursor{SourceID: event.ID, SourceVersion: event.Version},
		Assertions: assertions,
	}, nil
}

func (p *compiledMappingProjection) compileTemplates(rule map[string]any) error {
	if err := p.compileTemporalTemplate(rule); err != nil {
		return err
	}
	objects, _, err := mappingTemplateList(rule, "objects")
	if err != nil {
		return err
	}
	for _, template := range objects {
		fields, err := mappingTemplateMap(template, "object")
		if err != nil {
			return err
		}
		objectType, err := requiredStringField(fields, "object", "type")
		if err != nil {
			return err
		}
		id, err := requiredStringField(fields, "object", "id")
		if err != nil {
			return err
		}
		canonicalKey, err := requiredStringField(fields, "object", "canonical_key")
		if err != nil {
			return err
		}
		label := ""
		if _, ok := fields["label"]; ok {
			label, err = stringField(fields, "object", "label")
			if err != nil {
				return err
			}
		}
		p.objectTemplates = append(p.objectTemplates, compiledObjectTemplate{
			objectType:   normalizeID(objectType),
			id:           id,
			canonicalKey: canonicalKey,
			label:        label,
		})
	}

	for _, key := range []string{"properties", "facts"} {
		templates, _, err := mappingTemplateList(rule, key)
		if err != nil {
			return err
		}
		propertyField := "property_id"
		if key == "facts" {
			propertyField = "predicate"
		}
		for _, template := range templates {
			fields, err := mappingTemplateMap(template, "property")
			if err != nil {
				return err
			}
			objectType, err := requiredStringField(fields, "property", "object_type")
			if err != nil {
				return err
			}
			objectID, err := requiredStringField(fields, "property", "object_id")
			if err != nil {
				return err
			}
			predicate, err := requiredStringField(fields, "property", propertyField)
			if err != nil {
				return err
			}
			value, ok := fields["value"]
			if !ok {
				return fmt.Errorf("%w: property template requires value", ErrInvalidDefinition)
			}
			optional, err := optionalBoolField(fields, "property", "optional")
			if err != nil {
				return err
			}
			p.factTemplates = append(p.factTemplates, compiledFactTemplate{
				objectType: normalizeID(objectType),
				objectID:   objectID,
				predicate:  normalizeID(predicate),
				value:      value,
				optional:   optional,
			})
		}
	}

	links, _, err := mappingTemplateList(rule, "links")
	if err != nil {
		return err
	}
	for _, template := range links {
		fields, err := mappingTemplateMap(template, "link")
		if err != nil {
			return err
		}
		linkType, err := requiredStringField(fields, "link", "type")
		if err != nil {
			return err
		}
		fromType, err := requiredStringField(fields, "link", "from_type")
		if err != nil {
			return err
		}
		fromID, err := requiredStringField(fields, "link", "from_id")
		if err != nil {
			return err
		}
		toType, err := requiredStringField(fields, "link", "to_type")
		if err != nil {
			return err
		}
		toID, err := requiredStringField(fields, "link", "to_id")
		if err != nil {
			return err
		}
		optional, err := optionalBoolField(fields, "link", "optional")
		if err != nil {
			return err
		}
		identity := ""
		if _, ok := fields["identity"]; ok {
			identity, err = stringField(fields, "link", "identity")
			if err != nil {
				return err
			}
		}
		p.linkTemplates = append(p.linkTemplates, compiledLinkTemplate{
			identity: identity,
			linkType: normalizeID(linkType),
			fromType: normalizeID(fromType),
			fromID:   fromID,
			toType:   normalizeID(toType),
			toID:     toID,
			optional: optional,
		})
	}
	return nil
}

func (p *compiledMappingProjection) compileTemporalTemplate(rule map[string]any) error {
	raw, ok := rule["temporal"]
	if !ok {
		return nil
	}
	fields, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("%w: temporal template must be an object", ErrInvalidDefinition)
	}
	if err := requireAllowedFields(fields, "temporal", "mode", "business_valid_from", "business_valid_to", "source_recorded_at", "source_superseded_at"); err != nil {
		return err
	}
	modeText, err := requiredStringField(fields, "temporal", "mode")
	if err != nil {
		return err
	}
	mode, err := normalizeTemporalMode(TemporalMode(normalizeID(modeText)))
	if err != nil {
		return err
	}
	template := &compiledTemporalTemplate{mode: mode}
	for key, target := range map[string]*string{
		"business_valid_from":  &template.businessValidFrom,
		"business_valid_to":    &template.businessValidTo,
		"source_recorded_at":   &template.sourceRecordedAt,
		"source_superseded_at": &template.sourceSupersededAt,
	} {
		if _, exists := fields[key]; !exists {
			continue
		}
		value, err := stringField(fields, "temporal", key)
		if err != nil {
			return err
		}
		*target = value
	}
	p.temporalTemplate = template
	return nil
}

func requiredLinkTypesFromTemplates(templates []compiledLinkTemplate) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, template := range templates {
		if template.optional || template.linkType == "" || seen[template.linkType] {
			continue
		}
		seen[template.linkType] = true
		out = append(out, template.linkType)
	}
	return out
}

func factPredicatesFromRule(rule MappingRule) []string {
	out := []string{}
	for _, key := range []string{"properties", "facts"} {
		templates, _, err := mappingTemplateList(rule.Rule, key)
		if err != nil {
			continue
		}
		field := "property_id"
		if key == "facts" {
			field = "predicate"
		}
		for _, template := range templates {
			fields, err := mappingTemplateMap(template, "property")
			if err != nil {
				continue
			}
			predicate, err := requiredStringField(fields, "property", field)
			if err != nil {
				continue
			}
			optional, err := optionalBoolField(fields, "property", "optional")
			if err == nil && optional {
				continue
			}
			out = append(out, normalizeID(predicate))
		}
	}
	return normalizeIDs(out)
}

func (p *compiledMappingProjection) resolveTemporalContext(event graphprotocol.DomainEvent) (graphprotocol.TemporalContext, error) {
	temporal := event.Temporal
	if p.temporalTemplate != nil {
		temporal.Mode = string(p.temporalTemplate.mode)
		for path, target := range map[string]*time.Time{
			p.temporalTemplate.businessValidFrom: &temporal.BusinessValidFrom,
			p.temporalTemplate.sourceRecordedAt:  &temporal.SourceRecordedAt,
		} {
			if strings.TrimSpace(path) == "" {
				continue
			}
			value, found, err := resolveOptionalMappingValue(path, event.Payload)
			if err != nil {
				return graphprotocol.TemporalContext{}, err
			}
			if found {
				resolved, err := parseMappingTime(value, path)
				if err != nil {
					return graphprotocol.TemporalContext{}, err
				}
				*target = resolved
			}
		}
		for path, target := range map[string]**time.Time{
			p.temporalTemplate.businessValidTo:    &temporal.BusinessValidTo,
			p.temporalTemplate.sourceSupersededAt: &temporal.SourceSupersededAt,
		} {
			if strings.TrimSpace(path) == "" {
				continue
			}
			value, found, err := resolveOptionalMappingValue(path, event.Payload)
			if err != nil {
				return graphprotocol.TemporalContext{}, err
			}
			if found {
				resolved, err := parseMappingTime(value, path)
				if err != nil {
					return graphprotocol.TemporalContext{}, err
				}
				*target = &resolved
			}
		}
	}

	mode, err := normalizeTemporalMode(TemporalMode(normalizeID(temporal.Mode)))
	if err != nil {
		return graphprotocol.TemporalContext{}, err
	}
	temporal.Mode = string(mode)
	now := time.Now().UTC()
	if temporal.OrreryIngestedAt.IsZero() {
		temporal.OrreryIngestedAt = now
	} else {
		temporal.OrreryIngestedAt = temporal.OrreryIngestedAt.UTC()
	}
	if temporal.SourceRecordedAt.IsZero() {
		temporal.SourceRecordedAt = event.OccurredAt
	}
	if temporal.SourceRecordedAt.IsZero() {
		temporal.SourceRecordedAt = temporal.OrreryIngestedAt
	} else {
		temporal.SourceRecordedAt = temporal.SourceRecordedAt.UTC()
	}
	if mode != TemporalModeStatic && temporal.BusinessValidFrom.IsZero() {
		temporal.BusinessValidFrom = event.OccurredAt
	}
	if mode != TemporalModeStatic && temporal.BusinessValidFrom.IsZero() {
		return graphprotocol.TemporalContext{}, fmt.Errorf("%w: temporal mode %s requires business_valid_from", ErrInvalidDefinition, mode)
	}
	if !temporal.BusinessValidFrom.IsZero() {
		temporal.BusinessValidFrom = temporal.BusinessValidFrom.UTC()
	}
	if temporal.BusinessValidTo != nil {
		validTo := temporal.BusinessValidTo.UTC()
		temporal.BusinessValidTo = &validTo
		if temporal.BusinessValidFrom.IsZero() || !validTo.After(temporal.BusinessValidFrom) {
			return graphprotocol.TemporalContext{}, fmt.Errorf("%w: business_valid_to must be after business_valid_from", ErrInvalidDefinition)
		}
	}
	if temporal.SourceSupersededAt != nil {
		supersededAt := temporal.SourceSupersededAt.UTC()
		temporal.SourceSupersededAt = &supersededAt
		if !supersededAt.After(temporal.SourceRecordedAt) {
			return graphprotocol.TemporalContext{}, fmt.Errorf("%w: source_superseded_at must be after source_recorded_at", ErrInvalidDefinition)
		}
	}
	return temporal, nil
}

func resolveMappingValue(template any, payload map[string]any) (any, error) {
	text, ok := template.(string)
	if !ok {
		return template, nil
	}
	if strings.Contains(text, "||") {
		var missing error
		for _, candidate := range strings.Split(text, "||") {
			value, err := resolveMappingValue(strings.TrimSpace(candidate), payload)
			if err == nil {
				return value, nil
			}
			if !isMissingMappingPayloadField(err) {
				return nil, err
			}
			missing = err
		}
		if missing != nil {
			return nil, missing
		}
	}
	if strings.HasPrefix(text, "$.") {
		return payloadValue(text, payload)
	}
	return resolveTemplateString(text, payload)
}

func resolveOptionalMappingValue(template string, payload map[string]any) (any, bool, error) {
	value, err := resolveMappingValue(template, payload)
	if err != nil {
		if isMissingMappingPayloadField(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return value, true, nil
}

func resolveMappingString(template string, payload map[string]any) (string, error) {
	if template == "" {
		return "", nil
	}
	value, err := resolveMappingValue(template, payload)
	if err != nil {
		return "", err
	}
	return fmt.Sprint(value), nil
}

func resolveLinkEndpointID(template string, payload map[string]any, optional bool) (string, bool, error) {
	value, err := resolveMappingString(template, payload)
	if err != nil {
		if optional && isMissingMappingPayloadField(err) {
			return "", false, nil
		}
		return "", false, err
	}
	if optional && strings.TrimSpace(value) == "" {
		return "", false, nil
	}
	return value, true, nil
}

func payloadValue(path string, payload map[string]any) (any, error) {
	fieldPath := strings.TrimPrefix(strings.TrimSpace(path), "$.")
	if fieldPath == "" {
		return nil, fmt.Errorf("%w: invalid payload path %s", ErrInvalidDefinition, path)
	}
	var value any = payload
	for _, field := range strings.Split(fieldPath, ".") {
		fields, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: %w %s", ErrInvalidDefinition, errMissingMappingPayloadField, fieldPath)
		}
		value, ok = fields[field]
		if !ok || value == nil {
			return nil, fmt.Errorf("%w: %w %s", ErrInvalidDefinition, errMissingMappingPayloadField, fieldPath)
		}
	}
	return value, nil
}

func parseMappingTime(value any, source string) (time.Time, error) {
	switch typed := value.(type) {
	case time.Time:
		if !typed.IsZero() {
			return typed.UTC(), nil
		}
	case string:
		text := strings.TrimSpace(typed)
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02"} {
			parsed, err := time.Parse(layout, text)
			if err == nil {
				return parsed.UTC(), nil
			}
		}
	case json.Number:
		seconds, err := typed.Int64()
		if err == nil {
			return time.Unix(seconds, 0).UTC(), nil
		}
	case int64:
		return time.Unix(typed, 0).UTC(), nil
	case int:
		return time.Unix(int64(typed), 0).UTC(), nil
	case float64:
		seconds, fraction := math.Modf(typed)
		return time.Unix(int64(seconds), int64(fraction*float64(time.Second))).UTC(), nil
	}
	return time.Time{}, fmt.Errorf("%w: temporal source %s must be RFC3339, YYYY-MM-DD, or a Unix timestamp", ErrInvalidDefinition, source)
}

func isMissingMappingPayloadField(err error) bool {
	return errors.Is(err, errMissingMappingPayloadField)
}

var mappingTemplatePlaceholder = regexp.MustCompile(`\{([A-Za-z0-9_]+)\}`)

func resolveTemplateString(template string, payload map[string]any) (string, error) {
	var firstErr error
	out := mappingTemplatePlaceholder.ReplaceAllStringFunc(template, func(match string) string {
		if firstErr != nil {
			return match
		}
		key := strings.TrimSuffix(strings.TrimPrefix(match, "{"), "}")
		value, ok := payload[key]
		if !ok || value == nil {
			firstErr = fmt.Errorf("%w: %w %s", ErrInvalidDefinition, errMissingMappingPayloadField, key)
			return match
		}
		return fmt.Sprint(value)
	})
	if firstErr != nil {
		return "", firstErr
	}
	return out, nil
}
