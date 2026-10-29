package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/app/ontology"
)

type ontologyInitializationMode string

const (
	ontologyInitializationDryRun ontologyInitializationMode = "dry_run"
	ontologyInitializationApply  ontologyInitializationMode = "apply"
)

type ontologyInitializationRequest struct {
	Mode     ontologyInitializationMode
	Operator string
	PageSize int
}

type ontologyInitializationIssue struct {
	Domain    string `json:"domain"`
	SourceRef string `json:"source_ref,omitempty"`
	Code      string `json:"code"`
}

type ontologyInitializationDomainReport struct {
	Name       string                        `json:"name"`
	Source     int                           `json:"source"`
	Projected  int                           `json:"projected"`
	Unchanged  int                           `json:"unchanged"`
	Skipped    int                           `json:"skipped"`
	Failed     int                           `json:"failed"`
	Unresolved int                           `json:"unresolved"`
	Issues     []ontologyInitializationIssue `json:"issues,omitempty"`
}

type ontologyInitializationReport struct {
	RunID       string                               `json:"run_id"`
	Mode        ontologyInitializationMode           `json:"mode"`
	Operator    string                               `json:"operator"`
	StartedAt   time.Time                            `json:"started_at"`
	CompletedAt time.Time                            `json:"completed_at"`
	Source      int                                  `json:"source"`
	Projected   int                                  `json:"projected"`
	Unchanged   int                                  `json:"unchanged"`
	Skipped     int                                  `json:"skipped"`
	Failed      int                                  `json:"failed"`
	Unresolved  int                                  `json:"unresolved"`
	Domains     []ontologyInitializationDomainReport `json:"domains"`
	Before      ontologyInitializationInventory      `json:"before"`
	After       ontologyInitializationInventory      `json:"after"`
}

type ontologyInitializationInventory struct {
	Definitions            ontology.DefaultDefinitionAudit `json:"definitions"`
	Objects                map[string]int                  `json:"objects"`
	Links                  map[string]int                  `json:"links"`
	DuplicateCanonicalKeys int                             `json:"duplicate_canonical_keys"`
}

type ontologyInitializationAdapter interface {
	Name() string
	Reconcile(context.Context, ontologyInitializationRequest) (ontologyInitializationDomainReport, error)
}

type ontologyInitializationCoordinator struct {
	adapters []ontologyInitializationAdapter
}

func newOntologyInitializationCoordinator(adapters []ontologyInitializationAdapter) ontologyInitializationCoordinator {
	return ontologyInitializationCoordinator{adapters: append([]ontologyInitializationAdapter(nil), adapters...)}
}

func (c ontologyInitializationCoordinator) Run(ctx context.Context, request ontologyInitializationRequest) (ontologyInitializationReport, error) {
	request.Operator = strings.TrimSpace(request.Operator)
	switch request.Mode {
	case ontologyInitializationDryRun:
	case ontologyInitializationApply:
		if request.Operator == "" {
			return ontologyInitializationReport{}, fmt.Errorf("ontology initialization operator is required for apply")
		}
	default:
		return ontologyInitializationReport{}, fmt.Errorf("ontology initialization mode %q is not supported", request.Mode)
	}
	request.PageSize = normalizeOntologyInitializationPageSize(request.PageSize)
	runID, err := newOntologyInitializationRunID()
	if err != nil {
		return ontologyInitializationReport{}, err
	}
	report := ontologyInitializationReport{
		RunID:     runID,
		Mode:      request.Mode,
		Operator:  request.Operator,
		StartedAt: time.Now().UTC(),
		Domains:   make([]ontologyInitializationDomainReport, 0, len(c.adapters)),
	}
	for _, adapter := range c.adapters {
		if adapter == nil || strings.TrimSpace(adapter.Name()) == "" {
			report.CompletedAt = time.Now().UTC()
			return report, fmt.Errorf("ontology initialization adapter name is required")
		}
		domain, err := adapter.Reconcile(ctx, request)
		if domain.Name == "" {
			domain.Name = adapter.Name()
		}
		if len(domain.Issues) > 100 {
			domain.Issues = domain.Issues[:100]
		}
		report.Domains = append(report.Domains, domain)
		report.addDomain(domain)
		if err != nil {
			report.CompletedAt = time.Now().UTC()
			return report, fmt.Errorf("reconcile ontology domain %s: %w", adapter.Name(), err)
		}
	}
	report.CompletedAt = time.Now().UTC()
	return report, nil
}

func (r *ontologyInitializationReport) addDomain(domain ontologyInitializationDomainReport) {
	r.Source += domain.Source
	r.Projected += domain.Projected
	r.Unchanged += domain.Unchanged
	r.Skipped += domain.Skipped
	r.Failed += domain.Failed
	r.Unresolved += domain.Unresolved
}

func normalizeOntologyInitializationPageSize(value int) int {
	if value <= 0 {
		return 100
	}
	if value > 500 {
		return 500
	}
	return value
}

func newOntologyInitializationRunID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate ontology initialization run id: %w", err)
	}
	return "ontology-initialize:" + hex.EncodeToString(value[:]), nil
}
