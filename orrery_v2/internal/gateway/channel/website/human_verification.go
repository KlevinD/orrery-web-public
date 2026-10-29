package website

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	websiteHumanVerificationAudience = "orrery-website-human-verification"
	websiteHumanAssertionMaxTTL      = 5 * time.Minute
	websiteHumanVerificationTTL      = 30 * time.Minute
)

var errWebsiteHumanVerificationInvalid = errors.New("website human verification assertion is invalid")

type WebsiteHumanVerificationAssertionVerifier struct {
	key    []byte
	now    func() time.Time
	mu     sync.Mutex
	nonces map[string]time.Time
}

func NewWebsiteHumanVerificationAssertionVerifier(key []byte, now func() time.Time) (*WebsiteHumanVerificationAssertionVerifier, error) {
	if len(key) < 32 {
		return nil, fmt.Errorf("website human verification assertion key must contain at least 32 bytes")
	}
	if now == nil {
		now = time.Now
	}
	return &WebsiteHumanVerificationAssertionVerifier{
		key: append([]byte(nil), key...), now: now, nonces: map[string]time.Time{},
	}, nil
}

func (v *WebsiteHumanVerificationAssertionVerifier) Verify(assertion string) (string, error) {
	if v == nil || len(v.key) < 32 {
		return "", errWebsiteHumanVerificationInvalid
	}
	parts := strings.Split(strings.TrimSpace(assertion), ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", errWebsiteHumanVerificationInvalid
	}
	mac := hmac.New(sha256.New, v.key)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	provided, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(provided) != sha256.Size || subtle.ConstantTimeCompare(mac.Sum(nil), provided) != 1 {
		return "", errWebsiteHumanVerificationInvalid
	}
	header, err := decodeJSONObject(parts[0])
	if err != nil || !exactJSONKeys(header, "alg", "typ") || header["alg"] != "HS256" || header["typ"] != "JWT" {
		return "", errWebsiteHumanVerificationInvalid
	}
	claims, err := decodeJSONObject(parts[1])
	if err != nil || !exactJSONKeys(claims, "aud", "exp", "iat", "iss", "nonce", "sid") {
		return "", errWebsiteHumanVerificationInvalid
	}
	issuer, issuerOK := claims["iss"].(string)
	audience, audienceOK := claims["aud"].(string)
	sessionID, sessionOK := claims["sid"].(string)
	nonce, nonceOK := claims["nonce"].(string)
	issuedAt, issuedOK := jsonInteger(claims["iat"])
	expiresAt, expiresOK := jsonInteger(claims["exp"])
	if !issuerOK || issuer != websiteAssertionIssuer || !audienceOK || audience != websiteHumanVerificationAudience ||
		!sessionOK || !validWebsiteOpaqueClaim(sessionID) || !nonceOK || !validWebsiteOpaqueClaim(nonce) || !issuedOK || !expiresOK {
		return "", errWebsiteHumanVerificationInvalid
	}
	now := v.now().UTC()
	issued := time.Unix(issuedAt, 0)
	expires := time.Unix(expiresAt, 0)
	if issued.After(now.Add(30*time.Second)) || !expires.After(now) || !expires.After(issued) || expires.Sub(issued) > websiteHumanAssertionMaxTTL {
		return "", errWebsiteHumanVerificationInvalid
	}
	if !v.consumeNonce(nonce, expires, now) {
		return "", errWebsiteHumanVerificationInvalid
	}
	return sessionID, nil
}

func (v *WebsiteHumanVerificationAssertionVerifier) consumeNonce(nonce string, expires, now time.Time) bool {
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
