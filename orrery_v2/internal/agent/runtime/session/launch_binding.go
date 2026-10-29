package session

import (
	"encoding/json"
	"net/url"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

const (
	maxRuntimeProviderConfigBytes = 256 * 1024
	maxRuntimeLaunchArgs          = 128
	maxRuntimeLaunchEnvironment   = 256
	maxRuntimeLaunchExecutable    = 32 * 1024
	maxRuntimeLaunchVersion       = 4 * 1024
	maxRuntimeLaunchArgument      = 32 * 1024
	maxRuntimeLaunchEnvName       = 1024
	maxRuntimeLaunchEnvValue      = 128 * 1024
	maxRuntimeLaunchMaterial      = 512 * 1024
	maxRuntimeCredentialRef       = 4 * 1024
)

// RuntimeLaunchBinding is server-private process launch material extracted
// from an authorized runtime profile. It never crosses the canonical contract.
type RuntimeLaunchBinding struct {
	Executable  string
	Version     string
	ExtraArgs   []string
	Environment map[string]string
}

// RuntimeAuthBinding is server-private credential routing metadata. The
// credential itself is resolved by the application only when auth is requested.
type RuntimeAuthBinding struct {
	MethodID      string
	CredentialRef string
}

type runtimeLaunchConfig struct {
	Executable string             `json:"executable"`
	Version    string             `json:"version"`
	ExtraArgs  []string           `json:"extra_args"`
	Env        map[string]string  `json:"env"`
	Auth       *runtimeAuthConfig `json:"auth"`
}

type runtimeAuthConfig struct {
	MethodID      string `json:"method_id"`
	CredentialRef string `json:"credential_ref"`
}

func runtimeLaunchBinding(request agentcore.RuntimeRequest) (RuntimeLaunchBinding, RuntimeAuthBinding, error) {
	raw := request.RuntimeOptions.ProviderConfig
	if len(raw) > maxRuntimeProviderConfigBytes {
		return RuntimeLaunchBinding{}, RuntimeAuthBinding{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "runtime provider configuration exceeds the size limit", false)
	}
	var config runtimeLaunchConfig
	if len(raw) != 0 {
		if strings.TrimSpace(string(raw)) == "null" || json.Unmarshal(raw, &config) != nil {
			return RuntimeLaunchBinding{}, RuntimeAuthBinding{}, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "runtime provider configuration is invalid", false)
		}
	}
	environment := cloneStringMap(config.Env)
	if environment == nil && len(request.RuntimeOptions.Environment) != 0 {
		environment = make(map[string]string, len(request.RuntimeOptions.Environment))
	}
	for name, value := range request.RuntimeOptions.Environment {
		environment[name] = value
	}
	binding := RuntimeLaunchBinding{
		Executable:  strings.TrimSpace(config.Executable),
		Version:     strings.TrimSpace(config.Version),
		ExtraArgs:   append([]string(nil), config.ExtraArgs...),
		Environment: environment,
	}
	binding.ExtraArgs = append(binding.ExtraArgs, request.RuntimeOptions.ExtraArgs...)
	binding.ExtraArgs = append(binding.ExtraArgs, request.RuntimeOptions.CustomArgs...)
	if err := validateRuntimeLaunchBinding(binding); err != nil {
		return RuntimeLaunchBinding{}, RuntimeAuthBinding{}, err
	}
	auth := RuntimeAuthBinding{}
	if config.Auth != nil {
		auth.MethodID = strings.TrimSpace(config.Auth.MethodID)
		auth.CredentialRef = strings.TrimSpace(config.Auth.CredentialRef)
	}
	if err := validateRuntimeAuthBinding(auth); err != nil {
		return RuntimeLaunchBinding{}, RuntimeAuthBinding{}, err
	}
	return binding, auth, nil
}

func validateRuntimeLaunchBinding(binding RuntimeLaunchBinding) error {
	if len(binding.Executable) > maxRuntimeLaunchExecutable || len(binding.Version) > maxRuntimeLaunchVersion || strings.ContainsRune(binding.Executable, 0) || strings.ContainsRune(binding.Version, 0) {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "runtime launch identity contains an invalid value", false)
	}
	total := len(binding.Executable) + len(binding.Version)
	if len(binding.ExtraArgs) > maxRuntimeLaunchArgs {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "runtime launch argument count exceeds the limit", false)
	}
	for _, argument := range binding.ExtraArgs {
		total += len(argument)
		if len(argument) > maxRuntimeLaunchArgument || strings.ContainsRune(argument, 0) {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "runtime launch argument contains an invalid value", false)
		}
	}
	if len(binding.Environment) > maxRuntimeLaunchEnvironment {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "runtime launch environment count exceeds the limit", false)
	}
	for name, value := range binding.Environment {
		total += len(name) + len(value)
		if strings.TrimSpace(name) == "" || len(name) > maxRuntimeLaunchEnvName || len(value) > maxRuntimeLaunchEnvValue || strings.ContainsAny(name, "=\x00") || strings.ContainsRune(value, 0) {
			return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "runtime launch environment contains an invalid entry", false)
		}
	}
	if total > maxRuntimeLaunchMaterial {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "runtime launch material exceeds the size limit", false)
	}
	return nil
}

func validateRuntimeAuthBinding(binding RuntimeAuthBinding) error {
	if binding.MethodID == "" && binding.CredentialRef == "" {
		return nil
	}
	if binding.MethodID == "" || binding.CredentialRef == "" || len(binding.CredentialRef) > maxRuntimeCredentialRef || strings.ContainsRune(binding.CredentialRef, 0) {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "runtime auth binding requires method_id and credential_ref", false)
	}
	switch binding.MethodID {
	case "refresh", "chatgptAuthTokens":
	default:
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, "runtime auth method is not supported", false)
	}
	parsed, err := url.Parse(binding.CredentialRef)
	if err != nil || (parsed.Scheme != "secret" && parsed.Scheme != "settings") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "runtime auth credential_ref must be an opaque managed-secret reference", false)
	}
	return nil
}

func cloneRuntimeLaunchBinding(source RuntimeLaunchBinding) RuntimeLaunchBinding {
	return RuntimeLaunchBinding{
		Executable:  source.Executable,
		Version:     source.Version,
		ExtraArgs:   append([]string(nil), source.ExtraArgs...),
		Environment: cloneStringMap(source.Environment),
	}
}

func cloneRuntimeAuthBinding(source RuntimeAuthBinding) RuntimeAuthBinding { return source }
