package main

import (
	"context"
	"strings"

	"orrery_v2/internal/app/improvement"
	"orrery_v2/internal/app/reliability"
	"orrery_v2/internal/platform/logging"
)

// ObserveAutomaticRepair runs after the reliability service has accepted the
// current receipt and projected its status. The normal capture sweep retries
// this handoff if persistence fails; it never dispatches another repair.
func (s *productionAgentExecutionOntologySink) ObserveAutomaticRepair(ctx context.Context, r reliability.AutomaticRepairReceipt) error {
	if s == nil || s.improvements == nil || r.RepairRunID == "" || r.BatchID == "" {
		// Old receipts without a real execution identity remain historical.
		return nil
	}
	feedback := improvement.ManagedRepairFeedback{
		TenantID: r.TenantID, IncidentID: r.IncidentID, OccurrenceCount: r.OccurrenceCount, LastSeenNS: r.LastSeenNS,
		JobID: r.JobID, BatchID: r.BatchID, RepairRunID: r.RepairRunID, Status: r.Status, Verified: r.Verified,
		OccurredAt: r.UpdatedAt, SourceAccessRef: trustedOntologySourceRef("failure_incident", r.IncidentID, r.TenantID),
		Summary: strings.TrimSpace(logging.RedactDiagnostic(r.Summary, 4096)),
	}
	for _, ref := range r.Evidence {
		if ref = strings.TrimSpace(logging.RedactDiagnostic(ref, 512)); ref != "" {
			feedback.SourceEvidence = append(feedback.SourceEvidence, ref)
		}
	}
	_, err := s.improvements.ObserveManagedRepair(ctx, feedback)
	return err
}

var _ reliability.AutomaticRepairFeedbackSink = (*productionAgentExecutionOntologySink)(nil)
