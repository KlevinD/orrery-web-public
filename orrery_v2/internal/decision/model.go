// Package decision defines bounded choice assessments independently of any model.
package decision

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"
)

const MaxCandidates = 6
const MaxRequestBytes = 900

var ErrBudget = errors.New("decision: input budget exceeded")
var ErrResponse = errors.New("decision: invalid response")
var ErrConfiguration = errors.New("decision: configuration unavailable")

type Question struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria"`
}

type Decision struct {
	Choice           string             `json:"choice"`
	Confidence       float64            `json:"confidence"`
	AnswerConfidence float64            `json:"answer_confidence"`
	Probabilities    map[string]float64 `json:"probabilities,omitempty"`
}

type Judge interface {
	Choose(context.Context, []string, Question) ([]Decision, error)
}

type Metadata struct {
	ProfileID          string `json:"profile_id,omitempty"`
	Adapter            string `json:"adapter,omitempty"`
	Provider           string `json:"provider,omitempty"`
	Model              string `json:"model,omitempty"`
	ModelRevision      string `json:"model_revision"`
	SelectionSource    string `json:"selection_source,omitempty"`
	LLMProfileID       string `json:"llm_profile_id,omitempty"`
	LLMSelectionSource string `json:"llm_selection_source,omitempty"`
	ConfidenceKind     string `json:"confidence_kind,omitempty"`
	// CacheKey includes adapter settings; it is never written to execution records.
	CacheKey string `json:"-"`
}

type Resolved struct {
	Judge    Judge
	Metadata Metadata
	Timeout  time.Duration
}

type Resolver interface {
	Resolve(context.Context, string) (Resolved, error)
}
type ResolverFunc func(context.Context, string) (Resolved, error)

func (f ResolverFunc) Resolve(ctx context.Context, scene string) (Resolved, error) {
	return f(ctx, scene)
}

// ValidateInput preserves the existing bounded assessment contract. Adapters
// for other capabilities (prediction, optimization) need their own contracts.
func ValidateInput(states []string, question Question) error {
	if len(states) == 0 || len(states) > MaxCandidates || question.Type != "choice" || len(question.Criteria) < 2 || len(question.Criteria) > 5 || question.Instructions == "" {
		return ErrBudget
	}
	for k, v := range question.Criteria {
		if k == "" || v == "" {
			return ErrBudget
		}
	}
	for _, state := range states {
		body, err := json.Marshal(map[string]any{"state": state, "question": question})
		if state == "" || err != nil || len(body) > MaxRequestBytes {
			return ErrBudget
		}
	}
	return nil
}

func ValidateDecisions(results []Decision, count int, question Question) error {
	if len(results) != count {
		return ErrResponse
	}
	valid := func(p float64) bool { return !math.IsNaN(p) && !math.IsInf(p, 0) && p >= 0 && p <= 1 }
	for _, r := range results {
		if _, ok := question.Criteria[r.Choice]; !ok || !valid(r.Confidence) || !valid(r.AnswerConfidence) {
			return ErrResponse
		}
		if len(r.Probabilities) > 0 {
			if len(r.Probabilities) != len(question.Criteria) {
				return ErrResponse
			}
			sum := 0.0
			for choice := range question.Criteria {
				p, ok := r.Probabilities[choice]
				if !ok || !valid(p) {
					return ErrResponse
				}
				sum += p
			}
			if math.Abs(sum-1) > 0.02 {
				return ErrResponse
			}
		}
	}
	return nil
}
