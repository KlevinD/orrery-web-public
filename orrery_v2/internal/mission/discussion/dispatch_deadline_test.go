package discussion

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/platform/uow"
)

type dispatchHookTx struct {
	uow.Tx
	hook func(context.Context) error
}

func (tx *dispatchHookTx) AfterCommit(hook func(context.Context) error) { tx.hook = hook }

type deadlineMentionDispatcher struct {
	called bool
	t      *testing.T
}

func (d *deadlineMentionDispatcher) DispatchMention(ctx context.Context, _ InboxItem) error {
	d.called = true
	if ctx.Err() != nil {
		d.t.Fatalf("committed dispatch canceled with HTTP request: %v", ctx.Err())
	}
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 5*time.Second {
		d.t.Fatal("dispatch has no bounded deadline")
	}
	return nil
}

func TestCommittedMentionHasIndependentBoundedDeadline(t *testing.T) {
	dispatcher := &deadlineMentionDispatcher{t: t}
	service := &Service{dispatcher: dispatcher}
	tx := &dispatchHookTx{}
	if err := service.dispatchMentionAfterCommit(tx, InboxItem{Recipient: ViewerRef{Kind: ViewerAgent}, RequiredResponse: true, Status: InboxPending}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if tx.hook == nil {
		t.Fatal("missing hook")
	}
	if err := tx.hook(ctx); err != nil {
		t.Fatal(err)
	}
	if !dispatcher.called {
		t.Fatal("committed mention not dispatched")
	}
}
