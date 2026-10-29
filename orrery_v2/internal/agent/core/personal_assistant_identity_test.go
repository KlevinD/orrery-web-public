package core

import "testing"

func TestPersonalAssistantProfileIdentityRegistrySupportsCanonicalIDs(t *testing.T) {
	if err := RegisterPersonalAssistantProfileID("tenant-canonical", "owner-canonical", "A009999"); err != nil {
		t.Fatal(err)
	}
	if got := PersonalAssistantProfileIDForOwner("tenant-canonical", "owner-canonical"); got != "A009999" {
		t.Fatalf("registered personal assistant profile ID = %q", got)
	}
	if !IsPersonalAssistantProfileID("A009999") {
		t.Fatal("registered canonical profile ID was not recognized")
	}
	if IsPersonalAssistantProfileID("A009998") {
		t.Fatal("unregistered canonical profile ID was recognized")
	}
}
