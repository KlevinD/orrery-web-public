package userauth

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultPasswordVerificationConcurrency      = 4
	defaultPasswordVerificationConcurrencyPerIP = 1
	defaultSessionTTL                           = 12 * time.Hour
	defaultSessionIdleTTL                       = 30 * time.Minute
	defaultSessionRotationAge                   = 4 * time.Hour
	passwordChangeSessionTTL                    = 10 * time.Minute
	sessionTouchInterval                        = time.Minute
)

type passwordVerificationLimiter struct {
	mu       sync.Mutex
	global   chan struct{}
	perIPCap int
	perIP    map[string]*passwordVerificationSlot
}

type passwordVerificationSlot struct {
	sem  chan struct{}
	refs int
}

func newPasswordVerificationLimiter(globalLimit, perIPLimit int) *passwordVerificationLimiter {
	if globalLimit <= 0 {
		globalLimit = defaultPasswordVerificationConcurrency
	}
	if perIPLimit <= 0 {
		perIPLimit = defaultPasswordVerificationConcurrencyPerIP
	}
	return &passwordVerificationLimiter{
		global:   make(chan struct{}, globalLimit),
		perIPCap: perIPLimit,
		perIP:    map[string]*passwordVerificationSlot{},
	}
}

func (l *passwordVerificationLimiter) acquire(ctx context.Context, ipAddress string) (func(), error) {
	if l == nil {
		return func() {}, nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case l.global <- struct{}{}:
	default:
		return nil, ErrLoginThrottled
	}
	ipAddress = normalizeIPAddress(ipAddress)
	if ipAddress == "" {
		ipAddress = "unknown"
	}
	l.mu.Lock()
	slot := l.perIP[ipAddress]
	if slot == nil {
		slot = &passwordVerificationSlot{sem: make(chan struct{}, l.perIPCap)}
		l.perIP[ipAddress] = slot
	}
	slot.refs++
	l.mu.Unlock()
	select {
	case slot.sem <- struct{}{}:
		return func() {
			<-slot.sem
			<-l.global
			l.mu.Lock()
			slot.refs--
			if slot.refs == 0 {
				delete(l.perIP, ipAddress)
			}
			l.mu.Unlock()
		}, nil
	default:
		<-l.global
		l.mu.Lock()
		slot.refs--
		if slot.refs == 0 {
			delete(l.perIP, ipAddress)
		}
		l.mu.Unlock()
		return nil, ErrLoginThrottled
	}
}

type MFAValidator interface {
	Required(username string) bool
	Validate(ctx context.Context, username string, code string, at time.Time) error
}

type TOTPReplayStore interface {
	ConsumeTOTPCounter(ctx context.Context, username string, counter int64) (bool, error)
}

type TOTPValidator struct {
	secrets      map[string][]byte
	mu           sync.Mutex
	lastAccepted map[string]int64
	replayStore  TOTPReplayStore
}

func NewTOTPValidator(base32Secrets map[string]string) (*TOTPValidator, error) {
	validator := &TOTPValidator{
		secrets:      map[string][]byte{},
		lastAccepted: map[string]int64{},
	}
	decoder := base32.StdEncoding.WithPadding(base32.NoPadding)
	for username, encoded := range base32Secrets {
		username = normalizeUsername(username)
		encoded = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(encoded), " ", ""))
		if username == "" || encoded == "" {
			continue
		}
		secret, err := decoder.DecodeString(encoded)
		if err != nil || len(secret) < 20 {
			return nil, fmt.Errorf("%w: TOTP secret for %s must be valid base32 with at least 20 decoded bytes", ErrInvalidRequest, username)
		}
		validator.secrets[username] = append([]byte(nil), secret...)
	}
	return validator, nil
}

func (v *TOTPValidator) WithReplayStore(store TOTPReplayStore) *TOTPValidator {
	if v != nil {
		v.mu.Lock()
		v.replayStore = store
		v.mu.Unlock()
	}
	return v
}

func (v *TOTPValidator) Required(username string) bool {
	if v == nil {
		return false
	}
	_, ok := v.secrets[normalizeUsername(username)]
	return ok
}

func (v *TOTPValidator) Validate(ctx context.Context, username string, code string, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if v == nil {
		return ErrMFARequired
	}
	username = normalizeUsername(username)
	secret := v.secrets[username]
	if len(secret) == 0 {
		return ErrMFARequired
	}
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return ErrMFAInvalid
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	counter := at.Unix() / 30
	matchedCounter := int64(-1)
	for offset := int64(-1); offset <= 1; offset++ {
		candidate := counter + offset
		if candidate >= 0 && subtleCodeEqual(code, totpCode(secret, uint64(candidate))) {
			matchedCounter = candidate
			break
		}
	}
	if matchedCounter < 0 {
		return ErrMFAInvalid
	}
	v.mu.Lock()
	if previous, ok := v.lastAccepted[username]; ok && matchedCounter <= previous {
		v.mu.Unlock()
		return ErrMFAInvalid
	}
	replayStore := v.replayStore
	if replayStore == nil {
		v.lastAccepted[username] = matchedCounter
		v.mu.Unlock()
		return nil
	}
	v.mu.Unlock()
	accepted, err := replayStore.ConsumeTOTPCounter(ctx, username, matchedCounter)
	if err != nil {
		return fmt.Errorf("%w: persist TOTP replay counter: %v", ErrServiceDependency, err)
	}
	if !accepted {
		return ErrMFAInvalid
	}
	v.mu.Lock()
	v.lastAccepted[username] = matchedCounter
	v.mu.Unlock()
	return nil
}

func totpCode(secret []byte, counter uint64) string {
	var message [8]byte
	binary.BigEndian.PutUint64(message[:], counter)
	mac := hmac.New(sha1.New, secret)
	_, _ = mac.Write(message[:])
	digest := mac.Sum(nil)
	offset := digest[len(digest)-1] & 0x0f
	value := (uint32(digest[offset])&0x7f)<<24 |
		uint32(digest[offset+1])<<16 |
		uint32(digest[offset+2])<<8 |
		uint32(digest[offset+3])
	return fmt.Sprintf("%06d", value%1_000_000)
}

func subtleCodeEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	var different byte
	for i := range left {
		different |= left[i] ^ right[i]
	}
	return different == 0
}

func sessionDeviceLabel(userAgent string) string {
	userAgent = strings.TrimSpace(userAgent)
	if userAgent == "" {
		return "unknown device"
	}
	if len(userAgent) > 160 {
		userAgent = userAgent[:160]
	}
	return strconv.QuoteToASCII(userAgent)
}
