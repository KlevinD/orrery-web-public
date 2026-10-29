package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

const (
	defaultCodeTTL      = 5 * time.Minute
	defaultAccessTTL    = 5 * time.Minute
	defaultChallengeTTL = 45 * time.Second
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]*$`)

type Options struct {
	Store        Store
	Now          func() time.Time
	Random       io.Reader
	CodeTTL      time.Duration
	AccessTTL    time.Duration
	ChallengeTTL time.Duration
}

type Service struct {
	store        Store
	now          func() time.Time
	random       io.Reader
	codeTTL      time.Duration
	accessTTL    time.Duration
	challengeTTL time.Duration
}

func NewService(options Options) (*Service, error) {
	if options.Store == nil {
		return nil, fmt.Errorf("%w: store is required", ErrDependency)
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	if options.CodeTTL == 0 {
		options.CodeTTL = defaultCodeTTL
	}
	if options.AccessTTL == 0 {
		options.AccessTTL = defaultAccessTTL
	}
	if options.ChallengeTTL == 0 {
		options.ChallengeTTL = defaultChallengeTTL
	}
	if options.CodeTTL <= 0 || options.CodeTTL > 10*time.Minute || options.AccessTTL <= 0 || options.AccessTTL > 15*time.Minute || options.ChallengeTTL <= 0 || options.ChallengeTTL > 2*time.Minute {
		return nil, fmt.Errorf("%w: credential TTL is outside the allowed range", ErrInvalidRequest)
	}
	return &Service{store: options.Store, now: options.Now, random: options.Random, codeTTL: options.CodeTTL, accessTTL: options.AccessTTL, challengeTTL: options.ChallengeTTL}, nil
}

func ValidateAuthorizationRequest(request AuthorizationRequest) error {
	request.ResponseType = strings.TrimSpace(request.ResponseType)
	request.ClientID = strings.TrimSpace(request.ClientID)
	request.RedirectURI = strings.TrimSpace(request.RedirectURI)
	request.CodeChallenge = strings.TrimSpace(request.CodeChallenge)
	request.CodeChallengeMethod = strings.TrimSpace(request.CodeChallengeMethod)
	request.State = strings.TrimSpace(request.State)
	if request.ResponseType != "code" || request.ClientID != DesktopClientID || request.RedirectURI != DesktopRedirectURI || request.CodeChallengeMethod != "S256" {
		return ErrInvalidRequest
	}
	challenge, err := base64.RawURLEncoding.DecodeString(request.CodeChallenge)
	if err != nil || len(challenge) != sha256.Size || base64.RawURLEncoding.EncodeToString(challenge) != request.CodeChallenge {
		return ErrInvalidRequest
	}
	if len(request.State) < 16 || len(request.State) > 512 {
		return ErrInvalidRequest
	}
	return nil
}

func (s *Service) Authorize(ctx context.Context, principal BrowserPrincipal, request AuthorizationRequest) (AuthorizationGrant, error) {
	principal.UserID = strings.TrimSpace(principal.UserID)
	principal.DisplayName = strings.TrimSpace(principal.DisplayName)
	principal.TenantID = strings.TrimSpace(principal.TenantID)
	if err := ValidateAuthorizationRequest(request); err != nil || !validIdentifier(principal.UserID) || !validTenantID(principal.TenantID) {
		return AuthorizationGrant{}, ErrInvalidRequest
	}
	code, err := s.randomSecret(32)
	if err != nil {
		return AuthorizationGrant{}, err
	}
	now := s.now().UTC()
	record := codeRecord{
		CodeHash: hashSecret(code), ClientID: DesktopClientID, RedirectURI: DesktopRedirectURI,
		CodeChallenge: strings.TrimSpace(request.CodeChallenge), TenantID: principal.TenantID,
		OwnerID: principal.UserID, OwnerDisplayName: principal.DisplayName, CreatedAt: now, ExpiresAt: now.Add(s.codeTTL),
	}
	if err := s.store.SaveAuthorizationCode(ctx, record); err != nil {
		return AuthorizationGrant{}, err
	}
	return AuthorizationGrant{Code: code, RedirectURI: DesktopRedirectURI, State: strings.TrimSpace(request.State), ExpiresAt: record.ExpiresAt}, nil
}

func (s *Service) ExchangeToken(ctx context.Context, request TokenRequest) (Token, error) {
	switch strings.TrimSpace(request.GrantType) {
	case "authorization_code":
		return s.exchangeAuthorizationCode(ctx, request)
	case "refresh_token":
		return s.rotateRefreshCredential(ctx, request)
	default:
		return Token{}, ErrInvalidRequest
	}
}

func (s *Service) exchangeAuthorizationCode(ctx context.Context, request TokenRequest) (Token, error) {
	if strings.TrimSpace(request.ClientID) != DesktopClientID || strings.TrimSpace(request.RedirectURI) != DesktopRedirectURI || strings.TrimSpace(request.Code) == "" {
		return Token{}, ErrInvalidRequest
	}
	challenge, err := pkceChallenge(request.CodeVerifier)
	if err != nil {
		return Token{}, ErrInvalidRequest
	}
	accessToken, refreshCredential, pairingID, err := s.newCredentialSet()
	if err != nil {
		return Token{}, err
	}
	now := s.now().UTC()
	exchange := exchangeRecord{
		CodeHash: hashSecret(request.Code), ClientID: DesktopClientID, RedirectURI: DesktopRedirectURI, PresentedChallenge: challenge,
		Pairing: pairingRecord{PairingID: pairingID, RefreshHash: hashSecret(refreshCredential), RefreshRevision: 1, CreatedAt: now},
		Access:  AccessGrant{TokenHash: hashSecret(accessToken), PairingID: pairingID, ExpiresAt: now.Add(s.accessTTL)}, Now: now,
	}
	if err := s.store.ExchangeAuthorizationCode(ctx, exchange); err != nil {
		return Token{}, err
	}
	return Token{AccessToken: accessToken, RefreshCredential: refreshCredential, ExpiresIn: int64(s.accessTTL / time.Second)}, nil
}

func (s *Service) rotateRefreshCredential(ctx context.Context, request TokenRequest) (Token, error) {
	deviceID := strings.TrimSpace(request.DeviceID)
	refreshCredential := strings.TrimSpace(request.RefreshCredential)
	if strings.TrimSpace(request.ClientID) != DesktopClientID || !validIdentifier(deviceID) || refreshCredential == "" {
		return Token{}, ErrInvalidRequest
	}
	accessToken, err := s.randomSecret(32)
	if err != nil {
		return Token{}, err
	}
	replacement, err := s.randomSecret(48)
	if err != nil {
		return Token{}, err
	}
	now := s.now().UTC()
	if _, err := s.store.RotateRefreshCredential(ctx, deviceID, hashSecret(refreshCredential), hashSecret(replacement), hashSecret(accessToken), now.Add(s.accessTTL), now); err != nil {
		return Token{}, err
	}
	return Token{AccessToken: accessToken, RefreshCredential: replacement, ExpiresIn: int64(s.accessTTL / time.Second)}, nil
}

func (s *Service) RegisterDevice(ctx context.Context, accessToken string, registration DeviceRegistration) (Device, error) {
	if err := validateRegistration(registration); err != nil || strings.TrimSpace(accessToken) == "" {
		return Device{}, ErrInvalidRequest
	}
	deviceID, err := s.randomID("device")
	if err != nil {
		return Device{}, err
	}
	now := s.now().UTC()
	device := Device{
		DeviceID: deviceID, DeviceName: strings.TrimSpace(registration.DeviceName), Platform: strings.TrimSpace(registration.Platform),
		Architecture: strings.TrimSpace(registration.Architecture), ClientVersion: strings.TrimSpace(registration.ClientVersion),
		ProtocolMin: ProtocolVersion, ProtocolMax: ProtocolVersion, PublicKey: registration.PublicKey, CredentialRevision: 1,
		AllowedCapabilities: append([]string(nil), defaultAllowedCapabilities...), CreatedAt: now,
	}
	return s.store.RegisterDevice(ctx, hashSecret(accessToken), device, now)
}

func (s *Service) RevokeDevice(ctx context.Context, deviceID, refreshCredential string) error {
	deviceID, refreshCredential = strings.TrimSpace(deviceID), strings.TrimSpace(refreshCredential)
	if !validIdentifier(deviceID) || refreshCredential == "" {
		return ErrInvalidRequest
	}
	return s.store.RevokeDevice(ctx, deviceID, hashSecret(refreshCredential), s.now().UTC())
}

func (s *Service) AuthenticateAccessToken(ctx context.Context, accessToken string) (ConnectionIdentity, error) {
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return ConnectionIdentity{}, ErrUnauthorized
	}
	device, err := s.store.AuthenticateAccessToken(ctx, hashSecret(accessToken), s.now().UTC())
	if err != nil {
		return ConnectionIdentity{}, err
	}
	return ConnectionIdentity{
		DeviceID:           device.DeviceID,
		TenantID:           device.TenantID,
		OwnerID:            device.OwnerID,
		CredentialRevision: device.CredentialRevision,
	}, nil
}

func (s *Service) IssueChallenge(ctx context.Context, accessToken, deviceID string, credentialRevision int64) (Challenge, error) {
	deviceID = strings.TrimSpace(deviceID)
	if strings.TrimSpace(accessToken) == "" || !validIdentifier(deviceID) || credentialRevision < 1 {
		return Challenge{}, ErrInvalidRequest
	}
	nonce, err := s.randomSecret(32)
	if err != nil {
		return Challenge{}, err
	}
	now := s.now().UTC()
	record := challengeRecord{NonceHash: hashSecret(nonce), TokenHash: hashSecret(accessToken), DeviceID: deviceID, CreatedAt: now, ExpiresAt: now.Add(s.challengeTTL)}
	if _, err := s.store.IssueChallenge(ctx, record.TokenHash, deviceID, credentialRevision, record, now); err != nil {
		return Challenge{}, err
	}
	return Challenge{Nonce: nonce, ExpiresAt: record.ExpiresAt}, nil
}

func (s *Service) AuthenticateConnection(ctx context.Context, request ConnectionRequest) (ConnectionIdentity, error) {
	request.AccessToken = strings.TrimSpace(request.AccessToken)
	request.DeviceID = strings.TrimSpace(request.DeviceID)
	request.Nonce = strings.TrimSpace(request.Nonce)
	if request.Nonce == "" {
		request.Nonce = dpopNonce(request.Proof)
	}
	if request.AccessToken == "" || !validIdentifier(request.DeviceID) || request.CredentialRevision < 1 || request.Nonce == "" {
		return ConnectionIdentity{}, ErrUnauthorized
	}
	now := s.now().UTC()
	tokenHash, nonceHash := hashSecret(request.AccessToken), hashSecret(request.Nonce)
	device, err := s.store.ConnectionCandidate(ctx, tokenHash, request.DeviceID, nonceHash, request.CredentialRevision, now)
	if err != nil {
		return ConnectionIdentity{}, err
	}
	if err := VerifyDPoP(DPoPVerification{
		Proof: request.Proof, AccessToken: request.AccessToken, Nonce: request.Nonce, Method: request.Method,
		TargetURI: request.TargetURI, PublicKey: device.PublicKey, Now: now,
	}); err != nil {
		return ConnectionIdentity{}, ErrUnauthorized
	}
	device, err = s.store.ConsumeChallenge(ctx, tokenHash, request.DeviceID, nonceHash, request.CredentialRevision, now)
	if err != nil {
		return ConnectionIdentity{}, err
	}
	return ConnectionIdentity{DeviceID: device.DeviceID, TenantID: device.TenantID, OwnerID: device.OwnerID, CredentialRevision: device.CredentialRevision}, nil
}

func (s *Service) randomSecret(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := io.ReadFull(s.random, buffer); err != nil {
		return "", fmt.Errorf("generate device credential: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func (s *Service) randomID(prefix string) (string, error) {
	value, err := s.randomSecret(18)
	if err != nil {
		return "", err
	}
	return prefix + "_" + value, nil
}

func (s *Service) newCredentialSet() (string, string, string, error) {
	access, err := s.randomSecret(32)
	if err != nil {
		return "", "", "", err
	}
	refresh, err := s.randomSecret(48)
	if err != nil {
		return "", "", "", err
	}
	pairing, err := s.randomID("pairing")
	return access, refresh, pairing, err
}

func hashSecret(value string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func pkceChallenge(verifier string) (string, error) {
	verifier = strings.TrimSpace(verifier)
	if len(verifier) < 43 || len(verifier) > 128 {
		return "", ErrInvalidRequest
	}
	for _, char := range verifier {
		if !(char >= 'A' && char <= 'Z') && !(char >= 'a' && char <= 'z') && !(char >= '0' && char <= '9') && char != '-' && char != '.' && char != '_' && char != '~' {
			return "", ErrInvalidRequest
		}
	}
	digest := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:]), nil
}

func constantTimeStringEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func validIdentifier(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) >= 3 && len(value) <= 160 && identifierPattern.MatchString(value)
}

func validTenantID(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) >= 1 && len(value) <= 160 && !strings.ContainsAny(value, "\x00\r\n")
}

var defaultAllowedCapabilities = []string{
	"workspace.read", "workspace.write", "process.shell", "network.access",
	"artifact.patch", "artifact.summary", "artifact.selected",
}

func validateRegistration(registration DeviceRegistration) error {
	registration.DeviceName = strings.TrimSpace(registration.DeviceName)
	registration.Platform = strings.TrimSpace(registration.Platform)
	registration.Architecture = strings.TrimSpace(registration.Architecture)
	registration.ClientVersion = strings.TrimSpace(registration.ClientVersion)
	if len(registration.DeviceName) < 1 || len(registration.DeviceName) > 128 || len(registration.Architecture) < 1 || len(registration.Architecture) > 64 || len(registration.ClientVersion) < 1 || len(registration.ClientVersion) > 64 {
		return ErrInvalidRequest
	}
	if registration.Platform != "win32" && registration.Platform != "darwin" && registration.Platform != "linux" {
		return ErrInvalidRequest
	}
	if registration.ProtocolMin != ProtocolVersion || registration.ProtocolMax != ProtocolVersion {
		return ErrInvalidRequest
	}
	if _, err := registration.PublicKey.Ed25519(); err != nil {
		return ErrInvalidRequest
	}
	return nil
}
