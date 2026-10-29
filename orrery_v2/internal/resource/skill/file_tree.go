package skill

import (
	"context"
	"mime"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

type PreviewKind string

const (
	PreviewKindMarkdown PreviewKind = "markdown"
	PreviewKindText     PreviewKind = "text"
	PreviewKindImage    PreviewKind = "image"
	PreviewKindJSON     PreviewKind = "json"
	PreviewKindNone     PreviewKind = "none"
)

type ResourceCounts struct {
	Assets     int `json:"assets"`
	Resources  int `json:"resources"`
	References int `json:"references"`
	Scripts    int `json:"scripts"`
	Other      int `json:"other"`
	Total      int `json:"total"`
}

type FileTree struct {
	Roots  []FileRoot     `json:"roots"`
	Counts ResourceCounts `json:"counts"`
}

type FileRoot struct {
	Name     string     `json:"name"`
	Label    string     `json:"label"`
	Exists   bool       `json:"exists"`
	Children []FileNode `json:"children"`
}

type FileNode struct {
	Path        string      `json:"path"`
	Name        string      `json:"name"`
	Type        string      `json:"type"`
	Size        int64       `json:"size"`
	MediaType   string      `json:"media_type"`
	PreviewKind PreviewKind `json:"preview_kind"`
	DownloadURL string      `json:"download_url,omitempty"`
	Children    []FileNode  `json:"children,omitempty"`
}

func (s *PackageStore) BuildFileTree(ctx context.Context, id agentcore.SkillID) (FileTree, error) {
	if err := ctx.Err(); err != nil {
		return FileTree{}, err
	}
	dir, err := s.SkillDirectory(id)
	if err != nil {
		return FileTree{}, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return FileTree{}, ErrNotFound
		}
		return FileTree{}, err
	}
	if !info.IsDir() {
		return FileTree{}, ErrHydration
	}
	rootsByName := map[string]*FileRoot{}
	order := []string{"assets", "resources", "references", "scripts", "other"}
	for _, name := range order {
		rootsByName[name] = &FileRoot{Name: name, Label: rootLabel(name), Children: []FileNode{}}
	}
	err = filepath.WalkDir(dir, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if filePath == dir {
			return nil
		}
		relPath, err := filepath.Rel(dir, filePath)
		if err != nil {
			return err
		}
		relPath = filepath.ToSlash(relPath)
		if relPath == "SKILL.md" {
			return nil
		}
		rootName := fileRootName(relPath)
		root := rootsByName[rootName]
		root.Exists = true
		if entry.IsDir() {
			addDirectoryNode(root, relPath, rootName)
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		node := FileNode{
			Path:        relPath,
			Name:        path.Base(relPath),
			Type:        "file",
			Size:        info.Size(),
			MediaType:   mediaTypeForPath(relPath),
			PreviewKind: previewKindForPath(relPath, info.Size(), s.limits.MaxPreviewBytes),
			DownloadURL: "/api/resources/skills/" + url.PathEscape(string(id)) + "/files/download?path=" + url.QueryEscape(relPath),
		}
		addFileNode(root, relPath, rootName, node)
		return nil
	})
	if err != nil {
		return FileTree{}, err
	}
	var tree FileTree
	for _, name := range order {
		root := rootsByName[name]
		sortFileNodes(root.Children)
		tree.Roots = append(tree.Roots, *root)
	}
	tree.Counts = countRoots(tree.Roots)
	return tree, nil
}

func addFileNode(root *FileRoot, relPath, rootName string, fileNode FileNode) {
	parts := strings.Split(relPath, "/")
	if rootName != "other" && len(parts) > 0 && parts[0] == rootName {
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return
	}
	root.Children = addFileNodeParts(root.Children, parts, fileNode, rootName, "")
}

func addDirectoryNode(root *FileRoot, relPath, rootName string) {
	parts := strings.Split(relPath, "/")
	if rootName != "other" && len(parts) > 0 && parts[0] == rootName {
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return
	}
	root.Children = addDirectoryNodeParts(root.Children, parts, rootName, "")
}

func addDirectoryNodeParts(nodes []FileNode, parts []string, rootName, prefix string) []FileNode {
	name := parts[0]
	dirPath := path.Join(prefix, name)
	if rootName != "other" {
		dirPath = path.Join(rootName, dirPath)
	}
	for i := range nodes {
		if nodes[i].Type == "directory" && nodes[i].Name == name {
			if len(parts) > 1 {
				nodes[i].Children = addDirectoryNodeParts(nodes[i].Children, parts[1:], rootName, path.Join(prefix, name))
			}
			return nodes
		}
	}
	node := FileNode{
		Path:        dirPath,
		Name:        name,
		Type:        "directory",
		PreviewKind: PreviewKindNone,
		Children:    []FileNode{},
	}
	if len(parts) > 1 {
		node.Children = addDirectoryNodeParts(nil, parts[1:], rootName, path.Join(prefix, name))
	}
	return append(nodes, node)
}

func addFileNodeParts(nodes []FileNode, parts []string, fileNode FileNode, rootName, prefix string) []FileNode {
	if len(parts) == 1 {
		nodes = append(nodes, fileNode)
		return nodes
	}
	name := parts[0]
	dirPath := path.Join(prefix, name)
	if rootName != "other" {
		dirPath = path.Join(rootName, dirPath)
	}
	for i := range nodes {
		if nodes[i].Type == "directory" && nodes[i].Name == name {
			nodes[i].Children = addFileNodeParts(nodes[i].Children, parts[1:], fileNode, rootName, path.Join(prefix, name))
			return nodes
		}
	}
	nodes = append(nodes, FileNode{
		Path:        dirPath,
		Name:        name,
		Type:        "directory",
		PreviewKind: PreviewKindNone,
		Children:    addFileNodeParts(nil, parts[1:], fileNode, rootName, path.Join(prefix, name)),
	})
	return nodes
}

func sortFileNodes(nodes []FileNode) {
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Type != nodes[j].Type {
			return nodes[i].Type == "directory"
		}
		return strings.ToLower(nodes[i].Name) < strings.ToLower(nodes[j].Name)
	})
	for i := range nodes {
		sortFileNodes(nodes[i].Children)
	}
}

func countRoots(roots []FileRoot) ResourceCounts {
	var counts ResourceCounts
	for _, root := range roots {
		count := countFiles(root.Children)
		switch root.Name {
		case "assets":
			counts.Assets = count
		case "resources":
			counts.Resources = count
		case "references":
			counts.References = count
		case "scripts":
			counts.Scripts = count
		case "other":
			counts.Other = count
		}
		counts.Total += count
	}
	return counts
}

func countFiles(nodes []FileNode) int {
	total := 0
	for _, node := range nodes {
		if node.Type == "file" {
			total++
		}
		total += countFiles(node.Children)
	}
	return total
}

func rootLabel(name string) string {
	switch name {
	case "assets":
		return "Assets"
	case "resources":
		return "Resources"
	case "references":
		return "References"
	case "scripts":
		return "Scripts"
	default:
		return "Other"
	}
}

func fileRootName(relPath string) string {
	first := strings.Split(relPath, "/")[0]
	switch first {
	case "assets", "resources", "references", "scripts":
		return first
	default:
		return "other"
	}
}

func previewKindForPath(relPath string, size, maxPreviewBytes int64) PreviewKind {
	ext := strings.ToLower(path.Ext(relPath))
	if isImageExtension(ext) {
		return PreviewKindImage
	}
	if size > maxPreviewBytes {
		return PreviewKindNone
	}
	switch ext {
	case ".md", ".markdown":
		return PreviewKindMarkdown
	case ".json":
		return PreviewKindJSON
	}
	if isTextExtension(ext) {
		return PreviewKindText
	}
	return PreviewKindNone
}

func mediaTypeForPath(relPath string) string {
	if mediaType := mime.TypeByExtension(strings.ToLower(path.Ext(relPath))); mediaType != "" {
		return mediaType
	}
	switch previewKindForPath(relPath, 0, DefaultMaxPreviewBytes) {
	case PreviewKindMarkdown, PreviewKindText:
		return "text/plain"
	case PreviewKindJSON:
		return "application/json"
	default:
		return "application/octet-stream"
	}
}

func isImageExtension(ext string) bool {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg":
		return true
	default:
		return false
	}
}

func isTextExtension(ext string) bool {
	switch ext {
	case ".txt", ".text", ".yaml", ".yml", ".toml", ".csv", ".log", ".go", ".ts", ".tsx", ".js", ".jsx", ".svelte", ".css", ".html", ".htm", ".xml", ".ps1", ".sh", ".py", ".java", ".cs", ".rs", ".sql":
		return true
	default:
		return false
	}
}
