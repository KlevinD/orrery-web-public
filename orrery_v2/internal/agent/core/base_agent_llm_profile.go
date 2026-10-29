package core

const BaseAgentLLMProfileExtensionKey = "llm_profile"

// PlatformDefaultLLMProfileID makes a profile inherit the current platform LLM.
const PlatformDefaultLLMProfileID = "platform-default"

type BaseAgentLLMProfileSpec struct {
	ProfileID     string  `json:"profile_id"`
	DisplayName   string  `json:"display_name,omitempty"`
	Provider      string  `json:"provider"`
	BaseURL       string  `json:"base_url"`
	APIKeyRef     string  `json:"api_key_ref,omitempty"`
	Model         string  `json:"model,omitempty"`
	Temperature   float64 `json:"temperature,omitempty"`
	ContextLength int     `json:"context_length,omitempty"`
}
