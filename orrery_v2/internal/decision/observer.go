package decision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"
	"unicode/utf8"
)

type Job struct {
	ObservationID  string
	InputTruncated bool
	Kind           string
	Scope          string
	PromptVersion  string
	Sources        []string
	// Hash the complete source, including content omitted from the excerpt.
	InputHashes []string
	States      []string
	Question    Question
}

type Record struct {
	ObservationID  string `json:"observation_id,omitempty"`
	InputTruncated bool   `json:"input_truncated,omitempty"`
	// Set by the RSI evidence store when judgments of the same immutable
	// observation disagree; this is host metadata, not model confidence.
	Conflicting bool      `json:"conflicting,omitempty"`
	At          time.Time `json:"at"`
	Kind        string    `json:"kind"`
	ScopeHash   string    `json:"scope_hash"`
	Metadata
	ModelRevision string     `json:"model_revision"`
	PromptVersion string     `json:"prompt_version"`
	Sources       []string   `json:"sources"`
	InputHashes   []string   `json:"input_hashes"`
	Decisions     []Decision `json:"decisions,omitempty"`
	ProposedOrder []string   `json:"proposed_order,omitempty"`
	Fallback      string     `json:"fallback,omitempty"`
	Cached        bool       `json:"cached"`
	ElapsedMS     int64      `json:"elapsed_ms"`
	Mode          string     `json:"mode"`
}

type cacheEntry struct {
	record  Record
	expires time.Time
}

// Observer copies bounded inputs into one background worker. It never returns
// a decision to a production caller, and never receives mutable task results.
type Observer struct {
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	queue    chan Job
	resolver Resolver
	sink     func(Record)
	handlers map[string]func(Job)
	cache    map[string]cacheEntry
	mu       sync.Mutex
	closed   bool
}

func NewObserver(ctx context.Context, resolver Resolver, sink func(Record)) *Observer {
	return NewObserverWithHandlers(ctx, resolver, sink, nil)
}

// NewObserverWithHandlers routes selected scenes to specialized background
// handlers instead of bounded classification. Handlers share the bounded
// queue and shutdown lifecycle, and must persist their own outcome.
func NewObserverWithHandlers(ctx context.Context, resolver Resolver, sink func(Record), handlers map[string]func(Job)) *Observer {
	ctx, cancel := context.WithCancel(ctx)
	copyHandlers := make(map[string]func(Job), len(handlers))
	for scene, handler := range handlers {
		copyHandlers[scene] = handler
	}
	o := &Observer{ctx: ctx, cancel: cancel, done: make(chan struct{}), queue: make(chan Job, 8), resolver: resolver, sink: sink, handlers: copyHandlers, cache: make(map[string]cacheEntry)}
	go o.run()
	return o
}

func (o *Observer) Submit(ctx context.Context, job Job) bool {
	if o == nil || ctx.Err() != nil {
		return false
	}
	// Bound provenance and cache keys as well as model payloads.
	if job.Kind == "" || job.Scope == "" || job.PromptVersion == "" || len(job.Scope) > 512 || len(job.PromptVersion) > 128 || len(job.Kind) > 32 || (job.ObservationID != "" && len(job.ObservationID) != 64) || len(job.States) > MaxCandidates || len(job.Sources) != len(job.States) || len(job.InputHashes) != len(job.States) {
		return false
	}
	for i, ref := range job.Sources {
		if ref == "" || len(ref) > 512 || len(job.InputHashes[i]) != 64 {
			return false
		}
	}
	if ValidateInput(job.States, job.Question) != nil {
		o.emit(recordFor(job, "budget_exceeded"))
		return false
	}
	job.States = append([]string(nil), job.States...)
	job.Sources = append([]string(nil), job.Sources...)
	job.InputHashes = append([]string(nil), job.InputHashes...)
	criteria := make(map[string]string, len(job.Question.Criteria))
	for k, v := range job.Question.Criteria {
		criteria[k] = v
	}
	job.Question.Criteria = criteria
	o.mu.Lock()
	if o.closed || o.ctx.Err() != nil {
		o.mu.Unlock()
		return false
	}
	select {
	case o.queue <- job:
		o.mu.Unlock()
		return true
	default:
		o.mu.Unlock()
		o.emit(recordFor(job, "queue_full"))
		return false
	}
}

func (o *Observer) Close() {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.closed = true
	o.cancel()
	o.mu.Unlock()
	<-o.done
}

func (o *Observer) run() {
	defer close(o.done)
	defer func() {
		for {
			select {
			case job := <-o.queue:
				o.emit(recordFor(job, "cancelled"))
			default:
				return
			}
		}
	}()
	for {
		if o.ctx.Err() != nil {
			return
		}
		select {
		case <-o.ctx.Done():
			return
		case job := <-o.queue:
			if handler := o.handlers[job.Kind]; handler != nil {
				handler(job)
			} else {
				o.assess(job)
			}
		}
	}
}

func (o *Observer) assess(job Job) {
	started := time.Now()
	if o.resolver == nil {
		o.emit(recordFor(job, "configuration_unavailable"))
		return
	}
	resolveCtx, resolveCancel := context.WithTimeout(o.ctx, 5*time.Second)
	resolved, resolveErr := o.resolver.Resolve(resolveCtx, job.Kind)
	resolveCancel()
	if resolveErr == nil && (resolved.Judge == nil || resolved.Timeout <= 0 || resolved.Metadata.ModelRevision == "") {
		resolveErr = ErrConfiguration
	}
	if resolveErr != nil {
		reason := "configuration_unavailable"
		if o.ctx.Err() != nil {
			reason = "cancelled"
		}
		o.emit(recordFor(job, reason))
		return
	}
	raw, _ := json.Marshal(struct {
		Job      Job
		Metadata Metadata
		CacheKey string
	}{job, resolved.Metadata, resolved.Metadata.CacheKey})
	key := Hash(string(raw))
	if cached, ok := o.cache[key]; ok && time.Now().Before(cached.expires) {
		record := cached.record
		record.At = time.Now().UTC()
		record.Cached = true
		record.ElapsedMS = 0
		o.emit(record)
		return
	}
	ctx, cancel := context.WithTimeout(o.ctx, resolved.Timeout)
	decisions, err := resolved.Judge.Choose(ctx, job.States, job.Question)
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	if err == nil {
		err = ValidateDecisions(decisions, len(job.States), job.Question)
	}
	reason := ""
	if err != nil {
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded):
			reason = "timeout"
		case errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled):
			reason = "cancelled"
		case errors.Is(err, ErrBudget):
			reason = "budget_exceeded"
		case errors.Is(err, ErrResponse):
			reason = "invalid_response"
		default:
			reason = "inference_unavailable"
		}
	}
	cancel()
	record := recordFor(job, reason)
	record.Metadata = resolved.Metadata
	record.Metadata.CacheKey = ""
	record.ModelRevision = resolved.Metadata.ModelRevision
	record.ElapsedMS = time.Since(started).Milliseconds()
	if err == nil && len(decisions) != len(job.Sources) {
		record.Fallback = "invalid_response"
	}
	if record.Fallback == "" {
		record.Decisions = decisions
		if job.Kind == "knowledge" || job.Kind == "memory" {
			indices := make([]int, len(decisions))
			for i := range indices {
				indices[i] = i
			}
			priority := map[string]int{"direct": 3, "partial": 2, "insufficient": 1, "unrelated": 0}
			sort.SliceStable(indices, func(i, j int) bool {
				return priority[decisions[indices[i]].Choice] > priority[decisions[indices[j]].Choice]
			})
			for _, i := range indices {
				record.ProposedOrder = append(record.ProposedOrder, job.Sources[i])
			}
		}
		for k, v := range o.cache {
			if time.Now().After(v.expires) {
				delete(o.cache, k)
			}
		}
		if len(o.cache) >= 128 {
			clear(o.cache)
		}
		o.cache[key] = cacheEntry{record: record, expires: time.Now().Add(10 * time.Minute)}
	}
	o.emit(record)
}

func recordFor(job Job, reason string) Record {
	return Record{ObservationID: job.ObservationID, InputTruncated: job.InputTruncated, At: time.Now().UTC(), Kind: job.Kind, ScopeHash: Hash(job.Scope), ModelRevision: "unresolved", Metadata: Metadata{Adapter: "unresolved"},
		PromptVersion: job.PromptVersion, Sources: append([]string(nil), job.Sources...), InputHashes: append([]string(nil), job.InputHashes...), Fallback: reason, Mode: "shadow"}
}

func (o *Observer) emit(record Record) {
	if o.sink != nil {
		o.sink(record)
	}
}

func Hash(text string) string { sum := sha256.Sum256([]byte(text)); return hex.EncodeToString(sum[:]) }

func Excerpt(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	end := maxBytes
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end]
}
