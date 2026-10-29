package todo

import (
	"context"
	"strings"
)

func (s *Service) CreateIssueComment(ctx context.Context, viewer ViewerRef, req CreateIssueCommentRequest) (IssueComment, error) {
	viewer, err := normalizeViewer(viewer)
	if err != nil {
		return IssueComment{}, err
	}
	if s == nil || s.clock == nil || s.ids == nil || s.repo == nil {
		return IssueComment{}, ErrServiceDependency
	}
	issueID := strings.TrimSpace(req.IssueID)
	body := strings.TrimSpace(req.Body)
	if !isIssueKind(req.IssueKind) || issueID == "" || body == "" {
		return IssueComment{}, ErrInvalidRequest
	}
	if err := s.requireIssueVisible(ctx, viewer, req.IssueKind, issueID); err != nil {
		return IssueComment{}, err
	}
	commentID, err := s.ids.NewIDContext()
	if err != nil {
		return IssueComment{}, err
	}
	now := s.clock.Now().UTC()
	comment := IssueComment{
		TenantID:   viewer.TenantID,
		CommentID:  commentID,
		IssueKind:  req.IssueKind,
		IssueID:    issueID,
		Body:       body,
		AuthorID:   viewer.ViewerID,
		AuthorName: viewerDisplayName(viewer),
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := s.repo.SaveIssueComment(ctx, comment); err != nil {
		return IssueComment{}, err
	}
	return comment, nil
}

func (s *Service) ListIssueComments(ctx context.Context, viewer ViewerRef, query ListIssueCommentsQuery) (IssueCommentPage, error) {
	viewer, err := normalizeViewer(viewer)
	if err != nil {
		return IssueCommentPage{}, err
	}
	if s == nil || s.repo == nil {
		return IssueCommentPage{}, ErrServiceDependency
	}
	issueID := strings.TrimSpace(query.IssueID)
	if !isIssueKind(query.IssueKind) || issueID == "" {
		return IssueCommentPage{}, ErrInvalidRequest
	}
	if err := s.requireIssueVisible(ctx, viewer, query.IssueKind, issueID); err != nil {
		return IssueCommentPage{}, err
	}
	items, err := s.repo.ListIssueCommentsForTenant(ctx, viewer.TenantID, query.IssueKind, issueID)
	if err != nil {
		return IssueCommentPage{}, err
	}
	pageItems, total, limit, offset := paginate(items, query.Limit, query.Offset)
	return IssueCommentPage{Comments: pageItems, Total: total, Limit: limit, Offset: offset, HasMore: offset+len(pageItems) < total}, nil
}

func (s *Service) requireIssueVisible(ctx context.Context, viewer ViewerRef, kind IssueKind, issueID string) error {
	switch kind {
	case IssueKindTodo:
		_, err := s.GetTodo(ctx, viewer, issueID)
		return err
	case IssueKindIdea:
		_, err := s.GetIdea(ctx, viewer, issueID)
		return err
	default:
		return ErrInvalidRequest
	}
}
