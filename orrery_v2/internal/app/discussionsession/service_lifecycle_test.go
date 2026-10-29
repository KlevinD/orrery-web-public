package discussionsession

import (
	"testing"
	"time"
)

func TestRetryLifecycleSupersedesStaleOrdinaryLock(t *testing.T) {
	service := &Service{runLifecycleLocks: map[string]*discussionRunLifecycleLock{}}
	staleRelease := service.lockRunLifecycle("conv-1")
	retryRelease := service.beginRetryLifecycle("conv-1")
	retryRelease()

	acquired := make(chan func(), 1)
	go func() {
		acquired <- service.lockRunLifecycle("conv-1")
	}()
	select {
	case release := <-acquired:
		release()
	case <-time.After(time.Second):
		t.Fatal("ordinary turn remained blocked by superseded provider")
	}

	// A stale provider can return later without releasing the newer owner.
	staleRelease()
}
