package improvement

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

// RuntimeEvidenceRecorder observes the resolved request at BaseAgent's durable
// trace boundary. The request is prepared for dispatch, not proof that every
// bound resource was used or that the provider accepted the request.
type RuntimeEvidenceRecorder struct {
	Implementation *RuntimeImplementation
	Recorder       agentcore.TraceRecorder
	Evidence       *RuntimeEvidenceStore
	Journal        RuntimeFeedbackJournal
	Scope          func(agentcore.RuntimeRequest) (RuntimeEvidenceScope, error)
	Now            func() time.Time
}

var _ agentcore.TraceRecorder = RuntimeEvidenceRecorder{}
var _ agentcore.DurableTraceRecorder = RuntimeEvidenceRecorder{}

func (r RuntimeEvidenceRecorder) RecordTrace(ctx context.Context, input agentcore.AgentInput, response agentcore.RuntimeResponse) (string, error) {
	if r.Recorder == nil {
		return "", ErrUnavailable
	}
	return r.Recorder.RecordTrace(ctx, input, response)
}

func (r RuntimeEvidenceRecorder) BeginTrace(ctx context.Context, input agentcore.AgentInput) (agentcore.DurableTraceSession, error) {
	durable, ok := r.Recorder.(agentcore.DurableTraceRecorder)
	if !ok || r.Evidence == nil || r.Evidence.store.ready() != nil || r.Scope == nil {
		return nil, ErrUnavailable
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	base, err := durable.BeginTrace(ctx, input)
	if err != nil {
		return nil, err
	}
	if base == nil {
		return nil, ErrUnavailable
	}
	now := r.Now
	if now == nil {
		now = time.Now
	}
	var implementation *RuntimeImplementation
	if r.Implementation != nil {
		copied := *r.Implementation
		implementation = &copied
	}
	return &runtimeEvidenceTraceSession{DurableTraceSession: base, evidence: r.Evidence, journal: r.Journal, scope: r.Scope, now: now, implementation: implementation, task: privateCopy(input.Task), attempt: "runtime-attempt:" + hex.EncodeToString(nonce[:])}, nil
}

type runtimeEvidenceTraceSession struct {
	agentcore.DurableTraceSession
	evidence                 *RuntimeEvidenceStore
	journal                  RuntimeFeedbackJournal
	scope                    func(agentcore.RuntimeRequest) (RuntimeEvidenceScope, error)
	now                      func() time.Time
	attempt                  string
	mu                       sync.Mutex
	captured                 *RuntimeRequestEvidence
	implementation           *RuntimeImplementation
	execution                *RuntimeExecutionEvidence
	task                     agentcore.TaskInput
	dependencyEntries        []agentcore.RuntimeDependencyObservation
	dependencyBytes          int
	dependencyDropped        int
	dependencyInvalid        int
	dependencyRunnerReturned bool
	dependenciesFinished     bool
}

func (s *runtimeEvidenceTraceSession) RuntimeAttemptID() string { return s.attempt }

func (s *runtimeEvidenceTraceSession) RecordExecution(ctx context.Context, snapshot agentcore.RuntimeExecutionSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.implementation == nil {
		return nil
	} // No host identity: retain the v1 gaps.
	if s.captured == nil {
		return ErrInvalid
	}
	at := s.now()
	if s.execution != nil {
		at = s.execution.CapturedAt
	}
	e, err := BuildRuntimeExecutionEvidence(*s.captured, *s.implementation, snapshot, at)
	if err != nil {
		return err
	}
	if err = s.evidence.CaptureExecution(ctx, e); err != nil {
		return err
	}
	s.execution = &e
	return nil
}

func (s *runtimeEvidenceTraceSession) RecordRequest(ctx context.Context, req agentcore.RuntimeRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	scope, err := s.scope(req)
	if err != nil {
		return fmt.Errorf("runtime evidence scope: %w", err)
	}
	at := s.now()
	if s.captured != nil {
		at = s.captured.CapturedAt
	}
	e, err := BuildRuntimeRequestEvidence(req, scope, s.attempt, s.Ref(), at)
	if err != nil {
		return err
	}
	if err := s.evidence.Capture(ctx, e); err != nil {
		return fmt.Errorf("capture runtime evidence: %w", err)
	}
	s.captured = &e
	if s.task.ID != "" && s.task.MissionID != "" {
		if s.task.ID != req.TaskID || s.task.MissionID != req.MissionID {
			return ErrInvalid
		}
		if err := s.evidence.captureBusinessBinding(ctx, e, s.task); err != nil {
			return err
		}
	}
	return s.DurableTraceSession.RecordRequest(ctx, req)
}

func (s *runtimeEvidenceTraceSession) Finish(ctx context.Context, terminal agentcore.TraceTerminal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dependenciesFinished = true
	// Always finalize the underlying trace, even if RSI persistence fails.
	traceErr := s.DurableTraceSession.Finish(ctx, terminal)
	if s.captured == nil {
		return traceErr
	}
	traceStatus := "succeeded"
	if traceErr != nil {
		traceStatus = "failed"
	}
	end, err := buildRuntimeTerminal(*s.captured, terminal, traceStatus)
	if err != nil {
		return errors.Join(traceErr, err)
	}
	end.Dependencies = s.freezeDependencies(end.CompletedAt)
	end.ID = ""
	end.ID = "runtime-terminal-evidence:" + Digest(end)
	feedback := RuntimeFeedback{TenantID: s.captured.Scope.TenantID, Terminal: end}
	var journalErr error
	if s.journal != nil {
		journalErr = s.journal.Save(ctx, feedback)
		if errors.Is(journalErr, ErrConflict) || errors.Is(journalErr, ErrInvalid) {
			return errors.Join(traceErr, journalErr)
		}
	}
	_, evidenceErr := s.evidence.completeTerminal(ctx, *s.captured, end)
	if evidenceErr != nil {
		evidenceErr = fmt.Errorf("complete runtime evidence: %w", errors.Join(evidenceErr, journalErr))
	} else if s.journal != nil && journalErr == nil {
		// Cleanup failure is retried by recovery. Feedback is already committed;
		// it must not turn a completed business task into a provider retry.
		_ = s.journal.Acknowledge(ctx, feedback)
	}
	return errors.Join(traceErr, evidenceErr)
}
