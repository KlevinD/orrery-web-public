package llmprofile

import (
	"context"
	"testing"
)

func TestSelectionUsesLiveDefaultAndExplicitOverride(t *testing.T) {
	profiles := []LLMProfile{
		{ProfileID: "old", Model: "old-model", Enabled: true},
		{ProfileID: "current", Model: "current-model", Enabled: true, GlobalDefault: true},
		{ProfileID: "disabled", Enabled: false},
	}
	for _, tc := range []struct {
		name              string
		req               SelectionRequest
		id, model, source string
		fail              bool
	}{
		{"global", SelectionRequest{}, "current", "current-model", "global_default", false},
		{"inherit ignores legacy hint", SelectionRequest{ProfileID: PlatformDefaultProfileID, ConfigRef: "old"}, "current", "current-model", "global_default", false},
		{"explicit", SelectionRequest{ProfileID: "old", ConfigRef: "current"}, "old", "old-model", "explicit_profile", false},
		{"model override", SelectionRequest{Model: "custom"}, "current", "custom", "global_default", false},
		{"runtime ref", SelectionRequest{ConfigRef: "runtime-pi"}, "current", "current-model", "global_default", false},
		{"disabled", SelectionRequest{ProfileID: "disabled"}, "", "", "", true},
		{"missing", SelectionRequest{ProfileID: "missing"}, "", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Select(profiles, tc.req)
			if (err != nil) != tc.fail {
				t.Fatalf("selection=%+v error=%v", got, err)
			}
			if !tc.fail && (got.Profile.ProfileID != tc.id || got.Profile.Model != tc.model || got.Source != tc.source) {
				t.Fatalf("selection=%+v", got)
			}
		})
	}
	provider := &StaticProfileProvider{Profiles: profiles}
	first, err := Resolve(context.Background(), provider, SelectionRequest{ProfileID: PlatformDefaultProfileID})
	if err != nil {
		t.Fatal(err)
	}
	provider.Profiles[0].GlobalDefault, provider.Profiles[1].GlobalDefault = true, false
	second, err := Resolve(context.Background(), provider, SelectionRequest{ProfileID: PlatformDefaultProfileID})
	if err != nil {
		t.Fatal(err)
	}
	if first.Profile.ProfileID == second.Profile.ProfileID {
		t.Fatal("default change did not take effect")
	}
}

func TestSelectionRejectsAmbiguousConfigurationAndCancelledContext(t *testing.T) {
	_, err := Select([]LLMProfile{{Enabled: true, GlobalDefault: true}, {Enabled: true, GlobalDefault: true}}, SelectionRequest{})
	if err == nil {
		t.Fatal("multiple defaults accepted")
	}
	_, err = Select([]LLMProfile{{ProfileID: "same", Enabled: true}, {ProfileID: "same", Enabled: true}}, SelectionRequest{ProfileID: "same"})
	if err == nil {
		t.Fatal("duplicate explicit profiles accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Resolve(ctx, StaticProfileProvider{}, SelectionRequest{}); err != context.Canceled {
		t.Fatalf("error=%v", err)
	}
}
