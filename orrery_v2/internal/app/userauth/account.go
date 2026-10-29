package userauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"

	agentcore "orrery_v2/internal/agent/core"
)

const (
	SuperadminUsername       = "superadmin"
	SuperadminAgentProfileID = agentcore.SystemSuperadminPrincipalID

	LegacySuperadminAgentProfileID = agentcore.LegacySuperadminAgentProfileID
	LegacyLocalAdminProfileID      = agentcore.LegacyLocalAdminAgentProfileID
	LegacyLocalUserProfileID       = agentcore.LegacyLocalUserAgentProfileID

	passwordVerifierArgon2IDPrefix = "$argon2id$"
	passwordArgon2IDMemoryKB       = 64 * 1024
	passwordArgon2IDMaxMemoryKB    = 256 * 1024
	passwordArgon2IDIterations     = 3
	passwordArgon2IDMaxIterations  = 10
	passwordArgon2IDParallelism    = 2
	passwordArgon2IDMaxParallelism = 4
	passwordArgon2IDSaltBytes      = 16
	passwordArgon2IDKeyBytes       = 32
	MinimumPasswordLength          = 12
	MaximumPasswordLength          = 1024
)

func IsSystemPrincipalID(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), SuperadminAgentProfileID)
}

func IsReservedWorkforceProfileID(value string) bool {
	return agentcore.IsReservedNonWorkforceProfileID(value)
}

func IsReservedWorkforceUsername(value string) bool {
	switch normalizeUsername(value) {
	case SuperadminUsername, "local-admin", "local-user":
		return true
	default:
		return false
	}
}

var secureRandomReader io.Reader = rand.Reader

type Account struct {
	UserID           string
	Username         string
	DisplayName      string
	Nickname         string
	Email            string
	AgentProfileID   string
	Role             string
	Enabled          bool
	PasswordSalt     string
	PasswordVerifier string
	PermissionScope  []string
	UpdatedAt        time.Time
}

func (a *Account) SetPassword(password string) error {
	if a == nil {
		return nil
	}
	if err := ValidatePassword(password); err != nil {
		return err
	}
	salt, verifier, err := PasswordHash(password)
	if err != nil {
		return err
	}
	a.PasswordSalt = salt
	a.PasswordVerifier = verifier
	a.UpdatedAt = time.Now().UTC()
	return nil
}

func ValidatePassword(password string) error {
	length := len([]rune(password))
	if length < MinimumPasswordLength {
		return fmt.Errorf("%w: password must be at least %d characters", ErrInvalidRequest, MinimumPasswordLength)
	}
	if length > MaximumPasswordLength {
		return fmt.Errorf("%w: password must not exceed %d characters", ErrInvalidRequest, MaximumPasswordLength)
	}
	return nil
}

func (a Account) normalized() Account {
	a.Username = normalizeUsername(a.Username)
	a.DisplayName = strings.TrimSpace(firstNonEmpty(a.DisplayName, a.Nickname, a.Username))
	a.Nickname = strings.TrimSpace(firstNonEmpty(a.Nickname, a.DisplayName, a.Username))
	a.Email = strings.TrimSpace(a.Email)
	a.UserID = strings.TrimSpace(firstNonEmpty(a.UserID, a.Username))
	a.AgentProfileID = strings.TrimSpace(firstNonEmpty(a.AgentProfileID, "human-"+a.Username))
	a.Role = strings.TrimSpace(a.Role)
	a.PasswordSalt = strings.TrimSpace(a.PasswordSalt)
	a.PasswordVerifier = strings.TrimSpace(a.PasswordVerifier)
	a.PermissionScope = cleanScopes(a.PermissionScope)
	return a
}

func ClientPasswordDigest(password, salt string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(salt) + "\n" + password))
	return hex.EncodeToString(sum[:])
}

func ClientPasswordProof(password, salt, challengeID string) string {
	return ChallengePasswordProof(ClientPasswordDigest(password, salt), challengeID)
}

func ChallengePasswordProof(clientDigest, challengeID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(clientDigest) + "\n" + strings.TrimSpace(challengeID)))
	return hex.EncodeToString(sum[:])
}

func PasswordHash(password string) (string, string, error) {
	saltBytes, err := randomBytes(passwordArgon2IDSaltBytes)
	if err != nil {
		return "", "", err
	}
	salt := base64.RawStdEncoding.EncodeToString(saltBytes)
	hash := argon2.IDKey([]byte(password), saltBytes, passwordArgon2IDIterations, passwordArgon2IDMemoryKB, passwordArgon2IDParallelism, passwordArgon2IDKeyBytes)
	verifier := fmt.Sprintf("%sv=%d$m=%d,t=%d,p=%d$%s$%s",
		passwordVerifierArgon2IDPrefix,
		argon2.Version,
		passwordArgon2IDMemoryKB,
		passwordArgon2IDIterations,
		passwordArgon2IDParallelism,
		salt,
		base64.RawStdEncoding.EncodeToString(hash),
	)
	return salt, verifier, nil
}

func PasswordVerifierMatches(password, salt, verifier string) bool {
	verifier = strings.TrimSpace(verifier)
	if verifier == "" {
		return false
	}
	if strings.HasPrefix(verifier, passwordVerifierArgon2IDPrefix) {
		return argon2IDVerifierMatches(password, verifier)
	}
	return legacyPasswordVerifierMatches(password, salt, verifier)
}

func PasswordVerifierNeedsUpgrade(verifier string) bool {
	return strings.TrimSpace(verifier) != "" && !strings.HasPrefix(strings.TrimSpace(verifier), passwordVerifierArgon2IDPrefix)
}

func legacyPasswordVerifierMatches(password, salt, verifier string) bool {
	got := ClientPasswordDigest(password, salt)
	if len(got) != len(verifier) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(verifier)) == 1
}

func argon2IDVerifierMatches(password, verifier string) bool {
	parts := strings.Split(verifier, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	version, ok := parseArgon2Version(parts[2])
	if !ok || version != argon2.Version {
		return false
	}
	memory, iterations, parallelism, ok := parseArgon2Params(parts[3])
	if !ok {
		return false
	}
	if memory > passwordArgon2IDMaxMemoryKB || iterations > passwordArgon2IDMaxIterations || parallelism > passwordArgon2IDMaxParallelism {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != passwordArgon2IDSaltBytes {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) != passwordArgon2IDKeyBytes {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(expected)))
	return subtle.ConstantTimeCompare(got, expected) == 1
}

func parseArgon2Version(value string) (int, bool) {
	if !strings.HasPrefix(value, "v=") {
		return 0, false
	}
	version, err := strconv.Atoi(strings.TrimPrefix(value, "v="))
	return version, err == nil
}

func parseArgon2Params(value string) (uint32, uint32, uint8, bool) {
	parts := strings.Split(value, ",")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	values := map[string]int{}
	for _, part := range parts {
		key, raw, ok := strings.Cut(part, "=")
		if !ok {
			return 0, 0, 0, false
		}
		parsed, err := strconv.ParseUint(raw, 10, 32)
		if err != nil || parsed == 0 {
			return 0, 0, 0, false
		}
		values[key] = int(parsed)
	}
	memory, mok := values["m"]
	iterations, tok := values["t"]
	parallelism, pok := values["p"]
	if !mok || !tok || !pok || parallelism > 255 {
		return 0, 0, 0, false
	}
	return uint32(memory), uint32(iterations), uint8(parallelism), true
}

func randomToken(size int) (string, error) {
	buf, err := randomBytes(size)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func randomBytes(size int) ([]byte, error) {
	if size <= 0 {
		size = 16
	}
	if secureRandomReader == nil {
		return nil, fmt.Errorf("%w: secure random source is not configured", ErrServiceDependency)
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(secureRandomReader, buf); err != nil {
		return nil, fmt.Errorf("%w: secure random source failed: %v", ErrServiceDependency, err)
	}
	return buf, nil
}

func normalizeUsername(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeIPAddress(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	if ip := net.ParseIP(value); ip != nil {
		return ip.String()
	}
	return strings.ToLower(value)
}

func cleanScopes(scopes []string) []string {
	out := make([]string, 0, len(scopes))
	seen := map[string]struct{}{}
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		out = append(out, scope)
	}
	return out
}

func mergeScopes(existing []string, seeded []string) []string {
	return cleanScopes(append(append([]string(nil), existing...), seeded...))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
