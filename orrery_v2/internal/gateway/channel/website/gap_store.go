package website

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/gateway/protocol"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

type WebsiteKnowledgeGapStore interface {
	RecordGap(context.Context, WebsiteGapEvent) (WebsiteGapAggregate, error)
	ListKnowledgeGaps(context.Context, int, time.Time) ([]WebsiteGapAggregate, error)
}

type WebsiteKnowledgeGovernanceStore interface {
	WebsiteKnowledgeGapStore
	SaveRuleRevision(context.Context, AutomaticRuleRevision) error
	EvaluateRuleRevision(context.Context, string, bool, float64, float64, time.Time) (AutomaticRuleRevision, error)
	CreateProposal(context.Context, WebsiteKnowledgeProposalInput) (WebsiteKnowledgeProposal, error)
	ApproveProposal(context.Context, string, string, string, time.Time) (WebsiteKnowledgeProposal, error)
	PublishProposal(context.Context, string, string, string, time.Time) (WebsiteKnowledgeProposal, error)
}

type WebsiteFAQGovernanceStore interface {
	WebsiteKnowledgeGovernanceStore
	ListWebsiteKnowledgeProposals(context.Context, ProposalStatus, int) ([]WebsiteKnowledgeProposal, error)
	GetWebsiteKnowledgeProposal(context.Context, string) (WebsiteKnowledgeProposal, error)
	ListWebsiteKnowledgeReceipts(context.Context, string) ([]WebsiteKnowledgeReceipt, error)
	ListWebsiteFAQMetrics(context.Context, time.Time) ([]WebsiteFAQMetric, error)
	ListPublishedFAQRevisions(context.Context) ([]WebsiteFAQRevision, error)
}

type SQLWebsiteGapStore struct {
	store *db.Store
}

func NewSQLWebsiteGapStore(store *db.Store) *SQLWebsiteGapStore {
	return &SQLWebsiteGapStore{store: store}
}

func websiteGapMigration() migration.Migration {
	return migration.Migration{
		Version: "0189_website_knowledge_improvement", Description: "create sanitized website knowledge improvement tables",
		Statements: websiteGapMigrationStatements("INTEGER"),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: websiteGapMigrationStatements("BIGINT"),
		},
	}
}

func websiteGapMigrationStatements(timeType string) []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS website_knowledge_gap_events (
			event_id TEXT PRIMARY KEY,
			topic_fingerprint TEXT NOT NULL,
			conversation_fingerprint TEXT NOT NULL,
			tier TEXT NOT NULL CHECK (tier IN ('public', 'customer')),
			route TEXT NOT NULL,
			matched_intent TEXT NOT NULL DEFAULT '',
			retrieval_score REAL NOT NULL DEFAULT 0,
			source_ids_json TEXT NOT NULL DEFAULT '[]',
			outcome TEXT NOT NULL,
			feedback TEXT NOT NULL DEFAULT '',
			takeover INTEGER NOT NULL DEFAULT 0,
			occurred_at ` + timeType + ` NOT NULL,
			UNIQUE(topic_fingerprint, conversation_fingerprint)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_website_knowledge_gap_events_topic_time ON website_knowledge_gap_events (topic_fingerprint, tier, occurred_at)`,
		`CREATE TABLE IF NOT EXISTS website_knowledge_gap_aggregates (
			gap_id TEXT PRIMARY KEY,
			topic_fingerprint TEXT NOT NULL,
			tier TEXT NOT NULL CHECK (tier IN ('public', 'customer')),
			conversation_count INTEGER NOT NULL,
			candidate INTEGER NOT NULL DEFAULT 0,
			first_seen_at ` + timeType + ` NOT NULL,
			last_seen_at ` + timeType + ` NOT NULL,
			UNIQUE(topic_fingerprint, tier)
		)`,
		`CREATE TABLE IF NOT EXISTS website_knowledge_rule_revisions (
			revision_id TEXT PRIMARY KEY,
			revision_hash TEXT NOT NULL UNIQUE,
			kind TEXT NOT NULL,
			before_value TEXT NOT NULL,
			after_value TEXT NOT NULL,
			evidence_window TEXT NOT NULL,
			authoring_job TEXT NOT NULL,
			metrics_snapshot TEXT NOT NULL,
			approved_answer_revision TEXT NOT NULL,
			activated_at ` + timeType + ` NOT NULL,
			rollback_revision_id TEXT NOT NULL,
			rolled_back_at ` + timeType + `,
			rollback_reason TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS website_knowledge_proposal_revisions (
			proposal_id TEXT PRIMARY KEY,
			revision_hash TEXT NOT NULL UNIQUE,
			gap_id TEXT NOT NULL,
			target_knowledge_key TEXT NOT NULL,
			draft TEXT NOT NULL,
			source_refs_json TEXT NOT NULL,
			rationale TEXT NOT NULL,
			status TEXT NOT NULL CHECK (status IN ('draft', 'approved', 'published')),
			created_by TEXT NOT NULL,
			created_at ` + timeType + ` NOT NULL,
			approved_by TEXT NOT NULL DEFAULT '',
			approved_at ` + timeType + `,
			published_by TEXT NOT NULL DEFAULT '',
			published_at ` + timeType + `
		)`,
		`CREATE TABLE IF NOT EXISTS website_knowledge_approval_receipts (
			receipt_id TEXT PRIMARY KEY,
			proposal_id TEXT NOT NULL,
			revision_hash TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			created_at ` + timeType + ` NOT NULL,
			UNIQUE(proposal_id, revision_hash)
		)`,
		`CREATE TABLE IF NOT EXISTS website_knowledge_publication_receipts (
			receipt_id TEXT PRIMARY KEY,
			proposal_id TEXT NOT NULL,
			revision_hash TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			created_at ` + timeType + ` NOT NULL,
			UNIQUE(proposal_id, revision_hash)
		)`,
	}
}

func websiteFAQCatalogMigration() migration.Migration {
	return migration.Migration{
		Version: "0190_website_faq_catalog", Description: "add typed website FAQ proposals and published revisions",
		Statements: websiteFAQCatalogMigrationStatements("INTEGER"),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: websiteFAQCatalogMigrationStatements("BIGINT"),
		},
	}
}

func websiteFAQCatalogMigrationStatements(timeType string) []string {
	return []string{
		`ALTER TABLE website_knowledge_proposal_revisions ADD COLUMN proposal_kind TEXT NOT NULL DEFAULT 'knowledge_document'`,
		`ALTER TABLE website_knowledge_proposal_revisions ADD COLUMN faq_revision_json TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE IF NOT EXISTS website_faq_published_revisions (
			revision_hash TEXT PRIMARY KEY,
			logical_intent_id TEXT NOT NULL,
			proposal_id TEXT NOT NULL UNIQUE,
			faq_revision_json TEXT NOT NULL,
			published_at ` + timeType + ` NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_website_faq_published_intent_time ON website_faq_published_revisions (logical_intent_id, published_at)`,
	}
}

func (s *SQLWebsiteGapStore) RecordGap(ctx context.Context, event WebsiteGapEvent) (WebsiteGapAggregate, error) {
	if s == nil || s.store == nil || event.EventID == "" {
		return WebsiteGapAggregate{}, errors.New("website knowledge gap store is not configured")
	}
	sources, _ := json.Marshal(event.SourceIDs)
	err := s.store.WithTx(ctx, func(tx db.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO website_knowledge_gap_events (
			event_id, topic_fingerprint, conversation_fingerprint, tier, route, matched_intent,
			retrieval_score, source_ids_json, outcome, feedback, takeover, occurred_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(topic_fingerprint, conversation_fingerprint) DO UPDATE SET
			route = excluded.route, matched_intent = excluded.matched_intent,
			retrieval_score = excluded.retrieval_score, source_ids_json = excluded.source_ids_json,
			outcome = excluded.outcome, feedback = excluded.feedback, takeover = excluded.takeover,
			occurred_at = excluded.occurred_at`,
			event.EventID, event.TopicFingerprint, event.ConversationFingerprint, event.Tier, event.Route,
			event.MatchedIntent, event.RetrievalScore, string(sources), event.Outcome, event.Feedback,
			boolInt(event.Takeover), event.OccurredAt.UnixNano()); err != nil {
			return err
		}
		aggregate, err := queryGapAggregate(ctx, tx, event.TopicFingerprint, event.Tier, event.OccurredAt)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO website_knowledge_gap_aggregates (
			gap_id, topic_fingerprint, tier, conversation_count, candidate, first_seen_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(topic_fingerprint, tier) DO UPDATE SET conversation_count = excluded.conversation_count,
			candidate = excluded.candidate, first_seen_at = excluded.first_seen_at, last_seen_at = excluded.last_seen_at`,
			aggregate.GapID, aggregate.TopicFingerprint, aggregate.Tier, aggregate.ConversationCount,
			boolInt(aggregate.Candidate), aggregate.FirstSeenAt.UnixNano(), aggregate.LastSeenAt.UnixNano())
		return err
	})
	if err != nil {
		return WebsiteGapAggregate{}, err
	}
	return s.getGapAggregate(ctx, event.TopicFingerprint, event.Tier)
}

type gapQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func queryGapAggregate(ctx context.Context, queryer gapQueryer, topic string, tier protocol.AdvisorTier, now time.Time) (WebsiteGapAggregate, error) {
	var count int
	var first, last int64
	err := queryer.QueryRowContext(ctx, `SELECT COUNT(DISTINCT conversation_fingerprint), MIN(occurred_at), MAX(occurred_at)
		FROM website_knowledge_gap_events WHERE topic_fingerprint = ? AND tier = ? AND occurred_at >= ?`,
		topic, tier, now.Add(-websiteGapWindow).UnixNano()).Scan(&count, &first, &last)
	if err != nil {
		return WebsiteGapAggregate{}, err
	}
	return WebsiteGapAggregate{
		GapID: gapID(topic, tier), TopicFingerprint: topic, Tier: tier, ConversationCount: count,
		Candidate: count >= websiteGapThreshold(tier), FirstSeenAt: time.Unix(0, first).UTC(), LastSeenAt: time.Unix(0, last).UTC(),
	}, nil
}

func (s *SQLWebsiteGapStore) getGapAggregate(ctx context.Context, topic string, tier protocol.AdvisorTier) (WebsiteGapAggregate, error) {
	var aggregate WebsiteGapAggregate
	var candidate int
	var first, last int64
	err := s.store.QueryRow(ctx, `SELECT gap_id, topic_fingerprint, tier, conversation_count, candidate, first_seen_at, last_seen_at
		FROM website_knowledge_gap_aggregates WHERE topic_fingerprint = ? AND tier = ?`, topic, tier).Scan(
		&aggregate.GapID, &aggregate.TopicFingerprint, &aggregate.Tier, &aggregate.ConversationCount, &candidate, &first, &last)
	if err != nil {
		return WebsiteGapAggregate{}, err
	}
	aggregate.Candidate = candidate != 0
	aggregate.FirstSeenAt, aggregate.LastSeenAt = time.Unix(0, first).UTC(), time.Unix(0, last).UTC()
	return aggregate, nil
}

func (s *SQLWebsiteGapStore) ListKnowledgeGaps(ctx context.Context, minimumCount int, now time.Time) ([]WebsiteGapAggregate, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("website knowledge gap store is not configured")
	}
	if minimumCount < 1 {
		minimumCount = 1
	}
	rows, err := s.store.Query(ctx, `SELECT topic_fingerprint, tier, COUNT(DISTINCT conversation_fingerprint), MIN(occurred_at), MAX(occurred_at)
		FROM website_knowledge_gap_events WHERE occurred_at >= ? GROUP BY topic_fingerprint, tier
		HAVING COUNT(DISTINCT conversation_fingerprint) >= ? ORDER BY COUNT(DISTINCT conversation_fingerprint) DESC, topic_fingerprint ASC`,
		now.UTC().Add(-websiteGapWindow).UnixNano(), minimumCount)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []WebsiteGapAggregate{}
	for rows.Next() {
		var value WebsiteGapAggregate
		var first, last int64
		if err := rows.Scan(&value.TopicFingerprint, &value.Tier, &value.ConversationCount, &first, &last); err != nil {
			return nil, err
		}
		value.GapID, value.Candidate = gapID(value.TopicFingerprint, value.Tier), value.ConversationCount >= websiteGapThreshold(value.Tier)
		value.FirstSeenAt, value.LastSeenAt = time.Unix(0, first).UTC(), time.Unix(0, last).UTC()
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *SQLWebsiteGapStore) SaveRuleRevision(ctx context.Context, revision AutomaticRuleRevision) error {
	if s == nil || s.store == nil || revision.RevisionID == "" {
		return errors.New("website rule revision is invalid")
	}
	_, err := s.store.Exec(ctx, `INSERT INTO website_knowledge_rule_revisions (
		revision_id, revision_hash, kind, before_value, after_value, evidence_window, authoring_job,
		metrics_snapshot, approved_answer_revision, activated_at, rollback_revision_id
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, revision.RevisionID, revision.RevisionHash, revision.Kind,
		revision.Before, revision.After, revision.EvidenceWindow, revision.AuthoringJob, revision.MetricsSnapshot,
		revision.ApprovedAnswerRevision, revision.ActivatedAt.UnixNano(), revision.RollbackRevisionID)
	return err
}

func (s *SQLWebsiteGapStore) EvaluateRuleRevision(ctx context.Context, revisionID string, sourceValid bool, resolutionBefore, resolutionAfter float64, now time.Time) (AutomaticRuleRevision, error) {
	revision, err := s.getRuleRevision(ctx, revisionID)
	if err != nil {
		return AutomaticRuleRevision{}, err
	}
	reason := ""
	if !sourceValid {
		reason = "source_validity_regression"
	} else if resolutionAfter < resolutionBefore {
		reason = "resolution_regression"
	}
	if reason == "" || !revision.RolledBackAt.IsZero() {
		return revision, nil
	}
	_, err = s.store.Exec(ctx, `UPDATE website_knowledge_rule_revisions SET rolled_back_at = ?, rollback_reason = ? WHERE revision_id = ? AND rolled_back_at IS NULL`, now.UTC().UnixNano(), reason, revisionID)
	if err != nil {
		return AutomaticRuleRevision{}, err
	}
	return s.getRuleRevision(ctx, revisionID)
}

func (s *SQLWebsiteGapStore) getRuleRevision(ctx context.Context, revisionID string) (AutomaticRuleRevision, error) {
	var value AutomaticRuleRevision
	var activated int64
	var rolledBack sql.NullInt64
	err := s.store.QueryRow(ctx, `SELECT revision_id, revision_hash, kind, before_value, after_value, evidence_window,
		authoring_job, metrics_snapshot, approved_answer_revision, activated_at, rollback_revision_id, rolled_back_at, rollback_reason
		FROM website_knowledge_rule_revisions WHERE revision_id = ?`, revisionID).Scan(
		&value.RevisionID, &value.RevisionHash, &value.Kind, &value.Before, &value.After, &value.EvidenceWindow,
		&value.AuthoringJob, &value.MetricsSnapshot, &value.ApprovedAnswerRevision, &activated,
		&value.RollbackRevisionID, &rolledBack, &value.RollbackReason)
	if err != nil {
		return AutomaticRuleRevision{}, err
	}
	value.ActivatedAt = time.Unix(0, activated).UTC()
	if rolledBack.Valid {
		value.RolledBackAt = time.Unix(0, rolledBack.Int64).UTC()
	}
	return value, nil
}

func (s *SQLWebsiteGapStore) CreateProposal(ctx context.Context, input WebsiteKnowledgeProposalInput) (WebsiteKnowledgeProposal, error) {
	if s == nil || s.store == nil {
		return WebsiteKnowledgeProposal{}, errors.New("website knowledge proposal store is not configured")
	}
	proposal, err := NewWebsiteKnowledgeProposal(input)
	if err != nil {
		return WebsiteKnowledgeProposal{}, err
	}
	sources, _ := json.Marshal(proposal.SourceRefs)
	faqRevision := ""
	if proposal.FAQRevision != nil {
		encoded, err := json.Marshal(proposal.FAQRevision)
		if err != nil {
			return WebsiteKnowledgeProposal{}, err
		}
		faqRevision = string(encoded)
	}
	_, err = s.store.Exec(ctx, `INSERT INTO website_knowledge_proposal_revisions (
		proposal_id, revision_hash, proposal_kind, gap_id, target_knowledge_key, draft, source_refs_json, rationale,
		faq_revision_json, status, created_by, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, proposal.ProposalID, proposal.RevisionHash, proposal.ProposalKind,
		proposal.GapID, proposal.TargetKnowledgeKey, proposal.Draft, string(sources), proposal.Rationale, faqRevision,
		proposal.Status, proposal.CreatedBy, proposal.CreatedAt.UnixNano())
	return proposal, err
}

func (s *SQLWebsiteGapStore) ApproveProposal(ctx context.Context, proposalID, expectedHash, actor string, now time.Time) (WebsiteKnowledgeProposal, error) {
	proposal, err := s.getProposal(ctx, proposalID)
	if err != nil {
		return WebsiteKnowledgeProposal{}, err
	}
	approved, err := proposal.Approve(expectedHash, actor, now)
	if err != nil {
		return WebsiteKnowledgeProposal{}, err
	}
	err = s.store.WithTx(ctx, func(tx db.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE website_knowledge_proposal_revisions SET status = ?, approved_by = ?, approved_at = ?
			WHERE proposal_id = ? AND revision_hash = ? AND status = ?`, approved.Status, approved.ApprovedBy, approved.ApprovedAt.UnixNano(), proposalID, expectedHash, ProposalStatusDraft)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return errors.New("website proposal approval conflict")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO website_knowledge_approval_receipts (receipt_id, proposal_id, revision_hash, actor_id, created_at)
			VALUES (?, ?, ?, ?, ?)`, receiptID("approval", proposalID, expectedHash), proposalID, expectedHash, approved.ApprovedBy, approved.ApprovedAt.UnixNano())
		return err
	})
	return approved, err
}

func (s *SQLWebsiteGapStore) PublishProposal(ctx context.Context, proposalID, expectedHash, actor string, now time.Time) (WebsiteKnowledgeProposal, error) {
	proposal, err := s.getProposal(ctx, proposalID)
	if err != nil {
		return WebsiteKnowledgeProposal{}, err
	}
	published, err := proposal.Publish(expectedHash, actor, now)
	if err != nil {
		return WebsiteKnowledgeProposal{}, err
	}
	err = s.store.WithTx(ctx, func(tx db.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE website_knowledge_proposal_revisions SET status = ?, published_by = ?, published_at = ?
			WHERE proposal_id = ? AND revision_hash = ? AND status = ?`, published.Status, published.PublishedBy, published.PublishedAt.UnixNano(), proposalID, expectedHash, ProposalStatusApproved)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return errors.New("website proposal publication conflict")
		}
		if published.FAQRevision != nil {
			if published.FAQRevision.RevisionHash != expectedHash {
				return errors.New("website FAQ publication revision hash mismatch")
			}
			encoded, err := json.Marshal(published.FAQRevision)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO website_faq_published_revisions (
				revision_hash, logical_intent_id, proposal_id, faq_revision_json, published_at
			) VALUES (?, ?, ?, ?, ?)`, expectedHash, published.FAQRevision.LogicalIntentID, proposalID, string(encoded), published.PublishedAt.UnixNano()); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO website_knowledge_publication_receipts (receipt_id, proposal_id, revision_hash, actor_id, created_at)
			VALUES (?, ?, ?, ?, ?)`, receiptID("publication", proposalID, expectedHash), proposalID, expectedHash, published.PublishedBy, published.PublishedAt.UnixNano())
		return err
	})
	return published, err
}

func (s *SQLWebsiteGapStore) getProposal(ctx context.Context, proposalID string) (WebsiteKnowledgeProposal, error) {
	return s.GetWebsiteKnowledgeProposal(ctx, proposalID)
}

func (s *SQLWebsiteGapStore) GetWebsiteKnowledgeProposal(ctx context.Context, proposalID string) (WebsiteKnowledgeProposal, error) {
	if s == nil || s.store == nil {
		return WebsiteKnowledgeProposal{}, errors.New("website knowledge proposal store is not configured")
	}
	var value WebsiteKnowledgeProposal
	var sources, faqRevision string
	var created int64
	var approved, published sql.NullInt64
	err := s.store.QueryRow(ctx, `SELECT proposal_id, revision_hash, proposal_kind, gap_id, target_knowledge_key, draft, source_refs_json,
		rationale, faq_revision_json, status, created_by, created_at, approved_by, approved_at, published_by, published_at
		FROM website_knowledge_proposal_revisions WHERE proposal_id = ?`, strings.TrimSpace(proposalID)).Scan(
		&value.ProposalID, &value.RevisionHash, &value.ProposalKind, &value.GapID, &value.TargetKnowledgeKey, &value.Draft, &sources,
		&value.Rationale, &faqRevision, &value.Status, &value.CreatedBy, &created, &value.ApprovedBy, &approved, &value.PublishedBy, &published)
	if err != nil {
		return WebsiteKnowledgeProposal{}, err
	}
	if err := hydrateWebsiteKnowledgeProposal(&value, sources, faqRevision, created, approved, published); err != nil {
		return WebsiteKnowledgeProposal{}, err
	}
	return value, nil
}

func (s *SQLWebsiteGapStore) ListWebsiteKnowledgeProposals(ctx context.Context, status ProposalStatus, limit int) ([]WebsiteKnowledgeProposal, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("website knowledge proposal store is not configured")
	}
	if status != "" && status != ProposalStatusDraft && status != ProposalStatusApproved && status != ProposalStatusPublished {
		return nil, errors.New("website knowledge proposal status is invalid")
	}
	if limit < 1 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	query := `SELECT proposal_id, revision_hash, proposal_kind, gap_id, target_knowledge_key, draft, source_refs_json,
		rationale, faq_revision_json, status, created_by, created_at, approved_by, approved_at, published_by, published_at
		FROM website_knowledge_proposal_revisions`
	args := []any{}
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC, proposal_id ASC LIMIT ?`
	args = append(args, limit)
	rows, err := s.store.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []WebsiteKnowledgeProposal{}
	for rows.Next() {
		value, err := scanWebsiteKnowledgeProposal(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *SQLWebsiteGapStore) ListWebsiteKnowledgeReceipts(ctx context.Context, proposalID string) ([]WebsiteKnowledgeReceipt, error) {
	if s == nil || s.store == nil || strings.TrimSpace(proposalID) == "" {
		return nil, errors.New("website knowledge receipt query is invalid")
	}
	rows, err := s.store.Query(ctx, `SELECT receipt_id, 'approval', proposal_id, revision_hash, actor_id, created_at
		FROM website_knowledge_approval_receipts WHERE proposal_id = ?
		UNION ALL
		SELECT receipt_id, 'publication', proposal_id, revision_hash, actor_id, created_at
		FROM website_knowledge_publication_receipts WHERE proposal_id = ?
		ORDER BY created_at ASC, receipt_id ASC`, proposalID, proposalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []WebsiteKnowledgeReceipt{}
	for rows.Next() {
		var value WebsiteKnowledgeReceipt
		var created int64
		if err := rows.Scan(&value.ReceiptID, &value.Kind, &value.ProposalID, &value.RevisionHash, &value.ActorID, &created); err != nil {
			return nil, err
		}
		value.CreatedAt = time.Unix(0, created).UTC()
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *SQLWebsiteGapStore) ListPublishedFAQRevisions(ctx context.Context) ([]WebsiteFAQRevision, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("website FAQ revision store is not configured")
	}
	rows, err := s.store.Query(ctx, `SELECT logical_intent_id, revision_hash, faq_revision_json
		FROM website_faq_published_revisions ORDER BY logical_intent_id ASC, published_at DESC, revision_hash DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []WebsiteFAQRevision{}
	seen := map[string]struct{}{}
	for rows.Next() {
		var logicalID, revisionHash, encoded string
		if err := rows.Scan(&logicalID, &revisionHash, &encoded); err != nil {
			return nil, err
		}
		if _, exists := seen[logicalID]; exists {
			continue
		}
		var value WebsiteFAQRevision
		if err := json.Unmarshal([]byte(encoded), &value); err != nil {
			return nil, err
		}
		if value.LogicalIntentID != logicalID || value.RevisionHash != revisionHash {
			return nil, errors.New("stored website FAQ revision metadata does not match its payload")
		}
		seen[logicalID] = struct{}{}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *SQLWebsiteGapStore) ListWebsiteFAQMetrics(ctx context.Context, since time.Time) ([]WebsiteFAQMetric, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("website FAQ metrics store is not configured")
	}
	if since.IsZero() {
		since = time.Now().UTC().Add(-websiteGapWindow)
	}
	rows, err := s.store.Query(ctx, `SELECT matched_intent, tier, route, outcome, conversation_fingerprint,
		takeover, source_ids_json, occurred_at FROM website_knowledge_gap_events
		WHERE matched_intent <> '' AND occurred_at >= ?`, since.UTC().UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type metricAccumulator struct {
		metric        WebsiteFAQMetric
		conversations map[string]struct{}
		sources       map[string]struct{}
	}
	byKey := map[string]*metricAccumulator{}
	for rows.Next() {
		var intentID, tier, route, outcome, conversation, sourcesJSON string
		var takeover int
		var occurred int64
		if err := rows.Scan(&intentID, &tier, &route, &outcome, &conversation, &takeover, &sourcesJSON, &occurred); err != nil {
			return nil, err
		}
		intentID = logicalIntentID(intentID)
		key := strings.Join([]string{intentID, tier, route, outcome}, "\x00")
		current := byKey[key]
		if current == nil {
			current = &metricAccumulator{
				metric:        WebsiteFAQMetric{IntentID: intentID, Tier: protocol.AdvisorTier(tier), Route: route, Outcome: GapOutcome(outcome)},
				conversations: map[string]struct{}{}, sources: map[string]struct{}{},
			}
			byKey[key] = current
		}
		current.conversations[conversation] = struct{}{}
		if takeover != 0 {
			current.metric.TakeoverCount++
		}
		when := time.Unix(0, occurred).UTC()
		if when.After(current.metric.LastSeenAt) {
			current.metric.LastSeenAt = when
		}
		var sources []string
		if err := json.Unmarshal([]byte(sourcesJSON), &sources); err != nil {
			return nil, err
		}
		for _, source := range sources {
			current.sources[source] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	values := make([]WebsiteFAQMetric, 0, len(byKey))
	for _, current := range byKey {
		current.metric.ConversationCount = len(current.conversations)
		current.metric.SourceIDs = make([]string, 0, len(current.sources))
		for source := range current.sources {
			current.metric.SourceIDs = append(current.metric.SourceIDs, source)
		}
		sort.Strings(current.metric.SourceIDs)
		values = append(values, current.metric)
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].ConversationCount != values[j].ConversationCount {
			return values[i].ConversationCount > values[j].ConversationCount
		}
		left := strings.Join([]string{values[i].IntentID, string(values[i].Tier), values[i].Route, string(values[i].Outcome)}, "\x00")
		right := strings.Join([]string{values[j].IntentID, string(values[j].Tier), values[j].Route, string(values[j].Outcome)}, "\x00")
		return left < right
	})
	return values, nil
}

type websiteProposalScanner interface {
	Scan(...any) error
}

func scanWebsiteKnowledgeProposal(scanner websiteProposalScanner) (WebsiteKnowledgeProposal, error) {
	var value WebsiteKnowledgeProposal
	var sources, faqRevision string
	var created int64
	var approved, published sql.NullInt64
	if err := scanner.Scan(
		&value.ProposalID, &value.RevisionHash, &value.ProposalKind, &value.GapID, &value.TargetKnowledgeKey, &value.Draft, &sources,
		&value.Rationale, &faqRevision, &value.Status, &value.CreatedBy, &created, &value.ApprovedBy, &approved, &value.PublishedBy, &published,
	); err != nil {
		return WebsiteKnowledgeProposal{}, err
	}
	if err := hydrateWebsiteKnowledgeProposal(&value, sources, faqRevision, created, approved, published); err != nil {
		return WebsiteKnowledgeProposal{}, err
	}
	return value, nil
}

func hydrateWebsiteKnowledgeProposal(value *WebsiteKnowledgeProposal, sources, faqRevision string, created int64, approved, published sql.NullInt64) error {
	if err := json.Unmarshal([]byte(sources), &value.SourceRefs); err != nil {
		return err
	}
	if strings.TrimSpace(faqRevision) != "" {
		value.FAQRevision = &WebsiteFAQRevision{}
		if err := json.Unmarshal([]byte(faqRevision), value.FAQRevision); err != nil {
			return err
		}
	}
	value.CreatedAt = time.Unix(0, created).UTC()
	if approved.Valid {
		value.ApprovedAt = time.Unix(0, approved.Int64).UTC()
	}
	if published.Valid {
		value.PublishedAt = time.Unix(0, published.Int64).UTC()
	}
	return nil
}

func receiptID(kind, proposalID, hash string) string {
	return kind[:3] + "_" + hashWebsiteGapValue(kind, proposalID+"\x00"+hash)[:24]
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func parseMinimumCount(value string) int {
	count, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || count < 1 {
		return 1
	}
	if count > 100 {
		return 100
	}
	return count
}

func websiteGovernanceError(operation string, err error) error {
	return fmt.Errorf("website knowledge %s: %w", operation, err)
}
