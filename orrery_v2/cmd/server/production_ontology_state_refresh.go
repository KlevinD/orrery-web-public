package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/app/ontology"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/db"
)

const (
	productionOntologyStateRefreshKind            = "daily_state"
	productionOntologyStateSnapshotRetentionDays  = 180
	productionOntologyStateSnapshotRetentionBatch = 500
)

type productionOntologyStateRefreshReport struct {
	Fingerprint       string
	SourceCount       int
	ChangedCount      int
	SnapshotOnlyCount int
	NoopCount         int
	StaleCount        int
}

func (s *productionAgentExecutionOntologySink) projectScheduledTaskState(ctx context.Context, task schedulerapp.ScheduledTask) (productionOntologyStateRefreshReport, error) {
	return s.projectScheduledTaskStateWithMode(ctx, task, false)
}

func (s *productionAgentExecutionOntologySink) reconcileScheduledTaskState(ctx context.Context, task schedulerapp.ScheduledTask) (productionOntologyStateRefreshReport, error) {
	return s.projectScheduledTaskStateWithMode(ctx, task, true)
}

func (s *productionAgentExecutionOntologySink) projectScheduledTaskStateWithMode(ctx context.Context, task schedulerapp.ScheduledTask, authoritativeCurrentState bool) (productionOntologyStateRefreshReport, error) {
	var report productionOntologyStateRefreshReport
	task.AgentTaskID = firstNonBlank(task.AgentTaskID, schedulerapp.AgentTaskIDForScheduledTask(task.ScheduledTaskID))
	s.tasks.Store(task.ScheduledTaskID, task)
	for _, tenantID := range scheduledTaskProjectionTenantIDs(task) {
		for _, event := range scheduledTaskOntologyEventsForTenantWithAgentDisplayName(task, tenantID, s.agentDisplayName(ctx, task.Target.AgentID)) {
			var state ontology.StateProjectionReport
			var err error
			if authoritativeCurrentState {
				state, err = s.state.ApplyAuthoritativeCurrentState(ctx, event)
			} else {
				state, err = s.state.Apply(ctx, event)
			}
			if err != nil {
				return report, err
			}
			report.addOutcome(state.Outcome)
		}
	}
	return report, nil
}

func (r *productionOntologyStateRefreshReport) addOutcome(outcome ontology.StateProjectionOutcome) {
	switch outcome {
	case ontology.StateProjectionChanged:
		r.ChangedCount++
	case ontology.StateProjectionSnapshotOnly:
		r.SnapshotOnlyCount++
	case ontology.StateProjectionNoop:
		r.NoopCount++
	case ontology.StateProjectionStale:
		r.StaleCount++
	}
}

func (r *productionOntologyStateRefreshReport) add(other productionOntologyStateRefreshReport) {
	r.ChangedCount += other.ChangedCount
	r.SnapshotOnlyCount += other.SnapshotOnlyCount
	r.NoopCount += other.NoopCount
	r.StaleCount += other.StaleCount
}

type productionOntologyStateRefreshRepository interface {
	ListTasks(context.Context, bool) ([]schedulerapp.ScheduledTask, error)
}

type ontologyStateRefreshManifestStore interface {
	Claim(context.Context, ontology.StateRefreshManifest) (bool, error)
	Complete(context.Context, ontology.StateRefreshManifest) error
	Fail(context.Context, ontology.StateRefreshManifest) error
}

type productionOntologyStateRefresher struct {
	core       *db.Store
	repository productionOntologyStateRefreshRepository
	sink       *productionAgentExecutionOntologySink
	manifests  ontologyStateRefreshManifestStore
	now        func() time.Time
	metrics    ontologyMetricRecorder
}

func newProductionOntologyStateRefresher(core *db.Store, repository productionOntologyStateRefreshRepository, sink *productionAgentExecutionOntologySink, now func() time.Time, recorders ...ontologyMetricRecorder) *productionOntologyStateRefresher {
	var metrics ontologyMetricRecorder
	if len(recorders) > 0 {
		metrics = recorders[0]
	}
	return &productionOntologyStateRefresher{
		core:       core,
		repository: repository,
		sink:       sink,
		manifests:  ontology.NewPlatformStateRefreshManifestStore(core),
		now:        now,
		metrics:    metrics,
	}
}

func (r *productionOntologyStateRefresher) RefreshOntologyState(ctx context.Context, _ schedulerapp.OntologyStateRefreshRequest) (schedulerapp.OntologyStateRefreshResult, error) {
	report, err := r.Refresh(ctx)
	return schedulerapp.OntologyStateRefreshResult{
		Fingerprint:       report.Fingerprint,
		SourceCount:       report.SourceCount,
		ChangedCount:      report.ChangedCount,
		SnapshotOnlyCount: report.SnapshotOnlyCount,
		NoopCount:         report.NoopCount,
		StaleCount:        report.StaleCount,
	}, err
}

func (r *productionOntologyStateRefresher) Refresh(ctx context.Context) (report productionOntologyStateRefreshReport, err error) {
	if r == nil || r.core == nil || r.repository == nil || r.sink == nil || r.manifests == nil || r.now == nil {
		return report, fmt.Errorf("production ontology state refresh dependencies are required")
	}
	startedAt := r.now().UTC()
	metricStatus := "failed"
	defer func() {
		r.captureMetric(ctx, report, metricStatus, startedAt)
	}()
	mappingRevision, mappingSignature, err := productionOntologyCurrentMappingSignature(ctx, r.core)
	if err != nil {
		return report, err
	}
	tasks, err := r.repository.ListTasks(ctx, true)
	if err != nil {
		return r.recordFailure(ctx, startedAt, mappingRevision, mappingSignature, "source", 0, err)
	}
	report = productionOntologyStateRefreshReport{SourceCount: len(tasks)}
	report.Fingerprint, err = productionOntologyStateRefreshFingerprint(tasks, mappingSignature, startedAt)
	if err != nil {
		return r.recordFailure(ctx, startedAt, mappingRevision, mappingSignature, "fingerprint", len(tasks), err)
	}
	manifest := report.stateRefreshManifest(startedAt, mappingRevision)
	claimed, err := r.manifests.Claim(ctx, manifest)
	if err != nil {
		return report, err
	}
	if !claimed {
		metricStatus = "skipped"
		return report, nil
	}
	for _, task := range tasks {
		taskReport, err := r.sink.reconcileScheduledTaskState(ctx, task)
		report.add(taskReport)
		if err != nil {
			return r.failClaimed(ctx, report, startedAt, mappingRevision, err)
		}
	}
	if err := r.deleteExpiredDailySnapshots(ctx, startedAt); err != nil {
		return r.failClaimed(ctx, report, startedAt, mappingRevision, err)
	}
	manifest = report.stateRefreshManifest(startedAt, mappingRevision)
	manifest.CompletedAt = r.now().UTC()
	if err := r.manifests.Complete(ctx, manifest); err != nil {
		return report, fmt.Errorf("complete production ontology state refresh: %w", err)
	}
	metricStatus = "succeeded"
	return report, nil
}

func (r *productionOntologyStateRefresher) deleteExpiredDailySnapshots(ctx context.Context, snapshotAt time.Time) error {
	if r.sink == nil || r.sink.state.store == nil {
		return fmt.Errorf("ontology state projection store is required for snapshot retention")
	}
	cutoffDate := snapshotAt.UTC().AddDate(0, 0, -productionOntologyStateSnapshotRetentionDays).Format("2006-01-02")
	for {
		deleted, err := r.sink.state.store.DeleteDailySnapshotsBefore(ctx, cutoffDate, productionOntologyStateSnapshotRetentionBatch)
		if err != nil {
			return fmt.Errorf("delete expired ontology daily snapshots: %w", err)
		}
		if deleted < productionOntologyStateSnapshotRetentionBatch {
			return nil
		}
	}
}

func (r *productionOntologyStateRefresher) captureMetric(ctx context.Context, report productionOntologyStateRefreshReport, status string, startedAt time.Time) {
	if r.metrics == nil {
		return
	}
	duration := r.now().UTC().Sub(startedAt)
	if duration < 0 {
		duration = 0
	}
	r.metrics.CaptureContext(ctx, "ontology.state_refresh", map[string]any{
		"source_count":        report.SourceCount,
		"changed_count":       report.ChangedCount,
		"snapshot_only_count": report.SnapshotOnlyCount,
		"noop_count":          report.NoopCount,
		"stale_count":         report.StaleCount,
		"status":              status,
		"duration_ms":         int(duration.Milliseconds()),
	})
}

func (r *productionOntologyStateRefresher) recordFailure(
	ctx context.Context,
	startedAt time.Time,
	mappingRevision int64,
	mappingSignature string,
	stage string,
	sourceCount int,
	cause error,
) (productionOntologyStateRefreshReport, error) {
	report := productionOntologyStateRefreshReport{
		Fingerprint: productionOntologyStateRefreshFailureFingerprint(mappingSignature, startedAt, stage),
		SourceCount: sourceCount,
	}
	manifest := report.stateRefreshManifest(startedAt, mappingRevision)
	claimed, err := r.manifests.Claim(ctx, manifest)
	if err != nil {
		return report, errors.Join(cause, fmt.Errorf("claim failed ontology state refresh manifest: %w", err))
	}
	if !claimed {
		return report, cause
	}
	return r.failClaimed(ctx, report, startedAt, mappingRevision, cause)
}

func (r *productionOntologyStateRefresher) failClaimed(ctx context.Context, report productionOntologyStateRefreshReport, startedAt time.Time, mappingRevision int64, cause error) (productionOntologyStateRefreshReport, error) {
	manifest := report.stateRefreshManifest(startedAt, mappingRevision)
	manifest.CompletedAt = r.now().UTC()
	manifest.LastError = cause.Error()
	if err := r.manifests.Fail(ctx, manifest); err != nil {
		return report, errors.Join(cause, fmt.Errorf("record failed ontology state refresh: %w", err))
	}
	return report, cause
}

func (r productionOntologyStateRefreshReport) stateRefreshManifest(startedAt time.Time, mappingRevision int64) ontology.StateRefreshManifest {
	return ontology.StateRefreshManifest{
		RefreshKind:       productionOntologyStateRefreshKind,
		Fingerprint:       r.Fingerprint,
		SnapshotDate:      startedAt.UTC().Format("2006-01-02"),
		MappingRevision:   mappingRevision,
		SourceCount:       r.SourceCount,
		ChangedCount:      r.ChangedCount,
		SnapshotOnlyCount: r.SnapshotOnlyCount,
		NoopCount:         r.NoopCount,
		StaleCount:        r.StaleCount,
		StartedAt:         startedAt,
	}
}

func productionOntologyCurrentMappingSignature(ctx context.Context, core *db.Store) (int64, string, error) {
	if core == nil {
		return 0, "", fmt.Errorf("core database is required for ontology state refresh")
	}
	revision, err := ontology.NewPlatformMappingProjectionRefreshStore(core).MappingProjectionRevision(ctx)
	if err != nil {
		return 0, "", fmt.Errorf("read ontology mapping projection revision: %w", err)
	}
	rules, err := ontology.NewPlatformRegistry(core).ListMappingRules(ctx, "")
	if err != nil {
		return 0, "", fmt.Errorf("list ontology mapping rules for state refresh: %w", err)
	}
	signature, err := productionOntologyMappingSignature(revision, rules)
	if err != nil {
		return 0, "", err
	}
	return revision, signature, nil
}

func productionOntologyMappingSignature(mappingRevision int64, rules []ontology.MappingRule) (string, error) {
	encoded, err := json.Marshal(rules)
	if err != nil {
		return "", fmt.Errorf("encode ontology mapping rules for state refresh: %w", err)
	}
	hash := sha256.New()
	hash.Write([]byte("revision:" + strconv.FormatInt(mappingRevision, 10) + "\n"))
	hash.Write(encoded)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func productionOntologyStateRefreshFingerprint(tasks []schedulerapp.ScheduledTask, mappingSignature string, snapshotAt time.Time) (string, error) {
	if strings.TrimSpace(mappingSignature) == "" || snapshotAt.IsZero() {
		return "", fmt.Errorf("ontology state refresh mapping signature and snapshot time are required")
	}
	identities := make([]string, 0, len(tasks)*3)
	for _, task := range tasks {
		for _, event := range scheduledTaskOntologyEvents(task) {
			canonical, err := ontology.CanonicalStateProjection(ontology.StateProjectionInput{
				TenantID: event.TenantID, EventType: event.Type, EntityID: event.StreamID,
				Payload: event.Payload, SourceVersion: event.Version, SourceUpdatedAt: event.OccurredAt,
				SnapshotAt: snapshotAt,
			})
			if err != nil {
				return "", fmt.Errorf("fingerprint ontology state event %s: %w", event.Type, err)
			}
			identities = append(identities, strings.Join([]string{
				canonical.TenantID,
				canonical.EventType,
				canonical.EntityID,
				canonical.SemanticHash,
			}, "\x00"))
		}
	}
	sort.Strings(identities)
	hash := sha256.New()
	hash.Write([]byte("refresh_kind:" + productionOntologyStateRefreshKind + "\n"))
	hash.Write([]byte("snapshot_date:" + snapshotAt.UTC().Format("2006-01-02") + "\n"))
	hash.Write([]byte("mapping_signature:" + mappingSignature + "\n"))
	for _, identity := range identities {
		hash.Write([]byte(identity))
		hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func productionOntologyStateRefreshFailureFingerprint(mappingSignature string, snapshotAt time.Time, stage string) string {
	hash := sha256.New()
	hash.Write([]byte("refresh_kind:" + productionOntologyStateRefreshKind + "\n"))
	hash.Write([]byte("snapshot_date:" + snapshotAt.UTC().Format("2006-01-02") + "\n"))
	hash.Write([]byte("mapping_signature:" + mappingSignature + "\n"))
	hash.Write([]byte("failure_stage:" + strings.TrimSpace(stage) + "\n"))
	return hex.EncodeToString(hash.Sum(nil))
}
