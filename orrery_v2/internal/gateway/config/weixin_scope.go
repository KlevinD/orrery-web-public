package config

import (
	"encoding/base64"
	"strings"
)

const (
	DefaultTenantID        = "system"
	weixinBindingKeyPrefix = "wxscope:v1:"
)

// WeixinBindingKey returns an opaque key for maps that are scoped by both
// tenant and Weixin user. Legacy unscoped keys are interpreted as system data.
func WeixinBindingKey(tenantID string, userID string) string {
	tenantID = NormalizeTenantID(tenantID)
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return ""
	}
	encode := base64.RawURLEncoding.EncodeToString
	return weixinBindingKeyPrefix + encode([]byte(tenantID)) + ":" + encode([]byte(userID))
}

func ParseWeixinBindingKey(key string) (tenantID string, userID string, ok bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", false
	}
	if !strings.HasPrefix(key, weixinBindingKeyPrefix) {
		return DefaultTenantID, key, true
	}
	parts := strings.Split(strings.TrimPrefix(key, weixinBindingKeyPrefix), ":")
	if len(parts) != 2 {
		return "", "", false
	}
	decode := base64.RawURLEncoding.DecodeString
	tenantRaw, err := decode(parts[0])
	if err != nil {
		return "", "", false
	}
	userRaw, err := decode(parts[1])
	if err != nil {
		return "", "", false
	}
	tenantID = NormalizeTenantID(string(tenantRaw))
	userID = strings.TrimSpace(string(userRaw))
	return tenantID, userID, tenantID != "" && userID != ""
}

func NormalizeTenantID(tenantID string) string {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return DefaultTenantID
	}
	return tenantID
}
