package ontology

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/platform/db"
)

type MemoryMetadataProposalStore struct {
	mu        sync.RWMutex
	proposals map[string]OntologyMetadataProposal
	now       func() time.Time
}

func NewMemoryMetadataProposalStore() *MemoryMetadataProposalStore {
	return &MemoryMetadataProposalStore{
		proposals: map[string]OntologyMetadataProposal{},
		now:       time.Now,
	}
}

func (s *MemoryMetadataProposalStore) SaveMetadataProposal(ctx context.Context, proposal OntologyMetadataProposal) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	normalized, err := normalizeMetadataProposal(proposal, s.now)
	if err != nil {
		return err
	}
	normalized.Revision = 1
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, exists := s.proposals[normalized.ID]
	if exists {
		if existing.Status != MetadataProposalStatusProposed || existing.ContentHash == normalized.ContentHash {
			return nil
		}
		return fmt.Errorf("%w: metadata proposal %s content update requires expected revision", ErrVersionConflict, normalized.ID)
	}
	s.proposals[normalized.ID] = normalized
	return nil
}

func (s *MemoryMetadataProposalStore) SaveMetadataProposalWithExpectedRevision(ctx context.Context, proposal OntologyMetadataProposal, expectedRevision int64) (OntologyMetadataProposal, error) {
	if err := ctx.Err(); err != nil {
		return OntologyMetadataProposal{}, err
	}
	if expectedRevision <= 0 {
		return OntologyMetadataProposal{}, fmt.Errorf("%w: metadata proposal expected revision must be positive", ErrInvalidDefinition)
	}
	normalized, err := normalizeMetadataProposal(proposal, s.now)
	if err != nil {
		return OntologyMetadataProposal{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.proposals[normalized.ID]
	if !ok {
		return OntologyMetadataProposal{}, fmt.Errorf("%w: metadata proposal %s", ErrNotFound, normalized.ID)
	}
	if existing.Status != MetadataProposalStatusProposed || existing.Revision != expectedRevision {
		return OntologyMetadataProposal{}, fmt.Errorf("%w: metadata proposal %s expected revision %d", ErrVersionConflict, normalized.ID, expectedRevision)
	}
	if err := validateMetadataProposalImmutableIdentity(existing, normalized); err != nil {
		return OntologyMetadataProposal{}, err
	}
	if existing.ContentHash == normalized.ContentHash {
		return cloneMetadataProposal(existing), nil
	}
	normalized.Status = existing.Status
	normalized.PreviousStatus = existing.PreviousStatus
	normalized.ReviewedBy = existing.ReviewedBy
	normalized.ReviewedByKind = existing.ReviewedByKind
	normalized.ReviewedAt = existing.ReviewedAt
	normalized.ReviewReason = existing.ReviewReason
	normalized.ReviewEvidenceRefs = cloneMetadataEvidenceRefs(existing.ReviewEvidenceRefs)
	normalized.CreatedAt = existing.CreatedAt
	normalized.Revision = expectedRevision + 1
	s.proposals[normalized.ID] = normalized
	return cloneMetadataProposal(normalized), nil
}

func (s *MemoryMetadataProposalStore) ListMetadataProposalPage(ctx context.Context, query MetadataProposalQuery) (MetadataProposalPage, error) {
	if err := ctx.Err(); err != nil {
		return MetadataProposalPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	matched := make([]OntologyMetadataProposal, 0, len(s.proposals))
	for _, proposal := range s.proposals {
		if !metadataProposalMatches(proposal, query) {
			continue
		}
		matched = append(matched, cloneMetadataProposal(proposal))
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].CreatedAt.Equal(matched[j].CreatedAt) {
			return matched[i].ID < matched[j].ID
		}
		return matched[i].CreatedAt.Before(matched[j].CreatedAt)
	})
	return metadataProposalPage(matched, query), nil
}

func (s *MemoryMetadataProposalStore) DecideMetadataProposal(ctx context.Context, decision MetadataProposalDecision) (OntologyMetadataProposal, error) {
	if err := ctx.Err(); err != nil {
		return OntologyMetadataProposal{}, err
	}
	normalized, err := normalizeMetadataProposalDecision(decision, s.now)
	if err != nil {
		return OntologyMetadataProposal{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	proposal, ok := s.proposals[normalized.ProposalID]
	if !ok {
		return OntologyMetadataProposal{}, fmt.Errorf("%w: metadata proposal %s", ErrNotFound, normalized.ProposalID)
	}
	decided, err := applyMetadataProposalDecision(proposal, normalized, s.now)
	if err != nil {
		return OntologyMetadataProposal{}, err
	}
	s.proposals[decided.ID] = decided
	return cloneMetadataProposal(decided), nil
}

type PlatformMetadataProposalStore struct {
	store *db.Store
	tx    db.Tx
	now   func() time.Time
}

func NewPlatformMetadataProposalStore(store *db.Store) *PlatformMetadataProposalStore {
	return &PlatformMetadataProposalStore{store: store, now: time.Now}
}

func NewPlatformMetadataProposalStoreTx(tx db.Tx) *PlatformMetadataProposalStore {
	return &PlatformMetadataProposalStore{tx: tx, now: time.Now}
}

func (s *PlatformMetadataProposalStore) ready() error {
	if s == nil || (s.store == nil && s.tx == nil) {
		return fmt.Errorf("%w: store is required", ErrInvalidDefinition)
	}
	return nil
}

func (s *PlatformMetadataProposalStore) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if s.tx != nil {
		return s.tx.ExecContext(ctx, query, args...)
	}
	return s.store.Exec(ctx, query, args...)
}

func (s *PlatformMetadataProposalStore) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if s.tx != nil {
		return s.tx.QueryContext(ctx, query, args...)
	}
	return s.store.Query(ctx, query, args...)
}

func (s *PlatformMetadataProposalStore) queryRow(ctx context.Context, query string, args ...any) rowScanner {
	if s.tx != nil {
		return s.tx.QueryRowContext(ctx, query, args...)
	}
	return s.store.QueryRow(ctx, query, args...)
}

func (s *PlatformMetadataProposalStore) SaveMetadataProposal(ctx context.Context, proposal OntologyMetadataProposal) error {
	if err := s.ready(); err != nil {
		return err
	}
	normalized, err := normalizeMetadataProposal(proposal, s.now)
	if err != nil {
		return err
	}
	normalized.Revision = 1
	result, err := s.exec(ctx, `
INSERT INTO ontology_metadata_proposals (
	id, tenant_id, proposal_revision, proposal_content_hash, target_kind, target_id, metadata_key, proposed_value,
	source, source_kind, source_ref, source_version, confidence, status, reason, evidence_refs_json, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO NOTHING`,
		normalized.ID,
		normalized.TenantID,
		normalized.Revision,
		normalized.ContentHash,
		normalized.TargetKind,
		normalized.TargetID,
		normalized.MetadataKey,
		normalized.ProposedValue,
		normalized.Source,
		normalized.SourceKind,
		normalized.SourceRef,
		normalized.SourceVersion,
		normalized.Confidence,
		normalized.Status,
		normalized.Reason,
		mustJSON(normalized.EvidenceRefs),
		timeToUnixNano(normalized.CreatedAt),
		timeToUnixNano(normalized.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("save ontology metadata proposal: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected > 0 {
		return nil
	}
	existing, err := s.metadataProposalByIDAndTenant(ctx, normalized.ID, normalized.TenantID)
	if err != nil {
		return err
	}
	if existing.Status != MetadataProposalStatusProposed || existing.ContentHash == normalized.ContentHash {
		return nil
	}
	return fmt.Errorf("%w: metadata proposal %s content update requires expected revision", ErrVersionConflict, normalized.ID)
}

func (s *PlatformMetadataProposalStore) SaveMetadataProposalWithExpectedRevision(ctx context.Context, proposal OntologyMetadataProposal, expectedRevision int64) (OntologyMetadataProposal, error) {
	if err := s.ready(); err != nil {
		return OntologyMetadataProposal{}, err
	}
	if expectedRevision <= 0 {
		return OntologyMetadataProposal{}, fmt.Errorf("%w: metadata proposal expected revision must be positive", ErrInvalidDefinition)
	}
	normalized, err := normalizeMetadataProposal(proposal, s.now)
	if err != nil {
		return OntologyMetadataProposal{}, err
	}
	existing, err := s.metadataProposalByIDAndTenant(ctx, normalized.ID, normalized.TenantID)
	if err != nil {
		return OntologyMetadataProposal{}, err
	}
	if existing.Status != MetadataProposalStatusProposed || existing.Revision != expectedRevision {
		return OntologyMetadataProposal{}, fmt.Errorf("%w: metadata proposal %s expected revision %d", ErrVersionConflict, normalized.ID, expectedRevision)
	}
	if err := validateMetadataProposalImmutableIdentity(existing, normalized); err != nil {
		return OntologyMetadataProposal{}, err
	}
	if existing.ContentHash == normalized.ContentHash {
		return existing, nil
	}
	result, err := s.exec(ctx, `
UPDATE ontology_metadata_proposals
SET proposal_revision = ?,
	proposal_content_hash = ?,
	source = ?,
	source_kind = ?,
	source_ref = ?,
	source_version = ?,
	confidence = ?,
	reason = ?,
	evidence_refs_json = ?,
	updated_at = ?
WHERE id = ? AND tenant_id = ? AND status = ? AND proposal_revision = ?`,
		expectedRevision+1,
		normalized.ContentHash,
		normalized.Source,
		normalized.SourceKind,
		normalized.SourceRef,
		normalized.SourceVersion,
		normalized.Confidence,
		normalized.Reason,
		mustJSON(normalized.EvidenceRefs),
		timeToUnixNano(normalized.UpdatedAt),
		normalized.ID,
		existing.TenantID,
		MetadataProposalStatusProposed,
		expectedRevision,
	)
	if err != nil {
		return OntologyMetadataProposal{}, fmt.Errorf("update ontology metadata proposal content: %w", err)
	}
	if err := requireMetadataProposalDecisionRowsAffected(result, normalized.ID); err != nil {
		return OntologyMetadataProposal{}, err
	}
	return s.metadataProposalByIDAndTenant(ctx, normalized.ID, normalized.TenantID)
}

func (s *PlatformMetadataProposalStore) ListMetadataProposalPage(ctx context.Context, query MetadataProposalQuery) (MetadataProposalPage, error) {
	if err := s.ready(); err != nil {
		return MetadataProposalPage{}, err
	}
	limit := normalizeMetadataProposalLimit(query.Limit)
	offset := normalizeMetadataProposalOffset(query.Offset)
	where, args := metadataProposalWhere(query)
	countQuery := `SELECT COUNT(*) FROM ontology_metadata_proposals` + where
	var total int
	if err := s.queryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return MetadataProposalPage{}, fmt.Errorf("count ontology metadata proposals: %w", err)
	}
	selectQuery := metadataProposalSelectSQL() + where + ` ORDER BY created_at ASC, id ASC LIMIT ? OFFSET ?`
	selectArgs := append(args, limit, offset)
	rows, err := s.query(ctx, selectQuery, selectArgs...)
	if err != nil {
		return MetadataProposalPage{}, fmt.Errorf("list ontology metadata proposals: %w", err)
	}
	defer rows.Close()
	proposals := []OntologyMetadataProposal{}
	for rows.Next() {
		proposal, err := scanMetadataProposal(rows)
		if err != nil {
			return MetadataProposalPage{}, err
		}
		proposals = append(proposals, proposal)
	}
	if err := rows.Err(); err != nil {
		return MetadataProposalPage{}, err
	}
	return MetadataProposalPage{
		Proposals: proposals,
		Page: Page{
			Limit:    limit,
			Offset:   offset,
			Total:    total,
			Returned: len(proposals),
			HasMore:  offset+len(proposals) < total,
		},
	}, nil
}

func (s *PlatformMetadataProposalStore) DecideMetadataProposal(ctx context.Context, decision MetadataProposalDecision) (OntologyMetadataProposal, error) {
	if err := s.ready(); err != nil {
		return OntologyMetadataProposal{}, err
	}
	return s.decideMetadataProposal(ctx, decision.TenantID, decision)
}

func (s *PlatformMetadataProposalStore) DecideMetadataProposalForTenant(ctx context.Context, tenantID string, decision MetadataProposalDecision) (OntologyMetadataProposal, error) {
	if err := s.ready(); err != nil {
		return OntologyMetadataProposal{}, err
	}
	return s.decideMetadataProposal(ctx, tenantID, decision)
}

func (s *PlatformMetadataProposalStore) decideMetadataProposal(ctx context.Context, tenantID string, decision MetadataProposalDecision) (OntologyMetadataProposal, error) {
	normalized, err := normalizeMetadataProposalDecision(decision, s.now)
	if err != nil {
		return OntologyMetadataProposal{}, err
	}
	proposal, err := s.metadataProposalByIDForTenant(ctx, tenantID, normalized.ProposalID)
	if err != nil {
		return OntologyMetadataProposal{}, err
	}
	decided, err := applyMetadataProposalDecision(proposal, normalized, s.now)
	if err != nil {
		return OntologyMetadataProposal{}, err
	}
	where, args := metadataProposalIdentityWhere(tenantID, decided.ID)
	updateArgs := []any{
		decided.Status,
		decided.PreviousStatus,
		decided.Revision,
		decided.ReviewedBy,
		decided.ReviewedByKind,
		timeToUnixNano(decided.ReviewedAt),
		decided.ReviewReason,
		mustJSON(decided.ReviewEvidenceRefs),
		timeToUnixNano(decided.UpdatedAt),
	}
	updateArgs = append(updateArgs, args...)
	result, err := s.exec(ctx, `
UPDATE ontology_metadata_proposals
SET status = ?,
	previous_status = ?,
	proposal_revision = ?,
	reviewed_by = ?,
	reviewed_by_kind = ?,
	reviewed_at = ?,
	review_reason = ?,
	review_evidence_refs_json = ?,
	updated_at = ?
	`+where+` AND proposal_revision = ?`, append(updateArgs, normalized.ExpectedRevision)...)
	if err != nil {
		return OntologyMetadataProposal{}, fmt.Errorf("decide ontology metadata proposal: %w", err)
	}
	if err := requireMetadataProposalDecisionRowsAffected(result, decided.ID); err != nil {
		return OntologyMetadataProposal{}, err
	}
	return decided, nil
}

func (s *PlatformMetadataProposalStore) GetMetadataProposalForTenant(ctx context.Context, tenantID string, proposalID string) (OntologyMetadataProposal, error) {
	if err := s.ready(); err != nil {
		return OntologyMetadataProposal{}, err
	}
	return s.metadataProposalByIDForTenant(ctx, tenantID, proposalID)
}

func (s *PlatformMetadataProposalStore) metadataProposalByIDAndTenant(ctx context.Context, proposalID string, tenantID string) (OntologyMetadataProposal, error) {
	var proposal OntologyMetadataProposal
	row := s.queryRow(ctx, metadataProposalSelectSQL()+` WHERE id = ? AND tenant_id = ?`, strings.TrimSpace(proposalID), strings.TrimSpace(tenantID))
	proposal, err := scanMetadataProposal(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return OntologyMetadataProposal{}, fmt.Errorf("%w: metadata proposal %s", ErrNotFound, proposalID)
		}
		return OntologyMetadataProposal{}, err
	}
	return proposal, nil
}

func (s *PlatformMetadataProposalStore) metadataProposalByIDForTenant(ctx context.Context, tenantID string, proposalID string) (OntologyMetadataProposal, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return OntologyMetadataProposal{}, fmt.Errorf("%w: tenant_id is required", ErrInvalidDefinition)
	}
	return s.metadataProposalByIDAndTenant(ctx, proposalID, tenantID)
}

func metadataProposalIdentityWhere(tenantID string, proposalID string) (string, []any) {
	tenantID = strings.TrimSpace(tenantID)
	return "WHERE id = ? AND tenant_id = ?", []any{strings.TrimSpace(proposalID), tenantID}
}

func requireMetadataProposalDecisionRowsAffected(result sql.Result, proposalID string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("%w: metadata proposal %s decision was not persisted", ErrVersionConflict, strings.TrimSpace(proposalID))
	}
	return nil
}

func normalizeMetadataProposal(proposal OntologyMetadataProposal, now func() time.Time) (OntologyMetadataProposal, error) {
	proposal.ID = strings.TrimSpace(proposal.ID)
	proposal.TenantID = strings.TrimSpace(proposal.TenantID)
	proposal.TargetKind = normalizeID(proposal.TargetKind)
	proposal.TargetID = normalizeID(proposal.TargetID)
	proposal.MetadataKey = normalizeID(proposal.MetadataKey)
	proposal.ProposedValue = strings.TrimSpace(proposal.ProposedValue)
	proposal.Source = strings.TrimSpace(proposal.Source)
	proposal.SourceKind = normalizeMetadataProposalSourceKind(proposal.SourceKind)
	proposal.SourceRef = strings.TrimSpace(proposal.SourceRef)
	proposal.SourceVersion = strings.TrimSpace(proposal.SourceVersion)
	proposal.Reason = strings.TrimSpace(proposal.Reason)
	proposal.ReviewedBy = strings.TrimSpace(proposal.ReviewedBy)
	proposal.ReviewedByKind = strings.TrimSpace(proposal.ReviewedByKind)
	proposal.ReviewReason = strings.TrimSpace(proposal.ReviewReason)
	if proposal.Status == "" {
		proposal.Status = MetadataProposalStatusProposed
	}
	if proposal.Revision <= 0 {
		proposal.Revision = 1
	}
	if proposal.ID == "" {
		proposal.ID = metadataProposalID(proposal.TenantID, proposal.TargetKind, proposal.TargetID, proposal.MetadataKey, proposal.ProposedValue)
	}
	if proposal.TargetKind == "" || proposal.TargetID == "" || proposal.MetadataKey == "" || proposal.ProposedValue == "" || proposal.Source == "" {
		return OntologyMetadataProposal{}, fmt.Errorf("%w: metadata proposal target, key, value, and source are required", ErrInvalidDefinition)
	}
	proposal = fillMetadataProposalSourceTaxonomy(proposal)
	if math.IsNaN(proposal.Confidence) || math.IsInf(proposal.Confidence, 0) {
		return OntologyMetadataProposal{}, fmt.Errorf("%w: metadata proposal confidence must be finite", ErrInvalidDefinition)
	}
	if proposal.Confidence < 0 {
		proposal.Confidence = 0
	}
	if proposal.Confidence == 0 && proposal.SourceKind != MetadataProposalSourceModelInference {
		proposal.Confidence = 1
	}
	if proposal.Confidence > 1 {
		proposal.Confidence = 1
	}
	if now == nil {
		now = time.Now
	}
	at := now().UTC()
	if proposal.CreatedAt.IsZero() {
		proposal.CreatedAt = at
	}
	proposal.UpdatedAt = at
	proposal.EvidenceRefs = cloneMetadataEvidenceRefs(proposal.EvidenceRefs)
	proposal.ReviewEvidenceRefs = cloneMetadataEvidenceRefs(proposal.ReviewEvidenceRefs)
	proposal.ContentHash = CanonicalMetadataProposalContentHash(proposal)
	return proposal, nil
}

func normalizeMetadataProposalDecision(decision MetadataProposalDecision, now func() time.Time) (MetadataProposalDecision, error) {
	if now == nil {
		now = time.Now
	}
	decision.ProposalID = strings.TrimSpace(decision.ProposalID)
	decision.ActorID = strings.TrimSpace(decision.ActorID)
	decision.ActorKind = strings.TrimSpace(decision.ActorKind)
	decision.Reason = strings.TrimSpace(decision.Reason)
	if decision.ProposalID == "" || decision.ActorID == "" || decision.Reason == "" || decision.ExpectedRevision <= 0 {
		return MetadataProposalDecision{}, fmt.Errorf("%w: proposal_id, actor_id, reason, and positive expected_revision are required", ErrInvalidDefinition)
	}
	switch decision.Status {
	case MetadataProposalStatusAccepted, MetadataProposalStatusRejected:
	default:
		return MetadataProposalDecision{}, fmt.Errorf("%w: metadata proposal decision status must be accepted or rejected", ErrInvalidDefinition)
	}
	decision.EvidenceRefs = cloneMetadataEvidenceRefs(decision.EvidenceRefs)
	return decision, nil
}

func applyMetadataProposalDecision(proposal OntologyMetadataProposal, decision MetadataProposalDecision, now func() time.Time) (OntologyMetadataProposal, error) {
	if proposal.Revision != decision.ExpectedRevision {
		return OntologyMetadataProposal{}, fmt.Errorf("%w: metadata proposal %s expected revision %d, current revision %d", ErrVersionConflict, proposal.ID, decision.ExpectedRevision, proposal.Revision)
	}
	if proposal.Status != MetadataProposalStatusProposed {
		return OntologyMetadataProposal{}, fmt.Errorf("%w: metadata proposal %s has already been decided", ErrInvalidDefinition, proposal.ID)
	}
	if decision.Status == MetadataProposalStatusAccepted {
		if err := validateMetadataProposalAcceptance(proposal); err != nil {
			return OntologyMetadataProposal{}, err
		}
	}
	if now == nil {
		now = time.Now
	}
	if proposal.UpdatedAt.IsZero() {
		proposal.UpdatedAt = proposal.CreatedAt
	}
	decidedAt := now().UTC()
	proposal.PreviousStatus = proposal.Status
	proposal.Status = decision.Status
	proposal.ReviewedBy = decision.ActorID
	proposal.ReviewedByKind = decision.ActorKind
	proposal.ReviewReason = decision.Reason
	proposal.ReviewEvidenceRefs = cloneMetadataEvidenceRefs(decision.EvidenceRefs)
	proposal.ReviewedAt = decidedAt
	proposal.UpdatedAt = decidedAt
	proposal.Revision++
	return proposal, nil
}

func validateMetadataProposalAcceptance(proposal OntologyMetadataProposal) error {
	if issue, ok := firstBlockingMetadataProposalIssueForProposal(proposal); ok {
		return fmt.Errorf("%w: metadata proposal %s has blocking analysis issue %s", ErrInvalidDefinition, proposal.ID, issue.Code)
	}
	return nil
}

func metadataProposalMatches(proposal OntologyMetadataProposal, query MetadataProposalQuery) bool {
	if strings.TrimSpace(query.TenantID) != "" && proposal.TenantID != strings.TrimSpace(query.TenantID) {
		return false
	}
	if normalizeID(query.TargetKind) != "" && proposal.TargetKind != normalizeID(query.TargetKind) {
		return false
	}
	if normalizeID(query.TargetID) != "" && proposal.TargetID != normalizeID(query.TargetID) {
		return false
	}
	if normalizeID(query.MetadataKey) != "" && proposal.MetadataKey != normalizeID(query.MetadataKey) {
		return false
	}
	if strings.TrimSpace(query.Status) != "" && string(proposal.Status) != strings.TrimSpace(query.Status) {
		return false
	}
	if normalizeMetadataProposalSourceKind(query.SourceKind) != "" && proposal.SourceKind != normalizeMetadataProposalSourceKind(query.SourceKind) {
		return false
	}
	return true
}

func metadataProposalPage(proposals []OntologyMetadataProposal, query MetadataProposalQuery) MetadataProposalPage {
	limit := normalizeMetadataProposalLimit(query.Limit)
	offset := normalizeMetadataProposalOffset(query.Offset)
	total := len(proposals)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	items := append([]OntologyMetadataProposal(nil), proposals[offset:end]...)
	return MetadataProposalPage{
		Proposals: items,
		Page: Page{
			Limit:    limit,
			Offset:   offset,
			Total:    total,
			Returned: len(items),
			HasMore:  end < total,
		},
	}
}

func normalizeMetadataProposalLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func normalizeMetadataProposalOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}

func cloneMetadataProposal(src OntologyMetadataProposal) OntologyMetadataProposal {
	src.EvidenceRefs = cloneMetadataEvidenceRefs(src.EvidenceRefs)
	src.ReviewEvidenceRefs = cloneMetadataEvidenceRefs(src.ReviewEvidenceRefs)
	return src
}

func cloneMetadataEvidenceRefs(src []MetadataEvidenceRef) []MetadataEvidenceRef {
	out := make([]MetadataEvidenceRef, 0, len(src))
	for _, ref := range src {
		cloned := MetadataEvidenceRef{
			Kind: strings.TrimSpace(ref.Kind),
			ID:   strings.TrimSpace(ref.ID),
		}
		if len(ref.Metadata) > 0 {
			cloned.Metadata = map[string]string{}
			keys := make([]string, 0, len(ref.Metadata))
			for key := range ref.Metadata {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, originalKey := range keys {
				key := strings.TrimSpace(originalKey)
				if key == "" {
					continue
				}
				cloned.Metadata[key] = strings.TrimSpace(ref.Metadata[originalKey])
			}
		}
		out = append(out, cloned)
	}
	return out
}

func metadataProposalWhere(query MetadataProposalQuery) (string, []any) {
	clauses := []string{}
	args := []any{}
	if strings.TrimSpace(query.TenantID) != "" {
		clauses = append(clauses, "tenant_id = ?")
		args = append(args, strings.TrimSpace(query.TenantID))
	}
	if normalizeID(query.TargetKind) != "" {
		clauses = append(clauses, "target_kind = ?")
		args = append(args, normalizeID(query.TargetKind))
	}
	if normalizeID(query.TargetID) != "" {
		clauses = append(clauses, "target_id = ?")
		args = append(args, normalizeID(query.TargetID))
	}
	if normalizeID(query.MetadataKey) != "" {
		clauses = append(clauses, "metadata_key = ?")
		args = append(args, normalizeID(query.MetadataKey))
	}
	if normalizeMetadataProposalSourceKind(query.SourceKind) != "" {
		clauses = append(clauses, "source_kind = ?")
		args = append(args, normalizeMetadataProposalSourceKind(query.SourceKind))
	}
	if strings.TrimSpace(query.Status) != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, strings.TrimSpace(query.Status))
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func scanMetadataProposal(row rowScanner) (OntologyMetadataProposal, error) {
	var proposal OntologyMetadataProposal
	var evidenceJSON, reviewEvidenceJSON string
	var reviewedAt, createdAt, updatedAt int64
	if err := row.Scan(&proposal.ID, &proposal.TenantID, &proposal.Revision, &proposal.ContentHash, &proposal.TargetKind, &proposal.TargetID, &proposal.MetadataKey, &proposal.ProposedValue, &proposal.Source, &proposal.SourceKind, &proposal.SourceRef, &proposal.SourceVersion, &proposal.Confidence, &proposal.Status, &proposal.PreviousStatus, &proposal.Reason, &evidenceJSON, &proposal.ReviewedBy, &proposal.ReviewedByKind, &reviewedAt, &proposal.ReviewReason, &reviewEvidenceJSON, &createdAt, &updatedAt); err != nil {
		if err == sql.ErrNoRows {
			return OntologyMetadataProposal{}, err
		}
		return OntologyMetadataProposal{}, err
	}
	mustDecodeJSON(evidenceJSON, &proposal.EvidenceRefs)
	mustDecodeJSON(reviewEvidenceJSON, &proposal.ReviewEvidenceRefs)
	proposal.ReviewedAt = unixNanoToTime(reviewedAt)
	proposal.CreatedAt = unixNanoToTime(createdAt)
	proposal.UpdatedAt = unixNanoToTime(updatedAt)
	proposal = fillMetadataProposalSourceTaxonomy(proposal)
	return proposal, nil
}

func metadataProposalSelectSQL() string {
	return `SELECT id, tenant_id, proposal_revision, proposal_content_hash, target_kind, target_id, metadata_key, proposed_value,
source, source_kind, source_ref, source_version, confidence, status, previous_status, reason, evidence_refs_json, reviewed_by, reviewed_by_kind,
reviewed_at, review_reason, review_evidence_refs_json, created_at, updated_at
FROM ontology_metadata_proposals`
}

func CanonicalMetadataProposalContentHash(proposal OntologyMetadataProposal) string {
	proposal.TenantID = strings.TrimSpace(proposal.TenantID)
	proposal.TargetKind = normalizeID(proposal.TargetKind)
	proposal.TargetID = normalizeID(proposal.TargetID)
	proposal.MetadataKey = normalizeID(proposal.MetadataKey)
	proposal.ProposedValue = strings.TrimSpace(proposal.ProposedValue)
	proposal.Source = strings.TrimSpace(proposal.Source)
	proposal.SourceKind = normalizeMetadataProposalSourceKind(proposal.SourceKind)
	proposal.SourceRef = strings.TrimSpace(proposal.SourceRef)
	proposal.SourceVersion = strings.TrimSpace(proposal.SourceVersion)
	proposal.Reason = strings.TrimSpace(proposal.Reason)
	proposal = fillMetadataProposalSourceTaxonomy(proposal)
	if proposal.Confidence < 0 {
		proposal.Confidence = 0
	}
	if proposal.Confidence == 0 && proposal.SourceKind != MetadataProposalSourceModelInference {
		proposal.Confidence = 1
	}
	if proposal.Confidence > 1 {
		proposal.Confidence = 1
	}
	evidence := cloneMetadataEvidenceRefs(proposal.EvidenceRefs)
	sort.SliceStable(evidence, func(i, j int) bool {
		if evidence[i].Kind != evidence[j].Kind {
			return evidence[i].Kind < evidence[j].Kind
		}
		if evidence[i].ID != evidence[j].ID {
			return evidence[i].ID < evidence[j].ID
		}
		return mustJSON(evidence[i].Metadata) < mustJSON(evidence[j].Metadata)
	})
	hash := hashMappingRule(map[string]any{
		"tenant_id":      proposal.TenantID,
		"target_kind":    proposal.TargetKind,
		"target_id":      proposal.TargetID,
		"metadata_key":   proposal.MetadataKey,
		"proposed_value": proposal.ProposedValue,
		"source":         proposal.Source,
		"source_kind":    proposal.SourceKind,
		"source_ref":     proposal.SourceRef,
		"source_version": proposal.SourceVersion,
		"confidence":     proposal.Confidence,
		"reason":         proposal.Reason,
		"evidence_refs":  evidence,
	})
	if hash == "" {
		return ""
	}
	return "sha256:" + hash
}

func validateMetadataProposalImmutableIdentity(existing OntologyMetadataProposal, candidate OntologyMetadataProposal) error {
	if strings.TrimSpace(existing.TenantID) != strings.TrimSpace(candidate.TenantID) ||
		normalizeID(existing.TargetKind) != normalizeID(candidate.TargetKind) ||
		normalizeID(existing.TargetID) != normalizeID(candidate.TargetID) ||
		normalizeID(existing.MetadataKey) != normalizeID(candidate.MetadataKey) ||
		strings.TrimSpace(existing.ProposedValue) != strings.TrimSpace(candidate.ProposedValue) {
		return fmt.Errorf("%w: metadata proposal %s immutable identity cannot be changed", ErrInvalidDefinition, existing.ID)
	}
	return nil
}

func fillMetadataProposalSourceTaxonomy(proposal OntologyMetadataProposal) OntologyMetadataProposal {
	if proposal.SourceKind == "" {
		proposal.SourceKind = inferMetadataProposalSourceKind(proposal.Source)
	}
	if proposal.SourceRef == "" {
		proposal.SourceRef = inferMetadataProposalSourceRef(proposal.Source, proposal.SourceKind)
	}
	if proposal.SourceVersion == "" && proposal.SourceKind == MetadataProposalSourceDeterministic {
		proposal.SourceVersion = "v1"
	}
	return proposal
}

func normalizeMetadataProposalSourceKind(kind MetadataProposalSourceKind) MetadataProposalSourceKind {
	switch MetadataProposalSourceKind(normalizeID(string(kind))) {
	case MetadataProposalSourceDeterministic:
		return MetadataProposalSourceDeterministic
	case MetadataProposalSourceModelInference:
		return MetadataProposalSourceModelInference
	case MetadataProposalSourceOperatorSuggested:
		return MetadataProposalSourceOperatorSuggested
	case MetadataProposalSourceMigration:
		return MetadataProposalSourceMigration
	default:
		return ""
	}
}

func inferMetadataProposalSourceKind(source string) MetadataProposalSourceKind {
	prefix := source
	if before, _, ok := strings.Cut(source, "."); ok {
		prefix = before
	}
	return normalizeMetadataProposalSourceKind(MetadataProposalSourceKind(prefix))
}

func inferMetadataProposalSourceRef(source string, kind MetadataProposalSourceKind) string {
	source = strings.TrimSpace(source)
	kind = normalizeMetadataProposalSourceKind(kind)
	if source == "" || kind == "" {
		return ""
	}
	prefix := string(kind) + "."
	if strings.HasPrefix(source, prefix) {
		return strings.TrimSpace(strings.TrimPrefix(source, prefix))
	}
	return ""
}
