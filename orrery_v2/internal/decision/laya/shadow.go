package laya

import (
	"context"
	"orrery_v2/internal/decision"
	"time"
)

// Compatibility aliases keep existing stored assessments and integrations
// readable while the shared assessment pipeline lives in package decision.
type Job = decision.Job
type Record = decision.Record
type Observer = decision.Observer

func NewObserver(ctx context.Context, judge Judge, sink func(Record)) *Observer {
	return decision.NewObserver(ctx, decision.ResolverFunc(func(context.Context, string) (decision.Resolved, error) {
		return decision.Resolved{Judge: judge, Metadata: decision.Metadata{ProfileID: "decision-laya", Adapter: "laya", Provider: "laya", Model: "multilingual", ModelRevision: ModelRevision, ConfidenceKind: "model"}, Timeout: 5 * time.Second}, nil
	}), sink)
}
func Hash(text string) string                   { return decision.Hash(text) }
func Excerpt(value string, maxBytes int) string { return decision.Excerpt(value, maxBytes) }
