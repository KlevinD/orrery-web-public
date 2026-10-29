package openclaw

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func remoteWSDialer(timeout time.Duration, cfg RuntimeConfig) (*websocket.Dialer, error) {
	dialer := &websocket.Dialer{HandshakeTimeout: minDuration(timeout, 10*time.Second)}
	tlsConfig, err := tlsConfigForFingerprint(cfg.TLSFingerprint)
	if err != nil {
		return nil, fmt.Errorf("openclaw remote_ws %w", err)
	}
	if tlsConfig == nil {
		return dialer, nil
	}
	dialer.TLSClientConfig = tlsConfig
	return dialer, nil
}

func tlsConfigForFingerprint(rawFingerprint string) (*tls.Config, error) {
	expected := normalizeTLSFingerprint(rawFingerprint)
	if strings.TrimSpace(rawFingerprint) != "" && expected == "" {
		return nil, fmt.Errorf("tls_fingerprint must be a SHA-256 certificate fingerprint")
	}
	if expected == "" {
		return nil, nil
	}
	return &tls.Config{
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("gateway tls fingerprint unavailable")
			}
			actual := sha256.Sum256(rawCerts[0])
			if hex.EncodeToString(actual[:]) != expected {
				return fmt.Errorf("gateway tls fingerprint mismatch")
			}
			return nil
		},
	}, nil
}

func normalizeTLSFingerprint(input string) string {
	value := strings.ToLower(strings.TrimSpace(input))
	value = strings.TrimPrefix(value, "sha256:")
	value = strings.TrimPrefix(value, "sha-256:")
	var out strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'f' || r >= '0' && r <= '9' {
			out.WriteRune(r)
		}
	}
	if out.Len() != sha256.Size*2 {
		return ""
	}
	return out.String()
}
