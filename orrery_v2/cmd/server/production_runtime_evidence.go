package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/app/improvement"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/logging"
)

func productionRuntimeEvidenceRecorder(recorder agentcore.TraceRecorder, store agentcore.TraceStore) agentcore.TraceRecorder {
	provider, ok := store.(interface {
		RuntimeEvidenceStore() *improvement.RuntimeEvidenceStore
	})
	if !ok {
		return recorder
	}
	evidence := provider.RuntimeEvidenceStore()
	if evidence == nil {
		return recorder
	}
	var journal improvement.RuntimeFeedbackJournal
	if provider, ok := store.(interface {
		RuntimeFeedbackJournal() improvement.RuntimeFeedbackJournal
	}); ok {
		journal = provider.RuntimeFeedbackJournal()
	}
	return improvement.RuntimeEvidenceRecorder{Recorder: recorder, Evidence: evidence, Journal: journal, Scope: productionRuntimeEvidenceScope, Implementation: productionRuntimeImplementation()}
}

func (s productionTraceStore) RuntimeFeedbackJournal() improvement.RuntimeFeedbackJournal {
	return s.evidenceJournal
}

func (s productionTraceStore) RuntimeEvidenceStore() *improvement.RuntimeEvidenceStore {
	if s.evidenceCore == nil {
		return nil
	}
	return improvement.NewRuntimeEvidenceStore(s.evidenceCore)
}

func newProductionRuntimeFeedbackJournal(baseDir string, disabled bool) (improvement.RuntimeFeedbackJournal, error) {
	if disabled {
		return nil, nil
	}
	root, err := filepath.Abs(filepath.Join(baseDir, "data", "rsi-runtime-feedback"))
	if err != nil {
		return nil, err
	}
	return improvement.NewFileRuntimeFeedbackJournal(root)
}

type productionRuntimeFeedbackRecovery struct {
	store   *improvement.RuntimeEvidenceStore
	journal improvement.RuntimeFeedbackJournal
	limit   int
	logger  logging.Logger
}

func (s *productionRuntimeFeedbackRecovery) Sweep(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := s.store.RecoverFeedback(ctx, s.journal, s.limit)
	if s.logger != nil && result.Recovered > 0 {
		s.logger.Info(ctx, "runtime improvement feedback recovered", logging.Int("recovered", result.Recovered), logging.Int("retained", result.Retained))
	}
	return err
}

func (s *productionRuntimeFeedbackRecovery) Close() error {
	if closer, ok := s.journal.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

func registerProductionRuntimeFeedbackRecovery(rt *platform.Runtime, core *db.Store, journal improvement.RuntimeFeedbackJournal) error {
	if journal == nil {
		return nil // No startup writes or recovery work in disabled-background mode.
	}
	if rt == nil || core == nil {
		return improvement.ErrUnavailable
	}
	limit := rt.Config.Sweeper.BatchSize
	if limit < 1 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	return rt.RegisterSweeper("ontology.runtime_feedback_recovery", &productionRuntimeFeedbackRecovery{store: improvement.NewRuntimeEvidenceStore(core), journal: journal, limit: limit, logger: rt.Logger})
}

func productionRuntimeEvidenceScope(req agentcore.RuntimeRequest) (improvement.RuntimeEvidenceScope, error) {
	tenant := strings.TrimSpace(req.TenantID)
	if tenant == "" || tenant != req.TenantID {
		return improvement.RuntimeEvidenceScope{}, fmt.Errorf("%w: runtime tenant required", improvement.ErrInvalid)
	}
	mission := strings.TrimSpace(req.MissionID)
	if strings.TrimSpace(req.RuntimeOptions.ProviderSpecific["scheduled_task_id"]) == "" && mission != "" {
		_, scopedMission, scoped := canonicalMissionTenantSource(tenant)
		if scoped && scopedMission != mission {
			return improvement.RuntimeEvidenceScope{}, fmt.Errorf("%w: mission scope mismatch", improvement.ErrInvalid)
		}
		if !scoped {
			tenant = missionScopeTenantID(tenant, mission)
		}
	}
	run := runtimedaemon.AgentRun{RunID: req.RunID, Request: req, TaskID: req.TaskID}
	return improvement.RuntimeEvidenceScope{
		TenantID: tenant,
		Task:     improvement.ObjectRef{Kind: "agent_task", ID: productionAgentTaskOntologyID(run)},
		Run:      improvement.ObjectRef{Kind: "agent_run", ID: ontologyAgentRunID(req.RunID.String())},
		Source:   trustedOntologySourceRef("agent_run", req.RunID.String(), tenant),
	}, nil
}
