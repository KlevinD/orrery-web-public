package identity

import "testing"

func TestCanonicalProfileIDMapsManagedDigitalEmployees(t *testing.T) {
	tests := map[string]string{
		"gangbeng":                      TiebingProfileID,
		"lead-agent":                    LeadProfileID,
		"website-receptionist-customer": WebsiteCustomerReceptionistProfileID,
		"H000001":                       "H000001",
	}
	for input, want := range tests {
		if got := CanonicalProfileID(input); got != want {
			t.Fatalf("CanonicalProfileID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestLegacyProfileIDReturnsSourceIdentity(t *testing.T) {
	if got := LegacyProfileID(TangdouProfileID); got != "tangdou" {
		t.Fatalf("LegacyProfileID(%q) = %q", TangdouProfileID, got)
	}
}
