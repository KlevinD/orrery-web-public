package graphprotocol

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
)

type OntologyRegistry interface {
	HasObjectType(ctx context.Context, objectType string) (bool, error)
	HasLinkType(ctx context.Context, linkType string) (bool, error)
}

type OntologyLinkConstraint struct {
	FromObjectType   string
	ToObjectType     string
	EvidenceRequired bool
}

type OntologyPropertyConstraint struct {
	ValueType string
}

type OntologyConstraintRegistry interface {
	LinkConstraint(ctx context.Context, linkType string) (OntologyLinkConstraint, bool, error)
	PropertyConstraint(ctx context.Context, objectType string, predicate string) (OntologyPropertyConstraint, bool, error)
}

type ConformanceGate struct {
	ontology OntologyRegistry
}

type ConformanceGateOption func(*ConformanceGate)

func NewConformanceGate(options ...ConformanceGateOption) ConformanceGate {
	gate := ConformanceGate{}
	for _, option := range options {
		if option != nil {
			option(&gate)
		}
	}
	return gate
}

func WithOntologyRegistry(registry OntologyRegistry) ConformanceGateOption {
	return func(gate *ConformanceGate) {
		gate.ontology = registry
	}
}

type ConformanceIssue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ConformanceReport struct {
	OK               bool               `json:"ok"`
	Issues           []ConformanceIssue `json:"issues,omitempty"`
	ObjectTypes      []string           `json:"object_types,omitempty"`
	LinkTypes        []string           `json:"link_types,omitempty"`
	FactPredicates   []string           `json:"fact_predicates,omitempty"`
	ProjectionName   string             `json:"projection_name,omitempty"`
	ProjectionCursor ProjectionCursor   `json:"projection_cursor,omitempty"`
}

func (g ConformanceGate) Check(ctx context.Context, projection GraphProjection, event DomainEvent) ConformanceReport {
	report := ConformanceReport{OK: true}
	if projection == nil {
		report.addIssue("projection_missing", "graph projection is required")
		return report
	}
	spec := projection.Spec()
	report.ProjectionName = spec.Name
	validateSpec(&report, spec)
	validateEventSchemaVersion(&report, spec, event)
	g.validateSpecAgainstOntology(ctx, &report, spec)
	result, err := projection.Project(ctx, event)
	if err != nil {
		report.addIssue("projection_error", err.Error())
		return report
	}
	return g.CheckResult(ctx, projection, event, result)
}

// CheckResult validates the exact projection result that will be persisted.
func (g ConformanceGate) CheckResult(ctx context.Context, projection GraphProjection, event DomainEvent, result ProjectionResult) ConformanceReport {
	report := ConformanceReport{OK: true}
	if projection == nil {
		report.addIssue("projection_missing", "graph projection is required")
		return report
	}
	spec := projection.Spec()
	report.ProjectionName = spec.Name
	validateSpec(&report, spec)
	validateEventSchemaVersion(&report, spec, event)
	g.validateSpecAgainstOntology(ctx, &report, spec)
	report.ProjectionCursor = result.Cursor
	validateCursor(&report, result.Cursor, event)
	coverage := newCoverage()
	for _, assertion := range result.Assertions {
		g.validateAssertion(ctx, &report, spec, event, assertion, coverage)
	}
	report.ObjectTypes = sortedKeys(coverage.objects)
	report.LinkTypes = sortedKeys(coverage.links)
	report.FactPredicates = sortedKeys(coverage.facts)
	validateCoverage(&report, spec, coverage)
	return report
}

func (g ConformanceGate) ValidateSpec(ctx context.Context, spec ProjectionSpec) ConformanceReport {
	report := ConformanceReport{OK: true, ProjectionName: spec.Name}
	validateSpec(&report, spec)
	g.validateSpecAgainstOntology(ctx, &report, spec)
	return report
}

func (g ConformanceGate) validateSpecAgainstOntology(ctx context.Context, report *ConformanceReport, spec ProjectionSpec) {
	if g.ontology == nil {
		return
	}
	for _, objectType := range spec.ObjectTypes {
		g.validateOntologyObjectType(ctx, report, objectType)
	}
	for _, linkType := range spec.LinkTypes {
		g.validateOntologyLinkType(ctx, report, linkType)
	}
}

func validateSpec(report *ConformanceReport, spec ProjectionSpec) {
	if strings.TrimSpace(spec.Name) == "" {
		report.addIssue("missing_projection_name", "projection name is required")
	}
	if strings.TrimSpace(spec.Version) == "" {
		report.addIssue("missing_projection_version", "projection version is required")
	}
	if strings.TrimSpace(spec.SourceType) == "" {
		report.addIssue("missing_source_type", "projection source type is required")
	}
}

func validateEventSchemaVersion(report *ConformanceReport, spec ProjectionSpec, event DomainEvent) {
	want := strings.TrimSpace(spec.SourceSchemaVersion)
	if want == "" {
		return
	}
	got := strings.TrimSpace(event.SchemaVersion)
	if got == "" {
		got = "v1"
	}
	if got != want {
		report.addIssue("event_schema_version_mismatch", fmt.Sprintf("event schema version %q does not match projection schema version %q", got, want))
	}
}

func validateCursor(report *ConformanceReport, cursor ProjectionCursor, event DomainEvent) {
	if strings.TrimSpace(cursor.SourceID) == "" {
		report.addIssue("missing_cursor_source", "projection cursor source id is required")
	}
	if event.ID != "" && cursor.SourceID != event.ID {
		report.addIssue("cursor_source_mismatch", fmt.Sprintf("cursor source %q does not match event %q", cursor.SourceID, event.ID))
	}
	if event.Version != 0 && cursor.SourceVersion != event.Version {
		report.addIssue("cursor_version_mismatch", fmt.Sprintf("cursor version %d does not match event version %d", cursor.SourceVersion, event.Version))
	}
}

func (g ConformanceGate) validateAssertion(ctx context.Context, report *ConformanceReport, spec ProjectionSpec, event DomainEvent, assertion Assertion, coverage *coverageSet) {
	if assertion == nil {
		report.addIssue("nil_assertion", "assertion is nil")
		return
	}
	if event.TenantID != "" && assertion.tenantID() != event.TenantID {
		report.addIssue("tenant_mismatch", fmt.Sprintf("assertion tenant %q does not match event tenant %q", assertion.tenantID(), event.TenantID))
	}
	validateProvenance(report, assertion.provenanceRef(), event)
	switch typed := assertion.(type) {
	case ObjectAssertion:
		validateObjectAssertion(report, typed, coverage)
		g.validateOntologyObjectType(ctx, report, typed.Object.Type)
	case LinkAssertion:
		validateLinkAssertion(report, typed, coverage)
		g.validateOntologyObjectType(ctx, report, typed.From.Type)
		g.validateOntologyObjectType(ctx, report, typed.To.Type)
		g.validateOntologyLinkType(ctx, report, typed.LinkType)
		g.validateOntologyLinkAssertion(ctx, report, typed)
	case FactAssertion:
		validateFactAssertion(report, typed, coverage)
		g.validateOntologyObjectType(ctx, report, typed.Subject.Type)
		g.validateOntologyFactAssertion(ctx, report, typed)
	default:
		report.addIssue("unknown_assertion", fmt.Sprintf("unknown assertion kind %q", assertion.assertionKind()))
	}
}

func (g ConformanceGate) validateOntologyLinkAssertion(ctx context.Context, report *ConformanceReport, assertion LinkAssertion) {
	registry, ok := g.ontology.(OntologyConstraintRegistry)
	if !ok {
		return
	}
	constraint, found, err := registry.LinkConstraint(ctx, assertion.LinkType)
	if err != nil {
		report.addIssue("ontology_registry_error", err.Error())
		return
	}
	if !found {
		return
	}
	if assertion.From.Type != constraint.FromObjectType || assertion.To.Type != constraint.ToObjectType {
		report.addIssue("link_direction_mismatch", fmt.Sprintf("link type %q requires %s -> %s", assertion.LinkType, constraint.FromObjectType, constraint.ToObjectType))
	}
	if constraint.EvidenceRequired && strings.TrimSpace(assertion.Provenance.SourceEventID) == "" {
		report.addIssue("missing_required_evidence", fmt.Sprintf("link type %q requires evidence", assertion.LinkType))
	}
}

func (g ConformanceGate) validateOntologyFactAssertion(ctx context.Context, report *ConformanceReport, assertion FactAssertion) {
	registry, ok := g.ontology.(OntologyConstraintRegistry)
	if !ok {
		return
	}
	constraint, found, err := registry.PropertyConstraint(ctx, assertion.Subject.Type, assertion.Predicate)
	if err != nil {
		report.addIssue("ontology_registry_error", err.Error())
		return
	}
	if !found {
		report.addIssue("unknown_property_type", fmt.Sprintf("property %q is not registered for object type %q", assertion.Predicate, assertion.Subject.Type))
		return
	}
	if !conformanceValueMatchesType(assertion.Value, constraint.ValueType) {
		report.addIssue("fact_value_type_mismatch", fmt.Sprintf("property %q requires value type %q", assertion.Predicate, constraint.ValueType))
	}
}

func conformanceValueMatchesType(value any, valueType string) bool {
	if value == nil {
		return false
	}
	kind := reflect.TypeOf(value).Kind()
	switch strings.TrimSpace(valueType) {
	case "string":
		return kind == reflect.String
	case "number":
		switch value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, json.Number:
			return true
		default:
			return kind >= reflect.Int && kind <= reflect.Float64
		}
	case "boolean":
		return kind == reflect.Bool
	case "object":
		return kind == reflect.Map || kind == reflect.Struct
	case "ref":
		return kind == reflect.String || kind == reflect.Map || kind == reflect.Struct
	case "array":
		return kind == reflect.Array || kind == reflect.Slice
	case "date":
		if _, ok := value.(time.Time); ok {
			return true
		}
		text, ok := value.(string)
		if !ok {
			return false
		}
		_, err := time.Parse("2006-01-02", text)
		return err == nil
	case "datetime":
		if _, ok := value.(time.Time); ok {
			return true
		}
		text, ok := value.(string)
		if !ok {
			return false
		}
		_, err := time.Parse(time.RFC3339Nano, text)
		return err == nil
	case "duration":
		if _, ok := value.(time.Duration); ok {
			return true
		}
		text, ok := value.(string)
		if !ok {
			return false
		}
		_, err := time.ParseDuration(text)
		return err == nil
	case "enum":
		return kind == reflect.String
	default:
		return false
	}
}

func (g ConformanceGate) validateOntologyObjectType(ctx context.Context, report *ConformanceReport, objectType string) {
	objectType = strings.TrimSpace(objectType)
	if g.ontology == nil || objectType == "" {
		return
	}
	ok, err := g.ontology.HasObjectType(ctx, objectType)
	if err != nil {
		report.addIssue("ontology_registry_error", err.Error())
		return
	}
	if !ok {
		report.addIssue("unknown_object_type", fmt.Sprintf("object type %q is not registered in ontology", objectType))
	}
}

func (g ConformanceGate) validateOntologyLinkType(ctx context.Context, report *ConformanceReport, linkType string) {
	linkType = strings.TrimSpace(linkType)
	if g.ontology == nil || linkType == "" {
		return
	}
	ok, err := g.ontology.HasLinkType(ctx, linkType)
	if err != nil {
		report.addIssue("ontology_registry_error", err.Error())
		return
	}
	if !ok {
		report.addIssue("unknown_link_type", fmt.Sprintf("link type %q is not registered in ontology", linkType))
	}
}

func validateObjectAssertion(report *ConformanceReport, assertion ObjectAssertion, coverage *coverageSet) {
	if strings.TrimSpace(assertion.Object.Type) == "" || strings.TrimSpace(assertion.Object.ID) == "" {
		report.addIssue("invalid_object_ref", "object assertion requires object type and id")
		return
	}
	if strings.TrimSpace(assertion.CanonicalKey) == "" {
		report.addIssue("missing_canonical_key", fmt.Sprintf("object %s/%s missing canonical key", assertion.Object.Type, assertion.Object.ID))
	}
	coverage.objects[assertion.Object.Type] = struct{}{}
}

func validateLinkAssertion(report *ConformanceReport, assertion LinkAssertion, coverage *coverageSet) {
	if strings.TrimSpace(assertion.LinkType) == "" {
		report.addIssue("missing_link_type", "link assertion requires link type")
		return
	}
	if assertion.From.TenantID != assertion.To.TenantID {
		report.addIssue("cross_tenant_link", "link assertion endpoints must share tenant")
	}
	if strings.TrimSpace(assertion.From.ID) == "" || strings.TrimSpace(assertion.To.ID) == "" {
		report.addIssue("invalid_link_endpoint", "link assertion requires from/to object ids")
	}
	coverage.links[assertion.LinkType] = struct{}{}
}

func validateFactAssertion(report *ConformanceReport, assertion FactAssertion, coverage *coverageSet) {
	if strings.TrimSpace(assertion.Subject.Type) == "" || strings.TrimSpace(assertion.Subject.ID) == "" {
		report.addIssue("invalid_fact_subject", "fact assertion requires subject type and id")
	}
	if strings.TrimSpace(assertion.Predicate) == "" {
		report.addIssue("missing_fact_predicate", "fact assertion requires predicate")
		return
	}
	coverage.facts[assertion.Predicate] = struct{}{}
}

func validateProvenance(report *ConformanceReport, provenance ProvenanceRef, event DomainEvent) {
	if strings.TrimSpace(provenance.SourceEventID) == "" || strings.TrimSpace(provenance.SourceEventType) == "" || strings.TrimSpace(provenance.SourceSystem) == "" {
		report.addIssue("missing_provenance", "assertion requires source event id, source event type, and source system")
		return
	}
	expectedSourceEventID := event.SourceEventID
	if expectedSourceEventID == "" {
		expectedSourceEventID = event.ID
	}
	if expectedSourceEventID != "" && provenance.SourceEventID != expectedSourceEventID {
		report.addIssue("provenance_event_mismatch", fmt.Sprintf("provenance event %q does not match source event %q", provenance.SourceEventID, expectedSourceEventID))
	}
	if event.Type != "" && provenance.SourceEventType != event.Type {
		report.addIssue("provenance_type_mismatch", fmt.Sprintf("provenance type %q does not match event type %q", provenance.SourceEventType, event.Type))
	}
	if strings.TrimSpace(provenance.Actor.Kind) == "" || strings.TrimSpace(provenance.Actor.ID) == "" {
		report.addIssue("missing_provenance_actor", "provenance actor is required")
	}
	if provenance.ObservedAt.IsZero() {
		report.addIssue("missing_provenance_time", "provenance observed time is required")
	}
	validateTemporalContext(report, provenance.Temporal)
}

func validateTemporalContext(report *ConformanceReport, temporal TemporalContext) {
	mode := strings.ToLower(strings.TrimSpace(temporal.Mode))
	if mode == "" {
		return
	}
	switch mode {
	case "static", "state_interval", "event", "measurement":
	default:
		report.addIssue("invalid_temporal_mode", fmt.Sprintf("unsupported temporal mode %q", temporal.Mode))
		return
	}
	if temporal.SourceRecordedAt.IsZero() || temporal.OrreryIngestedAt.IsZero() {
		report.addIssue("missing_recorded_time", "temporal context requires source_recorded_at and orrery_ingested_at")
	}
	if mode != "static" && temporal.BusinessValidFrom.IsZero() {
		report.addIssue("missing_business_valid_time", fmt.Sprintf("temporal mode %q requires business_valid_from", mode))
	}
	if temporal.BusinessValidTo != nil &&
		(temporal.BusinessValidFrom.IsZero() || !temporal.BusinessValidTo.After(temporal.BusinessValidFrom)) {
		report.addIssue("invalid_business_valid_range", "business_valid_to must be after business_valid_from")
	}
	if temporal.SourceSupersededAt != nil &&
		(temporal.SourceRecordedAt.IsZero() || !temporal.SourceSupersededAt.After(temporal.SourceRecordedAt)) {
		report.addIssue("invalid_recorded_range", "source_superseded_at must be after source_recorded_at")
	}
}

func validateCoverage(report *ConformanceReport, spec ProjectionSpec, coverage *coverageSet) {
	for _, objectType := range spec.ObjectTypes {
		if _, ok := coverage.objects[objectType]; !ok {
			report.addIssue("missing_object_coverage", fmt.Sprintf("projection did not emit object type %q", objectType))
		}
	}
	for _, linkType := range spec.LinkTypes {
		if _, ok := coverage.links[linkType]; !ok {
			report.addIssue("missing_link_coverage", fmt.Sprintf("projection did not emit link type %q", linkType))
		}
	}
	for _, predicate := range spec.FactPredicates {
		if _, ok := coverage.facts[predicate]; !ok {
			report.addIssue("missing_fact_coverage", fmt.Sprintf("projection did not emit fact predicate %q", predicate))
		}
	}
}

func (r *ConformanceReport) addIssue(code string, message string) {
	r.OK = false
	r.Issues = append(r.Issues, ConformanceIssue{Code: code, Message: message})
}

func (r ConformanceReport) HasIssue(code string) bool {
	for _, issue := range r.Issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func (r ConformanceReport) CoversObjectType(objectType string) bool {
	return containsString(r.ObjectTypes, objectType)
}

func (r ConformanceReport) CoversLinkType(linkType string) bool {
	return containsString(r.LinkTypes, linkType)
}

func (r ConformanceReport) CoversFactPredicate(predicate string) bool {
	return containsString(r.FactPredicates, predicate)
}

type coverageSet struct {
	objects map[string]struct{}
	links   map[string]struct{}
	facts   map[string]struct{}
}

func newCoverage() *coverageSet {
	return &coverageSet{
		objects: map[string]struct{}{},
		links:   map[string]struct{}{},
		facts:   map[string]struct{}{},
	}
}
