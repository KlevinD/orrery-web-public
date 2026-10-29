package resourceadmin

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	toolres "orrery_v2/internal/resource/tool"
)

type fakeResourceAuditRecorder struct {
	events []AuditEvent
}

func (f *fakeResourceAuditRecorder) RecordAuditEvent(ctx context.Context, event AuditEvent) error {
	f.events = append(f.events, event)
	return nil
}

type fakeAgentProfileBindingStore struct {
	profiles map[agentcore.AgentProfileID]agentcore.AgentProfile
	page     agentcore.AgentDirectoryPage
	query    agentcore.AgentDirectoryQuery
	saveErr  error
}

func (f *fakeAgentProfileBindingStore) GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	profile, ok := f.profiles[id]
	if !ok {
		return agentcore.AgentProfile{}, errors.New("profile not found")
	}
	return profile, nil
}

func (f *fakeAgentProfileBindingStore) SaveProfile(ctx context.Context, profile agentcore.AgentProfile) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.profiles[profile.ID] = profile
	return nil
}

func (f *fakeAgentProfileBindingStore) QueryDirectory(ctx context.Context, q agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	f.query = q
	return f.page, nil
}

type fakeMCPRuntime struct {
	tools         []string
	toolDetails   []toolres.MCPExposedToolDefinition
	latency       time.Duration
	err           error
	healthStatus  string
	healthMessage string
	healthFailure string
	httpStatus    int
	healthToolID  string
	syncToolID    string
	healthCalls   []toolres.ToolDefinition
}

func (f *fakeMCPRuntime) CheckHealth(ctx context.Context, definition toolres.ToolDefinition) (MCPRuntimeHealth, error) {
	f.healthToolID = string(definition.ID)
	f.healthCalls = append(f.healthCalls, definition.Clone())
	if f.err != nil {
		status := f.healthStatus
		if status == "" {
			status = "unavailable"
		}
		failure := f.healthFailure
		if failure == "" {
			failure = f.err.Error()
		}
		return MCPRuntimeHealth{Status: status, Message: f.healthMessage, FailureReason: failure, HTTPStatus: f.httpStatus, Latency: f.latency}, f.err
	}
	return MCPRuntimeHealth{Status: "available", Message: f.healthMessage, HTTPStatus: f.httpStatus, Latency: f.latency}, nil
}

func (f *fakeMCPRuntime) ListTools(ctx context.Context, definition toolres.ToolDefinition) (MCPRuntimeToolList, error) {
	f.syncToolID = string(definition.ID)
	if f.err != nil {
		return MCPRuntimeToolList{Latency: f.latency}, f.err
	}
	return MCPRuntimeToolList{Tools: append([]string(nil), f.tools...), ToolDetails: append([]toolres.MCPExposedToolDefinition(nil), f.toolDetails...), Latency: f.latency}, nil
}

func buildSkillPackage(t *testing.T, skillID, name, markdown string) []byte {
	return buildSkillPackageWithEntries(t, skillID, name, markdown, map[string]string{
		"assets/logo.png":     "png",
		"resources/notes.txt": "notes",
	})
}

func buildSkillPackageWithEntries(t *testing.T, skillID, name, markdown string, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	skillName := strings.TrimPrefix(skillID, "skill.")
	skillMD := "---\nname: " + skillName + "\ndescription: " + name + "\nversion: 1.0.0\n---\n" + markdown
	if err := writeZipEntry(zw, "SKILL.md", []byte(skillMD)); err != nil {
		t.Fatalf("write skill markdown: %v", err)
	}
	for path, content := range entries {
		if err := writeZipEntry(zw, path, []byte(content)); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func buildSkillPackageWithName(t *testing.T, folderName, skillName, description string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	skillMD := "---\nname: " + skillName + "\ndescription: " + description + "\n---\n# " + skillName + "\n"
	if err := writeZipEntry(zw, folderName+"/SKILL.md", []byte(skillMD)); err != nil {
		t.Fatalf("write skill markdown: %v", err)
	}
	if err := writeZipEntry(zw, folderName+"/assets/usage_guide.md", []byte("# Usage\n")); err != nil {
		t.Fatalf("write asset: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func writeZipEntry(zw *zip.Writer, name string, content []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write(content)
	return err
}

func boolPtr(value bool) *bool {
	return &value
}

type sequenceIDGenerator struct {
	values []string
	index  int
}

func (g *sequenceIDGenerator) NewID(prefix string) (string, error) {
	if g.index < len(g.values) {
		value := g.values[g.index]
		g.index++
		return value, nil
	}
	return prefix + "-fallback", nil
}

func assertOpaqueResourceID(t *testing.T, label, prefix, value string) {
	t.Helper()
	expectedPrefix := prefix + "-"
	if !strings.HasPrefix(value, expectedPrefix) {
		t.Fatalf("%s = %q, want prefix %q", label, value, expectedPrefix)
	}
	suffix := strings.TrimPrefix(value, expectedPrefix)
	if len(suffix) != 32 {
		t.Fatalf("%s = %q, suffix should be 128-bit hex", label, value)
	}
	for _, char := range suffix {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			t.Fatalf("%s = %q, suffix should be random hex", label, value)
		}
	}
}
