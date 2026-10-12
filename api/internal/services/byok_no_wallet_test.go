package services

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"testing"
)

// Spec § 7.6 / § 17 item 1: no file of the BYOK rail imports the wallet client.
func TestNoByokFileImportsTheWallet(t *testing.T) {
	files, err := filepath.Glob("byok_*.go")
	if err != nil || len(files) == 0 {
		t.Fatal("no byok files", err)
	}
	files = append(files, "charge_gates.go")
	for _, f := range files {
		ast, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range ast.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == "gopkg.aoctech.app/billing/api/internal/wallet" {
				t.Errorf("%s imports the wallet client", f)
			}
		}
	}
}
