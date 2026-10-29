package llm

import "context"

type ChatModel interface {
	Generate(context.Context, []*Message, ...Option) (*Message, error)
	Stream(context.Context, []*Message, ...Option) (*MessageStream, error)
}

type ToolChoice string

const (
	ToolChoiceAuto      ToolChoice = "auto"
	ToolChoiceForbidden ToolChoice = "none"
	ToolChoiceForced    ToolChoice = "required"
)

type GenerationOptions struct {
	Model       *string
	Temperature *float32
	MaxTokens   *int
	Tools       []*Tool
	ToolChoice  *ToolChoice
}

type Option func(*GenerationOptions)

func Apply(options ...Option) GenerationOptions {
	var result GenerationOptions
	for _, option := range options {
		if option != nil {
			option(&result)
		}
	}
	return result
}

func WithTools(tools []*Tool) Option {
	return func(options *GenerationOptions) { options.Tools = append([]*Tool(nil), tools...) }
}
func WithTemperature(value float32) Option {
	return func(o *GenerationOptions) { o.Temperature = &value }
}
func WithMaxTokens(value int) Option { return func(o *GenerationOptions) { o.MaxTokens = &value } }
func WithToolChoice(value ToolChoice) Option {
	return func(o *GenerationOptions) { o.ToolChoice = &value }
}
