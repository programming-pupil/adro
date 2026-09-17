package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/adro-project/adro"

var allowed = map[string]map[string]bool{
	"core":         {"core": true},
	"ports":        {"core": true, "ports": true},
	"runtime":      {"core": true, "ports": true, "runtime": true},
	"controlplane": {"core": true, "ports": true, "runtime": true, "controlplane": true},
	"adapters":     {"core": true, "ports": true, "runtime": true, "controlplane": true, "adapters": true},
	"api":          {"core": true, "ports": true, "runtime": true, "controlplane": true, "api": true},
}

func TestLayerDependencies(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for layer, imports := range allowed {
		layerRoot := filepath.Join(root, layer)
		if _, err := os.Stat(layerRoot); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(layerRoot, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, spec := range parsed.Imports {
				checkImport(t, root, layer, imports, path, spec)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", layer, err)
		}
	}
}

func checkImport(t *testing.T, root, layer string, imports map[string]bool, path string, spec *ast.ImportSpec) {
	pathValue, err := strconv.Unquote(spec.Path.Value)
	if err != nil || !strings.HasPrefix(pathValue, module+"/") {
		return
	}
	relative := strings.TrimPrefix(pathValue, module+"/")
	target := strings.SplitN(relative, "/", 2)[0]
	if imports[target] {
		return
	}
	local, _ := filepath.Rel(root, path)
	t.Errorf("%s layer file %s imports forbidden %s layer", layer, local, target)
}
