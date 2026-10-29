package ontology

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const metadataGovernanceQueryMaxLimit = 100

type MetadataProposalSeek struct {
	UpdatedAt time.Time
	ID        string
}

type MetadataProposalCursorQuery struct {
	TenantID    string
	Status      MetadataProposalStatus
	TargetKind  string
	TargetID    string
	MetadataKey string
	Search      string
	Limit       int
	After       MetadataProposalSeek
}

type MetadataProposalCursorPage struct {
	Proposals []OntologyMetadataProposal
	HasMore   bool
}

func (s *MemoryMetadataProposalStore) ListMetadataProposalCursorPage(ctx context.Context, query MetadataProposalCursorQuery) (MetadataProposalCursorPage, error) {
	query, err := normalizeMetadataProposalCursorQuery(query)
	if err != nil {
		return MetadataProposalCursorPage{}, err
	}
	if err := ctx.Err(); err != nil {
		return MetadataProposalCursorPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	matched := make([]OntologyMetadataProposal, 0, len(s.proposals))
	for _, proposal := range s.proposals {
		if !metadataProposalCursorMatches(proposal, query) || !metadataProposalAfterSeek(proposal, query.After) {
			continue
		}
		matched = append(matched, cloneMetadataProposal(proposal))
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].UpdatedAt.Equal(matched[j].UpdatedAt) {
			return matched[i].ID < matched[j].ID
		}
		return matched[i].UpdatedAt.After(matched[j].UpdatedAt)
	})
	hasMore := len(matched) > query.Limit
	if hasMore {
		matched = matched[:query.Limit]
	}
	return MetadataProposalCursorPage{Proposals: matched, HasMore: hasMore}, nil
}

func (s *PlatformMetadataProposalStore) ListMetadataProposalCursorPage(ctx context.Context, query MetadataProposalCursorQuery) (MetadataProposalCursorPage, error) {
	if err := s.ready(); err != nil {
		return MetadataProposalCursorPage{}, err
	}
	query, err := normalizeMetadataProposalCursorQuery(query)
	if err != nil {
		return MetadataProposalCursorPage{}, err
	}
	where, args := metadataProposalCursorWhere(query)
	args = append(args, query.Limit+1)
	rows, err := s.query(ctx, metadataProposalSelectSQL()+where+` ORDER BY updated_at DESC, id ASC LIMIT ?`, args...)
	if err != nil {
		return MetadataProposalCursorPage{}, fmt.Errorf("list ontology metadata proposal cursor page: %w", err)
	}
	defer rows.Close()
	proposals := make([]OntologyMetadataProposal, 0, query.Limit+1)
	for rows.Next() {
		proposal, err := scanMetadataProposal(rows)
		if err != nil {
			return MetadataProposalCursorPage{}, err
		}
		proposals = append(proposals, proposal)
	}
	if err := rows.Err(); err != nil {
		return MetadataProposalCursorPage{}, err
	}
	hasMore := len(proposals) > query.Limit
	if hasMore {
		proposals = proposals[:query.Limit]
	}
	return MetadataProposalCursorPage{Proposals: proposals, HasMore: hasMore}, nil
}

func normalizeMetadataProposalCursorQuery(query MetadataProposalCursorQuery) (MetadataProposalCursorQuery, error) {
	query.TenantID = strings.TrimSpace(query.TenantID)
	query.TargetKind = normalizeID(query.TargetKind)
	query.TargetID = normalizeID(query.TargetID)
	query.MetadataKey = normalizeID(query.MetadataKey)
	query.Search = strings.TrimSpace(query.Search)
	query.After.ID = strings.TrimSpace(query.After.ID)
	if query.TenantID == "" || len(query.TenantID) > 256 || len(query.TargetKind) > 128 || len(query.TargetID) > 256 || len(query.MetadataKey) > 128 || len(query.After.ID) > 256 || query.Limit <= 0 || query.Limit > metadataGovernanceQueryMaxLimit || utf8.RuneCountInString(query.Search) > 128 {
		return MetadataProposalCursorQuery{}, fmt.Errorf("%w: valid tenant, limit and search are required", ErrInvalidDefinition)
	}
	if query.Status != "" {
		switch query.Status {
		case MetadataProposalStatusProposed, MetadataProposalStatusAccepted, MetadataProposalStatusRejected:
		default:
			return MetadataProposalCursorQuery{}, fmt.Errorf("%w: unsupported metadata proposal status", ErrInvalidDefinition)
		}
	}
	if query.After.UpdatedAt.IsZero() != (query.After.ID == "") {
		return MetadataProposalCursorQuery{}, fmt.Errorf("%w: proposal seek requires updated_at and id", ErrInvalidDefinition)
	}
	query.After.UpdatedAt = query.After.UpdatedAt.UTC()
	return query, nil
}

func metadataProposalCursorWhere(query MetadataProposalCursorQuery) (string, []any) {
	clauses := []string{"tenant_id = ?"}
	args := []any{query.TenantID}
	if query.Status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, query.Status)
	}
	if query.TargetKind != "" {
		clauses = append(clauses, "target_kind = ?")
		args = append(args, query.TargetKind)
	}
	if query.TargetID != "" {
		clauses = append(clauses, "target_id = ?")
		args = append(args, query.TargetID)
	}
	if query.MetadataKey != "" {
		clauses = append(clauses, "metadata_key = ?")
		args = append(args, query.MetadataKey)
	}
	if query.Search != "" {
		pattern := "%" + escapeMetadataGovernanceLike(strings.ToLower(query.Search)) + "%"
		clauses = append(clauses, `(LOWER(id) LIKE ? ESCAPE '\' OR LOWER(target_id) LIKE ? ESCAPE '\' OR LOWER(metadata_key) LIKE ? ESCAPE '\' OR LOWER(proposed_value) LIKE ? ESCAPE '\')`)
		args = append(args, pattern, pattern, pattern, pattern)
	}
	if query.After.ID != "" {
		updatedAt := timeToUnixNano(query.After.UpdatedAt)
		clauses = append(clauses, "(updated_at < ? OR (updated_at = ? AND id > ?))")
		args = append(args, updatedAt, updatedAt, query.After.ID)
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func metadataProposalCursorMatches(proposal OntologyMetadataProposal, query MetadataProposalCursorQuery) bool {
	if proposal.TenantID != query.TenantID ||
		query.Status != "" && proposal.Status != query.Status ||
		query.TargetKind != "" && proposal.TargetKind != query.TargetKind ||
		query.TargetID != "" && proposal.TargetID != query.TargetID ||
		query.MetadataKey != "" && proposal.MetadataKey != query.MetadataKey {
		return false
	}
	if query.Search == "" {
		return true
	}
	needle := strings.ToLower(query.Search)
	return strings.Contains(strings.ToLower(proposal.ID), needle) ||
		strings.Contains(strings.ToLower(proposal.TargetID), needle) ||
		strings.Contains(strings.ToLower(proposal.MetadataKey), needle) ||
		strings.Contains(strings.ToLower(proposal.ProposedValue), needle)
}

func metadataProposalAfterSeek(proposal OntologyMetadataProposal, after MetadataProposalSeek) bool {
	if after.ID == "" {
		return true
	}
	return proposal.UpdatedAt.Before(after.UpdatedAt) || proposal.UpdatedAt.Equal(after.UpdatedAt) && proposal.ID > after.ID
}

func escapeMetadataGovernanceLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}
