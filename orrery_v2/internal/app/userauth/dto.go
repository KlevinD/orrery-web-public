package userauth

import "time"

type BeginLoginRequest struct {
	Username  string `json:"username"`
	IPAddress string `json:"-"`
}

type LoginChallengeDTO struct {
	ChallengeID string    `json:"challenge_id"`
	Username    string    `json:"username"`
	Salt        string    `json:"salt"`
	ExpiresAt   time.Time `json:"expires_at,omitempty"`
}

type CompleteLoginRequest struct {
	Username       string `json:"username"`
	ChallengeID    string `json:"challenge_id"`
	PasswordDigest string `json:"password_digest"`
	Password       string `json:"password,omitempty"`
	MFACode        string `json:"mfa_code,omitempty"`
	IPAddress      string `json:"-"`
	UserAgent      string `json:"-"`
}

type UserDTO struct {
	UserID         string `json:"user_id"`
	Username       string `json:"username"`
	DisplayName    string `json:"display_name"`
	Nickname       string `json:"nickname,omitempty"`
	Email          string `json:"email,omitempty"`
	AgentProfileID string `json:"agent_profile_id"`
	Role           string `json:"role,omitempty"`
}

type SessionDTO struct {
	AccessToken        string    `json:"access_token,omitempty"`
	SubjectProvider    string    `json:"subject_provider,omitempty"`
	Subject            string    `json:"subject,omitempty"`
	IdentitySubjectID  string    `json:"identity_subject_id,omitempty"`
	User               UserDTO   `json:"user"`
	PermissionScope    []string  `json:"permission_scope"`
	ExpiresAt          time.Time `json:"expires_at,omitempty"`
	SessionID          string    `json:"session_id,omitempty"`
	CreatedAt          time.Time `json:"created_at,omitempty"`
	LastSeenAt         time.Time `json:"last_seen_at,omitempty"`
	MustChangePassword bool      `json:"must_change_password,omitempty"`
	RotationRequired   bool      `json:"rotation_required,omitempty"`
}

type LoginChallenge struct {
	ChallengeID string
	Username    string
	IPAddress   string
	Salt        string
	ExpiresAt   time.Time
}

type SessionRecord struct {
	AccessToken        string
	Username           string
	SubjectProvider    string
	Subject            string
	IdentitySubjectID  string
	CredentialVersion  string
	ExpiresAt          time.Time
	SessionID          string
	CreatedAt          time.Time
	LastSeenAt         time.Time
	MustChangePassword bool
	IPAddress          string
	UserAgent          string
}

type ChangePasswordRequest struct {
	NewPassword string `json:"new_password"`
}

type SessionSummaryDTO struct {
	SessionID  string    `json:"session_id"`
	Current    bool      `json:"current"`
	IPAddress  string    `json:"ip_address,omitempty"`
	UserAgent  string    `json:"user_agent,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type SessionPageDTO struct {
	Sessions []SessionSummaryDTO `json:"sessions"`
}

type IPBlockRecord struct {
	IPAddress    string    `json:"ip_address"`
	Status       string    `json:"status"`
	FailureCount int       `json:"failure_count"`
	LastUsername string    `json:"last_username,omitempty"`
	LastReason   string    `json:"last_reason,omitempty"`
	LockedAt     time.Time `json:"locked_at,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitempty"`
	UnlockedAt   time.Time `json:"unlocked_at,omitempty"`
	UnlockedBy   string    `json:"unlocked_by,omitempty"`
	UnlockReason string    `json:"unlock_reason,omitempty"`
}

type IPBlockQuery struct {
	Status string
	Limit  int
	Offset int
}

type UsernameBlockRecord struct {
	Username     string
	Status       string
	FailureCount int
	LastReason   string
	LockedAt     time.Time
	UpdatedAt    time.Time
}

type IPBlockPageDTO struct {
	Locks []IPBlockRecord `json:"locks"`
	Page  PageDTO         `json:"page"`
}

type PageDTO struct {
	Limit    int  `json:"limit"`
	Offset   int  `json:"offset"`
	Total    int  `json:"total"`
	Returned int  `json:"returned"`
	HasMore  bool `json:"has_more"`
}
