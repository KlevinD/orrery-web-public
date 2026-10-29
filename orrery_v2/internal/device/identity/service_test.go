package identity

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"orrery_v2/internal/platform/db"
)

func TestServicePairRefreshChallengeAndRevoke(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	service := newTestService(t, &now)
	publicKey, privateKey := testKeyPair(t)
	verifier := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	challenge, err := pkceChallenge(verifier)
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := service.Authorize(ctx, BrowserPrincipal{UserID: "user-1", DisplayName: "Owner", TenantID: "global"}, AuthorizationRequest{
		ResponseType: "code", ClientID: DesktopClientID, RedirectURI: DesktopRedirectURI,
		CodeChallenge: challenge, CodeChallengeMethod: "S256", State: "state_0123456789abcdef",
	})
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	token, err := service.ExchangeToken(ctx, TokenRequest{
		GrantType: "authorization_code", ClientID: DesktopClientID, Code: authorization.Code,
		CodeVerifier: verifier, RedirectURI: DesktopRedirectURI,
	})
	if err != nil {
		t.Fatalf("ExchangeToken: %v", err)
	}
	if _, err := service.ExchangeToken(ctx, TokenRequest{
		GrantType: "authorization_code", ClientID: DesktopClientID, Code: authorization.Code,
		CodeVerifier: verifier, RedirectURI: DesktopRedirectURI,
	}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("replayed authorization code error = %v, want ErrUnauthorized", err)
	}
	device, err := service.RegisterDevice(ctx, token.AccessToken, DeviceRegistration{
		DeviceName: "Workstation", Platform: "win32", Architecture: "x64", ClientVersion: "1.0.0",
		ProtocolMin: ProtocolVersion, ProtocolMax: ProtocolVersion, PublicKey: publicKey,
	})
	if err != nil {
		t.Fatalf("RegisterDevice: %v", err)
	}
	if device.TenantID != "global" || device.OwnerID != "user-1" || device.CredentialRevision != 1 || len(device.AllowedCapabilities) != 7 {
		t.Fatalf("registered device = %+v", device)
	}
	rotated, err := service.ExchangeToken(ctx, TokenRequest{
		GrantType: "refresh_token", ClientID: DesktopClientID, DeviceID: device.DeviceID, RefreshCredential: token.RefreshCredential,
	})
	if err != nil {
		t.Fatalf("refresh token: %v", err)
	}
	if rotated.RefreshCredential == token.RefreshCredential {
		t.Fatal("refresh credential was not rotated")
	}
	accessIdentity, err := service.AuthenticateAccessToken(ctx, rotated.AccessToken)
	if err != nil {
		t.Fatalf("AuthenticateAccessToken: %v", err)
	}
	if accessIdentity.DeviceID != device.DeviceID || accessIdentity.TenantID != device.TenantID || accessIdentity.OwnerID != device.OwnerID {
		t.Fatalf("access token identity = %+v", accessIdentity)
	}
	if _, err := service.IssueChallenge(ctx, token.AccessToken, device.DeviceID, device.CredentialRevision); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("superseded access token error = %v, want ErrUnauthorized", err)
	}
	if _, err := service.ExchangeToken(ctx, TokenRequest{
		GrantType: "refresh_token", ClientID: DesktopClientID, DeviceID: device.DeviceID, RefreshCredential: token.RefreshCredential,
	}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("reused refresh credential error = %v, want ErrUnauthorized", err)
	}
	issued, err := service.IssueChallenge(ctx, rotated.AccessToken, device.DeviceID, device.CredentialRevision)
	if err != nil {
		t.Fatalf("IssueChallenge: %v", err)
	}
	target := "http://127.0.0.1:52021/api/devices/v1/connect"
	proof := testDPoP(t, privateKey, publicKey, rotated.AccessToken, issued.Nonce, target, now)
	connection, err := service.AuthenticateConnection(ctx, ConnectionRequest{
		AccessToken: rotated.AccessToken, DeviceID: device.DeviceID, CredentialRevision: device.CredentialRevision,
		Proof: proof, Method: "GET", TargetURI: target,
	})
	if err != nil {
		t.Fatalf("AuthenticateConnection: %v", err)
	}
	if connection.DeviceID != device.DeviceID || connection.TenantID != device.TenantID || connection.OwnerID != device.OwnerID {
		t.Fatalf("connection identity = %+v", connection)
	}
	if _, err := service.AuthenticateConnection(ctx, ConnectionRequest{
		AccessToken: rotated.AccessToken, DeviceID: device.DeviceID, CredentialRevision: device.CredentialRevision,
		Nonce: issued.Nonce, Proof: proof, Method: "GET", TargetURI: target,
	}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("replayed challenge error = %v, want ErrUnauthorized", err)
	}
	if err := service.RevokeDevice(ctx, device.DeviceID, rotated.RefreshCredential); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}
	if _, err := service.AuthenticateAccessToken(ctx, rotated.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("access token after revoke error = %v, want ErrUnauthorized", err)
	}
	if _, err := service.ExchangeToken(ctx, TokenRequest{
		GrantType: "refresh_token", ClientID: DesktopClientID, DeviceID: device.DeviceID, RefreshCredential: rotated.RefreshCredential,
	}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("refresh after revoke error = %v, want ErrUnauthorized", err)
	}
}

func TestServiceRejectsInvalidProofWithoutConsumingChallenge(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	service := newTestService(t, &now)
	publicKey, privateKey := testKeyPair(t)
	device, token := pairTestDevice(t, ctx, service, publicKey)
	issued, err := service.IssueChallenge(ctx, token.AccessToken, device.DeviceID, 1)
	if err != nil {
		t.Fatal(err)
	}
	target := "http://localhost:8080/api/devices/v1/connect"
	wrong := testDPoP(t, privateKey, publicKey, "wrong-access-token", issued.Nonce, target, now)
	request := ConnectionRequest{AccessToken: token.AccessToken, DeviceID: device.DeviceID, CredentialRevision: 1, Nonce: issued.Nonce, Proof: wrong, Method: "GET", TargetURI: target}
	if _, err := service.AuthenticateConnection(ctx, request); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("invalid proof error = %v", err)
	}
	request.Proof = testDPoP(t, privateKey, publicKey, token.AccessToken, issued.Nonce, target, now)
	if _, err := service.AuthenticateConnection(ctx, request); err != nil {
		t.Fatalf("valid proof after rejected proof: %v", err)
	}
}

func TestServiceRejectsExpiredAndMismatchedPKCECodes(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	service := newTestService(t, &now)
	verifier := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	challenge, _ := pkceChallenge(verifier)
	request := AuthorizationRequest{ResponseType: "code", ClientID: DesktopClientID, RedirectURI: DesktopRedirectURI, CodeChallenge: challenge, CodeChallengeMethod: "S256", State: "state_0123456789abcdef"}
	grant, err := service.Authorize(ctx, BrowserPrincipal{UserID: "user-1", TenantID: "global"}, request)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ExchangeToken(ctx, TokenRequest{GrantType: "authorization_code", ClientID: DesktopClientID, Code: grant.Code, CodeVerifier: verifier + "x", RedirectURI: DesktopRedirectURI})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("mismatched verifier error = %v", err)
	}
	grant, err = service.Authorize(ctx, BrowserPrincipal{UserID: "user-1", TenantID: "global"}, request)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(6 * time.Minute)
	_, err = service.ExchangeToken(ctx, TokenRequest{GrantType: "authorization_code", ClientID: DesktopClientID, Code: grant.Code, CodeVerifier: verifier, RedirectURI: DesktopRedirectURI})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired code error = %v", err)
	}
}

func newTestService(t *testing.T, now *time.Time) *Service {
	t.Helper()
	store, err := db.Open(context.Background(), db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	identityStore, err := NewSQLStore(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(Options{Store: identityStore, Now: func() time.Time { return *now }})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func pairTestDevice(t *testing.T, ctx context.Context, service *Service, publicKey PublicKeyJWK) (Device, Token) {
	t.Helper()
	verifier := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	challenge, _ := pkceChallenge(verifier)
	grant, err := service.Authorize(ctx, BrowserPrincipal{UserID: "user-1", TenantID: "global"}, AuthorizationRequest{ResponseType: "code", ClientID: DesktopClientID, RedirectURI: DesktopRedirectURI, CodeChallenge: challenge, CodeChallengeMethod: "S256", State: "state_0123456789abcdef"})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := service.ExchangeToken(ctx, TokenRequest{GrantType: "authorization_code", ClientID: DesktopClientID, Code: grant.Code, CodeVerifier: verifier, RedirectURI: DesktopRedirectURI})
	if err != nil {
		t.Fatal(err)
	}
	device, err := service.RegisterDevice(ctx, pending.AccessToken, DeviceRegistration{DeviceName: "Desktop", Platform: "win32", Architecture: "x64", ClientVersion: "1.0.0", ProtocolMin: ProtocolVersion, ProtocolMax: ProtocolVersion, PublicKey: publicKey})
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := service.ExchangeToken(ctx, TokenRequest{GrantType: "refresh_token", ClientID: DesktopClientID, DeviceID: device.DeviceID, RefreshCredential: pending.RefreshCredential})
	if err != nil {
		t.Fatal(err)
	}
	return device, rotated
}

func testKeyPair(t *testing.T) (PublicKeyJWK, ed25519.PrivateKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return PublicKeyJWK{KTY: "OKP", CRV: "Ed25519", X: base64.RawURLEncoding.EncodeToString(public)}, private
}

func testDPoP(t *testing.T, private ed25519.PrivateKey, public PublicKeyJWK, accessToken, nonce, target string, now time.Time) string {
	t.Helper()
	header, _ := json.Marshal(dpopHeader{Type: "dpop+jwt", Alg: "EdDSA", JWK: public})
	digest := sha256.Sum256([]byte(accessToken))
	claims, _ := json.Marshal(dpopClaims{JTI: "proof-0123456789abcdef", HTM: "GET", HTU: target, IAT: now.Unix(), ATH: base64.RawURLEncoding.EncodeToString(digest[:]), Nonce: nonce})
	encodedHeader := base64.RawURLEncoding.EncodeToString(header)
	encodedClaims := base64.RawURLEncoding.EncodeToString(claims)
	signingInput := encodedHeader + "." + encodedClaims
	signature := ed25519.Sign(private, []byte(signingInput))
	return fmt.Sprintf("%s.%s", signingInput, base64.RawURLEncoding.EncodeToString(signature))
}
