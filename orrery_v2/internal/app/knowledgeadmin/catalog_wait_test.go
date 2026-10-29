package knowledgeadmin

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/knowledge/provider"
	"orrery_v2/internal/knowledge/testkit"
)

func TestCatalogReconcilerQueuedCallHonorsDeadline(t *testing.T) {
	base := catalogTestBase()
	source := &blockingCatalogSource{documents: []provider.FolderDocument{catalogRemoteDocument(base)}, entered: make(chan struct{}, 2), release: make(chan struct{})}
	reconciler := newCatalogReconcilerWithSource(testkit.NewFakeRepository(), source, testkit.NewFakeClock(time.Now()))
	finished := make(chan struct{})
	go func() { defer close(finished); _, _ = reconciler.Reconcile(context.Background(), base) }()
	defer func() { close(source.release); <-finished }()
	select {
	case <-source.entered:
	case <-time.After(time.Second):
		t.Fatal("first reconciliation did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := reconciler.Reconcile(ctx, base); result <- err }()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued reconciliation ignored deadline")
	}
}
