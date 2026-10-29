package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/app/reliability"
	knowledgecore "orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/provider"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/execution"
	"orrery_v2/internal/platform/health"
	"orrery_v2/internal/platform/logging"
)

type dependencyRecoveryPass struct {
	first   time.Time
	last    time.Time
	targets []string
}

// This bounded infrastructure check uses the existing maintenance loop. It does
// not create Agent tasks, replay business work, or infer delivery from DB health.
type productionDependencyRecovery struct {
	core           *db.Store
	control        *reliability.Service
	inbox          *reliability.CaptureInbox
	now            func() time.Time
	postgres       map[string]func(context.Context) error
	folders        func(context.Context, string) error
	capacity       func(string) (health.StorageCapacity, error)
	paths          []string
	mu             sync.Mutex
	lastRun        time.Time
	passes         map[string]dependencyRecoveryPass
	capacityLevels map[string]string
}

func registerProductionFailureRecovery(rt *platform.Runtime, control *reliability.Service, api *productionOntologyReliabilityAPI, registry dynamicKnowledgeProviderRegistry, disabled bool) (*productionClientDiagnostics, error) {
	diagnostics, err := registerProductionFailureCapture(rt, control, disabled)
	if err != nil || diagnostics == nil {
		return diagnostics, err
	}
	api.capture = diagnostics.inbox
	if err := registerProductionDependencyRecovery(rt, control, diagnostics.inbox, registry); err != nil {
		return nil, err
	}
	return diagnostics, nil
}

func registerProductionDependencyRecovery(rt *platform.Runtime, control *reliability.Service, inbox *reliability.CaptureInbox, registry dynamicKnowledgeProviderRegistry) error {
	core, err := rt.Databases.Store(db.RoleCore)
	if err != nil {
		return err
	}
	s := &productionDependencyRecovery{core: core, control: control, inbox: inbox, now: rt.Clock.Now, postgres: map[string]func(context.Context) error{}, capacity: health.ReadStorageCapacity, paths: []string{rt.Config.FileStore.Root, filepath.Dir(rt.Config.Logging.File)}, passes: map[string]dependencyRecoveryPass{}, capacityLevels: map[string]string{}}
	for role, raw := range rt.Config.DatabaseURLs {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
			continue
		}
		store, err := rt.Databases.Store(role)
		if err != nil {
			continue
		}
		port := u.Port()
		if port == "" {
			port = "5432"
		}
		target := "postgres://" + net.JoinHostPort(u.Hostname(), port) + u.Path
		s.postgres[target] = store.CheckWritable
	}
	s.folders = func(ctx context.Context, folder string) error {
		p, ok := registry.Provider(knowledgecore.ProviderPageIndex).(provider.FolderDocumentLister)
		if !ok {
			return errors.New("PageIndex folder query unavailable")
		}
		_, err := p.ListFolderDocuments(ctx, provider.FolderDocumentsRequest{FolderID: folder})
		return err
	}
	return rt.RegisterSweeper("reliability.dependency_recovery", execution.NewBackgroundSweeper("reliability.dependency_recovery", s, 25*time.Second, rt.Logger))
}

func (s *productionDependencyRecovery) Sweep(ctx context.Context) error {
	if !s.mu.TryLock() {
		return nil
	}
	defer s.mu.Unlock()
	now := s.now().UTC()
	if !s.lastRun.IsZero() && now.Sub(s.lastRun) < 30*time.Second {
		return nil
	}
	s.lastRun = now
	// Collect capacity before attempting DB access, so a full database does not
	// prevent the host inbox from retaining the actual available/free distinction.
	s.sampleCapacity(ctx)
	rows, err := s.core.Query(ctx, `SELECT tenant_id,id FROM reliability_failure_incidents WHERE source='server_log' AND component IN ('queue_claim','execution_runner','outbox_relay_run','runtime_hil_reconciliation_failed','knowledge_catalog_reconcile') AND status<>'quarantined' AND (status<>'resolved' OR updated_at>?) AND last_seen_at>=? AND last_seen_at<=? ORDER BY last_seen_at DESC LIMIT 40`, now.Add(-2*time.Minute).UnixNano(), now.Add(-24*time.Hour).UnixNano(), now.Add(-2*time.Minute).UnixNano())
	if err != nil {
		clear(s.passes)
		return err
	}
	var ids [][2]string
	for rows.Next() {
		var id [2]string
		if err := rows.Scan(&id[0], &id[1]); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	repo := reliability.NewPlatformRepository(s.core)
	active := map[string]bool{}
	checked := map[string]error{}
	var failures []error
	for _, id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		i, err := repo.GetIncident(ctx, id[0], id[1])
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if s.inbox != nil {
			pending, err := s.inbox.HasPendingRecurrence(i)
			if err != nil {
				failures = append(failures, err)
			}
			if err != nil || pending {
				delete(s.passes, (reliability.DependencyRecovery{TenantID: i.TenantID, IncidentID: i.ID, OccurrenceCount: i.OccurrenceCount, LastSeenNS: i.LastSeenAt.UnixNano()}).Key())
				continue
			}
		}
		if i.Status == reliability.IncidentStatusResolved {
			// Retry an interrupted event projection without changing the proof or
			// rerunning an Agent repair; canonical status and event stay idempotent.
			p, err := repo.DependencyRecovery(ctx, i)
			if err == nil && p != nil && now.Sub(p.VerifiedAt) < 2*time.Minute {
				err = s.control.ApplyDependencyRecovery(ctx, *p)
			}
			if err != nil {
				failures = append(failures, err)
			}
			continue
		}
		p := reliability.DependencyRecovery{TenantID: i.TenantID, IncidentID: i.ID, OccurrenceCount: i.OccurrenceCount, LastSeenNS: i.LastSeenAt.UnixNano(), VerifiedAt: now}
		if target := reliability.PostgresFailureTarget(i); target != "" && s.postgres[target] != nil {
			p.Dependency, p.Targets = "postgres", []string{target}
		} else if i.Component == "knowledge_catalog_reconcile" && s.inbox != nil {
			diagnostics, err := s.inbox.Diagnostics(i.TenantID, i.ID)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			// A summary may omit a second failed folder. Only the complete, latest
			// captured cause is sufficient; never guess from the first URL alone.
			if len(diagnostics) == 0 || !diagnostics[0].Observation.OccurredAt.Equal(i.LastSeenAt) {
				continue
			}
			cause := diagnostics[0].Diagnostics["cause"]
			if len(cause) >= 4000 || strings.Contains(cause, "context canceled") {
				continue
			}
			p.Dependency, p.Targets = "pageindex", reliability.PageIndexFailureFolders(i, cause)
		}
		if len(p.Targets) == 0 || len(p.Targets) > 16 {
			continue
		}
		key := p.Key()
		active[key] = true
		passed := true
		for _, target := range p.Targets {
			checkKey := p.Dependency + ":" + target
			checkErr, found := checked[checkKey]
			if !found {
				probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				if p.Dependency == "postgres" {
					checkErr = s.postgres[target](probeCtx)
				} else if s.folders != nil {
					checkErr = s.folders(probeCtx, target)
				} else {
					checkErr = errors.New("folder probe unavailable")
				}
				cancel()
				checked[checkKey] = checkErr
			}
			if checkErr != nil {
				passed = false
				break
			}
		}
		if !passed {
			delete(s.passes, key)
			continue
		}
		prior, exists := s.passes[key]
		if !exists || now.Sub(prior.last) > 90*time.Second || !slices.Equal(prior.targets, p.Targets) {
			s.passes[key] = dependencyRecoveryPass{first: now, last: now, targets: slices.Clone(p.Targets)}
			continue
		}
		prior.last = now
		s.passes[key] = prior
		if now.Sub(prior.first) < 30*time.Second {
			continue
		}
		p.FirstPassedAt = prior.first
		if err := s.control.ApplyDependencyRecovery(ctx, p); err != nil {
			failures = append(failures, err)
		} else {
			delete(s.passes, key)
		}
	}
	for key := range s.passes {
		if !active[key] {
			delete(s.passes, key)
		}
	}
	return errors.Join(failures...)
}

func (s *productionDependencyRecovery) sampleCapacity(ctx context.Context) {
	if s.capacity == nil || s.inbox == nil {
		return
	}
	for _, path := range s.paths {
		if strings.TrimSpace(path) == "" || path == "." {
			continue
		}
		capacity, err := s.capacity(path)
		if err != nil {
			continue
		}
		data, err := json.Marshal(capacity)
		if err != nil {
			continue
		}
		// A current, timestamped operational snapshot does not replace the
		// historical snapshot captured with an ENOSPC error.
		snapshotCtx, cancel := context.WithTimeout(ctx, time.Second)
		_ = s.core.PutRecord(snapshotCtx, "platform_storage_capacity", capacity.Path, db.Record{"payload": string(data)})
		cancel()
		level := "ok"
		severity := reliability.FailureSeverityWarning
		if capacity.Low() {
			level = "low"
		}
		if capacity.Critical() {
			level = "critical"
			severity = reliability.FailureSeverityError
		}
		if level == "ok" {
			s.capacityLevels[path] = level
			continue
		}
		if s.capacityLevels[path] == level {
			continue
		}
		err = s.inbox.Enqueue(ctx, reliability.CapturedFailure{Observation: reliability.FailureObservation{ID: reliability.NewCaptureID(), TenantID: "global", Source: "server_log", Component: "storage_capacity", ErrorCode: "storage_capacity_" + level, Category: reliability.FailureCategoryConfiguration, Severity: severity, Message: fmt.Sprintf("storage capacity %s: path=%s available_bytes=%d all_free_bytes=%d reserved_unavailable_bytes=%d sampled_at=%s; available is service-account space, low is a warning and does not mean the disk is full", level, capacity.Path, capacity.AvailableBytes, capacity.FreeBytes, capacity.ReservedBytes, capacity.SampledAt.Format(time.RFC3339Nano)), Actor: reliability.ActorRef{Kind: "service", ID: "storage-capacity-monitor"}}, Diagnostics: map[string]string{"storage_capacity": string(data)}})
		if err == nil {
			s.capacityLevels[path] = level
		}
	}
}

func failureCapacityDiagnostics(path, cause string) string {
	lower := strings.ToLower(cause)
	if !strings.Contains(lower, "no space left") && !strings.Contains(lower, "disk i/o error") {
		return ""
	}
	s, err := health.ReadStorageCapacity(path)
	if err != nil {
		return ""
	}
	data, err := json.Marshal(s)
	if err != nil {
		return ""
	}
	return logging.RedactDiagnostic(string(data), 4096)
}
