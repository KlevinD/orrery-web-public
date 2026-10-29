package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	resourceadmin "orrery_v2/internal/app/resourceadmin"
)

func TestHTTPRoutesResourceMCPCommandsThroughResourceCommandService(t *testing.T) {
	command := &fakeResourceCommand{
		mcp:    resourceadmin.MCPServerDTO{ToolDTO: resourceadmin.ToolDTO{ToolID: "mcp.github", Name: "GitHub MCP", Enabled: true}, ServerName: "github"},
		health: resourceadmin.MCPServerHealthDTO{ToolID: "mcp.github", Status: "configured", Mode: "startup"},
		sync:   resourceadmin.MCPServerSyncDTO{ToolID: "mcp.github", Status: "synced"},
	}
	handler := newTestRouter(RouterOptions{ResourceCommand: command})

	create := authedResourceRequest(http.MethodPost, "/api/resources/mcp-servers", `{"tool_id":"mcp.frontend","name":"GitHub MCP","server_name":"github","command":"github-mcp-server"}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, create)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.registerMCP.ToolID != "" || command.registerMCP.ServerName != "github" {
		t.Fatalf("register mcp request = %+v", command.registerMCP)
	}
	var mcp resourceadmin.MCPServerDTO
	if err := json.NewDecoder(rec.Body).Decode(&mcp); err != nil {
		t.Fatalf("decode mcp: %v", err)
	}
	if mcp.ToolID != "mcp.github" {
		t.Fatalf("mcp response = %+v", mcp)
	}

	update := authedResourceRequest(http.MethodPatch, "/api/resources/mcp-servers/mcp.github", `{"name":"GitHub MCP","server_name":"github","command":"github-mcp-server","version":"1.1.0","call_timeout_seconds":60}`)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, update)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.updateMCP.ToolID != "mcp.github" || command.updateMCP.Version != "1.1.0" || command.updateMCP.CallTimeoutSeconds != 60 {
		t.Fatalf("update mcp request = %+v", command.updateMCP)
	}
	if !hasString(command.updateMCP.UpdateMask, "name") || !hasString(command.updateMCP.UpdateMask, "version") || !hasString(command.updateMCP.UpdateMask, "call_timeout_seconds") || hasString(command.updateMCP.UpdateMask, "headers") {
		t.Fatalf("update mcp mask = %+v", command.updateMCP.UpdateMask)
	}

	disable := authedResourceRequest(http.MethodPost, "/api/resources/mcp-servers/mcp.github/disable", `{}`)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, disable)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("disable status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.disabledTool != "mcp.github" {
		t.Fatalf("disabled tool = %q", command.disabledTool)
	}

	deleteReq := authedResourceRequest(http.MethodDelete, "/api/resources/mcp-servers/mcp.github", "")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, deleteReq)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("delete status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.deletedMCP != "mcp.github" {
		t.Fatalf("deleted mcp = %q", command.deletedMCP)
	}

	sync := authedResourceRequest(http.MethodPost, "/api/resources/mcp-servers/mcp.github/sync", `{}`)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, sync)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("sync status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.syncedMCP != "mcp.github" {
		t.Fatalf("synced mcp = %q", command.syncedMCP)
	}

	health := authedResourceRequest(http.MethodGet, "/api/resources/mcp-servers/mcp.github/health", "")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, health)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.healthMCP != "mcp.github" {
		t.Fatalf("health mcp = %q", command.healthMCP)
	}
}

func TestHTTPRoutesResourceToolCommandsThroughResourceCommandService(t *testing.T) {
	command := &fakeResourceCommand{
		tool: resourceadmin.ToolDTO{ToolID: "tool.deploy", Kind: "cli_tool", Name: "Deploy", Version: "1.1.0", Enabled: true},
	}
	handler := newTestRouter(RouterOptions{ResourceCommand: command})

	create := authedResourceRequest(http.MethodPost, "/api/resources/tools", `{"tool_id":"tool.frontend","kind":"cli_tool","name":"Deploy","version":"1.0.0","command":"deploy","risk_level":"high"}`)
	create.Header.Set("X-Request-ID", "corr-resource-tool")
	create.Header.Set("Idempotency-Key", "idem-resource-tool")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, create)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.registeredTool.ToolID != "" || command.registeredTool.Name != "Deploy" || command.lastViewer.RequestID != "corr-resource-tool" {
		t.Fatalf("registered tool = %+v viewer=%+v", command.registeredTool, command.lastViewer)
	}
	var tool resourceadmin.ToolDTO
	if err := json.NewDecoder(rec.Body).Decode(&tool); err != nil {
		t.Fatalf("decode tool: %v", err)
	}
	if tool.ToolID != "tool.deploy" {
		t.Fatalf("tool response = %+v", tool)
	}

	update := authedResourceRequest(http.MethodPatch, "/api/resources/tools/tool.deploy", `{"kind":"cli_tool","name":"Deploy v2","version":"1.1.0","command":"deploy-v2"}`)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, update)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.updatedTool.ToolID != "tool.deploy" || command.updatedTool.Name != "Deploy v2" || command.updatedTool.Command != "deploy-v2" {
		t.Fatalf("updated tool = %+v", command.updatedTool)
	}
	if !hasString(command.updatedTool.UpdateMask, "name") || !hasString(command.updatedTool.UpdateMask, "command") || hasString(command.updatedTool.UpdateMask, "description") {
		t.Fatalf("updated tool mask = %+v", command.updatedTool.UpdateMask)
	}

	disable := authedResourceRequest(http.MethodPost, "/api/resources/tools/tool.deploy/disable", `{}`)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, disable)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("disable status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.disabledTool != "tool.deploy" {
		t.Fatalf("disabled tool = %q", command.disabledTool)
	}

	deleteReq := authedResourceRequest(http.MethodDelete, "/api/resources/tools/tool.deploy", "")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, deleteReq)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("delete status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.deletedTool != "tool.deploy" {
		t.Fatalf("deleted tool = %q", command.deletedTool)
	}
}

func TestHTTPRoutesResourceSkillCommandsThroughResourceCommandService(t *testing.T) {
	command := &fakeResourceCommand{
		skill:       resourceadmin.SkillDTO{SkillID: "skill.review", Name: "Review", Version: "1.1.0", Enabled: true},
		skillDetail: resourceadmin.SkillDetailDTO{Skill: resourceadmin.SkillDTO{SkillID: "skill.review", Name: "Review", Version: "1.1.0", Enabled: true}},
		pkg:         resourceadmin.SkillPackageDTO{FileName: "skill.review.skill.zip", ContentType: "application/zip", Bytes: []byte("zip-bytes")},
	}
	handler := newTestRouter(RouterOptions{ResourceCommand: command})

	create := authedResourceRequest(http.MethodPost, "/api/resources/skills", `{"name":"Review","description":"Review code","version":"1.1.0","task_type_tags":["review"],"prompt":"# Prompt"}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, create)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.createSkill.Name != "Review" || command.createSkill.Prompt != "# Prompt" || len(command.createSkill.TaskTypeTags) != 1 {
		t.Fatalf("create skill request = %+v", command.createSkill)
	}

	candidate := authedResourceRequest(http.MethodPost, "/api/resources/skills/skill.review/versions", `{"definition":{"name":"Review","version":"2.0.0","enabled":true}}`)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, candidate)
	if rec.Code != http.StatusCreated {
		t.Fatalf("candidate status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.skillVersionReq.Definition.ID != "skill.review" || command.skillVersionReq.Definition.Version != "2.0.0" {
		t.Fatalf("candidate request = %+v", command.skillVersionReq)
	}

	promote := authedResourceRequest(http.MethodPost, "/api/resources/skills/skill.review/versions/2.0.0/promote", `{"evaluation_ref":"eval-1","expected_active_version":"1.0.0"}`)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, promote)
	if rec.Code != http.StatusOK || command.promoteSkillID != "skill.review" || command.promoteVersion != "2.0.0" || command.promoteVersionReq.EvaluationRef != "eval-1" {
		t.Fatalf("promote status = %d request=%+v body=%s", rec.Code, command.promoteVersionReq, rec.Body.String())
	}

	rollback := authedResourceRequest(http.MethodPost, "/api/resources/skills/skill.review/versions/1.0.0/rollback", `{"expected_active_version":"2.0.0","reason":"regression"}`)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, rollback)
	if rec.Code != http.StatusOK || command.rollbackSkillID != "skill.review" || command.rollbackVersion != "1.0.0" || command.rollbackVersionReq.Reason != "regression" {
		t.Fatalf("rollback status = %d request=%+v body=%s", rec.Code, command.rollbackVersionReq, rec.Body.String())
	}

	disable := authedResourceRequest(http.MethodPost, "/api/resources/skills/skill.review/disable", `{}`)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, disable)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("disable status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.disabledSkill != "skill.review" {
		t.Fatalf("disabled skill = %q", command.disabledSkill)
	}

	deleteReq := authedResourceRequest(http.MethodDelete, "/api/resources/skills/skill.review", "")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, deleteReq)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("delete status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.deletedSkill != "skill.review" {
		t.Fatalf("deleted skill = %q", command.deletedSkill)
	}

	export := authedResourceRequest(http.MethodGet, "/api/resources/skills/skill.review/export", "")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, export)
	if rec.Code != http.StatusOK {
		t.Fatalf("export status = %d body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/zip" {
		t.Fatalf("export content type = %q", got)
	}
	if rec.Body.String() != "zip-bytes" {
		t.Fatalf("export body = %q", rec.Body.String())
	}

	folder := authedResourceRequest(http.MethodPost, "/api/resources/skills/skill.review/folders", `{"path":"resources/drafts"}`)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, folder)
	if rec.Code != http.StatusCreated {
		t.Fatalf("folder status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.skillFolderRequest.SkillID != "skill.review" || command.skillFolderRequest.Path != "resources/drafts" {
		t.Fatalf("folder request = %+v", command.skillFolderRequest)
	}
}

func TestHTTPRoutesResourceSkillBundleCommandsThroughResourceCommandService(t *testing.T) {
	command := &fakeResourceCommand{skillBundle: resourceadmin.SkillBundleDTO{
		BundleID: "skill-bundle-quality",
		Name:     "Quality pack",
		SkillIDs: []string{"skill.review"},
	}}
	handler := newTestRouter(RouterOptions{ResourceCommand: command})

	create := authedResourceRequest(http.MethodPost, "/api/resources/skill-bundles", `{"name":"Quality pack","description":"Review workflow","skill_ids":["skill.review"]}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, create)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.createSkillBundle.Name != "Quality pack" || len(command.createSkillBundle.SkillIDs) != 1 {
		t.Fatalf("create request = %+v", command.createSkillBundle)
	}

	add := authedResourceRequest(http.MethodPost, "/api/resources/skill-bundles/skill-bundle-quality/skills", `{"skill_ids":["skill.test"]}`)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, add)
	if rec.Code != http.StatusOK {
		t.Fatalf("add status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.bundleID != "skill-bundle-quality" || len(command.addBundleSkills.SkillIDs) != 1 || command.addBundleSkills.SkillIDs[0] != "skill.test" {
		t.Fatalf("add request = bundle:%q body:%+v", command.bundleID, command.addBundleSkills)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("package", "skill.imported.zip")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte("bundle-zip")); err != nil {
		t.Fatalf("write package: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	importReq := authedHTTPTestRequest(http.MethodPost, "/api/resources/skill-bundles/skill-bundle-quality/skills/import", &body)
	importReq.Header.Set("X-User-ID", "agent-1")
	importReq.Header.Set("X-Viewer-Kind", "agent")
	importReq.Header.Set("X-Permission-Scope", "resource.manage")
	importReq.Header.Set("Content-Type", writer.FormDataContentType())
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, importReq)
	if rec.Code != http.StatusOK || command.bundleImportSize != int64(len("bundle-zip")) {
		t.Fatalf("import status = %d size=%d body=%s", rec.Code, command.bundleImportSize, rec.Body.String())
	}

	remove := authedResourceRequest(http.MethodDelete, "/api/resources/skill-bundles/skill-bundle-quality/skills/skill.review", "")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, remove)
	if rec.Code != http.StatusOK || command.removedBundleSkill != "skill.review" {
		t.Fatalf("remove status = %d skill=%q body=%s", rec.Code, command.removedBundleSkill, rec.Body.String())
	}

	deleteReq := authedResourceRequest(http.MethodDelete, "/api/resources/skill-bundles/skill-bundle-quality?delete_skills=true", "")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, deleteReq)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("delete status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !command.deleteSkillBundle.DeleteSkills || command.deletedSkillBundle != "skill-bundle-quality" {
		t.Fatalf("delete request = bundle:%q body:%+v", command.deletedSkillBundle, command.deleteSkillBundle)
	}
}

func TestHTTPRoutesRejectOversizedSkillBundleImport(t *testing.T) {
	command := &fakeResourceCommand{}
	handler := newTestRouter(RouterOptions{ResourceCommand: command})
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("package", "oversized.zip")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte("oversized")); err != nil {
		t.Fatalf("write oversized package: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	request := authedHTTPTestRequest(http.MethodPost, "/api/resources/skill-bundles/skill-bundle-quality/skills/import", &body)
	request.Header.Set("X-User-ID", "agent-1")
	request.Header.Set("X-Viewer-Kind", "agent")
	request.Header.Set("X-Permission-Scope", "resource.manage")
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.ContentLength = maxSkillBundleImportRequestBytes + 1
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized import status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if command.bundleImportSize != 0 {
		t.Fatalf("oversized import reached command service with size %d", command.bundleImportSize)
	}
}

func TestHTTPRoutesResourceDownloadsFormatContentDispositionFilenames(t *testing.T) {
	command := &fakeResourceCommand{
		pkg:               resourceadmin.SkillPackageDTO{FileName: `skill "review";.zip`, ContentType: "application/zip", Bytes: []byte("zip-bytes")},
		skillFileDownload: resourceadmin.SkillFileDownloadDTO{FileName: `notes "draft";.md`, ContentType: "text/markdown", Bytes: []byte("# Notes")},
	}
	handler := newTestRouter(RouterOptions{ResourceCommand: command})

	export := authedResourceRequest(http.MethodGet, "/api/resources/skills/skill.review/export", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, export)
	if rec.Code != http.StatusOK {
		t.Fatalf("export status = %d body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="skill \"review\";.zip"` {
		t.Fatalf("export content disposition = %q", got)
	}

	download := authedResourceRequest(http.MethodGet, "/api/resources/skills/skill.review/files/download?path=notes.md", "")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, download)
	if rec.Code != http.StatusOK {
		t.Fatalf("download status = %d body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="notes \"draft\";.md"` {
		t.Fatalf("download content disposition = %q", got)
	}
}

func TestHTTPRoutesRejectLegacySkillDefinitionCommands(t *testing.T) {
	command := &fakeResourceCommand{}
	handler := newTestRouter(RouterOptions{ResourceCommand: command})

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{
			name:   "json update",
			method: http.MethodPatch,
			path:   "/api/resources/skills/skill.review",
			body:   `{"name":"Review","version":"1.1.0","enabled":true}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, authedResourceRequest(tc.method, tc.path, tc.body))
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
			}
		})
	}

	_ = command
}

func TestHTTPRoutesResourceSkillImportThroughResourceCommandService(t *testing.T) {
	command := &fakeResourceCommand{
		skill: resourceadmin.SkillDTO{SkillID: "skill.imported", Name: "Imported", Version: "1.0.0", Enabled: true},
	}
	handler := newTestRouter(RouterOptions{ResourceCommand: command})

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("package", "skill.imported.skill.zip")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte("zip-bytes")); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	importReq := authedHTTPTestRequest(http.MethodPost, "/api/resources/skills/import", &body)
	importReq.Header.Set("X-User-ID", "agent-1")
	importReq.Header.Set("X-Viewer-Kind", "agent")
	importReq.Header.Set("X-Permission-Scope", "resource.manage")
	importReq.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, importReq)
	if rec.Code != http.StatusCreated {
		t.Fatalf("import status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.importSize != int64(len("zip-bytes")) {
		t.Fatalf("import size = %d", command.importSize)
	}
}

func TestHTTPRoutesResourceBindingCommandsThroughResourceCommandService(t *testing.T) {
	command := &fakeResourceCommand{binding: resourceadmin.ResourceBindingDTO{ResourceID: "tool.deploy", ResourceType: "tool", BindingType: "agent_tool_ref", BoundToID: "profile-ops"}}
	handler := newTestRouter(RouterOptions{ResourceCommand: command})

	bind := authedResourceRequest(http.MethodPost, "/api/resources/bindings", `{"agent_id":"profile-ops","resource_type":"tool","resource_id":"tool.deploy","alias":"deploy-cli"}`)
	bind.Header.Set("X-Request-ID", "corr-bind-route")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, bind)
	if rec.Code != http.StatusCreated {
		t.Fatalf("bind status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.boundResource.AgentID != "profile-ops" || command.boundResource.ResourceID != "tool.deploy" || command.lastViewer.RequestID != "corr-bind-route" {
		t.Fatalf("bound resource = %+v viewer=%+v", command.boundResource, command.lastViewer)
	}

	unbind := authedResourceRequest(http.MethodDelete, "/api/resources/bindings", `{"agent_id":"profile-ops","resource_type":"tool","resource_id":"tool.deploy"}`)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, unbind)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("unbind status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.unboundResource.AgentID != "profile-ops" || command.unboundResource.ResourceID != "tool.deploy" {
		t.Fatalf("unbound resource = %+v", command.unboundResource)
	}
}

func authedResourceRequest(method, path, body string) *http.Request {
	req := authedRequest(method, path, body)
	req.Header.Set("X-Permission-Scope", "resource.manage,resource.read")
	return req
}

type fakeResourceCommand struct {
	mcp                resourceadmin.MCPServerDTO
	tool               resourceadmin.ToolDTO
	skill              resourceadmin.SkillDTO
	pkg                resourceadmin.SkillPackageDTO
	health             resourceadmin.MCPServerHealthDTO
	sync               resourceadmin.MCPServerSyncDTO
	registeredTool     resourceadmin.ToolDefinitionRequest
	updatedTool        resourceadmin.ToolDefinitionRequest
	registerMCP        resourceadmin.RegisterMCPServerRequest
	updateMCP          resourceadmin.RegisterMCPServerRequest
	skillDetail        resourceadmin.SkillDetailDTO
	skillVersion       resourceadmin.SkillVersionDTO
	skillVersionReq    resourceadmin.SkillVersionCandidateRequest
	promoteVersionReq  resourceadmin.PromoteSkillVersionRequest
	rollbackVersionReq resourceadmin.RollbackSkillVersionRequest
	promoteSkillID     string
	promoteVersion     string
	rollbackSkillID    string
	rollbackVersion    string
	skillFile          resourceadmin.SkillFileDTO
	skillFileDownload  resourceadmin.SkillFileDownloadDTO
	createSkill        resourceadmin.CreateSkillRequest
	savedSkillMD       string
	skillFileWrite     resourceadmin.SkillFileWriteRequest
	skillFileUpload    resourceadmin.SkillFileUploadRequest
	skillFileRequest   resourceadmin.SkillFileRequest
	skillFolderRequest resourceadmin.SkillFileRequest
	disabledTool       string
	disabledSkill      string
	deletedTool        string
	deletedMCP         string
	deletedSkill       string
	syncedMCP          string
	healthMCP          string
	importSize         int64
	lastViewer         resourceadmin.ViewerRef
	binding            resourceadmin.ResourceBindingDTO
	boundResource      resourceadmin.BindResourceRequest
	unboundResource    resourceadmin.UnbindResourceRequest
	skillBundle        resourceadmin.SkillBundleDTO
	createSkillBundle  resourceadmin.CreateSkillBundleRequest
	addBundleSkills    resourceadmin.AddSkillsToBundleRequest
	deleteSkillBundle  resourceadmin.DeleteSkillBundleRequest
	bundleID           string
	bundleImportSize   int64
	removedBundleSkill string
	deletedSkillBundle string
}

func (f *fakeResourceCommand) RegisterTool(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.ToolDefinitionRequest) (resourceadmin.ToolDTO, error) {
	f.lastViewer = viewer
	f.registeredTool = req
	return f.tool, nil
}

func (f *fakeResourceCommand) UpdateTool(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.ToolDefinitionRequest) (resourceadmin.ToolDTO, error) {
	f.lastViewer = viewer
	f.updatedTool = req
	return f.tool, nil
}

func (f *fakeResourceCommand) RegisterMCPServer(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.RegisterMCPServerRequest) (resourceadmin.MCPServerDTO, error) {
	f.lastViewer = viewer
	f.registerMCP = req
	return f.mcp, nil
}

func (f *fakeResourceCommand) UpdateMCPServer(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.RegisterMCPServerRequest) (resourceadmin.MCPServerDTO, error) {
	f.lastViewer = viewer
	f.updateMCP = req
	return f.mcp, nil
}

func (f *fakeResourceCommand) SyncMCPServer(ctx context.Context, viewer resourceadmin.ViewerRef, toolID string) (resourceadmin.MCPServerSyncDTO, error) {
	f.lastViewer = viewer
	f.syncedMCP = toolID
	return f.sync, nil
}

func (f *fakeResourceCommand) GetMCPServerHealth(ctx context.Context, viewer resourceadmin.ViewerRef, toolID string) (resourceadmin.MCPServerHealthDTO, error) {
	f.lastViewer = viewer
	f.healthMCP = toolID
	return f.health, nil
}

func (f *fakeResourceCommand) ImportSkillPackage(ctx context.Context, viewer resourceadmin.ViewerRef, reader io.ReaderAt, size int64) (resourceadmin.SkillDTO, error) {
	f.lastViewer = viewer
	f.importSize = size
	return f.skill, nil
}

func (f *fakeResourceCommand) CreateSkill(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.CreateSkillRequest) (resourceadmin.SkillDetailDTO, error) {
	f.lastViewer = viewer
	f.createSkill = req
	return f.skillDetail, nil
}

func (f *fakeResourceCommand) RegisterSkillVersion(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.SkillVersionCandidateRequest) (resourceadmin.SkillVersionDTO, error) {
	f.lastViewer = viewer
	f.skillVersionReq = req
	return f.skillVersion, nil
}

func (f *fakeResourceCommand) PromoteSkillVersion(ctx context.Context, viewer resourceadmin.ViewerRef, skillID, version string, req resourceadmin.PromoteSkillVersionRequest) (resourceadmin.SkillVersionDTO, error) {
	f.lastViewer = viewer
	f.promoteSkillID = skillID
	f.promoteVersion = version
	f.promoteVersionReq = req
	return f.skillVersion, nil
}

func (f *fakeResourceCommand) RollbackSkillVersion(ctx context.Context, viewer resourceadmin.ViewerRef, skillID, version string, req resourceadmin.RollbackSkillVersionRequest) (resourceadmin.SkillVersionDTO, error) {
	f.lastViewer = viewer
	f.rollbackSkillID = skillID
	f.rollbackVersion = version
	f.rollbackVersionReq = req
	return f.skillVersion, nil
}

func (f *fakeResourceCommand) CreateSkillBundle(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.CreateSkillBundleRequest) (resourceadmin.SkillBundleDTO, error) {
	f.lastViewer = viewer
	f.createSkillBundle = req
	return f.skillBundle, nil
}

func (f *fakeResourceCommand) AddSkillsToBundle(ctx context.Context, viewer resourceadmin.ViewerRef, bundleID string, req resourceadmin.AddSkillsToBundleRequest) (resourceadmin.SkillBundleDTO, error) {
	f.lastViewer = viewer
	f.bundleID = bundleID
	f.addBundleSkills = req
	return f.skillBundle, nil
}

func (f *fakeResourceCommand) ImportSkillIntoBundle(ctx context.Context, viewer resourceadmin.ViewerRef, bundleID string, reader io.ReaderAt, size int64) (resourceadmin.SkillBundleDTO, error) {
	f.lastViewer = viewer
	f.bundleID = bundleID
	f.bundleImportSize = size
	return f.skillBundle, nil
}

func (f *fakeResourceCommand) RemoveSkillFromBundle(ctx context.Context, viewer resourceadmin.ViewerRef, bundleID, skillID string) (resourceadmin.SkillBundleDTO, error) {
	f.lastViewer = viewer
	f.bundleID = bundleID
	f.removedBundleSkill = skillID
	return f.skillBundle, nil
}

func (f *fakeResourceCommand) DeleteSkillBundle(ctx context.Context, viewer resourceadmin.ViewerRef, bundleID string, req resourceadmin.DeleteSkillBundleRequest) error {
	f.lastViewer = viewer
	f.deletedSkillBundle = bundleID
	f.deleteSkillBundle = req
	return nil
}

func (f *fakeResourceCommand) SaveSkillMarkdown(ctx context.Context, viewer resourceadmin.ViewerRef, skillID, raw string) (resourceadmin.SkillDetailDTO, error) {
	f.lastViewer = viewer
	f.savedSkillMD = raw
	return f.skillDetail, nil
}

func (f *fakeResourceCommand) GetSkillFile(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.SkillFileRequest) (resourceadmin.SkillFileDTO, error) {
	f.lastViewer = viewer
	f.skillFileRequest = req
	return f.skillFile, nil
}

func (f *fakeResourceCommand) SaveSkillFile(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.SkillFileWriteRequest) (resourceadmin.SkillFileDTO, error) {
	f.lastViewer = viewer
	f.skillFileWrite = req
	return f.skillFile, nil
}

func (f *fakeResourceCommand) UploadSkillFile(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.SkillFileUploadRequest) (resourceadmin.SkillFileDTO, error) {
	f.lastViewer = viewer
	f.skillFileUpload = req
	return f.skillFile, nil
}

func (f *fakeResourceCommand) DownloadSkillFile(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.SkillFileRequest) (resourceadmin.SkillFileDownloadDTO, error) {
	f.lastViewer = viewer
	f.skillFileRequest = req
	return f.skillFileDownload, nil
}

func (f *fakeResourceCommand) DeleteSkillFile(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.SkillFileRequest) error {
	f.lastViewer = viewer
	f.skillFileRequest = req
	return nil
}

func (f *fakeResourceCommand) CreateSkillFolder(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.SkillFileRequest) error {
	f.lastViewer = viewer
	f.skillFolderRequest = req
	return nil
}

func (f *fakeResourceCommand) DisableTool(ctx context.Context, viewer resourceadmin.ViewerRef, toolID string) error {
	f.lastViewer = viewer
	f.disabledTool = toolID
	return nil
}

func (f *fakeResourceCommand) DisableSkill(ctx context.Context, viewer resourceadmin.ViewerRef, skillID string) error {
	f.lastViewer = viewer
	f.disabledSkill = skillID
	return nil
}

func (f *fakeResourceCommand) DeleteMCPServer(ctx context.Context, viewer resourceadmin.ViewerRef, toolID string) error {
	f.lastViewer = viewer
	f.deletedMCP = toolID
	return nil
}

func (f *fakeResourceCommand) DeleteTool(ctx context.Context, viewer resourceadmin.ViewerRef, toolID string) error {
	f.lastViewer = viewer
	f.deletedTool = toolID
	return nil
}

func (f *fakeResourceCommand) DeleteSkill(ctx context.Context, viewer resourceadmin.ViewerRef, skillID string) error {
	f.lastViewer = viewer
	f.deletedSkill = skillID
	return nil
}

func (f *fakeResourceCommand) ExportSkillPackage(ctx context.Context, viewer resourceadmin.ViewerRef, skillID string) (resourceadmin.SkillPackageDTO, error) {
	f.lastViewer = viewer
	return f.pkg, nil
}

func (f *fakeResourceCommand) BindResourceToAgent(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.BindResourceRequest) (resourceadmin.ResourceBindingDTO, error) {
	f.lastViewer = viewer
	f.boundResource = req
	return f.binding, nil
}

func (f *fakeResourceCommand) UnbindResourceFromAgent(ctx context.Context, viewer resourceadmin.ViewerRef, req resourceadmin.UnbindResourceRequest) error {
	f.lastViewer = viewer
	f.unboundResource = req
	return nil
}
