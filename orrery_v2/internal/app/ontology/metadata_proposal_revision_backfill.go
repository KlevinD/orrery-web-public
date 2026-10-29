package ontology

import (
	"context"
	"fmt"
	"strings"

	"orrery_v2/internal/platform/db"
)

type MetadataProposalRevisionBackfillOptions struct {
	Apply           bool
	Limit           int
	AfterProposalID string
}

type MetadataProposalRevisionBackfillIssue struct {
	ProposalID string `json:"proposal_id"`
	Reason     string `json:"reason"`
}

type MetadataProposalRevisionBackfillReport struct {
	DryRun              bool                                    `json:"dry_run"`
	Inspected           int                                     `json:"inspected"`
	Resolvable          int                                     `json:"resolvable"`
	Updated             int                                     `json:"updated"`
	Unresolved          int                                     `json:"unresolved"`
	Issues              []MetadataProposalRevisionBackfillIssue `json:"issues,omitempty"`
	NextAfterProposalID string                                  `json:"next_after_proposal_id,omitempty"`
	HasMore             bool                                    `json:"has_more"`
}

func BackfillMetadataProposalRevisions(ctx context.Context, store *db.Store, opts MetadataProposalRevisionBackfillOptions) (MetadataProposalRevisionBackfillReport, error) {
	report := MetadataProposalRevisionBackfillReport{DryRun: !opts.Apply}
	if store == nil {
		return report, fmt.Errorf("%w: store is required", ErrInvalidDefinition)
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := store.Query(ctx, metadataProposalSelectSQL()+`
 WHERE id > ? AND (proposal_revision <= 0 OR proposal_content_hash = '')
 ORDER BY id ASC LIMIT ?`, strings.TrimSpace(opts.AfterProposalID), limit+1)
	if err != nil {
		return report, fmt.Errorf("list legacy ontology metadata proposals: %w", err)
	}
	defer rows.Close()

	proposals := make([]OntologyMetadataProposal, 0, limit+1)
	for rows.Next() {
		proposal, err := scanMetadataProposal(rows)
		if err != nil {
			return report, fmt.Errorf("scan legacy ontology metadata proposal: %w", err)
		}
		proposals = append(proposals, proposal)
	}
	if err := rows.Err(); err != nil {
		return report, err
	}
	if len(proposals) > limit {
		report.HasMore = true
		proposals = proposals[:limit]
	}

	for _, proposal := range proposals {
		report.Inspected++
		report.NextAfterProposalID = proposal.ID
		if err := validateMetadataProposalContentForBackfill(proposal); err != nil {
			report.Unresolved++
			report.Issues = append(report.Issues, MetadataProposalRevisionBackfillIssue{ProposalID: proposal.ID, Reason: err.Error()})
			continue
		}
		contentHash := CanonicalMetadataProposalContentHash(proposal)
		if contentHash == "" {
			report.Unresolved++
			report.Issues = append(report.Issues, MetadataProposalRevisionBackfillIssue{ProposalID: proposal.ID, Reason: "canonical content hash is unavailable"})
			continue
		}
		report.Resolvable++
		if !opts.Apply {
			continue
		}
		result, err := store.Exec(ctx, `
UPDATE ontology_metadata_proposals
SET proposal_revision = CASE WHEN proposal_revision <= 0 THEN 1 ELSE proposal_revision END,
	proposal_content_hash = ?
WHERE id = ? AND (proposal_revision <= 0 OR proposal_content_hash = '')`, contentHash, proposal.ID)
		if err != nil {
			return report, fmt.Errorf("backfill ontology metadata proposal %s: %w", proposal.ID, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return report, err
		}
		if affected > 0 {
			report.Updated++
		}
	}
	return report, nil
}

func validateMetadataProposalContentForBackfill(proposal OntologyMetadataProposal) error {
	if strings.TrimSpace(proposal.ID) == "" ||
		normalizeID(proposal.TargetKind) == "" ||
		normalizeID(proposal.TargetID) == "" ||
		normalizeID(proposal.MetadataKey) == "" ||
		strings.TrimSpace(proposal.ProposedValue) == "" ||
		strings.TrimSpace(proposal.Source) == "" {
		return fmt.Errorf("required canonical proposal content is incomplete")
	}
	return nil
}
