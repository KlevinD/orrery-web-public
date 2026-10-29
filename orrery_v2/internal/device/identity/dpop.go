package identity

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
	"time"
)

type DPoPVerification struct {
	Proof       string
	AccessToken string
	Nonce       string
	Method      string
	TargetURI   string
	PublicKey   PublicKeyJWK
	Now         time.Time
}

type dpopHeader struct {
	Type string       `json:"typ"`
	Alg  string       `json:"alg"`
	JWK  PublicKeyJWK `json:"jwk"`
}

type dpopClaims struct {
	JTI   string `json:"jti"`
	HTM   string `json:"htm"`
	HTU   string `json:"htu"`
	IAT   int64  `json:"iat"`
	ATH   string `json:"ath"`
	Nonce string `json:"nonce"`
}

func (key PublicKeyJWK) Ed25519() (ed25519.PublicKey, error) {
	if key.KTY != "OKP" || key.CRV != "Ed25519" || strings.TrimSpace(key.X) == "" {
		return nil, ErrInvalidRequest
	}
	raw, err := base64.RawURLEncoding.DecodeString(key.X)
	if err != nil || len(raw) != ed25519.PublicKeySize || base64.RawURLEncoding.EncodeToString(raw) != key.X {
		return nil, ErrInvalidRequest
	}
	return ed25519.PublicKey(raw), nil
}

func VerifyDPoP(input DPoPVerification) error {
	segments := strings.Split(strings.TrimSpace(input.Proof), ".")
	if len(segments) != 3 || len(input.Proof) > 8192 {
		return ErrUnauthorized
	}
	headerRaw, err := decodeBase64URL(segments[0], 2048)
	if err != nil {
		return ErrUnauthorized
	}
	claimsRaw, err := decodeBase64URL(segments[1], 4096)
	if err != nil {
		return ErrUnauthorized
	}
	signature, err := decodeBase64URL(segments[2], 256)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return ErrUnauthorized
	}
	var header dpopHeader
	if err := decodeStrictJSON(headerRaw, &header); err != nil || header.Type != "dpop+jwt" || header.Alg != "EdDSA" {
		return ErrUnauthorized
	}
	if !constantTimeStringEqual(header.JWK.KTY, input.PublicKey.KTY) || !constantTimeStringEqual(header.JWK.CRV, input.PublicKey.CRV) || !constantTimeStringEqual(header.JWK.X, input.PublicKey.X) {
		return ErrUnauthorized
	}
	publicKey, err := input.PublicKey.Ed25519()
	if err != nil || !ed25519.Verify(publicKey, []byte(segments[0]+"."+segments[1]), signature) {
		return ErrUnauthorized
	}
	var claims dpopClaims
	if err := decodeStrictJSON(claimsRaw, &claims); err != nil {
		return ErrUnauthorized
	}
	expectedURI, err := normalizeDPoPTarget(input.TargetURI)
	if err != nil {
		return ErrUnauthorized
	}
	proofURI, err := normalizeDPoPTarget(claims.HTU)
	if err != nil || !constantTimeStringEqual(proofURI, expectedURI) || claims.HTM != strings.ToUpper(strings.TrimSpace(input.Method)) {
		return ErrUnauthorized
	}
	now := input.Now.UTC().Unix()
	if claims.IAT < now-60 || claims.IAT > now+60 || !validJTI(claims.JTI) || !constantTimeStringEqual(claims.Nonce, strings.TrimSpace(input.Nonce)) {
		return ErrUnauthorized
	}
	digest := sha256.Sum256([]byte(strings.TrimSpace(input.AccessToken)))
	expectedATH := base64.RawURLEncoding.EncodeToString(digest[:])
	if !constantTimeStringEqual(claims.ATH, expectedATH) {
		return ErrUnauthorized
	}
	return nil
}

func dpopNonce(proof string) string {
	segments := strings.Split(strings.TrimSpace(proof), ".")
	if len(segments) != 3 || len(proof) > 8192 {
		return ""
	}
	claimsRaw, err := decodeBase64URL(segments[1], 4096)
	if err != nil {
		return ""
	}
	var claims dpopClaims
	if err := decodeStrictJSON(claimsRaw, &claims); err != nil {
		return ""
	}
	return strings.TrimSpace(claims.Nonce)
}

func normalizeDPoPTarget(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.User != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", ErrInvalidRequest
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return "", ErrInvalidRequest
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func isLoopbackHost(host string) bool {
	host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func validJTI(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) >= 8 && len(value) <= 160 && !strings.ContainsAny(value, "\x00\r\n")
}

func decodeBase64URL(value string, maximum int) ([]byte, error) {
	if value == "" || len(value) > maximum*2 {
		return nil, errors.New("invalid base64url value")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) > maximum || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return nil, errors.New("invalid base64url value")
	}
	return decoded, nil
}

func decodeStrictJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON value")
	}
	return nil
}
