package core

import "testing"

func TestMemoryLayerDefaultsAndValidation(t *testing.T) {
	if got := NormalizeMemoryLayer(""); got != MemoryLayerFact {
		t.Fatalf("NormalizeMemoryLayer empty = %q, want %q", got, MemoryLayerFact)
	}
	for _, layer := range []MemoryLayer{MemoryLayerFact, MemoryLayerExperience, MemoryLayerWorld} {
		if !layer.IsValid() {
			t.Fatalf("layer %q should be valid", layer)
		}
		if got := NormalizeMemoryLayer(layer); got != layer {
			t.Fatalf("NormalizeMemoryLayer(%q) = %q", layer, got)
		}
	}
	if MemoryLayer("unknown").IsValid() {
		t.Fatalf("unknown layer should be invalid")
	}
}

func TestMemoryLifecycleDefaultsAndValidation(t *testing.T) {
	if got := NormalizeMemoryLifecycleStatus(""); got != MemoryLifecycleActive {
		t.Fatalf("NormalizeMemoryLifecycleStatus empty = %q, want %q", got, MemoryLifecycleActive)
	}
	for _, status := range []MemoryLifecycleStatus{
		MemoryLifecycleActive,
		MemoryLifecycleSettled,
		MemoryLifecycleDistilled,
		MemoryLifecycleArchived,
		MemoryLifecycleDeleted,
		MemoryLifecycleAnonymized,
	} {
		if !status.IsValid() {
			t.Fatalf("status %q should be valid", status)
		}
	}
	if MemoryLifecycleStatus("unknown").IsValid() {
		t.Fatalf("unknown lifecycle status should be invalid")
	}
}

func TestSensitivityDefaults(t *testing.T) {
	if got := NormalizeSensitivity(""); got != SensitivityNormal {
		t.Fatalf("NormalizeSensitivity empty = %q, want %q", got, SensitivityNormal)
	}
	if got := NormalizeDeIDLevel(""); got != DeIDRaw {
		t.Fatalf("NormalizeDeIDLevel empty = %q, want %q", got, DeIDRaw)
	}
	if got := NormalizeConsentState(""); got != ConsentNotRequired {
		t.Fatalf("NormalizeConsentState empty = %q, want %q", got, ConsentNotRequired)
	}
}
