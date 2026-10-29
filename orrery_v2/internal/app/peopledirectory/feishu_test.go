package peopledirectory

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	lark "github.com/larksuite/oapi-sdk-go/v3"

	"orrery_v2/internal/platform/migration"
	"orrery_v2/internal/platform/testkit"
)

func TestFeishuScannerStagesTwoPagesBeforeCompletingSnapshot(t *testing.T) {
	ctx := context.Background()
	database := testkit.NewTestDB(t)
	if err := migration.NewRunner([]migration.Migration{directoryMigration()}).Up(ctx, database); err != nil {
		t.Fatalf("apply directory migration: %v", err)
	}
	store := NewStore(database)
	source := &fakePageSource{
		pages: map[string]DirectoryPage{
			"": {
				Users:   []DirectoryUser{{SourceRecordID: "ou_1", EmployeeNumber: " e-001 ", Name: "屈正", EnterpriseEmail: "QUZHENG@EXAMPLE.COM", Active: true}},
				HasMore: true, NextPageToken: "page-2",
			},
			"page-2": {
				Users: []DirectoryUser{{SourceRecordID: "ou_2", EmployeeNumber: "E-002", Name: "李皮", EnterpriseEmail: "lipi@example.com", Active: true}},
			},
		},
		beforeSecondPage: func() {
			snapshot, err := store.Snapshot(ctx, "snapshot-1")
			if err != nil {
				t.Fatalf("load partial snapshot: %v", err)
			}
			if snapshot.Complete || snapshot.Status != SnapshotStatusScanning || snapshot.PageCount != 1 {
				t.Fatalf("partial snapshot = %+v", snapshot)
			}
			if err := store.SaveMatchResults(ctx, "snapshot-1", []MatchResult{{SourceRecordID: "ou_1", Action: MatchActionCreate}}); !errors.Is(err, ErrSnapshotIncomplete) {
				t.Fatalf("SaveMatchResults partial error = %v, want ErrSnapshotIncomplete", err)
			}
		},
	}
	scanner := NewScanner(source, store, "tenant-1", 50)

	if err := scanner.Scan(ctx, "snapshot-1"); err != nil {
		t.Fatalf("scan directory: %v", err)
	}
	snapshot, err := store.Snapshot(ctx, "snapshot-1")
	if err != nil {
		t.Fatalf("load complete snapshot: %v", err)
	}
	if !snapshot.Complete || snapshot.Status != SnapshotStatusComplete || snapshot.PageCount != 2 {
		t.Fatalf("complete snapshot = %+v", snapshot)
	}
	users, err := store.StagedUsers(ctx, "snapshot-1")
	if err != nil {
		t.Fatalf("load staged users: %v", err)
	}
	if len(users) != 2 || users[0].EmployeeNumber != "E-001" || users[0].EnterpriseEmail != "quzheng@example.com" {
		t.Fatalf("staged users = %+v", users)
	}
	if got := source.tokens; len(got) != 2 || got[0] != "" || got[1] != "page-2" {
		t.Fatalf("page tokens = %v", got)
	}
}

func TestFeishuScannerLeavesFailedSnapshotIncomplete(t *testing.T) {
	ctx := context.Background()
	database := testkit.NewTestDB(t)
	if err := migration.NewRunner([]migration.Migration{directoryMigration()}).Up(ctx, database); err != nil {
		t.Fatalf("apply directory migration: %v", err)
	}
	store := NewStore(database)
	source := &fakePageSource{
		pages: map[string]DirectoryPage{"": {Users: []DirectoryUser{{SourceRecordID: "ou_1", Active: true}}, HasMore: true, NextPageToken: "page-2"}},
		errs:  map[string]error{"page-2": errors.New("temporary Feishu failure")},
	}

	err := NewScanner(source, store, "tenant-1", 50).Scan(ctx, "snapshot-failed")
	if err == nil {
		t.Fatal("Scan error = nil")
	}
	snapshot, loadErr := store.Snapshot(ctx, "snapshot-failed")
	if loadErr != nil {
		t.Fatalf("load failed snapshot: %v", loadErr)
	}
	if snapshot.Complete || snapshot.Status != SnapshotStatusFailed {
		t.Fatalf("failed snapshot = %+v", snapshot)
	}
	if _, matchErr := MatchSnapshot(snapshot, nil, nil); !errors.Is(matchErr, ErrSnapshotIncomplete) {
		t.Fatalf("MatchSnapshot partial error = %v", matchErr)
	}
}

func TestLarkUserPageSourceListsExplicitlyAuthorizedUsers(t *testing.T) {
	server := newFeishuDirectoryServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/contact/v3/scopes":
			if got := r.URL.Query().Get("user_id_type"); got != "open_id" {
				t.Errorf("scope user_id_type = %q, want open_id", got)
			}
			fmt.Fprint(w, `{"code":0,"msg":"ok","data":{"user_ids":["ou_1","ou_2"],"has_more":false}}`)
		case "/open-apis/contact/v3/users/batch":
			if got := r.URL.Query()["user_ids"]; !slices.Equal(got, []string{"ou_1", "ou_2"}) {
				t.Errorf("batch user_ids = %v", got)
			}
			fmt.Fprint(w, `{"code":0,"msg":"ok","data":{"items":[{"open_id":"ou_1","employee_no":"E-001","name":"Alice","enterprise_email":"alice@example.com"},{"open_id":"ou_2","employee_no":"E-002","name":"Bob","email":"bob@example.com"}]}}`)
		default:
			http.Error(w, "unexpected Feishu API path", http.StatusNotFound)
		}
	})
	defer server.Close()

	page, err := newTestLarkUserPageSource(server.URL).ListUsers(context.Background(), "", 50)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if page.HasMore || page.NextPageToken != "" || len(page.Users) != 2 {
		t.Fatalf("directory page = %+v", page)
	}
	if page.Users[0].SourceRecordID != "ou_1" || page.Users[0].EnterpriseEmail != "alice@example.com" || !page.Users[0].Active {
		t.Fatalf("first directory user = %+v", page.Users[0])
	}
}

func TestLarkUserPageSourceRejectsUnsupportedAuthorizedDepartments(t *testing.T) {
	server := newFeishuDirectoryServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/open-apis/contact/v3/scopes" {
			http.Error(w, "unexpected Feishu API path", http.StatusNotFound)
			return
		}
		fmt.Fprint(w, `{"code":0,"msg":"ok","data":{"department_ids":["od_1"],"has_more":false}}`)
	})
	defer server.Close()

	_, err := newTestLarkUserPageSource(server.URL).ListUsers(context.Background(), "", 50)
	if err == nil || !strings.Contains(err.Error(), "department") {
		t.Fatalf("ListUsers error = %v, want unsupported department scope", err)
	}
}

func newTestLarkUserPageSource(baseURL string) *larkUserPageSource {
	client := lark.NewClient("app-id", "app-secret", lark.WithOpenBaseUrl(baseURL), lark.WithOAuthBaseUrl(baseURL))
	return &larkUserPageSource{client: client}
}

func newFeishuDirectoryServer(t *testing.T, contactHandler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "tenant_access_token") {
			fmt.Fprint(w, `{"code":0,"msg":"ok","tenant_access_token":"test-token","expire":7200}`)
			return
		}
		contactHandler(w, r)
	}))
}

type fakePageSource struct {
	pages            map[string]DirectoryPage
	errs             map[string]error
	tokens           []string
	beforeSecondPage func()
}

func (f *fakePageSource) ListUsers(_ context.Context, pageToken string, _ int) (DirectoryPage, error) {
	if len(f.tokens) == 1 && f.beforeSecondPage != nil {
		f.beforeSecondPage()
	}
	f.tokens = append(f.tokens, pageToken)
	if err := f.errs[pageToken]; err != nil {
		return DirectoryPage{}, err
	}
	return f.pages[pageToken], nil
}
