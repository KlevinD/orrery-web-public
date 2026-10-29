package ontology

import "fmt"

type mappingRuleLookup struct {
	objectTypeExists func(string) (bool, error)
	linkType         func(string) (LinkType, error)
	propertyExists   func(string, string) (bool, error)
}

func validateMappingRuleTemplates(rule MappingRule, lookup mappingRuleLookup) error {
	if len(rule.Rule) == 0 {
		return fmt.Errorf("%w: mapping rule requires objects, properties, facts, or links templates", ErrInvalidDefinition)
	}
	for key := range rule.Rule {
		switch key {
		case "objects", "properties", "facts", "links", "temporal":
		default:
			return fmt.Errorf("%w: unsupported mapping rule field %q", ErrInvalidDefinition, key)
		}
	}
	if err := validateTemporalMappingTemplate(rule.Rule); err != nil {
		return err
	}
	seen := false
	for _, key := range []string{"objects", "properties", "facts", "links"} {
		templates, ok, err := mappingTemplateList(rule.Rule, key)
		if err != nil {
			return err
		}
		if ok {
			seen = true
		}
		for _, template := range templates {
			switch key {
			case "objects":
				if err := validateObjectMappingTemplate(template, lookup); err != nil {
					return err
				}
			case "properties":
				if err := validatePropertyMappingTemplate(template, "property_id", lookup); err != nil {
					return err
				}
			case "facts":
				if err := validatePropertyMappingTemplate(template, "predicate", lookup); err != nil {
					return err
				}
			case "links":
				if err := validateLinkMappingTemplate(template, lookup); err != nil {
					return err
				}
			}
		}
	}
	if !seen {
		return fmt.Errorf("%w: mapping rule requires at least one template", ErrInvalidDefinition)
	}
	return nil
}

func validateTemporalMappingTemplate(rule map[string]any) error {
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
	mode, err := requiredStringField(fields, "temporal", "mode")
	if err != nil {
		return err
	}
	if _, err := normalizeTemporalMode(TemporalMode(normalizeID(mode))); err != nil {
		return err
	}
	for _, key := range []string{"business_valid_from", "business_valid_to", "source_recorded_at", "source_superseded_at"} {
		if _, exists := fields[key]; !exists {
			continue
		}
		if _, err := stringField(fields, "temporal", key); err != nil {
			return err
		}
	}
	return nil
}

func mappingTemplateList(rule map[string]any, key string) ([]any, bool, error) {
	value, ok := rule[key]
	if !ok {
		return nil, false, nil
	}
	switch typed := value.(type) {
	case []any:
		return typed, true, nil
	case []map[string]any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, item)
		}
		return out, true, nil
	default:
		return nil, true, fmt.Errorf("%w: mapping rule %s must be an array", ErrInvalidDefinition, key)
	}
}

func validateObjectMappingTemplate(template any, lookup mappingRuleLookup) error {
	fields, err := mappingTemplateMap(template, "object")
	if err != nil {
		return err
	}
	if err := requireAllowedFields(fields, "object", "type", "id", "canonical_key", "label"); err != nil {
		return err
	}
	objectType, err := requiredStringField(fields, "object", "type")
	if err != nil {
		return err
	}
	if _, err := requiredStringField(fields, "object", "id"); err != nil {
		return err
	}
	if _, err := requiredStringField(fields, "object", "canonical_key"); err != nil {
		return err
	}
	if _, ok := fields["label"]; ok {
		if _, err := stringField(fields, "object", "label"); err != nil {
			return err
		}
	}
	return requireMappingObjectType(lookup, objectType)
}

func validatePropertyMappingTemplate(template any, propertyField string, lookup mappingRuleLookup) error {
	fields, err := mappingTemplateMap(template, "property")
	if err != nil {
		return err
	}
	if err := requireAllowedFields(fields, "property", "object_type", "object_id", propertyField, "value", "optional"); err != nil {
		return err
	}
	objectType, err := requiredStringField(fields, "property", "object_type")
	if err != nil {
		return err
	}
	if _, err := requiredStringField(fields, "property", "object_id"); err != nil {
		return err
	}
	propertyID, err := requiredStringField(fields, "property", propertyField)
	if err != nil {
		return err
	}
	if _, ok := fields["value"]; !ok {
		return fmt.Errorf("%w: property template requires value", ErrInvalidDefinition)
	}
	if !isMappingScalar(fields["value"]) {
		return fmt.Errorf("%w: property template value must be a scalar or source path", ErrInvalidDefinition)
	}
	if _, err := optionalBoolField(fields, "property", "optional"); err != nil {
		return err
	}
	if err := requireMappingObjectType(lookup, objectType); err != nil {
		return err
	}
	return requireMappingPropertyType(lookup, objectType, propertyID)
}

func validateLinkMappingTemplate(template any, lookup mappingRuleLookup) error {
	fields, err := mappingTemplateMap(template, "link")
	if err != nil {
		return err
	}
	if err := requireAllowedFields(fields, "link", "type", "identity", "from_type", "from_id", "to_type", "to_id", "evidence_id", "optional"); err != nil {
		return err
	}
	linkTypeID, err := requiredStringField(fields, "link", "type")
	if err != nil {
		return err
	}
	if _, ok := fields["identity"]; ok {
		if _, err := stringField(fields, "link", "identity"); err != nil {
			return err
		}
	}
	fromType, err := requiredStringField(fields, "link", "from_type")
	if err != nil {
		return err
	}
	if _, err := requiredStringField(fields, "link", "from_id"); err != nil {
		return err
	}
	toType, err := requiredStringField(fields, "link", "to_type")
	if err != nil {
		return err
	}
	if _, err := requiredStringField(fields, "link", "to_id"); err != nil {
		return err
	}
	if _, ok := fields["evidence_id"]; ok {
		if _, err := stringField(fields, "link", "evidence_id"); err != nil {
			return err
		}
	}
	if _, err := optionalBoolField(fields, "link", "optional"); err != nil {
		return err
	}
	linkType, err := lookup.linkType(normalizeID(linkTypeID))
	if err != nil {
		return err
	}
	fromType = normalizeID(fromType)
	toType = normalizeID(toType)
	if linkType.FromObjectType != fromType || linkType.ToObjectType != toType {
		return fmt.Errorf("%w: link template %s endpoints must be %s -> %s", ErrInvalidDefinition, linkType.ID, linkType.FromObjectType, linkType.ToObjectType)
	}
	return nil
}

func mappingTemplateMap(template any, kind string) (map[string]any, error) {
	fields, ok := template.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: %s template must be an object", ErrInvalidDefinition, kind)
	}
	return fields, nil
}

func requireAllowedFields(fields map[string]any, kind string, allowed ...string) error {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = struct{}{}
	}
	for key := range fields {
		if _, ok := allowedSet[key]; !ok {
			return fmt.Errorf("%w: unsupported %s template field %q", ErrInvalidDefinition, kind, key)
		}
	}
	return nil
}

func requiredStringField(fields map[string]any, kind string, key string) (string, error) {
	value, ok := fields[key]
	if !ok {
		return "", fmt.Errorf("%w: %s template requires %s", ErrInvalidDefinition, kind, key)
	}
	text, ok := value.(string)
	if !ok || normalizeID(text) == "" {
		return "", fmt.Errorf("%w: %s template %s must be a non-empty string", ErrInvalidDefinition, kind, key)
	}
	return text, nil
}

func stringField(fields map[string]any, kind string, key string) (string, error) {
	text, ok := fields[key].(string)
	if !ok {
		return "", fmt.Errorf("%w: %s template %s must be a string", ErrInvalidDefinition, kind, key)
	}
	return text, nil
}

func optionalBoolField(fields map[string]any, kind string, key string) (bool, error) {
	value, ok := fields[key]
	if !ok {
		return false, nil
	}
	typed, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%w: %s template %s must be a boolean", ErrInvalidDefinition, kind, key)
	}
	return typed, nil
}

func requireMappingObjectType(lookup mappingRuleLookup, objectType string) error {
	ok, err := lookup.objectTypeExists(normalizeID(objectType))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownObjectType, objectType)
	}
	return nil
}

func requireMappingPropertyType(lookup mappingRuleLookup, objectType string, propertyID string) error {
	ok, err := lookup.propertyExists(normalizeID(objectType), normalizeID(propertyID))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: %s.%s", ErrUnknownPropertyType, objectType, propertyID)
	}
	return nil
}

func isMappingScalar(value any) bool {
	switch value.(type) {
	case nil, string, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return true
	default:
		return false
	}
}
