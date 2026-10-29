package governance

import (
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/app/ontology"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

func proposalListQueryHash(query ProposalListQuery, limit int) (string, error) {
	return ontologycontract.CanonicalQueryHash(struct {
		Resource   string                          `json:"resource"`
		Queue      ProposalQueue                   `json:"queue"`
		Status     ontology.MetadataProposalStatus `json:"status"`
		TargetKind string                          `json:"target_kind"`
		Search     string                          `json:"search"`
		Limit      int                             `json:"limit"`
	}{"proposals", query.Queue, query.Status, query.TargetKind, strings.ToLower(query.Search), limit})
}

func receiptListQueryHash(query ReceiptListQuery, limit int) (string, error) {
	return ontologycontract.CanonicalQueryHash(struct {
		Resource   string `json:"resource"`
		ProposalID string `json:"proposal_id"`
		Limit      int    `json:"limit"`
	}{"receipts", query.ProposalID, limit})
}

func auditEventListQueryHash(query AuditEventListQuery, limit int) (string, error) {
	return ontologycontract.CanonicalQueryHash(struct {
		Resource   string `json:"resource"`
		ProposalID string `json:"proposal_id"`
		Limit      int    `json:"limit"`
	}{"audit-events", query.ProposalID, limit})
}

func governanceCursorBinding(state governancePolicyState, queryHash string) ontologycontract.CursorBinding {
	snapshot := state.scope.Snapshot()
	return ontologycontract.CursorBinding{
		PrincipalKey: snapshot.PrincipalKey, TenantID: state.scope.Binding().TenantID,
		BindingVersion: state.scope.Binding().BindingVersion, QueryHash: queryHash,
		AuthorizationRevision: snapshot.AuthorizationRevision, PolicyRevision: state.revision,
	}
}

func (s *Service) resolveProposalCursor(token string, binding ontologycontract.CursorBinding, cacheable bool) (ontology.MetadataProposalSeek, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return ontology.MetadataProposalSeek{}, nil
	}
	position, err := s.resolveCursor(token, binding, cacheable)
	if err != nil {
		return ontology.MetadataProposalSeek{}, err
	}
	if len(position.Seek) != 2 || !validID(position.Seek[1]) {
		return ontology.MetadataProposalSeek{}, ontologycontract.ErrInvalidCursor
	}
	nanos, err := strconv.ParseInt(position.Seek[0], 10, 64)
	if err != nil || nanos <= 0 {
		return ontology.MetadataProposalSeek{}, ontologycontract.ErrInvalidCursor
	}
	return ontology.MetadataProposalSeek{UpdatedAt: time.Unix(0, nanos).UTC(), ID: position.Seek[1]}, nil
}

func (s *Service) issueProposalCursor(binding ontologycontract.CursorBinding, cacheable bool, seek ontology.MetadataProposalSeek) (string, error) {
	if seek.UpdatedAt.IsZero() || !validID(seek.ID) {
		return "", ontologycontract.ErrInvalidRequest
	}
	return s.issueCursor(binding, cacheable, strconv.FormatInt(seek.UpdatedAt.UTC().UnixNano(), 10), seek.ID)
}

func (s *Service) resolveReceiptCursor(token string, binding ontologycontract.CursorBinding, cacheable bool) (ontology.PromotionReceiptSeek, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return ontology.PromotionReceiptSeek{}, nil
	}
	position, err := s.resolveCursor(token, binding, cacheable)
	if err != nil {
		return ontology.PromotionReceiptSeek{}, err
	}
	if len(position.Seek) != 2 || !validID(position.Seek[1]) {
		return ontology.PromotionReceiptSeek{}, ontologycontract.ErrInvalidCursor
	}
	nanos, err := strconv.ParseInt(position.Seek[0], 10, 64)
	if err != nil || nanos <= 0 {
		return ontology.PromotionReceiptSeek{}, ontologycontract.ErrInvalidCursor
	}
	return ontology.PromotionReceiptSeek{UpdatedAt: time.Unix(0, nanos).UTC(), ReceiptID: position.Seek[1]}, nil
}

func (s *Service) issueReceiptCursor(binding ontologycontract.CursorBinding, cacheable bool, seek ontology.PromotionReceiptSeek) (string, error) {
	if seek.UpdatedAt.IsZero() || !validID(seek.ReceiptID) {
		return "", ontologycontract.ErrInvalidRequest
	}
	return s.issueCursor(binding, cacheable, strconv.FormatInt(seek.UpdatedAt.UTC().UnixNano(), 10), seek.ReceiptID)
}

func (s *Service) resolveAuditCursor(token string, binding ontologycontract.CursorBinding, cacheable bool) (ProposalAuditSeek, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return ProposalAuditSeek{}, nil
	}
	position, err := s.resolveCursor(token, binding, cacheable)
	if err != nil {
		return ProposalAuditSeek{}, err
	}
	if len(position.Seek) != 2 || !validID(position.Seek[1]) {
		return ProposalAuditSeek{}, ontologycontract.ErrInvalidCursor
	}
	nanos, err := strconv.ParseInt(position.Seek[0], 10, 64)
	if err != nil || nanos <= 0 {
		return ProposalAuditSeek{}, ontologycontract.ErrInvalidCursor
	}
	return ProposalAuditSeek{OccurredAt: time.Unix(0, nanos).UTC(), AuditID: position.Seek[1]}, nil
}

func (s *Service) issueAuditCursor(binding ontologycontract.CursorBinding, cacheable bool, seek ProposalAuditSeek) (string, error) {
	if seek.OccurredAt.IsZero() || !validID(seek.AuditID) {
		return "", ontologycontract.ErrInvalidRequest
	}
	return s.issueCursor(binding, cacheable, strconv.FormatInt(seek.OccurredAt.UTC().UnixNano(), 10), seek.AuditID)
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

func normalizeCursorToken(value string) string {
	return strings.TrimSpace(value)
}
