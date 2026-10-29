package core

import (
	"encoding/json"
	"testing"
)

func TestRuntimeKindAcceptsOpenClaw(t *testing.T) {
	if !RuntimeKindOpenClaw.IsValid() {
		t.Fatalf("openclaw runtime kind should be valid")
	}
}

func TestRuntimeKindAcceptsOpenCode(t *testing.T) {
	if !RuntimeKindOpenCode.IsValid() {
		t.Fatalf("opencode runtime kind should be valid")
	}
}

func TestRuntimeKindAcceptsHermes(t *testing.T) {
	if !RuntimeKindHermes.IsValid() {
		t.Fatalf("hermes runtime kind should be valid")
	}
}

func TestRuntimeKindAcceptsManaged(t *testing.T) {
	if !RuntimeKindManaged.IsValid() {
		t.Fatalf("managed runtime kind should be valid")
	}
}

func TestRuntimeKindAcceptsPi(t *testing.T) {
	if !RuntimeKindPi.IsValid() {
		t.Fatalf("pi runtime kind should be valid")
	}
}

func TestRuntimeKindAcceptsACP(t *testing.T) {
	if !RuntimeKindACP.IsValid() {
		t.Fatal("ACP runtime kind should be valid")
	}
}

func TestRuntimeSpecHasConfigTreatsJSONNullAsEmpty(t *testing.T) {
	if (RuntimeSpec{Config: json.RawMessage(`null`)}).HasConfig() {
		t.Fatalf("runtime config JSON null should be treated as empty")
	}
}

func TestRuntimeSpecHasConfigTreatsJSONObjectAsConfigured(t *testing.T) {
	if !(RuntimeSpec{Config: json.RawMessage(`{"executable":"pi"}`)}).HasConfig() {
		t.Fatalf("runtime config object should be treated as configured")
	}
}
