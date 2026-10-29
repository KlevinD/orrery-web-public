package operations

import "testing"

func TestOrganizationCodeHelpers(t *testing.T) {
	tests := []struct {
		code        string
		displayCode string
		level       int
		valid       bool
	}{
		{code: "01", displayCode: "01", level: 1, valid: true},
		{code: "0101", displayCode: "01.01", level: 2, valid: true},
		{code: "010101", displayCode: "01.01.01", level: 3, valid: true},
		{code: "", valid: false},
		{code: "1", valid: false},
		{code: "010", valid: false},
		{code: "01A1", valid: false},
	}

	for _, tc := range tests {
		if got := validOrganizationCode(tc.code); got != tc.valid {
			t.Fatalf("validOrganizationCode(%q) = %v, want %v", tc.code, got, tc.valid)
		}
		if !tc.valid {
			continue
		}
		if got := displayOrganizationCode(tc.code); got != tc.displayCode {
			t.Fatalf("displayOrganizationCode(%q) = %q, want %q", tc.code, got, tc.displayCode)
		}
		if got := organizationCodeLevel(tc.code); got != tc.level {
			t.Fatalf("organizationCodeLevel(%q) = %d, want %d", tc.code, got, tc.level)
		}
	}
}

func TestNextOrganizationCode(t *testing.T) {
	next, err := nextOrganizationCode("", nil)
	if err != nil {
		t.Fatalf("next root code: %v", err)
	}
	if next != "01" {
		t.Fatalf("next root code = %q, want 01", next)
	}

	next, err = nextOrganizationCode("01", []string{"0101", "0102"})
	if err != nil {
		t.Fatalf("next child code: %v", err)
	}
	if next != "0103" {
		t.Fatalf("next child code = %q, want 0103", next)
	}
}

func TestNextOrganizationCodeRejectsFullSiblingSet(t *testing.T) {
	existing := make([]string, 0, 99)
	for i := 1; i <= 99; i++ {
		existing = append(existing, "01"+twoDigitSuffix(i))
	}
	if _, err := nextOrganizationCode("01", existing); err == nil {
		t.Fatal("expected 100th child to be rejected")
	}
}
