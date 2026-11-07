package state

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestNoExportedRawWriters guards the final state-mutation surface: the only
// exported mutators are the transaction primitives (WithHomeState,
// RecoverHomeState, WithRootState, RepairRootState, WithKeyLocks,
// RemoveHomeStateIf). The raw
// writers and lock primitives must stay unexported.
func TestNoExportedRawWriters(t *testing.T) {
	forbidden := map[string]bool{
		"WriteState":              true,
		"WriteRootState":          true,
		"UpdateRootState":         true,
		"UpdateHomeState":         true,
		"UpdateExistingHomeState": true,
		"ReplaceHomeState":        true,
		"RemoveState":             true,
		"LockHomeState":           true,
		"LockRootState":           true,
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fileSet := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fileSet, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Name == nil {
				continue
			}
			if forbidden[fn.Name.Name] {
				t.Errorf("state.%s must not be exported", fn.Name.Name)
			}
		}
	}
}
