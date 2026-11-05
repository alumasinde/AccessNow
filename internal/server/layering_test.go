package server

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// moduleRoot is the repo directory: these tests run from internal/server.
var moduleRoot = filepath.Join("..", "..")

// These checks are what keeps "a module can be off and the system still works" true:
// Core serves passes without knowing a visitor module exists.
func TestCoreDoesNotImportModules(t *testing.T) {
	assertNoImport(t, "internal/core", "accessnow/internal/modules",
		"core must not import a module; it has no idea which modules are switched on")
}

func TestPlatformStaysBelowBoth(t *testing.T) {
	assertNoImport(t, "internal/platform", "accessnow/internal/core",
		"platform is the floor of the stack")
	assertNoImport(t, "internal/platform", "accessnow/internal/modules",
		"platform is the floor of the stack")
}

// One module importing another ties their lifecycles together: switching off
// contractor_access must not stop the visitor desk from issuing a pass. Modules talk
// through domain_events, so this edge must never exist in code either.
func TestModulesDoNotImportEachOther(t *testing.T) {
	keys := subdirectories(t, "internal/modules")
	if len(keys) < 2 {
		t.Skipf("only %d module(s) exist yet", len(keys))
	}

	const prefix = "accessnow/internal/modules/"
	for _, key := range keys {
		for file, imports := range importsIn(t, filepath.Join("internal", "modules", key)) {
			for _, importPath := range imports {
				if !strings.HasPrefix(importPath, prefix) {
					continue
				}
				if imported := strings.TrimPrefix(importPath, prefix); imported != key {
					t.Errorf("%s imports module %q", file, imported)
				}
			}
		}
	}
}

func assertNoImport(t *testing.T, dir, deny, why string) {
	t.Helper()
	files := importsIn(t, dir)
	if len(files) == 0 {
		t.Fatalf("no Go files under %s", dir)
	}
	for file, imports := range files {
		for _, importPath := range imports {
			if strings.HasPrefix(importPath, deny) {
				t.Errorf("%s imports %s: %s", file, importPath, why)
			}
		}
	}
}

func importsIn(t *testing.T, relDir string) map[string][]string {
	t.Helper()
	found := map[string][]string{}
	fset := token.NewFileSet()

	err := filepath.WalkDir(filepath.Join(moduleRoot, relDir), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		imports := make([]string, 0, len(parsed.Imports))
		for _, spec := range parsed.Imports {
			imports = append(imports, strings.Trim(spec.Path.Value, `"`))
		}
		found[path] = imports
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", relDir, err)
	}
	return found
}

func subdirectories(t *testing.T, relDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(moduleRoot, relDir))
	if err != nil {
		t.Fatalf("read %s: %v", relDir, err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	return names
}
