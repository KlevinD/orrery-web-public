package httptransport

import (
	"encoding/json"
	"io"
	"sort"
	"strings"
)

func decodeJSONWithUpdateMask[T any](body io.Reader, target *T, nestedKeys ...string) ([]string, error) {
	raw, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return nil, err
	}
	return jsonUpdateMask(raw, nestedKeys...), nil
}

func jsonUpdateMask(raw []byte, nestedKeys ...string) []string {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return []string{}
	}
	nested := map[string]struct{}{}
	for _, key := range nestedKeys {
		key = strings.TrimSpace(key)
		if key != "" {
			nested[key] = struct{}{}
		}
	}
	mask := make([]string, 0, len(body))
	for _, key := range jsonMaskSortedKeys(body) {
		if _, ok := nested[key]; ok {
			var nestedBody map[string]json.RawMessage
			if err := json.Unmarshal(body[key], &nestedBody); err == nil && len(nestedBody) > 0 {
				for _, nestedKey := range jsonMaskSortedKeys(nestedBody) {
					mask = append(mask, key+"."+nestedKey)
				}
				continue
			}
		}
		mask = append(mask, key)
	}
	if mask == nil {
		return []string{}
	}
	return mask
}

func jsonMaskSortedKeys(raw map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(raw))
	for key := range raw {
		key = strings.TrimSpace(key)
		if key != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}
