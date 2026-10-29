package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"slices"
	"time"

	"orrery_v2/internal/app/reliability"
)

const postgresNoticePrefix = "dependency-group:postgres:"

func postgresNoticeID(i reliability.FailureIncident) string {
	target := reliability.PostgresFailureTarget(i)
	if target == "" {
		return ""
	}
	u, err := url.Parse(target)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256([]byte(i.TenantID + "\x00" + u.Host))
	return postgresNoticePrefix + hex.EncodeToString(digest[:16])
}

// Group only the notification view. Component incidents, diagnostics, repair
// histories, access checks and Ontology identities retain their original IDs.
func (s productionPersonalAssistantSystemSource) postgresGroup(ctx context.Context, tenant, id string, diagnostics bool) (personalAssistantSystemUpdate, bool, error) {
	u := personalAssistantSystemUpdate{ID: id, TenantID: tenant, Kind: "failure", Category: "system_error", DiagnosticsHidden: !diagnostics}
	rows, err := s.core.Query(ctx, `SELECT id FROM reliability_failure_incidents WHERE tenant_id=? AND source='server_log' AND component IN ('queue_claim','execution_runner','outbox_relay_run','runtime_hil_reconciliation_failed') ORDER BY last_seen_at DESC LIMIT 100`, tenant)
	if err != nil {
		return u, false, err
	}
	var ids []string
	for rows.Next() {
		var member string
		if err := rows.Scan(&member); err != nil {
			rows.Close()
			return u, false, err
		}
		ids = append(ids, member)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return u, false, err
	}
	repo := reliability.NewPlatformRepository(s.core)
	var members []reliability.FailureIncident
	var latest time.Time
	for _, member := range ids {
		i, err := repo.GetIncident(ctx, tenant, member)
		if err != nil {
			return u, false, err
		}
		if postgresNoticeID(i) != id {
			continue
		}
		if latest.IsZero() {
			latest = i.LastSeenAt
		}
		// Separate outages must not inherit another outage's unresolved members.
		if latest.Sub(i.LastSeenAt) > 2*time.Minute {
			continue
		}
		members = append(members, i)
	}
	if len(members) == 0 {
		return u, false, nil
	}
	resolved, verified := true, true
	var components []string
	var memberFacts []map[string]any
	var count int64
	for _, i := range members {
		resolved = resolved && i.Status == reliability.IncidentStatusResolved
		if !slices.Contains(components, i.Component) {
			components = append(components, i.Component)
		}
		if i.OccurrenceCount > count {
			count = i.OccurrenceCount
		}
		if i.UpdatedAt.After(u.At) {
			u.At = i.UpdatedAt
			u.Revision = i.UpdatedAt.UnixNano()
		}
		fact := map[string]any{"incident_id": i.ID, "component": i.Component, "status": i.Status, "cumulative_occurrences": i.OccurrenceCount}
		if diagnostics {
			p, err := repo.DependencyRecovery(ctx, i)
			if err != nil {
				return u, false, err
			}
			verified = verified && p != nil && p.Current(i)
			if p != nil {
				fact["recovery_verified_at"] = p.VerifiedAt
				fact["recovery_evidence_id"] = p.Key()
			}
		}
		memberFacts = append(memberFacts, fact)
	}
	slices.Sort(components)
	status := "unavailable"
	if resolved {
		status = "resolved"
	}
	u.State = "postgres:" + status
	u.Facts = map[string]any{"status": status, "dependency": "postgres", "components": components, "members": memberFacts, "occurrences": count, "occurrence_count_basis": "maximum cumulative component count; not a sum or count of this outage", "last_failure_at": latest, "summary": "Multiple infrastructure operations affected by the same PostgreSQL instance", "business_acceptance": "not_established_by_dependency_recovery"}
	if diagnostics {
		u.Facts["host_verified"] = resolved && verified
	}
	return u, true, nil
}
