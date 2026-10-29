package userauth

import (
	"testing"
	"time"

	"orrery_v2/internal/app/identity"
)

func TestDesktopIdentitySessionRequiresVerifiedFirstPartyIdentityOnlyToken(t *testing.T) {
	tests := []struct {
		name, provider, client, scopes, source string
		expiry                                 bool
		want                                   bool
	}{
		{"desktop identity", "hydra", "orrery-desktop", "openid profile offline_access", "kratos", true, true},
		{"other client", "hydra", "orrery-cli", "openid profile", "kratos", true, false},
		{"other provider", "oidc", "orrery-desktop", "openid profile", "kratos", true, false},
		{"legacy capability", "hydra", "orrery-desktop", "openid profile orrery:mission.read", "kratos", true, false},
		{"unknown scope", "hydra", "orrery-desktop", "openid profile custom", "kratos", true, false},
		{"missing identity scope", "hydra", "orrery-desktop", "profile", "kratos", true, false},
		{"missing source", "hydra", "orrery-desktop", "openid profile", "", true, false},
		{"missing expiry", "hydra", "orrery-desktop", "openid profile", "kratos", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			external := identity.ExternalIdentity{Provider: tt.provider, Traits: map[string]string{"oidc_client_id": tt.client, "oidc_scope": tt.scopes, "oauth2_subject_provider": tt.source, "oauth2_subject": "source-subject"}}
			if tt.expiry {
				external.ExpiresAt = time.Now().Add(time.Hour)
			}
			if got := desktopIdentitySession(external); got != tt.want {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
}
