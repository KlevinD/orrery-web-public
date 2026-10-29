package main

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/gateway/channel"
	gwprotocol "orrery_v2/internal/gateway/protocol"
)

func productionGatewayViewer(input gwprotocol.SubmitTurnInput, route productionGatewayRoute) discussionsession.ViewerRef {
	senderName := strings.TrimSpace(input.Telemetry["weixin_sender_name"])
	senderID := strings.TrimSpace(input.Telemetry["weixin_sender_id"])
	if input.Identity != nil {
		senderName = firstNonBlank(input.Identity.DisplayName, senderName)
		senderID = firstNonBlank(input.Identity.ExternalUserID, senderID)
	}
	if senderID == "" {
		senderID = gatewayHash(strings.TrimSpace(input.SessionKey))
	}
	displayName := firstNonBlank(senderName, string(input.ChannelKey)+" user")
	if viewerID := strings.TrimSpace(route.ViewerID); viewerID != "" {
		return discussionsession.ViewerRef{
			ViewerID:       viewerID,
			OwnerID:        strings.TrimSpace(route.OwnerID),
			AgentProfileID: viewerID,
			ViewerKind:     discussionsession.ViewerKindUser,
			DisplayName:    displayName,
			TenantID:       strings.TrimSpace(route.TenantID),
		}
	}
	return discussionsession.ViewerRef{
		ViewerID:    "gateway-" + string(input.ChannelKey) + "-" + gatewayHash(senderID),
		ViewerKind:  discussionsession.ViewerKindUser,
		DisplayName: displayName,
	}
}

func gatewayAttachmentIDs(attachments []gwprotocol.Attachment) []string {
	ids := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		if attachment.Metadata == nil {
			continue
		}
		if value, ok := attachment.Metadata["attachment_id"].(string); ok && strings.TrimSpace(value) != "" {
			ids = append(ids, strings.TrimSpace(value))
		}
	}
	return ids
}

func gatewayRunID(event discussionsession.StreamEventDTO) string {
	if event.RunID != "" {
		return event.RunID
	}
	if event.Run != nil {
		return event.Run.RunID
	}
	return ""
}

func gatewayHash(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])[:16]
}

func gatewayStringMapPreview(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, 0, len(values))
	for key, value := range values {
		parts = append(parts, key+"="+value)
		if len(parts) == 3 {
			break
		}
	}
	return strings.Join(parts, ", ")
}

func startedGatewayKeys(started []channel.StartedAdapter) []string {
	keys := make([]string, 0, len(started))
	for _, adapter := range started {
		keys = append(keys, adapter.Key)
	}
	return keys
}
