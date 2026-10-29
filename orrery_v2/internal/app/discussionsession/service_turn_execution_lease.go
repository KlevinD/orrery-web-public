package discussionsession

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	discussionTurnExecutionClaimPrefix    = "discussion-turn:"
	discussionCommandExecutionClaimPrefix = "discussion-command:"
)

func (s *Service) discussionExecutionLeasesEnabled() bool {
	return s != nil && s.discussionClaims != nil
}

func (s *Service) acquireTurnExecutionLease(ctx context.Context, turnID string) (context.Context, func(), bool, error) {
	return s.acquireDiscussionExecutionLease(ctx, discussionTurnExecutionClaimPrefix, "turn_id", turnID)
}

func (s *Service) acquireCommandExecutionLease(ctx context.Context, commandID string) (func(), bool, error) {
	_, release, acquired, err := s.acquireDiscussionExecutionLease(contextWithoutCancellation(ctx), discussionCommandExecutionClaimPrefix, "command_id", commandID)
	return release, acquired, err
}

func (s *Service) acquireDiscussionExecutionLease(ctx context.Context, claimPrefix, resourceField, resourceID string) (context.Context, func(), bool, error) {
	if !s.discussionExecutionLeasesEnabled() {
		return ctx, func() {}, true, nil
	}
	resourceID = strings.TrimSpace(resourceID)
	workerID := strings.TrimSpace(s.discussionExecutorID)
	if resourceID == "" || workerID == "" {
		return ctx, func() {}, false, ErrServiceDependency
	}
	claimID := claimPrefix + resourceID
	acquired, err := s.discussionClaims.Acquire(ctx, claimID, workerID)
	if err != nil {
		return ctx, func() {}, false, fmt.Errorf("%w: acquire discussion execution lease: %v", ErrServiceDependency, err)
	}
	if !acquired {
		return ctx, func() {}, false, nil
	}

	leaseCtx, cancel := context.WithCancelCause(ctx)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(s.discussionLeaseRenew)
		defer ticker.Stop()
		for {
			select {
			case <-leaseCtx.Done():
				return
			case <-ticker.C:
				renewed, renewErr := s.discussionClaims.Renew(leaseCtx, claimID, workerID)
				if renewErr == nil && renewed {
					continue
				}
				cause := fmt.Errorf("%w: discussion turn execution lease lost", ErrServiceDependency)
				if renewErr != nil {
					cause = fmt.Errorf("%w: renew discussion turn execution lease: %v", ErrServiceDependency, renewErr)
				}
				if s.logger != nil {
					s.logger.Warn(contextWithoutCancellation(leaseCtx), "discussion execution lease lost", resourceField, resourceID, "error", cause)
				}
				cancel(cause)
				return
			}
		}
	}()

	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			cancel(context.Canceled)
			<-heartbeatDone
			releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer releaseCancel()
			if _, releaseErr := s.discussionClaims.Release(releaseCtx, claimID, workerID); releaseErr != nil && s.logger != nil {
				s.logger.Warn(releaseCtx, "discussion execution lease release failed", resourceField, resourceID, "error", releaseErr)
			}
		})
	}
	return leaseCtx, release, true, nil
}

func (s *Service) recoverInterruptedTurn(ctx context.Context, conversationID, turnID string, now time.Time) (bool, error) {
	staleBefore := now.Add(-s.turnRecoveryTimeout)
	if !s.discussionExecutionLeasesEnabled() {
		return s.repo.FailStaleTurn(ctx, conversationID, turnID, staleBefore, now)
	}
	_, release, acquired, err := s.acquireTurnExecutionLease(ctx, turnID)
	if err != nil {
		return false, err
	}
	if !acquired {
		return false, nil
	}
	defer release()
	return s.repo.FailStaleTurn(ctx, conversationID, turnID, now, now)
}

func turnExecutionLeaseCause(ctx context.Context, executionErr error) error {
	cause := context.Cause(ctx)
	if cause == nil {
		return executionErr
	}
	return cause
}

func combineReleases(releases ...func()) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			for _, release := range releases {
				if release != nil {
					release()
				}
			}
		})
	}
}

func (e AgentTurnExecutor) Execute(ctx context.Context, command DiscussionTurnCommand) (ConversationDTO, error) {
	if err := e.validate(command); err != nil {
		return ConversationDTO{}, err
	}
	leaseCtx, releaseLease, acquired, err := e.service.acquireTurnExecutionLease(ctx, command.TurnID)
	if err != nil {
		return ConversationDTO{}, err
	}
	if !acquired {
		return ConversationDTO{}, ErrCommandInProgress
	}
	defer releaseLease()
	ctx = leaseCtx
	finishStart := e.service.startDiscussionStage(ctx, command.ConversationID, command.TurnID, "", "turn_start")
	err = e.start(ctx, &command)
	finishStart(err)
	if err != nil {
		return ConversationDTO{}, err
	}

	finishTargets := e.service.startDiscussionStage(ctx, command.ConversationID, command.TurnID, "", "targets_execute")
	executionErr := turnExecutionLeaseCause(ctx, e.executeResolvedTargets(ctx, command))
	finishTargets(executionErr)
	var superseded *supersededDiscussionRunError
	if errors.As(executionErr, &superseded) {
		return ConversationDTO{}, superseded.cause
	}
	completed, finishErr := e.finish(context.WithoutCancel(ctx), command, executionErr)
	if finishErr != nil {
		return ConversationDTO{}, finishErr
	}
	if executionErr != nil {
		if errors.Is(executionErr, ErrRuntimeExecution) {
			// Preserve the failed conversation snapshot for callers, but keep the
			// command on the failure path so durable retries remain possible.
			return completed, executionErr
		}
		return ConversationDTO{}, executionErr
	}
	return completed, nil
}
