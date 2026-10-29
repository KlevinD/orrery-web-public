package httptransport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/runtimeapproval"
)

const (
	claudeNativeDefaultApprovalTimeoutMs = 600000
	claudeNativeDefaultPermissionMode    = "default"
	piDefaultApprovalMode                = "on-request"
	piDefaultApprovalTimeoutMs           = 600000
	piDefaultPromptAckTimeoutMs          = 60000
)

type SettingsDTO struct {
	PlatformName                 string                        `json:"platform_name"`
	OrganizationName             string                        `json:"organization_name"`
	Language                     string                        `json:"language"`
	AutoCreateAgentProfile       bool                          `json:"auto_create_agent_profile"`
	AutoLinkEvidence             bool                          `json:"auto_link_evidence"`
	HILDefaultEnabled            bool                          `json:"hil_default_enabled"`
	DiscussionAgentPairLoopLimit int                           `json:"discussion_agent_pair_loop_limit,omitempty"`
	WorkspaceRoot                string                        `json:"workspace_root,omitempty"`
	WorkspaceAllowlist           []string                      `json:"workspace_allowlist,omitempty"`
	Memory                       SettingsMemoryDTO             `json:"memory"`
	Knowledge                    SettingsKnowledgeDTO          `json:"knowledge"`
	RuntimeProfiles              []SettingsRuntimeProfileDTO   `json:"runtime_profiles,omitempty"`
	LLMProfiles                  []SettingsLLMProfileDTO       `json:"llm_profiles,omitempty"`
	ImageGeneration              SettingsImageGenerationDTO    `json:"image_generation"`
	PromptBlocks                 []SettingsPromptBlockDTO      `json:"prompt_blocks"`
	NotificationRules            []SettingsNotificationRuleDTO `json:"notification_rules"`
	RuntimeApprovals             runtimeapproval.Policy        `json:"runtime_approvals"`
}

type SettingsMemoryDTO struct {
	Enabled bool `json:"enabled"`
}

type SettingsKnowledgeDTO struct {
	Enabled         bool                          `json:"enabled"`
	DefaultProvider string                        `json:"default_provider"`
	PageIndex       SettingsPageIndexKnowledgeDTO `json:"pageindex"`
	Local           SettingsLocalKnowledgeDTO     `json:"local"`
}

type SettingsPageIndexKnowledgeDTO struct {
	APIURL          string `json:"api_url"`
	APIKey          string `json:"api_key,omitempty"`
	APIKeyRef       string `json:"api_key_ref,omitempty"`
	DefaultFolderID string `json:"default_folder_id,omitempty"`
	TimeoutSeconds  int    `json:"timeout_seconds,omitempty"`
}

type SettingsLocalKnowledgeDTO struct {
	Enabled bool `json:"enabled"`
}

type SettingsPageIndexConnectionTestRequestDTO struct {
	PageIndex SettingsPageIndexKnowledgeDTO `json:"pageindex"`
}

type SettingsPageIndexConnectionTestResultDTO struct {
	Status     string `json:"status"`
	APIURL     string `json:"api_url"`
	Endpoint   string `json:"endpoint"`
	HTTPStatus int    `json:"http_status,omitempty"`
	LatencyMS  int64  `json:"latency_ms"`
	Message    string `json:"message,omitempty"`
}

type SettingsRuntimeProfileDTO struct {
	ProfileID        string         `json:"profile_id"`
	DisplayName      string         `json:"display_name"`
	RuntimeKind      string         `json:"runtime_kind"`
	RuntimeConfigRef string         `json:"runtime_config_ref,omitempty"`
	RuntimeConfig    map[string]any `json:"runtime_config,omitempty"`
	Enabled          bool           `json:"enabled"`
}

type SettingsLLMProfileDTO struct {
	ProfileID     string  `json:"profile_id"`
	DisplayName   string  `json:"display_name"`
	Provider      string  `json:"provider"`
	BaseURL       string  `json:"base_url,omitempty"`
	APIKey        string  `json:"api_key,omitempty"`
	APIKeyRef     string  `json:"api_key_ref,omitempty"`
	Model         string  `json:"model,omitempty"`
	Temperature   float64 `json:"temperature,omitempty"`
	ContextLength int     `json:"context_length,omitempty"`
	Enabled       bool    `json:"enabled"`
	GlobalDefault bool    `json:"global_default"`
}

type SettingsNotificationRuleDTO struct {
	RuleID         string `json:"rule_id"`
	LabelKey       string `json:"label_key"`
	DescriptionKey string `json:"description_key"`
	Enabled        bool   `json:"enabled"`
}

type SettingsPromptBlockDTO struct {
	BlockID      string `json:"block_id"`
	Code         string `json:"code,omitempty"`
	Title        string `json:"title"`
	Description  string `json:"description,omitempty"`
	Role         string `json:"role"`
	Content      string `json:"content,omitempty"`
	Placeholder  string `json:"placeholder,omitempty"`
	Dynamic      bool   `json:"dynamic"`
	Editable     bool   `json:"editable"`
	Removable    bool   `json:"removable,omitempty"`
	VariableOnly bool   `json:"variable_only,omitempty"`
	Enabled      bool   `json:"enabled"`
}

type SettingsService interface {
	GetSettings(ctx context.Context) (SettingsDTO, error)
	SaveSettings(ctx context.Context, settings SettingsDTO) (SettingsDTO, error)
}

type SettingsSecretResolver interface {
	ResolveSecret(ctx context.Context, ref string) (string, error)
}

type memorySettingsService struct {
	mu       sync.RWMutex
	settings SettingsDTO
}

func newMemorySettingsService(settings SettingsDTO) *memorySettingsService {
	return &memorySettingsService{settings: cloneSettingsDTO(settings)}
}

func (s *memorySettingsService) GetSettings(ctx context.Context) (SettingsDTO, error) {
	if err := ctx.Err(); err != nil {
		return SettingsDTO{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneSettingsDTO(s.settings), nil
}

func (s *memorySettingsService) SaveSettings(ctx context.Context, settings SettingsDTO) (SettingsDTO, error) {
	if err := ctx.Err(); err != nil {
		return SettingsDTO{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings = cloneSettingsDTO(settings)
	return cloneSettingsDTO(settings), nil
}

func DefaultSettings() SettingsDTO {
	return SettingsDTO{
		PlatformName:                 "Orrery Mission Control",
		OrganizationName:             "Example Technology",
		Language:                     "zh-CN",
		AutoCreateAgentProfile:       true,
		AutoLinkEvidence:             true,
		HILDefaultEnabled:            true,
		DiscussionAgentPairLoopLimit: 8,
		Memory: SettingsMemoryDTO{
			Enabled: false,
		},
		Knowledge: SettingsKnowledgeDTO{
			Enabled:         true,
			DefaultProvider: "pageindex",
			PageIndex: SettingsPageIndexKnowledgeDTO{
				APIURL:         "https://api.pageindex.ai",
				TimeoutSeconds: 30,
			},
			Local: SettingsLocalKnowledgeDTO{Enabled: true},
		},
		RuntimeProfiles:  []SettingsRuntimeProfileDTO{defaultPiRuntimeProfile()},
		RuntimeApprovals: runtimeapproval.DefaultPolicy(),
		PromptBlocks:     promptBlockDTOsFromRuntime(agentcore.DefaultRuntimePromptBlocks()),
		NotificationRules: []SettingsNotificationRuleDTO{
			{RuleID: "mission_status", LabelKey: "settings.notification.missionStatus.label", DescriptionKey: "settings.notification.missionStatus.desc", Enabled: true},
			{RuleID: "approval", LabelKey: "settings.notification.approval.label", DescriptionKey: "settings.notification.approval.desc", Enabled: true},
			{RuleID: "blocked", LabelKey: "settings.notification.blocked.label", DescriptionKey: "settings.notification.blocked.desc", Enabled: true},
			{RuleID: "mention", LabelKey: "settings.notification.mention.label", DescriptionKey: "settings.notification.mention.desc", Enabled: true},
			{RuleID: "qa", LabelKey: "settings.notification.qa.label", DescriptionKey: "settings.notification.qa.desc", Enabled: true},
		},
	}
}

func defaultPiRuntimeProfile() SettingsRuntimeProfileDTO {
	return SettingsRuntimeProfileDTO{
		ProfileID:        "runtime-pi-local",
		DisplayName:      "PI",
		RuntimeKind:      "pi",
		RuntimeConfigRef: "runtime-pi-local",
		RuntimeConfig: map[string]any{
			"mode":                  "local",
			"executable":            "pi",
			"approval_mode":         piDefaultApprovalMode,
			"approval_timeout_ms":   piDefaultApprovalTimeoutMs,
			"prompt_ack_timeout_ms": piDefaultPromptAckTimeoutMs,
		},
		Enabled: true,
	}
}

func defaultSettings() SettingsDTO {
	return DefaultSettings()
}

func settingsWithScalarDefaults() SettingsDTO {
	defaults := defaultSettings()
	return SettingsDTO{
		PlatformName:                 defaults.PlatformName,
		OrganizationName:             defaults.OrganizationName,
		Language:                     defaults.Language,
		AutoCreateAgentProfile:       defaults.AutoCreateAgentProfile,
		AutoLinkEvidence:             defaults.AutoLinkEvidence,
		HILDefaultEnabled:            defaults.HILDefaultEnabled,
		DiscussionAgentPairLoopLimit: defaults.DiscussionAgentPairLoopLimit,
		WorkspaceRoot:                defaults.WorkspaceRoot,
		WorkspaceAllowlist:           append([]string(nil), defaults.WorkspaceAllowlist...),
		Memory:                       defaults.Memory,
		Knowledge:                    defaults.Knowledge,
		RuntimeProfiles:              cloneRuntimeProfileDTOs(defaults.RuntimeProfiles),
		LLMProfiles:                  append([]SettingsLLMProfileDTO(nil), defaults.LLMProfiles...),
		PromptBlocks:                 clonePromptBlockDTOs(defaults.PromptBlocks),
		RuntimeApprovals:             runtimeapproval.Clone(defaults.RuntimeApprovals),
	}
}

func (r *Router) handleGetSettings(w http.ResponseWriter, req *http.Request) {
	settings, err := r.settings.GetSettings(req.Context())
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	normalizeSettings(&settings)
	writeJSON(w, http.StatusOK, settingsForResponse(settings))
}

func (r *Router) handlePutSettings(w http.ResponseWriter, req *http.Request) {
	settings, err := r.settings.GetSettings(req.Context())
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	settings.Knowledge.PageIndex.APIKey = ""
	for i := range settings.LLMProfiles {
		settings.LLMProfiles[i].APIKey = ""
	}
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&settings); err != nil {
		status, body := invalidRequest(correlationID(req), "body", err.Error())
		writeJSON(w, status, body)
		return
	}
	normalizeSettings(&settings)
	if field, message, ok := validateSettings(settings); !ok {
		status, body := invalidRequest(correlationID(req), field, message)
		writeJSON(w, status, body)
		return
	}
	if settings.HILDefaultEnabled && runtimeapproval.ContainsNeverPolicy(settings.RuntimeApprovals) {
		viewer, viewerErr := ViewerFromRequest(req)
		if viewerErr != nil || !viewerHasScope(viewer.PermissionScope, permissionApprovalsManage, permissionApprovalsManage) {
			writeJSON(w, http.StatusForbidden, ErrorResponse{
				Code:          ErrorForbidden,
				Message:       "approvals.manage is required for always-allow runtime approval policy",
				CorrelationID: correlationID(req),
			})
			return
		}
	}
	saved, err := r.settings.SaveSettings(req.Context(), settings)
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, settingsForResponse(saved))
}

func (r *Router) handleTestPageIndexConnection(w http.ResponseWriter, req *http.Request) {
	var body SettingsPageIndexConnectionTestRequestDTO
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		status, response := invalidRequest(correlationID(req), "body", err.Error())
		writeJSON(w, status, response)
		return
	}
	pageIndex := body.PageIndex
	if strings.TrimSpace(pageIndex.APIKey) == "" {
		settings, err := r.settings.GetSettings(req.Context())
		if err != nil {
			status, response := MapError(err, correlationID(req))
			writeJSON(w, status, response)
			return
		}
		sameURL := normalizeURL(pageIndex.APIURL) == normalizeURL(settings.Knowledge.PageIndex.APIURL)
		if sameURL && strings.TrimSpace(pageIndex.APIKeyRef) == strings.TrimSpace(settings.Knowledge.PageIndex.APIKeyRef) {
			pageIndex.APIKey = settings.Knowledge.PageIndex.APIKey
		}
		if sameURL && strings.TrimSpace(pageIndex.APIKey) == "" && strings.TrimSpace(pageIndex.APIKeyRef) != "" {
			if r.settingsSecrets == nil {
				writeJSON(w, http.StatusOK, failedPageIndexConnectionTest(pageIndex.APIURL, "PageIndex secret resolver is not configured"))
				return
			}
			resolved, resolveErr := r.settingsSecrets.ResolveSecret(req.Context(), pageIndex.APIKeyRef)
			if resolveErr != nil || strings.TrimSpace(resolved) == "" {
				writeJSON(w, http.StatusOK, failedPageIndexConnectionTest(pageIndex.APIURL, "Could not resolve PageIndex API key ref"))
				return
			}
			pageIndex.APIKey = resolved
		}
	}
	normalizePageIndexKnowledgeSettings(&pageIndex)
	if field, message, ok := validatePageIndexKnowledgeSettings(pageIndex); !ok {
		status, response := invalidRequest(correlationID(req), field, message)
		writeJSON(w, status, response)
		return
	}
	result := testPageIndexConnection(req.Context(), pageIndex)
	writeJSON(w, http.StatusOK, result)
}

func failedPageIndexConnectionTest(apiURL string, message string) SettingsPageIndexConnectionTestResultDTO {
	return SettingsPageIndexConnectionTestResultDTO{
		Status:  "failed",
		APIURL:  normalizeURL(apiURL),
		Message: message,
	}
}

func testPageIndexConnection(ctx context.Context, pageIndex SettingsPageIndexKnowledgeDTO) SettingsPageIndexConnectionTestResultDTO {
	apiURL := normalizeURL(pageIndex.APIURL)
	endpoint, err := pageIndexHealthEndpoint(apiURL)
	if err != nil {
		return SettingsPageIndexConnectionTestResultDTO{
			Status:  "failed",
			APIURL:  apiURL,
			Message: err.Error(),
		}
	}
	timeout := time.Duration(pageIndex.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = time.Duration(defaultSettings().Knowledge.PageIndex.TimeoutSeconds) * time.Second
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return SettingsPageIndexConnectionTestResultDTO{
			Status:   "failed",
			APIURL:   apiURL,
			Endpoint: endpoint,
			Message:  err.Error(),
		}
	}
	if strings.TrimSpace(pageIndex.APIKey) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(pageIndex.APIKey))
	}
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	latencyMS := time.Since(start).Milliseconds()
	if err != nil {
		return SettingsPageIndexConnectionTestResultDTO{
			Status:    "failed",
			APIURL:    apiURL,
			Endpoint:  endpoint,
			LatencyMS: latencyMS,
			Message:   err.Error(),
		}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	status := "ok"
	message := "PageIndex server is reachable."
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		status = "failed"
		message = "PageIndex rejected the configured API key."
	} else if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status = "warning"
		message = fmt.Sprintf("PageIndex server responded with HTTP %d.", resp.StatusCode)
	}
	return SettingsPageIndexConnectionTestResultDTO{
		Status:     status,
		APIURL:     apiURL,
		Endpoint:   endpoint,
		HTTPStatus: resp.StatusCode,
		LatencyMS:  latencyMS,
		Message:    message,
	}
}

func pageIndexHealthEndpoint(apiURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(apiURL, "/"))
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("PageIndex API URL must include scheme and host")
	}
	parsed.Path = path.Join(parsed.Path, "/healthz")
	return parsed.String(), nil
}

func normalizeSettings(settings *SettingsDTO) {
	if settings == nil {
		return
	}
	defaults := defaultSettings()
	if settings.NotificationRules == nil {
		settings.NotificationRules = append([]SettingsNotificationRuleDTO(nil), defaults.NotificationRules...)
	}
	if len(settings.PromptBlocks) == 0 {
		settings.PromptBlocks = clonePromptBlockDTOs(defaults.PromptBlocks)
	}
	normalizePromptBlocks(settings)
	settings.WorkspaceRoot = normalizeWorkspaceRoot(settings.WorkspaceRoot)
	settings.WorkspaceAllowlist = normalizeWorkspaceAllowlist(settings.WorkspaceAllowlist)
	normalizeMemorySettings(settings)
	normalizeKnowledgeSettings(settings)
	normalizeImageGenerationSettings(&settings.ImageGeneration)
	normalizeRuntimeSettings(settings)
	settings.RuntimeApprovals = runtimeapproval.Normalize(settings.RuntimeApprovals)
	if !settings.HILDefaultEnabled {
		settings.RuntimeApprovals = runtimeapproval.WithoutHumanReview(settings.RuntimeApprovals)
		for index := range settings.RuntimeProfiles {
			profile := &settings.RuntimeProfiles[index]
			if strings.EqualFold(strings.TrimSpace(profile.RuntimeKind), "pi") {
				profile.RuntimeConfig["approval_mode"] = "never"
			}
			if strings.EqualFold(strings.TrimSpace(profile.RuntimeKind), "claude") &&
				strings.EqualFold(strings.TrimSpace(runtimeConfigString(profile.RuntimeConfig, "mode")), "native") {
				profile.RuntimeConfig["permission_mode"] = "bypassPermissions"
			}
		}
	}
	for i := range settings.LLMProfiles {
		profile := &settings.LLMProfiles[i]
		profile.ProfileID = strings.TrimSpace(profile.ProfileID)
		profile.DisplayName = strings.TrimSpace(profile.DisplayName)
		profile.Provider = strings.TrimSpace(profile.Provider)
		profile.BaseURL = strings.TrimSpace(profile.BaseURL)
		profile.APIKey = strings.TrimSpace(profile.APIKey)
		profile.APIKeyRef = strings.TrimSpace(profile.APIKeyRef)
		profile.Model = strings.TrimSpace(profile.Model)
	}
	defaultIndex := -1
	for i, profile := range settings.LLMProfiles {
		if profile.Enabled && profile.GlobalDefault {
			defaultIndex = i
		}
	}
	for i := range settings.LLMProfiles {
		settings.LLMProfiles[i].GlobalDefault = i == defaultIndex
	}
}

func NormalizeSettings(settings *SettingsDTO) {
	normalizeSettings(settings)
}

func NormalizeRuntimeProfiles(settings *SettingsDTO) {
	normalizeRuntimeSettings(settings)
}

func normalizeKnowledgeSettings(settings *SettingsDTO) {
	if settings == nil {
		return
	}
	defaults := defaultSettings().Knowledge
	if isEmptyKnowledgeSettings(settings.Knowledge, defaults) {
		settings.Knowledge = defaults
		return
	}
	settings.Knowledge.DefaultProvider = strings.TrimSpace(settings.Knowledge.DefaultProvider)
	if settings.Knowledge.DefaultProvider == "" {
		settings.Knowledge.DefaultProvider = defaults.DefaultProvider
	}
	normalizePageIndexKnowledgeSettings(&settings.Knowledge.PageIndex)
	if settings.Knowledge.PageIndex.APIURL == "" {
		settings.Knowledge.PageIndex.APIURL = defaults.PageIndex.APIURL
	}
	if settings.Knowledge.PageIndex.TimeoutSeconds == 0 {
		settings.Knowledge.PageIndex.TimeoutSeconds = defaults.PageIndex.TimeoutSeconds
	}
	if !settings.Knowledge.Local.Enabled && settings.Knowledge.DefaultProvider == "local" {
		settings.Knowledge.Local.Enabled = true
	}
}

func normalizeMemorySettings(settings *SettingsDTO) {
	if settings == nil {
		return
	}
	// bool zero value is a valid explicit disabled state. No migration defaulting here.
}

func isEmptyKnowledgeSettings(settings SettingsKnowledgeDTO, defaults SettingsKnowledgeDTO) bool {
	return !settings.Enabled &&
		strings.TrimSpace(settings.DefaultProvider) == "" &&
		strings.TrimSpace(settings.PageIndex.APIURL) == "" &&
		strings.TrimSpace(settings.PageIndex.APIKey) == "" &&
		strings.TrimSpace(settings.PageIndex.APIKeyRef) == "" &&
		strings.TrimSpace(settings.PageIndex.DefaultFolderID) == "" &&
		(settings.PageIndex.TimeoutSeconds == 0 || settings.PageIndex.TimeoutSeconds == defaults.PageIndex.TimeoutSeconds) &&
		!settings.Local.Enabled
}

func normalizePageIndexKnowledgeSettings(pageIndex *SettingsPageIndexKnowledgeDTO) {
	if pageIndex == nil {
		return
	}
	pageIndex.APIURL = normalizeURL(pageIndex.APIURL)
	pageIndex.APIKey = strings.TrimSpace(pageIndex.APIKey)
	pageIndex.APIKeyRef = strings.TrimSpace(pageIndex.APIKeyRef)
	pageIndex.DefaultFolderID = strings.TrimSpace(pageIndex.DefaultFolderID)
	if pageIndex.TimeoutSeconds == 0 {
		pageIndex.TimeoutSeconds = defaultSettings().Knowledge.PageIndex.TimeoutSeconds
	}
}

func normalizeRuntimeSettings(settings *SettingsDTO) {
	if settings == nil {
		return
	}
	if settings.RuntimeProfiles == nil {
		settings.RuntimeProfiles = []SettingsRuntimeProfileDTO{defaultPiRuntimeProfile()}
	}
	seen := map[string]int{}
	for i := range settings.RuntimeProfiles {
		profile := &settings.RuntimeProfiles[i]
		profile.ProfileID = strings.TrimSpace(profile.ProfileID)
		profile.DisplayName = strings.TrimSpace(profile.DisplayName)
		profile.RuntimeKind = strings.TrimSpace(profile.RuntimeKind)
		profile.RuntimeConfigRef = strings.TrimSpace(profile.RuntimeConfigRef)
		profile.RuntimeConfig = cloneRuntimeConfig(profile.RuntimeConfig)
		if profile.ProfileID == "" {
			profile.ProfileID = runtimeProfileID(profile.RuntimeKind, profile.RuntimeConfigRef, i)
		}
		if profile.DisplayName == "" {
			profile.DisplayName = runtimeDisplayName(profile.RuntimeKind)
		}
		normalizeRuntimeProfileConfig(profile)
		if count := seen[profile.ProfileID]; count > 0 {
			profile.ProfileID = profile.ProfileID + "-" + strconv.Itoa(count+1)
		}
		seen[profile.ProfileID]++
	}
}

func normalizeRuntimeProfileConfig(profile *SettingsRuntimeProfileDTO) {
	if profile == nil {
		return
	}
	if strings.EqualFold(strings.TrimSpace(profile.RuntimeKind), "pi") {
		normalizePiRuntimeProfileConfig(profile)
		return
	}
	if !strings.EqualFold(strings.TrimSpace(profile.RuntimeKind), "claude") {
		return
	}
	mode := strings.TrimSpace(runtimeConfigString(profile.RuntimeConfig, "mode"))
	if mode != "" && !strings.EqualFold(mode, "native") && !strings.EqualFold(mode, "agent_sdk") {
		return
	}
	if profile.RuntimeConfig == nil {
		profile.RuntimeConfig = map[string]any{}
	}
	profile.RuntimeConfig["mode"] = "native"
	if runtimeConfigString(profile.RuntimeConfig, "executable") == "" {
		if legacy := runtimeConfigString(profile.RuntimeConfig, "path_to_claude_code_executable"); legacy != "" {
			profile.RuntimeConfig["executable"] = legacy
		}
	}
	for _, key := range []string{"node_executable", "sidecar_entry", "path_to_claude_code_executable"} {
		delete(profile.RuntimeConfig, key)
	}
	if runtimeConfigInt64(profile.RuntimeConfig, "approval_timeout_ms") <= 0 {
		profile.RuntimeConfig["approval_timeout_ms"] = claudeNativeDefaultApprovalTimeoutMs
	}
	if runtimeConfigString(profile.RuntimeConfig, "permission_mode") == "" {
		profile.RuntimeConfig["permission_mode"] = claudeNativeDefaultPermissionMode
	}
}

func normalizePiRuntimeProfileConfig(profile *SettingsRuntimeProfileDTO) {
	if profile.RuntimeConfig == nil {
		profile.RuntimeConfig = map[string]any{}
	}
	if runtimeConfigString(profile.RuntimeConfig, "mode") == "" {
		profile.RuntimeConfig["mode"] = "local"
	}
	if runtimeConfigString(profile.RuntimeConfig, "approval_mode") == "" {
		profile.RuntimeConfig["approval_mode"] = piDefaultApprovalMode
	}
	if runtimeConfigInt64(profile.RuntimeConfig, "approval_timeout_ms") <= 0 {
		profile.RuntimeConfig["approval_timeout_ms"] = piDefaultApprovalTimeoutMs
	}
	if runtimeConfigInt64(profile.RuntimeConfig, "prompt_ack_timeout_ms") <= 0 {
		profile.RuntimeConfig["prompt_ack_timeout_ms"] = piDefaultPromptAckTimeoutMs
	}
}

func runtimeConfigString(config map[string]any, key string) string {
	if config == nil {
		return ""
	}
	value, ok := config[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func runtimeConfigInt64(config map[string]any, key string) int64 {
	if config == nil {
		return 0
	}
	switch value := config[key].(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	case json.Number:
		parsed, _ := value.Int64()
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		return parsed
	default:
		return 0
	}
}

func normalizePromptBlocks(settings *SettingsDTO) {
	if settings == nil {
		return
	}
	defaults := promptBlockDefaultMap()
	defaultCodeOwners := promptBlockDefaultCodeOwners(defaults)
	out := make([]SettingsPromptBlockDTO, 0, len(settings.PromptBlocks))
	appendMissingDefaults := false
	for i := range settings.PromptBlocks {
		block := settings.PromptBlocks[i]
		block.BlockID = strings.TrimSpace(block.BlockID)
		block.Code = strings.TrimSpace(block.Code)
		if block.BlockID == agentcore.PromptBlockAgentProfile {
			appendMissingDefaults = true
			for _, migrated := range legacyAgentProfileSettingsBlocks(block) {
				out = append(out, migrated)
			}
			continue
		}
		if isLegacyAgentVariablePromptBlock(block) {
			appendMissingDefaults = true
		}
		block.Title = strings.TrimSpace(block.Title)
		block.Description = strings.TrimSpace(block.Description)
		block.Role = normalizePromptBlockRole(block.Role)
		block.Content = strings.TrimSpace(block.Content)
		block.Placeholder = strings.TrimSpace(block.Placeholder)
		if block.Code == "" {
			block.Code = block.BlockID
		}
		if def, ok := defaults[block.BlockID]; ok {
			if owner := defaultCodeOwners[block.Code]; block.Code == "" || (owner != "" && owner != block.BlockID) {
				block.Code = def.Code
			}
			if block.Title == "" {
				block.Title = def.Title
			}
			if block.Description == "" {
				block.Description = def.Description
			}
			if block.Role == "" {
				block.Role = def.Role
			}
			if block.Placeholder == "" {
				block.Placeholder = def.Placeholder
			}
			block.Dynamic = def.Dynamic
			block.Editable = def.Editable
			block.Removable = def.Removable
			if block.Dynamic {
				block.Content = ""
			} else if block.Content == "" {
				block.Content = def.Content
			}
		} else {
			block.Dynamic = false
			block.Editable = true
			block.Removable = true
		}
		block.Placeholder = promptBlockVariableReference(block.Code)
		if block.Role == "" {
			block.Role = agentcore.RuntimePromptRoleUser
		}
		out = append(out, block)
	}
	if appendMissingDefaults {
		out = appendMissingDefaultPromptBlocks(out, defaults)
	}
	settings.PromptBlocks = promptBlockDTOsFromRuntime(agentcore.UpgradeRuntimePromptBehavior(runtimePromptBlocksFromSettingsRaw(out)))
}

func isLegacyAgentVariablePromptBlock(block SettingsPromptBlockDTO) bool {
	if !block.Dynamic || block.Editable {
		return false
	}
	switch strings.TrimSpace(block.BlockID) {
	case agentcore.PromptBlockAgentID,
		agentcore.PromptBlockAgentRealName,
		agentcore.PromptBlockAgentDisplayName,
		agentcore.PromptBlockAgentRoleName,
		agentcore.PromptBlockAgentKind,
		agentcore.PromptBlockAgentBackstory,
		agentcore.PromptBlockAgentResponsibilityBoundary,
		agentcore.PromptBlockAgentBehaviorBoundary:
		return true
	default:
		return false
	}
}

func appendMissingDefaultPromptBlocks(blocks []SettingsPromptBlockDTO, defaults map[string]SettingsPromptBlockDTO) []SettingsPromptBlockDTO {
	if len(blocks) == 0 {
		return blocks
	}
	for _, block := range blocks {
		if block.Removable {
			return blocks
		}
	}
	seen := map[string]bool{}
	for _, block := range blocks {
		seen[block.BlockID] = true
	}
	for _, id := range []string{agentcore.PromptBlockWorkspaceInfo} {
		if seen[id] {
			continue
		}
		if block, ok := defaults[id]; ok {
			blocks = append(blocks, block)
		}
	}
	return blocks
}

var runtimeProfileIDUnsafe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func runtimeProfileID(kind, configRef string, index int) string {
	base := strings.TrimSpace(configRef)
	if base == "" {
		base = "runtime-" + strings.TrimSpace(kind)
	}
	base = strings.Trim(runtimeProfileIDUnsafe.ReplaceAllString(base, "-"), "-")
	if base == "" || base == "runtime" {
		base = "runtime-local"
	}
	if index > 0 {
		return base + "-" + strconv.Itoa(index+1)
	}
	return base
}

func runtimeDisplayName(kind string) string {
	switch strings.TrimSpace(kind) {
	case "managed":
		return "Orrery AgentCore"
	case "codex":
		return "Codex"
	case "claude":
		return "Claude Code"
	case "cursor":
		return "Cursor"
	case "gemini":
		return "Gemini"
	case "openclaw":
		return "OpenClaw"
	case "hermes":
		return "Hermes"
	case "pi":
		return "PI"
	case "acp":
		return "ACP"
	default:
		if strings.TrimSpace(kind) == "" {
			return "Runtime"
		}
		return strings.TrimSpace(kind)
	}
}

func validateSettings(settings SettingsDTO) (string, string, bool) {
	if field, message, ok := validateImageGenerationSettings(settings.ImageGeneration); !ok {
		return field, message, false
	}
	if settings.WorkspaceRoot != "" && !filepath.IsAbs(settings.WorkspaceRoot) {
		return "workspace_root", "Workspace root must be an absolute path", false
	}
	for i, root := range settings.WorkspaceAllowlist {
		if root == "" {
			continue
		}
		field := "workspace_allowlist[" + strconv.Itoa(i) + "]"
		if !filepath.IsAbs(root) {
			return field, "Workspace allowlist entries must be absolute paths", false
		}
		if filepath.Clean(root) == string(filepath.Separator) {
			return field, "Workspace allowlist entries cannot be the filesystem root", false
		}
	}
	if !validKnowledgeProvider(settings.Knowledge.DefaultProvider) {
		return "knowledge.default_provider", "Knowledge default provider is not supported", false
	}
	if strings.TrimSpace(settings.Knowledge.PageIndex.APIKey) != "" {
		return "knowledge.pageindex.api_key", "Direct PageIndex API keys cannot be stored; use an API key ref", false
	}
	if field, message, ok := validatePageIndexKnowledgeSettings(settings.Knowledge.PageIndex); !ok {
		return field, message, false
	}
	if settings.DiscussionAgentPairLoopLimit < 1 || settings.DiscussionAgentPairLoopLimit > 50 {
		return "discussion_agent_pair_loop_limit", "discussion agent pair loop limit must be between 1 and 50", false
	}
	if field, message, ok := runtimeapproval.Validate(settings.RuntimeApprovals); !ok {
		return "runtime_approvals." + field, message, false
	}
	for i, profile := range settings.RuntimeProfiles {
		prefix := "runtime_profiles[" + strconv.Itoa(i) + "]"
		if strings.TrimSpace(profile.RuntimeKind) == "" {
			return prefix + ".runtime_kind", "Runtime kind is required", false
		}
		if !validSettingsRuntimeKind(profile.RuntimeKind) {
			return prefix + ".runtime_kind", "Runtime kind is not supported", false
		}
		if strings.TrimSpace(profile.ProfileID) == "" {
			return prefix + ".profile_id", "Runtime profile id is required", false
		}
		if strings.TrimSpace(profile.DisplayName) == "" {
			return prefix + ".display_name", "Runtime display name is required", false
		}
	}
	for i, profile := range settings.LLMProfiles {
		prefix := "llm_profiles[" + strconv.Itoa(i) + "]"
		if profile.BaseURL == "" {
			return prefix + ".base_url", "LLM base URL is required", false
		}
		if profile.APIKey != "" {
			return prefix + ".api_key", "Direct LLM API keys cannot be stored; use an API key ref", false
		}
		if profile.APIKeyRef == "" {
			return prefix + ".api_key_ref", "LLM API key ref is required", false
		}
	}
	seenPromptBlocks := map[string]bool{}
	seenPromptBlockCodes := map[string]bool{}
	for i, block := range settings.PromptBlocks {
		prefix := "prompt_blocks[" + strconv.Itoa(i) + "]"
		if strings.TrimSpace(block.BlockID) == "" {
			return prefix + ".block_id", "Prompt block id is required", false
		}
		if seenPromptBlocks[block.BlockID] {
			return prefix + ".block_id", "Prompt block id must be unique", false
		}
		seenPromptBlocks[block.BlockID] = true
		if strings.TrimSpace(block.Code) == "" {
			return prefix + ".code", "Prompt block code is required", false
		}
		if !validPromptBlockCode(block.Code) {
			return prefix + ".code", "Prompt block code must start with a letter and contain only letters, numbers, dot, dash, or underscore", false
		}
		if seenPromptBlockCodes[block.Code] {
			return prefix + ".code", "Prompt block code must be unique", false
		}
		seenPromptBlockCodes[block.Code] = true
		if strings.TrimSpace(block.Title) == "" {
			return prefix + ".title", "Prompt block title is required", false
		}
		switch strings.TrimSpace(block.Role) {
		case agentcore.RuntimePromptRoleSystem, agentcore.RuntimePromptRoleUser, agentcore.RuntimePromptRoleAssistant:
		default:
			return prefix + ".role", "Prompt block role is not supported", false
		}
		if !block.Dynamic && block.Editable && strings.TrimSpace(block.Content) == "" {
			return prefix + ".content", "Editable prompt block content is required", false
		}
	}
	return "", "", true
}

func validatePageIndexKnowledgeSettings(pageIndex SettingsPageIndexKnowledgeDTO) (string, string, bool) {
	if !validHTTPURL(pageIndex.APIURL) {
		return "knowledge.pageindex.api_url", "PageIndex API URL must be http or https", false
	}
	if pageIndex.APIKey != "" && !asciiPrintable(pageIndex.APIKey) {
		return "knowledge.pageindex.api_key", "PageIndex API key must contain only printable ASCII characters", false
	}
	if pageIndex.TimeoutSeconds < 1 || pageIndex.TimeoutSeconds > 300 {
		return "knowledge.pageindex.timeout_seconds", "PageIndex timeout must be between 1 and 300 seconds", false
	}
	return "", "", true
}

func normalizeWorkspaceRoot(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return filepath.Clean(value)
}

func normalizeWorkspaceAllowlist(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		root := normalizeWorkspaceRoot(value)
		if root == "" || seen[root] {
			continue
		}
		seen[root] = true
		out = append(out, root)
	}
	return out
}

func validKnowledgeProvider(provider string) bool {
	switch strings.TrimSpace(provider) {
	case "pageindex", "local":
		return true
	default:
		return false
	}
}

func validSettingsRuntimeKind(kind string) bool {
	switch strings.TrimSpace(kind) {
	case "managed", "codex", "claude", "cursor", "gemini", "openclaw", "hermes", "pi", "acp":
		return true
	default:
		return false
	}
}

func normalizeURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return strings.TrimRight(value, "/")
}

func validHTTPURL(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")
}

func asciiPrintable(value string) bool {
	for _, r := range value {
		if r < 0x20 || r > 0x7e {
			return false
		}
	}
	return true
}

func cloneSettingsDTO(settings SettingsDTO) SettingsDTO {
	settings.WorkspaceAllowlist = append([]string(nil), settings.WorkspaceAllowlist...)
	settings.RuntimeProfiles = cloneRuntimeProfileDTOs(settings.RuntimeProfiles)
	settings.LLMProfiles = append([]SettingsLLMProfileDTO(nil), settings.LLMProfiles...)
	settings.PromptBlocks = clonePromptBlockDTOs(settings.PromptBlocks)
	settings.NotificationRules = append([]SettingsNotificationRuleDTO(nil), settings.NotificationRules...)
	settings.RuntimeApprovals = runtimeapproval.Clone(settings.RuntimeApprovals)
	return settings
}

func settingsForResponse(settings SettingsDTO) SettingsDTO {
	settings = cloneSettingsDTO(settings)
	settings.Knowledge.PageIndex.APIKey = ""
	for i := range settings.LLMProfiles {
		settings.LLMProfiles[i].APIKey = ""
	}
	return settings
}

func PlaintextSettingsSecretField(settings SettingsDTO) string {
	if strings.TrimSpace(settings.Knowledge.PageIndex.APIKey) != "" {
		return "knowledge.pageindex.api_key"
	}
	for i, profile := range settings.LLMProfiles {
		if strings.TrimSpace(profile.APIKey) != "" {
			return "llm_profiles[" + strconv.Itoa(i) + "].api_key"
		}
	}
	return ""
}

func RuntimePromptBlocksFromSettings(blocks []SettingsPromptBlockDTO) []agentcore.RuntimePromptBlock {
	return agentcore.NormalizeRuntimePromptBlocks(runtimePromptBlocksFromSettingsRaw(blocks))
}

func runtimePromptBlocksFromSettingsRaw(blocks []SettingsPromptBlockDTO) []agentcore.RuntimePromptBlock {
	out := make([]agentcore.RuntimePromptBlock, 0, len(blocks))
	for _, block := range blocks {
		out = append(out, agentcore.RuntimePromptBlock{
			BlockID:      strings.TrimSpace(block.BlockID),
			Code:         strings.TrimSpace(block.Code),
			Title:        strings.TrimSpace(block.Title),
			Description:  strings.TrimSpace(block.Description),
			Role:         strings.TrimSpace(block.Role),
			Content:      strings.TrimSpace(block.Content),
			Placeholder:  strings.TrimSpace(block.Placeholder),
			Dynamic:      block.Dynamic,
			Editable:     block.Editable,
			Removable:    block.Removable,
			VariableOnly: block.VariableOnly,
			Enabled:      block.Enabled,
		})
	}
	return out
}

func PromptBlockDTOsFromRuntime(blocks []agentcore.RuntimePromptBlock) []SettingsPromptBlockDTO {
	return promptBlockDTOsFromRuntime(blocks)
}

func promptBlockDTOsFromRuntime(blocks []agentcore.RuntimePromptBlock) []SettingsPromptBlockDTO {
	out := make([]SettingsPromptBlockDTO, 0, len(blocks))
	for _, block := range blocks {
		out = append(out, SettingsPromptBlockDTO{
			BlockID:      strings.TrimSpace(block.BlockID),
			Code:         strings.TrimSpace(block.Code),
			Title:        strings.TrimSpace(block.Title),
			Description:  strings.TrimSpace(block.Description),
			Role:         strings.TrimSpace(block.Role),
			Content:      strings.TrimSpace(block.Content),
			Placeholder:  strings.TrimSpace(block.Placeholder),
			Dynamic:      block.Dynamic,
			Editable:     block.Editable,
			Removable:    block.Removable,
			VariableOnly: block.VariableOnly,
			Enabled:      block.Enabled,
		})
	}
	return out
}

func clonePromptBlockDTOs(blocks []SettingsPromptBlockDTO) []SettingsPromptBlockDTO {
	if blocks == nil {
		return nil
	}
	out := make([]SettingsPromptBlockDTO, len(blocks))
	copy(out, blocks)
	return out
}

func promptBlockDefaultMap() map[string]SettingsPromptBlockDTO {
	out := map[string]SettingsPromptBlockDTO{}
	for _, block := range promptBlockDTOsFromRuntime(agentcore.KnownRuntimePromptBlocks()) {
		out[block.BlockID] = block
	}
	return out
}

func promptBlockDefaultCodeOwners(defaults map[string]SettingsPromptBlockDTO) map[string]string {
	out := map[string]string{}
	for blockID, block := range defaults {
		if code := strings.TrimSpace(block.Code); code != "" {
			out[code] = blockID
		}
	}
	return out
}

func legacyAgentProfileSettingsBlocks(legacy SettingsPromptBlockDTO) []SettingsPromptBlockDTO {
	defaults := promptBlockDefaultMap()
	block, ok := defaults[agentcore.PromptBlockAgentProfileTemplate]
	if !ok {
		return nil
	}
	if strings.TrimSpace(legacy.Title) != "" {
		block.Title = strings.TrimSpace(legacy.Title)
	}
	if strings.TrimSpace(legacy.Description) != "" {
		block.Description = strings.TrimSpace(legacy.Description)
	}
	if role := normalizePromptBlockRole(legacy.Role); role != "" {
		block.Role = role
	}
	if strings.TrimSpace(legacy.Content) != "" {
		block.Content = strings.TrimSpace(legacy.Content)
	}
	if strings.TrimSpace(legacy.Code) != "" && validPromptBlockCode(legacy.Code) {
		block.Code = strings.TrimSpace(legacy.Code)
	}
	if block.Code == agentcore.PromptBlockAgentProfile {
		block.Code = agentcore.PromptBlockAgentProfileTemplate
	}
	block.BlockID = agentcore.PromptBlockAgentProfileTemplate
	block.Placeholder = promptBlockVariableReference(block.Code)
	block.Dynamic = false
	block.Editable = true
	block.Removable = false
	block.Enabled = legacy.Enabled
	return []SettingsPromptBlockDTO{block}
}

func normalizePromptBlockRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case agentcore.RuntimePromptRoleSystem:
		return agentcore.RuntimePromptRoleSystem
	case agentcore.RuntimePromptRoleAssistant:
		return agentcore.RuntimePromptRoleAssistant
	case agentcore.RuntimePromptRoleUser:
		return agentcore.RuntimePromptRoleUser
	default:
		return ""
	}
}

var promptBlockCodeRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]*$`)

func validPromptBlockCode(code string) bool {
	return promptBlockCodeRE.MatchString(strings.TrimSpace(code))
}

func promptBlockVariableReference(code string) string {
	code = strings.TrimSpace(code)
	if code == "" {
		return ""
	}
	return "<<" + code + ">>"
}

func cloneRuntimeProfileDTOs(profiles []SettingsRuntimeProfileDTO) []SettingsRuntimeProfileDTO {
	if profiles == nil {
		return nil
	}
	out := make([]SettingsRuntimeProfileDTO, len(profiles))
	for i, profile := range profiles {
		out[i] = profile
		out[i].RuntimeConfig = cloneRuntimeConfig(profile.RuntimeConfig)
	}
	return out
}

func cloneRuntimeConfig(config map[string]any) map[string]any {
	if config == nil {
		return nil
	}
	out := make(map[string]any, len(config))
	for key, value := range config {
		switch v := value.(type) {
		case []any:
			out[key] = append([]any(nil), v...)
		case []string:
			out[key] = append([]string(nil), v...)
		case map[string]any:
			out[key] = cloneRuntimeConfig(v)
		default:
			out[key] = v
		}
	}
	return out
}
