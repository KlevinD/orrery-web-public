package website

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func websiteDistillationMigration() migration.Migration {
	return migration.Migration{
		Version: "0191_website_answer_distillation", Description: "create sanitized website answer distillation candidates",
		Statements: websiteDistillationMigrationStatements("INTEGER"),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: websiteDistillationMigrationStatements("BIGINT"),
		},
	}
}

func websiteDistillationMigrationStatements(timeType string) []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS website_distillation_candidates (
			candidate_id TEXT PRIMARY KEY,
			cluster_id TEXT NOT NULL,
			topic_fingerprint TEXT NOT NULL,
			conversation_fingerprint TEXT NOT NULL,
			sanitized_question TEXT NOT NULL,
			sanitized_answer TEXT NOT NULL,
			locale TEXT NOT NULL,
			page_category TEXT NOT NULL DEFAULT '',
			source_ids_json TEXT NOT NULL,
			source_revision TEXT NOT NULL,
			model_revision TEXT NOT NULL,
			prompt_revision TEXT NOT NULL,
			policy_label TEXT NOT NULL,
			feedback TEXT NOT NULL DEFAULT '',
			occurred_at ` + timeType + ` NOT NULL,
			UNIQUE(cluster_id, conversation_fingerprint)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_website_distillation_cluster_time ON website_distillation_candidates (cluster_id, occurred_at)`,
		`CREATE INDEX IF NOT EXISTS idx_website_distillation_feedback_target ON website_distillation_candidates (topic_fingerprint, conversation_fingerprint)`,
	}
}

func (s *SQLWebsiteGapStore) RecordDistillationCandidate(ctx context.Context, candidate WebsiteDistillationCandidate) error {
	if s == nil || s.store == nil || candidate.CandidateID == "" || candidate.ClusterID == "" {
		return errors.New("website distillation store is not configured")
	}
	sources, err := json.Marshal(candidate.SourceIDs)
	if err != nil {
		return err
	}
	_, err = s.store.Exec(ctx, `INSERT INTO website_distillation_candidates (
		candidate_id, cluster_id, topic_fingerprint, conversation_fingerprint, sanitized_question, sanitized_answer,
		locale, page_category, source_ids_json, source_revision, model_revision, prompt_revision, policy_label, feedback, occurred_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(cluster_id, conversation_fingerprint) DO UPDATE SET
		topic_fingerprint = excluded.topic_fingerprint, sanitized_question = excluded.sanitized_question,
		sanitized_answer = excluded.sanitized_answer, locale = excluded.locale, page_category = excluded.page_category,
		source_ids_json = excluded.source_ids_json, source_revision = excluded.source_revision,
		model_revision = excluded.model_revision, prompt_revision = excluded.prompt_revision,
		policy_label = excluded.policy_label, occurred_at = excluded.occurred_at`,
		candidate.CandidateID, candidate.ClusterID, candidate.TopicFingerprint, candidate.ConversationFingerprint,
		candidate.Question, candidate.Answer, candidate.Locale, candidate.PageCategory, string(sources), candidate.SourceRevision,
		candidate.ModelRevision, candidate.PromptRevision, candidate.PolicyLabel, candidate.Feedback, candidate.OccurredAt.UTC().UnixNano())
	return err
}

func (s *SQLWebsiteGapStore) RecordDistillationFeedback(ctx context.Context, topicFingerprint, conversationFingerprint string, feedback GapFeedback) error {
	if s == nil || s.store == nil {
		return errors.New("website distillation store is not configured")
	}
	feedback = normalizeGapFeedback(feedback)
	if topicFingerprint == "" || conversationFingerprint == "" || feedback == "" {
		return errors.New("website distillation feedback is invalid")
	}
	_, err := s.store.Exec(ctx, `UPDATE website_distillation_candidates SET feedback = ?
		WHERE topic_fingerprint = ? AND conversation_fingerprint = ?`, feedback, topicFingerprint, conversationFingerprint)
	return err
}

func (s *SQLWebsiteGapStore) ListDistillationCandidateClusters(ctx context.Context, minimumCount int, now time.Time) ([]WebsiteDistillationCandidateCluster, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("website distillation store is not configured")
	}
	if minimumCount < 1 {
		minimumCount = 1
	}
	if minimumCount > 100 {
		minimumCount = 100
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	rows, err := s.store.Query(ctx, `SELECT cluster_id, conversation_fingerprint, sanitized_question, sanitized_answer,
		locale, source_ids_json, source_revision, feedback, occurred_at FROM website_distillation_candidates
		WHERE occurred_at >= ? ORDER BY occurred_at ASC, candidate_id ASC`, now.UTC().Add(-websiteGapWindow).UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type clusterAccumulator struct {
		cluster       WebsiteDistillationCandidateCluster
		conversations map[string]struct{}
		sources       map[string]struct{}
	}
	clusters := map[string]*clusterAccumulator{}
	for rows.Next() {
		var clusterID, conversation, question, answer, locale, sourcesJSON, sourceRevision, feedback string
		var occurred int64
		if err := rows.Scan(&clusterID, &conversation, &question, &answer, &locale, &sourcesJSON, &sourceRevision, &feedback, &occurred); err != nil {
			return nil, err
		}
		current := clusters[clusterID]
		if current == nil {
			current = &clusterAccumulator{cluster: WebsiteDistillationCandidateCluster{ClusterID: clusterID}, conversations: map[string]struct{}{}, sources: map[string]struct{}{}}
			clusters[clusterID] = current
		}
		if _, exists := current.conversations[conversation]; !exists {
			current.conversations[conversation] = struct{}{}
			switch GapFeedback(feedback) {
			case GapFeedbackResolved:
				current.cluster.ResolvedCount++
			case GapFeedbackUnresolved:
				current.cluster.UnresolvedCount++
			}
		}
		when := time.Unix(0, occurred).UTC()
		if !when.Before(current.cluster.LastSeenAt) {
			current.cluster.Locale = locale
			current.cluster.RepresentativeQuestion = question
			current.cluster.RepresentativeAnswer = answer
			current.cluster.SourceRevision = sourceRevision
			current.cluster.LastSeenAt = when
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
	values := make([]WebsiteDistillationCandidateCluster, 0, len(clusters))
	for _, current := range clusters {
		current.cluster.ConversationCount = len(current.conversations)
		if current.cluster.ConversationCount < minimumCount {
			continue
		}
		for source := range current.sources {
			current.cluster.SourceIDs = append(current.cluster.SourceIDs, source)
		}
		sort.Strings(current.cluster.SourceIDs)
		values = append(values, current.cluster)
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].ConversationCount != values[j].ConversationCount {
			return values[i].ConversationCount > values[j].ConversationCount
		}
		return values[i].ClusterID < values[j].ClusterID
	})
	return values, nil
}

func (s *SQLWebsiteGapStore) DeleteExpiredDistillationCandidates(ctx context.Context, cutoff time.Time) (int, error) {
	if s == nil || s.store == nil {
		return 0, errors.New("website distillation store is not configured")
	}
	if cutoff.IsZero() {
		cutoff = time.Now().UTC().Add(-websiteDistillationRetention)
	}
	result, err := s.store.Exec(ctx, `DELETE FROM website_distillation_candidates WHERE occurred_at < ?`, cutoff.UTC().UnixNano())
	if err != nil {
		return 0, err
	}
	removed, err := result.RowsAffected()
	return int(removed), err
}
