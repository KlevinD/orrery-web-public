package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	agentcore "orrery_v2/internal/agent/core"
	emailchannel "orrery_v2/internal/gateway/channel/email"
	gwconfig "orrery_v2/internal/gateway/config"
	mcptransport "orrery_v2/internal/transport/mcp"
)

const (
	maxEmailRecipients   = 50
	maxEmailSubjectRunes = 200
	maxEmailBodyBytes    = 1 << 20
)

type productionEmailConfigLoader interface {
	LoadOrDefault() (gwconfig.Record, error)
}

type productionEmailProfileStore interface {
	GetProfile(context.Context, agentcore.AgentProfileID) (agentcore.AgentProfile, error)
}

type productionEmailToolExecutor struct {
	configs  productionEmailConfigLoader
	profiles productionEmailProfileStore
	sender   emailchannel.Sender

	mu   sync.Mutex
	sent map[string]productionEmailIdempotencyRecord
}

type productionEmailIdempotencyRecord struct {
	fingerprint string
	result      mcptransport.EmailToolResult
}

func newProductionEmailToolExecutor(configs productionEmailConfigLoader, profiles productionEmailProfileStore, sender emailchannel.Sender) *productionEmailToolExecutor {
	return &productionEmailToolExecutor{configs: configs, profiles: profiles, sender: sender, sent: map[string]productionEmailIdempotencyRecord{}}
}

func (e *productionEmailToolExecutor) ExecuteEmailTool(ctx context.Context, toolID string, req mcptransport.EmailToolRequest) (mcptransport.EmailToolResult, error) {
	if e == nil || e.configs == nil || e.profiles == nil || e.sender == nil {
		return mcptransport.EmailToolResult{}, mcptransport.ErrServiceDependency
	}
	if strings.TrimSpace(toolID) != "email_send" {
		return mcptransport.EmailToolResult{}, mcptransport.ErrInvalidRequest
	}
	profileID := strings.TrimSpace(req.ProfileID)
	if profileID == "" {
		return mcptransport.EmailToolResult{}, fmt.Errorf("%w: agent profile is required", mcptransport.ErrInvalidRequest)
	}
	profile, err := e.profiles.GetProfile(ctx, agentcore.AgentProfileID(profileID))
	if err != nil {
		return mcptransport.EmailToolResult{}, err
	}
	if !profile.Enabled {
		return mcptransport.EmailToolResult{}, mcptransport.ErrPermissionDenied
	}
	tenantID := strings.TrimSpace(req.TenantID)
	if tenantID == "" || !strings.EqualFold(tenantID, strings.TrimSpace(string(profile.OrgIdentity.OrganizationRef.ID))) {
		return mcptransport.EmailToolResult{}, mcptransport.ErrPermissionDenied
	}

	message := emailchannel.Message{
		From:    strings.TrimSpace(profile.Email),
		To:      cleanEmailRecipients(req.To),
		Cc:      cleanEmailRecipients(req.Cc),
		Subject: strings.TrimSpace(req.Subject),
		Text:    req.Text,
		HTML:    req.HTML,
	}
	if err := validateEmailToolMessage(message, req.IdempotencyKey); err != nil {
		return mcptransport.EmailToolResult{}, fmt.Errorf("%w: %v", mcptransport.ErrInvalidRequest, err)
	}
	record, err := e.configs.LoadOrDefault()
	if err != nil {
		return mcptransport.EmailToolResult{}, fmt.Errorf("load email settings: %w", err)
	}
	if !record.Config.Adapters.Email.Enabled {
		return mcptransport.EmailToolResult{}, fmt.Errorf("%w: email channel is disabled", mcptransport.ErrServiceDependency)
	}
	options := emailchannel.OptionsFromConfig(record.Config.Adapters.Email)
	if err := emailchannel.ValidateSMTPOptions(options); err != nil {
		return mcptransport.EmailToolResult{}, fmt.Errorf("%w: %v", mcptransport.ErrServiceDependency, err)
	}

	idempotencyKey := strings.TrimSpace(req.IdempotencyKey)
	fingerprint := emailToolFingerprint(message)
	if idempotencyKey != "" {
		e.mu.Lock()
		defer e.mu.Unlock()
		cacheKey := profileID + "\x00" + idempotencyKey
		if previous, ok := e.sent[cacheKey]; ok {
			if previous.fingerprint != fingerprint {
				return mcptransport.EmailToolResult{}, fmt.Errorf("%w: idempotency_key was already used with different email content", mcptransport.ErrInvalidRequest)
			}
			return cloneEmailToolResult(previous.result), nil
		}
		result, sendErr := e.send(ctx, options, message)
		if sendErr == nil {
			e.sent[cacheKey] = productionEmailIdempotencyRecord{fingerprint: fingerprint, result: cloneEmailToolResult(result)}
		}
		return result, sendErr
	}
	return e.send(ctx, options, message)
}

func (e *productionEmailToolExecutor) send(ctx context.Context, options emailchannel.Options, message emailchannel.Message) (mcptransport.EmailToolResult, error) {
	receipt, err := e.sender.Send(ctx, options, message)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return mcptransport.EmailToolResult{}, err
		}
		return mcptransport.EmailToolResult{}, fmt.Errorf("email delivery failed: %w", err)
	}
	return mcptransport.EmailToolResult{Output: map[string]any{
		"status":     "sent",
		"message_id": strings.TrimSpace(receipt.MessageID),
		"from":       message.From,
		"to":         append([]string(nil), message.To...),
		"cc":         append([]string(nil), message.Cc...),
	}}, nil
}

func validateEmailToolMessage(message emailchannel.Message, idempotencyKey string) error {
	if len(message.To)+len(message.Cc) > maxEmailRecipients {
		return fmt.Errorf("email supports at most %d recipients", maxEmailRecipients)
	}
	if utf8.RuneCountInString(message.Subject) > maxEmailSubjectRunes {
		return fmt.Errorf("subject supports at most %d characters", maxEmailSubjectRunes)
	}
	if len(message.Text)+len(message.HTML) > maxEmailBodyBytes {
		return fmt.Errorf("email body supports at most %d bytes", maxEmailBodyBytes)
	}
	if len(strings.TrimSpace(idempotencyKey)) > 200 {
		return fmt.Errorf("idempotency_key supports at most 200 characters")
	}
	return emailchannel.ValidateMessage(message)
}

func cleanEmailRecipients(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, value)
	}
	return result
}

func emailToolFingerprint(message emailchannel.Message) string {
	raw, _ := json.Marshal(message)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func cloneEmailToolResult(result mcptransport.EmailToolResult) mcptransport.EmailToolResult {
	cloned := mcptransport.EmailToolResult{Output: make(map[string]any, len(result.Output))}
	for key, value := range result.Output {
		switch typed := value.(type) {
		case []string:
			cloned.Output[key] = append([]string(nil), typed...)
		default:
			cloned.Output[key] = value
		}
	}
	return cloned
}
