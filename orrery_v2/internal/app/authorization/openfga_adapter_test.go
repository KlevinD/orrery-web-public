package authorization

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orrery_v2/internal/app/identity"
)

func TestOpenFGAAdapterChecksRelationWithModelID(t *testing.T) {
	var requestBody openFGACheckRequest
	var authHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/stores/store-1/check" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		authHeader = req.Header.Get("Authorization")
		if err := json.NewDecoder(req.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"allowed":true}`))
	}))
	defer server.Close()
	adapter, err := NewOpenFGAAdapter(OpenFGAConfig{
		APIURL:               server.URL,
		StoreID:              "store-1",
		AuthorizationModelID: "model-1",
		Token:                "secret-token",
		HTTPClient:           server.Client(),
	})
	if err != nil {
		t.Fatalf("NewOpenFGAAdapter: %v", err)
	}
	principal := testPrincipalWithScopes("mission.write")
	principal.AgentProfileID = "human-operator"

	decision, err := adapter.Check(context.Background(), CheckRequest{
		Principal: principal,
		Action:    "mission.edit",
		Object:    identity.ObjectRef{Type: "mission", ID: "mission-1"},
	})

	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !decision.Allowed || decision.Source != DecisionSourceOpenFGA {
		t.Fatalf("decision = %+v", decision)
	}
	if authHeader != "Bearer secret-token" {
		t.Fatalf("authorization header = %q", authHeader)
	}
	if requestBody.AuthorizationModelID != "model-1" {
		t.Fatalf("authorization_model_id = %q", requestBody.AuthorizationModelID)
	}
	if requestBody.TupleKey.User != "user:human-operator" || requestBody.TupleKey.Relation != "editor" || requestBody.TupleKey.Object != "mission:global/mission-1" {
		t.Fatalf("tuple key = %+v", requestBody.TupleKey)
	}
}

func TestOpenFGAObjectRefAlwaysScopesBusinessIDs(t *testing.T) {
	got := openFGAObjectRef("development_project", "tenant-a/project-1", "tenant-a")
	want := "development_project:tenant-a/tenant-a/project-1"
	if got != want {
		t.Fatalf("openFGAObjectRef = %q, want %q", got, want)
	}
}

func TestOpenFGARefsEncodeReservedCharactersAndRoundTrip(t *testing.T) {
	id := "global/global/platform.admin/workforce:human-superadmin:platform.admin"
	got := openFGARef("role", id)
	if strings.Contains(strings.TrimPrefix(got, "role:"), ":") {
		t.Fatalf("encoded OpenFGA id still contains a reserved colon: %q", got)
	}
	kind, decodedID := splitOpenFGARef(got)
	if kind != "role" || decodedID != id {
		t.Fatalf("splitOpenFGARef(%q) = (%q, %q), want (%q, %q)", got, kind, decodedID, "role", id)
	}
}

func TestOpenFGARefsEncodeValuesThatResembleEncodingPrefix(t *testing.T) {
	id := openFGAEncodedIDPrefix + "business-id"
	got := openFGARef("project", id)
	if got == "project:"+id {
		t.Fatalf("prefix-like business id was not escaped: %q", got)
	}
	_, decodedID := splitOpenFGARef(got)
	if decodedID != id {
		t.Fatalf("splitOpenFGARef(%q) decoded id = %q, want %q", got, decodedID, id)
	}
}

func TestOpenFGAAdapterUsesScopeGateBeforeRelationCheck(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		called = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	adapter, err := NewOpenFGAAdapter(OpenFGAConfig{
		APIURL:               server.URL,
		StoreID:              "store-1",
		AuthorizationModelID: "model-1",
		HTTPClient:           server.Client(),
	})
	if err != nil {
		t.Fatalf("NewOpenFGAAdapter: %v", err)
	}

	decision, err := adapter.Check(context.Background(), CheckRequest{
		Principal: testPrincipalWithScopes("mission.read"),
		Action:    "mission.edit",
		Object:    identity.ObjectRef{Type: "mission", ID: "mission-1"},
	})

	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if decision.Allowed || decision.Source != DecisionSourceScope {
		t.Fatalf("decision = %+v", decision)
	}
	if called {
		t.Fatalf("openfga should not be called when coarse scope gate denies")
	}
}

func TestOpenFGAAdapterAlternativeCoarseScopeStillChecksRelation(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		called = true
		if req.URL.Path != "/stores/store-1/check" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"allowed":false}`))
	}))
	defer server.Close()
	adapter, err := NewOpenFGAAdapter(OpenFGAConfig{
		APIURL: server.URL, StoreID: "store-1", AuthorizationModelID: "model-1", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewOpenFGAAdapter: %v", err)
	}

	decision, err := adapter.Check(context.Background(), CheckRequest{
		Principal:               testPrincipalWithScopes("enterprise_graph.read"),
		Action:                  "mission.view",
		Object:                  identity.ObjectRef{Type: "mission", ID: "mission-1", TenantID: "global"},
		TenantID:                "global",
		CoarseScopeAlternatives: []string{"enterprise_graph.read"},
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !called {
		t.Fatal("openfga relation check was not called")
	}
	if decision.Allowed || decision.Source != DecisionSourceOpenFGA {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestOpenFGAAdapterBatchCheckUsesProtocolAndPreservesLocalDecisions(t *testing.T) {
	batchRequests := 0
	singleRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/stores/store-1/batch-check":
			batchRequests++
			var body struct {
				AuthorizationModelID string `json:"authorization_model_id"`
				Checks               []struct {
					TupleKey      openFGATupleKey `json:"tuple_key"`
					CorrelationID string          `json:"correlation_id"`
				} `json:"checks"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode batch request: %v", err)
			}
			if body.AuthorizationModelID != "model-1" || len(body.Checks) != 2 {
				t.Fatalf("batch body = %+v", body)
			}
			_, _ = fmt.Fprintf(w, `{"result":{"%s":{"allowed":true},"%s":{"allowed":false}}}`,
				body.Checks[0].CorrelationID, body.Checks[1].CorrelationID)
		case "/stores/store-1/check":
			singleRequests++
			var body openFGACheckRequest
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode single request: %v", err)
			}
			_, _ = fmt.Fprintf(w, `{"allowed":%t}`, !strings.Contains(body.TupleKey.Object, "mission-2"))
		default:
			t.Fatalf("path = %q", req.URL.Path)
		}
	}))
	defer server.Close()
	adapter, err := NewOpenFGAAdapter(OpenFGAConfig{
		APIURL: server.URL, StoreID: "store-1", AuthorizationModelID: "model-1", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewOpenFGAAdapter: %v", err)
	}
	principal := testPrincipalWithScopes("mission.read")
	principal.AgentProfileID = "human-operator"

	decisions, err := adapter.BatchCheck(context.Background(), []CheckRequest{
		{Principal: principal, Action: "mission.view", Object: identity.ObjectRef{Type: "mission", ID: "mission-1"}},
		{Principal: principal, Action: "mission.view", Object: identity.ObjectRef{Type: "mission", ID: "mission-2"}},
		{Principal: principal, Action: "mission.edit", Object: identity.ObjectRef{Type: "mission", ID: "mission-3"}},
	})
	if err != nil {
		t.Fatalf("BatchCheck: %v", err)
	}
	if len(decisions) != 3 || !decisions[0].Allowed || decisions[1].Allowed || decisions[2].Allowed || decisions[2].Source != DecisionSourceScope {
		t.Fatalf("decisions = %+v", decisions)
	}
	if batchRequests != 1 || singleRequests != 0 {
		t.Fatalf("batch requests = %d, single requests = %d, want 1 and 0", batchRequests, singleRequests)
	}
}

func TestOpenFGAAdapterBatchCheckChunksAtOpenFGALimit(t *testing.T) {
	batchSizes := make([]int, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/stores/store-1/batch-check" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		var body struct {
			Checks []struct {
				CorrelationID string `json:"correlation_id"`
			} `json:"checks"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode batch request: %v", err)
		}
		batchSizes = append(batchSizes, len(body.Checks))
		result := make(map[string]openFGACheckResponse, len(body.Checks))
		for _, check := range body.Checks {
			result[check.CorrelationID] = openFGACheckResponse{Allowed: true}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
	}))
	defer server.Close()
	adapter, err := NewOpenFGAAdapter(OpenFGAConfig{
		APIURL: server.URL, StoreID: "store-1", AuthorizationModelID: "model-1", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewOpenFGAAdapter: %v", err)
	}
	principal := testPrincipalWithScopes("mission.read")
	principal.AgentProfileID = "human-operator"
	requests := make([]CheckRequest, 51)
	for i := range requests {
		requests[i] = CheckRequest{Principal: principal, Action: "mission.view", Object: identity.ObjectRef{Type: "mission", ID: fmt.Sprintf("mission-%d", i)}}
	}

	decisions, err := adapter.BatchCheck(context.Background(), requests)
	if err != nil {
		t.Fatalf("BatchCheck: %v", err)
	}
	if len(decisions) != len(requests) || len(batchSizes) != 2 || batchSizes[0] != 50 || batchSizes[1] != 1 {
		t.Fatalf("decisions=%d batch sizes=%v, want 51 and [50 1]", len(decisions), batchSizes)
	}
}

func TestOpenFGAAdapterBatchCheckRejectsMissingResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.URL.Path == "/stores/store-1/check" {
			_, _ = w.Write([]byte(`{"allowed":true}`))
			return
		}
		_, _ = w.Write([]byte(`{"result":{}}`))
	}))
	defer server.Close()
	adapter, err := NewOpenFGAAdapter(OpenFGAConfig{
		APIURL: server.URL, StoreID: "store-1", AuthorizationModelID: "model-1", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewOpenFGAAdapter: %v", err)
	}
	principal := testPrincipalWithScopes("mission.read")
	principal.AgentProfileID = "human-operator"

	_, err = adapter.BatchCheck(context.Background(), []CheckRequest{{
		Principal: principal, Action: "mission.view", Object: identity.ObjectRef{Type: "mission", ID: "mission-1"},
	}})
	if err == nil {
		t.Fatal("BatchCheck error = nil, want missing result error")
	}
}

func TestOpenFGAAdapterDeniesUnsupportedModelObjectsWithoutRequest(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	adapter, err := NewOpenFGAAdapter(OpenFGAConfig{
		APIURL:               server.URL,
		StoreID:              "store-1",
		AuthorizationModelID: "model-1",
		HTTPClient:           server.Client(),
	})
	if err != nil {
		t.Fatalf("NewOpenFGAAdapter: %v", err)
	}
	principal := testPrincipalWithScopes("roles.read")
	principal.AgentProfileID = "human-operator"

	for _, objectType := range []string{"role_assignment", "workforce", "position", "organization_scope"} {
		decision, err := adapter.Check(context.Background(), CheckRequest{
			Principal: principal,
			Action:    "role.assignment.read",
			Object:    identity.ObjectRef{Type: objectType, ID: "target-1"},
		})
		if err != nil {
			t.Fatalf("Check(%s): %v", objectType, err)
		}
		if decision.Allowed || decision.Source != DecisionSourceOpenFGA || !strings.Contains(decision.Reason, objectType) {
			t.Fatalf("Check(%s) decision = %+v", objectType, decision)
		}
	}
	if requestCount != 0 {
		t.Fatalf("unsupported object checks sent %d requests to OpenFGA", requestCount)
	}
}

func TestOpenFGAAdapterChecksPlatformMembershipForGlobalCapabilities(t *testing.T) {
	var requestBody openFGACheckRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if err := json.NewDecoder(req.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"allowed":true}`))
	}))
	defer server.Close()
	adapter, err := NewOpenFGAAdapter(OpenFGAConfig{
		APIURL:               server.URL,
		StoreID:              "store-1",
		AuthorizationModelID: "model-1",
		HTTPClient:           server.Client(),
	})
	if err != nil {
		t.Fatalf("NewOpenFGAAdapter: %v", err)
	}
	principal := testPrincipalWithScopes("settings.manage")
	principal.AgentProfileID = "human-operator"

	decision, err := adapter.Check(context.Background(), CheckRequest{
		Principal: principal,
		Action:    "settings.manage",
		Object:    identity.ObjectRef{Type: "platform", ID: "global", TenantID: "global"},
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("decision = %+v", decision)
	}
	if requestBody.TupleKey.User != "user:human-operator" || requestBody.TupleKey.Relation != "member" || requestBody.TupleKey.Object != "platform:global/global" {
		t.Fatalf("tuple key = %+v", requestBody.TupleKey)
	}
}

func TestOpenFGAAdapterWritesRelationsAndListsObjects(t *testing.T) {
	var writeBody openFGAWriteRequest
	var deleteBody openFGAWriteRequest
	var listBody openFGAListObjectsRequest
	var checkBody openFGACheckRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/stores/store-1/write":
			if writeBody.Writes == nil {
				if err := json.NewDecoder(req.Body).Decode(&writeBody); err != nil {
					t.Fatalf("decode write request: %v", err)
				}
			} else {
				if err := json.NewDecoder(req.Body).Decode(&deleteBody); err != nil {
					t.Fatalf("decode delete request: %v", err)
				}
			}
			w.WriteHeader(http.StatusOK)
		case "/stores/store-1/list-objects":
			if err := json.NewDecoder(req.Body).Decode(&listBody); err != nil {
				t.Fatalf("decode list request: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"objects":["mission:tenant-1/mission-1","mission:tenant-1/mission-2","mission:tenant-2/mission-hidden"]}`))
		case "/stores/store-1/check":
			if err := json.NewDecoder(req.Body).Decode(&checkBody); err != nil {
				t.Fatalf("decode check request: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"allowed":true}`))
		default:
			t.Fatalf("path = %q", req.URL.Path)
		}
	}))
	defer server.Close()
	adapter, err := NewOpenFGAAdapter(OpenFGAConfig{
		APIURL:                server.URL,
		StoreID:               "store-1",
		AuthorizationModelID:  "model-1",
		HTTPClient:            server.Client(),
		IgnoreDuplicateWrites: true,
	})
	if err != nil {
		t.Fatalf("NewOpenFGAAdapter: %v", err)
	}

	err = adapter.WriteRelations(context.Background(), []RelationTuple{{
		Subject:  SubjectRef{Kind: "user", ID: "operator"},
		Relation: "editor",
		Object:   identity.ObjectRef{Type: "mission", ID: "mission-1"},
	}})
	if err != nil {
		t.Fatalf("WriteRelations: %v", err)
	}
	if writeBody.AuthorizationModelID != "model-1" || writeBody.Writes == nil || writeBody.Writes.OnDuplicate != "ignore" || len(writeBody.Writes.TupleKeys) != 1 {
		t.Fatalf("write body = %+v", writeBody)
	}
	if writeBody.Writes.TupleKeys[0].User != "user:operator" || writeBody.Writes.TupleKeys[0].Object != "mission:global/mission-1" {
		t.Fatalf("write tuple = %+v", writeBody.Writes.TupleKeys[0])
	}
	err = adapter.DeleteRelations(context.Background(), []RelationTuple{{
		Subject:  SubjectRef{Kind: "user", ID: "operator"},
		Relation: "editor",
		Object:   identity.ObjectRef{Type: "mission", ID: "mission-1"},
	}})
	if err != nil {
		t.Fatalf("DeleteRelations: %v", err)
	}
	if deleteBody.AuthorizationModelID != "model-1" || deleteBody.Deletes == nil || deleteBody.Deletes.OnMissing != "ignore" || len(deleteBody.Deletes.TupleKeys) != 1 {
		t.Fatalf("delete body = %+v", deleteBody)
	}
	if deleteBody.Deletes.TupleKeys[0].User != "user:operator" || deleteBody.Deletes.TupleKeys[0].Object != "mission:global/mission-1" {
		t.Fatalf("delete tuple = %+v", deleteBody.Deletes.TupleKeys[0])
	}

	listPrincipal := testPrincipalWithScopes("mission.read")
	listPrincipal.AgentProfileID = "human-operator"
	objects, err := adapter.ListObjects(context.Background(), listPrincipal, "viewer", "mission", "tenant-1")
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	if listBody.AuthorizationModelID != "model-1" || listBody.User != "user:human-operator" || listBody.Relation != "viewer" || listBody.Type != "mission" {
		t.Fatalf("list body = %+v", listBody)
	}
	if len(objects) != 2 || objects[0].ID != "mission-1" || objects[0].TenantID != "tenant-1" {
		t.Fatalf("objects = %+v", objects)
	}

	allowed, err := adapter.CheckRelation(context.Background(), RelationTuple{
		Subject:  SubjectRef{Kind: "agent", ID: "agent-1"},
		Relation: "operator",
		Object:   identity.ObjectRef{Type: "resource", ID: "tool.deploy"},
	})
	if err != nil {
		t.Fatalf("CheckRelation: %v", err)
	}
	if !allowed {
		t.Fatalf("CheckRelation allowed = false")
	}
	if checkBody.AuthorizationModelID != "model-1" || checkBody.TupleKey.User != "agent:agent-1" || checkBody.TupleKey.Relation != "operator" || checkBody.TupleKey.Object != "resource:global/tool.deploy" {
		t.Fatalf("check body = %+v", checkBody)
	}
}

func TestOpenFGAAdapterListRelationsRestoresTenantScopedTuples(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/stores/store-1/read" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		if requestCount == 1 {
			_, _ = w.Write([]byte(`{"tuples":[
				{"key":{"user":"user:human-a","relation":"owner","object":"development_project:tenant-a/project-1"}},
				{"key":{"user":"organization:tenant-a/org-1","relation":"org","object":"development_project:tenant-a/project-1"}},
				{"key":{"user":"development_project:tenant-b/project-2","relation":"user","object":"scm_connection:tenant-b/connection-1"}},
				{"key":{"user":"organization:tenant-other/org-2","relation":"org","object":"development_project:tenant-a/project-invalid"}},
				{"key":{"user":"user:legacy","relation":"owner","object":"project:unscoped"}}
			],"continuation_token":"next"}`))
			return
		}
		_, _ = w.Write([]byte(`{"tuples":[{"key":{"user":"agent:agent-b","relation":"executor","object":"development_project:tenant-b/project-2"}}]}`))
	}))
	defer server.Close()
	adapter, err := NewOpenFGAAdapter(OpenFGAConfig{
		APIURL: server.URL, StoreID: "store-1", AuthorizationModelID: "model-1", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewOpenFGAAdapter: %v", err)
	}

	tuples, err := adapter.ListRelations(context.Background())
	if err != nil {
		t.Fatalf("ListRelations: %v", err)
	}
	if requestCount != 2 {
		t.Fatalf("read request count = %d, want 2", requestCount)
	}
	want := []RelationTuple{
		{TenantID: "tenant-a", Subject: SubjectRef{Kind: "user", ID: "human-a"}, Relation: "owner", Object: identity.ObjectRef{Type: "development_project", ID: "project-1", TenantID: "tenant-a"}},
		{TenantID: "tenant-a", Subject: SubjectRef{Kind: "organization", ID: "org-1"}, Relation: "org", Object: identity.ObjectRef{Type: "development_project", ID: "project-1", TenantID: "tenant-a"}},
		{TenantID: "tenant-b", Subject: SubjectRef{Kind: "development_project", ID: "project-2"}, Relation: "user", Object: identity.ObjectRef{Type: "scm_connection", ID: "connection-1", TenantID: "tenant-b"}},
		{TenantID: "tenant-b", Subject: SubjectRef{Kind: "agent", ID: "agent-b"}, Relation: "executor", Object: identity.ObjectRef{Type: "development_project", ID: "project-2", TenantID: "tenant-b"}},
	}
	if len(tuples) != len(want) {
		t.Fatalf("tuples = %+v, want %+v", tuples, want)
	}
	for i := range want {
		if tuples[i] != want[i] {
			t.Fatalf("tuple %d = %+v, want %+v", i, tuples[i], want[i])
		}
	}
}

func TestOpenFGAAdapterPrechecksRoleOrgTupleWhenDuplicateWritesAreStrict(t *testing.T) {
	var writeBody openFGAWriteRequest
	checks := 0
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/stores/store-1/check":
			checks++
			var body openFGACheckRequest
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode check request: %v", err)
			}
			if body.TupleKey.User != "organization:global/org-1" || body.TupleKey.Relation != "org" || body.TupleKey.Object != "role:global/org-1/org.owner" {
				t.Fatalf("check tuple = %+v", body.TupleKey)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"allowed":true}`))
		case "/stores/store-1/write":
			writes++
			if err := json.NewDecoder(req.Body).Decode(&writeBody); err != nil {
				t.Fatalf("decode write request: %v", err)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("path = %q", req.URL.Path)
		}
	}))
	defer server.Close()
	adapter, err := NewOpenFGAAdapter(OpenFGAConfig{
		APIURL:               server.URL,
		StoreID:              "store-1",
		AuthorizationModelID: "model-1",
		HTTPClient:           server.Client(),
	})
	if err != nil {
		t.Fatalf("NewOpenFGAAdapter: %v", err)
	}

	err = adapter.WriteRelations(context.Background(), []RelationTuple{
		{
			Subject:  SubjectRef{Kind: "organization", ID: "org-1"},
			Relation: "org",
			Object:   identity.ObjectRef{Type: "role", ID: "org-1/org.owner"},
		},
		{
			Subject:  SubjectRef{Kind: "user", ID: "human-owner"},
			Relation: "assignee",
			Object:   identity.ObjectRef{Type: "role", ID: "org-1/org.owner"},
		},
		{
			Subject:  SubjectRef{Kind: "user", ID: "human-owner"},
			Relation: "assignee",
			Object:   identity.ObjectRef{Type: "role", ID: "org-1/org.owner"},
		},
	})
	if err != nil {
		t.Fatalf("WriteRelations: %v", err)
	}
	if checks != 1 || writes != 1 {
		t.Fatalf("checks=%d writes=%d, want 1 check and 1 write", checks, writes)
	}
	if writeBody.Writes == nil || writeBody.Writes.OnDuplicate != "" || len(writeBody.Writes.TupleKeys) != 1 {
		t.Fatalf("write body = %+v", writeBody)
	}
	if writeBody.Writes.TupleKeys[0].User != "user:human-owner" || writeBody.Writes.TupleKeys[0].Relation != "assignee" || writeBody.Writes.TupleKeys[0].Object != "role:global/org-1/org.owner" {
		t.Fatalf("written tuple = %+v", writeBody.Writes.TupleKeys[0])
	}
}

func TestOpenFGAAdapterBatchesRelationWritesAtOpenFGALimit(t *testing.T) {
	requests := make([]openFGAWriteRequest, 0, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body openFGAWriteRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode write request: %v", err)
		}
		requests = append(requests, body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	adapter, err := NewOpenFGAAdapter(OpenFGAConfig{
		APIURL:                server.URL,
		StoreID:               "store-1",
		AuthorizationModelID:  "model-1",
		HTTPClient:            server.Client(),
		IgnoreDuplicateWrites: true,
	})
	if err != nil {
		t.Fatalf("NewOpenFGAAdapter: %v", err)
	}

	if err := adapter.WriteRelations(context.Background(), openFGATestRelations(205)); err != nil {
		t.Fatalf("WriteRelations: %v", err)
	}

	if len(requests) != 3 {
		t.Fatalf("write requests = %d, want 3", len(requests))
	}
	for i, want := range []int{100, 100, 5} {
		if requests[i].AuthorizationModelID != "model-1" || requests[i].Writes == nil || len(requests[i].Writes.TupleKeys) != want {
			t.Fatalf("write request %d = %+v, want %d tuples", i+1, requests[i], want)
		}
		if requests[i].Writes.OnDuplicate != "ignore" {
			t.Fatalf("write request %d on_duplicate = %q", i+1, requests[i].Writes.OnDuplicate)
		}
	}
}

func TestOpenFGAAdapterBatchesRelationDeletesAtOpenFGALimit(t *testing.T) {
	batchSizes := make([]int, 0, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body openFGAWriteRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode delete request: %v", err)
		}
		if body.Deletes == nil {
			t.Fatal("delete request did not contain deletes")
		}
		batchSizes = append(batchSizes, len(body.Deletes.TupleKeys))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	adapter, err := NewOpenFGAAdapter(OpenFGAConfig{
		APIURL:               server.URL,
		StoreID:              "store-1",
		AuthorizationModelID: "model-1",
		HTTPClient:           server.Client(),
	})
	if err != nil {
		t.Fatalf("NewOpenFGAAdapter: %v", err)
	}

	if err := adapter.DeleteRelations(context.Background(), openFGATestRelations(205)); err != nil {
		t.Fatalf("DeleteRelations: %v", err)
	}

	want := []int{100, 100, 5}
	if len(batchSizes) != len(want) {
		t.Fatalf("delete requests = %d, want %d", len(batchSizes), len(want))
	}
	for i := range want {
		if batchSizes[i] != want[i] {
			t.Fatalf("delete request %d tuples = %d, want %d", i+1, batchSizes[i], want[i])
		}
	}
}

func TestOpenFGAAdapterStopsAfterRelationBatchFailure(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requestCount++
		if requestCount == 2 {
			http.Error(w, "batch rejected", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	adapter, err := NewOpenFGAAdapter(OpenFGAConfig{
		APIURL:                server.URL,
		StoreID:               "store-1",
		AuthorizationModelID:  "model-1",
		HTTPClient:            server.Client(),
		IgnoreDuplicateWrites: true,
	})
	if err != nil {
		t.Fatalf("NewOpenFGAAdapter: %v", err)
	}

	err = adapter.WriteRelations(context.Background(), openFGATestRelations(205))
	if err == nil || !strings.Contains(err.Error(), "openfga write batch 2/3") || !strings.Contains(err.Error(), "openfga returned 400") {
		t.Fatalf("WriteRelations error = %v", err)
	}
	if requestCount != 2 {
		t.Fatalf("write requests = %d, want 2", requestCount)
	}
}

func openFGATestRelations(count int) []RelationTuple {
	tuples := make([]RelationTuple, 0, count)
	for i := 0; i < count; i++ {
		tuples = append(tuples, RelationTuple{
			Subject:  SubjectRef{Kind: "user", ID: fmt.Sprintf("operator-%03d", i)},
			Relation: "editor",
			Object:   identity.ObjectRef{Type: "mission", ID: "mission-1"},
		})
	}
	return tuples
}

func TestOpenFGAAdapterRejectsIncompleteConfig(t *testing.T) {
	if _, err := NewOpenFGAAdapter(OpenFGAConfig{APIURL: "https://fga.example", StoreID: "store-1"}); err == nil {
		t.Fatalf("expected missing authorization_model_id error")
	}
}

func testPrincipalWithScopes(scopes ...string) identity.Principal {
	return identity.Principal{
		SubjectProvider: identity.ProviderLocal,
		Subject:         "operator",
		UserID:          "operator",
		Username:        "operator",
		ViewerKind:      identity.ViewerKindUser,
		CoarseScopes:    scopes,
	}
}
