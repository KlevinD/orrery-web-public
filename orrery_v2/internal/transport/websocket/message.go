package websockettransport

type MessageType string

const (
	MessageSnapshot  MessageType = "snapshot"
	MessagePatch     MessageType = "patch"
	MessageHeartbeat MessageType = "heartbeat"
	MessageError     MessageType = "error"
	MessageComplete  MessageType = "complete"
)

type SubscribeRequest struct {
	MissionID              string   `json:"mission_id"`
	ViewNames              []string `json:"view_names,omitempty"`
	Cursor                 string   `json:"cursor,omitempty"`
	PatchVersion           int      `json:"patch_version,omitempty"`
	SubscriptionID         string   `json:"subscription_id,omitempty"`
	ViewerID               string   `json:"-"`
	ViewerKind             string   `json:"-"`
	TenantID               string   `json:"-"`
	AuthorizationSubjectID string   `json:"-"`
	MissionRoles           []string `json:"-"`
	PermissionScope        []string `json:"-"`
}

type ServerEnvelope struct {
	Type           MessageType `json:"type"`
	SubscriptionID string      `json:"subscription_id,omitempty"`
	Payload        any         `json:"payload,omitempty"`
	Version        int         `json:"version,omitempty"`
	ServerTime     string      `json:"server_time,omitempty"`
}
