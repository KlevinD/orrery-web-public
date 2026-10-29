package operations

import (
	"context"
	"strings"
)

func (s *MemoryEvaluationProjectionStore) UpsertEvaluationReview(ctx context.Context, review EvaluationReviewDTO) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	review = normalizeEvaluationReview(review)
	if review.ReviewID == "" || review.EntityType == "" || review.EntityID == "" {
		return ErrInvalidRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.reviews {
		if existing.ReviewID == review.ReviewID {
			s.reviews[i] = review
			return nil
		}
	}
	s.reviews = append(s.reviews, review)
	return nil
}

func (s *MemoryEvaluationProjectionStore) GetEvaluationReview(ctx context.Context, reviewID string) (EvaluationReviewDTO, error) {
	if err := ctx.Err(); err != nil {
		return EvaluationReviewDTO{}, err
	}
	reviewID = strings.TrimSpace(reviewID)
	if reviewID == "" {
		return EvaluationReviewDTO{}, ErrInvalidRequest
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, review := range s.reviews {
		if review.ReviewID == reviewID {
			return normalizeEvaluationReview(review), nil
		}
	}
	return EvaluationReviewDTO{}, ErrNotFound
}

func (s *PlatformEvaluationProjectionStore) UpsertEvaluationReview(ctx context.Context, review EvaluationReviewDTO) error {
	if s == nil || s.router == nil {
		return ErrServiceDependency
	}
	review = normalizeEvaluationReview(review)
	if review.ReviewID == "" || review.EntityType == "" || review.EntityID == "" {
		return ErrInvalidRequest
	}
	return s.router.PutRecord(ctx, evaluationReviewTable, review.ReviewID, evaluationReviewRecord(review))
}

func (s *PlatformEvaluationProjectionStore) GetEvaluationReview(ctx context.Context, reviewID string) (EvaluationReviewDTO, error) {
	if s == nil || s.router == nil {
		return EvaluationReviewDTO{}, ErrServiceDependency
	}
	reviewID = strings.TrimSpace(reviewID)
	if reviewID == "" {
		return EvaluationReviewDTO{}, ErrInvalidRequest
	}
	record, ok, err := s.router.GetRecord(ctx, evaluationReviewTable, reviewID)
	if err != nil {
		return EvaluationReviewDTO{}, err
	}
	if !ok {
		return EvaluationReviewDTO{}, ErrNotFound
	}
	return evaluationReviewFromRecord(record), nil
}
