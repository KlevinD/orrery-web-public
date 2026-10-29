package discussionsession

import (
	"encoding/json"
	"fmt"

	"orrery_v2/internal/platform/db"
)

func encodePayload(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode discussion payload: %w", err)
	}
	return string(payload), nil
}

func decodePayload(record db.Record, out any) error {
	raw, ok := record[conversationPayload].(string)
	if !ok || raw == "" {
		return fmt.Errorf("discussion repository payload missing")
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return fmt.Errorf("decode discussion payload: %w", err)
	}
	return nil
}
