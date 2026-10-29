package metrics

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultBatchRelaySize     = 64
	defaultBatchRelayInterval = 10 * time.Second
)

type EventWriter interface {
	WriteEvents(context.Context, []Event) error
}

type EventWriterFunc func(context.Context, []Event) error

func (fn EventWriterFunc) WriteEvents(ctx context.Context, events []Event) error {
	if fn == nil {
		return fmt.Errorf("metrics event writer is nil")
	}
	return fn(ctx, events)
}

type BatchRelayConfig struct {
	BatchSize  int
	FlushEvery time.Duration
	Writer     EventWriter
}

type BatchRelayStats struct {
	Buffered int
	Dropped  int64
	Sent     int64
	Failed   int64
}

type BatchRelay struct {
	sink       *BoundedSink
	batchSize  int
	flushEvery time.Duration
	writer     EventWriter

	done chan struct{}
	wg   sync.WaitGroup

	startOnce sync.Once
	closeOnce sync.Once
	errMu     sync.Mutex
	err       error

	sent   atomic.Int64
	failed atomic.Int64
}

func NewBatchRelay(sink *BoundedSink, cfg BatchRelayConfig) (*BatchRelay, error) {
	if sink == nil {
		return nil, fmt.Errorf("metrics sink is required")
	}
	if cfg.Writer == nil {
		return nil, fmt.Errorf("metrics event writer is required")
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = defaultBatchRelaySize
	}
	if cfg.FlushEvery <= 0 {
		cfg.FlushEvery = defaultBatchRelayInterval
	}
	return &BatchRelay{
		sink:       sink,
		batchSize:  cfg.BatchSize,
		flushEvery: cfg.FlushEvery,
		writer:     cfg.Writer,
		done:       make(chan struct{}),
	}, nil
}

func (r *BatchRelay) Start() {
	if r == nil {
		return
	}
	r.startOnce.Do(func() {
		r.wg.Add(1)
		go r.run()
	})
}

func (r *BatchRelay) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.Start()
	r.closeOnce.Do(func() {
		close(r.done)
	})
	wait := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(wait)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-wait:
		return r.firstErr()
	}
}

func (r *BatchRelay) Stats() BatchRelayStats {
	if r == nil {
		return BatchRelayStats{}
	}
	snapshot := r.sink.Snapshot()
	return BatchRelayStats{
		Buffered: snapshot.Buffered,
		Dropped:  snapshot.Dropped,
		Sent:     r.sent.Load(),
		Failed:   r.failed.Load(),
	}
}

func (r *BatchRelay) run() {
	defer r.wg.Done()
	ticker := time.NewTicker(r.flushEvery)
	defer ticker.Stop()

	batch := make([]Event, 0, r.batchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		r.write(context.Background(), batch)
		batch = batch[:0]
	}

	for {
		events := r.sink.Drain(r.batchSize - len(batch))
		if len(events) > 0 {
			batch = append(batch, events...)
			if len(batch) >= r.batchSize {
				flush()
				continue
			}
		}
		select {
		case <-ticker.C:
			flush()
		case <-r.done:
			for {
				events := r.sink.Drain(r.batchSize - len(batch))
				if len(events) == 0 {
					flush()
					return
				}
				batch = append(batch, events...)
				if len(batch) >= r.batchSize {
					flush()
				}
			}
		case <-r.sink.notify:
		}
	}
}

func (r *BatchRelay) write(ctx context.Context, events []Event) {
	if len(events) == 0 {
		return
	}
	if err := r.writer.WriteEvents(ctx, events); err != nil {
		r.failed.Add(int64(len(events)))
		r.recordErr(err)
		return
	}
	r.sent.Add(int64(len(events)))
}

func (r *BatchRelay) recordErr(err error) {
	r.errMu.Lock()
	defer r.errMu.Unlock()
	if r.err == nil {
		r.err = err
	}
}

func (r *BatchRelay) firstErr() error {
	r.errMu.Lock()
	defer r.errMu.Unlock()
	return r.err
}
