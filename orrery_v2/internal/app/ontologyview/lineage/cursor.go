package lineage

import (
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/app/contextgraph"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

func normalizeFactPageQuery(query FactPageQuery) (FactPageQuery, int, error) {
	query.TenantID = strings.TrimSpace(query.TenantID)
	query.FactID = strings.TrimSpace(query.FactID)
	query.Cursor = strings.TrimSpace(query.Cursor)
	limit, err := ontologycontract.NormalizeLimit(query.Limit, ontologycontract.DefaultChildListLimit, ontologycontract.MaxPageLimit)
	if err != nil || !validID(query.TenantID) || !validID(query.FactID) {
		return FactPageQuery{}, 0, ontologycontract.ErrInvalidRequest
	}
	return query, limit, nil
}

func (s *Service) resolveCursorPosition(token string, binding ontologycontract.CursorBinding, cacheable bool) (ontologycontract.CursorPosition, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return ontologycontract.CursorPosition{Binding: binding}, nil
	}
	switch {
	case strings.HasPrefix(token, "oc1.") && cacheable && s.cursors.Encrypted != nil:
		return s.cursors.Encrypted.Decode(token, binding)
	case strings.HasPrefix(token, "ocs1."):
		return s.cursors.Stateful.Resolve(token, binding)
	case strings.HasPrefix(token, "oc1."):
		return ontologycontract.CursorPosition{}, ontologycontract.ErrCursorStale
	default:
		return ontologycontract.CursorPosition{}, ontologycontract.ErrInvalidCursor
	}
}

func (s *Service) issueCursor(binding ontologycontract.CursorBinding, cacheable bool, seek ...string) (string, error) {
	position := ontologycontract.CursorPosition{Binding: binding, Seek: append([]string(nil), seek...)}
	if cacheable && s.cursors.Encrypted != nil {
		return s.cursors.Encrypted.Encode(position)
	}
	return s.cursors.Stateful.Issue(position)
}

func (s *Service) resolveAssertionSeek(token string, binding ontologycontract.CursorBinding, cacheable bool) (contextgraph.FactAssertionSeek, error) {
	if strings.TrimSpace(token) == "" {
		return contextgraph.FactAssertionSeek{}, nil
	}
	position, err := s.resolveCursorPosition(token, binding, cacheable)
	if err != nil {
		return contextgraph.FactAssertionSeek{}, err
	}
	if len(position.Seek) != 2 {
		return contextgraph.FactAssertionSeek{}, ontologycontract.ErrInvalidCursor
	}
	nanos, err := strconv.ParseInt(position.Seek[0], 10, 64)
	if err != nil || nanos <= 0 || !validID(position.Seek[1]) {
		return contextgraph.FactAssertionSeek{}, ontologycontract.ErrInvalidCursor
	}
	return contextgraph.FactAssertionSeek{ObservedAt: time.Unix(0, nanos).UTC(), ID: position.Seek[1]}, nil
}

func (s *Service) issueAssertionCursor(binding ontologycontract.CursorBinding, cacheable bool, seek contextgraph.FactAssertionSeek) (string, error) {
	if seek.ObservedAt.IsZero() || !validID(seek.ID) {
		return "", ontologycontract.ErrInvalidRequest
	}
	return s.issueCursor(binding, cacheable, strconv.FormatInt(seek.ObservedAt.UTC().UnixNano(), 10), seek.ID)
}

func (s *Service) resolveProjectionSeek(token string, binding ontologycontract.CursorBinding, cacheable bool) (contextgraph.ProjectionAuditSeek, error) {
	if strings.TrimSpace(token) == "" {
		return contextgraph.ProjectionAuditSeek{}, nil
	}
	position, err := s.resolveCursorPosition(token, binding, cacheable)
	if err != nil {
		return contextgraph.ProjectionAuditSeek{}, err
	}
	if len(position.Seek) != 2 {
		return contextgraph.ProjectionAuditSeek{}, ontologycontract.ErrInvalidCursor
	}
	nanos, err := strconv.ParseInt(position.Seek[0], 10, 64)
	if err != nil || nanos <= 0 || !validID(position.Seek[1]) {
		return contextgraph.ProjectionAuditSeek{}, ontologycontract.ErrInvalidCursor
	}
	return contextgraph.ProjectionAuditSeek{ObservedAt: time.Unix(0, nanos).UTC(), ID: position.Seek[1]}, nil
}

func (s *Service) issueProjectionCursor(binding ontologycontract.CursorBinding, cacheable bool, seek contextgraph.ProjectionAuditSeek) (string, error) {
	if seek.ObservedAt.IsZero() || !validID(seek.ID) {
		return "", ontologycontract.ErrInvalidRequest
	}
	return s.issueCursor(binding, cacheable, strconv.FormatInt(seek.ObservedAt.UTC().UnixNano(), 10), seek.ID)
}

type evidenceSeek struct {
	Kind string
	ID   string
}

func (s *Service) resolveEvidenceSeek(token string, binding ontologycontract.CursorBinding, cacheable bool) (evidenceSeek, error) {
	if strings.TrimSpace(token) == "" {
		return evidenceSeek{}, nil
	}
	position, err := s.resolveCursorPosition(token, binding, cacheable)
	if err != nil {
		return evidenceSeek{}, err
	}
	if len(position.Seek) != 2 || !validID(position.Seek[0]) || !validID(position.Seek[1]) {
		return evidenceSeek{}, ontologycontract.ErrInvalidCursor
	}
	return evidenceSeek{Kind: position.Seek[0], ID: position.Seek[1]}, nil
}

func (s *Service) issueEvidenceCursor(binding ontologycontract.CursorBinding, cacheable bool, seek evidenceSeek) (string, error) {
	if !validID(seek.Kind) || !validID(seek.ID) {
		return "", ontologycontract.ErrInvalidRequest
	}
	return s.issueCursor(binding, cacheable, seek.Kind, seek.ID)
}
