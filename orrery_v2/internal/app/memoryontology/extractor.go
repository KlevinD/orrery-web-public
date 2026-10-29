package memoryontology

import (
	"context"
	"encoding/json"
	"fmt"
	"orrery_v2/internal/agent/llm"
	memcore "orrery_v2/internal/memory/core"
	"regexp"
	"strings"
)

type ChatModelProvider interface {
	Model(context.Context) (llm.ChatModel, error)
}

type ChatModelProviderFunc func(context.Context) (llm.ChatModel, error)

func (f ChatModelProviderFunc) Model(ctx context.Context) (llm.ChatModel, error) {
	return f(ctx)
}

type ChatExtractor struct {
	models  ChatModelProvider
	modelID string
}

func NewChatExtractor(models ChatModelProvider, modelID string) *ChatExtractor {
	return &ChatExtractor{models: models, modelID: strings.TrimSpace(modelID)}
}

func (e *ChatExtractor) ModelID() string {
	if e == nil {
		return ""
	}
	return e.modelID
}

func (e *ChatExtractor) Extract(ctx context.Context, record memcore.MemoryRecord) (Extraction, error) {
	if err := ctx.Err(); err != nil {
		return Extraction{}, err
	}
	if e == nil || e.models == nil {
		return Extraction{}, fmt.Errorf("memory ontology chat model is required")
	}
	chatModel, err := e.models.Model(ctx)
	if err != nil {
		return Extraction{}, err
	}
	if chatModel == nil {
		return Extraction{}, fmt.Errorf("memory ontology chat model is required")
	}
	payload, err := json.Marshal(memoryExtractionPayload(record))
	if err != nil {
		return Extraction{}, err
	}
	message, err := chatModel.Generate(ctx, []*llm.Message{
		llm.SystemMessage(memoryEntityExtractionPrompt()),
		llm.UserMessage(string(payload)),
	})
	if err != nil {
		return Extraction{}, err
	}
	return parseExtraction(message)
}

func memoryEntityExtractionPrompt() string {
	return strings.Join([]string{
		"Extract explicit entity mentions from one durable memory record for an ontology graph.",
		"Return JSON only with this schema: {\"mentions\":[{\"surface\":\"text exactly naming the entity\",\"object_type\":\"one allowed type\",\"object_hint\":\"exact identifier or canonical name when present\",\"confidence\":0.0,\"anchor\":\"summary|narrative|fact|lesson|decision|tag|entity\"}]}",
		"Allowed object_type values: " + strings.Join(SupportedObjectTypes(), ", ") + ".",
		"Only extract a named, concrete entity that the text explicitly mentions. Do not infer unnamed entities, relationships, intentions, generic concepts, dates, statuses, or operational trace fields.",
		"Preserve the entity surface in its source language. object_hint must be an exact ID or canonical name from the input when available; otherwise use the same text as surface.",
		"Confidence measures certainty that the text names that ontology type. Use 0.90 or higher only for explicit unambiguous mentions.",
		"Deduplicate repeated mentions of the same entity and type. Return an empty mentions array when no concrete entity is named.",
	}, "\n")
}

func memoryExtractionPayload(record memcore.MemoryRecord) map[string]any {
	return map[string]any{
		"memory_id": record.ID,
		"topic":     boundedText(record.Topic, 600),
		"content":   boundedText(record.Content, 4000),
		"summary":   boundedText(record.Summary, 2400),
		"narrative": boundedText(record.Narrative, 6000),
		"facts":     boundedStrings(record.Facts, 12, 1000),
		"lessons":   boundedStrings(record.Lessons, 12, 1000),
		"decisions": boundedStrings(record.Decisions, 12, 1000),
		"tags":      boundedStrings(record.Tags, 20, 200),
		"entities":  record.Entities,
	}
}

func boundedText(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > maxRunes {
		value = string(runes[:maxRunes])
	}
	return strings.TrimSpace(value)
}

func boundedStrings(values []string, maxItems, maxRunes int) []string {
	out := make([]string, 0, min(len(values), maxItems))
	for _, value := range values {
		if len(out) >= maxItems {
			break
		}
		if value = boundedText(value, maxRunes); value != "" {
			out = append(out, value)
		}
	}
	return out
}

var extractionThinkPattern = regexp.MustCompile(`(?is)<think\b[^>]*>.*?</think>`)

func parseExtraction(message *llm.Message) (Extraction, error) {
	if message == nil || strings.TrimSpace(message.Content) == "" {
		return Extraction{}, fmt.Errorf("memory ontology extraction is empty")
	}
	content := strings.TrimSpace(extractionThinkPattern.ReplaceAllString(message.Content, ""))
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	start, end := strings.Index(content, "{"), strings.LastIndex(content, "}")
	if start >= 0 && end >= start {
		content = content[start : end+1]
	}
	var extraction Extraction
	if err := json.Unmarshal([]byte(content), &extraction); err != nil {
		return Extraction{}, fmt.Errorf("decode memory ontology extraction: %w", err)
	}
	extraction.Mentions = normalizeMentions(extraction.Mentions)
	return extraction, nil
}

func normalizeMentions(mentions []Mention) []Mention {
	out := make([]Mention, 0, min(len(mentions), 64))
	seen := map[string]struct{}{}
	for _, mention := range mentions {
		mention.Surface = strings.TrimSpace(mention.Surface)
		mention.ObjectType = strings.ToLower(strings.TrimSpace(mention.ObjectType))
		mention.ObjectHint = strings.TrimSpace(mention.ObjectHint)
		mention.Anchor = strings.ToLower(strings.TrimSpace(mention.Anchor))
		if mention.Surface == "" || mention.ObjectType == "" || len(mention.ObjectType) > 80 || mention.Confidence < 0 || mention.Confidence > 1 {
			continue
		}
		if mention.ObjectHint == "" {
			mention.ObjectHint = mention.Surface
		}
		key := mention.ObjectType + "\x1f" + strings.ToLower(mention.ObjectHint) + "\x1f" + strings.ToLower(mention.Surface)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, mention)
		if len(out) == 64 {
			break
		}
	}
	return out
}
