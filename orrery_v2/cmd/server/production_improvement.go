package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/app/improvement"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/graphprotocol"
)

func (s *productionAgentExecutionOntologySink) observeRuntimeImprovement(ctx context.Context, event runtimedaemon.RuntimeLifecycleEvent, tenant, runID, taskID string, source graphprotocol.AccessRef) error {
	if s.improvements == nil || event.Kind == runtimedaemon.RuntimeLifecycleStarted {
		return nil
	}
	switch event.Kind {
	case runtimedaemon.RuntimeLifecycleCompleted, runtimedaemon.RuntimeLifecycleFailed, runtimedaemon.RuntimeLifecycleTimedOut, runtimedaemon.RuntimeLifecycleCancelled, runtimedaemon.RuntimeLifecycleBlocked:
	default:
		return nil
	}
	// Completion is an observation, not a claim that business quality passed.
	// Fixed summaries also avoid exposing task output or private reasoning.
	summary := "任务执行结束，等待业务质量与流程评估"
	if event.Kind != runtimedaemon.RuntimeLifecycleCompleted {
		summary = "任务执行未完成，等待任务与流程证据归因"
	}
	observation := improvement.Observation{
		TenantID: tenant,
		Task:     improvement.ObjectRef{Kind: "agent_task", ID: taskID}, Run: improvement.ObjectRef{Kind: "agent_run", ID: runID},
		Trigger: "runtime_" + string(event.Kind), Summary: summary, EvidenceIDs: []string{runID},
		SourceAccessRef: source, OccurredAt: event.CompletedAt,
	}
	if event.DurationMs > 0 {
		observation.Metrics = map[string]float64{"runtime_duration_ms": float64(event.DurationMs)}
	}
	seen := map[string]bool{runID: true}
	for _, ref := range []string{event.TraceRef, event.ContextSnapshotID} {
		if ref != "" && !seen[ref] {
			observation.EvidenceIDs = append(observation.EvidenceIDs, ref)
			seen[ref] = true
		}
	}
	// Only explicit versions are captured. Missing digests and missing bindings
	// must be resolved before a candidate can freeze its experiment baseline.
	if event.AgentVersionID != "" && event.AgentID != "" {
		observation.Dependencies = []improvement.ResourceVersion{{Kind: improvement.TargetAgentLoop, ID: event.AgentID.String(), Version: event.AgentVersionID}}
	}
	for _, ref := range event.KnowledgeRefs {
		if len(observation.RelatedObjects) >= 128 {
			break
		}
		if strings.TrimSpace(ref.Kind) != "" && strings.TrimSpace(ref.ID) != "" {
			observation.RelatedObjects = append(observation.RelatedObjects, improvement.ObjectRef{Kind: ref.Kind, ID: ref.ID})
		}
	}
	for _, ref := range event.MemoryRefs {
		if len(observation.RelatedObjects) >= 128 {
			break
		}
		if strings.TrimSpace(ref.ID) != "" {
			observation.RelatedObjects = append(observation.RelatedObjects, improvement.ObjectRef{Kind: "memory_record", ID: ref.ID})
		}
	}
	// Enriched terminal evidence is a new immutable observation. Identical
	// lifecycle replays deduplicate without conflicting with later enrichment.
	observation.ID = "runtime-result:" + improvement.Digest(observation)
	_, err := s.improvements.Observe(ctx, observation)
	return err
}

func (s *productionAgentExecutionOntologySink) ProjectImprovement(ctx context.Context, c improvement.Case) error {
	events := improvement.OntologyEvents(c)
	if improvement.IsCapturedRuntimeCase(c) {
		if s.improvements == nil {
			return improvement.ErrUnavailable
		}
		review, err := s.improvements.RuntimeReviewForCase(ctx, c)
		if err != nil {
			return err
		}
		event, err := improvement.RuntimeReviewOntologyEvent(c, review)
		if err != nil {
			return err
		}
		events = append(events, event)
		dependencies, err := s.improvements.RuntimeDependenciesForCase(ctx, c)
		if err != nil {
			return err
		}
		if dependencies != nil {
			events = append(events, *dependencies)
		}
	}
	for _, event := range events {
		if err := s.apply(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func (s *productionAgentExecutionOntologySink) ProjectBusinessAssessment(ctx context.Context, a improvement.BusinessAssessment) error {
	if s.improvements == nil {
		return improvement.ErrUnavailable
	}
	c, err := s.improvements.Get(ctx, a.TenantID, a.CaseID)
	if err != nil {
		return err
	}
	// Outbox batches can arrive in a different order. Materialize parents
	// before optional links, so a successful child event cannot lose them.
	if err := s.ProjectImprovement(ctx, c); err != nil {
		return err
	}
	event, err := improvement.BusinessAssessmentOntologyEvent(c, a)
	if err != nil {
		return err
	}
	return s.apply(ctx, event)
}

func registerProductionImprovementSweeper(rt *platform.Runtime, sink *productionAgentExecutionOntologySink) error {
	if rt == nil || sink == nil || sink.improvements == nil {
		return fmt.Errorf("improvement store and ontology sink are required")
	}
	if err := registerProductionRuntimeDiagnosisSweeper(rt, sink); err != nil {
		return err
	}
	return rt.RegisterSweeper("ontology.improvement_projection", platform.SweeperFunc(func(ctx context.Context) error {
		limit := rt.Config.Sweeper.BatchSize
		if limit < 1 {
			limit = 100
		}
		if limit > 1000 {
			limit = 1000
		}
		_, projectionErr := sink.improvements.ProjectPending(ctx, sink, limit)
		_, repairReconciliationErr := sink.improvements.ReconcileManagedRepairCases(ctx, limit)
		evidence := sink.improvements.RuntimeEvidence()
		_, businessErr := evidence.ReconcileBusiness(ctx, limit)
		_, businessProjectionErr := evidence.ProjectBusinessPending(ctx, sink, limit)
		_, artifactErr := evidence.ReconcileTaskArtifacts(ctx, productionTaskArtifactReader{store: rt.FileStore}, limit)
		_, artifactProjectionErr := evidence.ProjectTaskArtifactsPending(ctx, sink, limit)
		return errors.Join(projectionErr, repairReconciliationErr, businessErr, businessProjectionErr, artifactErr, artifactProjectionErr)
	}))
}

func (s *productionAgentExecutionOntologySink) ProjectTaskArtifactVerification(ctx context.Context, r improvement.TaskArtifactVerification) error {
	if s.improvements == nil {
		return improvement.ErrUnavailable
	}
	c, err := s.improvements.Get(ctx, r.TenantID, r.CaseID)
	if err != nil {
		return err
	}
	assessments, err := s.improvements.RuntimeEvidence().BusinessAssessments(ctx, r.TenantID, r.AttemptID)
	if err != nil {
		return err
	}
	parentFound := false
	for _, a := range assessments {
		if a.ID == r.BusinessAssessmentID {
			if err := s.ProjectBusinessAssessment(ctx, a); err != nil {
				return err
			}
			parentFound = true
			break
		}
	}
	if !parentFound {
		return improvement.ErrInvalid
	}
	event, err := improvement.TaskArtifactVerificationOntologyEvent(c, r)
	if err != nil {
		return err
	}
	return s.apply(ctx, event)
}
