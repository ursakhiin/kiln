package core

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestCoreDoesNoIO enforces that core stays pure: its non-test files must not
// import packages that do I/O.
func TestCoreDoesNoIO(t *testing.T) {
	files, err := filepath.Glob("*.go") // tests run in the package directory
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue // tests may do I/O
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, imp := range parsed.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if isIOImport(path) {
				t.Errorf("%s imports %q; internal/core must not do I/O", f, path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no non-test files checked")
	}
}

func isIOImport(path string) bool {
	for _, p := range []string{"os", "io", "syscall", "net"} {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return strings.HasPrefix(path, "path/")
}
