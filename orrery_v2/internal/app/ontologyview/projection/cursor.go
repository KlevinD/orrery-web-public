package projection

import (
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/app/contextgraph"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

func auditListQueryHash(query AuditListQuery, limit int) (string, error) {
	return ontologycontract.CanonicalQueryHash(struct {
		Resource   string `json:"resource"`
		Status     string `json:"status"`
		ReasonCode string `json:"reason_code"`
		Limit      int    `json:"limit"`
	}{"projection-audits", query.Status, query.ReasonCode, limit})
}

func replayHistoryQueryHash(query ReplayHistoryListQuery, limit int) (string, error) {
	return ontologycontract.CanonicalQueryHash(struct {
		Resource string `json:"resource"`
		AuditID  string `json:"audit_id"`
		Limit    int    `json:"limit"`
	}{"projection-replay-history", query.AuditID, limit})
}

func projectionCursorBinding(state projectionPolicyState, queryHash string) ontologycontract.CursorBinding {
	snapshot := state.scope.Snapshot()
	return ontologycontract.CursorBinding{
		PrincipalKey: snapshot.PrincipalKey, TenantID: state.scope.Binding().TenantID,
		BindingVersion: state.scope.Binding().BindingVersion, QueryHash: queryHash,
		AuthorizationRevision: snapshot.AuthorizationRevision, PolicyRevision: state.revision,
	}
}

func (s *Service) resolveAuditCursor(token string, binding ontologycontract.CursorBinding, cacheable bool) (contextgraph.ProjectionAuditSeek, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return contextgraph.ProjectionAuditSeek{}, nil
	}
	position, err := s.resolveCursor(token, binding, cacheable)
	if err != nil {
		return contextgraph.ProjectionAuditSeek{}, err
	}
	if len(position.Seek) != 2 || !validID(position.Seek[1]) {
		return contextgraph.ProjectionAuditSeek{}, ontologycontract.ErrInvalidCursor
	}
	nanos, err := strconv.ParseInt(position.Seek[0], 10, 64)
	if err != nil || nanos <= 0 {
		return contextgraph.ProjectionAuditSeek{}, ontologycontract.ErrInvalidCursor
	}
	return contextgraph.ProjectionAuditSeek{ObservedAt: time.Unix(0, nanos).UTC(), ID: position.Seek[1]}, nil
}

func (s *Service) issueAuditCursor(binding ontologycontract.CursorBinding, cacheable bool, seek contextgraph.ProjectionAuditSeek) (string, error) {
	if seek.ObservedAt.IsZero() || !validID(seek.ID) {
		return "", ontologycontract.ErrInvalidRequest
	}
	return s.issueCursor(binding, cacheable, strconv.FormatInt(seek.ObservedAt.UTC().UnixNano(), 10), seek.ID)
}

func (s *Service) resolveReplayHistoryCursor(token string, binding ontologycontract.CursorBinding, cacheable bool) (contextgraph.ProjectionReplayHistorySeek, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return contextgraph.ProjectionReplayHistorySeek{}, nil
	}
	position, err := s.resolveCursor(token, binding, cacheable)
	if err != nil {
		return contextgraph.ProjectionReplayHistorySeek{}, err
	}
	if len(position.Seek) != 2 || !validID(position.Seek[1]) {
		return contextgraph.ProjectionReplayHistorySeek{}, ontologycontract.ErrInvalidCursor
	}
	nanos, err := strconv.ParseInt(position.Seek[0], 10, 64)
	if err != nil || nanos <= 0 {
		return contextgraph.ProjectionReplayHistorySeek{}, ontologycontract.ErrInvalidCursor
	}
	return contextgraph.ProjectionReplayHistorySeek{AttemptedAt: time.Unix(0, nanos).UTC(), ID: position.Seek[1]}, nil
}

func (s *Service) issueReplayHistoryCursor(binding ontologycontract.CursorBinding, cacheable bool, seek contextgraph.ProjectionReplayHistorySeek) (string, error) {
	if seek.AttemptedAt.IsZero() || !validID(seek.ID) {
		return "", ontologycontract.ErrInvalidRequest
	}
	return s.issueCursor(binding, cacheable, strconv.FormatInt(seek.AttemptedAt.UTC().UnixNano(), 10), seek.ID)
}

func (s *Service) resolveCursor(token string, binding ontologycontract.CursorBinding, cacheable bool) (ontologycontract.CursorPosition, error) {
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
