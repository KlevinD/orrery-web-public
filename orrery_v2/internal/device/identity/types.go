package identity

import (
	"context"
	"errors"
	"time"
)

const (
	DesktopClientID    = "orrery-desktop"
	DesktopRedirectURI = "orrery://device/pairing-callback"
	ProtocolVersion    = "1.0"
)

var (
	ErrInvalidRequest = errors.New("device identity request is invalid")
	ErrUnauthorized   = errors.New("device credential is invalid")
	ErrConflict       = errors.New("device identity conflicts with existing state")
	ErrDependency     = errors.New("device identity dependency is unavailable")
)

type BrowserPrincipal struct {
	UserID      string
	DisplayName string
	TenantID    string
}

type AuthorizationRequest struct {
	ResponseType        string
	ClientID            string
	RedirectURI         string
	CodeChallenge       string
	CodeChallengeMethod string
	State               string
}

type AuthorizationGrant struct {
	Code        string
	RedirectURI string
	State       string
	ExpiresAt   time.Time
}

type TokenRequest struct {
	GrantType         string
	ClientID          string
	Code              string
	CodeVerifier      string
	RedirectURI       string
	DeviceID          string
	RefreshCredential string
}

type Token struct {
	AccessToken       string
	RefreshCredential string
	ExpiresIn         int64
}

type PublicKeyJWK struct {
	KTY string `json:"kty"`
	CRV string `json:"crv"`
	X   string `json:"x"`
}

type DeviceRegistration struct {
	DeviceName    string
	Platform      string
	Architecture  string
	ClientVersion string
	ProtocolMin   string
	ProtocolMax   string
	PublicKey     PublicKeyJWK
}

type Device struct {
	DeviceID            string
	PairingID           string
	TenantID            string
	OwnerID             string
	DeviceName          string
	Platform            string
	Architecture        string
	ClientVersion       string
	ProtocolMin         string
	ProtocolMax         string
	PublicKey           PublicKeyJWK
	CredentialRevision  int64
	AllowedCapabilities []string
	CreatedAt           time.Time
	RevokedAt           time.Time
}

type AccessGrant struct {
	TokenHash          string
	PairingID          string
	DeviceID           string
	TenantID           string
	OwnerID            string
	CredentialRevision int64
	ExpiresAt          time.Time
	RevokedAt          time.Time
}

type Challenge struct {
	Nonce     string
	ExpiresAt time.Time
}

type ConnectionRequest struct {
	AccessToken        string
	DeviceID           string
	CredentialRevision int64
	Nonce              string
	Proof              string
	Method             string
	TargetURI          string
}

type ConnectionIdentity struct {
	DeviceID           string
	TenantID           string
	OwnerID            string
	CredentialRevision int64
}

type codeRecord struct {
	CodeHash         string
	ClientID         string
	RedirectURI      string
	CodeChallenge    string
	TenantID         string
	OwnerID          string
	OwnerDisplayName string
	ExpiresAt        time.Time
	CreatedAt        time.Time
}

type pairingRecord struct {
	PairingID       string
	TenantID        string
	OwnerID         string
	RefreshHash     string
	RefreshRevision int64
	DeviceID        string
	CreatedAt       time.Time
}

type exchangeRecord struct {
	CodeHash           string
	ClientID           string
	RedirectURI        string
	PresentedChallenge string
	Pairing            pairingRecord
	Access             AccessGrant
	Now                time.Time
}

type challengeRecord struct {
	NonceHash string
	TokenHash string
	DeviceID  string
	ExpiresAt time.Time
	CreatedAt time.Time
}

type Store interface {
	SaveAuthorizationCode(ctx context.Context, record codeRecord) error
	ExchangeAuthorizationCode(ctx context.Context, record exchangeRecord) error
	RegisterDevice(ctx context.Context, tokenHash string, device Device, now time.Time) (Device, error)
	RotateRefreshCredential(ctx context.Context, deviceID, currentHash, replacementHash, accessHash string, accessExpiresAt, now time.Time) (AccessGrant, error)
	RevokeDevice(ctx context.Context, deviceID, refreshHash string, now time.Time) error
	AuthenticateAccessToken(ctx context.Context, tokenHash string, now time.Time) (Device, error)
	IssueChallenge(ctx context.Context, tokenHash, deviceID string, credentialRevision int64, challenge challengeRecord, now time.Time) (Device, error)
	ConnectionCandidate(ctx context.Context, tokenHash, deviceID, nonceHash string, credentialRevision int64, now time.Time) (Device, error)
	ConsumeChallenge(ctx context.Context, tokenHash, deviceID, nonceHash string, credentialRevision int64, now time.Time) (Device, error)
}
