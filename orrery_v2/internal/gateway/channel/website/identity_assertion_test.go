package website

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"orrery_v2/internal/gateway/protocol"
)

func TestWebsiteIdentityAssertionAcceptsOnlyFixedPublicAndCustomerClaims(t *testing.T) {
	now := time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC)
	key := []byte("website-assertion-key-0123456789abcdef")
	verifier, err := NewWebsiteIdentityAssertionVerifier(key, func() time.Time { return now })
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	for _, tier := range []protocol.AdvisorTier{protocol.AdvisorTierPublic, protocol.AdvisorTierCustomer} {
		claims := validWebsiteClaims(now, tier, "nonce-"+string(tier))
		identity, err := verifier.Verify(signWebsiteClaims(t, key, claims))
		if err != nil {
			t.Fatalf("verify %s: %v", tier, err)
		}
		if identity.Subject != "customer-1" || identity.Tier != tier {
			t.Fatalf("identity = %+v", identity)
		}
	}
}

func TestWebsiteIdentityAssertionRejectsInvalidOrLegacyAuthority(t *testing.T) {
	now := time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC)
	key := []byte("website-assertion-key-0123456789abcdef")
	tests := map[string]func(map[string]any){
		"issuer":     func(claims map[string]any) { claims["iss"] = "attacker" },
		"audience":   func(claims map[string]any) { claims["aud"] = "orrery-api" },
		"tier":       func(claims map[string]any) { claims["tier"] = "admin" },
		"expired":    func(claims map[string]any) { claims["exp"] = now.Unix() },
		"future":     func(claims map[string]any) { claims["iat"] = now.Add(31 * time.Second).Unix() },
		"no nonce":   func(claims map[string]any) { delete(claims, "nonce") },
		"no subject": func(claims map[string]any) { delete(claims, "sub") },
		"tenant":     func(claims map[string]any) { claims["tenant_id"] = "tenant-1" },
		"role":       func(claims map[string]any) { claims["role"] = "admin" },
		"scope":      func(claims map[string]any) { claims["scope"] = "mission.write" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			verifier, err := NewWebsiteIdentityAssertionVerifier(key, func() time.Time { return now })
			if err != nil {
				t.Fatalf("new verifier: %v", err)
			}
			claims := validWebsiteClaims(now, protocol.AdvisorTierCustomer, "nonce-"+name)
			mutate(claims)
			if _, err := verifier.Verify(signWebsiteClaims(t, key, claims)); err == nil {
				t.Fatal("invalid assertion was accepted")
			}
		})
	}

	verifier, _ := NewWebsiteIdentityAssertionVerifier(key, func() time.Time { return now })
	claims := validWebsiteClaims(now, protocol.AdvisorTierCustomer, "nonce-replay")
	assertion := signWebsiteClaims(t, key, claims)
	if _, err := verifier.Verify(assertion); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	if _, err := verifier.Verify(assertion); err == nil {
		t.Fatal("replayed nonce was accepted")
	}

	wrongKey := []byte("wrong-assertion-key-0123456789abcdef0")
	verifier, _ = NewWebsiteIdentityAssertionVerifier(key, func() time.Time { return now })
	if _, err := verifier.Verify(signWebsiteClaims(t, wrongKey, validWebsiteClaims(now, protocol.AdvisorTierCustomer, "nonce-wrong-key"))); err == nil {
		t.Fatal("wrong signature was accepted")
	}
}

func validWebsiteClaims(now time.Time, tier protocol.AdvisorTier, nonce string) map[string]any {
	return map[string]any{
		"iss": "minvia-website-auth", "aud": "orrery-website-channel", "sub": "customer-1",
		"tier": string(tier), "iat": now.Unix(), "exp": now.Add(2 * time.Minute).Unix(), "nonce": nonce,
	}
}

func signWebsiteClaims(t *testing.T, key []byte, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
