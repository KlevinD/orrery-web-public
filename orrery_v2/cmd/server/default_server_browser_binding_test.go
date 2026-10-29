package main

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/config"
	toolres "orrery_v2/internal/resource/tool"
)

func TestConfiguredServerBrowserAgentIDsIncludesDiscussionAgentAndExplicitAgents(t *testing.T) {
	t.Setenv(defaultServerBrowserAgentIDsEnv, " gangbeng, LEAD-AGENT; xiaodoubao\n gangbeng ")

	got := configuredServerBrowserAgentIDs(config.Config{})
	want := []string{defaultLeadAgentID, "A000001", "A000009"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("configured server browser agent IDs = %v, want %v", got, want)
	}
}

func TestEnsureAgentServerBrowserBindsAndRepairsOnce(t *testing.T) {
	ctx := context.Background()
	tools := toolres.NewMemoryRegistry()
	t.Setenv(defaultServerBrowserMCPCommandEnv, "server-browser-mcp")
	t.Setenv(defaultServerBrowserCDPURLCommandEnv, "http://127.0.0.1:19222")
	definition, ok := defaultServerBrowserMCPServerDefinition(time.Now())
	if !ok {
		t.Fatal("server browser definition was not created")
	}
	if err := tools.Register(ctx, definition); err != nil {
		t.Fatalf("register server browser: %v", err)
	}
	store := &serverBrowserAgentProfileStore{profile: agentcore.AgentProfile{
		ID:        "gangbeng",
		AgentKind: agentcore.AgentKindAI,
		Enabled:   true,
		ToolRefs: []agentcore.ToolRef{
			{ToolID: "tool.keep", PermissionScope: "keep.read"},
			{ToolID: "MCP.SERVER_BROWSER", Alias: "legacy", PermissionScope: "legacy.read", BindingPolicy: "allow"},
		},
	}}

	if err := ensureAgentServerBrowser(ctx, store, tools, " gangbeng "); err != nil {
		t.Fatalf("ensure server browser binding: %v", err)
	}
	if store.saves != 1 {
		t.Fatalf("save count = %d, want 1", store.saves)
	}
	if !hasToolRef(store.profile.ToolRefs, "tool.keep") {
		t.Fatalf("unrelated tool ref was removed: %+v", store.profile.ToolRefs)
	}
	browserRefs := toolRefsByID(store.profile.ToolRefs, defaultServerBrowserMCPServerToolID)
	if len(browserRefs) != 1 {
		t.Fatalf("browser refs = %+v, want exactly one", browserRefs)
	}
	if got := browserRefs[0]; got.Alias != defaultServerBrowserToolAlias ||
		got.PermissionScope != defaultServerBrowserPermission || got.BindingPolicy != "required" {
		t.Fatalf("browser ref = %+v", got)
	}

	if err := ensureAgentServerBrowser(ctx, store, tools, "gangbeng"); err != nil {
		t.Fatalf("ensure idempotent server browser binding: %v", err)
	}
	if store.saves != 1 {
		t.Fatalf("idempotent save count = %d, want 1", store.saves)
	}
}

func TestEnsureAgentServerBrowserSkipsUnavailableMissingOrHuman(t *testing.T) {
	ctx := context.Background()
	store := &serverBrowserAgentProfileStore{profile: agentcore.AgentProfile{
		ID:        "gangbeng",
		AgentKind: agentcore.AgentKindAI,
		Enabled:   true,
	}}
	if err := ensureAgentServerBrowser(ctx, store, toolres.NewMemoryRegistry(), "gangbeng"); err != nil {
		t.Fatalf("ensure without registered browser: %v", err)
	}
	if store.saves != 0 || len(store.profile.ToolRefs) != 0 {
		t.Fatalf("unavailable browser changed profile: %+v", store.profile)
	}

	t.Setenv(defaultServerBrowserMCPCommandEnv, "server-browser-mcp")
	t.Setenv(defaultServerBrowserCDPURLCommandEnv, "http://127.0.0.1:19222")
	definition, ok := defaultServerBrowserMCPServerDefinition(time.Now())
	if !ok {
		t.Fatal("server browser definition was not created")
	}
	tools := toolres.NewMemoryRegistry()
	if err := tools.Register(ctx, definition); err != nil {
		t.Fatalf("register server browser: %v", err)
	}
	missing := &serverBrowserAgentProfileStore{}
	if err := ensureAgentServerBrowser(ctx, missing, tools, "retired-agent"); err != nil {
		t.Fatalf("ensure for missing profile: %v", err)
	}
	if missing.saves != 0 {
		t.Fatalf("missing profile save count = %d, want 0", missing.saves)
	}
	store.profile.AgentKind = agentcore.AgentKindHuman
	if err := ensureAgentServerBrowser(ctx, store, tools, "gangbeng"); err != nil {
		t.Fatalf("ensure for human profile: %v", err)
	}
	if store.saves != 0 || len(store.profile.ToolRefs) != 0 {
		t.Fatalf("human profile received browser binding: %+v", store.profile)
	}
}

type serverBrowserAgentProfileStore struct {
	profile agentcore.AgentProfile
	saves   int
}

func (s *serverBrowserAgentProfileStore) GetProfile(_ context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	if s == nil || s.profile.ID != id {
		return agentcore.AgentProfile{}, fmt.Errorf("%w: %s", agentcore.ErrNotFound, id)
	}
	return s.profile, nil
}

func (s *serverBrowserAgentProfileStore) SaveProfile(_ context.Context, profile agentcore.AgentProfile) error {
	s.profile = profile
	s.saves++
	return nil
}

func toolRefsByID(refs []agentcore.ToolRef, id agentcore.ToolID) []agentcore.ToolRef {
	out := []agentcore.ToolRef{}
	for _, ref := range refs {
		if ref.ToolID == id {
			out = append(out, ref)
		}
	}
	return out
}
