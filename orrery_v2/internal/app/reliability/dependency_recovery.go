package reliability

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

// DependencyRecovery is infrastructure verification, not an Agent repair verdict
// or an assertion that the interrupted business operation completed.
type DependencyRecovery struct {
	TenantID        string    `json:"tenant_id"`
	IncidentID      string    `json:"incident_id"`
	OccurrenceCount int64     `json:"occurrence_count"`
	LastSeenNS      int64     `json:"last_seen_ns"`
	Dependency      string    `json:"dependency"`
	Targets         []string  `json:"targets"`
	FirstPassedAt   time.Time `json:"first_passed_at"`
	VerifiedAt      time.Time `json:"verified_at"`
}

var postgresTargetPattern = regexp.MustCompile(`database=([a-zA-Z0-9_]+)[^a-zA-Z0-9_\s]*\s+([a-zA-Z0-9.\[\]:-]+):([0-9]+)`)
var folderFailurePattern = regexp.MustCompile(`/folder/(folder_[a-zA-Z0-9_]+)/documents`)

// PostgresFailureTarget deliberately excludes provider send rejections, business
// failures, and arbitrary HTTP errors from database recovery and aggregation.
func PostgresFailureTarget(i FailureIncident) string {
	if i.Source != "server_log" {
		return ""
	}
	switch i.Component {
	case "queue_claim", "execution_runner", "outbox_relay_run", "runtime_hil_reconciliation_failed":
	default:
		return ""
	}
	m := strings.ToLower(i.RedactedSummary)
	if !strings.Contains(m, "failed to connect") || !(strings.Contains(m, "not yet accepting connections") || strings.Contains(m, "starting up") || strings.Contains(m, "recovery mode") || strings.Contains(m, "connection refused") || strings.Contains(m, "57p03")) {
		return ""
	}
	p := postgresTargetPattern.FindStringSubmatch(i.RedactedSummary)
	if len(p) != 4 {
		return ""
	}
	return "postgres://" + p[2] + ":" + p[3] + "/" + p[1]
}

func PageIndexFailureFolders(i FailureIncident, cause string) []string {
	if i.Source != "server_log" || i.Component != "knowledge_catalog_reconcile" || !strings.Contains(cause, "returned 500") {
		return nil
	}
	var folders []string
	for _, m := range folderFailurePattern.FindAllStringSubmatch(cause, -1) {
		if !slices.Contains(folders, m[1]) {
			folders = append(folders, m[1])
		}
	}
	slices.Sort(folders)
	return folders
}

func (p DependencyRecovery) Current(i FailureIncident) bool {
	return p.TenantID == i.TenantID && p.IncidentID == i.ID && p.OccurrenceCount == i.OccurrenceCount && p.LastSeenNS == i.LastSeenAt.UnixNano()
}

func (p DependencyRecovery) Key() string {
	return stableID("dependency-recovery", p.TenantID, p.IncidentID, fmt.Sprint(p.OccurrenceCount), fmt.Sprint(p.LastSeenNS))
}

func (p DependencyRecovery) validate(i FailureIncident, now time.Time) error {
	validScope := false
	switch p.Dependency {
	case "postgres":
		target := PostgresFailureTarget(i)
		validScope = target != "" && len(p.Targets) == 1 && p.Targets[0] == target
	case "pageindex":
		folders := PageIndexFailureFolders(i, i.RedactedSummary)
		validScope = len(folders) > 0 && len(p.Targets) >= len(folders) && len(p.Targets) <= 16
		for _, folder := range folders {
			validScope = validScope && slices.Contains(p.Targets, folder)
		}
		for _, target := range p.Targets {
			validScope = validScope && regexp.MustCompile(`^folder_[a-zA-Z0-9_]+$`).MatchString(target)
		}
	}
	if !p.Current(i) || !validScope || p.FirstPassedAt.Before(i.LastSeenAt.Add(2*time.Minute)) || p.VerifiedAt.Sub(p.FirstPassedAt) < 30*time.Second || p.VerifiedAt.After(now) || now.Sub(p.VerifiedAt) > 2*time.Minute {
		return fmt.Errorf("%w: dependency recovery lacks current, stable, scoped verification", ErrInvalidRequest)
	}
	return nil
}

type dependencyRecoveryRepository interface {
	acceptDependencyRecovery(context.Context, FailureIncident, DependencyRecovery, IncidentStatus, time.Time) (FailureIncident, error)
}

func (s *Service) ApplyDependencyRecovery(ctx context.Context, p DependencyRecovery) error {
	repo, ok := s.repository.(dependencyRecoveryRepository)
	if !ok {
		return fmt.Errorf("dependency recovery repository is unavailable")
	}
	for step := 0; step < 2; step++ {
		i, err := s.repository.GetIncident(ctx, p.TenantID, p.IncidentID)
		if err != nil {
			return err
		}
		if err := p.validate(i, s.now().UTC()); err != nil {
			return err
		}
		if i.Status == IncidentStatusQuarantined {
			return fmt.Errorf("%w: quarantined incident requires review", ErrInvalidRequest)
		}
		next := IncidentStatusResolved
		if i.Status == IncidentStatusRepairing {
			next = IncidentStatusMonitoring
		}
		if i.Status != next && !validIncidentTransition(i.Status, next) {
			return ErrInvalidRequest
		}
		changed := i.Status != next
		if changed {
			i, err = repo.acceptDependencyRecovery(ctx, i, p, next, s.now().UTC())
			if err != nil {
				return err
			}
		}
		if err := s.publishEvent(ctx, failureStatusEvent(i, ActorRef{Kind: "service", ID: "dependency-recovery-verifier"}), changed); err != nil {
			return err
		}
		if next == IncidentStatusResolved {
			return nil
		}
	}
	return ErrConflict
}

const dependencyRecoveryTable = "reliability_dependency_recoveries"

func (r *PlatformRepository) acceptDependencyRecovery(ctx context.Context, i FailureIncident, p DependencyRecovery, next IncidentStatus, now time.Time) (FailureIncident, error) {
	var updated FailureIncident
	err := r.store.WithTx(ctx, func(tx db.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE reliability_failure_incidents SET status=?,version=version+1,updated_at=? WHERE tenant_id=? AND id=? AND version=? AND occurrence_count=? AND last_seen_at=?`, next, now.UnixNano(), i.TenantID, i.ID, i.Version, p.OccurrenceCount, p.LastSeenNS)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrConflict
		}
		payload, err := json.Marshal(p)
		if err != nil {
			return err
		}
		if _, err := db.InsertRecordTx(ctx, tx, dependencyRecoveryTable, p.Key(), db.Record{"payload": string(payload)}); err != nil {
			return err
		}
		updated, err = scanFailureIncident(tx.QueryRowContext(ctx, failureIncidentSelectSQL()+` WHERE tenant_id=? AND id=?`, i.TenantID, i.ID))
		return err
	})
	return updated, err
}

func (r *PlatformRepository) DependencyRecovery(ctx context.Context, i FailureIncident) (*DependencyRecovery, error) {
	key := (DependencyRecovery{TenantID: i.TenantID, IncidentID: i.ID, OccurrenceCount: i.OccurrenceCount, LastSeenNS: i.LastSeenAt.UnixNano()}).Key()
	record, found, err := r.store.GetRecord(ctx, dependencyRecoveryTable, key)
	if err != nil || !found {
		return nil, err
	}
	var p DependencyRecovery
	payload, _ := record["payload"].(string)
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return nil, err
	}
	if !p.Current(i) {
		return nil, ErrConflict
	}
	return &p, nil
}
