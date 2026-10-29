package ontology

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

type ModelRunEvaluationTrendAlertNotificationPolicySenderOptions struct {
	Policies              ModelRunEvaluationTrendAlertNotificationPolicyStore
	Default               ModelRunEvaluationTrendAlertNotificationSender
	Client                *http.Client
	AllowPrivateEndpoints bool
	Resolver              ModelRunEvaluationTrendAlertNotificationEndpointResolver
	Secrets               ModelRunEvaluationTrendAlertNotificationSecretResolver
}

type ModelRunEvaluationTrendAlertNotificationPolicySender struct {
	policies              ModelRunEvaluationTrendAlertNotificationPolicyStore
	fallback              ModelRunEvaluationTrendAlertNotificationSender
	client                *http.Client
	allowPrivateEndpoints bool
	resolver              ModelRunEvaluationTrendAlertNotificationEndpointResolver
	secrets               ModelRunEvaluationTrendAlertNotificationSecretResolver
}

type ModelRunEvaluationTrendAlertNotificationSecretResolver interface {
	ResolveSecret(ctx context.Context, ref string) (string, error)
}

func NewModelRunEvaluationTrendAlertNotificationPolicySender(opts ModelRunEvaluationTrendAlertNotificationPolicySenderOptions) *ModelRunEvaluationTrendAlertNotificationPolicySender {
	return &ModelRunEvaluationTrendAlertNotificationPolicySender{
		policies:              opts.Policies,
		fallback:              opts.Default,
		client:                opts.Client,
		allowPrivateEndpoints: opts.AllowPrivateEndpoints,
		resolver:              opts.Resolver,
		secrets:               opts.Secrets,
	}
}

func (s *ModelRunEvaluationTrendAlertNotificationPolicySender) SendModelRunEvaluationTrendAlertNotification(ctx context.Context, event ModelRunEvaluationTrendAlertAuditEvent, plan ModelRunEvaluationTrendAlertNotificationPlan) (ModelRunEvaluationTrendAlertNotificationReceipt, error) {
	if s == nil {
		return ModelRunEvaluationTrendAlertNotificationReceipt{}, fmt.Errorf("%w: model run evaluation trend alert notification policy sender is required", ErrInvalidDefinition)
	}
	policy, found, err := s.lookupPolicy(ctx, event)
	if err != nil {
		return ModelRunEvaluationTrendAlertNotificationReceipt{}, err
	}
	if found && strings.TrimSpace(policy.Endpoint) != "" {
		headers, err := resolveModelRunEvaluationTrendAlertNotificationPolicyHeaders(ctx, policy.Headers, s.secrets)
		if err != nil {
			return ModelRunEvaluationTrendAlertNotificationReceipt{}, err
		}
		return NewModelRunEvaluationTrendAlertNotificationWebhookSender(ModelRunEvaluationTrendAlertNotificationWebhookSenderOptions{
			Endpoint:              policy.Endpoint,
			Channel:               policy.Channel,
			Headers:               headers,
			Client:                s.client,
			AllowPrivateEndpoints: s.allowPrivateEndpoints,
			Resolver:              s.resolver,
		}).SendModelRunEvaluationTrendAlertNotification(ctx, event, plan)
	}
	if s.fallback == nil {
		return ModelRunEvaluationTrendAlertNotificationReceipt{}, fmt.Errorf("%w: model run evaluation trend alert notification fallback sender is required", ErrInvalidDefinition)
	}
	return s.fallback.SendModelRunEvaluationTrendAlertNotification(ctx, event, plan)
}

func (s *ModelRunEvaluationTrendAlertNotificationPolicySender) lookupPolicy(ctx context.Context, event ModelRunEvaluationTrendAlertAuditEvent) (ModelRunEvaluationTrendAlertNotificationPolicyRecord, bool, error) {
	if s.policies == nil {
		return ModelRunEvaluationTrendAlertNotificationPolicyRecord{}, false, nil
	}
	page, err := s.policies.ListModelRunEvaluationTrendAlertNotificationPolicies(ctx, ModelRunEvaluationTrendAlertNotificationPolicyQuery{
		TenantID:    event.TenantID,
		RuleID:      event.RuleID,
		EnabledOnly: true,
		Limit:       1,
	})
	if err != nil {
		return ModelRunEvaluationTrendAlertNotificationPolicyRecord{}, false, err
	}
	if len(page.Policies) > 0 {
		return page.Policies[0], true, nil
	}
	page, err = s.policies.ListModelRunEvaluationTrendAlertNotificationPolicies(ctx, ModelRunEvaluationTrendAlertNotificationPolicyQuery{
		TenantID:    event.TenantID,
		EnabledOnly: true,
		Limit:       1,
	})
	if err != nil {
		return ModelRunEvaluationTrendAlertNotificationPolicyRecord{}, false, err
	}
	if len(page.Policies) == 0 {
		return ModelRunEvaluationTrendAlertNotificationPolicyRecord{}, false, nil
	}
	return page.Policies[0], true, nil
}

func resolveModelRunEvaluationTrendAlertNotificationPolicyHeaders(ctx context.Context, headers map[string]string, secrets ModelRunEvaluationTrendAlertNotificationSecretResolver) (map[string]string, error) {
	out := cloneStringMetadata(headers)
	for key, value := range out {
		value = strings.TrimSpace(value)
		if !modelRunEvaluationTrendAlertNotificationHeaderNeedsSecret(value) {
			continue
		}
		if secrets == nil {
			return nil, fmt.Errorf("%w: model run evaluation trend alert notification header %s secret resolver is required", ErrInvalidDefinition, key)
		}
		resolved, err := resolveModelRunEvaluationTrendAlertNotificationHeaderSecretValue(ctx, value, secrets)
		if err != nil {
			return nil, fmt.Errorf("resolve model run evaluation trend alert notification header %s secret: %w", key, err)
		}
		out[key] = resolved
	}
	return out, nil
}

var modelRunEvaluationTrendAlertNotificationHeaderSecretTemplatePattern = regexp.MustCompile(`\$\{((?:secret|settings)://[^}\s]+)\}`)

func resolveModelRunEvaluationTrendAlertNotificationHeaderSecretValue(ctx context.Context, value string, secrets ModelRunEvaluationTrendAlertNotificationSecretResolver) (string, error) {
	if modelRunEvaluationTrendAlertNotificationHeaderIsSecretRef(value) {
		return secrets.ResolveSecret(ctx, value)
	}
	var resolveErr error
	resolved := modelRunEvaluationTrendAlertNotificationHeaderSecretTemplatePattern.ReplaceAllStringFunc(value, func(match string) string {
		if resolveErr != nil {
			return match
		}
		parts := modelRunEvaluationTrendAlertNotificationHeaderSecretTemplatePattern.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		secretValue, err := secrets.ResolveSecret(ctx, parts[1])
		if err != nil {
			resolveErr = err
			return match
		}
		return secretValue
	})
	if resolveErr != nil {
		return "", resolveErr
	}
	return resolved, nil
}

func modelRunEvaluationTrendAlertNotificationHeaderNeedsSecret(value string) bool {
	value = strings.TrimSpace(value)
	return modelRunEvaluationTrendAlertNotificationHeaderIsSecretRef(value) ||
		modelRunEvaluationTrendAlertNotificationHeaderSecretTemplatePattern.MatchString(value)
}

func modelRunEvaluationTrendAlertNotificationHeaderIsSecretRef(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "secret://") || strings.HasPrefix(value, "settings://")
}
