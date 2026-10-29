package discussionsession

import (
	"context"
	"strings"
	"sync"
)

type discussionRunLifecycleLock struct {
	stateMu       sync.Mutex
	stateChanged  *sync.Cond
	retryMu       sync.Mutex
	nextToken     uint64
	activeToken   uint64
	queuedRetries int
	refs          int
}

// A turn carries its lifecycle owner into runtime tool callbacks so membership
// reconciliation can avoid recursively acquiring the same non-reentrant lock.
// The service and conversation identity keep the bypass scoped to that turn.
type discussionRunLifecycleOwnerKey struct{}

type discussionRunLifecycleOwner struct {
	service        *Service
	conversationID string
}

func (s *Service) withRunLifecycleOwner(ctx context.Context, conversationID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		conversationID = "__unscoped__"
	}
	if !s.runLifecycleIsActive(conversationID) {
		return ctx
	}
	return context.WithValue(ctx, discussionRunLifecycleOwnerKey{}, discussionRunLifecycleOwner{service: s, conversationID: conversationID})
}

func (s *Service) lockRunLifecycleForContext(ctx context.Context, conversationID string) func() {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		conversationID = "__unscoped__"
	}
	var owner discussionRunLifecycleOwner
	if ctx != nil {
		owner, _ = ctx.Value(discussionRunLifecycleOwnerKey{}).(discussionRunLifecycleOwner)
	}
	if owner.service == s && owner.conversationID == conversationID {
		return func() {}
	}
	return s.lockRunLifecycle(conversationID)
}

func (s *Service) lockRunLifecycle(conversationID string) func() {
	if s == nil {
		return func() {}
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		conversationID = "__unscoped__"
	}
	s.runLifecycleLocksMu.Lock()
	if s.runLifecycleLocks == nil {
		s.runLifecycleLocks = map[string]*discussionRunLifecycleLock{}
	}
	lock := s.runLifecycleLocks[conversationID]
	if lock == nil {
		lock = &discussionRunLifecycleLock{}
		s.runLifecycleLocks[conversationID] = lock
	}
	lock.refs++
	s.runLifecycleLocksMu.Unlock()
	lock.stateMu.Lock()
	if lock.stateChanged == nil {
		lock.stateChanged = sync.NewCond(&lock.stateMu)
	}
	for lock.activeToken != 0 || lock.queuedRetries > 0 {
		lock.stateChanged.Wait()
	}
	lock.nextToken++
	token := lock.nextToken
	lock.activeToken = token
	lock.stateMu.Unlock()
	return func() {
		lock.stateMu.Lock()
		if lock.activeToken == token {
			lock.activeToken = 0
			lock.stateChanged.Broadcast()
		}
		lock.stateMu.Unlock()
		s.runLifecycleLocksMu.Lock()
		defer s.runLifecycleLocksMu.Unlock()
		lock.refs--
		if lock.refs <= 0 && s.runLifecycleLocks[conversationID] == lock {
			delete(s.runLifecycleLocks, conversationID)
		}
	}
}

func (s *Service) runLifecycleIsActive(conversationID string) bool {
	if s == nil {
		return false
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		conversationID = "__unscoped__"
	}
	s.runLifecycleLocksMu.Lock()
	lock := s.runLifecycleLocks[conversationID]
	if lock == nil {
		s.runLifecycleLocksMu.Unlock()
		return false
	}
	lock.stateMu.Lock()
	active := lock.activeToken != 0
	lock.stateMu.Unlock()
	s.runLifecycleLocksMu.Unlock()
	return active
}

// beginRetryLifecycle fences newly admitted ordinary turns without waiting for
// the currently running provider. This lets retry invalidate a provider that
// ignores cancellation while keeping later ordinary turns queued until the
// retry reaches a terminal state.
func (s *Service) beginRetryLifecycle(conversationID string) func() {
	if s == nil {
		return func() {}
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		conversationID = "__unscoped__"
	}
	s.runLifecycleLocksMu.Lock()
	lock := s.runLifecycleLocks[conversationID]
	if lock == nil {
		lock = &discussionRunLifecycleLock{}
		s.runLifecycleLocks[conversationID] = lock
	}
	lock.refs++
	lock.stateMu.Lock()
	if lock.stateChanged == nil {
		lock.stateChanged = sync.NewCond(&lock.stateMu)
	}
	lock.queuedRetries++
	lock.stateMu.Unlock()
	s.runLifecycleLocksMu.Unlock()

	lock.retryMu.Lock()
	lock.stateMu.Lock()
	lock.queuedRetries--
	lock.nextToken++
	token := lock.nextToken
	lock.activeToken = token
	lock.stateChanged.Broadcast()
	lock.stateMu.Unlock()

	return func() {
		lock.stateMu.Lock()
		if lock.activeToken == token {
			lock.activeToken = 0
			lock.stateChanged.Broadcast()
		}
		lock.stateMu.Unlock()
		lock.retryMu.Unlock()
		s.runLifecycleLocksMu.Lock()
		lock.refs--
		if lock.refs <= 0 && s.runLifecycleLocks[conversationID] == lock {
			delete(s.runLifecycleLocks, conversationID)
		}
		s.runLifecycleLocksMu.Unlock()
	}
}

func (s *Service) registerActiveRun(runID string, cancel context.CancelFunc) {
	if s == nil || cancel == nil || strings.TrimSpace(runID) == "" {
		return
	}
	s.activeRunsMu.Lock()
	defer s.activeRunsMu.Unlock()
	if s.activeRuns == nil {
		s.activeRuns = map[string]context.CancelFunc{}
	}
	s.activeRuns[runID] = cancel
}

func (s *Service) unregisterActiveRun(runID string) {
	if s == nil || strings.TrimSpace(runID) == "" {
		return
	}
	s.activeRunsMu.Lock()
	defer s.activeRunsMu.Unlock()
	delete(s.activeRuns, runID)
}

func (s *Service) cancelActiveRun(runID string) {
	if s == nil || strings.TrimSpace(runID) == "" {
		return
	}
	s.activeRunsMu.Lock()
	cancel := s.activeRuns[runID]
	delete(s.activeRuns, runID)
	s.activeRunsMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Service) invalidateRun(runID string) {
	if s == nil || strings.TrimSpace(runID) == "" {
		return
	}
	s.activeRunsMu.Lock()
	if s.invalidatedRuns == nil {
		s.invalidatedRuns = map[string]bool{}
	}
	s.invalidatedRuns[runID] = true
	cancel := s.activeRuns[runID]
	delete(s.activeRuns, runID)
	s.activeRunsMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Service) isRunInvalidated(runID string) bool {
	if s == nil || strings.TrimSpace(runID) == "" {
		return true
	}
	s.activeRunsMu.Lock()
	defer s.activeRunsMu.Unlock()
	return s.invalidatedRuns[runID]
}
