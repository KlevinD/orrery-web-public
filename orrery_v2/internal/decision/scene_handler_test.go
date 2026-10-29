package decision

import (
	"context"
	"testing"
	"time"
)

func TestObserverSceneHandlerBypassesOnlyItsScene(t *testing.T) {
	handled := make(chan Job, 1)
	records := make(chan Record, 1)
	handlers := map[string]func(Job){"rsi_sample": func(job Job) { handled <- job }}
	o := NewObserverWithHandlers(context.Background(), nil, func(record Record) { records <- record }, handlers)
	defer o.Close()
	delete(handlers, "rsi_sample") // The observer owns an immutable copy.
	job := choiceJob()
	job.Kind = "rsi_sample"
	if !o.Submit(context.Background(), job) {
		t.Fatal("specialized scene rejected")
	}
	select {
	case got := <-handled:
		if got.Kind != job.Kind || got.InputHashes[0] != job.InputHashes[0] {
			t.Fatal("handler lost scene provenance")
		}
	case <-time.After(time.Second):
		t.Fatal("specialized scene reached the missing model resolver")
	}
	if !o.Submit(context.Background(), choiceJob()) {
		t.Fatal("ordinary scene rejected")
	}
	select {
	case record := <-records:
		if record.Kind != "knowledge" || record.Fallback != "configuration_unavailable" {
			t.Fatalf("ordinary routing changed: %+v", record)
		}
	case <-time.After(time.Second):
		t.Fatal("ordinary scene was intercepted")
	}
}
