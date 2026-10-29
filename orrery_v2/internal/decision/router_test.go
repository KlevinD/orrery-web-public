package decision

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type judgeFunc func(context.Context, []string, Question) ([]Decision, error)

func (f judgeFunc) Choose(ctx context.Context, s []string, q Question) ([]Decision, error) {
	return f(ctx, s, q)
}
func choiceJob() Job {
	return Job{Kind: "knowledge", Scope: "tenant/agent", PromptVersion: "v1", States: []string{"evidence"}, Sources: []string{"doc:1"}, InputHashes: []string{Hash("full evidence")}, Question: Question{Type: "choice", Instructions: "Classify evidence", Criteria: map[string]string{"a": "relevant", "b": "unrelated"}}}
}

func TestRouterSceneSelectionAndCacheFollowModelChanges(t *testing.T) {
	var calls atomic.Int32
	model := "first"
	c := Config{DefaultProfileID: "general", SceneProfiles: map[string]string{"memory": "special"}, Profiles: []Profile{{ProfileID: "general", Adapter: "custom", Enabled: true}, {ProfileID: "special", Adapter: "custom", Enabled: true, TimeoutMS: 100}}}
	router := NewRouter(func(context.Context) (Config, error) { return c, nil }, map[string]Factory{"custom": func(context.Context, Profile) (Resolved, error) {
		return Resolved{Judge: judgeFunc(func(context.Context, []string, Question) ([]Decision, error) {
			calls.Add(1)
			return []Decision{{Choice: "a"}}, nil
		}), Metadata: Metadata{Provider: "custom", Model: model, ModelRevision: model}}, nil
	}})
	r, err := router.Resolve(context.Background(), "memory")
	if err != nil || r.Metadata.ProfileID != "special" || r.Metadata.SelectionSource != "scene_profile" || r.Timeout != 100*time.Millisecond {
		t.Fatalf("scene route %+v %v", r, err)
	}
	ch := make(chan Record, 1)
	o := NewObserver(context.Background(), router, func(r Record) { ch <- r })
	defer o.Close()
	job := choiceJob()
	for i := 0; i < 3; i++ {
		if i == 2 {
			model = "second"
		}
		if !o.Submit(context.Background(), job) {
			t.Fatal("rejected")
		}
		select {
		case r := <-ch:
			if r.ModelRevision != model || r.Model != model || r.Cached != (i == 1) {
				t.Fatalf("stale model provenance/cache %+v", r)
			}
			raw, _ := json.Marshal(r)
			var decoded Record
			if json.Unmarshal(raw, &decoded) != nil || decoded.ModelRevision != model {
				t.Fatalf("record round trip %s", raw)
			}
		case <-time.After(time.Second):
			t.Fatal("no record")
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("inferences %d", calls.Load())
	}
}

func TestRouterRejectsInvalidExplicitRoutesAndUnknownAdapters(t *testing.T) {
	base := Config{DefaultProfileID: "a", Profiles: []Profile{{ProfileID: "a", Adapter: "known", Enabled: true}}}
	for _, change := range []func(*Config){func(c *Config) { c.SceneProfiles = map[string]string{"memory": "missing"} }, func(c *Config) { c.Profiles[0].Enabled = false }, func(c *Config) { c.Profiles[0].Adapter = "unknown" }, func(c *Config) { c.Profiles = append(c.Profiles, c.Profiles[0]) }} {
		c := base
		c.Profiles = append([]Profile(nil), base.Profiles...)
		change(&c)
		r := NewRouter(func(context.Context) (Config, error) { return c, nil }, map[string]Factory{"known": func(context.Context, Profile) (Resolved, error) {
			t.Fatal("invalid configuration reached adapter")
			return Resolved{}, nil
		}})
		if _, err := r.Resolve(context.Background(), "memory"); !errors.Is(err, ErrConfiguration) {
			t.Fatalf("accepted %+v", c)
		}
	}
}

func TestObserverRejectsInvalidAdapterOutputAndBoundsTimeout(t *testing.T) {
	for _, mode := range []string{"invalid", "timeout", "configuration"} {
		t.Run(mode, func(t *testing.T) {
			ch := make(chan Record, 1)
			r := ResolverFunc(func(context.Context, string) (Resolved, error) {
				if mode == "configuration" {
					return Resolved{}, ErrConfiguration
				}
				return Resolved{Metadata: Metadata{ModelRevision: "custom-v1"}, Timeout: 10 * time.Millisecond, Judge: judgeFunc(func(ctx context.Context, _ []string, _ Question) ([]Decision, error) {
					if mode == "timeout" {
						<-ctx.Done()
						return nil, ctx.Err()
					}
					return []Decision{{Choice: "not-a-criterion"}}, nil
				})}, nil
			})
			o := NewObserver(context.Background(), r, func(r Record) { ch <- r })
			defer o.Close()
			o.Submit(context.Background(), choiceJob())
			select {
			case record := <-ch:
				expected := map[string]string{"invalid": "invalid_response", "timeout": "timeout", "configuration": "configuration_unavailable"}[mode]
				if record.Fallback != expected || len(record.Decisions) > 0 {
					t.Fatalf("invalid result %+v", record)
				}
			case <-time.After(time.Second):
				t.Fatal("unbounded assessment")
			}
		})
	}
}
