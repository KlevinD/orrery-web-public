package realtime

import (
	"encoding/base64"
	"fmt"
	"strings"
)

const (
	channelConversation = "conversation"
	channelMission      = "mission"
	channelUser         = "user"
	channelRun          = "run"
	channelProject      = "project"
)

func ConversationChannel(id string) (string, error) {
	return buildChannel(channelConversation, id)
}

func MissionChannel(id string) (string, error) {
	return buildChannel(channelMission, id)
}

func UserChannel(id string) (string, error) {
	return buildChannel(channelUser, id)
}

func RunChannel(id string) (string, error) {
	return buildChannel(channelRun, id)
}

func ProjectChannel(tenantID, projectID string) (string, error) {
	tenantID = strings.TrimSpace(tenantID)
	projectID = strings.TrimSpace(projectID)
	if tenantID == "" {
		return "", fmt.Errorf("project channel tenant id is required")
	}
	if projectID == "" {
		return "", fmt.Errorf("project channel project id is required")
	}
	if strings.ContainsRune(tenantID, '\x00') || strings.ContainsRune(projectID, '\x00') {
		return "", fmt.Errorf("project channel identity must not contain NUL")
	}
	payload := base64.RawURLEncoding.EncodeToString([]byte(tenantID + "\x00" + projectID))
	return channelProject + ":" + payload, nil
}

func ParseProjectChannel(channel string) (string, string, error) {
	prefix, payload, ok := strings.Cut(strings.TrimSpace(channel), ":")
	if !ok || prefix != channelProject || strings.TrimSpace(payload) == "" || strings.Contains(payload, ":") {
		return "", "", fmt.Errorf("invalid project channel")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", "", fmt.Errorf("decode project channel: %w", err)
	}
	tenantID, projectID, ok := strings.Cut(string(decoded), "\x00")
	if !ok || strings.TrimSpace(tenantID) != tenantID || strings.TrimSpace(projectID) != projectID || tenantID == "" || projectID == "" || strings.ContainsRune(projectID, '\x00') {
		return "", "", fmt.Errorf("invalid project channel identity")
	}
	canonical, err := ProjectChannel(tenantID, projectID)
	if err != nil || canonical != strings.TrimSpace(channel) {
		return "", "", fmt.Errorf("non-canonical project channel")
	}
	return tenantID, projectID, nil
}

func buildChannel(kind, id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("%s channel id is required", kind)
	}
	if strings.Contains(id, ":") {
		return "", fmt.Errorf("%s channel id must not contain ':'", kind)
	}
	return kind + ":" + id, nil
}
