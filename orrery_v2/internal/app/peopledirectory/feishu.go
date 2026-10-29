package peopledirectory

import (
	"context"
	"fmt"
	"strings"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcontact "github.com/larksuite/oapi-sdk-go/v3/service/contact/v3"
)

type UserPageSource interface {
	ListUsers(ctx context.Context, pageToken string, pageSize int) (DirectoryPage, error)
}

type SnapshotStore interface {
	BeginSnapshot(ctx context.Context, snapshot Snapshot) error
	StagePage(ctx context.Context, snapshotID string, users []DirectoryUser, hasMore bool) error
	MarkSnapshotFailed(ctx context.Context, snapshotID string, cause error) error
}

type Scanner struct {
	source   UserPageSource
	store    SnapshotStore
	tenantID string
	pageSize int
}

func NewScanner(source UserPageSource, store SnapshotStore, tenantID string, pageSize int) *Scanner {
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 100
	}
	return &Scanner{source: source, store: store, tenantID: strings.TrimSpace(tenantID), pageSize: pageSize}
}

func NewFeishuScanner(appID, appSecret, tenantID string, store SnapshotStore) *Scanner {
	client := lark.NewClient(strings.TrimSpace(appID), strings.TrimSpace(appSecret))
	return NewScanner(&larkUserPageSource{client: client}, store, tenantID, 100)
}

func (s *Scanner) Scan(ctx context.Context, snapshotID string) error {
	if s == nil || s.source == nil || s.store == nil {
		return fmt.Errorf("People directory scanner is not configured")
	}
	snapshotID = strings.TrimSpace(snapshotID)
	if snapshotID == "" || s.tenantID == "" {
		return fmt.Errorf("snapshot id and tenant id are required")
	}
	if err := s.store.BeginSnapshot(ctx, Snapshot{ID: snapshotID, Source: SourceFeishu, TenantID: s.tenantID}); err != nil {
		return err
	}
	pageToken := ""
	seenTokens := map[string]struct{}{"": {}}
	for {
		page, err := s.source.ListUsers(ctx, pageToken, s.pageSize)
		if err != nil {
			return s.fail(ctx, snapshotID, fmt.Errorf("list Feishu directory users: %w", err))
		}
		if page.HasMore {
			page.NextPageToken = strings.TrimSpace(page.NextPageToken)
			if page.NextPageToken == "" {
				return s.fail(ctx, snapshotID, fmt.Errorf("Feishu directory returned has_more without a page token"))
			}
			if _, repeated := seenTokens[page.NextPageToken]; repeated {
				return s.fail(ctx, snapshotID, fmt.Errorf("Feishu directory repeated page token"))
			}
		}
		if err := s.store.StagePage(ctx, snapshotID, page.Users, page.HasMore); err != nil {
			return s.fail(ctx, snapshotID, err)
		}
		if !page.HasMore {
			return nil
		}
		pageToken = page.NextPageToken
		seenTokens[pageToken] = struct{}{}
	}
}

func (s *Scanner) fail(ctx context.Context, snapshotID string, cause error) error {
	if markErr := s.store.MarkSnapshotFailed(ctx, snapshotID, cause); markErr != nil {
		return fmt.Errorf("%v; mark snapshot failed: %w", cause, markErr)
	}
	return cause
}

type larkUserPageSource struct {
	client *lark.Client
}

func (s *larkUserPageSource) ListUsers(ctx context.Context, pageToken string, pageSize int) (DirectoryPage, error) {
	if s == nil || s.client == nil || s.client.Contact == nil || s.client.Contact.V3 == nil {
		return DirectoryPage{}, fmt.Errorf("Feishu contact client is not configured")
	}
	if pageSize <= 0 || pageSize > 50 {
		pageSize = 50
	}
	builder := larkcontact.NewListScopeReqBuilder().
		UserIdType("open_id").
		DepartmentIdType("open_department_id").
		PageSize(pageSize)
	if pageToken = strings.TrimSpace(pageToken); pageToken != "" {
		builder.PageToken(pageToken)
	}
	resp, err := s.client.Contact.V3.Scope.List(ctx, builder.Build())
	if err != nil {
		return DirectoryPage{}, err
	}
	if resp == nil || !resp.Success() {
		if resp == nil {
			return DirectoryPage{}, fmt.Errorf("Feishu contact API returned an empty response")
		}
		return DirectoryPage{}, fmt.Errorf("Feishu contact API rejected directory scope: code=%d message=%s", resp.Code, resp.Msg)
	}
	if resp.Data == nil {
		return DirectoryPage{}, fmt.Errorf("Feishu contact API returned no directory scope")
	}
	if len(resp.Data.DepartmentIds) > 0 || len(resp.Data.GroupIds) > 0 {
		return DirectoryPage{}, fmt.Errorf("Feishu directory scope contains unsupported department or group authorization")
	}
	page := DirectoryPage{HasMore: boolValue(resp.Data.HasMore), NextPageToken: stringValue(resp.Data.PageToken)}
	if len(resp.Data.UserIds) == 0 {
		return page, nil
	}
	userResp, err := s.client.Contact.V3.User.Batch(ctx, larkcontact.NewBatchUserReqBuilder().
		UserIds(resp.Data.UserIds).
		UserIdType("open_id").
		DepartmentIdType("open_department_id").
		Build())
	if err != nil {
		return DirectoryPage{}, err
	}
	if userResp == nil || !userResp.Success() {
		if userResp == nil {
			return DirectoryPage{}, fmt.Errorf("Feishu contact API returned an empty user response")
		}
		return DirectoryPage{}, fmt.Errorf("Feishu contact API rejected authorized user batch: code=%d message=%s", userResp.Code, userResp.Msg)
	}
	if userResp.Data == nil {
		return DirectoryPage{}, fmt.Errorf("Feishu contact API returned no authorized user data")
	}
	page.Users = make([]DirectoryUser, 0, len(userResp.Data.Items))
	found := make(map[string]struct{}, len(userResp.Data.Items))
	for _, item := range userResp.Data.Items {
		if item == nil {
			continue
		}
		sourceRecordID := stringValue(item.OpenId)
		found[sourceRecordID] = struct{}{}
		page.Users = append(page.Users, DirectoryUser{
			SourceRecordID:  sourceRecordID,
			EmployeeNumber:  stringValue(item.EmployeeNo),
			Name:            stringValue(item.Name),
			Email:           stringValue(item.Email),
			EnterpriseEmail: stringValue(item.EnterpriseEmail),
			Active:          activeFeishuUser(item.Status),
		})
	}
	for _, userID := range resp.Data.UserIds {
		if _, ok := found[strings.TrimSpace(userID)]; !ok {
			return DirectoryPage{}, fmt.Errorf("Feishu contact API omitted an authorized user")
		}
	}
	return page, nil
}

func activeFeishuUser(status *larkcontact.UserStatus) bool {
	return status == nil || (optionalBoolValue(status.IsActivated, true) &&
		!boolValue(status.IsFrozen) && !boolValue(status.IsResigned) &&
		!boolValue(status.IsExited) && !boolValue(status.IsUnjoin))
}

func optionalBoolValue(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func boolValue(value *bool) bool {
	return value != nil && *value
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
