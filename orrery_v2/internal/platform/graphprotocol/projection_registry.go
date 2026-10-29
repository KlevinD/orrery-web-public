package graphprotocol

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type ConformanceError struct {
	Report ConformanceReport
}

func (e ConformanceError) Error() string {
	if len(e.Report.Issues) == 0 {
		return "graph projection conformance failed"
	}
	return fmt.Sprintf("graph projection conformance failed: %s", e.Report.Issues[0].Code)
}

func AsConformanceError(err error) (ConformanceReport, bool) {
	if err == nil {
		return ConformanceReport{}, false
	}
	conformanceErr, ok := err.(ConformanceError)
	if ok {
		return conformanceErr.Report, true
	}
	return ConformanceReport{}, false
}

type ProjectionRegistry struct {
	mu          sync.RWMutex
	gate        ConformanceGate
	projections map[string]GraphProjection
	bySource    map[string][]string
}

func NewProjectionRegistry(gate ConformanceGate) *ProjectionRegistry {
	return &ProjectionRegistry{
		gate:        gate,
		projections: map[string]GraphProjection{},
		bySource:    map[string][]string{},
	}
}

func (r *ProjectionRegistry) Register(ctx context.Context, projection GraphProjection) error {
	if projection == nil {
		return ConformanceError{Report: ConformanceReport{
			OK:     false,
			Issues: []ConformanceIssue{{Code: "projection_missing", Message: "graph projection is required"}},
		}}
	}
	spec := projection.Spec()
	report := r.gate.ValidateSpec(ctx, spec)
	if !report.OK {
		return ConformanceError{Report: report}
	}
	name := strings.TrimSpace(spec.Name)
	sourceType := strings.TrimSpace(spec.SourceType)
	r.mu.Lock()
	defer r.mu.Unlock()
	if previous, ok := r.projections[name]; ok {
		previousSource := strings.TrimSpace(previous.Spec().SourceType)
		r.bySource[previousSource] = removeProjectionName(r.bySource[previousSource], name)
	}
	r.projections[name] = projection
	r.bySource[sourceType] = appendIfMissing(r.bySource[sourceType], name)
	sort.Strings(r.bySource[sourceType])
	return nil
}

func (r *ProjectionRegistry) ReplaceAll(ctx context.Context, projections []GraphProjection) error {
	next := NewProjectionRegistry(r.gate)
	for _, projection := range projections {
		if err := next.Register(ctx, projection); err != nil {
			return err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.projections = next.projections
	r.bySource = next.bySource
	return nil
}

func (r *ProjectionRegistry) Get(name string) (GraphProjection, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	projection, ok := r.projections[strings.TrimSpace(name)]
	return projection, ok
}

func (r *ProjectionRegistry) ListBySourceType(sourceType string) []GraphProjection {
	sourceType = strings.TrimSpace(sourceType)
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := append([]string(nil), r.bySource[sourceType]...)
	out := make([]GraphProjection, 0, len(names))
	for _, name := range names {
		if projection, ok := r.projections[name]; ok {
			out = append(out, projection)
		}
	}
	return out
}

func appendIfMissing(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func removeProjectionName(values []string, value string) []string {
	out := values[:0]
	for _, existing := range values {
		if existing != value {
			out = append(out, existing)
		}
	}
	return out
}
