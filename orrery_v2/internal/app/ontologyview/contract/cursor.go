package contract

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

const cursorVersion = 1
const defaultCursorTTL = 15 * time.Minute
const maxCursorTokenBytes = 4096
const maxSeekTupleBytes = 1024

var cursorKeyIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

type CursorBinding struct {
	PrincipalKey          string
	TenantID              string
	BindingVersion        int64
	QueryHash             string
	AuthorizationRevision string
	PolicyRevision        string
}

type CursorPosition struct {
	Binding CursorBinding
	Seek    []string
}

type CursorKey struct {
	ID  string
	Key []byte
}

type CursorCodecOptions struct {
	PrimaryID string
	Keys      []CursorKey
	TTL       time.Duration
	Now       func() time.Time
	Random    io.Reader
}

type CursorCodec struct {
	primaryID string
	keys      map[string]cipher.AEAD
	ttl       time.Duration
	now       func() time.Time
	random    io.Reader
}

type cursorPayload struct {
	Version               int      `json:"version"`
	PrincipalKey          string   `json:"principal_key"`
	TenantID              string   `json:"tenant_id"`
	BindingVersion        int64    `json:"binding_version"`
	QueryHash             string   `json:"query_hash"`
	AuthorizationRevision string   `json:"authorization_revision"`
	PolicyRevision        string   `json:"policy_revision"`
	Seek                  []string `json:"seek"`
	IssuedAt              int64    `json:"issued_at"`
	ExpiresAt             int64    `json:"expires_at"`
}

func NewCursorCodec(opts CursorCodecOptions) (*CursorCodec, error) {
	opts.PrimaryID = strings.TrimSpace(opts.PrimaryID)
	if !cursorKeyIDPattern.MatchString(opts.PrimaryID) || len(opts.Keys) == 0 {
		return nil, fmt.Errorf("%w: primary cursor key and key ring are required", ErrInvalidRequest)
	}
	ttl := opts.TTL
	if ttl == 0 {
		ttl = defaultCursorTTL
	}
	if ttl < time.Second || ttl > defaultCursorTTL {
		return nil, fmt.Errorf("%w: cursor TTL must be between 1 second and 15 minutes", ErrInvalidRequest)
	}
	keys := make(map[string]cipher.AEAD, len(opts.Keys))
	for _, key := range opts.Keys {
		key.ID = strings.TrimSpace(key.ID)
		if !cursorKeyIDPattern.MatchString(key.ID) || len(key.Key) != 32 {
			return nil, fmt.Errorf("%w: cursor keys require a safe id and 32 bytes", ErrInvalidRequest)
		}
		if _, duplicate := keys[key.ID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate cursor key id", ErrInvalidRequest)
		}
		block, err := aes.NewCipher(append([]byte(nil), key.Key...))
		if err != nil {
			return nil, fmt.Errorf("create cursor cipher: %w", err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("create cursor AEAD: %w", err)
		}
		keys[key.ID] = aead
	}
	if _, ok := keys[opts.PrimaryID]; !ok {
		return nil, fmt.Errorf("%w: primary cursor key id is absent from key ring", ErrInvalidRequest)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	random := opts.Random
	if random == nil {
		random = rand.Reader
	}
	return &CursorCodec{primaryID: opts.PrimaryID, keys: keys, ttl: ttl, now: now, random: random}, nil
}

func (c *CursorCodec) Encode(position CursorPosition) (string, error) {
	if c == nil {
		return "", fmt.Errorf("%w: cursor codec is required", ErrInvalidRequest)
	}
	binding, err := normalizeCursorBinding(position.Binding, true)
	if err != nil {
		return "", err
	}
	seek, err := normalizeSeek(position.Seek)
	if err != nil {
		return "", err
	}
	now := c.now().UTC()
	payload := cursorPayload{
		Version:               cursorVersion,
		PrincipalKey:          binding.PrincipalKey,
		TenantID:              binding.TenantID,
		BindingVersion:        binding.BindingVersion,
		QueryHash:             binding.QueryHash,
		AuthorizationRevision: binding.AuthorizationRevision,
		PolicyRevision:        binding.PolicyRevision,
		Seek:                  seek,
		IssuedAt:              now.Unix(),
		ExpiresAt:             now.Add(c.ttl).Unix(),
	}
	plaintext, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode cursor payload: %w", err)
	}
	aead := c.keys[c.primaryID]
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(c.random, nonce); err != nil {
		return "", fmt.Errorf("generate cursor nonce: %w", err)
	}
	aad := cursorAAD(c.primaryID)
	ciphertext := aead.Seal(nil, nonce, plaintext, aad)
	sealed := append(nonce, ciphertext...)
	token := "oc1." + c.primaryID + "." + base64.RawURLEncoding.EncodeToString(sealed)
	if len(token) > maxCursorTokenBytes {
		return "", fmt.Errorf("%w: encoded cursor exceeds its size limit", ErrInvalidRequest)
	}
	return token, nil
}

func (c *CursorCodec) Decode(token string, expected CursorBinding) (CursorPosition, error) {
	if c == nil {
		return CursorPosition{}, fmt.Errorf("%w: cursor codec is required", ErrInvalidCursor)
	}
	token = strings.TrimSpace(token)
	if token == "" || len(token) > maxCursorTokenBytes {
		return CursorPosition{}, ErrInvalidCursor
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "oc1" || !cursorKeyIDPattern.MatchString(parts[1]) {
		return CursorPosition{}, ErrInvalidCursor
	}
	aead, ok := c.keys[parts[1]]
	if !ok {
		return CursorPosition{}, ErrCursorStale
	}
	sealed, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sealed) <= aead.NonceSize() {
		return CursorPosition{}, ErrInvalidCursor
	}
	nonce, ciphertext := sealed[:aead.NonceSize()], sealed[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, ciphertext, cursorAAD(parts[1]))
	if err != nil {
		return CursorPosition{}, ErrInvalidCursor
	}
	var payload cursorPayload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return CursorPosition{}, ErrInvalidCursor
	}
	if payload.Version != cursorVersion || payload.IssuedAt <= 0 || payload.ExpiresAt <= payload.IssuedAt || c.now().UTC().Unix() >= payload.ExpiresAt {
		return CursorPosition{}, ErrCursorStale
	}
	binding, err := normalizeCursorBinding(CursorBinding{
		PrincipalKey:          payload.PrincipalKey,
		TenantID:              payload.TenantID,
		BindingVersion:        payload.BindingVersion,
		QueryHash:             payload.QueryHash,
		AuthorizationRevision: payload.AuthorizationRevision,
		PolicyRevision:        payload.PolicyRevision,
	}, true)
	if err != nil {
		return CursorPosition{}, ErrInvalidCursor
	}
	seek, err := normalizeSeek(payload.Seek)
	if err != nil {
		return CursorPosition{}, ErrInvalidCursor
	}
	expected, err = normalizeCursorBinding(expected, true)
	if err != nil {
		return CursorPosition{}, err
	}
	if binding != expected {
		return CursorPosition{}, ErrCursorStale
	}
	return CursorPosition{Binding: binding, Seek: seek}, nil
}

func CanonicalQueryHash(query any) (string, error) {
	raw, err := json.Marshal(query)
	if err != nil {
		return "", fmt.Errorf("%w: query cannot be canonicalized", ErrInvalidRequest)
	}
	if len(raw) > 16*1024 {
		return "", fmt.Errorf("%w: canonical query exceeds 16 KiB", ErrInvalidRequest)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func normalizeCursorBinding(binding CursorBinding, requireRevisions bool) (CursorBinding, error) {
	binding.PrincipalKey = strings.TrimSpace(binding.PrincipalKey)
	binding.TenantID = strings.TrimSpace(binding.TenantID)
	binding.QueryHash = strings.TrimSpace(binding.QueryHash)
	binding.AuthorizationRevision = strings.TrimSpace(binding.AuthorizationRevision)
	binding.PolicyRevision = strings.TrimSpace(binding.PolicyRevision)
	if binding.PrincipalKey == "" || len(binding.PrincipalKey) > 256 || binding.TenantID == "" || len(binding.TenantID) > 256 || binding.BindingVersion <= 0 || !isCanonicalQueryHash(binding.QueryHash) {
		return CursorBinding{}, fmt.Errorf("%w: cursor binding is incomplete", ErrInvalidRequest)
	}
	if requireRevisions && (binding.AuthorizationRevision == "" || binding.PolicyRevision == "") {
		return CursorBinding{}, fmt.Errorf("%w: encrypted cursor requires trusted authorization and policy revisions", ErrInvalidRequest)
	}
	if len(binding.AuthorizationRevision) > 256 || len(binding.PolicyRevision) > 256 {
		return CursorBinding{}, fmt.Errorf("%w: cursor revision exceeds 256 bytes", ErrInvalidRequest)
	}
	return binding, nil
}

func normalizeSeek(seek []string) ([]string, error) {
	if len(seek) == 0 || len(seek) > 8 {
		return nil, fmt.Errorf("%w: cursor seek tuple requires 1-8 values", ErrInvalidRequest)
	}
	out := make([]string, len(seek))
	totalBytes := 0
	for index, value := range seek {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 512 {
			return nil, fmt.Errorf("%w: cursor seek value is empty or too long", ErrInvalidRequest)
		}
		totalBytes += len(value)
		if totalBytes > maxSeekTupleBytes {
			return nil, fmt.Errorf("%w: cursor seek tuple is too large", ErrInvalidRequest)
		}
		out[index] = value
	}
	return out, nil
}

func isCanonicalQueryHash(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func cursorAAD(keyID string) []byte {
	return []byte("orrery-ontology-cursor-v1\x00" + keyID)
}
