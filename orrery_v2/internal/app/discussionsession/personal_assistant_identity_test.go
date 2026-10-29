package discussionsession

import (
	"strings"
	"testing"
)

func TestPersonalAssistantAgentIDIsStableAndOwnerScoped(t *testing.T) {
	first := PersonalAssistantAgentID("tenant-a", "account-1")
	if first == "" || first != PersonalAssistantAgentID("tenant-a", "account-1") {
		t.Fatalf("personal assistant id is not stable: %q", first)
	}
	if !IsPersonalAssistantAgentID(first) {
		t.Fatalf("personal assistant id is invalid: %q", first)
	}
	if strings.Contains(first, "tenant-a") || strings.Contains(first, "account-1") {
		t.Fatalf("personal assistant id exposes its owner: %q", first)
	}
	if first == PersonalAssistantAgentID("tenant-a", "account-2") {
		t.Fatal("different owners received the same personal assistant id")
	}
	if first == PersonalAssistantAgentID("tenant-b", "account-1") {
		t.Fatal("the same owner id in different tenants received the same personal assistant id")
	}
	if got := PersonalAssistantMemoryProfileID(first); got != "memory-"+first {
		t.Fatalf("memory profile id = %q", got)
	}
}

func TestPersonalAssistantAgentIDRequiresOwner(t *testing.T) {
	if got := PersonalAssistantAgentID("tenant-a", "  "); got != "" {
		t.Fatalf("personal assistant id without owner = %q", got)
	}
	if IsPersonalAssistantAgentID("personal-assistant-account-1") {
		t.Fatal("unhashed personal assistant id was accepted")
	}
	if IsPersonalAssistantAgentID("personal-assistant-" + strings.Repeat("z", 64)) {
		t.Fatal("non-hex personal assistant id was accepted")
	}
}

func TestRegisteredPersonalAssistantAgentIDUsesEmployeeNumber(t *testing.T) {
	if err := RegisterPersonalAssistantAgentID("tenant-employee-number", "owner-employee-number", "A000016"); err != nil {
		t.Fatal(err)
	}
	if got := PersonalAssistantAgentID("tenant-employee-number", "owner-employee-number"); got != "A000016" {
		t.Fatalf("PersonalAssistantAgentID() = %q, want A000016", got)
	}
	if !IsPersonalAssistantAgentID("A000016") {
		t.Fatal("registered employee number is not recognized as a personal assistant ID")
	}
	if got := PersonalAssistantMemoryProfileID("A000016"); got != "memory-A000016" {
		t.Fatalf("PersonalAssistantMemoryProfileID() = %q", got)
	}
}
