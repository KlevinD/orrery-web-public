package reliability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"orrery_v2/internal/platform/logging"
)

// AutomaticRepairReceipt is written atomically by the host's managed repair
// worker after reading its durable queue and independently validating results.
// Browser diagnostic input cannot create or update these receipts.
type AutomaticRepairReceipt struct {
	Version         int       `json:"version"`
	TenantID        string    `json:"tenant_id"`
	IncidentID      string    `json:"incident_id"`
	OccurrenceCount int64     `json:"occurrence_count"`
	LastSeenNS      int64     `json:"last_seen_ns"`
	JobID           string    `json:"job_id"`
	BatchID         string    `json:"batch_id"`
	Status          string    `json:"status"`
	Summary         string    `json:"summary"`
	Evidence        []string  `json:"evidence"`
	RepairRunID     string    `json:"repair_run_id"`
	UpdatedAt       time.Time `json:"updated_at"`
	Verified        bool      `json:"verified"`
}

// AutomaticRepairFeedbackSink receives accepted, current repair results. The
// capture sweep retries a failed handoff without repeating the repair itself.
type AutomaticRepairFeedbackSink interface {
	ObserveAutomaticRepair(context.Context, AutomaticRepairReceipt) error
}

func (r AutomaticRepairReceipt) Current(i FailureIncident) bool {
	return r.TenantID == i.TenantID && r.IncidentID == i.ID && r.OccurrenceCount == i.OccurrenceCount && r.LastSeenNS == i.LastSeenAt.UnixNano()
}

// CurrentResult additionally fences superseded progress. Independent verified
// recovery must not be presented as an old running or needs-attention verdict.
func (r AutomaticRepairReceipt) CurrentResult(i FailureIncident) bool {
	return r.Current(i) && (i.Status != IncidentStatusResolved || r.Status == "resolved")
}

func (q *CaptureInbox) AutomaticRepair(tenant, incident string) (*AutomaticRepairReceipt, error) {
	path := filepath.Join(q.dir, "repairs", captureKey(tenant, incident))
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 64*1024 {
		return nil, fmt.Errorf("invalid automatic repair receipt")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r AutomaticRepairReceipt
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	if r.Version != 1 || r.TenantID != tenant || r.IncidentID != incident || r.OccurrenceCount < 1 || r.LastSeenNS < 1 || r.JobID == "" || r.UpdatedAt.IsZero() || len(r.Evidence) > 20 {
		return nil, fmt.Errorf("automatic repair receipt binding is invalid")
	}
	switch r.Status {
	case "pending", "running", "retry_wait", "needs_attention", "technical_recovered", "resolved":
	default:
		return nil, fmt.Errorf("invalid automatic repair status")
	}
	r.Summary = logging.RedactDiagnostic(r.Summary, 4096)
	r.JobID = logging.RedactDiagnostic(r.JobID, 160)
	r.RepairRunID = logging.RedactDiagnostic(r.RepairRunID, 160)
	for i := range r.Evidence {
		r.Evidence[i] = logging.RedactDiagnostic(r.Evidence[i], 512)
	}
	return &r, nil
}

func (q *CaptureInbox) reconcileAutomaticRepairs(ctx context.Context) error {
	sink, ok := q.observer.(interface {
		ApplyAutomaticRepairReceipt(context.Context, AutomaticRepairReceipt) error
	})
	if !ok {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(q.dir, "repairs"))
	if err != nil {
		return err
	}
	var failures []error
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		if !entry.Type().IsRegular() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(q.dir, "repairs", entry.Name())
		info, statErr := entry.Info()
		if statErr != nil || info.Size() > 64*1024 {
			failures = append(failures, fmt.Errorf("invalid automatic repair receipt file"))
			continue
		}
		data, readErr := os.ReadFile(path)
		var binding AutomaticRepairReceipt
		if readErr != nil || json.Unmarshal(data, &binding) != nil || entry.Name() != captureKey(binding.TenantID, binding.IncidentID) {
			failures = append(failures, fmt.Errorf("invalid automatic repair receipt identity"))
			continue
		}
		receipt, loadErr := q.AutomaticRepair(binding.TenantID, binding.IncidentID)
		if loadErr == nil && receipt != nil {
			loadErr = sink.ApplyAutomaticRepairReceipt(logging.WithoutObservation(ctx), *receipt)
		}
		if loadErr != nil {
			failures = append(failures, loadErr)
		}
	}
	return errors.Join(failures...)
}

func (s *Service) ApplyAutomaticRepairReceipt(ctx context.Context, r AutomaticRepairReceipt) error {
	target := IncidentStatus("")
	switch r.Status {
	case "running":
		target = IncidentStatusRepairing
	case "technical_recovered":
		target = IncidentStatusMonitoring
	case "needs_attention":
		target = IncidentStatusEscalated
	case "resolved":
		if !r.Verified || r.RepairRunID == "" || len(r.Evidence) == 0 {
			return fmt.Errorf("automatic repair resolution lacks verified execution evidence")
		}
		target = IncidentStatusResolved
	default:
		return nil
	}
	actor := ActorRef{Kind: "service", ID: "managed-failure-repair"}
	for step := 0; step < 4; step++ {
		incident, err := s.repository.GetIncident(ctx, r.TenantID, r.IncidentID)
		if err != nil {
			return err
		}
		if !r.Current(incident) || incident.Status == IncidentStatusQuarantined || incident.Status == IncidentStatusResolved && target != IncidentStatusResolved {
			return nil
		}
		next := target
		if !validIncidentTransition(incident.Status, next) && incident.Status != next {
			switch incident.Status {
			case IncidentStatusOpen:
				next = IncidentStatusContained
			case IncidentStatusDiagnosed:
				next = IncidentStatusRepairing
			case IncidentStatusRepairing:
				next = IncidentStatusMonitoring
			default:
				return fmt.Errorf("automatic repair cannot transition incident from %s", incident.Status)
			}
		}
		changed := incident.Status != next
		if changed {
			incident, err = s.repository.UpdateIncidentStatus(ctx, incident.TenantID, incident.ID, incident.Version, next, s.now().UTC())
			if err != nil {
				return err
			}
		}
		if err := s.publishEvent(ctx, failureStatusEvent(incident, actor), changed); err != nil {
			return err
		}
		if next == target {
			if feedback, ok := s.sink.(AutomaticRepairFeedbackSink); ok && r.Status != "running" {
				return feedback.ObserveAutomaticRepair(ctx, r)
			}
			return nil
		}
	}
	return fmt.Errorf("automatic repair transition exceeded step limit")
}
