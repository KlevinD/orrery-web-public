package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdentityAccessDeploymentArtifactsStayAligned(t *testing.T) {
	compose := readDeploymentArtifact(t, "deploy", "ory", "docker-compose.identity.yml")
	env := readDeploymentArtifact(t, ".env.example")
	clients := readDeploymentArtifact(t, "deploy", "ory", "oauth2-clients.example.json")
	readme := readDeploymentArtifact(t, "deploy", "ory", "README.md")
	roadmap := readDeploymentArtifact(t, "docs", "agent-company-mission-control", "identity-access-three-phase-roadmap.md")

	for _, token := range []string{
		"hydra:",
		"hydra-migrate:",
		"openfga:",
		"openfga-migrate:",
		"URLS_LOGIN: ${ORRERY_APP_PUBLIC_URL:?ORRERY_APP_PUBLIC_URL is required}/api/oauth2/login",
		"URLS_CONSENT: ${ORRERY_APP_PUBLIC_URL:?ORRERY_APP_PUBLIC_URL is required}/api/oauth2/consent",
		"ORRERY_HYDRA_ADMIN_TOKEN:",
		"ORRERY_OIDC_CLIENT_SECRET:",
		"ORRERY_AUTHZ_OPENFGA_TOKEN:",
	} {
		if !strings.Contains(compose, token) {
			t.Fatalf("compose missing %q", token)
		}
	}
	for _, token := range []string{
		"ORRERY_HYDRA_ADMIN_URL:",
		"ORRERY_OIDC_INTROSPECTION_URL:",
		"ORRERY_AUTHZ_OPENFGA_API_URL:",
		"ORRERY_AUTHZ_MODE:",
		"ORRERY_AUTHZ_ROLE_SCOPE_MODE:",
		"ORRERY_AUTHZ_OPENFGA_ENFORCE_READY:",
		"ORRERY_IDENTITY_AUTO_LINK_ENABLED:",
	} {
		if strings.Contains(compose, token) {
			t.Fatalf("compose still injects non-sensitive API config %q", token)
		}
	}

	for _, token := range []string{
		"ORRERY_APP_PUBLIC_URL=",
		"ORY_HYDRA_PUBLIC_URL=",
		"ORY_HYDRA_SYSTEM_SECRET=",
		"ORRERY_AUTH_LOCAL_MFA_TOTP_SECRET=",
		"ORRERY_HYDRA_ADMIN_TOKEN=",
		"ORRERY_OIDC_CLIENT_SECRET=",
		"ORRERY_AUTHZ_OPENFGA_TOKEN=",
	} {
		if !strings.Contains(env, token) {
			t.Fatalf("env example missing %q", token)
		}
	}

	for _, token := range []string{
		`"client_id": "orrery-marketing"`,
		`"https://orrery.minvia.cn/api/oauth2/marketing/callback"`,
		`"client_id": "orrery-cli"`,
		`"client_id": "orrery-agent-runtime"`,
		`"client_id": "orrery-external-integration"`,
		`"redirect_uris"`,
		`"token_endpoint_auth_method": "none"`,
		`"token_endpoint_auth_method": "client_secret_basic"`,
	} {
		if !strings.Contains(clients, token) {
			t.Fatalf("oauth2 client examples missing %q", token)
		}
	}

	for _, token := range []string{
		"docs/agent-company-mission-control/identity-access-three-phase-roadmap.md",
		"POST /api/oauth2/clients",
		"openfga_shadow",
		"authz.openfga.enforce_ready",
		"authorization.role_scope_transition",
	} {
		if !strings.Contains(readme, token) {
			t.Fatalf("deployment runbook missing %q", token)
		}
	}

	for _, token := range []string{
		"### 4.2.6 当前落地状态：Hydra / OpenFGA deployment overlay",
		"`deploy/ory/docker-compose.identity.yml`",
		"`deploy/ory/oauth2-clients.example.json`",
		"模板为 `.env.example`",
	} {
		if !strings.Contains(roadmap, token) {
			t.Fatalf("roadmap missing %q", token)
		}
	}
}

func TestDevelopmentOpenFGAModelMatchesTrackedDSLTypes(t *testing.T) {
	dsl := readDeploymentArtifact(t, "docs", "agent-company-mission-control", "openfga", "authorization-model.fga")
	raw := readDeploymentArtifact(t, "deploy", "dev", "identity", "authorization-model.json")
	var model struct {
		SchemaVersion   string `json:"schema_version"`
		TypeDefinitions []struct {
			Type string `json:"type"`
		} `json:"type_definitions"`
	}
	if err := json.Unmarshal([]byte(raw), &model); err != nil {
		t.Fatalf("decode development OpenFGA model: %v", err)
	}
	if model.SchemaVersion != "1.1" {
		t.Fatalf("schema_version = %q", model.SchemaVersion)
	}
	wantTypes := []string{"user", "agent", "system", "platform", "organization", "department", "role", "mission", "discussion", "project", "development_project", "scm_connection", "todo", "resource"}
	if len(model.TypeDefinitions) != len(wantTypes) {
		t.Fatalf("type definition count = %d, want %d", len(model.TypeDefinitions), len(wantTypes))
	}
	for i, want := range wantTypes {
		if got := model.TypeDefinitions[i].Type; got != want {
			t.Fatalf("type definition %d = %q, want %q", i, got, want)
		}
		if !strings.Contains(dsl, "type "+want) {
			t.Fatalf("tracked OpenFGA DSL is missing type %q", want)
		}
	}
	for _, relation := range []string{"automation", "origin_todo", "origin_development_project", "qa_approver", "configurer", "merge_authorizer"} {
		if !strings.Contains(dsl, "define "+relation+":") || !strings.Contains(raw, `"`+relation+`"`) {
			t.Fatalf("OpenFGA deployment artifacts are missing relation %q", relation)
		}
	}
}

func TestDevelopmentKratosOffersFeishuThroughLarkProvider(t *testing.T) {
	kratos := readDeploymentArtifact(t, "deploy", "dev", "identity", "kratos.yml")
	compose := readDeploymentArtifact(t, "deploy", "dev", "identity", "docker-compose.yml")
	schema := readDeploymentArtifact(t, "deploy", "dev", "identity", "identity.schema.json")
	mapper := readDeploymentArtifact(t, "deploy", "dev", "identity", "feishu.jsonnet")

	for _, token := range []string{
		"oidc:",
		"provider: lark",
		"id: feishu",
		"client_id: ${ORRERY_FEISHU_APP_ID}",
		"client_secret: ${ORRERY_FEISHU_APP_SECRET}",
		"mapper_url: file:///etc/config/kratos/feishu.jsonnet",
		"scope: []",
	} {
		if !strings.Contains(kratos, token) {
			t.Fatalf("development Kratos config missing %q", token)
		}
	}
	for _, token := range []string{
		"./feishu.jsonnet:/etc/config/kratos/feishu.jsonnet:ro",
		"ORRERY_FEISHU_APP_ID:",
		"ORRERY_FEISHU_APP_SECRET:",
		`127.0.0.1:${ORRERY_KRATOS_ADMIN_PORT:-4434}:4434`,
	} {
		if !strings.Contains(compose, token) {
			t.Fatalf("development identity compose missing %q", token)
		}
	}
	for _, token := range []string{`"person_id"`, `"person_revision"`, `"readOnly": true`} {
		if !strings.Contains(schema, token) {
			t.Fatalf("development identity schema missing %q", token)
		}
	}
	for _, token := range []string{`std.extVar('claims')`, "username:", "display_name:"} {
		if !strings.Contains(mapper, token) {
			t.Fatalf("Feishu identity mapper missing %q", token)
		}
	}
}

func TestProductionKratosKeepsAdminPrivateAndOffersFeishu(t *testing.T) {
	compose := readDeploymentArtifact(t, "deploy", "ory", "docker-compose.identity.yml")
	env := readDeploymentArtifact(t, ".env.example")
	kratos := readDeploymentArtifact(t, "deploy", "ory", "kratos.yml")
	schema := readDeploymentArtifact(t, "deploy", "ory", "identity.schema.json")
	mapper := readDeploymentArtifact(t, "deploy", "ory", "feishu.jsonnet")

	for _, token := range []string{
		"kratos-postgres:",
		"kratos-migrate:",
		"kratos:",
		"ORY_KRATOS_IMAGE:",
		"SELFSERVICE_METHODS_OIDC_CONFIG_PROVIDERS_0_CLIENT_ID: ${ORRERY_FEISHU_APP_ID:?ORRERY_FEISHU_APP_ID is required}",
		"SELFSERVICE_METHODS_OIDC_CONFIG_PROVIDERS_0_CLIENT_SECRET:",
		"ORRERY_FEISHU_APP_ID: ${ORRERY_FEISHU_APP_ID:?ORRERY_FEISHU_APP_ID is required}",
		"ORRERY_FEISHU_APP_SECRET",
		"kratos-control",
		"${ORRERY_V2_ROOT:-.}/deploy/ory/kratos.yml:/etc/config/kratos/kratos.yml:ro",
	} {
		if !strings.Contains(compose, token) {
			t.Fatalf("production identity compose missing %q", token)
		}
	}
	if strings.Contains(compose, "4434:4434") {
		t.Fatal("production Kratos Admin API is mapped to the host")
	}
	for _, token := range []string{
		"ORY_KRATOS_POSTGRES_PASSWORD=",
		"ORY_KRATOS_COOKIE_SECRET=",
		"ORY_KRATOS_CIPHER_SECRET=",
		"ORRERY_FEISHU_APP_ID=",
		"ORRERY_FEISHU_APP_SECRET=",
		"ORRERY_KRATOS_ADMIN_TOKEN=",
	} {
		if !strings.Contains(env, token) {
			t.Fatalf("production identity env missing %q", token)
		}
	}
	for _, token := range []string{
		"provider: lark",
		"id: feishu",
		"mapper_url: file:///etc/config/kratos/feishu.jsonnet",
		"scope: []",
		"registration:\n      enabled: false",
	} {
		if !strings.Contains(kratos, token) {
			t.Fatalf("production Kratos config missing %q", token)
		}
	}
	for _, token := range []string{`"agent_profile_id"`, `"person_id"`, `"person_revision"`, `"readOnly": true`} {
		if !strings.Contains(schema, token) {
			t.Fatalf("production identity schema missing %q", token)
		}
	}
	for _, token := range []string{`std.extVar('claims')`, "username:", "display_name:"} {
		if !strings.Contains(mapper, token) {
			t.Fatalf("production Feishu mapper missing %q", token)
		}
	}
}

func readDeploymentArtifact(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{"..", ".."}, parts...)...)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.ReplaceAll(string(raw), "\r\n", "\n")
}
