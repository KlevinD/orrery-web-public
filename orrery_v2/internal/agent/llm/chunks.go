package llm

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

// MergeChunks joins provider deltas by tool index while keeping tool identities
// stable. It returns independent values so a provider cannot mutate the result.
func MergeChunks(chunks []*Message) (*Message, error) {
	out := &Message{}
	var content strings.Builder
	indexed := make(map[int]ToolCall)
	var order []int
	for _, chunk := range chunks {
		if chunk == nil {
			return nil, errors.New("nil model chunk")
		}
		if chunk.Role != "" {
			if out.Role != "" && out.Role != chunk.Role {
				return nil, errors.New("mixed model roles")
			}
			out.Role = chunk.Role
		}
		content.WriteString(chunk.Content)
		out.ReasoningContent += chunk.ReasoningContent
		if len(chunk.Extra) > 0 {
			raw, err := json.Marshal(chunk.Extra)
			if err != nil {
				return nil, err
			}
			var metadata map[string]any
			if err := json.Unmarshal(raw, &metadata); err != nil {
				return nil, err
			}
			if out.Extra == nil {
				out.Extra = make(map[string]any)
			}
			for key, value := range metadata {
				out.Extra[key] = value
			}
		}
		for _, call := range chunk.ToolCalls {
			if call.Index == nil {
				raw, err := json.Marshal(call)
				if err != nil {
					return nil, err
				}
				var copy ToolCall
				if err = json.Unmarshal(raw, &copy); err != nil {
					return nil, err
				}
				out.ToolCalls = append(out.ToolCalls, copy)
				continue
			}
			index := *call.Index
			if index < 0 {
				return nil, errors.New("negative tool index")
			}
			current, exists := indexed[index]
			if !exists {
				current.Index = &index
				order = append(order, index)
			}
			for _, pair := range [][2]string{{current.ID, call.ID}, {current.Type, call.Type}, {current.Function.Name, call.Function.Name}} {
				if pair[0] != "" && pair[1] != "" && pair[0] != pair[1] {
					return nil, errors.New("tool identity changed within stream")
				}
			}
			if call.ID != "" {
				current.ID = call.ID
			}
			if call.Type != "" {
				current.Type = call.Type
			}
			if call.Function.Name != "" {
				current.Function.Name = call.Function.Name
			}
			current.Function.Arguments += call.Function.Arguments
			indexed[index] = current
		}
		if chunk.ResponseMeta != nil {
			if out.ResponseMeta == nil {
				out.ResponseMeta = &ResponseMeta{}
			}
			if chunk.ResponseMeta.FinishReason != "" {
				out.ResponseMeta.FinishReason = chunk.ResponseMeta.FinishReason
			}
			if chunk.ResponseMeta.Usage != nil {
				value := *chunk.ResponseMeta.Usage
				out.ResponseMeta.Usage = &value
			}
		}
	}
	sort.Ints(order)
	for _, index := range order {
		out.ToolCalls = append(out.ToolCalls, indexed[index])
	}
	out.Content = content.String()
	return out, nil
}
