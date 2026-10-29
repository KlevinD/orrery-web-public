package core

import (
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
)

var personalAssistantProfileIDs = struct {
	sync.RWMutex
	byOwner map[string]string
	byID    map[string]string
}{byOwner: map[string]string{}, byID: map[string]string{}}

func RegisterPersonalAssistantProfileID(tenantID, ownerID, profileID string) error {
	ownerKey := personalAssistantProfileOwnerKey(tenantID, ownerID)
	profileID = strings.TrimSpace(profileID)
	if ownerKey == "" || profileID == "" {
		return fmt.Errorf("personal assistant tenant, owner, and AgentProfile ID are required")
	}

	profileKey := strings.ToLower(profileID)
	personalAssistantProfileIDs.Lock()
	defer personalAssistantProfileIDs.Unlock()
	if existingOwner := personalAssistantProfileIDs.byID[profileKey]; existingOwner != "" && existingOwner != ownerKey {
		return fmt.Errorf("personal assistant AgentProfile ID %q is already registered", profileID)
	}
	if previous := personalAssistantProfileIDs.byOwner[ownerKey]; previous != "" && !strings.EqualFold(previous, profileID) {
		delete(personalAssistantProfileIDs.byID, strings.ToLower(previous))
	}
	personalAssistantProfileIDs.byOwner[ownerKey] = profileID
	personalAssistantProfileIDs.byID[profileKey] = ownerKey
	return nil
}

func PersonalAssistantProfileIDForOwner(tenantID, ownerID string) string {
	ownerKey := personalAssistantProfileOwnerKey(tenantID, ownerID)
	if ownerKey == "" {
		return ""
	}
	personalAssistantProfileIDs.RLock()
	defer personalAssistantProfileIDs.RUnlock()
	return personalAssistantProfileIDs.byOwner[ownerKey]
}

func IsPersonalAssistantProfileID(value string) bool {
	value = strings.TrimSpace(value)
	personalAssistantProfileIDs.RLock()
	_, registered := personalAssistantProfileIDs.byID[strings.ToLower(value)]
	personalAssistantProfileIDs.RUnlock()
	if registered {
		return true
	}
	if !strings.HasPrefix(value, personalAssistantProfileIDPrefix) || len(value) != len(personalAssistantProfileIDPrefix)+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, personalAssistantProfileIDPrefix))
	return err == nil
}

func personalAssistantProfileOwnerKey(tenantID, ownerID string) string {
	tenantID = strings.ToLower(strings.TrimSpace(tenantID))
	ownerID = strings.ToLower(strings.TrimSpace(ownerID))
	if tenantID == "" || ownerID == "" {
		return ""
	}
	return tenantID + "\x00" + ownerID
}
