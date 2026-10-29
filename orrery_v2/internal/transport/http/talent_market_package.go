package httptransport

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"
	"orrery_v2/internal/app/operations"
)

const talentMarketZipFormat = "orrery.talent-market"

func readTalentMarketImportPreviewRequest(req *http.Request) (operations.TalentMarketImportPreviewRequest, error) {
	contentType := req.Header.Get("Content-Type")
	if !strings.HasPrefix(strings.ToLower(contentType), "multipart/form-data") {
		var body operations.TalentMarketImportPreviewRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			return operations.TalentMarketImportPreviewRequest{}, fmt.Errorf("%w: request body is invalid", operations.ErrInvalidRequest)
		}
		return body, nil
	}
	if err := req.ParseMultipartForm(64 << 20); err != nil {
		return operations.TalentMarketImportPreviewRequest{}, fmt.Errorf("%w: multipart zip upload is invalid", operations.ErrInvalidRequest)
	}
	file, _, err := req.FormFile("file")
	if err != nil {
		return operations.TalentMarketImportPreviewRequest{}, fmt.Errorf("%w: file is required", operations.ErrInvalidRequest)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return operations.TalentMarketImportPreviewRequest{}, err
	}
	parsed, err := parseTalentMarketZip(data)
	if err != nil {
		return operations.TalentMarketImportPreviewRequest{}, err
	}
	if len(parsed.Candidates) == 0 {
		return operations.TalentMarketImportPreviewRequest{}, fmt.Errorf("%w: zip does not contain importable agents", operations.ErrInvalidRequest)
	}
	return parsed, nil
}

func parseTalentMarketZip(data []byte) (operations.TalentMarketImportPreviewRequest, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return operations.TalentMarketImportPreviewRequest{}, fmt.Errorf("%w: zip file is invalid", operations.ErrInvalidRequest)
	}
	if exported, err := parseOrreryTalentMarketZip(reader); err != nil {
		return operations.TalentMarketImportPreviewRequest{}, err
	} else if len(exported.Candidates) > 0 {
		return exported, nil
	}

	var candidates []operations.TalentMarketImportCandidateDTO
	for _, file := range reader.File {
		if file.FileInfo().IsDir() || !strings.HasSuffix(strings.ToLower(file.Name), ".md") {
			continue
		}
		sourcePath := normalizeTalentMarketZipPath(file.Name)
		if shouldIgnoreTalentMarketMarkdown(sourcePath) {
			continue
		}
		content, err := readZipFile(file)
		if err != nil {
			return operations.TalentMarketImportPreviewRequest{}, err
		}
		candidate, ok := parseAgencyAgentMarkdown(string(content), sourcePath)
		if ok {
			candidates = append(candidates, candidate)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return strings.Compare(candidates[i].SourcePath, candidates[j].SourcePath) < 0
	})
	return operations.TalentMarketImportPreviewRequest{Candidates: candidates}, nil
}

func parseOrreryTalentMarketZip(reader *zip.Reader) (operations.TalentMarketImportPreviewRequest, error) {
	manifestFile := findZipFile(reader, "manifest.json")
	if manifestFile == nil {
		return operations.TalentMarketImportPreviewRequest{}, nil
	}
	manifestRaw, err := readZipFile(manifestFile)
	if err != nil {
		return operations.TalentMarketImportPreviewRequest{}, err
	}
	var manifest operations.TalentMarketExportManifestDTO
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil || manifest.Format != talentMarketZipFormat {
		return operations.TalentMarketImportPreviewRequest{}, nil
	}
	var candidates []operations.TalentMarketImportCandidateDTO
	var skills []operations.TalentMarketSkillDefinitionDTO
	files := append([]*zip.File(nil), reader.File...)
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	for _, file := range files {
		name := normalizeTalentMarketZipPath(file.Name)
		if file.FileInfo().IsDir() || !strings.HasSuffix(strings.ToLower(name), ".json") {
			continue
		}
		raw, err := readZipFile(file)
		if err != nil {
			return operations.TalentMarketImportPreviewRequest{}, err
		}
		if strings.HasPrefix(name, "agents/") {
			var candidate operations.TalentMarketImportCandidateDTO
			if err := json.Unmarshal(raw, &candidate); err != nil {
				return operations.TalentMarketImportPreviewRequest{}, fmt.Errorf("%w: agent json is invalid", operations.ErrInvalidRequest)
			}
			if strings.TrimSpace(candidate.RealName) != "" {
				candidates = append(candidates, candidate)
			}
			continue
		}
		if strings.HasPrefix(name, "skills/") {
			var definition operations.TalentMarketSkillDefinitionDTO
			if err := json.Unmarshal(raw, &definition); err != nil {
				return operations.TalentMarketImportPreviewRequest{}, fmt.Errorf("%w: skill json is invalid", operations.ErrInvalidRequest)
			}
			if strings.TrimSpace(definition.SkillID) != "" {
				skills = append(skills, definition)
			}
		}
	}
	return operations.TalentMarketImportPreviewRequest{Candidates: candidates, Skills: skills}, nil
}

func writeTalentMarketExportZip(w http.ResponseWriter, pkg operations.TalentMarketExportPackageDTO) error {
	var body bytes.Buffer
	writer := zip.NewWriter(&body)
	manifest, err := json.MarshalIndent(pkg.Manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := writeZipEntry(writer, "manifest.json", manifest); err != nil {
		return err
	}
	for _, agent := range pkg.Agents {
		payload, err := json.MarshalIndent(agent, "", "  ")
		if err != nil {
			return err
		}
		stem := safeTalentMarketFileStem(firstTalentMarketNonEmpty(agent.AgentProfileID, agent.SourceSlug, agent.RealName))
		if err := writeZipEntry(writer, "agents/"+stem+".json", payload); err != nil {
			return err
		}
	}
	for _, skill := range pkg.Skills {
		payload, err := json.MarshalIndent(skill, "", "  ")
		if err != nil {
			return err
		}
		stem := safeTalentMarketFileStem(skill.SkillID)
		if err := writeZipEntry(writer, "skills/"+stem+".json", payload); err != nil {
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return err
	}
	filename := "orrery-talent-market-" + time.Now().UTC().Format("2006-01-02") + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(body.Bytes())
	return err
}

func parseAgencyAgentMarkdown(content string, sourcePath string) (operations.TalentMarketImportCandidateDTO, bool) {
	frontmatter, body := splitMarkdownFrontmatter(content)
	name := strings.TrimSpace(frontmatter["name"])
	description := strings.TrimSpace(frontmatter["description"])
	if name == "" || description == "" {
		return operations.TalentMarketImportCandidateDTO{}, false
	}
	slug := safeTalentMarketFileStem(strings.TrimSuffix(path.Base(sourcePath), path.Ext(sourcePath)))
	category := ""
	if parts := strings.Split(sourcePath, "/"); len(parts) > 1 {
		category = parts[0]
	}
	return operations.TalentMarketImportCandidateDTO{
		AgentProfileID: "profile-market-" + slug,
		RealName:       name,
		DisplayName:    name,
		RoleName:       description,
		AgentKind:      "ai",
		Description:    description,
		Category:       category,
		SourceSlug:     slug,
		SourcePath:     sourcePath,
		SourceHash:     hashTalentMarketSource(content),
		SourcePrompt:   strings.TrimSpace(content),
		RuntimeKind:    "managed",
		Persona: operations.WorkforcePersonaDTO{
			Backstory:              firstTalentMarketNonEmpty(strings.TrimSpace(body), description),
			ResponsibilityBoundary: "负责" + description + "，并按 BaseAgent 审计要求提交过程、结果和证据。",
			BehaviorBoundary:       extractTalentMarketBehaviorBoundary(body),
		},
		SkillRefs: []operations.WorkforceSkillRefDTO{{
			SkillID: "imported." + slug,
			Alias:   name,
			Level:   "reference",
		}},
		ToolRefs:  []operations.WorkforceToolRefDTO{},
		QAEnabled: true,
		Enabled:   true,
	}, true
}

func splitMarkdownFrontmatter(content string) (map[string]string, string) {
	trimmed := strings.TrimPrefix(content, "\ufeff")
	if !strings.HasPrefix(trimmed, "---") {
		return map[string]string{}, trimmed
	}
	end := strings.Index(trimmed[3:], "\n---")
	if end < 0 {
		return map[string]string{}, trimmed
	}
	raw := strings.TrimSpace(trimmed[3 : 3+end])
	body := strings.TrimSpace(trimmed[3+end+4:])
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(raw), &parsed); err != nil {
		return map[string]string{}, body
	}
	out := map[string]string{}
	for key, value := range parsed {
		out[key] = strings.TrimSpace(fmt.Sprint(value))
	}
	return out, body
}

func extractTalentMarketBehaviorBoundary(body string) string {
	section := findTalentMarketMarkdownSection(body, []string{"规则", "原则", "Rules", "Guidelines"})
	if len(section) > 800 {
		section = section[:800]
	}
	return strings.TrimSpace(section)
}

func findTalentMarketMarkdownSection(body string, names []string) string {
	var collected []string
	collecting := false
	for _, line := range strings.Split(body, "\n") {
		header := regexp.MustCompile(`^#{2,4}\s+(.+)$`).FindStringSubmatch(strings.TrimSpace(line))
		if header != nil {
			if collecting {
				break
			}
			for _, name := range names {
				if strings.Contains(strings.ToLower(header[1]), strings.ToLower(name)) {
					collecting = true
					break
				}
			}
			continue
		}
		if collecting {
			collected = append(collected, line)
		}
	}
	return strings.Join(collected, "\n")
}

func normalizeTalentMarketZipPath(value string) string {
	normalized := path.Clean(strings.ReplaceAll(value, "\\", "/"))
	normalized = strings.TrimPrefix(normalized, "/")
	parts := strings.Split(normalized, "/")
	if len(parts) > 1 && strings.Contains(strings.ToLower(parts[0]), "agency-agents-zh") {
		return strings.Join(parts[1:], "/")
	}
	return normalized
}

func shouldIgnoreTalentMarketMarkdown(sourcePath string) bool {
	base := strings.ToLower(path.Base(sourcePath))
	switch base {
	case "readme.md", "catalog.md", "agent-list.md", "upstream.md", "contributing.md", "license.md":
		return true
	}
	lower := strings.ToLower(sourcePath)
	return strings.Contains(lower, "/examples/") || strings.Contains(lower, "/strategy/") || strings.Contains(lower, "/integrations/")
}

func readZipFile(file *zip.File) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func findZipFile(reader *zip.Reader, name string) *zip.File {
	for _, file := range reader.File {
		if normalizeTalentMarketZipPath(file.Name) == name {
			return file
		}
	}
	return nil
}

func writeZipEntry(writer *zip.Writer, name string, data []byte) error {
	entry, err := writer.Create(name)
	if err != nil {
		return err
	}
	_, err = entry.Write(data)
	return err
}

func safeTalentMarketFileStem(value string) string {
	var builder strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		allowed := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || unicode.Is(unicode.Han, r)
		if allowed {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	stem := strings.Trim(builder.String(), "-")
	if stem == "" {
		return "agent"
	}
	return stem
}

func hashTalentMarketSource(value string) string {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(value))
	return fmt.Sprintf("fnv1a:%08x", hash.Sum32())
}

func firstTalentMarketNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
