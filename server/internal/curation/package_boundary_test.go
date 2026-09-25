package curation

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCurationImplementationPackagesDoNotLeakToOtherProducts(t *testing.T) {
	serverRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	prefixes := []string{
		"github.com/vitlane/vitlane/server/internal/curation/planning/",
		"github.com/vitlane/vitlane/server/internal/curation/research/",
		"github.com/vitlane/vitlane/server/internal/curation/intelligence/",
	}
	err = filepath.WalkDir(filepath.Join(serverRoot, "internal"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		relative, err := filepath.Rel(serverRoot, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if strings.HasPrefix(relative, "internal/curation/") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			for _, prefix := range prefixes {
				if strings.HasPrefix(importPath, prefix) {
					t.Errorf("%s imports hidden Curation implementation %s", relative, importPath)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRetiredTopLevelProductPackagesStayAbsent(t *testing.T) {
	serverRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"planning", "research", "intelligence", "managedrunner",
		"shoppingsession", "purchase", "fulfillment",
	} {
		path := filepath.Join(serverRoot, "internal", name)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("retired top-level package still exists: %s", path)
		}
	}
}
