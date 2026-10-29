package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/db"
)

type productionOntologyGovernanceBackfillResource string

const (
	productionOntologyGovernanceBackfillProposals productionOntologyGovernanceBackfillResource = "proposals"
	productionOntologyGovernanceBackfillReceipts  productionOntologyGovernanceBackfillResource = "receipts"
)

type productionOntologyGovernanceBackfillOptions struct {
	Apply     bool
	Resource  productionOntologyGovernanceBackfillResource
	Limit     int
	AfterID   string
	ChangedBy string
}

type productionOntologyGovernanceBackfillReport struct {
	Resource       productionOntologyGovernanceBackfillResource     `json:"resource"`
	ChangedBy      string                                           `json:"changed_by,omitempty"`
	ProposalReport *ontology.MetadataProposalRevisionBackfillReport `json:"proposal_report,omitempty"`
	ReceiptReport  *ontology.MetadataReceiptBindingBackfillReport   `json:"receipt_report,omitempty"`
}

func runProductionOntologyGovernanceBackfill(ctx context.Context, cfg config.Config, opts productionOntologyGovernanceBackfillOptions, out io.Writer) error {
	runtime, err := platform.OpenWithModules(ctx, cfg, productionModules()...)
	if err != nil {
		return err
	}
	defer runtime.Close()
	if err := runtime.Initialize(ctx); err != nil {
		return err
	}
	core, err := runtime.Databases.Store(db.RoleCore)
	if err != nil {
		return err
	}
	return runOntologyGovernanceBackfill(ctx, core, opts, out)
}

func runOntologyGovernanceBackfill(ctx context.Context, core *db.Store, opts productionOntologyGovernanceBackfillOptions, out io.Writer) error {
	if core == nil {
		return fmt.Errorf("ontology governance backfill core store is required")
	}
	if out == nil {
		out = io.Discard
	}
	opts.Resource = productionOntologyGovernanceBackfillResource(strings.TrimSpace(string(opts.Resource)))
	opts.ChangedBy = strings.TrimSpace(opts.ChangedBy)
	if opts.Apply && opts.ChangedBy == "" {
		return fmt.Errorf("ontology governance backfill apply requires changed-by operator identity")
	}
	report := productionOntologyGovernanceBackfillReport{Resource: opts.Resource, ChangedBy: opts.ChangedBy}
	switch opts.Resource {
	case productionOntologyGovernanceBackfillProposals:
		result, err := ontology.BackfillMetadataProposalRevisions(ctx, core, ontology.MetadataProposalRevisionBackfillOptions{
			Apply:           opts.Apply,
			Limit:           opts.Limit,
			AfterProposalID: strings.TrimSpace(opts.AfterID),
		})
		if err != nil {
			return err
		}
		report.ProposalReport = &result
	case productionOntologyGovernanceBackfillReceipts:
		result, err := ontology.BackfillMetadataReceiptProposalBindings(ctx, core, ontology.MetadataReceiptBindingBackfillOptions{
			Apply:          opts.Apply,
			Limit:          opts.Limit,
			AfterReceiptID: strings.TrimSpace(opts.AfterID),
		})
		if err != nil {
			return err
		}
		report.ReceiptReport = &result
	default:
		return fmt.Errorf("unsupported ontology governance backfill resource %q", opts.Resource)
	}
	return json.NewEncoder(out).Encode(report)
}
