package improvement

import (
	"context"
	"errors"
	"fmt"
)

// RuntimeFeedback contains only the minimal terminal evidence. The journal is
// a trusted host recovery boundary; hashes detect corruption, not host compromise.
type RuntimeFeedback struct {
	TenantID string                  `json:"tenant_id"`
	Terminal RuntimeTerminalEvidence `json:"terminal"`
}

type RuntimeFeedbackJournal interface {
	Save(context.Context, RuntimeFeedback) error
	Scan(context.Context, int) ([]RuntimeFeedback, error)
	Acknowledge(context.Context, RuntimeFeedback) error
}

type RuntimeFeedbackRecovery struct {
	Scanned   int `json:"scanned"`
	Recovered int `json:"recovered"`
	Retained  int `json:"retained"`
}

// RecoverFeedback only replays host-journaled terminals against their exact
// captured request. Missing, corrupt or conflicting evidence remains pending.
// It does not invoke models, providers, delivery or business task execution.
func (s *RuntimeEvidenceStore) RecoverFeedback(ctx context.Context, journal RuntimeFeedbackJournal, limit int) (RuntimeFeedbackRecovery, error) {
	var result RuntimeFeedbackRecovery
	if s == nil || s.store.ready() != nil || journal == nil {
		return result, ErrUnavailable
	}
	if limit < 1 || limit > 1000 {
		return result, ErrInvalid
	}
	entries, scanErr := journal.Scan(ctx, limit)
	var failures []error
	if scanErr != nil {
		failures = append(failures, scanErr)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return result, errors.Join(append(failures, err)...)
		}
		result.Scanned++
		request, err := s.GetRequest(ctx, entry.TenantID, entry.Terminal.AttemptID)
		if err == nil {
			_, err = s.completeTerminal(ctx, request, entry.Terminal)
		}
		if err == nil {
			err = journal.Acknowledge(ctx, entry)
		}
		if err != nil {
			result.Retained++
			// Keep task contents out of errors; only host storage errors escape.
			failures = append(failures, fmt.Errorf("runtime feedback retained: %w", err))
			continue
		}
		result.Recovered++
	}
	return result, errors.Join(failures...)
}
