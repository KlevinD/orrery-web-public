package execution_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecutionProductionPackageDoesNotExposeMemoryBackends(t *testing.T) {
	root := "."
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.TypeSpec:
				if strings.Contains(n.Name.Name, "Memory") || strings.Contains(n.Name.Name, "Failing") {
					t.Fatalf("%s exposes production-only test backend %s; move it to platform/testkit or name a real ephemeral fallback explicitly", path, n.Name.Name)
				}
			case *ast.FuncDecl:
				if strings.Contains(n.Name.Name, "Memory") || strings.Contains(n.Name.Name, "Failing") {
					t.Fatalf("%s exposes production-only test backend constructor %s; move it to platform/testkit or name a real ephemeral fallback explicitly", path, n.Name.Name)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("inspect execution package: %v", err)
	}
}
