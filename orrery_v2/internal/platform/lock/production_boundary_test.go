package lock_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLockProductionPackageDoesNotExposeMemoryBackend(t *testing.T) {
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
				if n.Name.Name == "Memory" {
					t.Fatalf("%s exposes production memory lock backend; move it to platform/testkit", path)
				}
			case *ast.FuncDecl:
				if strings.Contains(n.Name.Name, "Memory") {
					t.Fatalf("%s exposes production memory lock constructor/function %s; move it to platform/testkit", path, n.Name.Name)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("inspect lock package: %v", err)
	}
}
