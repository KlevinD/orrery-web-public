package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// Image generation settings are independent of chat defaults and Marketing grants.
// Credentials are references only; raw API keys are not part of this contract.
type SettingsImageGenerationDTO struct {
	Enabled   bool   `json:"enabled"`
	Protocol  string `json:"protocol"`
	BaseURL   string `json:"base_url"`
	Model     string `json:"model"`
	APIKeyRef string `json:"api_key_ref"`
}

type SettingsImageConnectionTestDTO struct {
	Status             string `json:"status"`
	Reason             string `json:"reason"`
	ModelAvailable     bool   `json:"model_available"`
	GenerationVerified bool   `json:"generation_verified"`
	HTTPStatus         int    `json:"http_status,omitempty"`
	LatencyMS          int64  `json:"latency_ms"`
}

func normalizeImageGenerationSettings(settings *SettingsImageGenerationDTO) {
	settings.Protocol = strings.TrimSpace(settings.Protocol)
	if settings.Protocol == "" {
		settings.Protocol = "openai-compatible"
	}
	settings.BaseURL = strings.TrimRight(strings.TrimSpace(settings.BaseURL), "/")
	settings.Model = strings.TrimSpace(settings.Model)
	settings.APIKeyRef = strings.TrimSpace(settings.APIKeyRef)
}

func validateImageGenerationSettings(settings SettingsImageGenerationDTO) (string, string, bool) {
	normalizeImageGenerationSettings(&settings)
	if settings.Protocol != "openai-compatible" {
		return "image_generation.protocol", "Unsupported image generation protocol", false
	}
	if settings.Enabled || settings.BaseURL != "" {
		if _, err := imageGenerationBaseURL(settings.BaseURL); err != nil {
			return "image_generation.base_url", "A public HTTPS gateway URL without credentials, query or fragment is required", false
		}
	}
	if settings.Enabled && settings.Model == "" || len(settings.Model) > 200 || !asciiPrintable(settings.Model) {
		return "image_generation.model", "A model name of at most 200 printable ASCII characters is required", false
	}
	if settings.Enabled || settings.APIKeyRef != "" {
		ref := strings.TrimPrefix(settings.APIKeyRef, "secret://")
		if ref == settings.APIKeyRef || ref == "" || len(ref) > 256 || strings.ContainsAny(ref, " \t\r\n?#") || !asciiPrintable(ref) {
			return "image_generation.api_key_ref", "A managed secret:// credential reference is required", false
		}
	}
	return "", "", true
}

// ValidateImageGenerationSettings applies the same credential and URL policy to deployment defaults.
func ValidateImageGenerationSettings(settings SettingsImageGenerationDTO) (string, string, bool) {
	return validateImageGenerationSettings(settings)
}

func imageGenerationBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || len(raw) > 2048 {
		return nil, errors.New("invalid image gateway URL")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return nil, errors.New("private image gateway address")
	}
	if ip, err := netip.ParseAddr(host); err == nil && !imageGatewayPublicIP(ip) {
		return nil, errors.New("private image gateway address")
	}
	return u, nil
}

func imageGatewayPublicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return false
	}
	for _, block := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96", "2002::/16"} {
		if netip.MustParsePrefix(block).Contains(ip) {
			return false
		}
	}
	return true
}

func imageConnectionClient() *http.Client {
	transport := &http.Transport{
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 8 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil || len(ips) == 0 {
				return nil, errors.New("image gateway DNS failed")
			}
			for _, ip := range ips {
				if !imageGatewayPublicIP(ip) {
					return nil, errors.New("private image gateway address")
				}
			}
			// Dial the verified IP, never resolve the hostname again after validation.
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		},
	}
	return &http.Client{Transport: transport, Timeout: 10 * time.Second}
}

// NewImageGenerationClient shares the catalog's DNS-pinned public HTTPS policy.
// Generation may take longer than a catalog lookup and must never follow redirects.
func NewImageGenerationClient() *http.Client {
	client := imageConnectionClient()
	client.Timeout = 3 * time.Minute
	client.Transport.(*http.Transport).ResponseHeaderTimeout = 3 * time.Minute
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client
}

func (r *Router) handleGetImageGenerationSettings(w http.ResponseWriter, req *http.Request) {
	settings, err := r.settings.GetSettings(req.Context())
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	normalizeImageGenerationSettings(&settings.ImageGeneration)
	writeJSON(w, http.StatusOK, settings.ImageGeneration)
}

func (r *Router) handlePutImageGenerationSettings(w http.ResponseWriter, req *http.Request) {
	var imageSettings *SettingsImageGenerationDTO
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&imageSettings); err != nil || imageSettings == nil {
		status, body := invalidRequest(correlationID(req), "body", "Invalid image generation settings")
		writeJSON(w, status, body)
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		status, body := invalidRequest(correlationID(req), "body", "A single settings object is required")
		writeJSON(w, status, body)
		return
	}
	normalizeImageGenerationSettings(imageSettings)
	if field, message, ok := validateImageGenerationSettings(*imageSettings); !ok {
		status, body := invalidRequest(correlationID(req), field, message)
		writeJSON(w, status, body)
		return
	}
	settings, err := r.settings.GetSettings(req.Context())
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	settings.ImageGeneration = *imageSettings
	if _, err := r.settings.SaveSettings(req.Context(), settings); err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, imageSettings)
}

func (r *Router) handleTestImageGenerationConnection(w http.ResponseWriter, req *http.Request) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1024))
	decoder.DisallowUnknownFields()
	var payload *struct{}
	if err := decoder.Decode(&payload); (err != nil && err != io.EOF) || (err == nil && payload == nil) {
		status, body := invalidRequest(correlationID(req), "body", "Only saved image settings can be checked")
		writeJSON(w, status, body)
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		status, body := invalidRequest(correlationID(req), "body", "A single empty object is required")
		writeJSON(w, status, body)
		return
	}
	settings, err := r.settings.GetSettings(req.Context())
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	configured := settings.ImageGeneration
	normalizeImageGenerationSettings(&configured)
	configured.Enabled = true // Checking a saved, disabled configuration does not enable it.
	if field, message, ok := validateImageGenerationSettings(configured); !ok {
		status, body := invalidRequest(correlationID(req), field, message)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, checkImageGenerationConnection(req.Context(), configured, r.settingsSecrets, nil))
}

func checkImageGenerationConnection(ctx context.Context, settings SettingsImageGenerationDTO, secrets SettingsSecretResolver, client *http.Client) (result SettingsImageConnectionTestDTO) {
	started := time.Now()
	defer func() { result.LatencyMS = time.Since(started).Milliseconds() }()
	result.Status, result.Reason = "failed", "credential_unavailable"
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if secrets == nil {
		return
	}
	token, err := secrets.ResolveSecret(ctx, settings.APIKeyRef)
	if err != nil || strings.TrimSpace(token) == "" || !asciiPrintable(token) {
		return
	}
	base, err := imageGenerationBaseURL(settings.BaseURL)
	result.Reason = "connection_failed"
	if err != nil {
		return
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/models"
	base.RawPath = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("Accept", "application/json")
	if client == nil {
		client = imageConnectionClient()
		defer client.CloseIdleConnections()
	}
	copy := *client
	copy.Jar = nil
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := copy.Do(req)
	if err != nil {
		return
	}
	defer response.Body.Close()
	result.HTTPStatus = response.StatusCode
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		result.Reason = "authentication_failed"
		return
	}
	if response.StatusCode != http.StatusOK {
		result.Reason = "upstream_unavailable"
		return
	}
	result.Reason = "invalid_response"
	if response.ContentLength > 1<<20 {
		return
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return
	}
	var catalog struct {
		Data *[]struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &catalog) != nil || catalog.Data == nil {
		return
	}
	result.Status, result.Reason = "warning", "model_not_listed"
	for _, model := range *catalog.Data {
		if model.ID == settings.Model {
			result.Status, result.Reason, result.ModelAvailable = "ok", "model_listed", true
			break
		}
	}
	// A model catalog is not evidence that generation, storage or delivery works.
	return
}
