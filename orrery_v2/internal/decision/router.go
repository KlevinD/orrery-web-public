package decision

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"
)

type Profile struct {
	ProfileID    string `json:"profile_id"`
	Adapter      string `json:"adapter"`
	Enabled      bool   `json:"enabled"`
	LLMProfileID string `json:"llm_profile_id,omitempty"`
	Model        string `json:"model,omitempty"`
	TimeoutMS    int    `json:"timeout_ms,omitempty"`
}
type Config struct {
	DefaultProfileID string            `json:"default_profile_id"`
	SceneProfiles    map[string]string `json:"scene_profiles,omitempty"`
	Profiles         []Profile         `json:"profiles"`
}
type ConfigProvider func(context.Context) (Config, error)
type Factory func(context.Context, Profile) (Resolved, error)

// Router owns no provider-specific dependencies. Register a factory to add an
// implementation; explicit missing/disabled routes never silently fall back.
type Router struct {
	configs   ConfigProvider
	factories map[string]Factory
}

func NewRouter(configs ConfigProvider, factories map[string]Factory) *Router {
	copy := make(map[string]Factory, len(factories))
	for k, v := range factories {
		copy[k] = v
	}
	return &Router{configs: configs, factories: copy}
}
func (r *Router) Validate(ctx context.Context) error {
	if r == nil || r.configs == nil {
		return ErrConfiguration
	}
	c, err := r.configs(ctx)
	if err != nil {
		return ErrConfiguration
	}
	return r.validate(c)
}
func (r *Router) validate(c Config) error {
	seen := map[string]Profile{}
	for _, p := range c.Profiles {
		if p.ProfileID == "" || strings.TrimSpace(p.ProfileID) != p.ProfileID || len(p.ProfileID) > 128 || p.Adapter == "" || p.TimeoutMS < 0 || p.TimeoutMS > 60000 {
			return ErrConfiguration
		}
		if _, ok := seen[p.ProfileID]; ok {
			return ErrConfiguration
		}
		seen[p.ProfileID] = p
		if p.Enabled && r.factories[p.Adapter] == nil {
			return ErrConfiguration
		}
		if p.Adapter == "laya" && (p.LLMProfileID != "" || p.Model != "") {
			return ErrConfiguration
		}
	}
	valid := func(id string) bool { p, ok := seen[id]; return ok && p.Enabled }
	if !valid(c.DefaultProfileID) {
		return ErrConfiguration
	}
	for scene, id := range c.SceneProfiles {
		if scene == "" || len(scene) > 32 || !valid(id) {
			return ErrConfiguration
		}
	}
	return nil
}
func (r *Router) Resolve(ctx context.Context, scene string) (Resolved, error) {
	if err := ctx.Err(); err != nil {
		return Resolved{}, err
	}
	if r == nil || r.configs == nil {
		return Resolved{}, ErrConfiguration
	}
	c, err := r.configs(ctx)
	if err != nil || r.validate(c) != nil {
		return Resolved{}, ErrConfiguration
	}
	id, source := c.DefaultProfileID, "decision_default"
	if v, ok := c.SceneProfiles[scene]; ok {
		id, source = v, "scene_profile"
	}
	for _, p := range c.Profiles {
		if p.ProfileID == id {
			out, err := r.factories[p.Adapter](ctx, p)
			if err != nil {
				if ctx.Err() != nil {
					return Resolved{}, ctx.Err()
				}
				return Resolved{}, ErrConfiguration
			}
			if out.Judge == nil || out.Metadata.ModelRevision == "" {
				return Resolved{}, ErrConfiguration
			}
			out.Metadata.ProfileID = p.ProfileID
			out.Metadata.Adapter = p.Adapter
			out.Metadata.SelectionSource = source
			if p.TimeoutMS > 0 {
				out.Timeout = time.Duration(p.TimeoutMS) * time.Millisecond
			}
			if out.Timeout <= 0 {
				out.Timeout = 5 * time.Second
			}
			return out, nil
		}
	}
	return Resolved{}, ErrConfiguration
}

// FileConfig reads on each assessment so operators can atomically replace the
// configuration without restarting sessions. Keys stay in the secret store.
func FileConfig(path string) ConfigProvider {
	return func(ctx context.Context) (Config, error) {
		if err := ctx.Err(); err != nil {
			return Config{}, err
		}
		f, err := os.Open(path)
		if err != nil {
			return Config{}, ErrConfiguration
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, 65537))
		if err != nil || len(data) > 65536 {
			return Config{}, ErrConfiguration
		}
		var c Config
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&c) != nil {
			return Config{}, ErrConfiguration
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			return Config{}, ErrConfiguration
		}
		return c, nil
	}
}
