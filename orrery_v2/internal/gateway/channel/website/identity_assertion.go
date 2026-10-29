package website

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/gateway/protocol"
)

const (
	websiteAssertionIssuer   = "minvia-website-auth"
	websiteAssertionAudience = "orrery-website-channel"
	websiteAssertionMaxTTL   = 5 * time.Minute
)

var errWebsiteIdentityAssertionInvalid = errors.New("website identity assertion is invalid")

type WebsiteIdentity struct {
	Subject string
	Tier    protocol.AdvisorTier
}

type WebsiteIdentityAssertionVerifier struct {
	key    []byte
	now    func() time.Time
	mu     sync.Mutex
	nonces map[string]time.Time
}

func NewWebsiteIdentityAssertionVerifier(key []byte, now func() time.Time) (*WebsiteIdentityAssertionVerifier, error) {
	if len(key) < 32 {
		return nil, fmt.Errorf("website assertion key must contain at least 32 bytes")
	}
	if now == nil {
		now = time.Now
	}
	return &WebsiteIdentityAssertionVerifier{
		key: append([]byte(nil), key...), now: now, nonces: map[string]time.Time{},
	}, nil
}

func (v *WebsiteIdentityAssertionVerifier) Verify(assertion string) (WebsiteIdentity, error) {
	if v == nil || len(v.key) < 32 {
		return WebsiteIdentity{}, errWebsiteIdentityAssertionInvalid
	}
	parts := strings.Split(strings.TrimSpace(assertion), ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return WebsiteIdentity{}, errWebsiteIdentityAssertionInvalid
	}
	mac := hmac.New(sha256.New, v.key)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	provided, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(provided) != sha256.Size || subtle.ConstantTimeCompare(mac.Sum(nil), provided) != 1 {
		return WebsiteIdentity{}, errWebsiteIdentityAssertionInvalid
	}
	header, err := decodeJSONObject(parts[0])
	if err != nil || !exactJSONKeys(header, "alg", "typ") || header["alg"] != "HS256" || header["typ"] != "JWT" {
		return WebsiteIdentity{}, errWebsiteIdentityAssertionInvalid
	}
	claims, err := decodeJSONObject(parts[1])
	if err != nil || !exactJSONKeys(claims, "aud", "exp", "iat", "iss", "nonce", "sub", "tier") {
		return WebsiteIdentity{}, errWebsiteIdentityAssertionInvalid
	}
	issuer, issuerOK := claims["iss"].(string)
	audience, audienceOK := claims["aud"].(string)
	subject, subjectOK := claims["sub"].(string)
	nonce, nonceOK := claims["nonce"].(string)
	tierValue, tierOK := claims["tier"].(string)
	issuedAt, issuedOK := jsonInteger(claims["iat"])
	expiresAt, expiresOK := jsonInteger(claims["exp"])
	tier := protocol.AdvisorTier(tierValue)
	if !issuerOK || issuer != websiteAssertionIssuer || !audienceOK || audience != websiteAssertionAudience ||
		!subjectOK || !validWebsiteOpaqueClaim(subject) || !nonceOK || !validWebsiteOpaqueClaim(nonce) ||
		!tierOK || (tier != protocol.AdvisorTierPublic && tier != protocol.AdvisorTierCustomer) || !issuedOK || !expiresOK {
		return WebsiteIdentity{}, errWebsiteIdentityAssertionInvalid
	}
	now := v.now().UTC()
	issued := time.Unix(issuedAt, 0)
	expires := time.Unix(expiresAt, 0)
	if issued.After(now.Add(30*time.Second)) || !expires.After(now) || !expires.After(issued) || expires.Sub(issued) > websiteAssertionMaxTTL {
		return WebsiteIdentity{}, errWebsiteIdentityAssertionInvalid
	}
	if !v.consumeNonce(nonce, expires, now) {
		return WebsiteIdentity{}, errWebsiteIdentityAssertionInvalid
	}
	return WebsiteIdentity{Subject: subject, Tier: tier}, nil
}

func (v *WebsiteIdentityAssertionVerifier) consumeNonce(nonce string, expires, now time.Time) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	for value, deadline := range v.nonces {
		if !deadline.After(now) {
			delete(v.nonces, value)
		}
	}
	if _, exists := v.nonces[nonce]; exists {
		return false
	}
	v.nonces[nonce] = expires
	return true
}

func decodeJSONObject(value string) (map[string]any, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded == nil {
		return nil, errWebsiteIdentityAssertionInvalid
	}
	return decoded, nil
}

func exactJSONKeys(value map[string]any, expected ...string) bool {
	if len(value) != len(expected) {
		return false
	}
	for _, key := range expected {
		if _, ok := value[key]; !ok {
			return false
		}
	}
	return true
}

func jsonInteger(value any) (int64, bool) {
	number, ok := value.(float64)
	if !ok || number != float64(int64(number)) {
		return 0, false
	}
	return int64(number), true
}

func validWebsiteOpaqueClaim(value string) bool {
	if len(value) == 0 || len(value) > 255 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("._~-", character) {
			continue
		}
		return false
	}
	return true
}
