package ontology

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/clock"
)

const defaultModelRunEvaluationTrendAlertAuditSweeperName = "ontology.model_run_evaluation_alert_audit.recorder"
const defaultModelRunEvaluationTrendPolicyAlertAuditSweeperName = "ontology.model_run_evaluation_policy_alert_audit.recorder"

type ModelRunEvaluationTrendAlertAuditSweeperOptions struct {
	Name      string
	Ledger    ModelRunLedger
	Artifacts ModelRunEvaluationArtifactStore
	Policies  ModelRunEvaluationPolicyProfileStore
	Store     ModelRunEvaluationTrendAlertAuditStore
	Clock     clock.Clock
	Lookback  time.Duration
	Query     ModelRunEvaluationTrendQuery
	Rules     []ModelRunEvaluationTrendAlertRule
}

type ModelRunEvaluationTrendAlertAuditSweepReport struct {
	Recorded int `json:"recorded"`
	Buckets  int `json:"buckets"`
}

type ModelRunEvaluationTrendAlertAuditSweeper struct {
	name       string
	ledger     ModelRunLedger
	artifacts  ModelRunEvaluationArtifactStore
	policies   ModelRunEvaluationPolicyProfileStore
	store      ModelRunEvaluationTrendAlertAuditStore
	clock      clock.Clock
	lookback   time.Duration
	query      ModelRunEvaluationTrendQuery
	rules      []ModelRunEvaluationTrendAlertRule
	lastReport ModelRunEvaluationTrendAlertAuditSweepReport
}

type ModelRunEvaluationTrendPolicyAlertAuditSweeperOptions struct {
	Name         string
	Ledger       ModelRunLedger
	Artifacts    ModelRunEvaluationArtifactStore
	Policies     ModelRunEvaluationPolicyProfileStore
	Store        ModelRunEvaluationTrendAlertAuditStore
	Clock        clock.Clock
	Lookback     time.Duration
	Query        ModelRunEvaluationTrendQuery
	ProfileQuery ModelRunEvaluationPolicyProfileQuery
	MinFailures  int
}

type ModelRunEvaluationTrendPolicyAlertAuditSweepReport struct {
	Profiles int `json:"profiles"`
	Recorded int `json:"recorded"`
	Buckets  int `json:"buckets"`
}

type ModelRunEvaluationTrendPolicyAlertAuditSweeper struct {
	name         string
	ledger       ModelRunLedger
	artifacts    ModelRunEvaluationArtifactStore
	policies     ModelRunEvaluationPolicyProfileStore
	store        ModelRunEvaluationTrendAlertAuditStore
	clock        clock.Clock
	lookback     time.Duration
	query        ModelRunEvaluationTrendQuery
	profileQuery ModelRunEvaluationPolicyProfileQuery
	minFailures  int
	lastReport   ModelRunEvaluationTrendPolicyAlertAuditSweepReport
}

func NewModelRunEvaluationTrendAlertAuditSweeper(opts ModelRunEvaluationTrendAlertAuditSweeperOptions) *ModelRunEvaluationTrendAlertAuditSweeper {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = defaultModelRunEvaluationTrendAlertAuditSweeperName
	}
	sweepClock := opts.Clock
	if sweepClock == nil {
		sweepClock = clock.Real{}
	}
	return &ModelRunEvaluationTrendAlertAuditSweeper{
		name:      name,
		ledger:    opts.Ledger,
		artifacts: opts.Artifacts,
		policies:  opts.Policies,
		store:     opts.Store,
		clock:     sweepClock,
		lookback:  opts.Lookback,
		query:     opts.Query,
		rules:     append([]ModelRunEvaluationTrendAlertRule(nil), opts.Rules...),
	}
}

func NewModelRunEvaluationTrendPolicyAlertAuditSweeper(opts ModelRunEvaluationTrendPolicyAlertAuditSweeperOptions) *ModelRunEvaluationTrendPolicyAlertAuditSweeper {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = defaultModelRunEvaluationTrendPolicyAlertAuditSweeperName
	}
	sweepClock := opts.Clock
	if sweepClock == nil {
		sweepClock = clock.Real{}
	}
	return &ModelRunEvaluationTrendPolicyAlertAuditSweeper{
		name:         name,
		ledger:       opts.Ledger,
		artifacts:    opts.Artifacts,
		policies:     opts.Policies,
		store:        opts.Store,
		clock:        sweepClock,
		lookback:     opts.Lookback,
		query:        opts.Query,
		profileQuery: opts.ProfileQuery,
		minFailures:  opts.MinFailures,
	}
}

func (s *ModelRunEvaluationTrendAlertAuditSweeper) Name() string {
	if s == nil || strings.TrimSpace(s.name) == "" {
		return defaultModelRunEvaluationTrendAlertAuditSweeperName
	}
	return s.name
}

func (s *ModelRunEvaluationTrendPolicyAlertAuditSweeper) Name() string {
	if s == nil || strings.TrimSpace(s.name) == "" {
		return defaultModelRunEvaluationTrendPolicyAlertAuditSweeperName
	}
	return s.name
}

func (s *ModelRunEvaluationTrendAlertAuditSweeper) Sweep(ctx context.Context) error {
	if s == nil || s.ledger == nil || s.store == nil {
		return fmt.Errorf("%w: model run evaluation trend alert audit sweeper requires ledger and store", ErrInvalidDefinition)
	}
	now := s.now()
	recorded, page, err := RecordModelRunEvaluationTrendAlertAudits(ctx, s.ledger, s.artifacts, s.policies, s.store, s.collectionQuery(now), s.rules, now)
	if err != nil {
		return err
	}
	s.lastReport = ModelRunEvaluationTrendAlertAuditSweepReport{Recorded: recorded, Buckets: len(page.Buckets)}
	return nil
}

func (s *ModelRunEvaluationTrendPolicyAlertAuditSweeper) Sweep(ctx context.Context) error {
	if s == nil || s.ledger == nil || s.artifacts == nil || s.policies == nil || s.store == nil {
		return fmt.Errorf("%w: model run evaluation trend policy alert audit sweeper requires ledger, artifacts, policies, and store", ErrInvalidDefinition)
	}
	now := s.now()
	report := ModelRunEvaluationTrendPolicyAlertAuditSweepReport{}
	query := s.enabledProfileQuery()
	for {
		profiles, err := s.policies.ListModelRunEvaluationPolicyProfiles(ctx, query)
		if err != nil {
			return err
		}
		for _, profile := range profiles.Profiles {
			if err := ctx.Err(); err != nil {
				return err
			}
			report.Profiles++
			rules := BuildModelRunEvaluationTrendAlertRulesFromPolicyProfile(profile, s.minFailures)
			if len(rules) == 0 {
				continue
			}
			page, err := s.collectionPageForProfile(ctx, now, profile)
			if err != nil {
				return err
			}
			report.Buckets += len(page.Buckets)
			for _, event := range BuildModelRunEvaluationTrendAlertAuditEvents(EvaluateModelRunEvaluationTrendAlertRules(page, rules), now) {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := s.store.SaveModelRunEvaluationTrendAlertAuditEvent(ctx, event); err != nil {
					return err
				}
				report.Recorded++
			}
		}
		if !profiles.Page.HasMore || len(profiles.Profiles) == 0 {
			break
		}
		query.Offset = profiles.Page.Offset + len(profiles.Profiles)
	}
	s.lastReport = report
	return nil
}

func (s *ModelRunEvaluationTrendAlertAuditSweeper) LastReport() ModelRunEvaluationTrendAlertAuditSweepReport {
	if s == nil {
		return ModelRunEvaluationTrendAlertAuditSweepReport{}
	}
	return s.lastReport
}

func (s *ModelRunEvaluationTrendPolicyAlertAuditSweeper) LastReport() ModelRunEvaluationTrendPolicyAlertAuditSweepReport {
	if s == nil {
		return ModelRunEvaluationTrendPolicyAlertAuditSweepReport{}
	}
	return s.lastReport
}

func (s *ModelRunEvaluationTrendAlertAuditSweeper) now() time.Time {
	if s == nil || s.clock == nil {
		return time.Now().UTC()
	}
	return s.clock.Now().UTC()
}

func (s *ModelRunEvaluationTrendPolicyAlertAuditSweeper) now() time.Time {
	if s == nil || s.clock == nil {
		return time.Now().UTC()
	}
	return s.clock.Now().UTC()
}

func (s *ModelRunEvaluationTrendAlertAuditSweeper) collectionQuery(now time.Time) ModelRunEvaluationTrendQuery {
	query := s.query
	if s.lookback <= 0 {
		return query
	}
	query.RecordedFrom = now.Add(-s.lookback)
	query.RecordedTo = now
	return query
}

func (s *ModelRunEvaluationTrendPolicyAlertAuditSweeper) collectionPageForProfile(ctx context.Context, now time.Time, profile ModelRunEvaluationPolicyProfile) (ModelRunEvaluationTrendPage, error) {
	query := s.collectionQueryForProfile(now, profile)
	collected := ModelRunEvaluationTrendPage{}
	for {
		page, err := ListModelRunEvaluationTrendsWithEvaluationPolicyProfiles(ctx, s.ledger, s.artifacts, s.policies, query)
		if err != nil {
			return ModelRunEvaluationTrendPage{}, err
		}
		filtered := modelRunEvaluationTrendPageForPolicyAlertProfile(page, profile)
		if len(filtered.Buckets) > 0 {
			collected.SnapshotRecordedTo = filtered.SnapshotRecordedTo
			collected.Buckets = append(collected.Buckets, filtered.Buckets...)
		}
		if !page.Page.HasMore || len(page.Buckets) == 0 {
			collected.Page = Page{Limit: len(collected.Buckets), Total: len(collected.Buckets), Returned: len(collected.Buckets)}
			if collected.SnapshotRecordedTo.IsZero() {
				collected.SnapshotRecordedTo = page.SnapshotRecordedTo
			}
			return collected, nil
		}
		query.Offset = page.Page.Offset + len(page.Buckets)
	}
}

func (s *ModelRunEvaluationTrendPolicyAlertAuditSweeper) collectionQueryForProfile(now time.Time, profile ModelRunEvaluationPolicyProfile) ModelRunEvaluationTrendQuery {
	query := s.query
	if strings.TrimSpace(query.TenantID) == "" {
		query.TenantID = profile.TenantID
	}
	if query.Engine == "" {
		query.Engine = profile.Engine
	}
	if s.lookback > 0 {
		query.RecordedFrom = now.Add(-s.lookback)
		query.RecordedTo = now
	}
	return query
}

func (s *ModelRunEvaluationTrendPolicyAlertAuditSweeper) enabledProfileQuery() ModelRunEvaluationPolicyProfileQuery {
	query := s.profileQuery
	if strings.TrimSpace(query.TenantID) == "" {
		query.TenantID = s.query.TenantID
	}
	if query.Engine == "" {
		query.Engine = s.query.Engine
	}
	query.EnabledOnly = true
	if query.Limit <= 0 {
		query.Limit = 100
	}
	return query
}

func modelRunEvaluationTrendPageForPolicyAlertProfile(page ModelRunEvaluationTrendPage, profile ModelRunEvaluationPolicyProfile) ModelRunEvaluationTrendPage {
	buckets := make([]ModelRunEvaluationTrendBucket, 0, len(page.Buckets))
	for _, bucket := range page.Buckets {
		if modelRunEvaluationTrendBucketMatchesPolicyAlertProfile(bucket, profile) {
			buckets = append(buckets, bucket)
		}
	}
	page.Buckets = buckets
	page.Page = Page{Limit: len(buckets), Total: len(buckets), Returned: len(buckets)}
	return page
}

func modelRunEvaluationTrendBucketMatchesPolicyAlertProfile(bucket ModelRunEvaluationTrendBucket, profile ModelRunEvaluationPolicyProfile) bool {
	return (strings.TrimSpace(profile.TenantID) == "" || bucket.TenantID == strings.TrimSpace(profile.TenantID)) &&
		(profile.Engine == "" || bucket.Engine == profile.Engine) &&
		(strings.TrimSpace(profile.Model) == "" || bucket.Model == strings.TrimSpace(profile.Model)) &&
		(strings.TrimSpace(profile.ModelVersion) == "" || bucket.ModelVersion == strings.TrimSpace(profile.ModelVersion))
}
