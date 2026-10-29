package realtime

import "testing"

func TestProjectChannelScopesSameProjectIDByTenantAndRoundTrips(t *testing.T) {
	first, err := ProjectChannel("tenant-a", "shared-project")
	if err != nil {
		t.Fatal(err)
	}
	second, err := ProjectChannel("tenant-b", "shared-project")
	if err != nil {
		t.Fatal(err)
	}
	if first == second || first == "project:shared-project" || second == "project:shared-project" {
		t.Fatalf("project channels are not tenant-scoped and opaque: first=%q second=%q", first, second)
	}
	tenantID, projectID, err := ParseProjectChannel(first)
	if err != nil || tenantID != "tenant-a" || projectID != "shared-project" {
		t.Fatalf("ParseProjectChannel(%q) = tenant:%q project:%q err:%v", first, tenantID, projectID, err)
	}
}

func TestParseProjectChannelRejectsLegacyAndMalformedChannels(t *testing.T) {
	for _, channel := range []string{"project:shared-project", "project:", "conversation:abc", "project:dGVuYW50LWE"} {
		if tenantID, projectID, err := ParseProjectChannel(channel); err == nil {
			t.Fatalf("ParseProjectChannel(%q) = tenant:%q project:%q, want error", channel, tenantID, projectID)
		}
	}
}
