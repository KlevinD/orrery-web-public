package peopleaccount

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

var errKratosIdentityNotFound = errors.New("Kratos identity not found")

type KratosAdminConfig struct {
	AdminURL         string
	AdminToken       string
	SchemaID         string
	FeishuProviderID string
	HTTPClient       *http.Client
}

type KratosAdminClient struct {
	adminURL         *url.URL
	adminToken       string
	schemaID         string
	feishuProviderID string
	httpClient       *http.Client
}

func NewKratosAdminClient(cfg KratosAdminConfig) (*KratosAdminClient, error) {
	adminURL, err := url.Parse(strings.TrimSpace(cfg.AdminURL))
	if err != nil || (adminURL.Scheme != "http" && adminURL.Scheme != "https") || adminURL.Host == "" {
		return nil, fmt.Errorf("Kratos admin_url is invalid")
	}
	schemaID := strings.TrimSpace(cfg.SchemaID)
	if schemaID == "" {
		schemaID = "default"
	}
	providerID := strings.TrimSpace(cfg.FeishuProviderID)
	if providerID == "" {
		providerID = "feishu"
	}
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &KratosAdminClient{
		adminURL: adminURL, adminToken: strings.TrimSpace(cfg.AdminToken), schemaID: schemaID,
		feishuProviderID: providerID, httpClient: client,
	}, nil
}

func (c *KratosAdminClient) FindIdentityByUsername(ctx context.Context, username string) (KratosIdentity, bool, error) {
	endpoint := c.resolve("/admin/identities")
	query := endpoint.Query()
	query.Set("credentials_identifier", strings.TrimSpace(username))
	query.Add("include_credential", "password")
	query.Add("include_credential", "oidc")
	endpoint.RawQuery = query.Encode()
	var identities []kratosAdminIdentity
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, &identities, http.StatusOK); err != nil {
		return KratosIdentity{}, false, err
	}
	if len(identities) == 0 {
		return KratosIdentity{}, false, nil
	}
	if len(identities) != 1 {
		return KratosIdentity{}, false, ErrProjectionConflict
	}
	identity, err := identities[0].projectionIdentity(c.feishuProviderID)
	return identity, err == nil, err
}

func (c *KratosAdminClient) GetIdentity(ctx context.Context, identityID string) (KratosIdentity, bool, error) {
	endpoint := c.resolve("/admin/identities/" + url.PathEscape(strings.TrimSpace(identityID)))
	query := endpoint.Query()
	query.Add("include_credential", "password")
	query.Add("include_credential", "oidc")
	endpoint.RawQuery = query.Encode()
	var identity kratosAdminIdentity
	err := c.doJSON(ctx, http.MethodGet, endpoint, nil, &identity, http.StatusOK)
	if errors.Is(err, errKratosIdentityNotFound) {
		return KratosIdentity{}, false, nil
	}
	if err != nil {
		return KratosIdentity{}, false, err
	}
	projected, err := identity.projectionIdentity(c.feishuProviderID)
	return projected, err == nil, err
}

func (c *KratosAdminClient) CreateIdentity(ctx context.Context, projection Projection, credential KratosCredential) (KratosIdentity, error) {
	body := c.identityBody(projection, nil)
	body["credentials"] = credentialBody(c.feishuProviderID, projection.FeishuSubject, credential)
	metadata := body["metadata_admin"].(map[string]any)
	metadata["password_change_required"] = credential.PasswordChangeRequired
	var created kratosAdminIdentity
	if err := c.doJSON(ctx, http.MethodPost, c.resolve("/admin/identities"), body, &created, http.StatusCreated); err != nil {
		return KratosIdentity{}, err
	}
	return created.projectionIdentity(c.feishuProviderID)
}

func (c *KratosAdminClient) UpdateIdentity(ctx context.Context, current KratosIdentity, projection Projection) (KratosIdentity, error) {
	body := c.identityBody(projection, current.MetadataAdmin)
	if subject := strings.TrimSpace(projection.FeishuSubject); subject != "" {
		credentials, err := c.credentialsWithOIDCRelation(current, subject)
		if err != nil {
			return KratosIdentity{}, err
		}
		if credentials != nil {
			body["credentials"] = credentials
		}
	}
	var updated kratosAdminIdentity
	endpoint := c.resolve("/admin/identities/" + url.PathEscape(strings.TrimSpace(current.ID)))
	if err := c.doJSON(ctx, http.MethodPut, endpoint, body, &updated, http.StatusOK); err != nil {
		return KratosIdentity{}, err
	}
	return updated.projectionIdentity(c.feishuProviderID)
}

func (c *KratosAdminClient) RevokeIdentitySessions(ctx context.Context, identityID string) error {
	endpoint := c.resolve("/admin/identities/" + url.PathEscape(strings.TrimSpace(identityID)) + "/sessions")
	return c.doJSON(ctx, http.MethodDelete, endpoint, nil, nil, http.StatusNoContent)
}

func (c *KratosAdminClient) InitialPasswordChangeRequired(ctx context.Context, identityID string) (bool, error) {
	current, err := c.getAdminIdentity(ctx, identityID)
	if err != nil {
		return false, err
	}
	if strings.ToLower(strings.TrimSpace(current.State)) != "active" {
		return false, ErrProjectionConflict
	}
	return metadataBool(current.MetadataAdmin, "password_change_required"), nil
}

func (c *KratosAdminClient) ChangeInitialPassword(ctx context.Context, identityID, newPassword string) error {
	identityID = strings.TrimSpace(identityID)
	current, err := c.getAdminIdentity(ctx, identityID)
	if err != nil {
		return err
	}
	if strings.ToLower(strings.TrimSpace(current.State)) != "active" || !metadataBool(current.MetadataAdmin, "password_change_required") {
		return ErrProjectionConflict
	}
	body := map[string]any{
		"schema_id":      firstNonEmpty(strings.TrimSpace(current.SchemaID), c.schemaID),
		"state":          current.State,
		"traits":         current.Traits,
		"metadata_admin": current.MetadataAdmin,
		"credentials": map[string]any{
			"password": map[string]any{"config": map[string]any{"password": newPassword}},
		},
	}
	updateEndpoint := c.resolve("/admin/identities/" + url.PathEscape(identityID))
	if err := c.doJSON(ctx, http.MethodPut, updateEndpoint, body, nil, http.StatusOK); err != nil {
		return err
	}
	if err := c.RevokeIdentitySessions(ctx, identityID); err != nil {
		return err
	}
	patch := []map[string]string{{"op": "remove", "path": "/metadata_admin/password_change_required"}}
	return c.doJSON(ctx, http.MethodPatch, updateEndpoint, patch, nil, http.StatusOK)
}

func (c *KratosAdminClient) getAdminIdentity(ctx context.Context, identityID string) (kratosAdminIdentity, error) {
	identityID = strings.TrimSpace(identityID)
	if identityID == "" {
		return kratosAdminIdentity{}, ErrProjectionConflict
	}
	endpoint := c.resolve("/admin/identities/" + url.PathEscape(identityID))
	var current kratosAdminIdentity
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, &current, http.StatusOK); err != nil {
		return kratosAdminIdentity{}, err
	}
	if strings.TrimSpace(current.ID) != identityID {
		return kratosAdminIdentity{}, ErrProjectionConflict
	}
	return current, nil
}

func (c *KratosAdminClient) identityBody(projection Projection, existingMetadata map[string]any) map[string]any {
	traits := map[string]any{
		"username": projection.Username, "display_name": projection.DisplayName,
		"person_id": projection.PersonID, "person_revision": projection.Revision,
	}
	if email := strings.TrimSpace(projection.EnterpriseEmail); email != "" {
		traits["email"] = email
	}
	metadata := cloneAnyMap(existingMetadata)
	metadata["person_id"] = projection.PersonID
	metadata["person_revision"] = projection.Revision
	state := "inactive"
	if projection.Enabled {
		state = "active"
	}
	return map[string]any{
		"schema_id": c.schemaID, "state": state, "traits": traits, "metadata_admin": metadata,
	}
}

func credentialBody(providerID, subject string, credential KratosCredential) map[string]any {
	credentials := map[string]any{
		"password": map[string]any{"config": map[string]any{"hashed_password": credential.HashedPassword}},
	}
	if subject = strings.TrimSpace(subject); subject != "" {
		credentials["oidc"] = map[string]any{
			"config": map[string]any{"providers": []map[string]string{{"provider": providerID, "subject": subject}}},
		}
	}
	return credentials
}

func (c *KratosAdminClient) credentialsWithOIDCRelation(current KratosIdentity, subject string) (map[string]any, error) {
	if current.unsupportedCredentials {
		return nil, ErrProjectionConflict
	}
	providers := cloneProviderMaps(current.oidcProviders)
	for _, provider := range providers {
		if metadataString(provider, "provider") != c.feishuProviderID {
			continue
		}
		if metadataString(provider, "subject") == subject {
			return nil, nil
		}
		return nil, ErrProjectionConflict
	}
	if current.passwordCredentialPresent && strings.TrimSpace(current.hashedPassword) == "" {
		return nil, ErrProjectionConflict
	}
	providers = append(providers, map[string]any{"provider": c.feishuProviderID, "subject": subject})
	credentials := map[string]any{
		"oidc": map[string]any{"config": map[string]any{"providers": providers}},
	}
	if current.passwordCredentialPresent {
		credentials["password"] = map[string]any{
			"config": map[string]any{"hashed_password": current.hashedPassword},
		}
	}
	return credentials, nil
}

func (c *KratosAdminClient) doJSON(ctx context.Context, method string, endpoint *url.URL, requestBody any, responseBody any, expectedStatus int) error {
	if c == nil || c.adminURL == nil || c.httpClient == nil {
		return fmt.Errorf("Kratos admin client is not configured")
	}
	var body bytes.Buffer
	if requestBody != nil {
		if err := json.NewEncoder(&body).Encode(requestBody); err != nil {
			return fmt.Errorf("encode Kratos admin request: %w", err)
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), &body)
	if err != nil {
		return fmt.Errorf("create Kratos admin request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		contentType := "application/json"
		if method == http.MethodPatch {
			contentType = "application/json-patch+json"
		}
		req.Header.Set("Content-Type", contentType)
	}
	if c.adminToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.adminToken)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("Kratos admin request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != expectedStatus {
		if method == http.MethodGet && resp.StatusCode == http.StatusNotFound {
			return errKratosIdentityNotFound
		}
		return fmt.Errorf("Kratos admin request returned status %d", resp.StatusCode)
	}
	if responseBody != nil {
		if err := json.NewDecoder(resp.Body).Decode(responseBody); err != nil {
			return fmt.Errorf("decode Kratos admin response: %w", err)
		}
	}
	return nil
}

func (c *KratosAdminClient) resolve(path string) *url.URL {
	resolved := *c.adminURL
	resolved.Path = strings.TrimRight(resolved.Path, "/") + "/" + strings.TrimLeft(path, "/")
	resolved.RawQuery = ""
	return &resolved
}

type kratosAdminIdentity struct {
	ID            string         `json:"id"`
	SchemaID      string         `json:"schema_id"`
	State         string         `json:"state"`
	Traits        map[string]any `json:"traits"`
	MetadataAdmin map[string]any `json:"metadata_admin"`
	Credentials   map[string]struct {
		Config json.RawMessage `json:"config"`
	} `json:"credentials"`
}

func metadataBool(metadata map[string]any, key string) bool {
	value, ok := metadata[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func (i kratosAdminIdentity) projectionIdentity(feishuProviderID string) (KratosIdentity, error) {
	identity := KratosIdentity{
		ID: strings.TrimSpace(i.ID), PersonID: metadataString(i.MetadataAdmin, "person_id"),
		State: strings.TrimSpace(i.State), MetadataAdmin: cloneAnyMap(i.MetadataAdmin),
	}
	for credentialType, credential := range i.Credentials {
		switch credentialType {
		case "password":
			identity.passwordCredentialPresent = true
			var config struct {
				HashedPassword string `json:"hashed_password"`
			}
			if len(credential.Config) == 0 || string(credential.Config) == "null" {
				continue
			}
			if err := json.Unmarshal(credential.Config, &config); err != nil {
				return KratosIdentity{}, fmt.Errorf("decode Kratos password credential: %w", err)
			}
			identity.hashedPassword = strings.TrimSpace(config.HashedPassword)
		case "oidc":
			var config struct {
				Providers []map[string]any `json:"providers"`
			}
			if len(credential.Config) == 0 || string(credential.Config) == "null" {
				continue
			}
			if err := json.Unmarshal(credential.Config, &config); err != nil {
				return KratosIdentity{}, fmt.Errorf("decode Kratos OIDC credential: %w", err)
			}
			identity.oidcProviders = cloneProviderMaps(config.Providers)
			for _, provider := range config.Providers {
				if metadataString(provider, "provider") == strings.TrimSpace(feishuProviderID) {
					identity.FeishuSubject = metadataString(provider, "subject")
					break
				}
			}
		default:
			identity.unsupportedCredentials = true
		}
	}
	return identity, nil
}

func metadataString(metadata map[string]any, key string) string {
	value, _ := metadata[key].(string)
	return strings.TrimSpace(value)
}

func cloneAnyMap(source map[string]any) map[string]any {
	cloned := make(map[string]any, len(source)+2)
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func cloneProviderMaps(source []map[string]any) []map[string]any {
	cloned := make([]map[string]any, 0, len(source))
	for _, provider := range source {
		cloned = append(cloned, cloneAnyMap(provider))
	}
	return cloned
}
