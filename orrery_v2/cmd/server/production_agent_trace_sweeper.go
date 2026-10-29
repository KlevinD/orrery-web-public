package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/app/agenttrace"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/missioncontrol"
	missionprojection "orrery_v2/internal/mission/projection"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/filestore"
	"orrery_v2/internal/platform/logging"
)

type productionAgentTraceActivity struct {
	discussions *discussionsession.Repository
	missions    *missioncontrol.DBProjectionRepository
}

func (s productionAgentTraceActivity) RunActive(ctx context.Context, tenantID, runID string) (bool, error) {
	if s.discussions != nil {
		run, err := s.discussions.GetRun(ctx, runID)
		if err == nil {
			return strings.EqualFold(strings.TrimSpace(run.Status), "running"), nil
		}
		if !errors.Is(err, discussionsession.ErrNotFound) {
			return false, err
		}
	}
	if s.missions == nil {
		return false, nil
	}
	after := ""
	for {
		models, more, err := s.missions.ListMissionCatalog(ctx, after, 100)
		if err != nil {
			return false, err
		}
		for _, model := range models {
			for _, session := range model.RuntimeSessions {
				if session.RunID.String() == runID && (session.Status == missionprojection.RuntimeSessionQueued || session.Status == missionprojection.RuntimeSessionRunning) {
					return true, nil
				}
			}
			if model.Mission.MissionID.String() != "" {
				after = firstNonBlank(model.TenantID, tenantID) + "/" + model.Mission.MissionID.String()
			}
		}
		if !more || len(models) == 0 {
			return false, nil
		}
	}
}

type productionAgentTraceSweeper struct {
	logger        logging.Logger
	mu            sync.Mutex
	retention     *agenttrace.RetentionService
	reconciler    *agenttrace.Reconciler
	now           func() time.Time
	lastRetention time.Time
	lastReconcile time.Time
}

func (s *productionAgentTraceSweeper) Sweep(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	var errs []error
	if s.lastReconcile.IsZero() || now.Sub(s.lastReconcile) >= time.Minute {
		_, err := s.reconciler.Run(ctx, 100)
		errs = append(errs, err)
		s.lastReconcile = now
	}
	if s.lastRetention.IsZero() || now.Sub(s.lastRetention) >= time.Hour {
		report, err := s.retention.Run(ctx, 100)
		errs = append(errs, err)
		s.lastRetention = now
		if report.Scanned == 100 && report.Archived+report.Missing+report.Purged > 0 {
			s.lastRetention = now.Add(-time.Hour + time.Minute)
		}
		if s.logger != nil && report.Scanned > 0 {
			s.logger.Info(context.WithoutCancel(ctx), "agent trace retention batch completed",
				logging.Int("scanned", report.Scanned), logging.Int("archived", report.Archived),
				logging.Int("missing", report.Missing), logging.Int("failed", report.Failed),
				logging.Int("purged", report.Purged))
		}
	}
	return errors.Join(errs...)
}

func registerProductionAgentTraceSweeper(rt *platform.Runtime, core *db.Store, discussions *discussionsession.Repository, missions *missioncontrol.DBProjectionRepository) error {
	if rt == nil || core == nil || rt.FileStore == nil {
		return nil
	}
	now := rt.Clock.Now
	repository := agenttrace.NewPlatformStore(core)
	objects := agenttrace.NewFilestoreObjects(rt.FileStore)
	return rt.RegisterSweeper("agent.runtime_trace", &productionAgentTraceSweeper{
		logger:    rt.Logger,
		retention: agenttrace.NewRetentionService(repository, objects, now),
		reconciler: agenttrace.NewReconciler(repository, objects, productionAgentTraceActivity{
			discussions: discussions, missions: missions,
		}, now),
		now: now,
	})
}

func newProductionAgentTraceService(core *db.Store, store filestore.Store) *agenttrace.Service {
	if core == nil || store == nil {
		return nil
	}
	return agenttrace.NewService(agenttrace.NewPlatformStore(core), agenttrace.NewFilestoreObjects(store))
}
