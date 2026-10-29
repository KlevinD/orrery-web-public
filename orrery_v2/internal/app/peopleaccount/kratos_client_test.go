package peopleaccount

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestKratosAccountProjectionLooksUpExactUsername(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet || req.URL.Path != "/admin/identities" {
			t.Fatalf("request = %s %s", req.Method, req.URL.Path)
		}
		if got := req.URL.Query().Get("credentials_identifier"); got != "quzheng@example.com" {
			t.Fatalf("credentials_identifier = %q", got)
		}
		if got := req.URL.Query()["include_credential"]; len(got) != 2 || got[0] != "password" || got[1] != "oidc" {
			t.Fatalf("include_credential = %v", got)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer admin-token" {
			t.Fatalf("authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"id": "kratos-quzheng", "state": "active", "metadata_admin": map[string]any{"person_id": "human-quzheng"},
			"credentials": map[string]any{
				"password": map[string]any{"config": map[string]any{"hashed_password": "$2a$10$existing"}},
				"oidc": map[string]any{"config": map[string]any{"providers": []map[string]any{
					{"provider": "github", "subject": "gh-1"},
					{"provider": "feishu", "subject": "ou_quzheng"},
				}}},
			},
		}})
	}))
	defer server.Close()
	client := newKratosTestClient(t, server.URL)

	identity, found, err := client.FindIdentityByUsername(context.Background(), "quzheng@example.com")
	if err != nil {
		t.Fatalf("FindIdentityByUsername: %v", err)
	}
	if !found || identity.ID != "kratos-quzheng" || identity.PersonID != "human-quzheng" || identity.FeishuSubject != "ou_quzheng" || !identity.passwordCredentialPresent || identity.hashedPassword != "$2a$10$existing" || len(identity.oidcProviders) != 2 {
		t.Fatalf("identity = %+v found=%v", identity, found)
	}
}

func TestKratosAccountProjectionCreatesHashedCredentialAndFeishuRelation(t *testing.T) {
	const plaintext = "plain-initial-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost || req.URL.Path != "/admin/identities" {
			t.Fatalf("request = %s %s", req.Method, req.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode create body: %v", err)
		}
		encoded, _ := json.Marshal(body)
		if strings.Contains(string(encoded), plaintext) {
			t.Fatalf("create body contains plaintext password: %s", encoded)
		}
		if body["schema_id"] != "default" || body["state"] != "active" {
			t.Fatalf("create body = %+v", body)
		}
		traits := body["traits"].(map[string]any)
		if traits["person_id"] != "human-quzheng" || traits["person_revision"] != float64(7) || traits["username"] != "quzheng" {
			t.Fatalf("traits = %+v", traits)
		}
		metadata := body["metadata_admin"].(map[string]any)
		if metadata["person_id"] != "human-quzheng" || metadata["password_change_required"] != true {
			t.Fatalf("metadata_admin = %+v", metadata)
		}
		credentials := body["credentials"].(map[string]any)
		password := credentials["password"].(map[string]any)
		passwordConfig := password["config"].(map[string]any)
		if passwordConfig["hashed_password"] != "$2a$10$already-hashed" {
			t.Fatalf("password credential = %+v", password)
		}
		oidc := credentials["oidc"].(map[string]any)
		oidcConfig := oidc["config"].(map[string]any)
		providers := oidcConfig["providers"].([]any)
		provider := providers[0].(map[string]any)
		if provider["provider"] != "feishu" || provider["subject"] != "ou_quzheng" {
			t.Fatalf("oidc providers = %+v", providers)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "kratos-quzheng", "state": "active", "metadata_admin": map[string]any{"person_id": "human-quzheng"}})
	}))
	defer server.Close()
	client := newKratosTestClient(t, server.URL)

	identity, err := client.CreateIdentity(context.Background(), Projection{
		PersonID: "human-quzheng", Revision: 7, Username: "quzheng", DisplayName: "屈正", EnterpriseEmail: "quzheng@example.com", Enabled: true, FeishuSubject: "ou_quzheng",
	}, KratosCredential{HashedPassword: "$2a$10$already-hashed", PasswordChangeRequired: true})
	if err != nil {
		t.Fatalf("CreateIdentity: %v", err)
	}
	if identity.ID != "kratos-quzheng" {
		t.Fatalf("identity = %+v", identity)
	}
}

func TestKratosAccountProjectionUpdatesWithoutResettingPassword(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPut || req.URL.Path != "/admin/identities/kratos-quzheng" {
			t.Fatalf("request = %s %s", req.Method, req.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode update body: %v", err)
		}
		if _, ok := body["credentials"]; ok {
			t.Fatalf("update must not reset credentials: %+v", body)
		}
		if body["state"] != "active" {
			t.Fatalf("update state = %+v", body["state"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "kratos-quzheng", "state": "active", "metadata_admin": map[string]any{"person_id": "human-quzheng"}})
	}))
	defer server.Close()
	client := newKratosTestClient(t, server.URL)

	_, err := client.UpdateIdentity(context.Background(), KratosIdentity{ID: "kratos-quzheng", PersonID: "human-quzheng"}, Projection{
		PersonID: "human-quzheng", Revision: 8, Username: "quzheng", DisplayName: "屈正", Enabled: true,
	})
	if err != nil {
		t.Fatalf("UpdateIdentity: %v", err)
	}
}

func TestKratosAccountProjectionLinksFeishuWithoutDroppingExistingCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPut || req.URL.Path != "/admin/identities/kratos-quzheng" {
			t.Fatalf("request = %s %s", req.Method, req.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode update body: %v", err)
		}
		credentials := body["credentials"].(map[string]any)
		passwordConfig := credentials["password"].(map[string]any)["config"].(map[string]any)
		if passwordConfig["hashed_password"] != "$2a$10$existing" {
			t.Fatalf("password credential = %+v", passwordConfig)
		}
		oidcConfig := credentials["oidc"].(map[string]any)["config"].(map[string]any)
		providers := oidcConfig["providers"].([]any)
		if len(providers) != 2 {
			t.Fatalf("OIDC providers = %+v", providers)
		}
		first := providers[0].(map[string]any)
		second := providers[1].(map[string]any)
		if first["provider"] != "github" || first["subject"] != "gh-1" || second["provider"] != "feishu" || second["subject"] != "ou_quzheng" {
			t.Fatalf("OIDC providers = %+v", providers)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "kratos-quzheng", "state": "active", "metadata_admin": map[string]any{"person_id": "human-quzheng"}})
	}))
	defer server.Close()
	client := newKratosTestClient(t, server.URL)
	current := KratosIdentity{
		ID: "kratos-quzheng", PersonID: "human-quzheng", passwordCredentialPresent: true,
		hashedPassword: "$2a$10$existing", oidcProviders: []map[string]any{{"provider": "github", "subject": "gh-1"}},
	}

	if _, err := client.UpdateIdentity(context.Background(), current, Projection{
		PersonID: "human-quzheng", Revision: 8, Username: "quzheng", DisplayName: "屈正", Enabled: true, FeishuSubject: "ou_quzheng",
	}); err != nil {
		t.Fatalf("UpdateIdentity: %v", err)
	}
}

func TestKratosAccountProjectionRejectsFeishuLinkWhenCredentialsCannotBePreserved(t *testing.T) {
	client := newKratosTestClient(t, "http://127.0.0.1:1")
	current := KratosIdentity{ID: "kratos-quzheng", PersonID: "human-quzheng", unsupportedCredentials: true}

	_, err := client.UpdateIdentity(context.Background(), current, Projection{
		PersonID: "human-quzheng", Revision: 8, Username: "quzheng", Enabled: true, FeishuSubject: "ou_quzheng",
	})
	if !errors.Is(err, ErrProjectionConflict) {
		t.Fatalf("UpdateIdentity error = %v, want ErrProjectionConflict", err)
	}
}

func TestKratosAccountProjectionRevokesSessionsOnDisable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodDelete || req.URL.Path != "/admin/identities/kratos-quzheng/sessions" {
			t.Fatalf("request = %s %s", req.Method, req.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := newKratosTestClient(t, server.URL)

	if err := client.RevokeIdentitySessions(context.Background(), "kratos-quzheng"); err != nil {
		t.Fatalf("RevokeIdentitySessions: %v", err)
	}
}

func TestKratosInitialPasswordRequirementReadsAdminMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet || req.URL.Path != "/admin/identities/kratos-quzheng" {
			t.Fatalf("request = %s %s", req.Method, req.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "kratos-quzheng", "state": "active",
			"metadata_admin": map[string]any{"password_change_required": true},
		})
	}))
	defer server.Close()
	client := newKratosTestClient(t, server.URL)

	required, err := client.InitialPasswordChangeRequired(context.Background(), "kratos-quzheng")
	if err != nil {
		t.Fatalf("InitialPasswordChangeRequired: %v", err)
	}
	if !required {
		t.Fatal("InitialPasswordChangeRequired = false, want true")
	}
}

func TestKratosInitialPasswordChangeUpdatesKratosAndRevokesSessions(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests++
		switch requests {
		case 1:
			if req.Method != http.MethodGet || req.URL.Path != "/admin/identities/kratos-quzheng" {
				t.Fatalf("request 1 = %s %s", req.Method, req.URL.Path)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "kratos-quzheng", "schema_id": "default", "state": "active",
				"traits":         map[string]any{"username": "quzheng", "person_id": "human-quzheng"},
				"metadata_admin": map[string]any{"person_id": "human-quzheng", "password_change_required": true},
				"credentials": map[string]any{
					"password": map[string]any{"config": map[string]any{"hashed_password": "$2a$10$old"}},
					"oidc":     map[string]any{"config": map[string]any{"providers": []map[string]any{{"provider": "feishu", "subject": "ou_quzheng"}}}},
				},
			})
		case 2:
			if req.Method != http.MethodPut || req.URL.Path != "/admin/identities/kratos-quzheng" {
				t.Fatalf("request %d = %s %s", requests, req.Method, req.URL.Path)
			}
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode update body: %v", err)
			}
			credentials := body["credentials"].(map[string]any)
			if len(credentials) != 1 {
				t.Fatalf("credentials = %+v, want password only", credentials)
			}
			passwordConfig := credentials["password"].(map[string]any)["config"].(map[string]any)
			if passwordConfig["password"] != "replacement-password-1" {
				t.Fatalf("password credential = %+v", passwordConfig)
			}
			metadata := body["metadata_admin"].(map[string]any)
			if metadata["password_change_required"] != true {
				t.Fatalf("metadata = %+v", metadata)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "kratos-quzheng"})
		case 3:
			if req.Method != http.MethodDelete || req.URL.Path != "/admin/identities/kratos-quzheng/sessions" {
				t.Fatalf("request 3 = %s %s", req.Method, req.URL.Path)
			}
			w.WriteHeader(http.StatusNoContent)
		case 4:
			if req.Method != http.MethodPatch || req.URL.Path != "/admin/identities/kratos-quzheng" {
				t.Fatalf("request 4 = %s %s", req.Method, req.URL.Path)
			}
			if got := req.Header.Get("Content-Type"); got != "application/json-patch+json" {
				t.Fatalf("request 4 content type = %q", got)
			}
			var patch []map[string]string
			if err := json.NewDecoder(req.Body).Decode(&patch); err != nil {
				t.Fatalf("decode metadata patch: %v", err)
			}
			if len(patch) != 1 || patch[0]["op"] != "remove" || patch[0]["path"] != "/metadata_admin/password_change_required" {
				t.Fatalf("metadata patch = %+v", patch)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "kratos-quzheng"})
		default:
			t.Fatalf("unexpected request %d: %s %s", requests, req.Method, req.URL.Path)
		}
	}))
	defer server.Close()
	client := newKratosTestClient(t, server.URL)

	if err := client.ChangeInitialPassword(context.Background(), "kratos-quzheng", "replacement-password-1"); err != nil {
		t.Fatalf("ChangeInitialPassword: %v", err)
	}
	if requests != 4 {
		t.Fatalf("requests = %d, want 4", requests)
	}
}

func TestKratosAccountProjectionErrorDoesNotExposeRequestSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"plain-initial-secret"}`))
	}))
	defer server.Close()
	client := newKratosTestClient(t, server.URL)

	_, err := client.CreateIdentity(context.Background(), Projection{PersonID: "human-quzheng", Revision: 1, Username: "quzheng", Enabled: true}, KratosCredential{
		HashedPassword: "$2a$10$already-hashed", PasswordChangeRequired: true,
	})
	if err == nil || strings.Contains(err.Error(), "plain-initial-secret") {
		t.Fatalf("CreateIdentity error = %v", err)
	}
}

func newKratosTestClient(t *testing.T, serverURL string) *KratosAdminClient {
	t.Helper()
	client, err := NewKratosAdminClient(KratosAdminConfig{
		AdminURL: serverURL, AdminToken: "admin-token", SchemaID: "default", FeishuProviderID: "feishu", HTTPClient: http.DefaultClient,
	})
	if err != nil {
		t.Fatalf("NewKratosAdminClient: %v", err)
	}
	return client
}
